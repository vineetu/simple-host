package data

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

var (
	resourceNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	// TableNameRE is the shape of a SQLite table name a route may name.
	TableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ValidResourceName reports whether name can name a KV namespace, SQLite
// database or file bucket.
func ValidResourceName(name string) bool { return resourceNameRE.MatchString(name) }

// ResourceCall resolves the resource a call names: the name's shape, the
// stored resource, its kind (a visitor never learns that a resource of
// another kind exists) and, for a signed-in visitor, whether they are the
// site's owner on its own address.
func (s *Service) ResourceCall(ctx context.Context, call Call, name, kind string) (Call, error) {
	if !ValidResourceName(name) {
		return call, &Error{Code: "invalid_resource", Message: "invalid resource name"}
	}
	resource, err := s.LoadResource(ctx, call.SiteID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return call, &Error{Code: "resource_not_found", Message: "resource not found"}
	}
	if err != nil {
		return call, err
	}
	if kind != "" && resource.Kind != kind {
		if !call.Owner {
			return call, &Error{Code: "resource_not_found", Message: "resource not found"}
		}
		return call, &Error{Code: "resource_kind_conflict", Message: "resource has another kind"}
	}
	call.ResourceName, call.Resource = name, resource
	call.OwnerOnSite = s.OwnerOnSite(ctx, call)
	return call, nil
}

// OwnerOnSite reports whether a signed-in visitor is the site's owner signed
// in on the site's own address: then the page acts
// with owner rights for storage data only. Access establishes the address
// (OnOwnAddress); the binding provider decides who counts as the owner (the
// account, a member of the team that holds the site, an editor; on Simple
// Hack the team's members or the event's organisers). A resource saved
// before presets never had an owner on the site. An AI visitor never is.
func (s *Service) OwnerOnSite(ctx context.Context, call Call) bool {
	if call.Owner || call.VisitorAI || call.VisitorID == "" || !call.OnOwnAddress || call.Resource.BeforePresets {
		return false
	}
	ok, err := s.deps.Bindings.OwnerOnSite(ctx, call.SiteID, call.VisitorID)
	return err == nil && ok
}

// VisitorRefsMax is the most visitor ids and emails one lookup takes.
const VisitorRefsMax = 100

var visitorIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// VisitorRefs reads a visitor lookup (?id= and ?email=, each repeated or
// comma-separated): lowercased, deduplicated, 1 to VisitorRefsMax in all.
func VisitorRefs(idValues, emailValues []string) (ids, emails []string, err error) {
	ids, err = visitorRefList(idValues, visitorIDRE.MatchString, "visitor ids look like 1b4e28ba-2fa1-11d2-883f-0016d3cca427, as in a row's visitor_id")
	if err != nil {
		return nil, nil, err
	}
	emails, err = visitorRefList(emailValues, func(s string) bool { return strings.Contains(s, "@") }, "email looks like name@example.com")
	if err != nil {
		return nil, nil, err
	}
	if total := len(ids) + len(emails); total == 0 || total > VisitorRefsMax {
		return nil, nil, &Error{Code: "invalid_visitor_id", Message: "give 1 to 100 visitor ids or emails: ?id=<visitor_id>,<visitor_id> and/or ?email=<address>,<address>"}
	}
	return ids, emails, nil
}

func visitorRefList(values []string, valid func(string) bool, hint string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, ref := range strings.Split(v, ",") {
			ref = strings.ToLower(strings.TrimSpace(ref))
			if ref == "" || seen[ref] {
				continue
			}
			if !valid(ref) {
				return nil, &Error{Code: "invalid_visitor_id", Message: hint}
			}
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, nil
}
