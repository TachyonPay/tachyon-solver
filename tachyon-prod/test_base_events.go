package main

import (
	"context"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
	"strings"
)

const bridgeIntentABI = `[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"intentId","type":"uint256"},{"indexed":true,"internalType":"address","name":"user","type":"address"},{"indexed":false,"internalType":"address","name":"tokenA","type":"address"},{"indexed":false,"internalType":"address","name":"tokenB","type":"address"},{"indexed":false,"internalType":"uint256","name":"amountA","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"expectedAmountB","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"reward","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"endTime","type":"uint256"}],"name":"IntentCreated","type":"event"}]`

func main() {
	godotenv.Load()

	baseRPC := os.Getenv("PAIR_1_DEST_RPC")
	baseBridge := os.Getenv("PAIR_1_DEST_BRIDGE")

	fmt.Printf("Connecting to Base Sepolia...\n")
	fmt.Printf("  RPC: %s\n", baseRPC)
	fmt.Printf("  Bridge: %s\n", baseBridge)

	client, err := ethclient.Dial(baseRPC)
	if err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return
	}
	defer client.Close()

	currentBlock, err := client.BlockNumber(context.Background())
	if err != nil {
		fmt.Printf("Failed to get block number: %v\n", err)
		return
	}
	fmt.Printf("  Current block: %d\n\n", currentBlock)

	// Parse ABI to get IntentCreated event signature
	contractABI, err := abi.JSON(strings.NewReader(bridgeIntentABI))
	if err != nil {
		fmt.Printf("Failed to parse ABI: %v\n", err)
		return
	}

	intentCreatedEvent := contractABI.Events["IntentCreated"]
	eventSignature := intentCreatedEvent.ID

	fmt.Printf("IntentCreated event signature: %s\n\n", eventSignature.Hex())

	// Query in smaller chunks (10 blocks at a time due to Alchemy free tier limit)
	blocksToScan := 100
	chunkSize := 10

	fmt.Printf("Querying for IntentCreated events in last %d blocks (in chunks of %d)...\n", blocksToScan, chunkSize)
	
	totalEvents := 0
	for i := 0; i < blocksToScan; i += chunkSize {
		fromBlock := big.NewInt(int64(currentBlock) - int64(blocksToScan) + int64(i))
		toBlock := big.NewInt(int64(currentBlock) - int64(blocksToScan) + int64(i) + int64(chunkSize) - 1)
		
		if toBlock.Uint64() > currentBlock {
			toBlock = big.NewInt(int64(currentBlock))
		}

		query := ethereum.FilterQuery{
			FromBlock: fromBlock,
			ToBlock:   toBlock,
			Addresses: []common.Address{common.HexToAddress(baseBridge)},
			Topics:    [][]common.Hash{{eventSignature}},
		}

		logs, err := client.FilterLogs(context.Background(), query)
		if err != nil {
			fmt.Printf("Failed to query logs for blocks %s-%s: %v\n", fromBlock.String(), toBlock.String(), err)
			continue
		}

		if len(logs) > 0 {
			fmt.Printf("\nFound %d events in blocks %s-%s:\n", len(logs), fromBlock.String(), toBlock.String())
			for _, log := range logs {
				var intentID *big.Int
				if len(log.Topics) > 1 {
					intentID = new(big.Int).SetBytes(log.Topics[1].Bytes())
				}
				fmt.Printf("  Intent ID %s at block %d, tx %s\n", intentID.String(), log.BlockNumber, log.TxHash.Hex())
				totalEvents++
			}
		}
	}

	fmt.Printf("\nTotal events found: %d\n", totalEvents)
}
