package handler

import (
	"testing"

	"github.com/vsriram/simple-host/host/model"
)

// Pin the pre-R1-S4 HTTP answer, including authorization before the absent DB.
func TestMissingSQLiteReadResponse(t *testing.T) {
	s := newStorageTestSite(t)
	for _, preset := range []string{"wall", "private"} {
		saveStatus := 201
		if preset == "private" {
			saveStatus = 200
		}
		rules := map[string]string{"wall": rulesWall, "private": string(model.OwnerOnlyRules)}[preset]
		s.must(t, "PUT", "/resources/missing", storageRules("sqlite", rules), s.key, saveStatus)
		for _, path := range []string{"/sqlite/missing/tables/posts/rows/1", "/sqlite/missing/tables/posts/rows"} {
			for _, caller := range []string{"owner", "anon"} {
				t.Run(preset+"/"+caller+path, func(t *testing.T) {
					headers := s.callers["anon"]
					if caller == "owner" {
						headers = s.key
					}
					status, body := 400, "{\"error\":\"invalid row request\",\"code\":\"invalid_rows\"}\n"
					if preset == "private" && caller == "anon" {
						status = 403
						body = "{\"error\":\"read is not open to this visitor here\",\"code\":\"forbidden\"}\n"
					}
					got := s.must(t, "GET", path, nil, headers, status)
					if string(got.body) != body {
						t.Fatalf("body=%q want=%q", got.body, body)
					}
				})
			}
		}
	}
}
