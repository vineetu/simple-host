package sitestest

import (
	"bytes"
	"context"
	"mime/multipart"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// A retried photo save can ask "was this exact batch already committed?"
// (X-Photo-Reconcile). It gets the committed version back, each photo in its
// own file, and an unconfirmed batch is refused with no new version.
func TestRDPhotoReconcile(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "reconcile")
	a.Deploy(t, p, "home")
	jpg := photoJPEG(t, 8, 6)
	send := func(names []string, images [][]byte, reconcile bool) rt.Resp {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for i, name := range names {
			part, err := mw.CreateFormFile("photos", name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write(images[i])
		}
		_ = mw.Close()
		headers := map[string]string{"X-API-Key": p.Key, "Content-Type": mw.FormDataContentType()}
		if reconcile {
			headers["X-Photo-Reconcile"] = "1"
		}
		return a.At(t, "POST", rt.SiteDomain, "/v1/sites/home/photos", &body, headers)
	}
	names, images := []string{"front.jpg", "front.jpg"}, [][]byte{jpg, jpg}
	if r := send(names, images, true); r.Status != 409 {
		t.Fatalf("absent batch: %d", r.Status)
	}
	if r := send(names, images, false); r.Status != 200 {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	for i := 0; i < 2; i++ {
		r := send(names, images, true)
		if r.Status != 200 || r.JSON(t)["version"] != float64(2) || r.JSON(t)["reconciled"] != true {
			t.Fatalf("reconcile: %d %s", r.Status, r.Body)
		}
		photos := r.JSON(t)["photos"].([]any)
		if photos[0].(map[string]any)["path"] == photos[1].(map[string]any)["path"] {
			t.Fatal("same file counted twice")
		}
	}
	// Exact names take precedence over another photo's collision suffix.
	if r := send([]string{"front.jpg", "front-2.jpg"}, images, true); r.Status != 200 {
		t.Fatalf("overlapping names: %d %s", r.Status, r.Body)
	}
	for _, tc := range []struct {
		names  []string
		images [][]byte
	}{
		{[]string{"front.jpg", "front.jpg", "front.jpg"}, [][]byte{jpg, jpg, jpg}},
		{[]string{"front.jpg"}, [][]byte{photoJPEG(t, 9, 6)}},
		{[]string{"other.jpg"}, [][]byte{jpg}},
	} {
		if r := send(tc.names, tc.images, true); r.Status != 409 {
			t.Fatalf("unconfirmed: %d %s", r.Status, r.Body)
		}
	}
	site, err := sitesrepo.GetSiteByUser(context.Background(), a.Database, p.ID, "home")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := sitesrepo.ListVersionsBySite(context.Background(), a.Database, site.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("reconciliation wrote a version: %d %v", len(versions), err)
	}
}
