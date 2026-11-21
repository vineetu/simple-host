package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// RandomID is prefix followed by n random bytes in hex.
func RandomID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

// EventID is a fresh event id.
func EventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "evt_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// EventSite decides whether an event name about siteID is wanted, and
// returns the site it is about when it is: someone subscribes to it, it is
// not a visitor event about the site's own owner, no repeat of it for the
// same visitor was stored within dedupe, and the site's event caps allow
// one more. Lookup failures are recorded and drop the event.
func (e *Engine) EventSite(ctx context.Context, siteID, name, visitor string, dedupe time.Duration) (model.Site, bool) {
	yes, err := e.svc.HasNotificationsFor(ctx, siteID, name)
	if err != nil {
		e.emits.Fail("lookup subscriptions", err)
	}
	if err != nil || !yes {
		return model.Site{}, false
	}
	site, err := sitesrepo.GetSiteByID(ctx, e.svc.database, siteID)
	if err != nil {
		e.emits.Fail("load event site", err)
		return model.Site{}, false
	}
	if visitor != "" && visitor == site.UserID {
		return model.Site{}, false
	}
	if dedupe > 0 && visitor != "" {
		if recent, err := e.svc.RecentSiteEvent(ctx, siteID, name, visitor, dedupe); err != nil || recent {
			return model.Site{}, false
		}
	}
	if !e.minute.Allow(siteID) || !e.day.Allow(siteID) {
		log.Printf("notifications: site %s event cap reached, dropped=%d", siteID, e.dropped.Add(1))
		return model.Site{}, false
	}
	return site, true
}

// RecordEvent is a committed storage write, after EventSite admitted it.
type RecordEvent struct {
	SiteID, Name      string
	SiteName, SiteURL string
	Resource, Kind    string
	// Writer is who wrote it: a visitor's id, or "" for the owner.
	Writer string
	// Data is the write's own fields, with the storage layer's routing
	// hints owner_write and row_visitor_id.
	Data map[string]any
}

// StoreRecordEvent stores a storage write's event. The web-push routing
// facts (who wrote it, whose row it is) leave the payload and go beside
// it, so webhook and MCP bodies never carry them. Fields are truncated to
// NotifyFieldMaxBytes each, and dropped when the body would still be too
// large.
func (e *Engine) StoreRecordEvent(ctx context.Context, ev RecordEvent) {
	data := ev.Data
	pushActor := ev.Writer
	if owner, _ := data["owner_write"].(bool); owner {
		pushActor = "owner"
	}
	pushTarget, _ := data["row_visitor_id"].(string)
	delete(data, "owner_write")
	delete(data, "row_visitor_id")
	data["v"], data["site"], data["site_url"] = 1, ev.SiteName, ev.SiteURL
	data["storage"], data["kind"], data["visitor"] = ev.Resource, ev.Kind, nil
	if ev.Writer != "" {
		data["visitor"] = ev.Writer
	}
	untrusted := []string{}
	for _, key := range []string{"fields", "key", "path"} {
		if _, ok := data[key]; ok {
			untrusted = append(untrusted, key)
		}
	}
	data["untrusted"] = untrusted
	if value, ok := data["fields"].(json.RawMessage); ok {
		data["fields"] = Fields(value)
	}
	Truncate(data, e.limits.NotifyFieldMaxBytes)
	raw, err := json.Marshal(data)
	if len(raw) > e.limits.NotifyBodyMaxBytes {
		delete(data, "fields")
		data["truncated"] = []string{"fields"}
		raw, err = json.Marshal(data)
	}
	if err != nil {
		return
	}
	e.stored(e.svc.EmitSiteEventFor(ctx, ev.SiteID, EventID(), ev.Name, ev.Resource, raw, pushActor, pushTarget), true)
}

// SiteEvent is a site-level event (a deploy, a domain's status, a
// sign-in), after EventSite admitted it.
type SiteEvent struct {
	SiteID, Name      string
	SiteName, SiteURL string
	// Visitor is the visitor it is about ("" for none); with Dedupe > 0 a
	// repeat for them within Dedupe is not stored.
	Visitor string
	Dedupe  time.Duration
	Data    map[string]any
}

// StoreSiteEvent stores a site event; one too large is dropped.
func (e *Engine) StoreSiteEvent(ctx context.Context, ev SiteEvent) {
	data := ev.Data
	data["v"], data["site"], data["site_url"] = 1, ev.SiteName, ev.SiteURL
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	if len(raw) > e.limits.NotifyBodyMaxBytes {
		log.Printf("notifications: site %s %s event too large, dropped=%d", ev.SiteID, ev.Name, e.dropped.Add(1))
		return
	}
	if ev.Dedupe <= 0 || ev.Visitor == "" {
		e.stored(e.svc.EmitSiteEvent(ctx, ev.SiteID, EventID(), ev.Name, "", raw), true)
		return
	}
	stored, err := e.svc.EmitSiteEventOnce(ctx, ev.SiteID, EventID(), ev.Name, raw, ev.Visitor, ev.Dedupe)
	e.stored(err, stored)
}

// stored records an event store's outcome and wakes a worker for a new one.
func (e *Engine) stored(err error, stored bool) {
	switch {
	case err != nil:
		e.emits.Fail("store event", err)
	case stored:
		e.emits.Progress()
		e.signal()
	}
}

// Cut shortens s to at most max bytes on a UTF-8 boundary.
func Cut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}

// Truncate cuts every string inside data["fields"] to fieldMax bytes and
// lists the cut paths, sorted, as data["truncated"].
func Truncate(data map[string]any, fieldMax int) {
	fields, ok := data["fields"].(map[string]any)
	if !ok {
		return
	}
	truncated := []string{}
	for key, value := range fields {
		fields[key] = cutValue(value, key, fieldMax, &truncated)
	}
	if len(truncated) > 0 {
		sort.Strings(truncated)
		data["truncated"] = truncated
	}
}

// cutValue cuts the strings in value (at path) to max bytes, appending each
// cut path to truncated.
func cutValue(value any, path string, max int, truncated *[]string) any {
	switch v := value.(type) {
	case string:
		if len(v) > max {
			*truncated = append(*truncated, path)
			return Cut(v, max)
		}
	case map[string]any:
		for k, x := range v {
			v[k] = cutValue(x, path+"."+k, max, truncated)
		}
	case []any:
		for i, x := range v {
			v[i] = cutValue(x, fmt.Sprintf("%s.%d", path, i), max, truncated)
		}
	}
	return value
}

// Fields is a write's JSON as an event's fields: an object as it is,
// anything else under "value" (invalid JSON as its text).
func Fields(raw json.RawMessage) map[string]any {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) || dec.Decode(&value) != nil {
		value = string(raw)
	}
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	return map[string]any{"value": value}
}

// Body is the delivered envelope of d at cursor. Over bodyMax it drops the
// fields, then cuts path and key to 256 bytes, listing each in truncated.
// Converting at delivery keeps old queued events in the same shape as new.
func Body(d model.NotificationDelivery, cursor int64, bodyMax int) []byte {
	body := map[string]any{"eventId": d.EventID, "name": d.Name, "timestamp": d.CreatedAt.UTC().Format(time.RFC3339), "cursor": fmt.Sprintf("c_%d", cursor), "data": d.Data}
	raw, _ := json.Marshal(body)
	if len(raw) <= bodyMax {
		return raw
	}
	data := Fields(d.Data)
	delete(data, "fields")
	data["truncated"] = []string{"fields"}
	body["data"] = data
	raw, _ = json.Marshal(body)
	if len(raw) <= bodyMax {
		return raw
	}
	truncated := []string{"fields"}
	for _, key := range []string{"path", "key"} {
		if value, ok := data[key].(string); ok && len(value) > 256 {
			data[key] = Cut(value, 256)
			truncated = append(truncated, key)
		}
	}
	data["truncated"] = truncated
	raw, _ = json.Marshal(body)
	return raw
}
