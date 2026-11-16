CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    period_type TEXT NOT NULL DEFAULT 'mingguan',
    hours_per_week DOUBLE PRECISION NOT NULL DEFAULT 32,
    work_days_json TEXT NOT NULL DEFAULT '["Sen","Sel","Rab","Kam","Jum"]',
    holidays_json TEXT NOT NULL DEFAULT '[]',
    baseline_minutes DOUBLE PRECISION NOT NULL DEFAULT 24,
    subscription_plan TEXT NOT NULL DEFAULT 'pilot',
    onboarding_done BOOLEAN NOT NULL DEFAULT FALSE,
    next_task_seq INTEGER NOT NULL DEFAULT 1,
    next_decision_seq INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS work_periods (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    label TEXT NOT NULL,
    start_date TEXT NOT NULL,
    end_date TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (organization_id, start_date, end_date)
);

CREATE TABLE IF NOT EXISTS recommendation_settings (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    preset TEXT NOT NULL DEFAULT 'seimbang',
    level_tolerance INTEGER NOT NULL DEFAULT 1,
    overload_at DOUBLE PRECISION NOT NULL DEFAULT 0.9,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS competencies (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS members (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    initials TEXT NOT NULL,
    role_title TEXT NOT NULL,
    email TEXT NOT NULL,
    weekly_limit DOUBLE PRECISION NOT NULL DEFAULT 32,
    photo_url TEXT NOT NULL DEFAULT '',
    access_role TEXT NOT NULL DEFAULT 'anggota',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    invite_pending BOOLEAN NOT NULL DEFAULT FALSE,
    prefers_json TEXT NOT NULL DEFAULT '[]',
    avoids_json TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, id),
    UNIQUE (organization_id, email)
);

CREATE TABLE IF NOT EXISTS member_competencies (
    organization_id TEXT NOT NULL,
    member_id TEXT NOT NULL,
    competency_id TEXT NOT NULL,
    lvl INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT 'klaim',
    verified_by TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ NULL,
    PRIMARY KEY (organization_id, member_id, competency_id),
    FOREIGN KEY (organization_id, member_id) REFERENCES members(organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, competency_id) REFERENCES competencies(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS member_availabilities (
    organization_id TEXT NOT NULL,
    member_id TEXT NOT NULL,
    period_id TEXT NOT NULL REFERENCES work_periods(id) ON DELETE CASCADE,
    available_hours DOUBLE PRECISION NULL,
    weekly_limit DOUBLE PRECISION NOT NULL DEFAULT 32,
    leave_dates_json TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, member_id, period_id),
    FOREIGN KEY (organization_id, member_id) REFERENCES members(organization_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    member_id TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'manajer', 'anggota')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    session_version BIGINT NOT NULL DEFAULT 1,
    notify_prefs_json TEXT NOT NULL DEFAULT '{"assigned":true,"rejected":true,"reminder":false}',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (LOWER(email));

CREATE TABLE IF NOT EXISTS refresh_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    remember_me BOOLEAN NOT NULL DEFAULT FALSE,
    user_agent TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS invitation_tokens (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    organization_name TEXT NOT NULL,
    member_id TEXT NOT NULL,
    email TEXT NOT NULL,
    name TEXT NOT NULL,
    role_title TEXT NOT NULL,
    access_role TEXT NOT NULL DEFAULT 'anggota',
    token_hash TEXT NOT NULL UNIQUE,
    invited_by_user_id TEXT NOT NULL,
    invited_by_name TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS password_resets (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    client TEXT NOT NULL,
    due_date TEXT NOT NULL,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS tasks (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    period_id TEXT NOT NULL REFERENCES work_periods(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    due_date TEXT NOT NULL,
    priority TEXT NOT NULL DEFAULT 'normal',
    status TEXT NOT NULL DEFAULT 'belum',
    remaining_hours DOUBLE PRECISION NOT NULL DEFAULT 0,
    actual_hours DOUBLE PRECISION NULL,
    response_reason TEXT NOT NULL DEFAULT '',
    legacy_competency_ids_json TEXT NOT NULL DEFAULT '[]',
    dna_json TEXT NULL,
    assignments_json TEXT NOT NULL DEFAULT '[]',
    flow_started_at TIMESTAMPTZ NULL,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, id),
    FOREIGN KEY (organization_id, project_id) REFERENCES projects(organization_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS decisions (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    decided_by TEXT NOT NULL DEFAULT '',
    member_ids_json TEXT NOT NULL DEFAULT '[]',
    rank INTEGER NULL,
    rejected BOOLEAN NOT NULL DEFAULT FALSE,
    preset TEXT NOT NULL,
    reason_code TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    decided_at TEXT NOT NULL,
    decision_ms BIGINT NOT NULL DEFAULT 0,
    conflicts_seen INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS notifications (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    title TEXT NOT NULL,
    subtitle TEXT NOT NULL DEFAULT '',
    href TEXT NOT NULL DEFAULT '/notifikasi',
    kind TEXT NOT NULL DEFAULT 'operational',
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    read_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, user_id, id)
);

CREATE INDEX IF NOT EXISTS idx_tasks_org_period_status ON tasks (organization_id, period_id, status, due_date);
CREATE INDEX IF NOT EXISTS idx_decisions_org_decided ON decisions (organization_id, decided_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_org_task ON decisions (organization_id, task_id);
CREATE INDEX IF NOT EXISTS idx_notifs_user_created ON notifications (organization_id, user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_periods_org_start ON work_periods (organization_id, start_date DESC);

CREATE OR REPLACE FUNCTION prevent_decision_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'decision log is immutable (append-only audit)';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_decisions_immutable ON decisions;
CREATE TRIGGER trg_decisions_immutable
BEFORE UPDATE OR DELETE ON decisions
FOR EACH ROW EXECUTE FUNCTION prevent_decision_mutation();
