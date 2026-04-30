// safe-transfer-out moves a small amount of tokens from each Safe back to
// the EOA (PRIVATE_KEY's address) via the relayer's RelayProxy. Used as
// pre-test setup to give the EOA enough tokenA + reward to create intents
// that the Safe-wired solver will then bid on.
//
// Usage:
//   cd tachyon-prod && go run ./cmd/safe-transfer-out/
package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/rebalancer"
)

type chainSpec struct {
	chainID  string
	rpc      string
	bridge   string
	safeAddr string
	transfers []transferSpec
}

type transferSpec struct {
	tokenName string
	tokenAddr string
	amount    string // raw units
}

var chains = []chainSpec{
	{
		chainID:  rebalancer.ChainIDBase,
		rpc:      "https://base-mainnet.g.alchemy.com/v2/ZnxNrK14O1olpqoj-KVkP",
		bridge:   "0x52F77B624a9f20d41a60eF550e5792a00747262c",
		safeAddr: "0xa4A8E2dDA54321E257615fef9FF467401D1C17D4",
		transfers: []transferSpec{
			{tokenName: "USDC", tokenAddr: "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", amount: "200000"}, // 0.2 USDC
		},
	},
	{
		chainID:  rebalancer.ChainIDHorizen,
		rpc:      "https://horizen.calderachain.xyz/http",
		bridge:   "0x1809703DE921177cca0f1c80FB58D788E09AdDB4",
		safeAddr: "0x71ca2818D92123dfC7072486F73aDBF5F1E28974",
		transfers: []transferSpec{
			{tokenName: "USDC.e", tokenAddr: "0xDF7108f8B10F9b9eC1aba01CCa057268cbf86B6c", amount: "500000"}, // 0.5 USDC.e
		},
	},
}

// ERC20.transfer selector
var transferSel = []byte{0xa9, 0x05, 0x9c, 0xbb}

func main() {
	if err := godotenv.Load(".env"); err != nil {
		fmt.Printf("warn: no .env loaded: %v\n", err)
	}

	pkHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pkHex == "" {
		fatal("PRIVATE_KEY not set")
	}
	priv, err := crypto.HexToECDSA(pkHex)
	if err != nil {
		fatal(fmt.Sprintf("invalid PRIVATE_KEY: %v", err))
	}
	eoa := crypto.PubkeyToAddress(priv.PublicKey)

	// Need at least one Safe signing key for the SafeSigner.
	signerKeyHex := strings.TrimPrefix(os.Getenv("SAFE_SIGNER_KEY_1"), "0x")
	if signerKeyHex == "" {
		fatal("SAFE_SIGNER_KEY_1 not set")
	}
	signerKey, err := crypto.HexToECDSA(signerKeyHex)
	if err != nil {
		fatal(fmt.Sprintf("invalid SAFE_SIGNER_KEY_1: %v", err))
	}

	ctx := context.Background()
	clients := make(map[string]*blockchain.Client)
	for _, ch := range chains {
		idU64, err := strconv.ParseUint(ch.chainID, 10, 64)
		if err != nil {
			fatal(fmt.Sprintf("bad chainID %s: %v", ch.chainID, err))
		}
		client, err := blockchain.NewClient(
			ch.rpc,
			ch.bridge,
			pkHex,
			new(big.Int).SetUint64(idU64),
			"chain-"+ch.chainID,
			ch.safeAddr,
			[]*ecdsa.PrivateKey{signerKey},
			"",
		)
		if err != nil {
			fatal(fmt.Sprintf("client init for %s: %v", ch.chainID, err))
		}
		defer client.Close()
		clients[ch.chainID] = client
	}

	signer, err := rebalancer.NewSafeSignerFromClients(clients)
	if err != nil {
		fatal(fmt.Sprintf("safe signer init: %v", err))
	}
	if signer == nil {
		fatal("safe signer is nil")
	}

	fmt.Printf("EOA: %s\n", eoa.Hex())
	fmt.Printf("Relayer URL: %s\n\n", rebalancer.RelayerURL())

	for _, ch := range chains {
		client := clients[ch.chainID]
		fmt.Printf("=== chain %s (Safe %s) ===\n", ch.chainID, ch.safeAddr)
		for _, t := range ch.transfers {
			amt, _ := new(big.Int).SetString(t.amount, 10)
			tokenAddr := common.HexToAddress(t.tokenAddr)

			fmt.Printf("  %s: transferring %s raw → EOA %s\n", t.tokenName, amt.String(), eoa.Hex())

			data := append([]byte{}, transferSel...)
			data = append(data, common.LeftPadBytes(eoa.Bytes(), 32)...)
			data = append(data, common.LeftPadBytes(amt.Bytes(), 32)...)

			tx, err := signer.SendTx(ctx, ch.chainID, tokenAddr, big.NewInt(0), data, 100_000)
			if err != nil {
				fatal(fmt.Sprintf("    %s send: %v", t.tokenName, err))
			}
			fmt.Printf("    relayer accepted, tx %s — waiting...\n", tx.Hash().Hex())

			waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			rcpt, err := bind.WaitMined(waitCtx, client.EthClient(), tx)
			cancel()
			if err != nil {
				fatal(fmt.Sprintf("    %s wait: %v", t.tokenName, err))
			}
			if rcpt.Status != 1 {
				fatal(fmt.Sprintf("    %s tx reverted (%s)", t.tokenName, tx.Hash().Hex()))
			}
			fmt.Printf("    confirmed at block %d\n", rcpt.BlockNumber.Uint64())
		}
		fmt.Println()
	}

	fmt.Println("Done.")
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s\n", msg)
	os.Exit(1)
}
