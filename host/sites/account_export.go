package sites

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// AccountAddresses is the account's site addresses (with the earlier ones
// still served while a new one is pending) and the names its sites retired,
// for Download my data.
func (svc *Service) AccountAddresses(ctx context.Context, userID string) ([]model.AccountAddress, []model.RetiredAccountName, error) {
	addrs, err := repo.ListAccountAddresses(ctx, svc.DB, userID)
	if err != nil {
		return nil, nil, err
	}
	retired, err := repo.ListRetiredNames(ctx, svc.DB, userID)
	if err != nil {
		return nil, nil, err
	}
	return addrs, retired, nil
}
