package oauth

import "testing"

func TestParseGoogleUserInfo(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantID       string
		wantEmail    string
		wantVerified bool
		wantErr      bool
	}{
		{
			name:   "openid userinfo without email",
			body:   `{"sub":"110169484474386276334","name":"Jane Doe","given_name":"Jane","picture":"https://example.test/p.jpg","locale":"en"}`,
			wantID: "110169484474386276334",
		},
		{
			name:         "verified email",
			body:         `{"sub":"110169484474386276334","email":"Jane@Example.COM","email_verified":true}`,
			wantID:       "110169484474386276334",
			wantEmail:    "jane@example.com",
			wantVerified: true,
		},
		{
			name:      "unverified email",
			body:      `{"sub":"1","email":"jane@example.com","email_verified":false}`,
			wantID:    "1",
			wantEmail: "jane@example.com",
		},
		{
			name:      "email_verified missing is not verified",
			body:      `{"sub":"1","email":"jane@example.com"}`,
			wantID:    "1",
			wantEmail: "jane@example.com",
		},
		{
			name:      "email_verified string is not a JSON boolean",
			body:      `{"sub":"1","email":"jane@example.com","email_verified":"true"}`,
			wantID:    "1",
			wantEmail: "jane@example.com",
		},
		{
			name:    "missing sub",
			body:    `{"name":"Jane Doe"}`,
			wantErr: true,
		},
		{
			name:    "empty sub",
			body:    `{"sub":""}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    `not-json`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseGoogleUserInfo([]byte(c.body))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Provider != "google" || got.UserID != c.wantID {
				t.Fatalf("got %+v, want google/%s", got, c.wantID)
			}
			if got.Email != c.wantEmail || got.EmailVerified != c.wantVerified {
				t.Fatalf("email %+v verified=%v, want %q verified=%v", got.Email, got.EmailVerified, c.wantEmail, c.wantVerified)
			}
		})
	}
}

func TestParseGitHubUserInfo(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantID  string
		wantErr bool
	}{
		{
			name:   "numeric id",
			body:   `{"login":"octocat","id":1,"type":"User"}`,
			wantID: "1",
		},
		{
			name:   "large id",
			body:   `{"id":9007199254740991,"login":"big"}`,
			wantID: "9007199254740991",
		},
		{
			name:    "missing id",
			body:    `{"login":"octocat"}`,
			wantErr: true,
		},
		{
			name:    "id as string",
			body:    `{"id":"1","login":"octocat"}`,
			wantErr: true,
		},
		{
			name:    "not json",
			body:    `{`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseGitHubUserInfo([]byte(c.body))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Provider != "github" || got.UserID != c.wantID {
				t.Fatalf("got %+v, want github/%s", got, c.wantID)
			}
			if got.Email != "" || got.EmailVerified {
				t.Fatalf("userinfo must not set email, got %+v", got)
			}
		})
	}
}

func TestParseGitHubEmails(t *testing.T) {
	email, ok := ParseGitHubEmails([]byte(`[
		{"email":"unverified@example.com","primary":true,"verified":false},
		{"email":"Jane@Example.COM","primary":true,"verified":true},
		{"email":"other@example.com","primary":false,"verified":true}
	]`))
	if !ok || email != "jane@example.com" {
		t.Fatalf("got %q verified=%v, want jane@example.com true", email, ok)
	}

	email, ok = ParseGitHubEmails([]byte(`[
		{"email":"only@example.com","primary":true,"verified":false}
	]`))
	if ok || email != "" {
		t.Fatalf("unverified primary must refuse, got %q verified=%v", email, ok)
	}

	email, ok = ParseGitHubEmails([]byte(`{"email":"not-an-array"}`))
	if ok || email != "" {
		t.Fatalf("bad payload must refuse, got %q verified=%v", email, ok)
	}
}
