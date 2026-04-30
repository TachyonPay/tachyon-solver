package main

// Standalone relay-batch E2E test — 3 Safes in one batch.
//
// Run with:
//   cd tachyon-prod/test/e2e/relay_batch
//   go run main.go
//
// What it tests:
//   1. Register a single recipient (solver wallet) with the relayer
//   2. For each of 3 stealth slots:
//        a. generate-stealth  → unique stealth address
//        b. derive-stealth-key → stealth private key
//        c. deploy-safe        → 1-of-1 Gnosis Safe owned by that stealth key
//        d. fund Safe          → transfer 1 token unit into the Safe
//   3. Build one Safe tx per Safe (transfer tokens back to test wallet)
//   4. Sign each tx hash with its stealth key
//   5. POST /recipient/relay-batch with all 3 items → one on-chain tx via Multicall3
//   6. Verify all 3 Safe balances moved from 1 → 0
//
// Prerequisites:
//   - Relayer running at RELAYER_URL
//   - Test wallet (PRIVATE_KEY) holds TOKEN_WETH_BASE on Base Sepolia
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

const numSafes = 3

// safeSlot holds per-Safe state accumulated across the setup steps.
type safeSlot struct {
	idx            int
	stealthAddr    common.Address
	stealthAddrStr string
	stealthPrivKey *ecdsa.PrivateKey
	safeAddr       common.Address
	safeAddrStr    string
	balBefore      *big.Int
	transferData   []byte
	sig            []byte
}

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
  {"inputs":[{"name":"recipient","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}
]`

// ── EIP-712 helpers (TachyonStealth domain) ───────────────────────────────────

var (
	rbDomainTypeHash     = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	rbDomainSeparator    = crypto.Keccak256Hash(rbDomainTypeHash.Bytes(), crypto.Keccak256([]byte("TachyonStealth")), crypto.Keccak256([]byte("1")))
	rbRegisterTypeHash   = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
	rbDeriveTypeHash     = crypto.Keccak256Hash([]byte("DeriveStealthKey(address recipientAddress,address stealthAddress)"))
	rbDeploySafeTypeHash = crypto.Keccak256Hash([]byte("DeploySafe(address stealthAddress,uint256 chainId)"))
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

// ── misc helpers ──────────────────────────────────────────────────────────────

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

// waitCodeVisible polls eth_getCode until the contract is visible on the node.
func waitCodeVisible(ctx context.Context, client *ethclient.Client, addr common.Address) {
	fmt.Print("   ⏳ waiting for contract code on RPC...")
	for i := 0; i < 15; i++ {
		code, _ := client.CodeAt(ctx, addr, nil)
		if len(code) > 0 {
			fmt.Printf(" visible (%d bytes)\n", len(code))
			return
		}
		fmt.Print(".")
		time.Sleep(3 * time.Second)
	}
	fmt.Println("\n   ⚠️  code not visible after 45s — continuing")
}

// deploySafeAndGetAddr calls deploy-safe and extracts the actual deployed address
// from the ProxyCreation event in the receipt (bypasses CREATE2 prediction mismatch).
func deploySafeAndGetAddr(ctx context.Context, client *ethclient.Client, relayerURL string, s *safeSlot, chainID *big.Int, chainIDStr string) {
	deploySig := rbSignDeploySafe(s.stealthPrivKey, s.stealthAddr, chainID)
	deployBody, status, err := post(relayerURL+"/recipient/deploy-safe", map[string]string{
		"stealthAddress": s.stealthAddrStr,
		"signature":      hex.EncodeToString(deploySig),
		"chainId":        chainIDStr,
	})
	if err != nil || status != 200 {
		fmt.Printf("❌  [safe %d] deploy-safe: status=%d body=%s err=%v\n", s.idx+1, status, deployBody, err)
		os.Exit(1)
	}
	var result map[string]interface{}
	json.Unmarshal(deployBody, &result)

	s.safeAddrStr = result["safeAddress"].(string)
	s.safeAddr = common.HexToAddress(s.safeAddrStr)

	if txHashStr, ok := result["txHash"].(string); ok && txHashStr != "" {
		receipt, recErr := client.TransactionReceipt(ctx, common.HexToHash(txHashStr))
		if recErr == nil && receipt != nil {
			proxyCreationTopic := crypto.Keccak256Hash([]byte("ProxyCreation(address,address)"))
			for _, l := range receipt.Logs {
				if len(l.Topics) == 0 || l.Topics[0] != proxyCreationTopic {
					continue
				}
				if len(l.Topics) >= 2 {
					s.safeAddr = common.HexToAddress(l.Topics[1].Hex())
					s.safeAddrStr = s.safeAddr.Hex()
				} else if len(l.Data) >= 32 {
					s.safeAddr = common.BytesToAddress(l.Data[:32])
					s.safeAddrStr = s.safeAddr.Hex()
				}
				break
			}
		}
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	godotenv.Load("../../../.env")

	relayerURL := os.Getenv("RELAYER_URL")
	if relayerURL == "" {
		relayerURL = "http://localhost:3000"
	}
	baseRPC    := mustEnv("CHAIN_2_RPC")
	tokenAddr  := common.HexToAddress(mustEnv("TOKEN_WETH_BASE"))
	chainIDStr := mustEnv("CHAIN_2_ID")

	ctx := context.Background()

	client, err := ethclient.Dial(baseRPC)
	if err != nil {
		fmt.Printf("❌  connect Base: %v\n", err)
		os.Exit(1)
	}
	chainID, _ := client.ChainID(ctx)

	walletKey, err := crypto.HexToECDSA(strings.TrimPrefix(mustEnv("PRIVATE_KEY"), "0x"))
	if err != nil {
		fmt.Printf("❌  parse PRIVATE_KEY: %v\n", err)
		os.Exit(1)
	}
	walletAddr    := crypto.PubkeyToAddress(walletKey.PublicKey)
	recipientKey  := walletKey
	recipientAddr := walletAddr

	erc20, _          := abi.JSON(strings.NewReader(erc20ABIForTest))
	tokenContract     := bind.NewBoundContract(tokenAddr, erc20, client, client, client)
	ownersABI, _      := abi.JSON(strings.NewReader(`[{"inputs":[],"name":"getOwners","outputs":[{"internalType":"address[]","name":"","type":"address[]"}],"stateMutability":"view","type":"function"}]`))

	fmt.Printf("🚀  relay-batch E2E test (%d Safes)\n", numSafes)
	fmt.Printf("   relayer : %s\n", relayerURL)
	fmt.Printf("   chain   : Base Sepolia (%s)\n", chainID)
	fmt.Printf("   wallet  : %s\n\n", walletAddr.Hex())

	// ── 1. Register recipient ─────────────────────────────────────────────────
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

	// ── 2. Set up each Safe slot ──────────────────────────────────────────────
	slots := make([]safeSlot, numSafes)

	for i := range slots {
		s := &slots[i]
		s.idx = i
		fmt.Printf("\n─── Safe %d of %d ───────────────────────────────────────\n", i+1, numSafes)

		// a. generate-stealth
		genBody, status, err := post(relayerURL+"/recipient/generate-stealth", map[string]string{
			"recipientAddress": recipientAddr.Hex(),
			"chainId":          chainIDStr,
			"intentId":         fmt.Sprintf("relay-batch-test-%d", i+1),
		})
		if err != nil || status != 200 {
			fmt.Printf("❌  generate-stealth: status=%d body=%s err=%v\n", status, genBody, err)
			os.Exit(1)
		}
		var genResult map[string]interface{}
		json.Unmarshal(genBody, &genResult)
		s.stealthAddrStr = genResult["stealthAddress"].(string)
		s.stealthAddr    = common.HexToAddress(s.stealthAddrStr)
		fmt.Printf("   stealth : %s\n", s.stealthAddrStr)

		// b. derive-stealth-key
		deriveSig := rbSignDeriveStealthKey(recipientKey, recipientAddr, s.stealthAddr)
		deriveBody, status, err := post(relayerURL+"/recipient/derive-stealth-key", map[string]interface{}{
			"recipientAddress": recipientAddr.Hex(),
			"stealthAddress":   s.stealthAddrStr,
			"signature":        hex.EncodeToString(deriveSig),
		})
		if err != nil || status != 200 {
			fmt.Printf("❌  derive-stealth-key: status=%d body=%s err=%v\n", status, deriveBody, err)
			os.Exit(1)
		}
		var deriveResult map[string]interface{}
		json.Unmarshal(deriveBody, &deriveResult)
		privHex := deriveResult["stealthPrivateKey"].(string)
		s.stealthPrivKey, err = crypto.HexToECDSA(strings.TrimPrefix(privHex, "0x"))
		if err != nil {
			fmt.Printf("❌  parse stealth key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("   key     : controls %s\n", crypto.PubkeyToAddress(s.stealthPrivKey.PublicKey).Hex())

		// c. deploy-safe
		deploySafeAndGetAddr(ctx, client, relayerURL, s, chainID, chainIDStr)
		fmt.Printf("   safe    : %s\n", s.safeAddrStr)

		// Wait for contract code to be queryable on the RPC node.
		waitCodeVisible(ctx, client, s.safeAddr)

		// Print owners for visibility.
		safeContract := bind.NewBoundContract(s.safeAddr, ownersABI, client, client, client)
		var ownersOut []interface{}
		if callErr := safeContract.Call(&bind.CallOpts{Context: ctx}, &ownersOut, "getOwners"); callErr == nil && len(ownersOut) > 0 {
			for _, o := range ownersOut[0].([]common.Address) {
				fmt.Printf("   owner   : %s\n", o.Hex())
			}
		}

		// d. fund Safe
		walletNonce, _ := client.PendingNonceAt(ctx, walletAddr)
		gasPrice, _    := client.SuggestGasPrice(ctx)
		auth, _        := bind.NewKeyedTransactorWithChainID(walletKey, chainID)
		auth.Nonce      = big.NewInt(int64(walletNonce))
		auth.GasLimit   = uint64(100000)
		auth.GasPrice   = gasPrice

		fundTx, err := tokenContract.Transact(auth, "transfer", s.safeAddr, big.NewInt(1))
		if err != nil {
			fmt.Printf("❌  fund Safe %d: %v\n", i+1, err)
			os.Exit(1)
		}
		if err := waitTx(ctx, client, fundTx.Hash()); err != nil {
			fmt.Printf("❌  fund tx Safe %d: %v\n", i+1, err)
			os.Exit(1)
		}

		// Poll until balance is visible (RPC propagation lag).
		for j := 0; j < 10; j++ {
			s.balBefore = tokenBalance(ctx, client, tokenAddr, s.safeAddr)
			if s.balBefore.Cmp(big.NewInt(0)) > 0 {
				break
			}
			time.Sleep(3 * time.Second)
		}
		if s.balBefore.Cmp(big.NewInt(0)) == 0 {
			fmt.Printf("❌  Safe %d balance still 0 after polling\n", i+1)
			os.Exit(1)
		}
		fmt.Printf("   funded  : balance = %s\n", s.balBefore)
	}

	// ── 3. Build Safe txs and sign ────────────────────────────────────────────
	fmt.Printf("\n─── Building and signing %d Safe txs ───────────────────\n", numSafes)
	for i := range slots {
		s := &slots[i]

		txData, err := erc20.Pack("transfer", walletAddr, s.balBefore)
		if err != nil {
			fmt.Printf("❌  encode transfer [%d]: %v\n", i+1, err)
			os.Exit(1)
		}
		s.transferData = txData

		safeTxHash, err := getSafeTxHash(ctx, client, s.safeAddr, tokenAddr, big.NewInt(0), txData, 0)
		if err != nil {
			fmt.Printf("❌  getSafeTxHash [%d]: %v\n", i+1, err)
			os.Exit(1)
		}

		rawSig, err := crypto.Sign(safeTxHash[:], s.stealthPrivKey)
		if err != nil {
			fmt.Printf("❌  sign [%d]: %v\n", i+1, err)
			os.Exit(1)
		}
		rawSig[64] += 27 // Safe expects v = 27 or 28
		s.sig = rawSig

		fmt.Printf("   [%d] hash=%x  sig_v=%d\n", i+1, safeTxHash[:8], rawSig[64])
	}

	// ── 4. POST relay-batch with all 3 items ──────────────────────────────────
	items := make([]map[string]interface{}, numSafes)
	for i, s := range slots {
		items[i] = map[string]interface{}{
			"safeAddress": s.safeAddrStr,
			"chainId":     chainIDStr,
			"to":          tokenAddr.Hex(),
			"value":       "0",
			"data":        "0x" + hex.EncodeToString(s.transferData),
			"operation":   0,
			"signature":   "0x" + hex.EncodeToString(s.sig),
		}
	}
	batchPayload := map[string]interface{}{
		"allowFailure": false,
		"items":        items,
	}

	fmt.Printf("\n📡  calling relay-batch with %d items...\n", numSafes)
	batchBody, status, err := post(relayerURL+"/recipient/relay-batch", batchPayload)
	if err != nil || status != 200 {
		fmt.Printf("❌  relay-batch: status=%d body=%s err=%v\n", status, batchBody, err)
		os.Exit(1)
	}
	var batchResult map[string]interface{}
	json.Unmarshal(batchBody, &batchResult)
	txHashStr := batchResult["txHash"].(string)
	fmt.Printf("✅  relay-batch submitted — txHash: %s\n", txHashStr)

	// ── 5. Wait for batch tx and inspect receipt ──────────────────────────────
	batchTxHash := common.HexToHash(txHashStr)
	if err := waitTx(ctx, client, batchTxHash); err != nil {
		fmt.Printf("❌  batch tx: %v\n", err)
		os.Exit(1)
	}

	receipt, _ := client.TransactionReceipt(ctx, batchTxHash)
	if receipt != nil {
		execSuccessTopic := crypto.Keccak256Hash([]byte("ExecutionSuccess(bytes32,uint256)"))
		execFailTopic    := crypto.Keccak256Hash([]byte("ExecutionFailure(bytes32,uint256)"))
		for _, l := range receipt.Logs {
			if len(l.Topics) == 0 {
				continue
			}
			switch l.Topics[0] {
			case execSuccessTopic:
				fmt.Printf("ℹ️   Safe %s  ExecutionSuccess\n", l.Address.Hex()[:14]+"...")
			case execFailTopic:
				fmt.Printf("⚠️   Safe %s  ExecutionFailure — inner call failed\n", l.Address.Hex()[:14]+"...")
			}
		}
	}

	// ── 6. Verify all balances ────────────────────────────────────────────────
	fmt.Println()
	passed := 0
	for i, s := range slots {
		balAfter := tokenBalance(ctx, client, tokenAddr, s.safeAddr)
		moved := balAfter.Cmp(big.NewInt(0)) == 0 && s.balBefore.Cmp(big.NewInt(0)) > 0
		status := "✅"
		if !moved {
			status = "❌"
		} else {
			passed++
		}
		fmt.Printf("%s  Safe %d  before=%-3s  after=%-3s  %s\n",
			status, i+1, s.balBefore, balAfter,
			map[bool]string{true: "tokens moved", false: "FAILED — balance unchanged"}[moved])
	}

	fmt.Println()
	if passed == numSafes {
		fmt.Printf("✅  relay-batch E2E PASSED — all %d Safes drained in one tx\n", numSafes)
	} else {
		fmt.Printf("❌  relay-batch E2E FAILED — %d/%d Safes passed\n", passed, numSafes)
		os.Exit(1)
	}
}
