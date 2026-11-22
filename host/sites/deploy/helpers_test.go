package deploy_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"mime/multipart"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// photoJPEG is a blank w×h JPEG.
func photoJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// addPhotos posts one photo named name to site with key (none when "").
func addPhotos(t *testing.T, a *fixture.App, key, site, name string, photo []byte) rt.Resp {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("photos", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(photo)
	_ = mw.Close()
	h := map[string]string{"Content-Type": mw.FormDataContentType()}
	if key != "" {
		h["X-API-Key"] = key
	}
	return a.At(t, "POST", rt.SiteDomain, "/v1/sites/"+site+"/photos", &body, h)
}
