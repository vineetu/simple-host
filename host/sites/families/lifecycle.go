package families

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// ConnectFamily adds a pending family (BindFamily) and refreshes the served
// families. An account that already has the name gets its family back with
// created false. Refusals: model.ErrFamilyTaken, model.ErrFamilyCap.
func (s Service) ConnectFamily(ctx context.Context, b model.FamilyBind) (f model.AddressFamily, created bool, err error) {
	f, err = s.BindFamily(ctx, b)
	if errors.Is(err, model.ErrFamilyExists) {
		f, err = s.GetFamilyByUserSuffix(ctx, b.UserID, b.Suffix)
		return f, false, err
	}
	if err != nil {
		return f, false, err
	}
	s.Deps.Hosts.RefreshFamilies(ctx)
	return f, true, nil
}

// UpdateFamily applies p to the account's family, keeps its issuer request in
// step and returns it.
func (s Service) UpdateFamily(ctx context.Context, userID string, suffix string, p model.FamilyPatch) (model.AddressFamily, error) {
	f, err := repo.UpdateFamily(ctx, s.DB, userID, suffix, p)
	if err != nil {
		return f, err
	}
	s.SyncFamilyRequest(ctx, f)
	s.Deps.Hosts.RefreshFamilies(ctx)
	return f, nil
}

// SetFamilyCert sets the family's certificate mode and the operator's
// lineage name (admin only), keeps its issuer request in step and returns
// the family as it now is (zero if it went in the meantime).
func (s Service) SetFamilyCert(ctx context.Context, id string, mode string, certName string) (model.AddressFamily, error) {
	if err := repo.SetFamilyCert(ctx, s.DB, id, mode, certName); err != nil {
		return model.AddressFamily{}, err
	}
	f, _ := s.GetFamilyByID(ctx, id)
	s.SyncFamilyRequest(ctx, f)
	s.Deps.Hosts.RefreshFamilies(ctx)
	return f, nil
}

// SetFamilyProofExempt turns the admin's "no TXT record needed" flag on or
// off and returns the family as it now is (zero if it went in the meantime).
func (s Service) SetFamilyProofExempt(ctx context.Context, id string, on bool) (model.AddressFamily, error) {
	if err := repo.SetFamilyProofExempt(ctx, s.DB, id, on); err != nil {
		return model.AddressFamily{}, err
	}
	f, _ := s.GetFamilyByID(ctx, id)
	return f, nil
}

// ReleaseFamily lets a family go: the row, the link (so nginx stops serving
// it at once), the issuer's request (so it withdraws the server; it never
// touches the operator's certificate), and every redirect that followed it.
func (s Service) ReleaseFamily(ctx context.Context, f model.AddressFamily) error {
	if _, err := s.DeleteFamily(ctx, f.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hosts := s.Deps.Hosts
	if err := s.Disk.UnbindFamily(f.Suffix); err != nil {
		log.Printf("address family %s: unbind: %v", f.Suffix, err)
	}
	hosts.RemoveFamilyRequest(context.Background(), f.Suffix)
	if f.VerifiedAt.Valid || f.ProofExempt {
		hosts.ReleaseFamily(f.Suffix)
	}
	hosts.RefreshFamilies(ctx)
	hosts.SyncUserRedirects(ctx, f.UserID)
	log.Printf("address family *.%s of %s released", f.Suffix, f.UserID)
	return nil
}

// familyRequestBody is requests/<suffix>: the token, the link target, the
// certificate mode and lineage, the site prefix and the reserved labels.
func (s Service) familyRequestBody(f model.AddressFamily) string {
	return f.Token + "\n" + filepath.Join("..", "by-id", f.UserID) + "\n" + f.CertMode + "\n" + f.CertName + "\n" +
		f.SitePrefix + "\n" + strings.Join(s.Deps.Limits.ReservedFamilyLabels(), " ") + "\n"
}

// SyncFamilyRequest keeps requests/<suffix> in step with a verified family
// that has its certificate set up (rewritten only when it changed, so the
// issuer's path unit fires only then); anything else has none.
func (s Service) SyncFamilyRequest(ctx context.Context, f model.AddressFamily) {
	s.SyncFamilyCertRequest(ctx, FamilyCertRequestInput{
		Directory: s.Deps.Hosts.FamilyCertDir(), Suffix: f.Suffix, Body: s.familyRequestBody(f),
		ValidKey: sites.ValidFamilyKey(f.Suffix), Verified: f.VerifiedAt.Valid, CertName: f.CertName, Wildcard: f.CertMode == "wildcard",
	})
}
