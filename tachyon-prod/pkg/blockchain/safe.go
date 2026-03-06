package blockchain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	log "github.com/sirupsen/logrus"
)

// Safe v1.3.0 canonical deployment addresses (same on all EVM chains)
var (
	SafeSingletonAddress     = common.HexToAddress("0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552") // Safe v1.3.0
	SafeProxyFactoryAddress  = common.HexToAddress("0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2") // SafeProxyFactory v1.3.0
)

// ABIs for Safe contracts
const safeProxyFactoryABI = `[{"inputs":[{"internalType":"address","name":"_singleton","type":"address"},{"internalType":"bytes","name":"initializer","type":"bytes"},{"internalType":"uint256","name":"saltNonce","type":"uint256"}],"name":"createProxyWithNonce","outputs":[{"internalType":"contract GnosisSafeProxy","name":"proxy","type":"address"}],"stateMutability":"nonpayable","type":"function"}]`

const safeSingletonABI = `[{"inputs":[{"internalType":"address[]","name":"_owners","type":"address[]"},{"internalType":"uint256","name":"_threshold","type":"uint256"},{"internalType":"address","name":"to","type":"address"},{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"address","name":"fallbackHandler","type":"address"},{"internalType":"address","name":"paymentToken","type":"address"},{"internalType":"uint256","name":"payment","type":"uint256"},{"internalType":"address payable","name":"paymentReceiver","type":"address"}],"name":"setup","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

// DeploySafe deploys a new Safe proxy with the given owner address.
// The owner should be the stealth address that controls this Safe.
// Returns the transaction and the predicted Safe address.
func (c *Client) DeploySafe(ctx context.Context, ownerAddress common.Address, saltNonce *big.Int) (*types.Transaction, common.Address, error) {
	// Parse ABIs
	proxyFactoryABI, err := abi.JSON(strings.NewReader(safeProxyFactoryABI))
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to parse proxy factory ABI: %w", err)
	}

	singletonABI, err := abi.JSON(strings.NewReader(safeSingletonABI))
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to parse singleton ABI: %w", err)
	}

	// Encode the Safe setup call data
	// 1-of-1 multisig with the stealth address as sole owner
	owners := []common.Address{ownerAddress}
	threshold := big.NewInt(1)
	fallbackHandler := common.HexToAddress("0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4") // CompatibilityFallbackHandler v1.3.0

	setupData, err := singletonABI.Pack("setup",
		owners,
		threshold,
		common.Address{},    // to (no delegate call)
		[]byte{},            // data
		fallbackHandler,     // fallbackHandler
		common.Address{},    // paymentToken (ETH)
		big.NewInt(0),       // payment
		common.Address{},    // paymentReceiver
	)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to encode setup data: %w", err)
	}

	// Predict the Safe proxy address
	predictedAddress, err := PredictSafeAddress(ownerAddress, saltNonce, setupData)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to predict safe address: %w", err)
	}

	// Deploy via ProxyFactory.createProxyWithNonce
	proxyFactory := bind.NewBoundContract(SafeProxyFactoryAddress, proxyFactoryABI, c.ethClient, c.ethClient, c.ethClient)

	auth, err := c.getTransactOpts(ctx)
	if err != nil {
		return nil, common.Address{}, err
	}
	auth.GasLimit = uint64(500000) // Safe deployment needs more gas

	tx, err := proxyFactory.Transact(auth, "createProxyWithNonce",
		SafeSingletonAddress,
		setupData,
		saltNonce,
	)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to deploy safe: %w", err)
	}

	log.WithFields(log.Fields{
		"network":     c.networkName,
		"owner":       ownerAddress.Hex(),
		"predicted":   predictedAddress.Hex(),
		"saltNonce":   saltNonce.String(),
		"txHash":      tx.Hash().Hex(),
	}).Info("Safe deployment transaction submitted")

	return tx, predictedAddress, nil
}

// PredictSafeAddressForOwner predicts the Safe proxy address for a given owner (stealth) address.
// This uses the same deterministic salt and setup data as DeploySafe, so the predicted address
// will match the actual deployed Safe address.
func PredictSafeAddressForOwner(ownerAddress common.Address) (common.Address, error) {
	singletonABI, err := abi.JSON(strings.NewReader(safeSingletonABI))
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to parse singleton ABI: %w", err)
	}

	owners := []common.Address{ownerAddress}
	threshold := big.NewInt(1)
	fallbackHandler := common.HexToAddress("0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4")

	setupData, err := singletonABI.Pack("setup",
		owners,
		threshold,
		common.Address{},
		[]byte{},
		fallbackHandler,
		common.Address{},
		big.NewInt(0),
		common.Address{},
	)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to encode setup data: %w", err)
	}

	saltNonce := new(big.Int).SetBytes(crypto.Keccak256(ownerAddress.Bytes()))
	return PredictSafeAddress(ownerAddress, saltNonce, setupData)
}

// PredictSafeAddress computes the CREATE2 address for a Safe proxy.
// address = keccak256(0xff ++ factory ++ salt ++ keccak256(initCode))[12:]
func PredictSafeAddress(ownerAddress common.Address, saltNonce *big.Int, setupData []byte) (common.Address, error) {
	// The salt used by SafeProxyFactory is keccak256(keccak256(setupData) ++ saltNonce)
	setupHash := crypto.Keccak256(setupData)
	salt := crypto.Keccak256(
		setupHash,
		common.LeftPadBytes(saltNonce.Bytes(), 32),
	)

	// GnosisSafeProxy creation code (from the canonical deployment)
	// The proxy bytecode is the same across all deployments
	// initCode = proxyCreationCode ++ abi.encode(singleton)
	proxyCreationCode := common.FromHex("608060405234801561001057600080fd5b506040516101e63803806101e68339818101604052602081101561003357600080fd5b8101908080519060200190929190505050600073ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff1614156100ca576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004018080602001828103825260228152602001806101c46022913960400191505060405180910390fd5b806000806101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050609b806101296000396000f3fe608060405273ffffffffffffffffffffffffffffffffffffffff600054167fa619486e0000000000000000000000000000000000000000000000000000000060003514156050578060005260206000f35b3660008037600080366000845af43d6000803e60008114156070573d6000fd5b3d6000f3fea2646970667358221220d1429297349653a4918076d650332de1a1068c5f3e07c5c82360c277770b955264736f6c63430007060033496e76616c69642073696e676c65746f6e20616464726573732070726f7669646564")
	singletonPadded := common.LeftPadBytes(SafeSingletonAddress.Bytes(), 32)
	initCode := append(proxyCreationCode, singletonPadded...)
	initCodeHash := crypto.Keccak256(initCode)

	// CREATE2: keccak256(0xff ++ factory ++ salt ++ initCodeHash)[12:]
	create2Input := []byte{0xff}
	create2Input = append(create2Input, SafeProxyFactoryAddress.Bytes()...)
	create2Input = append(create2Input, salt...)
	create2Input = append(create2Input, initCodeHash...)

	addr := crypto.Keccak256(create2Input)[12:]
	return common.BytesToAddress(addr), nil
}
