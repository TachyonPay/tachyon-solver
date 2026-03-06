package stealth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// GenerateKeyPair generates a new ECDSA key pair on secp256k1
func GenerateKeyPair() (*ecdsa.PrivateKey, error) {
	return crypto.GenerateKey()
}

// PrivKeyToHex encodes a private key as hex string
func PrivKeyToHex(key *ecdsa.PrivateKey) string {
	return hex.EncodeToString(crypto.FromECDSA(key))
}

// PubKeyToHex encodes a public key as uncompressed hex (65 bytes)
func PubKeyToHex(key *ecdsa.PublicKey) string {
	return hex.EncodeToString(crypto.FromECDSAPub(key))
}

// HexToPrivKey decodes a hex-encoded private key
func HexToPrivKey(hexKey string) (*ecdsa.PrivateKey, error) {
	return crypto.HexToECDSA(hexKey)
}

// HexToPubKey decodes a hex-encoded uncompressed public key
func HexToPubKey(hexKey string) (*ecdsa.PublicKey, error) {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return crypto.UnmarshalPubkey(keyBytes)
}

// GenerateEphemeralKey generates a deterministic ephemeral private key from
// the viewing private key and a nonce. This ensures the same stealth address
// is reproducible given the same inputs.
//
// ephemeral = keccak256(viewPrivKey || nonce || chainId)
func GenerateEphemeralKey(viewPrivKey *ecdsa.PrivateKey, nonce uint64, chainID uint64) (*ecdsa.PrivateKey, error) {
	viewBytes := crypto.FromECDSA(viewPrivKey)

	nonceBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBytes, nonce)

	chainBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(chainBytes, chainID)

	seed := crypto.Keccak256(viewBytes, nonceBytes, chainBytes)

	// Use seed as private key scalar (mod N to ensure it's valid on secp256k1)
	curve := crypto.S256()
	k := new(big.Int).SetBytes(seed)
	k.Mod(k, curve.Params().N)

	// Ensure non-zero
	if k.Sign() == 0 {
		return nil, fmt.Errorf("derived zero scalar, increment nonce")
	}

	privKey, err := crypto.ToECDSA(common.LeftPadBytes(k.Bytes(), 32))
	if err != nil {
		return nil, fmt.Errorf("failed to create ephemeral key: %w", err)
	}

	return privKey, nil
}

// DeriveStealthAddress computes a stealth address using ECDH:
//
//	sharedSecret = ECDH(ephemeralPriv, spendPubKey) = ephemeralPriv * spendPubKey
//	stealthPrivKey = spendPrivKey + keccak256(sharedSecret.x) mod N
//	stealthAddress = pubToAddr(stealthPrivKey * G)
//
// The sender/TEE knows ephemeralPriv + spendPubKey to compute the stealth address.
// The recipient knows spendPrivKey + ephemeralPubKey to derive the stealth private key.
func DeriveStealthAddress(ephemeralPrivKey *ecdsa.PrivateKey, spendPubKey *ecdsa.PublicKey) (common.Address, error) {
	curve := crypto.S256()

	// ECDH: shared point = ephemeralPriv * spendPubKey
	sx, _ := curve.ScalarMult(spendPubKey.X, spendPubKey.Y, ephemeralPrivKey.D.Bytes())

	// Hash the shared secret x-coordinate
	sharedSecretHash := crypto.Keccak256(common.LeftPadBytes(sx.Bytes(), 32))
	hashInt := new(big.Int).SetBytes(sharedSecretHash)
	hashInt.Mod(hashInt, curve.Params().N)

	// stealth public key = spendPubKey + hashInt * G
	hx, hy := curve.ScalarBaseMult(hashInt.Bytes())
	stealthX, stealthY := curve.Add(spendPubKey.X, spendPubKey.Y, hx, hy)

	stealthPub := ecdsa.PublicKey{Curve: curve, X: stealthX, Y: stealthY}
	return crypto.PubkeyToAddress(stealthPub), nil
}

// DeriveStealthPrivateKey allows the recipient to compute the private key for a stealth address.
// stealthPrivKey = spendPrivKey + keccak256(ECDH(spendPrivKey, ephemeralPubKey).x) mod N
//
// Note: The recipient uses their spendPrivKey with the ephemeralPubKey to get the same shared secret.
func DeriveStealthPrivateKey(spendPrivKey *ecdsa.PrivateKey, ephemeralPubKey *ecdsa.PublicKey) (*ecdsa.PrivateKey, error) {
	curve := crypto.S256()

	// ECDH from recipient side: shared point = spendPrivKey * ephemeralPubKey
	sx, _ := curve.ScalarMult(ephemeralPubKey.X, ephemeralPubKey.Y, spendPrivKey.D.Bytes())

	// Same hash as sender computed
	sharedSecretHash := crypto.Keccak256(common.LeftPadBytes(sx.Bytes(), 32))
	hashInt := new(big.Int).SetBytes(sharedSecretHash)
	hashInt.Mod(hashInt, curve.Params().N)

	// stealthPrivKey = spendPrivKey + hashInt mod N
	stealthD := new(big.Int).Add(spendPrivKey.D, hashInt)
	stealthD.Mod(stealthD, curve.Params().N)

	stealthPrivKey, err := crypto.ToECDSA(common.LeftPadBytes(stealthD.Bytes(), 32))
	if err != nil {
		return nil, fmt.Errorf("failed to create stealth private key: %w", err)
	}

	return stealthPrivKey, nil
}

// VerifyStealthSignature verifies that a signature was produced by the holder of the
// stealth address's private key. The message should be the agreed-upon message format.
func VerifyStealthSignature(stealthAddress common.Address, message []byte, signature []byte) (bool, error) {
	if len(signature) != 65 {
		return false, fmt.Errorf("invalid signature length: %d", len(signature))
	}

	// Ethereum personal sign: hash = keccak256("\x19Ethereum Signed Message:\n" + len(message) + message)
	prefixedMsg := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256([]byte(prefixedMsg))

	// Recover public key from signature
	sigCopy := make([]byte, 65)
	copy(sigCopy, signature)
	if sigCopy[64] >= 27 {
		sigCopy[64] -= 27
	}

	pubKey, err := crypto.SigToPub(hash, sigCopy)
	if err != nil {
		return false, fmt.Errorf("failed to recover public key: %w", err)
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	return recoveredAddr == stealthAddress, nil
}

// GenerateRandomNonce generates a cryptographically random nonce
func GenerateRandomNonce() (uint64, error) {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}
