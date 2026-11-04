package papermetrics

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lib/pq"
)

const hackSlot = time.Minute
const hackEventCap = 200
const hackShardCount = 4

type hackEventState struct {
	stage     string
	published time.Time
}
type hackEventInfo struct {
	id, slug, account string // memory only; source ID exists only in the private key table
	key               int64
	synthetic         atomic.Bool
	state             atomic.Pointer[hackEventState]
	current           atomic.Pointer[hackSlotCount]
	inflight          atomic.Int64
}
type hackMetadata struct {
	events, accounts map[string]*hackEventInfo
	members          map[string]map[string]struct{}
	hosts            map[string]*hackEventInfo
}
type hackCell struct {
	arrivals, writes1s [60]atomic.Int32
	hist               LogHistogram
	bytes              atomic.Int64
}
type hackCounts struct {
	pending                               atomic.Int64
	requests, writes, dataWrites, deploys atomic.Int64
	arrivals, writes1s                    [60]atomic.Int64
	cells                                 [numClasses][4]atomic.Pointer[hackCell]
}
type hackSlotCount struct {
	hackCounts
	shards              [hackShardCount - 1]atomic.Pointer[hackCounts]
	event               *hackEventInfo
	slot                time.Time
	active, inflightMax atomic.Int64
	members             hackMemberSet // cleared after completed start slots
	pending             atomic.Int64
	sealed              atomic.Bool
}
type hackMetrics struct {
	refreshMu     sync.Mutex
	refresh       chan struct{}
	phases        chan hackPhaseNotice
	owner         *Collector
	meta          atomic.Pointer[hackMetadata]
	mu            sync.Mutex // admission/rotation only, never taken on the steady request path
	slots         map[int64]map[int64]*hackSlotCount
	flushedBefore int64
	requests      sync.Pool
	nextShard     atomic.Uint64
	cells         sync.Pool
	hist          [16][numClasses]classHist
}

func (c *Collector) WithHack() *Collector {
	c.hack = &hackMetrics{owner: c, slots: map[int64]map[int64]*hackSlotCount{}, refresh: make(chan struct{}, 1), phases: make(chan hackPhaseNotice, 256)}
	c.hack.meta.Store(&hackMetadata{events: map[string]*hackEventInfo{}, accounts: map[string]*hackEventInfo{}, members: map[string]map[string]struct{}{}, hosts: map[string]*hackEventInfo{}})
	return c
}
func hackActive(stage string, published, now time.Time) bool {
	switch stage {
	case "open", "building", "closed", "judging":
		return true
	case "results":
		return !published.IsZero() && !now.Before(published) && now.Before(published.Add(24*time.Hour))
	}
	return false
}

type hackRequestKey struct{}
type hackRequest struct {
	writer                                statusWriter
	metrics                               *hackMetrics
	start                                 time.Time
	class                                 int
	event                                 *hackEventInfo
	slot                                  *hackSlotCount
	counts                                *hackCounts
	shard                                 uint8
	second                                int
	member                                string
	write, deploy, apiCandidate, apiWrite bool
}

// ResolveHackEvent uses an already-resolved event. A verified member can be
// supplied directly; public/key-only paths use the immutable membership cache.
func ResolveHackEvent(ctx context.Context, slug, stage, member string, published ...time.Time) {
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		event := r.event
		if event == nil || event.slug != slug {
			event = r.metrics.meta.Load().events[slug]
		}
		if event == nil {
			return
		}
		stamp := event.state.Load().published
		if len(published) > 0 && !published[0].IsZero() {
			stamp = published[0]
		}
		if r.slot == nil && hackActive(stage, stamp, r.start) {
			r.attach(event)
		}
		if member != "" && r.event == event {
			r.member = member
		}
	}
}
func HackMember(ctx context.Context, member string) {
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok && r.event != nil {
		if _, found := r.metrics.meta.Load().members[r.event.slug][member]; found {
			r.member = member
		}
	}
}
func ResolveHackSite(ctx context.Context, account, site, member string, write bool) {
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		meta := r.metrics.meta.Load()
		event := r.event
		if event == nil || event.account != account {
			event = meta.accounts[account]
		}
		if event == nil || event.id == site && !write {
			return
		}
		state := event.state.Load()
		if r.slot == nil && hackActive(state.stage, state.published, r.start) {
			r.attach(event)
		}
		if r.event == event {
			if _, ok := meta.members[event.slug][member]; ok {
				r.member = member
			}
		}
	}
}

// HackWriteCommitted counts successful mutating operations, not transport 2xx.
// In particular an MCP HTTP 200 carrying a failed operation is not a write.
func HackWriteCommitted(ctx context.Context) {
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		r.write = true
	}
}

func HackDeploy(ctx context.Context) {
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok {
		r.deploy = true
	}
}
func (r *hackRequest) attach(event *hackEventInfo) {
	if r.class == classMCPStream || r.slot != nil {
		return
	}
	slot := r.metrics.slotFor(event, r.start)
	if slot == nil {
		return
	}
	counts := r.metrics.countsFor(slot, int(r.shard)%hackShardCount)
	if counts == nil || slot.sealed.Load() {
		return
	}
	for {
		refs := counts.pending.Load()
		if refs < 0 {
			return
		}
		if counts.pending.CompareAndSwap(refs, refs+1) {
			break
		}
	}
	r.event, r.slot, r.counts = event, slot, counts
	r.second = r.start.Second()
	atomicMax(&slot.inflightMax, event.inflight.Add(1))
}
func (h *hackMetrics) slotFor(event *hackEventInfo, start time.Time) *hackSlotCount {
	minute := start.Unix() / 60
	if slot := event.current.Load(); slot != nil && slot.slot.Unix()/60 == minute && !slot.sealed.Load() {
		return slot
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	events := h.slots[minute]
	if slot := events[event.key]; slot != nil {
		return slot
	}
	if minute < h.flushedBefore {
		return nil
	}
	if events == nil {
		events = map[int64]*hackSlotCount{}
		h.slots[minute] = events
	}
	if slot := events[event.key]; slot != nil {
		return slot
	}
	if len(events) >= hackEventCap {
		return nil
	}
	slot := &hackSlotCount{event: event, slot: start.UTC().Truncate(hackSlot)}
	slot.inflightMax.Store(event.inflight.Load())
	events[event.key] = slot
	if current := event.current.Load(); current == nil || !current.slot.After(slot.slot) {
		event.current.Store(slot)
	}
	return slot
}

// Contexts outlive responses; only synchronous request state is recycled.
type hackRequestContext struct {
	context.Context
	state atomic.Pointer[hackRequest]
}

func (r *hackRequestContext) Value(key any) any {
	if _, ok := key.(hackRequestKey); ok {
		if state := r.state.Load(); state != nil {
			return state
		}
		return nil
	}
	return r.Context.Value(key)
}
func (c *Collector) wrapHack(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		class := c.classify(r)
		if class == classNone {
			next.ServeHTTP(w, r)
			return
		}
		state, _ := c.hack.requests.Get().(*hackRequest)
		if state == nil {
			state = &hackRequest{shard: uint8(c.hack.nextShard.Add(1) % hackShardCount)}
		}
		state.metrics, state.start, state.class = c.hack, start, class
		state.apiCandidate = mutationMethod(r.Method) && strings.HasPrefix(r.URL.Path, "/v1/hack/events/")
		state.writer = statusWriter{ResponseWriter: w, status: 200}
		// Resolve cached event URL/host before routing/auth so the gauge spans the
		// entire handler. Other site forms attach at their existing resolution hook.
		meta := c.hack.meta.Load()
		var event *hackEventInfo
		if path, ok := strings.CutPrefix(r.URL.Path, "/v1/hack/events/"); ok {
			slug, _, _ := strings.Cut(path, "/")
			event = meta.events[slug]
		}
		if event == nil {
			event = meta.hosts[r.Host]
		}
		if event != nil {
			stage := event.state.Load()
			if hackActive(stage.stage, stage.published, start) {
				state.attach(event)
			}
		}
		ctx := &hackRequestContext{Context: r.Context()}
		ctx.state.Store(state)
		completed := false
		defer func() {
			// Preserve the panic for net/http while releasing both gauges and
			// recording the start slot. An aborted handler has server-error
			// semantics for metrics even if it already wrote a partial body.
			if !completed {
				state.writer.status = http.StatusInternalServerError
			}
			elapsed := time.Since(start)
			c.hack.hist[state.shard][class].observe(elapsed, state.writer.status, state.writer.bytes)
			state.finish(elapsed)
			ctx.state.Store(nil)
			*state = hackRequest{shard: state.shard}
			c.hack.requests.Put(state)
		}()
		next.ServeHTTP(&state.writer, r.WithContext(ctx))
		completed = true
	})
}
func (r *hackRequest) finish(elapsed time.Duration) {
	slot := r.slot
	if slot == nil {
		return
	}
	status := r.writer.status
	group := status/100 - 2
	if group < 0 {
		group = 0
	}
	if group > 3 {
		group = 3
	}
	cell := r.counts.cells[r.class][group].Load()
	if cell == nil {
		candidate := r.metrics.getCell()
		if r.counts.cells[r.class][group].CompareAndSwap(nil, candidate) {
			cell = candidate
		} else {
			r.metrics.putCell(candidate)
			cell = r.counts.cells[r.class][group].Load()
		}
	}
	cell.arrivals[r.second].Add(1)
	cell.hist.Observe(elapsed.Microseconds())
	cell.bytes.Add(r.writer.bytes)
	if r.write {
		r.counts.dataWrites.Add(1)
	}
	if r.write || r.apiWrite || (r.apiCandidate && status >= 200 && status < 300) {
		cell.writes1s[r.second].Add(1)
	}
	if r.deploy {
		r.counts.deploys.Add(1)
	}

	if r.member != "" && slot.members.add(r.member) {
		slot.active.Add(1)
	}

	r.event.inflight.Add(-1)
	r.counts.pending.Add(-1)
}

// Only completed, quiescent start slots are detached. A long request retains
// its start slot until completion rather than moving its latency into a new slot.
func (h *hackMetrics) drain(now time.Time) []*hackSlotCount {
	cutoff := now.Unix() / 60
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []*hackSlotCount
	for minute, events := range h.slots {
		if minute >= cutoff {
			continue
		}
		for key, slot := range events {
			if !slot.seal() {
				continue
			}
			slot.pending.Store(-1)
			slot.mergeShards(h)
			slot.members.clear()
			slot.event.current.CompareAndSwap(slot, nil)
			if slot.requests.Load() != 0 || slot.writes.Load() != 0 || slot.deploys.Load() != 0 {
				result = append(result, slot)
			}
			delete(events, key)
		}
		if len(events) == 0 {
			delete(h.slots, minute)
		}
	}
	h.flushedBefore = cutoff
	return result
}
func (c *Collector) flushHack(ctx context.Context, now time.Time) {
	run, err := c.ensureRun(ctx)
	if err != nil {
		c.tracker.Fail("paper metrics run", err)
		log.Printf("paper metrics run: %v", err)
		return
	}
	rows := c.hack.drain(now)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, slot := range rows {
		c.flushHackSlot(ctx, run, slot)
		c.hack.releaseCells(slot)
	}
	for _, table := range []string{"paper_hack_latency", "paper_hack_minute"} {
		if _, err := c.execMetric(ctx, c.db, nil, `DELETE FROM `+table+` WHERE slot<$1`, now.AddDate(0, 0, -400)); err != nil {
			c.tracker.Fail("paper metrics Hack retention", err)
			log.Printf("paper metrics Hack retention: %v", err)
		}
	}
}
func (c *Collector) flushHackSlot(ctx context.Context, run int64, slot *hackSlotCount) {
	var arrivals, writes [60]int64
	var peak int64
	for i := range arrivals {
		arrivals[i] = slot.arrivals[i].Load()
		writes[i] = slot.writes1s[i].Load()
		if arrivals[i] > peak {
			peak = arrivals[i]
		}
	}
	var written int64
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.tracker.Fail("paper metrics Hack slot", err)
		log.Printf("paper metrics Hack slot: %v", err)
		return
	}
	_, err = c.execMetric(ctx, tx, &written, `INSERT INTO paper_hack_minute (run_id,slot,slot_secs,event_key,synthetic,requests,writes,data_writes,deploys,active_members,inflight_max,peak_rps_1s,arrivals_1s,writes_1s)
   VALUES ($1,$2,60,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT DO NOTHING`, run, slot.slot, slot.event.key, slot.event.synthetic.Load(), slot.requests.Load(), slot.writes.Load(), slot.dataWrites.Load(), slot.deploys.Load(), slot.active.Load(), slot.inflightMax.Load(), peak, pq.Array(arrivals[:]), pq.Array(writes[:]))
	for class := 1; class < numClasses && err == nil; class++ {
		for group := 0; group < 4 && err == nil; group++ {
			if cell := slot.cells[class][group].Load(); cell != nil {
				hist := cell.hist.Snapshot()
				var classArrivals, classWrites [60]int64
				for i := range classArrivals {
					classArrivals[i] = int64(cell.arrivals[i].Load())
					classWrites[i] = int64(cell.writes1s[i].Load())
				}
				_, err = c.execMetric(ctx, tx, &written, `INSERT INTO paper_hack_latency (run_id,slot,event_key,synthetic,class,status_group,n,sum_us,max_us,bytes_out,idx_offset,counts,arrivals_1s,writes_1s)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT DO NOTHING`, run, slot.slot, slot.event.key, slot.event.synthetic.Load(), classNames[class], []string{"2xx", "3xx", "4xx", "5xx"}[group], hist.N, hist.SumUS, hist.MaxUS, cell.bytes.Load(), hist.Offset, pq.Array(hist.Counts), pq.Array(classArrivals[:]), pq.Array(classWrites[:]))
			}
		}
	}
	if err == nil {
		err = c.commitMetrics(tx, written)
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		c.tracker.Fail("paper metrics Hack slot", err)
		log.Printf("paper metrics Hack slot: %v", err)
	}
}
func (c *Collector) hackLoop(ctx context.Context) {
	tick := time.NewTimer(time.Until(time.Now().UTC().Truncate(hackSlot).Add(hackSlot)))
	defer tick.Stop()
	c.refreshHackBackground(ctx)
	for {
		select {
		case <-ctx.Done():
			finalCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			c.flushHack(finalCtx, time.Now().UTC())
			cancel()
			return
		case <-c.hack.refresh:
			c.refreshHackBackground(ctx)
		case now := <-tick.C:
			c.flushHack(ctx, now)
			c.refreshHackBackground(ctx)
			tick.Reset(time.Until(time.Now().UTC().Truncate(hackSlot).Add(hackSlot)))
		}
	}
}

func mutationMethod(method string) bool {
	return method != "GET" && method != "HEAD" && method != "OPTIONS"
}

// HackMutationResult observes the existing replay status rather than the MCP
// transport status, which is 200 even for a failed event API operation.
func HackMutationResult(ctx context.Context, method, path string, status int) {
	if !mutationMethod(method) || !strings.HasPrefix(path, "/v1/hack/events/") || status < 200 || status >= 300 {
		return
	}
	if r, ok := ctx.Value(hackRequestKey{}).(*hackRequest); ok && r.event != nil {
		r.apiWrite = true
	}
}

// Fold quiescent shards only after sealing; event totals are derived from
// the single class/status arrival/write observations, never duplicate adds.
func (slot *hackSlotCount) mergeShards(h *hackMetrics) {
	for i := range slot.shards {
		shard := slot.shards[i].Load()
		if shard == nil {
			continue
		}
		slot.dataWrites.Add(shard.dataWrites.Load())
		slot.deploys.Add(shard.deploys.Load())
		for class := 1; class < numClasses; class++ {
			for group := 0; group < 4; group++ {
				src := shard.cells[class][group].Swap(nil)
				if src == nil {
					continue
				}
				dst := slot.cells[class][group].Load()
				if dst == nil {
					slot.cells[class][group].Store(src)
					continue
				}
				dst.hist.merge(src.hist.Snapshot())
				dst.bytes.Add(src.bytes.Load())
				for sec := 0; sec < 60; sec++ {
					dst.arrivals[sec].Add(src.arrivals[sec].Load())
					dst.writes1s[sec].Add(src.writes1s[sec].Load())
				}
				h.putCell(src)
			}
		}
	}
	for class := 1; class < numClasses; class++ {
		for group := 0; group < 4; group++ {
			if cell := slot.cells[class][group].Load(); cell != nil {
				slot.requests.Add(cell.hist.n.Load())
				for sec := 0; sec < 60; sec++ {
					slot.arrivals[sec].Add(int64(cell.arrivals[sec].Load()))
					w := int64(cell.writes1s[sec].Load())
					slot.writes1s[sec].Add(w)
					slot.writes.Add(w)
				}
			}
		}
	}
}
func (h *hackMetrics) countsFor(slot *hackSlotCount, shard int) *hackCounts {
	if shard == 0 {
		return &slot.hackCounts
	}
	ptr := &slot.shards[shard-1]
	if counts := ptr.Load(); counts != nil {
		return counts
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if slot.sealed.Load() {
		return nil
	}
	counts := ptr.Load()
	if counts == nil {
		counts = &hackCounts{}
		ptr.Store(counts)
	}
	return counts
}
func (slot *hackSlotCount) seal() bool {
	counts := [hackShardCount]*hackCounts{&slot.hackCounts}
	for i := range slot.shards {
		counts[i+1] = slot.shards[i].Load()
	}
	for _, c := range counts {
		if c != nil && c.pending.Load() != 0 {
			return false
		}
	}
	slot.sealed.Store(true)
	for _, c := range counts {
		if c != nil && !c.pending.CompareAndSwap(0, -1) {
			for _, other := range counts {
				if other != nil {
					other.pending.CompareAndSwap(-1, 0)
				}
			}
			slot.sealed.Store(false)
			return false
		}
	}
	return true
}
func (h *hackMetrics) getCell() *hackCell {
	cell, _ := h.cells.Get().(*hackCell)
	if cell == nil {
		cell = &hackCell{}
	}
	return cell
}
func (h *hackMetrics) putCell(cell *hackCell) {
	cell.hist.Reset()
	cell.bytes.Store(0)
	for sec := 0; sec < 60; sec++ {
		cell.arrivals[sec].Store(0)
		cell.writes1s[sec].Store(0)
	}
	h.cells.Put(cell)
}
func (h *hackMetrics) releaseCells(slot *hackSlotCount) {
	for class := 1; class < numClasses; class++ {
		for group := 0; group < 4; group++ {
			if cell := slot.cells[class][group].Swap(nil); cell != nil {
				h.putCell(cell)
			}
		}
	}
}
