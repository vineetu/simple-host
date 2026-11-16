package storage

import (
	"bufio"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

//go:embed storage-notice.json
var defaultNotice []byte

type NoticeTemplates struct {
	Presets     string `json:"presets"`
	Legacy      string `json:"legacy"`
	SaveWarning string `json:"save_warning"`
	RulesGuide  string `json:"rules_guide"`
	MovingGuide string `json:"moving_guide"`
}

// LoadNoticeTemplates permits operator wording changes without rebuilding.
// This file is outside hosted site directories and is never served.
func LoadNoticeTemplates(dataDir string) (NoticeTemplates, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "storage-notice.json"))
	if errors.Is(err, os.ErrNotExist) {
		raw = defaultNotice
		err = nil
	}
	if err != nil {
		return NoticeTemplates{}, err
	}
	var t NoticeTemplates
	err = json.Unmarshal(raw, &t)
	return t, err
}

func (t NoticeTemplates) Render(text string, at time.Time) string {
	date := "<DATE>"
	if !at.IsZero() {
		date = at.UTC().Format(time.RFC3339)
	}
	return strings.NewReplacer("<DATE>", date, "<RULES_GUIDE_URL>", t.RulesGuide, "<MOVING_GUIDE_URL>", t.MovingGuide).Replace(text)
}

type NoticeResource struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Allows string `json:"allows_today"`
}
type NoticeSite struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Resources []NoticeResource `json:"resources,omitempty"`
	Evidence  []string         `json:"evidence,omitempty"`
}
type NoticeRecipient struct {
	AccountID string       `json:"account_id"`
	Account   string       `json:"account"`
	Address   string       `json:"address"`
	Sites     []NoticeSite `json:"sites"`
}
type HeldNotice struct {
	Event     string     `json:"event"`
	AccountID string     `json:"account_id"`
	Site      NoticeSite `json:"site"`
}
type NoticeReport struct {
	Presets         []NoticeRecipient `json:"presets"`
	Legacy          []NoticeRecipient `json:"older_saved_data"`
	Held            []HeldNotice      `json:"held_by_an_event"`
	PresetResources int               `json:"preset_resource_count"`
	PresetDraft     string            `json:"preset_draft"`
	LegacyDraft     string            `json:"older_saved_data_draft"`
}

// NoticeGroup returns an event label or sql.ErrNoRows for an ordinary account.
// The product supplies the lookup; the platform never queries product tables.
type NoticeGroup func(context.Context, *sql.DB, string) (string, error)

var legacyCallRE = regexp.MustCompile(`\blegacy_storage_call site=([0-9a-fA-F-]{36})(?:\s|$)`)

func LegacyNoticeCalls(r io.Reader) ([]string, error) {
	if r == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		if m := legacyCallRE.FindStringSubmatch(scan.Text()); m != nil {
			seen[m[1]] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, scan.Err()
}

func noticeAllows(x rules.Resource) string {
	m := x.Access
	if m == nil {
		legacy := rules.LegacyMatrix(x.Read, x.Write, x.WriteMode)
		m = &legacy
	}
	who := func(v string) string {
		switch v {
		case "anyone":
			return "any visitor"
		case "signed-in":
			return "signed-in visitors"
		case "own":
			return "each signed-in visitor, for their own entries"
		case "checked":
			return "visitors who pass the spam check"
		case "owner":
			return "only you"
		default:
			return "no page caller"
		}
	}
	describe := func(m rules.AccessMatrix) string {
		return fmt.Sprintf("read: %s; add: %s; edit: %s; delete: %s", who(m.Read), who(m.Add), who(m.Edit), who(m.Delete))
	}
	text := describe(*m)
	tables := make([]string, 0, len(x.Tables))
	for name := range x.Tables {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	for _, name := range tables {
		text += "; table " + name + ": " + describe(x.Tables[name].Matrix)
	}
	if x.ApproveFirst {
		text += "; new visitor posts submitted through the spam check wait for your approval"
	}
	return text
}

// ListNoticeRecipients is read-only. Every run recomputes eligibility per resource.
func ListNoticeRecipients(ctx context.Context, db *sql.DB, legacyRoutes bool, calls io.Reader, group NoticeGroup, templates NoticeTemplates, at time.Time) (NoticeReport, error) {
	report := NoticeReport{Presets: []NoticeRecipient{}, Legacy: []NoticeRecipient{}, Held: []HeldNotice{}, PresetDraft: templates.Render(templates.Presets, at), LegacyDraft: templates.Render(templates.Legacy, at)}
	resources, err := repo.ConvertScope(ctx, db, nil)
	if err != nil {
		return report, err
	}
	report.PresetResources = len(resources)
	labels := map[string]string{}
	label := func(id string) (string, error) {
		if v, ok := labels[id]; ok {
			return v, nil
		}
		v := ""
		if group != nil {
			var e error
			v, e = group(ctx, db, id)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return "", e
			}
		}
		labels[id] = v
		return v, nil
	}
	add := func(list *[]NoticeRecipient, id, account, address string, site NoticeSite) {
		for i := range *list {
			if (*list)[i].AccountID == id {
				for j := range (*list)[i].Sites {
					if (*list)[i].Sites[j].ID == site.ID {
						(*list)[i].Sites[j].Resources = append((*list)[i].Sites[j].Resources, site.Resources...)
						return
					}
				}
				(*list)[i].Sites = append((*list)[i].Sites, site)
				return
			}
		}
		*list = append(*list, NoticeRecipient{AccountID: id, Account: account, Address: address, Sites: []NoticeSite{site}})
	}
	for _, r := range resources {
		x, e := repo.LoadResource(ctx, db, r.SiteID, r.Name)
		if e != nil {
			return report, e
		}
		if x.Kind == "sqlite" {
			x.Tables, e = repo.LoadTables(ctx, db, r.SiteID, r.Name)
			if e != nil {
				return report, e
			}
		}
		site := NoticeSite{ID: r.SiteID, Name: r.SiteName, Resources: []NoticeResource{{Name: r.Name, Kind: r.Kind, Allows: noticeAllows(x)}}}
		event, e := label(r.OwnerID)
		if e != nil {
			return report, e
		}
		if event != "" {
			report.Held = append(report.Held, HeldNotice{Event: event, AccountID: r.OwnerID, Site: site})
			continue
		}
		address, e := repo.NoticeAccount(ctx, db, r.OwnerID)
		if e != nil {
			return report, e
		}
		add(&report.Presets, r.OwnerID, r.Account, address, site)
	}
	if !legacyRoutes {
		return report, nil
	}
	ids, err := LegacyNoticeCalls(calls)
	if err != nil {
		return report, err
	}
	sites, err := repo.LegacyNoticeSites(ctx, db, ids)
	if err != nil {
		return report, err
	}
	for _, s := range sites {
		event, e := label(s.OwnerID)
		if e != nil {
			return report, e
		}
		if event != "" {
			continue
		}
		site := NoticeSite{ID: s.ID, Name: s.Name}
		if s.State {
			site.Evidence = append(site.Evidence, "shared state")
		}
		if s.Collections {
			site.Evidence = append(site.Evidence, "collections")
		}
		if s.Declared {
			site.Evidence = append(site.Evidence, "declared data")
		}
		if s.Calls {
			site.Evidence = append(site.Evidence, "legacy_storage_call")
		}
		add(&report.Legacy, s.OwnerID, s.Account, s.Address, site)
	}
	return report, nil
}

// WriteNoticeReport atomically replaces the recipient file with a root-owned
// 0600 file. Other users cannot generate this account-address report.
func WriteNoticeReport(path string, report NoticeReport) error {
	if os.Geteuid() != 0 {
		return errors.New("recipient reports must be generated as root")
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".storage-notice-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		var info os.FileInfo
		info, err = f.Stat()
		if err == nil {
			owner, ok := info.Sys().(*syscall.Stat_t)
			if !ok || owner.Uid != 0 || info.Mode().Perm() != 0600 {
				err = errors.New("recipient report must be owned by root with mode 0600")
			}
		}
	}
	if err == nil {
		_, err = f.Write(append(raw, '\n'))
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
