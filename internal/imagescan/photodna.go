package imagescan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// rawCap bounds the response body kept as evidence.
	rawCap = 64 << 10
	// bodyReadCap bounds how much of any response is read at all.
	bodyReadCap = 1 << 20
	// snippetCap bounds the body excerpt in an error message.
	snippetCap = 200
)

func (p *PhotoDNA) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Match implements Matcher: POST the image bytes to MatchURL.
func (p *PhotoDNA) Match(ctx context.Context, img *Image) (Match, error) {
	if img == nil || len(img.Data) == 0 {
		return Match{}, errors.New("photodna: match: no image bytes")
	}
	u := p.MatchURL
	if u == "" {
		u = DefaultMatchURL
	}
	if p.Enhance {
		if strings.Contains(u, "?") {
			u += "&enhance=true"
		} else {
			u += "?enhance=true"
		}
	}
	ct := img.ContentType
	if ct == "" {
		ct = http.DetectContentType(img.Data)
	}
	status, body, err := p.post(ctx, u, ct, img.Data)
	if err != nil {
		return Match{}, fmt.Errorf("photodna: match: %w: %v", ErrRetryable, err)
	}
	if status < 200 || status > 299 {
		return Match{}, httpError("match", status, body, true)
	}

	var r matchResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return Match{}, fmt.Errorf("photodna: match: %w: HTTP %d with an unparsable body: %s", ErrRetryable, status, snippet(body))
	}
	if r.Status != nil && int(r.Status.Code) != StatusOK {
		return Match{}, statusError(int(r.Status.Code), r.Status.Description)
	}
	if r.IsMatch == nil {
		// A reply without a verdict is not a clean result.
		return Match{}, fmt.Errorf("photodna: match: %w: HTTP %d with no IsMatch in the reply: %s", ErrRetryable, status, snippet(body))
	}
	m := Match{IsMatch: *r.IsMatch, TrackingID: r.TrackingID, Raw: capRaw(body)}
	seen := map[string]bool{}
	for _, f := range r.MatchDetails.MatchFlags {
		if m.Source == "" {
			m.Source = f.Source
		}
		for _, v := range f.Violations {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			m.Violations = append(m.Violations, v)
		}
	}
	return m, nil
}

// Report implements Reporter: POST the report JSON to ReportURL.
func (p *PhotoDNA) Report(ctx context.Context, in Report) (ReportResult, error) {
	if err := validateReport(in); err != nil {
		return ReportResult{}, err
	}
	u := p.ReportURL
	if u == "" {
		u = DefaultReportURL
	}
	body, err := json.Marshal(buildReportBody(in))
	if err != nil {
		return ReportResult{}, fmt.Errorf("photodna: report: encode: %w", err)
	}
	status, resp, err := p.post(ctx, u, "application/json", body)
	if err != nil {
		return ReportResult{}, fmt.Errorf("photodna: report: %w: %v", ErrRetryable, err)
	}
	if status < 200 || status > 299 {
		return ReportResult{}, httpError("report", status, resp, false)
	}
	res := ReportResult{Raw: capRaw(resp)}
	// Success needs a JSON body with a report id or an explicit OK status.
	// Anything else is a permanent error: an automatic retry could file a
	// duplicate, so the operator decides.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(resp, &top); err != nil || top == nil {
		return ReportResult{}, fmt.Errorf("photodna: report not acknowledged: HTTP %d: %s", status, snippet(resp))
	}
	statusOK := false
	if raw, ok := top["Status"]; ok {
		var st statusBlock
		if json.Unmarshal(raw, &st) == nil && st.Code != 0 {
			if int(st.Code) != StatusOK {
				return ReportResult{}, fmt.Errorf("photodna: report: status %d: %s", int(st.Code), st.Description)
			}
			statusOK = true
		}
	}
	for _, k := range []string{"ReportId", "ReportID", "Id", "TrackingId"} {
		raw, ok := top[k]
		if !ok {
			continue
		}
		if id := stringValue(raw); id != "" {
			res.ReportID = id
			break
		}
	}
	if res.ReportID == "" && !statusOK {
		return ReportResult{}, fmt.Errorf("photodna: report not acknowledged: HTTP %d with no report id: %s", status, snippet(resp))
	}
	return res, nil
}

// post sends one request and returns the status and the body (read up to
// bodyReadCap). A transport error, including a timeout or a cancelled
// context, comes back as err.
func (p *PhotoDNA) post(ctx context.Context, u, contentType string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.Key)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, bodyReadCap))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, data, nil
}

// httpError classifies a non-2xx response. imageStatus400 is true for
// Match, where a 400 whose body carries an image-content status code means
// the service refused the image itself. Any other 400 is permanent.
func httpError(op string, status int, body []byte, imageStatus400 bool) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("photodna: subscription key refused (HTTP %d)", status)
	case status == http.StatusBadRequest:
		if imageStatus400 {
			var r struct {
				Status *statusBlock `json:"Status"`
			}
			if json.Unmarshal(body, &r) == nil && r.Status != nil && imageContentStatus(int(r.Status.Code)) {
				return fmt.Errorf("photodna: %s: %w (HTTP 400): status %d: %s", op, ErrUnsupported, int(r.Status.Code), r.Status.Description)
			}
		}
		return fmt.Errorf("photodna: %s: HTTP 400: %s", op, snippet(body))
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500:
		return fmt.Errorf("photodna: %s: %w: HTTP %d: %s", op, ErrRetryable, status, snippet(body))
	}
	return fmt.Errorf("photodna: %s: HTTP %d: %s", op, status, snippet(body))
}

// PhotoDNA status codes, as Wikimedia's MediaModeration integration lists
// them (src/PhotoDNA/Response.php).
const (
	statusBadRequest      = 3002 // invalid or missing request parameter
	statusUnknownScenario = 3004 // unknown scenario or unhandled error
	statusNotAnImage      = 3206 // could not verify the file as an image (size in bytes out of range)
	statusPixelSize       = 3208 // image dimensions out of range
	statusRequestTooLarge = 3209 // request too large
)

// imageContentStatus is true for codes that mean the image itself cannot be
// checked.
func imageContentStatus(code int) bool {
	switch code {
	case statusNotAnImage, statusPixelSize, statusRequestTooLarge:
		return true
	}
	return false
}

// statusError classifies a non-3000 code inside a Match reply: image-content
// codes are ErrUnsupported, a request-shape code is permanent, and 3004 or
// any unknown code is retryable.
func statusError(code int, desc string) error {
	switch {
	case imageContentStatus(code):
		return fmt.Errorf("photodna: %w: status %d: %s", ErrUnsupported, code, desc)
	case code == statusBadRequest:
		return fmt.Errorf("photodna: status %d: %s", code, desc)
	}
	return fmt.Errorf("photodna: %w: status %d: %s", ErrRetryable, code, desc)
}

type statusBlock struct {
	Code        flexInt `json:"Code"`
	Description string  `json:"Description"`
}

type matchResponse struct {
	Status       *statusBlock `json:"Status"`
	IsMatch      *bool        `json:"IsMatch"`
	TrackingID   string       `json:"TrackingId"`
	MatchDetails struct {
		MatchFlags []struct {
			Source     string   `json:"Source"`
			Violations []string `json:"Violations"`
		} `json:"MatchFlags"`
	} `json:"MatchDetails"`
}

// flexInt accepts a JSON number or a quoted number.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	s = strings.Trim(s, `"`)
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("status code %q is not a number", s)
	}
	*f = flexInt(n)
	return nil
}

// stringValue reads a JSON string, or a number as its text.
func stringValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

type reportKV struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type reportFileBody struct {
	Name               string     `json:"Name"`
	Value              string     `json:"Value"`
	UploadDateTime     string     `json:"UploadDateTime"`
	AdditionalMetadata []reportKV `json:"AdditionalMetadata"`
}

// reportBody is the Report operation's request. It carries no IP field of
// any kind, by design: see the package comment.
type reportBody struct {
	OrgName                    string           `json:"OrgName"`
	ReporterName               string           `json:"ReporterName"`
	ReporterEmail              string           `json:"ReporterEmail"`
	IncidentTime               string           `json:"IncidentTime"`
	ReporteeName               string           `json:"ReporteeName"`
	ViolationContentCollection []reportFileBody `json:"ViolationContentCollection"`
	AdditionalMetadata         []reportKV       `json:"AdditionalMetadata"`
}

func validateReport(in Report) error {
	switch {
	case strings.TrimSpace(in.OrgName) == "":
		return errors.New("photodna: report refused: organisation name is missing")
	case strings.TrimSpace(in.ReporterName) == "":
		return errors.New("photodna: report refused: reporter name is missing")
	case !strings.Contains(in.ReporterEmail, "@"):
		return errors.New("photodna: report refused: reporter email is missing or not an address")
	}
	for _, f := range in.Files {
		if len(f.Data) > 0 {
			return nil
		}
	}
	return errors.New("photodna: report refused: no file with bytes")
}

func buildReportBody(in Report) reportBody {
	incident := in.IncidentTime
	if incident.IsZero() {
		incident = time.Now()
	}
	incident = incident.UTC()
	b := reportBody{
		OrgName:            in.OrgName,
		ReporterName:       in.ReporterName,
		ReporterEmail:      in.ReporterEmail,
		IncidentTime:       incident.Format(time.RFC3339),
		ReporteeName:       in.ReporteeName,
		AdditionalMetadata: sortedKV(in.Metadata),
	}
	for i, f := range in.Files {
		if len(f.Data) == 0 {
			continue
		}
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("file-%d", i+1)
		}
		up := f.UploadedAt
		if up.IsZero() {
			up = incident
		}
		b.ViolationContentCollection = append(b.ViolationContentCollection, reportFileBody{
			Name:               name,
			Value:              base64.StdEncoding.EncodeToString(f.Data),
			UploadDateTime:     up.UTC().Format(time.RFC3339),
			AdditionalMetadata: sortedKV(f.Metadata),
		})
	}
	return b
}

func sortedKV(m map[string]string) []reportKV {
	out := make([]reportKV, 0, len(m))
	for k, v := range m {
		out = append(out, reportKV{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func capRaw(body []byte) json.RawMessage {
	if len(body) > rawCap {
		body = body[:rawCap]
	}
	return json.RawMessage(append([]byte(nil), body...))
}

// snippet is a short, printable excerpt of a response body for an error
// message. It is never the request body.
func snippet(body []byte) string {
	if len(body) > snippetCap {
		body = body[:snippetCap]
	}
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, string(body))
	return strings.TrimSpace(s)
}
