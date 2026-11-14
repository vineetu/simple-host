package handler

import (
	"context"

	"github.com/vsriram/simple-host/internal/app/accounts"
)

func HackAccountDeleteBlock(ctx context.Context, q accounts.DeletionQueries, userID string, holdingAccounts bool) (string, string, error) {
	return accounts.CheckHackAccountDeletion(ctx, q, userID, holdingAccounts)
}

func HackHoldingAccount(ctx context.Context, q accounts.DeletionQueries, userID string, holdingAccounts bool) (bool, error) {
	return accounts.HoldingAccount(ctx, q, userID, holdingAccounts)
}
