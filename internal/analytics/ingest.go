// Package analytics periodically ingests an nginx analytics access log into
// per-site hourly view/visitor aggregates, split by traffic class (person / bot
// / infra). Disabled unless ANALYTICS_LOG is set.
package analytics

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
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
	// pruneRetentionDays: drop aggregate rows older than this (best-effort, outside main tx).
	pruneRetentionDays = 400
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
	saltSecret  string
	contentHost string
	siteDomain  string
}

// NewIngester builds an ingester. saltSecret should be a stable server secret
// (ADMIN_API_KEY); contentHost and siteDomain drive host→site attribution.
func NewIngester(db *sql.DB, logPath, saltSecret, contentHost, siteDomain string) *Ingester {
	return &Ingester{
		db:          db,
		logPath:     logPath,
		saltSecret:  saltSecret,
		contentHost: strings.ToLower(strings.TrimSpace(contentHost)),
		siteDomain:  strings.ToLower(strings.TrimSpace(siteDomain)),
	}
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
// country; it is never written to the database.
type hit struct {
	key    bucketKey
	ipHash []byte
	ip     string
}

type attrMaps struct {
	handleToUser map[string]string // handle -> user_id
	userNameToID map[string]string // user_id+"/"+name -> site_id
	nameToOldest map[string]string // name -> oldest site_id
	domainToID   map[string]string // lower(custom_domain) -> site_id
}

// runOnce performs one ingest pass: read new log lines, attribute, aggregate,
// and commit view/visitor upserts + offset advance in a single transaction.
func (i *Ingester) runOnce(ctx context.Context) error {
	if i.logPath == "" {
		return nil
	}

	storedOffset, storedInode, err := i.loadState(ctx)
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
		// Inode change → old file should be logPath+".1" (logrotate create, not
		// copytruncate). Same-inode truncate has no .1; skip straight to active@0.
		if activeInode != storedInode {
			rotPath := i.logPath + ".1"
			if rotInfo, rerr := os.Stat(rotPath); rerr == nil {
				rotInode := fileInode(rotInfo)
				// Resume from stored offset when .1 is the file we were reading
				// (inode matches). Otherwise start .1 from 0 (accept small gap).
				from := int64(0)
				if rotInode == storedInode {
					from = storedOffset
				}
				lines, newOff, rerr := readLines(rotPath, from, remaining)
				if rerr != nil {
					return fmt.Errorf("read rotated: %w", rerr)
				}
				allLines = append(allLines, lines...)
				remaining -= len(lines)
				if remaining <= 0 {
					// Capped mid-rotated file: persist progress into .1 so the
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

func (i *Ingester) processAndCommit(ctx context.Context, lines []string, inode, offset int64) error {
	maps, err := i.buildAttrMaps(ctx)
	if err != nil {
		return fmt.Errorf("attr maps: %w", err)
	}

	hits := make([]hit, 0, len(lines))
	ips := map[string]struct{}{}
	for _, line := range lines {
		h, ok := i.parseAndAttribute(line, maps)
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

	// P0: offset advance is in the SAME transaction as the upserts.
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
	cutoff := time.Now().UTC().AddDate(0, 0, -pruneRetentionDays).Format(time.RFC3339)
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
}

func (i *Ingester) loadState(ctx context.Context) (offset, inode int64, err error) {
	err = i.db.QueryRowContext(ctx, `
		SELECT offset_bytes, inode FROM analytics_ingest_state WHERE logfile = $1
	`, i.logPath).Scan(&offset, &inode)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return offset, inode, err
}

func (i *Ingester) buildAttrMaps(ctx context.Context) (*attrMaps, error) {
	m := &attrMaps{
		handleToUser: map[string]string{},
		userNameToID: map[string]string{},
		nameToOldest: map[string]string{},
		domainToID:   map[string]string{},
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

	// name -> oldest site_id (legacy label.siteDomain)
	nrows, err := i.db.QueryContext(ctx, `
		SELECT DISTINCT ON (name) id, name
		FROM sites
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

// parseAndAttribute fails soft: wrong field count, bad ts, non-document, or
// unresolved host all return ok=false (line skipped).
func (i *Ingester) parseAndAttribute(line string, maps *attrMaps) (hit, bool) {
	// Format: ts \t host \t status \t method \t uri \t remote_addr \t user_agent
	fields := strings.Split(line, "\t")
	if len(fields) < 6 {
		return hit{}, false
	}
	tsStr := fields[0]
	host := strings.ToLower(strings.TrimSpace(fields[1]))
	status := fields[2]
	method := fields[3]
	uri := fields[4]
	remoteAddr := fields[5]
	// The user-agent is optional: lines written before the log format grew a
	// seventh field still parse, they just classify as bot (empty UA).
	ua := ""
	if len(fields) >= 7 {
		ua = fields[6]
	}

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

	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		// nginx $time_iso8601 sometimes uses +00:00 which RFC3339 accepts;
		// also try without timezone colon variants.
		ts, err = time.Parse("2006-01-02T15:04:05-07:00", tsStr)
		if err != nil {
			return hit{}, false
		}
	}
	ts = ts.UTC()

	siteID := i.attribute(host, uri, maps)
	if siteID == "" {
		return hit{}, false
	}

	return hit{
		key: bucketKey{
			siteID: siteID,
			hour:   ts.Truncate(time.Hour).Format(time.RFC3339),
			class:  Classify(remoteAddr, ua, uri),
		},
		ipHash: hashIP(i.saltSecret, remoteAddr),
		ip:     remoteAddr,
	}, true
}

func (i *Ingester) attribute(host, uri string, maps *attrMaps) string {
	// content host: /<handle>/<site>/...
	if host == i.contentHost {
		path := uri
		if q := strings.IndexByte(path, '?'); q >= 0 {
			path = path[:q]
		}
		path = strings.TrimPrefix(path, "/")
		// drop trailing empty from trailing slash
		segs := strings.Split(path, "/")
		// filter empty segments
		clean := segs[:0]
		for _, s := range segs {
			if s != "" {
				clean = append(clean, s)
			}
		}
		if len(clean) < 2 {
			return ""
		}
		handle, siteName := clean[0], clean[1]
		userID, ok := maps.handleToUser[handle]
		if !ok {
			return ""
		}
		return maps.userNameToID[userID+"/"+siteName]
	}

	// legacy: <label>.<siteDomain>
	suffix := "." + i.siteDomain
	if strings.HasSuffix(host, suffix) {
		label := strings.TrimSuffix(host, suffix)
		// single label only (no dots)
		if label != "" && !strings.Contains(label, ".") {
			return maps.nameToOldest[label]
		}
	}

	// custom domain
	return maps.domainToID[host]
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

// hashIP returns sha256(visitorSalt + remoteAddr)[:16].
func hashIP(secret, remoteAddr string) []byte {
	sum := sha256.Sum256([]byte(visitorSalt(secret) + remoteAddr))
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

	r := bufio.NewReaderSize(f, 256*1024)
	offset := fromOffset
	for len(lines) < maxLines {
		line, rerr := r.ReadString('\n')
		if len(line) == 0 && rerr != nil {
			if rerr == io.EOF {
				break
			}
			return lines, offset, rerr
		}
		// Incomplete last line (EOF without newline): do not advance past it.
		if rerr == io.EOF && !strings.HasSuffix(line, "\n") {
			break
		}
		offset += int64(len(line))
		lines = append(lines, strings.TrimRight(line, "\r\n"))
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return lines, offset, rerr
		}
	}
	return lines, offset, nil
}

func fileInode(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Ino)
	}
	return 0
}
