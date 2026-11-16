package sqlstore

import (
	"context"
	"testing"
)

func TestDecisionImmutabilityTrigger(t *testing.T) {
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO organizations (id, slug, name, created_at, updated_at) VALUES ('org1', 'org1', 'Org 1', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z');
		INSERT INTO decisions (
			organization_id, id, task_id, decided_by, member_ids_json, rank, rejected, preset,
			reason_code, note, decided_at, decision_ms, conflicts_seen
		) VALUES (
			'org1', 'KPT-0001', 'T1', 'u1', '["ayu"]', 1, 0, 'seimbang',
			'', 'Test', '2026-10-01T00:00:00Z', 1200, 0
		);
	`)
	if err != nil {
		t.Fatalf("insert decision: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE decisions SET rejected = 1 WHERE id = 'KPT-0001'`); err == nil {
		t.Fatalf("expected SQLite trigger to block UPDATE on decisions")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM decisions WHERE id = 'KPT-0001'`); err == nil {
		t.Fatalf("expected SQLite trigger to block DELETE on decisions")
	}
}
