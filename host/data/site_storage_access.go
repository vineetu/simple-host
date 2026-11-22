package data

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
)

// Storage access: rules replace presets, for every kind. A
// key-value namespace, SQLite database or file bucket is governed by its rules,
// checked in the storage service (appstorage authorize, fileRule).
//
// The owner's tools (their key, the connector) always have full control.
// The site's owner signed in on the site's own address through visitor
// sign-in (D1) is the auth.role "owner" of the rules: see storageOwnerOnSite.

func (h *Handler) LoadStorageResource(ctx context.Context, siteID, name string) (model.Resource, error) {
	return h.StorageSvc().LoadResource(ctx, siteID, name)
}
