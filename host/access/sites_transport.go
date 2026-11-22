package access

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/access/repo"
)

func (s *Service) GetShowcaseBio(ctx context.Context, userID string) (string, error) {
	return repo.GetShowcaseBio(ctx, s.database, userID)
}

// SetShowcaseBio saves the account's showcase bio, trimmed, and returns it as
// stored. Refusal bio_too_long (400) when it has more than max characters.
func (s *Service) SetShowcaseBio(ctx context.Context, userID, bio string, max int) (string, error) {
	bio, err := normalizeBio(bio, max)
	if err != nil {
		return "", err
	}
	return bio, repo.SetShowcaseBio(ctx, s.database, userID, bio)
}

func normalizeBio(bio string, max int) (string, error) {
	bio = strings.TrimSpace(bio)
	if utf8.RuneCountInString(bio) > max {
		return "", &Refusal{http.StatusBadRequest, "bio_too_long", fmt.Sprintf("bio must be at most %d characters", max)}
	}
	return bio, nil
}
