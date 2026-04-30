package rebalancer

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// erc20ABI exposes only the methods we need: balanceOf, allowance, approve, decimals.
const erc20ABIJSON = `[
  {"name":"balanceOf","type":"function","stateMutability":"view","inputs":[{"name":"account","type":"address"}],"outputs":[{"type":"uint256"}]},
  {"name":"allowance","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"type":"uint256"}]},
  {"name":"approve","type":"function","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"type":"bool"}]},
  {"name":"decimals","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]}
]`

// stargateOFTABI covers the subset of LayerZero v2 OFT / Stargate Pool methods
// we use. Both Stargate V2 USDC pools and the ZEN OFT speak this surface.
const stargateOFTABIJSON = `[
  {
    "name":"send","type":"function","stateMutability":"payable",
    "inputs":[
      {"name":"_sendParam","type":"tuple","components":[
        {"name":"dstEid","type":"uint32"},
        {"name":"to","type":"bytes32"},
        {"name":"amountLD","type":"uint256"},
        {"name":"minAmountLD","type":"uint256"},
        {"name":"extraOptions","type":"bytes"},
        {"name":"composeMsg","type":"bytes"},
        {"name":"oftCmd","type":"bytes"}
      ]},
      {"name":"_fee","type":"tuple","components":[
        {"name":"nativeFee","type":"uint256"},
        {"name":"lzTokenFee","type":"uint256"}
      ]},
      {"name":"_refundAddress","type":"address"}
    ],
    "outputs":[
      {"type":"tuple","components":[
        {"name":"guid","type":"bytes32"},
        {"name":"nonce","type":"uint64"},
        {"name":"fee","type":"tuple","components":[
          {"name":"nativeFee","type":"uint256"},
          {"name":"lzTokenFee","type":"uint256"}
        ]}
      ]},
      {"type":"tuple","components":[
        {"name":"amountSentLD","type":"uint256"},
        {"name":"amountReceivedLD","type":"uint256"}
      ]}
    ]
  },
  {
    "name":"quoteSend","type":"function","stateMutability":"view",
    "inputs":[
      {"name":"_sendParam","type":"tuple","components":[
        {"name":"dstEid","type":"uint32"},
        {"name":"to","type":"bytes32"},
        {"name":"amountLD","type":"uint256"},
        {"name":"minAmountLD","type":"uint256"},
        {"name":"extraOptions","type":"bytes"},
        {"name":"composeMsg","type":"bytes"},
        {"name":"oftCmd","type":"bytes"}
      ]},
      {"name":"_payInLzToken","type":"bool"}
    ],
    "outputs":[
      {"type":"tuple","components":[
        {"name":"nativeFee","type":"uint256"},
        {"name":"lzTokenFee","type":"uint256"}
      ]}
    ]
  },
  {"name":"approvalRequired","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"bool"}]},
  {"name":"token","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]}
]`

// aerodromeRouterABI is the V2 (Solidly-fork) router. We only use single-hop swaps.
const aerodromeRouterABIJSON = `[
  {
    "name":"swapExactTokensForTokens","type":"function","stateMutability":"nonpayable",
    "inputs":[
      {"name":"amountIn","type":"uint256"},
      {"name":"amountOutMin","type":"uint256"},
      {"name":"routes","type":"tuple[]","components":[
        {"name":"from","type":"address"},
        {"name":"to","type":"address"},
        {"name":"stable","type":"bool"},
        {"name":"factory","type":"address"}
      ]},
      {"name":"to","type":"address"},
      {"name":"deadline","type":"uint256"}
    ],
    "outputs":[{"type":"uint256[]"}]
  },
  {
    "name":"getAmountsOut","type":"function","stateMutability":"view",
    "inputs":[
      {"name":"amountIn","type":"uint256"},
      {"name":"routes","type":"tuple[]","components":[
        {"name":"from","type":"address"},
        {"name":"to","type":"address"},
        {"name":"stable","type":"bool"},
        {"name":"factory","type":"address"}
      ]}
    ],
    "outputs":[{"type":"uint256[]"}]
  }
]`

// slipstreamRouterABI: Aerodrome's Concentrated-Liquidity SwapRouter.
// Multi-hop via exactInput uses a packed bytes path: tokenIn|ts(3)|...|tokenOut.
const slipstreamRouterABIJSON = `[
  {
    "name":"exactInput","type":"function","stateMutability":"payable",
    "inputs":[{"name":"params","type":"tuple","components":[
      {"name":"path","type":"bytes"},
      {"name":"recipient","type":"address"},
      {"name":"deadline","type":"uint256"},
      {"name":"amountIn","type":"uint256"},
      {"name":"amountOutMinimum","type":"uint256"}
    ]}],
    "outputs":[{"name":"amountOut","type":"uint256"}]
  }
]`

// slipstreamQuoterABI: Aerodrome's QuoterV2 (Uniswap-style; non-view but
// callable via eth_call since it reverts internally to deliver the answer).
//
// quoteExactOutput's `path` is reversed: tokenOut | ts | ... | tokenIn.
const slipstreamQuoterABIJSON = `[
  {
    "name":"quoteExactInput","type":"function","stateMutability":"nonpayable",
    "inputs":[
      {"name":"path","type":"bytes"},
      {"name":"amountIn","type":"uint256"}
    ],
    "outputs":[
      {"name":"amountOut","type":"uint256"},
      {"name":"sqrtPriceX96AfterList","type":"uint160[]"},
      {"name":"initializedTicksCrossedList","type":"uint32[]"},
      {"name":"gasEstimate","type":"uint256"}
    ]
  },
  {
    "name":"quoteExactOutput","type":"function","stateMutability":"nonpayable",
    "inputs":[
      {"name":"path","type":"bytes"},
      {"name":"amountOut","type":"uint256"}
    ],
    "outputs":[
      {"name":"amountIn","type":"uint256"},
      {"name":"sqrtPriceX96AfterList","type":"uint160[]"},
      {"name":"initializedTicksCrossedList","type":"uint32[]"},
      {"name":"gasEstimate","type":"uint256"}
    ]
  }
]`

var (
	erc20ABI           abi.ABI
	stargateOFTABI     abi.ABI
	aerodromeRouterABI abi.ABI
	slipstreamRouterABI abi.ABI
	slipstreamQuoterABI abi.ABI
)

func init() {
	var err error
	erc20ABI, err = abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		panic("erc20 ABI: " + err.Error())
	}
	stargateOFTABI, err = abi.JSON(strings.NewReader(stargateOFTABIJSON))
	if err != nil {
		panic("stargate OFT ABI: " + err.Error())
	}
	aerodromeRouterABI, err = abi.JSON(strings.NewReader(aerodromeRouterABIJSON))
	if err != nil {
		panic("aerodrome router ABI: " + err.Error())
	}
	slipstreamRouterABI, err = abi.JSON(strings.NewReader(slipstreamRouterABIJSON))
	if err != nil {
		panic("slipstream router ABI: " + err.Error())
	}
	slipstreamQuoterABI, err = abi.JSON(strings.NewReader(slipstreamQuoterABIJSON))
	if err != nil {
		panic("slipstream quoter ABI: " + err.Error())
	}
}

// callMsgFromTx is a tiny helper for read-only / estimate calls.
func callMsgFromTx(from, to common.Address, value *big.Int, data []byte) ethereum.CallMsg {
	return ethereum.CallMsg{From: from, To: &to, Value: value, Data: data}
}

// erc20BalanceOf reads holder's balance of tokenAddr on chainID.
func erc20BalanceOf(ctx context.Context, s Signer, chainID string, tokenAddr, holder common.Address) (*big.Int, error) {
	data, err := erc20ABI.Pack("balanceOf", holder)
	if err != nil {
		return nil, err
	}
	out, err := s.CallReadOnly(ctx, chainID, tokenAddr, data)
	if err != nil {
		return nil, err
	}
	res, err := erc20ABI.Unpack("balanceOf", out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty balanceOf result")
	}
	v, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected balanceOf type %T", res[0])
	}
	return v, nil
}

// erc20Allowance reads owner's allowance for spender on tokenAddr.
func erc20Allowance(ctx context.Context, s Signer, chainID string, tokenAddr, owner, spender common.Address) (*big.Int, error) {
	data, err := erc20ABI.Pack("allowance", owner, spender)
	if err != nil {
		return nil, err
	}
	out, err := s.CallReadOnly(ctx, chainID, tokenAddr, data)
	if err != nil {
		return nil, err
	}
	res, err := erc20ABI.Unpack("allowance", out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("empty allowance result")
	}
	v, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected allowance type %T", res[0])
	}
	return v, nil
}

// erc20Approve sends approve(spender, amount) and waits for inclusion. The
// approval is granted by Signer.Address() (EOA or Safe), not the gas-paying
// EOA — so for a SafeSigner this lets the Safe approve other contracts to
// spend its tokens.
func erc20Approve(ctx context.Context, s Signer, chainID string, tokenAddr, spender common.Address, amount *big.Int) (string, error) {
	data, err := erc20ABI.Pack("approve", spender, amount)
	if err != nil {
		return "", err
	}
	tx, _, err := s.SendAndWait(ctx, chainID, tokenAddr, big.NewInt(0), data, 100_000, 0)
	if err != nil {
		return "", err
	}
	return tx.Hash().Hex(), nil
}

// ensureAllowance approves spender to spend at least amount of tokenAddr from
// the signer's address. If the existing allowance is already sufficient it
// is a no-op.
func ensureAllowance(ctx context.Context, s Signer, chainID string, tokenAddr, spender common.Address, amount *big.Int) (string, error) {
	current, err := erc20Allowance(ctx, s, chainID, tokenAddr, s.Address(), spender)
	if err != nil {
		return "", fmt.Errorf("read allowance: %w", err)
	}
	if current.Cmp(amount) >= 0 {
		return "", nil
	}
	// Approve max to avoid repeated approvals on reuse.
	maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	return erc20Approve(ctx, s, chainID, tokenAddr, spender, maxUint)
}
