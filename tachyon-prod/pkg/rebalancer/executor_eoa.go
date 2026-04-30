package rebalancer

import (
	"context"
	"fmt"
)

// SignerExecutor implements Executor for any Signer (EOA or Safe). It dispatches
// each action to the right free-function helper.
//
// The executor itself is signer-agnostic: bridge/swap helpers take the Signer
// interface, so EOAExecutor and SafeExecutor are now the same type with a
// different signer wired underneath.
//
// Backwards-compat aliases below preserve the old EOAExecutor and SafeExecutor
// names that callers may already import.
type SignerExecutor struct {
	signer Signer
	cfg    *Config
}

// NewSignerExecutor returns an executor that dispatches actions through the
// provided Signer. signer may be an EOASigner (test) or a SafeSigner (prod).
func NewSignerExecutor(signer Signer, cfg *Config) *SignerExecutor {
	return &SignerExecutor{signer: signer, cfg: cfg}
}

// NewEOAExecutor preserves the old name for the EOA test path.
func NewEOAExecutor(signer Signer, cfg *Config) *SignerExecutor {
	return NewSignerExecutor(signer, cfg)
}

// NewSafeExecutor returns the same Executor wired with a Safe-mode Signer.
func NewSafeExecutor(signer Signer, cfg *Config) *SignerExecutor {
	return NewSignerExecutor(signer, cfg)
}

// Execute dispatches an Action to the right helper.
func (e *SignerExecutor) Execute(ctx context.Context, a Action) (string, error) {
	switch a.Kind {
	case ActionStargateUSDC:
		return stargateUSDCBridge(ctx, e.signer, a.SrcChainID, a.DstChainID, a.Amount, e.cfg.SlippagePct, e.cfg.StargateGasETH)

	case ActionOFTZEN:
		return oftZENBridge(ctx, e.signer, a.SrcChainID, a.DstChainID, a.Amount, e.cfg.SlippagePct, e.cfg.OFTGasETH)

	case ActionAerodromeSwap:
		if a.SrcChainID != ChainIDBase {
			return "", fmt.Errorf("aerodrome swaps only supported on Base, got %s", a.SrcChainID)
		}
		// Pick the right router based on the pair: V2 volatile pool for
		// USDC↔WETH (deeper / cheaper), Slipstream for anything involving ZEN.
		switch {
		case (a.SrcToken == TokenUSDC && a.DstToken == TokenWETH) ||
			(a.SrcToken == TokenWETH && a.DstToken == TokenUSDC):
			return aerodromeSwapUSDCWETH(ctx, e.signer, a.SrcToken, a.DstToken, a.Amount, e.cfg.SlippagePct)
		case (a.SrcToken == TokenUSDC && a.DstToken == TokenZEN) ||
			(a.SrcToken == TokenZEN && a.DstToken == TokenUSDC),
			(a.SrcToken == TokenZEN && a.DstToken == TokenWETH) ||
				(a.SrcToken == TokenWETH && a.DstToken == TokenZEN):
			return aerodromeSlipstreamSwap(ctx, e.signer, a.SrcToken, a.DstToken, a.Amount, e.cfg.SlippagePct)
		default:
			return "", fmt.Errorf("aerodrome swap %s→%s not supported", a.SrcToken, a.DstToken)
		}

	default:
		return "", fmt.Errorf("unhandled action kind %q", a.Kind)
	}
}
