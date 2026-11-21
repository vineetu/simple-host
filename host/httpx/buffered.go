package httpx

import (
	"bytes"
	"net/http"
	"time"
)

// BodyReadTimeout allows the full body at 250,000 bytes/s plus ten minutes
// for overhead and variation, while retaining a finite deadline for stalled
// requests.
func BodyReadTimeout(allowedBytes int64) time.Duration {
	const bytesPerSecond = 250000
	transfer := time.Duration(allowedBytes/bytesPerSecond)*time.Second +
		time.Duration(allowedBytes%bytesPerSecond)*time.Second/bytesPerSecond
	return max(15*time.Minute, transfer+10*time.Minute)
}

// BufferedResponse holds a handler's status, header and body instead of
// sending them, so the caller can inspect or replay the response.
type BufferedResponse struct {
	underlying http.ResponseWriter
	header     http.Header
	status     int
	body       bytes.Buffer
}

func NewBufferedResponse(w http.ResponseWriter) *BufferedResponse {
	return &BufferedResponse{header: make(http.Header), underlying: w}
}

func (b *BufferedResponse) Unwrap() http.ResponseWriter { return b.underlying }

func (b *BufferedResponse) Header() http.Header {
	return b.header
}

func (b *BufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *BufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Status is the status written so far, 0 when none was.
func (b *BufferedResponse) Status() int { return b.status }

// Body is the bytes written so far.
func (b *BufferedResponse) Body() []byte { return b.body.Bytes() }
