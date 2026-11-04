package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// BindFamily adds a pending family for an account. Refused when another
// account holds the name (ErrFamilyTaken), when this account already has it
// (ErrFamilyExists) or when the account is at its cap (ErrFamilyCap). Runs
// under the lock that custom-domain binds under the same registrable domain
// also take, so the two can never race past each other's check.
func (svc *Service) BindFamily(ctx context.Context, b model.FamilyBind) (model.AddressFamily, error) {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return model.AddressFamily{}, err
	}
	defer tx.Rollback()
	if err := repo.LockFamilyName(ctx, tx, b.Suffix); err != nil {
		return model.AddressFamily{}, err
	}
	if err := repo.LockFamilyUser(ctx, tx, b.UserID); err != nil {
		return model.AddressFamily{}, err
	}
	var exists bool
	var n int
	if exists, n, err = repo.FamilyBindCount(ctx, tx, b.UserID, b.Suffix); err != nil {
		return model.AddressFamily{}, err
	}
	if exists {
		return model.AddressFamily{}, model.ErrFamilyExists
	}
	if n >= b.Max {
		return model.AddressFamily{}, model.ErrFamilyCap
	}
	taken, err := repo.FamilyConflict(ctx, tx, b.UserID, b.Suffix)
	if err != nil {
		return model.AddressFamily{}, err
	}
	if taken {
		return model.AddressFamily{}, model.ErrFamilyTaken
	}
	mode := b.CertMode
	if mode == "" {
		mode = "wildcard"
	}
	var id string
	if id, err = repo.InsertFamily(ctx, tx, b, mode); err != nil {
		return model.AddressFamily{}, err
	}
	f, err := repo.GetFamilyByID(ctx, tx, id)
	if err != nil {
		return model.AddressFamily{}, err
	}
	return f, tx.Commit()
}

// VerifyFamily records that a pending family proved itself. It wins only when
// no other account has verified an overlapping family and no other account's
// site has a custom domain under it meanwhile (won=false, nothing changed).
// Every other account's pending family on an overlapping name is let go and
// returned, so their owners can be told.
func (svc *Service) VerifyFamily(ctx context.Context, id string) (won bool, losers []model.AddressFamily, err error) {
	tx, err := svc.database.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	f, err := repo.GetFamilyByID(ctx, tx, id)
	if err != nil {
		return false, nil, err
	}
	if err := repo.LockFamilyName(ctx, tx, f.Suffix); err != nil {
		return false, nil, err
	}
	if f.VerifiedAt.Valid {
		return true, nil, nil
	}
	taken, err := repo.FamilyConflict(ctx, tx, f.UserID, f.Suffix)
	if err != nil {
		return false, nil, err
	}
	if taken {
		return false, nil, nil
	}
	if err := repo.ActivateFamily(ctx, tx, f.ID); err != nil {
		return false, nil, err
	}
	losers, err = repo.OverlappingPendingFamilies(ctx, tx, f.UserID, f.Suffix)
	if err != nil {
		return false, nil, err
	}
	for _, l := range losers {
		if err := repo.RemoveFamily(ctx, tx, l.ID); err != nil {
			return false, nil, err
		}
	}
	return true, losers, tx.Commit()
}
