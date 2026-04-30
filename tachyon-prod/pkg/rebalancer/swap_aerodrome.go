package rebalancer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
)

// aerodromeRoute mirrors Aerodrome V2 Router's IRouter.Route struct.
type aerodromeRoute struct {
	From    common.Address
	To      common.Address
	Stable  bool
	Factory common.Address
}

// aerodromeGetAmountsOut returns the predicted output along the given V2 route.
func aerodromeGetAmountsOut(ctx context.Context, s Signer, amountIn *big.Int, routes []aerodromeRoute) (*big.Int, error) {
	data, err := aerodromeRouterABI.Pack("getAmountsOut", amountIn, routes)
	if err != nil {
		return nil, err
	}
	out, err := s.CallReadOnly(ctx, ChainIDBase, BaseAerodromeRouter, data)
	if err != nil {
		return nil, fmt.Errorf("getAmountsOut: %w", err)
	}
	res, err := aerodromeRouterABI.Unpack("getAmountsOut", out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty getAmountsOut")
	}
	amounts, ok := res[0].([]*big.Int)
	if !ok || len(amounts) == 0 {
		return nil, fmt.Errorf("unexpected getAmountsOut shape %T", res[0])
	}
	return amounts[len(amounts)-1], nil
}

// aerodromeSwapUSDCWETH performs USDC ↔ WETH on Base via the V2 volatile pool.
//
//   - For dstToken=WETH, the action's Amount is interpreted as the *minimum
//     WETH out we want*. The function reverses-quotes the USDC input by
//     overshooting the deficit by 1/(1-slippagePct) and then calls swapExactTokensForTokens.
//   - For dstToken=USDC, Amount is interpreted as the *USDC we want out*; same
//     overshoot applies on the WETH side.
//
// Returns the swap tx hash on Base.
func aerodromeSwapUSDCWETH(ctx context.Context, s Signer, srcToken, dstToken TokenSym, targetOut *big.Int, slippagePct float64) (string, error) {
	if (srcToken != TokenUSDC || dstToken != TokenWETH) && (srcToken != TokenWETH || dstToken != TokenUSDC) {
		return "", fmt.Errorf("aerodromeSwapUSDCWETH only supports USDC<->WETH, got %s→%s", srcToken, dstToken)
	}
	if targetOut == nil || targetOut.Sign() <= 0 {
		return "", fmt.Errorf("targetOut must be positive")
	}

	srcAddr := TokenAddress(ChainIDBase, srcToken)
	dstAddr := TokenAddress(ChainIDBase, dstToken)

	routes := []aerodromeRoute{{
		From:    srcAddr,
		To:      dstAddr,
		Stable:  false,
		Factory: BaseAerodromeFactory,
	}}

	// Step 1: probe with a 1-unit input to read the current rate, then size
	// amountIn so that getAmountsOut returns >= targetOut * (1 + slippage).
	probe := big.NewInt(1)
	for i := uint8(0); i < TokenDecimals(srcToken); i++ {
		probe = new(big.Int).Mul(probe, big.NewInt(10))
	}
	probeOut, err := aerodromeGetAmountsOut(ctx, s, probe, routes)
	if err != nil {
		return "", fmt.Errorf("probe quote: %w", err)
	}
	if probeOut.Sign() <= 0 {
		return "", fmt.Errorf("probe returned zero — pool may be empty")
	}

	// price = probeOut / probe (in dst raw units per src raw unit, scaled by 1eD_src)
	// amountIn ≈ targetOut * probe / probeOut, then padded for slippage.
	amountIn := new(big.Int).Mul(targetOut, probe)
	amountIn = new(big.Int).Div(amountIn, probeOut)
	amountIn = pctOf(amountIn, 1.0+slippagePct*2) // padding so quote-time rate covers slippage and actual exec

	// Verify with a real quote at the planned amountIn.
	expectedOut, err := aerodromeGetAmountsOut(ctx, s, amountIn, routes)
	if err != nil {
		return "", fmt.Errorf("verify quote: %w", err)
	}
	if expectedOut.Cmp(targetOut) < 0 {
		// Edge case: nonlinear price impact made the linear estimate too low.
		// Bump amountIn by another slippage worth and re-verify.
		amountIn = pctOf(amountIn, 1.0+slippagePct)
		expectedOut, err = aerodromeGetAmountsOut(ctx, s, amountIn, routes)
		if err != nil {
			return "", fmt.Errorf("verify quote (retry): %w", err)
		}
		if expectedOut.Cmp(targetOut) < 0 {
			return "", fmt.Errorf("can't reach target out %s — best %s for in %s", targetOut, expectedOut, amountIn)
		}
	}

	// Floor amountOutMin at targetOut (we accept anything >= targetOut).
	amountOutMin := new(big.Int).Set(targetOut)

	// Approve router.
	if hash, err := ensureAllowance(ctx, s, ChainIDBase, srcAddr, BaseAerodromeRouter, amountIn); err != nil {
		return "", fmt.Errorf("approve: %w", err)
	} else if hash != "" {
		log.WithFields(log.Fields{
			"token":   srcAddr.Hex(),
			"spender": BaseAerodromeRouter.Hex(),
			"txHash":  hash,
		}).Info("Approval submitted before Aerodrome swap")
	}

	deadline := big.NewInt(time.Now().Unix() + 600) // 10 min

	data, err := aerodromeRouterABI.Pack("swapExactTokensForTokens",
		amountIn, amountOutMin, routes, s.AddressOnChain(ChainIDBase), deadline)
	if err != nil {
		return "", fmt.Errorf("pack swap: %w", err)
	}

	tx, _, err := s.SendAndWait(ctx, ChainIDBase, BaseAerodromeRouter, big.NewInt(0), data, 400_000, 0)
	if err != nil {
		return "", err
	}

	log.WithFields(log.Fields{
		"src":          string(srcToken),
		"dst":          string(dstToken),
		"amountIn":     amountIn.String(),
		"amountOutMin": amountOutMin.String(),
		"expectedOut":  expectedOut.String(),
		"txHash":       tx.Hash().Hex(),
	}).Info("Aerodrome swap submitted")

	return tx.Hash().Hex(), nil
}
