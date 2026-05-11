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
	"github.com/gagliardetto/solana-go"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
	log "github.com/sirupsen/logrus"
)

// solanaPubkeyFromString accepts base58 (canonical) or hex-encoded
// (with/without 0x) 32-byte Solana pubkeys and returns the raw 32 bytes.
func solanaPubkeyFromString(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Try base58 first.
	if pk, err := solana.PublicKeyFromBase58(s); err == nil {
		out := make([]byte, 32)
		copy(out, pk.Bytes())
		return out, nil
	}
	// Hex fallback.
	cleaned := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(cleaned) == 64 {
		b, err := hex.DecodeString(cleaned)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not a valid Solana pubkey (base58 or 64-hex): %s", s)
}

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

	expectedAmountB := intent.ExpectedAmountB
	if expectedAmountB == nil || expectedAmountB.Sign() <= 0 {
		logger.Error("expectedAmountB is non-positive — aborting")
		return
	}

	tokenBAddr := blockchain.ChainAddress{
		Kind:  "solana",
		Bytes: append([]byte(nil), tokenBMint[:]...),
	}

	// Recipients: consult relayer's /get-recipients first. If present, fan
	// out the delivery across N stealth-PDA ATAs. Else fall back to the
	// single recipient from the on-chain `solanaIntentExt.recipient` field.
	recipientList, amountList := buildSolanaRecipients(s, intentID, sourceChainID, recipient[:], expectedAmountB, logger)
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
		recipientList,
		amountList,
	)
	if err != nil {
		logger.WithError(err).Error("Solana solve_intent_on_chain2 failed")
		return
	}

	logger.WithFields(log.Fields{
		"sig":            sig,
		"recipientCount": len(recipientList),
		"amount":         expectedAmountB.String(),
		"tokenBMint":     hex.EncodeToString(tokenBMint[:]),
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

// buildSolanaRecipients consults the relayer's /get-recipients endpoint
// to retrieve an encrypted-then-decrypted list of N stealth-PDA recipients
// for `intentID`. Falls back to the single on-chain recipient if the
// relayer has nothing stored.
//
// The relayer returns recipient pubkeys as base58 strings; we convert
// each to a 32-byte ChainAddress for the Anchor instruction.
func buildSolanaRecipients(
	s *Solver,
	intentID *big.Int,
	sourceChainID *big.Int,
	onchainRecipient []byte,
	expectedAmountB *big.Int,
	logger *log.Entry,
) ([]blockchain.ChainAddress, []*big.Int) {
	// Single-recipient fallback (current behavior).
	fallback := []blockchain.ChainAddress{{
		Kind:  "solana",
		Bytes: append([]byte(nil), onchainRecipient...),
	}}
	fallbackAmt := []*big.Int{expectedAmountB}

	recipients, amounts, err := s.getRecipientsFromRelayer(intentID.String(), sourceChainID.String())
	if err != nil {
		logger.WithError(err).Debug("no stored recipients (using on-chain single recipient)")
		return fallback, fallbackAmt
	}
	if len(recipients) == 0 || len(recipients) != len(amounts) {
		logger.WithFields(log.Fields{"got": len(recipients), "amounts": len(amounts)}).
			Warn("stored recipients malformed; using on-chain single recipient")
		return fallback, fallbackAmt
	}

	outAddrs := make([]blockchain.ChainAddress, 0, len(recipients))
	outAmts := make([]*big.Int, 0, len(amounts))
	for i, r := range recipients {
		pk, perr := solanaPubkeyFromString(r)
		if perr != nil {
			logger.WithFields(log.Fields{"idx": i, "raw": r, "error": perr}).
				Warn("invalid Solana recipient pubkey; falling back to single recipient")
			return fallback, fallbackAmt
		}
		amt, ok := new(big.Int).SetString(amounts[i], 10)
		if !ok {
			logger.WithFields(log.Fields{"idx": i, "raw": amounts[i]}).
				Warn("invalid amount; falling back to single recipient")
			return fallback, fallbackAmt
		}
		outAddrs = append(outAddrs, blockchain.ChainAddress{Kind: "solana", Bytes: pk})
		outAmts = append(outAmts, amt)
	}
	logger.WithField("recipientCount", len(outAddrs)).
		Info("EVM→Solana: using multi-recipient list from relayer")
	return outAddrs, outAmts
}
