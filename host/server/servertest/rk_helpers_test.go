package servertest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
)

// Helpers of the keys, scopes, teams, holds and quota journeys (rk_ files).

// rkEdition is the default edition with the settings in m changed.
func rkEdition(t *testing.T, m map[string]string) *edition.Edition {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return edition.Default(l)
}

func rkKey(k string) map[string]string { return map[string]string{"X-API-Key": k} }

// rkMintDeploy has p mint a deploy-only key, returning the key and its id.
func rkMintDeploy(t *testing.T, a *PrivateApp, p Person) (key, id string) {
	t.Helper()
	r := a.At(t, "POST", PcSiteDomain, "/v1/me/keys", map[string]any{"name": "CI", "scope": "deploy"}, rkKey(p.Key))
	if r.Status != http.StatusCreated || r.JSON(t)["scope"] != "deploy" {
		t.Fatalf("mint deploy key: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["api_key"].(string), r.JSON(t)["id"].(string)
}

// rkMCPKey calls an MCP tool with an API key rather than a connector token.
func rkMCPKey(t *testing.T, a *PrivateApp, key, tool string, args map[string]any) (string, bool) {
	t.Helper()
	r := a.At(t, "POST", PcSiteDomain, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}},
		map[string]string{"X-API-Key": key, "MCP-Protocol-Version": "2025-06-18"})
	text, _, isErr := ToolResultOf(t, r)
	return text, isErr
}

func rkFiles(body string) map[string]any {
	return map[string]any{"files": map[string]string{"index.html": body}}
}

// rkTarGz is a gzipped tar holding index.html.
func rkTarGz(t *testing.T, page string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: int64(len(page)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(page)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// rkRefusal checks r is the refusal status with the given JSON code.
func rkRefusal(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status || r.JSON(t)["code"] != code {
		t.Fatalf("%s: %d %s, want %d %s", what, r.Status, r.Body, status, code)
	}
}

func rkContains(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// rkTarFiles reads a .tar.gz into name -> content.
func rkTarFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[hdr.Name] = string(body)
	}
}
