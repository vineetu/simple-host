package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

const opaqueOwner = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
const opaqueSite = "2b4e28ba-2fa1-11d2-883f-0016d3cca427"
const opaqueTeam = "3b4e28ba-2fa1-11d2-883f-0016d3cca427"

func assertOpaqueToken(t *testing.T, token string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{opaqueOwner, opaqueSite, opaqueTeam} {
		uuidBytes, _ := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
		for _, representation := range []string{id, strings.ReplaceAll(id, "-", ""), hex.EncodeToString([]byte(id)), base64.RawURLEncoding.EncodeToString([]byte(id)), base64.StdEncoding.EncodeToString([]byte(id)), base64.RawURLEncoding.EncodeToString(uuidBytes)} {
			if strings.Contains(token, representation) || bytes.Contains(raw, []byte(representation)) {
				t.Fatal("token reveals an internal identifier")
			}
		}
		if bytes.Contains(raw, uuidBytes) {
			t.Fatal("decoded token reveals UUID bytes")
		}
	}
}

func TestOpaqueLinkPayloadsAndAuthentication(t *testing.T) {
	key := newExportKey()
	h := &SiteHandler{exportKey: key}
	hack := &HackHandler{archiveLinkKey: key}
	now := time.Now().Truncate(time.Second)
	expiry := now.Add(time.Minute)
	claim := hackArchiveClaim{UserID: opaqueOwner, EventID: opaqueSite, TeamID: opaqueTeam, Slug: "event", Kind: "own-team", Expiry: expiry.Unix()}
	storage := storageCall{ownerID: opaqueOwner, siteID: opaqueSite, resourceName: "photos", linkScope: "team:" + opaqueTeam}
	cases := []struct {
		name, domain string
		mint         func() string
	}{
		{"export", exportLinkDomain, func() string { return h.signExportToken(opaqueOwner, opaqueSite, expiry) }},
		{"preview", previewLinkDomain, func() string { return h.signPreviewToken(opaqueSite, 2, expiry) }},
		{"file", "site storage file v1", func() string { return h.storageFileToken(storage, "a/photo.png", expiry) }},
		{"hack", hackArchiveLinkDomain, func() string { return hack.signHackArchiveClaim(claim) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.mint()
			assertOpaqueToken(t, token)
			if token == tc.mint() {
				t.Error("nonce reused")
			}
			plain, err := openLink(key, tc.domain, token)
			if err != nil || !bytes.Contains(plain, []byte(opaqueSite)) {
				t.Fatal("payload failed to round trip")
			}
			raw, _ := base64.RawURLEncoding.DecodeString(token)
			raw[len(raw)/2] ^= 1
			for _, bad := range []string{"", token[:len(token)/2], base64.RawURLEncoding.EncodeToString(raw)} {
				if _, err := openLink(key, tc.domain, bad); err == nil {
					t.Error("tampered token accepted")
				}
			}
			if _, err := openLink(newExportKey(), tc.domain, token); err == nil {
				t.Error("wrong key accepted")
			}
			if _, err := openLink(key, tc.domain+"other", token); err == nil {
				t.Error("wrong link kind accepted")
			}
		})
	}
	if owner, site, err := h.checkExportToken(cases[0].mint(), now); err != nil || owner != opaqueOwner || site != opaqueSite {
		t.Fatal("export binding lost")
	}
	if site, version, err := h.checkPreviewToken(cases[1].mint(), now); err != nil || site != opaqueSite || version != 2 {
		t.Fatal("preview binding lost")
	}
	if got, ok := hack.checkHackArchiveClaim(cases[3].mint(), now); !ok || got != claim {
		t.Fatal("archive binding lost")
	}
	raw, err := openLink(key, "site storage file v1", cases[2].mint())
	expected := opaqueOwner + "." + opaqueSite + ".photos." + base64.RawURLEncoding.EncodeToString([]byte("a/photo.png")) + "."
	if err != nil || !strings.HasPrefix(string(raw), expected) || !strings.HasSuffix(string(raw), "."+base64.RawURLEncoding.EncodeToString([]byte(storage.linkScope))) {
		t.Fatal("file scope lost")
	}
	for _, exp := range []time.Time{now.Add(-time.Second), now.Add(previewLinkTTL() + time.Minute)} {
		if _, _, err := h.checkPreviewToken(h.signPreviewToken(opaqueSite, 2, exp), now); err == nil {
			t.Error("preview expiry accepted")
		}
	}
	for _, exp := range []time.Time{now.Add(-time.Second), now.Add(hackArchiveLinkTTL() + time.Minute)} {
		bad := claim
		bad.Expiry = exp.Unix()
		if _, ok := hack.checkHackArchiveClaim(hack.signHackArchiveClaim(bad), now); ok {
			t.Error("archive expiry accepted")
		}
	}
	// Invalid file tokens must fail before any database access.
	for _, token := range []string{"bad", h.storageFileToken(storage, "a/photo.png", now.Add(-time.Minute)), h.storageFileToken(storage, "a/photo.png", now.Add(time.Hour))} {
		w := httptest.NewRecorder()
		h.downloadStorageFile(w, httptest.NewRequest("GET", "/v1/storage-download?token="+token, nil))
		if w.Code != 404 {
			t.Errorf("invalid file link: %d", w.Code)
		}
	}
}

func TestMCPNotificationVisitorReferences(t *testing.T) {
	previous := hackMode
	hackMode = false
	t.Cleanup(func() { hackMode = previous })
	h := &SiteHandler{exportKey: newExportKey()}
	refs := mcp.NewVisitorReferences(h.exportKey)
	expected, _ := refs.Encode(opaqueSite, opaqueOwner)
	for _, name := range []string{"record.added", "record.changed", "file.uploaded"} {
		for _, visitor := range []any{opaqueOwner, nil} {
			raw, _ := json.Marshal(map[string]any{"visitor": visitor, "site": "blog", "fields": map[string]any{"item": "cake"}})
			d := db.NotificationDelivery{EventID: "evt_test", Name: name, Data: raw}
			for _, source := range []string{"mcp", "webhook"} {
				body := h.notificationBody(db.Notification{Source: source, SiteID: opaqueSite}, d, 1)
				var payload struct {
					Data map[string]any `json:"data"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				want := visitor
				if source == "mcp" && visitor != nil {
					want = expected
				}
				if payload.Data["visitor"] != want {
					t.Fatalf("%s %s visitor = %v", name, source, payload.Data["visitor"])
				}
				if source == "mcp" && bytes.Contains(body, []byte(opaqueOwner)) {
					t.Fatal("notification leaks account ID")
				}
				if !bytes.Equal(d.Data, raw) {
					t.Fatal("stored event mutated")
				}
			}
		}
	}
	hackMode = true
	raw, _ := json.Marshal(map[string]any{"visitor": opaqueOwner})
	body := h.notificationBody(db.Notification{Source: "mcp", SiteID: opaqueSite}, db.NotificationDelivery{Data: raw}, 1)
	if !bytes.Contains(body, []byte(opaqueOwner)) {
		t.Fatal("Hack notification changed")
	}
}

func TestConnectorUsesSiteVisitorKey(t *testing.T) {
	h := &SiteHandler{exportKey: newExportKey()}
	connector := &ConnectorHandler{}
	connector.SetVisitorReferences(h)
	ref, err := connector.mcpConfig.VisitorRefs.Encode(opaqueSite, opaqueOwner)
	expected, _ := mcp.NewVisitorReferences(h.exportKey).Encode(opaqueSite, opaqueOwner)
	if err != nil || ref != expected {
		t.Fatal("connector and notification references differ")
	}
}
