-- Shoal telemetry + jobs schema (Postgres-primary, lab :5433 / shoal_telemetry).
-- Idempotent: safe to run on every process start.

CREATE TABLE IF NOT EXISTS jobs (
  id               TEXT PRIMARY KEY,
  device_id        TEXT NOT NULL,
  profile_ref      TEXT NOT NULL DEFAULT '',
  state            TEXT NOT NULL,
  attempt          INT  NOT NULL DEFAULT 0,
  phase            TEXT,
  percent          INT,
  last_marker_seq  INT  NOT NULL DEFAULT 0,
  started_at       TIMESTAMPTZ,
  updated_at       TIMESTAMPTZ NOT NULL,
  error            TEXT,
  sol_session_id   TEXT,
  iso_url          TEXT,
  bmc_endpoint     TEXT
);

CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  device_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  type TEXT,
  severity TEXT,
  component TEXT,
  message TEXT,
  raw_ref TEXT
);

CREATE TABLE IF NOT EXISTS sensor_readings (
  device_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  sensor TEXT,
  value DOUBLE PRECISION,
  unit TEXT
);

CREATE TABLE IF NOT EXISTS job_log (
  job_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  line TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_state_idx ON jobs (state);
CREATE INDEX IF NOT EXISTS events_device_ts_idx ON events (device_id, ts);
CREATE INDEX IF NOT EXISTS job_log_job_ts_idx ON job_log (job_id, ts);
