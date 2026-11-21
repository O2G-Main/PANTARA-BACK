package sqlstore

import (
	"context"
	"testing"

	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/security"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
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

func TestUpgradeExistingArunikaSeed(t *testing.T) {
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	deps := seeddata.SeedDeps{
		Store:    s,
		Members:  MemberRepo{Store: s},
		Projects: ProjectRepo{Store: s},
		Tasks:    TaskRepo{Store: s},
		Decs:     DecisionRepo{Store: s},
		Notifs:   NotificationRepo{Store: s},
		HR:       HRRepo{Store: s},
		Hasher:   security.Argon2Hasher{},
		Clock:    security.RealClock{},
	}

	seeded, err := seeddata.SeedArunika(ctx, deps, seeddata.SeedOptions{FixedDates: true})
	if err != nil || !seeded {
		t.Fatalf("initial seed failed: seeded=%v err=%v", seeded, err)
	}

	// Simulate an older deployed org-arunika DB missing Corporate-First & HR records.
	for _, stmt := range []string{
		`DELETE FROM users WHERE email = 'hr@pantara.id'`,
		`UPDATE members SET employment_type = 'karyawan' WHERE id IN ('hana', 'wulan')`,
		`UPDATE members SET leave_requests_json = '[]' WHERE id = 'fikri'`,
		`DELETE FROM tasks WHERE id = 'TGS-0174'`,
		`UPDATE tasks SET extra_time_json = NULL WHERE id = 'TGS-0164'`,
		`UPDATE tasks SET completed_at = '', quality_score = NULL WHERE id = 'TGS-0128'`,
		`UPDATE organizations SET next_task_seq = 173 WHERE id = 'org-arunika'`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("simulate old db (%s): %v", stmt, err)
		}
	}

	upgraded, err := seeddata.UpgradeArunikaIfPresent(ctx, deps)
	if err != nil || !upgraded {
		t.Fatalf("expected UpgradeArunikaIfPresent to apply changes: upgraded=%v err=%v", upgraded, err)
	}
	upgradedAgain, err := seeddata.UpgradeArunikaIfPresent(ctx, deps)
	if err != nil || upgradedAgain {
		t.Fatalf("expected second UpgradeArunikaIfPresent call to be idempotent no-op: upgraded=%v err=%v", upgradedAgain, err)
	}

	u, err := s.GetUserByEmail(ctx, "hr@pantara.id")
	if err != nil || u.Role != auth.RoleHR {
		t.Fatalf("expected hr@pantara.id restored with role hr: u=%+v err=%v", u, err)
	}
	hana, err := MemberRepo{Store: s}.Get(ctx, seeddata.ArunikaOrgID, seeddata.ActivePeriodID, "hana")
	if err != nil || hana.EmploymentType != member.EmploymentFreelancer {
		t.Fatalf("expected hana backfilled to freelancer: got=%+v err=%v", hana, err)
	}
	fikri, err := MemberRepo{Store: s}.Get(ctx, seeddata.ArunikaOrgID, seeddata.ActivePeriodID, "fikri")
	if err != nil || len(fikri.LeaveRequests) != 1 || fikri.LeaveRequests[0].ID != "CUTI-fikri-1" {
		t.Fatalf("expected fikri CUTI-fikri-1 restored: got=%+v err=%v", fikri, err)
	}
	t174, err := TaskRepo{Store: s}.Get(ctx, seeddata.ArunikaOrgID, "TGS-0174")
	if err != nil || t174.ExternalSource == "" {
		t.Fatalf("expected TGS-0174 restored: got=%+v err=%v", t174, err)
	}
	t164, err := TaskRepo{Store: s}.Get(ctx, seeddata.ArunikaOrgID, "TGS-0164")
	if err != nil || t164.ExtraTime == nil || t164.ExtraTime.Hours != 3 {
		t.Fatalf("expected TGS-0164 extraTime restored: got=%+v err=%v", t164, err)
	}
	t128, err := TaskRepo{Store: s}.Get(ctx, seeddata.ArunikaOrgID, "TGS-0128")
	if err != nil || t128.CompletedAt == "" || t128.QualityScore == nil || *t128.QualityScore != 80 {
		t.Fatalf("expected TGS-0128 completedAt & qualityScore restored: got=%+v err=%v", t128, err)
	}
}
