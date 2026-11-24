package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ncruces/go-sqlite3/driver"

	"simplehost/core/limits"
	data "simplehost/data/core"
)

// versions: the live version and the last published ones before it, up to
// the kept count, hard-linked from the old folders. The live version comes
// from the old site's current/ folder, which is what the old service serves
// (some sites change files there in place, or rsync straight into it):
// followed when it is a link, copied when it is a real folder. Drafts never
// published stay behind.
func (r *runner) versions(ctx context.Context, write bool) ([]row, error) {
	old, err := query(ctx, r.old, r.sql(fmt.Sprintf(`
		SELECT v.site_id::text, v.version_number::text,
			(SELECT u.id FROM users u WHERE u.id::text = v.author OR lower(u.username) = lower(v.author) OR u.handle = v.author LIMIT 1)::text,
			COALESCE(v.created_at, {never})::text, {dir} || '/v' || v.version_number, (v.version_number = s.active_version)::text
		FROM (SELECT v.*, row_number() OVER (PARTITION BY v.site_id ORDER BY v.version_number = s.active_version DESC, v.version_number DESC) AS rank
			FROM versions v JOIN sites s ON s.id = v.site_id WHERE v.status = 'active' AND s.id IN {copied}) v
		JOIN sites s ON s.id = v.site_id WHERE v.rank <= %d ORDER BY 1, 2`, limits.KeptVersions.N)))
	if err != nil {
		return nil, err
	}
	var out []row
	for _, x := range old {
		n, _ := strconv.Atoi(x[1].String)
		src, dst, link := filepath.Join(r.cfg.OldDir, x[4].String), r.sites.VersionDir(x[0].String, n), true
		if x[5].String == "true" {
			src, link = liveFolder(src)
		}
		if _, err := os.Stat(src); err != nil {
			r.note("files missing on disk", x[0].String, "version %d has no folder in the old data folder", n)
			continue
		}
		size, err := r.tree(src, dst, link, write)
		if err != nil {
			return nil, err
		}
		r.keep[dst], r.keep[filepath.Dir(dst)] = true, true
		out = append(out, row{x[0], x[1], x[2], x[3], text(strconv.FormatInt(size, 10))})
	}
	return out, nil
}

// liveFolder is what the old service serves for a site whose live version is
// in folder v: its current/ folder beside v, followed when it is a link (then
// hard links are safe), copied when it is a real folder (it changes in place).
// No current/ at all: v itself.
func liveFolder(v string) (string, bool) {
	cur := filepath.Join(filepath.Dir(v), "current")
	info, err := os.Lstat(cur)
	switch {
	case err != nil:
		return v, true
	case info.Mode()&fs.ModeSymlink != 0:
		if to, err := filepath.EvalSymlinks(cur); err == nil {
			return to, true
		}
		return v, true
	}
	return cur, false
}

func text(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// tree makes dst hold exactly the regular files of src: hard links for
// versions, copies for file stores. Without write it only reports what
// differs. It returns the bytes in src.
func (r *runner) tree(src, dst string, link, write bool) (int64, error) {
	var total int64
	seen := map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		seen[rel] = true
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		to := filepath.Join(dst, rel)
		if same(p, to) {
			return nil
		}
		if !write {
			r.diff("file %s: missing or different in the new data folder", to)
			return nil
		}
		r.changes++
		os.Remove(to)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if link {
			return os.Link(p, to)
		}
		return copyFile(p, to)
	})
	if err != nil {
		return 0, err
	}
	filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if rel, _ := filepath.Rel(dst, p); err == nil && !d.IsDir() && !seen[rel] {
			if write {
				r.changes++
				return os.Remove(p)
			}
			r.diff("file %s: in the new data folder but not in the old", p)
		}
		return nil
	})
	return total, nil
}

// same: the same file (a hard link) or the same bytes.
func same(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || ia.Size() != ib.Size() {
		return false
	}
	return os.SameFile(ia, ib) || hashFile(a) == hashFile(b)
}

func hashFile(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// space is one new namespace being built from old saved data.
type space struct {
	site, name, created string
	seed                string // old SQLite file the namespace starts from ("" = empty)
	rules               map[string]json.RawMessage
	fill                func(ctx context.Context, f *sql.DB) error
	files               string // old file store folder
}

// namespaces turns every old resource into a namespace, and the older kinds
// kept in Postgres (sites.state, collection_items) into "state" and
// "collections" namespaces for sites visited in the last 90 days.
func (r *runner) namespaces(ctx context.Context, write bool) ([]row, error) {
	res, err := query(ctx, r.old, r.sql(`
		SELECT x.site_id::text, x.name, x.kind, x.rules::text, COALESCE((SELECT json_object_agg(lower(t.table_name), t.rules) FROM site_storage_tables t
			WHERE t.site_id = x.site_id AND t.resource_name = x.name AND t.rules IS NOT NULL)::text, '{}'), COALESCE(s.created_at, {never})::text, {dir}
		FROM site_storage_resources x JOIN sites s ON s.id = x.site_id WHERE s.id IN {copied} ORDER BY 1, 2`))
	if err != nil {
		return nil, err
	}
	files, err := query(ctx, r.old, r.sql(`SELECT site_id::text, resource_name, path, NULLIF(writer_id, ''), created_at::text
		FROM site_storage_files WHERE site_id IN {copied} ORDER BY 1, 2, 3`))
	if err != nil {
		return nil, err
	}
	var spaces []*space
	taken := map[string]bool{}
	for _, x := range res {
		site, name, kind, dir := x[0].String, x[1].String, x[2].String, filepath.Join(r.cfg.OldDir, x[6].String, "runtime")
		if !data.ValidName(name) {
			r.note("saved data not carried", site, "%q is not a namespace name (lower-case letters, digits, _ or -)", name)
			continue
		}
		taken[site+"/"+name] = true
		var all json.RawMessage
		if x[3].Valid {
			all = json.RawMessage(x[3].String)
		}
		sp := &space{site: site, name: name, created: x[5].String, rules: map[string]json.RawMessage{}}
		switch kind {
		case "sqlite":
			var own map[string]json.RawMessage
			json.Unmarshal([]byte(x[4].String), &own)
			sp.seed = filepath.Join(dir, "sqlite", name+".sqlite")
			sp.fill = func(ctx context.Context, f *sql.DB) error {
				names, err := query(ctx, f, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('_values', '_files')`)
				for _, t := range names {
					if rs, ok := own[strings.ToLower(t[0].String)]; ok {
						sp.rules[t[0].String] = rs
					} else if all != nil {
						sp.rules[t[0].String] = all
					}
				}
				return err
			}
		case "kv":
			if all != nil {
				sp.rules["_values"] = all
			}
			sp.fill = func(ctx context.Context, f *sql.DB) error {
				return r.values(ctx, f, filepath.Join(dir, "kv", name+".sqlite"))
			}
		case "files":
			if all != nil {
				sp.rules["_files"] = rename(all, "path", "name")
			}
			sp.files = filepath.Join(dir, "files", name)
			sp.fill = func(ctx context.Context, f *sql.DB) error {
				for _, y := range files {
					if y[0].String != site || y[1].String != name {
						continue
					}
					err := data.ValidFileName(y[2].String) // a plain path inside the store, or never read
					var head []byte
					var size int64
					if err == nil {
						head, size, err = sniff(filepath.Join(sp.files, y[2].String))
					}
					if err != nil {
						r.note("files missing on disk", site, "file %s of %s is missing", y[2].String, name)
						continue
					}
					if _, err := f.ExecContext(ctx, `INSERT INTO _files (name, size, type, visitor_id, created_at) VALUES (?, ?, ?, ?, ?)`,
						y[2].String, size, http.DetectContentType(head), y[3], y[4].String); err != nil {
						return err
					}
				}
				return nil
			}
		}
		spaces = append(spaces, sp)
	}
	older, err := r.older(ctx, taken)
	if err != nil {
		return nil, err
	}
	var out []row
	for _, sp := range append(spaces, older...) {
		x, err := r.build(ctx, sp, write)
		if err != nil {
			return nil, fmt.Errorf("namespace %s/%s: %w", sp.site, sp.name, err)
		}
		out = append(out, x)
	}
	return out, nil
}

// values fills _values from an old key-value file.
func (r *runner) values(ctx context.Context, f *sql.DB, old string) error {
	if _, err := os.Stat(old); err != nil {
		return nil // declared, never written
	}
	tmp := filepath.Join(r.tmp, "kv.sqlite")
	defer os.Remove(tmp)
	defer os.Remove(tmp + "-wal")
	if err := seed(old, tmp); err != nil {
		return err
	}
	kv, err := driver.Open("file:" + tmp)
	if err != nil {
		return err
	}
	defer kv.Close()
	rows, err := query(ctx, kv, `SELECT key, value, NULLIF(visitor_id, ''), updated_at FROM kv`)
	for _, x := range rows {
		if _, err := f.ExecContext(ctx, `INSERT INTO _values (key, value, visitor_id, updated_at) VALUES (?, ?, ?, ?)`, args(x)...); err != nil {
			return err
		}
	}
	return err
}

// older are the saved data kinds the old service kept in Postgres.
func (r *runner) older(ctx context.Context, taken map[string]bool) ([]*space, error) {
	state, err := query(ctx, r.old, r.sql(`SELECT s.id::text, s.state::text, COALESCE(s.created_at, {never})::text, `+recentVisits+`::text
		FROM sites s WHERE s.id IN {copied} AND s.state IS NOT NULL ORDER BY 1`))
	if err != nil {
		return nil, err
	}
	items, err := query(ctx, r.old, r.sql(`SELECT c.site_id::text, c.collection, c.id::text, c.data::text, c.submitted_by::text,
		COALESCE(c.created_at, {never})::text, COALESCE(s.created_at, {never})::text, `+recentVisits+`::text
		FROM collection_items c JOIN sites s ON s.id = c.site_id WHERE s.id IN {copied} AND c.deleted_at IS NULL ORDER BY 1, 2, 3`))
	if err != nil {
		return nil, err
	}
	var out []*space
	usable := func(site, name, recent string) bool {
		switch {
		case recent != "true":
			r.note("older saved data not carried", site, "%s: no visits in 90 days", name)
		case taken[site+"/"+name]:
			r.note("older saved data not carried", site, "%s: the site already has saved data of that name", name)
		default:
			return true
		}
		return false
	}
	for _, x := range state {
		doc := x[1].String
		if usable(x[0].String, "state", x[3].String) {
			out = append(out, &space{site: x[0].String, name: "state", created: x[2].String, fill: func(ctx context.Context, f *sql.DB) error {
				var obj map[string]json.RawMessage
				if json.Unmarshal([]byte(doc), &obj) != nil {
					obj = map[string]json.RawMessage{"state": json.RawMessage(doc)}
				}
				for k, v := range obj {
					if _, err := f.ExecContext(ctx, `INSERT INTO _values (key, value, updated_at) VALUES (?, ?, ?)`, k, string(v), x[2].String); err != nil {
						return err
					}
				}
				return nil
			}})
		}
	}
	for i := 0; i < len(items); {
		j, site := i, items[i][0].String
		for j < len(items) && items[j][0].String == site {
			j++
		}
		group := items[i:j]
		if usable(site, "collections", group[0][7].String) {
			out = append(out, &space{site: site, name: "collections", created: group[0][6].String, fill: func(ctx context.Context, f *sql.DB) error {
				made := map[string]bool{}
				for _, x := range group {
					t := `"` + strings.ReplaceAll(x[1].String, `"`, `""`) + `"`
					if !made[t] {
						if _, err := f.ExecContext(ctx, `CREATE TABLE `+t+` (id INTEGER PRIMARY KEY, data TEXT NOT NULL, visitor_id TEXT, created_at TEXT)`); err != nil {
							return err
						}
						made[t] = true
					}
					if _, err := f.ExecContext(ctx, `INSERT INTO `+t+` (id, data, visitor_id, created_at) VALUES (?, ?, ?, ?)`, x[2].String, x[3].String, x[4], x[5].String); err != nil {
						return err
					}
				}
				return nil
			}})
		}
		i = j
	}
	return out, nil
}

// build makes the namespace's file in the scratch folder, then (copy) puts it
// in place only when its tables differ from the file already there, or
// (check) reports the tables that differ.
func (r *runner) build(ctx context.Context, sp *space, write bool) (row, error) {
	tmp := filepath.Join(r.tmp, "ns.sqlite")
	os.Remove(tmp)
	os.Remove(tmp + "-wal")
	if _, err := os.Stat(sp.seed); sp.seed != "" && err == nil {
		if err := seed(sp.seed, tmp); err != nil {
			return nil, err
		}
	}
	f, err := driver.Open("file:" + tmp)
	if err != nil {
		return nil, err
	}
	_, err = f.ExecContext(ctx, data.BuiltIn)
	if err == nil && sp.fill != nil {
		err = sp.fill(ctx, f)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return nil, err
	}
	final := filepath.Join(r.cfg.New.DataDir, "sites", sp.site, "data", sp.name+".sqlite")
	r.keep[final], r.keep[filepath.Dir(final)], r.keep[filepath.Dir(filepath.Dir(final))] = true, true, true
	got, want := checksums(final), checksums(tmp)
	if differ := diffSums(want, got); len(differ) > 0 {
		if !write {
			r.diff("namespace %s/%s: %s differ", sp.site, sp.name, strings.Join(differ, ", "))
		} else {
			r.changes++
			if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
				return nil, err
			}
			os.Remove(final + "-wal")
			os.Remove(final + "-shm")
			if err := os.Rename(tmp, final); err != nil {
				return nil, err
			}
		}
	}
	if sp.files != "" {
		dst := filepath.Join(r.cfg.New.DataDir, "sites", sp.site, "files", sp.name)
		r.keep[dst], r.keep[filepath.Dir(dst)] = true, true
		if _, err := os.Stat(sp.files); err == nil {
			if _, err := r.tree(sp.files, dst, false, write); err != nil {
				return nil, err
			}
		}
	}
	rules, err := json.Marshal(sp.rules)
	if err != nil {
		return nil, err
	}
	var canon string
	if err := r.new.QueryRowContext(ctx, `SELECT $1::jsonb::text`, string(rules)).Scan(&canon); err != nil {
		return nil, err
	}
	spam := strings.Contains(canon, "auth.checked") // the platform's one key pair checks it
	return row{text(sp.site), text(sp.name), text(canon), text(strconv.FormatBool(spam)), text(sp.created)}, nil
}

// seed copies an old SQLite file and its write-ahead log, so a file whose
// last writes are still in the log comes across whole.
func seed(from, to string) error {
	if err := copyFile(from, to); err != nil {
		return err
	}
	if _, err := os.Stat(from + "-wal"); err == nil {
		return copyFile(from+"-wal", to+"-wal")
	}
	return nil
}

// checksums is each table's definition, row count and checksum, the rows
// sorted so the order they were written in does not matter. nil: no file.
func checksums(path string) map[string]string {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	f, err := driver.Open("file:" + path + "?mode=ro")
	if err != nil {
		return map[string]string{"(file)": err.Error()}
	}
	defer f.Close()
	ctx := context.Background()
	out := map[string]string{}
	defs, err := query(ctx, f, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return map[string]string{"(file)": err.Error()}
	}
	for _, d := range defs {
		rows, err := f.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(d[0].String, `"`, `""`)+`"`)
		if err != nil {
			out[d[0].String] = err.Error()
			continue
		}
		cols, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(vals))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			b, _ := json.Marshal(vals)
			lines = append(lines, string(b))
		}
		rows.Close()
		sort.Strings(lines)
		h := sha256.Sum256([]byte(d[1].String + "\n" + strings.Join(lines, "\n")))
		out[d[0].String] = fmt.Sprintf("%d rows %x", len(lines), h[:8])
	}
	return out
}

func diffSums(want, got map[string]string) []string {
	var out []string
	if got == nil {
		return []string{"all tables"}
	}
	for t, s := range want {
		if got[t] != s {
			out = append(out, t)
		}
	}
	for t := range got {
		if _, ok := want[t]; !ok {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// rename changes a column name inside a rule set's expressions: the new
// _files table calls a file's path "name".
func rename(set json.RawMessage, from, to string) json.RawMessage {
	re := regexp.MustCompile(`\b` + from + `\b`)
	var s struct {
		Version  any              `json:"version"`
		Policies []map[string]any `json:"policies"`
	}
	if json.Unmarshal(set, &s) != nil {
		return set
	}
	for _, p := range s.Policies {
		for _, k := range []string{"using", "check"} {
			if e, ok := p[k].(string); ok {
				p[k] = re.ReplaceAllString(e, to)
			}
		}
	}
	b, _ := json.Marshal(s)
	return b
}

func sniff(p string) ([]byte, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return bytes.Clone(head[:n]), info.Size(), nil
}

// prune removes what the copy no longer wants from the new data folder: sites
// gone from the old service, versions past the kept count, namespaces and
// file stores gone. Unknown entries are left alone.
func (r *runner) prune() error {
	root := filepath.Join(r.cfg.New.DataDir, "sites")
	siteDirs, _ := os.ReadDir(root)
	for _, s := range siteDirs {
		dir := filepath.Join(root, s.Name())
		if !r.keep[dir] {
			r.changes++
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			continue
		}
		for _, sub := range []string{".", "data", "files"} {
			entries, _ := os.ReadDir(filepath.Join(dir, sub))
			for _, e := range entries {
				p := filepath.Join(dir, sub, e.Name())
				name := strings.TrimSuffix(strings.TrimSuffix(e.Name(), "-wal"), "-shm")
				versionDir := sub == "." && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == ""
				if (versionDir || sub != ".") && !r.keep[filepath.Join(dir, sub, name)] {
					r.changes++
					if err := os.RemoveAll(p); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
