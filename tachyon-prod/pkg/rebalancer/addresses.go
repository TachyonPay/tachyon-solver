package rebalancer

import (
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// LayerZero v2 endpoint IDs.
const (
	LzEidBase    uint32 = 30184
	LzEidHorizen uint32 = 30399
)

// Chain IDs (matches the env CHAIN_*_ID values).
const (
	ChainIDBase           = "8453"
	ChainIDHorizen        = "26514"
	ChainIDBaseSepolia    = "84532"
	ChainIDHorizenTestnet = "2651420"
)

// Base mainnet — verified addresses.
var (
	BaseUSDC                = common.HexToAddress("0x833589fcd6edb6e08f4c7c32d4f71b54bda02913")
	BaseWETH                = common.HexToAddress("0x4200000000000000000000000000000000000006")
	BaseZEN                 = common.HexToAddress("0xf43eB8De897Fbc7F2502483B2Bef7Bb9EA179229")
	BaseLZEndpointV2        = common.HexToAddress("0x1a44076050125825900e736c501f859c50fE728c")
	BaseStargatePoolUSDC    = common.HexToAddress("0x27a16dc786820B16E5c9028b75B99F6f604b5d26")
	BaseZENOFTAdapter       = common.HexToAddress("0x57da2D504bf8b83Ef304759d9f2648522D7a9280")
	BaseAerodromeRouter     = common.HexToAddress("0xcF77a3Ba9A5CA399B7c97c74d54e5b1Beb874E43")
	BaseAerodromeFactory    = common.HexToAddress("0x420DD381b31aEf6683db6B902084cB0FFECe40Da")
	BaseAerodromeUniversalR = common.HexToAddress("0x6Cb442acF35158D5eDa88fe602221b67B400Be3E")
	BaseAeroUSDCWETHPool    = common.HexToAddress("0xcDAC0d6c6C59727a65F871236188350531885C43") // V2 volatile, $13M TVL
	BaseAeroZENWETHPool     = common.HexToAddress("0x0392b12a1ceb0cd13af5ea448cf5586ea609852d") // Slipstream tickSpacing 100, $2.25M TVL
	BaseAeroZENWETHTickSpc  = int64(100)

	// Slipstream (concentrated-liquidity) infra.
	BaseSlipstreamRouter      = common.HexToAddress("0xBE6D8f0d05cC4be24d5167a3eF062215bE6D18a5")
	BaseSlipstreamQuoter      = common.HexToAddress("0x254cF9E1E6e233aa1AC962CB9B05b2cfeAaE15b0")
	BaseAeroUSDCWETHCLPool    = common.HexToAddress("0xb2cc224c1c9feE385f8ad6a55b4d94E92359DC59") // Slipstream tickSpacing 100, deepest USDC/WETH
	BaseAeroUSDCWETHCLTickSpc = int64(100)
)

// Horizen mainnet — verified addresses.
var (
	HzUSDCe             = common.HexToAddress("0xDF7108f8B10F9b9eC1aba01CCa057268cbf86B6c")
	HzZENOFT            = common.HexToAddress("0x57da2D504bf8b83Ef304759d9f2648522D7a9280")
	HzLZEndpointV2      = common.HexToAddress("0x6F475642a6e85809B1c36Fa62763669b1b48DD5B")
	HzStargateOFTUSDC   = common.HexToAddress("0x3a1293Bdb83bBbDd5Ebf4fAc96605aD2021BbC0f")
)

// TokenSym is a canonical token identifier used by the rebalancer.
// On Base, USDC is the native USDC. On Horizen, USDC means USDC.e.
type TokenSym string

const (
	TokenUSDC TokenSym = "USDC"
	TokenWETH TokenSym = "WETH"
	TokenZEN  TokenSym = "ZEN"
)

// TokenAddress returns the on-chain ERC20 address for (chainID, sym).
// Returns common.Address{} if unsupported on that chain.
//
// Mainnet chains (Base 8453, Horizen 26514) use the hardcoded canonical
// addresses. Testnet chains (Base Sepolia 84532, Horizen testnet 2651420)
// fall back to the TOKEN_<SYM>_<CHAIN> env vars from .env (e.g.
// TOKEN_USDC_BASE, TOKEN_USDC_HORIZEN).
func TokenAddress(chainID string, sym TokenSym) common.Address {
	switch chainID {
	case ChainIDBase:
		switch sym {
		case TokenUSDC:
			return BaseUSDC
		case TokenWETH:
			return BaseWETH
		case TokenZEN:
			return BaseZEN
		}
	case ChainIDHorizen:
		switch sym {
		case TokenUSDC:
			return HzUSDCe
		case TokenZEN:
			return HzZENOFT
		}
	case ChainIDBaseSepolia:
		if v := envTokenAddr("TOKEN_" + string(sym) + "_BASE"); v != (common.Address{}) {
			return v
		}
	case ChainIDHorizenTestnet:
		if v := envTokenAddr("TOKEN_" + string(sym) + "_HORIZEN"); v != (common.Address{}) {
			return v
		}
	}
	return common.Address{}
}

func envTokenAddr(envVar string) common.Address {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" || !common.IsHexAddress(v) {
		return common.Address{}
	}
	return common.HexToAddress(v)
}

// TokenDecimals returns the canonical decimals for sym, used as a static
// reference for env target parsing. The runtime swap/bridge code still calls
// the contract's decimals() to be safe — this is only for parsing config.
func TokenDecimals(sym TokenSym) uint8 {
	switch sym {
	case TokenUSDC:
		return 6
	case TokenWETH, TokenZEN:
		return 18
	}
	return 18
}
