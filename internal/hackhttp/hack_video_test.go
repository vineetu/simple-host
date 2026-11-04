package hackhttp

import (
	"testing"
)

func TestVideoEmbed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=10s", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"http://vimeo.com/123456789", "https://player.vimeo.com/video/123456789"},
		{"https://www.loom.com/share/0123456789abcdef0123456789abcdef", "https://www.loom.com/embed/0123456789abcdef0123456789abcdef"},
		{"https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/view?usp=sharing", "https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/preview"},
		// Hostile or off-table links never embed.
		{"https://youtube.com.evil.test/watch?v=dQw4w9WgXcQ", ""},
		{"https://evil.test/youtube.com/watch?v=dQw4w9WgXcQ", ""},
		{"javascript:alert(1)//youtu.be/dQw4w9WgXcQ", ""},
		{"https://youtu.be/dQw4w9WgXc\"", ""},
		{`https://www.youtube.com/watch?v=dQw4w9WgX"Q`, ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQx", ""},
		{"https://youtu.be/dQw4w9WgXcQ/extra", ""},
		{"https://user@youtu.be/dQw4w9WgXcQ", ""},
		{"https://youtu.be:8443/dQw4w9WgXcQ", ""},
		{"https://drive.google.com/file/d/short/view", ""},
		{"https://drive.google.com/open?id=1AbCdEfGhIjKlMnOpQrStUvWx", ""},
		{"https://vimeo.com/channels/staff/123", ""},
		{"https://www.loom.com/share/NOTHEX0123456789abcdef0123456789", ""},
		{"https://www.dropbox.com/s/abc/demo.mp4", ""},
		{"ftp://youtu.be/dQw4w9WgXcQ", ""},
		{"", ""},
	} {
		if got := videoEmbed(c.in); got != c.want {
			t.Errorf("videoEmbed(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
