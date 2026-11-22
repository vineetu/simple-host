package deploy

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

func TestReadPhotoUploads(t *testing.T) {
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	type part struct{ field, name string }
	read := func(parts []part, data []byte, limit int64) ([]PhotoUpload, error) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for _, p := range parts {
			w, _ := writer.CreateFormFile(p.field, p.name)
			w.Write(data)
		}
		writer.Close()
		r := httptest.NewRequest("POST", "/", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, limit)
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		return ReadPhotoUploads(reader)
	}
	many := make([]part, sites.PhotoCount+1)
	for i := range many {
		many[i] = part{"photos", "front.jpg"}
	}
	for _, tc := range []struct {
		name     string
		parts    []part
		data     []byte
		limit    int64
		message  string
		tooLarge bool
	}{
		{"none", nil, jpg.Bytes(), 1 << 20, "Choose at least one photo.", false},
		{"too many", many, jpg.Bytes(), 1 << 30, "Choose up to 30 photos at a time.", false},
		{"field", []part{{"other", "front.jpg"}}, jpg.Bytes(), 1 << 20, "Use a photo name with lowercase letters, numbers, and hyphens, followed by .webp or .jpg.", false},
		{"name", []part{{"photos", "Front.jpg"}}, jpg.Bytes(), 1 << 20, "Use a photo name with lowercase letters, numbers, and hyphens, followed by .webp or .jpg.", false},
		{"extension", []part{{"photos", "front.webp"}}, jpg.Bytes(), 1 << 20, "The photo extension does not match its contents.", false},
		{"contents", []part{{"photos", "front.jpg"}}, []byte("<html/>"), 1 << 20, "This photo is incomplete or is not a supported WebP or JPEG image.", false},
		{"photo over cap", []part{{"photos", "front.jpg"}}, make([]byte, sites.PhotoMaxBytes()+1), 1 << 30, "A photo is too large. Choose it again to make a smaller copy.", true},
		{"body over cap", []part{{"photos", "front.jpg"}}, jpg.Bytes(), 300, "The photo upload is incomplete.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := read(tc.parts, tc.data, tc.limit)
			var e *PhotoUploadError
			if !errors.As(err, &e) || e.Message != tc.message || e.TooLarge != tc.tooLarge {
				t.Fatalf("got %#v", err)
			}
		})
	}
	got, err := read([]part{{"photos", "front.jpg"}, {"photos", "back.jpg"}}, jpg.Bytes(), 1<<20)
	if err != nil || len(got) != 2 || got[1].Name != "back.jpg" || got[0].Width != 8 || got[0].Height != 6 {
		t.Fatalf("accepted: %+v %v", got, err)
	}
	if photos := UploadedPhotos(got); len(photos) != 2 || !bytes.Equal(photos[0].Data, jpg.Bytes()) {
		t.Fatal("uploaded photos")
	}
}
