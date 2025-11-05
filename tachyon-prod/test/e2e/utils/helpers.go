package utils

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TestConfig holds test configuration
type TestConfig struct {
	HorizenRPC             string
	BaseRPC                string
	BridgeHorizenAddr      string
	BridgeBaseAddr         string
	PrivateKey             string
	SolverURL              string
	RelayerURL             string
	AuctionDuration        int64
	TestTimeout            time.Duration
}

// TestWallet represents a test wallet
type TestWallet struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
	Address    common.Address
}

// NetworkClients holds clients for both networks
type NetworkClients struct {
	HorizenClient *ethclient.Client
	BaseClient    *ethclient.Client
	Wallet        *TestWallet
	HorizenChainID *big.Int
	BaseChainID    *big.Int
}

// CreateTestWallet creates a test wallet from private key
func CreateTestWallet(privateKeyHex string) (*TestWallet, error) {
	privateKey, err := crypto.HexToECDSA(privateKeyHex[2:]) // Remove 0x prefix
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to cast public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return &TestWallet{
		PrivateKey: privateKey,
		PublicKey:  publicKeyECDSA,
		Address:    address,
	}, nil
}

// InitClients initializes Ethereum clients for both networks
func InitClients(ctx context.Context, cfg *TestConfig) (*NetworkClients, error) {
	// Connect to Horizen
	horizenClient, err := ethclient.Dial(cfg.HorizenRPC)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Horizen: %w", err)
	}

	// Connect to Base
	baseClient, err := ethclient.Dial(cfg.BaseRPC)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Base: %w", err)
	}

	// Create wallet
	wallet, err := CreateTestWallet(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	// Get chain IDs
	horizenChainID, err := horizenClient.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Horizen chain ID: %w", err)
	}

	baseChainID, err := baseClient.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Base chain ID: %w", err)
	}

	return &NetworkClients{
		HorizenClient:  horizenClient,
		BaseClient:     baseClient,
		Wallet:         wallet,
		HorizenChainID: horizenChainID,
		BaseChainID:    baseChainID,
	}, nil
}

// GetTransactOpts creates transaction options for signing
func GetTransactOpts(ctx context.Context, client *ethclient.Client, wallet *TestWallet, chainID *big.Int) (*bind.TransactOpts, error) {
	nonce, err := client.PendingNonceAt(ctx, wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(wallet.PrivateKey, chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0)
	auth.GasLimit = uint64(3000000)
	auth.GasPrice = gasPrice
	auth.Context = ctx

	return auth, nil
}

// WaitForTransaction waits for a transaction to be mined
func WaitForTransaction(ctx context.Context, client *ethclient.Client, txHash common.Hash, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("transaction wait timeout")
		case <-ticker.C:
			receipt, err := client.TransactionReceipt(ctx, txHash)
			if err == nil && receipt != nil {
				if receipt.Status == 1 {
					return nil
				}
				return fmt.Errorf("transaction failed with status: %d", receipt.Status)
			}
		}
	}
}

// GenerateRandomAddress generates a random Ethereum address
func GenerateRandomAddress() common.Address {
	privateKey, _ := crypto.GenerateKey()
	publicKey := privateKey.Public()
	publicKeyECDSA := publicKey.(*ecdsa.PublicKey)
	return crypto.PubkeyToAddress(*publicKeyECDSA)
}

// ParseEther converts ether amount to wei
func ParseEther(ether string) *big.Int {
	eth := new(big.Float)
	eth.SetString(ether)
	wei := new(big.Float)
	wei.Mul(eth, big.NewFloat(1e18))
	result := new(big.Int)
	wei.Int(result)
	return result
}

// FormatEther converts wei to ether string
func FormatEther(wei *big.Int) string {
	eth := new(big.Float).SetInt(wei)
	eth.Quo(eth, big.NewFloat(1e18))
	return eth.Text('f', 4)
}

// SleepWithProgress sleeps with progress indicator
func SleepWithProgress(duration time.Duration, message string) {
	fmt.Printf("%s", message)

	steps := 10
	sleepPerStep := duration / time.Duration(steps)

	for i := 0; i < steps; i++ {
		time.Sleep(sleepPerStep)
		fmt.Print(".")
	}
	fmt.Println(" Done!")
}
