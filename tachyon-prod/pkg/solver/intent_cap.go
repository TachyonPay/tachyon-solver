package solver

import (
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
	"github.com/zk-tachyon/tachyon-prod/pkg/storage"
)

// intentCapKeyPrefix namespaces our per-user daily volume entries in PebbleDB.
// Layout: "user-cap:<UTC-day>:<user-address>" → decimal-string cents.
const intentCapKeyPrefix = "user-cap:"

// intentCapTracker enforces a per-user daily USD-value ceiling on intents the
// solver will bid on. Volume is tallied in USD cents (int64) so we get
// deterministic arithmetic and easy persistence. Each intent's USD value is
// `amountA / 10^decimalsA * priceA` and is computed by the caller via the
// price oracle before invoking Allow / Record.
//
// State is persisted to PebbleDB so a solver restart in the middle of a day
// does NOT reset user counters. Day boundary is calendar UTC.
type intentCapTracker struct {
	capCents int64          // dollar cap × 100; 0 = disabled
	store    *storage.Store // shared with the solver
	mu       sync.Mutex
}

func newIntentCapTracker(capCents int64, store *storage.Store) *intentCapTracker {
	return &intentCapTracker{capCents: capCents, store: store}
}

// Allow returns true if the user can absorb an intent worth intentCents today
// without breaching the cap. When the tracker is disabled (cap ≤ 0) it always
// returns true.
func (t *intentCapTracker) Allow(user common.Address, intentCents int64) bool {
	if t == nil || t.capCents <= 0 {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.read(user)
	return current+intentCents <= t.capCents
}

// Record adds intentCents to user's daily total. Call after auction is won.
func (t *intentCapTracker) Record(user common.Address, intentCents int64) {
	if t == nil || t.capCents <= 0 || intentCents <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.read(user)
	if err := t.store.Put(t.key(user), []byte(strconv.FormatInt(current+intentCents, 10))); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user":   user.Hex(),
			"cents":  intentCents,
		}).Warn("Failed to persist user daily cap counter")
	}
}

// Used returns the user's current daily total in cents.
func (t *intentCapTracker) Used(user common.Address) int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.read(user)
}

// CapCents returns the configured ceiling (0 = disabled).
func (t *intentCapTracker) CapCents() int64 {
	if t == nil {
		return 0
	}
	return t.capCents
}

// read returns the user's volume for today. Returns 0 if none recorded.
// Note that we don't actively delete previous-day keys — they're cheap and
// implicitly become invisible because we key by today's date.
func (t *intentCapTracker) read(user common.Address) int64 {
	val, err := t.store.Get(t.key(user))
	if err != nil || val == nil {
		return 0
	}
	out, err := strconv.ParseInt(string(val), 10, 64)
	if err != nil {
		return 0
	}
	return out
}

func (t *intentCapTracker) key(user common.Address) string {
	return intentCapKeyPrefix + dayKey() + ":" + user.Hex()
}

// dayKey returns YYYY-MM-DD in UTC.
func dayKey() string {
	return time.Now().UTC().Format("2006-01-02")
}
