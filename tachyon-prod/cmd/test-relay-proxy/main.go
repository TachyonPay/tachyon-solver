// test-relay-proxy: tests the full flow:
//   register → generate-stealth → deploy-safe → derive-stealth-key
//   → sign Safe tx hash → relay-proxy (mint tokens via Safe)
//
// Usage: RELAYER_URL=http://localhost:3000 go run ./cmd/test-relay-proxy/
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	pkgtypes "github.com/zk-tachyon/tachyon-prod/pkg/types"
)

const safeHashABI = `[
  {"inputs":[],"name":"nonce","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"value","type":"uint256"},{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"uint8","name":"operation","type":"uint8"},{"internalType":"uint256","name":"safeTxGas","type":"uint256"},{"internalType":"uint256","name":"baseGas","type":"uint256"},{"internalType":"uint256","name":"gasPrice","type":"uint256"},{"internalType":"address","name":"gasToken","type":"address"},{"internalType":"address","name":"refundReceiver","type":"address"},{"internalType":"uint256","name":"_nonce","type":"uint256"}],"name":"getTransactionHash","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"}
]`

const erc20ABI = `[
  {"inputs":[{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"mint","outputs":[],"stateMutability":"nonpayable","type":"function"},
  {"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}
]`

var (
	chainID   = "84532"
	rpcURL    = "https://sepolia.base.org"
	tokenAddr = common.HexToAddress("0x39b2DfD00D262B7A8d83468C244901c2c1252256") // USDC Base Sepolia
	// mint 1 USDC (6 decimals)
	mintAmount = new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil)
)

func main() {
	relayerURL := os.Getenv("RELAYER_URL")
	if relayerURL == "" {
		relayerURL = "http://localhost:3000"
	}

	privateKeyHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if privateKeyHex == "" {
		privateKeyHex = "1dd0046b5493fff5b112fc1efea1d309d35e04eee398f851ac9f9a2434658f22"
	}

	privKey, err := crypto.HexToECDSA(privateKeyHex)
	must(err, "parse private key")
	recipientAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ethClient, err := ethclient.DialContext(ctx, rpcURL)
	must(err, "dial rpc")
	defer ethClient.Close()

	safeABIParsed, err := abi.JSON(strings.NewReader(safeHashABI))
	must(err, "parse safe abi")
	erc20ABIParsed, err := abi.JSON(strings.NewReader(erc20ABI))
	must(err, "parse erc20 abi")

	fmt.Printf("👤 Recipient: %s\n", recipientAddr.Hex())
	fmt.Printf("🌐 Relayer:   %s\n\n", relayerURL)

	// ── Step 1: Register ──────────────────────────────────────────────────────
	fmt.Println("[1] Registering recipient...")
	regSig, err := stealth.SignRegisterStealth(privKey, recipientAddr)
	must(err, "sign register")
	regResp := &pkgtypes.RegisterRecipientResponse{}
	must(postJSON(ctx, relayerURL+"/recipient/register", pkgtypes.RegisterRecipientRequest{
		RecipientAddress: recipientAddr.Hex(),
		Signature:        "0x" + hex.EncodeToString(regSig),
	}, regResp), "register")
	if !regResp.Success {
		fatalf("register failed: %s", regResp.Error)
	}
	fmt.Printf("    SpendPubKey: %s...\n", regResp.SpendPubKey[:20])

	// ── Step 2: Generate stealth address ─────────────────────────────────────
	fmt.Println("\n[2] Generating stealth address...")
	genResp := &pkgtypes.GenerateStealthResponse{}
	must(postJSON(ctx, relayerURL+"/recipient/generate-stealth", pkgtypes.GenerateStealthRequest{
		RecipientAddress: recipientAddr.Hex(),
		ChainID:          chainID,
		IntentID:         fmt.Sprintf("test-relay-%d", time.Now().Unix()),
	}, genResp), "generate-stealth")
	stealthAddr := common.HexToAddress(genResp.StealthAddress)
	safeAddr := common.HexToAddress(genResp.SafeAddress)
	fmt.Printf("    StealthAddr: %s\n", stealthAddr.Hex())
	fmt.Printf("    SafeAddr:    %s\n", safeAddr.Hex())

	// ── Step 3: Derive stealth key ────────────────────────────────────────────
	fmt.Println("\n[3] Deriving stealth key...")
	deriveSig, _ := stealth.SignDeriveStealthKey(privKey, recipientAddr, stealthAddr)
	deriveResp := &pkgtypes.DeriveStealthKeyResponse{}
	must(postJSON(ctx, relayerURL+"/recipient/derive-stealth-key", pkgtypes.DeriveStealthKeyRequest{
		RecipientAddress: recipientAddr.Hex(),
		StealthAddress:   stealthAddr.Hex(),
		Signature:        "0x" + hex.EncodeToString(deriveSig),
	}, deriveResp), "derive-stealth-key")
	if !deriveResp.Success {
		fatalf("derive-stealth-key failed: %s", deriveResp.Error)
	}
	stealthPrivKey, err := crypto.HexToECDSA(strings.TrimPrefix(deriveResp.StealthPrivateKey, "0x"))
	must(err, "parse stealth key")
	fmt.Printf("    ✅ Stealth key derived\n")

	// ── Step 4: Deploy Safe ───────────────────────────────────────────────────
	fmt.Println("\n[4] Deploying Safe...")
	chainIDBig, _ := new(big.Int).SetString(chainID, 10)
	deploySig, _ := stealth.SignDeploySafe(stealthPrivKey, stealthAddr, chainIDBig)
	deployResp := &pkgtypes.DeploySafeResponse{}
	must(postJSON(ctx, relayerURL+"/recipient/deploy-safe", pkgtypes.DeploySafeRequest{
		StealthAddress: stealthAddr.Hex(),
		ChainID:        chainID,
		Signature:      "0x" + hex.EncodeToString(deploySig),
	}, deployResp), "deploy-safe")
	if !deployResp.Success {
		fatalf("deploy-safe failed: %s", deployResp.Error)
	}
	fmt.Printf("    TxHash: %s\n", deployResp.TxHash)
	fmt.Printf("    Safe:   %s\n", deployResp.SafeAddress)

	// Wait for Safe deployment to be confirmed on-chain
	fmt.Println("    Waiting for Safe deployment confirmation...")
	for i := 0; i < 15; i++ {
		time.Sleep(3 * time.Second)
		code, err := ethClient.CodeAt(ctx, safeAddr, nil)
		if err == nil && len(code) > 0 {
			fmt.Printf("    ✅ Safe deployed (%d bytes)\n", len(code))
			break
		}
		if i == 14 {
			fatalf("Safe not deployed after timeout")
		}
	}

	// ── Step 5: Build mint calldata ───────────────────────────────────────────
	fmt.Println("\n[5] Building mint calldata...")
	mintData, err := erc20ABIParsed.Pack("mint", safeAddr, mintAmount)
	must(err, "pack mint")
	fmt.Printf("    Minting %s raw USDC to Safe\n", mintAmount.String())

	// ── Step 6: Get Safe nonce + tx hash ─────────────────────────────────────
	fmt.Println("\n[6] Fetching Safe nonce + tx hash...")
	safeContract := bind.NewBoundContract(safeAddr, safeABIParsed, ethClient, ethClient, ethClient)

	var nonceResult []interface{}
	must(safeContract.Call(&bind.CallOpts{Context: ctx}, &nonceResult, "nonce"), "get safe nonce")
	safeNonce := nonceResult[0].(*big.Int)
	fmt.Printf("    Safe nonce: %s\n", safeNonce.String())

	var hashResult []interface{}
	must(safeContract.Call(&bind.CallOpts{Context: ctx}, &hashResult, "getTransactionHash",
		tokenAddr, big.NewInt(0), mintData, uint8(0),
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		safeNonce,
	), "getTransactionHash")
	safeTxHash := hashResult[0].([32]byte)
	fmt.Printf("    SafeTxHash: 0x%x\n", safeTxHash)

	// ── Step 7: Sign Safe tx hash with stealth key ────────────────────────────
	fmt.Println("\n[7] Signing Safe tx hash with stealth key...")
	sig, err := crypto.Sign(safeTxHash[:], stealthPrivKey)
	must(err, "sign safe tx hash")
	sig[64] += 27 // Safe expects v = 27 or 28
	fmt.Printf("    Sig: 0x%x\n", sig)

	// ── Step 8: Relay via relayer ─────────────────────────────────────────────
	fmt.Println("\n[8] Relaying via POST /recipient/relay-proxy...")
	relayResp := &pkgtypes.RelayProxyResponse{}
	must(postJSON(ctx, relayerURL+"/recipient/relay-proxy", pkgtypes.RelayProxyRequest{
		SafeAddress: safeAddr.Hex(),
		ChainID:     chainID,
		To:          tokenAddr.Hex(),
		Value:       "0",
		Data:        "0x" + hex.EncodeToString(mintData),
		Operation:   0,
		Signature:   "0x" + hex.EncodeToString(sig),
	}, relayResp), "relay-proxy")
	if !relayResp.Success {
		fatalf("relay-proxy failed: %s", relayResp.Error)
	}
	fmt.Printf("    TxHash: %s\n", relayResp.TxHash)

	// ── Step 9: Verify balance ────────────────────────────────────────────────
	fmt.Println("\n[9] Verifying token balance (waiting for confirmation)...")
	erc20Contract := bind.NewBoundContract(tokenAddr, erc20ABIParsed, ethClient, ethClient, ethClient)
	var bal *big.Int
	for i := 0; i < 10; i++ {
		time.Sleep(3 * time.Second)
		var balResult []interface{}
		if err := erc20Contract.Call(&bind.CallOpts{Context: ctx}, &balResult, "balanceOf", safeAddr); err == nil {
			bal = balResult[0].(*big.Int)
			if bal.Cmp(big.NewInt(0)) > 0 {
				break
			}
		}
	}
	fmt.Printf("    Safe USDC balance: %s\n", bal.String())
	if bal != nil && bal.Cmp(mintAmount) >= 0 {
		fmt.Println("\n✅ PASSED — Safe received minted tokens via relay-proxy")
	} else {
		fmt.Println("\n❌ FAILED — balance not updated")
		os.Exit(1)
	}
}

func postJSON(ctx context.Context, url string, body interface{}, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal %s: %v\n", msg, err)
		os.Exit(1)
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
