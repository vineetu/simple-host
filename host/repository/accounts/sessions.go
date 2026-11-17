package accounts

import "context"

func SweepSSO(ctx context.Context, q Querier) error {
	_, err := SweepSSOCount(ctx, q)
	return err
}
