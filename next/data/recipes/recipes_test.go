package recipes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/data/files"
	"simplehost/data/recipes"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
	"simplehost/sites/locks"
)

// block is the text of the fenced block that opens with fence.
func block(text, fence string) string {
	_, rest, _ := strings.Cut(text, "```"+fence+"\n")
	body, _, _ := strings.Cut(rest, "```")
	return body
}

// SPEC test 34 (F25): each recipe's page works on first publish: its setup
// runs as written, then the calls its page makes answer as the recipe says.
func TestEachRecipeWorks(t *testing.T) {
	k := testkit.Start(t, rows.Mount, values.Mount, files.Mount, datasql.Mount, datarules.Mount, recipes.Mount)
	owner, client, stranger := k.Person(t, "owner"), k.Person(t, "client"), k.Person(t, "stranger")
	for _, topic := range recipes.Topics {
		t.Run(topic, func(t *testing.T) {
			r := testkit.Call(t, k.Browser(), "GET", "https://simple-host.app/v1/recipes/"+topic+"?site="+topic, nil)
			var got struct{ Text string }
			json.Unmarshal([]byte(r.Body), &got)
			text := got.Text
			k.Publish(t, owner, topic, map[string]string{"index.html": block(text, "html")})
			s, _ := k.App.Sites.Get(context.Background(), owner.ID, topic)
			addr := k.App.Sites.URL(s)

			_, setup, _ := strings.Cut(text, "## Setup")
			setup, _, _ = strings.Cut(setup, "\n## ")
			for _, line := range strings.Split(setup, "\n") {
				tool, args, _ := strings.Cut(line, " ")
				var in map[string]any
				if json.Unmarshal([]byte(args), &in) != nil {
					continue
				}
				ns, _ := in["namespace"].(string)
				base := "https://simple-host.app/v1/sites/" + topic + "/storage/"
				var res testkit.Res
				switch tool {
				case "run_sql":
					res = testkit.Call(t, k.Browser(), "POST", base+"sqlite/"+ns+"/execute", in, "Authorization", "Bearer "+owner.Key)
				case "set_data_rules":
					res = testkit.Call(t, k.Browser(), "PUT", base+ns+"/rules", in, "Authorization", "Bearer "+owner.Key)
				case "set_site_access": // the only recipe that uses it names viewers
					var emails []string
					for _, e := range in["viewers"].([]any) {
						emails = append(emails, e.(string))
					}
					if err := locks.SetViewers(context.Background(), k.App, s, emails); err != nil {
						t.Fatal(err)
					}
					continue
				default:
					continue
				}
				if res.Status != 200 {
					t.Fatalf("%s: %d %s", line, res.Status, res.Body)
				}
			}

			if !strings.Contains(block(text, "html"), "auth.js") || block(text, "calls") == "" {
				t.Fatal("the recipe has no page or no calls")
			}
			browsers := map[string]testkit.Who{"visitor": client, "other": stranger}
			signed := map[string]*http.Client{}
			for _, line := range strings.Split(strings.TrimSpace(block(text, "calls")), "\n") {
				call, expect, _ := strings.Cut(line, " -> ")
				f := strings.SplitN(call, " ", 4)
				who, method, path := f[0], f[1], f[2]
				header := []string{"X-SH-CSRF", "1"}
				var body any
				if len(f) == 4 {
					var v any
					json.Unmarshal([]byte(f[3]), &v)
					if str, ok := v.(string); ok {
						body = str
						header = append(header, "Content-Type", "image/jpeg")
					} else {
						body = v
					}
				}
				b := k.Browser()
				switch who {
				case "anon+checked":
					header = append(header, "X-SH-Turnstile", k.Turnstile)
				case "visitor", "other":
					if signed[who] == nil {
						signed[who] = k.Browser()
						k.SignIn(t, signed[who], browsers[who], addr)
					}
					b = signed[who]
				}
				status, has, _ := strings.Cut(expect, " ")
				want, _ := strconv.Atoi(status)
				json.Unmarshal([]byte(has), &has)
				res := testkit.Call(t, b, method, addr+"v1/sites/"+topic+"/"+path, body, header...)
				if res.Status != want || !strings.Contains(res.Body, has) {
					t.Fatalf("%s: got %d %s", line, res.Status, res.Body)
				}
			}
		})
	}
}
