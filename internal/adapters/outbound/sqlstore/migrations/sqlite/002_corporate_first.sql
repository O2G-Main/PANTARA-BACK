CREATE TABLE IF NOT EXISTS hr_settings (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    weekly_hours REAL NOT NULL DEFAULT 40,
    reserve_hours REAL NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS performance_configs (
    organization_id TEXT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    automatic_weight REAL NOT NULL DEFAULT 50,
    criteria_json TEXT NOT NULL DEFAULT '[]',
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS organization_divisions (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (organization_id, name)
);

CREATE TABLE IF NOT EXISTS hr_requests (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    id TEXT NOT NULL,
    sender TEXT NOT NULL,
    sender_role TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    is_handled INTEGER NOT NULL DEFAULT 0,
    is_read INTEGER NOT NULL DEFAULT 0,
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
    automatic_weight REAL NOT NULL,
    automatic_score REAL NULL,
    on_time_score REAL NULL,
    quality_score REAL NULL,
    criteria_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL,
    PRIMARY KEY (organization_id, id),
    UNIQUE (organization_id, member_id, period)
);

CREATE INDEX IF NOT EXISTS idx_hr_requests_org_created ON hr_requests (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_perf_reviews_org_period ON performance_reviews (organization_id, period, member_id);
