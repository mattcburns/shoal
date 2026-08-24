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
  bmc_endpoint     TEXT,
  system_id        TEXT,
  credential_ref   TEXT
);

-- Phase 5a.1: columns for cross-process cancel/orphan BMC cleanup
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS system_id TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS credential_ref TEXT;

-- Multi-stage provisioning M1: stage runner metadata
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS current_stage TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS install_strategy TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS stages_json TEXT;

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

ALTER TABLE sensor_readings ADD COLUMN IF NOT EXISTS note TEXT;

CREATE TABLE IF NOT EXISTS job_log (
  job_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  line TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS device_power (
  device_id TEXT PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL,
  power_state TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS firmware_inventory (
  device_id TEXT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  component_id TEXT NOT NULL,
  name TEXT,
  version TEXT,
  manufacturer TEXT,
  software_id TEXT,
  health TEXT,
  updateable BOOLEAN,
  release_date TEXT
);

CREATE INDEX IF NOT EXISTS jobs_state_idx ON jobs (state);
CREATE INDEX IF NOT EXISTS jobs_device_idx ON jobs (device_id);
CREATE INDEX IF NOT EXISTS events_device_ts_idx ON events (device_id, ts);
CREATE INDEX IF NOT EXISTS job_log_job_ts_idx ON job_log (job_id, ts);
CREATE INDEX IF NOT EXISTS firmware_inventory_device_ts_idx ON firmware_inventory (device_id, ts);
