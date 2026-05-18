package privacy

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
)

// Processor drives the lifecycle of every BridgeIntent.
//
// Source-side detection (v0): the user is told to send the source token
// directly to the TEE hot Safe on the source chain. The processor watches
// each TEE Safe's Transfer-into events and matches them against pending
// intents by (chain, token, amount, from-address). On match, the intent
// transitions pending → swept.
//
// In v1 we'll add per-session inbound stealth Safes for true source-side
// privacy; for now the privacy guarantee comes from anonymity-set pooling
// at the TEE Safe + random delay/batching on the outbound leg.
type Processor struct {
	cfg     *Config
	store   *Store
	bridger *Bridger

	// One eth client per supported chain.
	clients map[string]*ethclient.Client

	// erc20 Transfer event topic0 = keccak256("Transfer(address,address,uint256)").
	transferTopic common.Hash

	// Per-chain "last block scanned" cursor. Forward-only, updated as
	// events are processed. Each chain has its own watcher goroutine, but
	// Go's map type is not safe for concurrent access even on disjoint
	// keys, so cursorsMu guards every read/write.
	lastScanned map[string]uint64
	cursorsMu   sync.Mutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewProcessor builds a Processor and dials each configured chain's RPC.
func NewProcessor(cfg *Config, store *Store, bridger *Bridger) (*Processor, error) {
	clients := map[string]*ethclient.Client{}
	for chainID, cc := range cfg.Chains {
		client, err := ethclient.Dial(cc.RPC)
		if err != nil {
			return nil, fmt.Errorf("dial %s rpc %s: %w", chainID, cc.RPC, err)
		}
		clients[chainID] = client
		log.WithFields(log.Fields{
			"chainID": chainID,
			"rpc":     cc.RPC,
			"teeSafe": cc.TEESafe.Hex(),
		}).Info("Privacy processor connected to chain")
	}
	return &Processor{
		cfg:           cfg,
		store:         store,
		bridger:       bridger,
		clients:       clients,
		transferTopic: crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")),
		lastScanned:   map[string]uint64{},
		stopCh:        make(chan struct{}),
	}, nil
}

// Start launches the per-chain deposit watchers and the outbound dispatch
// loop. Returns immediately; Stop blocks until they exit.
func (p *Processor) Start(ctx context.Context) {
	for chainID := range p.clients {
		p.wg.Add(1)
		go p.watchDeposits(ctx, chainID)
	}
	p.wg.Add(1)
	go p.dispatchLoop(ctx)
	p.wg.Add(1)
	go p.expirySweeper(ctx)
}

// expirySweeper periodically marks abandoned pending intents (those
// older than PendingTTL with no deposit yet) as failed. Keeps the
// deposit-matching bucket clean so future deposits at the same
// (chain, token, amount, sender) can't accidentally land on a stale
// pending intent the user has already given up on.
func (p *Processor) expirySweeper(ctx context.Context) {
	defer p.wg.Done()
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-tick.C:
		}
		n, err := p.store.ExpireStalePendings(PendingTTL)
		if err != nil {
			log.WithError(err).Warn("expirySweeper: ExpireStalePendings failed")
			continue
		}
		if n > 0 {
			log.WithField("count", n).Info("Expired stale pending intents")
		}
	}
}

// Stop signals shutdown and waits for goroutines to exit.
func (p *Processor) Stop() {
	close(p.stopCh)
	p.wg.Wait()
	for _, c := range p.clients {
		c.Close()
	}
}

// ---------------------------------------------------------------------------
// Deposit watcher (per chain)
// ---------------------------------------------------------------------------

// watchDeposits scans the chain's recent blocks for ERC20 Transfer events
// where `to == teeSafe` and tries to match each transfer against a pending
// intent. Matches transition pending → swept and record the source tx hash.
//
// Cursor (lastScanned) starts at "current block - 50" on first run so
// we don't replay months of history; subsequent ticks advance forward.
func (p *Processor) watchDeposits(ctx context.Context, chainID string) {
	defer p.wg.Done()
	cc := p.cfg.Chains[chainID]
	client := p.clients[chainID]

	// 15s ≫ 8s on deposit-detection has no UX impact: downstream bridges take
	// 30-60s to deliver, so latency on the detection leg is invisible to the
	// end-to-end flow. Bumping from 8s to 15s cuts deposit-watcher RPC volume
	// by ~47% (90 → 48 eth_blockNumber+eth_getLogs calls/min across 6 chains).
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
		}

		latest, err := client.BlockNumber(ctx)
		if err != nil {
			log.WithError(err).WithField("chain", chainID).Warn("BlockNumber failed")
			continue
		}

		p.cursorsMu.Lock()
		from := p.lastScanned[chainID]
		p.cursorsMu.Unlock()
		if from == 0 {
			if latest > 10 {
				from = latest - 10
			}
		}
		if from > latest {
			continue
		}

		// Many providers (Alchemy free tier, Infura) cap eth_getLogs at
		// 10-block windows. Walk forward in 10-block chunks rather than
		// asking for the whole catch-up range at once.
		teeTopic := common.BytesToHash(common.LeftPadBytes(cc.TEESafe.Bytes(), 32))
		const maxChunk = uint64(10)
		cursor := from
		for cursor <= latest {
			chunkTo := cursor + maxChunk - 1
			if chunkTo > latest {
				chunkTo = latest
			}
			query := ethereum.FilterQuery{
				FromBlock: new(big.Int).SetUint64(cursor),
				ToBlock:   new(big.Int).SetUint64(chunkTo),
				Topics: [][]common.Hash{
					{p.transferTopic},
					nil,        // any sender
					{teeTopic}, // recipient = TEE Safe
				},
			}
			logs, err := client.FilterLogs(ctx, query)
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"chain": chainID,
					"from":  cursor, "to": chunkTo,
				}).Warn("FilterLogs chunk failed")
				break // retry next tick from the same cursor
			}
			for _, lg := range logs {
				p.tryMatchDeposit(chainID, lg)
			}
			p.cursorsMu.Lock()
			p.lastScanned[chainID] = chunkTo + 1
			p.cursorsMu.Unlock()
			cursor = chunkTo + 1
		}
	}
}

// tryMatchDeposit takes a single Transfer-into-TEE-Safe log and walks the
// pending intents on the same chain; if exactly one matches by (token,
// amount, from-address), we mark it swept.
func (p *Processor) tryMatchDeposit(chainID string, lg types.Log) {
	if len(lg.Topics) < 3 || len(lg.Data) < 32 {
		return
	}
	tokenAddr := lg.Address
	from := common.BytesToAddress(lg.Topics[1].Bytes()[12:])
	amount := new(big.Int).SetBytes(lg.Data[:32])

	pending, err := p.store.ListByStatus(StatusPending)
	if err != nil {
		log.WithError(err).Warn("ListByStatus(pending) failed")
		return
	}
	for _, intent := range pending {
		if intent.SourceChainID != chainID {
			continue
		}
		if !strings.EqualFold(intent.InboundSafe, from.Hex()) {
			continue
		}
		if TokenAddress(chainID, intent.SourceToken) != tokenAddr {
			continue
		}
		if intent.Amount.Cmp(amount) != 0 {
			continue
		}
		// Match. Move to swept and stamp the on-chain tx as the sweep tx
		// (in v0 the deposit IS the sweep — no extra tx needed because
		// funds already landed at the TEE Safe). Lock in a random
		// fire deadline so dispatch waits a privacy-preserving amount
		// of time before sending the bridge tx.
		if err := p.store.ChangeStatus(intent.SessionID, StatusPending, StatusSwept); err != nil {
			log.WithError(err).WithField("sessionID", intent.SessionID).Warn("ChangeStatus to swept failed")
			return
		}
		_ = p.store.SetTxHash(intent.SessionID, "sweep", lg.TxHash.Hex())
		fireAt := pickFireDeadline()
		_ = p.store.SetFireAt(intent.SessionID, fireAt)
		log.WithFields(log.Fields{
			"sessionID":  intent.SessionID,
			"chain":      chainID,
			"amount":     amount.String(),
			"from":       from.Hex(),
			"sweepTx":    lg.TxHash.Hex(),
			"fireAt":     fireAt.Format("15:04:05"),
			"fireInSecs": int(time.Until(fireAt).Seconds()),
		}).Info("Deposit matched → intent swept")
		return
	}
}

// ---------------------------------------------------------------------------
// Outbound dispatch (batching + Stargate send)
// ---------------------------------------------------------------------------

// Dispatch policy. Each intent gets a randomly-chosen fire deadline
// in [fireMinS, fireMaxS] when it transitions to swept. The bucket
// fires either when its size reaches batchMinSize (immediate batch
// fire) or when any intent in the bucket passes its deadline
// (singleton or partial-batch fire). The random window IS the jitter
// — we don't add a separate jitter step at fire time.
//
// Overridable via env (production defaults: 3 / 20 / 60). For e2e
// testing set PRIVACY_BATCH_MIN_SIZE=1 and FIRE_MIN/MAX=0 to fire on
// the first dispatch tick after the intent is swept.
var (
	batchMinSize = envInt("PRIVACY_BATCH_MIN_SIZE", 3)
	fireMinS     = envInt("PRIVACY_FIRE_MIN_S", 20)
	fireMaxS     = envInt("PRIVACY_FIRE_MAX_S", 60)
)

// pickFireDeadline returns now + random(fireMinS, fireMaxS) seconds.
// Used at swept-transition time to lock in this intent's deadline.
func pickFireDeadline() time.Time {
	if fireMaxS <= fireMinS {
		return time.Now().Add(time.Duration(fireMinS) * time.Second)
	}
	delta := rand.Intn(fireMaxS - fireMinS)
	return time.Now().Add(time.Duration(fireMinS+delta) * time.Second)
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// dispatchLoop wakes every few seconds, groups swept intents by
// (src,dst,srcTok,dstTok), and fires batches that meet the policy.
func (p *Processor) dispatchLoop(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Track which intents we've already kicked off into a goroutine, so
	// we don't double-fire while the goroutine is sleeping in jitter.
	inFlight := map[string]bool{}
	var muFlight sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
		}

		swept, err := p.store.ListByStatus(StatusSwept)
		if err != nil {
			log.WithError(err).Warn("ListByStatus(swept) failed")
			continue
		}

		// Group by BatchKey.
		buckets := map[BatchKey][]*BridgeIntent{}
		for _, in := range swept {
			muFlight.Lock()
			if inFlight[in.SessionID] {
				muFlight.Unlock()
				continue
			}
			muFlight.Unlock()
			k := BatchKey{
				SourceChainID: in.SourceChainID,
				SourceToken:   in.SourceToken,
				DestChainID:   in.DestChainID,
				DestToken:     in.DestToken,
			}
			buckets[k] = append(buckets[k], in)
		}

		now := time.Now()
		for key, items := range buckets {
			// Fire when EITHER size threshold reached OR any intent in
			// the bucket has reached its random deadline. Empty FireAt
			// (legacy intents from before this field existed) is treated
			// as "deadline passed" so they don't get stuck.
			deadlinePassed := false
			var earliestFire time.Time
			for _, it := range items {
				if it.FireAt.IsZero() || now.After(it.FireAt) {
					deadlinePassed = true
				}
				if earliestFire.IsZero() || (!it.FireAt.IsZero() && it.FireAt.Before(earliestFire)) {
					earliestFire = it.FireAt
				}
			}
			fire := len(items) >= batchMinSize || deadlinePassed
			if !fire {
				continue
			}
			log.WithFields(log.Fields{
				"bucket": key.String(),
				"count":  len(items),
				"reason": map[bool]string{true: "deadline", false: "size"}[deadlinePassed && len(items) < batchMinSize],
			}).Info("Privacy batch fire")
			for _, in := range items {
				muFlight.Lock()
				inFlight[in.SessionID] = true
				muFlight.Unlock()
				p.wg.Add(1)
				go func(intent *BridgeIntent) {
					defer p.wg.Done()
					p.dispatchIntent(ctx, intent)
					muFlight.Lock()
					delete(inFlight, intent.SessionID)
					muFlight.Unlock()
				}(in)
			}
		}
	}
}

// dispatchIntent calls the LI.FI bridge and updates the intent's
// status. The privacy-preserving delay was already absorbed by the
// FireAt deadline at swept-time; no separate jitter step here.
func (p *Processor) dispatchIntent(ctx context.Context, intent *BridgeIntent) {

	if err := p.store.ChangeStatus(intent.SessionID, StatusSwept, StatusBridging); err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("ChangeStatus to bridging failed; abandoning")
		return
	}

	srcTxHash, err := p.bridger.Send(ctx, intent)
	if err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Error("Bridge failed")
		_ = p.store.SetError(intent.SessionID, err.Error())
		return
	}
	_ = p.store.SetTxHash(intent.SessionID, "bridge", srcTxHash)
	intent.BridgeTxHash = srcTxHash // mirror so waitForDelivery sees it

	// Cross-chain delivery typically lands within minutes — kick off a
	// poll-for-arrival without blocking the dispatch loop.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.waitForDelivery(ctx, intent)
	}()
}

// waitForDelivery resolves a `bridging` intent to either `delivered`
// or `failed` by polling the appropriate cross-chain confirmation
// service. Stargate / OFT routes use the public LayerZero scan API
// (which knows about Stargate v2 + ZEN OFT). LI.FI routes use
// LI.FI's /v1/status (which tracks Across/Relay/native gateway).
//
// On DELIVERED → records DeliveredTxHash, transitions to
// StatusDelivered, and publishes to the relayer's transaction
// history. On FAILED → marks the intent failed (user can /refund).
func (p *Processor) waitForDelivery(ctx context.Context, intent *BridgeIntent) {
	if intent.BridgeTxHash == "" {
		_ = p.store.SetError(intent.SessionID, "waitForDelivery: missing bridge tx hash")
		return
	}

	if IsStargateRoute(intent.SourceChainID, intent.DestChainID, intent.SourceToken, intent.DestToken) {
		p.waitForLZDelivery(ctx, intent)
		return
	}

	deadline := time.Now().Add(20 * time.Minute)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-tick.C:
		}
		if time.Now().After(deadline) {
			_ = p.store.SetError(intent.SessionID, "delivery timeout (20m)")
			return
		}
		s, err := p.bridger.lifi.Status(ctx, intent.BridgeTxHash)
		if err != nil {
			log.WithError(err).WithField("sessionID", intent.SessionID).Debug("LI.FI status poll error, retrying")
			continue
		}
		switch strings.ToUpper(s.Status) {
		case "DONE":
			if s.Receiving.TxHash != "" {
				_ = p.store.SetTxHash(intent.SessionID, "delivered", s.Receiving.TxHash)
			}
			if err := p.store.ChangeStatus(intent.SessionID, StatusBridging, StatusDelivered); err != nil {
				log.WithError(err).WithField("sessionID", intent.SessionID).Warn("ChangeStatus to delivered failed")
			}
			log.WithFields(log.Fields{
				"sessionID":  intent.SessionID,
				"dstChain":   intent.DestChainID,
				"deliveryTx": s.Receiving.TxHash,
				"amount":     s.Receiving.Amount,
			}).Info("Privacy bridge delivery confirmed (LI.FI status=DONE)")
			// Re-read the intent so we publish the latest tx hashes that
			// the in-flight handlers wrote to disk via SetTxHash above.
			if fresh, err := p.store.Get(intent.SessionID); err == nil && fresh != nil {
				PublishDelivered(p.cfg, fresh)
			} else {
				PublishDelivered(p.cfg, intent)
			}
			return
		case "FAILED", "INVALID":
			_ = p.store.SetError(intent.SessionID, "lifi status: "+s.Status+" "+s.Substatus)
			return
		default:
			// PENDING / NOT_FOUND — keep polling.
		}
	}
}

// waitForLZDelivery is the Stargate / LZ-OFT counterpart to
// waitForDelivery's LI.FI-status loop. It polls layerzero-scan by the
// source-chain tx hash, terminating on DELIVERED or FAILED.
func (p *Processor) waitForLZDelivery(ctx context.Context, intent *BridgeIntent) {
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-p.stopCh:
			cancel()
		case <-pollCtx.Done():
		}
	}()

	status, err := waitLZDelivery(pollCtx, intent.BridgeTxHash, 10*time.Second, 20*time.Minute)
	if err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("LZ delivery wait failed")
		_ = p.store.SetError(intent.SessionID, "lz delivery: "+err.Error())
		return
	}
	if status.DstTxHash != "" {
		_ = p.store.SetTxHash(intent.SessionID, "delivered", status.DstTxHash)
	}
	if err := p.store.ChangeStatus(intent.SessionID, StatusBridging, StatusDelivered); err != nil {
		log.WithError(err).WithField("sessionID", intent.SessionID).Warn("ChangeStatus to delivered failed")
	}
	log.WithFields(log.Fields{
		"sessionID":  intent.SessionID,
		"dstChain":   intent.DestChainID,
		"deliveryTx": status.DstTxHash,
	}).Info("Privacy bridge delivery confirmed (LZ scan status=DELIVERED)")
	if fresh, err := p.store.Get(intent.SessionID); err == nil && fresh != nil {
		PublishDelivered(p.cfg, fresh)
	} else {
		PublishDelivered(p.cfg, intent)
	}
}

// erc20BalanceOf is a thin balanceOf(address) reader using the standard
// ERC20 selector (no abigen needed — keeps the dependency graph small).
func erc20BalanceOf(ctx context.Context, client *ethclient.Client, token, holder common.Address) (*big.Int, error) {
	// balanceOf(address) selector = 0x70a08231
	data := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(holder.Bytes(), 32)...)
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(out[:32]), nil
}
