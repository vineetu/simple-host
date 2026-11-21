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
	run, err := c.platform.EnsureRun(ctx)
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
			_, err = c.platform.ExecMetric(ctx, c.db, nil, `INSERT INTO paper_hack_key (event_id,event_key,synthetic,run_id) VALUES ($1,$2,$3,$4) ON CONFLICT (event_id) DO NOTHING`, item.id, key, item.synthetic, run)
			if err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	_, err = c.platform.ExecMetric(ctx, c.db, nil, `UPDATE paper_hack_key k SET synthetic=true FROM events e LEFT JOIN users u ON u.id=e.created_by WHERE k.event_id=e.id AND NOT k.synthetic AND `+syntheticSQL)
	return err
}

// refreshHack reloads the Hack metadata snapshot (events, active members and team site hosts) and records any stage change it notices.
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
	meta := &hackMetadata{events: map[string]*hackEventInfo{}, accounts: map[string]*hackEventInfo{}, members: map[string]map[string]struct{}{}, hosts: map[string]*hackEventInfo{}}
	changes, ok := c.loadHackEvents(ctx, c.hack.meta.Load(), meta)
	if !ok {
		return
	}
	c.loadHackMembers(ctx, meta)
	c.loadHackHosts(ctx, meta)
	c.hack.meta.Store(meta)
	now := time.Now().UTC()
	for _, change := range changes {
		if err := c.recordHackPhase(ctx, change.id, change.stage, now, !change.baseline, change.baseline, change.origin); err != nil {
			c.tracker.Fail("paper metrics Hack observed phase", err)
			log.Printf("paper metrics Hack observed phase: %v", err)
		}
	}
}

// hackStageChange is a stage refresh found that no phase records yet; baseline means the event had no phase at all.
type hackStageChange struct {
	id, stage string
	baseline  bool
	origin    time.Time
}

// loadHackEvents fills meta's events and accounts, keeping the info of events old already knew, and lists the stage changes it noticed.
func (c *Collector) loadHackEvents(ctx context.Context, old, meta *hackMetadata) ([]hackStageChange, bool) {
	rows, err := c.db.QueryContext(ctx, `SELECT e.id::text,e.slug,e.account_id::text,e.stage,COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id)),k.event_key,k.synthetic,last.stage,e.updated_at FROM events e JOIN paper_hack_key k ON k.event_id=e.id
 LEFT JOIN LATERAL (SELECT stage FROM paper_hack_phase p WHERE p.event_key=k.event_key
 AND stage IN ('draft','open','building','closed','judging','results','archived')
 ORDER BY recorded_at DESC LIMIT 1) last ON true`)
	if err != nil {
		c.tracker.Fail("paper metrics Hack metadata", err)
		log.Printf("paper metrics Hack metadata: %v", err)
		return nil, false
	}
	defer rows.Close()
	var changes []hackStageChange
	for rows.Next() {
		var id, slug, account, stage string
		var origin time.Time
		var published sql.NullTime
		var last sql.NullString
		var key int64
		var synthetic bool
		if err := rows.Scan(&id, &slug, &account, &stage, &published, &key, &synthetic, &last, &origin); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			return nil, false
		}
		event := old.events[slug]
		if event == nil {
			event = &hackEventInfo{id: id, slug: slug, account: account, key: key}
		}
		event.synthetic.Store(synthetic)
		if !last.Valid || last.String != stage {
			changes = append(changes, hackStageChange{id, stage, !last.Valid, origin})
		}
		event.state.Store(&hackEventState{stage: stage, published: published.Time})
		meta.events[slug], meta.accounts[account] = event, event
	}
	if err := rows.Err(); err != nil {
		c.tracker.Fail("read Hack metadata", err)
		return nil, false
	}
	return changes, true
}

// loadHackMembers fills meta's members of events that are running, or whose results went out in the last day.
func (c *Collector) loadHackMembers(ctx context.Context, meta *hackMetadata) {
	c.eachHackRow(ctx, `SELECT e.slug,m.user_id::text FROM event_members m JOIN events e ON e.id=m.event_id WHERE e.stage IN ('open','building','closed','judging') OR (e.stage='results' AND COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id))<=now() AND COALESCE(e.results_published_at,(SELECT published_at FROM event_results r WHERE r.event_id=e.id))>now()-interval '24 hours')`, func(slug, member string) {
		if meta.members[slug] == nil {
			meta.members[slug] = map[string]struct{}{}
		}
		meta.members[slug][member] = struct{}{}
	})
}

// loadHackHosts maps every team site host of every known event to it.
func (c *Collector) loadHackHosts(ctx context.Context, meta *hackMetadata) {
	c.eachHackRow(ctx, `SELECT user_id::text,name FROM sites WHERE deleted_at IS NULL`, func(account, name string) {
		if event := meta.accounts[account]; event != nil && name != event.id {
			for base := range c.apexHosts {
				meta.hosts[name+"."+event.slug+"."+base] = event
			}
		}
	})
}

// eachHackRow runs a two-column metadata query and hands each row to use, reporting (not returning) a failed read.
func (c *Collector) eachHackRow(ctx context.Context, query string, use func(a, b string)) {
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		c.tracker.Fail("query Hack metadata", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			return
		}
		use(a, b)
	}
	if err := rows.Err(); err != nil {
		c.tracker.Fail("read Hack metadata", err)
	}
}

// Stage hooks capture time after commit and only enqueue background work.
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
		stamp := phaseStamp(at, origin)
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

// recordHackPhase records that an event reached stage at at.
func (c *Collector) recordHackPhase(ctx context.Context, eventID, stage string, at time.Time, observed, baseline bool, origin ...time.Time) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("recordHackPhase", resultErr)
		}
	}()
	if err := c.ensureHackKeys(ctx); err != nil {
		return err
	}
	run, err := c.platform.EnsureRun(ctx)
	if err != nil {
		return err
	}
	stamp := phaseStamp(at, origin)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key int64
	if err = tx.QueryRowContext(ctx, `SELECT event_key FROM paper_hack_key WHERE event_id=$1 FOR UPDATE`, eventID).Scan(&key); err != nil {
		return err
	}
	prev, err := lastHackPhase(ctx, tx, key)
	if err != nil {
		return err
	}
	plan := planHackPhase(prev, stage, !observed && !baseline)
	if !plan.record {
		return nil
	}
	var written int64
	if plan.moveOrigin {
		if err := c.moveHackPhaseOrigin(ctx, tx, &written, key, stage, prev.origin, stamp); err != nil {
			return err
		}
	}
	if plan.firstOpen {
		if _, err = c.platform.ExecMetric(ctx, tx, &written, `UPDATE paper_hack_key SET first_open_at=COALESCE(first_open_at,$2) WHERE event_id=$1`, eventID, at); err != nil {
			return err
		}
	}
	if _, err = c.platform.ExecMetric(ctx, tx, &written, insertHackPhaseSQL, run, eventID, stage, at, observed, baseline, stamp); err != nil {
		return err
	}
	return c.platform.CommitMetrics(tx, written)
}

// hackPhasePlan is what recording one phase does besides inserting it.
type hackPhasePlan struct{ record, moveOrigin, firstOpen bool }

// planHackPhase decides from the latest recorded phase.
func planHackPhase(prev hackPhaseRow, stage string, hook bool) hackPhasePlan {
	if !hook {
		return hackPhasePlan{record: prev.stage != stage}
	}
	return hackPhasePlan{record: true, moveOrigin: prev.stage == stage && (prev.observed || prev.baseline), firstOpen: stage == "open"}
}

// phaseStamp is the change a phase dates from: origin when given, else at.
func phaseStamp(at time.Time, origin []time.Time) time.Time {
	if len(origin) > 0 && !origin[0].IsZero() {
		return origin[0]
	}
	return at
}

// hackPhaseRow is the latest stage phase recorded for an event.
type hackPhaseRow struct {
	stage              string
	origin             time.Time
	observed, baseline bool
}

// lastHackPhase reads the latest stage phase of an event key; none is the zero row.
func lastHackPhase(ctx context.Context, tx *sql.Tx, key int64) (p hackPhaseRow, err error) {
	err = tx.QueryRowContext(ctx, `SELECT stage,origin_change,observed,baseline FROM paper_hack_phase WHERE event_key=$1 AND stage IN ('draft','open','building','closed','judging','results','archived') ORDER BY recorded_at DESC LIMIT 1`, key).Scan(&p.stage, &p.origin, &p.observed, &p.baseline)
	if err == sql.ErrNoRows {
		err = nil
	}
	return p, err
}

// moveHackPhaseOrigin re-dates a phase refresh or the baseline recorded first to the hook's exact change, unless that change is already recorded.
func (c *Collector) moveHackPhaseOrigin(ctx context.Context, tx *sql.Tx, written *int64, key int64, stage string, from, to time.Time) error {
	var exact bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paper_hack_phase WHERE event_key=$1 AND stage=$2 AND origin_change=$3)`, key, stage, to).Scan(&exact); err != nil || exact {
		return err
	}
	_, err := c.platform.ExecMetric(ctx, tx, written, `UPDATE paper_hack_phase SET origin_change=$4 WHERE event_key=$1 AND stage=$2 AND origin_change=$3`, key, stage, from, to)
	return err
}

// insertHackPhaseSQL records one stage phase; a hook's row replaces the timing of an observed or baseline row for the same change.
const insertHackPhaseSQL = `INSERT INTO paper_hack_phase(run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed,baseline,origin_change)
 SELECT $1,k.event_key,k.synthetic,$3,date_trunc('minute',$4::timestamptz),round(EXTRACT(epoch FROM ($4::timestamptz-COALESCE(e.starts_at,k.first_open_at)))/60)::int,date_trunc('minute',COALESCE(e.starts_at,k.first_open_at)+interval '30 seconds'),$5,$6,$7
 FROM events e JOIN paper_hack_key k ON k.event_id=e.id WHERE e.id=$2
 ON CONFLICT(event_key,stage,origin_change) DO UPDATE SET
 baseline=paper_hack_phase.baseline AND EXCLUDED.baseline,
 run_id=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.run_id ELSE paper_hack_phase.run_id END,
 phase_slot=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.phase_slot ELSE paper_hack_phase.phase_slot END,
 minute_offset=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.minute_offset ELSE paper_hack_phase.minute_offset END,
 origin_slot=CASE WHEN (paper_hack_phase.observed OR paper_hack_phase.baseline) AND NOT EXCLUDED.observed THEN EXCLUDED.origin_slot ELSE paper_hack_phase.origin_slot END,
 observed=paper_hack_phase.observed AND EXCLUDED.observed`

func (c *Collector) hackTimestampPhases(ctx context.Context) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("hackTimestampPhases", resultErr)
		}
	}()
	run, err := c.platform.EnsureRun(ctx)
	if err != nil {
		return err
	}
	_, err = c.platform.ExecMetric(ctx, c.db, nil, `INSERT INTO paper_hack_phase (run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed,origin_change)
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
