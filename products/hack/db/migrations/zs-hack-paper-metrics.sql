-- Simple Hack paper metrics (SIM-335): aggregates and run provenance.
-- Only the non-exported event-key lookup holds a source identifier.
-- Definition 4: shared provenance and private key lookup; Host tables unchanged.
CREATE TABLE IF NOT EXISTS paper_run (
 run_id BIGSERIAL PRIMARY KEY, started_at TIMESTAMPTZ NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('host','hack')),
 vcs_revision TEXT, vcs_modified BOOLEAN NOT NULL, go_version TEXT NOT NULL,
 binary_sha256 TEXT, config_hash TEXT NOT NULL, metric_def_version INTEGER NOT NULL,
 bucket_scheme TEXT NOT NULL, gomaxprocs INTEGER NOT NULL, num_cpu INTEGER NOT NULL,
 cgroup_memory_max TEXT, cgroup_cpu_max TEXT
);
CREATE TABLE IF NOT EXISTS paper_hack_key (
 event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
 event_key BIGINT UNIQUE NOT NULL, synthetic BOOLEAN NOT NULL,
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), first_open_at TIMESTAMPTZ
);
-- No source identifiers in the exported tables below.
CREATE TABLE IF NOT EXISTS paper_hack_event (
  snapshot_day DATE NOT NULL,
  event_key BIGINT NOT NULL,
  run_id BIGINT NOT NULL REFERENCES paper_run(run_id),
  synthetic BOOLEAN NOT NULL,
  stage TEXT NOT NULL CHECK (stage IN ('draft','open','building','closed','judging','results','archived')),
  created_week DATE NOT NULL,
  duration_hours DOUBLE PRECISION,
  expected_participants INTEGER,
  participants BIGINT,
  organisers BIGINT,
  judges BIGINT,
  applications BIGINT,
  applications_approved BIGINT,
  teams BIGINT,
  teams_with_entry BIGINT,
  teams_kept_after_leave BIGINT,
  team_size_p50 BIGINT,
  team_size_max BIGINT,
  team_sites BIGINT,
  team_site_bytes_sum BIGINT,
  team_site_bytes_p50 BIGINT,
  team_site_versions_sum BIGINT,
  event_storage_bytes BIGINT,
  submissions BIGINT,
  entries_with_code_url BIGINT,
  entries_with_video BIGINT,
  scores BIGINT,
  score_criteria BIGINT,
  assignments BIGINT,
  conflicts BIGINT,
  votes BIGINT,
  announcements BIGINT,
  results_published BOOLEAN,
  deploys_total BIGINT,
  minutes_create_to_first_deploy BIGINT,
  PRIMARY KEY(snapshot_day,event_key)
);

CREATE INDEX IF NOT EXISTS paper_hack_event_key_day_idx ON paper_hack_event(event_key,snapshot_day DESC);

-- Active event-minute summaries. Histograms are separate to avoid repeating
-- and accidentally summing event-wide member/concurrency/arrival totals.
CREATE TABLE IF NOT EXISTS paper_hack_minute (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), slot TIMESTAMPTZ NOT NULL,
 slot_secs SMALLINT NOT NULL DEFAULT 60 CHECK(slot_secs=60), event_key BIGINT NOT NULL,
 synthetic BOOLEAN NOT NULL, requests BIGINT NOT NULL, writes BIGINT NOT NULL, data_writes BIGINT NOT NULL,
 deploys BIGINT NOT NULL, active_members INTEGER NOT NULL, inflight_max BIGINT NOT NULL,
 peak_rps_1s BIGINT NOT NULL, arrivals_1s INT4[] NOT NULL CHECK(cardinality(arrivals_1s)=60),
 writes_1s INT4[] NOT NULL CHECK(cardinality(writes_1s)=60),
 PRIMARY KEY(slot,event_key,run_id)
);
CREATE TABLE IF NOT EXISTS paper_hack_latency (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), slot TIMESTAMPTZ NOT NULL,
 event_key BIGINT NOT NULL, synthetic BOOLEAN NOT NULL,
 class TEXT NOT NULL CHECK(class IN ('site_file','data_read','data_write','auth','mcp','api_read','api_write','platform_page')),
 status_group TEXT NOT NULL CHECK(status_group IN ('2xx','3xx','4xx','5xx')),
 n BIGINT NOT NULL, sum_us BIGINT NOT NULL, max_us BIGINT NOT NULL, bytes_out BIGINT NOT NULL,
 idx_offset SMALLINT NOT NULL, counts INT8[] NOT NULL,
 arrivals_1s INT4[] NOT NULL CHECK(cardinality(arrivals_1s)=60),
 writes_1s INT4[] NOT NULL CHECK(cardinality(writes_1s)=60),
 PRIMARY KEY(slot,event_key,class,status_group,run_id)
);
CREATE TABLE IF NOT EXISTS paper_hack_phase (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), event_key BIGINT NOT NULL, synthetic BOOLEAN NOT NULL,
 stage TEXT NOT NULL CHECK(stage IN ('draft','open','building','closed','judging','results','archived','submission_deadline','judging_locked','results_published','voting_opens','voting_closes')),
 phase_slot TIMESTAMPTZ NOT NULL, minute_offset INTEGER, origin_slot TIMESTAMPTZ, observed BOOLEAN NOT NULL, baseline BOOLEAN NOT NULL DEFAULT false,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), origin_change TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(event_key,stage,origin_change)
);
-- Versioned companion for Hack metrics in the legacy shared paper_daily table.
CREATE TABLE IF NOT EXISTS paper_hack_daily (
 run_id BIGINT NOT NULL REFERENCES paper_run(run_id), day DATE NOT NULL,
 metric TEXT NOT NULL, value DOUBLE PRECISION NOT NULL,
 PRIMARY KEY(day,metric)
);
