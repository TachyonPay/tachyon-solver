package rebalancer

import (
	"context"
	"math/big"
	"testing"
)

// --- helpers --------------------------------------------------------------

var (
	keyUSDCBase    = Key{ChainID: ChainIDBase, Token: TokenUSDC}
	keyUSDCHorizen = Key{ChainID: ChainIDHorizen, Token: TokenUSDC}
	keyZENBase     = Key{ChainID: ChainIDBase, Token: TokenZEN}
	keyZENHorizen  = Key{ChainID: ChainIDHorizen, Token: TokenZEN}
	keyWETHBase    = Key{ChainID: ChainIDBase, Token: TokenWETH}
)

// bi parses a decimal string into *big.Int. Panics on bad input.
func bi(s string) *big.Int {
	out, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad bigint: " + s)
	}
	return out
}

// snap builds a Snapshot from balance + target maps.
func snap(balances, targets map[Key]*big.Int) *Snapshot {
	s := &Snapshot{
		Balances: make(map[Key]*big.Int, len(balances)),
		Targets:  make(map[Key]*big.Int, len(targets)),
	}
	for k, v := range balances {
		s.Balances[k] = new(big.Int).Set(v)
	}
	for k, v := range targets {
		s.Targets[k] = new(big.Int).Set(v)
	}
	return s
}

// testCfg returns a Config with standard 30%/10% rules.
func testCfg() *Config {
	return &Config{
		Enabled:      true,
		ThresholdPct: 0.30,
		BufferPct:    0.10,
		SlippagePct:  0.01,
	}
}

// expected describes a (kind, src→dst, srcToken→dstToken) tuple we expect
// in the plan, in order. Amount is not asserted (varies with price/heuristics).
type expected struct {
	Kind   ActionKind
	Src    string
	Dst    string
	SrcTok TokenSym
	DstTok TokenSym
}

func assertActions(t *testing.T, plan *Plan, want []expected) {
	t.Helper()
	if len(plan.Actions) != len(want) {
		t.Fatalf("expected %d actions, got %d:\n  actions: %+v\n  notes:   %v",
			len(want), len(plan.Actions), plan.Actions, plan.Notes)
	}
	for i, w := range want {
		a := plan.Actions[i]
		if a.Kind != w.Kind ||
			a.SrcChainID != w.Src || a.DstChainID != w.Dst ||
			a.SrcToken != w.SrcTok || a.DstToken != w.DstTok {
			t.Errorf("action[%d] mismatch:\n  want: %s %s→%s %s→%s\n  got:  %s %s→%s %s→%s",
				i, w.Kind, w.Src, w.Dst, w.SrcTok, w.DstTok,
				a.Kind, a.SrcChainID, a.DstChainID, a.SrcToken, a.DstToken)
		}
	}
}

// neutralBuckets returns balances + targets where the listed buckets are in
// the neutral zone (no deficit, no surplus). Used to keep tests focused on
// the bucket(s) under test.
func neutralBuckets(buckets ...Key) (map[Key]*big.Int, map[Key]*big.Int) {
	bal := make(map[Key]*big.Int)
	tgt := make(map[Key]*big.Int)
	for _, k := range buckets {
		// Pick a balance equal to target * 0.5 — above threshold (0.3) and
		// below floor (0.9), i.e. neutral.
		switch k.Token {
		case TokenUSDC:
			tgt[k] = big.NewInt(1_000_000)
			bal[k] = big.NewInt(500_000)
		case TokenZEN, TokenWETH:
			tgt[k] = bi("1000000000000000000")
			bal[k] = bi("500000000000000000")
		}
	}
	return bal, tgt
}

// merge combines multiple maps into one, later maps override earlier on collision.
func merge(maps ...map[Key]*big.Int) map[Key]*big.Int {
	out := make(map[Key]*big.Int)
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// --- USDC scenarios -------------------------------------------------------

func TestPlan_USDC_BaseShort_HorizenSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyZENBase, keyZENHorizen)
	bal[keyUSDCBase] = big.NewInt(100_000)        // 0.1 USDC, target 1 → deficit
	bal[keyUSDCHorizen] = big.NewInt(10_000_000)  // 10 USDC.e, target 1 → surplus
	tgt[keyUSDCBase] = big.NewInt(1_000_000)
	tgt[keyUSDCHorizen] = big.NewInt(1_000_000)

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionStargateUSDC, ChainIDHorizen, ChainIDBase, TokenUSDC, TokenUSDC},
	})
}

func TestPlan_USDC_HorizenShort_BaseSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyZENBase, keyZENHorizen)
	bal[keyUSDCBase] = big.NewInt(10_000_000)
	bal[keyUSDCHorizen] = big.NewInt(100_000)
	tgt[keyUSDCBase] = big.NewInt(1_000_000)
	tgt[keyUSDCHorizen] = big.NewInt(1_000_000)

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionStargateUSDC, ChainIDBase, ChainIDHorizen, TokenUSDC, TokenUSDC},
	})
}

func TestPlan_USDC_HorizenShort_NoUSDCBase_ZENBaseSurplus(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(500_000),                     // neutral
		keyUSDCHorizen: big.NewInt(100_000),                     // deficit
		keyZENBase:     bi("10000000000000000000"),              // 10 ZEN, surplus
		keyZENHorizen:  bi("500000000000000000"),                // neutral
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenZEN, TokenUSDC},
		{ActionStargateUSDC, ChainIDBase, ChainIDHorizen, TokenUSDC, TokenUSDC},
	})
}

func TestPlan_USDC_BaseShort_NoUSDCHorizen_ZENBaseSurplus(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(100_000),                     // deficit
		keyUSDCHorizen: big.NewInt(500_000),                     // neutral
		keyZENBase:     bi("10000000000000000000"),              // surplus
		keyZENHorizen:  bi("500000000000000000"),                // neutral
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		// Single swap on Base — no bridge needed, deficit is on Base.
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenZEN, TokenUSDC},
	})
}

func TestPlan_USDC_BaseShort_NothingAvailable(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(100_000), // deficit
		keyUSDCHorizen: big.NewInt(500_000), // neutral
		keyZENBase:     bi("500000000000000000"),
		keyZENHorizen:  bi("500000000000000000"),
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, nil)
	if len(plan.Notes) == 0 {
		t.Errorf("expected at least one Note explaining no source available")
	}
}

// --- ZEN scenarios --------------------------------------------------------

func TestPlan_ZEN_BaseShort_HorizenSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen)
	bal[keyZENBase] = bi("100000000000000000")    // 0.1 ZEN, target 1 → deficit
	bal[keyZENHorizen] = bi("10000000000000000000") // surplus
	tgt[keyZENBase] = bi("1000000000000000000")
	tgt[keyZENHorizen] = bi("1000000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionOFTZEN, ChainIDHorizen, ChainIDBase, TokenZEN, TokenZEN},
	})
}

func TestPlan_ZEN_HorizenShort_BaseSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen)
	bal[keyZENBase] = bi("10000000000000000000")
	bal[keyZENHorizen] = bi("100000000000000000")
	tgt[keyZENBase] = bi("1000000000000000000")
	tgt[keyZENHorizen] = bi("1000000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionOFTZEN, ChainIDBase, ChainIDHorizen, TokenZEN, TokenZEN},
	})
}

func TestPlan_ZEN_HorizenShort_NoZENBase_USDCBaseSurplus(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(10_000_000),                  // surplus
		keyUSDCHorizen: big.NewInt(500_000),                     // neutral
		keyZENBase:     bi("500000000000000000"),                // neutral
		keyZENHorizen:  bi("100000000000000000"),                // deficit
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenUSDC, TokenZEN},
		{ActionOFTZEN, ChainIDBase, ChainIDHorizen, TokenZEN, TokenZEN},
	})
}

func TestPlan_ZEN_BaseShort_NoZENHorizen_USDCBaseSurplus(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(10_000_000),                  // surplus
		keyUSDCHorizen: big.NewInt(500_000),                     // neutral
		keyZENBase:     bi("100000000000000000"),                // deficit
		keyZENHorizen:  bi("500000000000000000"),                // neutral
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		// Single swap — deficit is on Base, no bridge needed.
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenUSDC, TokenZEN},
	})
}

func TestPlan_ZEN_BaseShort_NoOtherZEN_NoUSDCBase_USDCHorizenSurplus(t *testing.T) {
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(500_000),                     // neutral
		keyUSDCHorizen: big.NewInt(10_000_000),                  // surplus
		keyZENBase:     bi("100000000000000000"),                // deficit
		keyZENHorizen:  bi("500000000000000000"),                // neutral
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("1000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	// Two-leg: bridge USDC.e Horizen→Base, then swap USDC→ZEN on Base.
	assertActions(t, plan, []expected{
		{ActionStargateUSDC, ChainIDHorizen, ChainIDBase, TokenUSDC, TokenUSDC},
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenUSDC, TokenZEN},
	})
}

// --- WETH scenarios -------------------------------------------------------

func TestPlan_WETH_BaseShort_USDCBaseSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCHorizen, keyZENBase, keyZENHorizen)
	bal[keyUSDCBase] = big.NewInt(10_000_000)
	bal[keyWETHBase] = bi("0")
	tgt[keyUSDCBase] = big.NewInt(1_000_000)
	tgt[keyWETHBase] = bi("1000000000000000") // 0.001 WETH

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenUSDC, TokenWETH},
	})
}

func TestPlan_WETH_BaseShort_NoUSDCBase_ZENBaseSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen, keyZENHorizen)
	bal[keyZENBase] = bi("10000000000000000000")
	bal[keyWETHBase] = bi("0")
	tgt[keyZENBase] = bi("1000000000000000000")
	tgt[keyWETHBase] = bi("1000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, []expected{
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenZEN, TokenWETH},
	})
}

func TestPlan_WETH_BaseShort_OnlyUSDCHorizenSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyZENBase, keyZENHorizen)
	bal[keyUSDCHorizen] = big.NewInt(10_000_000)
	bal[keyWETHBase] = bi("0")
	tgt[keyUSDCHorizen] = big.NewInt(1_000_000)
	tgt[keyWETHBase] = bi("1000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	// Two-leg: bridge USDC.e Horizen→Base, then swap USDC→WETH on Base.
	assertActions(t, plan, []expected{
		{ActionStargateUSDC, ChainIDHorizen, ChainIDBase, TokenUSDC, TokenUSDC},
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenUSDC, TokenWETH},
	})
}

func TestPlan_WETH_BaseShort_OnlyZENHorizenSurplus(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen, keyZENBase)
	bal[keyZENHorizen] = bi("10000000000000000000")
	bal[keyWETHBase] = bi("0")
	tgt[keyZENHorizen] = bi("1000000000000000000")
	tgt[keyWETHBase] = bi("1000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	// Two-leg: bridge ZEN Horizen→Base via OFT, then swap ZEN→WETH on Base.
	assertActions(t, plan, []expected{
		{ActionOFTZEN, ChainIDHorizen, ChainIDBase, TokenZEN, TokenZEN},
		{ActionAerodromeSwap, ChainIDBase, ChainIDBase, TokenZEN, TokenWETH},
	})
}

func TestPlan_WETH_BaseShort_NothingAvailable(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen, keyZENBase, keyZENHorizen)
	bal[keyWETHBase] = bi("0")
	tgt[keyWETHBase] = bi("1000000000000000")

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, nil)
	if len(plan.Notes) == 0 {
		t.Errorf("expected Note about no source for WETH deficit")
	}
}

// --- meta -----------------------------------------------------------------

func TestPlan_AllNeutral_NoActions(t *testing.T) {
	bal, tgt := neutralBuckets(keyUSDCBase, keyUSDCHorizen, keyZENBase, keyZENHorizen, keyWETHBase)
	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	assertActions(t, plan, nil)
	if len(plan.Notes) != 0 {
		t.Errorf("expected no notes for all-neutral plan, got: %v", plan.Notes)
	}
}

func TestPlan_DeficitOrdering_BiggestFirst(t *testing.T) {
	// Use two single-action deficits so action[0] = (whichever deficit was
	// planned first). Bigger deficit should win:
	//   ZEN@base shortfall = 9.9 ZEN  (raw 9.9e18)
	//   USDC@base shortfall = 0.9 USDC (raw 900_000)
	// Numerically the ZEN deficit is huge (different decimals!), but both
	// are sorted purely by raw *big.Int comparison — so ZEN@base wins.
	bal := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(100_000),                  // deficit 900_000
		keyUSDCHorizen: big.NewInt(10_000_000),               // surplus
		keyZENBase:     bi("100000000000000000"),             // 0.1 ZEN, deficit 9.9e18
		keyZENHorizen:  bi("10000000000000000000"),           // surplus
	}
	tgt := map[Key]*big.Int{
		keyUSDCBase:    big.NewInt(1_000_000),
		keyUSDCHorizen: big.NewInt(1_000_000),
		keyZENBase:     bi("10000000000000000000"),
		keyZENHorizen:  bi("1000000000000000000"),
	}

	plan := BuildPlan(context.Background(), snap(bal, tgt), testCfg(), nil)
	if len(plan.Actions) < 2 {
		t.Fatalf("expected 2 actions, got %d", len(plan.Actions))
	}
	if plan.Actions[0].DstToken != TokenZEN {
		t.Errorf("expected first action to fill ZEN deficit (bigger raw), got %s→%s",
			plan.Actions[0].SrcToken, plan.Actions[0].DstToken)
	}
	if plan.Actions[1].DstToken != TokenUSDC {
		t.Errorf("expected second action to fill USDC deficit, got %s→%s",
			plan.Actions[1].SrcToken, plan.Actions[1].DstToken)
	}
}
