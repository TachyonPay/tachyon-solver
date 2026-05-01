// =============================================================================
// TRANSACTION STATUS — Frontend Integration Reference
// =============================================================================
//
// This file tests the 3 transaction-status endpoints on the RELAYER (port 8080).
// It simulates exactly what the FE and solver do in a real intent flow.
//
// -----------------------------------------------------------------------------
// WHO CALLS WHAT
// -----------------------------------------------------------------------------
//
//   FRONTEND calls:
//     POST /transactions       — immediately after deposit tx mines (write pending)
//     GET  /transactions       — to show history / poll for status updates
//
//   SOLVER calls (automatically, FE never calls this):
//     POST /admin/transactions/complete — after settling, to mark it completed
//
// -----------------------------------------------------------------------------
// AUTHENTICATION (X-Signature + X-Timestamp headers)
// -----------------------------------------------------------------------------
//
// Both POST /transactions and GET /transactions require wallet auth.
// The FE signs an EIP-712 typed message using ethers.js:
//
//   const domain = { name: "TachyonStealth", version: "1" }
//   const types  = { AuthSession: [{ name: "timestamp", type: "uint256" }] }
//   const value  = { timestamp: Math.floor(Date.now() / 1000) }
//
//   const signature = await signer.signTypedData(domain, types, value)
//
//   // Then attach to every request:
//   headers["X-Signature"] = signature        // hex string, e.g. "0xabc123..."
//   headers["X-Timestamp"] = value.timestamp  // unix seconds as a string
//
// Signatures are valid for 30 minutes. The recovered address from the signature
// is used as the authenticated user — no login/session needed.
//
// -----------------------------------------------------------------------------
// POST /transactions — write pending (call this right after deposit tx mines)
// -----------------------------------------------------------------------------
//
//   POST http://relayer:8080/transactions
//   Headers: X-Signature, X-Timestamp, Content-Type: application/json
//   Body:
//   {
//     "intentId":        "42",                          // from on-chain event/receipt
//     "userAddress":     "0xYourWalletAddress",         // must match authenticated wallet
//     "sourceChainId":   "2651420",                     // chain where user deposited
//     "destChainId":     "84532",                       // chain where tokens arrive
//     "tokenA":          "0xTokenAAddress",             // token user deposited
//     "tokenB":          "0xTokenBAddress",             // token user wants back
//     "amountA":         "100000000000000000000",       // raw wei (100 tokens × 10^18)
//     "expectedAmountB": "95000000000000000000",        // raw wei (95 tokens × 10^18)
//     "depositTxHash":   "0xDepositTxHash"              // hash of the deposit tx
//   }
//
//   Response: { "success": true, "intentId": "42" }
//
// -----------------------------------------------------------------------------
// GET /transactions — read status (poll this to show history)
// -----------------------------------------------------------------------------
//
//   GET http://relayer:8080/transactions
//   Headers: X-Signature, X-Timestamp
//   Optional query params: ?status=pending  ?page=1  ?limit=20
//
//   Response:
//   {
//     "success": true,
//     "transactions": [
//       {
//         "intentId":        "42",
//         "userAddress":     "0xYourWalletAddress",
//         "sourceChainId":   "2651420",
//         "destChainId":     "84532",
//         "tokenA":          "0xTokenAAddress",
//         "tokenB":          "0xTokenBAddress",
//         "amountA":         "100000000000000000000",
//         "expectedAmountB": "95000000000000000000",
//         "status":          "pending" | "completed",
//         "depositTxHash":   "0x...",   // present immediately
//         "solveTxHash":     "0x...",   // present once solver delivers tokens
//         "settleTxHash":    "0x...",   // present once settlement confirmed
//         "createdAt":       "2026-04-30T12:00:00Z",
//         "updatedAt":       "2026-04-30T12:01:00Z",
//         "completedAt":     "2026-04-30T12:01:00Z"  // only when completed
//       }
//     ],
//     "page": 1,
//     "limit": 20,
//     "total": 1,
//     "totalPages": 1
//   }
//
// -----------------------------------------------------------------------------
// STATUS LIFECYCLE
// -----------------------------------------------------------------------------
//
//   "pending"   — set by FE immediately on POST /transactions
//   "completed" — set by solver automatically after settlement
//
//   FE can poll GET /transactions every few seconds and update the UI when
//   status changes from "pending" → "completed" and solveTxHash/settleTxHash appear.
//
// =============================================================================
package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
)

// --- helpers -----------------------------------------------------------------

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
		os.Exit(1)
	}
}

func postJSON(url string, body any, headers map[string]string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	must(err, "build request")
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	must(err, "POST "+url)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func getJSON(url string, headers map[string]string) (int, map[string]any) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	must(err, "build request")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	must(err, "GET "+url)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// walletAuthHeaders produces X-Signature / X-Timestamp for WalletSignatureAuth.
// Must match RecoverAuthSession in pkg/stealth/eip712.go: EIP-712 AuthSession(uint256 timestamp)
// under the TachyonStealth v1 domain.
func walletAuthHeaders(privKey *ecdsa.PrivateKey) map[string]string {
	ts := time.Now().Unix()

	domainTypeHash := crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	domainSeparator := crypto.Keccak256Hash(
		domainTypeHash.Bytes(),
		crypto.Keccak256([]byte("TachyonStealth")),
		crypto.Keccak256([]byte("1")),
	)
	authSessionTypeHash := crypto.Keccak256Hash([]byte("AuthSession(uint256 timestamp)"))
	structHash := crypto.Keccak256Hash(
		authSessionTypeHash.Bytes(),
		common.LeftPadBytes(big.NewInt(ts).Bytes(), 32),
	)
	digest := crypto.Keccak256Hash([]byte{0x19, 0x01}, domainSeparator.Bytes(), structHash.Bytes())

	sig, err := crypto.Sign(digest.Bytes(), privKey)
	must(err, "sign auth")
	return map[string]string{
		"X-Signature": hexutil.Encode(sig),
		"X-Timestamp": strconv.FormatInt(ts, 10),
	}
}

// --- main --------------------------------------------------------------------

func main() {
	godotenv.Load("../../../.env")

	relayerURL := os.Getenv("RELAYER_URL")
	if relayerURL == "" {
		relayerURL = "http://localhost:8080"
	}
	adminKey := os.Getenv("ADMIN_API_KEY")
	if adminKey == "" {
		fmt.Println("WARN: ADMIN_API_KEY not set — complete endpoint test will be skipped")
	}

	// Use one of the Safe signer keys as the test wallet (already funded in .env)
	rawKey := os.Getenv("SAFE_SIGNER_KEY_1")
	must(func() error {
		if rawKey == "" {
			return fmt.Errorf("SAFE_SIGNER_KEY_1 not set")
		}
		return nil
	}(), "load key")

	privKey, err := crypto.HexToECDSA(rawKey[2:]) // strip 0x
	must(err, "parse private key")
	userAddr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()

	fmt.Printf("Test wallet: %s\n", userAddr)
	fmt.Printf("Relayer:     %s\n\n", relayerURL)

	// Use a deterministic fake intentId so re-runs don't accumulate junk
	intentID := new(big.Int).SetBytes(crypto.Keccak256([]byte("test-intent-v1"))).String()
	sourceChainID := "2651420"
	destChainID := "84532"
	depositTxHash := "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	// -------------------------------------------------------------------------
	// 1. POST /transactions  — FE creates pending record
	// -------------------------------------------------------------------------
	fmt.Println("=== 1. POST /transactions (create pending) ===")
	authHeaders := walletAuthHeaders(privKey)
	code, resp := postJSON(relayerURL+"/transactions", map[string]any{
		"intentId":        intentID,
		"userAddress":     userAddr,
		"sourceChainId":   sourceChainID,
		"destChainId":     destChainID,
		"tokenA":          "0xF37dF505936f1b12aA64C2B568BD037025E7e569",
		"tokenB":          "0xF37dF505936f1b12aA64C2B568BD037025E7e569",
		"amountA":         "100000000000000000000",
		"expectedAmountB": "95000000000000000000",
		"depositTxHash":   depositTxHash,
	}, authHeaders)
	fmt.Printf("  status=%d  body=%v\n", code, resp)
	if code != http.StatusOK {
		fmt.Println("  FAILED: expected 200")
		os.Exit(1)
	}
	fmt.Println("  PASSED")

	// -------------------------------------------------------------------------
	// 2. GET /transactions  — should see the pending record immediately
	// -------------------------------------------------------------------------
	fmt.Println("\n=== 2. GET /transactions (read pending) ===")
	authHeaders = walletAuthHeaders(privKey)
	code, resp = getJSON(relayerURL+"/transactions", authHeaders)
	fmt.Printf("  status=%d  body=%v\n", code, resp)
	if code != http.StatusOK {
		fmt.Println("  FAILED: expected 200")
		os.Exit(1)
	}
	txns, _ := resp["transactions"].([]any)
	found := false
	for _, t := range txns {
		rec, _ := t.(map[string]any)
		if rec["intentId"] == intentID && rec["status"] == "pending" {
			found = true
			break
		}
	}
	if !found {
		fmt.Printf("  FAILED: intentId %s with status=pending not found in response\n", intentID)
		os.Exit(1)
	}
	fmt.Println("  PASSED — pending record visible immediately")

	// -------------------------------------------------------------------------
	// 3. POST /admin/transactions/complete  — solver marks it completed
	// -------------------------------------------------------------------------
	fmt.Println("\n=== 3. POST /admin/transactions/complete (solver settles) ===")
	if adminKey == "" {
		fmt.Println("  SKIPPED — ADMIN_API_KEY not set")
	} else {
		solveTxHash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		settleTxHash := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		code, resp = postJSON(relayerURL+"/admin/transactions/complete", map[string]any{
			"sourceChainId": sourceChainID,
			"intentId":      intentID,
			"depositTxHash": depositTxHash,
			"solveTxHash":   solveTxHash,
			"settleTxHash":  settleTxHash,
		}, map[string]string{"X-API-Key": adminKey})
		fmt.Printf("  status=%d  body=%v\n", code, resp)
		if code != http.StatusOK {
			fmt.Println("  FAILED: expected 200")
			os.Exit(1)
		}
		fmt.Println("  PASSED")

		// -----------------------------------------------------------------------
		// 4. GET /transactions  — should now show completed with tx hashes
		// -----------------------------------------------------------------------
		fmt.Println("\n=== 4. GET /transactions (read completed) ===")
		authHeaders = walletAuthHeaders(privKey)
		code, resp = getJSON(relayerURL+"/transactions", authHeaders)
		fmt.Printf("  status=%d  body=%v\n", code, resp)
		txns, _ = resp["transactions"].([]any)
		found = false
		for _, t := range txns {
			rec, _ := t.(map[string]any)
			if rec["intentId"] == intentID && rec["status"] == "completed" &&
				rec["solveTxHash"] == solveTxHash && rec["settleTxHash"] == settleTxHash {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("  FAILED: intentId %s with status=completed not found\n", intentID)
			os.Exit(1)
		}
		fmt.Println("  PASSED — completed record with all tx hashes visible")
	}

	fmt.Println("\n✓ All tests passed")
}
