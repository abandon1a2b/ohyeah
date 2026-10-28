CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    workspace TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS data_types (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    args_json TEXT NOT NULL DEFAULT '[]',
    revision INTEGER NOT NULL,
    config_hash TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(project_id, name)
);
CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL DEFAULT '',
    project_workspace TEXT NOT NULL DEFAULT '',
    type_id TEXT NOT NULL DEFAULT '',
    mount_name TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL,
    type TEXT NOT NULL,
    path TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    managed_by TEXT NOT NULL DEFAULT 'cli',
    state TEXT NOT NULL DEFAULT 'active',
    type_config_hash TEXT NOT NULL DEFAULT '',
    mount_config_hash TEXT NOT NULL DEFAULT '',
    type_revision INTEGER NOT NULL DEFAULT 0,
    mount_revision INTEGER NOT NULL DEFAULT 0,
    options_json TEXT NOT NULL DEFAULT '{}',
    execution_json TEXT NOT NULL DEFAULT '{}',
    writer TEXT NOT NULL DEFAULT '',
    cursor_json TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS source_objects (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    parser_version INTEGER NOT NULL,
    reference_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    UNIQUE(source_id, external_id)
);
CREATE TABLE IF NOT EXISTS memory_units (
    id TEXT PRIMARY KEY,
    object_id TEXT NOT NULL REFERENCES source_objects(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL DEFAULT '',
    type_id TEXT NOT NULL DEFAULT '',
    mount_id TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    thread_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    heading TEXT NOT NULL DEFAULT '',
    occurred_at TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    requirement_ids_json TEXT NOT NULL DEFAULT '[]',
    repos_json TEXT NOT NULL DEFAULT '[]',
    entities_json TEXT NOT NULL DEFAULT '[]',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_memory_units_source ON memory_units(source_id);
CREATE INDEX IF NOT EXISTS idx_memory_units_thread ON memory_units(thread_id);
CREATE INDEX IF NOT EXISTS idx_memory_units_workspace ON memory_units(workspace_id);
CREATE TABLE IF NOT EXISTS relations (
    id TEXT PRIMARY KEY,
    owner_object_id TEXT NOT NULL REFERENCES source_objects(id) ON DELETE CASCADE,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    type TEXT NOT NULL,
    confidence REAL NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_relations_owner ON relations(owner_object_id);
CREATE INDEX IF NOT EXISTS idx_relations_to ON relations(to_id, type);
CREATE TABLE IF NOT EXISTS sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id TEXT NOT NULL,
    status TEXT NOT NULL,
    scanned INTEGER NOT NULL DEFAULT 0,
    changed INTEGER NOT NULL DEFAULT 0,
    deleted INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_sync_runs_source_id ON sync_runs(source_id, id DESC);
CREATE TABLE IF NOT EXISTS index_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    operation TEXT NOT NULL,
    unit_id TEXT NOT NULL,
    payload BLOB,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    delivered_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON index_outbox(delivered_at, id);
CREATE INDEX IF NOT EXISTS idx_sync_runs_finished ON sync_runs(finished_at);

CREATE TABLE IF NOT EXISTS auth_credentials (
 id TEXT PRIMARY KEY, token_hash TEXT UNIQUE NOT NULL, subject TEXT NOT NULL, issuer TEXT NOT NULL,
 kind TEXT NOT NULL, name TEXT NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS auth_flows (
 key TEXT PRIMARY KEY, kind TEXT NOT NULL, payload TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auth_flows_expiry ON auth_flows(expires_at);

CREATE TABLE IF NOT EXISTS upload_state(source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE, version INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS upload_batches(source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE, batch_id TEXT NOT NULL, request_hash TEXT NOT NULL, receipt_json TEXT NOT NULL, PRIMARY KEY(source_id,batch_id));
CREATE TABLE IF NOT EXISTS audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at INTEGER NOT NULL, issuer TEXT NOT NULL, subject TEXT NOT NULL, credential_id TEXT NOT NULL, actor_kind TEXT NOT NULL DEFAULT 'principal', action TEXT NOT NULL, resource TEXT NOT NULL, status INTEGER NOT NULL, details TEXT NOT NULL DEFAULT '{}');
CREATE INDEX IF NOT EXISTS idx_audit_subject ON audit_events(issuer,subject,id DESC);
CREATE TABLE IF NOT EXISTS auth_principals(issuer TEXT NOT NULL, subject TEXT NOT NULL, policy_json TEXT NOT NULL, version INTEGER NOT NULL, PRIMARY KEY(issuer,subject));
CREATE INDEX IF NOT EXISTS idx_audit_issuer_id ON audit_events(issuer,id DESC);
CREATE TABLE IF NOT EXISTS schema_versions (
    component TEXT PRIMARY KEY,
    version INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO schema_versions(component, version, updated_at)
VALUES ('store', 9, CURRENT_TIMESTAMP)
ON CONFLICT(component) DO UPDATE SET version=excluded.version, updated_at=excluded.updated_at;
PRAGMA user_version=9;
