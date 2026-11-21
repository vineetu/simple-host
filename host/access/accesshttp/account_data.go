package accesshttp

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/ops"

	datarepo "github.com/vsriram/simple-host/host/data/repo"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/model"
)

// Download my data and Delete my account (GDPR self-service).
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
func (h *AccessHandler) deleteMe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if accounts.IsOperator(user.IsAdmin, user.Username) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "an admin account cannot delete itself here", Code: "admin_account"})
		return
	}
	// Only the person's own key, never a connected app acting for them: an
	// app reading visitor-written content must not be talked into this.
	if accountKeyUser(w, r, "deleting the account") == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req deleteMeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `send {"confirm": "<your handle>"} (your email if the account has no handle)`, Code: "confirm_required"})
		return
	}
	deleted, err := h.cutoff().ErasePerson(r.Context(), accounts.DeleteAccountInput{ID: user.ID, Confirm: req.Confirm})
	if writeLifecycleFailure(w, err) {
		return
	}
	if strings.Contains(deleted.Username, "@") {
		go h.emailAccountDeleted(deleted.Username)
	}
	w.WriteHeader(http.StatusNoContent)
}

// cutoff is the one workflow that erases, deletes a team or suspends.
func (h *AccessHandler) cutoff() ops.Cutoff {
	return ops.Cutoff{DB: h.Database, Sites: h.sitesSvc(), Hosts: h.Hosts, Files: h.SiteFiles}
}

func (h *AccessHandler) emailAccountDeleted(to string) {
	mailer, ok := h.Mailer.(email.NoticeSender)
	if !ok {
		return
	}
	text := `Your Simple Host account and all its data were deleted.

Your sites, their files and saved data, your keys and connected apps, and the entries you sent to other people's lists while signed in are gone for good. Your address is not given to anyone else.

What you saved on other people's sites stays there, no longer linked to your name or email. Entries sent without signing in, and older public-list entries, were never linked to you and stay. For help with those, write to support@simple-host.app.

If you did not ask for this, write to support@simple-host.app.

Simple Host
`
	if err := mailer.SendNotice(to, "Your Simple Host account was deleted", web.SupportText(text)); err != nil {
		log.Printf("account deleted: confirmation email: %v", err)
	}
}

// ---- export ----------------------------------------------------------------

// exportMe handles GET /v1/me/export.zip (and the older
// /v1/me/export.tar.gz address, which serves the same zip): streamed, like the per-site
// export, so a large account never sits in memory.
func (h *AccessHandler) exportMe(w http.ResponseWriter, r *http.Request) {
	// Only the person's own key, as for delete: a connected app must not be
	// able to pull the whole account in one archive.
	user := accountKeyUser(w, r, "downloading all your data")
	if user == nil {
		return
	}
	a, err := h.loadAccountArchive(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// One .zip: it opens with a double click on every computer. The old .tar.gz address serves the same zip.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, a.root))
	if err := h.writeAccountArchive(r.Context(), zip.NewWriter(w), a); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// accountArchive is everything small in an account's download, read before
// the first byte goes out, so a database error is still an honest 500
// rather than a truncated archive.
type accountArchive struct {
	root    string
	docs    []exportDoc
	sites   []model.Site
	deleted []model.DeletedSite
}

func (h *AccessHandler) loadAccountArchive(ctx context.Context, userID string) (accountArchive, error) {
	me, err := h.StoreAccounts().GetUserByID(ctx, userID)
	if err != nil {
		return accountArchive{}, err
	}
	a := accountArchive{}
	if a.docs, err = h.accountDocuments(ctx, me); err != nil {
		log.Printf("export account %s: %v", me.ID, err)
		return a, err
	}
	if a.sites, err = h.sitesSvc().ListSitesByUser(ctx, me.ID); err != nil {
		return a, err
	}
	if a.deleted, err = h.sitesSvc().ListDeletedSitesByUser(ctx, me.ID); err != nil {
		return a, err
	}
	name := me.Handle.String
	if name == "" {
		name = "account"
	}
	a.root = "simple-host-" + name + "-" + time.Now().UTC().Format("2006-01-02")
	return a, nil
}

// writeAccountArchive streams the documents, every live site, and the sites
// in Recently deleted (in their own folder so nobody mistakes one for a live
// site, each with when it goes for good).
func (h *AccessHandler) writeAccountArchive(ctx context.Context, zw *zip.Writer, a accountArchive) error {
	put := appsites.ZipPut(zw)
	for _, d := range a.docs {
		if err := appsites.PutBytes(put, a.root+"/"+d.name, d.body); err != nil {
			return err
		}
	}
	for _, s := range a.sites {
		files := appsites.FileVersion{UserID: s.UserID, SiteName: s.Name}
		if err := h.SiteFiles.WriteArchive(ctx, put, a.root+"/sites/"+s.Name, s.ID, files); err != nil {
			return err
		}
	}
	for _, d := range a.deleted {
		prefix := a.root + "/recently-deleted/" + d.Name
		info, _ := json.MarshalIndent(map[string]any{
			"site": d.Name, "deleted_at": d.DeletedAt.UTC(), "removed_for_good_at": d.PurgeAt(h.Limits.DeletedRetention).UTC(),
		}, "", "  ")
		if err := appsites.PutBytes(put, prefix+"/deleted.json", info); err != nil {
			return err
		}
		files := appsites.FileVersion{UserID: d.UserID, SiteName: d.Name, TrashID: d.ID}
		if err := h.SiteFiles.WriteArchive(ctx, put, prefix, d.ID, files); err != nil {
			return err
		}
	}
	return zw.Close()
}

type exportDoc struct {
	name string
	body []byte
}

// accountDocuments builds README.txt, account.json, keys.json,
// connected_apps.json and visitor.json.
func (h *AccessHandler) accountDocuments(ctx context.Context, me model.User) ([]exportDoc, error) {
	account, err := h.accountExport(ctx, me)
	if err != nil {
		return nil, err
	}
	keys, err := h.StoreAccounts().ListAPIKeys(ctx, me.ID)
	if err != nil {
		return nil, err
	}
	conns, err := h.StoreAccounts().ListOAuthConnections(ctx, me.ID)
	if err != nil {
		return nil, err
	}
	visitor, err := h.visitorExport(ctx, me.ID)
	if err != nil {
		return nil, err
	}
	keyList, apps := exportKeys(keys), exportApps(conns)
	out := []exportDoc{{name: "README.txt", body: []byte(exportReadme())}}
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

type exportKey struct {
	Name       string     `json:"name"`
	Last4      string     `json:"last4,omitempty"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// exportKeys is keys.json: never a key itself, only its last four.
func exportKeys(keys []model.APIKey) []exportKey {
	out := []exportKey{}
	for _, k := range keys {
		name := k.Name
		if name == "" {
			name = "Earlier key"
		}
		out = append(out, exportKey{Name: name, Last4: k.Last4, Scope: k.Scope, CreatedAt: k.CreatedAt.UTC(), LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt})
	}
	return out
}

type exportApp struct {
	Name        string    `json:"name"`
	Device      string    `json:"device,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

// exportApps is connected_apps.json.
func exportApps(conns []model.OAuthConnection) []exportApp {
	out := []exportApp{}
	for _, c := range conns {
		out = append(out, exportApp{Name: c.ClientName, Device: c.Device, ConnectedAt: c.ConnectedAt.UTC(), LastUsedAt: c.LastUsedAt.UTC()})
	}
	return out
}

// accountExport is account.json: the handles, addresses and sign-in methods.
func (h *AccessHandler) accountExport(ctx context.Context, me model.User) (map[string]any, error) {
	aliases, err := h.StoreAccounts().ListHandleAliases(ctx, me.ID)
	if err != nil {
		return nil, err
	}
	addrs, err := sitesrepo.ListAccountAddresses(ctx, h.Database, me.ID)
	if err != nil {
		return nil, err
	}
	retired, err := sitesrepo.ListRetiredNames(ctx, h.Database, me.ID)
	if err != nil {
		return nil, err
	}
	idents, err := h.StoreAccounts().ListSignInIdentities(ctx, me.ID)
	if err != nil {
		return nil, err
	}
	if aliases == nil {
		aliases = []string{}
	}
	claimedNames, customDomains := exportAddresses(h.Hosts.Addressing(), addrs, retired)
	account := map[string]any{
		"handle":          me.Handle.String,
		"display_name":    me.DisplayName.String,
		"created_at":      me.CreatedAt.UTC(),
		"handle_aliases":  aliases,
		"claimed_names":   claimedNames,
		"custom_domains":  customDomains,
		"sign_in_methods": exportSignInMethods(idents),
	}
	// The account's address families (*.<domain> for every site).
	if fams, err := h.sitesSvc().ListFamiliesByUser(ctx, me.ID); err == nil {
		account["address_families"] = exportFamilies(fams)
	}
	if err := h.exportAccountName(ctx, me, account); err != nil {
		return nil, err
	}
	if p := h.Hosts.Addressing().PersonPageURL(me.Handle.String); p != "" {
		account["address"] = p
	}
	return account, nil
}

type exportClaimedName struct {
	Name    string `json:"name"`
	Site    string `json:"site,omitempty"`
	Retired bool   `json:"retired,omitempty"`
}

type exportDomain struct {
	Domain string `json:"domain"`
	Site   string `json:"site"`
	Status string `json:"status,omitempty"`
}

// exportAddresses splits the account's addresses (and the earlier ones still
// served while a new one is pending) into claimed free names and custom
// domains; retired names are claimed names too.
func exportAddresses(a appsites.Addressing, addrs []model.AccountAddress, retired []model.RetiredAccountName) ([]exportClaimedName, []exportDomain) {
	claimedNames := []exportClaimedName{}
	customDomains := []exportDomain{}
	for _, addr := range addrs {
		for _, d := range []string{addr.Domain, addr.Previous} {
			if d == "" {
				continue
			}
			status := addr.Status
			if d == addr.Previous {
				status = "earlier address, still served while the new one is pending"
			}
			if a.IsFreeName(d) {
				claimedNames = append(claimedNames, exportClaimedName{Name: d, Site: addr.Site})
			} else {
				customDomains = append(customDomains, exportDomain{Domain: d, Site: addr.Site, Status: status})
			}
		}
	}
	for _, n := range retired {
		claimedNames = append(claimedNames, exportClaimedName{Name: n.Name, Site: n.Site, Retired: true})
	}
	return claimedNames, customDomains
}

type exportIdentity struct {
	Provider string    `json:"provider"`
	Email    string    `json:"email,omitempty"`
	LinkedAt time.Time `json:"linked_at"`
}

func exportSignInMethods(idents []model.SignInIdentity) []exportIdentity {
	ids := []exportIdentity{}
	for _, i := range idents {
		ids = append(ids, exportIdentity{Provider: i.Provider, Email: i.Email, LinkedAt: i.LinkedAt.UTC()})
	}
	return ids
}

func exportFamilies(fams []model.AddressFamily) []map[string]any {
	out := []map[string]any{}
	for _, f := range fams {
		m := map[string]any{"family": "*." + f.Suffix, "site_prefix": f.SitePrefix, "status": f.Status,
			"main_address": f.Canonical, "connected_at": f.BoundAt.UTC()}
		if f.VerifiedAt.Valid {
			m["verified_at"] = f.VerifiedAt.Time.UTC()
		}
		out = append(out, m)
	}
	return out
}

// exportAccountName is the account's email, or its account name. A self
// sign-up's account_name is a synthetic "pw-..." value
// (CreatePasswordAccount), never what the account actually signs in with:
// its own login (account_passwords.login) is included too, the same field
// GET /v1/me and the admin page already show in its place (user.go's
// me/finishSignIn, site.go's adminAccounts).
func (h *AccessHandler) exportAccountName(ctx context.Context, me model.User, account map[string]any) error {
	if strings.Contains(me.Username, "@") {
		account["email"] = me.Username
	} else {
		account["account_name"] = me.Username
	}
	login, has, err := h.StoreAccounts().PasswordLogin(ctx, me.ID)
	if err != nil {
		return err
	}
	if has {
		account["login"] = login
	}
	return nil
}

// visitorExport is visitor.json: the sites signed in to, and the changes
// made there to other people's saved data.
func (h *AccessHandler) visitorExport(ctx context.Context, userID string) (map[string]any, error) {
	signIns, err := h.StoreAccounts().ListVisitorSignIns(ctx, userID)
	if err != nil {
		return nil, err
	}
	changes, err := datarepo.ListChangesElsewhere(ctx, h.Database, userID)
	if err != nil {
		return nil, err
	}
	type signIn struct {
		Site        string    `json:"site"`
		FirstSignIn time.Time `json:"first_sign_in"`
		LastSignIn  time.Time `json:"last_seen"`
	}
	addrCache := map[string]string{}
	siteAddr := func(siteID, fallback string) string {
		if a, ok := addrCache[siteID]; ok {
			return a
		}
		a, ok := h.Hosts.SiteAddressFor(ctx, siteID, "")
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
		if c.Kind == "list" {
			what = "list " + c.Name
		}
		changeList = append(changeList, change{Site: siteAddr(c.SiteID, "(a site that has since been deleted)"), What: what, ItemID: c.ItemID, Change: c.Op, At: c.At.UTC()})
	}
	return map[string]any{"signed_in_to": signInList, "changed": changeList}, nil
}

// exportReadme is README.txt at the top of Download my data.
func exportReadme() string {
	return web.SupportText(exportReadmeText)
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
                     and the changes recorded against you on other people's
                     saved data while signed in ("changed": which site, what
                     and when; the site's owner sees your address next to
                     each). These records are kept 30 days. Changes made
                     without signing in are not linked to you, so they are
                     not here; for help with those, write to
                     support@simple-host.app.
sites/<name>/        One folder per site, the same as that site's download:
                     files/ (the live version) and storage/ (its saved data).
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
