// EVM->Solana cross-chain dispatch.
//
// When the EVM solver wins an intent whose `destChainId` resolves to a
// Solana cluster (via the LayerZero EID convention — 30168 mainnet,
// 40168 devnet), it must deliver tokenB by calling
// `solve_intent_on_chain2` on the Solana side rather than executing the
// existing EVM destination path. This file holds the dispatcher; the
// existing `solveIntentOnDestinationChain` short-circuits into here when
// the destination is a configured Solana chain.

package solver

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
	log "github.com/sirupsen/logrus"
)

// dispatchEVMToSolanaSolve is invoked from solveIntentOnDestinationChain
// when the destination chain is configured as Solana. It pulls the
// recipient list from the relayer (those recipients are 32-byte Solana
// pubkeys returned as hex/base58 strings), looks up the destination SPL
// mint from the EVM contract's `solanaIntentExt` mapping, and submits the
// solve via the SolanaClient. On success it kicks off settle on the
// source chain via the existing settleIntent path.
func (s *Solver) dispatchEVMToSolanaSolve(
	ctx context.Context,
	sourceNetwork string,
	intentID *big.Int,
	intent *types.Intent,
	sc *blockchain.SolanaClient,
) {
	logger := log.WithFields(log.Fields{
		"intentID":      intentID,
		"sourceNetwork": sourceNetwork,
		"destChain":     sc.NetworkName(),
		"destChainID":   intent.DestChainId.String(),
	})
	logger.Info("Dispatching EVM->Solana solve")

	sourceClient := s.clients[sourceNetwork]
	sourceChainID := sourceClient.ChainID()

	// Read Solana-only intent fields from the EVM contract via the
	// solanaIntentExt mapping. tokenBMint, recipient (single, not array),
	// and destSolanaEID live there.
	tokenBMint, recipient, destEID, isSolana, err := sourceClient.GetSolanaIntentExt(ctx, intentID)
	if err != nil || !isSolana {
		logger.WithError(err).Error("intent is not flagged as Solana-destination on-chain — aborting dispatch")
		return
	}
	if destEID == 0 {
		logger.Error("destSolanaEID = 0 in solanaIntentExt — aborting")
		return
	}

	// Recipients on Solana side: phase 3 supports single-recipient
	// EVM->Solana intents (the contract's `solanaIntentExt.recipient` is
	// a single bytes32). The full amount goes there.
	expectedAmountB := intent.ExpectedAmountB
	if expectedAmountB == nil || expectedAmountB.Sign() <= 0 {
		logger.Error("expectedAmountB is non-positive — aborting")
		return
	}

	recipientAddr := blockchain.ChainAddress{
		Kind:  "solana",
		Bytes: append([]byte(nil), recipient[:]...),
	}
	tokenBAddr := blockchain.ChainAddress{
		Kind:  "solana",
		Bytes: append([]byte(nil), tokenBMint[:]...),
	}
	// Pad/truncate the EVM source `user` (20 bytes) to 32 bytes for the
	// Anchor instruction. The Solana program treats `user` as opaque
	// 32-byte tag for cross-chain bookkeeping; left-pad with zeros so a
	// reader can identify it as an EVM address.
	userBytes := make([]byte, 32)
	copy(userBytes[12:], common.HexToAddress(intent.User).Bytes())
	userAddr := blockchain.ChainAddress{Kind: "solana", Bytes: userBytes}

	dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	sig, err := sc.SolveIntentOnChain2(
		dctx,
		sourceChainID,
		intentID,
		userAddr,
		tokenBAddr,
		expectedAmountB,
		[]blockchain.ChainAddress{recipientAddr},
		[]*big.Int{expectedAmountB},
	)
	if err != nil {
		logger.WithError(err).Error("Solana solve_intent_on_chain2 failed")
		return
	}

	logger.WithFields(log.Fields{
		"sig":         sig,
		"recipient":   solanaB58Recipient(recipient[:]),
		"amount":      expectedAmountB.String(),
		"tokenBMint":  hex.EncodeToString(tokenBMint[:]),
	}).Info("Solana solve confirmed; proceeding to settle on source")

	// Settle on the source chain. The EVM relayer handles this via the
	// existing /settle endpoint, which calls
	// settleIntentWithChain2Verification on the source contract.
	key := fmt.Sprintf("%s-%s", sourceNetwork, intentID.String())
	s.mu.RLock()
	var depositTxHash string
	if status, ok := s.activeIntents[key]; ok {
		depositTxHash = status.DepositTxHash
	}
	s.mu.RUnlock()

	go s.settleIntent(
		ctx,
		sourceNetwork,
		intentID,
		intentID,
		sourceClient.GetSolverAddress(),
		sc.NetworkName(),
		depositTxHash,
		sig,
		intent,
		sourceChainID.String(),
	)
}

// findSolanaClientForDestChainID returns the Solana client whose
// configured chainID (LayerZero EID) matches `destChainID`, or nil if
// no Solana chain is configured for that EID.
func (s *Solver) findSolanaClientForDestChainID(destChainID *big.Int) *blockchain.SolanaClient {
	if destChainID == nil {
		return nil
	}
	target := destChainID.String()
	for _, sc := range s.solanaClients {
		if sc.ChainID().String() == target {
			return sc
		}
	}
	return nil
}

// solanaB58Recipient renders a 32-byte recipient buffer for log output.
// Plain hex is fine here — base58 conversion lives in solana-go and
// would pull the dependency into solver.go just for diagnostics. Phase 4
// can swap in canonical base58 once the chain-agnostic key utility lands.
func solanaB58Recipient(b []byte) string {
	return strings.ToLower(hex.EncodeToString(b))
}
