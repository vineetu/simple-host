package imagescan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	matchBody = `{"Status":{"Code":3000,"Description":"OK","Exception":null},"ContentId":"c1","IsMatch":true,` +
		`"MatchDetails":{"AdvancedInfo":[{"Key":"k","Value":"v"}],"MatchFlags":[` +
		`{"AdvancedInfo":[{"Key":"MatchId","Value":"m1"}],"Source":"NCMEC","Violations":["A1","A2"]},` +
		`{"AdvancedInfo":[],"Source":"IWF","Violations":["A2","B1"]}]},"XPartnerCustomerId":null,"TrackingId":"t-123"}`
	cleanBody = `{"Status":{"Code":3000,"Description":"OK","Exception":null},"ContentId":"c2","IsMatch":false,` +
		`"MatchDetails":null,"XPartnerCustomerId":null,"TrackingId":"t-456"}`
)

type captured struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

func server(t *testing.T, status int, body string, got *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = captured{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone(), body: b}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testImg() *Image {
	return &Image{Data: []byte("\xff\xd8\xffJPEGBYTES"), ContentType: "image/jpeg", Width: 100, Height: 100, SHA256: "deadbeef"}
}

func TestMatchSendsKeyAndBytes(t *testing.T) {
	var got captured
	srv := server(t, 200, matchBody, &got)
	p := NewPhotoDNA("secret-key")
	p.MatchURL = srv.URL + "/photodna/v1.0/Match"
	m, err := p.Match(context.Background(), testImg())
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/photodna/v1.0/Match" || got.query != "" {
		t.Errorf("request = %s %s?%s", got.method, got.path, got.query)
	}
	if got.header.Get("Ocp-Apim-Subscription-Key") != "secret-key" {
		t.Errorf("key header = %q", got.header.Get("Ocp-Apim-Subscription-Key"))
	}
	if got.header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("content type = %q", got.header.Get("Content-Type"))
	}
	if string(got.body) != string(testImg().Data) {
		t.Errorf("body was not the image bytes")
	}
	if !m.IsMatch || m.TrackingID != "t-123" || m.Source != "NCMEC" {
		t.Errorf("match = %+v", m)
	}
	if strings.Join(m.Violations, ",") != "A1,A2,B1" {
		t.Errorf("violations = %v", m.Violations)
	}
	if string(m.Raw) != matchBody {
		t.Errorf("raw not kept")
	}
}

func TestMatchEnhanceQuery(t *testing.T) {
	var got captured
	srv := server(t, 200, cleanBody, &got)
	p := NewPhotoDNA("k")
	p.MatchURL = srv.URL + "/Match"
	p.Enhance = true
	if _, err := p.Match(context.Background(), testImg()); err != nil {
		t.Fatal(err)
	}
	if got.query != "enhance=true" {
		t.Errorf("query = %q", got.query)
	}
}

func TestMatchClean(t *testing.T) {
	srv := server(t, 200, cleanBody, nil)
	p := NewPhotoDNA("k")
	p.MatchURL = srv.URL
	m, err := p.Match(context.Background(), testImg())
	if err != nil {
		t.Fatal(err)
	}
	if m.IsMatch || m.Source != "" || len(m.Violations) != 0 || m.TrackingID != "t-456" {
		t.Errorf("match = %+v", m)
	}
}

func TestMatchStatusCodes(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		unsupported bool
	}{
		{"3002", `{"Status":{"Code":3002,"Description":"Invalid image format"},"IsMatch":false}`, true},
		{"3004", `{"Status":{"Code":3004,"Description":"Image too small"},"IsMatch":false}`, true},
		{"3005", `{"Status":{"Code":3005,"Description":"Image too large"},"IsMatch":false}`, true},
		{"3xxx by description", `{"Status":{"Code":3009,"Description":"The Image Size is not supported"},"IsMatch":false}`, true},
		{"quoted code", `{"Status":{"Code":"3002","Description":"bad image"},"IsMatch":false}`, true},
		{"other code", `{"Status":{"Code":3001,"Description":"Quota exceeded"},"IsMatch":false}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := server(t, 200, c.body, nil)
			p := NewPhotoDNA("k")
			p.MatchURL = srv.URL
			_, err := p.Match(context.Background(), testImg())
			if err == nil {
				t.Fatal("want an error")
			}
			if errors.Is(err, ErrUnsupported) != c.unsupported {
				t.Errorf("ErrUnsupported = %v, want %v: %v", !c.unsupported, c.unsupported, err)
			}
			if IsRetryable(err) {
				t.Errorf("must not be retryable: %v", err)
			}
			if !strings.Contains(err.Error(), "300") {
				t.Errorf("error does not name the code: %v", err)
			}
		})
	}
}

func TestMatchHTTPErrors(t *testing.T) {
	cases := []struct {
		status      int
		retryable   bool
		unsupported bool
		contains    string
	}{
		{400, false, true, "HTTP 400"},
		{401, false, false, "subscription key refused (HTTP 401)"},
		{403, false, false, "subscription key refused (HTTP 403)"},
		{408, true, false, "HTTP 408"},
		{429, true, false, "HTTP 429"},
		{500, true, false, "HTTP 500"},
		{503, true, false, "HTTP 503"},
		{404, false, false, "HTTP 404"},
	}
	for _, c := range cases {
		srv := server(t, c.status, `{"error":"something"}`, nil)
		p := NewPhotoDNA("k")
		p.MatchURL = srv.URL
		_, err := p.Match(context.Background(), testImg())
		if err == nil {
			t.Fatalf("%d: want an error", c.status)
		}
		if IsRetryable(err) != c.retryable {
			t.Errorf("%d: retryable = %v, want %v: %v", c.status, !c.retryable, c.retryable, err)
		}
		if errors.Is(err, ErrUnsupported) != c.unsupported {
			t.Errorf("%d: unsupported = %v, want %v: %v", c.status, !c.unsupported, c.unsupported, err)
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%d: %v does not contain %q", c.status, err, c.contains)
		}
		if strings.Contains(err.Error(), "JPEGBYTES") {
			t.Errorf("%d: error leaks the request body", c.status)
		}
		srv.Close()
	}
}

func TestMatchUnparsableBodyIsRetryable(t *testing.T) {
	srv := server(t, 200, `<html>gateway</html>`, nil)
	p := NewPhotoDNA("k")
	p.MatchURL = srv.URL
	_, err := p.Match(context.Background(), testImg())
	if !IsRetryable(err) {
		t.Errorf("want retryable: %v", err)
	}
}

func TestMatchContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	p := NewPhotoDNA("k")
	p.MatchURL = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Match(ctx, testImg())
	if !IsRetryable(err) {
		t.Errorf("want retryable: %v", err)
	}
	if strings.Contains(err.Error(), "k") && strings.Contains(err.Error(), "Ocp") {
		t.Errorf("error mentions the key header: %v", err)
	}
}

func TestMatchNetworkError(t *testing.T) {
	srv := server(t, 200, cleanBody, nil)
	url := srv.URL
	srv.Close()
	p := NewPhotoDNA("k")
	p.MatchURL = url
	_, err := p.Match(context.Background(), testImg())
	if !IsRetryable(err) {
		t.Errorf("want retryable: %v", err)
	}
}

func TestMatchRawCap(t *testing.T) {
	pad := strings.Repeat("x", 100<<10)
	body := `{"Status":{"Code":3000,"Description":"OK"},"IsMatch":false,"TrackingId":"t","Pad":"` + pad + `"}`
	srv := server(t, 200, body, nil)
	p := NewPhotoDNA("k")
	p.MatchURL = srv.URL
	m, err := p.Match(context.Background(), testImg())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Raw) != rawCap {
		t.Errorf("Raw is %d bytes, want %d", len(m.Raw), rawCap)
	}
	if m.TrackingID != "t" {
		t.Errorf("TrackingID = %q", m.TrackingID)
	}
}

func TestMatchErrorsOnNoBytes(t *testing.T) {
	p := NewPhotoDNA("k")
	if _, err := p.Match(context.Background(), &Image{}); err == nil || IsRetryable(err) {
		t.Errorf("err = %v", err)
	}
	if _, err := p.Match(context.Background(), nil); err == nil {
		t.Errorf("nil image accepted")
	}
}

func sampleReport() Report {
	return Report{
		OrgName:       "Simple Host",
		ReporterName:  "Trust and Safety",
		ReporterEmail: "support@simple-host.app",
		IncidentTime:  time.Date(2026, 11, 2, 10, 30, 0, 0, time.FixedZone("x", 3600)),
		ReporteeName:  "user-42",
		Files: []ReportFile{{
			Name:        "photo.jpg",
			Data:        []byte("\xff\xd8\xffbytes"),
			ContentType: "image/jpeg",
			UploadedAt:  time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			Metadata:    map[string]string{"site": "x.y.simple-host.app", "path": "/a.jpg", "writer": "w1"},
		}},
		Metadata: map[string]string{"tracking_id": "t-123", "hash": "abc"},
	}
}

func TestReportBodyShape(t *testing.T) {
	var got captured
	srv := server(t, 200, `{"ReportId":"r-1"}`, &got)
	p := NewPhotoDNA("secret-key")
	p.ReportURL = srv.URL + "/Report"
	res, err := p.Report(context.Background(), sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	if res.ReportID != "r-1" {
		t.Errorf("ReportID = %q", res.ReportID)
	}
	if got.header.Get("Ocp-Apim-Subscription-Key") != "secret-key" || got.header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", got.header)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"OrgName", "ReporterName", "ReporterEmail", "IncidentTime", "ReporteeName", "ViolationContentCollection", "AdditionalMetadata"} {
		if _, ok := body[k]; !ok {
			t.Errorf("body lacks %s", k)
		}
	}
	if len(body) != 7 {
		t.Errorf("body has %d top-level keys, want 7: %v", len(body), keys(body))
	}
	if body["IncidentTime"] != "2026-11-02T09:30:00Z" {
		t.Errorf("IncidentTime = %v", body["IncidentTime"])
	}
	files := body["ViolationContentCollection"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	f := files[0].(map[string]any)
	if f["Name"] != "photo.jpg" || f["UploadDateTime"] != "2026-11-01T09:00:00Z" {
		t.Errorf("file = %v", f)
	}
	if f["Value"] != "/9j/Ynl0ZXM=" {
		t.Errorf("Value = %v", f["Value"])
	}
	meta := f["AdditionalMetadata"].([]any)
	if len(meta) != 3 || meta[0].(map[string]any)["Key"] != "path" || meta[1].(map[string]any)["Key"] != "site" || meta[2].(map[string]any)["Key"] != "writer" {
		t.Errorf("file metadata not sorted: %v", meta)
	}
	top := body["AdditionalMetadata"].([]any)
	if len(top) != 2 || top[0].(map[string]any)["Key"] != "hash" || top[0].(map[string]any)["Value"] != "abc" {
		t.Errorf("top metadata = %v", top)
	}
	assertNoIPKeys(t, body)
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func assertNoIPKeys(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if strings.Contains(k, "IP") || strings.Contains(k, "Ip") {
				t.Errorf("report body has an IP field: %q", k)
			}
			assertNoIPKeys(t, child)
		}
	case []any:
		for _, child := range x {
			assertNoIPKeys(t, child)
		}
	}
}

func TestReportValidation(t *testing.T) {
	srv := server(t, 200, `{"ReportId":"r-1"}`, nil)
	p := NewPhotoDNA("k")
	p.ReportURL = srv.URL
	cases := []struct {
		name string
		mut  func(*Report)
		want string
	}{
		{"org", func(r *Report) { r.OrgName = " " }, "organisation name"},
		{"reporter", func(r *Report) { r.ReporterName = "" }, "reporter name"},
		{"email", func(r *Report) { r.ReporterEmail = "nobody" }, "reporter email"},
		{"no files", func(r *Report) { r.Files = nil }, "no file with bytes"},
		{"empty file", func(r *Report) { r.Files[0].Data = nil }, "no file with bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := sampleReport()
			c.mut(&in)
			_, err := p.Report(context.Background(), in)
			if err == nil {
				t.Fatal("want an error")
			}
			if IsRetryable(err) {
				t.Errorf("must not be retryable: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v does not name %q", err, c.want)
			}
		})
	}
}

func TestReportIDParsing(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"ReportId":"a"}`, "a"},
		{`{"ReportID":"b"}`, "b"},
		{`{"Id":"c"}`, "c"},
		{`{"TrackingId":"d"}`, "d"},
		{`{"Id":123}`, "123"},
		{`{"TrackingId":"d","ReportId":"a"}`, "a"},
		{`{"Status":{"Code":3000,"Description":"OK"},"ReportID":"b"}`, "b"},
		{`{}`, ""},
		{``, ""},
		{`not json`, ""},
	}
	for _, c := range cases {
		srv := server(t, 200, c.body, nil)
		p := NewPhotoDNA("k")
		p.ReportURL = srv.URL
		res, err := p.Report(context.Background(), sampleReport())
		if err != nil {
			t.Errorf("%q: %v", c.body, err)
		}
		if res.ReportID != c.want {
			t.Errorf("%q: ReportID = %q, want %q", c.body, res.ReportID, c.want)
		}
		srv.Close()
	}
}

func TestReportStatusError(t *testing.T) {
	srv := server(t, 200, `{"Status":{"Code":3001,"Description":"Quota exceeded"}}`, nil)
	p := NewPhotoDNA("k")
	p.ReportURL = srv.URL
	_, err := p.Report(context.Background(), sampleReport())
	if err == nil || IsRetryable(err) || !strings.Contains(err.Error(), "3001") {
		t.Errorf("err = %v", err)
	}
}

func TestReportHTTPErrors(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
		contains  string
	}{
		{400, false, "HTTP 400: bad field"},
		{401, false, "subscription key refused (HTTP 401)"},
		{403, false, "subscription key refused (HTTP 403)"},
		{429, true, "HTTP 429"},
		{500, true, "HTTP 500"},
		{502, true, "HTTP 502"},
	}
	for _, c := range cases {
		srv := server(t, c.status, `bad field`, nil)
		p := NewPhotoDNA("k")
		p.ReportURL = srv.URL
		_, err := p.Report(context.Background(), sampleReport())
		if err == nil {
			t.Fatalf("%d: want an error", c.status)
		}
		if IsRetryable(err) != c.retryable {
			t.Errorf("%d: retryable = %v, want %v: %v", c.status, !c.retryable, c.retryable, err)
		}
		if errors.Is(err, ErrUnsupported) {
			t.Errorf("%d: report errors never wrap ErrUnsupported: %v", c.status, err)
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%d: %v does not contain %q", c.status, err, c.contains)
		}
		srv.Close()
	}
	srv := server(t, 200, `{}`, nil)
	url := srv.URL
	srv.Close()
	p := NewPhotoDNA("k")
	p.ReportURL = url
	if _, err := p.Report(context.Background(), sampleReport()); !IsRetryable(err) {
		t.Errorf("network error: want retryable: %v", err)
	}
}

func TestReportBodySnippetIsCapped(t *testing.T) {
	srv := server(t, 400, strings.Repeat("y", 5000), nil)
	p := NewPhotoDNA("k")
	p.ReportURL = srv.URL
	_, err := p.Report(context.Background(), sampleReport())
	if err == nil || len(err.Error()) > 300 {
		t.Errorf("err = %v (len %d)", err, len(err.Error()))
	}
}

func TestNilClientGetsDefault(t *testing.T) {
	srv := server(t, 200, cleanBody, nil)
	p := &PhotoDNA{Key: "k", MatchURL: srv.URL}
	if _, err := p.Match(context.Background(), testImg()); err != nil {
		t.Fatal(err)
	}
}
