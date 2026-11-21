package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

func (s *Store) CreateWorkspaceBootstrap(
	ctx context.Context,
	org workspace.Organization,
	period workspace.WorkPeriod,
	settings workspace.EngineSettings,
	competencies []member.Competency,
	adminUser auth.User,
) error {
	wdJSON, _ := json.Marshal(org.WorkDays)
	holJSON, _ := json.Marshal(org.Holidays)
	prefsJSON, _ := json.Marshal(adminUser.NotifyPrefs)

	ex := s.exec(ctx)
	if _, err := ex.ExecContext(ctx, s.q(`
		INSERT INTO organizations (id, slug, name, timezone, period_type, hours_per_week, work_days_json, holidays_json, baseline_minutes, subscription_plan, onboarding_done, next_task_seq, next_decision_seq, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 1, 1, $12, $13)
	`), org.ID, org.Slug, org.Name, org.Timezone, string(org.PeriodType), org.HoursPerWeek, string(wdJSON), string(holJSON), org.BaselineMinutes, org.SubscriptionPlan, org.OnboardingDone, org.CreatedAt, org.UpdatedAt); err != nil {
		return errs.Internal("gagal membuat organisasi", err)
	}

	if _, err := ex.ExecContext(ctx, s.q(`
		INSERT INTO work_periods (id, organization_id, code, label, start_date, end_date, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`), period.ID, org.ID, period.Code, period.Label, period.StartDate, period.EndDate, period.IsActive, period.CreatedAt); err != nil {
		return errs.Internal("gagal membuat periode awal", err)
	}

	if _, err := ex.ExecContext(ctx, s.q(`
		INSERT INTO recommendation_settings (organization_id, preset, level_tolerance, overload_at, reserve_ratio, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`), org.ID, string(settings.Preset), settings.LevelTolerance, settings.OverloadAt, settings.ReserveRatio, org.CreatedAt); err != nil {
		return errs.Internal("gagal membuat pengaturan rekomendasi", err)
	}

	for _, c := range competencies {
		if _, err := ex.ExecContext(ctx, s.q(`
			INSERT INTO competencies (organization_id, id, name, category, is_archived, created_at)
			VALUES ($1, $2, $3, $4, FALSE, $5)
		`), org.ID, c.ID, c.Name, c.Category, org.CreatedAt); err != nil {
			return errs.Internal("gagal membuat katalog kompetensi", err)
		}
	}

	if _, err := ex.ExecContext(ctx, s.q(`
		INSERT INTO users (id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`), adminUser.ID, org.ID, adminUser.MemberID, strings.ToLower(adminUser.Email), adminUser.Name, adminUser.PasswordHash, string(adminUser.Role), adminUser.IsActive, adminUser.SessionVersion, string(prefsJSON), adminUser.CreatedAt, adminUser.UpdatedAt); err != nil {
		return errs.Internal("gagal membuat akun admin", err)
	}
	return nil
}

func scanUser(row interface{ Scan(...any) error }) (auth.User, error) {
	var u auth.User
	var roleStr, prefsRaw string
	err := row.Scan(&u.ID, &u.OrganizationID, &u.MemberID, &u.Email, &u.Name, &u.PasswordHash, &roleStr, &u.IsActive, &u.SessionVersion, &prefsRaw, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return auth.User{}, errs.NotFound("Akun tidak ditemukan.")
		}
		return auth.User{}, errs.Internal("gagal membaca akun", err)
	}
	u.Role = auth.Role(roleStr)
	u.NotifyPrefs = auth.DefaultNotifyPrefs()
	_ = json.Unmarshal([]byte(prefsRaw), &u.NotifyPrefs)
	return u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (auth.User, error) {
	row := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at
		FROM users WHERE LOWER(email) = LOWER($1)
	`), email)
	return scanUser(row)
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (auth.User, error) {
	row := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at
		FROM users WHERE id = $1
	`), userID)
	return scanUser(row)
}

func (s *Store) GetUserByMemberID(ctx context.Context, orgID, memberID string) (auth.User, error) {
	row := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at
		FROM users WHERE organization_id = $1 AND member_id = $2
	`), orgID, memberID)
	return scanUser(row)
}

func (s *Store) ListUsersByOrg(ctx context.Context, orgID string) ([]auth.User, error) {
	rows, err := s.exec(ctx).QueryContext(ctx, s.q(`
		SELECT id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at
		FROM users WHERE organization_id = $1 ORDER BY created_at ASC
	`), orgID)
	if err != nil {
		return nil, errs.Internal("gagal memuat pengguna", err)
	}
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUserProfile(ctx context.Context, userID, name, email string) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE users SET name = $1, email = $2, updated_at = $3 WHERE id = $4
	`), name, strings.ToLower(email), time.Now().UTC(), userID)
	return err
}

func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string, bumpSession bool) error {
	inc := 0
	if bumpSession {
		inc = 1
	}
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE users SET password_hash = $1, session_version = session_version + $2, updated_at = $3 WHERE id = $4
	`), passwordHash, inc, time.Now().UTC(), userID)
	return err
}

func (s *Store) UpdateUserNotifyPrefs(ctx context.Context, userID string, prefs auth.NotifyPrefs) error {
	raw, _ := json.Marshal(prefs)
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE users SET notify_prefs_json = $1, updated_at = $2 WHERE id = $3
	`), string(raw), time.Now().UTC(), userID)
	return err
}

func (s *Store) UpdateUserAccessAndActive(ctx context.Context, orgID, memberID string, role auth.Role, active bool, bumpSession bool) error {
	inc := 0
	if bumpSession {
		inc = 1
	}
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE users SET role = $1, is_active = $2, session_version = session_version + $3, updated_at = $4
		WHERE organization_id = $5 AND member_id = $6
	`), string(role), active, inc, time.Now().UTC(), orgID, memberID)
	return err
}

func (s *Store) BumpSessionVersion(ctx context.Context, userID string) (int64, error) {
	ex := s.exec(ctx)
	if _, err := ex.ExecContext(ctx, s.q(`
		UPDATE users SET session_version = session_version + 1, updated_at = $1 WHERE id = $2
	`), time.Now().UTC(), userID); err != nil {
		return 0, err
	}
	var ver int64
	if err := ex.QueryRowContext(ctx, s.q(`SELECT session_version FROM users WHERE id = $1`), userID).Scan(&ver); err != nil {
		return 0, err
	}
	return ver, nil
}

func (s *Store) CreateRefreshSession(ctx context.Context, sess auth.RefreshSession) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO refresh_sessions (id, user_id, token_hash, remember_me, user_agent, expires_at, revoked_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`), sess.ID, sess.UserID, sess.TokenHash, sess.RememberMe, sess.UserAgent, sess.ExpiresAt, sess.RevokedAt, sess.CreatedAt)
	return err
}

func (s *Store) GetRefreshSessionByHash(ctx context.Context, tokenHash string) (auth.RefreshSession, error) {
	var sess auth.RefreshSession
	var revoked sql.NullTime
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, user_id, token_hash, remember_me, user_agent, expires_at, revoked_at, created_at
		FROM refresh_sessions WHERE token_hash = $1
	`), tokenHash).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.RememberMe, &sess.UserAgent, &sess.ExpiresAt, &revoked, &sess.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return auth.RefreshSession{}, errs.Unauthorized("Sesi tidak ditemukan.")
		}
		return auth.RefreshSession{}, err
	}
	if revoked.Valid {
		t := revoked.Time
		sess.RevokedAt = &t
	}
	return sess, nil
}

func (s *Store) RevokeRefreshSession(ctx context.Context, id string) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE refresh_sessions SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL
	`), time.Now().UTC(), id)
	return err
}

func (s *Store) RevokeAllRefreshSessions(ctx context.Context, userID string) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE refresh_sessions SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL
	`), time.Now().UTC(), userID)
	return err
}

func (s *Store) CreateInvitation(ctx context.Context, inv auth.InvitationToken) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO invitation_tokens (id, organization_id, organization_name, member_id, email, name, role_title, access_role, token_hash, invited_by_user_id, invited_by_name, expires_at, accepted_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULL, $13)
	`), inv.ID, inv.OrganizationID, inv.OrganizationName, inv.MemberID, inv.Email, inv.Name, inv.RoleTitle, string(inv.AccessRole), inv.TokenHash, inv.InvitedByUserID, inv.InvitedByName, inv.ExpiresAt, inv.CreatedAt)
	return err
}

func (s *Store) GetInvitationByHash(ctx context.Context, tokenHash string) (auth.InvitationToken, error) {
	var inv auth.InvitationToken
	var roleStr string
	var accepted sql.NullTime
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, organization_name, member_id, email, name, role_title, access_role, token_hash, invited_by_user_id, invited_by_name, expires_at, accepted_at, created_at
		FROM invitation_tokens WHERE token_hash = $1
	`), tokenHash).Scan(&inv.ID, &inv.OrganizationID, &inv.OrganizationName, &inv.MemberID, &inv.Email, &inv.Name, &inv.RoleTitle, &roleStr, &inv.TokenHash, &inv.InvitedByUserID, &inv.InvitedByName, &inv.ExpiresAt, &accepted, &inv.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return auth.InvitationToken{}, errs.NotFound("Undangan tidak ditemukan.")
		}
		return auth.InvitationToken{}, err
	}
	inv.AccessRole = auth.Role(roleStr)
	if accepted.Valid {
		t := accepted.Time
		inv.AcceptedAt = &t
	}
	return inv, nil
}

func (s *Store) ListPendingInvitations(ctx context.Context, orgID string) ([]auth.InvitationToken, error) {
	rows, err := s.exec(ctx).QueryContext(ctx, s.q(`
		SELECT id, organization_id, organization_name, member_id, email, name, role_title, access_role, '', invited_by_user_id, invited_by_name, expires_at, accepted_at, created_at
		FROM invitation_tokens WHERE organization_id = $1 AND accepted_at IS NULL ORDER BY created_at DESC
	`), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]auth.InvitationToken, 0)
	for rows.Next() {
		var inv auth.InvitationToken
		var roleStr string
		var accepted sql.NullTime
		if err := rows.Scan(&inv.ID, &inv.OrganizationID, &inv.OrganizationName, &inv.MemberID, &inv.Email, &inv.Name, &inv.RoleTitle, &roleStr, &inv.TokenHash, &inv.InvitedByUserID, &inv.InvitedByName, &inv.ExpiresAt, &accepted, &inv.CreatedAt); err != nil {
			return nil, err
		}
		inv.AccessRole = auth.Role(roleStr)
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) AcceptInvitation(ctx context.Context, invitationID string, u auth.User, memberID string) error {
	ex := s.exec(ctx)
	now := time.Now().UTC()
	res, err := ex.ExecContext(ctx, s.q(`
		UPDATE invitation_tokens SET accepted_at = $1 WHERE id = $2 AND accepted_at IS NULL
	`), now, invitationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Conflict("Undangan sudah digunakan.")
	}
	prefsRaw, _ := json.Marshal(u.NotifyPrefs)
	if _, err := ex.ExecContext(ctx, s.q(`
		INSERT INTO users (id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, $8, $9, $10, $11)
	`), u.ID, u.OrganizationID, memberID, strings.ToLower(u.Email), u.Name, u.PasswordHash, string(u.Role), u.SessionVersion, string(prefsRaw), u.CreatedAt, u.UpdatedAt); err != nil {
		return errs.Conflict("Email sudah memiliki akun aktif.")
	}
	_, err = ex.ExecContext(ctx, s.q(`
		UPDATE members SET user_id = $1, name = $2, invite_pending = FALSE, updated_at = $3
		WHERE organization_id = $4 AND id = $5
	`), u.ID, u.Name, now, u.OrganizationID, memberID)
	return err
}

func (s *Store) CreatePasswordReset(ctx context.Context, r auth.PasswordResetToken) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO password_resets (id, user_id, email, token_hash, expires_at, used_at, created_at)
		VALUES ($1, $2, $3, $4, $5, NULL, $6)
	`), r.ID, r.UserID, r.Email, r.TokenHash, r.ExpiresAt, r.CreatedAt)
	return err
}

func (s *Store) GetPasswordResetByHash(ctx context.Context, tokenHash string) (auth.PasswordResetToken, error) {
	var r auth.PasswordResetToken
	var used sql.NullTime
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, user_id, email, token_hash, expires_at, used_at, created_at
		FROM password_resets WHERE token_hash = $1
	`), tokenHash).Scan(&r.ID, &r.UserID, &r.Email, &r.TokenHash, &r.ExpiresAt, &used, &r.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return auth.PasswordResetToken{}, errs.Validation("Tautan reset tidak valid.")
		}
		return auth.PasswordResetToken{}, err
	}
	if used.Valid {
		t := used.Time
		r.UsedAt = &t
	}
	return r, nil
}

func (s *Store) ConsumePasswordReset(ctx context.Context, resetID, userID, newPasswordHash string) error {
	ex := s.exec(ctx)
	now := time.Now().UTC()
	res, err := ex.ExecContext(ctx, s.q(`
		UPDATE password_resets SET used_at = $1 WHERE id = $2 AND used_at IS NULL
	`), now, resetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errs.Validation("Tautan reset sandi sudah pernah digunakan.")
	}
	_, err = ex.ExecContext(ctx, s.q(`
		UPDATE users SET password_hash = $1, session_version = session_version + 1, updated_at = $2 WHERE id = $3
	`), newPasswordHash, now, userID)
	return err
}

// --- WorkspaceRepository ---

func (s *Store) GetOrganization(ctx context.Context, orgID string) (workspace.Organization, error) {
	var org workspace.Organization
	var pType, wdRaw, holRaw string
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, slug, name, timezone, period_type, hours_per_week, work_days_json, holidays_json, baseline_minutes, subscription_plan, onboarding_done, created_at, updated_at
		FROM organizations WHERE id = $1
	`), orgID).Scan(&org.ID, &org.Slug, &org.Name, &org.Timezone, &pType, &org.HoursPerWeek, &wdRaw, &holRaw, &org.BaselineMinutes, &org.SubscriptionPlan, &org.OnboardingDone, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return workspace.Organization{}, errs.NotFound("Organisasi tidak ditemukan.")
		}
		return workspace.Organization{}, err
	}
	org.PeriodType = workspace.PeriodType(pType)
	_ = json.Unmarshal([]byte(wdRaw), &org.WorkDays)
	_ = json.Unmarshal([]byte(holRaw), &org.Holidays)
	if org.WorkDays == nil {
		org.WorkDays = []string{}
	}
	if org.Holidays == nil {
		org.Holidays = []string{}
	}
	return org, nil
}

func (s *Store) UpdateOrganization(ctx context.Context, org workspace.Organization) error {
	wdRaw, _ := json.Marshal(org.WorkDays)
	holRaw, _ := json.Marshal(org.Holidays)
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE organizations SET name = $1, timezone = $2, period_type = $3, hours_per_week = $4, work_days_json = $5, holidays_json = $6, onboarding_done = $7, updated_at = $8
		WHERE id = $9
	`), org.Name, org.Timezone, string(org.PeriodType), org.HoursPerWeek, string(wdRaw), string(holRaw), org.OnboardingDone, org.UpdatedAt, org.ID)
	return err
}

func (s *Store) UpdateBaselineMinutes(ctx context.Context, orgID string, minutes float64) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE organizations SET baseline_minutes = $1, updated_at = $2 WHERE id = $3
	`), minutes, time.Now().UTC(), orgID)
	return err
}

func (s *Store) ListPeriods(ctx context.Context, orgID string) ([]workspace.WorkPeriod, error) {
	rows, err := s.exec(ctx).QueryContext(ctx, s.q(`
		SELECT id, organization_id, code, label, start_date, end_date, is_active, created_at
		FROM work_periods WHERE organization_id = $1 ORDER BY start_date DESC
	`), orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workspace.WorkPeriod, 0)
	for rows.Next() {
		var p workspace.WorkPeriod
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Code, &p.Label, &p.StartDate, &p.EndDate, &p.IsActive, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetActivePeriod(ctx context.Context, orgID string) (workspace.WorkPeriod, error) {
	var p workspace.WorkPeriod
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, code, label, start_date, end_date, is_active, created_at
		FROM work_periods WHERE organization_id = $1 ORDER BY is_active DESC, start_date DESC LIMIT 1
	`), orgID).Scan(&p.ID, &p.OrganizationID, &p.Code, &p.Label, &p.StartDate, &p.EndDate, &p.IsActive, &p.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return workspace.WorkPeriod{}, errs.NotFound("Periode aktif belum dibuat.")
		}
		return workspace.WorkPeriod{}, err
	}
	return p, nil
}

func (s *Store) GetPeriod(ctx context.Context, orgID, periodID string) (workspace.WorkPeriod, error) {
	var p workspace.WorkPeriod
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, code, label, start_date, end_date, is_active, created_at
		FROM work_periods WHERE organization_id = $1 AND id = $2
	`), orgID, periodID).Scan(&p.ID, &p.OrganizationID, &p.Code, &p.Label, &p.StartDate, &p.EndDate, &p.IsActive, &p.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return workspace.WorkPeriod{}, errs.NotFound("Periode tidak ditemukan.")
		}
		return workspace.WorkPeriod{}, err
	}
	return p, nil
}

func (s *Store) GetPreviousPeriod(ctx context.Context, orgID, currentPeriodID string) (*workspace.WorkPeriod, error) {
	cur, err := s.GetPeriod(ctx, orgID, currentPeriodID)
	if err != nil {
		return nil, err
	}
	var prev workspace.WorkPeriod
	err = s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT id, organization_id, code, label, start_date, end_date, is_active, created_at
		FROM work_periods WHERE organization_id = $1 AND start_date < $2 ORDER BY start_date DESC LIMIT 1
	`), orgID, cur.StartDate).Scan(&prev.ID, &prev.OrganizationID, &prev.Code, &prev.Label, &prev.StartDate, &prev.EndDate, &prev.IsActive, &prev.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &prev, nil
}

func (s *Store) CreatePeriod(ctx context.Context, p workspace.WorkPeriod, activate bool) error {
	return s.WithinTx(ctx, func(txCtx context.Context) error {
		ex := s.exec(txCtx)
		if activate {
			if _, err := ex.ExecContext(txCtx, s.q(`UPDATE work_periods SET is_active = FALSE WHERE organization_id = $1`), p.OrganizationID); err != nil {
				return err
			}
		}
		_, err := ex.ExecContext(txCtx, s.q(`
			INSERT INTO work_periods (id, organization_id, code, label, start_date, end_date, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`), p.ID, p.OrganizationID, p.Code, p.Label, p.StartDate, p.EndDate, activate, p.CreatedAt)
		if err != nil {
			return errs.Conflict("Periode dengan tanggal tersebut sudah ada.")
		}
		return nil
	})
}

func (s *Store) ActivatePeriod(ctx context.Context, orgID, periodID string) error {
	return s.WithinTx(ctx, func(txCtx context.Context) error {
		ex := s.exec(txCtx)
		if _, err := ex.ExecContext(txCtx, s.q(`UPDATE work_periods SET is_active = FALSE WHERE organization_id = $1`), orgID); err != nil {
			return err
		}
		res, err := ex.ExecContext(txCtx, s.q(`UPDATE work_periods SET is_active = TRUE WHERE organization_id = $1 AND id = $2`), orgID, periodID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errs.NotFound("Periode tidak ditemukan.")
		}
		return nil
	})
}

func (s *Store) GetEngineSettings(ctx context.Context, orgID string) (workspace.EngineSettings, error) {
	var set workspace.EngineSettings
	var presetStr string
	err := s.exec(ctx).QueryRowContext(ctx, s.q(`
		SELECT preset, level_tolerance, overload_at, reserve_ratio FROM recommendation_settings WHERE organization_id = $1
	`), orgID).Scan(&presetStr, &set.LevelTolerance, &set.OverloadAt, &set.ReserveRatio)
	if err != nil {
		if isNoRows(err) {
			return workspace.DefaultEngineSettings(), nil
		}
		return set, err
	}
	set.Preset = workspace.PresetID(presetStr)
	return set, nil
}

func (s *Store) SaveEngineSettings(ctx context.Context, orgID string, set workspace.EngineSettings) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO recommendation_settings (organization_id, preset, level_tolerance, overload_at, reserve_ratio, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id) DO UPDATE SET
			preset = EXCLUDED.preset,
			level_tolerance = EXCLUDED.level_tolerance,
			overload_at = EXCLUDED.overload_at,
			reserve_ratio = EXCLUDED.reserve_ratio,
			updated_at = EXCLUDED.updated_at
	`), orgID, string(set.Preset), set.LevelTolerance, set.OverloadAt, set.ReserveRatio, time.Now().UTC())
	return err
}

// SetSequenceCounters sets next_task_seq and next_decision_seq on an organization (used during deterministic seeding).
func (s *Store) SetSequenceCounters(ctx context.Context, orgID string, nextTaskSeq, nextDecisionSeq int) error {
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		UPDATE organizations SET next_task_seq = $1, next_decision_seq = $2 WHERE id = $3
	`), nextTaskSeq, nextDecisionSeq, orgID)
	return err
}

// CreateUser inserts an additional authenticated user account directly (used by seeders).
func (s *Store) CreateUser(ctx context.Context, u auth.User) error {
	prefsJSON, _ := json.Marshal(u.NotifyPrefs)
	_, err := s.exec(ctx).ExecContext(ctx, s.q(`
		INSERT INTO users (id, organization_id, member_id, email, name, password_hash, role, is_active, session_version, notify_prefs_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`), u.ID, u.OrganizationID, u.MemberID, strings.ToLower(u.Email), u.Name, u.PasswordHash, string(u.Role), u.IsActive, u.SessionVersion, string(prefsJSON), u.CreatedAt, u.UpdatedAt)
	return err
}

// HasAnyOrganization reports whether the database already contains at least one organization.
func (s *Store) HasAnyOrganization(ctx context.Context) (bool, error) {
	var count int
	err := s.exec(ctx).QueryRowContext(ctx, `SELECT COUNT(1) FROM organizations`).Scan(&count)
	return count > 0, err
}
