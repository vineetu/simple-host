package db

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

type ShowcasePreference = model.ShowcasePreference

func GetShowcaseBio(ctx context.Context, q Querier, userID string) (string, error) {
	return accounts.GetShowcaseBio(ctx, q, userID)
}

func SetShowcaseBio(ctx context.Context, q Querier, userID, bio string) error {
	return accounts.SetShowcaseBio(ctx, q, userID, bio)
}

// Empty userID is the operator's all-sites listing; otherwise only that owner's preferences.
func ShowcasePreferences(ctx context.Context, q *sql.DB, userID string) (map[string]ShowcasePreference, error) {
	return sites.ShowcasePreferences(ctx, q, userID)
}

func GetShowcasePreference(ctx context.Context, q Querier, userID, name string) (ShowcasePreference, error) {
	return sites.GetShowcasePreference(ctx, q, userID, name)
}

func SetShowcasePreference(ctx context.Context, q Querier, userID, name string, pinned *bool, order *int) (ShowcasePreference, error) {
	return sites.SetShowcasePreference(ctx, q, userID, name, pinned, order)
}
