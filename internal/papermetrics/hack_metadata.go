package papermetrics

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"log"
	"time"
)

const syntheticSQL = `(e.slug LIKE 'e2e-%' OR e.slug LIKE 'mock-%' OR e.slug LIKE 'mock2-%' OR e.slug LIKE 'local-%' OR COALESCE(u.username LIKE 'e2e-%',false))`

func (c *Collector) ensureHackKeys(ctx context.Context) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("ensureHackKeys", resultErr)
		}
	}()
	run, err := c.ensureRun(ctx)
	if err != nil {
		return err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.id::text,`+syntheticSQL+` FROM events e LEFT JOIN users u ON u.id=e.created_by WHERE NOT EXISTS (SELECT 1 FROM paper_hack_key k WHERE k.event_id=e.id)`)
	if err != nil {
		return err
	}
	type missing struct {
		id        string
		synthetic bool
	}
	var items []missing
	for rows.Next() {
		var item missing
		if err := rows.Scan(&item.id, &item.synthetic); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	if rows.Err() != nil {
		c.tracker.Fail("read Hack metadata", rows.Err())
		return rows.Err()
	}
	for _, item := range items {
		for attempts := 0; attempts < 4; attempts++ {
			var bytes [8]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return err
			}
			key := int64(binary.BigEndian.Uint64(bytes[:]))
			_, err = c.execMetric(ctx, c.db, nil, `INSERT INTO paper_hack_key (event_id,event_key,synthetic,run_id) VALUES ($1,$2,$3,$4) ON CONFLICT (event_id) DO NOTHING`, item.id, key, item.synthetic, run)
			if err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	_, err = c.execMetric(ctx, c.db, nil, `UPDATE paper_hack_key k SET synthetic=true FROM events e LEFT JOIN users u ON u.id=e.created_by WHERE k.event_id=e.id AND NOT k.synthetic AND `+syntheticSQL)
	return err
}

func (c *Collector) refreshHack(ctx context.Context) {
	c.hack.refreshMu.Lock()
	defer c.hack.refreshMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.ensureHackKeys(ctx); err != nil {
		c.tracker.Fail("paper metrics Hack keys", err)
		log.Printf("paper metrics Hack keys: %v", err)
		return
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.id::text,e.slug,e.account_id::text,e.stage,COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id)),k.event_key,k.synthetic,last.stage,e.updated_at FROM events e JOIN paper_hack_key k ON k.event_id=e.id
 LEFT JOIN LATERAL (SELECT stage FROM paper_hack_phase p WHERE p.event_key=k.event_key
 AND stage IN ('draft','open','building','closed','judging','results','archived')
 ORDER BY recorded_at DESC LIMIT 1) last ON true`)
	if err != nil {
		c.tracker.Fail("paper metrics Hack metadata", err)
		log.Printf("paper metrics Hack metadata: %v", err)
		return
	}
	old := c.hack.meta.Load()
	meta := &hackMetadata{events: map[string]*hackEventInfo{}, accounts: map[string]*hackEventInfo{}, members: map[string]map[string]struct{}{}, hosts: map[string]*hackEventInfo{}}
	type change struct {
		id, stage string
		baseline  bool
		origin    time.Time
	}
	var changes []change
	for rows.Next() {
		var id, slug, account, stage string
		var origin time.Time
		var published sql.NullTime
		var last sql.NullString
		var key int64
		var synthetic bool
		if err := rows.Scan(&id, &slug, &account, &stage, &published, &key, &synthetic, &last, &origin); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			rows.Close()
			return
		}
		event := old.events[slug]
		if event == nil {
			event = &hackEventInfo{id: id, slug: slug, account: account, key: key}
		}
		event.synthetic.Store(synthetic)
		if !last.Valid || last.String != stage {
			changes = append(changes, change{id, stage, !last.Valid, origin})
		}
		event.state.Store(&hackEventState{stage: stage, published: published.Time})
		meta.events[slug], meta.accounts[account] = event, event
	}
	rows.Close()
	if rows.Err() != nil {
		c.tracker.Fail("read Hack metadata", rows.Err())
		return
	}
	rows, err = c.db.QueryContext(ctx, `SELECT e.slug,m.user_id::text FROM event_members m JOIN events e ON e.id=m.event_id WHERE e.stage IN ('open','building','closed','judging') OR (e.stage='results' AND COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id))<=now() AND COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id))>now()-interval '24 hours')`)
	if err != nil {
		c.tracker.Fail("query Hack metadata", err)
	}
	if err == nil {
		for rows.Next() {
			var slug, member string
			if err := rows.Scan(&slug, &member); err != nil {
				c.tracker.Fail("scan Hack metadata", err)
				break
			}
			if meta.members[slug] == nil {
				meta.members[slug] = map[string]struct{}{}
			}
			meta.members[slug][member] = struct{}{}
		}
		rows.Close()
		if rows.Err() != nil {
			c.tracker.Fail("read Hack metadata", rows.Err())
		}
	}
	rows, err = c.db.QueryContext(ctx, `SELECT user_id::text,name FROM sites WHERE deleted_at IS NULL`)
	if err != nil {
		c.tracker.Fail("query Hack metadata", err)
	}
	if err == nil {
		for rows.Next() {
			var account, name string
			if err := rows.Scan(&account, &name); err != nil {
				c.tracker.Fail("scan Hack metadata", err)
				break
			}
			if event := meta.accounts[account]; event != nil && name != event.id {
				for base := range c.apexHosts {
					meta.hosts[name+"."+event.slug+"."+base] = event
				}
			}
		}
		rows.Close()
		if rows.Err() != nil {
			c.tracker.Fail("read Hack metadata", rows.Err())
		}
	}
	c.hack.meta.Store(meta)
	now := time.Now().UTC()
	for _, change := range changes {
		if err := c.recordHackPhase(ctx, change.id, change.stage, now, !change.baseline, change.baseline, change.origin); err != nil {
			c.tracker.Fail("paper metrics Hack observed phase", err)
			log.Printf("paper metrics Hack observed phase: %v", err)
		}
	}
}

// Stage hooks capture time after commit and only enqueue background work.
// A full queue never blocks the business request; refresh observes the latest
// database stage as a fallback (not every intermediate transition).
type hackPhaseNotice struct {
	eventID, stage string
	at, origin     time.Time
}

func (h *hackMetrics) nudge() {
	select {
	case h.refresh <- struct{}{}:
	default:
	}
}
func RecordHackStage(ctx context.Context, eventID, stage string, origin ...time.Time) {
	if request, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		h := request.metrics
		at := time.Now().UTC()
		stamp := at
		if len(origin) > 0 && !origin[0].IsZero() {
			stamp = origin[0]
		}
		select {
		case h.phases <- hackPhaseNotice{eventID, stage, at, stamp}:
		default:
		}
		h.nudge()
	}
}
func (c *Collector) refreshHackBackground(ctx context.Context) {
	for {
		select {
		case notice := <-c.hack.phases:
			if err := c.recordHackPhase(ctx, notice.eventID, notice.stage, notice.at, false, false, notice.origin); err != nil {
				c.tracker.Fail("paper metrics Hack phase", err)
				log.Printf("paper metrics Hack phase: %v", err)
			}
		default:
			c.refreshHack(ctx)
			if err := c.hackTimestampPhases(ctx); err != nil {
				c.tracker.Fail("paper metrics Hack timestamp phases", err)
				log.Printf("paper metrics Hack timestamp phases: %v", err)
			}
			return
		}
	}
}
func (c *Collector) recordHackStage(ctx context.Context, eventID, stage string, at time.Time, observed bool) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("recordHackStage", resultErr)
		}
	}()
	return c.recordHackPhase(ctx, eventID, stage, at, observed, false)
}
func (c *Collector) recordHackPhase(ctx context.Context, eventID, stage string, at time.Time, observed, baseline bool, origin ...time.Time) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("recordHackPhase", resultErr)
		}
	}()
	if err := c.ensureHackKeys(ctx); err != nil {
		return err
	}
	run, err := c.ensureRun(ctx)
	if err != nil {
		return err
	}
	stamp := at
	if len(origin) > 0 && !origin[0].IsZero() {
		stamp = origin[0]
	}
	var written int64
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key int64
	if err = tx.QueryRowContext(ctx, `SELECT event_key FROM paper_hack_key WHERE event_id=$1 FOR UPDATE`, eventID).Scan(&key); err != nil {
		return err
	}
	var previous string
	var previousOrigin time.Time
	var wasObserved, wasBaseline bool
	err = tx.QueryRowContext(ctx, `SELECT stage,origin_change,observed,baseline FROM paper_hack_phase WHERE event_key=$1 AND stage IN ('draft','open','building','closed','judging','results','archived') ORDER BY recorded_at DESC LIMIT 1`, key).Scan(&previous, &previousOrigin, &wasObserved, &wasBaseline)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	// Recheck under the key lock: a hook may have recorded this transition
	// after refresh read its metadata snapshot.
	if (observed || baseline) && previous == stage {
		return nil
	}
	if !observed && !baseline && previous == stage && (wasObserved || wasBaseline) {
		var exact bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paper_hack_phase WHERE event_key=$1 AND stage=$2 AND origin_change=$3)`, key, stage, stamp).Scan(&exact); err != nil {
			return err
		}
		if !exact {
			if _, err = c.execMetric(ctx, tx, &written, `UPDATE paper_hack_phase SET origin_change=$4 WHERE event_key=$1 AND stage=$2 AND origin_change=$3`, key, stage, previousOrigin, stamp); err != nil {
				return err
			}
		}
	}
	if stage == "open" && !observed && !baseline {
		if _, err = c.execMetric(ctx, tx, &written, `UPDATE paper_hack_key SET first_open_at=COALESCE(first_open_at,$2) WHERE event_id=$1`, eventID, at); err != nil {
			return err
		}
	}
	_, err = c.execMetric(ctx, tx, &written, `INSERT INTO paper_hack_phase(run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed,baseline,origin_change)
 SELECT $1,k.event_key,k.synthetic,$3,date_trunc('minute',$4::timestamptz),round(EXTRACT(epoch FROM ($4::timestamptz-COALESCE(e.starts_at,k.first_open_at)))/60)::int,date_trunc('minute',COALESCE(e.starts_at,k.first_open_at)+interval '30 seconds'),$5,$6,$7
 FROM events e JOIN paper_hack_key k ON k.event_id=e.id WHERE e.id=$2
 ON CONFLICT(event_key,stage,origin_change) DO UPDATE SET
 baseline=paper_hack_phase.baseline AND EXCLUDED.baseline,
 run_id=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.run_id ELSE paper_hack_phase.run_id END,
 phase_slot=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.phase_slot ELSE paper_hack_phase.phase_slot END,
 minute_offset=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.minute_offset ELSE paper_hack_phase.minute_offset END,
 origin_slot=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.origin_slot ELSE paper_hack_phase.origin_slot END,
 observed=paper_hack_phase.observed AND EXCLUDED.observed`, run, eventID, stage, at, observed, baseline, stamp)
	if err != nil {
		return err
	}
	return c.commitMetrics(tx, written)
}
func (c *Collector) hackTimestampPhases(ctx context.Context) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("hackTimestampPhases", resultErr)
		}
	}()
	run, err := c.ensureRun(ctx)
	if err != nil {
		return err
	}
	_, err = c.execMetric(ctx, c.db, nil, `INSERT INTO paper_hack_phase (run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed,origin_change)
 SELECT $1,k.event_key,k.synthetic,v.stage,date_trunc('minute',v.at),round(EXTRACT(epoch FROM (v.at-COALESCE(e.starts_at,k.first_open_at)))/60)::int,date_trunc('minute',COALESCE(e.starts_at,k.first_open_at)+interval '30 seconds'),false,v.at
 FROM events e JOIN paper_hack_key k ON k.event_id=e.id
 CROSS JOIN LATERAL (VALUES ('submission_deadline',e.submission_deadline),('judging_locked',e.judging_locked_at),('results_published',COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id))),('voting_opens',e.voting_opens_at),('voting_closes',e.voting_closes_at)) v(stage,at)
 WHERE v.at IS NOT NULL ON CONFLICT DO NOTHING`, run)
	return err
}

func HackPhasesChanged(ctx context.Context) {
	if request, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		request.metrics.nudge()
	}
}

// RefreshHackBackground drains phase notices and refreshes the metadata snapshot.
func (c *Collector) RefreshHackBackground(ctx context.Context) { c.refreshHackBackground(ctx) }
