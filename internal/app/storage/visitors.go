package storage

import (
	"context"
	"database/sql"
	"errors"

	repository "github.com/vsriram/simple-host/internal/repository/storage"
)

// VisitorsInput identifies the site's requested visitors.
type VisitorsInput struct {
	Call Call
	IDs  []string
}

// Visitor is a resolved visitor identity.
type Visitor struct {
	VisitorID, Email string
	Found            bool
}

// Visitors resolves identities only for visitors who signed in on the site.
func (s *Service) Visitors(ctx context.Context, in VisitorsInput) ([]Visitor, error) {
	if !in.Call.Owner {
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	wrote, err := repository.SignedInAmong(ctx, s.db, in.Call.SiteID, in.IDs)
	if err != nil {
		return nil, err
	}
	out := make([]Visitor, 0, len(in.IDs))
	for _, id := range in.IDs {
		v := Visitor{VisitorID: id}
		if wrote[id] {
			email, err := s.deps.VisitorEmail(ctx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			v.Email, v.Found = email, err == nil && email != ""
		}
		out = append(out, v)
	}
	return out, nil
}
