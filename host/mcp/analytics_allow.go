package mcp

import "strings"

// A product tool's telemetry copy keeps a string only under a key listed
// here; every other string becomes "[removed]". Numbers, booleans and null
// are always kept, and the keys themselves are kept. So a new field that
// holds text a person typed (a judge's note, a message) never leaves unless
// someone adds its key here on purpose. Add a key only when its values are
// chosen from a fixed set or name a thing, never a person. Platform tools
// are not affected: their copies are shaped by the field rules in
// analytics_sanitize.go.

// analyticsAllowedKeys are allowed by exact name.
var analyticsAllowedKeys = map[string]bool{
	// What a call is about.
	"slug": true, "site": true, "owner": true, "event": true, "team": true, "track": true,
	"tracks": true, "teams": true, "id": true, "ids": true, "path": true, "filename": true,
	"website_site": true, "site_url": true, "teams_under_target": true,
	// Names and titles of things (people's names are removed before this).
	"name": true, "title": true, "prize": true, "tier": true,
	// Values chosen from a fixed set.
	"stage": true, "stages": true, "stages_offered": true, "role": true, "roles": true,
	"status": true, "state": true, "mode": true, "kind": true, "type": true, "action": true,
	"decision": true, "content_type": true, "visibility": true, "topic": true, "tab": true,
	"section": true, "preset": true, "panel": true, "panels": true, "screen": true,
	"refusal": true, "theme": true, "maturity": true, "entry_required": true,
	"tie_decider": true, "joined_via": true, "video_host": true, "code_host": true,
	"fix_target": true, "stage_rule": true, "precedence": true,
	// Times.
	"deadline": true, "time_zone": true, "version": true,
}

// analyticsAllowedSuffixes allow a key by its ending: team_slug, event_stage,
// approval_status, score_mode, created_at, team_id, track_name, event_title.
var analyticsAllowedSuffixes = []string{
	"_slug", "_slugs", "_stage", "_status", "_mode", "_kind", "_type", "_role",
	"_visibility", "_at", "_deadline", "_id", "_ids", "_name", "_title", "_version",
}

func analyticsAllowedKey(key string) bool {
	key = strings.ToLower(key)
	return analyticsAllowedKeys[key] || analyticsEndsWithAny(key, analyticsAllowedSuffixes)
}

// analyticsAllowed is a product string field's value in the copy: kept and
// scrubbed under an allowed key, else removed. A list of strings is judged
// item by item under the list's key; anything else is walked as usual.
func analyticsAllowed(policy toolPolicy, key string, field any, depth int) any {
	allowed := analyticsAllowedKey(key)
	keep := func(text string) any {
		if data, ok := analyticsDataURI(text); ok {
			return data
		}
		if !allowed {
			return "[removed]"
		}
		return scrubAnalyticsText(text)
	}
	switch v := field.(type) {
	case string:
		return keep(v)
	case []any:
		for i, item := range v {
			if text, ok := item.(string); ok {
				v[i] = keep(text)
			} else {
				v[i] = dropAnalyticsValue(policy, item, depth+1)
			}
		}
		return v
	}
	return dropAnalyticsValue(policy, field, depth)
}
