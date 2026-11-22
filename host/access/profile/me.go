package profile

import (
	"context"
	"errors"
	"net/http"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	resp, err := h.meFor(r.Context(), user.ID)
	if errors.Is(err, errFileUsage) {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not measure account files"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// errFileUsage: the account's files could not be measured.
var errFileUsage = errors.New("could not measure account files")

// meFor is GET /v1/me for the account as it is now: who it is, its limits,
// how it signs in, and (where accounts own sites) its home site and teams.
func (h *Handler) meFor(ctx context.Context, userID string) (meResponse, error) {
	user, err := h.accounts.GetUserByID(ctx, userID)
	if err != nil {
		return meResponse{}, err
	}
	alertsOn, err := h.accounts.SignInAlertsOn(ctx, user.ID)
	if err != nil {
		return meResponse{}, err
	}
	resp := meResponse{
		ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin,
		Handle: user.Handle.String, DisplayName: user.DisplayName.String, SignInAlerts: &alertsOn,
	}
	if err := h.meLimits(ctx, &user, &resp); err != nil {
		return resp, err
	}
	if h.PasswordEnabled {
		login, has, err := h.accounts.PasswordLogin(ctx, user.ID)
		if err != nil {
			return resp, err
		}
		resp.HasPassword, resp.Login = &has, login
	}
	if h.Hosts != nil && user.Handle.String != "" {
		resp.PublicPage = h.Hosts.Addressing().PersonPageURL(user.Handle.String)
	}
	return resp, h.mePersonalSites(ctx, &user, &resp)
}

// meLimits are the account's site count, file use, kept versions and site
// size limits.
func (h *Handler) meLimits(ctx context.Context, user *model.User, resp *meResponse) error {
	if !user.IsAdmin {
		n := accounts.New(h.database).MaxSites(ctx, user, h.Limits)
		resp.MaxSites = &n
	}
	if h.Hosts != nil && h.ed.Identity().PersonalSites {
		usage, err := sites.FileUsage(ctx, h.database, h.Disk, h.FileLimits, user)
		if err != nil {
			return errFileUsage
		}
		resp.FileUsage = usage
	}
	resp.CanSetKeepVersions = h.Limits.CanSetKeepVersions(accounts.New(h.database).SiteAccountHandles(ctx, user)...)
	resp.KeepVersions = h.Limits.KeepVersionsFor(web.DefaultKeepVersions, accounts.New(h.database).SiteAccountHandles(ctx, user)...)
	limit, _ := accounts.New(h.database).SiteLimit(ctx, user, h.Limits, sites.Upload.Body)
	resp.MaxSiteMB = sites.StatedSiteMB(limit)
	return nil
}

// mePersonalSites is the own-address state, home site, teams and team
// invitations of an account that owns its sites. A Simple Hack account has
// no personal sites (no_personal_sites), so it has no own site address to
// wait for.
func (h *Handler) mePersonalSites(ctx context.Context, user *model.User, resp *meResponse) error {
	if !h.ed.Identity().PersonalSites {
		return nil
	}
	if h.Hosts != nil && user.Handle.String != "" {
		resp.Address = h.Hosts.AddressState(user.Handle.String)
	}
	home, hasHome, err := appsites.HomeSite(ctx, h.database, user.ID)
	if err != nil {
		return err
	}
	if hasHome {
		resp.HomeSite = &home.Name
	}
	teams, err := h.accounts.TeamsOf(ctx, user.ID)
	if err != nil {
		return err
	}
	resp.Teams = teamsForMe(teams)
	resp.TeamInvitations, err = accounts.MyInvitations(ctx, h.database, user)
	return err
}

type meResponse struct {
	FileUsage          *sites.AccountFileUsage `json:"file_usage,omitempty"`
	CanSetKeepVersions bool                    `json:"can_set_keep_versions"`
	KeepVersions       int                     `json:"keep_versions"`
	HomeSite           *string                 `json:"home_site"`
	DisplayName        string                  `json:"display_name,omitempty"`
	ID                 string                  `json:"id"`
	Username           string                  `json:"username"`
	IsAdmin            bool                    `json:"is_admin"`
	Handle             string                  `json:"handle,omitempty"`
	// PublicPage is the account's public page: https://<handle>.<SITE_DOMAIN>/
	// (or the path address when person hosts are not the canonical address).
	PublicPage string `json:"public_page,omitempty"`
	// SignInAlerts: an email after each sign-in (default on).
	SignInAlerts *bool `json:"signin_alerts,omitempty"`
	// HasPassword: whether this account has a password set. Present only
	// when PASSWORD_SIGNIN is on (self-hosted installs only), so hosted
	// responses stay byte-identical with the field absent.
	HasPassword *bool `json:"has_password,omitempty"`
	// Login is what a password account actually signs in with (its own
	// account_passwords.login), present whenever HasPassword is true. A
	// self sign-up's Username is a synthetic, never-shown value instead
	// (CreatePasswordAccount), so the dashboard and Hack account screen
	// must show Login, not Username, for a password account.
	Login string `json:"login,omitempty"`
	// MaxSites is the sites this account may hold (MAX_SITES_OVERRIDES, else
	// MAX_SITES_PER_ACCOUNT); absent for admins, who have no limit.
	MaxSites *int `json:"max_sites,omitempty"`
	// MaxSiteMB is the largest site this account may deploy, in MB
	// (MAX_ARCHIVE_MB_OVERRIDES, else MAX_ARCHIVE_MB). Admins too.
	MaxSiteMB int `json:"max_site_mb"`
	// Address is the state of the account's own site address
	// (https://<site>.<handle>.<SITE_DOMAIN>/): ready, or waiting / failing
	// with a rough time, while sites are at <handle>.<SITE_DOMAIN>/<site>/.
	Address *sites.AddressState `json:"address,omitempty"`
	// Teams are the teams this person belongs to: each one's id, which never
	// changes, and its handle (owner=<handle> on the site routes).
	Teams []map[string]string `json:"teams,omitempty"`
	// TeamInvitations are the invitations to join a team waiting for this
	// person's answer (POST /v1/me/team-invitations/{id}).
	TeamInvitations []model.TeamInvitation `json:"team_invitations,omitempty"`
}

// teamsForMe is the teams part of GET /v1/me: each team's id (stable: record
// it rather than the name) and handle.
func teamsForMe(teams []model.Team) []map[string]string {
	out := make([]map[string]string, 0, len(teams))
	for _, t := range teams {
		out = append(out, map[string]string{"id": t.ID, "handle": t.Handle})
	}
	return out
}
