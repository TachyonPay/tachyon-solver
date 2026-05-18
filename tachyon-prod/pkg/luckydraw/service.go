package luckydraw

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

const entryPrefix = "luckydraw:entry:"

// Entry is persisted in PebbleDB, keyed by the stealth address.
type Entry struct {
	StealthAddress string `json:"stealthAddress"` // dedup key — unique per real-world user
	ProxyAddress   string `json:"proxyAddress"`   // Safe that entered
	ChainID        string `json:"chainId"`        // chain of the entry
	Won            bool   `json:"won"`            // true only if the random draw awarded a drip
	AmountUSDC     int    `json:"amountUSDC"`     // 0 if Won=false
	AmountRaw      string `json:"amountRaw"`      // "0" if Won=false
	TxHash         string `json:"txHash"`         // "" if Won=false
	EnteredAt      int64  `json:"enteredAt"`      // unix seconds
}

// EnterRequest is the body for POST /lucky-draw/enter.
//
// # EIP-712 signing (frontend)
//
//	domain:  { name: "TachyonStealth", version: "1" }
//	types:   { LuckyDrawEntry: [
//	             { name: "proxyAddress", type: "address" },
//	             { name: "chainId",      type: "uint256" }
//	           ]}
//	message: { proxyAddress: checksumAddress(proxy), chainId: BigInt(chainId) }
//
// Sign with the stealth private key that owns the proxy Safe.
type EnterRequest struct {
	ProxyAddress string `json:"proxyAddress" binding:"required"` // proxy Safe receiving the drip
	ChainID      string `json:"chainId"      binding:"required"` // destination chain of the bridge
	Signature    string `json:"signature"    binding:"required"` // EIP-712, 65-byte hex
}

// EnterResult is returned on a successful entry.
// Won=false means the entry was accepted but the random draw did not award a drip.
type EnterResult struct {
	StealthAddress string `json:"stealthAddress"`
	ProxyAddress   string `json:"proxyAddress"`
	ChainID        string `json:"chainId"`
	Won            bool   `json:"won"`
	AmountUSDC     int    `json:"amountUSDC"` // 0 when Won=false
	AmountRaw      string `json:"amountRaw"`  // "0" when Won=false
	TxHash         string `json:"txHash"`     // "" when Won=false
}

// StatusResult is returned by GET /lucky-draw/status?stealth=0x...
type StatusResult struct {
	Entered        bool   `json:"entered"`
	Won            bool   `json:"won"`
	StealthAddress string `json:"stealthAddress"`
	ProxyAddress   string `json:"proxyAddress,omitempty"`
	ChainID        string `json:"chainId,omitempty"`
	AmountUSDC     int    `json:"amountUSDC,omitempty"`
	TxHash         string `json:"txHash,omitempty"`
	EnteredAt      int64  `json:"enteredAt,omitempty"`
}

// chainClients bundles the two clients the service needs per EVM chain.
type chainClients struct {
	ethRPC      *ethclient.Client  // read-only: balanceOf, getOwners
	safeClient  *blockchain.Client // ExecuteViaSafe for the USDC drip
	usdcAddress common.Address
}

// Service is the lucky draw service.
// Supports an arbitrary number of EVM chains via LUCKY_DRAW_CHAIN_N_* env vars.
type Service struct {
	cfg    *Config
	chains map[string]*chainClients // keyed by chainId decimal string
	store  *storage.Store
	rng    *rand.Rand
	mu     sync.Mutex // serialises Enter to prevent same-proxy races
}

// NewService initialises one client pair per configured chain.
// Supports any number of chains — just increment LUCKY_DRAW_CHAIN_COUNT.
func NewService(cfg *Config, store *storage.Store) (*Service, error) {
	chains := make(map[string]*chainClients, len(cfg.Chains))

	for _, ce := range cfg.Chains {
		rpc, err := ethclient.Dial(ce.RPCURL)
		if err != nil {
			return nil, fmt.Errorf("dial RPC for chain %s: %w", ce.ChainName, err)
		}

		// Pass all 4 signing keys (3 TEE + 1 admin) so execTransaction meets
		// the 4-of-5 Safe threshold automatically.
		safe, err := blockchain.NewClient(
			ce.RPCURL,
			"", // no bridge contract needed
			cfg.PrivateKey,
			ce.ChainID,
			ce.ChainName,
			ce.SafeAddress,
			cfg.SafeSignerKeys,
			"", // no multicall
		)
		if err != nil {
			rpc.Close()
			return nil, fmt.Errorf("init safe client for chain %s: %w", ce.ChainName, err)
		}

		chains[ce.ChainID.String()] = &chainClients{
			ethRPC:      rpc,
			safeClient:  safe,
			usdcAddress: ce.USDCAddress,
		}

		log.WithFields(log.Fields{
			"chain":       ce.ChainName,
			"chainID":     ce.ChainID,
			"safe":        ce.SafeAddress,
			"usdc":        ce.USDCAddress.Hex(),
			"signerCount": len(cfg.SafeSignerKeys),
		}).Info("Lucky draw: chain client ready")
	}

	return &Service{
		cfg:    cfg,
		chains: chains,
		store:  store,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// SupportedChains returns all chain IDs this instance can drip on.
func (s *Service) SupportedChains() []string {
	ids := make([]string, 0, len(s.chains))
	for id := range s.chains {
		ids = append(ids, id)
	}
	return ids
}

// ─── Eligibility rules ────────────────────────────────────────────────────────
//
//  1. The EIP-712 signature must recover to an owner of the claimed proxy Safe
//     on the specified chain.  This proves the caller holds the stealth key.
//     Holding the stealth key IS proof of bridging — the TEE only issues stealth
//     private keys to recipients of a Tachyon bridge settlement.
//
//  2. One drip per stealth identity, forever, across all chains and proxies.
//     A user who bridges to Base and Horizen can still only enter once.
//
// ─────────────────────────────────────────────────────────────────────────────

// Enter validates eligibility, runs the lucky draw, and drips USDC.
func (s *Service) Enter(ctx context.Context, req EnterRequest) (*EnterResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Resolve chain — any supported chain can be the drip destination.
	cc, ok := s.chains[req.ChainID]
	if !ok {
		return nil, fmt.Errorf("chain %s not supported (configured: %v)", req.ChainID, s.SupportedChains())
	}
	chainID, _ := new(big.Int).SetString(req.ChainID, 10)

	proxyAddr := common.HexToAddress(req.ProxyAddress)
	normalizedProxy := proxyAddr.Hex()

	// 1. Recover stealth address from EIP-712 LuckyDrawEntry signature.
	sigBytes, err := hexutil.Decode(req.Signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}
	stealthAddr, err := stealth.RecoverLuckyDrawEntry(sigBytes, proxyAddr, chainID)
	if err != nil {
		return nil, fmt.Errorf("signature recovery failed: %w", err)
	}

	// 2. Confirm the stealth address is an owner of the proxy Safe on-chain.
	//    Owning the stealth key (and therefore the Safe) is the proof of bridging —
	//    no separate USDC balance check needed.
	if err := verifySafeOwner(ctx, cc.ethRPC, proxyAddr, stealthAddr); err != nil {
		return nil, fmt.Errorf("ownership check failed: %w", err)
	}

	// 3. One entry per stealth identity (chain-agnostic).
	//    A user who has proxies on multiple chains can only win once.
	dbKey := entryKey(stealthAddr.Hex())
	exists, err := s.store.Exists(dbKey)
	if err != nil {
		return nil, fmt.Errorf("DB check: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("stealth address %s has already entered the lucky draw", stealthAddr.Hex())
	}

	// 5. Lucky draw — random win/lose.
	//    WinChance is typically 0.33 (1-in-3). Every eligible stealth address
	//    may enter once, but only winners receive a USDC drip.
	won := s.rng.Float64() < s.cfg.WinChance

	if !won {
		// Record the loss so the stealth address cannot enter again.
		entry := Entry{
			StealthAddress: stealthAddr.Hex(),
			ProxyAddress:   normalizedProxy,
			ChainID:        req.ChainID,
			Won:            false,
			AmountUSDC:     0,
			AmountRaw:      "0",
			TxHash:         "",
			EnteredAt:      time.Now().Unix(),
		}
		if err := s.store.PutJSON(dbKey, entry); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"stealth": stealthAddr.Hex(),
			}).Warn("Lucky draw: failed to persist losing entry — address may re-enter on retry")
		}
		log.WithFields(log.Fields{
			"stealth":    stealthAddr.Hex(),
			"proxy":      normalizedProxy,
			"chainId":    req.ChainID,
			"winChance":  s.cfg.WinChance,
		}).Info("Lucky draw: entry recorded, no drip (did not win)")
		return &EnterResult{
			Won:            false,
			StealthAddress: stealthAddr.Hex(),
			ProxyAddress:   normalizedProxy,
			ChainID:        req.ChainID,
			AmountUSDC:     0,
			AmountRaw:      "0",
			TxHash:         "",
		}, nil
	}

	// 6. Winner: draw a random amount (whole USDC, inclusive range).
	amountUSDC := s.cfg.MinUSDC
	if s.cfg.MaxUSDC > s.cfg.MinUSDC {
		amountUSDC += s.rng.Intn(s.cfg.MaxUSDC - s.cfg.MinUSDC + 1)
	}
	amountRaw := new(big.Int).Mul(big.NewInt(int64(amountUSDC)), big.NewInt(1_000_000)) // USDC = 6 decimals

	// 7. Transfer USDC from the team Safe to the user's proxy Safe.
	//    ExecuteViaSafe fetches the Safe nonce, signs with all keys (TEE + optional admin),
	//    sorts by address, and submits execTransaction from the relayer EOA.
	transferData, err := encodeERC20Transfer(proxyAddr, amountRaw)
	if err != nil {
		return nil, fmt.Errorf("encode ERC20 transfer: %w", err)
	}
	tx, err := cc.safeClient.ExecuteViaSafe(ctx, cc.usdcAddress, transferData)
	if err != nil {
		return nil, fmt.Errorf("Safe transfer failed: %w", err)
	}

	// 8. Persist winning entry — log loudly if it fails (funds already sent).
	entry := Entry{
		StealthAddress: stealthAddr.Hex(),
		ProxyAddress:   normalizedProxy,
		ChainID:        req.ChainID,
		Won:            true,
		AmountUSDC:     amountUSDC,
		AmountRaw:      amountRaw.String(),
		TxHash:         tx.Hash().Hex(),
		EnteredAt:      time.Now().Unix(),
	}
	if err := s.store.PutJSON(dbKey, entry); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"stealth": stealthAddr.Hex(),
			"proxy":   normalizedProxy,
			"chain":   req.ChainID,
			"txHash":  tx.Hash().Hex(),
		}).Error("Lucky draw: USDC sent but entry could not be persisted — manual reconciliation required")
	}

	log.WithFields(log.Fields{
		"stealth":    stealthAddr.Hex(),
		"proxy":      normalizedProxy,
		"chainId":    req.ChainID,
		"amountUSDC": amountUSDC,
		"txHash":     tx.Hash().Hex(),
	}).Info("Lucky draw: winner — USDC dripped")

	return &EnterResult{
		Won:            true,
		StealthAddress: stealthAddr.Hex(),
		ProxyAddress:   normalizedProxy,
		ChainID:        req.ChainID,
		AmountUSDC:     amountUSDC,
		AmountRaw:      amountRaw.String(),
		TxHash:         tx.Hash().Hex(),
	}, nil
}

// Status returns the draw state for a stealth address.
// The stealth address is chain-agnostic — one result covers all chains.
func (s *Service) Status(stealthAddress string) (*StatusResult, error) {
	normalized := common.HexToAddress(stealthAddress).Hex()

	var entry Entry
	if err := s.store.GetJSON(entryKey(normalized), &entry); err != nil {
		return &StatusResult{Entered: false, StealthAddress: normalized}, nil
	}
	return &StatusResult{
		Entered:        true,
		Won:            entry.Won,
		StealthAddress: normalized,
		ProxyAddress:   entry.ProxyAddress,
		ChainID:        entry.ChainID,
		AmountUSDC:     entry.AmountUSDC,
		TxHash:         entry.TxHash,
		EnteredAt:      entry.EnteredAt,
	}, nil
}

// ListEntries returns all recorded entries (admin use).
func (s *Service) ListEntries() ([]Entry, error) {
	raw, err := s.store.GetAllByPrefix(entryPrefix)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(raw))
	for _, v := range raw {
		var e Entry
		if err := json.Unmarshal(v, &e); err == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// entryKey builds the PebbleDB key for a stealth address (the dedup unit).
func entryKey(stealthAddress string) string {
	return entryPrefix + stealthAddress
}

// ─── on-chain read helpers ────────────────────────────────────────────────────

var safeGetOwnersABI = mustParseABI(`[{
	"inputs": [],
	"name": "getOwners",
	"outputs": [{"internalType":"address[]","name":"","type":"address[]"}],
	"stateMutability": "view",
	"type": "function"
}]`)

var erc20TransferABI = mustParseABI(`[{
	"inputs": [
		{"internalType":"address","name":"to","type":"address"},
		{"internalType":"uint256","name":"amount","type":"uint256"}
	],
	"name": "transfer",
	"outputs": [{"internalType":"bool","name":"","type":"bool"}],
	"stateMutability": "nonpayable",
	"type": "function"
}]`)

func verifySafeOwner(ctx context.Context, rpc *ethclient.Client, safeAddr, signer common.Address) error {
	c := bind.NewBoundContract(safeAddr, safeGetOwnersABI, rpc, rpc, rpc)
	var result []interface{}
	if err := c.Call(&bind.CallOpts{Context: ctx}, &result, "getOwners"); err != nil {
		return fmt.Errorf("getOwners(%s): %w", safeAddr.Hex(), err)
	}
	owners, ok := result[0].([]common.Address)
	if !ok {
		return fmt.Errorf("unexpected getOwners return type")
	}
	for _, o := range owners {
		if o == signer {
			return nil
		}
	}
	return fmt.Errorf("recovered stealth address %s is not an owner of proxy Safe %s", signer.Hex(), safeAddr.Hex())
}


func encodeERC20Transfer(to common.Address, amount *big.Int) ([]byte, error) {
	return erc20TransferABI.Pack("transfer", to, amount)
}

func mustParseABI(jsonStr string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(jsonStr))
	if err != nil {
		panic("luckydraw: invalid ABI literal: " + err.Error())
	}
	return a
}
