package sites

import (
	"context"

	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// MoveSiteBase moves stored platform addresses while preserving reserved names.
func (s *Service) MoveSiteBase(ctx context.Context, from, to string, apply bool) (repo.MoveSiteBaseReport, error) {
	return repo.MoveSiteBase(ctx, s.database, from, to, rules.LabelReserved, apply)
}
