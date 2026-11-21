package sites

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/host/sites/photos"
)

var photoName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}\.(webp|jpg)$`)

// PhotoUpload is one checked photo from an upload request.
type PhotoUpload struct {
	Photo
	Width, Height int
}

// PhotoUploadError refuses a photo upload. TooLarge marks a size refusal.
type PhotoUploadError struct {
	Message  string
	TooLarge bool
}

func (e *PhotoUploadError) Error() string { return e.Message }

// ReadPhotoUploads reads and checks every part of a photo upload, refusing
// the whole upload at the first bad part. Errors are *PhotoUploadError.
func ReadPhotoUploads(reader *multipart.Reader) ([]PhotoUpload, error) {
	maxBytes, maxDimension := int64(PhotoMaxBytes()), PhotoMaxDimension()
	uploads := []PhotoUpload{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, photoReadError("The photo upload is incomplete or too large.", err)
		}
		if len(uploads) == PhotoCount {
			return nil, &PhotoUploadError{Message: "Choose up to 30 photos at a time."}
		}
		upload, err := readPhotoPart(part, maxBytes, maxDimension)
		if err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if len(uploads) == 0 {
		return nil, &PhotoUploadError{Message: "Choose at least one photo."}
	}
	return uploads, nil
}

func readPhotoPart(part *multipart.Part, maxBytes int64, maxDimension int) (PhotoUpload, error) {
	// Part.FileName applies filepath.Base: inspect the original parameter instead.
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	filename := params["filename"]
	if err != nil || part.FormName() != "photos" || !photoName.MatchString(filename) {
		return PhotoUpload{}, &PhotoUploadError{Message: "Use a photo name with lowercase letters, numbers, and hyphens, followed by .webp or .jpg."}
	}
	data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
	if int64(len(data)) > maxBytes {
		return PhotoUpload{}, &PhotoUploadError{Message: "A photo is too large. Choose it again to make a smaller copy.", TooLarge: true}
	}
	if err != nil {
		return PhotoUpload{}, photoReadError("The photo upload is incomplete.", err)
	}
	cfg, ext, err := photos.Validate(data, maxDimension)
	if err != nil {
		return PhotoUpload{}, &PhotoUploadError{Message: err.Error()}
	}
	if !strings.HasSuffix(filename, "."+ext) {
		return PhotoUpload{}, &PhotoUploadError{Message: "The photo extension does not match its contents."}
	}
	return PhotoUpload{Photo{Name: filename, Data: data}, cfg.Width, cfg.Height}, nil
}

func photoReadError(message string, err error) *PhotoUploadError {
	var tooLarge *http.MaxBytesError
	return &PhotoUploadError{Message: message, TooLarge: errors.As(err, &tooLarge)}
}

// UploadedPhotos returns the photos to save from checked uploads.
func UploadedPhotos(uploads []PhotoUpload) []Photo {
	out := make([]Photo, len(uploads))
	for i, u := range uploads {
		out[i] = u.Photo
	}
	return out
}
