package access

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) InternalKeyFor(userID string) (string, func(), error) {
	return repo.IssueInternalKey(userID)
}
func (s *Service) ListOAuthConnections(ctx context.Context, userID string) ([]model.OAuthConnection, error) {
	return repo.ListOAuthConnections(ctx, s.database, userID)
}
func (s *Service) NamespaceDomains() []string { return repo.PlatformDomains() }

// HashSecret is how every connector secret is stored and looked up. The
// secrets are 256-bit random values, so a fast hash is the right one: there is
// nothing to brute-force.
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
