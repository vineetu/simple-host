package analytics

import "testing"

func TestClassify(t *testing.T) {
	const chrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0 Mobile/15E148 Safari/604.1"

	tests := []struct {
		name       string
		remoteAddr string
		ua         string
		uri        string
		want       Class
	}{
		// The case that motivated all of this: 97% of recorded "traffic".
		{"local probe", "127.0.0.1", "nginx-directory/1.0", "/", ClassInfra},
		{"local probe ipv6", "::1", "nginx-directory/1.0", "/", ClassInfra},
		// Loopback wins even when the UA looks like a person.
		{"loopback wearing a browser UA", "127.0.0.1", chrome, "/", ClassInfra},
		{"hosted uptime checker", "216.144.250.150", "Mozilla/5.0+(compatible; UptimeRobot/2.0; http://uptimerobot.com/)", "/", ClassInfra},

		{"real desktop visitor", "85.11.167.152", chrome, "/", ClassPerson},
		{"real mobile visitor", "104.36.50.31", iphone, "/vineetu/eb2-wait/", ClassPerson},

		{"googlebot", "66.249.66.1", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "/", ClassBot},
		{"ai crawler", "1.2.3.4", "Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)", "/", ClassBot},
		{"security scanner", "45.148.10.125", "Mozilla/5.0 (l9scan/2.0.832; +https://leakix.net)", "/", ClassBot},
		{"curl", "1.2.3.4", "curl/8.5.0", "/", ClassBot},
		{"headless chrome", "1.2.3.4", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/151.0.0.0 Safari/537.36", "/", ClassBot},
		{"empty UA", "1.2.3.4", "", "/", ClassBot},
		{"dash UA", "1.2.3.4", "-", "/", ClassBot},
		// Seen in this box's own log.
		{"UA that is a URL", "94.175.60.15", "http://agent-deploy.dev/wp-admin/install.php?step=1", "/", ClassBot},
		// Browser-shaped UA, but the path gives it away.
		{"exploit path with browser UA", "1.2.3.4", chrome, "/wp-login.php", ClassBot},
		{"env probe with browser UA", "1.2.3.4", chrome, "/.env", ClassBot},
		{"query string is ignored for path matching", "1.2.3.4", chrome, "/wp-admin/install.php?step=1", ClassBot},

		// A normal page whose name merely contains a bot-ish word must stay human.
		{"innocent path", "1.2.3.4", iphone, "/robotics-club/", ClassPerson},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.remoteAddr, tc.ua, tc.uri); got != tc.want {
				t.Errorf("Classify(%q, %q, %q) = %q, want %q",
					tc.remoteAddr, tc.ua, tc.uri, got, tc.want)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	yes := []string{"127.0.0.1", "127.1.2.3", "::1", "[::1]", "127.0.0.1:54321"}
	no := []string{"", "10.0.0.1", "85.11.167.152", "not-an-ip", "2001:db8::1"}

	for _, s := range yes {
		if !isLoopback(s) {
			t.Errorf("isLoopback(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isLoopback(s) {
			t.Errorf("isLoopback(%q) = true, want false", s)
		}
	}
}
