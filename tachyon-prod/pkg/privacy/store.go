package privacy

import (
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

// Store wraps the Pebble store with privacy-router specific keying.
//
// Key layout:
//   privacy:intent:<sessionID>          → BridgeIntent JSON
//   privacy:by-status:<status>:<sessionID> → "" (sentinel; iterated to find intents in a status)
type Store struct {
	s *storage.Store
}

const (
	intentPrefix    = "privacy:intent:"
	byStatusPrefix  = "privacy:by-status:"
)

// NewStore opens (or creates) the privacy Pebble store under
// dbPath/privacy/.
func NewStore(dbPath string) (*Store, error) {
	full := filepath.Join(dbPath, "privacy")
	s, err := storage.NewStore(full)
	if err != nil {
		return nil, fmt.Errorf("open privacy store at %s: %w", full, err)
	}
	log.WithField("path", full).Info("Privacy store initialized")
	return &Store{s: s}, nil
}

// Close releases the Pebble db.
func (st *Store) Close() error {
	if st.s == nil {
		return nil
	}
	return st.s.Close()
}

// Save persists the intent (overwriting any existing record with the same
// sessionID) and updates the by-status index. Use this for INSERT only;
// for status transitions call ChangeStatus so the by-status index stays
// consistent.
func (st *Store) Save(intent *BridgeIntent) error {
	if intent.SessionID == "" {
		return fmt.Errorf("sessionID required")
	}
	intent.Updated = time.Now().UTC()
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if err := st.s.Put(intentPrefix+intent.SessionID, body); err != nil {
		return err
	}
	idxKey := byStatusPrefix + string(intent.Status) + ":" + intent.SessionID
	return st.s.Put(idxKey, []byte{})
}

// Get fetches an intent by sessionID. Returns (nil, nil) when not found.
func (st *Store) Get(sessionID string) (*BridgeIntent, error) {
	body, err := st.s.Get(intentPrefix + sessionID)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, nil
	}
	var intent BridgeIntent
	if err := json.Unmarshal(body, &intent); err != nil {
		return nil, err
	}
	return &intent, nil
}

// ChangeStatus updates the intent's status field, removes the old
// by-status index entry, and writes the new one. Use this whenever a
// processor moves an intent through the lifecycle.
func (st *Store) ChangeStatus(sessionID string, from, to Status) error {
	intent, err := st.Get(sessionID)
	if err != nil {
		return err
	}
	if intent == nil {
		return fmt.Errorf("intent %s not found", sessionID)
	}
	if intent.Status != from {
		return fmt.Errorf("intent %s expected status %q got %q", sessionID, from, intent.Status)
	}
	if err := st.s.Delete(byStatusPrefix + string(from) + ":" + sessionID); err != nil {
		return err
	}
	intent.Status = to
	intent.Updated = time.Now().UTC()
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	if err := st.s.Put(intentPrefix+sessionID, body); err != nil {
		return err
	}
	return st.s.Put(byStatusPrefix+string(to)+":"+sessionID, []byte{})
}

// SetFireAt stamps the intent's dispatch deadline (used by the
// scheduler to decide when to fire singleton intents).
func (st *Store) SetFireAt(sessionID string, fireAt time.Time) error {
	intent, err := st.Get(sessionID)
	if err != nil {
		return err
	}
	if intent == nil {
		return fmt.Errorf("intent %s not found", sessionID)
	}
	intent.FireAt = fireAt
	intent.Updated = time.Now().UTC()
	body, _ := json.Marshal(intent)
	return st.s.Put(intentPrefix+sessionID, body)
}

// SetTxHash stores a tx hash on the intent for one of the lifecycle
// stages. Does NOT change status.
func (st *Store) SetTxHash(sessionID string, field string, txHash string) error {
	intent, err := st.Get(sessionID)
	if err != nil {
		return err
	}
	if intent == nil {
		return fmt.Errorf("intent %s not found", sessionID)
	}
	switch field {
	case "sweep":
		intent.SweepTxHash = txHash
	case "bridge":
		intent.BridgeTxHash = txHash
	case "delivered":
		intent.DeliveredTxHash = txHash
	case "refund":
		intent.RefundTxHash = txHash
	default:
		return fmt.Errorf("unknown tx-hash field %q", field)
	}
	intent.Updated = time.Now().UTC()
	body, _ := json.Marshal(intent)
	return st.s.Put(intentPrefix+sessionID, body)
}

// SetError records a failure message and transitions to StatusFailed.
func (st *Store) SetError(sessionID string, errMsg string) error {
	intent, err := st.Get(sessionID)
	if err != nil {
		return err
	}
	if intent == nil {
		return fmt.Errorf("intent %s not found", sessionID)
	}
	prev := intent.Status
	intent.Error = errMsg
	intent.Status = StatusFailed
	intent.Updated = time.Now().UTC()
	body, _ := json.Marshal(intent)
	if err := st.s.Put(intentPrefix+sessionID, body); err != nil {
		return err
	}
	_ = st.s.Delete(byStatusPrefix + string(prev) + ":" + sessionID)
	return st.s.Put(byStatusPrefix+string(StatusFailed)+":"+sessionID, []byte{})
}

// FindActiveCollision returns an existing intent in {pending, swept}
// that shares the (userAddress, sourceChainID, sourceToken, amount)
// tuple a new submit would carry. Used to reject submit-time
// collisions: a single shared TEE Safe per chain means the deposit
// watcher matches by (chain, token, amount, from), so two pending
// intents from the same user at the same amount would race for the
// next deposit. Returns the youngest match, or nil if none.
func (st *Store) FindActiveCollision(userAddr, srcChain string, srcTok Token, amount *big.Int) (*BridgeIntent, error) {
	candidates := []*BridgeIntent{}
	for _, status := range []Status{StatusPending, StatusSwept} {
		batch, err := st.ListByStatus(status)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, batch...)
	}
	var youngest *BridgeIntent
	for _, c := range candidates {
		if !strings.EqualFold(c.UserAddress, userAddr) {
			continue
		}
		if c.SourceChainID != srcChain {
			continue
		}
		if c.SourceToken != srcTok {
			continue
		}
		if c.Amount == nil || amount == nil || c.Amount.Cmp(amount) != 0 {
			continue
		}
		if youngest == nil || c.Created.After(youngest.Created) {
			youngest = c
		}
	}
	return youngest, nil
}

// ExpireStalePendings walks all pending intents, marks any with
// `created < now - maxAge` as failed (with a synthetic "expired:
// pending TTL" error), removes them from the by-status index, and
// returns the count of expired entries. Callers should run this
// periodically to clear abandoned submits so they can't collide with
// future deposits at the same amount.
func (st *Store) ExpireStalePendings(maxAge time.Duration) (int, error) {
	pending, err := st.ListByStatus(StatusPending)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	expired := 0
	for _, intent := range pending {
		if !intent.Created.Before(cutoff) {
			continue
		}
		if err := st.SetError(intent.SessionID, "expired: pending TTL exceeded"); err != nil {
			log.WithError(err).WithField("sessionID", intent.SessionID).Warn("ExpireStalePendings: SetError failed")
			continue
		}
		expired++
	}
	return expired, nil
}

// ListByStatus returns all intents currently in the given status, in
// arbitrary order.
func (st *Store) ListByStatus(status Status) ([]*BridgeIntent, error) {
	prefix := byStatusPrefix + string(status) + ":"
	keys, err := st.s.ListByPrefix(prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*BridgeIntent, 0, len(keys))
	for _, k := range keys {
		sid := strings.TrimPrefix(k, prefix)
		intent, err := st.Get(sid)
		if err != nil {
			log.WithError(err).WithField("sessionID", sid).Warn("Failed to load intent during ListByStatus")
			continue
		}
		if intent != nil {
			out = append(out, intent)
		}
	}
	return out, nil
}
