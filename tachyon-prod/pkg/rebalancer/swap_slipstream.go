package rebalancer

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
)

// slipstreamHop is one segment of a Slipstream path: trade tokenIn at the
// pool with this tickSpacing into the next token in the chain.
type slipstreamHop struct {
	TokenIn     common.Address
	TickSpacing int64
}

// encodeSlipstreamPath builds the packed bytes path used by Aerodrome's
// Slipstream SwapRouter.exactInput / QuoterV2.quoteExactInput.
//
// Layout: tokenIn(20) | ts0(3 BE) | tokenMid(20) | ts1(3 BE) | ... | tokenOut(20)
//
// The tickSpacing is encoded as int24 big-endian in 3 bytes (Slipstream replaces
// Uniswap V3's uint24 fee with the pool's tickSpacing).
func encodeSlipstreamPath(hops []slipstreamHop, tokenOut common.Address) ([]byte, error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("at least one hop required")
	}
	out := make([]byte, 0, 20+(20+3)*len(hops))
	for i, h := range hops {
		if h.TickSpacing < 0 || h.TickSpacing > 0xFFFFFF {
			return nil, fmt.Errorf("hop %d tickSpacing out of int24 range: %d", i, h.TickSpacing)
		}
		out = append(out, h.TokenIn.Bytes()...)
		var tsBytes [3]byte
		// Big-endian 3-byte encoding of the (positive) tickSpacing.
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(h.TickSpacing))
		copy(tsBytes[:], buf[1:])
		out = append(out, tsBytes[:]...)
	}
	out = append(out, tokenOut.Bytes()...)
	return out, nil
}

// slipstreamQuoteExactInput calls QuoterV2.quoteExactInput as eth_call. The
// quoter is non-view but the contract delivers its result via revert+catch
// internally so a static call works.
func slipstreamQuoteExactInput(ctx context.Context, s Signer, path []byte, amountIn *big.Int) (*big.Int, error) {
	data, err := slipstreamQuoterABI.Pack("quoteExactInput", path, amountIn)
	if err != nil {
		return nil, err
	}
	out, err := s.CallReadOnly(ctx, ChainIDBase, BaseSlipstreamQuoter, data)
	if err != nil {
		return nil, fmt.Errorf("slipstream quoter call: %w", err)
	}
	res, err := slipstreamQuoterABI.Unpack("quoteExactInput", out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty quoter result")
	}
	amountOut, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected quoter return %T", res[0])
	}
	return amountOut, nil
}

// slipstreamExactInput submits a multi-hop CL swap via the Slipstream SwapRouter.
type slipstreamExactInputParams struct {
	Path             []byte
	Recipient        common.Address
	Deadline         *big.Int
	AmountIn         *big.Int
	AmountOutMinimum *big.Int
}

func slipstreamExactInput(ctx context.Context, s Signer, path []byte, amountIn, amountOutMin *big.Int) (string, error) {
	params := slipstreamExactInputParams{
		Path:             path,
		Recipient:        s.AddressOnChain(ChainIDBase),
		Deadline:         big.NewInt(time.Now().Unix() + 600),
		AmountIn:         amountIn,
		AmountOutMinimum: amountOutMin,
	}
	data, err := slipstreamRouterABI.Pack("exactInput", params)
	if err != nil {
		return "", fmt.Errorf("pack exactInput: %w", err)
	}
	tx, _, err := s.SendAndWait(ctx, ChainIDBase, BaseSlipstreamRouter, big.NewInt(0), data, 600_000, 0)
	if err != nil {
		return "", err
	}
	return tx.Hash().Hex(), nil
}

// aerodromeSlipstreamSwap performs a Slipstream swap on Base for an arbitrary
// supported pair. Picks single-hop or multi-hop path internally:
//   - USDC ↔ ZEN: 2-hop USDC ↔ WETH ↔ ZEN (both legs tickSpacing 100)
//   - ZEN  ↔ WETH: 1-hop direct (tickSpacing 100)
//   - USDC ↔ WETH: 1-hop direct (tickSpacing 100)
//
// targetOut is the minimum dstToken out we want; the function quotes via
// QuoterV2 to size amountIn so that quoted output >= targetOut * (1 + slippage),
// then submits with amountOutMin = targetOut.
func aerodromeSlipstreamSwap(ctx context.Context, s Signer, srcToken, dstToken TokenSym, targetOut *big.Int, slippagePct float64) (string, error) {
	if targetOut == nil || targetOut.Sign() <= 0 {
		return "", fmt.Errorf("targetOut must be positive")
	}

	srcAddr := TokenAddress(ChainIDBase, srcToken)
	dstAddr := TokenAddress(ChainIDBase, dstToken)
	if srcAddr == (common.Address{}) || dstAddr == (common.Address{}) {
		return "", fmt.Errorf("unsupported pair %s→%s on Base", srcToken, dstToken)
	}

	// Pick the path based on the pair.
	var hops []slipstreamHop
	switch {
	case (srcToken == TokenUSDC && dstToken == TokenZEN) ||
		(srcToken == TokenZEN && dstToken == TokenUSDC):
		// 2-hop via WETH. Both pools are tickSpacing 100.
		hops = []slipstreamHop{
			{TokenIn: srcAddr, TickSpacing: BaseAeroUSDCWETHCLTickSpc},
			{TokenIn: BaseWETH, TickSpacing: BaseAeroZENWETHTickSpc},
		}
	case (srcToken == TokenZEN && dstToken == TokenWETH) ||
		(srcToken == TokenWETH && dstToken == TokenZEN):
		hops = []slipstreamHop{
			{TokenIn: srcAddr, TickSpacing: BaseAeroZENWETHTickSpc},
		}
	case (srcToken == TokenUSDC && dstToken == TokenWETH) ||
		(srcToken == TokenWETH && dstToken == TokenUSDC):
		hops = []slipstreamHop{
			{TokenIn: srcAddr, TickSpacing: BaseAeroUSDCWETHCLTickSpc},
		}
	default:
		return "", fmt.Errorf("no Slipstream path configured for %s→%s", srcToken, dstToken)
	}

	path, err := encodeSlipstreamPath(hops, dstAddr)
	if err != nil {
		return "", err
	}

	return executeSlipstreamSwap(ctx, s, srcToken, dstToken, srcAddr, path, targetOut, slippagePct)
}

// executeSlipstreamSwap is the shared probe/quote/size/approve/execute pipeline.
// Factored out so any path encoding can reuse it.
func executeSlipstreamSwap(
	ctx context.Context,
	s Signer,
	srcToken, dstToken TokenSym,
	srcAddr common.Address,
	path []byte,
	targetOut *big.Int,
	slippagePct float64,
) (string, error) {
	// Probe with 1 unit of srcToken to read the rate. Pad amountIn so quoted
	// output >= targetOut * (1 + 2*slippage): (a) linear scale-up loses
	// precision on convex curves; (b) we want headroom between quoted-out
	// and amountOutMin.
	probe := big.NewInt(1)
	for i := uint8(0); i < TokenDecimals(srcToken); i++ {
		probe = new(big.Int).Mul(probe, big.NewInt(10))
	}
	probeOut, err := slipstreamQuoteExactInput(ctx, s, path, probe)
	if err != nil {
		return "", fmt.Errorf("probe quote: %w", err)
	}
	if probeOut.Sign() <= 0 {
		return "", fmt.Errorf("probe returned zero — pools may be empty or path wrong")
	}

	amountIn := new(big.Int).Mul(targetOut, probe)
	amountIn = new(big.Int).Div(amountIn, probeOut)
	amountIn = pctOf(amountIn, 1.0+slippagePct*2)
	if amountIn.Sign() <= 0 {
		amountIn = big.NewInt(1)
	}

	expectedOut, err := slipstreamQuoteExactInput(ctx, s, path, amountIn)
	if err != nil {
		return "", fmt.Errorf("verify quote: %w", err)
	}
	if expectedOut.Cmp(targetOut) < 0 {
		amountIn = pctOf(amountIn, 1.0+slippagePct)
		expectedOut, err = slipstreamQuoteExactInput(ctx, s, path, amountIn)
		if err != nil {
			return "", fmt.Errorf("verify quote (retry): %w", err)
		}
		if expectedOut.Cmp(targetOut) < 0 {
			return "", fmt.Errorf("can't reach target out %s — best %s for in %s",
				targetOut.String(), expectedOut.String(), amountIn.String())
		}
	}

	amountOutMin := new(big.Int).Set(targetOut)

	if hash, err := ensureAllowance(ctx, s, ChainIDBase, srcAddr, BaseSlipstreamRouter, amountIn); err != nil {
		return "", fmt.Errorf("approve: %w", err)
	} else if hash != "" {
		log.WithFields(log.Fields{
			"token":   srcAddr.Hex(),
			"spender": BaseSlipstreamRouter.Hex(),
			"txHash":  hash,
		}).Info("Approval submitted before Slipstream swap")
	}

	hash, err := slipstreamExactInput(ctx, s, path, amountIn, amountOutMin)
	if err != nil {
		return "", err
	}

	log.WithFields(log.Fields{
		"src":          string(srcToken),
		"dst":          string(dstToken),
		"amountIn":     amountIn.String(),
		"expectedOut":  expectedOut.String(),
		"amountOutMin": amountOutMin.String(),
		"path":         fmt.Sprintf("%x", path),
		"txHash":       hash,
	}).Info("Slipstream swap submitted")

	return hash, nil
}
