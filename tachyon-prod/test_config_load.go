package main

import (
	"fmt"
	"github.com/zk-tachyon/tachyon-prod/pkg/config"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	fmt.Printf("Loaded %d chain pairs:\n", len(cfg.ChainPairs))
	for i, pair := range cfg.ChainPairs {
		fmt.Printf("  Pair %d: %s (Chain ID: %s, Bridge: %s) -> %s (Chain ID: %s, Bridge: %s)\n",
			i+1,
			pair.SourceChain.Name,
			pair.SourceChain.ChainID.String(),
			pair.SourceChain.BridgeAddress,
			pair.DestChain.Name,
			pair.DestChain.ChainID.String(),
			pair.DestChain.BridgeAddress,
		)
	}

	fmt.Printf("\nNetworks map has %d entries:\n", len(cfg.Networks))
	for name, net := range cfg.Networks {
		fmt.Printf("  %s: Chain ID %s, Bridge %s, RPC %s\n",
			name,
			net.ChainID.String(),
			net.BridgeAddress,
			net.RpcURL,
		)
	}
}
