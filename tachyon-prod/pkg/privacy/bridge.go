package privacy

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/rebalancer"
)

// Bridger executes the outbound bridge for a single intent via LI.FI.
//
// Flow per Send():
//   1. Call LI.FI /v1/quote with fromAddress = TEE Safe on src chain,
//      toAddress  = user's destination stealth Safe.
//   2. If srcToken is ERC20, ensure the TEE Safe has approved
//      estimate.approvalAddress for at least the quoted fromAmount.
//   3. Submit the returned transactionRequest as a Safe execTransaction
//      via the relayer-mediated SafeSigner (relayer pays gas, TEE keys
//      sign the SafeTxHash).
//   4. Return the source-chain tx hash. Cross-chain delivery polling
//      lives in Processor.waitForDelivery.
type Bridger struct {
	cfg    *Config
	signer *rebalancer.SafeSigner
	lifi   *LiFiClient

	slippagePct float64
}

func NewBridger(cfg *Config) (*Bridger, error) {
	teeKeys, err := loadSafeSignerKeys()
	if err != nil {
		return nil, err
	}
	if len(teeKeys) == 0 {
		return nil, fmt.Errorf("no SAFE_SIGNER_KEY_* set; the privacy router needs at least one TEE signing key")
	}

	// blockchain.NewClient requires a valid hex private key for its EOA
	// derivation, even though the privacy router only uses the Safe path
	// (relayer pays gas). Reuse PRIVATE_KEY here — it's the same EOA the
	// solver/relayer already use, and we only need its address to be
	// derivable. Actual signing happens via teeKeys against the Safe.
	pkHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pkHex == "" {
		return nil, fmt.Errorf("PRIVATE_KEY must be set (used to derive an EOA address; signing still happens via SAFE_SIGNER_KEY_*)")
	}

	clients := map[string]*blockchain.Client{}
	for chainID, cc := range cfg.Chains {
		cidBig, ok := new(big.Int).SetString(chainID, 10)
		if !ok {
			return nil, fmt.Errorf("invalid chain ID %q", chainID)
		}
		client, err := blockchain.NewClient(
			cc.RPC, "", pkHex, cidBig, "privacy-"+chainID,
			cc.TEESafe.Hex(), teeKeys, "",
		)
		if err != nil {
			return nil, fmt.Errorf("blockchain.NewClient %s: %w", chainID, err)
		}
		clients[chainID] = client
	}
	signer, err := rebalancer.NewSafeSignerFromClients(clients)
	if err != nil {
		return nil, fmt.Errorf("safe signer: %w", err)
	}
	if signer == nil {
		return nil, fmt.Errorf("no chains configured with TEE Safe addresses")
	}
	log.WithField("chains", len(clients)).Info("Privacy bridger ready (LI.FI)")

	return &Bridger{
		cfg:         cfg,
		signer:      signer,
		lifi:        NewLiFiClient(cfg.LifiAPIKey),
		slippagePct: 0.01, // 1% default slippage
	}, nil
}

// Send fulfils a single BridgeIntent. Returns the source-chain tx hash
// once the TEE Safe execTransaction is mined. Delivery confirmation is
// the caller's responsibility (Processor.waitForDelivery polls /status).
//
// Routes are dispatched to one of two bridges based on the route table
// in addresses.go:
//   - IsStargateRoute (Horizen-touching, USDC.e ↔ USDC) → sendStargate
//   - IsLifiRoute (MegaETH-touching, USDC/USDT0/ETH)     → sendLifi
func (b *Bridger) Send(ctx context.Context, intent *BridgeIntent) (string, error) {
	if IsStargateRoute(intent.SourceChainID, intent.DestChainID, intent.SourceToken, intent.DestToken) {
		return b.sendStargate(ctx, intent)
	}
	return b.sendLifi(ctx, intent)
}

// sendStargate calls Stargate's OFT.send() directly via the TEE Safe.
// Reuses the rebalancer's LzSend primitive, which handles quoteSend +
// approve + send + WaitMined + cross-chain arrival polling.
func (b *Bridger) sendStargate(ctx context.Context, intent *BridgeIntent) (string, error) {
	src := intent.SourceChainID
	dst := intent.DestChainID
	if _, ok := b.cfg.Chains[src]; !ok {
		return "", fmt.Errorf("source chain %s not configured", src)
	}

	srcOFT := StargateOFT(src, intent.SourceToken)
	srcToken := TokenAddress(src, intent.SourceToken)
	dstToken := TokenAddress(dst, intent.DestToken)
	dstEid := LzEid(dst)
	if srcOFT == (common.Address{}) || dstEid == 0 {
		return "", fmt.Errorf("stargate route resolution failed for %s:%s → %s:%s",
			src, intent.SourceToken, dst, intent.DestToken)
	}
	dstRecipient := common.HexToAddress(intent.DestStealthSafe)

	// Skim StargateFeeBps off the user's deposit before bridging. The
	// deducted amount stays in the TEE Safe (since LzSend only moves
	// `bridgeAmount` into the OFT pool) and accrues as operator
	// reserve to fund future LZ messaging fees, paid in source-chain
	// native ETH by the relayer EOA via msg.value forwarding.
	// Mirrors tachyon's 2.5% intent-spread compensation.
	bridgeAmount := new(big.Int).Set(intent.Amount)
	feeAmount := big.NewInt(0)
	if b.cfg.StargateFeeBps > 0 {
		feeAmount = new(big.Int).Mul(intent.Amount, big.NewInt(b.cfg.StargateFeeBps))
		feeAmount.Div(feeAmount, big.NewInt(10_000))
		bridgeAmount.Sub(bridgeAmount, feeAmount)
	}

	log.WithFields(log.Fields{
		"sessionID":     intent.SessionID,
		"src":           src,
		"dst":           dst,
		"srcToken":      intent.SourceToken,
		"dstToken":      intent.DestToken,
		"srcOFT":        srcOFT.Hex(),
		"dstEid":        dstEid,
		"recipient":     dstRecipient.Hex(),
		"depositAmount": intent.Amount.String(),
		"bridgeAmount":  bridgeAmount.String(),
		"operatorFee":   feeAmount.String(),
		"feeBps":        b.cfg.StargateFeeBps,
	}).Info("Privacy bridge: Stargate send")

	hash, err := rebalancer.LzSend(
		ctx,
		b.signer,
		src,
		srcOFT,
		srcToken,
		dst,
		dstToken,
		dstEid,
		dstRecipient,
		bridgeAmount,
		b.slippagePct,
		big.NewInt(0),
	)
	return hash, err
}

// sendLifi is the previous LI.FI-routed path (MegaETH ↔ {Base, Eth,
// Arb, Opt}). Kept as a separate method so the dispatcher in Send is
// trivially extensible.
func (b *Bridger) sendLifi(ctx context.Context, intent *BridgeIntent) (string, error) {
	src := intent.SourceChainID
	cc, ok := b.cfg.Chains[src]
	if !ok {
		return "", fmt.Errorf("source chain %s not configured", src)
	}

	srcTokenAddr := TokenAddress(src, intent.SourceToken)
	dstTokenAddr := TokenAddress(intent.DestChainID, intent.DestToken)
	if srcTokenAddr == (common.Address{}) || dstTokenAddr == (common.Address{}) {
		return "", fmt.Errorf("unresolved token address for route %s:%s → %s:%s",
			src, intent.SourceToken, intent.DestChainID, intent.DestToken)
	}

	dstStealth := common.HexToAddress(intent.DestStealthSafe)
	teeSafe := cc.TEESafe

	log.WithFields(log.Fields{
		"sessionID":  intent.SessionID,
		"src":        src,
		"dst":        intent.DestChainID,
		"srcToken":   intent.SourceToken,
		"dstToken":   intent.DestToken,
		"amount":     intent.Amount.String(),
		"fromSafe":   teeSafe.Hex(),
		"toRecipient": dstStealth.Hex(),
	}).Info("Privacy bridge: requesting LI.FI quote")

	// LI.FI's quote endpoint can intermittently return 404 ("No
	// available quotes") for small amounts when no Across/Relay
	// relayer is online to take the order. The pool typically
	// recovers within a minute. Retry transient quote failures with
	// backoff before giving up — without this, ~1% of small bridges
	// land in `failed` and need a manual /refund + resubmit.
	const lifiQuoteRetries = 3
	const lifiQuoteBackoff = 30 * time.Second
	var quote *LiFiQuote
	var quoteErr error
	for attempt := 0; attempt < lifiQuoteRetries; attempt++ {
		quote, quoteErr = b.lifi.Quote(ctx, src, intent.DestChainID,
			srcTokenAddr.Hex(), dstTokenAddr.Hex(),
			teeSafe.Hex(), dstStealth.Hex(),
			intent.Amount, b.slippagePct)
		if quoteErr == nil {
			break
		}
		if !isTransientLifiErr(quoteErr) {
			return "", fmt.Errorf("lifi quote: %w", quoteErr)
		}
		log.WithError(quoteErr).WithFields(log.Fields{
			"sessionID": intent.SessionID,
			"attempt":   attempt + 1,
			"max":       lifiQuoteRetries,
		}).Warn("LI.FI quote transient failure, retrying after backoff")
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(lifiQuoteBackoff):
		}
	}
	if quoteErr != nil {
		return "", fmt.Errorf("lifi quote: %w (after %d attempts)", quoteErr, lifiQuoteRetries)
	}
	log.WithFields(log.Fields{
		"sessionID":   intent.SessionID,
		"tool":        quote.ToolDetails.Name,
		"approveTo":   quote.Estimate.ApprovalAddress,
		"toAmountUSD": quote.Estimate.ToAmountUSD,
		"durationS":   quote.Estimate.ExecutionDur,
		"target":      quote.TransactionRequest.To,
	}).Info("Privacy bridge: quote received")

	// 1. Approve src ERC20 to LI.FI's spender if needed (skip for native).
	// ensureAllowance returns submitted=true iff it actually sent an
	// approve tx; in that case we wait for the Safe nonce to advance
	// before signing the bridge tx (otherwise the second
	// SignSafeTransaction can read a stale nonce off a lagging RPC node
	// and the bridge fails with Safe GS026).
	if !IsNativeToken(intent.SourceToken) && quote.Estimate.ApprovalAddress != "" {
		preNonce, _ := b.readSafeNonce(ctx, src)
		submitted, err := b.ensureAllowance(ctx, src,
			srcTokenAddr,
			common.HexToAddress(quote.Estimate.ApprovalAddress),
			intent.Amount)
		if err != nil {
			return "", fmt.Errorf("approve: %w", err)
		}
		if submitted {
			if err := b.waitNonceAdvance(ctx, src, preNonce, 30*time.Second); err != nil {
				return "", fmt.Errorf("nonce-advance after approve: %w", err)
			}
		}
	}

	// 2. Submit transactionRequest via TEE Safe.
	target := common.HexToAddress(quote.TransactionRequest.To)
	value, err := decodeHexUint(quote.TransactionRequest.Value)
	if err != nil {
		return "", fmt.Errorf("decode tx value: %w", err)
	}
	data, err := hexutil.Decode(quote.TransactionRequest.Data)
	if err != nil {
		return "", fmt.Errorf("decode tx data: %w", err)
	}
	gasLimit, _ := decodeHexUint(quote.TransactionRequest.GasLimit)
	if gasLimit == nil || gasLimit.Sign() <= 0 {
		gasLimit = big.NewInt(800_000) // fall back to a generous fixed limit
	}

	tx, _, err := b.signer.SendAndWait(ctx, src, target, value, data, gasLimit.Uint64(), 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("submit safe execTx: %w", err)
	}
	log.WithFields(log.Fields{
		"sessionID": intent.SessionID,
		"srcTxHash": tx.Hash().Hex(),
		"target":    target.Hex(),
	}).Info("Privacy bridge: source tx mined")

	return tx.Hash().Hex(), nil
}

// isTransientLifiErr classifies a LI.FI client error as worth
// retrying. The two patterns we see in production:
//
//   - 404 "No available quotes for the requested transfer" — Across/
//     Relay relayer pool briefly empty for this (chain pair, amount).
//     Recovers within a minute.
//   - 5xx server errors / network timeouts — generic transient.
//
// Validation errors (400s on bad address, unsupported chain, etc.)
// and 4xx other than 404 are NOT retried — they won't recover and
// the user needs to refund + resubmit.
func isTransientLifiErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"):
		return true
	case strings.Contains(msg, "500"), strings.Contains(msg, "502"), strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return true
	case strings.Contains(msg, "context deadline exceeded"):
		return true
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "i/o timeout"):
		return true
	}
	return false
}

// Refund returns an `intent.Amount` of `intent.SourceToken` from the
// TEE Safe on `intent.SourceChainID` to the user's original EOA
// (`intent.UserAddress`). Caller must have validated state already
// — Refund itself only checks that the chain is configured and the
// token resolves to a real contract.
//
// Returns the on-chain tx hash. The Safe execTx is signed with the
// TEE keys and submitted via the relayer (gas paid by relayer EOA),
// same as every other privacy-router on-chain action.
func (b *Bridger) Refund(ctx context.Context, intent *BridgeIntent) (string, error) {
	src := intent.SourceChainID
	if _, ok := b.cfg.Chains[src]; !ok {
		return "", fmt.Errorf("source chain %s not configured", src)
	}
	if intent.Amount == nil || intent.Amount.Sign() <= 0 {
		return "", fmt.Errorf("intent amount %v is not positive", intent.Amount)
	}
	srcTokenAddr := TokenAddress(src, intent.SourceToken)
	if srcTokenAddr == (common.Address{}) {
		return "", fmt.Errorf("no token address for %s on chain %s", intent.SourceToken, src)
	}
	if !common.IsHexAddress(intent.UserAddress) {
		return "", fmt.Errorf("intent.UserAddress %q invalid", intent.UserAddress)
	}
	user := common.HexToAddress(intent.UserAddress)

	// transfer(address,uint256) selector = 0xa9059cbb
	data := append([]byte{0xa9, 0x05, 0x9c, 0xbb},
		append(common.LeftPadBytes(user.Bytes(), 32),
			common.LeftPadBytes(intent.Amount.Bytes(), 32)...)...)

	log.WithFields(log.Fields{
		"sessionID": intent.SessionID,
		"chain":     src,
		"token":     srcTokenAddr.Hex(),
		"to":        user.Hex(),
		"amount":    intent.Amount.String(),
	}).Info("Privacy bridge: refund initiated")

	tx, _, err := b.signer.SendAndWait(ctx, src, srcTokenAddr, big.NewInt(0), data, 100_000, 2*time.Minute)
	if err != nil {
		return "", fmt.Errorf("refund execTx: %w", err)
	}
	return tx.Hash().Hex(), nil
}

// ensureAllowance checks the TEE Safe's current allowance for `spender`
// on `token` and, if below `amount`, submits an `approve` Safe execTx
// for max-uint to amortise future approvals. Returns submitted=true iff
// an approve tx was actually sent (and mined); false when allowance
// was already sufficient.
func (b *Bridger) ensureAllowance(ctx context.Context, chainID string, token, spender common.Address, amount *big.Int) (submitted bool, err error) {
	allowance, err := b.allowanceOf(ctx, chainID, token, b.signer.AddressOnChain(chainID), spender)
	if err != nil {
		return false, fmt.Errorf("read allowance: %w", err)
	}
	if allowance.Cmp(amount) >= 0 {
		return false, nil // already enough
	}

	// approve(spender, max). max uint256:
	maxApprove := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	// approve(address,uint256) selector = 0x095ea7b3
	data := append([]byte{0x09, 0x5e, 0xa7, 0xb3},
		append(common.LeftPadBytes(spender.Bytes(), 32),
			common.LeftPadBytes(maxApprove.Bytes(), 32)...)...)
	tx, _, err := b.signer.SendAndWait(ctx, chainID, token, big.NewInt(0), data, 100_000, 2*time.Minute)
	if err != nil {
		return false, fmt.Errorf("approve safe execTx: %w", err)
	}
	log.WithFields(log.Fields{
		"chain":   chainID,
		"token":   token.Hex(),
		"spender": spender.Hex(),
		"txHash":  tx.Hash().Hex(),
	}).Info("Privacy bridge: approval submitted")
	return true, nil
}

// readSafeNonce reads the Safe's current nonce on `chainID` via eth_call.
// nonce() selector = 0xaffed0e0. Returns 0 on any error so callers can
// keep moving (the caller's poll-loop will surface real read failures).
func (b *Bridger) readSafeNonce(ctx context.Context, chainID string) (*big.Int, error) {
	data := []byte{0xaf, 0xfe, 0xd0, 0xe0}
	out, err := b.signer.CallReadOnly(ctx, chainID, b.signer.AddressOnChain(chainID), data)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// waitNonceAdvance polls Safe.nonce() on chainID until it strictly
// exceeds `prev`, or `timeout` elapses. Used after ensureAllowance to
// ensure the RPC node we're about to call SignSafeTransaction against
// is past the approve tx — otherwise SignSafeTransaction reads the
// stale nonce and the bridge tx fails with Safe GS026.
func (b *Bridger) waitNonceAdvance(ctx context.Context, chainID string, prev *big.Int, timeout time.Duration) error {
	if prev == nil {
		prev = new(big.Int)
	}
	deadline := time.Now().Add(timeout)
	for {
		cur, err := b.readSafeNonce(ctx, chainID)
		if err == nil && cur != nil && cur.Cmp(prev) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("safe nonce did not advance past %s within %s (current=%v err=%v)", prev, timeout, cur, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// allowanceOf reads ERC20 allowance(owner, spender) via eth_call. The
// rebalancer's Signer exposes CallReadOnly which we reuse.
func (b *Bridger) allowanceOf(ctx context.Context, chainID string, token, owner, spender common.Address) (*big.Int, error) {
	// allowance(address,address) selector = 0xdd62ed3e
	data := append([]byte{0xdd, 0x62, 0xed, 0x3e},
		append(common.LeftPadBytes(owner.Bytes(), 32),
			common.LeftPadBytes(spender.Bytes(), 32)...)...)
	out, err := b.signer.CallReadOnly(ctx, chainID, token, data)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// decodeHexUint accepts "0x..." (or empty) and returns a *big.Int.
// Empty / "0x" / "0x0" all map to 0.
func decodeHexUint(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0x" || s == "0x0" {
		return big.NewInt(0), nil
	}
	// LI.FI returns either decimal or hex; handle both.
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := hexutil.DecodeBig(s)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("not a valid integer: %q", s)
	}
	return v, nil
}

// loadSafeSignerKeys mirrors the relayer/solver pattern: read up to
// three SAFE_SIGNER_KEY_{1,2,3} env vars, parse as hex private keys.
func loadSafeSignerKeys() ([]*ecdsa.PrivateKey, error) {
	keys := []*ecdsa.PrivateKey{}
	for _, env := range []string{"SAFE_SIGNER_KEY_1", "SAFE_SIGNER_KEY_2", "SAFE_SIGNER_KEY_3"} {
		hexStr := strings.TrimPrefix(os.Getenv(env), "0x")
		if hexStr == "" {
			continue
		}
		k, err := crypto.HexToECDSA(hexStr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", env, err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}
