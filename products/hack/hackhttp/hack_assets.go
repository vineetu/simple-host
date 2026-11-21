package hackhttp

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/web"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// Assets owns Hack's text rewriting and download caches.
type Assets struct {
	services      web.AssetServices
	inkBase       string
	hackSkillsZip struct {
		once  sync.Once
		bytes []byte
		err   error
	}
}

func NewAssets(services web.AssetServices, inkBase string) *Assets {
	if inkBase == "" {
		inkBase = "https://simple-hack.app"
	}
	// The setup script is assembled from the platform's file.
	product, err := hackassets.Static.ReadFile("static/setup/setup-hack.js")
	if err != nil {
		panic("setup-hack.js: the product file is missing")
	}
	shared, ok := services.Static("setup/setup.js")
	if !ok {
		panic("setup.js: the platform file is missing")
	}
	if _, err := buildSetupJS(shared, product); err != nil {
		panic(err.Error())
	}
	return &Assets{services: services, inkBase: strings.TrimRight(inkBase, "/")}
}
func (p *Assets) inkBaseURL() string { return p.inkBase }

func (p *Assets) Hooks() web.ProductAssetHooks {
	return web.ProductAssetHooks{
		InstanceText: p.HackInstanceText, RewritePluginZip: p.RewriteHackPluginZip,
		SkillAvailable: hostedHackSkill, ExpandConnectSteps: expandConnectSteps,
		PersonalPresentationHTML: p.personalPresentationHTML,
		CatalogPlugin:            "simple-hack",
		GroupTexts:               groupHostTexts,
	}
}

// HackInstanceText rewrites simple-hack.app's own address to this Hack
// instance's, in served Hack text.
func (p *Assets) HackInstanceText(data []byte) []byte {
	if p.inkBaseURL() == "https://simple-hack.app" {
		return data
	}
	u, err := url.Parse(p.inkBaseURL())
	if err != nil || u.Host == "" {
		return data
	}
	return []byte(strings.NewReplacer("https://simple-hack.app", strings.TrimRight(p.inkBaseURL(), "/"), "simple-hack.app", u.Host).Replace(string(data)))
}

// HackSkillNames are the five Simple Hack skills bundled in the hosted zip.
var HackSkillNames = []string{"run-hackathon", "join-hackathon", "judge-hackathon", "website-deploy", "website-deploy-builder"}

// hostedHackSkill reports whether name is one of the five bundled Hack skills.
func hostedHackSkill(name string) bool {
	for _, skill := range HackSkillNames {
		if name == skill {
			return true
		}
	}
	return false
}

// BuildHackSkillsZip is the hosted bundle's five Simple Hack skills from the
// canonical embed, built once and cached by this product receiver.
func (p *Assets) BuildHackSkillsZip() ([]byte, error) {
	z := &p.hackSkillsZip
	z.once.Do(func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range HackSkillNames {
			root, err := fs.Sub(hackassets.HackSkills, "skills/"+name)
			if err != nil {
				z.err = err
				break
			}
			err = fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || path == "." || entry.IsDir() {
					return walkErr
				}
				data, err := fs.ReadFile(root, path)
				if err != nil {
					return err
				}
				dst, err := zw.Create(name + "/" + path)
				if err != nil {
					return err
				}
				// services.InstanceText is the platform's limits pass plus its old
				// base-domain swap (simple-host.site).
				_, err = dst.Write(p.HackInstanceText(p.services.InstanceText(data)))
				return err
			})
			if err != nil {
				z.err = err
				break
			}
		}
		if err := zw.Close(); z.err == nil {
			z.err = err
		}
		if z.err == nil {
			z.bytes = buf.Bytes()
		}
	})
	return z.bytes, z.err
}

// ServeHackSkillsZip serves the product bundle through the generic cache mechanics.
func (p *Assets) ServeHackSkillsZip() http.HandlerFunc {
	return p.services.ServeCachedZip(p.BuildHackSkillsZip, "simple-hack-skills.zip")
}

// RewriteHackPluginZip keeps binary assets intact and rewrites only our
// text (.md/.yaml/.json) for a self-hosted instance's own address.
func (p *Assets) RewriteHackPluginZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		src, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			return nil, err
		}
		switch filepath.Ext(f.Name) {
		case ".md", ".yaml", ".json":
			body = p.HackInstanceText(body)
		}
		dst, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err = dst.Write(body); err != nil {
			return nil, err
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// hack-connect-steps.json is the one source for how each AI app connects to
// Simple Hack. /get-started renders every app's list from it
// (expandConnectSteps, called from chrome.go's chromePageRaw: the page
// marks each spot with <!--sh:connect:ID-->), and the Website screen
// (participant-website.js) fetches the same file raw, shows the apps marked
// "tab" with the shared publish steps below, and names the signed-in email
// in signin_hint. html, after and fallback are our own trusted markup,
// never user input.
//
// The platform chrome pipeline reaches expansion through ProductAssetHooks.

// ConnectCopy is an extra copyable value inside a step (a client id, an
// install command).
type ConnectCopy struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConnectStep is one numbered step.
type ConnectStep struct {
	HTML         string       `json:"html"`
	Address      bool         `json:"address,omitempty"`
	AddressLabel string       `json:"address_label,omitempty"`
	Copy         *ConnectCopy `json:"copy,omitempty"`
	Command      string       `json:"command,omitempty"`
	After        string       `json:"after,omitempty"`
	Prompt       bool         `json:"prompt,omitempty"`
}

// ConnectApp is one AI app's connect steps.
type ConnectApp struct {
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Tab           bool          `json:"tab,omitempty"`
	Connect       []ConnectStep `json:"connect"`
	ConnectHosted []ConnectStep `json:"connect_hosted,omitempty"`
	Fallback      string        `json:"fallback,omitempty"`
	Note          string        `json:"note,omitempty"`
}

// ConnectStepsDoc is the whole hack-connect-steps.json file.
type ConnectStepsDoc struct {
	V          int           `json:"v"`
	MCPPath    string        `json:"mcp_path"`
	MCPAddress string        `json:"mcp_address"`
	SigninHint string        `json:"signin_hint"`
	Publish    []ConnectStep `json:"publish"`
	Apps       []ConnectApp  `json:"apps"`
}

var (
	connectStepsOnce sync.Once
	connectStepsDoc  ConnectStepsDoc
	connectStepsErr  error
)

// loadConnectSteps parses the embedded hack-connect-steps.json once.
func loadConnectSteps() (ConnectStepsDoc, error) {
	connectStepsOnce.Do(func() {
		raw, err := hackassets.Static.ReadFile("static/hack-connect-steps.json")
		if err != nil {
			connectStepsErr = err
			return
		}
		connectStepsErr = json.Unmarshal(raw, &connectStepsDoc)
	})
	return connectStepsDoc, connectStepsErr
}

// connectAppByID finds one app's steps.
func connectAppByID(id string) (ConnectApp, bool) {
	doc, err := loadConnectSteps()
	if err != nil {
		return ConnectApp{}, false
	}
	for _, a := range doc.Apps {
		if a.ID == id {
			return a, true
		}
	}
	return ConnectApp{}, false
}

func connectCopyRow(id, value, label string) string {
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `">` + html.EscapeString(value) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectAddressRow is connectCopyRow for the one MCP address, marked
// data-mcp-address so the page's own script can refill it from the
// viewer's origin (exact on a self-hosted instance too, not just
// HackInstanceText's string rewrite).
func connectAddressRow(id, value, label string) string {
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `" data-mcp-address>` + html.EscapeString(value) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectCommandRow renders a ConnectStep.Command template, filled with
// mcpAddress, in its own copy box.
func connectCommandRow(id, template, mcpAddress, label string) string {
	filled := strings.ReplaceAll(template, "{address}", mcpAddress)
	return `<div class="copyrow"><code id="` + html.EscapeString(id) + `" data-mcp-cmd="` + html.EscapeString(template) + `">` + html.EscapeString(filled) +
		`</code><button class="btn" type="button" data-copy-target="` + html.EscapeString(id) +
		`" aria-label="` + html.EscapeString(label) + `">Copy</button></div>`
}

// connectStepsListHTML is one app's <ol class="steps"> for /get-started.
func connectStepsListHTML(doc ConnectStepsDoc, app ConnectApp) string {
	var b strings.Builder
	b.WriteString(`<ol class="steps">`)
	for _, s := range app.Connect {
		b.WriteString("\n            <li>" + s.HTML)
		if s.Copy != nil {
			b.WriteString(connectCopyRow(s.Copy.ID, s.Copy.Value, s.Copy.Label))
		}
		if s.Address {
			label := s.AddressLabel
			if label == "" {
				label = app.Label
			}
			b.WriteString("\n              " + connectAddressRow("mcp-"+app.ID, doc.MCPAddress, "Copy "+label+" connector address") + "\n            ")
		}
		if s.Command != "" {
			b.WriteString("\n              " + connectCommandRow("cmd-"+app.ID, s.Command, doc.MCPAddress, "Copy "+app.Label+" command") + "\n            ")
		}
		b.WriteString(s.After + "</li>")
	}
	b.WriteString("\n          </ol>")
	return b.String()
}

// expandConnectSteps replaces each <!--sh:connect:ID--> in a page with that
// app's list.
func expandConnectSteps(page []byte) []byte {
	if !bytes.Contains(page, []byte("<!--sh:connect:")) {
		return page
	}
	doc, err := loadConnectSteps()
	if err != nil {
		return page
	}
	for _, app := range doc.Apps {
		page = bytes.ReplaceAll(page, []byte("<!--sh:connect:"+app.ID+"-->"), []byte(connectStepsListHTML(doc, app)))
		if app.Fallback != "" {
			page = bytes.ReplaceAll(page, []byte("<!--sh:connect-fallback:"+app.ID+"-->"), []byte(`<p class="hint">`+app.Fallback+`</p>
          `+connectCopyRow("mcp-"+app.ID, doc.MCPAddress, "Copy "+app.Label+" connector address")))
		}
	}
	return page
}

// hackPersonalPresentationHTML: shared help pages omit personal
// presentation when served on Hack.
func (p *Assets) personalPresentationHTML(name string, body []byte) []byte {
	if name == "enterprise.html" {
		return bytes.ReplaceAll(body, []byte(`href="https://simple-hack.app/hackathons"`), []byte(`href="`+p.inkBaseURL()+`/hackathons"`))
	}
	return hackPersonalPresentationHTML(name, body)
}

func hackPersonalPresentationHTML(name string, body []byte) []byte {
	switch name {
	case "features.html":
		body = regexp.MustCompile(`<a data-host-only [^>]*>[^<]*</a>`).ReplaceAll(body, nil)
		// The page is generated from features.json; each feature is one <details>.
		start := bytes.Index(body, []byte(`<details class="feat" id="your-home-page">`))
		if start >= 0 {
			end := bytes.Index(body[start:], []byte("</details>"))
			if end >= 0 {
				return append(append([]byte{}, body[:start]...), body[start+end+len("</details>"):]...)
			}
		}
	case "architecture.html":
		return bytes.ReplaceAll(body, []byte("showcase or selected home, with a live feed, bio, pins and order"), []byte("showcase"))
	}
	return body
}

// Hack serves the same visitor sign-in and storage helper as Host, with its
// own header and product wording.
func hackAuthJS(b []byte) []byte {
	end := bytes.Index(b, []byte("*/\n"))
	if end < 0 {
		panic("auth.js header changed")
	}
	b = append([]byte(`/* Simple Hack visitor sign-in and site storage.
 * SH.mount(target), SH.email and SH.requireSignIn() handle a visitor's session.
 * SH.storage.kv(), SH.storage.sqlite() and SH.storage.files() use each
 * resource's read/write policy. Signed-in access covers a whole resource,
 * not separate records per visitor. A resource may allow anonymous access.
 * Owner-only resource setup happens through the Simple Hack connector.
 */
`), b[end+3:]...)
	// Visitor wording on Hack. The two lines stay exact in the embedded file.
	oldBy := []byte(`var signInBy = "Sign-in by Simple Host";`)
	oldProduct := []byte(`var signInProduct = "Simple Host";`)
	if !bytes.Contains(b, oldBy) || !bytes.Contains(b, oldProduct) {
		panic("auth.js visitor product line changed")
	}
	b = bytes.Replace(b, oldBy, []byte(`var signInBy = "Sign-in by Simple Hack";`), 1)
	b = bytes.Replace(b, oldProduct, []byte(`var signInProduct = "Simple Hack";`), 1)
	return b
}

// setupInsertions are the places where the platform's setup.js takes Simple
// Hack's choice: each anchor appears exactly once in the platform file and is
// replaced by the product's text.
var setupInsertions = [][2]string{
	{"// lists are generated from code.", "// lists are generated from code; Simple Hack steps live in simple-hack.json."},
	{"var FILES = { small: '/setup/small-box-settings.json', ent: '/setup/enterprise-settings.json' };", "var FILES = { small: '/setup/small-box-settings.json', ent: '/setup/enterprise-settings.json', hack: '/setup/simple-hack.json' };"},
	{"product: 'small',", "product: document.body.getAttribute('data-setup-product') === 'hack' ? 'hack' : 'small',"},
	{"    /*sh:product-state*/\n", "      hack: { SITE_DOMAIN: '', SUPPORT_EMAIL: '', PRIVACY_URL: '', RESEND_API_KEY: '', MAIL_FROM: '', assistant: false, ASSISTANT_BASE_URL: '', ASSISTANT_MODEL: '', ASSISTANT_API_KEY: '' },\n"},
	{"    /*sh:product-choice*/\n", "    else if (want === 'simple-hack') S.product = 'hack';\n"},
	{"S.mode === 'advanced' /*sh:product-progress*/", "S.mode === 'advanced' && S.product !== 'hack'"},
	{"    /*sh:product-render*/\n    [renderChoose, renderBasics, renderAdvanced, renderOutput][S.step]();", "    if (S.product === 'hack' && S.step === 1) renderHackBasics();\n    else if (S.product === 'hack' && S.step === 3) renderHackOutput();\n    else [renderChoose, renderBasics, renderAdvanced, renderOutput][S.step]();"},
	{"    /*sh:product-mode*/ app.appendChild", "    if (S.product !== 'hack') app.appendChild"},
}

const setupProductAnchor = "  function productChoices(choices) { return choices; }\n  /*sh:product-setup*/\n\n"

// buildSetupJS puts Simple Hack's choice into the platform's setup.js.
func buildSetupJS(shared, product []byte) ([]byte, error) {
	once := func(anchor string, with []byte) error {
		if n := bytes.Count(shared, []byte(anchor)); n != 1 {
			return fmt.Errorf("setup.js: anchor %q appears %d times, want 1", anchor, n)
		}
		shared = bytes.Replace(shared, []byte(anchor), with, 1)
		return nil
	}
	for _, in := range setupInsertions {
		if err := once(in[0], []byte(in[1])); err != nil {
			return nil, err
		}
	}
	if err := once(setupProductAnchor, product); err != nil {
		return nil, err
	}
	return shared, nil
}

// Rewrite builds the product view of shared assets.
func (p *Assets) Rewrite(name string, body []byte) []byte {
	switch name {
	case "static/features.json", "features.json":
		body = stripFeatureMarkers(body, "self", "hosted")
		return stripFeatureMarkers(body, "hashack", "nohack")
	case "static/auth.js", "auth.js":
		return hackAuthJS(body)
	case "static/setup/setup.js", "setup/setup.js":
		shared, ok := p.services.Static("setup/setup.js")
		if !ok {
			panic("setup.js: the platform file is missing")
		}
		out, err := buildSetupJS(shared, body)
		if err != nil {
			panic(err.Error())
		}
		return out
	}
	return body
}

// ServeSetupJS serves the product setup script assembled from the shared helper.
func (p *Assets) ServeSetupJS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := hackassets.Static.ReadFile("static/setup/setup-hack.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body = p.Rewrite("setup/setup.js", body)
		http.ServeContent(w, r, "setup.js", time.Time{}, bytes.NewReader(body))
	}
}

// stripFeatureMarkers resolves the product's bracket blocks in the raw JSON
// reference just as its HTML page resolves the corresponding comment blocks.
func stripFeatureMarkers(body []byte, keep, drop string) []byte {
	open, close := []byte("[["+drop+"]]"), []byte("[[/"+drop+"]]")
	for {
		i := bytes.Index(body, open)
		if i < 0 {
			break
		}
		j := bytes.Index(body[i:], close)
		if j < 0 {
			break
		}
		body = append(body[:i:i], body[i+j+len(close):]...)
	}
	body = bytes.ReplaceAll(body, []byte("[["+keep+"]]"), nil)
	return bytes.ReplaceAll(body, []byte("[[/"+keep+"]]"), nil)
}
