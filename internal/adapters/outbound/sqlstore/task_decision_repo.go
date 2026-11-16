package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

func scanTask(row interface{ Scan(...any) error }) (task.Task, error) {
	var t task.Task
	var prioStr, statusStr, legacyRaw, asgnsRaw string
	var actual sql.NullFloat64
	var dnaRaw sql.NullString
	var flowAt sql.NullTime

	err := row.Scan(
		&t.OrganizationID, &t.ID, &t.ProjectID, &t.PeriodID, &t.Title, &t.Description, &t.DueDate,
		&prioStr, &statusStr, &t.RemainingHours, &actual, &t.ResponseReason, &legacyRaw, &dnaRaw,
		&asgnsRaw, &flowAt, &t.IsArchived, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if isNoRows(err) {
			return task.Task{}, errs.NotFound("Tugas tidak ditemukan.")
		}
		return task.Task{}, errs.Internal("gagal membaca tugas", err)
	}
	t.Priority = task.Priority(prioStr)
	t.Status = task.Status(statusStr)
	if actual.Valid {
		v := actual.Float64
		t.ActualHours = &v
	}
	if flowAt.Valid {
		v := flowAt.Time
		t.FlowStartedAt = &v
	}
	_ = json.Unmarshal([]byte(legacyRaw), &t.CompetencyIDs)
	if t.CompetencyIDs == nil {
		t.CompetencyIDs = []string{}
	}
	_ = json.Unmarshal([]byte(asgnsRaw), &t.Assignments)
	if t.Assignments == nil {
		t.Assignments = []task.Assignment{}
	}
	if dnaRaw.Valid && dnaRaw.String != "" {
		var dna task.TaskDNA
		if jErr := json.Unmarshal([]byte(dnaRaw.String), &dna); jErr == nil {
			t.DNA = &dna
		}
	}
	return t, nil
}

const taskCols = `organization_id, id, project_id, period_id, title, description, due_date, priority, status, remaining_hours, actual_hours, response_reason, legacy_competency_ids_json, dna_json, assignments_json, flow_started_at, is_archived, created_by, created_at, updated_at`

func (r TaskRepo) List(ctx context.Context, orgID, periodID string, includeArchived bool) ([]task.Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE organization_id = $1`
	args := []any{orgID}
	if periodID != "" {
		q += ` AND (period_id = $2 OR status = 'selesai')`
		args = append(args, periodID)
	}
	if !includeArchived {
		q += ` AND is_archived = FALSE`
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]task.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r TaskRepo) Get(ctx context.Context, orgID, id string) (task.Task, error) {
	row := r.exec(ctx).QueryRowContext(ctx, r.q(`SELECT `+taskCols+` FROM tasks WHERE organization_id = $1 AND id = $2 AND is_archived = FALSE`), orgID, id)
	return scanTask(row)
}

func (r TaskRepo) GetForUpdate(ctx context.Context, orgID, id string) (task.Task, error) {
	row := r.exec(ctx).QueryRowContext(ctx, r.q(`SELECT `+taskCols+` FROM tasks WHERE organization_id = $1 AND id = $2 AND is_archived = FALSE`+r.forUpdate()), orgID, id)
	return scanTask(row)
}

func (r TaskRepo) NextTaskCode(ctx context.Context, orgID string) (string, error) {
	ex := r.exec(ctx)
	if _, err := ex.ExecContext(ctx, r.q(`UPDATE organizations SET next_task_seq = next_task_seq + 1 WHERE id = $1`), orgID); err != nil {
		return "", err
	}
	var seq int
	if err := ex.QueryRowContext(ctx, r.q(`SELECT next_task_seq - 1 FROM organizations WHERE id = $1`), orgID).Scan(&seq); err != nil {
		return "", err
	}
	return fmt.Sprintf("TGS-%04d", seq), nil
}

func marshalTaskFields(t task.Task) (legacyRaw, asgnsRaw string, dnaVal sql.NullString, actualVal sql.NullFloat64, flowVal sql.NullTime) {
	b1, _ := json.Marshal(t.CompetencyIDs)
	b2, _ := json.Marshal(t.Assignments)
	legacyRaw, asgnsRaw = string(b1), string(b2)
	if t.DNA != nil {
		if b, err := json.Marshal(t.DNA); err == nil {
			dnaVal = sql.NullString{String: string(b), Valid: true}
		}
	}
	if t.ActualHours != nil {
		actualVal = sql.NullFloat64{Float64: *t.ActualHours, Valid: true}
	}
	if t.FlowStartedAt != nil {
		flowVal = sql.NullTime{Time: *t.FlowStartedAt, Valid: true}
	}
	return
}

func (r TaskRepo) Create(ctx context.Context, t task.Task) error {
	legacyRaw, asgnsRaw, dnaVal, actualVal, flowVal := marshalTaskFields(t)
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO tasks (organization_id, id, project_id, period_id, title, description, due_date, priority, status, remaining_hours, actual_hours, response_reason, legacy_competency_ids_json, dna_json, assignments_json, flow_started_at, is_archived, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`), t.OrganizationID, t.ID, t.ProjectID, t.PeriodID, t.Title, t.Description, t.DueDate, string(t.Priority), string(t.Status), t.RemainingHours, actualVal, t.ResponseReason, legacyRaw, dnaVal, asgnsRaw, flowVal, t.IsArchived, t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	return err
}

func (r TaskRepo) Update(ctx context.Context, t task.Task) error {
	legacyRaw, asgnsRaw, dnaVal, actualVal, flowVal := marshalTaskFields(t)
	res, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE tasks SET project_id=$1, period_id=$2, title=$3, description=$4, due_date=$5, priority=$6, status=$7, remaining_hours=$8, actual_hours=$9, response_reason=$10, legacy_competency_ids_json=$11, dna_json=$12, assignments_json=$13, flow_started_at=COALESCE(flow_started_at, $14), is_archived=$15, updated_at=$16
		WHERE organization_id=$17 AND id=$18
	`), t.ProjectID, t.PeriodID, t.Title, t.Description, t.DueDate, string(t.Priority), string(t.Status), t.RemainingHours, actualVal, t.ResponseReason, legacyRaw, dnaVal, asgnsRaw, flowVal, t.IsArchived, t.UpdatedAt, t.OrganizationID, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("Tugas tidak ditemukan.")
	}
	return nil
}

func (r TaskRepo) DeleteOrArchive(ctx context.Context, orgID, id string) error {
	var decCount int
	_ = r.exec(ctx).QueryRowContext(ctx, r.q(`SELECT COUNT(1) FROM decisions WHERE organization_id = $1 AND task_id = $2`), orgID, id).Scan(&decCount)
	if decCount > 0 {
		_, err := r.exec(ctx).ExecContext(ctx, r.q(`UPDATE tasks SET is_archived = TRUE, updated_at = $1 WHERE organization_id = $2 AND id = $3`), time.Now().UTC(), orgID, id)
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`DELETE FROM tasks WHERE organization_id = $1 AND id = $2`), orgID, id)
	return err
}

func (r TaskRepo) SetFlowStartedIfEmpty(ctx context.Context, orgID, id string, at time.Time) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE tasks SET flow_started_at = $1 WHERE organization_id = $2 AND id = $3 AND flow_started_at IS NULL
	`), at, orgID, id)
	return err
}

// --- DecisionRepo (append-only audit) ---

func scanDecisions(rows *sql.Rows) ([]decision.Decision, error) {
	out := make([]decision.Decision, 0)
	for rows.Next() {
		var d decision.Decision
		var midsRaw, presetStr string
		var rank sql.NullInt64
		if err := rows.Scan(&d.OrganizationID, &d.ID, &d.TaskID, &d.DecidedBy, &midsRaw, &rank, &d.Rejected, &presetStr, &d.ReasonCode, &d.Note, &d.DecidedAt, &d.DecisionMs, &d.ConflictsSeen); err != nil {
			return nil, err
		}
		d.Preset = workspace.PresetID(presetStr)
		if rank.Valid {
			v := int(rank.Int64)
			d.Rank = &v
		}
		_ = json.Unmarshal([]byte(midsRaw), &d.MemberIDs)
		if d.MemberIDs == nil {
			d.MemberIDs = []string{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r DecisionRepo) List(ctx context.Context, orgID string) ([]decision.Decision, error) {
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(`
		SELECT organization_id, id, task_id, decided_by, member_ids_json, rank, rejected, preset, reason_code, note, decided_at, decision_ms, conflicts_seen
		FROM decisions WHERE organization_id = $1 ORDER BY decided_at DESC, id DESC
	`), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDecisions(rows)
}

func (r DecisionRepo) ListByTask(ctx context.Context, orgID, taskID string) ([]decision.Decision, error) {
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(`
		SELECT organization_id, id, task_id, decided_by, member_ids_json, rank, rejected, preset, reason_code, note, decided_at, decision_ms, conflicts_seen
		FROM decisions WHERE organization_id = $1 AND task_id = $2 ORDER BY decided_at DESC, id DESC
	`), orgID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDecisions(rows)
}

func (r DecisionRepo) NextDecisionCode(ctx context.Context, orgID string) (string, error) {
	ex := r.exec(ctx)
	if _, err := ex.ExecContext(ctx, r.q(`UPDATE organizations SET next_decision_seq = next_decision_seq + 1 WHERE id = $1`), orgID); err != nil {
		return "", err
	}
	var seq int
	if err := ex.QueryRowContext(ctx, r.q(`SELECT next_decision_seq - 1 FROM organizations WHERE id = $1`), orgID).Scan(&seq); err != nil {
		return "", err
	}
	return fmt.Sprintf("KPT-%03d", seq), nil
}

func (r DecisionRepo) Create(ctx context.Context, d decision.Decision) error {
	midsRaw, _ := json.Marshal(d.MemberIDs)
	var rankVal sql.NullInt64
	if d.Rank != nil {
		rankVal = sql.NullInt64{Int64: int64(*d.Rank), Valid: true}
	}
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO decisions (organization_id, id, task_id, decided_by, member_ids_json, rank, rejected, preset, reason_code, note, decided_at, decision_ms, conflicts_seen)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`), d.OrganizationID, d.ID, d.TaskID, d.DecidedBy, string(midsRaw), rankVal, d.Rejected, string(d.Preset), d.ReasonCode, d.Note, d.DecidedAt, d.DecisionMs, d.ConflictsSeen)
	return err
}

// --- NotificationRepo ---

func (r NotificationRepo) ListByUser(ctx context.Context, orgID, userID string) ([]notification.Notice, error) {
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(`
		SELECT id, organization_id, user_id, title, subtitle, href, kind, is_read, read_at, created_at
		FROM notifications WHERE organization_id = $1 AND user_id = $2 ORDER BY created_at DESC
	`), orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]notification.Notice, 0)
	for rows.Next() {
		var n notification.Notice
		var kindStr string
		var readAt sql.NullTime
		if err := rows.Scan(&n.ID, &n.OrganizationID, &n.UserID, &n.Title, &n.Subtitle, &n.Href, &kindStr, &n.Read, &readAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Kind = notification.Kind(kindStr)
		if readAt.Valid {
			t := readAt.Time
			n.ReadAt = &t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r NotificationRepo) Upsert(ctx context.Context, n notification.Notice) error {
	var readAt sql.NullTime
	if n.ReadAt != nil {
		readAt = sql.NullTime{Time: *n.ReadAt, Valid: true}
	}
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO notifications (organization_id, user_id, id, title, subtitle, href, kind, is_read, read_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (organization_id, user_id, id) DO UPDATE SET
			title = EXCLUDED.title,
			subtitle = EXCLUDED.subtitle,
			href = EXCLUDED.href,
			is_read = CASE WHEN notifications.is_read THEN TRUE ELSE EXCLUDED.is_read END,
			read_at = COALESCE(notifications.read_at, EXCLUDED.read_at)
	`), n.OrganizationID, n.UserID, n.ID, n.Title, n.Subtitle, n.Href, string(n.Kind), n.Read, readAt, n.CreatedAt)
	return err
}

func (r NotificationRepo) MarkRead(ctx context.Context, orgID, userID, id string, at time.Time) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE notifications SET is_read = TRUE, read_at = COALESCE(read_at, $1)
		WHERE organization_id = $2 AND user_id = $3 AND id = $4
	`), at, orgID, userID, id)
	return err
}

func (r NotificationRepo) MarkAllRead(ctx context.Context, orgID, userID string, at time.Time) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE notifications SET is_read = TRUE, read_at = COALESCE(read_at, $1)
		WHERE organization_id = $2 AND user_id = $3 AND is_read = FALSE
	`), at, orgID, userID)
	return err
}
