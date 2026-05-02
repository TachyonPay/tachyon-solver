package privacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// RegisterWithRelayer announces this privacy router to the upstream
// relayer twice — once per bridge type — so frontends can call
// GET /solvers?type=lifiXtach (or stargateXtach) to discover the
// per-bridge route catalog.
//
// "Address" is a single representative TEE Safe address (we pick the
// first configured chain's Safe); the relayer keys solver entries by
// (type, address), so the same Safe can register multiple types.
func RegisterWithRelayer(cfg *Config) error {
	if cfg.RelayerURL == "" {
		return fmt.Errorf("RELAYER_URL not set; skipping relayer registration")
	}

	addr := representativeSafe(cfg)
	if addr == "" {
		return fmt.Errorf("no TEE Safe configured; cannot register with relayer (run in degraded mode until safes are set)")
	}

	lifiRoutes := LifiSupportedRoutes()
	stargateRoutes := StargateSupportedRoutes()
	if err := postRegistration(cfg, string(SolverTypeLifiXTach), addr, routesToTypes(lifiRoutes), tokensForRoutes(lifiRoutes)); err != nil {
		return fmt.Errorf("lifiXtach register: %w", err)
	}
	if err := postRegistration(cfg, string(SolverTypeStargateXTach), addr, routesToTypes(stargateRoutes), tokensForRoutes(stargateRoutes)); err != nil {
		return fmt.Errorf("stargateXtach register: %w", err)
	}
	return nil
}

// representativeSafe returns the first configured chain's TEE Safe so
// the relayer has a stable string to key on. Iteration over a Go map
// is randomized but the relayer doesn't care which we pick — only that
// it stays the same across the two registrations in this single call.
func representativeSafe(cfg *Config) string {
	for _, ch := range cfg.Chains {
		zero := [20]byte{}
		if ch.TEESafe != zero {
			return ch.TEESafe.Hex()
		}
	}
	return ""
}

// routesToTypes flattens RouteSpec into the relayer's Route shape.
// Chain IDs are passed as numeric strings ("8453") and tokens as
// human-readable symbols ("USDC") — the frontend joins them against
// addresses.go to recover concrete addresses. The tachyon solver uses
// chain *names* in the same fields; the type tag tells consumers which
// convention to apply.
func routesToTypes(specs []RouteSpec) []types.Route {
	out := make([]types.Route, 0, len(specs))
	for _, s := range specs {
		out = append(out, types.Route{
			SourceChain: s.SourceChainID,
			DestChain:   s.DestChainID,
			TokenA:      strings.ToUpper(string(s.SourceToken)),
			TokenB:      strings.ToUpper(string(s.DestToken)),
		})
	}
	return out
}

// tokensForRoutes derives the unique (chainID, symbol) pairs used by
// the route list and returns the per-token metadata the frontend needs
// to render — address (or LI.FI native sentinel for ETH) and decimals.
// Output is sorted by chainID then symbol so registration payloads are
// stable across restarts.
func tokensForRoutes(routes []RouteSpec) []types.TokenInfo {
	type key struct{ chain string; sym Token }
	seen := map[key]bool{}
	out := []types.TokenInfo{}
	add := func(chainID string, sym Token) {
		k := key{chainID, sym}
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, types.TokenInfo{
			ChainID:  chainID,
			Symbol:   strings.ToUpper(string(sym)),
			Address:  TokenAddress(chainID, sym).Hex(),
			Decimals: TokenDecimals(sym),
		})
	}
	for _, r := range routes {
		add(r.SourceChainID, r.SourceToken)
		add(r.DestChainID, r.DestToken)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChainID != out[j].ChainID {
			return out[i].ChainID < out[j].ChainID
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out
}

func postRegistration(cfg *Config, solverType, addr string, routes []types.Route, tokens []types.TokenInfo) error {
	req := types.RegisterSolverRequest{
		Type:          solverType,
		SolverAddress: addr,
		Routes:        routes,
		Tokens:        tokens,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := cfg.RelayerURL + "/register-solver"
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relayer %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed types.RegisterSolverResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if !parsed.Success {
		return fmt.Errorf("relayer rejected: %s", parsed.Error)
	}

	log.WithFields(log.Fields{
		"type":        solverType,
		"address":     addr,
		"routesCount": parsed.RoutesCount,
	}).Info("Privacy router registered with relayer")
	return nil
}
