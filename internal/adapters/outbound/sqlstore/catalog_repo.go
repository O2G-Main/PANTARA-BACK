package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
)

// --- CompetencyRepository ---

func (s *Store) List(ctx context.Context, orgID string, includeArchived bool) ([]member.Competency, error) {
	query := `SELECT id, organization_id, name, category, is_archived, created_at FROM competencies WHERE organization_id = $1`
	if !includeArchived {
		query += ` AND is_archived = FALSE`
	}
	query += ` ORDER BY category ASC, name ASC`
	rows, err := s.exec(ctx).QueryContext(ctx, s.q(query), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]member.Competency, 0)
	for rows.Next() {
		var c member.Competency
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Category, &c.IsArchived, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, id string) (member.Competency, error) {
	var c member.Competency
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, name, category, is_archived, created_at
		FROM competencies WHERE organization_id = $1 AND id = $2
	`), orgID, id).Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Category, &c.IsArchived, &c.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return member.Competency{}, errs.NotFound("Kompetensi tidak ditemukan.")
		}
		return member.Competency{}, err
	}
	return c, nil
}

func (s *Store) Create(ctx context.Context, c member.Competency) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO competencies (organization_id, id, name, category, is_archived, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`), c.OrganizationID, c.ID, c.Name, c.Category, c.IsArchived, c.CreatedAt)
	return err
}

func (s *Store) Update(ctx context.Context, c member.Competency) error {
	res, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE competencies SET name = $1, category = $2, is_archived = $3
		WHERE organization_id = $4 AND id = $5
	`), c.Name, c.Category, c.IsArchived, c.OrganizationID, c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("Kompetensi tidak ditemukan.")
	}
	return nil
}

func (s *Store) ArchiveOrDelete(ctx context.Context, orgID, id string) error {
	var holderCount int
	_ = s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT COUNT(1) FROM member_competencies WHERE organization_id = $1 AND competency_id = $2
	`), orgID, id).Scan(&holderCount)
	if holderCount > 0 {
		_, err := s.exec(ctx).ExecContext(ctx, s.q(`
			UPDATE competencies SET is_archived = TRUE WHERE organization_id = $1 AND id = $2
		`), orgID, id)
		return err
	}
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		DELETE FROM competencies WHERE organization_id = $1 AND id = $2
	`), orgID, id)
	return err
}

// --- MemberRepositoryAdapter wraps Store to avoid method name collisions with CompetencyRepository & ProjectRepository ---

type MemberRepo struct{ *Store }
type ProjectRepo struct{ *Store }
type CompetencyRepo struct{ *Store }
type TaskRepo struct{ *Store }
type DecisionRepo struct{ *Store }
type NotificationRepo struct{ *Store }

func (r MemberRepo) List(ctx context.Context, orgID, periodID string, includeInactive bool) ([]member.Member, error) {
	q := `SELECT id, organization_id, user_id, name, initials, role_title, email, weekly_limit, photo_url, access_role, is_active, invite_pending, prefers_json, avoids_json, created_at, updated_at
		FROM members WHERE organization_id = $1`
	if !includeInactive {
		q += ` AND is_active = TRUE`
	}
	q += ` ORDER BY name ASC`
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(q), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]member.Member, 0)
	indexByID := make(map[string]int)
	for rows.Next() {
		var m member.Member
		var accessStr, prefersRaw, avoidsRaw string
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Name, &m.Initials, &m.Role, &m.Email, &m.WeeklyLimit, &m.Photo, &accessStr, &m.IsActive, &m.InvitePending, &prefersRaw, &avoidsRaw, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Access = auth.Role(accessStr)
		_ = json.Unmarshal([]byte(prefersRaw), &m.Prefers)
		_ = json.Unmarshal([]byte(avoidsRaw), &m.Avoids)
		if m.Prefers == nil {
			m.Prefers = []string{}
		}
		if m.Avoids == nil {
			m.Avoids = []string{}
		}
		m.Competencies = []member.MemberCompetency{}
		m.LeaveDates = []string{}
		indexByID[m.ID] = len(members)
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return members, nil
	}

	// Batch load member competencies (zero N+1)
	cRows, err := r.exec(ctx).QueryContext(ctx, r.q(`
		SELECT member_id, competency_id, lvl, source, verified_by, verified_at
		FROM member_competencies WHERE organization_id = $1
	`), orgID)
	if err != nil {
		return nil, err
	}
	defer cRows.Close()
	for cRows.Next() {
		var mid, cid, src, vby string
		var lvl int
		var vat sql.NullTime
		if err := cRows.Scan(&mid, &cid, &lvl, &src, &vby, &vat); err != nil {
			return nil, err
		}
		if idx, ok := indexByID[mid]; ok {
			mc := member.MemberCompetency{
				ID:         cid,
				Level:      member.Level(lvl),
				Source:     member.CompetencySource(src),
				VerifiedBy: vby,
			}
			if vat.Valid {
				t := vat.Time
				mc.VerifiedAt = &t
			}
			members[idx].Competencies = append(members[idx].Competencies, mc)
		}
	}
	if err := cRows.Err(); err != nil {
		return nil, err
	}

	// Batch load period availability
	if periodID != "" {
		aRows, err := r.exec(ctx).QueryContext(ctx, r.q(`
			SELECT member_id, available_hours, weekly_limit, leave_dates_json
			FROM member_availabilities WHERE organization_id = $1 AND period_id = $2
		`), orgID, periodID)
		if err != nil {
			return nil, err
		}
		defer aRows.Close()
		for aRows.Next() {
			var mid, leaveRaw string
			var avail sql.NullFloat64
			var wlim float64
			if err := aRows.Scan(&mid, &avail, &wlim, &leaveRaw); err != nil {
				return nil, err
			}
			if idx, ok := indexByID[mid]; ok {
				if avail.Valid {
					v := avail.Float64
					members[idx].AvailableHours = &v
				}
				if wlim > 0 {
					members[idx].WeeklyLimit = wlim
				}
				_ = json.Unmarshal([]byte(leaveRaw), &members[idx].LeaveDates)
				if members[idx].LeaveDates == nil {
					members[idx].LeaveDates = []string{}
				}
			}
		}
		if err := aRows.Err(); err != nil {
			return nil, err
		}
	}

	return members, nil
}

func (r MemberRepo) Get(ctx context.Context, orgID, periodID, id string) (member.Member, error) {
	all, err := r.List(ctx, orgID, periodID, true)
	if err != nil {
		return member.Member{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return member.Member{}, errs.NotFound("Karyawan tidak ditemukan.")
}

func (r MemberRepo) GetAvailability(ctx context.Context, memberID, periodID string) (*member.AvailabilitySnapshot, error) {
	var snap member.AvailabilitySnapshot
	var avail sql.NullFloat64
	var leaveRaw string
	err := r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT member_id, period_id, available_hours, weekly_limit, leave_dates_json
		FROM member_availabilities WHERE member_id = $1 AND period_id = $2
	`), memberID, periodID).Scan(&snap.MemberID, &snap.PeriodID, &avail, &snap.WeeklyLimit, &leaveRaw)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	if avail.Valid {
		v := avail.Float64
		snap.AvailableHours = &v
	}
	_ = json.Unmarshal([]byte(leaveRaw), &snap.LeaveDates)
	if snap.LeaveDates == nil {
		snap.LeaveDates = []string{}
	}
	return &snap, nil
}

func (r MemberRepo) Create(ctx context.Context, orgID, periodID string, m member.Member) error {
	prefersRaw, _ := json.Marshal(m.Prefers)
	avoidsRaw, _ := json.Marshal(m.Avoids)
	now := time.Now().UTC()
	ex := r.exec(ctx)
	if _, err := ex.ExecContext(ctx, r.q(`
		INSERT INTO members (organization_id, id, user_id, name, initials, role_title, email, weekly_limit, photo_url, access_role, is_active, invite_pending, prefers_json, avoids_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`), orgID, m.ID, m.UserID, m.Name, m.Initials, m.Role, m.Email, m.WeeklyLimit, m.Photo, string(m.Access), m.IsActive, m.InvitePending, string(prefersRaw), string(avoidsRaw), now, now); err != nil {
		return errs.Conflict(fmt.Sprintf("Gagal menambahkan karyawan (%s).", m.Email))
	}

	for _, c := range m.Competencies {
		if _, err := ex.ExecContext(ctx, r.q(`
			INSERT INTO member_competencies (organization_id, member_id, competency_id, lvl, source, verified_by, verified_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`), orgID, m.ID, c.ID, int(c.Level), string(c.Source), c.VerifiedBy, c.VerifiedAt); err != nil {
			return err
		}
	}

	if periodID != "" {
		if err := r.SaveAvailability(ctx, orgID, periodID, m.ID, m.AvailableHours, m.WeeklyLimit, m.LeaveDates); err != nil {
			return err
		}
	}
	return nil
}

func (r MemberRepo) UpdateProfile(ctx context.Context, orgID string, m member.Member) error {
	return r.WithinTx(ctx, func(txCtx context.Context) error {
		ex := r.exec(txCtx)
		prefersRaw, _ := json.Marshal(m.Prefers)
		avoidsRaw, _ := json.Marshal(m.Avoids)
		res, err := ex.ExecContext(txCtx, r.q(`
			UPDATE members SET name = $1, initials = $2, role_title = $3, email = $4, weekly_limit = $5, photo_url = $6, prefers_json = $7, avoids_json = $8, updated_at = $9
			WHERE organization_id = $10 AND id = $11
		`), m.Name, m.Initials, m.Role, m.Email, m.WeeklyLimit, m.Photo, string(prefersRaw), string(avoidsRaw), time.Now().UTC(), orgID, m.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errs.NotFound("Karyawan tidak ditemukan.")
		}
		if _, err := ex.ExecContext(txCtx, r.q(`
			DELETE FROM member_competencies WHERE organization_id = $1 AND member_id = $2
		`), orgID, m.ID); err != nil {
			return err
		}
		for _, c := range m.Competencies {
			if _, err := ex.ExecContext(txCtx, r.q(`
				INSERT INTO member_competencies (organization_id, member_id, competency_id, lvl, source, verified_by, verified_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`), orgID, m.ID, c.ID, int(c.Level), string(c.Source), c.VerifiedBy, c.VerifiedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r MemberRepo) SaveAvailability(ctx context.Context, orgID, periodID, memberID string, avail *float64, limit float64, leaveDates []string) error {
	if leaveDates == nil {
		leaveDates = []string{}
	}
	leaveRaw, _ := json.Marshal(leaveDates)
	var availVal sql.NullFloat64
	if avail != nil {
		availVal = sql.NullFloat64{Float64: *avail, Valid: true}
	}
	ex := r.exec(ctx)
	if _, err := ex.ExecContext(ctx, r.q(`
		INSERT INTO member_availabilities (organization_id, member_id, period_id, available_hours, weekly_limit, leave_dates_json, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (organization_id, member_id, period_id) DO UPDATE SET
			available_hours = EXCLUDED.available_hours,
			weekly_limit = EXCLUDED.weekly_limit,
			leave_dates_json = EXCLUDED.leave_dates_json,
			updated_at = EXCLUDED.updated_at
	`), orgID, memberID, periodID, availVal, limit, string(leaveRaw), time.Now().UTC()); err != nil {
		return err
	}
	_, err := ex.ExecContext(ctx, r.q(`UPDATE members SET weekly_limit = $1, updated_at = $2 WHERE organization_id = $3 AND id = $4`), limit, time.Now().UTC(), orgID, memberID)
	return err
}

func (r MemberRepo) VerifyCompetency(ctx context.Context, orgID, memberID, compID, verifierID string, at time.Time) error {
	res, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE member_competencies SET source = 'verifikasi', verified_by = $1, verified_at = $2
		WHERE organization_id = $3 AND member_id = $4 AND competency_id = $5
	`), verifierID, at, orgID, memberID, compID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("Kompetensi karyawan tidak ditemukan.")
	}
	return nil
}

func (r MemberRepo) SetActive(ctx context.Context, orgID, memberID string, active bool) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE members SET is_active = $1, updated_at = $2 WHERE organization_id = $3 AND id = $4
	`), active, time.Now().UTC(), orgID, memberID)
	return err
}

func (r MemberRepo) SetAccess(ctx context.Context, orgID, memberID string, role auth.Role) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE members SET access_role = $1, updated_at = $2 WHERE organization_id = $3 AND id = $4
	`), string(role), time.Now().UTC(), orgID, memberID)
	return err
}

// --- ProjectRepo ---

func (r ProjectRepo) List(ctx context.Context, orgID string, includeArchived bool) ([]project.Project, error) {
	q := `SELECT id, organization_id, name, client, due_date, is_archived, created_at, updated_at FROM projects WHERE organization_id = $1`
	if !includeArchived {
		q += ` AND is_archived = FALSE`
	}
	q += ` ORDER BY due_date ASC`
	rows, err := r.exec(ctx).QueryContext(ctx, r.q(q), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]project.Project, 0)
	for rows.Next() {
		var p project.Project
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Client, &p.DueDate, &p.IsArchived, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r ProjectRepo) Get(ctx context.Context, orgID, id string) (project.Project, error) {
	var p project.Project
	err := r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT id, organization_id, name, client, due_date, is_archived, created_at, updated_at
		FROM projects WHERE organization_id = $1 AND id = $2
	`), orgID, id).Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Client, &p.DueDate, &p.IsArchived, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return project.Project{}, errs.NotFound("Proyek tidak ditemukan.")
		}
		return project.Project{}, err
	}
	return p, nil
}

func (r ProjectRepo) Create(ctx context.Context, p project.Project) error {
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		INSERT INTO projects (organization_id, id, name, client, due_date, is_archived, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`), p.OrganizationID, p.ID, p.Name, p.Client, p.DueDate, p.IsArchived, p.CreatedAt, p.UpdatedAt)
	return err
}

func (r ProjectRepo) Update(ctx context.Context, p project.Project) error {
	res, err := r.exec(ctx).ExecContext(ctx, r.q(`
		UPDATE projects SET name = $1, client = $2, due_date = $3, is_archived = $4, updated_at = $5
		WHERE organization_id = $6 AND id = $7
	`), p.Name, p.Client, p.DueDate, p.IsArchived, p.UpdatedAt, p.OrganizationID, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.NotFound("Proyek tidak ditemukan.")
	}
	return nil
}

func (r ProjectRepo) ArchiveOrDelete(ctx context.Context, orgID, id string) error {
	var taskCount int
	_ = r.exec(ctx).QueryRowContext(ctx, r.q(`
		SELECT COUNT(1) FROM tasks WHERE organization_id = $1 AND project_id = $2
	`), orgID, id).Scan(&taskCount)
	if taskCount > 0 {
		_, err := r.exec(ctx).ExecContext(ctx, r.q(`
			UPDATE projects SET is_archived = TRUE, updated_at = $1 WHERE organization_id = $2 AND id = $3
		`), time.Now().UTC(), orgID, id)
		return err
	}
	_, err := r.exec(ctx).ExecContext(ctx, r.q(`
		DELETE FROM projects WHERE organization_id = $1 AND id = $2
	`), orgID, id)
	return err
}
