// Package limits is the one table of limits, the same for everyone. The
// operator's own account has none. Every refusal names the limit it hit.
package limits

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"simplehost/core/web"
)

// Limit is one row of the table. A rate limit allows N per Per. A size limit
// allows N bytes (Per is zero). Fix is what the refusal tells the person.
type Limit struct {
	Name string
	N    int64
	Per  time.Duration
	Fix  string
}

// The table. Change a number here and it changes for everyone.
var (
	Requests    = Limit{"requests", 600, time.Minute, "Too many requests from you. Wait a minute and try again."}
	SignInCodes = Limit{"sign_in_codes", 5, 15 * time.Minute, "Too many codes asked for this email. Wait 15 minutes."}
	CodeMails   = Limit{"code_mails", 30, time.Hour, "Too many sign-in codes sent from your address. Wait an hour."}
	ReviewTries = Limit{"review_tries", 10, 15 * time.Minute, "Too many sign-in tries. Wait 15 minutes."}
	// ReviewFails counts only wrong reviewer passwords, from everyone, so
	// guessing from many addresses is slow without locking the reviewer out.
	ReviewFails   = Limit{"review_fails", 50, 15 * time.Minute, "Too many sign-in tries. Wait 15 minutes."}
	PasscodeTries = Limit{"passcode_tries", 5, 15 * time.Minute, "Too many wrong passcodes. Wait 15 minutes and try again."}
	Reports       = Limit{"reports", 10, time.Hour, "You sent a few reports already. The operator will read them; wait an hour to send more."}
	AccessAsks    = Limit{"access_asks", 3, time.Hour, "You asked for access a few times already. The owner has been told; wait for them."}
	SiteBytes     = Limit{"site_size", 100 << 20, 0, "A site can hold 100 MB of published files. Remove or shrink some files."}
	DataBytes     = Limit{"data_size", 100 << 20, 0, "A site's saved data can hold 100 MB. Delete rows or values you no longer need."}
	FilesBytes    = Limit{"files_size", 200 << 20, 0, "A site's file stores can hold 200 MB in all. Delete files you no longer need."}
	// SandboxMemory is the fixed memory ceiling of one open site database
	// (its sandbox, answer included). DatabaseMemory is all the memory given
	// to databases: DatabaseMemory / SandboxMemory sandboxes are open at once.
	SandboxMemory  = Limit{"sandbox_memory", 32 << 20, 0, "This needs more than the 32 MB a site's database may use at once. Ask for fewer rows or smaller values."}
	DatabaseMemory = Limit{"database_memory", 1 << 30, 0, "The server is busy with other sites' data. Try again in a few seconds."}
	// Publishes is how many publishes run at once (one per person). Past
	// that: busy, try again.
	Publishes = Limit{"busy_publishes", 3, 0, "The server is taking other publishes. Try again in a few seconds."}
	// PushDevices is how many browsers one person gets notifications on.
	PushDevices = Limit{"push_devices", 20, 0, "You have 20 devices for notifications. Remove one in the dashboard first."}
	// KeptVersions is how many published versions are kept per site.
	KeptVersions = Limit{"kept_versions", 4, 0, ""}
	// QueryTime is the longest one saved-data query may run. It is a guard
	// on the database, so it applies to every caller.
	QueryTime = Limit{"query_time", 0, 2 * time.Second, "The query ran longer than 2 seconds. Add a WHERE or LIMIT, or an index."}
)

// Allow counts one use of a rate limit for key, and refuses when the window
// is full. An empty key means the caller, or their address when nobody is
// signed in. The operator is never limited.
func Allow(r *http.Request, l Limit, key string) error {
	c := web.CallerOf(r)
	if c.Operator {
		return nil
	}
	if key == "" {
		key = c.ID
		if key == "" {
			key = "ip:" + Address(r)
		}
	}
	wait := counters.take(l, key, time.Now())
	if wait == 0 {
		return nil
	}
	ref := web.Refuse(http.StatusTooManyRequests, "limit_"+l.Name, l.Fix)
	ref.RetryAfter = int(wait/time.Second) + 1
	return ref
}

// Full refuses like Allow when the window for key is full, but counts nothing.
func Full(r *http.Request, l Limit, key string) error {
	if web.CallerOf(r).Operator {
		return nil
	}
	wait := counters.left(l, key, time.Now())
	if wait == 0 {
		return nil
	}
	ref := web.Refuse(http.StatusTooManyRequests, "limit_"+l.Name, l.Fix)
	ref.RetryAfter = int(wait/time.Second) + 1
	return ref
}

// Enter takes one of a fixed number of slots for heavy work (Publishes): l.N across the service and one per person (or address).
// Call the returned func when done. Full: a plain "busy, try again".
func Enter(r *http.Request, l Limit) (func(), error) {
	c := web.CallerOf(r)
	who := l.Name + "\x00" + c.ID
	if c.ID == "" {
		who = l.Name + "\x00ip:" + Address(r)
	}
	busy.Lock()
	defer busy.Unlock()
	if busy.n[l.Name] >= l.N || busy.n[who] >= 1 {
		ref := web.Refuse(http.StatusServiceUnavailable, "limit_"+l.Name, l.Fix)
		ref.RetryAfter = 5
		return nil, ref
	}
	busy.n[l.Name]++
	busy.n[who]++
	return func() {
		busy.Lock()
		defer busy.Unlock()
		for _, k := range []string{l.Name, who} {
			if busy.n[k]--; busy.n[k] <= 0 {
				delete(busy.n, k)
			}
		}
	}, nil
}

var busy = struct {
	sync.Mutex
	n map[string]int64
}{n: map[string]int64{}}

// Address is the caller's address as limits count it: an IPv4 address, or
// the /64 an IPv6 address is in (one home or server holds a whole /64, so
// counting single addresses would let it rotate past every limit).
func Address(r *http.Request) string {
	ip := net.ParseIP(web.ClientIP(r))
	if ip == nil || ip.To4() != nil {
		return web.ClientIP(r)
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// Fits refuses when total bytes would exceed a size limit. The operator is
// never limited.
func Fits(r *http.Request, l Limit, total int64) error {
	if total <= l.N || web.CallerOf(r).Operator {
		return nil
	}
	return web.Refuse(http.StatusRequestEntityTooLarge, "limit_"+l.Name, l.Fix+" (limit "+strconv.FormatInt(l.N>>20, 10)+" MB)")
}

// window counts uses of one limit by one key since start.
type window struct {
	start time.Time
	n     int64
}

type table struct {
	mu    sync.Mutex
	m     map[string]*window
	swept time.Time
}

var counters = table{m: map[string]*window{}}

// take records a use and returns zero, or how long until the window opens.
func (t *table) take(l Limit, key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(t.swept) > time.Hour {
		for k, w := range t.m {
			if now.Sub(w.start) > time.Hour {
				delete(t.m, k)
			}
		}
		t.swept = now
	}
	k := l.Name + "\x00" + key
	w := t.m[k]
	if w == nil || now.Sub(w.start) >= l.Per {
		w = &window{start: now}
		t.m[k] = w
	}
	if w.n >= l.N {
		return w.start.Add(l.Per).Sub(now)
	}
	w.n++
	return 0
}

// left is how long until a full window opens, or zero when there is room.
func (t *table) left(l Limit, key string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.m[l.Name+"\x00"+key]
	if w == nil || now.Sub(w.start) >= l.Per || w.n < l.N {
		return 0
	}
	return w.start.Add(l.Per).Sub(now)
}
