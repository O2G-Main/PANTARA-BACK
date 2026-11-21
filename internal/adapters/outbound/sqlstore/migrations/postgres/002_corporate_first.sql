ALTER TABLE recommendation_settings ADD COLUMN IF NOT EXISTS reserve_ratio DOUBLE PRECISION NOT NULL DEFAULT 0;

ALTER TABLE members ADD COLUMN IF NOT EXISTS employment_type TEXT NOT NULL DEFAULT 'karyawan';
ALTER TABLE members ADD COLUMN IF NOT EXISTS team TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN IF NOT EXISTS division TEXT NOT NULL DEFAULT '';
ALTER TABLE members ADD COLUMN IF NOT EXISTS work_hours_override DOUBLE PRECISION NULL;
ALTER TABLE members ADD COLUMN IF NOT EXISTS reserve_hours_override DOUBLE PRECISION NULL;
ALTER TABLE members ADD COLUMN IF NOT EXISTS leave_requests_json TEXT NOT NULL DEFAULT '[]';

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS due_time TEXT NOT NULL DEFAULT '17:00';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS completed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS quality_score DOUBLE PRECISION NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS external_source TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS extra_time_json TEXT NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS result_note TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS change_request_json TEXT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        JOIN pg_class t ON c.conrelid = t.oid
        WHERE t.relname = 'users' AND c.conname = 'users_role_check'
          AND pg_get_constraintdef(c.oid) LIKE '%hr%'
    ) THEN
        ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
        ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'manajer', 'anggota', 'hr'));
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS hr_settings (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    weekly_hours DOUBLE PRECISION NOT NULL DEFAULT 40,
    reserve_hours DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS performance_configs (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    automatic_weight DOUBLE PRECISION NOT NULL DEFAULT 50,
    criteria_json TEXT NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS organization_divisions (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, name)
);

CREATE TABLE IF NOT EXISTS hr_requests (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    sender TEXT NOT NULL,
    sender_role TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    is_handled BOOLEAN NOT NULL DEFAULT FALSE,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (organization_id, id)
);

CREATE TABLE IF NOT EXISTS performance_reviews (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    member_id TEXT NOT NULL,
    period TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL,
    automatic_weight DOUBLE PRECISION NOT NULL,
    automatic_score DOUBLE PRECISION NULL,
    on_time_score DOUBLE PRECISION NULL,
    quality_score DOUBLE PRECISION NULL,
    criteria_json TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (organization_id, id),
    UNIQUE (organization_id, member_id, period)
);

CREATE INDEX IF NOT EXISTS idx_hr_requests_org_created ON hr_requests (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_perf_reviews_org_period ON performance_reviews (organization_id, period, member_id);
