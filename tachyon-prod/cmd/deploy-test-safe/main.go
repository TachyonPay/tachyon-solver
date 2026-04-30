// deploy-test-safe deploys a 1-of-1 Gnosis Safe on Base mainnet and Horizen
// mainnet, owned by the address derived from PRIVATE_KEY in tachyon-prod/.env.
//
// Usage:
//   cd tachyon-prod && go run ./cmd/deploy-test-safe/
//
// Prints the deployed Safe addresses on each chain, ready to paste into
// CHAIN_3_SAFE_ADDRESS / CHAIN_4_SAFE_ADDRESS for local Safe-mode rebalancer
// tests.
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

type chainSpec struct {
	name    string
	chainID uint64
	rpc     string
}

var chains = []chainSpec{
	// Base mainnet was deployed at 0xa4A8E2dDA54321E257615fef9FF467401D1C17D4 in a
	// previous run; uncomment to redeploy.
	// {name: "base_mainnet", chainID: 8453, rpc: "https://base-mainnet.g.alchemy.com/v2/ZnxNrK14O1olpqoj-KVkP"},
	{
		name:    "horizen_mainnet",
		chainID: 26514,
		rpc:     "https://horizen.calderachain.xyz/http",
	},
}

func main() {
	if err := godotenv.Load(".env"); err != nil {
		// Not fatal; PRIVATE_KEY might be exported directly.
		fmt.Printf("warn: no .env loaded: %v\n", err)
	}

	pkHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pkHex == "" {
		fatal("PRIVATE_KEY not set (read from tachyon-prod/.env or environment)")
	}
	priv, err := crypto.HexToECDSA(pkHex)
	if err != nil {
		fatal(fmt.Sprintf("invalid PRIVATE_KEY: %v", err))
	}
	owner := crypto.PubkeyToAddress(priv.PublicKey)

	saltNonce := big.NewInt(time.Now().Unix())

	fmt.Printf("\n=== Deploy 1-of-1 Safe ===\n")
	fmt.Printf("Owner (PRIVATE_KEY address): %s\n", owner.Hex())
	fmt.Printf("Salt nonce (unix time):      %s\n\n", saltNonce.String())

	ctx := context.Background()
	results := make(map[string]string, len(chains))

	for _, ch := range chains {
		fmt.Printf("--- %s (chainID %d) ---\n", ch.name, ch.chainID)

		// blockchain.NewClient requires a bridge contract address; we don't
		// actually use it for Safe deployment, so we pass a placeholder.
		client, err := blockchain.NewClient(
			ch.rpc,
			"0x0000000000000000000000000000000000000001", // dummy bridge addr
			pkHex,
			new(big.Int).SetUint64(ch.chainID),
			ch.name,
			"",  // no Safe yet
			nil, // no Safe signing keys
			"",  // no multicall3
		)
		if err != nil {
			fatal(fmt.Sprintf("%s: client init: %v", ch.name, err))
		}
		defer client.Close()

		// Predict (just for logging — DeploySafe also predicts internally).
		predicted, err := blockchain.PredictSafeAddressForOwnerOnChain(owner, ch.chainID)
		if err != nil {
			fmt.Printf("  predict: %v (continuing)\n", err)
		} else {
			fmt.Printf("  predicted (saltNonce=0):  %s  (note: not used; we use unique salt below)\n", predicted.Hex())
		}

		fmt.Printf("  deploying with saltNonce %s...\n", saltNonce.String())
		tx, addr, err := client.DeploySafe(ctx, owner, saltNonce)
		if err != nil {
			fatal(fmt.Sprintf("%s: deploy: %v", ch.name, err))
		}
		fmt.Printf("  tx hash:   %s\n", tx.Hash().Hex())
		fmt.Printf("  predicted: %s\n", addr.Hex())

		// Wait for inclusion + verify.
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		rcpt, err := bind.WaitMined(waitCtx, client.EthClient(), tx)
		cancel()
		if err != nil {
			fatal(fmt.Sprintf("%s: wait mined: %v", ch.name, err))
		}
		if rcpt.Status != 1 {
			fatal(fmt.Sprintf("%s: deploy tx reverted (block %d)", ch.name, rcpt.BlockNumber.Uint64()))
		}

		// Confirm code at the predicted address. Retry to absorb RPC state
		// propagation lag — the receipt can land before `latest` reflects it
		// on some endpoints.
		var code []byte
		for attempt := 1; attempt <= 6; attempt++ {
			code, err = client.EthClient().CodeAt(ctx, addr, nil)
			if err == nil && len(code) > 0 {
				break
			}
			fmt.Printf("  code probe attempt %d: len=%d err=%v — retrying\n", attempt, len(code), err)
			time.Sleep(3 * time.Second)
		}
		if len(code) == 0 {
			fatal(fmt.Sprintf("%s: no code at predicted address %s after retries — deploy mismatch", ch.name, addr.Hex()))
		}

		fmt.Printf("  confirmed: %d code bytes at %s (block %d)\n\n",
			len(code), addr.Hex(), rcpt.BlockNumber.Uint64())
		results[ch.name] = addr.Hex()
	}

	fmt.Printf("=== Done — paste into tachyon-prod/.env ===\n\n")
	fmt.Printf("CHAIN_3_SAFE_ADDRESS=%s   # Horizen mainnet\n", results["horizen_mainnet"])
	fmt.Printf("CHAIN_4_SAFE_ADDRESS=%s   # Base mainnet\n", results["base_mainnet"])
	fmt.Printf("SAFE_SIGNER_KEY_1=%s\n", pkHex)
	fmt.Printf("# (leave SAFE_SIGNER_KEY_2 / SAFE_SIGNER_KEY_3 unset — 1-of-1 Safe)\n")
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s\n", msg)
	os.Exit(1)
}
