// topup-solver pulls USDC from the main treasury Safe into the solver Safe on
// Base by invoking AllowanceModule.executeAllowanceTransfer. The solver Safe is
// the configured delegate, so signature == "" works (the AllowanceModule's
// `msg.sender == expectedDelegate` shortcut covers contract delegates).
//
// Flow per call:
//   1. Build calldata for executeAllowanceTransfer(mainSafe, USDC, solverSafe, amount, 0, 0, solverSafe, "")
//   2. Wrap as a Safe execTransaction (to=AllowanceModule, value=0, data=above, op=0)
//   3. Sign locally with SAFE_SIGNER_KEY_1
//   4. POST to RELAYER_URL/recipient/relay-proxy — relayer pays outer gas
//   5. Wait for receipt
//
// Usage:
//   cd tachyon-prod && go run ./cmd/topup-solver -amount 100        # 100 USDC
//   go run ./cmd/topup-solver -amount 5000                          # full 5k
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

const (
	allowanceModuleAddr = "0xCFbFaC74C26F8647cBDb8c5caf80BB5b32E43134"
	mainSafeAddr        = "0xBa09cE9986f3BE2E553f155F02987122C84Fae63"
	usdcBaseAddr        = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	baseChainID         = "8453"
)

const allowanceModuleABI = `[{
  "inputs": [
    {"name":"safe","type":"address"},
    {"name":"token","type":"address"},
    {"name":"to","type":"address"},
    {"name":"amount","type":"uint96"},
    {"name":"paymentToken","type":"address"},
    {"name":"payment","type":"uint96"},
    {"name":"delegate","type":"address"},
    {"name":"signature","type":"bytes"}
  ],
  "name":"executeAllowanceTransfer",
  "outputs":[],
  "stateMutability":"nonpayable",
  "type":"function"
}]`

const erc20ABI = `[{
  "inputs":[{"name":"account","type":"address"}],
  "name":"balanceOf",
  "outputs":[{"name":"","type":"uint256"}],
  "stateMutability":"view","type":"function"
}]`

type relayProxyRequest struct {
	SafeAddress string `json:"safeAddress"`
	ChainID     string `json:"chainId"`
	To          string `json:"to"`
	Value       string `json:"value,omitempty"`
	Data        string `json:"data,omitempty"`
	Operation   uint8  `json:"operation"`
	Signature   string `json:"signature"`
}

type relayProxyResponse struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash"`
	Error   string `json:"error"`
}

func main() {
	amountFlag := flag.Float64("amount", 100, "USDC amount to pull (whole-token units, e.g. 100 = 100 USDC)")
	flag.Parse()

	if err := godotenv.Load(".env"); err != nil {
		fmt.Printf("warn: no .env loaded: %v\n", err)
	}

	rpc := envOr("CHAIN_2_RPC", os.Getenv("PRIVACY_RPC_BASE"))
	if rpc == "" {
		fatal("CHAIN_2_RPC / PRIVACY_RPC_BASE not set")
	}
	signerHex := strings.TrimPrefix(envOr("SAFE_SIGNER_KEY_1", os.Getenv("PRIVATE_KEY")), "0x")
	if signerHex == "" {
		fatal("SAFE_SIGNER_KEY_1 / PRIVATE_KEY not set")
	}
	relayerURL := strings.TrimRight(envOr("RELAYER_URL", "http://localhost:3000"), "/")

	signerKey, err := crypto.HexToECDSA(signerHex)
	if err != nil {
		fatal(fmt.Sprintf("invalid signer key: %v", err))
	}

	solverSafe := common.HexToAddress("0xa4A8E2dDA54321E257615fef9FF467401D1C17D4")
	mainSafe := common.HexToAddress(mainSafeAddr)
	mod := common.HexToAddress(allowanceModuleAddr)
	usdc := common.HexToAddress(usdcBaseAddr)

	// 100 USDC = 100 * 10^6.
	amountRaw := new(big.Int).SetUint64(uint64(*amountFlag * 1_000_000))
	if amountRaw.Sign() <= 0 {
		fatal("amount must be > 0")
	}

	ctx := context.Background()

	// Build a blockchain.Client for Base. The Safe address it tracks must be
	// the solver Safe (the delegate); SAFE_SIGNER_KEY_1 signs the SafeTxHash.
	client, err := blockchain.NewClient(
		rpc,
		"0x0000000000000000000000000000000000000000", // contractAddress unused here
		signerHex,                                    // EOA private key (also drives Safe signer slot 1)
		big.NewInt(8453),
		"base",
		solverSafe.Hex(),
		[]*ecdsa.PrivateKey{signerKey},
		"0xcA11bde05977b3631167028862bE2a173976CA11",
	)
	if err != nil {
		fatal(fmt.Sprintf("blockchain.NewClient: %v", err))
	}

	eth, err := ethclient.Dial(rpc)
	if err != nil {
		fatal(fmt.Sprintf("dial RPC: %v", err))
	}

	mainBefore := mustBal(ctx, eth, usdc, mainSafe)
	solverBefore := mustBal(ctx, eth, usdc, solverSafe)
	fmt.Printf("Pre-tx balances:\n")
	fmt.Printf("  main Safe   %s : %s USDC\n", mainSafe.Hex(), formatUSDC(mainBefore))
	fmt.Printf("  solver Safe %s : %s USDC\n", solverSafe.Hex(), formatUSDC(solverBefore))
	fmt.Printf("  pulling     : %s USDC\n\n", formatUSDC(amountRaw))

	// Encode AllowanceModule.executeAllowanceTransfer call.
	modABI, err := abi.JSON(strings.NewReader(allowanceModuleABI))
	if err != nil {
		fatal(fmt.Sprintf("parse module ABI: %v", err))
	}
	innerData, err := modABI.Pack(
		"executeAllowanceTransfer",
		mainSafe,        // safe (the one being debited)
		usdc,            // token
		solverSafe,      // to (recipient)
		amountRaw,       // amount (uint96)
		common.Address{}, // paymentToken
		new(big.Int),    // payment
		solverSafe,      // delegate (msg.sender check)
		[]byte{},        // signature — empty triggers msg.sender == delegate path
	)
	if err != nil {
		fatal(fmt.Sprintf("pack allowance call: %v", err))
	}

	// Sign Safe execTransaction (to=AllowanceModule, value=0, data=innerData, op=0).
	sig, err := client.SignSafeTransaction(ctx, mod, big.NewInt(0), innerData, 0)
	if err != nil {
		fatal(fmt.Sprintf("SignSafeTransaction: %v", err))
	}

	body, _ := json.Marshal(relayProxyRequest{
		SafeAddress: solverSafe.Hex(),
		ChainID:     baseChainID,
		To:          mod.Hex(),
		Value:       "0",
		Data:        "0x" + hex.EncodeToString(innerData),
		Operation:   0,
		Signature:   "0x" + hex.EncodeToString(sig),
	})

	url := relayerURL + "/recipient/relay-proxy"
	fmt.Printf("Submitting Safe execTransaction → %s\n", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fatal(fmt.Sprintf("build request: %v", err))
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		fatal(fmt.Sprintf("relay-proxy request: %v", err))
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var parsed relayProxyResponse
	if err := json.Unmarshal(rb, &parsed); err != nil {
		fatal(fmt.Sprintf("parse response (%d): %s", resp.StatusCode, string(rb)))
	}
	if !parsed.Success || parsed.TxHash == "" {
		fatal(fmt.Sprintf("relay-proxy refused (%d): %s", resp.StatusCode, parsed.Error))
	}
	fmt.Printf("submitted tx: %s\n", parsed.TxHash)

	// Wait for confirmation.
	hash := common.HexToHash(parsed.TxHash)
	var lastErr error
	for attempt := 0; attempt < 40; attempt++ {
		tx, _, err := eth.TransactionByHash(ctx, hash)
		if err == nil && tx != nil {
			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			rcpt, werr := bind.WaitMined(waitCtx, eth, tx)
			cancel()
			if werr != nil {
				fatal(fmt.Sprintf("wait mined: %v", werr))
			}
			if rcpt.Status != 1 {
				fatal(fmt.Sprintf("tx %s reverted (block %d)", hash.Hex(), rcpt.BlockNumber.Uint64()))
			}
			fmt.Printf("confirmed in block %d, gas used %d\n\n", rcpt.BlockNumber.Uint64(), rcpt.GasUsed)
			break
		}
		lastErr = err
		select {
		case <-ctx.Done():
			fatal("ctx canceled before tx propagated")
		case <-time.After(2 * time.Second):
		}
	}
	if lastErr != nil && lastErr.Error() != "" {
		// not fatal — just visibility if propagation was slow
		fmt.Printf("(last lookup error during propagation wait: %v)\n", lastErr)
	}

	mainAfter := mustBal(ctx, eth, usdc, mainSafe)
	solverAfter := mustBal(ctx, eth, usdc, solverSafe)
	fmt.Printf("Post-tx balances:\n")
	fmt.Printf("  main Safe   : %s USDC  (Δ %s)\n", formatUSDC(mainAfter), deltaSigned(mainBefore, mainAfter))
	fmt.Printf("  solver Safe : %s USDC  (Δ %s)\n", formatUSDC(solverAfter), deltaSigned(solverBefore, solverAfter))
}

func mustBal(ctx context.Context, eth *ethclient.Client, token, holder common.Address) *big.Int {
	tokABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		fatal(fmt.Sprintf("parse erc20 ABI: %v", err))
	}
	c := bind.NewBoundContract(token, tokABI, eth, eth, eth)
	var out []interface{}
	if err := c.Call(&bind.CallOpts{Context: ctx}, &out, "balanceOf", holder); err != nil {
		fatal(fmt.Sprintf("balanceOf %s @ %s: %v", holder.Hex(), token.Hex(), err))
	}
	return out[0].(*big.Int)
}

func formatUSDC(raw *big.Int) string {
	if raw == nil {
		return "0.000000"
	}
	q, r := new(big.Int).QuoRem(raw, big.NewInt(1_000_000), new(big.Int))
	return fmt.Sprintf("%s.%06d", q.String(), r.Int64())
}

func deltaSigned(before, after *big.Int) string {
	d := new(big.Int).Sub(after, before)
	sign := ""
	if d.Sign() > 0 {
		sign = "+"
	}
	return sign + formatUSDC(d)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s\n", msg)
	os.Exit(1)
}
