package main

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
)

func main() {
	check := func(label string, stealthHex string, chainID uint64, actualDeployed string) {
		stealthAddr := common.HexToAddress(stealthHex)
		predicted, err := blockchain.PredictSafeAddressForOwnerOnChain(stealthAddr, chainID)
		if err != nil {
			fmt.Printf("❌ %s: error: %v\n", label, err)
			return
		}
		match := strings.EqualFold(predicted.Hex(), actualDeployed)
		if match {
			fmt.Printf("✅ %s: %s\n", label, predicted.Hex())
		} else {
			fmt.Printf("❌ %s:\n   predicted: %s\n   actual:    %s\n", label, predicted.Hex(), actualDeployed)
		}
	}

	check("Base Sepolia  (84532)",    "0x4eccDDa3d3d28021Cb0Dd12C184442045883E654", 84532,     "0x31ecfc01800a5862888f57065bc9d1a0c178902d")
	check("Horizen       (845320009)", "0x7b6786Cb46748BC430f9aa3F8DA29DD71745F7b8", 845320009, "0xbf284c2324d0e05d06f98bbe1fe517dfbde5510f")
}
