package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	rpcURL := "https://horizen-rpc-testnet.appchain.base.org"
	tokenAddr := common.HexToAddress("0x6d5be3853E3b277f9Cd2de7B911e4318eE78cfCb")
	walletAddr := common.HexToAddress("0x93dC72De59c169AA07b23F8D487021e15C57776E")

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatal(err)
	}

	// Get ETH balance
	ethBalance, err := client.BalanceAt(context.Background(), walletAddr, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Get TokenA balance
	tokenABI := `[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(tokenABI))
	tokenContract := bind.NewBoundContract(tokenAddr, parsedABI, client, client, client)

	var balance []interface{}
	err = tokenContract.Call(&bind.CallOpts{}, &balance, "balanceOf", walletAddr)
	if err != nil {
		log.Fatal(err)
	}

	tokenBalance := balance[0].(*big.Int)

	// Calculate amounts in readable format
	ethFloat := new(big.Float).SetInt(ethBalance)
	ethFloat = ethFloat.Quo(ethFloat, big.NewFloat(1e18))

	tokenFloat := new(big.Float).SetInt(tokenBalance)
	tokenFloat = tokenFloat.Quo(tokenFloat, big.NewFloat(1e18))

	fmt.Printf("Wallet: %s\n", walletAddr.Hex())
	fmt.Printf("ETH Balance: %s ETH (%s wei)\n", ethFloat.Text('f', 6), ethBalance.String())
	fmt.Printf("TokenA Balance: %s TokenA (%s wei)\n", tokenFloat.Text('f', 6), tokenBalance.String())
	fmt.Printf("\nRequired for bid: 98 TokenA (98000000000000000000 wei)\n")
	fmt.Printf("Shortfall: %s TokenA\n", new(big.Float).Sub(big.NewFloat(98), tokenFloat).Text('f', 6))
}
