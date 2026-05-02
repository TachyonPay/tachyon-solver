// Privacy bridge e2e: Base USDC → MegaETH USDT0 via the privacy-router.
//
// 1. GET /pubkey — the service's ECIES public key
// 2. Build a BridgeIntentPayload (Base USDC → MegaETH USDT0, recipient EOA)
// 3. ECIES-encrypt + POST /bridge → sessionID
// 4. Send the source USDC from PRIVATE_KEY's EOA to TEE_SAFE_BASE on chain
// 5. Poll /bridge/:id every 8s, print status transitions
//
// Run from tachyon-prod root:
//
//	go run ./test/e2e/privacy_flow
//
// Env (loaded from ../../.env):
//
//	PRIVACY_URL            base URL of the privacy-router (default http://localhost:3013)
//	PRIVATE_KEY            EOA used as the "user" — funds the bridge
//	PRIVACY_TEE_SAFE_BASE  TEE Safe address on Base (default = current solver Safe)
//	PRIVACY_TEST_AMOUNT    amount in USDC atomic units (default 50000 = $0.05)
//	PRIVACY_TEST_DEST      dest address on MegaETH (default = EOA)
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

const (
	defaultPrivacyURL  = "http://localhost:3013"
	defaultTeeSafeBase = "0xa4A8E2dDA54321E257615fef9FF467401D1C17D4"
	baseUSDC           = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	baseRPC            = "https://base-mainnet.g.alchemy.com/v2/ZnxNrK14O1olpqoj-KVkP"
	chainBase          = "8453"
	chainMegaETH       = "4326"
)

type bridgeIntentPayload struct {
	SessionID       string `json:"sessionId"`
	UserAddress     string `json:"userAddress"`
	SourceChainID   string `json:"sourceChainId"`
	SourceToken     string `json:"sourceToken"`
	InboundSafe     string `json:"inboundSafe"`
	AmountStr       string `json:"amount"`
	DestChainID     string `json:"destChainId"`
	DestToken       string `json:"destToken"`
	DestStealthSafe string `json:"destStealthSafe"`
	UnixTimestamp   int64  `json:"unixTimestamp"`
}

type submitResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"sessionId"`
	Error     string `json:"error"`
}

type statusResponse struct {
	Success         bool   `json:"success"`
	Status          string `json:"status"`
	SweepTxHash     string `json:"sweepTxHash"`
	BridgeTxHash    string `json:"bridgeTxHash"`
	DeliveredTxHash string `json:"deliveredTxHash"`
	Error           string `json:"error"`
}

func main() {
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load("../../../.env")

	privacyURL := envOr("PRIVACY_URL", defaultPrivacyURL)
	teeSafeBase := common.HexToAddress(envOr("PRIVACY_TEE_SAFE_BASE", defaultTeeSafeBase))
	pkHex := strings.TrimPrefix(os.Getenv("PRIVATE_KEY"), "0x")
	if pkHex == "" {
		die("PRIVATE_KEY required")
	}
	priv, err := crypto.HexToECDSA(pkHex)
	if err != nil {
		die("PRIVATE_KEY parse: %v", err)
	}
	eoa := crypto.PubkeyToAddress(priv.PublicKey)

	// Generate a fresh stealth keypair and compute its predicted Safe
	// address on the destination chain (CREATE2, undeployed). Funds
	// land at this address; the user can deploy + sweep later using
	// the stealth key. Override with PRIVACY_TEST_DEST to bypass.
	stealthKey, err := crypto.GenerateKey()
	if err != nil {
		die("generate stealth key: %v", err)
	}
	stealthOwner := crypto.PubkeyToAddress(stealthKey.PublicKey)
	predictedSafe, err := blockchain.PredictSafeAddressForOwner(stealthOwner)
	if err != nil {
		die("predict safe: %v", err)
	}
	dest := predictedSafe
	if v := os.Getenv("PRIVACY_TEST_DEST"); v != "" {
		dest = common.HexToAddress(v)
	}

	amountStr := envOr("PRIVACY_TEST_AMOUNT", "50000") // 0.05 USDC
	amount, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		die("PRIVACY_TEST_AMOUNT %q is not a number", amountStr)
	}

	fmt.Println("Privacy bridge e2e (Base USDC → MegaETH USDT0)")
	fmt.Printf("  router:           %s\n", privacyURL)
	fmt.Printf("  user EOA:         %s\n", eoa.Hex())
	fmt.Printf("  TEE Safe Base:    %s\n", teeSafeBase.Hex())
	fmt.Printf("  amount:           %s atomic ($%s USDC)\n", amount, formatTokenAmount(amount, 6))
	fmt.Printf("  stealth owner:    %s  (private key NOT persisted)\n", stealthOwner.Hex())
	fmt.Printf("  predicted Safe:   %s  (canonical v1.3.0, undeployed)\n", predictedSafe.Hex())
	fmt.Printf("  dest (MegaETH):   %s\n\n", dest.Hex())

	fmt.Println("[1] fetching service pubkey...")
	pubHex, err := fetchPubKey(privacyURL)
	if err != nil {
		die("fetchPubKey: %v", err)
	}
	pubKey, err := ecies.NewPublicKeyFromHex(strings.TrimPrefix(pubHex, "0x"))
	if err != nil {
		die("parse pubkey: %v", err)
	}
	fmt.Printf("    pubkey = %s...%s\n\n", pubHex[:18], pubHex[len(pubHex)-8:])

	sessionID := "privacy-test-" + uuid.NewString()
	payload := bridgeIntentPayload{
		SessionID:       sessionID,
		UserAddress:     eoa.Hex(),
		SourceChainID:   chainBase,
		SourceToken:     "USDC",
		InboundSafe:     eoa.Hex(),
		AmountStr:       amount.String(),
		DestChainID:     chainMegaETH,
		DestToken:       "USDT0",
		DestStealthSafe: dest.Hex(),
		UnixTimestamp:   time.Now().Unix(),
	}
	clear, _ := json.Marshal(payload)

	fmt.Println("[2] encrypting + POST /bridge...")
	cipher, err := ecies.Encrypt(pubKey, clear)
	if err != nil {
		die("ecies encrypt: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"encrypted": "0x" + hex.EncodeToString(cipher)})
	resp, err := http.Post(privacyURL+"/bridge", "application/json", bytes.NewReader(body))
	if err != nil {
		die("/bridge POST: %v", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var sub submitResponse
	if err := json.Unmarshal(rb, &sub); err != nil {
		die("submit decode: %v body=%s", err, string(rb))
	}
	if !sub.Success {
		die("submit failed: %s body=%s", sub.Error, string(rb))
	}
	fmt.Printf("    sessionID = %s\n\n", sub.SessionID)

	fmt.Println("[3] sending source USDC EOA → TEE Safe on Base...")
	txHash, err := sendUSDC(priv, eoa, teeSafeBase, amount)
	if err != nil {
		die("sendUSDC: %v", err)
	}
	fmt.Printf("    on-chain tx: %s\n\n", txHash)

	fmt.Println("[4] polling /bridge/:id (up to 25 min)...")
	deadline := time.Now().Add(25 * time.Minute)
	last := ""
	for time.Now().Before(deadline) {
		time.Sleep(8 * time.Second)
		s, err := fetchStatus(privacyURL, sub.SessionID)
		if err != nil {
			fmt.Printf("    poll err: %v\n", err)
			continue
		}
		line := fmt.Sprintf("%s sweep=%s bridge=%s delivered=%s err=%s",
			s.Status, short(s.SweepTxHash), short(s.BridgeTxHash), short(s.DeliveredTxHash), s.Error)
		if line != last {
			fmt.Printf("    %s  %s\n", time.Now().Format("15:04:05"), line)
			last = line
		}
		if s.Status == "delivered" {
			fmt.Println("\n✅ DELIVERED")
			fmt.Printf("   sweepTxHash:     %s\n", s.SweepTxHash)
			fmt.Printf("   bridgeTxHash:    %s\n", s.BridgeTxHash)
			fmt.Printf("   deliveredTxHash: %s\n", s.DeliveredTxHash)

			// Verify the predicted Safe address actually received USDT0
			// on MegaETH, even though the Safe contract isn't deployed.
			bal, err := readUSDT0OnMegaETH(dest)
			if err != nil {
				fmt.Printf("\n⚠️  could not verify dest balance: %v\n", err)
			} else {
				fmt.Printf("\n💰 USDT0 balance at predicted Safe %s on MegaETH: %s atomic ($%s)\n",
					dest.Hex(), bal, formatTokenAmount(bal, 6))
				code, _ := readBytecode(dest)
				if code == "0x" || code == "" {
					fmt.Printf("   📦 Safe NOT deployed yet — funds sit at the predicted CREATE2 address\n")
					fmt.Printf("      Owner can deploy v1.3.0 SafeProxy via factory %s\n", blockchain.SafeProxyFactoryAddress.Hex())
					fmt.Printf("      with owner=%s, threshold=1, then sweep via Safe execTx.\n", stealthOwner.Hex())
				} else {
					fmt.Printf("   📦 Safe is already deployed (bytecode len=%d chars)\n", len(code))
				}
			}
			return
		}
		if s.Status == "failed" {
			die("intent failed: %s", s.Error)
		}
	}
	die("timeout waiting for delivery")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func short(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) <= 14 {
		return h
	}
	return h[:8] + ".." + h[len(h)-6:]
}

func formatTokenAmount(amount *big.Int, decimals int) string {
	f := new(big.Float).SetInt(amount)
	d := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	f.Quo(f, d)
	v, _ := f.Float64()
	return fmt.Sprintf("%.6f", v)
}

func fetchPubKey(url string) (string, error) {
	resp, err := http.Get(url + "/pubkey")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	if !r.Success || r.PublicKey == "" {
		return "", fmt.Errorf("bad /pubkey response")
	}
	return r.PublicKey, nil
}

func fetchStatus(url, sessionID string) (*statusResponse, error) {
	resp, err := http.Get(url + "/bridge/" + sessionID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var s statusResponse
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, fmt.Errorf("decode: %v body=%s", err, string(rb))
	}
	return &s, nil
}

// sendUSDC submits a Base mainnet USDC transfer of `amount` from `from`
// to `to`, signed by `priv`. Builds an EIP-1559 (DynamicFeeTx) directly,
// no abi/bind dance.
func sendUSDC(priv *ecdsa.PrivateKey, from, to common.Address, amount *big.Int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client, err := ethclient.Dial(baseRPC)
	if err != nil {
		return "", err
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return "", err
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return "", err
	}
	gasTip, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return "", err
	}
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return "", err
	}
	gasFeeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), gasTip)

	// transfer(address,uint256) selector = 0xa9059cbb
	data := append([]byte{0xa9, 0x05, 0x9c, 0xbb},
		append(common.LeftPadBytes(to.Bytes(), 32),
			common.LeftPadBytes(amount.Bytes(), 32)...)...)

	usdc := common.HexToAddress(baseUSDC)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTip,
		GasFeeCap: gasFeeCap,
		Gas:       80_000,
		To:        &usdc,
		Value:     big.NewInt(0),
		Data:      data,
	})
	signedTx, err := types.SignTx(tx, types.NewLondonSigner(chainID), priv)
	if err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return "", err
	}
	receipt, err := bind.WaitMined(ctx, client, signedTx)
	if err != nil {
		return signedTx.Hash().Hex(), err
	}
	if receipt.Status != 1 {
		return signedTx.Hash().Hex(), fmt.Errorf("USDC transfer reverted")
	}
	return signedTx.Hash().Hex(), nil
}

// readUSDT0OnMegaETH reads USDT0.balanceOf(holder) on MegaETH mainnet.
// Used to verify the privacy bridge actually delivered tokens to the
// predicted Safe address.
func readUSDT0OnMegaETH(holder common.Address) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ethclient.Dial("https://mainnet.megaeth.com/rpc")
	if err != nil {
		return nil, err
	}
	defer client.Close()
	usdt0 := common.HexToAddress("0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb")
	data := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(holder.Bytes(), 32)...)
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &usdt0, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return new(big.Int), nil
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// readBytecode returns the contract bytecode at addr on MegaETH (or "0x" if none).
func readBytecode(addr common.Address) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ethclient.Dial("https://mainnet.megaeth.com/rpc")
	if err != nil {
		return "", err
	}
	defer client.Close()
	code, err := client.CodeAt(ctx, addr, nil)
	if err != nil {
		return "", err
	}
	return "0x" + common.Bytes2Hex(code), nil
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}
