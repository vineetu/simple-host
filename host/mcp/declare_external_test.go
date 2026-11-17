package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// A product can declare and call a tool without access to the private registry,
// policies, run field, caller state or REST-result fields.
func TestExportedContributionDeclaration(t *testing.T) {
	tool := mcp.Tool{Name: "example_read", Title: "Read example", Description: "Read a fixture.", InputSchema: mcp.NoArgs(), OutputSchema: mcp.OutObject(map[string]any{"ok": mcp.OutBool("Succeeded.")}), Annotations: mcp.ReadOnly()}
	tool = mcp.WithPolicy(tool, mcp.MemberResult("fixture"), mcp.Analytics(mcp.SecretText()), mcp.ShowAllowed(func(c mcp.Caller, ui bool) bool { return c.Mode == mcp.CallerModeEvents && ui }))
	tool = mcp.WithRun(tool, func(c *mcp.Call, args map[string]any) (mcp.Output, error) {
		if c.Caller().APIKey != "test-key" || c.Request() == nil || c.MemberResult() != "fixture" || c.APIHost() != "recipes.example.test" {
			t.Fatal("call context lost")
		}
		res := c.Do(http.MethodGet, "/v1/example", nil, nil)
		if !res.OK() || res.Status() != http.StatusOK {
			return mcp.Output{}, mcp.RestError("example_read", res)
		}
		return mcp.Output{Text: string(res.Body()), Structured: map[string]any{"ok": true}}, nil
	})
	contribution := mcp.Contribution{Families: []mcp.FamilyTools{{Family: "example", Tools: []mcp.Tool{tool}}}}
	upstreamCalls := 0
	s := mcp.NewServer(mcp.Config{APIHost: "recipes.example.test", Contribution: &contribution, Upstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.URL.Path != "/v1/example" || r.Header.Get("X-API-Key") != "test-key" {
			t.Fatal("REST replay lost path or credential")
		}
		w.Write([]byte(`{"ok":true}`))
	})})
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"example_read"}}`))
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{APIKey: "test-key", Mode: mcp.CallerModeEvents}))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var reply struct {
		Result struct {
			Structured map[string]any `json:"structuredContent"`
			IsError    bool           `json:"isError"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || upstreamCalls != 1 || reply.Result.IsError || reply.Result.Structured["ok"] != true {
		t.Fatalf("call failed (%d REST calls): %s", upstreamCalls, rec.Body.String())
	}
}

func TestExportedOptionalString(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
		err  string
	}{
		{name: "absent"},
		{name: "null", args: map[string]any{"site": nil}},
		{name: "empty", args: map[string]any{"site": ""}},
		{name: "blank", args: map[string]any{"site": " \t\n "}},
		{name: "trimmed", args: map[string]any{"site": " fixture \n"}, want: "fixture"},
		{name: "wrong type", args: map[string]any{"site": true}, err: "site must be a string, got bool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcp.OptionalString(tc.args, "site")
			if got != tc.want || (err != nil) != (tc.err != "") || (err != nil && err.Error() != tc.err) {
				t.Fatalf("OptionalString = %q, %v; want %q, %q", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestExportedAppMetadata(t *testing.T) {
	called := false
	contribution := mcp.Contribution{Apps: func(ctx mcp.AppContext) mcp.AppContribution {
		called = true
		screen := mcp.AppScreen{
			ID: "example", URI: "ui://example/screen",
			CSP:         map[string][]string{"connectDomains": {"$origin"}, "resourceDomains": {}},
			Permissions: map[string]any{"clipboardWrite": map[string]any{}},
		}
		want := map[string]any{"ui": map[string]any{
			"csp":           map[string][]string{"connectDomains": {"https://screens.example.test"}, "resourceDomains": {}},
			"prefersBorder": true, "permissions": screen.Permissions,
		}}
		if got := ctx.Meta(screen); !reflect.DeepEqual(got, want) {
			t.Fatalf("metadata = %#v; want %#v", got, want)
		}
		return mcp.AppContribution{}
	}}
	mcp.NewServer(mcp.Config{APIHost: "screens.example.test", Contribution: &contribution})
	if !called {
		t.Fatal("app factory was not called")
	}
}
