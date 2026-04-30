package rebalancer

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Quoter computes how many srcToken raw units are needed to receive amountOut
// raw units of dstToken via the swap that the rebalancer would execute on
// chainID. The planner uses this to size bridge legs that fund a same-chain
// swap with live on-chain prices.
//
// Implementations should return (nil, error) on any failure; callers will
// fall back to a hardcoded heuristic in that case.
type Quoter interface {
	QuoteIn(ctx context.Context, chainID string, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error)
}

// QuoteIn implements Quoter on EOASigner. Delegates to free function quoteIn,
// which works with any Signer (also used by SafeSigner).
func (s *EOASigner) QuoteIn(ctx context.Context, chainID string, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	return quoteIn(ctx, s, chainID, srcToken, dstToken, amountOut)
}

// quoteIn dispatches to the right Aerodrome quoter based on the pair.
//
//   - USDC ↔ WETH on Base: Aerodrome V2 router (probe-and-extrapolate via
//     getAmountsOut, since V2's getAmountsIn isn't exposed in our ABI subset).
//   - USDC ↔ ZEN on Base: Slipstream QuoterV2.quoteExactOutput, 2-hop via WETH.
//   - ZEN ↔ WETH on Base: Slipstream QuoterV2.quoteExactOutput, 1-hop direct.
//
// Other pairs return an error.
func quoteIn(ctx context.Context, s Signer, chainID string, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	if chainID != ChainIDBase {
		return nil, fmt.Errorf("quoter only supports swaps on Base, got %s", chainID)
	}
	if amountOut == nil || amountOut.Sign() <= 0 {
		return nil, fmt.Errorf("amountOut must be positive")
	}

	switch {
	case (srcToken == TokenUSDC && dstToken == TokenWETH) ||
		(srcToken == TokenWETH && dstToken == TokenUSDC):
		return quoteV2In(ctx, s, srcToken, dstToken, amountOut)

	case (srcToken == TokenUSDC && dstToken == TokenZEN) ||
		(srcToken == TokenZEN && dstToken == TokenUSDC),
		(srcToken == TokenZEN && dstToken == TokenWETH) ||
			(srcToken == TokenWETH && dstToken == TokenZEN):
		return quoteSlipstreamIn(ctx, s, srcToken, dstToken, amountOut)
	}
	return nil, fmt.Errorf("no quoter route for %s→%s", srcToken, dstToken)
}

// quoteV2In probes the V2 USDC/WETH pool with a small input and extrapolates
// linearly to estimate amountIn for the target amountOut. Pads 1% for the
// curve's nonlinearity.
func quoteV2In(ctx context.Context, s Signer, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	srcAddr := TokenAddress(ChainIDBase, srcToken)
	dstAddr := TokenAddress(ChainIDBase, dstToken)

	routes := []aerodromeRoute{{
		From: srcAddr, To: dstAddr,
		Stable: false, Factory: BaseAerodromeFactory,
	}}

	// Probe with 1 unit of srcToken (10^decimals).
	probe := big.NewInt(1)
	for i := uint8(0); i < TokenDecimals(srcToken); i++ {
		probe = new(big.Int).Mul(probe, big.NewInt(10))
	}
	probeOut, err := aerodromeGetAmountsOut(ctx, s, probe, routes)
	if err != nil {
		return nil, fmt.Errorf("v2 probe: %w", err)
	}
	if probeOut.Sign() <= 0 {
		return nil, fmt.Errorf("v2 probe returned zero — pool empty?")
	}
	// amountIn ≈ amountOut * probe / probeOut, padded 1% for curve convexity.
	amountIn := new(big.Int).Mul(amountOut, probe)
	amountIn.Div(amountIn, probeOut)
	amountIn = pctOf(amountIn, 1.01)
	if amountIn.Sign() <= 0 {
		amountIn = big.NewInt(1)
	}
	return amountIn, nil
}

// quoteSlipstreamIn calls QuoterV2.quoteExactOutput with the reversed path.
// Slipstream's path encoding for exact-output is:
//
//	tokenOut(20) | ts(3) | tokenMid(20) | ts(3) | tokenIn(20)
//
// i.e. exactly the reverse of the exact-input path.
func quoteSlipstreamIn(ctx context.Context, s Signer, srcToken, dstToken TokenSym, amountOut *big.Int) (*big.Int, error) {
	srcAddr := TokenAddress(ChainIDBase, srcToken)
	dstAddr := TokenAddress(ChainIDBase, dstToken)

	var hops []slipstreamHop
	switch {
	case (srcToken == TokenUSDC && dstToken == TokenZEN) ||
		(srcToken == TokenZEN && dstToken == TokenUSDC):
		// 2-hop. Reverse path: dstAddr → WETH → srcAddr.
		hops = []slipstreamHop{
			{TokenIn: dstAddr, TickSpacing: BaseAeroZENWETHTickSpc},
			{TokenIn: BaseWETH, TickSpacing: BaseAeroUSDCWETHCLTickSpc},
		}
	case (srcToken == TokenZEN && dstToken == TokenWETH) ||
		(srcToken == TokenWETH && dstToken == TokenZEN):
		hops = []slipstreamHop{
			{TokenIn: dstAddr, TickSpacing: BaseAeroZENWETHTickSpc},
		}
	default:
		return nil, fmt.Errorf("no slipstream path for %s→%s", srcToken, dstToken)
	}

	// Path ends with srcAddr (the input token).
	path, err := encodeSlipstreamPath(hops, srcAddr)
	if err != nil {
		return nil, err
	}

	data, err := slipstreamQuoterABI.Pack("quoteExactOutput", path, amountOut)
	if err != nil {
		return nil, err
	}
	out, err := s.CallReadOnly(ctx, ChainIDBase, BaseSlipstreamQuoter, data)
	if err != nil {
		return nil, fmt.Errorf("quoteExactOutput call: %w", err)
	}
	res, err := slipstreamQuoterABI.Unpack("quoteExactOutput", out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty quoter result")
	}
	amountIn, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected quoteExactOutput return %T", res[0])
	}
	if amountIn.Sign() <= 0 {
		return nil, fmt.Errorf("quoter returned zero amountIn")
	}
	return amountIn, nil
}

// suppress unused-import linter warning for common.Address — referenced
// indirectly via TokenAddress signature.
var _ = common.Address{}
