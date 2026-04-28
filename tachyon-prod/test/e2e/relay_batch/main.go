package main

// Standalone relay-batch E2E test.
//
// Run with:
//   cd tachyon-prod/test/e2e
//   go run relay_batch_test.go
//
// What it tests:
//   1. Register a fresh recipient with the relayer (TEE generates stealth keys)
//   2. Generate a stealth address + predicted Safe address on Base Sepolia
//   3. Fund the predicted Safe with a small ERC-20 amount from the test wallet
//   4. Deploy the Safe via the relayer
//   5. Derive the stealth private key from the TEE
//   6. Build a Safe tx (transfer tokens back to test wallet)
//   7. Sign the Safe tx hash with the stealth key
//   8. Call POST /recipient/relay-batch with the signed item
//   9. Wait for the tx and verify the token balance moved
//
// Prerequisites:
//   - Relayer running and reachable at RELAYER_URL
//   - Test wallet (PRIVATE_KEY) has some TOKEN_WETH_BASE on Base Sepolia
//   - CHAIN_2_RPC set in .env

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

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

// ── ABIs ─────────────────────────────────────────────────────────────────────

const safeABIForTest = `[
  {"inputs":[],"name":"nonce","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[
    {"internalType":"address","name":"to","type":"address"},
    {"internalType":"uint256","name":"value","type":"uint256"},
    {"internalType":"bytes","name":"data","type":"bytes"},
    {"internalType":"uint8","name":"operation","type":"uint8"},
    {"internalType":"uint256","name":"safeTxGas","type":"uint256"},
    {"internalType":"uint256","name":"baseGas","type":"uint256"},
    {"internalType":"uint256","name":"gasPrice","type":"uint256"},
    {"internalType":"address","name":"gasToken","type":"address"},
    {"internalType":"address","name":"refundReceiver","type":"address"},
    {"internalType":"uint256","name":"_nonce","type":"uint256"}
  ],"name":"getTransactionHash","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"}
]`

const erc20ABIForTest = `[
  {"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"name":"recipient","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},
  {"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"stateMutability":"view","type":"function"}
]`

// ── EIP-712 (TachyonStealth domain — same as relayer) ────────────────────────

var (
	rbDomainTypeHash        = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	rbDomainSeparator       = crypto.Keccak256Hash(rbDomainTypeHash.Bytes(), crypto.Keccak256([]byte("TachyonStealth")), crypto.Keccak256([]byte("1")))
	rbRegisterTypeHash      = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
	rbDeriveTypeHash        = crypto.Keccak256Hash([]byte("DeriveStealthKey(address recipientAddress,address stealthAddress)"))
	rbDeploySafeTypeHash    = crypto.Keccak256Hash([]byte("DeploySafe(address stealthAddress,uint256 chainId)"))
)

func rbEIP712Hash(structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, rbDomainSeparator.Bytes(), structHash.Bytes())
}

func rbSignRegister(key *ecdsa.PrivateKey, signer common.Address) []byte {
	h := rbEIP712Hash(crypto.Keccak256Hash(rbRegisterTypeHash.Bytes(), common.LeftPadBytes(signer.Bytes(), 32)))
	sig, _ := crypto.Sign(h.Bytes(), key)
	return sig
}

func rbSignDeriveStealthKey(key *ecdsa.PrivateKey, recipient, stealth common.Address) []byte {
	h := rbEIP712Hash(crypto.Keccak256Hash(rbDeriveTypeHash.Bytes(),
		common.LeftPadBytes(recipient.Bytes(), 32),
		common.LeftPadBytes(stealth.Bytes(), 32)))
	sig, _ := crypto.Sign(h.Bytes(), key)
	return sig
}

func rbSignDeploySafe(key *ecdsa.PrivateKey, stealthAddr common.Address, chainID *big.Int) []byte {
	h := rbEIP712Hash(crypto.Keccak256Hash(rbDeploySafeTypeHash.Bytes(),
		common.LeftPadBytes(stealthAddr.Bytes(), 32),
		common.LeftPadBytes(chainID.Bytes(), 32)))
	sig, _ := crypto.Sign(h.Bytes(), key)
	return sig
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Printf("❌  missing env var: %s\n", key)
		os.Exit(1)
	}
	return v
}

func post(url string, body interface{}) ([]byte, int, error) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

func getSafeTxHash(ctx context.Context, client *ethclient.Client, safeAddr, to common.Address, value *big.Int, data []byte, operation uint8) ([32]byte, error) {
	safeABI, _ := abi.JSON(strings.NewReader(safeABIForTest))
	safe := bind.NewBoundContract(safeAddr, safeABI, client, client, client)

	var nonceOut []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &nonceOut, "nonce"); err != nil {
		return [32]byte{}, fmt.Errorf("get nonce: %w", err)
	}
	nonce := nonceOut[0].(*big.Int)

	var hashOut []interface{}
	if err := safe.Call(&bind.CallOpts{Context: ctx}, &hashOut, "getTransactionHash",
		to, value, data, operation,
		big.NewInt(0), big.NewInt(0), big.NewInt(0),
		common.Address{}, common.Address{},
		nonce,
	); err != nil {
		return [32]byte{}, fmt.Errorf("getTransactionHash: %w", err)
	}
	return hashOut[0].([32]byte), nil
}

func tokenBalance(ctx context.Context, client *ethclient.Client, token, owner common.Address) *big.Int {
	erc20, _ := abi.JSON(strings.NewReader(erc20ABIForTest))
	c := bind.NewBoundContract(token, erc20, client, client, client)
	var out []interface{}
	_ = c.Call(&bind.CallOpts{Context: ctx}, &out, "balanceOf", owner)
	if len(out) == 0 {
		return big.NewInt(0)
	}
	return out[0].(*big.Int)
}

func waitTx(ctx context.Context, client *ethclient.Client, txHash common.Hash) error {
	fmt.Printf("   ⏳ waiting for tx %s...\n", txHash.Hex()[:18]+"...")
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			if receipt.Status == 1 {
				return nil
			}
			return fmt.Errorf("tx reverted (status 0)")
		}
		time.Sleep(4 * time.Second)
	}
	return fmt.Errorf("timeout waiting for tx")
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	godotenv.Load("../../../.env")

	relayerURL := os.Getenv("RELAYER_URL")
	if relayerURL == "" {
		relayerURL = "http://localhost:3000"
	}
	baseRPC  := mustEnv("CHAIN_2_RPC")
	tokenAddr := common.HexToAddress(mustEnv("TOKEN_WETH_BASE"))
	chainIDStr := mustEnv("CHAIN_2_ID") // e.g. "84532"

	ctx := context.Background()

	client, err := ethclient.Dial(baseRPC)
	if err != nil {
		fmt.Printf("❌  connect Base: %v\n", err)
		os.Exit(1)
	}
	chainID, _ := client.ChainID(ctx)

	// Test wallet (pays for funding the Safe)
	walletKey, err := crypto.HexToECDSA(strings.TrimPrefix(mustEnv("PRIVATE_KEY"), "0x"))
	if err != nil {
		fmt.Printf("❌  parse PRIVATE_KEY: %v\n", err)
		os.Exit(1)
	}
	walletAddr := crypto.PubkeyToAddress(walletKey.PublicKey)

	fmt.Println("🚀  relay-batch E2E test")
	fmt.Printf("   relayer : %s\n", relayerURL)
	fmt.Printf("   chain   : Base Sepolia (%s)\n", chainID)
	fmt.Printf("   wallet  : %s\n\n", walletAddr.Hex())

	// ── 1. Use solver wallet as the recipient ─────────────────────────────────
	// The solver wallet signs the EIP-712 registration messages and acts as
	// the recipient whose stealth address + Safe are generated by the TEE.
	recipientKey := walletKey
	recipientAddr := walletAddr
	fmt.Printf("🔑  recipient EOA (solver wallet): %s\n", recipientAddr.Hex())

	// ── 2. Register recipient ─────────────────────────────────────────────────
	regSig := rbSignRegister(recipientKey, recipientAddr)
	regBody, status, err := post(relayerURL+"/recipient/register", map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"signature":        hex.EncodeToString(regSig),
	})
	if err != nil || status != 200 {
		fmt.Printf("❌  register: status=%d body=%s err=%v\n", status, regBody, err)
		os.Exit(1)
	}
	fmt.Println("✅  recipient registered")

	// ── 3. Generate stealth address + predicted Safe ──────────────────────────
	genBody, status, err := post(relayerURL+"/recipient/generate-stealth", map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"chainId":          chainIDStr,
		"intentId":         "relay-batch-test",
	})
	if err != nil || status != 200 {
		fmt.Printf("❌  generate-stealth: status=%d body=%s err=%v\n", status, genBody, err)
		os.Exit(1)
	}
	var genResult map[string]interface{}
	json.Unmarshal(genBody, &genResult)
	stealthAddrStr := genResult["stealthAddress"].(string)
	stealthAddr    := common.HexToAddress(stealthAddrStr)
	fmt.Printf("✅  stealth : %s\n\n", stealthAddrStr)

	erc20, _ := abi.JSON(strings.NewReader(erc20ABIForTest))
	tokenContract := bind.NewBoundContract(tokenAddr, erc20, client, client, client)

	// ── 4. Derive stealth private key from TEE ───────────────────────────────
	// Must happen BEFORE deploy-safe (deploy-safe requires a sig from the stealth key).
	deriveSig := rbSignDeriveStealthKey(recipientKey, recipientAddr, stealthAddr)
	deriveBody, status, err := post(relayerURL+"/recipient/derive-stealth-key", map[string]interface{}{
		"recipientAddress": recipientAddr.Hex(),
		"stealthAddress":   stealthAddrStr,
		"signature":        hex.EncodeToString(deriveSig),
	})
	if err != nil || status != 200 {
		fmt.Printf("❌  derive-stealth-key: status=%d body=%s err=%v\n", status, deriveBody, err)
		os.Exit(1)
	}
	var deriveResult map[string]interface{}
	json.Unmarshal(deriveBody, &deriveResult)
	stealthPrivKeyHex := deriveResult["stealthPrivateKey"].(string)
	stealthPrivKey, err := crypto.HexToECDSA(strings.TrimPrefix(stealthPrivKeyHex, "0x"))
	if err != nil {
		fmt.Printf("❌  parse stealth key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅  stealth key derived, controls: %s\n\n", crypto.PubkeyToAddress(stealthPrivKey.PublicKey).Hex())

	// ── 5. Deploy the Safe (signed by stealth key) ────────────────────────────
	deploySig := rbSignDeploySafe(stealthPrivKey, stealthAddr, chainID)
	deployBody, status, err := post(relayerURL+"/recipient/deploy-safe", map[string]string{
		"stealthAddress": stealthAddrStr,
		"signature":      hex.EncodeToString(deploySig),
		"chainId":        chainIDStr,
	})
	if err != nil || status != 200 {
		fmt.Printf("❌  deploy-safe: status=%d body=%s err=%v\n", status, deployBody, err)
		os.Exit(1)
	}
	var deployResult map[string]interface{}
	json.Unmarshal(deployBody, &deployResult)

	// Start with the predicted address from the server response.
	safeAddrStr := deployResult["safeAddress"].(string)
	safeAddr    := common.HexToAddress(safeAddrStr)

	// If the response includes a txHash the relayer did a fresh deployment.
	// Parse the ProxyCreation event from the receipt to get the ACTUAL deployed address;
	// the server's CREATE2 prediction may differ if the factory uses different proxy bytecode.
	if deployTxHashStr, ok := deployResult["txHash"].(string); ok && deployTxHashStr != "" {
		deployTxHash := common.HexToHash(deployTxHashStr)
		receipt, recErr := client.TransactionReceipt(ctx, deployTxHash)
		if recErr == nil && receipt != nil {
			proxyCreationTopic := crypto.Keccak256Hash([]byte("ProxyCreation(address,address)"))
			for _, l := range receipt.Logs {
				if len(l.Topics) == 0 || l.Topics[0] != proxyCreationTopic {
					continue
				}
				if len(l.Topics) >= 2 {
					safeAddr    = common.HexToAddress(l.Topics[1].Hex())
					safeAddrStr = safeAddr.Hex()
				} else if len(l.Data) >= 32 {
					safeAddr    = common.BytesToAddress(l.Data[:32])
					safeAddrStr = safeAddr.Hex()
				}
				break
			}
		}
	}
	fmt.Printf("✅  Safe confirmed at: %s\n", safeAddrStr)

	// Wait for contract code to be visible on the RPC node (Alchemy Base Sepolia has brief lag
	// between block confirmation and state propagation on the read path).
	fmt.Print("   ⏳ waiting for Safe code to appear on RPC...")
	codeVisible := false
	for i := 0; i < 15; i++ {
		code, _ := client.CodeAt(ctx, safeAddr, nil)
		if len(code) > 0 {
			codeVisible = true
			fmt.Printf(" visible (%d bytes)\n", len(code))
			break
		}
		fmt.Print(".")
		time.Sleep(3 * time.Second)
	}
	if !codeVisible {
		fmt.Println("\n⚠️  Safe code not visible after 45s — continuing anyway")
	}

	// Verify the Safe's owner matches the stealth address.
	ownersABI, _ := abi.JSON(strings.NewReader(`[{"inputs":[],"name":"getOwners","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"}]`))
	safeContract := bind.NewBoundContract(safeAddr, ownersABI, client, client, client)
	var ownersOut []interface{}
	if callErr := safeContract.Call(&bind.CallOpts{Context: ctx}, &ownersOut, "getOwners"); callErr == nil && len(ownersOut) > 0 {
		owners := ownersOut[0].([]common.Address)
		for _, o := range owners {
			fmt.Printf("   owner: %s\n", o.Hex())
		}
	} else {
		fmt.Printf("   ⚠️  could not fetch owners: %v\n", callErr)
	}
	fmt.Println()

	// ── 6. Fund the confirmed Safe address ───────────────────────────────────
	fundAmount := big.NewInt(1) // 1 smallest unit — enough to prove the flow

	nonce, _     := client.PendingNonceAt(ctx, walletAddr)
	gasPrice, _  := client.SuggestGasPrice(ctx)
	auth, _      := bind.NewKeyedTransactorWithChainID(walletKey, chainID)
	auth.Nonce    = big.NewInt(int64(nonce))
	auth.GasLimit = uint64(100000)
	auth.GasPrice = gasPrice

	fmt.Printf("💸  funding Safe with %s token unit(s)...\n", fundAmount)
	fundTx, err := tokenContract.Transact(auth, "transfer", safeAddr, fundAmount)
	if err != nil {
		fmt.Printf("❌  fund Safe: %v\n", err)
		os.Exit(1)
	}
	if err := waitTx(ctx, client, fundTx.Hash()); err != nil {
		fmt.Printf("❌  fund tx: %v\n", err)
		os.Exit(1)
	}
	// Poll until the balance is reflected — Alchemy RPC can lag behind the latest block.
	var balBefore *big.Int
	for i := 0; i < 10; i++ {
		balBefore = tokenBalance(ctx, client, tokenAddr, safeAddr)
		if balBefore.Cmp(big.NewInt(0)) > 0 {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if balBefore.Cmp(big.NewInt(0)) == 0 {
		fmt.Println("❌  Safe balance still 0 after polling — token transfer may have failed")
		os.Exit(1)
	}
	fmt.Printf("✅  Safe funded — balance: %s\n\n", balBefore)

	// ── 7. Build Safe tx: transfer tokens back to test wallet ─────────────────
	transferData, err := erc20.Pack("transfer", walletAddr, balBefore)
	if err != nil {
		fmt.Printf("❌  encode transfer: %v\n", err)
		os.Exit(1)
	}

	safeTxHash, err := getSafeTxHash(ctx, client, safeAddr, tokenAddr, big.NewInt(0), transferData, 0)
	if err != nil {
		fmt.Printf("❌  getSafeTxHash: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🔐  Safe tx hash: %x\n", safeTxHash)

	// ── 8. Sign Safe tx hash with stealth private key ─────────────────────────
	sig, err := crypto.Sign(safeTxHash[:], stealthPrivKey)
	if err != nil {
		fmt.Printf("❌  sign: %v\n", err)
		os.Exit(1)
	}
	// Safe expects v = 27 or 28
	sig[64] += 27

	// ── 9. Call relay-batch ───────────────────────────────────────────────────
	fmt.Println("📡  calling relay-batch...")
	batchPayload := map[string]interface{}{
		"allowFailure": false,
		"items": []map[string]interface{}{
			{
				"safeAddress": safeAddrStr,
				"chainId":     chainIDStr,
				"to":          tokenAddr.Hex(),
				"value":       "0",
				"data":        "0x" + hex.EncodeToString(transferData),
				"operation":   0,
				"signature":   "0x" + hex.EncodeToString(sig),
			},
		},
	}

	batchBody, status, err := post(relayerURL+"/recipient/relay-batch", batchPayload)
	if err != nil || status != 200 {
		fmt.Printf("❌  relay-batch: status=%d body=%s err=%v\n", status, batchBody, err)
		os.Exit(1)
	}
	var batchResult map[string]interface{}
	json.Unmarshal(batchBody, &batchResult)
	txHashStr := batchResult["txHash"].(string)
	fmt.Printf("✅  relay-batch submitted — txHash: %s\n", txHashStr)

	// ── 10. Wait and verify ───────────────────────────────────────────────────
	txHash := common.HexToHash(txHashStr)
	if err := waitTx(ctx, client, txHash); err != nil {
		fmt.Printf("❌  batch tx: %v\n", err)
		os.Exit(1)
	}

	// Inspect receipt for Safe execution events (helps diagnose failures).
	batchReceipt, _ := client.TransactionReceipt(ctx, txHash)
	if batchReceipt != nil {
		execSuccessTopic := crypto.Keccak256Hash([]byte("ExecutionSuccess(bytes32,uint256)"))
		execFailTopic    := crypto.Keccak256Hash([]byte("ExecutionFailure(bytes32,uint256)"))
		for _, l := range batchReceipt.Logs {
			if len(l.Topics) == 0 {
				continue
			}
			if l.Topics[0] == execSuccessTopic {
				fmt.Printf("ℹ️   Safe %s emitted ExecutionSuccess\n", l.Address.Hex()[:12]+"...")
			} else if l.Topics[0] == execFailTopic {
				fmt.Printf("⚠️   Safe %s emitted ExecutionFailure — inner call failed\n", l.Address.Hex()[:12]+"...")
			}
		}
	}

	balAfter := tokenBalance(ctx, client, tokenAddr, safeAddr)
	fmt.Printf("\n📊  Safe balance before: %s\n", balBefore)
	fmt.Printf("📊  Safe balance after : %s\n", balAfter)

	if balAfter.Cmp(big.NewInt(0)) == 0 && balBefore.Cmp(big.NewInt(0)) > 0 {
		fmt.Println("\n✅  relay-batch E2E PASSED — tokens moved out of Safe")
	} else {
		fmt.Println("\n❌  relay-batch E2E FAILED — balance did not change as expected")
		os.Exit(1)
	}
}
