// aori-e2e-test stands in for the FE to drive a real Aori bridge through
// the deployed privacy router. Submits a 0.2 USDC@Op → USDM@MegaETH
// intent, funds the TEE Safe to trigger deposit detection, then polls
// /bridge/<sessionID> until the privacy router reports delivered/failed.
//
// Run:
//   cd tachyon-prod && go run ./cmd/aori-e2e-test
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

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// EIP-712 schema for the relayer's RegisterStealth flow. Matches the
// canonical definition in test/e2e/complete_flow.go and the relayer's
// /recipient/register handler.
var (
	eip712DomainTypeHash    = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	eip712DomainSeparator   = crypto.Keccak256Hash(eip712DomainTypeHash.Bytes(), crypto.Keccak256([]byte("TachyonStealth")), crypto.Keccak256([]byte("1")))
	registerStealthTypeHash = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
)

func eip712Hash(structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, eip712DomainSeparator.Bytes(), structHash.Bytes())
}

func signRegisterStealth(key *ecdsa.PrivateKey, signer common.Address) ([]byte, error) {
	structHash := crypto.Keccak256Hash(registerStealthTypeHash.Bytes(), common.LeftPadBytes(signer.Bytes(), 32))
	digest := eip712Hash(structHash)
	return crypto.Sign(digest.Bytes(), key)
}

const (
	privacyRouterURL = "https://privacy.tachyon.pe"
	// Source: Op USDC. Target: MegaETH USDM.
	chainOpID         = "10"
	chainMegaETHID    = "4326"
	usdcOpAddr        = "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"
	usdmMegaETHAddr   = "0xFAfDdbb3FC7688494971a79cc65DCa3EF82079E7"
	teeSafeOpAddr     = "0x2d1e2cD358C75dD17B2Ffc87495D4F2BbcfA4a7C"
	megaETHRPC        = "https://mainnet.megaeth.com/rpc"
)

func main() {
	amountUSDC := flag.Float64("amount", 0.2, "USDC amount (decimal, e.g. 0.2)")
	flag.Parse()

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
	userAddr := crypto.PubkeyToAddress(priv.PublicKey)
	fmt.Printf("user (relayer EOA): %s\n", userAddr.Hex())

	opRPC := os.Getenv("CHAIN_6_RPC")
	if opRPC == "" {
		opRPC = "https://opt-mainnet.g.alchemy.com/v2/" + strings.TrimSpace(strings.TrimPrefix(os.Getenv("OP_RPC"), "https://opt-mainnet.g.alchemy.com/v2/"))
	}
	if !strings.HasPrefix(opRPC, "http") {
		fatal("CHAIN_6_RPC not set (Optimism RPC URL)")
	}

	ctx := context.Background()

	// Amount in raw units (USDC = 6 decimals).
	amountRaw := big.NewInt(int64(*amountUSDC * 1_000_000))
	if amountRaw.Sign() <= 0 {
		fatal("amount must be > 0")
	}

	// 1. Get the privacy router's ECIES public key.
	fmt.Println("\n=== Step 1: fetch ECIES pubkey ===")
	pubkeyHex, err := fetchPubkey(ctx)
	if err != nil {
		fatal(fmt.Sprintf("fetch pubkey: %v", err))
	}
	fmt.Printf("pubkey: %s\n", pubkeyHex)
	eciesPub, err := ecies.NewPublicKeyFromHex(strings.TrimPrefix(pubkeyHex, "0x"))
	if err != nil {
		fatal(fmt.Sprintf("parse pubkey: %v", err))
	}

	// 2. Build the BridgeIntentPayload. Mode 2 — we set DestStealthSafe
	//    directly. Using the relayer EOA's own address on MegaETH so we
	//    can verify USDM landed by reading its balance.
	sessionID := "aori-e2e-" + uuid.New().String()
	// Mode 1 (relayer-managed stealth): leave destStealthSafe empty,
	// pass an EIP-712 RegisterStealth(userAddress) signature. Privacy
	// router calls the relayer to mint a fresh stealth Safe address
	// for this intent — we discover it in the POST /bridge response.
	regSig, err := signRegisterStealth(priv, userAddr)
	if err != nil {
		fatal(fmt.Sprintf("sign RegisterStealth: %v", err))
	}
	regSigHex := "0x" + hex.EncodeToString(regSig)

	// inboundSafe is the SENDER's address (the address that will transfer
	// USDC into the TEE Safe — the watcher matches deposits by from-address).
	payload := map[string]interface{}{
		"sessionId":          sessionID,
		"userAddress":        userAddr.Hex(),
		"sourceChainId":      chainOpID,
		"sourceToken":        "USDC",
		"inboundSafe":        userAddr.Hex(),
		"amount":             amountRaw.String(),
		"destChainId":        chainMegaETHID,
		"destToken":          "USDM",
		"registerStealthSig": regSigHex,
		"unixTimestamp":      time.Now().Unix(),
	}
	plainJSON, _ := json.Marshal(payload)
	fmt.Printf("\n=== Step 2: payload ===\n%s\n", string(plainJSON))

	// 3. Encrypt with ECIES.
	encrypted, err := ecies.Encrypt(eciesPub, plainJSON)
	if err != nil {
		fatal(fmt.Sprintf("encrypt: %v", err))
	}
	encryptedHex := "0x" + hex.EncodeToString(encrypted)

	// 4. POST /bridge.
	fmt.Println("\n=== Step 3: POST /bridge ===")
	submitResp, err := postBridge(ctx, encryptedHex)
	if err != nil {
		fatal(fmt.Sprintf("POST /bridge: %v", err))
	}
	fmt.Printf("sessionId:       %s\n", submitResp.SessionID)
	fmt.Printf("destStealthSafe: %s\n", submitResp.DestStealthSafe)

	// 5. Read pre-bridge USDM balance on MegaETH at the freshly-generated
	//    stealth Safe. Mode-1: the privacy router minted this address for us
	//    by calling the relayer's /recipient/generate-stealth. It's a
	//    CREATE2-predicted Safe — likely undeployed at this moment but
	//    perfectly able to receive ERC20 transfers.
	stealthAddr := common.HexToAddress(submitResp.DestStealthSafe)
	if stealthAddr == (common.Address{}) {
		fatal("privacy router didn't return a destStealthSafe (mode 1 sourcing failed?)")
	}
	preUSDM := readUSDMBalance(ctx, stealthAddr)
	fmt.Printf("\npre-bridge USDM @ stealth Safe %s: %s (raw 18dec)\n", stealthAddr.Hex(), preUSDM.String())

	// 6. Send the USDC from user → TEE Safe on Op.
	fmt.Println("\n=== Step 4: send USDC to TEE Safe on Op ===")
	opClient, err := ethclient.Dial(opRPC)
	if err != nil {
		fatal(fmt.Sprintf("dial Op RPC: %v", err))
	}
	depositTx, err := sendERC20Transfer(ctx, opClient, big.NewInt(10), priv, userAddr,
		common.HexToAddress(usdcOpAddr),
		common.HexToAddress(teeSafeOpAddr),
		amountRaw)
	if err != nil {
		fatal(fmt.Sprintf("send deposit: %v", err))
	}
	fmt.Printf("deposit tx: %s\n", depositTx.Hash().Hex())
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rcpt, err := bind.WaitMined(waitCtx, opClient, depositTx)
	if err != nil {
		fatal(fmt.Sprintf("wait deposit mined: %v", err))
	}
	if rcpt.Status != 1 {
		fatal(fmt.Sprintf("deposit tx reverted: %s", depositTx.Hash().Hex()))
	}
	fmt.Printf("deposit mined, block %d\n", rcpt.BlockNumber.Uint64())

	// 7. Poll /bridge/<sessionID> for status until terminal or timeout.
	fmt.Println("\n=== Step 5: poll /bridge/<sessionID> ===")
	deadline := time.Now().Add(10 * time.Minute)
	pollTicker := time.NewTicker(8 * time.Second)
	defer pollTicker.Stop()
	for {
		st, err := fetchSession(ctx, sessionID)
		if err == nil && st != nil {
			fmt.Printf("[%s] status=%s sweep=%s bridge=%s deliveredTx=%s err=%s\n",
				time.Now().Format("15:04:05"),
				st.Status, short(st.SweepTxHash), short(st.BridgeTxHash), short(st.DeliveredTxHash), st.Error)
			switch st.Status {
			case "delivered", "complete", "completed":
				postUSDM := readUSDMBalance(ctx, stealthAddr)
				diff := new(big.Int).Sub(postUSDM, preUSDM)
				fmt.Printf("\n=== SUCCESS ===\nstealth Safe: %s\nUSDM delta on stealth Safe: %s (raw 18dec)\n", stealthAddr.Hex(), diff.String())
				codeLen := readCodeLen(ctx, stealthAddr)
				if codeLen == 0 {
					fmt.Println("note: stealth Safe is currently undeployed (CREATE2 prediction holds the USDM until the user runs /recipient/deploy-safe)")
				} else {
					fmt.Printf("note: stealth Safe is deployed (%d bytes of code on MegaETH)\n", codeLen)
				}
				return
			case "refunded", "failed", "expired":
				fmt.Printf("\n=== TERMINAL FAILURE: %s ===\n", st.Status)
				if st.Error != "" {
					fmt.Println("error:", st.Error)
				}
				os.Exit(1)
			}
		} else if err != nil {
			fmt.Printf("[%s] poll error: %v\n", time.Now().Format("15:04:05"), err)
		}
		if time.Now().After(deadline) {
			fmt.Println("\n=== TIMEOUT after 10min ===")
			os.Exit(1)
		}
		<-pollTicker.C
	}
}

type submitResponse struct {
	Success         bool   `json:"success"`
	SessionID       string `json:"sessionId,omitempty"`
	DestStealthSafe string `json:"destStealthSafe,omitempty"`
	Error           string `json:"error,omitempty"`
}

type sessionState struct {
	Status          string `json:"status"`
	SweepTxHash     string `json:"sweepTxHash"`
	BridgeTxHash    string `json:"bridgeTxHash"`
	DeliveredTxHash string `json:"deliveredTxHash"`
	Error           string `json:"error"`
}

func fetchPubkey(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", privacyRouterURL+"/pubkey", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var p struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return strings.TrimSpace(string(body)), nil
	}
	if p.PublicKey == "" {
		return "", fmt.Errorf("empty publicKey in response: %s", string(body))
	}
	return p.PublicKey, nil
}

func postBridge(ctx context.Context, encryptedHex string) (*submitResponse, error) {
	body, _ := json.Marshal(map[string]string{"encrypted": encryptedHex})
	req, _ := http.NewRequestWithContext(ctx, "POST", privacyRouterURL+"/bridge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var r submitResponse
	if err := json.Unmarshal(rb, &r); err != nil {
		return nil, err
	}
	if !r.Success {
		return nil, fmt.Errorf("server error: %s", r.Error)
	}
	return &r, nil
}

func fetchSession(ctx context.Context, sessionID string) (*sessionState, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", privacyRouterURL+"/bridge/"+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var s sessionState
	if err := json.Unmarshal(rb, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func sendERC20Transfer(
	ctx context.Context,
	client *ethclient.Client,
	chainID *big.Int,
	priv *ecdsa.PrivateKey,
	from common.Address,
	token, to common.Address,
	amount *big.Int,
) (*types.Transaction, error) {
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, err
	}
	tip, _ := client.SuggestGasTipCap(ctx)
	head, _ := client.HeaderByNumber(ctx, nil)
	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)

	// transfer(address,uint256)
	data := []byte{0xa9, 0x05, 0x9c, 0xbb}
	data = append(data, common.LeftPadBytes(to.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       100_000,
		To:        &token,
		Value:     big.NewInt(0),
		Data:      data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), priv)
	if err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, err
	}
	return signed, nil
}

func readUSDMBalance(ctx context.Context, holder common.Address) *big.Int {
	c, err := ethclient.Dial(megaETHRPC)
	if err != nil {
		return big.NewInt(0)
	}
	tok := common.HexToAddress(usdmMegaETHAddr)
	data := []byte{0x70, 0xa0, 0x82, 0x31}
	data = append(data, common.LeftPadBytes(holder.Bytes(), 32)...)
	out, err := c.CallContract(ctx, ethereum.CallMsg{From: holder, To: &tok, Data: data}, nil)
	if err != nil || len(out) < 32 {
		return big.NewInt(0)
	}
	return new(big.Int).SetBytes(out[:32])
}

// readCodeLen returns the byte-length of contract code at addr on MegaETH.
// 0 means EOA or CREATE2-predicted-but-undeployed.
func readCodeLen(ctx context.Context, addr common.Address) int {
	c, err := ethclient.Dial(megaETHRPC)
	if err != nil {
		return 0
	}
	code, err := c.CodeAt(ctx, addr, nil)
	if err != nil {
		return 0
	}
	return len(code)
}

func short(s string) string {
	if len(s) < 12 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "FATAL: %s\n", msg)
	os.Exit(1)
}
