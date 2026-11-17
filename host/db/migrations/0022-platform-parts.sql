-- D3-S5c: platform part of the historical mixed paper-metrics migration.
-- No later platform migration depends on paper_run. Existing installs are unchanged.
CREATE TABLE IF NOT EXISTS paper_run (
 run_id BIGSERIAL PRIMARY KEY, started_at TIMESTAMPTZ NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('host','hack')),
 vcs_revision TEXT, vcs_modified BOOLEAN NOT NULL, go_version TEXT NOT NULL,
 binary_sha256 TEXT, config_hash TEXT NOT NULL, metric_def_version INTEGER NOT NULL,
 bucket_scheme TEXT NOT NULL, gomaxprocs INTEGER NOT NULL, num_cpu INTEGER NOT NULL,
 cgroup_memory_max TEXT, cgroup_cpu_max TEXT
);
