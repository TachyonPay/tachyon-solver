// Solana-source settle path for the relayer.
//
// Mirrors the EVM SettleIntent flow but uses the Anchor `bridge_intent`
// program: read the IntentRecord PDA on the Solana source chain, verify
// the destination chain (EVM) recorded the solve, then submit
// `settle_intent_with_chain2_verification` signed by the relayer's
// authorized ed25519 key.

package relayer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// settleSolanaSourceIntent handles req.Network being a configured Solana
// chain. The req.IntentID is the on-chain Solana IntentRecord ID; req.
// Chain2IntentID is the EVM-side intent ID that was used as `_intentId`
// for the solveIntentOnChain2 call (we use the same ID since both sides
// keep their own counters keyed by srcChainID + intentID).
func (r *Relayer) settleSolanaSourceIntent(ctx context.Context, req *types.SettleRequest) (*types.RelayerResponse, error) {
	sc, ok := r.solanaClients[req.Network]
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("solana source network %s not configured", req.Network),
		}, nil
	}

	intentID, ok := new(big.Int).SetString(req.IntentID, 10)
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   "invalid intent ID",
		}, nil
	}
	chain2IntentID, ok := new(big.Int).SetString(req.Chain2IntentID, 10)
	if !ok {
		return &types.RelayerResponse{
			Success: false,
			Error:   "invalid chain2 intent ID",
		}, nil
	}

	// Read the IntentRecord PDA so we know the destination chain ID.
	snap, err := sc.GetIntent(ctx, intentID)
	if err != nil {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read solana intent: %v", err),
		}, nil
	}
	if snap.Completed {
		return &types.RelayerResponse{Success: true, Error: "already settled"}, nil
	}

	destChainIDStr := snap.DestChainID.String()
	log.WithFields(log.Fields{
		"sourceNetwork":  req.Network,
		"destNetwork":    destChainIDStr,
		"intentID":       req.IntentID,
		"chain2IntentID": req.Chain2IntentID,
	}).Info("Settling Solana-source intent")

	// Verify the destination chain saw the solve. With retries to ride out
	// EVM RPC propagation lag — same shape as the EVM-source path.
	const maxRetries = 10
	var solved bool
	var verifyErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 3 * time.Second)
		}
		solved, verifyErr = r.VerifyIntentSolved(ctx, destChainIDStr, req.Network, req.Chain2IntentID)
		if verifyErr == nil && solved {
			log.WithField("attempt", i+1).Info("Destination solve verified")
			break
		}
	}
	if verifyErr != nil {
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("verification failed: %v", verifyErr),
		}, nil
	}
	if !solved {
		return &types.RelayerResponse{
			Success: false,
			Error:   "intent not solved on destination chain",
		}, nil
	}

	// Submit settle_intent_with_chain2_verification on Solana.
	r.txMutexes[req.Network].Lock()
	defer r.txMutexes[req.Network].Unlock()

	sig, err := sc.SettleIntent(ctx, intentID, chain2IntentID)
	if err != nil {
		log.WithError(err).Error("Solana SettleIntent failed")
		return &types.RelayerResponse{
			Success: false,
			Error:   fmt.Sprintf("solana settle failed: %v", err),
		}, nil
	}

	log.WithFields(log.Fields{
		"intentID": req.IntentID,
		"sig":      sig,
	}).Info("Solana settle confirmed")

	return &types.RelayerResponse{
		Success:      true,
		SettleTxHash: sig,
	}, nil
}
