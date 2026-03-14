// test-safe-deploy: tests the full stealth+safe deployment flow against a running relayer.
// Verifies the predicted Safe address matches the ACTUAL on-chain deployed address
// by reading the ProxyCreation event from the deployment tx receipt.
// Usage: RELAYER_URL=http://localhost:3000 go run ./cmd/test-safe-deploy/
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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	pkgtypes "github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// ProxyCreation event topic: keccak256("ProxyCreation(address,address)")
var proxyCreationTopic = crypto.Keccak256Hash([]byte("ProxyCreation(address,address)"))

var chainRPCs = map[string]string{
	"84532":     "https://sepolia.base.org",
	"845320009": "https://horizen-rpc-testnet.appchain.base.org",
}

func main() {
	relayerURL := os.Getenv("RELAYER_URL")
	if relayerURL == "" {
		relayerURL = "http://localhost:3000"
	}

	privateKeyHex := os.Getenv("PRIVATE_KEY")
	if privateKeyHex == "" {
		privateKeyHex = "0x1dd0046b5493fff5b112fc1efea1d309d35e04eee398f851ac9f9a2434658f22"
	}
	if strings.HasPrefix(privateKeyHex, "0x") {
		privateKeyHex = privateKeyHex[2:]
	}

	privKey, err := crypto.HexToECDSA(privateKeyHex)
	must(err, "parse private key")
	recipientAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	fmt.Printf("👤 Recipient: %s\n", recipientAddr.Hex())
	fmt.Printf("🌐 Relayer:   %s\n\n", relayerURL)

	chains := []struct{ name, chainID string }{
		{"Base Sepolia", "84532"},
		{"Horizen Testnet", "845320009"},
	}

	passed := 0
	for _, chain := range chains {
		fmt.Printf("══════════════════════════════════════════════════\n")
		fmt.Printf("🔗 Chain: %s (ID %s)\n", chain.name, chain.chainID)
		fmt.Printf("══════════════════════════════════════════════════\n")

		if err := runTest(relayerURL, privKey, recipientAddr, chain.chainID); err != nil {
			fmt.Printf("❌ FAILED: %v\n\n", err)
		} else {
			fmt.Printf("✅ PASSED\n\n")
			passed++
		}
	}

	fmt.Printf("Results: %d/%d chains passed\n", passed, len(chains))
	if passed < len(chains) {
		os.Exit(1)
	}
}

func runTest(relayerURL string, privKey *ecdsa.PrivateKey, recipientAddr common.Address, chainID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	rpcURL, ok := chainRPCs[chainID]
	if !ok {
		return fmt.Errorf("no RPC URL configured for chain %s", chainID)
	}
	ethClient, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("dial RPC: %w", err)
	}
	defer ethClient.Close()

	// Step 1: Register recipient
	fmt.Printf("\n[1] Registering recipient...\n")
	regSig, err := stealth.SignRegisterStealth(privKey, recipientAddr)
	must(err, "sign register")

	regResp := &pkgtypes.RegisterRecipientResponse{}
	if err := postJSON(ctx, relayerURL+"/recipient/register", pkgtypes.RegisterRecipientRequest{
		RecipientAddress: recipientAddr.Hex(),
		Signature:        "0x" + hex.EncodeToString(regSig),
	}, regResp); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if !regResp.Success {
		return fmt.Errorf("register failed: %s", regResp.Error)
	}
	fmt.Printf("    SpendPubKey: %s...\n", regResp.SpendPubKey[:20])

	// Step 2: Generate stealth address
	fmt.Printf("\n[2] Generating stealth address for chain %s...\n", chainID)
	genResp := &pkgtypes.GenerateStealthResponse{}
	if err := postJSON(ctx, relayerURL+"/recipient/generate-stealth", pkgtypes.GenerateStealthRequest{
		RecipientAddress: recipientAddr.Hex(),
		ChainID:          chainID,
		IntentID:         fmt.Sprintf("test-%s-%d", chainID, time.Now().Unix()),
	}, genResp); err != nil {
		return fmt.Errorf("generate-stealth: %w", err)
	}
	stealthAddr := common.HexToAddress(genResp.StealthAddress)
	predictedSafe := common.HexToAddress(genResp.SafeAddress)
	fmt.Printf("    StealthAddr:   %s\n", stealthAddr.Hex())
	fmt.Printf("    PredictedSafe: %s\n", predictedSafe.Hex())

	// Step 3: Derive stealth key
	fmt.Printf("\n[3] Deriving stealth key...\n")
	deriveSig, _ := stealth.SignDeriveStealthKey(privKey, recipientAddr, stealthAddr)
	deriveResp := &pkgtypes.DeriveStealthKeyResponse{}
	if err := postJSON(ctx, relayerURL+"/recipient/derive-stealth-key", pkgtypes.DeriveStealthKeyRequest{
		RecipientAddress: recipientAddr.Hex(),
		StealthAddress:   stealthAddr.Hex(),
		Signature:        "0x" + hex.EncodeToString(deriveSig),
	}, deriveResp); err != nil {
		return fmt.Errorf("derive-stealth-key: %w", err)
	}
	if !deriveResp.Success {
		return fmt.Errorf("derive-stealth-key failed: %s", deriveResp.Error)
	}
	stealthPrivKeyHex := strings.TrimPrefix(deriveResp.StealthPrivateKey, "0x")
	stealthPrivKey, err := crypto.HexToECDSA(stealthPrivKeyHex)
	if err != nil {
		return fmt.Errorf("parse stealth private key: %w", err)
	}
	if crypto.PubkeyToAddress(stealthPrivKey.PublicKey) != stealthAddr {
		return fmt.Errorf("stealth key address mismatch")
	}
	fmt.Printf("    ✅ Stealth key verified\n")

	// Step 4: Deploy Safe
	fmt.Printf("\n[4] Deploying Safe on chain %s...\n", chainID)
	chainIDBig, _ := new(big.Int).SetString(chainID, 10)
	deploySig, _ := stealth.SignDeploySafe(stealthPrivKey, stealthAddr, chainIDBig)

	deployResp := &pkgtypes.DeploySafeResponse{}
	if err := postJSON(ctx, relayerURL+"/recipient/deploy-safe", pkgtypes.DeploySafeRequest{
		StealthAddress: stealthAddr.Hex(),
		ChainID:        chainID,
		Signature:      "0x" + hex.EncodeToString(deploySig),
	}, deployResp); err != nil {
		return fmt.Errorf("deploy-safe: %w", err)
	}
	if !deployResp.Success {
		return fmt.Errorf("deploy-safe failed: %s", deployResp.Error)
	}
	fmt.Printf("    TxHash:        %s\n", deployResp.TxHash)
	fmt.Printf("    Predicted:     %s (from relayer)\n", deployResp.SafeAddress)

	// Step 5: Get ACTUAL deployed proxy from tx receipt's ProxyCreation event
	fmt.Printf("\n[5] Verifying on-chain...\n")
	txHash := common.HexToHash(deployResp.TxHash)
	receipt, err := ethClient.TransactionReceipt(ctx, txHash)
	if err != nil {
		return fmt.Errorf("get receipt: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("tx reverted (status=%d)", receipt.Status)
	}

	actualProxy, err := extractProxyFromReceipt(receipt)
	if err != nil {
		return fmt.Errorf("extract proxy from receipt: %w", err)
	}
	fmt.Printf("    Actual proxy:  %s (from ProxyCreation event)\n", actualProxy.Hex())

	// Verify prediction matches on-chain reality
	if !strings.EqualFold(predictedSafe.Hex(), actualProxy.Hex()) {
		return fmt.Errorf("MISMATCH:\n   predicted: %s\n   actual:    %s", predictedSafe.Hex(), actualProxy.Hex())
	}
	fmt.Printf("    ✅ Predicted address matches actual on-chain proxy\n")

	// Verify the proxy has bytecode (retry a few times to handle RPC node staleness)
	var code []byte
	for i := 0; i < 5; i++ {
		code, err = ethClient.CodeAt(ctx, actualProxy, nil)
		if err == nil && len(code) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || len(code) == 0 {
		return fmt.Errorf("proxy has no bytecode at %s (after retries)", actualProxy.Hex())
	}
	fmt.Printf("    ✅ Proxy has bytecode (%d bytes)\n", len(code))

	return nil
}

// extractProxyFromReceipt finds the ProxyCreation(address proxy, address singleton) event
// and returns the proxy address.
func extractProxyFromReceipt(receipt *types.Receipt) (common.Address, error) {
	for _, log := range receipt.Logs {
		if len(log.Topics) > 0 && log.Topics[0] == proxyCreationTopic {
			// proxy address is first indexed topic (topics[1]) or in data
			// ProxyCreation(address indexed proxy, address singleton) — proxy is NOT indexed
			// Data: proxy (32 bytes) + singleton (32 bytes)
			if len(log.Data) >= 32 {
				proxy := common.BytesToAddress(log.Data[:32])
				return proxy, nil
			}
		}
	}
	// Fallback: some versions emit proxy as topics[1]
	for _, log := range receipt.Logs {
		if len(log.Topics) >= 2 && log.Topics[0] == proxyCreationTopic {
			return common.BytesToAddress(log.Topics[1].Bytes()), nil
		}
	}
	return common.Address{}, fmt.Errorf("ProxyCreation event not found in receipt (topic: %s)", proxyCreationTopic.Hex())
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
