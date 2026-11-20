package analytics

import (
	"context"
	"database/sql"
	"encoding/hex"
	"sync"
	"time"
)

// OwnerMarkDays is how long a mark counts: a hit from a marked address is
// the owner's for this many days after the address was last seen signed in.
const OwnerMarkDays = 30

// A mark is one row of owner_visitor_marks: (user_id, ip_hash, last_seen).
// It says "this account was signed in from this address", written by the
// server when the owner's own browser loads the site list or an analytics
// read on the apex, and when a visitor signs in on a site with the account
// that owns it (handler/ownermarks.go). ip_hash is the same salted,
// truncated hash the ingester writes to site_visitor_hourly, so the two can
// be compared without ever storing an address.
//
// The ingester loads the marks newer than OwnerMarkDays once per run and
// files a page view as ClassOwner when it would have been ClassPerson, the
// site belongs to the marked account, and the hit's hash equals the mark.
// Owner views are left out of People everywhere People means class
// 'person'. Two caveats follow from the design and are stated to users:
// a visit from a device the owner never used while signed in still counts
// as a person, and other people behind the same address as a marked device
// are not counted while the mark lasts (30 days).
//
// cmd/analytics-rebuild replays old logs against today's marks, so a
// rebuild files past visits by the marks that exist now, not the ones that
// existed then.

// VisitorSalt returns hex(sha256(secret + "|visitor")), the salt the
// ingester derives from ADMIN_API_KEY when ANALYTICS_SALT is not set.
func VisitorSalt(secret string) string { return visitorSalt(secret) }

// HashIP returns sha256(salt + remoteAddr)[:16], byte-identical to the
// ip_hash the ingester writes for the same salt and address.
func HashIP(salt, remoteAddr string) []byte { return hashIP(salt, remoteAddr) }

// markThrottle is how long a (user, hash) pair is not written again after a
// write; markRecentMax bounds the throttle map (it is dropped whole beyond it).
const (
	markThrottle  = 10 * time.Minute
	markRecentMax = 10_000
	markTimeout   = 2 * time.Second
)

// OwnerMarks writes marks (see the package notes above). Safe for use from
// many requests at once.
type OwnerMarks struct {
	db   *sql.DB
	salt string

	mu     sync.Mutex
	recent map[string]time.Time // user_id+"|"+hex(ip_hash) -> last write
}

// NewOwnerMarks derives the salt exactly like
// NewIngester(db, "", saltSecret, "", "").WithSalt(explicitSalt): the explicit
// ANALYTICS_SALT when set, else VisitorSalt(saltSecret).
func NewOwnerMarks(database *sql.DB, saltSecret, explicitSalt string) *OwnerMarks {
	salt := visitorSalt(saltSecret)
	if explicitSalt != "" {
		salt = explicitSalt
	}
	return &OwnerMarks{db: database, salt: salt, recent: map[string]time.Time{}}
}

// Mark records that userID was seen signed in from remoteAddr. It does
// nothing when either is empty or the address is loopback, and nothing when
// the same (user, address) was written in the last markThrottle. Otherwise it
// upserts the row with a markTimeout deadline derived from ctx and returns
// the database error, if any; callers log it and never let it change a
// response.
func (m *OwnerMarks) Mark(ctx context.Context, userID, remoteAddr string) error {
	if m == nil || userID == "" || remoteAddr == "" || isLoopback(remoteAddr) {
		return nil
	}
	hash := hashIP(m.salt, remoteAddr)
	key := userID + "|" + hex.EncodeToString(hash)
	now := time.Now()

	m.mu.Lock()
	if last, ok := m.recent[key]; ok && now.Sub(last) < markThrottle {
		m.mu.Unlock()
		return nil
	}
	if len(m.recent) >= markRecentMax {
		m.recent = map[string]time.Time{}
	}
	m.recent[key] = now
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, markTimeout)
	defer cancel()
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO owner_visitor_marks (user_id, ip_hash, last_seen)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id, ip_hash) DO UPDATE SET last_seen = now()
	`, userID, hash)
	if err != nil {
		// Let the next request try again rather than wait out the throttle.
		m.mu.Lock()
		delete(m.recent, key)
		m.mu.Unlock()
	}
	return err
}
