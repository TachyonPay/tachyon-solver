package rebalancer

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	log "github.com/sirupsen/logrus"
)

// ActionKind enumerates what a single rebalance step does.
type ActionKind string

const (
	ActionStargateUSDC  ActionKind = "stargate_usdc_bridge"  // bridge USDC ↔ USDC.e Base ↔ Horizen
	ActionOFTZEN        ActionKind = "oft_zen_bridge"        // bridge ZEN Base ↔ Horizen via OFT
	ActionAerodromeSwap ActionKind = "aerodrome_swap"        // swap on Base via Aerodrome
)

// Action is one executable rebalance step.
type Action struct {
	Kind       ActionKind
	SrcChainID string   // where the action originates / signs from
	DstChainID string   // where funds end up (same as SrcChainID for swaps)
	SrcToken   TokenSym // what we spend
	DstToken   TokenSym // what we receive (same as SrcToken for bridges)
	Amount     *big.Int // raw units of SrcToken (or DstToken when used as a swap target)
	Reason     string   // human-readable explanation for logs
}

// Plan is the ordered list of actions to bring balances toward targets.
type Plan struct {
	Actions []Action
	Notes   []string // soft warnings / unactionable deficits
}

// BuildPlan turns a snapshot into a list of actions.
//
// `quoter` is optional. When provided, the planner uses it to size bridge legs
// that fund a same-chain swap (e.g. WETH deficit funded by bridging USDC.e
// from Horizen and swapping on Base). When nil, those legs fall back to a
// hardcoded approximate-price heuristic.
//
// Strategy:
//  1. Walk every bucket; collect those in deficit (balance < threshold).
//  2. Sort deficits by raw size descending so we tackle the biggest gap first.
//  3. For each deficit, pick a route. Update an in-memory shadow of balances
//     as we plan, so a single cycle doesn't try to spend the same surplus
//     twice.
func BuildPlan(ctx context.Context, snap *Snapshot, cfg *Config, quoter Quoter) *Plan {
	p := &planner{
		ctx:    ctx,
		cfg:    cfg,
		quoter: quoter,
		plan:   &Plan{},
	}

	buckets := snap.Buckets(cfg)
	p.byKey = make(map[Key]*Bucket, len(buckets))
	for i := range buckets {
		p.byKey[buckets[i].Key] = &buckets[i]
	}

	deficits := make([]*Bucket, 0)
	for i := range buckets {
		if buckets[i].Deficit.Sign() > 0 {
			deficits = append(deficits, &buckets[i])
		}
	}
	sort.Slice(deficits, func(i, j int) bool {
		return deficits[i].Deficit.Cmp(deficits[j].Deficit) > 0
	})

	for _, d := range deficits {
		switch {
		case d.Key.Token == TokenUSDC:
			p.planUSDCDeficit(d)
		case d.Key.Token == TokenZEN:
			p.planZENDeficit(d)
		case d.Key.Token == TokenWETH && d.Key.ChainID == ChainIDBase:
			p.planWETHDeficit(d)
		default:
			p.plan.Notes = append(p.plan.Notes,
				fmt.Sprintf("no route configured for deficit on %s (need %s more)",
					d.Key, d.Deficit.String()))
		}
	}

	return p.plan
}

// planner is the in-flight state for a single BuildPlan call.
type planner struct {
	ctx    context.Context
	cfg    *Config
	quoter Quoter
	plan   *Plan
	byKey  map[Key]*Bucket
}

// planUSDCDeficit covers USDC short on Base or Horizen.
//
// Sources tried in order:
//
//	1. USDC@<other-chain> direct bridge
//	2. ZEN@base swap → bridge if needed
//	(3. ZEN@horizen 3-leg path is left as a Note)
func (p *planner) planUSDCDeficit(d *Bucket) {
	otherChain := ChainIDBase
	if d.Key.ChainID == ChainIDBase {
		otherChain = ChainIDHorizen
	}

	// Step 1 — direct same-token bridge.
	if src, ok := p.byKey[Key{ChainID: otherChain, Token: TokenUSDC}]; ok {
		amount := minBig(d.Deficit, src.Surplus)
		if amount.Sign() > 0 {
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionStargateUSDC,
				SrcChainID: otherChain,
				DstChainID: d.Key.ChainID,
				SrcToken:   TokenUSDC, DstToken: TokenUSDC,
				Amount: amount,
				Reason: fmt.Sprintf("top up USDC@%s by %s from USDC@%s surplus",
					d.Key.ChainID, amount.String(), otherChain),
			})
			d.Balance = new(big.Int).Add(d.Balance, amount)
			d.Deficit = new(big.Int).Sub(d.Deficit, amount)
			src.Balance = new(big.Int).Sub(src.Balance, amount)
			src.Surplus = new(big.Int).Sub(src.Surplus, amount)
		}
	}

	if d.Deficit.Sign() <= 0 {
		return
	}

	// Step 2 — ZEN@base surplus → swap ZEN→USDC on Base, bridge if deficit on Horizen.
	if zenBase, ok := p.byKey[Key{ChainID: ChainIDBase, Token: TokenZEN}]; ok && zenBase.Surplus.Sign() > 0 {
		swapAmount := new(big.Int).Set(d.Deficit) // amountOut target = USDC deficit

		p.plan.Actions = append(p.plan.Actions, Action{
			Kind:       ActionAerodromeSwap,
			SrcChainID: ChainIDBase, DstChainID: ChainIDBase,
			SrcToken:   TokenZEN, DstToken: TokenUSDC,
			Amount: swapAmount,
			Reason: fmt.Sprintf("synthesize USDC@base via ZEN→WETH→USDC Slipstream swap to cover USDC@%s deficit (%s USDC)",
				d.Key.ChainID, d.Deficit.String()),
		})

		if d.Key.ChainID == ChainIDHorizen {
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionStargateUSDC,
				SrcChainID: ChainIDBase, DstChainID: ChainIDHorizen,
				SrcToken:   TokenUSDC, DstToken: TokenUSDC,
				Amount: new(big.Int).Set(d.Deficit),
				Reason: fmt.Sprintf("bridge synthesized USDC@base → USDC@horizen (%s USDC)",
					d.Deficit.String()),
			})
		}

		d.Balance = new(big.Int).Add(d.Balance, d.Deficit)
		d.Deficit = big.NewInt(0)
		return
	}

	p.plan.Notes = append(p.plan.Notes,
		fmt.Sprintf("USDC short on %s by %s — no opposite-chain USDC surplus and no ZEN@base surplus to swap. ZEN@horizen 3-leg path is not yet planned.",
			d.Key.ChainID, d.Deficit.String()))
}

// planZENDeficit covers ZEN short on Base or Horizen.
//
// Sources tried in order:
//
//	1. ZEN@<other-chain> direct OFT bridge
//	2. USDC@base surplus → swap USDC→ZEN, bridge to Horizen if needed
//	3. USDC@horizen surplus, deficit on Base → bridge USDC.e→USDC, swap USDC→ZEN
func (p *planner) planZENDeficit(d *Bucket) {
	otherChain := ChainIDBase
	if d.Key.ChainID == ChainIDBase {
		otherChain = ChainIDHorizen
	}

	// Step 1 — direct same-token OFT bridge.
	if src, ok := p.byKey[Key{ChainID: otherChain, Token: TokenZEN}]; ok {
		amount := minBig(d.Deficit, src.Surplus)
		if amount.Sign() > 0 {
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionOFTZEN,
				SrcChainID: otherChain,
				DstChainID: d.Key.ChainID,
				SrcToken:   TokenZEN, DstToken: TokenZEN,
				Amount: amount,
				Reason: fmt.Sprintf("top up ZEN@%s by %s from ZEN@%s surplus",
					d.Key.ChainID, amount.String(), otherChain),
			})
			d.Balance = new(big.Int).Add(d.Balance, amount)
			d.Deficit = new(big.Int).Sub(d.Deficit, amount)
			src.Balance = new(big.Int).Sub(src.Balance, amount)
			src.Surplus = new(big.Int).Sub(src.Surplus, amount)
		}
	}

	if d.Deficit.Sign() <= 0 {
		return
	}

	// Step 2 — USDC@base surplus → swap USDC→ZEN, bridge if deficit on Horizen.
	if usdcBase, ok := p.byKey[Key{ChainID: ChainIDBase, Token: TokenUSDC}]; ok && usdcBase.Surplus.Sign() > 0 {
		p.plan.Actions = append(p.plan.Actions, Action{
			Kind:       ActionAerodromeSwap,
			SrcChainID: ChainIDBase, DstChainID: ChainIDBase,
			SrcToken:   TokenUSDC, DstToken: TokenZEN,
			Amount: new(big.Int).Set(d.Deficit),
			Reason: fmt.Sprintf("synthesize ZEN@base via USDC→WETH→ZEN Slipstream swap to cover ZEN@%s deficit (%s ZEN)",
				d.Key.ChainID, d.Deficit.String()),
		})
		if d.Key.ChainID == ChainIDHorizen {
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionOFTZEN,
				SrcChainID: ChainIDBase, DstChainID: ChainIDHorizen,
				SrcToken:   TokenZEN, DstToken: TokenZEN,
				Amount: new(big.Int).Set(d.Deficit),
				Reason: fmt.Sprintf("bridge synthesized ZEN@base → ZEN@horizen (%s ZEN)",
					d.Deficit.String()),
			})
		}
		d.Balance = new(big.Int).Add(d.Balance, d.Deficit)
		d.Deficit = big.NewInt(0)
		return
	}

	// Step 3 — USDC.e@horizen surplus + ZEN deficit on base: bridge USDC to
	// base, then swap to ZEN.
	if d.Key.ChainID == ChainIDBase {
		if usdcHz, ok := p.byKey[Key{ChainID: ChainIDHorizen, Token: TokenUSDC}]; ok && usdcHz.Surplus.Sign() > 0 {
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionStargateUSDC,
				SrcChainID: ChainIDHorizen, DstChainID: ChainIDBase,
				SrcToken:   TokenUSDC, DstToken: TokenUSDC,
				Amount: new(big.Int).Set(d.Deficit),
				Reason: fmt.Sprintf("bridge USDC.e@horizen → USDC@base to fund ZEN@base deficit (%s ZEN)",
					d.Deficit.String()),
			})
			p.plan.Actions = append(p.plan.Actions, Action{
				Kind:       ActionAerodromeSwap,
				SrcChainID: ChainIDBase, DstChainID: ChainIDBase,
				SrcToken:   TokenUSDC, DstToken: TokenZEN,
				Amount: new(big.Int).Set(d.Deficit),
				Reason: fmt.Sprintf("swap bridged USDC@base → ZEN@base (%s ZEN)",
					d.Deficit.String()),
			})
			d.Balance = new(big.Int).Add(d.Balance, d.Deficit)
			d.Deficit = big.NewInt(0)
			return
		}
	}

	p.plan.Notes = append(p.plan.Notes,
		fmt.Sprintf("ZEN short on %s by %s — no available source (no ZEN surplus on either chain, no USDC@base surplus, no 2-leg path)",
			d.Key.ChainID, d.Deficit.String()))
}

// planWETHDeficit covers WETH short on Base. WETH only exists on Base, so all
// fallbacks are Base-side swaps (single-hop) plus an optional pre-bridge to
// get the source asset onto Base.
//
//	1. USDC@base surplus → V2 swap USDC→WETH        (cheapest, deepest)
//	2. ZEN@base  surplus → Slipstream swap ZEN→WETH (single-hop CL)
//	3. USDC@horizen surplus → bridge USDC.e→base → swap USDC→WETH
//	4. ZEN@horizen  surplus → bridge ZEN→base → swap ZEN→WETH
//
// Bridge size for cases 3+4 comes from the Quoter when available, falling
// back to a hardcoded heuristic when nil/errors.
func (p *planner) planWETHDeficit(d *Bucket) {
	target := new(big.Int).Set(d.Deficit)

	tryDirectSwap := func(srcToken TokenSym) bool {
		src, ok := p.byKey[Key{ChainID: ChainIDBase, Token: srcToken}]
		if !ok || src.Surplus.Sign() <= 0 {
			return false
		}
		p.plan.Actions = append(p.plan.Actions, Action{
			Kind:       ActionAerodromeSwap,
			SrcChainID: ChainIDBase, DstChainID: ChainIDBase,
			SrcToken:   srcToken, DstToken: TokenWETH,
			Amount: target,
			Reason: fmt.Sprintf("top up WETH@base by ~%s via %s→WETH on Aerodrome",
				target.String(), srcToken),
		})
		d.Deficit = big.NewInt(0)
		return true
	}

	tryBridgeThenSwap := func(srcToken TokenSym, bridgeKind ActionKind) bool {
		src, ok := p.byKey[Key{ChainID: ChainIDHorizen, Token: srcToken}]
		if !ok || src.Surplus.Sign() <= 0 {
			return false
		}
		bridgeAmount := p.quoteSrcForWETH(srcToken, target)
		// Cap at actual surplus so we don't over-promise.
		if bridgeAmount.Cmp(src.Surplus) > 0 {
			bridgeAmount = new(big.Int).Set(src.Surplus)
		}
		p.plan.Actions = append(p.plan.Actions, Action{
			Kind:       bridgeKind,
			SrcChainID: ChainIDHorizen, DstChainID: ChainIDBase,
			SrcToken:   srcToken, DstToken: srcToken,
			Amount: bridgeAmount,
			Reason: fmt.Sprintf("bridge %s@horizen → %s@base (%s) to fund WETH@base deficit (%s WETH)",
				srcToken, srcToken, bridgeAmount.String(), target.String()),
		})
		p.plan.Actions = append(p.plan.Actions, Action{
			Kind:       ActionAerodromeSwap,
			SrcChainID: ChainIDBase, DstChainID: ChainIDBase,
			SrcToken:   srcToken, DstToken: TokenWETH,
			Amount: target,
			Reason: fmt.Sprintf("swap bridged %s@base → WETH@base (%s WETH)",
				srcToken, target.String()),
		})
		d.Deficit = big.NewInt(0)
		return true
	}

	if tryDirectSwap(TokenUSDC) {
		return
	}
	if tryDirectSwap(TokenZEN) {
		return
	}
	if tryBridgeThenSwap(TokenUSDC, ActionStargateUSDC) {
		return
	}
	if tryBridgeThenSwap(TokenZEN, ActionOFTZEN) {
		return
	}

	p.plan.Notes = append(p.plan.Notes,
		fmt.Sprintf("WETH short on Base by %s — no surplus available in USDC@base, ZEN@base, USDC@horizen, or ZEN@horizen to fund the swap",
			d.Deficit.String()))
}

// quoteSrcForWETH returns the amount of srcToken raw units needed to acquire
// `wethTarget` raw WETH on Base. Uses the Quoter when wired, falls back to
// the hardcoded approxSourceForWETH heuristic on nil/error. Adds a 5% buffer
// over the live quote so the swap can absorb price drift between plan and
// execute time.
func (p *planner) quoteSrcForWETH(srcToken TokenSym, wethTarget *big.Int) *big.Int {
	if p.quoter != nil {
		quoted, err := p.quoter.QuoteIn(p.ctx, ChainIDBase, srcToken, TokenWETH, wethTarget)
		if err == nil && quoted != nil && quoted.Sign() > 0 {
			return pctOf(quoted, 1.05)
		}
		log.WithError(err).WithFields(log.Fields{
			"srcToken":   srcToken,
			"wethTarget": wethTarget.String(),
		}).Warn("Quoter failed for WETH funding, falling back to heuristic")
	}
	return approxSourceForWETH(wethTarget, srcToken)
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

// approxSourceForWETH returns a rough estimate of how many srcToken raw units
// are needed to acquire `wethTarget` raw WETH using hardcoded approximate
// prices. Used only as a fallback when Quoter is unavailable.
//
//	WETH = $4000   ZEN = $5   USDC = $1
//
// 1.5x buffer baked in to absorb price drift.
func approxSourceForWETH(wethTarget *big.Int, srcToken TokenSym) *big.Int {
	switch srcToken {
	case TokenUSDC:
		// 1 WETH (1e18 raw) ≈ 4000 USDC ($4000) → 4_000_000_000 raw USDC.
		// Buffered (1.5x) → wethTarget * 6 / 1e9.
		return new(big.Int).Div(
			new(big.Int).Mul(wethTarget, big.NewInt(6)),
			big.NewInt(1_000_000_000),
		)
	case TokenZEN:
		// 1 WETH ≈ 800 ZEN (both 18 decimals). Buffered (1.5x) → 1200.
		return new(big.Int).Mul(wethTarget, big.NewInt(1200))
	}
	return new(big.Int).Set(wethTarget)
}
