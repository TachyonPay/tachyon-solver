package contracts

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// MockERC20 is a simple interface for interacting with ERC20 tokens
type MockERC20 interface {
	Approve(opts *bind.TransactOpts, spender common.Address, amount *big.Int) (*types.Transaction, error)
	BalanceOf(opts *bind.CallOpts, account common.Address) (*big.Int, error)
	Allowance(opts *bind.CallOpts, owner common.Address, spender common.Address) (*big.Int, error)
	Transfer(opts *bind.TransactOpts, recipient common.Address, amount *big.Int) (*types.Transaction, error)
}

// MockERC20ABI is the ABI for MockERC20 contract
const MockERC20ABI = `[
	{
		"inputs": [
			{"internalType": "string", "name": "name", "type": "string"},
			{"internalType": "string", "name": "symbol", "type": "string"},
			{"internalType": "uint256", "name": "initialSupply", "type": "uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "constructor"
	},
	{
		"inputs": [
			{"internalType": "address", "name": "spender", "type": "address"},
			{"internalType": "uint256", "name": "amount", "type": "uint256"}
		],
		"name": "approve",
		"outputs": [{"internalType": "bool", "name": "", "type": "bool"}],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"inputs": [{"internalType": "address", "name": "account", "type": "address"}],
		"name": "balanceOf",
		"outputs": [{"internalType": "uint256", "name": "", "type": "uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType": "address", "name": "owner", "type": "address"},
			{"internalType": "address", "name": "spender", "type": "address"}
		],
		"name": "allowance",
		"outputs": [{"internalType": "uint256", "name": "", "type": "uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType": "address", "name": "recipient", "type": "address"},
			{"internalType": "uint256", "name": "amount", "type": "uint256"}
		],
		"name": "transfer",
		"outputs": [{"internalType": "bool", "name": "", "type": "bool"}],
		"stateMutability": "nonpayable",
		"type": "function"
	}
]`

// MockERC20Bytecode is the bytecode for deploying MockERC20
const MockERC20Bytecode = "0x60806040523480156200001157600080fd5b506040516200131238038062001312833981810160405281019062000037919062000293565b82600390816200004891906200053f565b5081600490816200005a91906200053f565b508060058190555080600080620000766200011660201b60201c565b73ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff168152602001908152602001600020819055505050506200062f565b60003373ffffffffffffffffffffffffffffffffffffffff16905090565b6000604051905090565b600080fd5b600080fd5b600080fd5b600080fd5b6000601f19601f8301169050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b6200013782620000ec565b810181811067ffffffffffffffff82111715620001595762000158620000fd565b5b80604052505050565b60006200016e620000d3565b90506200017c82826200012c565b919050565b600067ffffffffffffffff8211156200019f576200019e620000fd565b5b620001aa82620000ec565b9050602081019050919050565b60005b83811015620001d7578082015181840152602081019050620001ba565b60008484015250505050565b6000620001fa620001f48462000181565b62000162565b905082815260208101848484011115620002195762000218620000e7565b5b62000226848285620001b7565b509392505050565b600082601f830112620002465762000245620000e2565b5b815162000258848260208601620001e3565b91505092915050565b6000819050919050565b620002768162000261565b81146200028257600080fd5b50565b60008151905062000296816200026b565b92915050565b600080600060608486031215620002b857620002b7620000dd565b5b600084015167ffffffffffffffff811115620002d957620002d8620000e2565b5b620002e7868287016200022e565b935050602084015167ffffffffffffffff8111156200030b576200030a620000e2565b5b62000319868287016200022e565b92505060406200032c8682870162000285565b9150509250925092565b600081519050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052602260045260246000fd5b600060028204905060018216806200038857607f821691505b6020821081036200039e576200039d62000341565b5b50919050565b60008190508160005260206000209050919050565b60006020601f8301049050919050565b600082821b905092915050565b600060088302620004087fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff82620003c9565b620004148683620003c9565b95508019841693508086168417925050509392505050565b6000819050919050565b60006200045762000451620004<truncated>
