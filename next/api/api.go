// Package api is the four routes that combine several features: me (route
// 6), an owner's settings (7), everything about one site (23) and changing
// a site (24). Each field is read or changed by the package that owns it.
package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"simplehost/access/connector"
	access "simplehost/access/core"
	"simplehost/access/keys"
	"simplehost/access/teams"
	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
	"simplehost/ops/holds"
	"simplehost/ops/notify"
	sites "simplehost/sites/core"
	"simplehost/sites/domains"
	"simplehost/sites/locks"
	"simplehost/sites/manage"
	"simplehost/sites/publicpage"
	"simplehost/sites/shortnames"
)

type handler struct{ a *app.App }

// Mount adds routes 6, 7, 23 and 24.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("GET /v1/me", web.Func(h.me))
	a.Routes.Platform.Handle("PATCH /v1/owners/{owner}", web.Func(h.owner))
	a.Routes.Platform.Handle("GET /v1/sites/{site}", web.Func(h.site))
	a.Routes.Platform.Handle("PATCH /v1/sites/{site}", web.Func(h.change))
}

var errSignIn = web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as X-API-Key: <key>.")

// me is route 6: me, my teams with their members, invitations, keys,
// connected apps, push devices and use against the limits.
func (h *handler) me(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	ctx := r.Context()
	p, err := h.a.People.Get(ctx, c.ID)
	if err != nil {
		return err
	}
	out := map[string]any{"id": p.ID, "email": p.Email, "handle": p.Handle, "display_name": p.DisplayName, "operator": p.Operator,
		"push_key": notify.PublicKey(h.a)}
	if err := h.ownerFields(r, p.ID, out); err != nil {
		return err
	}
	list, err := teams.List(ctx, h.a.DB, p.ID, p.Email)
	if err != nil {
		return err
	}
	mine, invitations := []map[string]any{}, []map[string]any{}
	for _, t := range list {
		if t.Invite != "" {
			invitations = append(invitations, map[string]any{"id": t.Invite, "team": t.Handle, "display_name": t.DisplayName})
			continue
		}
		members, invited, err := teams.Members(ctx, h.a.DB, t.ID)
		if err != nil {
			return err
		}
		inv := []map[string]string{}
		for _, e := range invited {
			inv = append(inv, map[string]string{"email": e})
		}
		mine = append(mine, map[string]any{"id": t.ID, "handle": t.Handle, "display_name": t.DisplayName, "members": members, "invites": inv})
	}
	out["teams"], out["invitations"] = mine, invitations
	if out["keys"], err = keys.List(ctx, h.a.DB, p.ID); err != nil {
		return err
	}
	if out["apps"], err = connector.Apps(ctx, h.a.DB, p.ID); err != nil {
		return err
	}
	if out["devices"], err = notify.Devices(ctx, h.a, p.ID); err != nil {
		return err
	}
	n, err := h.a.Sites.Count(ctx, p.ID)
	if err != nil {
		return err
	}
	out["use"] = map[string]any{"sites": n, "site_bytes_limit": limits.SiteBytes.N, "data_bytes_limit": limits.DataBytes.N,
		"files_bytes_limit": limits.FilesBytes.N, "kept_versions": limits.KeptVersions.N, "unlimited": p.Operator}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// ownerFields adds an owner's home site and domain for all sites.
func (h *handler) ownerFields(r *http.Request, ownerID string, out map[string]any) error {
	home, err := publicpage.Home(r, h.a, ownerID)
	if err != nil {
		return err
	}
	shared, err := domains.OfOwner(r.Context(), h.a, ownerID)
	if err != nil {
		return err
	}
	out["home_site"], out["domain"], out["domain_status"] = home, "", nil
	if len(shared) > 0 {
		out["domain"], out["domain_status"] = shared[0].Host, shared[0]
	}
	return nil
}

// owner is route 7: a person's or team's handle, display name, home site and
// domain for all sites. {owner} is a handle, an id, or "me".
func (h *handler) owner(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	ctx := r.Context()
	o, err := h.a.People.ByHandle(ctx, r.PathValue("owner"))
	if err != nil {
		id := r.PathValue("owner")
		if id == "me" {
			id = c.ID
		}
		if o, err = h.a.People.Get(ctx, id); err != nil {
			return err
		}
	}
	if ok, err := h.a.People.ActsFor(ctx, c, o.ID); err != nil || !ok {
		if err == nil {
			err = web.Refuse(http.StatusForbidden, "not_yours", "Only the person, or a member of the team, can change this.")
		}
		return err
	}
	var in struct {
		Handle      *string `json:"handle"`
		DisplayName *string `json:"display_name"`
		HomeSite    *string `json:"home_site"`
		Domain      *string `json:"domain"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	if in.Handle != nil && *in.Handle != o.Handle {
		if err := h.rename(ctx, o, strings.ToLower(strings.TrimSpace(*in.Handle))); err != nil {
			return err
		}
	}
	if in.DisplayName != nil {
		if err := h.a.People.Rename(ctx, o.ID, *in.DisplayName); err != nil {
			return err
		}
	}
	if in.HomeSite != nil {
		if err := publicpage.SetHome(r, h.a, o.ID, strings.TrimSpace(*in.HomeSite)); err != nil {
			return err
		}
	}
	if in.Domain != nil {
		if err := h.shareDomain(ctx, o.ID, strings.ToLower(strings.TrimSpace(*in.Domain))); err != nil {
			return err
		}
	}
	h.a.Sites.Forget(o.ID)
	if o, err = h.a.People.Get(ctx, o.ID); err != nil {
		return err
	}
	out := map[string]any{"id": o.ID, "handle": o.Handle, "display_name": o.DisplayName}
	if err := h.ownerFields(r, o.ID, out); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// rename gives the owner a new handle. The old one is retired for good, with
// no redirect, and every site moves to addresses under the new one.
func (h *handler) rename(ctx context.Context, o access.Person, handle string) error {
	tx, err := h.a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if o.Handle != "" {
		if err := access.RetireName(ctx, tx, o.Handle); err != nil {
			return err
		}
		if err := h.a.Sites.Rehome(ctx, tx, o.ID); err != nil {
			return err
		}
	}
	if err := access.ClaimName(ctx, tx, handle, "handle", o.ID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// shareDomain sets the owner's domain for all sites ("" removes it).
func (h *handler) shareDomain(ctx context.Context, ownerID, domain string) error {
	have, err := domains.OfOwner(ctx, h.a, ownerID)
	if err != nil {
		return err
	}
	for _, d := range have {
		if d.Host != domain {
			if err := domains.Remove(ctx, h.a, ownerID, d.Host); err != nil {
				return err
			}
		}
	}
	if domain != "" {
		_, err = domains.Share(ctx, h.a, ownerID, domain)
	}
	return err
}

// site is route 23. The owner (or their team, or the operator) gets
// everything; anyone else who may open the site gets its address.
func (h *handler) site(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	out, err := h.view(r, site)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

func (h *handler) view(r *http.Request, site *sites.Site) (map[string]any, error) {
	out := map[string]any{"name": site.Name, "owner": site.OwnerHandle, "url": h.a.Sites.URL(site), "live_version": site.LiveVersion}
	if err := h.a.Sites.Manage(r, site); err != nil {
		d, verr := h.a.Sites.Visit(r, site)
		if verr != nil {
			return nil, verr
		}
		if derr := d.Err(); derr != nil {
			return nil, derr
		}
		return out, nil
	}
	ctx := r.Context()
	var err error
	out["listed"], out["offline"], out["taken_down"], out["access"] = site.Listed, site.Offline, site.TakenDown, site.Access
	if out["versions"], err = h.a.Sites.Versions(ctx, site.ID); err != nil {
		return nil, err
	}
	if out["passcode"], err = locks.Passcode(h.a, site); err != nil {
		return nil, err
	}
	if out["viewers"], err = locks.Viewers(ctx, h.a, site); err != nil {
		return nil, err
	}
	if out["short_name"], err = shortnames.Of(r, h.a, site); err != nil {
		return nil, err
	}
	doms, err := domains.OfSite(ctx, h.a, site)
	if err != nil {
		return nil, err
	}
	out["domain"] = nil
	if len(doms) > 0 {
		out["domain"] = doms[0]
	}
	nss, err := h.a.Data.Namespaces(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	sizes, total := h.a.Data.Sizes(site.ID)
	list := []map[string]any{}
	for _, n := range nss {
		ns, err := h.a.Data.Open(r, site, n.Name)
		if err != nil {
			return nil, err
		}
		tables, err := ns.Query(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		ns.Close()
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, t := range tables.Rows {
			names = append(names, t[0].(string))
		}
		list = append(list, map[string]any{"name": n.Name, "bytes": sizes[n.Name], "rules": n.Rules, "spam_check": n.SpamCheck, "tables": names})
	}
	out["namespaces"] = list
	if out["notifications"], err = notify.Rules(ctx, h.a, site.ID); err != nil {
		return nil, err
	}
	out["use"] = map[string]any{"data_bytes": total, "data_bytes_limit": limits.DataBytes.N, "site_bytes_limit": limits.SiteBytes.N}
	return out, nil
}

// change is route 24. Each field present is changed by the package that
// owns it, in this order; the answer is the site as route 23 shows it.
func (h *handler) change(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Offline         *bool          `json:"offline"`
		Listed          *bool          `json:"listed"`
		TakenDown       *bool          `json:"taken_down"`
		Reason          string         `json:"reason"`
		LiveVersion     *int           `json:"live_version"`
		Restore         bool           `json:"restore"`
		Access          *string        `json:"access"`
		Passcode        *string        `json:"passcode"`
		Viewers         *[]string      `json:"viewers"`
		ViewersAdd      []string       `json:"viewers_add"`
		ViewersRemove   []string       `json:"viewers_remove"`
		SignEveryoneOut bool           `json:"sign_everyone_out"`
		Domain          *string        `json:"domain"`
		ShortName       *string        `json:"short_name"`
		Notifications   *[]notify.Rule `json:"notifications"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if in.Restore {
		owner := web.CallerOf(r).ID
		if handle := r.URL.Query().Get("owner"); handle != "" {
			o, err := h.a.People.ByHandle(ctx, handle)
			if err != nil {
				return err
			}
			owner = o.ID
		}
		if _, err := manage.Restore(r, h.a, owner, r.PathValue("site")); err != nil {
			return err
		}
	}
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if in.TakenDown != nil {
		if err := holds.TakeDown(r, h.a, site, *in.TakenDown, in.Reason); err != nil {
			return err
		}
	} else if err := h.a.Sites.Manage(r, site); err != nil {
		return err
	}
	if in.Offline != nil && err == nil {
		err = manage.SetOffline(r, h.a, site, *in.Offline)
	}
	if in.Listed != nil && err == nil {
		err = manage.SetListed(r, h.a, site, *in.Listed)
	}
	if in.LiveVersion != nil && err == nil {
		err = manage.Rollback(r, h.a, site, *in.LiveVersion)
	}
	if err == nil {
		err = h.access(ctx, site, in.Access, in.Passcode, in.Viewers, in.ViewersAdd, in.ViewersRemove)
	}
	if in.SignEveryoneOut && err == nil {
		err = locks.SignEveryoneOut(ctx, h.a, site)
	}
	if in.Domain != nil && err == nil {
		err = h.domain(ctx, site, *in.Domain)
	}
	if in.ShortName != nil && err == nil {
		if name := strings.ToLower(strings.TrimSpace(*in.ShortName)); name == "" {
			err = shortnames.Release(r, h.a, site)
		} else {
			_, err = shortnames.Claim(r, h.a, site, name)
		}
	}
	if in.Notifications != nil && err == nil {
		err = notify.Set(ctx, h.a, site.ID, *in.Notifications)
	}
	if err != nil {
		return err
	}
	if site, err = h.a.Sites.ByID(ctx, site.ID); err != nil {
		return err
	}
	out, err := h.view(r, site)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// access is who may open the site: "open", "passcode" (a new passcode, or
// the one it had) or "viewers" (the list given, or the one it had, with
// people added and removed).
func (h *handler) access(ctx context.Context, site *sites.Site, mode, code *string, list *[]string, add, remove []string) error {
	named := list != nil || len(add) > 0 || len(remove) > 0
	switch {
	case named || mode != nil && *mode == "viewers":
		if list == nil {
			have, err := locks.Viewers(ctx, h.a, site)
			if err != nil {
				return err
			}
			list = &[]string{}
			for _, v := range have {
				*list = append(*list, v.Email)
			}
		}
		emails := []string{}
		for _, e := range append(*list, add...) {
			e = strings.ToLower(strings.TrimSpace(e))
			if !slices.ContainsFunc(remove, func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), e) }) && !slices.Contains(emails, e) {
				emails = append(emails, e)
			}
		}
		return locks.SetViewers(ctx, h.a, site, emails)
	case code != nil:
		return locks.SetPasscode(ctx, h.a, site, *code)
	case mode == nil:
		return nil
	case *mode == "open":
		return locks.Open(ctx, h.a, site)
	case *mode == "passcode" && site.PasscodeEnc != nil:
		old, err := locks.Passcode(h.a, site)
		if err != nil {
			return err
		}
		return locks.SetPasscode(ctx, h.a, site, old)
	case *mode == "passcode":
		return web.Refuse(http.StatusBadRequest, "need_passcode", "Give a passcode of at least 6 characters to lock the site with.")
	}
	return web.Refuse(http.StatusBadRequest, "bad_access", `Set "access" to "open", "passcode" or "viewers".`)
}

// domain connects the site's own domain ("" removes it). An earlier one is
// removed first: a site has one own domain.
func (h *handler) domain(ctx context.Context, site *sites.Site, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	have, err := domains.OfSite(ctx, h.a, site)
	if err != nil {
		return err
	}
	for _, d := range have {
		if d.Host != host {
			if err := domains.Remove(ctx, h.a, site.OwnerID, d.Host); err != nil {
				return err
			}
		}
	}
	if host != "" {
		_, err = domains.Connect(ctx, h.a, site, host)
	}
	return err
}
