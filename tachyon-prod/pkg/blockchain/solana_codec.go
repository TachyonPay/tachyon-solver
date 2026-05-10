package blockchain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// anchorAccountDiscriminator returns the 8-byte prefix Anchor uses to tag
// every account: sha256("account:<Name>")[:8].
func anchorAccountDiscriminator(structName string) [8]byte {
	h := sha256.Sum256([]byte("account:" + structName))
	var out [8]byte
	copy(out[:], h[:8])
	return out
}

// anchorEventDiscriminator returns the 8-byte prefix Anchor uses on
// emit!()'d events in `Program data:` log lines.
func anchorEventDiscriminator(eventName string) [8]byte {
	h := sha256.Sum256([]byte("event:" + eventName))
	var out [8]byte
	copy(out[:], h[:8])
	return out
}

func u64ToLE(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

// decodeIntentRecord parses raw account bytes returned by getAccountInfo
// for an `IntentRecord` PDA. Layout (Borsh, little-endian, no padding):
//
//	[0..8)    discriminator
//	[8..16)   intent_id          u64
//	[16..48)  user               [u8;32]
//	[48..80)  token_a_mint       [u8;32]
//	[80..112) token_b_mint       [u8;32]
//	[112..120) amount_a          u64
//	[120..128) expected_amount_b u64
//	[128..136) reward            u64
//	[136..144) dest_chain_id     u64
//	[144..176) recipient         [u8;32]
//	[176..184) end_time          i64
//	[184..216) winning_solver    [u8;32]
//	[216..224) winning_bid       u64
//	[224]      completed         u8
//	[225]      deposited         u8
//	[226]      bump              u8
func decodeIntentRecord(data []byte) (*IntentSnapshot, error) {
	const minLen = 227
	if len(data) < minLen {
		return nil, fmt.Errorf("IntentRecord too short: %d bytes (need %d)", len(data), minLen)
	}
	var disc [8]byte
	copy(disc[:], data[0:8])
	if disc != discIntentRecord {
		return nil, errors.New("IntentRecord discriminator mismatch")
	}

	intentID := binary.LittleEndian.Uint64(data[8:16])
	user := append([]byte(nil), data[16:48]...)
	tokenAMint := append([]byte(nil), data[48:80]...)
	tokenBMint := append([]byte(nil), data[80:112]...)
	amountA := binary.LittleEndian.Uint64(data[112:120])
	expectedAmountB := binary.LittleEndian.Uint64(data[120:128])
	reward := binary.LittleEndian.Uint64(data[128:136])
	destChainID := binary.LittleEndian.Uint64(data[136:144])
	recipient := append([]byte(nil), data[144:176]...)
	endTime := int64(binary.LittleEndian.Uint64(data[176:184]))
	winningSolver := append([]byte(nil), data[184:216]...)
	winningBid := binary.LittleEndian.Uint64(data[216:224])
	completed := data[224] != 0
	deposited := data[225] != 0

	return &IntentSnapshot{
		IntentID:        new(big.Int).SetUint64(intentID),
		User:            ChainAddress{Kind: "solana", Bytes: user},
		TokenA:          ChainAddress{Kind: "solana", Bytes: tokenAMint},
		TokenB:          ChainAddress{Kind: "solana", Bytes: tokenBMint},
		AmountA:         new(big.Int).SetUint64(amountA),
		ExpectedAmountB: new(big.Int).SetUint64(expectedAmountB),
		Reward:          new(big.Int).SetUint64(reward),
		DestChainID:     new(big.Int).SetUint64(destChainID),
		Recipient:       ChainAddress{Kind: "solana", Bytes: recipient},
		EndTime:         endTime,
		WinningSolver:   ChainAddress{Kind: "solana", Bytes: winningSolver},
		WinningBid:      new(big.Int).SetUint64(winningBid),
		Completed:       completed,
		Deposited:       deposited,
	}, nil
}

// SilenceUnused keeps the event-discriminator helper alive until the phase 1
// log parser starts consuming it. Removed once event decoding lands.
var _ = anchorEventDiscriminator
var _ = discIntentBids
var _ = discSolvedRecord
var _ = discConfig
var _ = discStealthRegist
