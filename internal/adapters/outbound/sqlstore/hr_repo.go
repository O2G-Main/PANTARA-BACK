package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// HRRepo implements port.HRRepository over Postgres and SQLite.
type HRRepo struct{ *Store }

func (r HRRepo) GetHRSettings(ctx context.Context, orgID string) (workspace.HRSettings, error) {
	var s workspace.HRSettings
	err := r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT weekly_hours, reserve_hours FROM hr_settings WHERE organization_id = $1
	`), orgID).Scan(&s.WeeklyHours, &s.ReserveHours)
	if err != nil {
		if isNoRows(err) {
			return workspace.DefaultHRSettings(), nil
		}
		return s, err
	}
	return s, nil
}

func (r HRRepo) SaveHRSettings(ctx context.Context, orgID string, s workspace.HRSettings) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO hr_settings (organization_id, weekly_hours, reserve_hours, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id) DO UPDATE SET
			weekly_hours = EXCLUDED.weekly_hours,
			reserve_hours = EXCLUDED.reserve_hours,
			updated_at = EXCLUDED.updated_at
	`), orgID, s.WeeklyHours, s.ReserveHours, time.Now().UTC())
	return err
}

func (r HRRepo) GetPerformanceConfig(ctx context.Context, orgID string) (workspace.PerformanceConfig, error) {
	var cfg workspace.PerformanceConfig
	var raw string
	err := r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT automatic_weight, criteria_json FROM performance_configs WHERE organization_id = $1
	`), orgID).Scan(&cfg.AutomaticWeight, &raw)
	if err != nil {
		if isNoRows(err) {
			return workspace.DefaultPerformanceConfig(), nil
		}
		return cfg, err
	}
	_ = json.Unmarshal([]byte(raw), &cfg.Criteria)
	if cfg.Criteria == nil {
		cfg.Criteria = []workspace.PerformanceCriterion{}
	}
	return cfg, nil
}

func (r HRRepo) SavePerformanceConfig(ctx context.Context, orgID string, cfg workspace.PerformanceConfig) error {
	raw, _ := json.Marshal(cfg.Criteria)
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO performance_configs (organization_id, automatic_weight, criteria_json, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id) DO UPDATE SET
			automatic_weight = EXCLUDED.automatic_weight,
			criteria_json = EXCLUDED.criteria_json,
			updated_at = EXCLUDED.updated_at
	`), orgID, cfg.AutomaticWeight, string(raw), time.Now().UTC())
	return err
}

func (r HRRepo) ListDivisions(ctx context.Context, orgID string) ([]string, error) {
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(`
		SELECT name FROM organization_divisions WHERE organization_id = $1
		UNION
		SELECT DISTINCT TRIM(division) AS name FROM members WHERE organization_id = $2 AND TRIM(division) <> ''
		ORDER BY name ASC
	`), orgID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (r HRRepo) CreateDivision(ctx context.Context, orgID, name string) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO organization_divisions (organization_id, name, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, name) DO NOTHING
	`), orgID, name, time.Now().UTC())
	return err
}

func (r HRRepo) RenameDivision(ctx context.Context, orgID, oldName, newName string) error {
	return r.WithinTx(ctx, func(txCtx context.Context) error {
		ex := r.exec(txCtx)
		now := time.Now().UTC()
		if _, err := ex.ExecContext(txCtx, r.q(`
			DELETE FROM organization_divisions WHERE organization_id = $1 AND LOWER(name) = LOWER($2)
		`), orgID, oldName); err != nil {
			return err
		}
		if _, err := ex.ExecContext(txCtx, r.q(`
			INSERT INTO organization_divisions (organization_id, name, created_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, name) DO NOTHING
		`), orgID, newName, now); err != nil {
			return err
		}
		if _, err := ex.ExecContext(txCtx, r.q(`
			UPDATE members SET division = $1, updated_at = $2
			WHERE organization_id = $3 AND LOWER(TRIM(division)) = LOWER(TRIM($4))
		`), newName, now, orgID, oldName); err != nil {
			return err
		}
		return nil
	})
}

func (r HRRepo) PlaceMembers(ctx context.Context, orgID string, memberIDs []string, division string, team *string) error {
	return r.WithinTx(ctx, func(txCtx context.Context) error {
		ex := r.exec(txCtx)
		now := time.Now().UTC()
		if strings.TrimSpace(division) != "" {
			if _, err := ex.ExecContext(txCtx, r.q(`
				INSERT INTO organization_divisions (organization_id, name, created_at)
				VALUES ($1, $2, $3)
				ON CONFLICT (organization_id, name) DO NOTHING
			`), orgID, strings.TrimSpace(division), now); err != nil {
				return err
			}
		}
		for _, id := range memberIDs {
			if team != nil {
				if _, err := ex.ExecContext(txCtx, r.q(`
					UPDATE members SET division = $1, team = $2, updated_at = $3
					WHERE organization_id = $4 AND id = $5
				`), division, *team, now, orgID, id); err != nil {
					return err
				}
			} else {
				if _, err := ex.ExecContext(txCtx, r.q(`
					UPDATE members SET division = $1, updated_at = $2
					WHERE organization_id = $3 AND id = $4
				`), division, now, orgID, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (r HRRepo) ListHRRequests(ctx context.Context, orgID, statusFilter string) ([]workspace.HRRequest, error) {
	q := `SELECT organization_id, id, sender, sender_role, title, description, is_handled, is_read, created_at
		FROM hr_requests WHERE organization_id = $1`
	switch statusFilter {
	case "belum":
		q += ` AND is_handled = FALSE`
		if r.dialect == DialectSQLite {
			q = strings.ReplaceAll(q, "is_handled = FALSE", "is_handled = 0")
		}
	case "selesai":
		q += ` AND is_handled = TRUE`
		if r.dialect == DialectSQLite {
			q = strings.ReplaceAll(q, "is_handled = TRUE", "is_handled = 1")
		}
	}
	q += ` ORDER BY created_at DESC, id DESC`

	rows, err := r.exec(ctx).QueryContext(ctx, r.q(q), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workspace.HRRequest, 0)
	for rows.Next() {
		var req workspace.HRRequest
		if err := rows.Scan(&req.OrganizationID, &req.ID, &req.Sender, &req.SenderRole, &req.Title, &req.Description, &req.Handled, &req.Read, &req.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func (r HRRepo) GetHRRequest(ctx context.Context, orgID, id string) (workspace.HRRequest, error) {
	var req workspace.HRRequest
	err := r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT organization_id, id, sender, sender_role, title, description, is_handled, is_read, created_at
		FROM hr_requests WHERE organization_id = $1 AND id = $2
	`), orgID, id).Scan(&req.OrganizationID, &req.ID, &req.Sender, &req.SenderRole, &req.Title, &req.Description, &req.Handled, &req.Read, &req.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return workspace.HRRequest{}, errs.NotFound("Pengajuan HR tidak ditemukan.")
		}
		return workspace.HRRequest{}, err
	}
	return req, nil
}

func (r HRRepo) CreateHRRequest(ctx context.Context, req workspace.HRRequest) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO hr_requests (organization_id, id, sender, sender_role, title, description, is_handled, is_read, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`), req.OrganizationID, req.ID, req.Sender, req.SenderRole, req.Title, req.Description, req.Handled, req.Read, req.CreatedAt)
	return err
}

func (r HRRepo) UpdateHRRequest(ctx context.Context, req workspace.HRRequest) error {
	res, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE hr_requests SET is_handled = $1, is_read = $2
		WHERE organization_id = $3 AND id = $4
	`), req.Handled, req.Read, req.OrganizationID, req.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("Pengajuan HR tidak ditemukan.")
	}
	return nil
}

func (r HRRepo) ListPerformanceReviews(ctx context.Context, orgID, period, memberID string) ([]workspace.PerformanceReview, error) {
	q := `SELECT organization_id, id, member_id, period, note, author, automatic_weight, automatic_score, on_time_score, quality_score, criteria_json, created_at
		FROM performance_reviews WHERE organization_id = $1`
	args := []any{orgID}
	if period != "" {
		args = append(args, period)
		q += fmt.Sprintf(" AND period = $%d", len(args))
	}
	if memberID != "" {
		args = append(args, memberID)
		q += fmt.Sprintf(" AND member_id = $%d", len(args))
	}
	q += ` ORDER BY period DESC, created_at DESC`

	rows, err := r.exec(ctx).QueryContext(ctx, r.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workspace.PerformanceReview, 0)
	for rows.Next() {
		var rev workspace.PerformanceReview
		var autoScore, onTimeScore, qualityScore sql.NullFloat64
		var critRaw string
		if err := rows.Scan(
			&rev.OrganizationID, &rev.ID, &rev.MemberID, &rev.Period, &rev.Note, &rev.Author,
			&rev.AutomaticWeight, &autoScore, &onTimeScore, &qualityScore, &critRaw, &rev.CreatedAt,
		); err != nil {
			return nil, err
		}
		if autoScore.Valid {
			v := autoScore.Float64
			rev.AutomaticScore = &v
		}
		if onTimeScore.Valid {
			v := onTimeScore.Float64
			rev.OnTimeScore = &v
		}
		if qualityScore.Valid {
			v := qualityScore.Float64
			rev.QualityScore = &v
		}
		_ = json.Unmarshal([]byte(critRaw), &rev.Criteria)
		if rev.Criteria == nil {
			rev.Criteria = []workspace.PerformanceReviewCriterion{}
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

func (r HRRepo) CreatePerformanceReview(ctx context.Context, rev workspace.PerformanceReview) error {
	var autoVal, onTimeVal, qualVal sql.NullFloat64
	if rev.AutomaticScore != nil {
		autoVal = sql.NullFloat64{Float64: *rev.AutomaticScore, Valid: true}
	}
	if rev.OnTimeScore != nil {
		onTimeVal = sql.NullFloat64{Float64: *rev.OnTimeScore, Valid: true}
	}
	if rev.QualityScore != nil {
		qualVal = sql.NullFloat64{Float64: *rev.QualityScore, Valid: true}
	}
	critRaw, _ := json.Marshal(rev.Criteria)
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO performance_reviews (organization_id, id, member_id, period, note, author, automatic_weight, automatic_score, on_time_score, quality_score, criteria_json, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`), rev.OrganizationID, rev.ID, rev.MemberID, rev.Period, rev.Note, rev.Author, rev.AutomaticWeight, autoVal, onTimeVal, qualVal, string(critRaw), rev.CreatedAt)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") {
			return errs.Conflict("Penilaian untuk anggota dan periode ini sudah ada.")
		}
		return err
	}
	return nil
}
