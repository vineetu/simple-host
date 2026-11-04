package legacy

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
)

// VisitorCollectionSettings reads the declaration used by visitor collection tools.
func (a *Adapter) VisitorCollectionSettings(ctx context.Context, siteID, name string) (model.DataSettings, error) {
	return repo.GetDataSettings(ctx, a.database, siteID, name)
}

// VisitorEntryOwned checks the stored author; mutation calls also require a live entry.
// A missing row or a failed lookup is deliberately indistinguishable from another author's row.
func (a *Adapter) VisitorEntryOwned(ctx context.Context, siteID, name string, id int64, userID string, live bool) bool {
	author, err := repo.GetItemAuthor(ctx, a.database, siteID, name, id)
	return err == nil && author.SubmittedBy == userID && (!live || author.DeletedAt == nil)
}

// VisitorEntryOwnership leaves lookup failures distinct so read results keep their original shape.
func (a *Adapter) VisitorEntryOwnership(ctx context.Context, siteID, name string, id int64, userID string) (bool, error) {
	author, err := repo.GetItemAuthor(ctx, a.database, siteID, name, id)
	return author.SubmittedBy == userID, err
}
