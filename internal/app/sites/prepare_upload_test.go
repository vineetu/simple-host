package sites

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/vsriram/simple-host/internal/model"
)

func TestUploadPreparationPrecedence(t *testing.T) {
	svc := &Service{deps: Deps{MaxFilesPerSite: 100}}
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	f, err := zw.Create("program.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("executable")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, digest string
		strict       bool
		want         string
	}{
		{"integrity before extraction", "wrong", true, "digest_mismatch"},
		{"root before extensions", "", true, "missing_index"},
		{"host extensions without root requirement", "", false, "blocked_file_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.PrepareArchive(ArchivePreparationInput{Body: body.Bytes(), Filename: "site.zip", ContentDigest: tc.digest, Strict: tc.strict})
			var e *UploadError
			if !errors.As(err, &e) || e.Code != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestFilePreparationAccountBudget(t *testing.T) {
	svc := &Service{deps: Deps{MaxFilesPerSite: 100}}
	files := map[string][]byte{"index.html": []byte("ok")}
	// The default body allowance does not narrow the extractor's independent caps.
	if _, _, err := svc.PrepareFiles(FilePreparationInput{Files: files, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.PrepareFiles(FilePreparationInput{Files: files, Limit: 1, Override: true})
	var e *UploadError
	if !errors.As(err, &e) || !e.TooLarge {
		t.Fatalf("override should enforce its byte budget: %v", err)
	}
}

func TestUploadNameDecisionsBeforeOptionsAndDatabase(t *testing.T) {
	// A nil database makes accidental reads before the name/option decisions visible.
	svc := &Service{}
	user := &model.User{ID: "account"}
	for _, tc := range []struct{ name, publish, create, want string }{
		{"bad/name", "bad", "bad", "invalid_name"},
		{"valid-name", "bad", "bad", "invalid_request"},
		{"valid-name", "true", "bad", "invalid_request"},
	} {
		_, err := svc.UpdatePreflight(context.Background(), UpdatePreflightInput{User: user, Name: tc.name, Publish: tc.publish, Create: tc.create})
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.want {
			t.Fatalf("%+v: got %v", tc, err)
		}
	}
	// Existing reserved names remain valid update targets without create requested.
	if _, err := svc.UpdatePreflight(context.Background(), UpdatePreflightInput{User: user, Name: "admin"}); err != nil {
		t.Fatal(err)
	}
}
