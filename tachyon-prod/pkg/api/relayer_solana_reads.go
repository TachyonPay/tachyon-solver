// FE-facing read endpoints + Helius webhook receiver.
//
// These are the wire surface the frontend needs to build Anchor
// instructions against the bridge_intent program without itself holding
// any chain config. Everything returned here is non-sensitive (program
// IDs, allowlist mints, derived PDAs, on-chain account state).

package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// RegisterSolanaReadRoutes adds the FE-facing GET endpoints and the
// Helius webhook POST. Called from cmd/relayer/main.go alongside
// RegisterSolanaRoutes.
func (api *RelayerAPI) RegisterSolanaReadRoutes(r *gin.Engine) {
	g := r.Group("/solana")
	{
		g.GET("/program-info", api.SolanaProgramInfo)
		g.GET("/program-info/:chainId", api.SolanaProgramInfoByChainID)
		g.GET("/intent/:chainId/:intentId", api.SolanaGetIntent)
		g.GET("/solved/:srcChainId/:srcIntentId", api.SolanaIsSolvedAcrossChains)
		g.POST("/helius-webhook", api.SolanaHeliusWebhook)
	}
}

// ----- /solana/program-info -----

type solanaProgramInfoEntry struct {
	Name              string             `json:"name"`
	ChainID           string             `json:"chainId"`           // LZ EID, stringified
	ProgramID         string             `json:"programId"`         // base58
	USDCMint          string             `json:"usdcMint"`          // base58
	WSOLMint          string             `json:"wsolMint"`          // base58
	Commitment        string             `json:"commitment"`
	Explorer          string             `json:"explorer,omitempty"`
	ConfigPDA         string             `json:"configPda"`         // base58
	EscrowAuthorityPDA string            `json:"escrowAuthorityPda"`// base58
	Seeds             solanaSeedsExport  `json:"seeds"`
}

// solanaSeedsExport is a fixed copy of the on-chain seed prefixes the FE
// needs to derive PDAs locally (intent records, bid lists, solved
// records, stealth registrations, relayer auth). Hex so the FE doesn't
// have to hardcode raw bytes.
type solanaSeedsExport struct {
	Config         string `json:"config"`         // hex of "config"
	Escrow         string `json:"escrow"`         // hex of "escrow"
	Intent         string `json:"intent"`         // hex of "intent"
	Bids           string `json:"bids"`           // hex of "bids"
	Solved         string `json:"solved"`         // hex of "solved"
	Stealth        string `json:"stealth"`        // hex of "stealth"
	Relayer        string `json:"relayer"`        // hex of "relayer"
}

func solanaSeeds() solanaSeedsExport {
	return solanaSeedsExport{
		Config:  hex.EncodeToString([]byte("config")),
		Escrow:  hex.EncodeToString([]byte("escrow")),
		Intent:  hex.EncodeToString([]byte("intent")),
		Bids:    hex.EncodeToString([]byte("bids")),
		Solved:  hex.EncodeToString([]byte("solved")),
		Stealth: hex.EncodeToString([]byte("stealth")),
		Relayer: hex.EncodeToString([]byte("relayer")),
	}
}

// SolanaProgramInfo returns the per-chain program info for ALL configured
// Solana chains. The FE typically calls this once on boot and caches.
func (api *RelayerAPI) SolanaProgramInfo(c *gin.Context) {
	cfgs := api.relayer.SolanaConfigs()
	clients := api.relayer.SolanaClients()

	out := make(map[string]solanaProgramInfoEntry, len(cfgs))
	for chainID, cfg := range cfgs {
		entry := solanaProgramInfoEntry{
			Name:       cfg.Name,
			ChainID:    cfg.ChainID,
			ProgramID:  cfg.ProgramID,
			USDCMint:   cfg.USDCMint,
			WSOLMint:   cfg.WSOLMint,
			Commitment: cfg.Commitment,
			Explorer:   cfg.Explorer,
			Seeds:      solanaSeeds(),
		}
		if sc, ok := clients[chainID]; ok {
			if pda, _, err := sc.ConfigPDA(); err == nil {
				entry.ConfigPDA = pda.String()
			}
			if pda, _, err := sc.EscrowAuthorityPDA(); err == nil {
				entry.EscrowAuthorityPDA = pda.String()
			}
		}
		out[chainID] = entry
	}
	c.JSON(http.StatusOK, gin.H{"chains": out})
}

// SolanaProgramInfoByChainID returns a single chain's entry. Convenience
// for the FE when it only cares about the active cluster.
func (api *RelayerAPI) SolanaProgramInfoByChainID(c *gin.Context) {
	chainID := c.Param("chainId")
	cfgs := api.relayer.SolanaConfigs()
	cfg, ok := cfgs[chainID]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "chainId not configured as solana"})
		return
	}
	entry := solanaProgramInfoEntry{
		Name:       cfg.Name,
		ChainID:    cfg.ChainID,
		ProgramID:  cfg.ProgramID,
		USDCMint:   cfg.USDCMint,
		WSOLMint:   cfg.WSOLMint,
		Commitment: cfg.Commitment,
		Explorer:   cfg.Explorer,
		Seeds:      solanaSeeds(),
	}
	if sc := api.relayer.FindSolanaClientByChainID(chainID); sc != nil {
		if pda, _, err := sc.ConfigPDA(); err == nil {
			entry.ConfigPDA = pda.String()
		}
		if pda, _, err := sc.EscrowAuthorityPDA(); err == nil {
			entry.EscrowAuthorityPDA = pda.String()
		}
	}
	c.JSON(http.StatusOK, entry)
}

// ----- /solana/intent/:chainId/:intentId -----

type solanaIntentJSON struct {
	IntentID        string `json:"intentId"`
	User            string `json:"user"`            // base58
	TokenAMint      string `json:"tokenAMint"`      // base58
	TokenBMint      string `json:"tokenBMint"`      // base58
	AmountA         string `json:"amountA"`         // base-10 string for u64-safe JSON
	ExpectedAmountB string `json:"expectedAmountB"`
	Reward          string `json:"reward"`
	DestChainID     string `json:"destChainId"`
	Recipient       string `json:"recipient"`       // base58 (PDA / pubkey) or 0x-hex (EVM addr left-padded)
	EndTime         int64  `json:"endTime"`
	WinningSolver   string `json:"winningSolver"`   // base58, empty when default
	WinningBid      string `json:"winningBid"`
	Completed       bool   `json:"completed"`
	Deposited       bool   `json:"deposited"`
}

// SolanaGetIntent reads an `IntentRecord` PDA from the configured Solana
// chain and returns a JSON-friendly snapshot.
func (api *RelayerAPI) SolanaGetIntent(c *gin.Context) {
	chainID := c.Param("chainId")
	intentIDStr := c.Param("intentId")

	sc := api.relayer.FindSolanaClientByChainID(chainID)
	if sc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "chainId not configured as solana"})
		return
	}

	intentID, ok := new(big.Int).SetString(intentIDStr, 10)
	if !ok || intentID.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid intentId"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	snap, err := sc.GetIntent(ctx, intentID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Recipient: render as base58 (Solana pubkey) by default. If the
	// dest chain is an EVM EID and the high 12 bytes are zero (left-pad),
	// render as 0x-hex of the trailing 20 bytes for FE friendliness.
	recipientStr := solana.PublicKeyFromBytes(snap.Recipient.Bytes).String()
	if isLeftPaddedEVMAddr(snap.Recipient.Bytes) {
		recipientStr = "0x" + hex.EncodeToString(snap.Recipient.Bytes[12:])
	}

	out := solanaIntentJSON{
		IntentID:        snap.IntentID.String(),
		User:            solana.PublicKeyFromBytes(snap.User.Bytes).String(),
		TokenAMint:      solana.PublicKeyFromBytes(snap.TokenA.Bytes).String(),
		TokenBMint:      solana.PublicKeyFromBytes(snap.TokenB.Bytes).String(),
		AmountA:         snap.AmountA.String(),
		ExpectedAmountB: snap.ExpectedAmountB.String(),
		Reward:          snap.Reward.String(),
		DestChainID:     snap.DestChainID.String(),
		Recipient:       recipientStr,
		EndTime:         snap.EndTime,
		WinningSolver:   solana.PublicKeyFromBytes(snap.WinningSolver.Bytes).String(),
		WinningBid:      snap.WinningBid.String(),
		Completed:       snap.Completed,
		Deposited:       snap.Deposited,
	}
	c.JSON(http.StatusOK, out)
}

func isLeftPaddedEVMAddr(b []byte) bool {
	if len(b) != 32 {
		return false
	}
	for i := 0; i < 12; i++ {
		if b[i] != 0 {
			return false
		}
	}
	// And at least one of the trailing 20 bytes is non-zero (else it's
	// just the default zero pubkey, render as base58 zeros).
	for i := 12; i < 32; i++ {
		if b[i] != 0 {
			return true
		}
	}
	return false
}

// ----- /solana/solved/:srcChainId/:srcIntentId -----

// SolanaIsSolvedAcrossChains reports whether the (srcChainId,
// srcIntentId) pair has been solved on ANY configured Solana cluster.
// Used by FEs polling EVM->Solana intent status.
func (api *RelayerAPI) SolanaIsSolvedAcrossChains(c *gin.Context) {
	srcChainIDStr := c.Param("srcChainId")
	srcIntentIDStr := c.Param("srcIntentId")

	srcChainID, ok := new(big.Int).SetString(srcChainIDStr, 10)
	if !ok || srcChainID.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid srcChainId"})
		return
	}
	srcIntentID, ok := new(big.Int).SetString(srcIntentIDStr, 10)
	if !ok || srcIntentID.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid srcIntentId"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	out := gin.H{}
	for chainID, sc := range api.relayer.SolanaClients() {
		solved, _ := sc.IsIntentSolvedOnChain2(ctx, srcChainID, srcIntentID)
		out[chainID] = solved
	}
	c.JSON(http.StatusOK, out)
}

// ----- Helius webhook -----

// heliusWebhookEvent matches Helius's enhanced webhook envelope. We
// accept a flexible map plus the fields we actually need; any unknown
// fields are logged at debug level for forward-compat.
type heliusWebhookEvent struct {
	Signature   string   `json:"signature"`
	Slot        uint64   `json:"slot"`
	Timestamp   int64    `json:"timestamp"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	AccountData []any    `json:"accountData,omitempty"`
	Logs        []string `json:"logs,omitempty"`
	// `meta.logMessages` is the alternative path for raw program logs in
	// some Helius webhook configurations.
	Meta struct {
		LogMessages []string `json:"logMessages,omitempty"`
	} `json:"meta"`
}

// SolanaHeliusWebhook accepts Helius enhanced-webhook POSTs. The
// configured `HELIUS_WEBHOOK_AUTH` header (a shared secret) is required
// — without it we 401 to keep open posters from injecting fake events.
//
// Phase 3 only logs structured events; phase 4 routes them into the
// solver's bid worker (replacing the WS-based listener as the primary
// path in production).
func (api *RelayerAPI) SolanaHeliusWebhook(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	expected := strings.TrimSpace(os.Getenv("HELIUS_WEBHOOK_AUTH"))
	if expected == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "HELIUS_WEBHOOK_AUTH not configured"})
		return
	}
	if !strings.EqualFold(authHeader, expected) && !strings.EqualFold(authHeader, "Bearer "+expected) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook auth"})
		return
	}

	var events []heliusWebhookEvent
	if err := c.ShouldBindJSON(&events); err != nil {
		// Helius can also POST a single object — try once more.
		var single heliusWebhookEvent
		raw, _ := c.GetRawData()
		if jerr := json.Unmarshal(raw, &single); jerr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook body: " + err.Error()})
			return
		}
		events = []heliusWebhookEvent{single}
	}

	for _, evt := range events {
		logs := evt.Logs
		if len(logs) == 0 {
			logs = evt.Meta.LogMessages
		}
		log.WithFields(log.Fields{
			"sig":   evt.Signature,
			"slot":  evt.Slot,
			"type":  evt.Type,
			"src":   evt.Source,
			"nlogs": len(logs),
		}).Info("Helius webhook event received")
		// TODO(phase4): parse logs via blockchain.parseAnchorEvents and
		// dispatch into the solver state machine. The parser is exported
		// in pkg/blockchain (solana_events.go); plumbing the trigger is
		// a follow-up because it requires the relayer to hold a back-
		// reference to the running solver instance, which doesn't exist
		// in the current cmd layout.
	}
	c.JSON(http.StatusOK, gin.H{"accepted": len(events)})
}

