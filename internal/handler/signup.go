package handler

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// Where an account came from (users.signup_*), recorded once when it is
// created and shown on the admin page. It is a label, not a permission:
// nothing reads it to decide anything, so a caller that lies about itself only
// mislabels its own account. Never an IP address.

// signupAgentMax bounds the agent name kept for an API sign-up.
const signupAgentMax = 40

// recordSignup stores s for a just-created account. A failure is logged and
// never fails the sign-in: the label is not worth refusing a person over.
func recordSignup(ctx context.Context, q db.Querier, userID string, s db.Signup) {
	if err := db.SetSignup(ctx, q, userID, s); err != nil {
		log.Printf("signup: record source user_id=%s: %v", userID, err)
	}
}

// signupForVerify says where an account created by POST /v1/auth/verify came
// from: the /mcp consent page (it names the app's client_id), one of the
// site's own pages (a same-site browser request), or an agent calling the API.
func signupForVerify(ctx context.Context, q db.Querier, r *http.Request, publicBaseURL, clientID string, viaLink bool) db.Signup {
	method := db.SignupMethodEmail
	if clientID = strings.TrimSpace(clientID); clientID != "" {
		if s, ok := connectorSignup(ctx, q, clientID, method); ok {
			return s
		}
	}
	if viaLink || fromOwnPages(r, publicBaseURL) {
		return db.Signup{Source: db.SignupWebsite, Method: method}
	}
	return db.Signup{Source: db.SignupAgent, Agent: signupAgentName(r), Method: method}
}

// connectorSignup names the app registered as clientID.
func connectorSignup(ctx context.Context, q db.Querier, clientID, method string) (db.Signup, bool) {
	c, err := db.GetOAuthClient(ctx, q, clientID)
	if err != nil {
		return db.Signup{}, false
	}
	return db.Signup{Source: db.SignupConnectorPrefix + cleanClientName(c.Name), Method: method}, true
}

// signupForOwnerReturnTo says where a Google sign-up that started on one of
// the site's pages came from: the consent page (/oauth/authorize, whose query
// names the app) or anywhere else on the site.
func signupForOwnerReturnTo(ctx context.Context, q db.Querier, returnTo string) db.Signup {
	if u, err := url.Parse(returnTo); err == nil && u.Path == "/oauth/authorize" {
		if s, ok := connectorSignup(ctx, q, u.Query().Get("client_id"), db.SignupMethodGoogle); ok {
			return s
		}
	}
	return db.Signup{Source: db.SignupWebsite, Method: db.SignupMethodGoogle}
}

// fromOwnPages reports a browser request made by one of this site's pages.
// Browsers set Sec-Fetch-Site themselves (a page cannot); Origin is the
// fallback for one that does not send it.
func fromOwnPages(r *http.Request, publicBaseURL string) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site":
		return true
	case "":
	default:
		return false
	}
	o := strings.TrimRight(r.Header.Get("Origin"), "/")
	return o != "" && publicBaseURL != "" && strings.EqualFold(o, strings.TrimRight(publicBaseURL, "/"))
}

// signupAgentName names the agent behind an API sign-up: the
// X-Simple-Host-Client header, else "skill <X-Skill-Version>", else the
// User-Agent's product token ("curl", "python-requests"). Printable ASCII
// only, at most signupAgentMax characters; "" when there is nothing to go on.
func signupAgentName(r *http.Request) string {
	if v := cleanAgentName(r.Header.Get("X-Simple-Host-Client")); v != "" {
		return v
	}
	if v := cleanAgentName(r.Header.Get("X-Skill-Version")); v != "" {
		return clipAgent("skill " + v)
	}
	ua := strings.TrimSpace(r.UserAgent())
	if i := strings.IndexAny(ua, "/ ("); i >= 0 {
		ua = ua[:i]
	}
	ua = cleanAgentName(ua)
	if strings.EqualFold(ua, "Mozilla") {
		return "browser"
	}
	return ua
}

// cleanAgentName keeps letters, digits and . _ - + / : ( ) and single spaces.
// An IP address is dropped: no IP is ever kept.
func cleanAgentName(s string) string {
	s = strings.TrimSpace(s)
	if net.ParseIP(strings.Trim(s, "[]")) != nil {
		return ""
	}
	var b strings.Builder
	space := false
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("._-+/:()", c):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(c)
		case c == ' ' || c == '\t':
			space = true
		}
	}
	return clipAgent(b.String())
}

func clipAgent(s string) string {
	if len(s) > signupAgentMax {
		s = strings.TrimSpace(s[:signupAgentMax])
	}
	return s
}
