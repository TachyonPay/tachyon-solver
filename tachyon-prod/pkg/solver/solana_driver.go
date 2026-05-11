// Solana driver — wires per-Solana-chain event listeners + handlers into
// the existing Solver state machine without touching the EVM hot path.
//
// Lifecycle:
//
//   solver.Start() (existing)
//     └── for each chain in solanaClients: start a driver goroutine
//          ├── SubscribeIntentEvents(programID) via WebSocket
//          ├── on IntentCreated: register intent, schedule bid worker
//          ├── on IntentWon (we won): deposit_and_pickup
//          ├── on IntentSolvedOnChain2 / IntentSettled: mark complete
//
// EVM->Solana destination delivery (the case where source=EVM and the
// solver needs to call solve_intent_on_chain2 on Solana) is triggered
// from the EVM listener — the EVM solver path detects the
// IntentCreatedToSolana log, looks up the destination Solana client by
// dest_chain_id, and calls SolanaClient.SolveIntentOnChain2 directly.
// That trigger lives on the EVM side (separate change in solver.go's
// chain2 dispatch); here we only handle Solana-as-source flows.

package solver

import (
	"bytes"
	"context"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gagliardetto/solana-go"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/rebalancer"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// startSolanaDrivers spawns one goroutine per configured Solana chain to
// subscribe to bridge_intent program logs and react to lifecycle events.
// Returns immediately; goroutines run until ctx is cancelled.
//
// Called once from Solver.Start after the EVM listeners are launched.
func (s *Solver) startSolanaDrivers(ctx context.Context) {
	for chainID, sc := range s.solanaClients {
		s.bidWorkerWg.Add(1)
		go s.runSolanaDriver(ctx, chainID, sc)
	}
}

// runSolanaDriver is the per-chain driver goroutine. Subscribes once, then
// reconnects with exponential backoff on disconnect (up to 60s).
func (s *Solver) runSolanaDriver(ctx context.Context, chainID string, sc *blockchain.SolanaClient) {
	defer s.bidWorkerWg.Done()

	logger := log.WithFields(log.Fields{
		"chain":     sc.NetworkName(),
		"chainID":   chainID,
		"programID": sc.ProgramID().String(),
	})
	logger.Info("Solana driver: starting")

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			logger.Info("Solana driver: context cancelled, exiting")
			return
		default:
		}

		// Subscribe — `cancel` returns when the WS or context closes.
		var wg sync.WaitGroup
		wg.Add(1)
		cancel, err := sc.SubscribeIntentEvents(ctx, func(evt blockchain.IntentEvent) {
			s.dispatchSolanaEvent(ctx, chainID, sc, evt)
		})
		if err != nil {
			logger.WithError(err).Warn("Solana driver: subscribe failed, retrying")
			wg.Done()
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		logger.Info("Solana driver: subscribed to bridge_intent program logs")

		// Hold the goroutine until ctx is done; the subscription's own
		// goroutine will run handlers on each event. When ctx fires, we
		// tear the subscription down.
		go func() {
			defer wg.Done()
			<-ctx.Done()
			cancel()
		}()
		wg.Wait()
	}
}

// dispatchSolanaEvent routes one parsed Anchor event to the appropriate
// solver-state handler. Mirrors what the EVM polling loop does for log
// events — bid scheduling, win detection, completion bookkeeping.
func (s *Solver) dispatchSolanaEvent(ctx context.Context, chainID string, sc *blockchain.SolanaClient, evt blockchain.IntentEvent) {
	logger := log.WithFields(log.Fields{
		"chain":   sc.NetworkName(),
		"chainID": chainID,
		"intent":  evt.Snapshot.IntentID.String(),
	})

	switch evt.Kind {
	case blockchain.EventIntentCreated:
		s.handleSolanaIntentCreated(ctx, chainID, sc, evt)
	case blockchain.EventBidPlaced:
		// Informational — we don't need to act on others' bids beyond
		// metric-style logging. The EVM path also no-ops here.
		logger.WithField("bidder", solanaB58(evt.Snapshot.WinningSolver)).Debug("Solana: BidPlaced")
	case blockchain.EventIntentWon:
		s.handleSolanaIntentWon(ctx, chainID, sc, evt)
	case blockchain.EventSolverDeposited:
		logger.Info("Solana: SolverDeposited acknowledged on-chain")
	case blockchain.EventIntentSolvedOnChain2, blockchain.EventIntentSettled, blockchain.EventIntentCompleted:
		s.markSolanaIntentComplete(chainID, evt)
	case blockchain.EventIntentCancelled:
		s.markSolanaIntentComplete(chainID, evt)
	}
}

// handleSolanaIntentCreated decides whether to bid on a freshly-emitted
// Solana intent. The EVM `calculateProfitableBid` is shaped around
// `*types.Intent` from EVM and would NPE on Solana payloads, so the
// Solana driver runs its own (smaller) profitability check inline.
//
// Profitability formula mirrors EVM: revenue = amountA + reward priced
// in USD via the oracle; payout = expectedAmountB priced via the oracle;
// solver accepts iff (revenue - payout) / payout >= MinProfitMargin.
// Solana gas is skipped per the locked design.
func (s *Solver) handleSolanaIntentCreated(ctx context.Context, chainID string, sc *blockchain.SolanaClient, evt blockchain.IntentEvent) {
	logger := log.WithFields(log.Fields{
		"chain":   sc.NetworkName(),
		"chainID": chainID,
		"intent":  evt.Snapshot.IntentID.String(),
	})

	// Skip if we already track this intent (replay protection — the WS
	// can deliver the same log twice across reconnects).
	key := chainID + "-" + evt.Snapshot.IntentID.String()
	s.mu.Lock()
	if _, exists := s.activeIntents[key]; exists {
		s.mu.Unlock()
		return
	}
	s.activeIntents[key] = &types.IntentStatus{
		IntentID:    evt.Snapshot.IntentID.String(),
		Network:     chainID,
		Status:      "evaluating",
		LastUpdated: time.Now(),
	}
	s.mu.Unlock()

	logger.WithFields(log.Fields{
		"amountA":     evt.Snapshot.AmountA.String(),
		"reward":      evt.Snapshot.Reward.String(),
		"destChainID": evt.Snapshot.DestChainID.String(),
		"endTime":     evt.Snapshot.EndTime,
	}).Info("Solana: IntentCreated detected")

	// Compute target bid + decide whether to bid. Bypass the EVM
	// bidWorker entirely — its bidJob is shaped around *blockchain.Client
	// (EVM) and would NPE on Solana payloads.
	targetBid, ok := s.calculateProfitableBidSolana(chainID, sc, evt.Snapshot)
	if !ok {
		logger.Info("Solana: intent not profitable, skipping")
		return
	}

	go func() {
		bctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		sig, err := sc.PlaceBid(bctx, evt.Snapshot.IntentID, targetBid)
		if err != nil {
			logger.WithError(err).Error("Solana: PlaceBid failed")
			return
		}
		logger.WithFields(log.Fields{
			"sig":       sig,
			"bidAmount": targetBid.String(),
		}).Info("Solana: bid placed")
	}()

	// Schedule auction finalize after endTime + small buffer. The Anchor
	// program's `finalize_auction` is permissionless; we drive it here so
	// we don't rely on the user or another solver. It reverts cleanly if
	// already finalized (handled by swallowing errors below — see logger
	// at Debug level).
	go s.scheduleSolanaFinalize(ctx, sc, evt.Snapshot.IntentID, evt.Snapshot.EndTime, logger)
}

// dispatchPostSolanaDeposit handles destination-chain delivery for a
// Solana-source intent we just deposited on. Reads the IntentRecord to
// learn the dest chain ID and recipient, then either:
//
//   - routes to the EVM solver's existing solve path (Solana -> EVM), or
//   - calls SolanaClient.SolveIntentOnChain2 on the destination Solana
//     cluster (Solana -> Solana; rare but supported).
//
// On successful destination solve it triggers settle via the relayer's
// /settle endpoint, which now branches by source-chain runtime
// (settleSolanaSourceIntent for Solana sources).
func (s *Solver) dispatchPostSolanaDeposit(
	ctx context.Context,
	chainID string,
	sc *blockchain.SolanaClient,
	intentID *big.Int,
	logger *log.Entry,
) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	snap, err := sc.GetIntent(rctx, intentID)
	cancel()
	if err != nil {
		logger.WithError(err).Error("Solana: post-deposit GetIntent failed")
		return
	}
	if snap.DestChainID == nil {
		logger.Error("Solana: intent has nil destChainID")
		return
	}
	destChainIDStr := snap.DestChainID.String()

	// Solana -> Solana: deliver via destination Solana client.
	if destSC, ok := s.solanaClients[destChainIDStr]; ok && destSC != sc {
		logger.WithField("destChainID", destChainIDStr).
			Info("Solana->Solana destination delivery (not yet wired in driver)")
		// Phase 5: build solve_intent_on_chain2 on destSC. Out of scope
		// for the EVM-paired test harness; same shape as the EVM-side
		// dispatchEVMToSolanaSolve.
		return
	}

	// Solana -> EVM: route through EVM client.
	evmClient, ok := s.clients[destChainIDStr]
	if !ok {
		logger.WithField("destChainID", destChainIDStr).
			Error("Solana: destination chain not configured")
		return
	}

	if len(snap.Recipient.Bytes) != 32 {
		logger.Error("Solana: intent recipient malformed")
		return
	}
	recipientAddr := common.BytesToAddress(snap.Recipient.Bytes[12:]) // last 20 bytes = EVM addr
	if recipientAddr == (common.Address{}) {
		logger.Error("Solana: recipient resolves to zero EVM address")
		return
	}

	// Resolve the EVM destination token. Solana tokenA mint maps to a
	// canonical symbol (USDC / WSOL); we look up the EVM-side address by
	// (destChainID, symbol) from the rebalancer's token registry.
	chainCfg, _ := s.cfg.GetChainByID(sc.ChainID())
	tokenAMintB58 := splPubkeyBytesToBase58(snap.TokenA.Bytes)
	symbol, ok := splMintToSymbol(chainCfg, tokenAMintB58)
	if !ok {
		logger.WithField("mint", tokenAMintB58).Error("Solana: tokenA mint not in allowlist")
		return
	}
	evmTokenB := rebalancer.TokenAddress(destChainIDStr, rebalancer.TokenSym(symbol))
	if evmTokenB == (common.Address{}) {
		logger.WithFields(log.Fields{
			"destChainID": destChainIDStr,
			"symbol":      symbol,
		}).Error("Solana: no EVM destination token for (chainID, symbol)")
		return
	}

	// Approve + solve on EVM. The user param on the EVM contract is just
	// for event-log bookkeeping (the contract checks it != 0 but doesn't
	// use it for transfers); we pass the recipient address as a non-zero
	// stand-in since the Solana sender has no native EVM identity.
	expectedAmountB := snap.ExpectedAmountB
	destChainName := destChainIDStr
	if cfg, ok := s.cfg.Chains[destChainIDStr]; ok {
		destChainName = cfg.Name
	}
	logger.WithFields(log.Fields{
		"destChain":     destChainName,
		"evmTokenB":     evmTokenB.Hex(),
		"recipient":     recipientAddr.Hex(),
		"amountB":       expectedAmountB.String(),
	}).Info("Solana->EVM: dispatching solveIntentOnChain2")

	// Cross-decimal scaling: SPL token decimals vary per mint (USDC=6,
	// SOL/WSOL=9, others arbitrary). EVM token decimals also vary
	// per ERC20. Read BOTH dynamically from on-chain so the math is
	// always correct regardless of which mint/token pair the user uses.
	//
	// Source: SPL mint stored in the IntentRecord (snap.TokenA).
	// Dest:   ERC20 contract on the EVM chain (evmTokenB).
	srcMintPubkey := solana.PublicKeyFromBytes(snap.TokenA.Bytes)
	srcDecimalsU8, srcDecErr := sc.GetMintDecimals(ctx, srcMintPubkey)
	if srcDecErr != nil {
		logger.WithError(srcDecErr).Warn("Solana->EVM: couldn't fetch source mint decimals; assuming 6")
		srcDecimalsU8 = 6
	}
	dstDecimalsU8, dstDecErr := evmClient.GetTokenDecimals(ctx, evmTokenB)
	if dstDecErr != nil {
		logger.WithError(dstDecErr).Warn("Solana->EVM: couldn't fetch dest token decimals; assuming same as source (no scaling)")
		dstDecimalsU8 = srcDecimalsU8
	}
	srcDecimals := int(srcDecimalsU8)
	dstDecimals := int(dstDecimalsU8)

	// Scale up when destination uses more decimals, down (lossy) when fewer.
	// Equal decimals → factor = 1, passthrough.
	scaleFactor := new(big.Int).SetInt64(1)
	switch {
	case dstDecimals > srcDecimals:
		scaleFactor.Exp(big.NewInt(10), big.NewInt(int64(dstDecimals-srcDecimals)), nil)
	case dstDecimals < srcDecimals:
		// Down-scale by integer division. Truncates fractional source
		// units that don't fit in the destination's lower precision.
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(srcDecimals-dstDecimals)), nil)
		scaleFactor.SetInt64(0) // sentinel: divide instead of multiply below
		_ = divisor
		logger.WithFields(log.Fields{"srcDecimals": srcDecimals, "dstDecimals": dstDecimals}).
			Warn("Solana->EVM: destination has fewer decimals than source; amounts will be truncated")
	}

	scaleAmount := func(raw *big.Int) *big.Int {
		if dstDecimals == srcDecimals {
			return new(big.Int).Set(raw)
		}
		if dstDecimals > srcDecimals {
			return new(big.Int).Mul(raw, scaleFactor)
		}
		divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(srcDecimals-dstDecimals)), nil)
		return new(big.Int).Quo(raw, divisor)
	}

	scaledExpectedAmountB := scaleAmount(expectedAmountB)
	logger.WithFields(log.Fields{
		"srcMint":               srcMintPubkey.String(),
		"srcDecimals":           srcDecimals,
		"dstToken":              evmTokenB.Hex(),
		"dstDecimals":           dstDecimals,
		"rawExpectedAmountB":    expectedAmountB.String(),
		"scaledExpectedAmountB": scaledExpectedAmountB.String(),
	}).Info("Solana->EVM: cross-decimal scaling applied")

	// Recipients: consult relayer's /get-recipients first. If present, fan
	// out to N predicted Safe addresses; else fall back to the single
	// recipient from the on-chain IntentRecord. Per-recipient amounts also
	// get the same scaling.
	evmRecipientsRaw, evmAmountsRaw := buildEVMRecipients(s, intentID, sc.ChainID(), recipientAddr, expectedAmountB, logger)
	evmRecipients := evmRecipientsRaw
	evmAmounts := make([]*big.Int, len(evmAmountsRaw))
	for i, a := range evmAmountsRaw {
		evmAmounts[i] = new(big.Int).Mul(a, scaleFactor)
	}

	approveCtx, approveCancel := context.WithTimeout(ctx, 90*time.Second)
	defer approveCancel()
	approveTx, err := evmClient.ApproveToken(approveCtx, evmTokenB, scaledExpectedAmountB)
	if err != nil {
		logger.WithError(err).Error("Solana->EVM: approveToken failed")
		return
	}
	if _, err := evmClient.WaitForTransaction(approveCtx, approveTx); err != nil {
		logger.WithError(err).Error("Solana->EVM: approve confirmation failed")
		return
	}
	time.Sleep(5 * time.Second) // RPC propagation buffer

	solveCtx, solveCancel := context.WithTimeout(ctx, 120*time.Second)
	defer solveCancel()
	solveTx, err := evmClient.SolveIntentOnChain2(
		solveCtx,
		sc.ChainID(),                     // sourceChainId = Solana EID
		intentID,
		recipientAddr,                    // _user (event bookkeeping)
		evmTokenB,
		scaledExpectedAmountB,
		evmRecipients,
		evmAmounts,
	)
	if err != nil {
		logger.WithError(err).Error("Solana->EVM: solveIntentOnChain2 failed")
		return
	}
	if _, err := evmClient.WaitForTransaction(solveCtx, solveTx); err != nil {
		logger.WithError(err).Error("Solana->EVM: solve confirmation failed")
		return
	}
	logger.WithField("solveTx", solveTx.Hash().Hex()).
		Info("Solana->EVM: destination delivery confirmed")

	// Trigger settle on the Solana source via the relayer's /settle
	// endpoint. The relayer's settleSolanaSourceIntent will verify the
	// EVM-side solve and submit settle_intent_with_chain2_verification
	// to the Anchor program, releasing escrow back to us.
	go s.settleIntent(
		ctx,
		chainID,                   // sourceNetwork = Solana chain ID string
		intentID,
		intentID,                  // chain2IntentID — same value (we use intentID symmetrically)
		evmClient.GetSolverAddress(),
		destChainName,
		"",                        // no Solana deposit tx hash threaded yet
		solveTx.Hash().Hex(),
		nil,                       // no EVM-shaped types.Intent for Solana-source
		sc.ChainID().String(),
	)
}

// buildEVMRecipients consults the relayer's /get-recipients for the
// Solana-source intent. If present, returns N predicted-Safe addresses
// and their split amounts. Falls back to single-recipient delivery using
// the on-chain IntentRecord.recipient (the EVM address derived from
// snap.Recipient.Bytes[12:]) when nothing is stored.
func buildEVMRecipients(
	s *Solver,
	intentID *big.Int,
	sourceChainID *big.Int,
	onchainRecipient common.Address,
	expectedAmountB *big.Int,
	logger *log.Entry,
) ([]common.Address, []*big.Int) {
	fallback := []common.Address{onchainRecipient}
	fallbackAmt := []*big.Int{expectedAmountB}

	recipients, amounts, err := s.getRecipientsFromRelayer(intentID.String(), sourceChainID.String())
	if err != nil {
		logger.WithError(err).Debug("no stored recipients (using single on-chain recipient)")
		return fallback, fallbackAmt
	}
	if len(recipients) == 0 || len(recipients) != len(amounts) {
		logger.WithFields(log.Fields{"got": len(recipients), "amounts": len(amounts)}).
			Warn("stored recipients malformed; using single on-chain recipient")
		return fallback, fallbackAmt
	}

	outAddrs := make([]common.Address, 0, len(recipients))
	outAmts := make([]*big.Int, 0, len(amounts))
	for i, r := range recipients {
		if !common.IsHexAddress(r) {
			logger.WithFields(log.Fields{"idx": i, "raw": r}).
				Warn("invalid EVM recipient hex; falling back to single recipient")
			return fallback, fallbackAmt
		}
		amt, ok := new(big.Int).SetString(amounts[i], 10)
		if !ok {
			logger.WithFields(log.Fields{"idx": i, "raw": amounts[i]}).
				Warn("invalid amount; falling back to single recipient")
			return fallback, fallbackAmt
		}
		outAddrs = append(outAddrs, common.HexToAddress(r))
		outAmts = append(outAmts, amt)
	}
	logger.WithField("recipientCount", len(outAddrs)).
		Info("Solana→EVM: using multi-recipient list from relayer")
	return outAddrs, outAmts
}

// scheduleSolanaFinalize sleeps until the auction window closes, then
// submits `finalize_auction` for our intent. Anchor reverts if someone
// already finalized; we log-and-continue.
func (s *Solver) scheduleSolanaFinalize(
	ctx context.Context,
	sc *blockchain.SolanaClient,
	intentID *big.Int,
	endTime int64,
	logger *log.Entry,
) {
	const finalizeBuffer = 5 * time.Second
	wait := time.Until(time.Unix(endTime, 0)) + finalizeBuffer
	if wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	fctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	sig, err := sc.FinalizeAuction(fctx, intentID)
	if err != nil {
		// Most-likely failure is "auction already finalized" — another
		// participant beat us to it. Not actionable.
		logger.WithError(err).Debug("Solana: finalize_auction declined (likely already finalized)")
		return
	}
	logger.WithField("sig", sig).Info("Solana: finalize_auction submitted")
}

// calculateProfitableBidSolana mirrors the EVM profitability gate for a
// Solana-source intent. Returns (targetBid, true) when the intent is
// worth bidding on; (nil, false) otherwise. Skips Solana gas per the
// design lock.
func (s *Solver) calculateProfitableBidSolana(
	chainID string,
	sc *blockchain.SolanaClient,
	snap blockchain.IntentSnapshot,
) (*big.Int, bool) {
	chainCfg, err := s.cfg.GetChainByID(sc.ChainID())
	if err != nil {
		log.WithError(err).WithField("chainID", chainID).Info("Solana: chain config missing, skipping")
		return nil, false
	}

	tokenAMint := splPubkeyBytesToBase58(snap.TokenA.Bytes)
	tokenBMint := splPubkeyBytesToBase58(snap.TokenB.Bytes)

	symbolA, ok := splMintToSymbol(chainCfg, tokenAMint)
	if !ok {
		log.WithField("mint", tokenAMint).Info("Solana: tokenA mint not in allowlist, skipping")
		return nil, false
	}
	symbolB, ok := splMintToSymbol(chainCfg, tokenBMint)
	if !ok {
		// For Solana->EVM intents, tokenB is a sentinel (== tokenA mint);
		// the actual EVM token lives off-chain and isn't priced via this
		// path. Treat the Solana side as if both legs are tokenA-priced
		// (correct because the user's deposit and the solver's payout are
		// both in tokenA on Solana). This keeps the profitability check
		// honest for Solana->EVM without needing the EVM token symbol.
		symbolB = symbolA
	}

	priceA, err := s.oracle.FetchConsensusPrice(symbolA)
	if err != nil {
		log.WithError(err).WithField("symbol", symbolA).Info("Solana: oracle priceA failed, skipping")
		return nil, false
	}
	priceB, err := s.oracle.FetchConsensusPrice(symbolB)
	if err != nil {
		log.WithError(err).WithField("symbol", symbolB).Info("Solana: oracle priceB failed, skipping")
		return nil, false
	}

	decA := 6
	if d, ok := splMintDecimals(chainCfg, tokenAMint); ok {
		decA = int(d)
	}
	decB := 6
	if d, ok := splMintDecimals(chainCfg, tokenBMint); ok {
		decB = int(d)
	}

	amountAFloat, _ := new(big.Float).SetInt(snap.AmountA).Float64()
	rewardFloat, _ := new(big.Float).SetInt(snap.Reward).Float64()
	expectedBFloat, _ := new(big.Float).SetInt(snap.ExpectedAmountB).Float64()

	revenueUSD := ((amountAFloat + rewardFloat) / math.Pow(10, float64(decA))) * priceA
	payoutUSD := (expectedBFloat / math.Pow(10, float64(decB))) * priceB
	if payoutUSD <= 0 {
		return nil, false
	}
	margin := (revenueUSD - payoutUSD) / payoutUSD
	if margin < s.cfg.SolverConfig.MinProfitMargin {
		log.WithFields(log.Fields{
			"intent":      snap.IntentID.String(),
			"revenueUSD":  revenueUSD,
			"payoutUSD":   payoutUSD,
			"margin":      margin,
			"minMargin":   s.cfg.SolverConfig.MinProfitMargin,
		}).Info("Solana: margin below threshold")
		return nil, false
	}

	// Target bid = (amountA + reward) * (1 - MinProfitMargin). Mirrors
	// the EVM formula: solver pledges this much tokenA on Solana to win
	// the auction; receives amountA + reward + winningBid back at settle.
	minBid := new(big.Int).Add(snap.AmountA, snap.Reward)
	marginFactor := new(big.Float).SetFloat64(1.0 - s.cfg.SolverConfig.MinProfitMargin)
	targetFloat := new(big.Float).Mul(new(big.Float).SetInt(minBid), marginFactor)
	target, _ := targetFloat.Int(nil)
	if target.Sign() <= 0 {
		return nil, false
	}
	if target.Cmp(s.cfg.SolverConfig.MaxBidAmount) > 0 {
		log.WithFields(log.Fields{
			"target":       target.String(),
			"maxBidAmount": s.cfg.SolverConfig.MaxBidAmount.String(),
		}).Info("Solana: bid would exceed MaxBidAmount, skipping")
		return nil, false
	}
	return target, true
}

// handleSolanaIntentWon is invoked when finalize_auction sets the winning
// solver to *us*. We must call deposit_and_pickup within the deposit
// window or the intent will be cancellable.
func (s *Solver) handleSolanaIntentWon(ctx context.Context, chainID string, sc *blockchain.SolanaClient, evt blockchain.IntentEvent) {
	logger := log.WithFields(log.Fields{
		"chain":   sc.NetworkName(),
		"chainID": chainID,
		"intent":  evt.Snapshot.IntentID.String(),
	})

	// Bytes-equality check: avoid the (string-format vs string-format)
	// pitfall where hex/base58 mismatches silently pass through.
	mySignerBytes := sc.SignerPubkey().Bytes()
	if len(evt.Snapshot.WinningSolver.Bytes) != 32 ||
		!bytes.Equal(evt.Snapshot.WinningSolver.Bytes, mySignerBytes) {
		logger.WithField("winner", solanaB58(evt.Snapshot.WinningSolver)).
			Debug("Solana: intent won by another solver")
		return
	}
	logger.Info("Solana: we won — depositing")

	// One in-flight deposit per intent.
	s.mu.Lock()
	if s.handlingWins[chainID+"-"+evt.Snapshot.IntentID.String()] {
		s.mu.Unlock()
		return
	}
	s.handlingWins[chainID+"-"+evt.Snapshot.IntentID.String()] = true
	s.mu.Unlock()

	go func() {
		dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		sig, err := sc.DepositAndPickup(dctx, evt.Snapshot.IntentID)
		if err != nil {
			logger.WithError(err).Error("Solana: deposit_and_pickup failed")
			s.mu.Lock()
			delete(s.handlingWins, chainID+"-"+evt.Snapshot.IntentID.String())
			s.mu.Unlock()
			return
		}
		logger.WithField("sig", sig).Info("Solana: deposit_and_pickup confirmed")

		// Chain into destination delivery. Read the IntentRecord so we
		// learn the destination chain ID and the recipient bytes; route
		// EVM-destination intents into the EVM client; route Solana-
		// destination intents (Solana->Solana) into the Solana sibling
		// path (not implemented — would call solve_intent_on_chain2 on
		// the destination cluster).
		s.dispatchPostSolanaDeposit(ctx, chainID, sc, evt.Snapshot.IntentID, logger)
	}()
}

// markSolanaIntentComplete clears tracking state for a finished intent
// (settled, solved-on-chain2, completed, or cancelled).
func (s *Solver) markSolanaIntentComplete(chainID string, evt blockchain.IntentEvent) {
	key := chainID + "-" + evt.Snapshot.IntentID.String()
	s.mu.Lock()
	delete(s.activeIntents, key)
	delete(s.handlingWins, key)
	s.mu.Unlock()
}

// solanaB58 stringifies a 32-byte Solana ChainAddress as base58. Returns
// empty string on malformed input rather than panicking; callers log it
// for diagnostics only.
func solanaB58(a blockchain.ChainAddress) string {
	if a.Kind != "solana" || len(a.Bytes) != 32 {
		return ""
	}
	// Lazy: avoid pulling base58 into the solver package; format as hex.
	// This is debug-only output. Phase 2 swaps to canonical base58 once
	// the EVM/Solana key utility helpers are unified.
	return hexStr(a.Bytes)
}

func hexStr(b []byte) string {
	const tab = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = tab[x>>4]
		out[i*2+1] = tab[x&0x0f]
	}
	return string(out)
}

// Avoid unused import warnings if a future refactor drops the math/big use.
var _ = (*big.Int)(nil)
