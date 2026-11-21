package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/webassets"
)

var EmbeddedTree = webassets.Static

// StaticFiles is the embedded tree, product-neutral (a pipeline's TextFS is
// the tree as one instance serves it). Public addresses use
// simple-host.app; basetext.go retains the compatibility rewriter.
var StaticFiles = baseTextFS{fsys: EmbeddedTree}

var SkillsModTime = time.Now().UTC()

// PluginVersion reads the version field from the embedded plugin.json once.
// Used at boot to construct the notice middleware and served at
// /skills/version for explicit checks by the MCP client.
var PluginVersion = sync.OnceValues(func() (string, error) {
	src, err := webassets.Host.Open(".claude-plugin/plugin.json")
	if err != nil {
		return "", fmt.Errorf("open plugin.json: %w", err)
	}
	defer src.Close()
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(src).Decode(&manifest); err != nil {
		return "", fmt.Errorf("decode plugin.json: %w", err)
	}
	if manifest.Version == "" {
		return "", fmt.Errorf("plugin.json missing version")
	}
	return manifest.Version, nil
})

// ServeStaticPage serves one embedded HTML page at a fixed route, for pages that
// need a clean URL rather than the .html the file server would expose. The
// shared chrome is filled in here; see chrome.go.
func (p *AssetPipeline) ServeStaticPage(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := p.BuildChromePage(name, p.ChromeDataFor(r, ""))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if name == "pricing.html" {
			body = applyPricingNumbers(body, p.hostedEvents, p.limits)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if p.ChromeProduct {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Write(httpx.StampNonce(r, body))
	})
}

// UICSP is AdminUICSP with the page's own frame-src sources.
func (p *AssetPipeline) UICSP(next http.Handler, frameSrc string) http.Handler {
	const policyFmt = "default-src 'self'; " +
		"script-src 'self' 'nonce-%s'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"img-src 'self' data:; " +
		"font-src 'self' data: https://fonts.gstatic.com; " +
		"connect-src 'self'; " +
		"frame-src %s; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		nonce := base64.StdEncoding.EncodeToString(b[:])
		policy := policyFmt
		if p.ChromeProduct {
			policy = strings.ReplaceAll(policy, " https://fonts.googleapis.com", "")
			policy = strings.ReplaceAll(policy, " https://fonts.gstatic.com", "")
		}
		w.Header().Set("Content-Security-Policy", fmt.Sprintf(policy, nonce, frameSrc))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpx.NonceKey{}, nonce)))
	})
}

func (p *AssetPipeline) ServeSkillZip(skillName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := p.buildSingleSkillZip(skillName)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		filename := skillName + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, SkillsModTime, bytes.NewReader(data))
	}
}

func (p *AssetPipeline) ServeSkillMarkdown(skillName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, err := p.servedSkillFS().Open("skills/" + skillName + "/SKILL.md")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		// A skill tells an agent which host to publish to. On a non-canonical
		// instance that must be this one, or the agent deploys elsewhere —
		// except a control-plane skill, whose endpoints and limits are the
		// public instance's (see copyRewritten).
		if !controlPlaneSkill(skillName + "/SKILL.md") {
			data = rewriteServedText(data, p.limitsText)
		}

		filename := skillName + "-SKILL.md"
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, SkillsModTime, bytes.NewReader(data))
	}
}

// BuildSkillsZip walks the embedded skills/ tree and emits a flat zip.
func (p *AssetPipeline) BuildSkillsZip() ([]byte, error) {
	z := &p.skillsZip
	z.once.Do(func() {
		root, err := fs.Sub(p.servedSkillFS(), "skills")
		if err == nil {
			z.bytes, err = zipTree(root, "", p.limitsText)
		}
		z.err = err
	})
	return z.bytes, z.err
}

func (p *AssetPipeline) buildSingleSkillZip(skillName string) ([]byte, error) {
	root, err := fs.Sub(p.servedSkillFS(), "skills/"+skillName)
	if err != nil {
		return nil, err
	}
	return zipTree(root, skillName+"/", p.limitsText)
}

// BuildPluginZip emits the plugin layout: .claude-plugin/plugin.json and
// skills/<name>/SKILL.md at the zip root.
func (p *AssetPipeline) BuildPluginZip() ([]byte, error) {
	z := &p.pluginZip
	z.once.Do(func() { z.bytes, z.err = zipTree(webassets.Host, "", p.limitsText) })
	return z.bytes, z.err
}

// zipTree zips every file under root at prefix+path, rewritten for this
// instance (copyRewritten) unless it belongs to a control-plane skill.
func zipTree(root fs.FS, prefix string, lr *limitsRewriter) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == "." || d.IsDir() {
			return walkErr
		}
		src, err := root.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := zw.Create(prefix + path)
		if err != nil {
			return err
		}
		return copyRewritten(dst, src, lr, controlPlaneSkill(prefix+path))
	})
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// copyRewritten writes src into dst with this instance's hostnames and limits
// substituted. lr is the instance's limits rewriter (AssetPipeline.limitsText).
//
// The skills are text, and small: reading one fully is cheaper than the
// alternative of streaming and rewriting across chunk boundaries, where a
// hostname split over two reads would silently survive unrewritten.
func copyRewritten(dst io.Writer, src io.Reader, lr *limitsRewriter, skipRewrite bool) error {
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	// Only rewrite text. Every file in the skills tree is markdown or JSON
	// today, but a substitution over a future binary asset would corrupt it
	// silently, and a corrupted file in a downloaded zip is very hard to trace
	// back to here.
	if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		// A control-plane skill keeps the public instance's hostnames and
		// limits: its endpoints exist only there (controlPlaneSkills).
		if !skipRewrite {
			data = rewriteServedText(data, lr)
		}
	}
	_, err = dst.Write(data)
	return err
}
