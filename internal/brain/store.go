package brain

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ErrNotReady is returned while no bundle has been built yet.
var ErrNotReady = errors.New("the product knowledge bundle has not been built yet; run sh-brain-sync")

// Store serves queries from the bundle file and reloads it when the file
// changes (checked at most every checkEvery).
type Store struct {
	path       string
	checkEvery time.Duration

	mu      sync.RWMutex
	bundle  *Bundle
	index   *index
	modTime time.Time
	size    int64
	checked time.Time
}

const checkEvery = 5 * time.Second

// Open returns a store for the bundle at path. A missing or bad bundle is not
// an error here: queries answer ErrNotReady until a good one appears.
func Open(path string) *Store {
	s := &Store{path: path, checkEvery: checkEvery}
	s.mu.Lock()
	s.reloadLocked(time.Now())
	s.mu.Unlock()
	return s
}

// current returns the loaded bundle, reloading first when the file changed.
func (s *Store) current() (*Bundle, *index, error) {
	now := time.Now()
	s.mu.RLock()
	fresh := now.Sub(s.checked) < s.checkEvery
	b, ix := s.bundle, s.index
	s.mu.RUnlock()
	if !fresh {
		s.mu.Lock()
		if now.Sub(s.checked) >= s.checkEvery {
			s.reloadLocked(now)
		}
		b, ix = s.bundle, s.index
		s.mu.Unlock()
	}
	if b == nil {
		return nil, nil, ErrNotReady
	}
	return b, ix, nil
}

func (s *Store) reloadLocked(now time.Time) {
	s.checked = now
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	if s.bundle != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return
	}
	b, err := load(s.path)
	if err != nil {
		// Keep serving the bundle already loaded.
		log.Printf("brain: %s not loaded: %v", s.path, err)
		return
	}
	s.bundle, s.index, s.modTime, s.size = b, newIndex(b), info.ModTime(), info.Size()
	log.Printf("brain: loaded bundle built %s (%d docs, %d parity rows, %d decisions, %d changes)",
		b.BuiltAt.Format(time.RFC3339), len(b.Docs), len(b.Parity), len(b.Decisions), len(b.Changes))
}

func load(path string) (*Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	if b.Format != Format {
		return nil, fmt.Errorf("bundle format %d, want %d", b.Format, Format)
	}
	for _, d := range b.Docs {
		if !Allowed(d.Path) {
			return nil, fmt.Errorf("bundle holds %q, which is not on the allowlist", d.Path)
		}
	}
	return &b, nil
}

// ---- search index -------------------------------------------------------------

type ref struct{ doc, sec int }

type index struct {
	postings map[string]map[ref]int // term -> section -> count
	lengths  map[ref]int
	avgLen   float64
	n        int
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be by can do does for from has have how i in is it its of on or our that the their this to was we what when where which who why will with you your about did there they them than then into not no yes any all") {
		stopwords[w] = true
	}
}

func tokens(text string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if len(f) < 2 || stopwords[f] {
			continue
		}
		out = append(out, stem(f))
	}
	return out
}

// stem folds plurals and a few common endings, so "importer" and "imports"
// both find "import".
func stem(w string) string {
	for _, suf := range []string{"ing", "ers", "er", "es", "ed", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

func newIndex(b *Bundle) *index {
	ix := &index{postings: map[string]map[ref]int{}, lengths: map[ref]int{}}
	total := 0
	for di, d := range b.Docs {
		for si, s := range d.Sections {
			r := ref{di, si}
			// The heading counts three times: a match there means the
			// section is about the term.
			toks := append(tokens(s.Text), tokens(strings.Repeat(s.Heading+" ", 3))...)
			for _, t := range toks {
				m := ix.postings[t]
				if m == nil {
					m = map[ref]int{}
					ix.postings[t] = m
				}
				m[r]++
			}
			ix.lengths[r] = len(toks)
			total += len(toks)
			ix.n++
		}
	}
	if ix.n > 0 {
		ix.avgLen = float64(total) / float64(ix.n)
	}
	return ix
}

type hit struct {
	ref   ref
	score float64
}

// search ranks sections by BM25 over the query's terms.
func (ix *index) search(query string, keep func(ref) bool) []hit {
	scores := map[ref]float64{}
	seen := map[string]bool{}
	for _, t := range tokens(query) {
		if seen[t] {
			continue
		}
		seen[t] = true
		post := ix.postings[t]
		if len(post) == 0 {
			continue
		}
		idf := math.Log(1 + (float64(ix.n)-float64(len(post))+0.5)/(float64(len(post))+0.5))
		for r, tf := range post {
			if keep != nil && !keep(r) {
				continue
			}
			l := float64(ix.lengths[r])
			scores[r] += idf * (float64(tf) * 2.2) / (float64(tf) + 1.2*(0.25+0.75*l/ix.avgLen))
		}
	}
	hits := make([]hit, 0, len(scores))
	for r, sc := range scores {
		hits = append(hits, hit{r, sc})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].ref.doc != hits[j].ref.doc {
			return hits[i].ref.doc < hits[j].ref.doc
		}
		return hits[i].ref.sec < hits[j].ref.sec
	})
	return hits
}

// textScore is a small relevance score of one piece of text for a query,
// for ranking parity rows and decisions (which are not index sections).
func textScore(query, text string) float64 {
	q := tokens(query)
	if len(q) == 0 {
		return 0
	}
	have := map[string]int{}
	for _, t := range tokens(text) {
		have[t]++
	}
	score, matched := 0.0, 0
	for _, t := range q {
		if n := have[t]; n > 0 {
			matched++
			score += 1 + math.Log(float64(n))
		}
	}
	// Every term matched beats a pile of one term.
	return score * float64(matched) / float64(len(q))
}

// snippet is about n characters of text around the first query term.
func snippet(text, query string, n int) string {
	lower := strings.ToLower(text)
	at := -1
	for _, f := range strings.Fields(strings.ToLower(query)) {
		f = strings.Trim(f, `"'?.,:;!()`)
		if len(f) < 3 || stopwords[f] {
			continue
		}
		if i := strings.Index(lower, stem(f)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 || len(text) <= n {
		return clip(collapse(text), n)
	}
	start := at - n/3
	if start < 0 {
		start = 0
	}
	for start > 0 && !utf8Start(text[start]) {
		start--
	}
	if i := strings.IndexAny(text[start:], " \n"); start > 0 && i >= 0 && i < 40 {
		start += i + 1
	}
	out := clip(collapse(text[start:]), n)
	if start > 0 {
		out = "… " + out
	}
	return out
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
