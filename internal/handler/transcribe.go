package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
)

// TranscribeHandler turns a chat voice recording into text.
//
// The model runs on this box (Moonshine, CPU-only, ~7x faster than realtime) as
// a localhost service, so audio never leaves the host and there is no per-minute
// bill. This handler is only the public edge: it keeps the sign-in gate, the
// rate limit, and the size cap out here where the untrusted request arrives, and
// hands the bytes to a service that speaks no auth of its own.
//
// Disabled when TRANSCRIBE_URL is unset, exactly like the AI-create endpoint.
type TranscribeHandler struct {
	url         string
	client      *http.Client
	ipLimiter   *rateLimiter
	userLimiter *rateLimiter
}

// A voice prompt is seconds of speech. 25 MB is generous for that even at
// browser bitrates, and the STT service caps duration independently.
const maxAudioBytes = 25 << 20

func NewTranscribeHandler(url string) *TranscribeHandler {
	// The chat sends an interim pass every few seconds while the mic is open, so
	// one ordinary recording is a burst of calls, not one. Sized for that: a
	// 90-second recording is ~22 interim passes plus a final. Still real CPU on a
	// 4-core box, so the refill stays slow enough to bound scripted abuse.
	ipLimiter := newRateLimiter(60, 1.0/3.0)   // burst 60, +1 every 3s
	userLimiter := newRateLimiter(60, 1.0/3.0) // burst 60, +1 every 3s
	ipLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	userLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	return &TranscribeHandler{
		url: url,
		// Generously above the worst case: the service caps audio at 5 minutes,
		// which transcribes in well under a minute.
		client:      &http.Client{Timeout: 3 * time.Minute},
		ipLimiter:   ipLimiter,
		userLimiter: userLimiter,
	}
}

func (h *TranscribeHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/transcribe", authMW(http.HandlerFunc(h.transcribe)))
}

func (h *TranscribeHandler) transcribe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "sign in to use voice input"})
		return
	}
	if !h.ipLimiter.allow(clientIP(r)) || !h.userLimiter.allow(user.ID) {
		tooManyRequests(w)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAudioBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "that recording is too long"})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "no audio received"})
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not transcribe that — please try again"})
		return
	}
	// Pass the container through untouched. The service decodes with ffmpeg and
	// sniffs the format itself, which matters because browsers disagree: Chrome
	// records WebM/Opus and iOS Safari records MP4/AAC.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("transcribe: %v", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not transcribe that — please try again"})
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// The service's own message is written for a user ("could not decode that
		// audio"), so pass it through rather than flattening every failure into
		// one unhelpful string.
		var out errorResponse
		msg := "could not transcribe that — please try again"
		if json.Unmarshal(raw, &out) == nil && out.Error != "" {
			msg = out.Error
		}
		writeJSON(w, resp.StatusCode, errorResponse{Error: msg})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}
