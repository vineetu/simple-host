package handler

import (
	"bytes"
	"net/http"
	"strings"
)

func serveAuthMarkdown(w http.ResponseWriter, r *http.Request) {
	name := "auth.md"
	if hackMode {
		name = "hack-auth.md"
	}
	body, err := staticFiles.ReadFile("static/" + name)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if hackMode {
		body = hackInstanceText(bytes.ReplaceAll(body, []byte("{sign_in}"), []byte(authSignInMethods())))
	} else if !emailSignInEnabled {
		body = hostAuthWithoutEmail(body)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	http.ServeContent(w, r, "auth.md", skillsModTime, bytes.NewReader(body))
}

// These are the same configuration facts used by the instance's llms.txt.
func authSignInMethods() string {
	var ways []string
	if emailSignInEnabled {
		ways = append(ways, "an emailed code")
	}
	for _, provider := range instanceFacts.SignInProviders {
		switch provider {
		case "google":
			ways = append(ways, "Google")
		case "github":
			ways = append(ways, "GitHub")
		case "oidc":
			label := instanceFacts.OIDCLabel
			if label == "" {
				label = "your company account"
			}
			ways = append(ways, label+" (company OpenID Connect)")
		}
	}
	text := "Browser sign-in is unavailable on this server. Ask the operator about access."
	if len(ways) > 0 {
		text = "Sign in in the browser with " + strings.Join(ways, " or ") + "."
	}
	if !emailSignInEnabled {
		text += " Email-code sign-in is unavailable: EMAIL_SIGNIN=off or no email sender is configured. Do not request or verify email codes."
	}
	return text
}

// Keep the hosted Host guide unchanged. Boxes without email retain the OAuth
// protocol reference but must not instruct a person to request an email code.
func hostAuthWithoutEmail(body []byte) []byte {
	var paragraphs []string
	for _, paragraph := range strings.Split(string(body), "\n\n") {
		switch {
		case strings.HasPrefix(paragraph, "This server supports two methods:"):
			paragraph = "This server supports OAuth 2.1 for MCP clients and chat apps, and locally configured API keys. " + authSignInMethods()
		case strings.HasPrefix(paragraph, "Use OAuth if"):
			paragraph = "Use the connector's browser sign-in when available. For direct REST calls, use an API key supplied locally by the operator; keep it out of chat."
		case strings.HasPrefix(paragraph, "For API keys, there is no separate"), strings.HasPrefix(paragraph, "For an API key, POST"), strings.HasPrefix(paragraph, "For API keys, `/v1/auth/verify`"):
			continue
		case strings.HasPrefix(paragraph, "Email sign-in can return"):
			paragraph = "Email-code requests are unavailable (403 `email_signin_off`). Owner calls can also return 403 `deploy_only_key` or `account_suspended`."
		}
		paragraphs = append(paragraphs, paragraph)
	}
	return []byte(strings.Join(paragraphs, "\n\n"))
}
