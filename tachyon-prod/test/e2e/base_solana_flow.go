// Self-contained e2e test that runs ONLY Base ↔ Solana intent flows.
//
//   Test 1: Base   -> Solana (mock USDC on Base -> SPL test-USDC on devnet)
//   Test 2: Solana -> Base   (SPL test-USDC on devnet -> mock USDC on Base)
//
// No dependency on complete_flow.go / complete_flow_solana.go. Run with:
//
//   go run base_solana_flow.go
//
// Reads from tachyon-prod/.env via godotenv. Required env:
//   PRIVATE_KEY, CHAIN_2_RPC, CHAIN_2_BRIDGE, TOKEN_USDC_BASE
//   SOLVER_SOLANA_PRIVATE_KEY, SOLANA_PROGRAM_ID, SOLANA_USDC_MINT
//   SOLANA_RPC_URL (or CHAIN_3_RPC / CHAIN_4_RPC), SOLANA_CHAIN_ID
//   TEST_AMOUNT_A, TEST_AMOUNT_B, TEST_REWARD, TEST_AUCTION_DURATION

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	ecies "github.com/ecies/go/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/joho/godotenv"

	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon-solver/tachyon-prod/test/e2e/utils"
)

// ----- ECIES + /store-recipients helpers (mirror of complete_flow.go) -----

func bsFetchECIESPubkey(relayerURL string) (*ecies.PublicKey, error) {
	resp, err := http.Get(relayerURL + "/ecies-pubkey")
	if err != nil {
		return nil, fmt.Errorf("GET /ecies-pubkey: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("/ecies-pubkey returned %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Success   bool   `json:"success"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode pubkey: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("/ecies-pubkey not successful")
	}
	pkBytes, err := hex.DecodeString(result.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid pubkey hex: %w", err)
	}
	return ecies.NewPublicKeyFromBytes(pkBytes)
}

func bsEncryptRecipients(pk *ecies.PublicKey, recipients, amounts []string) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{"recipients": recipients, "amounts": amounts})
	enc, err := ecies.Encrypt(pk, payload)
	if err != nil {
		return "", fmt.Errorf("ecies encrypt: %w", err)
	}
	return hex.EncodeToString(enc), nil
}

// ----- Salt register / generate / retrieve helpers (deterministic flow) -----
//
// Mirrors the EVM stealth flow:
//   1. Recipient calls /register once (ed25519 sig) → relayer creates viewSecret
//   2. Sender asks /generate-salt per intent → relayer derives + returns salt
//   3. Recipient retrieves via /get-salts (ed25519 sig) → relayer re-derives
//
// The salt is never persisted at the relayer in this flow — only the
// viewSecret + nonce metadata.

type bsRegisterRecipientResp struct {
	Success         bool   `json:"success"`
	RecipientPubkey string `json:"recipientPubkey"`
	AlreadyExists   bool   `json:"alreadyExists"`
	Error           string `json:"error,omitempty"`
}

// bsRegisterSolanaRecipient signs and POSTs the canonical register
// message. Idempotent — returns AlreadyExists=true if already set up.
func bsRegisterSolanaRecipient(relayerURL string, recipientKey solana.PrivateKey) (*bsRegisterRecipientResp, error) {
	pk := recipientKey.PublicKey().String()
	ts := time.Now().Unix()
	msg := []byte(fmt.Sprintf("REGISTER_SOLANA_STEALTH:%s:%d", pk, ts))
	sig, err := recipientKey.Sign(msg)
	if err != nil {
		return nil, fmt.Errorf("sign register msg: %w", err)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"recipientPubkey": pk,
		"timestamp":       ts,
		"signature":       hex.EncodeToString(sig[:]),
	})
	resp, err := http.Post(relayerURL+"/solana/recipient/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("/solana/recipient/register returned %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed bsRegisterRecipientResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode register response: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("register not successful: %s", parsed.Error)
	}
	return &parsed, nil
}

type bsGenerateSaltResp struct {
	Success    bool   `json:"success"`
	Salt       string `json:"salt"`
	Nonce      uint64 `json:"nonce"`
	StealthPDA string `json:"stealthPda"`
	Error      string `json:"error,omitempty"`
}

// bsGenerateSolanaSalt asks the relayer for the next deterministic salt
// for (recipient, intentID, chainID). Unauthenticated.
func bsGenerateSolanaSalt(relayerURL, recipientPubkey, intentID, chainID, programID string) (*bsGenerateSaltResp, error) {
	body, _ := json.Marshal(map[string]string{
		"recipientPubkey": recipientPubkey,
		"intentId":        intentID,
		"chainId":         chainID,
		"programId":       programID,
	})
	resp, err := http.Post(relayerURL+"/solana/recipient/generate-salt", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("/solana/recipient/generate-salt returned %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed bsGenerateSaltResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode generate-salt response: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("generate-salt not successful: %s", parsed.Error)
	}
	return &parsed, nil
}

// ----- Legacy salt store helpers (kept for backward compat) -----

type bsStoreSaltReq struct {
	RecipientPubkey string `json:"recipientPubkey"`
	IntentID        string `json:"intentId"`
	ChainID         string `json:"chainId"`
	Salt            string `json:"salt"`
	StealthPDA      string `json:"stealthPda"`
}

type bsSaltRecord struct {
	IntentID   string `json:"intentId"`
	ChainID    string `json:"chainId"`
	Salt       string `json:"salt"`
	StealthPDA string `json:"stealthPda"`
	CreatedAt  int64  `json:"createdAt"`
}

type bsGetSaltsResp struct {
	Success bool           `json:"success"`
	Records []bsSaltRecord `json:"records"`
	Error   string         `json:"error,omitempty"`
}

// bsPostStoreSalt persists (intentID, salt, stealthPDA) at the relayer
// for the given recipient. No signature required to store — leaking the
// salt allows only correlation, not theft (sweep_stealth still requires
// the recipient's signature).
func bsPostStoreSalt(relayerURL string, req bsStoreSaltReq) error {
	body, _ := json.Marshal(req)
	resp, err := http.Post(relayerURL+"/solana/recipient/store-salt", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/solana/recipient/store-salt returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// bsFetchSaltsFromRelayer signs an ed25519 retrieval request with the
// recipient's wallet and POSTs to /solana/recipient/get-salts. The
// canonical message format is "GET_SOLANA_STEALTH:<pubkey>:<timestamp>".
func bsFetchSaltsFromRelayer(relayerURL string, recipientKey solana.PrivateKey) ([]bsSaltRecord, error) {
	pk := recipientKey.PublicKey().String()
	ts := time.Now().Unix()
	msg := []byte(fmt.Sprintf("GET_SOLANA_STEALTH:%s:%d", pk, ts))
	sig, err := recipientKey.Sign(msg)
	if err != nil {
		return nil, fmt.Errorf("sign get-salts msg: %w", err)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"recipientPubkey": pk,
		"timestamp":       ts,
		"signature":       hex.EncodeToString(sig[:]),
	})
	resp, err := http.Post(relayerURL+"/solana/recipient/get-salts", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("/solana/recipient/get-salts returned %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed bsGetSaltsResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode get-salts response: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("get-salts not successful: %s", parsed.Error)
	}
	return parsed.Records, nil
}

// bsComputeSplits computes per-recipient split amounts that sum to totalRaw.
// When N==3, honors TEST_RECIPIENT_{1,2,3}_AMOUNT from cfg (matching
// complete_flow.go); otherwise evenly splits and dumps any remainder into
// the last bucket so the sum is exact.
func bsComputeSplits(n int, totalRaw *big.Int, cfg *bsConfig) []*big.Int {
	out := make([]*big.Int, n)
	if n == 3 {
		// Use TEST_RECIPIENT_*_AMOUNT (units, before decimals). The split
		// uses tokenB's decimals (6 for SPL USDC).
		decimals := 6
		r1 := strings.TrimSpace(os.Getenv("TEST_RECIPIENT_1_AMOUNT"))
		r2 := strings.TrimSpace(os.Getenv("TEST_RECIPIENT_2_AMOUNT"))
		r3 := strings.TrimSpace(os.Getenv("TEST_RECIPIENT_3_AMOUNT"))
		if r1 != "" && r2 != "" && r3 != "" {
			out[0] = utils.ParseToken(r1, decimals)
			out[1] = utils.ParseToken(r2, decimals)
			out[2] = utils.ParseToken(r3, decimals)
			sum := new(big.Int).Add(new(big.Int).Add(out[0], out[1]), out[2])
			if sum.Cmp(totalRaw) == 0 {
				return out
			}
			fmt.Printf("⚠️  TEST_RECIPIENT_*_AMOUNT sum (%s) != totalAmountB (%s); using even split\n", sum, totalRaw)
		}
	}
	// Even split with remainder → last bucket.
	share := new(big.Int).Quo(totalRaw, big.NewInt(int64(n)))
	for i := 0; i < n-1; i++ {
		out[i] = new(big.Int).Set(share)
	}
	accounted := new(big.Int).Mul(share, big.NewInt(int64(n-1)))
	out[n-1] = new(big.Int).Sub(totalRaw, accounted)
	return out
}

// bsStoreRecipientsEncrypted encrypts and POSTs the recipient list keyed
// to (intentID, sourceChainID). The relayer decrypts with its ECIES
// private key and stores it for the winning solver to fetch later.
//
// sourceChainID is the chain that ORIGINATED the intent (Base for test 1,
// Solana for test 2). The relayer uses this to key the recipient store.
func bsStoreRecipientsEncrypted(relayerURL, intentID, sourceChainID string, recipients, amounts []string) error {
	pk, err := bsFetchECIESPubkey(relayerURL)
	if err != nil {
		return err
	}
	encrypted, err := bsEncryptRecipients(pk, recipients, amounts)
	if err != nil {
		return err
	}
	chainID, _ := strconv.ParseInt(sourceChainID, 10, 64)
	body, _ := json.Marshal(map[string]interface{}{
		"intentId":      intentID,
		"chainId":       chainID,
		"encryptedData": encrypted,
	})
	resp, err := http.Post(relayerURL+"/store-recipients", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST /store-recipients: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/store-recipients returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ----- Self-contained ABIs (kept minimal — only the fns this test uses) -----

const bsBridgeABI = `[
{"inputs":[],"name":"getLatestIntentId","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
{"inputs":[{"internalType":"uint256","name":"_sourceChainId","type":"uint256"},{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"isIntentSolvedOnChain2","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},
{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getIntent","outputs":[{"components":[{"internalType":"uint256","name":"intentId","type":"uint256"},{"internalType":"address","name":"user","type":"address"},{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"},{"internalType":"uint256","name":"amountA","type":"uint256"},{"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"reward","type":"uint256"},{"internalType":"uint256","name":"destChainId","type":"uint256"},{"internalType":"uint256","name":"endTime","type":"uint256"},{"internalType":"address","name":"winningSolver","type":"address"},{"internalType":"uint256","name":"winningBid","type":"uint256"},{"internalType":"bool","name":"completed","type":"bool"},{"internalType":"bool","name":"deposited","type":"bool"}],"internalType":"struct BridgeIntent.Intent","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},
{"inputs":[{"internalType":"address","name":"_tokenA","type":"address"},{"internalType":"bytes32","name":"_tokenBMint","type":"bytes32"},{"internalType":"uint256","name":"_amountA","type":"uint256"},{"internalType":"uint256","name":"_expectedAmountB","type":"uint256"},{"internalType":"uint256","name":"_reward","type":"uint256"},{"internalType":"uint64","name":"_destSolanaEID","type":"uint64"},{"internalType":"bytes32","name":"_recipient","type":"bytes32"},{"internalType":"uint256","name":"_auctionDuration","type":"uint256"}],"name":"createIntentToSolana","outputs":[],"stateMutability":"nonpayable","type":"function"},
{"inputs":[{"internalType":"uint256","name":"_intentId","type":"uint256"}],"name":"getSolanaIntentExt","outputs":[{"internalType":"bytes32","name":"tokenBMint","type":"bytes32"},{"internalType":"uint64","name":"destSolanaEID","type":"uint64"},{"internalType":"bytes32","name":"recipient","type":"bytes32"},{"internalType":"bool","name":"isSolanaDestIntent","type":"bool"}],"stateMutability":"view","type":"function"}
]`

const bsERC20ABI = `[
{"inputs":[{"internalType":"address","name":"spender","type":"address"},{"internalType":"uint256","name":"amount","type":"uint256"}],"name":"approve","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},
{"inputs":[],"name":"decimals","outputs":[{"internalType":"uint8","name":"","type":"uint8"}],"stateMutability":"view","type":"function"}
]`

// ----- Config -----

type bsConfig struct {
	BaseRPC         string
	BaseBridgeAddr  string
	BaseTokenUSDC   string
	PrivateKey      string

	SolanaRPC        string
	SolanaWS         string
	SolanaProgramID  string
	SolanaUSDCMint   string
	SolanaWSOLMint   string
	SolanaChainID    uint64
	UserSolanaSecret string

	AmountA         string
	AmountB         string
	Reward          string
	AuctionDuration int64
}

func bsLoadConfig() (*bsConfig, error) {
	// Overload (not Load) so values in .env always take precedence over
	// stale shell exports — without this, a previously-exported old
	// CHAIN_2_BRIDGE (etc.) would shadow the updated .env value.
	if err := godotenv.Overload("../../.env"); err != nil {
		fmt.Printf("Warning: failed to load ../../.env: %v\n", err)
	}

	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}

	cfg := &bsConfig{
		BaseRPC:        pick("CHAIN_2_RPC"),
		BaseBridgeAddr: pick("CHAIN_2_BRIDGE"),
		BaseTokenUSDC:  pick("TOKEN_USDC_BASE"),
		PrivateKey:     pick("PRIVATE_KEY"),

		SolanaRPC:        pick("CHAIN_4_RPC", "CHAIN_3_RPC", "SOLANA_RPC_URL"),
		SolanaWS:         pick("CHAIN_4_HELIUS_RPC", "CHAIN_3_HELIUS_RPC", "SOLANA_WS_URL"),
		SolanaProgramID:  pick("CHAIN_4_SOLANA_PROGRAM_ID", "CHAIN_3_SOLANA_PROGRAM_ID", "SOLANA_PROGRAM_ID"),
		SolanaUSDCMint:   pick("CHAIN_4_SOLANA_USDC_MINT", "CHAIN_3_SOLANA_USDC_MINT", "SOLANA_USDC_MINT"),
		SolanaWSOLMint:   pick("CHAIN_4_SOLANA_WSOL_MINT", "CHAIN_3_SOLANA_WSOL_MINT", "SOLANA_WSOL_MINT"),
		UserSolanaSecret: pick("USER_SOLANA_PRIVATE_KEY", "SOLVER_SOLANA_PRIVATE_KEY"),

		AmountA: pick("TEST_AMOUNT_A"),
		AmountB: pick("TEST_AMOUNT_B"),
		Reward:  pick("TEST_REWARD"),
	}

	if cfg.SolanaWSOLMint == "" {
		cfg.SolanaWSOLMint = "So11111111111111111111111111111111111111112"
	}
	if cfg.SolanaRPC == "" {
		cfg.SolanaRPC = rpc.DevNet_RPC
	}
	if cfg.SolanaWS == "" {
		cfg.SolanaWS = rpc.DevNet_WS
	}
	if cfg.AmountA == "" {
		cfg.AmountA = "100"
	}
	if cfg.AmountB == "" {
		cfg.AmountB = "95"
	}
	if cfg.Reward == "" {
		cfg.Reward = "5"
	}

	chainStr := pick("CHAIN_4_ID", "CHAIN_3_ID", "SOLANA_CHAIN_ID")
	if chainStr == "" {
		chainStr = "40168"
	}
	id, err := strconv.ParseUint(chainStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid solana chain id %q: %w", chainStr, err)
	}
	cfg.SolanaChainID = id

	durStr := strings.TrimSpace(os.Getenv("TEST_AUCTION_DURATION"))
	if durStr == "" {
		cfg.AuctionDuration = 100
	} else {
		n, err := strconv.ParseInt(durStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TEST_AUCTION_DURATION: %w", err)
		}
		cfg.AuctionDuration = n
	}

	missing := []string{}
	if cfg.BaseRPC == "" {
		missing = append(missing, "CHAIN_2_RPC")
	}
	if cfg.BaseBridgeAddr == "" {
		missing = append(missing, "CHAIN_2_BRIDGE")
	}
	if cfg.BaseTokenUSDC == "" {
		missing = append(missing, "TOKEN_USDC_BASE")
	}
	if cfg.PrivateKey == "" {
		missing = append(missing, "PRIVATE_KEY")
	}
	if cfg.SolanaProgramID == "" {
		missing = append(missing, "SOLANA_PROGRAM_ID")
	}
	if cfg.SolanaUSDCMint == "" {
		missing = append(missing, "SOLANA_USDC_MINT")
	}
	if cfg.UserSolanaSecret == "" {
		missing = append(missing, "SOLVER_SOLANA_PRIVATE_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// ----- Result type -----

type bsResult struct {
	Direction       string
	IntentID        *big.Int
	SourceTxHash    string
	SourceCompleted bool
	DestVerified    bool
	Note            string
	Err             error
}

// ----- Helpers -----

// ----- Balance snapshot helpers -----

// bsFetchERC20Balance returns the raw balance of holder for the given ERC20.
func bsFetchERC20Balance(ctx context.Context, client *ethclient.Client, tokenAddr string, holder common.Address) *big.Int {
	parsed, _ := abi.JSON(strings.NewReader(bsERC20BalanceABI))
	c := bind.NewBoundContract(common.HexToAddress(tokenAddr), parsed, client, client, client)
	var out []interface{}
	if err := c.Call(&bind.CallOpts{Context: ctx}, &out, "balanceOf", holder); err != nil || len(out) == 0 {
		return big.NewInt(0)
	}
	if v, ok := out[0].(*big.Int); ok {
		return v
	}
	return big.NewInt(0)
}

// bsFetchSPLBalance returns the raw token balance at ata. If the account
// doesn't exist (no token transfers yet), returns 0.
func bsFetchSPLBalance(ctx context.Context, rpcCli *rpc.Client, ata solana.PublicKey) *big.Int {
	bal, err := rpcCli.GetTokenAccountBalance(ctx, ata, rpc.CommitmentConfirmed)
	if err != nil || bal == nil || bal.Value == nil {
		return big.NewInt(0)
	}
	v, ok := new(big.Int).SetString(bal.Value.Amount, 10)
	if !ok {
		return big.NewInt(0)
	}
	return v
}

// bsFmtBal formats a raw integer balance with the given decimals for
// display, keeping the raw value alongside.
func bsFmtBal(raw *big.Int, decimals int) string {
	if raw == nil {
		return "0 (raw=0)"
	}
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole := new(big.Int).Quo(raw, div)
	rem := new(big.Int).Mod(raw, div)
	return fmt.Sprintf("%s.%0*s (raw=%s)", whole.String(), decimals, rem.String(), raw.String())
}

func bsDelta(before, after *big.Int) string {
	d := new(big.Int).Sub(after, before)
	sign := "+"
	if d.Sign() < 0 {
		sign = ""
	}
	return sign + d.String()
}

const bsERC20BalanceABI = `[{"inputs":[{"internalType":"address","name":"account","type":"address"}],"name":"balanceOf","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

func bsFetchTokenDecimals(ctx context.Context, client *ethclient.Client, tokenAddr string) int {
	parsed, _ := abi.JSON(strings.NewReader(bsERC20ABI))
	c := bind.NewBoundContract(common.HexToAddress(tokenAddr), parsed, client, client, client)
	var out []interface{}
	if err := c.Call(&bind.CallOpts{Context: ctx}, &out, "decimals"); err != nil || len(out) == 0 {
		return 18
	}
	return int(out[0].(uint8))
}

func bsCheckIntentCompleted(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int) bool {
	var result []interface{}
	if err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "getIntent", intentID); err != nil || len(result) == 0 {
		return false
	}
	intent, ok := result[0].(struct {
		IntentId        *big.Int       `json:"intentId"`
		User            common.Address `json:"user"`
		TokenA          common.Address `json:"tokenA"`
		TokenB          common.Address `json:"tokenB"`
		AmountA         *big.Int       `json:"amountA"`
		ExpectedAmountB *big.Int       `json:"expectedAmountB"`
		Reward          *big.Int       `json:"reward"`
		DestChainId     *big.Int       `json:"destChainId"`
		EndTime         *big.Int       `json:"endTime"`
		WinningSolver   common.Address `json:"winningSolver"`
		WinningBid      *big.Int       `json:"winningBid"`
		Completed       bool           `json:"completed"`
		Deposited       bool           `json:"deposited"`
	})
	if !ok {
		return false
	}
	return intent.Completed
}

func bsCheckIntentSolved(ctx context.Context, bridge *bind.BoundContract, sourceChainID, intentID *big.Int) bool {
	var result []interface{}
	if err := bridge.Call(&bind.CallOpts{Context: ctx}, &result, "isIntentSolvedOnChain2", sourceChainID, intentID); err != nil || len(result) == 0 {
		return false
	}
	return result[0].(bool)
}

func bsSolanaPubkeyTo32(pk solana.PublicKey) [32]byte {
	var out [32]byte
	copy(out[:], pk.Bytes())
	return out
}

func bsEVMAddrTo32(addr common.Address) [32]byte {
	var out [32]byte
	copy(out[12:], addr.Bytes())
	return out
}

func bsParseTokenU64(amount string, decimals int) uint64 {
	bi := utils.ParseToken(amount, decimals)
	if !bi.IsUint64() {
		panic(fmt.Sprintf("amount %s does not fit u64", bi))
	}
	return bi.Uint64()
}

func bsPollSolanaSolvedRecord(ctx context.Context, rpcURL, programIDStr string, srcChainID, intentID uint64, timeout time.Duration) bool {
	progID, err := solana.PublicKeyFromBase58(programIDStr)
	if err != nil {
		return false
	}
	srcLE := make([]byte, 8)
	binary.LittleEndian.PutUint64(srcLE, srcChainID)
	intentLE := make([]byte, 8)
	binary.LittleEndian.PutUint64(intentLE, intentID)
	pda, _, err := solana.FindProgramAddress([][]byte{[]byte("solved"), srcLE, intentLE}, progID)
	if err != nil {
		return false
	}
	cli := rpc.New(rpcURL)
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		acct, err := cli.GetAccountInfoWithOpts(ctx, pda, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
		if err == nil && acct != nil && acct.Value != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func bsPollEVMSolvedIntents(ctx context.Context, bridge *bind.BoundContract, srcChainID, intentID *big.Int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		if bsCheckIntentSolved(ctx, bridge, srcChainID, intentID) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func bsSendSingleSolanaIx(ctx context.Context, sc *blockchain.SolanaClient, ix solana.Instruction, signer solana.PrivateKey) (string, error) {
	rpcCli := sc.RPC()
	hash, err := rpcCli.GetLatestBlockhash(ctx, sc.Commitment())
	if err != nil {
		return "", fmt.Errorf("getLatestBlockhash: %w", err)
	}
	tx, err := solana.NewTransaction(
		[]solana.Instruction{ix},
		hash.Value.Blockhash,
		solana.TransactionPayer(signer.PublicKey()),
	)
	if err != nil {
		return "", fmt.Errorf("build tx: %w", err)
	}
	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key == signer.PublicKey() {
			return &signer
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}
	sig, err := rpcCli.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		PreflightCommitment: sc.Commitment(),
	})
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		stRes, err := rpcCli.GetSignatureStatuses(ctx, false, sig)
		if err == nil && stRes != nil && len(stRes.Value) > 0 && stRes.Value[0] != nil {
			st := stRes.Value[0]
			if st.ConfirmationStatus == rpc.ConfirmationStatusConfirmed || st.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
				return sig.String(), nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return sig.String(), nil
}

// ----- Test 1: Base -> Solana -----

func bsRunBaseToSolana(
	ctx context.Context,
	cfg *bsConfig,
	wallet *utils.TestWallet,
	baseClient *ethclient.Client,
	baseChainID *big.Int,
	bridgeBaseAddr common.Address,
) *bsResult {
	res := &bsResult{Direction: "base->solana"}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🧪 TEST 1: Base → Solana (createIntentToSolana)")
	fmt.Println(strings.Repeat("=", 80))

	usdcMint, err := solana.PublicKeyFromBase58(cfg.SolanaUSDCMint)
	if err != nil {
		res.Err = fmt.Errorf("invalid devnet USDC mint: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	userSolanaKey, err := solana.PrivateKeyFromBase58(cfg.UserSolanaSecret)
	if err != nil {
		res.Err = fmt.Errorf("invalid solana user key: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	tokenBMint32 := bsSolanaPubkeyTo32(usdcMint)

	// Stealth flow (opt-in via E2E_USE_STEALTH=1) with multi-recipient
	// encrypted splits. Mirrors complete_flow.go's EVM-side privacy pattern:
	//   1. Generate N random 32-byte salts
	//   2. Derive N StealthRegistration PDAs
	//   3. Call register_stealth on Solana for each
	//   4. Encrypt the (PDA, amount) list with relayer's ECIES pubkey
	//   5. POST /store-recipients keyed by (predictedIntentID, baseChainID)
	//   6. Use FIRST PDA as the on-chain `recipient` placeholder (contract
	//      requires non-zero; using a stealth addr keeps privacy intact)
	//   7. Solver fetches /get-recipients, fans out delivery to ATA(PDA_i, USDC)
	//   8. User calls sweep_stealth(salt_i) for each to claim funds
	useStealth := strings.EqualFold(strings.TrimSpace(os.Getenv("E2E_USE_STEALTH")), "1")
	stealthN := 3
	if v := strings.TrimSpace(os.Getenv("E2E_STEALTH_N")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 10 {
			stealthN = n
		}
	}
	var (
		recipient32     [32]byte
		stealthPDAs     []solana.PublicKey
		stealthSalts    [][32]byte
		stealthAmounts  []*big.Int
		stealthClient   *blockchain.SolanaClient
		relayerURL      = func() string {
			if u := strings.TrimSpace(os.Getenv("RELAYER_URL")); u != "" {
				return u
			}
			return "http://localhost:8080"
		}()
	)
	// Predict the EVM-side intent ID up-front so we can feed it into
	// /generate-salt (which derives salt = HMAC(viewSecret, intentID || ...)
	// — the intentID makes salts for different intents diverge).
	bridgeABI, _ := abi.JSON(strings.NewReader(bsBridgeABI))
	bridgeContract := bind.NewBoundContract(bridgeBaseAddr, bridgeABI, baseClient, baseClient, baseClient)
	var latestRes []interface{}
	if err := bridgeContract.Call(&bind.CallOpts{Context: ctx}, &latestRes, "getLatestIntentId"); err != nil {
		res.Err = fmt.Errorf("getLatestIntentId: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	prevLatest := latestRes[0].(*big.Int)
	expectedID := new(big.Int).Add(prevLatest, big.NewInt(1))
	res.IntentID = expectedID
	fmt.Printf("🎯 Expecting intent ID %s\n", expectedID)

	if useStealth {
		sc, scErr := blockchain.NewSolanaClient(
			cfg.SolanaRPC, cfg.SolanaWS,
			cfg.SolanaProgramID,
			new(big.Int).SetUint64(cfg.SolanaChainID),
			"solana-stealth-test",
			"confirmed", 0,
			cfg.SolanaUSDCMint, cfg.SolanaWSOLMint,
			cfg.UserSolanaSecret,
		)
		if scErr != nil {
			res.Err = fmt.Errorf("NewSolanaClient for stealth: %w", scErr)
			fmt.Printf("❌ %v\n", res.Err)
			return res
		}
		stealthClient = sc

		fmt.Println("🕵️  Multi-recipient stealth mode enabled (HMAC-derived salts)")
		fmt.Printf("   N        = %d stealth recipients\n", stealthN)
		fmt.Printf("   user     = %s\n", userSolanaKey.PublicKey())

		// One-time recipient registration at the relayer (idempotent).
		// Generates the per-recipient viewSecret server-side. Mirrors EVM's
		// /recipient/register call shape.
		fmt.Println("   📝 Registering recipient at relayer (ed25519-signed)...")
		regResp, regErr := bsRegisterSolanaRecipient(relayerURL, userSolanaKey)
		if regErr != nil {
			res.Err = fmt.Errorf("register: %w", regErr)
			fmt.Printf("❌ %v\n", res.Err)
			return res
		}
		if regResp.AlreadyExists {
			fmt.Println("   ✓ recipient already registered (reusing existing viewSecret)")
		} else {
			fmt.Println("   ✓ recipient newly registered (viewSecret generated server-side)")
		}

		stealthPDAs = make([]solana.PublicKey, stealthN)
		stealthSalts = make([][32]byte, stealthN)
		for i := 0; i < stealthN; i++ {
			gen, gErr := bsGenerateSolanaSalt(relayerURL,
				userSolanaKey.PublicKey().String(),
				expectedID.String(),
				baseChainID.String(),
				cfg.SolanaProgramID,
			)
			if gErr != nil {
				res.Err = fmt.Errorf("generate-salt[%d]: %w", i, gErr)
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			saltBytes, _ := hex.DecodeString(gen.Salt)
			if len(saltBytes) != 32 {
				res.Err = fmt.Errorf("relayer returned salt of wrong length: %d", len(saltBytes))
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			copy(stealthSalts[i][:], saltBytes)
			pda, perr := solana.PublicKeyFromBase58(gen.StealthPDA)
			if perr != nil {
				res.Err = fmt.Errorf("invalid PDA returned for [%d]: %w", i, perr)
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			stealthPDAs[i] = pda
			fmt.Printf("   [%d] nonce=%d  PDA=%s\n", i, gen.Nonce, pda)
			fmt.Printf("       salt   = 0x%s   (HMAC-derived; not persisted)\n", gen.Salt)

			// Still need to register_stealth on-chain (Anchor program requires
			// the StealthRegistration PDA before sweep_stealth can drain).
			regIx, regErr := sc.BuildRegisterStealthIx(userSolanaKey.PublicKey(), stealthSalts[i])
			if regErr != nil {
				res.Err = fmt.Errorf("BuildRegisterStealthIx[%d]: %w", i, regErr)
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			sig, sErr := bsSendSingleSolanaIx(ctx, sc, regIx, userSolanaKey)
			if sErr != nil {
				res.Err = fmt.Errorf("register_stealth[%d]: %w", i, sErr)
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			fmt.Printf("       register_stealth ✓ %s\n", sig)
		}

		// On-chain `recipient` is the FIRST stealth PDA (contract requires
		// non-zero; using a stealth addr keeps observers from linking to the user).
		recipient32 = bsSolanaPubkeyTo32(stealthPDAs[0])
	} else {
		recipient32 = bsSolanaPubkeyTo32(userSolanaKey.PublicKey())
	}

	decimalsA := bsFetchTokenDecimals(ctx, baseClient, cfg.BaseTokenUSDC)
	decimalsB := 6
	amountA := utils.ParseToken(cfg.AmountA, decimalsA)
	expectedAmountB := utils.ParseToken(cfg.AmountB, decimalsB)
	reward := utils.ParseToken(cfg.Reward, decimalsA)
	auctionDuration := big.NewInt(cfg.AuctionDuration)

	// Compute per-recipient split amounts (sums to expectedAmountB). Reuses
	// TEST_RECIPIENT_1/2/3_AMOUNT when N==3 (matches complete_flow.go); falls
	// back to an even split otherwise.
	if useStealth {
		stealthAmounts = bsComputeSplits(stealthN, expectedAmountB, cfg)
	}

	recipientLabel := userSolanaKey.PublicKey().String()
	if useStealth {
		recipientLabel = fmt.Sprintf("%s + %d encrypted (first PDA on-chain)", stealthPDAs[0], stealthN-1)
	}
	fmt.Printf("💱 tokenA=%s (decimals %d), tokenB=devnet USDC (mint %s), recipient=%s\n",
		cfg.BaseTokenUSDC, decimalsA, cfg.SolanaUSDCMint, recipientLabel)
	fmt.Printf("   amountA=%s expectedAmountB=%s reward=%s\n", amountA, expectedAmountB, reward)
	if useStealth {
		fmt.Println("   splits:")
		for i := range stealthPDAs {
			fmt.Printf("     [%d] %s ← %s\n", i, stealthPDAs[i], stealthAmounts[i])
		}
	}

	// ----- Pre-flight balance snapshot -----
	rpcCli := rpc.New(cfg.SolanaRPC)
	userATA, _, _ := solana.FindAssociatedTokenAddress(userSolanaKey.PublicKey(), usdcMint)
	var stealthATAs []solana.PublicKey
	var preStealthBals []*big.Int
	if useStealth {
		stealthATAs = make([]solana.PublicKey, len(stealthPDAs))
		preStealthBals = make([]*big.Int, len(stealthPDAs))
		for i, pda := range stealthPDAs {
			ata, _, _ := solana.FindAssociatedTokenAddress(pda, usdcMint)
			stealthATAs[i] = ata
			preStealthBals[i] = bsFetchSPLBalance(ctx, rpcCli, ata)
		}
	}

	preBaseUserUSDC := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, wallet.Address)
	preSolanaUserUSDC := bsFetchSPLBalance(ctx, rpcCli, userATA)

	fmt.Println("\n📊 Balances BEFORE:")
	fmt.Printf("   Base   user USDC      = %s\n", bsFmtBal(preBaseUserUSDC, decimalsA))
	fmt.Printf("   Solana user USDC ATA  = %s   (%s)\n", bsFmtBal(preSolanaUserUSDC, decimalsB), userATA)
	if useStealth {
		for i, ata := range stealthATAs {
			fmt.Printf("   Solana stealth[%d] ATA = %s   (%s)\n", i, bsFmtBal(preStealthBals[i], decimalsB), ata)
		}
	}

	erc20ABI, _ := abi.JSON(strings.NewReader(bsERC20ABI))
	tokenAContract := bind.NewBoundContract(common.HexToAddress(cfg.BaseTokenUSDC), erc20ABI, baseClient, baseClient, baseClient)
	auth, err := utils.GetTransactOpts(ctx, baseClient, wallet, baseChainID)
	if err != nil {
		res.Err = err
		fmt.Printf("❌ TransactOpts: %v\n", err)
		return res
	}
	approveAmount := new(big.Int).Add(amountA, reward)
	fmt.Println("🔐 Approving USDC on Base...")
	approveTx, err := tokenAContract.Transact(auth, "approve", bridgeBaseAddr, approveAmount)
	if err != nil {
		res.Err = fmt.Errorf("approve: %w", err)
		fmt.Printf("❌ approve failed: %v\n", err)
		return res
	}
	if err := utils.WaitForTransaction(ctx, baseClient, approveTx.Hash(), 2*time.Minute); err != nil {
		res.Err = fmt.Errorf("approve tx reverted: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	fmt.Println("✅ Approved")

	// bridgeContract + expectedID were computed above the stealth block
	// so the salt generation could feed expectedID into /generate-salt.

	// Encrypted recipient list (Solana base58 pubkeys + amounts) must be
	// stored at the relayer BEFORE the solver picks up the intent, since
	// the solver consults /get-recipients during its delivery dispatch.
	if useStealth {
		recipientStrs := make([]string, len(stealthPDAs))
		amountStrs := make([]string, len(stealthAmounts))
		for i := range stealthPDAs {
			recipientStrs[i] = stealthPDAs[i].String()
			amountStrs[i] = stealthAmounts[i].String()
		}
		fmt.Println("🔒 Encrypting + storing recipient list at relayer...")
		if err := bsStoreRecipientsEncrypted(relayerURL, expectedID.String(), baseChainID.String(), recipientStrs, amountStrs); err != nil {
			res.Err = fmt.Errorf("store-recipients: %w", err)
			fmt.Printf("❌ %v\n", res.Err)
			return res
		}
		fmt.Printf("   ✅ stored %d encrypted recipients for intentID=%s sourceChainID=%s\n",
			len(recipientStrs), expectedID, baseChainID)

		// No per-intent salt storage needed — relayer already persisted
		// the (nonce, intentID, chainID, stealthPDA) metadata during the
		// /generate-salt calls above. Salts are reconstructed deterministically
		// from the recipient's viewSecret on each /get-salts retrieval.
		fmt.Println("📦 Salts are deterministic — no explicit storage needed (relayer holds viewSecret + nonce records)")
	}

	auth2, _ := utils.GetTransactOpts(ctx, baseClient, wallet, baseChainID)
	fmt.Println("🎯 Submitting createIntentToSolana...")
	createTx, err := bridgeContract.Transact(auth2, "createIntentToSolana",
		common.HexToAddress(cfg.BaseTokenUSDC),
		tokenBMint32,
		amountA, expectedAmountB, reward,
		cfg.SolanaChainID,
		recipient32,
		auctionDuration,
	)
	if err != nil {
		res.Err = fmt.Errorf("createIntentToSolana: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	res.SourceTxHash = createTx.Hash().Hex()
	if err := utils.WaitForTransaction(ctx, baseClient, createTx.Hash(), 2*time.Minute); err != nil {
		res.Err = fmt.Errorf("createIntentToSolana tx reverted: %w (tx=%s — check on basescan; most common cause: Base Sepolia bridge contract is the old version without createIntentToSolana)", err, res.SourceTxHash)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	fmt.Printf("✅ createIntentToSolana confirmed: %s\n", res.SourceTxHash)

	time.Sleep(8 * time.Second)
	var ext []interface{}
	if err := bridgeContract.Call(&bind.CallOpts{Context: ctx}, &ext, "getSolanaIntentExt", expectedID); err == nil && len(ext) >= 4 {
		isSolanaDest, _ := ext[3].(bool)
		fmt.Printf("   isSolanaDestIntent[%s] = %v\n", expectedID, isSolanaDest)
	}

	fmt.Println("⏳ Polling Solana for SolvedRecord PDA (up to 3 min)...")
	res.DestVerified = bsPollSolanaSolvedRecord(ctx, cfg.SolanaRPC, cfg.SolanaProgramID, baseChainID.Uint64(), expectedID.Uint64(), 3*time.Minute)
	if res.DestVerified {
		fmt.Println("✅ Solana SolvedRecord PDA exists — destination delivery confirmed")
	} else {
		res.Note = "destination delivery not observed within timeout; solver may not be running"
		fmt.Println("⚠️  SolvedRecord PDA not found within timeout")
	}

	// Stealth post-delivery: funds split across ATA(stealthPDA_i, USDC).
	// **Mirror of EVM TEE retrieval flow**: we don't rely on the in-memory
	// `stealthSalts` slice — instead, we fetch the salts from the relayer
	// (ed25519-signed retrieval). This proves the persistence path: even
	// if the test process crashed between deliver and sweep, the user can
	// still claim by re-running with just their wallet.
	if useStealth && res.DestVerified && stealthClient != nil {
		userAta, _, _ := solana.FindAssociatedTokenAddress(userSolanaKey.PublicKey(), usdcMint)
		fmt.Println("\n🕵️  Stealth sweeps:")
		fmt.Printf("   user ATA = %s (sweep target)\n", userAta)

		fmt.Println("   🔑 Fetching salts from relayer (ed25519-signed retrieval)...")
		records, fetchErr := bsFetchSaltsFromRelayer(relayerURL, userSolanaKey)
		if fetchErr != nil {
			fmt.Printf("   ⚠️  fetch-salts failed: %v (falling back to in-memory salts)\n", fetchErr)
		} else {
			// Filter to records for THIS intent only.
			filtered := records[:0]
			for _, rec := range records {
				if rec.IntentID == expectedID.String() {
					filtered = append(filtered, rec)
				}
			}
			fmt.Printf("   ✅ retrieved %d salts for intentID=%s (total stored: %d)\n", len(filtered), expectedID, len(records))

			for i, rec := range filtered {
				saltBytes, decErr := hex.DecodeString(strings.TrimPrefix(rec.Salt, "0x"))
				if decErr != nil || len(saltBytes) != 32 {
					fmt.Printf("   ⚠️  invalid salt[%d] from relayer: %v\n", i, decErr)
					continue
				}
				var salt [32]byte
				copy(salt[:], saltBytes)

				pda, _, _ := stealthClient.StealthPDA(userSolanaKey.PublicKey(), salt)
				ata, _, _ := solana.FindAssociatedTokenAddress(pda, usdcMint)
				if bal, err := stealthClient.RPC().GetTokenAccountBalance(ctx, ata, rpc.CommitmentConfirmed); err == nil && bal != nil && bal.Value != nil {
					fmt.Printf("   [%d] PDA=%s pre-sweep balance: %s\n", i, pda, bal.Value.Amount)
				}
				sweepIx, sErr := stealthClient.BuildSweepStealthIx(userSolanaKey.PublicKey(), usdcMint, userAta, salt)
				if sErr != nil {
					fmt.Printf("   ⚠️  BuildSweepStealthIx[%d]: %v\n", i, sErr)
					continue
				}
				sweepSig, sweepErr := bsSendSingleSolanaIx(ctx, stealthClient, sweepIx, userSolanaKey)
				if sweepErr != nil {
					fmt.Printf("   ⚠️  sweep_stealth[%d]: %v\n", i, sweepErr)
					continue
				}
				fmt.Printf("   ✅ sweep_stealth[%d] confirmed: %s\n", i, sweepSig)
			}
		}
		stealthClient.Close()
	}

	fmt.Println("⏳ Polling Base for intent.completed (up to 2 min)...")
	res.SourceCompleted = bsPollIntentCompleted(ctx, bridgeContract, expectedID, 2*time.Minute)
	if res.SourceCompleted {
		fmt.Println("✅ Base intent.completed = true")
	} else {
		fmt.Println("⚠️  Base intent not settled within timeout")
	}

	// ----- Post-flight balance snapshot + deltas -----
	postBaseUserUSDC := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, wallet.Address)
	postSolanaUserUSDC := bsFetchSPLBalance(ctx, rpcCli, userATA)

	fmt.Println("\n📊 Balances AFTER:")
	fmt.Printf("   Base   user USDC      = %s   delta=%s\n",
		bsFmtBal(postBaseUserUSDC, decimalsA), bsDelta(preBaseUserUSDC, postBaseUserUSDC))
	fmt.Printf("   Solana user USDC ATA  = %s   delta=%s\n",
		bsFmtBal(postSolanaUserUSDC, decimalsB), bsDelta(preSolanaUserUSDC, postSolanaUserUSDC))
	if useStealth {
		for i, ata := range stealthATAs {
			post := bsFetchSPLBalance(ctx, rpcCli, ata)
			fmt.Printf("   Solana stealth[%d] ATA = %s   delta=%s\n",
				i, bsFmtBal(post, decimalsB), bsDelta(preStealthBals[i], post))
		}
		fmt.Println("   (each stealth ATA delta should be 0 if sweep ran: tokens went in then out)")
	}
	return res
}

func bsPollIntentCompleted(ctx context.Context, bridge *bind.BoundContract, intentID *big.Int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		if bsCheckIntentCompleted(ctx, bridge, intentID) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// ----- EVM stealth helpers (mirror of complete_flow.go's TachyonStealth flow) -----
//
// Used when Solana → Base intents need an ephemeral Safe address as the
// recipient on the EVM side (so funds don't land directly in the user's
// wallet, preserving link-unlinkability between the Solana sender and
// EVM recipient).

var (
	bsDomainTypeHash         = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))
	bsDomainSeparator        = crypto.Keccak256Hash(bsDomainTypeHash.Bytes(), crypto.Keccak256([]byte("TachyonStealth")), crypto.Keccak256([]byte("1")))
	bsRegisterStealthTypeHsh = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
	bsDeriveStealthKeyTypeHs = crypto.Keccak256Hash([]byte("DeriveStealthKey(address recipientAddress,address stealthAddress)"))
	bsDeploySafeTypeHash     = crypto.Keccak256Hash([]byte("DeploySafe(address stealthAddress,uint256 chainId)"))
)

func bsEIP712Hash(structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, bsDomainSeparator.Bytes(), structHash.Bytes())
}

func bsSignRegisterStealth(key *ecdsa.PrivateKey, signer common.Address) []byte {
	structHash := crypto.Keccak256Hash(bsRegisterStealthTypeHsh.Bytes(), common.LeftPadBytes(signer.Bytes(), 32))
	sig, _ := crypto.Sign(bsEIP712Hash(structHash).Bytes(), key)
	return sig
}

func bsSignDeriveStealthKey(key *ecdsa.PrivateKey, recipient, stealthAddr common.Address) []byte {
	structHash := crypto.Keccak256Hash(bsDeriveStealthKeyTypeHs.Bytes(), common.LeftPadBytes(recipient.Bytes(), 32), common.LeftPadBytes(stealthAddr.Bytes(), 32))
	sig, _ := crypto.Sign(bsEIP712Hash(structHash).Bytes(), key)
	return sig
}

func bsSignDeploySafe(key *ecdsa.PrivateKey, stealthAddr common.Address, chainID *big.Int) []byte {
	structHash := crypto.Keccak256Hash(bsDeploySafeTypeHash.Bytes(), common.LeftPadBytes(stealthAddr.Bytes(), 32), common.LeftPadBytes(chainID.Bytes(), 32))
	sig, _ := crypto.Sign(bsEIP712Hash(structHash).Bytes(), key)
	return sig
}

// bsEVMStealth holds the per-intent stealth addresses returned by the
// relayer's TEE. The Safe address is the on-chain recipient (predicted
// deterministically); the stealth EOA is what the recipient later uses
// to sign the Safe deployment.
type bsEVMStealth struct {
	RecipientKey   *ecdsa.PrivateKey // fresh EOA owned by the test
	RecipientAddr  common.Address
	StealthAddress common.Address // TEE-derived EOA
	SafeAddress    common.Address // predicted Safe, used as recipient
}

// bsSetupEVMStealth runs the register + generate-stealth pair against the
// relayer, returning the predicted Safe address to use as the intent
// recipient. destChainID is the EVM chain (Base=84532). Predicted intent
// ID must be known up-front — pass `Config.next_intent_id` from Solana.
func bsSetupEVMStealth(relayerURL string, destChainID, predictedIntentID uint64) (*bsEVMStealth, error) {
	recipientKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate recipient key: %w", err)
	}
	recipientAddr := crypto.PubkeyToAddress(recipientKey.PublicKey)

	// register
	regSig := bsSignRegisterStealth(recipientKey, recipientAddr)
	regBody, _ := json.Marshal(map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"signature":        hex.EncodeToString(regSig),
	})
	regResp, err := http.Post(relayerURL+"/recipient/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		return nil, fmt.Errorf("POST /recipient/register: %w", err)
	}
	regRespBody, _ := io.ReadAll(regResp.Body)
	regResp.Body.Close()
	if regResp.StatusCode != 200 {
		return nil, fmt.Errorf("register failed [%d]: %s", regResp.StatusCode, string(regRespBody))
	}

	// generate-stealth
	genBody, _ := json.Marshal(map[string]string{
		"recipientAddress": recipientAddr.Hex(),
		"chainId":          strconv.FormatUint(destChainID, 10),
		"intentId":         strconv.FormatUint(predictedIntentID, 10),
	})
	genResp, err := http.Post(relayerURL+"/recipient/generate-stealth", "application/json", bytes.NewReader(genBody))
	if err != nil {
		return nil, fmt.Errorf("POST /recipient/generate-stealth: %w", err)
	}
	genRespBody, _ := io.ReadAll(genResp.Body)
	genResp.Body.Close()
	if genResp.StatusCode != 200 {
		return nil, fmt.Errorf("generate-stealth failed [%d]: %s", genResp.StatusCode, string(genRespBody))
	}
	var genResult map[string]interface{}
	if err := json.Unmarshal(genRespBody, &genResult); err != nil {
		return nil, fmt.Errorf("decode generate-stealth response: %w", err)
	}
	if ok, _ := genResult["success"].(bool); !ok {
		return nil, fmt.Errorf("generate-stealth not successful: %v", genResult["error"])
	}
	stealthStr, _ := genResult["stealthAddress"].(string)
	safeStr, _ := genResult["safeAddress"].(string)
	if stealthStr == "" || safeStr == "" {
		return nil, fmt.Errorf("generate-stealth missing fields: %v", genResult)
	}
	return &bsEVMStealth{
		RecipientKey:   recipientKey,
		RecipientAddr:  recipientAddr,
		StealthAddress: common.HexToAddress(stealthStr),
		SafeAddress:    common.HexToAddress(safeStr),
	}, nil
}

// ----- Test 2: Solana -> Base -----

func bsRunSolanaToBase(
	ctx context.Context,
	cfg *bsConfig,
	wallet *utils.TestWallet,
	baseClient *ethclient.Client,
	baseChainID *big.Int,
	bridgeBaseAddr common.Address,
) *bsResult {
	res := &bsResult{Direction: "solana->base"}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🧪 TEST 2: Solana → Base (Anchor create_intent)")
	fmt.Println(strings.Repeat("=", 80))

	userKey, err := solana.PrivateKeyFromBase58(cfg.UserSolanaSecret)
	if err != nil {
		res.Err = fmt.Errorf("invalid user key: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	usdcMint, err := solana.PublicKeyFromBase58(cfg.SolanaUSDCMint)
	if err != nil {
		res.Err = fmt.Errorf("invalid USDC mint: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	sc, err := blockchain.NewSolanaClient(
		cfg.SolanaRPC, cfg.SolanaWS,
		cfg.SolanaProgramID,
		new(big.Int).SetUint64(cfg.SolanaChainID),
		"solana-devnet-test",
		"confirmed", 0,
		cfg.SolanaUSDCMint, cfg.SolanaWSOLMint,
		cfg.UserSolanaSecret,
	)
	if err != nil {
		res.Err = fmt.Errorf("NewSolanaClient: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	defer sc.Close()

	rpcCli := sc.RPC()

	configPda, _, err := sc.ConfigPDA()
	if err != nil {
		res.Err = fmt.Errorf("derive config PDA: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	cfgAcct, err := rpcCli.GetAccountInfoWithOpts(ctx, configPda, &rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed})
	if err != nil || cfgAcct == nil || cfgAcct.Value == nil {
		res.Err = fmt.Errorf("Config PDA not found at %s — has the program been initialized?", configPda)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	cfgData := cfgAcct.Value.Data.GetBinary()
	if len(cfgData) < 8+32+8+64+8 {
		res.Err = fmt.Errorf("Config account too short: %d bytes", len(cfgData))
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	nextIntentID := binary.LittleEndian.Uint64(cfgData[8+32+8+64 : 8+32+8+64+8])
	fmt.Printf("🎯 Config.next_intent_id = %d\n", nextIntentID)

	amountA := bsParseTokenU64(cfg.AmountA, 6)
	expectedAmountB := bsParseTokenU64(cfg.AmountB, 6)
	reward := bsParseTokenU64(cfg.Reward, 6)
	auctionDuration := uint64(cfg.AuctionDuration)
	destChainID := baseChainID.Uint64()

	// Multi-recipient stealth on Base (opt-in via E2E_USE_STEALTH=1):
	//   1. Generate N fresh recipient EOAs
	//   2. For each: /recipient/register + /recipient/generate-stealth
	//      → N predicted Safe addresses (deterministic CREATE2 addrs)
	//   3. Encrypt the (Safe, amount) list with relayer's ECIES pubkey
	//   4. POST /store-recipients keyed to (predictedSolanaIntentID, solanaChainID)
	//   5. Use FIRST Safe as on-chain `recipient` (contract requires non-zero)
	//   6. Solver fetches /get-recipients, fans out to N Safes on Base
	//   7. Post-delivery: each recipient claims their Safe via the TEE flow
	useStealth := strings.EqualFold(strings.TrimSpace(os.Getenv("E2E_USE_STEALTH")), "1")
	stealthN := 3
	if v := strings.TrimSpace(os.Getenv("E2E_STEALTH_N")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 10 {
			stealthN = n
		}
	}
	relayerURL := strings.TrimSpace(os.Getenv("RELAYER_URL"))
	if relayerURL == "" {
		relayerURL = "http://localhost:8080"
	}

	var (
		recipient32     [32]byte
		evmStealths     []*bsEVMStealth
		stealthAmountsB []*big.Int
	)
	if useStealth {
		evmStealths = make([]*bsEVMStealth, stealthN)
		// expectedAmountB is u64 raw here (already in 6-decimal SPL units)
		stealthAmountsB = bsComputeSplits(stealthN, new(big.Int).SetUint64(expectedAmountB), cfg)

		fmt.Println("🕵️  Multi-recipient EVM stealth mode enabled")
		fmt.Printf("   N = %d Safe recipients on Base\n", stealthN)
		for i := 0; i < stealthN; i++ {
			st, err := bsSetupEVMStealth(relayerURL, destChainID, nextIntentID)
			if err != nil {
				res.Err = fmt.Errorf("EVM stealth setup[%d]: %w", i, err)
				fmt.Printf("❌ %v\n", res.Err)
				return res
			}
			evmStealths[i] = st
			fmt.Printf("   [%d] recipientEOA=%s\n", i, st.RecipientAddr.Hex())
			fmt.Printf("       stealthEOA  =%s\n", st.StealthAddress.Hex())
			fmt.Printf("       predicted Safe=%s ← receives %s (raw)\n", st.SafeAddress.Hex(), stealthAmountsB[i])
		}
		// On-chain `recipient` is the FIRST Safe address (contract requires non-zero).
		recipient32 = bsEVMAddrTo32(evmStealths[0].SafeAddress)

		// Encrypt + POST to relayer BEFORE submitting the intent on Solana,
		// so the solver finds it when delivery dispatch fires.
		recipientStrs := make([]string, stealthN)
		amountStrs := make([]string, stealthN)
		for i, st := range evmStealths {
			recipientStrs[i] = st.SafeAddress.Hex()
			amountStrs[i] = stealthAmountsB[i].String()
		}
		fmt.Println("🔒 Encrypting + storing recipient list at relayer...")
		if err := bsStoreRecipientsEncrypted(relayerURL, fmt.Sprintf("%d", nextIntentID), strconv.FormatUint(cfg.SolanaChainID, 10), recipientStrs, amountStrs); err != nil {
			res.Err = fmt.Errorf("store-recipients: %w", err)
			fmt.Printf("❌ %v\n", res.Err)
			return res
		}
		fmt.Printf("   ✅ stored %d encrypted recipients for intentID=%d sourceChainID=%d\n", stealthN, nextIntentID, cfg.SolanaChainID)
	} else {
		recipient32 = bsEVMAddrTo32(wallet.Address)
	}

	userATA, _, err := solana.FindAssociatedTokenAddress(userKey.PublicKey(), usdcMint)
	if err != nil {
		res.Err = fmt.Errorf("derive user ATA: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	fmt.Printf("💱 user=%s userATA=%s amountA=%d expectedAmountB=%d reward=%d destChainID=%d\n",
		userKey.PublicKey(), userATA, amountA, expectedAmountB, reward, destChainID)

	// ----- Pre-flight balance snapshot -----
	baseDecimals := bsFetchTokenDecimals(ctx, baseClient, cfg.BaseTokenUSDC)
	preSolanaUserUSDC := bsFetchSPLBalance(ctx, sc.RPC(), userATA)
	preBaseWalletUSDC := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, wallet.Address)
	preBaseSafeBals := []*big.Int{}
	if useStealth {
		preBaseSafeBals = make([]*big.Int, len(evmStealths))
		for i, st := range evmStealths {
			preBaseSafeBals[i] = bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, st.SafeAddress)
		}
	}

	// After the solver-side cross-decimal scaling lands, Safe balances on
	// Base are in the destination token's native decimals (e.g., 18 for
	// our mock USDC.e). Use baseDecimals throughout so the math lines up
	// with what's actually on-chain.
	fmt.Println("\n📊 Balances BEFORE:")
	fmt.Printf("   Solana user USDC ATA = %s   (%s)\n", bsFmtBal(preSolanaUserUSDC, 6), userATA)
	fmt.Printf("   Base   user wallet   = %s   (%s)\n", bsFmtBal(preBaseWalletUSDC, baseDecimals), wallet.Address.Hex())
	if useStealth {
		for i, st := range evmStealths {
			fmt.Printf("   Base   stealth Safe[%d] = %s   (%s)\n", i, bsFmtBal(preBaseSafeBals[i], baseDecimals), st.SafeAddress.Hex())
		}
	}

	ix, err := sc.BuildCreateIntentIx(
		userKey.PublicKey(),
		usdcMint, usdcMint,
		userATA,
		nextIntentID,
		amountA, expectedAmountB, reward,
		destChainID,
		recipient32,
		auctionDuration,
	)
	if err != nil {
		res.Err = fmt.Errorf("BuildCreateIntentIx: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}

	fmt.Println("🎯 Submitting Anchor create_intent on Solana devnet...")
	sig, err := bsSendSingleSolanaIx(ctx, sc, ix, userKey)
	if err != nil {
		res.Err = fmt.Errorf("send create_intent: %w", err)
		fmt.Printf("❌ %v\n", res.Err)
		return res
	}
	res.SourceTxHash = sig
	res.IntentID = new(big.Int).SetUint64(nextIntentID)
	fmt.Printf("✅ create_intent confirmed: %s\n", sig)

	time.Sleep(5 * time.Second)
	snap, err := sc.GetIntent(ctx, res.IntentID)
	if err != nil {
		fmt.Printf("⚠️  could not read IntentRecord: %v\n", err)
	} else {
		fmt.Printf("   IntentRecord OK: amountA=%s reward=%s destChainID=%s deposited=%v completed=%v\n",
			snap.AmountA, snap.Reward, snap.DestChainID, snap.Deposited, snap.Completed)
	}

	fmt.Println("⏳ Polling Base for solvedIntents flag (up to 3 min)...")
	bridgeABI, _ := abi.JSON(strings.NewReader(bsBridgeABI))
	bridgeContract := bind.NewBoundContract(bridgeBaseAddr, bridgeABI, baseClient, baseClient, baseClient)
	res.DestVerified = bsPollEVMSolvedIntents(ctx, bridgeContract, new(big.Int).SetUint64(cfg.SolanaChainID), res.IntentID, 3*time.Minute)
	if res.DestVerified {
		fmt.Println("✅ Base solvedIntents = true — destination delivery confirmed")
	} else {
		res.Note = "destination delivery not observed within timeout; solver may not be running"
		fmt.Println("⚠️  Base solvedIntents not flipped within timeout")
	}

	// Stealth post-delivery check: confirm tokens landed at the predicted
	// Safe addresses (not yet deployed — just the addresses hold the balances).
	if useStealth && len(evmStealths) > 0 && res.DestVerified {
		fmt.Println("\n🕵️  EVM stealth check (per-Safe balances):")
		for i, st := range evmStealths {
			bal := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, st.SafeAddress)
			fmt.Printf("   [%d] %s → balance=%s   expected=+%s\n",
				i, st.SafeAddress.Hex(), bsFmtBal(bal, baseDecimals), stealthAmountsB[i])
		}
		fmt.Println("   (Safe deployment + signature-based claim is the same TEE flow as complete_flow.go;")
		fmt.Println("    each registered recipient EOA can derive their stealth key + deploy their Safe)")
	}

	fmt.Println("⏳ Polling Solana for intent_record.completed (up to 2 min)...")
	deadline := time.Now().Add(2 * time.Minute)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		updated, err := sc.GetIntent(ctx, res.IntentID)
		if err == nil && updated.Completed {
			res.SourceCompleted = true
			fmt.Println("✅ Solana intent_record.completed = true")
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("⚠️  Solana intent not settled within timeout")
			break
		}
		select {
		case <-ctx.Done():
			return res
		case <-tick.C:
		}
	}

	// ----- Post-flight balance snapshot + deltas -----
	postSolanaUserUSDC := bsFetchSPLBalance(ctx, sc.RPC(), userATA)
	postBaseWalletUSDC := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, wallet.Address)

	fmt.Println("\n📊 Balances AFTER:")
	fmt.Printf("   Solana user USDC ATA = %s   delta=%s\n",
		bsFmtBal(postSolanaUserUSDC, 6), bsDelta(preSolanaUserUSDC, postSolanaUserUSDC))
	fmt.Printf("   Base   user wallet   = %s   delta=%s\n",
		bsFmtBal(postBaseWalletUSDC, baseDecimals), bsDelta(preBaseWalletUSDC, postBaseWalletUSDC))
	if useStealth {
		for i, st := range evmStealths {
			post := bsFetchERC20Balance(ctx, baseClient, cfg.BaseTokenUSDC, st.SafeAddress)
			fmt.Printf("   Base   stealth Safe[%d] = %s   delta=%s\n",
				i, bsFmtBal(post, baseDecimals), bsDelta(preBaseSafeBals[i], post))
		}
		fmt.Println("   (sum of Safe deltas should ≈ +expectedAmountB; user wallet delta ≈ 0)")
	} else {
		fmt.Println("   (user wallet delta should ≈ +expectedAmountB)")
	}
	return res
}

// ----- main -----

func main() {
	fmt.Println("🚀 Base ↔ Solana E2E Test")
	fmt.Println("Testing 2 bidirectional intent flows:")
	fmt.Println("  1. Base   → Solana (USDC → SPL devnet USDC)")
	fmt.Println("  2. Solana → Base   (SPL devnet USDC → USDC)")
	fmt.Println()

	cfg, err := bsLoadConfig()
	if err != nil {
		fmt.Printf("❌ Config error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fmt.Println("📡 Connecting to Base...")
	baseClient, err := ethclient.Dial(cfg.BaseRPC)
	if err != nil {
		fmt.Printf("❌ Failed to connect to Base: %v\n", err)
		os.Exit(1)
	}
	baseChainID, err := baseClient.ChainID(ctx)
	if err != nil || baseChainID == nil {
		fmt.Printf("❌ Failed to get Base chain ID (RPC may be unhealthy): %v\n", err)
		os.Exit(1)
	}
	block, err := baseClient.BlockNumber(ctx)
	if err != nil {
		fmt.Printf("❌ Failed to get Base block number: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Base: chainID=%s block=%d bridge=%s\n", baseChainID, block, cfg.BaseBridgeAddr)

	wallet, err := utils.CreateTestWallet(cfg.PrivateKey)
	if err != nil {
		fmt.Printf("❌ wallet: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("💼 EVM Wallet: %s\n", wallet.Address.Hex())

	bridgeBaseAddr := common.HexToAddress(cfg.BaseBridgeAddr)

	r1 := bsRunBaseToSolana(ctx, cfg, wallet, baseClient, baseChainID, bridgeBaseAddr)

	fmt.Println("\n⏳ Waiting 15s before test 2...")
	time.Sleep(15 * time.Second)

	r2 := bsRunSolanaToBase(ctx, cfg, wallet, baseClient, baseChainID, bridgeBaseAddr)

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("📊 Base ↔ Solana Test Results")
	fmt.Println(strings.Repeat("=", 80))
	for _, r := range []*bsResult{r1, r2} {
		status := "✅"
		if r.Err != nil || (!r.SourceCompleted && !r.DestVerified) {
			status = "⚠️"
		}
		intentIDStr := "?"
		if r.IntentID != nil {
			intentIDStr = r.IntentID.String()
		}
		fmt.Printf("   %s  %-18s intentID=%s sourceCompleted=%v destVerified=%v\n",
			status, r.Direction, intentIDStr, r.SourceCompleted, r.DestVerified)
		if r.Err != nil {
			fmt.Printf("       err: %v\n", r.Err)
		} else if r.Note != "" {
			fmt.Printf("       note: %s\n", r.Note)
		}
	}
	fmt.Println()
}
