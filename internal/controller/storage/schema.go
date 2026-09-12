package storage

const schemaVersion = 2

const migrationV1 = `
CREATE TABLE network_scopes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    enrolled_at_ns INTEGER NOT NULL,
    retired_at_ns INTEGER,
    metadata TEXT NOT NULL CHECK (json_valid(metadata)),
    CHECK (retired_at_ns IS NULL OR retired_at_ns >= enrolled_at_ns)
) STRICT;

CREATE TABLE sensors (
    id TEXT PRIMARY KEY,
    scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    ownership TEXT NOT NULL,
    registered_at_ns INTEGER NOT NULL,
    metadata TEXT NOT NULL CHECK (json_valid(metadata))
) STRICT;
CREATE INDEX sensors_scope_idx ON sensors(scope_id, kind);

CREATE TABLE observations (
    id TEXT PRIMARY KEY,
    scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
    sensor_id TEXT NOT NULL REFERENCES sensors(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    source_stream TEXT NOT NULL,
    source_key TEXT NOT NULL,
    source_event_id TEXT,
    source_time_ns INTEGER,
    ingested_at_ns INTEGER NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    confidence REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    attribution TEXT NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    retention_class TEXT NOT NULL,
    expires_at_ns INTEGER NOT NULL,
    UNIQUE(sensor_id, source_stream, source_key)
) STRICT;
CREATE INDEX observations_scope_time_idx ON observations(scope_id, ingested_at_ns DESC, id);
CREATE INDEX observations_sensor_time_idx ON observations(sensor_id, ingested_at_ns DESC, id);
CREATE INDEX observations_kind_time_idx ON observations(kind, ingested_at_ns DESC, id);
CREATE INDEX observations_expiry_idx ON observations(expires_at_ns, id);

CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    user_label TEXT,
    created_at_ns INTEGER NOT NULL,
    retired_at_ns INTEGER,
    CHECK (retired_at_ns IS NULL OR retired_at_ns >= created_at_ns)
) STRICT;

CREATE TABLE identity_claims (
    id TEXT PRIMARY KEY,
    scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL,
    value TEXT NOT NULL,
    observed_at_ns INTEGER NOT NULL,
    valid_until_ns INTEGER,
    confidence REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    source_sensor_id TEXT NOT NULL REFERENCES sensors(id) ON DELETE RESTRICT,
    source_observation_id TEXT REFERENCES observations(id) ON DELETE SET NULL,
    retention_class TEXT NOT NULL,
    expires_at_ns INTEGER NOT NULL,
    CHECK (valid_until_ns IS NULL OR valid_until_ns >= observed_at_ns),
    UNIQUE(source_observation_id, kind, value)
) STRICT;
CREATE INDEX identity_claims_scope_value_idx ON identity_claims(scope_id, kind, value, observed_at_ns DESC);
CREATE INDEX identity_claims_expiry_idx ON identity_claims(expires_at_ns, id);

CREATE TABLE device_claim_links (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    claim_id TEXT NOT NULL REFERENCES identity_claims(id) ON DELETE CASCADE,
    valid_from_ns INTEGER NOT NULL,
    valid_until_ns INTEGER,
    confidence REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    authority TEXT NOT NULL,
    reason TEXT NOT NULL,
    evidence_observation_id TEXT REFERENCES observations(id) ON DELETE SET NULL,
    created_at_ns INTEGER NOT NULL,
    CHECK (valid_until_ns IS NULL OR valid_until_ns >= valid_from_ns)
) STRICT;
CREATE INDEX device_claim_links_device_time_idx ON device_claim_links(device_id, valid_from_ns DESC, id);
CREATE INDEX device_claim_links_claim_time_idx ON device_claim_links(claim_id, valid_from_ns DESC, id);

CREATE TABLE coverage_samples (
    id TEXT PRIMARY KEY,
    scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
    sensor_id TEXT NOT NULL REFERENCES sensors(id) ON DELETE RESTRICT,
    capability_id TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at_ns INTEGER NOT NULL,
    ended_at_ns INTEGER NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    evidence TEXT NOT NULL CHECK (json_valid(evidence)),
    retention_class TEXT NOT NULL,
    expires_at_ns INTEGER NOT NULL,
    CHECK (ended_at_ns >= started_at_ns)
) STRICT;
CREATE INDEX coverage_scope_time_idx ON coverage_samples(scope_id, capability_id, ended_at_ns DESC, id);
CREATE INDEX coverage_expiry_idx ON coverage_samples(expires_at_ns, id);

CREATE TABLE findings (
    id TEXT PRIMARY KEY,
    scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    category TEXT NOT NULL,
    severity TEXT NOT NULL,
    confidence REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    observed_at_ns INTEGER NOT NULL,
    created_at_ns INTEGER NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    retention_class TEXT NOT NULL,
    expires_at_ns INTEGER NOT NULL
) STRICT;
CREATE INDEX findings_scope_time_idx ON findings(scope_id, observed_at_ns DESC, id);
CREATE INDEX findings_expiry_idx ON findings(expires_at_ns, id);

CREATE TABLE finding_evidence (
    finding_id TEXT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    observation_id TEXT NOT NULL,
    PRIMARY KEY (finding_id, observation_id)
) WITHOUT ROWID, STRICT;

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    actor TEXT NOT NULL,
    occurred_at_ns INTEGER NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    payload TEXT NOT NULL CHECK (json_valid(payload)),
    retention_class TEXT NOT NULL,
    expires_at_ns INTEGER NOT NULL
) STRICT;
CREATE INDEX audit_events_time_idx ON audit_events(occurred_at_ns DESC, id);
CREATE INDEX audit_events_expiry_idx ON audit_events(expires_at_ns, id);

CREATE TABLE ingestion_checkpoints (
    sensor_id TEXT NOT NULL REFERENCES sensors(id) ON DELETE CASCADE,
    stream_id TEXT NOT NULL,
    cursor TEXT NOT NULL,
    updated_at_ns INTEGER NOT NULL,
    PRIMARY KEY (sensor_id, stream_id)
) WITHOUT ROWID, STRICT;

CREATE TABLE storage_events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    occurred_at_ns INTEGER NOT NULL,
    details TEXT NOT NULL CHECK (json_valid(details)),
    expires_at_ns INTEGER NOT NULL
) STRICT;
CREATE INDEX storage_events_time_idx ON storage_events(occurred_at_ns DESC, id);
CREATE INDEX storage_events_expiry_idx ON storage_events(expires_at_ns, id);
`

// Configuration changes and their audits use one SQLite statement. This avoids
// opening a transaction on the pinned connection that could absorb other writes.
const migrationV2 = `
CREATE TABLE resolver_configurations (
 id TEXT PRIMARY KEY,
 scope_id TEXT NOT NULL REFERENCES network_scopes(id) ON DELETE RESTRICT,
 resolver_id TEXT NOT NULL UNIQUE,
 query_id TEXT NOT NULL UNIQUE,
 configuration TEXT NOT NULL CHECK (length(configuration) <= 4096 AND json_valid(configuration)),
 created_at_ns INTEGER NOT NULL,
 retired_at_ns INTEGER CHECK (retired_at_ns IS NULL OR retired_at_ns >= created_at_ns),
 audit_expires_at_ns INTEGER NOT NULL
) STRICT;
CREATE TRIGGER resolver_configuration_insert_guard BEFORE INSERT ON resolver_configurations BEGIN
 SELECT CASE WHEN (SELECT count(*) FROM resolver_configurations) >= 256
 OR (SELECT count(*) FROM resolver_configurations WHERE retired_at_ns IS NULL) >= 16
 OR EXISTS (SELECT 1 FROM resolver_configurations WHERE id = NEW.id OR resolver_id = NEW.resolver_id OR query_id = NEW.query_id)
 OR NEW.retired_at_ns IS NOT NULL
 OR NOT EXISTS (SELECT 1 FROM network_scopes WHERE id = NEW.scope_id AND kind = 'lan' AND retired_at_ns IS NULL)
 THEN RAISE(ABORT, 'resolver configuration unavailable') END;
END;
CREATE TRIGGER resolver_configuration_update_guard BEFORE UPDATE ON resolver_configurations BEGIN
 SELECT CASE WHEN NEW.id != OLD.id OR NEW.scope_id != OLD.scope_id
 OR NEW.resolver_id != OLD.resolver_id OR NEW.query_id != OLD.query_id
 OR NEW.configuration != OLD.configuration OR NEW.created_at_ns != OLD.created_at_ns
 OR OLD.retired_at_ns IS NOT NULL OR NEW.retired_at_ns IS NULL
 THEN RAISE(ABORT, 'resolver configuration is immutable') END;
END;
CREATE TRIGGER resolver_configuration_delete_guard BEFORE DELETE ON resolver_configurations BEGIN
 SELECT RAISE(ABORT, 'resolver configuration is immutable');
END;
CREATE TRIGGER resolver_configuration_created AFTER INSERT ON resolver_configurations BEGIN
 INSERT INTO audit_events (id, kind, actor, occurred_at_ns, schema_version, payload, retention_class, expires_at_ns)
 VALUES ('audit.resolver-configuration.' || NEW.id || '.created', 'resolver-configuration', 'local-os-user', NEW.created_at_ns, 1,
 json_object('schema_version', 1, 'state', 'created', 'selection_id', NEW.id, 'resolver_id', NEW.resolver_id, 'query_id', NEW.query_id, 'scope_id', NEW.scope_id),
 'audit', NEW.audit_expires_at_ns);
END;
CREATE TRIGGER resolver_configuration_retired AFTER UPDATE ON resolver_configurations BEGIN
 INSERT INTO audit_events (id, kind, actor, occurred_at_ns, schema_version, payload, retention_class, expires_at_ns)
 VALUES ('audit.resolver-configuration.' || NEW.id || '.retired', 'resolver-configuration', 'local-os-user', NEW.retired_at_ns, 1,
 json_object('schema_version', 1, 'state', 'retired', 'selection_id', NEW.id, 'resolver_id', NEW.resolver_id, 'query_id', NEW.query_id, 'scope_id', NEW.scope_id),
 'audit', NEW.audit_expires_at_ns);
END;
`
