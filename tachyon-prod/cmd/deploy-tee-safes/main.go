// deploy-tee-safes deploys a fresh privacy-router TEE Safe on each
// configured chain that doesn't already have one in the .env file.
// Same saltNonce + same EOA owner + identical canonical factory means
// every successful deployment ends up at the same address — useful
// for ops since you can paste one Safe into all four TEE_SAFE_* slots.
//
// Usage: from tachyon-prod/, `go run ./cmd/deploy-tee-safes`. Reads
// PRIVATE_KEY + PRIVACY_RPC_<CHAIN> from .env, skips chains where
// TEE_SAFE_<CHAIN> is already populated.
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

type chainSpec struct {
	id       string
	chainID  *big.Int
	name     string
	rpcEnv   string
	safeEnv  string
	gasLimit uint64   // 0 = library default (500k); MegaETH burns ~494k so set 1M
	gasPxMul *big.Int // multiplier (per 100) for SuggestGasPrice; nil = no bump; Arbitrum needs 150 (1.5×)
}

var chains = []chainSpec{
	{"4326", big.NewInt(4326), "MegaETH", "PRIVACY_RPC_MEGAETH", "TEE_SAFE_MEGAETH", 2_500_000, nil},
	{"1", big.NewInt(1), "Ethereum", "PRIVACY_RPC_ETHEREUM", "TEE_SAFE_ETHEREUM", 0, nil},
	{"42161", big.NewInt(42161), "Arbitrum", "PRIVACY_RPC_ARBITRUM", "TEE_SAFE_ARBITRUM", 0, big.NewInt(150)},
	{"10", big.NewInt(10), "Optimism", "PRIVACY_RPC_OPTIMISM", "TEE_SAFE_OPTIMISM", 0, nil},
}

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	_ = godotenv.Load(".env")

	privKey := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if privKey == "" {
		log.Fatal("PRIVATE_KEY not set in .env")
	}

	// Fresh deterministic salt — distinct from whatever was used for the
	// existing tachyon solver Safes. Same value on every chain so all
	// four deployments resolve to the same Safe address.
	saltNonce := new(big.Int).SetBytes(crypto.Keccak256([]byte("tachyon-privacy-tee-v0")))
	fmt.Printf("Using saltNonce: %s\n", saltNonce.Text(16))

	for _, c := range chains {
		fmt.Printf("\n=== %s (chainID %s) ===\n", c.name, c.id)

		if existing := os.Getenv(c.safeEnv); existing != "" {
			fmt.Printf("  skip: %s already set to %s\n", c.safeEnv, existing)
			continue
		}
		rpc := os.Getenv(c.rpcEnv)
		if rpc == "" {
			fmt.Printf("  skip: %s not set\n", c.rpcEnv)
			continue
		}

		client, err := blockchain.NewClient(rpc, "", privKey, c.chainID, c.name, "", nil, "")
		if err != nil {
			fmt.Printf("  client init: %v\n", err)
			continue
		}

		eoa := client.GetSolverAddress()
		fmt.Printf("  EOA: %s\n", eoa.Hex())

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

		opts := blockchain.DeploySafeOpts{GasLimit: c.gasLimit}
		if c.gasPxMul != nil {
			suggested, gerr := client.SuggestGasPrice(ctx)
			if gerr != nil {
				cancel()
				fmt.Printf("  suggest gas price: %v\n", gerr)
				continue
			}
			bumped := new(big.Int).Mul(suggested, c.gasPxMul)
			bumped.Div(bumped, big.NewInt(100))
			opts.GasPrice = bumped
			fmt.Printf("  gas price (bumped): %s wei (suggested %s)\n", bumped.String(), suggested.String())
		}
		tx, predicted, err := client.DeploySafeWithOpts(ctx, eoa, saltNonce, opts)
		cancel()
		if err != nil {
			fmt.Printf("  deploy submit: %v\n", err)
			continue
		}
		fmt.Printf("  predicted Safe: %s\n", predicted.Hex())
		fmt.Printf("  tx hash:        %s\n", tx.Hash().Hex())

		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
		receipt, err := client.WaitForTransaction(ctx2, tx)
		cancel2()
		if err != nil {
			fmt.Printf("  wait: %v\n", err)
			continue
		}
		if receipt.Status != 1 {
			fmt.Printf("  reverted (status=%d, block=%d)\n", receipt.Status, receipt.BlockNumber.Uint64())
			continue
		}
		fmt.Printf("  ✓ deployed at %s (block %d, gasUsed %d)\n",
			predicted.Hex(), receipt.BlockNumber.Uint64(), receipt.GasUsed)
		fmt.Printf("  → set %s=%s in .env\n", c.safeEnv, predicted.Hex())
	}
}
