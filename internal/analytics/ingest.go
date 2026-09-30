// Package analytics periodically ingests an nginx analytics access log into
// per-site hourly view/visitor aggregates, split by traffic class (person / bot
// / infra). Disabled unless ANALYTICS_LOG is set.
package analytics

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// maxLinesPerRun bounds a single ingest pass so a huge backlog cannot
	// monopolize the process. The next run continues from the mid-file offset.
	maxLinesPerRun = 200_000
	// visitorInsertChunk is the max multi-row VALUES size for site_visitor_hourly.
	visitorInsertChunk = 1000
	// runTimeout is the overall timeout for one runOnce (DB + file I/O).
	runTimeout = 60 * time.Second
)

// asset extensions whose last path segment disqualifies a URI as a "document".
var assetExt = map[string]struct{}{
	"css": {}, "js": {}, "mjs": {}, "png": {}, "jpg": {}, "jpeg": {},
	"gif": {}, "svg": {}, "ico": {}, "webp": {}, "avif": {},
	"woff": {}, "woff2": {}, "ttf": {}, "otf": {}, "eot": {},
	"map": {}, "json": {}, "xml": {}, "txt": {}, "pdf": {},
	"mp4": {}, "webm": {}, "mp3": {}, "wav": {}, "zip": {}, "wasm": {},
}

// Ingester tails an nginx analytics log and upserts daily aggregates.
type Ingester struct {
	db          *sql.DB
	logPath     string
	salt        string // visitor ip_hash salt, used verbatim (see visitorSalt)
	contentHost string
	siteDomain  string
	// alsoBases are other domains people's addresses live under
	// (SITE_BASE_DOMAIN while it moves from SITE_DOMAIN; WithBases).
	alsoBases []string
	// retentionDays: drop aggregate rows older than this (best-effort,
	// outside the main tx). ANALYTICS_RETENTION_DAYS, default 400.
	retentionDays int
	// pagesPerDay and refsPerDay cap the distinct paths and referring
	// domains kept per site per day (ANALYTICS_PAGES_PER_SITE_DAY,
	// ANALYTICS_REFERRERS_PER_SITE_DAY); views of further new ones that
	// day go to otherItem.
	pagesPerDay int
	refsPerDay  int
}

// otherItem is the row that takes a day's views of pages or referring
// domains beyond the per-site cap. It cannot collide with a real one: a path
// starts with "/" and a domain has no parentheses.
const otherItem = "(other)"

// NewIngester builds an ingester. saltSecret should be a stable server secret
// (ADMIN_API_KEY); contentHost and siteDomain drive host→site attribution.
func NewIngester(db *sql.DB, logPath, saltSecret, contentHost, siteDomain string) *Ingester {
	return &Ingester{
		db:            db,
		logPath:       logPath,
		salt:          visitorSalt(saltSecret),
		contentHost:   strings.ToLower(strings.TrimSpace(contentHost)),
		siteDomain:    strings.ToLower(strings.TrimSpace(siteDomain)),
		retentionDays: 400,
		pagesPerDay:   200,
		refsPerDay:    100,
	}
}

// WithRetentionDays sets how long aggregate rows are kept
// (ANALYTICS_RETENTION_DAYS). Zero or less keeps the default.
// WithItemCaps sets how many distinct pages and referring domains a site
// keeps per day (values <= 0 keep the defaults).
func (i *Ingester) WithItemCaps(pages, referrers int) *Ingester {
	if pages > 0 {
		i.pagesPerDay = pages
	}
	if referrers > 0 {
		i.refsPerDay = referrers
	}
	return i
}

// WithBases adds domains people's addresses also live under (SITE_BASE_DOMAIN
// while addresses move between it and SITE_DOMAIN): hits there attribute the
// same way, to the same sites, so charts stay continuous across the move.
func (i *Ingester) WithBases(bases ...string) *Ingester {
	for _, b := range bases {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == "" || b == i.siteDomain {
			continue
		}
		dup := false
		for _, x := range i.alsoBases {
			dup = dup || x == b
		}
		if !dup {
			i.alsoBases = append(i.alsoBases, b)
		}
	}
	return i
}

func (i *Ingester) WithRetentionDays(days int) *Ingester {
	if days > 0 {
		i.retentionDays = days
	}
	return i
}

// WithSalt replaces the salt derived from saltSecret with an explicit one
// (ANALYTICS_SALT). Empty keeps the derived salt. Passing the derived value
// itself, hex(sha256(saltSecret + "|visitor")), yields identical hashes.
func (i *Ingester) WithSalt(salt string) *Ingester {
	if salt != "" {
		i.salt = salt
	}
	return i
}

// Start launches the background ingest loop. A panic in any run is recovered
// and logged; the process is never crashed by the ingester.
func (i *Ingester) Start(interval time.Duration) {
	go func() {
		for {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("analytics ingest panic: %v", rec)
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
				defer cancel()
				if err := i.runOnce(ctx); err != nil {
					log.Printf("analytics ingest: %v", err)
				}
			}()
			time.Sleep(interval)
		}
	}()
}

// bucketKey identifies one aggregate row: a site, a UTC hour, and who was asking.
type bucketKey struct {
	siteID string
	hour   string // RFC3339 UTC, truncated to the hour
	class  Class
}

// geoKey identifies one row of site_geo_daily.
type geoKey struct {
	siteID  string
	day     string // YYYY-MM-DD UTC
	country string
	class   Class
}

// hit is one accepted log line. ip is held only long enough to resolve a
// country; it is never written to the database. path is the site-relative
// page and referrer the referring domain ("" when none, or the site's own
// address).
type hit struct {
	key      bucketKey
	ipHash   []byte
	ip       string
	path     string
	referrer string
}

// dayItemKey identifies one row of site_page_daily or site_referrer_daily.
type dayItemKey struct {
	siteID string
	day    string // YYYY-MM-DD UTC
	item   string // path or domain
}

type attrMaps struct {
	handleToUser map[string]string // handle -> user_id
	userNameToID map[string]string // user_id+"/"+name -> site_id
	nameToOldest map[string]string // name -> oldest site_id
	domainToID   map[string]string // lower(custom_domain) -> site_id
	families     map[string]familyAttr
}

// familyAttr is a verified address family: its owner and site-name prefix.
type familyAttr struct {
	userID, prefix string
}

// runOnce performs one ingest pass: read new log lines, attribute, aggregate,
// and commit view/visitor upserts + offset advance in a single transaction.
func (i *Ingester) runOnce(ctx context.Context) error {
	if i.logPath == "" {
		return nil
	}

	storedOffset, storedInode, storedAt, err := i.loadState(ctx)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	activeInfo, err := os.Stat(i.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // log not created yet; wait for next cycle
		}
		return fmt.Errorf("stat log: %w", err)
	}
	activeInode := fileInode(activeInfo)
	activeSize := activeInfo.Size()

	// Rotation (inode changed) or truncate (size < offset): drain .1 from the
	// stored offset (when inode changed), then read active from 0.
	// Else continue the active file from the stored offset.
	rotated := activeInode != storedInode || activeSize < storedOffset

	var (
		allLines   []string
		finalInode = activeInode
		finalOff   int64
		remaining  = maxLinesPerRun
	)

	if rotated {
		// Inode change → finish the file we were reading first (see
		// previousLog), then the active file from 0. Same-inode truncate has
		// no predecessor; skip straight to active@0.
		if activeInode != storedInode {
			if rotPath, rotInode, from, ok := i.previousLog(storedInode, storedOffset, storedAt); ok {
				lines, newOff, rerr := readLines(rotPath, from, remaining)
				if rerr != nil {
					return fmt.Errorf("read rotated: %w", rerr)
				}
				allLines = append(allLines, lines...)
				remaining -= len(lines)
				if remaining <= 0 {
					// Capped mid-rotated file: persist progress into it so the
					// next run continues (inode still != active → drain again).
					return i.processAndCommit(ctx, allLines, rotInode, newOff)
				}
			}
		}
		// Active file from the start.
		lines, newOff, rerr := readLines(i.logPath, 0, remaining)
		if rerr != nil {
			return fmt.Errorf("read active: %w", rerr)
		}
		allLines = append(allLines, lines...)
		finalInode = activeInode
		finalOff = newOff
	} else {
		lines, newOff, rerr := readLines(i.logPath, storedOffset, remaining)
		if rerr != nil {
			return fmt.Errorf("read active: %w", rerr)
		}
		allLines = append(allLines, lines...)
		finalInode = activeInode
		finalOff = newOff
	}

	// Nothing new and state already current — skip the write.
	if len(allLines) == 0 && finalInode == storedInode && finalOff == storedOffset {
		return nil
	}

	return i.processAndCommit(ctx, allLines, finalInode, finalOff)
}

// previousLog finds the file the ingester was reading before the active log
// was rotated away, and the offset to resume it from.
//
// logrotate (`create`, `delaycompress`) leaves it as plain `<log>.1`: resume
// at the stored offset when the inode matches, otherwise read it from 0
// (accept a small gap). Caddy has no `.1`; it renames the file to
// `access-<timestamp>-<reason>.log` and gzips it moments later, which changes
// the inode. So without a `.1`, take Caddy's newest roll: plain with a
// matching inode resumes at the stored offset; plain with another inode is
// read from 0 like `.1`.
//
// A gzip roll has lost the inode, so its identity is inferred from time
// (storedAt is when the position was last saved): the file we were reading
// was rolled after storedAt, and the roll before it was not. Only then does
// it resume at the stored offset (offsets count uncompressed bytes, which
// gzip preserves). Two or more rolls since storedAt mean the newest is not
// ours: it is read from 0 and the older ones are a gap. No roll since
// storedAt means the newest was already read before we moved on: there is
// no predecessor. Limitation: this compares file mtimes with the database
// clock, so a skew between the two larger than the time between a save and
// the next roll can pick the wrong case.
func (i *Ingester) previousLog(storedInode, storedOffset int64, storedAt time.Time) (path string, inode, from int64, ok bool) {
	candidate := i.logPath + ".1"
	var rolls []string
	if _, err := os.Stat(candidate); err != nil {
		rolls = caddyRolls(i.logPath)
		if len(rolls) == 0 {
			return "", 0, 0, false
		}
		candidate = rolls[len(rolls)-1]
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", 0, 0, false
	}
	inode = fileInode(info)
	switch {
	case inode == storedInode:
		from = storedOffset
	case strings.HasSuffix(candidate, ".gz"):
		since := 0
		for _, r := range rolls {
			if ri, err := os.Stat(r); err == nil && !ri.ModTime().Before(storedAt) {
				since++
			}
		}
		if since == 0 {
			return "", 0, 0, false
		}
		if since == 1 {
			from = storedOffset
		}
	}
	return candidate, inode, from, true
}

func (i *Ingester) processAndCommit(ctx context.Context, lines []string, inode, offset int64) error {
	return i.commitLines(ctx, lines, true, inode, offset)
}

// commitLines aggregates lines and upserts them in one transaction. With
// saveState it also advances the ingest position to (inode, offset) in that
// transaction; a rebuild replaying archives passes false, since archives are
// not a position the live loop can resume.
func (i *Ingester) commitLines(ctx context.Context, lines []string, saveState bool, inode, offset int64) error {
	maps, err := i.buildAttrMaps(ctx)
	if err != nil {
		return fmt.Errorf("attr maps: %w", err)
	}

	hits := make([]hit, 0, len(lines))
	ips := map[string]struct{}{}
	// Bytes and requests served, from every line (not only page views): the
	// whole log for the admin page's totals, per site where the host is one.
	total := map[trafficKey]trafficSum{}
	perSite := map[trafficKey]trafficSum{}
	for _, line := range lines {
		l, ok := parseLine(line)
		if !ok {
			continue
		}
		if k, siteID, ok := i.trafficOf(l, maps); ok {
			total[k] = total[k].add(l.bytes)
			if siteID != "" {
				sk := k
				sk.siteID = siteID
				perSite[sk] = perSite[sk].add(l.bytes)
			}
		}
		h, ok := i.viewHit(l, maps)
		if !ok {
			continue
		}
		hits = append(hits, h)
		ips[h.ip] = struct{}{}
	}

	// Resolved before the transaction opens: it is a read against reference data
	// and has nothing to do with the writes below.
	country, err := resolveCountries(ctx, i.db, ips)
	if err != nil {
		return fmt.Errorf("resolve countries: %w", err)
	}

	type visitor struct {
		hash    []byte
		country string
	}
	viewDelta := map[bucketKey]int64{}
	geoDelta := map[geoKey]int64{}
	// visitorSet[bucketKey][hex(ipHash)] = one distinct visitor
	visitorSet := map[bucketKey]map[string]visitor{}
	// (site, day) pairs whose per-country visitor counts need recomputing.
	touched := map[[2]string]struct{}{}
	// Top pages and referring domains: people only.
	pageDelta := map[dayItemKey]int64{}
	refDelta := map[dayItemKey]int64{}

	for _, h := range hits {
		cc := country[h.ip]
		day := h.key.hour[:len("2006-01-02")]
		viewDelta[h.key]++
		geoDelta[geoKey{siteID: h.key.siteID, day: day, country: cc, class: h.key.class}]++
		if visitorSet[h.key] == nil {
			visitorSet[h.key] = map[string]visitor{}
		}
		visitorSet[h.key][hex.EncodeToString(h.ipHash)] = visitor{hash: h.ipHash, country: cc}
		touched[[2]string{h.key.siteID, day}] = struct{}{}
		if h.key.class == ClassPerson {
			if h.path != "" {
				pageDelta[dayItemKey{h.key.siteID, day, h.path}]++
			}
			if h.referrer != "" {
				refDelta[dayItemKey{h.key.siteID, day, h.referrer}]++
			}
		}
	}

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// View upserts — one row per (site, hour, class) per run.
	for k, n := range viewDelta {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO site_view_hourly (site_id, hour, class, views)
			VALUES ($1, $2::timestamptz, $3, $4)
			ON CONFLICT (site_id, hour, class) DO UPDATE
			SET views = site_view_hourly.views + EXCLUDED.views
		`, k.siteID, k.hour, string(k.class), n)
		if err != nil {
			return fmt.Errorf("upsert views: %w", err)
		}
	}

	// Batched visitor inserts (ON CONFLICT DO NOTHING).
	type vrow struct {
		siteID  string
		hour    string
		class   string
		hash    []byte
		country string
	}
	var vrows []vrow
	for k, set := range visitorSet {
		for _, v := range set {
			vrows = append(vrows, vrow{siteID: k.siteID, hour: k.hour, class: string(k.class), hash: v.hash, country: v.country})
		}
	}
	for start := 0; start < len(vrows); start += visitorInsertChunk {
		end := start + visitorInsertChunk
		if end > len(vrows) {
			end = len(vrows)
		}
		chunk := vrows[start:end]
		var b strings.Builder
		b.WriteString(`INSERT INTO site_visitor_hourly (site_id, hour, class, ip_hash, country) VALUES `)
		args := make([]any, 0, len(chunk)*5)
		for i, r := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			base := i*5 + 1
			fmt.Fprintf(&b, "($%d,$%d::timestamptz,$%d,$%d,$%d)", base, base+1, base+2, base+3, base+4)
			args = append(args, r.siteID, r.hour, r.class, r.hash, r.country)
		}
		b.WriteString(` ON CONFLICT DO NOTHING`)
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return fmt.Errorf("insert visitors: %w", err)
		}
	}

	// Per-country views: additive, exactly like the hourly view upserts.
	for k, n := range geoDelta {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_geo_daily (site_id, day, country, class, views)
			VALUES ($1, $2::date, $3, $4, $5)
			ON CONFLICT (site_id, day, country, class) DO UPDATE
			SET views = site_geo_daily.views + EXCLUDED.views
		`, k.siteID, k.day, k.country, string(k.class), n); err != nil {
			return fmt.Errorf("upsert geo views: %w", err)
		}
	}

	if pageDelta, err = capDayItems(ctx, tx, pageDelta, `SELECT path FROM site_page_daily WHERE site_id = $1 AND day = $2::date`, i.pagesPerDay); err != nil {
		return fmt.Errorf("cap pages: %w", err)
	}
	if refDelta, err = capDayItems(ctx, tx, refDelta, `SELECT domain FROM site_referrer_daily WHERE site_id = $1 AND day = $2::date`, i.refsPerDay); err != nil {
		return fmt.Errorf("cap referrers: %w", err)
	}
	for k, n := range pageDelta {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_page_daily (site_id, day, path, views)
			VALUES ($1, $2::date, $3, $4)
			ON CONFLICT (site_id, day, path) DO UPDATE
			SET views = site_page_daily.views + EXCLUDED.views
		`, k.siteID, k.day, k.item, n); err != nil {
			return fmt.Errorf("upsert page views: %w", err)
		}
	}
	for k, n := range refDelta {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_referrer_daily (site_id, day, domain, views)
			VALUES ($1, $2::date, $3, $4)
			ON CONFLICT (site_id, day, domain) DO UPDATE
			SET views = site_referrer_daily.views + EXCLUDED.views
		`, k.siteID, k.day, k.item, n); err != nil {
			return fmt.Errorf("upsert referrer views: %w", err)
		}
	}

	// Per-country visitors: recounted from site_visitor_hourly for every day
	// this run touched, never incremented. A visitor seen again in a later run
	// is the same person, so adding would inflate the count; recounting is the
	// same COUNT(DISTINCT ip_hash) the rest of analytics uses.
	for sd := range touched {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_geo_daily (site_id, day, country, class, visitors)
			SELECT $1::uuid, $2::date, country, class, COUNT(DISTINCT ip_hash)
			FROM site_visitor_hourly
			WHERE site_id = $1::uuid
			  AND hour >= ($2::date)::timestamp AT TIME ZONE 'UTC'
			  AND hour <  ($2::date + 1)::timestamp AT TIME ZONE 'UTC'
			GROUP BY country, class
			ON CONFLICT (site_id, day, country, class) DO UPDATE
			SET visitors = EXCLUDED.visitors
		`, sd[0], sd[1]); err != nil {
			return fmt.Errorf("recount geo visitors: %w", err)
		}
	}

	// Traffic: additive like the view counts, and in the same transaction as
	// the offset so a line is never counted twice.
	for k, t := range total {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_daily (day, kind, bytes, requests)
			VALUES ($1::date, $2, $3, $4)
			ON CONFLICT (day, kind) DO UPDATE
			SET bytes = traffic_daily.bytes + EXCLUDED.bytes,
			    requests = traffic_daily.requests + EXCLUDED.requests
		`, k.day, k.kind, t.bytes, t.requests); err != nil {
			return fmt.Errorf("upsert traffic: %w", err)
		}
	}
	for k, t := range perSite {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO site_traffic_daily (site_id, day, kind, bytes, requests)
			VALUES ($1, $2::date, $3, $4, $5)
			ON CONFLICT (site_id, day, kind) DO UPDATE
			SET bytes = site_traffic_daily.bytes + EXCLUDED.bytes,
			    requests = site_traffic_daily.requests + EXCLUDED.requests
		`, k.siteID, k.day, k.kind, t.bytes, t.requests); err != nil {
			return fmt.Errorf("upsert site traffic: %w", err)
		}
	}

	// P0: offset advance is in the SAME transaction as the upserts.
	if saveState {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO analytics_ingest_state (logfile, offset_bytes, inode, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (logfile) DO UPDATE SET
				offset_bytes = EXCLUDED.offset_bytes,
				inode        = EXCLUDED.inode,
				updated_at   = now()
		`, i.logPath, offset, inode); err != nil {
			return fmt.Errorf("update ingest state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Best-effort prune outside the main tx (bounded retention).
	i.pruneOld(ctx)

	if n := len(viewDelta); n > 0 || len(lines) > 0 {
		var people, bots, infra int64
		for k, v := range viewDelta {
			switch k.class {
			case ClassPerson:
				people += v
			case ClassBot:
				bots += v
			case ClassInfra:
				infra += v
			}
		}
		log.Printf("analytics ingest: lines=%d buckets=%d people=%d bots=%d infra=%d offset=%d inode=%d",
			len(lines), n, people, bots, infra, offset, inode)
	}
	return nil
}

func (i *Ingester) pruneOld(ctx context.Context) {
	cutoff := time.Now().UTC().AddDate(0, 0, -i.retentionDays).Format(time.RFC3339)
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_view_hourly WHERE hour < $1::timestamptz`, cutoff); err != nil {
		log.Printf("analytics prune views: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_visitor_hourly WHERE hour < $1::timestamptz`, cutoff); err != nil {
		log.Printf("analytics prune visitors: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_geo_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune geo: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_page_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune pages: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_referrer_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune referrers: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_traffic_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune site traffic: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM traffic_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune traffic: %v", err)
	}
	// Legacy pre-split daily tables: no longer written, same retention.
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_view_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune daily views: %v", err)
	}
	if _, err := i.db.ExecContext(ctx,
		`DELETE FROM site_visitor_daily WHERE day < $1::date`, cutoff[:len("2006-01-02")]); err != nil {
		log.Printf("analytics prune daily visitors: %v", err)
	}
}

// loadState returns the saved position and when it was saved (zero time when
// there is none).
func (i *Ingester) loadState(ctx context.Context) (offset, inode int64, savedAt time.Time, err error) {
	err = i.db.QueryRowContext(ctx, `
		SELECT offset_bytes, inode, updated_at FROM analytics_ingest_state WHERE logfile = $1
	`, i.logPath).Scan(&offset, &inode, &savedAt)
	if err == sql.ErrNoRows {
		return 0, 0, time.Time{}, nil
	}
	return offset, inode, savedAt, err
}

func (i *Ingester) buildAttrMaps(ctx context.Context) (*attrMaps, error) {
	m := &attrMaps{
		handleToUser: map[string]string{},
		userNameToID: map[string]string{},
		nameToOldest: map[string]string{},
		domainToID:   map[string]string{},
		families:     map[string]familyAttr{},
	}

	// handle -> user_id
	hrows, err := i.db.QueryContext(ctx, `
		SELECT id, handle FROM users WHERE handle IS NOT NULL AND handle <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var id, handle string
		if err := hrows.Scan(&id, &handle); err != nil {
			return nil, err
		}
		m.handleToUser[handle] = id
	}
	if err := hrows.Err(); err != nil {
		return nil, err
	}

	// (user_id, name) -> site_id and custom_domain -> site_id
	srows, err := i.db.QueryContext(ctx, `
		SELECT id, user_id, name, custom_domain FROM sites
	`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var id, userID, name string
		var custom sql.NullString
		if err := srows.Scan(&id, &userID, &name, &custom); err != nil {
			return nil, err
		}
		m.userNameToID[userID+"/"+name] = id
		if custom.Valid && custom.String != "" {
			m.domainToID[strings.ToLower(custom.String)] = id
		}
	}
	if err := srows.Err(); err != nil {
		return nil, err
	}

	// Address families: <label>.<suffix> is the family owner's site
	// <prefix><label> (verified families only; handler/familyhost.go).
	frows, err := i.db.QueryContext(ctx, `
		SELECT suffix, user_id, site_prefix FROM address_families WHERE verified_at IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var fa familyAttr
		var suffix string
		if err := frows.Scan(&suffix, &fa.userID, &fa.prefix); err != nil {
			return nil, err
		}
		m.families[strings.ToLower(suffix)] = fa
	}
	if err := frows.Err(); err != nil {
		return nil, err
	}

	// name -> oldest site_id (legacy label.siteDomain)
	nrows, err := i.db.QueryContext(ctx, `
		SELECT DISTINCT ON (name) id, name
		FROM sites
		WHERE deleted_at IS NULL
		ORDER BY name, created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer nrows.Close()
	for nrows.Next() {
		var id, name string
		if err := nrows.Scan(&id, &name); err != nil {
			return nil, err
		}
		m.nameToOldest[name] = id
	}
	if err := nrows.Err(); err != nil {
		return nil, err
	}

	return m, nil
}

// logLine is one access-log record, normalised across the two formats this
// ingester understands. See parseTSV and parseCaddyJSON.
type logLine struct {
	tsStr      string
	ts         time.Time // set by parseCaddyJSON, which carries a numeric ts
	host       string
	status     string
	method     string
	uri        string
	remoteAddr string
	ua         string
	referrer   string // the referring host only (see referrerDomain)
	// bytes is what the response put on the wire, headers included (nginx
	// $bytes_sent, Caddy's size); 0 on lines written before it was logged.
	bytes int64
}

// parseTSV reads the nginx `shanalytics` format:
//
//	ts \t host \t status \t method \t request_uri \t remote_addr \t user_agent [\t referrer_host [\t bytes_sent]]
//
// The eighth field (the referring host, nothing else of the referrer) was
// added 2026-09-27; the ninth (bytes sent, for the admin page's traffic
// figures) 2026-09-30. Lines written before either still parse.
//
// This is what simple-host.app itself writes, and an analytics rebuild replays
// every retained archive of it (about 30 days, see
// deploy/prod/logrotate-analytics.conf), so it must keep parsing old lines
// exactly as before.
func parseTSV(line string) (logLine, bool) {
	fields := strings.Split(line, "\t")
	if len(fields) < 6 {
		return logLine{}, false
	}
	l := logLine{
		tsStr:      fields[0],
		host:       fields[1],
		status:     fields[2],
		method:     fields[3],
		uri:        fields[4],
		remoteAddr: fields[5],
	}
	// The user-agent is optional: lines written before the log format grew a
	// seventh field still parse, they just classify as bot (empty UA).
	if len(fields) >= 7 {
		l.ua = fields[6]
	}
	if len(fields) >= 8 {
		l.referrer = referrerDomain(fields[7])
	}
	if len(fields) >= 9 {
		if n, err := strconv.ParseInt(strings.TrimSpace(fields[8]), 10, 64); err == nil && n > 0 {
			l.bytes = n
		}
	}
	return l, true
}

// referrerDomain reduces a referrer to its host name, lowercased and without a
// port: the only part of a referrer analytics keeps. nginx already logs just
// the host; a full URL (Caddy's header, or anything unexpected) is cut down
// here, so no path or query is ever stored. "" for none or anything that is
// not a plain host name.
func referrerDomain(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "-" {
		return ""
	}
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if i := strings.LastIndexByte(v, '@'); i >= 0 {
		v = v[i+1:] // never keep userinfo
	}
	if h, _, ok := strings.Cut(v, ":"); ok {
		v = h
	}
	v = strings.TrimSuffix(strings.ToLower(v), ".")
	if v == "" || len(v) > 253 || !strings.Contains(v, ".") {
		return ""
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return ""
		}
	}
	return v
}

// maxPagePath bounds a stored page path, so a crawler inventing long URLs
// cannot bloat the table.
const maxPagePath = 200

// pagePath is how a page is stored for "top pages": the site-relative path
// without its query or fragment, percent-escapes decoded once, runs of "/"
// collapsed and dot segments resolved (a trailing slash is kept), with a
// trailing index.html dropped (/ and /index.html are the same page), cut to
// maxPagePath bytes. The raw request line has many spellings of one page
// (/%61bout/, //about/, /about/?x); each would otherwise be its own row.
// Case is kept: /About and /about can be different files.
func pagePath(p string) string {
	if q := strings.IndexAny(p, "?#"); q >= 0 {
		p = p[:q]
	}
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	if q := strings.IndexAny(p, "?#"); q >= 0 {
		p = p[:q] // an escaped ? or # is not part of the page either
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	slash := strings.HasSuffix(p, "/")
	p = path.Clean(p)
	if slash && p != "/" {
		p += "/"
	}
	if strings.HasSuffix(p, "/index.html") {
		p = strings.TrimSuffix(p, "index.html")
	}
	p = strings.ToValidUTF8(p, "")
	if len(p) > maxPagePath {
		p = strings.ToValidUTF8(p[:maxPagePath], "")
	}
	return p
}

// capDayItems bounds how many distinct pages (or referring domains) one site
// keeps per day. existing selects the items a (site, day) already has. An item
// already stored keeps counting; a new one takes a free slot while the site
// has fewer than max that day (the most viewed first), and otherwise its views
// go to otherItem. Without this, random paths or referrer spam would add a row
// per request.
func capDayItems(ctx context.Context, tx *sql.Tx, delta map[dayItemKey]int64, existing string, max int) (map[dayItemKey]int64, error) {
	if len(delta) == 0 {
		return delta, nil
	}
	type siteDay struct{ siteID, day string }
	groups := map[siteDay]map[string]int64{}
	for k, n := range delta {
		sd := siteDay{k.siteID, k.day}
		if groups[sd] == nil {
			groups[sd] = map[string]int64{}
		}
		groups[sd][k.item] += n
	}
	out := make(map[dayItemKey]int64, len(delta))
	for sd, items := range groups {
		rows, err := tx.QueryContext(ctx, existing, sd.siteID, sd.day)
		if err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for rows.Next() {
			var item string
			if err := rows.Scan(&item); err != nil {
				rows.Close()
				return nil, err
			}
			have[item] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for item, n := range capItems(have, items, max) {
			out[dayItemKey{sd.siteID, sd.day, item}] = n
		}
	}
	return out, nil
}

// capItems is capDayItems for one (site, day): have is what is stored,
// items this run's views. otherItem does not take a slot.
func capItems(have map[string]bool, items map[string]int64, max int) map[string]int64 {
	used := len(have)
	if have[otherItem] {
		used--
	}
	var fresh []string
	out := map[string]int64{}
	for item, n := range items {
		if have[item] || item == otherItem {
			out[item] += n
		} else {
			fresh = append(fresh, item)
		}
	}
	sort.Slice(fresh, func(a, b int) bool {
		if items[fresh[a]] != items[fresh[b]] {
			return items[fresh[a]] > items[fresh[b]]
		}
		return fresh[a] < fresh[b]
	})
	for _, item := range fresh {
		if used < max {
			out[item] += items[item]
			used++
		} else {
			out[otherItem] += items[item]
		}
	}
	return out
}

// caddyAccess is the subset of Caddy's JSON access log this needs.
//
// Event boxes run Caddy rather than nginx, because Caddy obtains certificates
// by itself. Caddy has no arbitrary log-format template in a stock build, so
// rather than maintain a custom Caddy image with a third-party encoder, the
// ingester learns Caddy's own shape. Every field it needs is already there.
type caddyAccess struct {
	TS      float64 `json:"ts"`
	Msg     string  `json:"msg"`
	Status  int     `json:"status"`
	Size    int64   `json:"size"` // response body bytes
	Request struct {
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		RemoteIP string              `json:"remote_ip"`
		ClientIP string              `json:"client_ip"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
}

func parseCaddyJSON(line string) (logLine, bool) {
	var a caddyAccess
	if err := json.Unmarshal([]byte(line), &a); err != nil {
		return logLine{}, false
	}
	// Caddy writes runtime and error records to the same stream shape. Only
	// access records describe a request.
	if a.Msg != "handled request" || a.Request.Host == "" {
		return logLine{}, false
	}
	// client_ip is remote_ip resolved through Caddy's trusted-proxy config, so
	// it is the right choice when one is in front. It is absent on older Caddy,
	// hence the fallback rather than a hard requirement.
	remote := a.Request.ClientIP
	if remote == "" {
		remote = a.Request.RemoteIP
	}
	ua, ref := "", ""
	// Header names arrive canonicalised, but a map lookup is cheap insurance
	// against a future change in casing.
	for k, v := range a.Request.Headers {
		if strings.EqualFold(k, "User-Agent") && len(v) > 0 {
			ua = v[0]
		}
		if strings.EqualFold(k, "Referer") && len(v) > 0 {
			ref = referrerDomain(v[0])
		}
	}
	sec, frac := math.Modf(a.TS)
	return logLine{
		ts:         time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(),
		host:       a.Request.Host,
		status:     strconv.Itoa(a.Status),
		method:     a.Request.Method,
		uri:        a.Request.URI,
		remoteAddr: remote,
		ua:         ua,
		referrer:   ref,
		bytes:      max(a.Size, 0),
	}, true
}

// parseLine reads one line of either format this ingester understands.
func parseLine(line string) (logLine, bool) {
	// A JSON object can only be Caddy; anything else is the nginx TSV. Cheap
	// discrimination, and neither format can be mistaken for the other.
	if strings.HasPrefix(strings.TrimSpace(line), "{") {
		return parseCaddyJSON(line)
	}
	return parseTSV(line)
}

// lineTime is when a line's request was served, in UTC.
func lineTime(l logLine) (time.Time, bool) {
	if !l.ts.IsZero() {
		return l.ts.UTC(), true
	}
	ts, err := time.Parse(time.RFC3339, l.tsStr)
	if err != nil {
		// nginx $time_iso8601 sometimes uses +00:00 which RFC3339 accepts;
		// also try without timezone colon variants.
		ts, err = time.Parse("2006-01-02T15:04:05-07:00", l.tsStr)
		if err != nil {
			return time.Time{}, false
		}
	}
	return ts.UTC(), true
}

// Traffic kinds: a site's files (pages, images, scripts) and its /v1 API
// (saved data, visitor sign-in).
const (
	TrafficPages = "pages"
	TrafficAPI   = "api"
)

// trafficKey is one row of traffic_daily (siteID "") or site_traffic_daily.
type trafficKey struct {
	siteID string
	day    string // YYYY-MM-DD UTC
	kind   string // TrafficPages or TrafficAPI
}

type trafficSum struct{ bytes, requests int64 }

func (t trafficSum) add(b int64) trafficSum { return trafficSum{t.bytes + b, t.requests + 1} }

// trafficOf is the traffic row a line adds to, and the site it was for ("" when
// its host is not a site's). Every request counts, whatever its method or
// status: a 404 still went out over the network.
func (i *Ingester) trafficOf(l logLine, maps *attrMaps) (trafficKey, string, bool) {
	ts, ok := lineTime(l)
	if !ok {
		return trafficKey{}, "", false
	}
	host := strings.ToLower(strings.TrimSpace(l.host))
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	kind := TrafficPages
	if p, _, _ := strings.Cut(l.uri, "?"); p == "/v1" || strings.HasPrefix(p, "/v1/") {
		kind = TrafficAPI
	}
	siteID, _ := i.attribute(host, l.uri, maps)
	return trafficKey{day: ts.Format("2006-01-02"), kind: kind}, siteID, true
}

// parseAndAttribute fails soft: unparseable line, bad ts, non-document, or
// unresolved host all return ok=false (line skipped).
func (i *Ingester) parseAndAttribute(line string, maps *attrMaps) (hit, bool) {
	l, ok := parseLine(line)
	if !ok {
		return hit{}, false
	}
	return i.viewHit(l, maps)
}

// viewHit is the page view a parsed line counts as, if any.
func (i *Ingester) viewHit(l logLine, maps *attrMaps) (hit, bool) {

	host := strings.ToLower(strings.TrimSpace(l.host))
	status := l.status
	method := l.method
	uri := l.uri
	remoteAddr := l.remoteAddr
	ua := l.ua

	// Strip optional port from host.
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}

	if method != "GET" {
		return hit{}, false
	}
	// Only successful document responses count as a view, for every class alike.
	// This means a scanner spraying 404s at /wp-login.php never reaches the bot
	// column -- it did not view anything. Bot counts are therefore "bots that
	// actually loaded a page", which is the comparable number next to people.
	if status != "200" && status != "304" {
		return hit{}, false
	}
	if !isDocumentURI(uri) {
		return hit{}, false
	}

	ts, ok := lineTime(l)
	if !ok {
		return hit{}, false
	}

	siteID, sitePath := i.attribute(host, uri, maps)
	if siteID == "" {
		return hit{}, false
	}
	// A link from the site's own address is moving around the site, not
	// arriving from somewhere.
	ref := l.referrer
	if ref == host {
		ref = ""
	}

	return hit{
		key: bucketKey{
			siteID: siteID,
			hour:   ts.Truncate(time.Hour).Format(time.RFC3339),
			class:  Classify(remoteAddr, ua, uri),
		},
		ipHash:   hashIP(i.salt, remoteAddr),
		ip:       remoteAddr,
		path:     pagePath(sitePath),
		referrer: ref,
	}, true
}

// attribute names the site a request was for, and the page's path within
// that site (the address prefix a content-host or person-host URL carries is
// dropped). "" when the host is not a site's.
func (i *Ingester) attribute(host, uri string, maps *attrMaps) (string, string) {
	path := uri
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	// content host: /<handle>/<site>/...
	if host == i.contentHost {
		segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
		// filter empty segments
		clean := segs[:0]
		for _, s := range segs {
			if s != "" {
				clean = append(clean, s)
			}
		}
		if len(clean) < 2 {
			return "", ""
		}
		handle, siteName := clean[0], clean[1]
		userID, ok := maps.handleToUser[handle]
		if !ok {
			return "", ""
		}
		return maps.userNameToID[userID+"/"+siteName], trimPathPrefix(path, 2)
	}

	// <label>.<siteDomain>: a claimed site address first (it is a domain like
	// any other), then a person host (<handle>.<siteDomain>/<site>/...), then
	// the retired per-name host. The same under every other base.
	for _, base := range append([]string{i.siteDomain}, i.alsoBases...) {
		suffix := "." + base
		if !strings.HasSuffix(host, suffix) {
			continue
		}
		label := strings.TrimSuffix(host, suffix)
		// single label only (no dots)
		if label != "" && !strings.Contains(label, ".") {
			if id := maps.domainToID[host]; id != "" {
				return id, path
			}
			// A claimed name stored under another base.
			for _, b := range append([]string{i.siteDomain}, i.alsoBases...) {
				if id := maps.domainToID[label+"."+b]; b != base && id != "" {
					return id, path
				}
			}
			if userID, ok := maps.handleToUser[label]; ok {
				seg, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
				if seg == "" {
					return "", ""
				}
				return maps.userNameToID[userID+"/"+seg], trimPathPrefix(path, 1)
			}
			return maps.nameToOldest[label], path
		}
		// <site>.<handle>.<siteDomain>: a site's own host (owner decision
		// 2026-09-26). The whole host names the site; the path does not.
		if site, handle, ok := strings.Cut(label, "."); ok && site != "" && !strings.Contains(handle, ".") {
			if userID, ok := maps.handleToUser[handle]; ok {
				return maps.userNameToID[userID+"/"+site], path
			}
			return "", ""
		}
		break
	}

	// custom domain (an exact one wins over a family it is under)
	if id := maps.domainToID[host]; id != "" {
		return id, path
	}
	// an address family's <label>.<suffix>
	if label, suffix, ok := strings.Cut(host, "."); ok && label != "" {
		if fa, ok := maps.families[suffix]; ok {
			return maps.userNameToID[fa.userID+"/"+fa.prefix+label], path
		}
	}
	return "", path
}

// trimPathPrefix drops the first n non-empty segments of path (the handle and
// site name in an address path), keeping the rest as the site-relative path.
func trimPathPrefix(path string, n int) string {
	rest := strings.TrimLeft(path, "/")
	for ; n > 0; n-- {
		_, after, found := strings.Cut(rest, "/")
		if !found {
			return "/"
		}
		rest = strings.TrimLeft(after, "/")
	}
	return "/" + rest
}

// isDocumentURI keeps only HTML-ish document requests; strips query for path checks.
func isDocumentURI(uri string) bool {
	path := uri
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	if path == "" {
		path = "/"
	}

	// Exclude API / internal / ACME paths.
	if strings.HasPrefix(path, "/v1/") || path == "/v1" ||
		strings.HasPrefix(path, "/internal/") || path == "/internal" ||
		strings.HasPrefix(path, "/.well-known/") || path == "/.well-known" {
		return false
	}

	// Last path segment (may be empty when path ends in /).
	last := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}

	// Asset extension on the last segment → not a document.
	if last != "" {
		if dot := strings.LastIndexByte(last, '.'); dot >= 0 && dot < len(last)-1 {
			ext := strings.ToLower(last[dot+1:])
			if _, isAsset := assetExt[ext]; isAsset {
				return false
			}
		}
	}

	lower := strings.ToLower(path)
	if strings.HasSuffix(path, "/") || strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm") {
		return true
	}
	// No "." in last segment → treat as extensionless document route.
	if !strings.Contains(last, ".") {
		return true
	}
	return false
}

// visitorSalt returns hex(sha256(secret + "|visitor")) — stable for the life of
// the server secret.
//
// This deliberately does NOT rotate per day. A per-day salt makes a visitor's
// hash unlinkable across days, which also makes counting unique visitors
// impossible: the same person browsing on Monday and Tuesday looks like two
// people, so any range total is really "sum of daily uniques" and always
// overstates the audience. A stable salt is what makes "unique visitors over
// the last 30 days" a real number.
//
// Raw IPs are still never stored; the hash is truncated to 16 bytes and salted
// with a secret that never leaves the server.
func visitorSalt(secret string) string {
	sum := sha256.Sum256([]byte(secret + "|visitor"))
	return hex.EncodeToString(sum[:])
}

// hashIP returns sha256(salt + remoteAddr)[:16], salt being visitorSalt's
// output or an explicit ANALYTICS_SALT.
func hashIP(salt, remoteAddr string) []byte {
	sum := sha256.Sum256([]byte(salt + remoteAddr))
	out := make([]byte, 16)
	copy(out, sum[:16])
	return out
}

// readLines reads up to maxLines complete newline-terminated lines starting at
// fromOffset. An incomplete trailing line (no newline yet) is left unconsumed
// so the next run can pick it up once the writer finishes the line.
// newOffset is the absolute file offset after the last fully consumed line.
func readLines(path string, fromOffset int64, maxLines int) (lines []string, newOffset int64, err error) {
	if maxLines <= 0 {
		return nil, fromOffset, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fromOffset, nil
		}
		return nil, fromOffset, err
	}
	defer f.Close()

	var src io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		// A rolled archive: offsets count uncompressed bytes, so skip forward
		// through the decompressed stream instead of seeking.
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fromOffset, err
		}
		defer zr.Close()
		if n, err := io.CopyN(io.Discard, zr, fromOffset); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, fromOffset, nil // shorter than the offset: nothing new
			}
			return nil, fromOffset + n, err
		}
		src = zr
	} else {
		info, err := f.Stat()
		if err != nil {
			return nil, fromOffset, err
		}
		size := info.Size()
		if fromOffset > size {
			// Truncation already handled by caller; defensive clamp.
			fromOffset = 0
		}
		if fromOffset == size {
			return nil, fromOffset, nil
		}
		if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
			return nil, fromOffset, err
		}
	}

	lines, n, err := readChunk(bufio.NewReaderSize(src, 256*1024), maxLines)
	return lines, fromOffset + n, err
}

// readChunk reads up to maxLines complete lines from r and returns them with
// the number of bytes they occupied. An incomplete trailing line is left
// unread (and uncounted).
func readChunk(r *bufio.Reader, maxLines int) (lines []string, consumed int64, err error) {
	for len(lines) < maxLines {
		line, rerr := r.ReadString('\n')
		if len(line) == 0 && rerr != nil {
			if rerr == io.EOF {
				break
			}
			return lines, consumed, rerr
		}
		// Incomplete last line (EOF without newline): do not advance past it.
		if rerr == io.EOF && !strings.HasSuffix(line, "\n") {
			break
		}
		consumed += int64(len(line))
		lines = append(lines, strings.TrimRight(line, "\r\n"))
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return lines, consumed, rerr
		}
	}
	return lines, consumed, nil
}

func fileInode(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Ino)
	}
	return 0
}
