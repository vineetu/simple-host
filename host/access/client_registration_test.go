package access

import "testing"

func TestValidRedirectURI(t *testing.T) {
	good := []string{
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"https://claude.ai/api/mcp/auth_callback",
		"https://www.cursor.com/agents/mcp/oauth/callback",
		"https://vscode.dev/redirect",
		"http://localhost:33418/callback",
		"http://localhost:8787/callback",
		"http://127.0.0.1:8080/cb",
		"http://127.0.0.1/cb",
		"http://[::1]:9000/cb",
		"https://app.example.com/cb?x=1",
		// RFC 8252 private-use schemes: desktop apps' own sign-in returns.
		"cursor://anysphere.cursor-mcp/oauth/callback",
		"vscode://vscode.github-authentication/did-authenticate",
		"vscode-insiders://ms-vscode.remote/cb",
		"claude://claude.ai/mcp/callback",
		"windsurf://codeium.windsurf/oauth/callback",
		"zed://oauth/callback",
		"goose://oauth",
		"io.goose.app:/oauth",
		"com.example.app:/oauth2redirect",
		"com.example.app:///oauth2redirect",
	}
	bad := []string{
		"", "http://example.com/cb", "https://example.com/cb#frag", "javascript:alert(1)",
		"javascript://anything/%0aalert(1)", "JavaScript://x/", "data:text/html,hi", "data://x/",
		"file:///etc/passwd", "file://host/share", "vbscript://x/", "blob:https://a.example/x",
		"about:blank", "view-source://x/", "ws://localhost/cb", "wss://x.example/cb",
		"ms-msdt://x/", "ms-settings://x/", "search-ms://query", "intent://x/#Intent;end",
		"mailto://a@b.example", "chrome://settings/",
		"https://user:pw@example.com/cb", "/relative", "ftp://example.com/", "https:///nohost",
		"http://localhost.evil.com/cb", "https://exa mple.com/", "http://10.0.0.1/cb",
		"http://169.254.169.254/cb", "myapp:callback", "myapp:", "cursor://user@anysphere.cursor-mcp/cb",
		"cursor://anysphere.cursor-mcp/cb#x", "cursor://anysphere.cursor-mcp:443/cb", "x://a/",
		`cursor://anysphere.cursor-mcp\@evil.example/cb`,
		// Review 2026-10-15: no query on an app scheme, no hostless plain
		// scheme, and Apple/Windows installer and settings handlers refused.
		"itms-services:///?action=download-manifest&url=https://evil.example/m.plist",
		"itms-services://x/?action=download-manifest&url=https://evil.example/m.plist",
		"itms-apps://apps.apple.com/app/id1", "x-apple.systempreferences:/x",
		"cursor://anysphere.cursor-mcp/cb?url=https://evil.example", "cursor://anysphere.cursor-mcp/cb?",
		"myapp:/cb", "myapp:///cb", "steam://run/1", "chrome-extension://abc/cb",
		"microsoft-edge://x/", "smsto://x/", "facetime-audio://x/", "itunes://x/", "content://x/y",
	}
	for _, u := range good {
		if !ValidRedirectURI(u) {
			t.Errorf("refused %q", u)
		}
	}
	for _, u := range bad {
		if ValidRedirectURI(u) {
			t.Errorf("accepted %q", u)
		}
	}
}
