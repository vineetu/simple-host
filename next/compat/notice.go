package compat

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"simplehost/core/web"
)

// The notice switch, SH_COMPAT_NOTICE (see next/RUNBOOK.md): "off" (the
// default) translates silently; "on" tells people to move: the old skill on
// each answer, an old tool list once per MCP session (or, with no session
// id, once an hour per account). Counting and the Deprecation/Sunset/Link
// headers do not depend on it. Goes with this package.
func noticeOn() bool { return strings.TrimSpace(os.Getenv("SH_COMPAT_NOTICE")) == "on" }

// lastDay is the sunset day as people read it: the last day old ways work.
func lastDay() string { return Until().Add(-24 * time.Hour).Format("2 January 2006") }

func toolNotice() string {
	return "Simple Host has new tools. Start a new chat, or refresh the Simple Host connection, before " + lastDay() + ". Old tool names stop working then."
}

func skillNotice() string {
	return "Your Simple Host skill is out of date. Reinstall it from https://simple-host.app/skills.zip before " + lastDay() + "."
}

var told = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// toolNoticeDue reports whether this old-name tool answer carries the
// notice, and marks it sent.
func toolNoticeDue(r *http.Request) bool {
	if !noticeOn() {
		return false
	}
	key, every := "session "+r.Header.Get("Mcp-Session-Id"), 100*365*24*time.Hour
	if r.Header.Get("Mcp-Session-Id") == "" {
		key, every = "person "+web.CallerOf(r).ID, time.Hour
	}
	now := time.Now()
	told.Lock()
	defer told.Unlock()
	if at, ok := told.at[key]; ok && now.Sub(at) < every {
		return false
	}
	if len(told.at) > 10000 { // sessions end without saying so: forget a day's worth
		for k, at := range told.at {
			if now.Sub(at) > 24*time.Hour {
				delete(told.at, k)
			}
		}
	}
	told.at[key] = now
	return true
}
