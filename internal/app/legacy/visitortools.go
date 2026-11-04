package legacy

import (
	"context"
	"database/sql"
	"fmt"

	repo "github.com/vsriram/simple-host/internal/repository/legacy"
)

// VisitorToolWriteInput contains the actor, target and validated write action.
type VisitorToolWriteInput struct {
	SiteID, UserID, App, Action, Resource, Kind string
	Cap                                         int
}

// VisitorToolWrite counts the attempt, writes the resource and records its app label.
func (a *Adapter) VisitorToolWrite(ctx context.Context, in VisitorToolWriteInput) (any, error) {
	err := repo.CountVisitorAIWrite(ctx, a.database, in.UserID, in.SiteID, in.Cap)
	if err == sql.ErrNoRows {
		return nil, &Error{Code: "visitor_write_limit", Message: fmt.Sprintf("visitor_write_limit: daily limit of %d writes on this site reached; try again tomorrow (UTC)", in.Cap)}
	}
	if err != nil {
		return nil, err
	}
	// Attempts count too, so failures and concurrent retries cannot evade the cap.
	result, err := a.deps.VisitorResourceWrite(in.Action)
	if err != nil {
		return nil, err
	}
	if entry, ok := result.(map[string]any); ok {
		entry["mine"] = true
	}
	// A history marker has no earlier value and therefore cannot be restored.
	historyKind := "storage"
	if in.Kind == "collection" {
		historyKind = "list"
	}
	err = repo.RecordVisitorAIWrite(ctx, a.database, in.SiteID, in.Resource, in.Action+" "+in.Kind, in.UserID, in.App, historyKind)
	if err != nil {
		return map[string]any{"entry": result, "note": "Saved, but the app label could not be recorded in data history."}, nil
	}
	return result, nil
}
