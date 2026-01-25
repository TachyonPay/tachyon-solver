package storage

import (
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/zk-tachyon/tachyon-prod/pkg/types"
)

// Intent Storage Helpers

// SaveIntentStatus saves an intent status
func (s *Store) SaveIntentStatus(networkKey string, intentID string, status *types.IntentStatus) error {
	key := fmt.Sprintf("%s%s-%s", PrefixActiveIntent, networkKey, intentID)
	return s.PutJSON(key, status)
}

// GetIntentStatus retrieves an intent status
func (s *Store) GetIntentStatus(networkKey string, intentID string) (*types.IntentStatus, error) {
	key := fmt.Sprintf("%s%s-%s", PrefixActiveIntent, networkKey, intentID)
	var status types.IntentStatus
	if err := s.GetJSON(key, &status); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &status, nil
}

// DeleteIntentStatus removes an intent status
func (s *Store) DeleteIntentStatus(networkKey string, intentID string) error {
	key := fmt.Sprintf("%s%s-%s", PrefixActiveIntent, networkKey, intentID)
	return s.Delete(key)
}

// GetAllIntentStatuses returns all active intent statuses
func (s *Store) GetAllIntentStatuses() (map[string]*types.IntentStatus, error) {
	result := make(map[string]*types.IntentStatus)
	data, err := s.GetAllByPrefix(PrefixActiveIntent)
	if err != nil {
		return nil, err
	}

	for key, value := range data {
		var status types.IntentStatus
		if err := unmarshalJSON(value, &status); err != nil {
			continue
		}
		// Strip prefix from key
		shortKey := key[len(PrefixActiveIntent):]
		result[shortKey] = &status
	}
	return result, nil
}

// Last Block Storage

// SaveLastProcessedBlock saves the last processed block number
func (s *Store) SaveLastProcessedBlock(networkKey string, blockNum *big.Int) error {
	key := fmt.Sprintf("%s%s", PrefixLastBlock, networkKey)
	return s.Put(key, []byte(blockNum.String()))
}

// GetLastProcessedBlock retrieves the last processed block number
func (s *Store) GetLastProcessedBlock(networkKey string) (*big.Int, error) {
	key := fmt.Sprintf("%s%s", PrefixLastBlock, networkKey)
	data, err := s.Get(key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	blockNum, ok := new(big.Int).SetString(string(data), 10)
	if !ok {
		return nil, fmt.Errorf("invalid block number: %s", string(data))
	}
	return blockNum, nil
}

// Handling Wins Storage

// SetHandlingWin marks an intent as being handled for win
func (s *Store) SetHandlingWin(networkKey string, intentID string, handling bool) error {
	key := fmt.Sprintf("%s%s-%s", PrefixHandlingWin, networkKey, intentID)
	if handling {
		return s.Put(key, []byte("1"))
	}
	return s.Delete(key)
}

// IsHandlingWin checks if an intent is being handled for win
func (s *Store) IsHandlingWin(networkKey string, intentID string) (bool, error) {
	key := fmt.Sprintf("%s%s-%s", PrefixHandlingWin, networkKey, intentID)
	return s.Exists(key)
}

// Relayer Storage Helpers

// SaveRecipientInfo saves recipient information
func (s *Store) SaveRecipientInfo(sourceChainID int64, intentID string, info *types.RecipientInfo) error {
	key := fmt.Sprintf("%s%d-%s", PrefixRecipient, sourceChainID, intentID)
	return s.PutJSON(key, info)
}

// GetRecipientInfo retrieves recipient information
func (s *Store) GetRecipientInfo(sourceChainID int64, intentID string) (*types.RecipientInfo, error) {
	key := fmt.Sprintf("%s%d-%s", PrefixRecipient, sourceChainID, intentID)
	var info types.RecipientInfo
	if err := s.GetJSON(key, &info); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}

// GetRecipientInfoByKey retrieves recipient info by composite key
func (s *Store) GetRecipientInfoByKey(storeKey string) (*types.RecipientInfo, error) {
	key := PrefixRecipient + storeKey
	var info types.RecipientInfo
	if err := s.GetJSON(key, &info); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}

// DeleteRecipientInfo removes recipient information
func (s *Store) DeleteRecipientInfo(storeKey string) error {
	key := PrefixRecipient + storeKey
	return s.Delete(key)
}

// ListStoredIntentIDs returns all stored intent IDs
func (s *Store) ListStoredIntentIDs() ([]string, error) {
	keys, err := s.ListByPrefix(PrefixRecipient)
	if err != nil {
		return nil, err
	}
	
	// Strip prefix from keys
	result := make([]string, len(keys))
	for i, key := range keys {
		result[i] = key[len(PrefixRecipient):]
	}
	return result, nil
}

// Intent Details Storage

// SaveIntentDetails saves intent details
func (s *Store) SaveIntentDetails(sourceChainID string, intentID string, details *types.IntentDetails) error {
	key := fmt.Sprintf("%s%s-%s", PrefixIntentDetails, sourceChainID, intentID)
	return s.PutJSON(key, details)
}

// GetIntentDetails retrieves intent details
func (s *Store) GetIntentDetails(sourceChainID string, intentID string) (*types.IntentDetails, error) {
	key := fmt.Sprintf("%s%s-%s", PrefixIntentDetails, sourceChainID, intentID)
	var details types.IntentDetails
	if err := s.GetJSON(key, &details); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &details, nil
}

// Solver Registration Storage

// SaveSolverInfo saves solver registration info
func (s *Store) SaveSolverInfo(solverAddress string, info *types.SolverInfo) error {
	key := PrefixSolver + solverAddress
	return s.PutJSON(key, info)
}

// GetSolverInfo retrieves solver info
func (s *Store) GetSolverInfo(solverAddress string) (*types.SolverInfo, error) {
	key := PrefixSolver + solverAddress
	var info types.SolverInfo
	if err := s.GetJSON(key, &info); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}

// GetAllSolvers returns all registered solvers
func (s *Store) GetAllSolvers() (map[string]*types.SolverInfo, error) {
	result := make(map[string]*types.SolverInfo)
	data, err := s.GetAllByPrefix(PrefixSolver)
	if err != nil {
		return nil, err
	}

	for key, value := range data {
		var info types.SolverInfo
		if err := unmarshalJSON(value, &info); err != nil {
			continue
		}
		// Strip prefix from key
		solverAddr := key[len(PrefixSolver):]
		result[solverAddr] = &info
	}
	return result, nil
}

// Cleanup old data

// CleanupOldIntents removes intents older than the specified duration
func (s *Store) CleanupOldIntents(maxAge time.Duration) (int, error) {
	keys, err := s.ListByPrefix(PrefixActiveIntent)
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge)
	deleted := 0

	for _, key := range keys {
		var status types.IntentStatus
		if err := s.GetJSON(key, &status); err != nil {
			continue
		}

		if status.LastUpdated.Before(cutoff) && (status.Completed || status.Status == "completed") {
			if err := s.Delete(key); err == nil {
				deleted++
			}
		}
	}

	return deleted, nil
}

// Helper function
func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
