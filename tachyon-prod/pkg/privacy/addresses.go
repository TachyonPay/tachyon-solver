// Package privacy implements the partial-privacy bridging service.
//
// Inbound user funds land at a per-chain TEE-controlled hot Safe; the
// service batches by (src,dst,srcTok,dstTok), applies random delay, and
// dispatches a LI.FI bridge to user-specified destination stealth Safes.
// LI.FI auto-routes through whichever bridge has a live path (Across,
// Relay, Stargate, native gateway, etc.) — we just sign the returned
// transactionRequest as a Safe execTransaction and let the relayer pay
// the gas.
package privacy

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// Supported chain IDs (string form so they JSON-serialize as numbers
// without quotes via the Token map keying).
const (
	ChainIDMegaETH  = "4326"
	ChainIDHorizen  = "26514"
	ChainIDBase     = "8453"
	ChainIDEthereum = "1"
	ChainIDArbitrum = "42161"
	ChainIDOptimism = "10"
)

// LayerZero v2 endpoint IDs for chains we route through Stargate.
// Used in SendParam.dstEid when calling Stargate Pool/OFT.send().
const (
	LzEidMegaETH  uint32 = 30398
	LzEidHorizen  uint32 = 30399
	LzEidBase     uint32 = 30184
	LzEidEthereum uint32 = 30101
	LzEidArbitrum uint32 = 30110
	LzEidOptimism uint32 = 30111
)

// LiFi short-codes mirror what /v1/quote expects in the `fromChain` /
// `toChain` query parameters when callers want non-numeric input. We
// always pass numeric chainIds in our API calls so this mapping is
// kept just for clarity in logs and config.
var LiFiChainKey = map[string]string{
	ChainIDMegaETH:  "MEG",
	ChainIDBase:     "BAS",
	ChainIDEthereum: "ETH",
	ChainIDArbitrum: "ARB",
	ChainIDOptimism: "OPT",
	// Horizen isn't routed through LI.FI — it uses Stargate directly.
}

// Token symbols. USDT0 is MegaETH's de-facto cross-chain stable; USDC
// is canonical on Base/Ethereum/Arbitrum/Optimism. ETH is the native
// gas token on every supported chain — Across covers MegaETH ↔ all
// of Base/Ethereum/Arbitrum/Optimism for native ETH bidirectionally,
// so ETH-rail bridging works symmetrically while stables-rail has
// outbound-from-MegaETH limitations (only Optimism today).
type Token string

const (
	TokenUSDT0 Token = "USDT0"
	TokenUSDC  Token = "USDC"
	TokenUSDCe Token = "USDC.e" // bridged USDC variant — Horizen uses this
	TokenETH   Token = "ETH"
	TokenZEN   Token = "ZEN" // Horizen's native, also lives as ERC-20 on Base via OFT
)

// LifiNativeSentinel is the address LI.FI (and most aggregators) use
// to mean "native gas token" — same sentinel on every chain, no token
// contract involved.
var LifiNativeSentinel = common.HexToAddress("0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE")

// Token ERC20 addresses + Stargate v2 OFT/Pool entry points per chain.
//
// LI.FI routes (MegaETH-touching): the privacy-router only needs the
// underlying ERC20 address — LI.FI's /v1/quote returns the call data.
//
// Stargate routes (Horizen-touching): the privacy-router calls
// `send()` directly on the per-chain OFT/Pool — these addresses are the
// SgOFT (Horizen) or SgUSDCPool (other chains) entry points.
var (
	MegaETHUSDT0 = common.HexToAddress("0xB8CE59FC3717ada4C02eaDF9682A9e934F625ebb")
	MegaETHWETH  = common.HexToAddress("0x4200000000000000000000000000000000000006")

	BaseUSDC     = common.HexToAddress("0x833589fcd6edb6e08f4c7c32d4f71b54bda02913")
	EthereumUSDC = common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	ArbitrumUSDC = common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831")
	OptimismUSDC = common.HexToAddress("0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85")

	// Horizen mainnet — bridged USDC variant + Stargate OFT entry point.
	HorizenUSDCe      = common.HexToAddress("0xDF7108f8B10F9b9eC1aba01CCa057268cbf86B6c")
	HorizenStargateOFT = common.HexToAddress("0x3a1293Bdb83bBbDd5Ebf4fAc96605aD2021BbC0f")

	// ZEN (Horizen's native asset) lives as an ERC-20 on Base via the
	// LayerZero OFT pattern. Bridge is Base ↔ Horizen only — no other
	// chain has a ZEN OFT deployed. Mirrors the rebalancer's addresses
	// (BaseZEN / BaseZENOFTAdapter / HzZENOFT).
	BaseZEN          = common.HexToAddress("0xf43eB8De897Fbc7F2502483B2Bef7Bb9EA179229")
	BaseZENOFTAdapter = common.HexToAddress("0x57da2D504bf8b83Ef304759d9f2648522D7a9280")
	HorizenZENOFT    = common.HexToAddress("0x57da2D504bf8b83Ef304759d9f2648522D7a9280")

	// Stargate v2 USDC Pool addresses on the ethereum-family chains.
	// All call `send(SendParam, MessagingFee, refund)` per the OFT v2 ABI.
	BaseStargatePoolUSDC     = common.HexToAddress("0x27a16dc786820B16E5c9028b75B99F6f604b5d26")
	EthereumStargatePoolUSDC = common.HexToAddress("0xc026395860Db2d07ee33e05fE50ed7bD583189C7")
	ArbitrumStargatePoolUSDC = common.HexToAddress("0xe8CDF27AcD73a434D661C84887215F7598e7d0d3")
	OptimismStargatePoolUSDC = common.HexToAddress("0xcE8CcA271Ebc0533920C83d39F417ED6A0abB7D0")

	// Safe v1.3.0 canonical (deployed on all 6 chains we support; used
	// by frontend to predict CREATE2 dest stealth Safe addresses).
	SafeProxyFactory    = common.HexToAddress("0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2")
	SafeSingleton       = common.HexToAddress("0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552")
	SafeFallbackHandler = common.HexToAddress("0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4")
)

// TokenAddress returns the on-chain address for (chainID, token).
// Returns LifiNativeSentinel for ETH (LI.FI's "native" placeholder) and
// the ERC20 address for stables. Returns common.Address{} if unsupported.
func TokenAddress(chainID string, t Token) common.Address {
	if t == TokenETH {
		switch chainID {
		case ChainIDMegaETH, ChainIDBase, ChainIDEthereum, ChainIDArbitrum, ChainIDOptimism:
			return LifiNativeSentinel
		}
		return common.Address{}
	}
	switch chainID {
	case ChainIDMegaETH:
		if t == TokenUSDT0 {
			return MegaETHUSDT0
		}
	case ChainIDHorizen:
		switch t {
		case TokenUSDCe:
			return HorizenUSDCe
		case TokenZEN:
			return HorizenZENOFT
		}
	case ChainIDBase:
		switch t {
		case TokenUSDC:
			return BaseUSDC
		case TokenZEN:
			return BaseZEN
		}
	case ChainIDEthereum:
		if t == TokenUSDC {
			return EthereumUSDC
		}
	case ChainIDArbitrum:
		if t == TokenUSDC {
			return ArbitrumUSDC
		}
	case ChainIDOptimism:
		if t == TokenUSDC {
			return OptimismUSDC
		}
	}
	return common.Address{}
}

// StargateOFT returns the contract to call `send()` on for (chainID, token).
// Used for Horizen-touching routes; LI.FI handles MegaETH-touching routes.
// Returns common.Address{} if no Stargate OFT is configured.
func StargateOFT(chainID string, t Token) common.Address {
	switch chainID {
	case ChainIDHorizen:
		switch t {
		case TokenUSDCe:
			return HorizenStargateOFT
		case TokenZEN:
			return HorizenZENOFT
		}
	case ChainIDBase:
		switch t {
		case TokenUSDC:
			return BaseStargatePoolUSDC
		case TokenZEN:
			return BaseZENOFTAdapter
		}
	case ChainIDEthereum:
		if t == TokenUSDC {
			return EthereumStargatePoolUSDC
		}
	case ChainIDArbitrum:
		if t == TokenUSDC {
			return ArbitrumStargatePoolUSDC
		}
	case ChainIDOptimism:
		if t == TokenUSDC {
			return OptimismStargatePoolUSDC
		}
	}
	return common.Address{}
}

// LzEid returns the LayerZero v2 endpoint ID for chainID. Used as
// SendParam.dstEid when bridging via Stargate.
func LzEid(chainID string) uint32 {
	switch chainID {
	case ChainIDMegaETH:
		return LzEidMegaETH
	case ChainIDHorizen:
		return LzEidHorizen
	case ChainIDBase:
		return LzEidBase
	case ChainIDEthereum:
		return LzEidEthereum
	case ChainIDArbitrum:
		return LzEidArbitrum
	case ChainIDOptimism:
		return LzEidOptimism
	}
	return 0
}

// IsStargateRoute reports whether (src, srcTok, dst, dstTok) can be
// served via LayerZero OFT directly (Stargate's USDC bus or the ZEN
// OFT adapter). Requires Horizen on at least one side, and both chains
// to have a matching OFT deployment.
//
// Two parallel buses today:
//   - USDC bus: Horizen USDC.e ↔ {Base, Eth, Arb, Opt} USDC.
//   - ZEN OFT: Base ↔ Horizen only — ZEN doesn't have OFT deployments
//     on Eth/Arb/Opt, so those legs are rejected here.
func IsStargateRoute(src, dst string, srcTok, dstTok Token) bool {
	if src != ChainIDHorizen && dst != ChainIDHorizen {
		return false
	}
	// ZEN: only Base ↔ Horizen, and both sides must be ZEN (the OFT
	// adapter does not swap, just transports).
	if srcTok == TokenZEN || dstTok == TokenZEN {
		if srcTok != TokenZEN || dstTok != TokenZEN {
			return false
		}
		return (src == ChainIDBase && dst == ChainIDHorizen) ||
			(src == ChainIDHorizen && dst == ChainIDBase)
	}
	// USDC bus. Horizen side must be USDC.e; non-Horizen side must be USDC.
	if src == ChainIDHorizen && srcTok != TokenUSDCe {
		return false
	}
	if dst == ChainIDHorizen && dstTok != TokenUSDCe {
		return false
	}
	if src != ChainIDHorizen && srcTok != TokenUSDC {
		return false
	}
	if dst != ChainIDHorizen && dstTok != TokenUSDC {
		return false
	}
	return StargateOFT(src, srcTok) != (common.Address{}) &&
		StargateOFT(dst, dstTok) != (common.Address{})
}

// IsLifiRoute reports whether the route should be dispatched via LI.FI.
// Anything that's NOT a Stargate route falls through to LI.FI; the
// MegaETH ↔ {Base, Eth, Arb, Opt} matrix is verified live, other tuples
// will simply fail at quote time and the intent transitions to failed.
func IsLifiRoute(src, dst string, srcTok, dstTok Token) bool {
	if IsStargateRoute(src, dst, srcTok, dstTok) {
		return false
	}
	if src != ChainIDMegaETH && dst != ChainIDMegaETH {
		return false
	}
	// ETH-source intents can't be detected by the deposit watcher
	// (no ERC-20 Transfer log on native sends). ETH as the *destination*
	// is fine — the user deposits a stable, the bridge delivers ETH on
	// the other chain. Lift the source guard once watchDeposits gains a
	// balance-diff path for native ETH.
	if srcTok == TokenETH {
		return false
	}
	return TokenAddress(src, srcTok) != (common.Address{}) &&
		TokenAddress(dst, dstTok) != (common.Address{})
}

// IsNativeToken returns true iff t is ETH (or any other native gas
// token we add later). Used by the bridge code to decide whether to
// approve an ERC20 before calling LI.FI's transactionRequest target.
func IsNativeToken(t Token) bool { return t == TokenETH }

// NormalizeToken maps any case-variation of a known token symbol to
// the canonical Token constant. The catalog publishes uppercase
// (USDC.E), the const is mixed-case (USDC.e), and clients will send
// whichever they copied — without normalization the strict equality
// check in IsStargateRoute / IsLifiRoute rejects everything but the
// const's exact spelling. Unknown tokens pass through unchanged so
// the route check rejects them on missing token address.
func NormalizeToken(s string) Token {
	switch strings.ToUpper(s) {
	case "USDT0":
		return TokenUSDT0
	case "USDC":
		return TokenUSDC
	case "USDC.E":
		return TokenUSDCe
	case "ETH":
		return TokenETH
	case "ZEN":
		return TokenZEN
	}
	return Token(s)
}

// TokenDecimals returns the canonical decimals for token.
func TokenDecimals(t Token) uint8 {
	switch t {
	case TokenUSDT0, TokenUSDC, TokenUSDCe:
		return 6
	case TokenETH, TokenZEN:
		return 18
	}
	return 18
}

// IsSupportedRoute reports whether (srcChain, srcTok, dstChain, dstTok) is
// structurally permitted. Light gating only:
//
//   - same-chain transfers rejected
//   - non-MegaETH-only legs rejected (the solver+rebalancer already covers
//     Base↔Horizen and we don't want this service touching Base↔Ethereum)
//   - both tokens must be known on their chains
//
// We deliberately do NOT hardcode which (src,dst,srcTok,dstTok) tuples
// have a live LI.FI bridge — that matrix shifts as bridges add/remove
// destinations. Instead the frontend pre-checks viability with LI.FI's
// /quote before submitting, and the bridger fails an intent gracefully
// (StatusFailed) if no quote is available at execution time.
//
// As of 2026-05-02 the verified matrix is:
//
//	INBOUND  (X → MegaETH):
//	  USDC → USDT0:   ✅ Base, Ethereum, Arbitrum, Optimism (Across)
//	  USDC → ETH:     ✅ Base, Ethereum, Arbitrum, Optimism (Across)
//
//	OUTBOUND (MegaETH → X):
//	  USDT0 → USDC:   ✅ Base, Optimism (Across); ❌ Eth/Arb
//	  USDT0 → ETH:    ✅ Base, Arbitrum, Optimism (Across); ❌ Eth
//
// ETH-source routes are intentionally NOT published: the deposit
// watcher (processor.watchDeposits) listens for ERC-20 Transfer logs
// only, so native ETH sends to the TEE Safe wouldn't be detected.
// ETH-dest is fine — bridge delivers ETH on the dest chain and the
// recipient's safe receives it normally.
//
// Stargate ZEN OFT (Base ↔ Horizen) lives on top of the existing
// rebalancer ZEN OFT adapter; LI.FI doesn't list ZEN. Frontend should
// mirror this matrix when populating dest dropdowns.
func IsSupportedRoute(src, dst string, srcTok, dstTok Token) bool {
	if src == dst {
		return false
	}
	return IsLifiRoute(src, dst, srcTok, dstTok) || IsStargateRoute(src, dst, srcTok, dstTok)
}

// SolverType is the registration label this service publishes when
// it calls /register-solver. The relayer indexes registrations by
// type so the frontend can discover routes per-bridge.
type SolverType string

const (
	SolverTypeTachyon       SolverType = "tachyon"       // existing solver (Base ↔ Horizen, intent-auction)
	SolverTypeLifiXTach     SolverType = "lifiXtach"     // privacy router, LI.FI-routed legs
	SolverTypeStargateXTach SolverType = "stargateXtach" // privacy router, Stargate-routed legs
)

// LifiSupportedRoutes lists every (src, srcTok, dst, dstTok) tuple the
// privacy router publishes under SolverTypeLifiXTach. Source token is
// always a stable (deposit detection requires an ERC-20 Transfer log);
// destination can be a stable or ETH.
//
// Verified live against li.quest /v1/quote on 2026-05-02.
func LifiSupportedRoutes() []RouteSpec {
	out := []RouteSpec{}
	// Inbound: every other chain's USDC bridges into MegaETH USDT0 or ETH.
	for _, ch := range []string{ChainIDBase, ChainIDEthereum, ChainIDArbitrum, ChainIDOptimism} {
		out = append(out, RouteSpec{ch, TokenUSDC, ChainIDMegaETH, TokenUSDT0})
		out = append(out, RouteSpec{ch, TokenUSDC, ChainIDMegaETH, TokenETH})
	}
	// Outbound USDT0 → USDC: Base, Optimism only. Eth/Arb don't quote yet.
	for _, ch := range []string{ChainIDBase, ChainIDOptimism} {
		out = append(out, RouteSpec{ChainIDMegaETH, TokenUSDT0, ch, TokenUSDC})
	}
	// Outbound USDT0 → ETH: Base, Arbitrum, Optimism. Eth doesn't quote yet.
	for _, ch := range []string{ChainIDBase, ChainIDArbitrum, ChainIDOptimism} {
		out = append(out, RouteSpec{ChainIDMegaETH, TokenUSDT0, ch, TokenETH})
	}
	return out
}

// StargateSupportedRoutes lists every (src, srcTok, dst, dstTok) tuple
// the privacy router publishes under SolverTypeStargateXTach.
//
//   - USDC bus: Horizen USDC.e ↔ {Base, Eth, Arb, Opt} USDC (bidirectional).
//   - ZEN OFT:  Base ↔ Horizen ZEN (bidirectional). ZEN doesn't have an
//     OFT deployment on Eth/Arb/Opt.
func StargateSupportedRoutes() []RouteSpec {
	out := []RouteSpec{}
	for _, ch := range []string{ChainIDBase, ChainIDEthereum, ChainIDArbitrum, ChainIDOptimism} {
		out = append(out, RouteSpec{ChainIDHorizen, TokenUSDCe, ch, TokenUSDC})
		out = append(out, RouteSpec{ch, TokenUSDC, ChainIDHorizen, TokenUSDCe})
	}
	out = append(out, RouteSpec{ChainIDBase, TokenZEN, ChainIDHorizen, TokenZEN})
	out = append(out, RouteSpec{ChainIDHorizen, TokenZEN, ChainIDBase, TokenZEN})
	return out
}

// RouteSpec is the canonical (src, srcTok, dst, dstTok) tuple used
// for registration payloads and the /routes discovery endpoint.
type RouteSpec struct {
	SourceChainID string `json:"sourceChainId"`
	SourceToken   Token  `json:"sourceToken"`
	DestChainID   string `json:"destChainId"`
	DestToken     Token  `json:"destToken"`
}
