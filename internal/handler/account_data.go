package handler

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// Download my data and Delete my account (GDPR self-service, owner decision
// 2026-09-27).
//
//   GET    /v1/me/export.zip     one .zip of everything held about the
//                                caller (/v1/me/export.tar.gz serves the same
//                                zip): every site's export, those in Recently
//                                deleted too, the
//                                account, keys (never the keys themselves),
//                                connected apps, and what they did as a
//                                visitor: sign-ins, entries sent to other
//                                people's lists and changes made to their
//                                saved data while signed in. Writes made
//                                without signing in, and public-list entries
//                                sent before authors were recorded, carry no
//                                link to anyone.
//   DELETE /v1/me                {"confirm": "<handle, or email with no handle>"}
//                                deletes the account and all its data at once
//                                and for good, including the items they sent
//                                to other people's lists while signed in.
//
// DELETE /v1/admin/users/{id} (accounts.go) runs the same erasure.

// ---- delete ----------------------------------------------------------------

type deleteMeRequest struct {
	Confirm string `json:"confirm"`
}

// deleteMe handles DELETE /v1/me.
func (h *SiteHandler) deleteMe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if user.IsAdmin || user.ID == h.adminUserID {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "an admin account cannot delete itself here", Code: "admin_account"})
		return
	}
	// Only the person's own key, never a connected app acting for them: an
	// app reading visitor-written content must not be talked into this.
	if accountKeyUser(w, r, "deleting the account") == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req deleteMeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"confirm": "<your handle>"} (your email if the account has no handle)`, Code: "confirm_required"})
		return
	}
	// Every site's lock first, then the account row: a deploy, rollback,
	// rename or take-down in flight finishes before the erasure, and one that
	// was waiting finds the site gone.
	unlock, err := h.lockAccountSites(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer unlock()
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	acct, err := db.LockAccountForDelete(r.Context(), tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if acct.IsAdmin || acct.ID == h.adminUserID {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "an admin account cannot delete itself here", Code: "admin_account"})
		return
	}
	// The operator is handling a suspended account; it stays as it is.
	if acct.Suspended {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: supportText("this account is suspended; write to support@simple-host.app about deleting it"), Code: "account_suspended"})
		return
	}
	// A site the operator took down stays as it is (its owner cannot delete
	// it), so the account that holds it cannot be deleted here either.
	if acct.TakenDown > 0 {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: supportText("a site of this account was taken down; write to support@simple-host.app about deleting the account"), Code: "site_suspended"})
		return
	}
	if acct.EventClaims > 0 {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this account still holds event hostnames; release them first so their DNS records are removed", Code: "event_hostnames"})
		return
	}
	want := acct.Handle
	what := "your handle"
	if want == "" {
		want, what = acct.Username, "your email"
	}
	if !strings.EqualFold(strings.TrimSpace(req.Confirm), want) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "type " + what + " (" + want + ") as confirm to delete the account", Code: "confirm_mismatch"})
		return
	}
	erased, err := db.EraseAccount(r.Context(), tx, acct)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("delete account %s: %v", acct.ID, err)
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if err := h.eraseAccountFiles(r.Context(), erased); err != nil {
		log.Printf("delete account %s: files: %v", acct.ID, err)
	}
	if strings.Contains(acct.Username, "@") {
		go h.emailAccountDeleted(acct.Username)
	}
	log.Printf("account %s deleted by its owner", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// lockAccountSites takes the per-site lock (site.go lockSite) of every site
// the account has, live and Recently deleted, in the same sorted order rename
// uses, and returns the func that releases them all. A rename that lands
// between listing and locking changes the names, so the list is read again
// under the locks and the locks retaken until it holds still (a few tries;
// the account row lock in the erasure then stops anything new).
func (h *SiteHandler) lockAccountSites(ctx context.Context, userID string) (func(), error) {
	var unlocks []func()
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
		unlocks = nil
	}
	names, err := db.AccountSiteNames(ctx, h.database, userID)
	for try := 0; err == nil; try++ {
		sort.Strings(names)
		for _, n := range names {
			unlocks = append(unlocks, h.lockSite(userID, n))
		}
		var again []string
		again, err = db.AccountSiteNames(ctx, h.database, userID)
		if err != nil {
			break
		}
		sort.Strings(again)
		if slices.Equal(names, again) || try == 4 {
			return release, nil
		}
		release()
		names = again
	}
	release()
	return func() {}, err
}

// eraseAccountFiles removes on disk what db.EraseAccount removed from the
// database. It runs after the commit, never before: the other order means a
// commit failure leaves a live account whose content is gone. A failure here
// strands files an operator can delete. The caller holds the sites' locks.
//
// A domain link goes only while it still points at this account's site, and
// its certificate request is withdrawn only then and while no other site has
// taken the domain up since the commit. The take-down markers live in the
// site folders and go with them.
func (h *SiteHandler) eraseAccountFiles(ctx context.Context, e db.ErasedAccount) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, s := range e.Sites {
		keep(h.disk.PurgeTrashedSiteLinks(e.UserID, s.Name, ""))
		for _, d := range s.Domains {
			mine, err := h.disk.UnbindDomainOf(d, e.UserID, s.Name)
			keep(err)
			if err != nil || !mine {
				continue
			}
			if _, err := db.GetSiteByCustomDomain(ctx, h.database, d); errors.Is(err, sql.ErrNoRows) {
				h.cancelDomainCert(d)
			}
		}
	}
	for _, a := range e.Aliases {
		keep(h.disk.RemoveHandleLinkOf(a, e.UserID))
	}
	// Address families: the link (only while it is still this account's)
	// and the issuer's request, so nothing is served there any more.
	for _, f := range e.Families {
		if h.disk.FamilyLinkTarget(f) == filepath.Join("..", "by-id", e.UserID) {
			keep(h.disk.UnbindFamily(f))
		}
		h.removeFamilyRequest(f)
	}
	if len(e.Families) > 0 {
		h.refreshFamilies(ctx)
	}
	keep(h.disk.DeleteUser(e.UserID, e.Handle))
	return first
}

func (h *SiteHandler) emailAccountDeleted(to string) {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return
	}
	text := `Your Simple Host account and all its data were deleted.

Your sites, their files and saved data, your keys and connected apps, and the entries you sent to other people's lists while signed in are gone for good. Your address is not given to anyone else.

Changes you made to other people's page data stay with their sites, with your address taken off them. Entries sent without signing in, and older public-list entries, were never linked to you and stay. For help with those, write to support@simple-host.app.

If you did not ask for this, write to support@simple-host.app.

Simple Host
`
	if err := mailer.SendNotice(to, "Your Simple Host account was deleted", supportText(text)); err != nil {
		log.Printf("account deleted: confirmation email: %v", err)
	}
}

// ---- export ----------------------------------------------------------------

// exportMe handles GET /v1/me/export.zip (and the older
// /v1/me/export.tar.gz address, which serves the same zip): streamed, like the per-site
// export, so a large account never sits in memory.
func (h *SiteHandler) exportMe(w http.ResponseWriter, r *http.Request) {
	// Only the person's own key, as for delete: a connected app must not be
	// able to pull the whole account in one archive.
	user := accountKeyUser(w, r, "downloading all your data")
	if user == nil {
		return
	}
	ctx := r.Context()
	me, err := db.GetUserByID(ctx, h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Everything small is read before the first byte goes out, so a database
	// error is still an honest 500 rather than a truncated archive.
	docs, err := h.accountDocuments(ctx, me)
	if err != nil {
		log.Printf("export account %s: %v", me.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	sites, err := db.ListSitesByUser(ctx, h.database, me.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	deleted, err := db.ListDeletedSitesByUser(ctx, h.database, me.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	name := me.Handle.String
	if name == "" {
		name = "account"
	}
	root := "simple-host-" + name + "-" + time.Now().UTC().Format("2006-01-02")
	// One .zip (owner decision 2026-09-27): it opens with a double click on
	// every computer. The old .tar.gz address serves the same zip.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, root))
	zw := zip.NewWriter(w)
	defer zw.Close()
	put := zipPut(zw)
	for _, d := range docs {
		if err := putBytes(put, root+"/"+d.name, d.body); err != nil {
			return
		}
	}
	for _, s := range sites {
		current := filepath.Join(h.disk.SiteDir(s.UserID, s.Name), "current")
		if err := h.writeSiteArchive(ctx, put, root+"/sites/"+s.Name, s.ID, current); err != nil {
			return // the client went away, or the stream broke
		}
	}
	// Sites in Recently deleted, in their own folder so nobody mistakes one
	// for a live site, each with when it goes for good.
	for _, d := range deleted {
		prefix := root + "/recently-deleted/" + d.Name
		info, _ := json.MarshalIndent(map[string]any{
			"site": d.Name, "deleted_at": d.DeletedAt.UTC(), "removed_for_good_at": d.PurgeAt().UTC(),
		}, "", "  ")
		if err := putBytes(put, prefix+"/deleted.json", info); err != nil {
			return
		}
		current := filepath.Join(h.disk.TrashDir(d.UserID, d.ID), "current")
		if err := h.writeSiteArchive(ctx, put, prefix, d.ID, current); err != nil {
			return
		}
	}
}

type exportDoc struct {
	name string
	body []byte
}

// accountDocuments builds README.txt, account.json, keys.json,
// connected_apps.json and visitor.json.
func (h *SiteHandler) accountDocuments(ctx context.Context, me db.User) ([]exportDoc, error) {
	aliases, err := db.ListHandleAliases(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	addrs, err := db.ListAccountAddresses(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	retired, err := db.ListRetiredNames(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	idents, err := db.ListSignInIdentities(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	keys, err := db.ListAPIKeys(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	conns, err := db.ListOAuthConnections(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	signIns, err := db.ListVisitorSignIns(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	subs, err := db.ListSubmissionsElsewhere(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}
	changes, err := db.ListChangesElsewhere(ctx, h.database, me.ID)
	if err != nil {
		return nil, err
	}

	type claimed struct {
		Name    string `json:"name"`
		Site    string `json:"site,omitempty"`
		Retired bool   `json:"retired,omitempty"`
	}
	type domain struct {
		Domain string `json:"domain"`
		Site   string `json:"site"`
		Status string `json:"status,omitempty"`
	}
	claimedNames := []claimed{}
	customDomains := []domain{}
	for _, a := range addrs {
		for _, d := range []string{a.Domain, a.Previous} {
			if d == "" {
				continue
			}
			status := a.Status
			if d == a.Previous {
				status = "earlier address, still served while the new one is pending"
			}
			if h.isFreeName(d) {
				claimedNames = append(claimedNames, claimed{Name: d, Site: a.Site})
			} else {
				customDomains = append(customDomains, domain{Domain: d, Site: a.Site, Status: status})
			}
		}
	}
	for _, n := range retired {
		claimedNames = append(claimedNames, claimed{Name: n.Name, Site: n.Site, Retired: true})
	}
	type identity struct {
		Provider string    `json:"provider"`
		Email    string    `json:"email,omitempty"`
		LinkedAt time.Time `json:"linked_at"`
	}
	ids := []identity{}
	for _, i := range idents {
		ids = append(ids, identity{Provider: i.Provider, Email: i.Email, LinkedAt: i.LinkedAt.UTC()})
	}
	if aliases == nil {
		aliases = []string{}
	}
	account := map[string]any{
		"handle":          me.Handle.String,
		"display_name":    me.DisplayName.String,
		"created_at":      me.CreatedAt.UTC(),
		"handle_aliases":  aliases,
		"claimed_names":   claimedNames,
		"custom_domains":  customDomains,
		"sign_in_methods": ids,
	}
	// The account's address families (*.<domain> for every site).
	if fams, err := db.ListFamiliesByUser(ctx, h.database, me.ID); err == nil {
		out := []map[string]any{}
		for _, f := range fams {
			m := map[string]any{"family": "*." + f.Suffix, "site_prefix": f.SitePrefix, "status": f.Status,
				"main_address": f.Canonical, "connected_at": f.BoundAt.UTC()}
			if f.VerifiedAt.Valid {
				m["verified_at"] = f.VerifiedAt.Time.UTC()
			}
			out = append(out, m)
		}
		account["address_families"] = out
	}
	if strings.Contains(me.Username, "@") {
		account["email"] = me.Username
	} else {
		account["account_name"] = me.Username
	}
	if p := h.PersonPageURL(me.Handle.String); p != "" {
		account["address"] = p
	}

	type key struct {
		Name       string     `json:"name"`
		Last4      string     `json:"last4,omitempty"`
		Scope      string     `json:"scope"`
		CreatedAt  time.Time  `json:"created_at"`
		LastUsedAt *time.Time `json:"last_used_at"`
		ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	}
	keyList := []key{}
	for _, k := range keys {
		name := k.Name
		if name == "" {
			name = "Earlier key"
		}
		keyList = append(keyList, key{Name: name, Last4: k.Last4, Scope: k.Scope, CreatedAt: k.CreatedAt.UTC(), LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt})
	}

	type app struct {
		Name        string    `json:"name"`
		Device      string    `json:"device,omitempty"`
		ConnectedAt time.Time `json:"connected_at"`
		LastUsedAt  time.Time `json:"last_used_at"`
	}
	apps := []app{}
	for _, c := range conns {
		apps = append(apps, app{Name: c.ClientName, Device: c.Device, ConnectedAt: c.ConnectedAt.UTC(), LastUsedAt: c.LastUsedAt.UTC()})
	}

	type signIn struct {
		Site        string    `json:"site"`
		FirstSignIn time.Time `json:"first_sign_in"`
		LastSignIn  time.Time `json:"last_seen"`
	}
	type submission struct {
		Site      string          `json:"site"`
		List      string          `json:"list"`
		ID        int64           `json:"id"`
		CreatedAt time.Time       `json:"created_at"`
		Item      json.RawMessage `json:"item"`
	}
	addrCache := map[string]string{}
	siteAddr := func(siteID, fallback string) string {
		if a, ok := addrCache[siteID]; ok {
			return a
		}
		a, ok := h.siteAddressFor(ctx, siteID, "")
		if !ok {
			a = fallback
		}
		addrCache[siteID] = a
		return a
	}
	signInList := []signIn{}
	for _, v := range signIns {
		signInList = append(signInList, signIn{Site: siteAddr(v.SiteID, "https://"+v.Host+"/"), FirstSignIn: v.FirstSeen.UTC(), LastSignIn: v.LastSeen.UTC()})
	}
	subList := []submission{}
	for _, s := range subs {
		subList = append(subList, submission{Site: siteAddr(s.SiteID, "(a site that has since been deleted)"), List: s.Collection, ID: s.ID, CreatedAt: s.CreatedAt.UTC(), Item: json.RawMessage(s.Data)})
	}
	// Changes to other people's saved data made while signed in: where,
	// what and when, never the data (it is the site owner's).
	type change struct {
		Site   string    `json:"site"`
		What   string    `json:"what"`
		ItemID *int64    `json:"item_id,omitempty"`
		Change string    `json:"change"`
		At     time.Time `json:"at"`
	}
	changeList := []change{}
	for _, c := range changes {
		what := "page data"
		if c.Kind == db.HistoryList {
			what = "list " + c.Name
		}
		changeList = append(changeList, change{Site: siteAddr(c.SiteID, "(a site that has since been deleted)"), What: what, ItemID: c.ItemID, Change: c.Op, At: c.At.UTC()})
	}
	visitor := map[string]any{"signed_in_to": signInList, "submitted": subList, "changed": changeList}

	out := []exportDoc{{name: "README.txt", body: []byte(exportReadme(h.savedData.UndoDays))}}
	for _, d := range []struct {
		name string
		v    any
	}{
		{"account.json", account},
		{"keys.json", keyList},
		{"connected_apps.json", apps},
		{"visitor.json", visitor},
	} {
		b, err := json.MarshalIndent(d.v, "", "  ")
		if err != nil {
			return nil, err
		}
		out = append(out, exportDoc{name: d.name, body: b})
	}
	return out, nil
}

// supportText puts this install's contact (auth.SupportContact) where s
// names the hosted service's support address.
func supportText(s string) string {
	if auth.SupportContact == "support@simple-host.app" {
		return s
	}
	return strings.ReplaceAll(s, "support@simple-host.app", auth.SupportContact)
}

// exportReadme is README.txt at the top of Download my data. undoDays is
// SAVED_DATA_UNDO_DAYS, how far back visitor.json's changes go.
func exportReadme(undoDays int) string {
	return supportText(strings.ReplaceAll(exportReadmeText, "{undo_days}", config.Count(undoDays, "day")))
}

const exportReadmeText = `Your Simple Host data
=====================

This .zip holds everything Simple Host keeps about your account.

account.json         Your email, handle, display name, when the account was
                     made, your earlier handles, the names your sites claimed
                     (retired ones too), custom domains, and the Google or
                     GitHub sign-ins linked to it.
keys.json            Your API keys: name, last 4 characters, when made and
                     last used. The keys themselves are never stored, so they
                     are not here.
connected_apps.json  Apps connected through the Simple Host connector
                     (ChatGPT, Claude, Grok): name, when connected, last used.
visitor.json         Sites where you are signed in as a visitor (first sign-in
                     and last seen; a sign-in is kept only until it expires),
                     every entry you sent to other people's lists while
                     signed in ("submitted"), and every change you made to
                     other people's page data or list entries while signed
                     in, in the last {undo_days} ("changed": which site, what and
                     when; the site's owner sees your address next to each).
                     Changes made without signing in, and older public-list
                     entries, are not linked to you, so they are not here;
                     for help with those, write to support@simple-host.app.
sites/<name>/        One folder per site, the same as that site's download:
                     files/ (the live version), state.json (saved data) and
                     collections.json (every list, with each entry's id, time
                     and who sent it, when they were signed in).
recently-deleted/<name>/
                     Sites in Recently deleted, laid out the same way, plus
                     deleted.json (when it was deleted and when it is removed
                     for good). Restore one from your page before then to
                     keep it.

Visitor analytics hold no personal data: visitors are counted by a salted
hash, never by address, so there is nothing about you to export.

To delete your account and all of this, use "Delete my account" on your page,
or DELETE /v1/me. Anything else: support@simple-host.app.
`
