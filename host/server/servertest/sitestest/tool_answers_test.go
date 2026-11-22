package sitestest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// The optional parameters that let one REST request answer a connector
// tool: ?files= on the site list, {version} live with ?read= on a version's
// files, ?confirm_name= on delete, ?return=site on domain removal.
func TestOneRequestToolAnswers(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")

	r := a.api(t, olive, "GET", "/v1/sites?files=shop", nil)
	var list []struct {
		Name  string `json:"name"`
		Files *[]struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if r.Status != 200 || json.Unmarshal(r.Body, &list) != nil || len(list) != 2 {
		t.Fatalf("list: %d %s", r.Status, r.Body)
	}
	for _, s := range list {
		if s.Name == "shop" && (s.Files == nil || len(*s.Files) != 2 || (*s.Files)[0].Path != "index.html" || (*s.Files)[0].Size != 13) {
			t.Fatalf("shop files: %s", r.Body)
		}
		if s.Name == "blog" && s.Files != nil {
			t.Fatalf("blog got files it was not asked for: %s", r.Body)
		}
	}

	r = a.api(t, olive, "GET", "/v1/sites/shop/versions/live/files?read=index.html&read=gone.html&read=../x", nil)
	var read struct {
		Version int `json:"version_number"`
		Files   []struct {
			Path    string  `json:"path"`
			Content *string `json:"content"`
			Status  int     `json:"status"`
		} `json:"files"`
	}
	if r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("read files Cache-Control: %q", r.Header.Get("Cache-Control"))
	}
	if r.Status != 200 || json.Unmarshal(r.Body, &read) != nil || read.Version != 1 || len(read.Files) != 3 {
		t.Fatalf("read: %d %s", r.Status, r.Body)
	}
	if read.Files[0].Content == nil || *read.Files[0].Content != "<h1>shop</h1>" || read.Files[1].Status != 404 || read.Files[2].Status != 400 {
		t.Fatalf("read files: %s", r.Body)
	}
	for n, want := range map[int]int{11: 200, 51: 400} {
		q := strings.Repeat("&read=index.html", n)
		if r := a.api(t, olive, "GET", "/v1/sites/shop/versions/live/files?"+q[1:], nil); r.Status != want {
			t.Fatalf("read %d paths: %d %s", n, r.Status, r.Body)
		}
	}
	if r := a.api(t, olive, "GET", "/v1/sites/shop/versions/live/files/index.html", nil); r.Status != http.StatusBadRequest {
		t.Fatalf("live is for the listing only: %d %s", r.Status, r.Body)
	}

	claimed := uniq("olv") + "." + rt.SiteDomain
	if r := a.bind(t, olive, "shop", claimed); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	for _, empty := range []string{".", "", "https://"} {
		if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain?return=site&domain="+url.QueryEscape(empty), nil); r.Status != http.StatusBadRequest {
			t.Fatalf("domain=%q drops nothing: %d %s", empty, r.Status, r.Body)
		}
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain?return=site&domain=other.example.com", nil); r.Status != http.StatusConflict || r.Code() != "domain_changed" {
		t.Fatalf("wrong domain: %d %s", r.Status, r.Body)
	}
	r = a.api(t, olive, "DELETE", "/v1/sites/shop/domain?return=site&domain="+claimed, nil)
	if r.Status != 200 || r.JSON(t)["name"] != "shop" || r.JSON(t)["live_url"] == "https://"+claimed+"/" || r.JSON(t)["custom_domain"] != nil {
		t.Fatalf("remove domain: %d %s", r.Status, r.Body)
	}

	if r := a.api(t, olive, "DELETE", "/v1/sites/shop?confirm_name=shopp", nil); r.Status != http.StatusBadRequest || r.Code() != "confirm_name_mismatch" {
		t.Fatalf("wrong confirm_name: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop?confirm_name=SHOP", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
}
