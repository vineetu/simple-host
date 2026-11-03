package hackteams

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// teamKeyBinding returns the team a stored team key is bound to.
func TeamKeyBinding(ctx context.Context, q Querier, keyHash string) (teamID, memberID string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT b.team_id, b.user_id FROM event_team_keys b JOIN api_keys k ON k.id = b.key_id
		 WHERE k.key_hash = $1`, keyHash).Scan(&teamID, &memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrTeamKeyInactive
	}
	return teamID, memberID, err
}

// ReplaceTeamKey stores apiKey as the person's one team key for the event
// (their earlier one, if any, stops working in the same transaction).
func ReplaceTeamKey(ctx context.Context, tx *sql.Tx, eventID, teamID, userID, apiKey, name string) (TeamKeyInfo, error) {
	if _, err := RevokeTeamKeys(ctx, tx, eventID, userID); err != nil {
		return TeamKeyInfo{}, err
	}
	var k TeamKeyInfo
	err := tx.QueryRowContext(ctx, `
		INSERT INTO api_keys (key_hash, user_id, name, last4, scope)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, COALESCE(last4, ''), created_at`,
		hashAPIKey(apiKey), userID, name, keyLast4(apiKey), KeyScopeTeam).Scan(&k.KeyID, &k.Last4, &k.CreatedAt)
	if err != nil {
		return TeamKeyInfo{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_team_keys (key_id, event_id, team_id, user_id) VALUES ($1, $2, $3, $4)`,
		k.KeyID, eventID, teamID, userID); err != nil {
		return TeamKeyInfo{}, err
	}
	k.TeamID = teamID
	return k, nil
}
func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func keyLast4(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}

const KeyScopeTeam = "team"
