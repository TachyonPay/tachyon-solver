// deploy-spl-test-tokens — mints two test SPL tokens on a Solana cluster
// (devnet by default), mirroring the Hardhat MockERC20 deploy on the EVM
// side. Outputs a JSON file the bridge_intent program + Go services can
// load to know the canonical test-USDC and a secondary mock mint.
//
// Usage:
//
//	export SOLANA_RPC_URL=https://api.devnet.solana.com
//	export DEPLOYER_KEYPAIR_PATH=$HOME/.config/solana/id.json
//	export INITIAL_MINT_TO=<base58 wallet that should receive an initial 100k of each>
//	go run ./cmd/deploy-spl-test-tokens
//
// The deployer wallet must hold enough SOL to pay rent (~0.003 SOL) and tx
// fees. On devnet you can fund it via `solana airdrop 2 <pubkey>`.
//
// Output: solana/deployments/<cluster>.json
//
//	{
//	  "cluster": "devnet",
//	  "deployer": "...",
//	  "tokens": {
//	    "mockUSDC":      { "mint": "...", "decimals": 6 },
//	    "mockSecondary": { "mint": "...", "decimals": 6 }
//	  }
//	}
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	atok "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/rpc"
	confirm "github.com/gagliardetto/solana-go/rpc/sendAndConfirmTransaction"
	"github.com/gagliardetto/solana-go/rpc/ws"
)

const (
	mockUSDCDecimals      = 6
	mockSecondaryDecimals = 6
	initialMintAmount     = 100_000 * 1_000_000 // 100k tokens at 6 decimals
)

type tokenInfo struct {
	Mint     string `json:"mint"`
	Decimals uint8  `json:"decimals"`
}

type output struct {
	Cluster   string               `json:"cluster"`
	Deployer  string               `json:"deployer"`
	Timestamp string               `json:"timestamp"`
	Tokens    map[string]tokenInfo `json:"tokens"`
}

func main() {
	ctx := context.Background()

	rpcURL := strings.TrimSpace(os.Getenv("SOLANA_RPC_URL"))
	if rpcURL == "" {
		rpcURL = rpc.DevNet_RPC
	}
	wsURL := strings.TrimSpace(os.Getenv("SOLANA_WS_URL"))
	if wsURL == "" {
		wsURL = rpc.DevNet_WS
	}
	cluster := clusterName(rpcURL)

	keypairPath := os.Getenv("DEPLOYER_KEYPAIR_PATH")
	if keypairPath == "" {
		home, _ := os.UserHomeDir()
		keypairPath = filepath.Join(home, ".config", "solana", "id.json")
	}
	deployer, err := solana.PrivateKeyFromSolanaKeygenFile(keypairPath)
	must(err, "load deployer keypair")
	fmt.Printf("[deploy] cluster=%s deployer=%s\n", cluster, deployer.PublicKey())

	cli := rpc.New(rpcURL)
	wsCli, err := ws.Connect(ctx, wsURL)
	must(err, "ws connect")
	defer wsCli.Close()

	// Confirm balance
	bal, err := cli.GetBalance(ctx, deployer.PublicKey(), rpc.CommitmentConfirmed)
	must(err, "getBalance")
	fmt.Printf("[deploy] deployer SOL balance: %d lamports (%.4f SOL)\n",
		bal.Value, float64(bal.Value)/float64(solana.LAMPORTS_PER_SOL))
	if bal.Value < uint64(0.05*float64(solana.LAMPORTS_PER_SOL)) {
		fmt.Println("[deploy] WARNING: deployer balance is low; deploy may fail")
	}

	mockUSDCMint := createMint(ctx, cli, wsCli, deployer, mockUSDCDecimals)
	fmt.Printf("[deploy] mockUSDC mint = %s (decimals=%d)\n", mockUSDCMint, mockUSDCDecimals)

	mockSecondaryMint := createMint(ctx, cli, wsCli, deployer, mockSecondaryDecimals)
	fmt.Printf("[deploy] mockSecondary mint = %s (decimals=%d)\n", mockSecondaryMint, mockSecondaryDecimals)

	if to := strings.TrimSpace(os.Getenv("INITIAL_MINT_TO")); to != "" {
		toPub, err := solana.PublicKeyFromBase58(to)
		must(err, "parse INITIAL_MINT_TO")
		mintInitial(ctx, cli, wsCli, deployer, mockUSDCMint, toPub, initialMintAmount, "mockUSDC")
		mintInitial(ctx, cli, wsCli, deployer, mockSecondaryMint, toPub, initialMintAmount, "mockSecondary")
	} else {
		fmt.Println("[deploy] INITIAL_MINT_TO unset; skipping initial mint")
	}

	out := output{
		Cluster:   cluster,
		Deployer:  deployer.PublicKey().String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Tokens: map[string]tokenInfo{
			"mockUSDC":      {Mint: mockUSDCMint.String(), Decimals: mockUSDCDecimals},
			"mockSecondary": {Mint: mockSecondaryMint.String(), Decimals: mockSecondaryDecimals},
		},
	}

	cwd, err := os.Getwd()
	must(err, "getwd")
	deploymentsDir := filepath.Join(cwd, "..", "..", "..", "solana", "deployments")
	if _, err := os.Stat(deploymentsDir); os.IsNotExist(err) {
		// Fallback: write next to cwd
		deploymentsDir = filepath.Join(cwd, "deployments")
		_ = os.MkdirAll(deploymentsDir, 0o755)
	}
	_ = os.MkdirAll(deploymentsDir, 0o755)
	outPath := filepath.Join(deploymentsDir, cluster+".json")
	buf, _ := json.MarshalIndent(out, "", "  ")
	must(os.WriteFile(outPath, buf, 0o644), "write deployments file")
	fmt.Printf("[deploy] wrote %s\n", outPath)
}

func createMint(
	ctx context.Context,
	cli *rpc.Client,
	wsCli *ws.Client,
	payer solana.PrivateKey,
	decimals uint8,
) solana.PublicKey {
	mint := solana.NewWallet()
	rentLamports, err := cli.GetMinimumBalanceForRentExemption(ctx, token.MINT_SIZE, rpc.CommitmentConfirmed)
	must(err, "rent for mint")

	create := system.NewCreateAccountInstruction(
		rentLamports,
		token.MINT_SIZE,
		token.ProgramID,
		payer.PublicKey(),
		mint.PublicKey(),
	).Build()

	initMint := token.NewInitializeMint2Instruction(
		decimals,
		payer.PublicKey(),
		solana.PublicKey{},
		mint.PublicKey(),
	).Build()

	hash, err := cli.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	must(err, "blockhash")

	tx, err := solana.NewTransaction(
		[]solana.Instruction{create, initMint},
		hash.Value.Blockhash,
		solana.TransactionPayer(payer.PublicKey()),
	)
	must(err, "build mint tx")

	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		switch key {
		case payer.PublicKey():
			return &payer
		case mint.PublicKey():
			pk := mint.PrivateKey
			return &pk
		}
		return nil
	})
	must(err, "sign mint tx")

	_, err = confirm.SendAndConfirmTransaction(ctx, cli, wsCli, tx)
	must(err, "send mint tx")

	return mint.PublicKey()
}

func mintInitial(
	ctx context.Context,
	cli *rpc.Client,
	wsCli *ws.Client,
	payer solana.PrivateKey,
	mint, recipient solana.PublicKey,
	amount uint64,
	label string,
) {
	ata, _, err := solana.FindAssociatedTokenAddress(recipient, mint)
	must(err, "derive ATA")

	createATA := atok.NewCreateInstruction(
		payer.PublicKey(),
		recipient,
		mint,
	).Build()

	mintTo := token.NewMintToInstruction(
		amount,
		mint,
		ata,
		payer.PublicKey(),
		nil,
	).Build()

	hash, err := cli.GetLatestBlockhash(ctx, rpc.CommitmentConfirmed)
	must(err, "blockhash")

	tx, err := solana.NewTransaction(
		[]solana.Instruction{createATA, mintTo},
		hash.Value.Blockhash,
		solana.TransactionPayer(payer.PublicKey()),
	)
	must(err, "build mint-initial tx")

	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key == payer.PublicKey() {
			return &payer
		}
		return nil
	})
	must(err, "sign mint-initial tx")

	sig, err := confirm.SendAndConfirmTransaction(ctx, cli, wsCli, tx)
	must(err, "send mint-initial tx")
	fmt.Printf("[deploy] minted %d of %s to %s (sig=%s)\n", amount, label, recipient, sig)
}

func clusterName(rpcURL string) string {
	switch {
	case strings.Contains(rpcURL, "devnet"):
		return "devnet"
	case strings.Contains(rpcURL, "testnet"):
		return "testnet"
	case strings.Contains(rpcURL, "mainnet"):
		return "mainnet"
	case strings.Contains(rpcURL, "127.0.0.1"), strings.Contains(rpcURL, "localhost"):
		return "localnet"
	default:
		return "custom"
	}
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[deploy] %s: %v\n", ctx, err)
		os.Exit(1)
	}
}
