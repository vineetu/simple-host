package core

import (
	"testing"
)

func TestVideoEmbed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"http://vimeo.com/123456789", "https://player.vimeo.com/video/123456789"},
		{"https://www.loom.com/share/0123456789abcdef0123456789abcdef", "https://www.loom.com/embed/0123456789abcdef0123456789abcdef"},
		{"https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/view?usp=sharing", "https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/preview"},
		// Hostile or off-table links never embed.
		{"https://youtube.com.evil.test/watch?v=dQw4w9WgXcQ", ""},
		{"javascript:alert(1)//youtu.be/dQw4w9WgXcQ", ""},
		{"https://user@youtu.be/dQw4w9WgXcQ", ""},
		{"https://drive.google.com/file/d/short/view", ""},
		{"", ""},
	} {
		if got := VideoEmbed(c.in); got != c.want {
			t.Errorf("VideoEmbed(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
