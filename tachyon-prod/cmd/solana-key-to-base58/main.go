// solana-key-to-base58 — converts a `solana-keygen` JSON array file into the
// base58-encoded secret string the tachyon-prod env loader expects in
// SOLVER_SOLANA_PRIVATE_KEY.
//
// Usage:
//
//	go run ./cmd/solana-key-to-base58 <path-to-keypair.json>
//	go run ./cmd/solana-key-to-base58 ~/.config/solana/solver-solana.json
package main

import (
	"fmt"
	"os"

	"github.com/gagliardetto/solana-go"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: solana-key-to-base58 <path-to-keypair.json>")
		os.Exit(2)
	}
	pk, err := solana.PrivateKeyFromSolanaKeygenFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read keypair: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(pk.String())
	fmt.Fprintf(os.Stderr, "pubkey: %s\n", pk.PublicKey().String())
}
