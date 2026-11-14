package sites

import (
	"maps"

	"github.com/vsriram/simple-host/internal/wpimport"
)

// Advice is rendered from a copy at report time. The conversion record still
// contains the original setup, so switching the flag off restores its bytes.
func importRulesAdvice(original *wpimport.Report) *wpimport.Report {
	report := *original
	report.Features = append([]wpimport.Feature(nil), original.Features...)
	for i := range report.Features {
		f := &report.Features[i]
		if f.Status != wpimport.StatusNeedsSetup {
			continue
		}
		read, add, remove := `auth.role == "owner"`, `true`, `auth.role == "owner"`
		switch f.Kind {
		case wpimport.FeatureContactForm, wpimport.FeatureSubscribe:
			f.Use = "Anyone sends; only the owner reads, changes or removes entries. Nothing is emailed automatically."
		case wpimport.FeatureComments, wpimport.FeatureGuestbook:
			read, add = `true`, `auth.checked || auth.ai || auth.role == "owner"`
			f.Use = "Anyone reads and posts after a spam check; only the owner changes or removes posts. Ask whether new posts should wait for approval."
		case wpimport.FeatureForum:
			read, add, remove = `true`, `auth.signed_in`, `visitor_id == auth.id || auth.role == "owner"`
			f.Use = "Anyone reads; signed-in people post and can remove their own posts; the owner changes or removes any post."
		default:
			continue
		}
		policies := []map[string]any{
			{"name": "read", "command": "read", "using": read},
			{"name": "add", "command": "add", "check": add},
			{"name": "edit", "command": "edit", "using": `auth.role == "owner"`},
			{"name": "delete", "command": "delete", "using": remove},
		}
		if f.Moderated && (f.Kind == wpimport.FeatureComments || f.Kind == wpimport.FeatureGuestbook) {
			policies = append(policies, map[string]any{"name": "approve_first", "command": "read", "using": `auth.role == "owner" || approved_at is not null`, "restrictive": true})
		}
		f.Setup = append([]wpimport.ToolCall(nil), f.Setup...)
		for j := range f.Setup {
			call := &f.Setup[j]
			if call.Tool != "storage_set_resource" {
				continue
			}
			body, _ := call.Args["body"].(map[string]any)
			body = maps.Clone(body)
			delete(body, "preset")
			delete(body, "approve_first")
			body["rules"] = map[string]any{"version": 1, "policies": policies}
			call.Args = maps.Clone(call.Args)
			call.Args["body"] = body
		}
	}
	return &report
}
