package blockchain

import (
	"encoding/base64"
	"math/big"
	"strings"

	"github.com/gagliardetto/solana-go"
	log "github.com/sirupsen/logrus"
)

// Anchor emits events on-chain via `emit!()` macros, which serialize the
// event struct (Borsh) and write it as a `Program data: <base64>` line in
// the program log. The first 8 bytes of the decoded payload are
// sha256("event:<EventName>")[:8]; the remainder is the Borsh-encoded
// fields in the order declared in events.rs.
//
// We pre-compute the discriminator for each event we care about so the
// hot path is just a constant-time map lookup.

var (
	eventDiscIntentCreated      = anchorEventDiscriminator("IntentCreated")
	eventDiscBidPlaced          = anchorEventDiscriminator("BidPlaced")
	eventDiscIntentWon          = anchorEventDiscriminator("IntentWon")
	eventDiscSolverDeposited    = anchorEventDiscriminator("SolverDeposited")
	eventDiscIntentSettled      = anchorEventDiscriminator("IntentSettled")
	eventDiscIntentSolvedOnCh2  = anchorEventDiscriminator("IntentSolvedOnChain2")
	eventDiscIntentCompleted    = anchorEventDiscriminator("IntentCompleted")
	eventDiscIntentCancelled    = anchorEventDiscriminator("IntentCancelled")
	eventDiscStealthRegistered  = anchorEventDiscriminator("StealthRegistered")
	eventDiscStealthSwept       = anchorEventDiscriminator("StealthSwept")
)

// parseAnchorEvents scans a Solana log slice and yields one IntentEvent
// per recognized Anchor event. txSig is the transaction signature (used
// as the snapshot's TxHash for downstream traceability); slotTimestamp is
// the block time captured from the websocket envelope.
func parseAnchorEvents(logs []string, txSig string, slotTimestamp int64) []IntentEvent {
	var out []IntentEvent
	for _, line := range logs {
		// Anchor `emit!` writes lines like:
		//   "Program data: <base64>"
		// We only care about that prefix; the older "Program log:" lines
		// (msg!()) are noise here.
		const prefix = "Program data: "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len(prefix):]))
		if err != nil || len(raw) < 8 {
			continue
		}
		var disc [8]byte
		copy(disc[:], raw[:8])
		body := raw[8:]

		evt, ok := decodeAnchorEvent(disc, body)
		if !ok {
			continue
		}
		evt.TxHash = txSig
		evt.Timestamp = slotTimestamp
		out = append(out, evt)
	}
	return out
}

// decodeAnchorEvent matches the discriminator and decodes the fields. The
// event-specific decoders live below.
func decodeAnchorEvent(disc [8]byte, body []byte) (IntentEvent, bool) {
	switch disc {
	case eventDiscIntentCreated:
		return decodeIntentCreated(body)
	case eventDiscBidPlaced:
		return decodeBidPlaced(body)
	case eventDiscIntentWon:
		return decodeIntentWon(body)
	case eventDiscSolverDeposited:
		return decodeSolverDeposited(body)
	case eventDiscIntentSettled:
		return decodeIntentSettled(body)
	case eventDiscIntentSolvedOnCh2:
		return decodeIntentSolvedOnChain2(body)
	case eventDiscIntentCompleted:
		return decodeIntentCompleted(body)
	case eventDiscIntentCancelled:
		return decodeIntentCancelled(body)
	}
	return IntentEvent{}, false
}

// ---- per-event decoders. Field order MUST match events.rs. ----

// IntentCreated:
//
//	intent_id u64, user [u8;32], token_a_mint [u8;32], token_b_mint [u8;32],
//	amount_a u64, expected_amount_b u64, reward u64, dest_chain_id u64,
//	recipient [u8;32], end_time i64
func decodeIntentCreated(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	user, ok2 := d.readPubkey()
	tokenAMint, ok3 := d.readPubkey()
	tokenBMint, ok4 := d.readPubkey()
	amountA, ok5 := d.readU64()
	expectedAmountB, ok6 := d.readU64()
	reward, ok7 := d.readU64()
	destChainID, ok8 := d.readU64()
	recipient, ok9 := d.readFixed32()
	endTime, ok10 := d.readI64()
	if !(ok && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8 && ok9 && ok10) {
		log.Warn("anchor IntentCreated: short payload")
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentCreated,
		Snapshot: IntentSnapshot{
			IntentID:        new(big.Int).SetUint64(intentID),
			User:            ChainAddress{Kind: "solana", Bytes: user.Bytes()},
			TokenA:          ChainAddress{Kind: "solana", Bytes: tokenAMint.Bytes()},
			TokenB:          ChainAddress{Kind: "solana", Bytes: tokenBMint.Bytes()},
			AmountA:         new(big.Int).SetUint64(amountA),
			ExpectedAmountB: new(big.Int).SetUint64(expectedAmountB),
			Reward:          new(big.Int).SetUint64(reward),
			DestChainID:     new(big.Int).SetUint64(destChainID),
			Recipient:       ChainAddress{Kind: "solana", Bytes: append([]byte(nil), recipient[:]...)},
			EndTime:         endTime,
		},
	}, true
}

// BidPlaced: intent_id u64, solver [u8;32], bid_amount u64, timestamp i64
func decodeBidPlaced(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	solver, ok2 := d.readPubkey()
	bidAmount, ok3 := d.readU64()
	if !(ok && ok2 && ok3) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventBidPlaced,
		Snapshot: IntentSnapshot{
			IntentID:      new(big.Int).SetUint64(intentID),
			WinningSolver: ChainAddress{Kind: "solana", Bytes: solver.Bytes()},
			WinningBid:    new(big.Int).SetUint64(bidAmount),
		},
	}, true
}

// IntentWon: intent_id u64, winning_solver [u8;32], winning_bid u64
func decodeIntentWon(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	winner, ok2 := d.readPubkey()
	winningBid, ok3 := d.readU64()
	if !(ok && ok2 && ok3) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentWon,
		Snapshot: IntentSnapshot{
			IntentID:      new(big.Int).SetUint64(intentID),
			WinningSolver: ChainAddress{Kind: "solana", Bytes: winner.Bytes()},
			WinningBid:    new(big.Int).SetUint64(winningBid),
		},
	}, true
}

// SolverDeposited: intent_id u64, solver [u8;32], amount u64
func decodeSolverDeposited(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	solver, ok2 := d.readPubkey()
	amount, ok3 := d.readU64()
	if !(ok && ok2 && ok3) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventSolverDeposited,
		Snapshot: IntentSnapshot{
			IntentID:      new(big.Int).SetUint64(intentID),
			WinningSolver: ChainAddress{Kind: "solana", Bytes: solver.Bytes()},
			WinningBid:    new(big.Int).SetUint64(amount),
		},
	}, true
}

// IntentSettled: intent_id u64, solver [u8;32], amount_a u64, reward u64,
//                winning_bid u64, chain2_intent_id u64
func decodeIntentSettled(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	solver, ok2 := d.readPubkey()
	amountA, ok3 := d.readU64()
	reward, ok4 := d.readU64()
	winningBid, ok5 := d.readU64()
	if !(ok && ok2 && ok3 && ok4 && ok5) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentSettled,
		Snapshot: IntentSnapshot{
			IntentID:      new(big.Int).SetUint64(intentID),
			WinningSolver: ChainAddress{Kind: "solana", Bytes: solver.Bytes()},
			AmountA:       new(big.Int).SetUint64(amountA),
			Reward:        new(big.Int).SetUint64(reward),
			WinningBid:    new(big.Int).SetUint64(winningBid),
			Completed:     true,
		},
	}, true
}

// IntentSolvedOnChain2: src_chain_id u64, src_intent_id u64, solver [u8;32],
//                       token_b_mint [u8;32], total_amount u64
func decodeIntentSolvedOnChain2(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	srcChainID, ok := d.readU64()
	srcIntentID, ok2 := d.readU64()
	solver, ok3 := d.readPubkey()
	tokenBMint, ok4 := d.readPubkey()
	totalAmount, ok5 := d.readU64()
	if !(ok && ok2 && ok3 && ok4 && ok5) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentSolvedOnChain2,
		Snapshot: IntentSnapshot{
			IntentID:        new(big.Int).SetUint64(srcIntentID),
			DestChainID:     new(big.Int).SetUint64(srcChainID),
			WinningSolver:   ChainAddress{Kind: "solana", Bytes: solver.Bytes()},
			TokenB:          ChainAddress{Kind: "solana", Bytes: tokenBMint.Bytes()},
			ExpectedAmountB: new(big.Int).SetUint64(totalAmount),
			Completed:       true,
		},
	}, true
}

// IntentCompleted: intent_id u64, solver [u8;32]
func decodeIntentCompleted(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	solver, ok2 := d.readPubkey()
	if !(ok && ok2) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentCompleted,
		Snapshot: IntentSnapshot{
			IntentID:      new(big.Int).SetUint64(intentID),
			WinningSolver: ChainAddress{Kind: "solana", Bytes: solver.Bytes()},
			Completed:     true,
		},
	}, true
}

// IntentCancelled: intent_id u64, user [u8;32]
func decodeIntentCancelled(b []byte) (IntentEvent, bool) {
	d := newBorshDecoder(b)
	intentID, ok := d.readU64()
	user, ok2 := d.readPubkey()
	if !(ok && ok2) {
		return IntentEvent{}, false
	}
	return IntentEvent{
		Kind: EventIntentCancelled,
		Snapshot: IntentSnapshot{
			IntentID: new(big.Int).SetUint64(intentID),
			User:     ChainAddress{Kind: "solana", Bytes: user.Bytes()},
		},
	}, true
}

// Reference unused (events that arrive via logs but don't produce
// IntentEvents in the chain-agnostic model — stealth ops are FE-driven).
var (
	_ = eventDiscStealthRegistered
	_ = eventDiscStealthSwept
	_ = solana.PublicKey{}
)
