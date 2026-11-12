package hackevents

import (
	"regexp"
	"strings"
	"testing"
)

func TestAnnouncementHTML(t *testing.T) {
	cases := []struct {
		name, in string
		has      []string
		not      []string
	}{
		{"plain text escaped", `Hi <b>there</b> & "you"`, []string{`Hi &lt;b&gt;there&lt;/b&gt; &amp; &#34;you&#34;`}, []string{"<b>"}},
		{"all text escapes", `Text < > & "double" 'single'`, []string{`Text &lt; &gt; &amp; &#34;double&#34; &#39;single&#39;`}, nil},
		{"bold", `a **big** day`, []string{`a <strong>big</strong> day`}, nil},
		{"unclosed bold stays text", `a **big day`, []string{`a **big day`}, []string{"<strong>"}},
		{"empty bold stays text", `a **** b`, []string{`a **** b`}, []string{"<strong>"}},
		{"bullets", "- one\n- **two**\n\nafter", []string{`<ul><li>one</li><li><strong>two</strong></li></ul><p>after</p>`}, nil},
		{"line breaks and paragraphs", "a\nb\n\nc", []string{`<p>a<br>b</p><p>c</p>`}, nil},
		{"crlf", "a\r\nb", []string{`<p>a<br>b</p>`}, nil},
		{"labelled link", `See [the docs](https://example.com/a?b=1&c=2) now`, []string{`<a href="https://example.com/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer">the docs</a> now`}, nil},
		{"bare url inline", `Read https://example.com/x, then go.`, []string{`<a href="https://example.com/x" target="_blank" rel="noopener noreferrer">https://example.com/x</a>, then go.`}, nil},
		{"bare url in parens", `(see https://example.com/x)`, []string{`>https://example.com/x</a>)`}, nil},
		{"unicode path encoded", `https://example.com/caf%C3%A9 https://example.com/café`, []string{`href="https://example.com/caf%C3%A9"`}, []string{`href="https://example.com/café"`}},
		{"javascript scheme", `[x](javascript:alert(1)) javascript:alert(1)`, nil, []string{"<a ", "href"}},
		{"data scheme", `[x](data:text/html;base64,PHNjcmlwdD4=) data:text/html,<script>`, nil, []string{"<a ", "<script>"}},
		{"uppercase javascript", `[x](JaVaScRiPt:alert(1))`, nil, []string{"<a "}},
		{"mailto not linked", `[x](mailto:a@b.co) ftp://example.com/x`, nil, []string{"<a "}},
		{"user info refused", `https://zoom.us@evil.example/j/1`, nil, []string{"<a ", "ann-card"}},
		{"attribute breaking", `[x](https://example.com/"onmouseover="alert(1))`, nil, []string{`onmouseover="`, `" onmouse`}},
		{"quote in bare url", `https://example.com/a"onclick="x`, []string{`>https://example.com/a</a>`}, []string{`onclick="`}},
		{"label with markup", `[<img src=x onerror=alert(1)>](https://example.com)`, []string{`&lt;img src=x onerror=alert(1)&gt;`}, []string{"<img"}},
		{"nested link markup", `[a [b](https://x.example)](https://y.example)`, []string{`>b</a>`}, nil},
		{"bidi and zero width", "https://example.com/a\u202eb https://example.com\u200b/x", nil, []string{`href="https://example.com/ab`, `href="https://example.com/x`}},
		{"non-ascii host refused", `https://exаmple.com/x`, nil, []string{"<a "}},
		{"space in url", `[x](https://example.com/a b)`, nil, []string{`href="https://example.com/a b"`, `>x</a>`}},
		{"label empty", `[](https://example.com)`, []string{`>https://example.com</a>`}, nil},
		{"html entity text not decoded", `&lt;script&gt;`, []string{`&amp;lt;script&amp;gt;`}, nil},
		{"zoom card bare", "Join:\nhttps://us02web.zoom.us/j/123456?pwd=abc", []string{`<p>Join:</p>`, `class="ann-card ann-call"`, `Zoom`, `Join the call`, `us02web.zoom.us`}, nil},
		{"zoom card with label", "[Kickoff call](https://zoom.us/j/1)", []string{`ann-call`, `Kickoff call`, `Join the call`}, nil},
		{"meet card", "https://meet.google.com/abc-defg-hij", []string{`Google Meet`, `Join the call`}, nil},
		{"teams card", "https://teams.microsoft.com/l/meetup-join/xyz\nhttps://teams.live.com/meet/123", []string{`Microsoft Teams`}, nil},
		{"youtube watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10", []string{`ann-video`, `src="https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"`, `Watch the video`, `YouTube`}, nil},
		{"youtube short", "https://youtu.be/dQw4w9WgXcQ?si=abc", []string{`ann-video`, `vi/dQw4w9WgXcQ/`}, nil},
		{"youtube shorts path", "https://youtube.com/shorts/dQw4w9WgXcQ", []string{`ann-video`}, nil},
		{"youtube bad id is a link", "https://www.youtube.com/watch?v=abc\"onerror=1", nil, []string{"ann-card", "<img"}},
		{"youtube channel is a link", "https://www.youtube.com/@someone", []string{`<a href=`}, []string{"ann-card"}},
		{"look-alike hosts are not cards", "https://notzoom.us/j/1\nhttps://zoom.us.evil.example/j/1\nhttps://evilmeet.google.com.example/x", []string{`<a href=`}, []string{"ann-card"}},
		{"other link stays a link", "https://example.com/page", []string{`<p><a href="https://example.com/page"`}, []string{"ann-card"}},
		{"url in a sentence is not a card", "Join us at https://zoom.us/j/1 tonight", []string{`<a href="https://zoom.us/j/1"`}, []string{"ann-card"}},
		{"balanced parentheses in a labelled address", `[Go](https://en.wikipedia.org/wiki/Go_(programming_language)) rocks`, []string{`href="https://en.wikipedia.org/wiki/Go_(programming_language)"`, `>Go</a> rocks`}, []string{`</a>)`}},
		{"no-break space ends a bare address", "Read https://example.com\u00a0now", []string{`href="https://example.com"`, "</a>\u00a0now"}, nil},
		{"dash before a bare address", "Join\u2014https://example.com/x", []string{`href="https://example.com/x"`}, nil},
		{"letter before a bare address", "xhttps://example.com/x", nil, []string{"<a "}},
		{"unlabelled card shows the service only", "https://zoom.us/j/1", []string{`<span class="ann-card-label">Zoom</span>`}, []string{"ann-card-svc"}},
		{"labelled card shows the service above", "[Kickoff](https://zoom.us/j/1)", []string{`<span class="ann-card-svc">Zoom</span><span class="ann-card-label">Kickoff</span>`}, nil},
		{"labelled link in a sentence is not a card", "Join [the call](https://zoom.us/j/1) tonight", nil, []string{"ann-card"}},
		{"url in a bullet is not a card", "- https://zoom.us/j/1", []string{`<li><a href=`}, []string{"ann-card"}},
		{"card label escaped", "[<script>](https://zoom.us/j/1)", []string{`&lt;script&gt;`}, []string{"<script>"}},
		{"http allowed", "http://example.com/x", []string{`href="http://example.com/x"`}, nil},
		{"empty", "", nil, []string{"<"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AnnouncementHTML(c.in)
			for _, s := range c.has {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in %s", s, got)
				}
			}
			for _, s := range c.not {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q in %s", s, got)
				}
			}
		})
	}
}

func TestAnnouncementHTMLNeverEmitsRawTags(t *testing.T) {
	inputs := []string{
		"<script>alert(1)</script>", "<img src=x onerror=alert(1)>", "[a](https://e.com)<b>", "**<i>**",
		"- <iframe src=x>", "https://e.com/<svg onload=1>", "\"><svg/onload=1>", "<a href=\"javascript:1\">x</a>",
	}
	allowed := regexpTags()
	for _, in := range inputs {
		for name, out := range map[string]string{"web": AnnouncementHTML(in), "email": AnnouncementEmailHTML(in)} {
			for _, tag := range allowed.FindAllString(out, -1) {
				if !strings.HasPrefix(tag, "<a ") && !strings.HasPrefix(tag, "</a") && !strings.HasPrefix(tag, "<p") && !strings.HasPrefix(tag, "</p") &&
					!strings.HasPrefix(tag, "<ul") && !strings.HasPrefix(tag, "</ul") && !strings.HasPrefix(tag, "<li") && !strings.HasPrefix(tag, "</li") &&
					!strings.HasPrefix(tag, "<strong") && !strings.HasPrefix(tag, "</strong") && tag != "<br>" && !strings.HasPrefix(tag, "<span") && !strings.HasPrefix(tag, "</span") &&
					!strings.HasPrefix(tag, "<img") && !strings.HasPrefix(tag, "<svg") && !strings.HasPrefix(tag, "</svg") && !strings.HasPrefix(tag, "<rect") && !strings.HasPrefix(tag, "<path") &&
					!strings.HasPrefix(tag, "<table") && !strings.HasPrefix(tag, "</table") && !strings.HasPrefix(tag, "<tr") && !strings.HasPrefix(tag, "</tr") &&
					!strings.HasPrefix(tag, "<td") && !strings.HasPrefix(tag, "</td") && !strings.HasPrefix(tag, "<div") && !strings.HasPrefix(tag, "</div") {
					t.Errorf("%s output for %q has unexpected tag %q: %s", name, in, tag, out)
				}
				if strings.Contains(tag, "onerror") || strings.Contains(tag, "onload") || strings.Contains(tag, "onclick") {
					t.Errorf("%s output for %q has event handler: %s", name, in, tag)
				}
			}
		}
	}
}

func TestAnnouncementEmailHTMLButton(t *testing.T) {
	got := AnnouncementEmailHTML("Kickoff:\n[Join the kickoff](https://zoom.us/j/1?pwd=a&b=2)\n\nWatch:\nhttps://youtu.be/dQw4w9WgXcQ\n\n- **Bring** a laptop")
	for _, s := range []string{
		`<table role="presentation"`, `href="https://zoom.us/j/1?pwd=a&amp;b=2"`, `Join the call`, `Watch the video`, `Join the kickoff`,
		`<li style="margin:0 0 4px;"><strong>Bring</strong> a laptop</li>`, `bgcolor="#a33f31"`,
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in %s", s, got)
		}
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "<iframe") || strings.Contains(got, "<img") {
		t.Errorf("unexpected markup: %s", got)
	}
}

func TestAnnouncementText(t *testing.T) {
	got := AnnouncementText("Hello **team**\n\n[Join the kickoff](https://zoom.us/j/1)\nhttps://example.com/x.\n- see [https://a.example](https://a.example)\n[x](javascript:alert(1))")
	want := "Hello team\n\nJoin the kickoff (https://zoom.us/j/1)\nhttps://example.com/x.\n- see https://a.example\n[x](javascript:alert(1))"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	if plain := "No markup here.\n\n  indented\n"; AnnouncementText(plain) != plain {
		t.Errorf("plain text changed: %q", AnnouncementText(plain))
	}
}

func regexpTags() *regexp.Regexp { return regexp.MustCompile(`</?[a-zA-Z][^>]*>`) }

func TestAnnouncementURLAndNestedMarkup(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "JAVASCRIPT:alert(1)", "data:text/html,x", "vbscript:msgbox(1)", "//evil.example", "http:/x", "https://user@host", "https://example.com/a b", "https://example.com/a\tb", "https://example.com/a\x00b", "https://example.com/a\u200bb", "https://example.com/a\u202eb", "https://example.com/a\u2066b"} {
		t.Run(raw, func(t *testing.T) {
			if _, ok := cleanAnnouncementURL(raw); ok {
				t.Fatalf("accepted %q", raw)
			}
			got := AnnouncementHTML("[label](" + raw + ")")
			if strings.Contains(got, `>label</a>`) {
				t.Fatalf("linked invalid URL: %s", got)
			}
		})
	}
	for _, in := range []string{`[<>&"'](https://example.com/a?x=1&y=2)`, `[x](https://example.com/"onmouseover="x)`, `[x](https://example.com/'onmouseover='x)`, `[**x**](https://example.com)`, `**[a](https://example.com)**`, `[a]([b](https://example.com))`} {
		for _, render := range []func(string) string{AnnouncementHTML, AnnouncementEmailHTML} {
			out := render(in)
			for _, tag := range regexpTags().FindAllString(out, -1) {
				if strings.Contains(tag, ` onmouseover=`) || strings.Contains(tag, `<script`) {
					t.Fatalf("attribute/tag injection: %s", out)
				}
			}
			for _, match := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(out, -1) {
				if !strings.HasPrefix(match[1], "https://") && !strings.HasPrefix(match[1], "http://") {
					t.Fatalf("invalid href: %s", out)
				}
			}
		}
	}
	for _, host := range []string{"evilzoom.us", "zoom.us.evil.com", "meet.google.com.evil.io"} {
		if got := AnnouncementHTML("https://" + host + "/x"); strings.Contains(got, "ann-card") {
			t.Fatal(got)
		}
	}
	for _, in := range []string{"https://youtube.com/watch?v=invalid", "https://youtu.be/invalid"} {
		got := AnnouncementHTML(in)
		if strings.Contains(got, "ann-card") || !strings.Contains(got, `<a href=`) {
			t.Fatal(got)
		}
	}
	if got := AnnouncementHTML(`**[a](https://example.com)**`); !strings.Contains(got, `<strong><a href="https://example.com"`) {
		t.Fatal(got)
	}
	if got := AnnouncementHTML(`[<>&"'](https://example.com)`); !strings.Contains(got, `&lt;&gt;&amp;&#34;&#39;`) {
		t.Fatal(got)
	}
}
