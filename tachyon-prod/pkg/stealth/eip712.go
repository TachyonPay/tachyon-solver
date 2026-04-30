package stealth

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EIP-712 Domain Separator for TachyonStealth
// Off-chain verification only: no verifyingContract or chainId needed
var (
	// EIP712DomainTypeHash = keccak256("EIP712Domain(string name,string version)")
	EIP712DomainTypeHash = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version)"))

	// Domain values
	domainName    = "TachyonStealth"
	domainVersion = "1"

	// Precomputed domain separator
	DomainSeparator = computeDomainSeparator()

	// Type hashes for each message type
	RegisterStealthTypeHash     = crypto.Keccak256Hash([]byte("RegisterStealth(address signer)"))
	GetStealthAddressesTypeHash = crypto.Keccak256Hash([]byte("GetStealthAddresses(address signer)"))
	DeriveStealthKeyTypeHash    = crypto.Keccak256Hash([]byte("DeriveStealthKey(address recipientAddress,address stealthAddress)"))
	DeploySafeTypeHash          = crypto.Keccak256Hash([]byte("DeploySafe(address stealthAddress,uint256 chainId)"))
	AuthSessionTypeHash         = crypto.Keccak256Hash([]byte("AuthSession(uint256 timestamp)"))
)

func init() {
	fmt.Printf("[EIP-712] DomainTypeHash:     %x\n", EIP712DomainTypeHash)
	fmt.Printf("[EIP-712] DomainSeparator:    %x\n", DomainSeparator)
	fmt.Printf("[EIP-712] RegisterTypeHash:   %x\n", RegisterStealthTypeHash)
}

func computeDomainSeparator() common.Hash {
	return crypto.Keccak256Hash(
		EIP712DomainTypeHash.Bytes(),
		crypto.Keccak256([]byte(domainName)),
		crypto.Keccak256([]byte(domainVersion)),
	)
}

// hashTypedData computes the EIP-712 hash: keccak256("\x19\x01" || domainSeparator || structHash)
func hashTypedData(structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash(
		[]byte{0x19, 0x01},
		DomainSeparator.Bytes(),
		structHash.Bytes(),
	)
}

// --- Struct hash builders ---

func hashRegisterStealth(signer common.Address) common.Hash {
	return crypto.Keccak256Hash(
		RegisterStealthTypeHash.Bytes(),
		common.LeftPadBytes(signer.Bytes(), 32),
	)
}

func hashGetStealthAddresses(signer common.Address) common.Hash {
	return crypto.Keccak256Hash(
		GetStealthAddressesTypeHash.Bytes(),
		common.LeftPadBytes(signer.Bytes(), 32),
	)
}

func hashDeriveStealthKey(recipientAddress, stealthAddress common.Address) common.Hash {
	return crypto.Keccak256Hash(
		DeriveStealthKeyTypeHash.Bytes(),
		common.LeftPadBytes(recipientAddress.Bytes(), 32),
		common.LeftPadBytes(stealthAddress.Bytes(), 32),
	)
}

func hashDeploySafe(stealthAddress common.Address, chainId *big.Int) common.Hash {
	return crypto.Keccak256Hash(
		DeploySafeTypeHash.Bytes(),
		common.LeftPadBytes(stealthAddress.Bytes(), 32),
		common.LeftPadBytes(chainId.Bytes(), 32),
	)
}

// --- Sign functions ---

// SignRegisterStealth signs a RegisterStealth EIP-712 message
func SignRegisterStealth(key *ecdsa.PrivateKey, signer common.Address) ([]byte, error) {
	digest := hashTypedData(hashRegisterStealth(signer))
	return crypto.Sign(digest.Bytes(), key)
}

// SignGetStealthAddresses signs a GetStealthAddresses EIP-712 message
func SignGetStealthAddresses(key *ecdsa.PrivateKey, signer common.Address) ([]byte, error) {
	digest := hashTypedData(hashGetStealthAddresses(signer))
	return crypto.Sign(digest.Bytes(), key)
}

// SignDeriveStealthKey signs a DeriveStealthKey EIP-712 message
func SignDeriveStealthKey(key *ecdsa.PrivateKey, recipientAddress, stealthAddress common.Address) ([]byte, error) {
	digest := hashTypedData(hashDeriveStealthKey(recipientAddress, stealthAddress))
	return crypto.Sign(digest.Bytes(), key)
}

// SignDeploySafe signs a DeploySafe EIP-712 message
func SignDeploySafe(key *ecdsa.PrivateKey, stealthAddress common.Address, chainId *big.Int) ([]byte, error) {
	digest := hashTypedData(hashDeploySafe(stealthAddress, chainId))
	return crypto.Sign(digest.Bytes(), key)
}

// --- Verify functions ---

// VerifyRegisterStealth verifies a RegisterStealth EIP-712 signature
func VerifyRegisterStealth(expectedAddr common.Address, signature []byte) bool {
	structHash := hashRegisterStealth(expectedAddr)
	digest := hashTypedData(structHash)
	fmt.Printf("[EIP-712-VERIFY] RegisterStealth: addr=%s structHash=%x digest=%x sigLen=%d\n", expectedAddr.Hex(), structHash[:8], digest[:8], len(signature))
	return verifySig(expectedAddr, digest, signature)
}

// VerifyGetStealthAddresses verifies a GetStealthAddresses EIP-712 signature
func VerifyGetStealthAddresses(expectedAddr common.Address, signature []byte) bool {
	digest := hashTypedData(hashGetStealthAddresses(expectedAddr))
	return verifySig(expectedAddr, digest, signature)
}

// VerifyDeriveStealthKey verifies a DeriveStealthKey EIP-712 signature
func VerifyDeriveStealthKey(expectedAddr common.Address, recipientAddress, stealthAddress common.Address, signature []byte) bool {
	digest := hashTypedData(hashDeriveStealthKey(recipientAddress, stealthAddress))
	return verifySig(expectedAddr, digest, signature)
}

// VerifyDeploySafe verifies a DeploySafe EIP-712 signature
func VerifyDeploySafe(expectedAddr common.Address, stealthAddress common.Address, chainId *big.Int, signature []byte) bool {
	digest := hashTypedData(hashDeploySafe(stealthAddress, chainId))
	return verifySig(expectedAddr, digest, signature)
}

func hashAuthSession(timestamp *big.Int) common.Hash {
	return crypto.Keccak256Hash(
		AuthSessionTypeHash.Bytes(),
		common.LeftPadBytes(timestamp.Bytes(), 32),
	)
}

// RecoverAuthSession recovers the signer address from an EIP-712 AuthSession signature.
// The client signs AuthSession(uint256 timestamp) under the TachyonStealth v1 domain.
func RecoverAuthSession(signature []byte, timestamp int64) (common.Address, error) {
	digest := hashTypedData(hashAuthSession(big.NewInt(timestamp)))

	if len(signature) != 65 {
		return common.Address{}, fmt.Errorf("invalid signature length: %d", len(signature))
	}
	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}

	pubKey, err := crypto.SigToPub(digest.Bytes(), sigCopy)
	if err != nil {
		return common.Address{}, fmt.Errorf("could not recover signer: %w", err)
	}
	return crypto.PubkeyToAddress(*pubKey), nil
}

// verifySig recovers the signer from a 65-byte signature and compares to expected address
func verifySig(expectedAddr common.Address, digest common.Hash, signature []byte) bool {
	if len(signature) != 65 {
		return false
	}

	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}

	pubKey, err := crypto.SigToPub(digest.Bytes(), sigCopy)
	if err != nil {
		return false
	}

	recovered := crypto.PubkeyToAddress(*pubKey)
	if recovered != expectedAddr {
		fmt.Printf("[EIP-712] Signature mismatch: expected %s, recovered %s\n", expectedAddr.Hex(), recovered.Hex())
		return false
	}
	return true
}
