package relayer

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/blockchain"
	"github.com/zk-tachyon/tachyon-prod/pkg/stealth"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// RegisterRecipient handles the first-time registration of a recipient.
// TEE generates view + spend key pair, stores them, and returns public keys.
// On subsequent calls, returns existing keys and stealth addresses.
func (r *Relayer) RegisterRecipient(req *types.RegisterRecipientRequest) (*types.RegisterRecipientResponse, error) {
	recipientAddr := common.HexToAddress(req.RecipientAddress).Hex() // normalize

	// Verify EIP-712 signature: RegisterStealth(address signer)
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(req.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	if !stealth.VerifyRegisterStealth(common.HexToAddress(recipientAddr), sigBytes) {
		return nil, fmt.Errorf("invalid signature: does not match recipient address")
	}

	// Check if already registered
	account, err := r.store.GetRecipientAccount(recipientAddr)
	if err != nil {
		return nil, fmt.Errorf("storage error: %w", err)
	}

	if account != nil {
		// Already registered, return existing data
		return &types.RegisterRecipientResponse{
			Success:          true,
			SpendPubKey:      account.SpendPubKey,
			ViewPubKey:       account.ViewPubKey,
			StealthAddresses: account.StealthAddresses,
		}, nil
	}

	// Generate new key pairs in TEE
	viewPrivKey, err := stealth.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate view key: %w", err)
	}

	spendPrivKey, err := stealth.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate spend key: %w", err)
	}

	now := time.Now().Unix()
	account = &types.RecipientAccount{
		RecipientAddress: recipientAddr,
		ViewPrivKey:      stealth.PrivKeyToHex(viewPrivKey),
		SpendPrivKey:     stealth.PrivKeyToHex(spendPrivKey),
		SpendPubKey:      stealth.PubKeyToHex(&spendPrivKey.PublicKey),
		ViewPubKey:       stealth.PubKeyToHex(&viewPrivKey.PublicKey),
		StealthAddresses: []types.StealthAddress{},
		Nonce:            0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := r.store.SaveRecipientAccount(recipientAddr, account); err != nil {
		return nil, fmt.Errorf("failed to save recipient account: %w", err)
	}

	log.WithFields(log.Fields{
		"recipient":   recipientAddr,
		"spendPubKey": account.SpendPubKey[:16] + "...",
	}).Info("Registered new recipient with stealth keys")

	return &types.RegisterRecipientResponse{
		Success:          true,
		SpendPubKey:      account.SpendPubKey,
		ViewPubKey:       account.ViewPubKey,
		StealthAddresses: account.StealthAddresses,
	}, nil
}

// GetStealthAddresses returns all stealth addresses for a registered recipient.
// Requires signature to prove ownership.
func (r *Relayer) GetStealthAddresses(recipientAddr string, signature string) (*types.GetStealthAddressesResponse, error) {
	normalizedAddr := common.HexToAddress(recipientAddr).Hex()

	// Verify EIP-712 signature: GetStealthAddresses(address signer)
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	if !stealth.VerifyGetStealthAddresses(common.HexToAddress(normalizedAddr), sigBytes) {
		return nil, fmt.Errorf("invalid signature")
	}

	account, err := r.store.GetRecipientAccount(normalizedAddr)
	if err != nil {
		return nil, fmt.Errorf("storage error: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("recipient not registered")
	}

	return &types.GetStealthAddressesResponse{
		Success:          true,
		StealthAddresses: account.StealthAddresses,
	}, nil
}

// GenerateStealthAddress generates a new stealth address for a recipient.
// Called internally by the TEE when a sender sends to this recipient.
func (r *Relayer) GenerateStealthAddress(req *types.GenerateStealthRequest) (*types.GenerateStealthResponse, error) {
	recipientAddr := common.HexToAddress(req.RecipientAddress).Hex()

	account, err := r.store.GetRecipientAccount(recipientAddr)
	if err != nil {
		return nil, fmt.Errorf("storage error: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("recipient %s not registered, must call /recipient/register first", recipientAddr)
	}

	// Load keys
	viewPrivKey, err := stealth.HexToPrivKey(account.ViewPrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load view key: %w", err)
	}

	spendPubKey, err := stealth.HexToPubKey(account.SpendPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load spend pub key: %w", err)
	}

	// Parse chain ID
	chainID, err := strconv.ParseUint(req.ChainID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid chainId: %w", err)
	}

	// Generate ephemeral key from view key + nonce + chainId
	ephemeralKey, err := stealth.GenerateEphemeralKey(viewPrivKey, account.Nonce, chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key: %w", err)
	}

	// Derive stealth address
	stealthAddr, err := stealth.DeriveStealthAddress(ephemeralKey, spendPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive stealth address: %w", err)
	}

	// Predict the Safe address controlled by this stealth EOA
	predictedSafe, err := blockchain.PredictSafeAddressForOwner(stealthAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to predict safe address: %w", err)
	}

	// Store the new stealth address with predicted Safe
	stealthEntry := types.StealthAddress{
		Address:         stealthAddr.Hex(),
		EphemeralPubKey: stealth.PubKeyToHex(&ephemeralKey.PublicKey),
		ChainID:         req.ChainID,
		IntentID:        req.IntentID,
		SafeDeployed:    false,
		SafeAddress:     predictedSafe.Hex(),
		CreatedAt:       time.Now().Unix(),
	}

	account.StealthAddresses = append(account.StealthAddresses, stealthEntry)
	account.Nonce++
	account.UpdatedAt = time.Now().Unix()

	if err := r.store.SaveRecipientAccount(recipientAddr, account); err != nil {
		return nil, fmt.Errorf("failed to update recipient account: %w", err)
	}

	// Save reverse mapping: stealth address -> recipient
	if err := r.store.SaveStealthToRecipientMapping(stealthAddr.Hex(), recipientAddr); err != nil {
		log.WithError(err).Warn("Failed to save stealth->recipient mapping")
	}

	log.WithFields(log.Fields{
		"recipient":      recipientAddr,
		"stealthAddress": stealthAddr.Hex(),
		"safeAddress":    predictedSafe.Hex(),
		"chainId":        req.ChainID,
		"intentId":       req.IntentID,
		"nonce":          account.Nonce - 1,
	}).Info("Generated new stealth address with predicted Safe for recipient")

	return &types.GenerateStealthResponse{
		StealthAddress: stealthAddr.Hex(),
		SafeAddress:    predictedSafe.Hex(),
	}, nil
}

// DeriveStealthKey derives the stealth private key for a given stealth address.
// The recipient proves ownership by signing with their original EOA.
// The TEE uses spendPrivKey + ephemeralPubKey to compute the stealth private key.
// Message format: "DERIVE_STEALTH_KEY:<recipientAddress>:<stealthAddress>"
func (r *Relayer) DeriveStealthKey(req *types.DeriveStealthKeyRequest) (*types.DeriveStealthKeyResponse, error) {
	recipientAddr := common.HexToAddress(req.RecipientAddress).Hex()
	stealthAddr := common.HexToAddress(req.StealthAddress)

	// Verify EIP-712 signature: DeriveStealthKey(address recipientAddress, address stealthAddress)
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(req.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	if !stealth.VerifyDeriveStealthKey(common.HexToAddress(recipientAddr), common.HexToAddress(recipientAddr), stealthAddr, sigBytes) {
		return nil, fmt.Errorf("invalid signature: does not match recipient address")
	}

	// Verify this stealth address belongs to this recipient
	ownerAddr, err := r.store.GetRecipientByStealthAddress(stealthAddr.Hex())
	if err != nil || ownerAddr == "" {
		return nil, fmt.Errorf("stealth address not found")
	}
	if common.HexToAddress(ownerAddr) != common.HexToAddress(recipientAddr) {
		return nil, fmt.Errorf("stealth address does not belong to this recipient")
	}

	// Load recipient account
	account, err := r.store.GetRecipientAccount(recipientAddr)
	if err != nil || account == nil {
		return nil, fmt.Errorf("recipient account not found")
	}

	// Find the stealth entry to get the ephemeral pub key
	var ephemeralPubKeyHex string
	for _, sa := range account.StealthAddresses {
		if common.HexToAddress(sa.Address) == stealthAddr {
			ephemeralPubKeyHex = sa.EphemeralPubKey
			break
		}
	}
	if ephemeralPubKeyHex == "" {
		return nil, fmt.Errorf("stealth address not found in account")
	}

	// Derive the stealth private key using TEE-held spend key
	spendPrivKey, err := stealth.HexToPrivKey(account.SpendPrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load spend key: %w", err)
	}

	ephemeralPubKey, err := stealth.HexToPubKey(ephemeralPubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to load ephemeral pub key: %w", err)
	}

	stealthPrivKey, err := stealth.DeriveStealthPrivateKey(spendPrivKey, ephemeralPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive stealth private key: %w", err)
	}

	// Verify derived key matches expected stealth address
	derivedAddr := crypto.PubkeyToAddress(stealthPrivKey.PublicKey)
	if derivedAddr != stealthAddr {
		return nil, fmt.Errorf("stealth key derivation mismatch")
	}

	log.WithFields(log.Fields{
		"recipient":      recipientAddr,
		"stealthAddress": stealthAddr.Hex(),
	}).Info("Derived stealth private key for recipient")

	return &types.DeriveStealthKeyResponse{
		Success:          true,
		StealthAddress:   stealthAddr.Hex(),
		StealthPrivateKey: stealth.PrivKeyToHex(stealthPrivKey),
	}, nil
}

// DeploySafe deploys a Safe smart account on a stealth address.
// Anyone can call this endpoint, but must provide a valid signature from
// the stealth address's spend private key.
// Message format: "DEPLOY_SAFE:<stealthAddress>:<chainId>"
func (r *Relayer) DeploySafe(ctx context.Context, req *types.DeploySafeRequest) (*types.DeploySafeResponse, error) {
	stealthAddr := common.HexToAddress(req.StealthAddress)

	// Look up which recipient owns this stealth address
	recipientAddr, err := r.store.GetRecipientByStealthAddress(stealthAddr.Hex())
	if err != nil || recipientAddr == "" {
		return nil, fmt.Errorf("stealth address %s not found", stealthAddr.Hex())
	}

	account, err := r.store.GetRecipientAccount(recipientAddr)
	if err != nil || account == nil {
		return nil, fmt.Errorf("recipient account not found")
	}

	// Find the stealth address entry
	var stealthEntry *types.StealthAddress
	var stealthIdx int
	for i, sa := range account.StealthAddresses {
		if common.HexToAddress(sa.Address) == stealthAddr {
			stealthEntry = &account.StealthAddresses[i]
			stealthIdx = i
			break
		}
	}
	if stealthEntry == nil {
		return nil, fmt.Errorf("stealth address not found in account")
	}

	if stealthEntry.SafeDeployed {
		return &types.DeploySafeResponse{
			Success:     true,
			SafeAddress: stealthEntry.SafeAddress,
		}, nil
	}

	// Verify signature from the spend key derived stealth address
	// The caller must prove they hold the spend private key by signing as the stealth address
	// We reconstruct the stealth private key in the TEE to verify
	spendPrivKey, err := stealth.HexToPrivKey(account.SpendPrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load spend key: %w", err)
	}

	ephemeralPubKey, err := stealth.HexToPubKey(stealthEntry.EphemeralPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load ephemeral pub key: %w", err)
	}

	// Derive the stealth private key (TEE can do this since it holds spend priv key)
	stealthPrivKey, err := stealth.DeriveStealthPrivateKey(spendPrivKey, ephemeralPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive stealth private key: %w", err)
	}

	// Verify the expected stealth address matches
	derivedAddr := crypto.PubkeyToAddress(stealthPrivKey.PublicKey)
	if derivedAddr != stealthAddr {
		return nil, fmt.Errorf("stealth key derivation mismatch")
	}

	// Verify EIP-712 signature: DeploySafe(address stealthAddress, uint256 chainId)
	chainIDBig, ok := new(big.Int).SetString(req.ChainID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid chainId: %s", req.ChainID)
	}
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(req.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	if !stealth.VerifyDeploySafe(stealthAddr, stealthAddr, chainIDBig, sigBytes) {
		return nil, fmt.Errorf("invalid signature: must be signed by stealth address spend key")
	}

	// Find the blockchain client for this chain
	client, ok := r.clients[req.ChainID]
	if !ok {
		return nil, fmt.Errorf("chain %s not configured", req.ChainID)
	}

	// Use a deterministic salt based on the stealth address
	saltNonce := new(big.Int).SetBytes(crypto.Keccak256(stealthAddr.Bytes()))

	r.txMutexes[req.ChainID].Lock()
	defer r.txMutexes[req.ChainID].Unlock()

	tx, predictedSafe, err := client.DeploySafe(ctx, stealthAddr, saltNonce)
	if err != nil {
		return nil, fmt.Errorf("safe deployment failed: %w", err)
	}

	// Wait for confirmation
	receipt, err := client.WaitForTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("safe deployment tx failed: %w", err)
	}

	log.WithFields(log.Fields{
		"stealthAddress": stealthAddr.Hex(),
		"safeAddress":    predictedSafe.Hex(),
		"txHash":         tx.Hash().Hex(),
		"blockNumber":    receipt.BlockNumber,
		"gasUsed":        receipt.GasUsed,
	}).Info("Safe deployed successfully for stealth address")

	// Update the stealth entry
	account.StealthAddresses[stealthIdx].SafeDeployed = true
	account.StealthAddresses[stealthIdx].SafeAddress = predictedSafe.Hex()
	account.UpdatedAt = time.Now().Unix()

	if err := r.store.SaveRecipientAccount(recipientAddr, account); err != nil {
		log.WithError(err).Warn("Failed to persist safe deployment to storage")
	}

	return &types.DeploySafeResponse{
		Success:     true,
		SafeAddress: predictedSafe.Hex(),
		TxHash:      tx.Hash().Hex(),
	}, nil
}

