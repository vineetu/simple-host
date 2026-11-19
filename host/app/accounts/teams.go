package accounts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/accounts"
	sites "github.com/vsriram/simple-host/host/repository/sites"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
	authrules "github.com/vsriram/simple-host/host/rules/auth"
)

// ActsAsOwner is the one answer to "may this person act as the owner of this
// account's sites?": the account is their own, or it is a team they belong
// to. Every site, storage, version, domain, passcode, viewer and export path
// reaches it, either through the request's User (ResolveOwner turns a
// member's owner=<team> into the team's account) or, for a person signed in
// on a site's own address, through the storage binding provider, the frame
// guard, the visitor "me" answer, the push owner check and the named-viewer
// check. The membership is read on every call, so a removed member loses
// access on their next request.
func ActsAsOwner(ctx context.Context, q sqlkit.Querier, personID, accountID string) (bool, error) {
	if personID == "" || accountID == "" {
		return false, nil
	}
	if personID == accountID {
		return true, nil
	}
	return repo.IsTeamMember(ctx, q, accountID, personID)
}

// ActsAsOwner is the package function on the service's database.
func (s *Service) ActsAsOwner(ctx context.Context, personID, accountID string) (bool, error) {
	return ActsAsOwner(ctx, s.database, personID, accountID)
}

// OwnerRefusal is why a request naming owner=<x> is refused.
type OwnerRefusal struct {
	Status        int
	Code, Message string
}

// actingKey carries, for one request, the account a member acts as: the
// User GetUserByAPIKey returns for that request's key. Only ScopeGate sets it
// (WithActingOwner), after ResolveOwner has checked the membership.
type actingKey struct{}

type acting struct {
	key  string
	user *model.User
}

// WithActingOwner makes GetUserByAPIKey answer user for apiKey for the rest
// of this request. A nil user clears what an outer request set, so an inner
// request never inherits another request's owner.
func WithActingOwner(ctx context.Context, apiKey string, user *model.User) context.Context {
	if user == nil {
		if _, ok := ctx.Value(actingKey{}).(acting); !ok {
			return ctx
		}
		return context.WithValue(ctx, actingKey{}, acting{})
	}
	return context.WithValue(ctx, actingKey{}, acting{key: apiKey, user: user})
}

func actingFor(ctx context.Context, apiKey string) (model.User, bool) {
	a, ok := ctx.Value(actingKey{}).(acting)
	if !ok || a.user == nil || a.key == "" || a.key != apiKey {
		return model.User{}, false
	}
	return *a.user, true
}

// ResolveOwner answers a request that names owner (a handle) as the owner of
// the sites it acts on. A nil user and nil refusal leave the request as it
// is: the key is not one this check reads (an unknown, expired or suspended
// key, the admin key, a visitor credential), the caller is an admin, or owner
// is the caller's own handle; the route then does exactly what it did before.
// Otherwise the caller must be a member of the team owner names, with a full
// key or their own connection: the answer is then the team's account acting
// for them. lenient (the analytics routes, where owner already meant "an
// admin reads another account's site") also leaves a request alone when
// owner names no team, so those routes answer exactly as before.
func (s *Service) ResolveOwner(ctx context.Context, apiKey, owner string, keyIdle time.Duration, lenient bool) (*model.User, *OwnerRefusal, error) {
	owner = strings.ToLower(strings.TrimSpace(owner))
	if owner == "" {
		return nil, nil, nil
	}
	person, err := s.GetUserByAPIKey(ctx, apiKey, keyIdle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, model.ErrAccountSuspended) ||
			errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle) || errors.Is(err, model.ErrTeamKeyInactive) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if person.IsAdmin || strings.EqualFold(person.Handle.String, owner) {
		return nil, nil, nil
	}
	if person.Team != nil || person.EventWebsite != nil {
		return nil, &OwnerRefusal{http.StatusForbidden, "owner_not_allowed", "this credential acts for one site only; owner cannot name another account"}, nil
	}
	if person.KeyScope == authrules.KeyScopeDeploy {
		return nil, &OwnerRefusal{http.StatusForbidden, "deploy_only_key", "a deploy-only key acts only on its own account's sites; to act for a team, use a full key or your own sign-in with owner=<team>"}, nil
	}
	notFound := &OwnerRefusal{http.StatusNotFound, "owner_not_found", "no team named " + owner + " that you belong to; GET /v1/teams lists your teams"}
	team, err := repo.GetTeamByHandle(ctx, s.database, owner)
	if errors.Is(err, sql.ErrNoRows) {
		if lenient {
			return nil, nil, nil
		}
		return nil, notFound, nil
	}
	if err != nil {
		return nil, nil, err
	}
	member, err := ActsAsOwner(ctx, s.database, person.ID, team.ID)
	if err != nil {
		return nil, nil, err
	}
	if !member {
		return nil, notFound, nil
	}
	if team.Suspended {
		return nil, &OwnerRefusal{http.StatusForbidden, "account_suspended", "this team is suspended by the operator"}, nil
	}
	return &model.User{
		ID: team.ID, Username: team.Username, CreatedAt: team.CreatedAt, Handle: team.Handle, DisplayName: team.DisplayName,
		KeyScope: person.KeyScope, KeyExpiresAt: person.KeyExpiresAt,
		Member: &model.MemberIdentity{PersonID: person.ID, PersonHandle: person.Handle.String},
	}, nil, nil
}

// IsTeam reports whether the account id is a team.
func (s *Service) IsTeam(ctx context.Context, id string) (bool, error) {
	return repo.IsTeamAccount(ctx, s.database, id)
}

// TeamsOf lists the teams a person belongs to, for GET /v1/me and who_am_i.
func (s *Service) TeamsOf(ctx context.Context, personID string) ([]model.Team, error) {
	return repo.ListTeamsOf(ctx, s.database, personID)
}

// eraseSoleMemberTeams deletes, inside an account deletion's tx, every team
// personID is the only member of, with all its sites, so nobody is trapped in
// a team and no team is left without a member. Each team's row is locked
// before its members are counted, so a person joining (accept locks the team
// row too) or a site being made for it either finishes first and is seen,
// or waits and then finds the team gone. The caller removes the returned
// accounts' files after the commit, as for the person's own. Unless the
// operator deletes, a team that is suspended or has a taken-down site blocks
// it, as the person's own account would (EraseTeam).
func eraseSoleMemberTeams(ctx context.Context, tx *sql.Tx, personID string, operator bool) ([]model.ErasedAccount, error) {
	ids, err := repo.TeamIDsOf(ctx, tx, personID)
	if err != nil {
		return nil, err
	}
	var out []model.ErasedAccount
	for _, id := range ids {
		if err := repo.LockTeam(ctx, tx, id); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return nil, err
		}
		n, err := repo.CountTeamMembers(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if n != 1 {
			continue
		}
		erased, err := EraseTeam(ctx, tx, id, personID, operator)
		if err != nil {
			return nil, err
		}
		out = append(out, erased)
	}
	return out, nil
}

// EraseTeam deletes a team inside tx, the way any account is deleted: its
// sites (Recently deleted included) go with it and their names and the
// team's handle are retired, never given out again; its memberships and
// invitations go too, and the deletion is recorded. The caller holds the
// team's row lock and has decided under it that the team may go. Unless
// operator, a suspended team or one with a taken-down site is refused with
// the codes a person's own deletion meets (deletionHold).
func EraseTeam(ctx context.Context, tx *sql.Tx, teamID, actorID string, operator bool) (model.ErasedAccount, error) {
	acct, err := repo.LockAccountForDelete(ctx, tx, teamID)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	if !operator {
		if err := deletionHold(acct, "the team "+acct.Handle); err != nil {
			return model.ErasedAccount{}, err
		}
	}
	ops := repo.ErasureOperations{RetireSiteNames: func(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
		return sites.RetireSiteNames(ctx, tx, id, repo.PlatformDomains()...)
	}}
	erased, err := repo.EraseAccount(ctx, tx, acct, ops)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	return erased, repo.InsertTeamAudit(ctx, tx, teamID, acct.Handle, actorID, "delete", "", "")
}
