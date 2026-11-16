CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    period_type TEXT NOT NULL DEFAULT 'mingguan',
    hours_per_week REAL NOT NULL DEFAULT 32,
    work_days_json TEXT NOT NULL DEFAULT '["Sen","Sel","Rab","Kam","Jum"]',
    holidays_json TEXT NOT NULL DEFAULT '[]',
    baseline_minutes REAL NOT NULL DEFAULT 24,
    subscription_plan TEXT NOT NULL DEFAULT 'pilot',
    onboarding_done INTEGER NOT NULL DEFAULT 0,
    next_task_seq INTEGER NOT NULL DEFAULT 1,
    next_decision_seq INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS work_periods (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code TEXT NOT NULL,
    label TEXT NOT NULL,
    start_date TEXT NOT NULL,
    end_date TEXT NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    UNIQUE (organization_id, start_date, end_date)
);

CREATE TABLE IF NOT EXISTS recommendation_settings (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    preset TEXT NOT NULL DEFAULT 'seimbang',
    level_tolerance INTEGER NOT NULL DEFAULT 1,
    overload_at REAL NOT NULL DEFAULT 0.9,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS competencies (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL,
    is_archived INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
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
    weekly_limit REAL NOT NULL DEFAULT 32,
    photo_url TEXT NOT NULL DEFAULT '',
    access_role TEXT NOT NULL DEFAULT 'anggota',
    is_active INTEGER NOT NULL DEFAULT 1,
    invite_pending INTEGER NOT NULL DEFAULT 0,
    prefers_json TEXT NOT NULL DEFAULT '[]',
    avoids_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
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
    verified_at DATETIME NULL,
    PRIMARY KEY (organization_id, member_id, competency_id)
);

CREATE TABLE IF NOT EXISTS member_availabilities (
    organization_id TEXT NOT NULL,
    member_id TEXT NOT NULL,
    period_id TEXT NOT NULL REFERENCES work_periods(id) ON DELETE CASCADE,
    available_hours REAL NULL,
    weekly_limit REAL NOT NULL DEFAULT 32,
    leave_dates_json TEXT NOT NULL DEFAULT '[]',
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (organization_id, member_id, period_id)
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    member_id TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'manajer', 'anggota')),
    is_active INTEGER NOT NULL DEFAULT 1,
    session_version INTEGER NOT NULL DEFAULT 1,
    notify_prefs_json TEXT NOT NULL DEFAULT '{"assigned":true,"rejected":true,"reminder":false}',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (LOWER(email));

CREATE TABLE IF NOT EXISTS refresh_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    remember_me INTEGER NOT NULL DEFAULT 0,
    user_agent TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL
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
    expires_at DATETIME NOT NULL,
    accepted_at DATETIME NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS password_resets (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    client TEXT NOT NULL,
    due_date TEXT NOT NULL,
    is_archived INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
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
    remaining_hours REAL NOT NULL DEFAULT 0,
    actual_hours REAL NULL,
    response_reason TEXT NOT NULL DEFAULT '',
    legacy_competency_ids_json TEXT NOT NULL DEFAULT '[]',
    dna_json TEXT NULL,
    assignments_json TEXT NOT NULL DEFAULT '[]',
    flow_started_at DATETIME NULL,
    is_archived INTEGER NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS decisions (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    decided_by TEXT NOT NULL DEFAULT '',
    member_ids_json TEXT NOT NULL DEFAULT '[]',
    rank INTEGER NULL,
    rejected INTEGER NOT NULL DEFAULT 0,
    preset TEXT NOT NULL,
    reason_code TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    decided_at TEXT NOT NULL,
    decision_ms INTEGER NOT NULL DEFAULT 0,
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
    is_read INTEGER NOT NULL DEFAULT 0,
    read_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (organization_id, user_id, id)
);

CREATE TRIGGER IF NOT EXISTS trg_decisions_no_update BEFORE UPDATE ON decisions BEGIN
    SELECT RAISE(ABORT, 'decision log is immutable');
END;
CREATE TRIGGER IF NOT EXISTS trg_decisions_no_delete BEFORE DELETE ON decisions BEGIN
    SELECT RAISE(ABORT, 'decision log is immutable');
END;
