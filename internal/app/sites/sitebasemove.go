package sites

import (
	"context"

	repo "github.com/vsriram/simple-host/internal/repository/sites"
	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

// MoveSiteBase moves stored platform addresses while preserving reserved names.
func (s *Service) MoveSiteBase(ctx context.Context, from, to string, apply bool) (repo.MoveSiteBaseReport, error) {
	return repo.MoveSiteBase(ctx, s.database, from, to, rules.LabelReserved, apply)
}
