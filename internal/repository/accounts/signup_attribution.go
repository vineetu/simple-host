package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
)

func SetSignup(ctx context.Context, q Querier, userID string, s model.Signup) error {
	src := clip(s.Source)
	if src == "" || userID == "" {
		return nil
	}
	var detail sql.NullString
	if s.Detail != nil {
		b, err := json.Marshal(s.Detail)
		if err != nil {
			return err
		}
		detail = sql.NullString{String: string(b), Valid: true}
	}
	_, err := q.ExecContext(ctx, `
		UPDATE users SET signup_source = $2, signup_agent = NULLIF($3, ''), signup_method = NULLIF($4, ''), signup_detail = $5::jsonb
		 WHERE id = $1 AND signup_source IS NULL`,
		userID, src, clip(s.Agent), clip(s.Method), detail)
	return err
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > signupFieldMax {
		s = string(r[:signupFieldMax])
	}
	return s
}

const signupFieldMax = 80
