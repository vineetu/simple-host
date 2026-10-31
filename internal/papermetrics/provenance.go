package papermetrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/buildinfo"
)

var processStartedAt = time.Now()

// Only these parsed, non-secret config fields are ever hashed. No whole-config
// serialisation or environment dump is used, even transiently.
var runConfigKeys = []string{"limits", "events", "write_auth_mode", "person_hosts", "site_hosts", "paper_metrics"}

func (c *Collector) WithRunConfig(values map[string]any) *Collector {
	c.runConfig = map[string]any{}
	for _, key := range runConfigKeys {
		if value, ok := values[key]; ok {
			c.runConfig[key] = value
		}
	}
	return c
}
func configHash(values map[string]any) string {
	filtered := map[string]any{}
	for _, key := range runConfigKeys {
		if value, ok := values[key]; ok {
			filtered[key] = value
		}
	}
	data, _ := json.Marshal(filtered)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var executableDigest struct {
	sync.Once
	value any
}

func binaryDigest() any {
	executableDigest.Do(func() {
		path, err := os.Executable()
		if err != nil {
			return
		}
		file, err := os.Open(path)
		if err != nil {
			return
		}
		defer file.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err == nil {
			executableDigest.value = hex.EncodeToString(hash.Sum(nil))
		}
	})
	return executableDigest.value
}

var revisionPattern = regexp.MustCompile(`^[a-fA-F0-9]{7,64}$`)
var memoryLimitPattern = regexp.MustCompile(`^(max|[0-9]+)$`)
var cpuLimitPattern = regexp.MustCompile(`^(max|[0-9]+) [0-9]+$`)

func cgroupLimits() (memory, cpu any) {
	root := "/sys/fs/cgroup"
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if path, ok := strings.CutPrefix(line, "0::/"); ok {
				candidate := filepath.Join(root, filepath.Clean("/"+path))
				if _, err := os.Stat(filepath.Join(candidate, "memory.max")); err == nil {
					root = candidate
				}
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "memory.max")); err == nil {
		value := strings.TrimSpace(string(data))
		if memoryLimitPattern.MatchString(value) {
			memory = value
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "cpu.max")); err == nil {
		value := strings.TrimSpace(string(data))
		if cpuLimitPattern.MatchString(value) {
			cpu = value
		}
	}
	return
}

func (c *Collector) ensureRun(ctx context.Context) (int64, error) {
	if !Enabled() {
		return 0, errors.New("paper metrics disabled")
	}
	if id := c.runID.Load(); id != 0 {
		return id, nil
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	if id := c.runID.Load(); id != 0 {
		return id, nil
	}
	var revision any
	dirty := strings.HasSuffix(buildinfo.Commit, "-dirty")
	candidate := strings.TrimSuffix(buildinfo.Commit, "-dirty")
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				candidate = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
	}
	if revisionPattern.MatchString(candidate) {
		revision = candidate
	}
	memory, cpu := cgroupLimits()
	role := "host"
	version, scheme := 1, LegacyBucketScheme
	if c.hack != nil {
		role = "hack"
		version, scheme = MetricDefVersion, BucketScheme
	}
	var id int64
	err := c.db.QueryRowContext(ctx, `INSERT INTO paper_run (started_at,role,vcs_revision,vcs_modified,go_version,binary_sha256,config_hash,metric_def_version,bucket_scheme,gomaxprocs,num_cpu,cgroup_memory_max,cgroup_cpu_max)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING run_id`, processStartedAt.UTC(), role, revision, dirty, runtime.Version(), binaryDigest(), configHash(c.runConfig), version, scheme, runtime.GOMAXPROCS(0), runtime.NumCPU(), memory, cpu).Scan(&id)
	if err == nil {
		c.runID.Store(id)
	}
	return id, err
}

// RegisterRun records provenance only while collection is enabled.
func (c *Collector) RegisterRun(ctx context.Context) error { _, err := c.ensureRun(ctx); return err }
