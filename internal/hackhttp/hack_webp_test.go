package hackhttp

import "testing"

func TestHackM2ReviewRound2(t *testing.T) {
	if w, h, ok := webpSize([]byte("RIFF\x16\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\x0f\x00\x00\x09\x00\x00")); !ok || w != 16 || h != 10 {
		t.Fatalf("webp size %d %d %v", w, h, ok)
	}
}
