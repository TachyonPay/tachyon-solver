package storage

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cockroachdb/pebble"
	log "github.com/sirupsen/logrus"
)

// Store provides persistent key-value storage using PebbleDB
type Store struct {
	db   *pebble.DB
	path string
	mu   sync.RWMutex
}

// Prefix constants for different data types
const (
	PrefixIntent           = "intent:"
	PrefixRecipient        = "recipient:"
	PrefixIntentDetails    = "intent_details:"
	PrefixSolver           = "solver:"
	PrefixLastBlock        = "last_block:"
	PrefixActiveIntent     = "active_intent:"
	PrefixHandlingWin      = "handling_win:"
	PrefixRecipientAccount = "recipient_account:"
	PrefixStealthByAddr    = "stealth_by_addr:"
	PrefixTxHistory        = "tx_history:"
	PrefixSolanaStealth    = "solana_stealth:" // key: recipient base58 pubkey → SolanaRecipientAccount
)

// NewStore creates a new PebbleDB store at the given path
func NewStore(dbPath string) (*Store, error) {
	opts := &pebble.Options{
		// Use default options optimized for general workloads
		MaxConcurrentCompactions: func() int { return 2 },
	}

	db, err := pebble.Open(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open pebble db at %s: %w", dbPath, err)
	}

	log.WithField("path", dbPath).Info("PebbleDB store initialized")

	return &Store{
		db:   db,
		path: dbPath,
	}, nil
}

// Close closes the database
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Put stores a value with the given key
func (s *Store) Put(key string, value []byte) error {
	return s.db.Set([]byte(key), value, pebble.Sync)
}

// Get retrieves a value by key
func (s *Store) Get(key string) ([]byte, error) {
	value, closer, err := s.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	// Copy the value since it's only valid until closer.Close()
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
}

// Delete removes a key-value pair
func (s *Store) Delete(key string) error {
	return s.db.Delete([]byte(key), pebble.Sync)
}

// Exists checks if a key exists
func (s *Store) Exists(key string) (bool, error) {
	_, closer, err := s.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	closer.Close()
	return true, nil
}

// PutJSON stores a JSON-serializable value
func (s *Store) PutJSON(key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	return s.Put(key, data)
}

// GetJSON retrieves and unmarshals a JSON value
func (s *Store) GetJSON(key string, dest interface{}) error {
	data, err := s.Get(key)
	if err != nil {
		return err
	}
	if data == nil {
		return pebble.ErrNotFound
	}
	return json.Unmarshal(data, dest)
}

// ListByPrefix returns all keys with a given prefix
func (s *Store) ListByPrefix(prefix string) ([]string, error) {
	var keys []string

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: []byte(prefixEnd(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		keys = append(keys, string(iter.Key()))
	}

	return keys, iter.Error()
}

// GetAllByPrefix returns all values with a given prefix
func (s *Store) GetAllByPrefix(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: []byte(prefixEnd(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := string(iter.Key())
		value := make([]byte, len(iter.Value()))
		copy(value, iter.Value())
		result[key] = value
	}

	return result, iter.Error()
}

// DeleteByPrefix deletes all keys with a given prefix
func (s *Store) DeleteByPrefix(prefix string) error {
	keys, err := s.ListByPrefix(prefix)
	if err != nil {
		return err
	}

	batch := s.db.NewBatch()
	defer batch.Close()

	for _, key := range keys {
		if err := batch.Delete([]byte(key), nil); err != nil {
			return err
		}
	}

	return batch.Commit(pebble.Sync)
}

// Batch provides batch write operations
type Batch struct {
	batch *pebble.Batch
	store *Store
}

// NewBatch creates a new batch for atomic writes
func (s *Store) NewBatch() *Batch {
	return &Batch{
		batch: s.db.NewBatch(),
		store: s,
	}
}

// Put adds a put operation to the batch
func (b *Batch) Put(key string, value []byte) {
	b.batch.Set([]byte(key), value, nil)
}

// PutJSON adds a JSON put operation to the batch
func (b *Batch) PutJSON(key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.Put(key, data)
	return nil
}

// Delete adds a delete operation to the batch
func (b *Batch) Delete(key string) {
	b.batch.Delete([]byte(key), nil)
}

// Commit commits all batch operations atomically
func (b *Batch) Commit() error {
	return b.batch.Commit(pebble.Sync)
}

// Close discards the batch without committing
func (b *Batch) Close() error {
	return b.batch.Close()
}

// prefixEnd returns the end boundary for prefix iteration
func prefixEnd(prefix string) string {
	if len(prefix) == 0 {
		return ""
	}
	end := []byte(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return string(end)
		}
	}
	return ""
}

// Stats returns database statistics
func (s *Store) Stats() string {
	metrics := s.db.Metrics()
	return fmt.Sprintf(
		"Levels: %d, Size: %d bytes, Compactions: %d",
		len(metrics.Levels),
		metrics.DiskSpaceUsage(),
		metrics.Compact.Count,
	)
}
