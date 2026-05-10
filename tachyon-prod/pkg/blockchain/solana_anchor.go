package blockchain

import (
	"crypto/sha256"

	"github.com/gagliardetto/solana-go"
)

// anchorMethodDiscriminator returns the 8-byte prefix Anchor uses on every
// program instruction: sha256("global:<method_name>")[:8].
func anchorMethodDiscriminator(methodName string) [8]byte {
	h := sha256.Sum256([]byte("global:" + methodName))
	var out [8]byte
	copy(out[:], h[:8])
	return out
}

// anchorInstruction is a lightweight solana.Instruction implementation we
// use for Anchor instructions. solana-go's GenericInstruction works but
// requires building the byte slice and accounts list separately; this
// wrapper keeps the call sites readable.
type anchorInstruction struct {
	programID solana.PublicKey
	accounts  []*solana.AccountMeta
	data      []byte
}

func (i *anchorInstruction) ProgramID() solana.PublicKey      { return i.programID }
func (i *anchorInstruction) Accounts() []*solana.AccountMeta  { return i.accounts }
func (i *anchorInstruction) Data() ([]byte, error)            { return i.data, nil }

// buildAnchorIx assembles a Solana instruction targeting our bridge_intent
// program. `methodName` is the Anchor method name in snake_case (the same
// name as in lib.rs); `argsBorsh` is the Borsh-encoded args; `accounts`
// must be in the exact order the Anchor `#[derive(Accounts)]` struct
// declares them.
func buildAnchorIx(
	programID solana.PublicKey,
	methodName string,
	argsBorsh []byte,
	accounts []*solana.AccountMeta,
) solana.Instruction {
	disc := anchorMethodDiscriminator(methodName)
	data := make([]byte, 0, 8+len(argsBorsh))
	data = append(data, disc[:]...)
	data = append(data, argsBorsh...)
	return &anchorInstruction{
		programID: programID,
		accounts:  accounts,
		data:      data,
	}
}

// meta is a small helper to keep account-list construction terse.
func meta(pk solana.PublicKey, isSigner, isWritable bool) *solana.AccountMeta {
	return &solana.AccountMeta{PublicKey: pk, IsSigner: isSigner, IsWritable: isWritable}
}

func metaR(pk solana.PublicKey) *solana.AccountMeta  { return meta(pk, false, false) }
func metaW(pk solana.PublicKey) *solana.AccountMeta  { return meta(pk, false, true) }
func metaS(pk solana.PublicKey) *solana.AccountMeta  { return meta(pk, true, false) }
func metaSW(pk solana.PublicKey) *solana.AccountMeta { return meta(pk, true, true) }
