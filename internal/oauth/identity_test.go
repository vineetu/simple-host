package oauth

import "testing"

func TestParseGoogleUserInfo(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantID  string
		wantErr bool
	}{
		{
			name:   "openid userinfo",
			body:   `{"sub":"110169484474386276334","name":"Jane Doe","given_name":"Jane","picture":"https://example.test/p.jpg","locale":"en"}`,
			wantID: "110169484474386276334",
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
		})
	}
}
