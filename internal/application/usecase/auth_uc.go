package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
)

const (
	MaxLoginAttempts   = 5
	LoginLockDuration  = 15 * time.Minute
	AccessTokenTTL     = 15 * time.Minute
	RefreshShortTTL    = 24 * time.Hour
	RefreshRememberTTL = 30 * 24 * time.Hour
	ResetTokenTTL      = 30 * time.Minute
	InviteTokenTTL     = 7 * 24 * time.Hour
)

var emailRe = regexp.MustCompile(`^\S+@\S+\.\S+$`)

// AuthTokens bundles freshly issued session credentials returned to HTTP handlers.
type AuthTokens struct {
	User         auth.User
	AccessToken  string
	RefreshToken string
	RememberMe   bool
}

// AuthUseCase handles registration, authentication, lockout, refresh rotation, and password lifecycle.
type AuthUseCase struct {
	tx      port.TxManager
	auth    port.AuthRepository
	ws      port.WorkspaceRepository
	sec     port.SecurityStore
	hasher  port.PasswordHasher
	tokens  port.TokenService
	mailer  port.Mailer
	clock   port.Clock
	baseURL string
}

func NewAuthUseCase(
	tx port.TxManager,
	authRepo port.AuthRepository,
	ws port.WorkspaceRepository,
	sec port.SecurityStore,
	hasher port.PasswordHasher,
	tokens port.TokenService,
	mailer port.Mailer,
	clock port.Clock,
	baseURL string,
) *AuthUseCase {
	return &AuthUseCase{
		tx:      tx,
		auth:    authRepo,
		ws:      ws,
		sec:     sec,
		hasher:  hasher,
		tokens:  tokens,
		mailer:  mailer,
		clock:   clock,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

func slugify(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	prevDash := false
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "item"
	}
	return res
}

func initialsOf(name string) string {
	parts := strings.Fields(name)
	var b strings.Builder
	for i := 0; i < len(parts) && i < 2; i++ {
		runes := []rune(parts[i])
		if len(runes) > 0 {
			b.WriteRune(runes[0])
		}
	}
	if b.Len() == 0 {
		return "PN"
	}
	return strings.ToUpper(b.String())
}

// CurrentWeekWindow returns Monday..Friday dates and readable Indonesian label for clock's current week.
func CurrentWeekWindow(now time.Time) (startISO, endISO, label string) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	t := now.In(loc)
	wd := int(t.Weekday())
	shift := (wd + 6) % 7
	mon := t.AddDate(0, 0, -shift)
	fri := mon.AddDate(0, 0, 4)
	months := []string{"", "Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
	if mon.Month() == fri.Month() {
		label = fmt.Sprintf("%d - %d %s %d", mon.Day(), fri.Day(), months[fri.Month()], fri.Year())
	} else {
		label = fmt.Sprintf("%d %s - %d %s %d", mon.Day(), months[mon.Month()], fri.Day(), months[fri.Month()], fri.Year())
	}
	return mon.Format("2006-01-02"), fri.Format("2006-01-02"), label
}

// RegisterWorkspace creates a new tenant organization, initial active period, default competency catalog, and admin user.
func (uc *AuthUseCase) RegisterWorkspace(ctx context.Context, orgName, adminName, email, password, userAgent string) (AuthTokens, error) {
	orgName = strings.TrimSpace(orgName)
	adminName = strings.TrimSpace(adminName)
	email = strings.ToLower(strings.TrimSpace(email))
	if len(orgName) < 3 {
		return AuthTokens{}, errs.Validation("Tulis nama organisasi.", map[string]string{"org": "Minimal 3 karakter."})
	}
	if len(adminName) < 3 {
		return AuthTokens{}, errs.Validation("Tulis nama lengkap admin.", map[string]string{"name": "Minimal 3 karakter."})
	}
	if !emailRe.MatchString(email) {
		return AuthTokens{}, errs.Validation("Email belum valid.", map[string]string{"email": "Format email tidak valid."})
	}
	if len(password) < 8 {
		return AuthTokens{}, errs.Validation("Sandi minimal 8 karakter.", map[string]string{"password": "Minimal 8 karakter."})
	}

	if _, err := uc.auth.GetUserByEmail(ctx, email); err == nil {
		return AuthTokens{}, errs.Conflict("Email ini sudah terdaftar di ruang kerja lain.")
	}

	hash, err := uc.hasher.Hash(password)
	if err != nil {
		return AuthTokens{}, errs.Internal("gagal memproses sandi", err)
	}

	now := uc.clock.Now().UTC()
	orgID := uuid.NewString()
	startISO, endISO, label := CurrentWeekWindow(now)
	org := workspace.Organization{
		ID:               orgID,
		Slug:             slugify(orgName) + "-" + orgID[:6],
		Name:             orgName,
		Timezone:         "Asia/Jakarta",
		PeriodType:       workspace.PeriodWeekly,
		HoursPerWeek:     32,
		WorkDays:         []string{"Sen", "Sel", "Rab", "Kam", "Jum"},
		Holidays:         []string{},
		BaselineMinutes:  24,
		SubscriptionPlan: "pilot",
		OnboardingDone:   false,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	period := workspace.WorkPeriod{
		ID:             uuid.NewString(),
		OrganizationID: orgID,
		Code:           startISO,
		Label:          label,
		StartDate:      startISO,
		EndDate:        endISO,
		IsActive:       true,
		CreatedAt:      now,
	}
	settings := workspace.DefaultEngineSettings()

	ds, err := seeddata.LoadArunika()
	if err != nil {
		return AuthTokens{}, errs.Internal("gagal memuat katalog kompetensi awal", err)
	}
	comps := make([]member.Competency, len(ds.Competencies))
	for i, c := range ds.Competencies {
		c.OrganizationID = orgID
		c.CreatedAt = now
		comps[i] = c
	}

	user := auth.User{
		ID:             uuid.NewString(),
		OrganizationID: orgID,
		Email:          email,
		Name:           adminName,
		PasswordHash:   hash,
		Role:           auth.RoleAdmin,
		IsActive:       true,
		SessionVersion: 1,
		NotifyPrefs:    auth.DefaultNotifyPrefs(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		return uc.auth.CreateWorkspaceBootstrap(txCtx, org, period, settings, comps, user)
	})
	if err != nil {
		return AuthTokens{}, err
	}

	return uc.issueTokenPair(ctx, user, true, userAgent)
}

// Login authenticates an account with persistent 5-attempt / 15-minute lockout protection.
func (uc *AuthUseCase) Login(ctx context.Context, email, password string, rememberMe bool, userAgent string) (AuthTokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRe.MatchString(email) || password == "" {
		return AuthTokens{}, errs.Validation("Tulis email kerja dan sandi yang valid.")
	}

	lockKey := "login:" + email
	if locked, retryAfter, err := uc.sec.GetLoginLock(ctx, lockKey); err == nil && locked {
		mins := int(retryAfter.Minutes()) + 1
		return AuthTokens{}, errs.Locked(fmt.Sprintf("Terlalu banyak percobaan. Akun dikunci %d menit lagi. Pakai Lupa sandi untuk membuka lebih cepat.", mins))
	}

	user, err := uc.auth.GetUserByEmail(ctx, email)
	if err != nil || !user.IsActive || !uc.hasher.Verify(password, user.PasswordHash) {
		tries, _, _ := uc.sec.RecordLoginFailure(ctx, lockKey, MaxLoginAttempts, LoginLockDuration)
		left := MaxLoginAttempts - tries
		if left <= 0 {
			return AuthTokens{}, errs.Locked("Terlalu banyak percobaan. Akun dikunci 15 menit. Pakai Lupa sandi untuk membuka lebih cepat.")
		}
		return AuthTokens{}, errs.Unauthorized(fmt.Sprintf("Email atau sandi salah. Sisa %d percobaan sebelum akun dikunci sementara.", left))
	}

	_ = uc.sec.ClearLoginFailures(ctx, lockKey)
	return uc.issueTokenPair(ctx, user, rememberMe, userAgent)
}

func (uc *AuthUseCase) issueTokenPair(ctx context.Context, user auth.User, rememberMe bool, userAgent string) (AuthTokens, error) {
	now := uc.clock.Now().UTC()
	claims := auth.Claims{
		UserID:         user.ID,
		OrganizationID: user.OrganizationID,
		MemberID:       user.MemberID,
		Email:          user.Email,
		Name:           user.Name,
		Role:           user.Role,
		SessionVersion: user.SessionVersion,
	}
	access, err := uc.tokens.IssueAccessToken(claims, AccessTokenTTL)
	if err != nil {
		return AuthTokens{}, errs.Internal("gagal membuat token akses", err)
	}
	rawRefresh, hashRefresh, err := uc.tokens.NewOpaqueToken()
	if err != nil {
		return AuthTokens{}, errs.Internal("gagal membuat token sesi", err)
	}
	ttl := RefreshShortTTL
	if rememberMe {
		ttl = RefreshRememberTTL
	}
	sess := auth.RefreshSession{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		TokenHash:  hashRefresh,
		RememberMe: rememberMe,
		UserAgent:  userAgent,
		ExpiresAt:  now.Add(ttl),
		CreatedAt:  now,
	}
	if err := uc.auth.CreateRefreshSession(ctx, sess); err != nil {
		return AuthTokens{}, err
	}
	_ = uc.sec.SetSessionVersion(ctx, user.ID, user.SessionVersion, AccessTokenTTL)
	return AuthTokens{
		User:         user,
		AccessToken:  access,
		RefreshToken: rawRefresh,
		RememberMe:   rememberMe,
	}, nil
}

// Refresh rotates a valid refresh token into a new access/refresh pair while verifying user active status.
func (uc *AuthUseCase) Refresh(ctx context.Context, rawRefreshToken, userAgent string) (AuthTokens, error) {
	if strings.TrimSpace(rawRefreshToken) == "" {
		return AuthTokens{}, errs.Unauthorized("Sesi berakhir, silakan masuk kembali.")
	}
	hash := uc.tokens.HashOpaqueToken(rawRefreshToken)
	sess, err := uc.auth.GetRefreshSessionByHash(ctx, hash)
	if err != nil || sess.RevokedAt != nil || uc.clock.Now().UTC().After(sess.ExpiresAt) {
		return AuthTokens{}, errs.Unauthorized("Sesi tidak valid atau sudah berakhir.")
	}
	user, err := uc.auth.GetUserByID(ctx, sess.UserID)
	if err != nil || !user.IsActive {
		return AuthTokens{}, errs.Unauthorized("Akun tidak aktif.")
	}

	var issued AuthTokens
	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := uc.auth.RevokeRefreshSession(txCtx, sess.ID); err != nil {
			return err
		}
		var issueErr error
		issued, issueErr = uc.issueTokenPair(txCtx, user, sess.RememberMe, userAgent)
		return issueErr
	})
	return issued, err
}

// Logout revokes the current refresh session.
func (uc *AuthUseCase) Logout(ctx context.Context, rawRefreshToken string) error {
	if strings.TrimSpace(rawRefreshToken) == "" {
		return nil
	}
	hash := uc.tokens.HashOpaqueToken(rawRefreshToken)
	sess, err := uc.auth.GetRefreshSessionByHash(ctx, hash)
	if err != nil {
		return nil
	}
	return uc.auth.RevokeRefreshSession(ctx, sess.ID)
}

// LogoutAll increments user session_version and revokes all active refresh sessions (P33).
func (uc *AuthUseCase) LogoutAll(ctx context.Context, userID string) error {
	var newVer int64
	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		var bumpErr error
		newVer, bumpErr = uc.auth.BumpSessionVersion(txCtx, userID)
		if bumpErr != nil {
			return bumpErr
		}
		return uc.auth.RevokeAllRefreshSessions(txCtx, userID)
	})
	if err != nil {
		return err
	}
	_ = uc.sec.SetSessionVersion(ctx, userID, newVer, 24*time.Hour)
	return nil
}

// RequestPasswordReset creates a 30-minute reset link when email matches an active user.
func (uc *AuthUseCase) RequestPasswordReset(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRe.MatchString(email) {
		return errs.Validation("Email belum valid.")
	}
	user, err := uc.auth.GetUserByEmail(ctx, email)
	if err != nil || !user.IsActive {
		// Safe non-enumerating success response matching P7 UX
		return nil
	}
	raw, hash, err := uc.tokens.NewOpaqueToken()
	if err != nil {
		return errs.Internal("gagal membuat tautan reset sandi", err)
	}
	now := uc.clock.Now().UTC()
	rst := auth.PasswordResetToken{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		Email:     user.Email,
		TokenHash: hash,
		ExpiresAt: now.Add(ResetTokenTTL),
		CreatedAt: now,
	}
	if err := uc.auth.CreatePasswordReset(ctx, rst); err != nil {
		return err
	}
	resetURL := fmt.Sprintf("%s/reset-sandi?token=%s", uc.baseURL, raw)
	return uc.mailer.SendPasswordReset(ctx, user.Email, resetURL)
}

// ResetPassword atomically consumes a valid reset token, sets new password, clears login locks, and revokes old sessions.
func (uc *AuthUseCase) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	if strings.TrimSpace(rawToken) == "" {
		return errs.Validation("Tautan reset sandi tidak valid.")
	}
	if len(newPassword) < 8 {
		return errs.Validation("Sandi minimal 8 karakter.")
	}
	hash := uc.tokens.HashOpaqueToken(rawToken)
	rst, err := uc.auth.GetPasswordResetByHash(ctx, hash)
	if err != nil || rst.UsedAt != nil || uc.clock.Now().UTC().After(rst.ExpiresAt) {
		return errs.Validation("Tautan reset sandi tidak valid atau sudah kedaluwarsa.")
	}
	pwHash, err := uc.hasher.Hash(newPassword)
	if err != nil {
		return errs.Internal("gagal memproses sandi", err)
	}
	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := uc.auth.ConsumePasswordReset(txCtx, rst.ID, rst.UserID, pwHash); err != nil {
			return err
		}
		return uc.auth.RevokeAllRefreshSessions(txCtx, rst.UserID)
	})
	if err != nil {
		return err
	}
	user, _ := uc.auth.GetUserByID(ctx, rst.UserID)
	if user.Email != "" {
		_ = uc.sec.ClearLoginFailures(ctx, "login:"+user.Email)
		_ = uc.sec.SetSessionVersion(ctx, user.ID, user.SessionVersion, 24*time.Hour)
	}
	return nil
}

// InspectInvitation returns organization and inviter metadata for a pending invitation link (P8).
func (uc *AuthUseCase) InspectInvitation(ctx context.Context, rawToken string) (auth.InvitationToken, error) {
	if strings.TrimSpace(rawToken) == "" {
		return auth.InvitationToken{}, errs.NotFound("Undangan tidak ditemukan.")
	}
	hash := uc.tokens.HashOpaqueToken(rawToken)
	inv, err := uc.auth.GetInvitationByHash(ctx, hash)
	if err != nil {
		return auth.InvitationToken{}, errs.NotFound("Undangan tidak ditemukan atau sudah tidak berlaku.")
	}
	if inv.AcceptedAt != nil {
		return auth.InvitationToken{}, errs.Conflict("Undangan ini sudah digunakan.")
	}
	if uc.clock.Now().UTC().After(inv.ExpiresAt) {
		return auth.InvitationToken{}, errs.Validation("Undangan sudah kedaluwarsa. Minta admin mengirim ulang.")
	}
	return inv, nil
}

// AcceptInvitation activates the invited member account atomically and signs them in.
func (uc *AuthUseCase) AcceptInvitation(ctx context.Context, rawToken, fullName, password, userAgent string) (AuthTokens, error) {
	fullName = strings.TrimSpace(fullName)
	if len(fullName) < 3 {
		return AuthTokens{}, errs.Validation("Tulis nama lengkap.")
	}
	if len(password) < 8 {
		return AuthTokens{}, errs.Validation("Sandi minimal 8 karakter.")
	}
	inv, err := uc.InspectInvitation(ctx, rawToken)
	if err != nil {
		return AuthTokens{}, err
	}
	pwHash, err := uc.hasher.Hash(password)
	if err != nil {
		return AuthTokens{}, errs.Internal("gagal memproses sandi", err)
	}
	now := uc.clock.Now().UTC()
	newUser := auth.User{
		ID:             uuid.NewString(),
		OrganizationID: inv.OrganizationID,
		MemberID:       inv.MemberID,
		Email:          inv.Email,
		Name:           fullName,
		PasswordHash:   pwHash,
		Role:           inv.AccessRole,
		IsActive:       true,
		SessionVersion: 1,
		NotifyPrefs:    auth.DefaultNotifyPrefs(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		return uc.auth.AcceptInvitation(txCtx, inv.ID, newUser, inv.MemberID)
	})
	if err != nil {
		return AuthTokens{}, err
	}
	return uc.issueTokenPair(ctx, newUser, true, userAgent)
}

func (uc *AuthUseCase) GetMe(ctx context.Context, userID string) (auth.User, error) {
	return uc.auth.GetUserByID(ctx, userID)
}

func (uc *AuthUseCase) UpdateProfile(ctx context.Context, userID, name, email string) (auth.User, error) {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if len(name) < 2 {
		return auth.User{}, errs.Validation("Nama minimal 2 karakter.")
	}
	if !emailRe.MatchString(email) {
		return auth.User{}, errs.Validation("Email belum valid.")
	}
	if existing, err := uc.auth.GetUserByEmail(ctx, email); err == nil && existing.ID != userID {
		return auth.User{}, errs.Conflict("Email sudah dipakai akun lain.")
	}
	if err := uc.auth.UpdateUserProfile(ctx, userID, name, email); err != nil {
		return auth.User{}, err
	}
	return uc.auth.GetUserByID(ctx, userID)
}

func (uc *AuthUseCase) ChangePassword(ctx context.Context, userID, currentPw, newPw string) error {
	if currentPw == "" {
		return errs.Validation("Isi sandi saat ini.")
	}
	if len(newPw) < 8 {
		return errs.Validation("Sandi baru minimal 8 karakter.")
	}
	user, err := uc.auth.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !uc.hasher.Verify(currentPw, user.PasswordHash) {
		return errs.Validation("Sandi saat ini tidak cocok.")
	}
	hash, err := uc.hasher.Hash(newPw)
	if err != nil {
		return errs.Internal("gagal memproses sandi baru", err)
	}
	if err := uc.auth.UpdateUserPassword(ctx, userID, hash, false); err != nil {
		return err
	}
	return nil
}

func (uc *AuthUseCase) UpdateNotifyPrefs(ctx context.Context, userID string, prefs auth.NotifyPrefs) error {
	return uc.auth.UpdateUserNotifyPrefs(ctx, userID, prefs)
}

// VerifyAccessClaims validates signature, active user status, and Redis/DB session_version revocation.
func (uc *AuthUseCase) VerifyAccessClaims(ctx context.Context, tokenStr string) (auth.Claims, error) {
	claims, err := uc.tokens.VerifyAccessToken(tokenStr)
	if err != nil {
		return auth.Claims{}, errs.Unauthorized("Token akses tidak valid atau kedaluwarsa.")
	}
	if cachedVer, hit, err := uc.sec.GetSessionVersion(ctx, claims.UserID); err == nil && hit {
		if claims.SessionVersion != cachedVer {
			return auth.Claims{}, errs.Unauthorized("Sesi telah dicabut. Silakan masuk ulang.")
		}
		return claims, nil
	}
	user, err := uc.auth.GetUserByID(ctx, claims.UserID)
	if err != nil || !user.IsActive || claims.SessionVersion != user.SessionVersion {
		return auth.Claims{}, errs.Unauthorized("Sesi tidak lagi berlaku.")
	}
	_ = uc.sec.SetSessionVersion(ctx, user.ID, user.SessionVersion, AccessTokenTTL)
	return claims, nil
}

// HashSHA256 produces deterministic content hashes for TaskDNA caching.
func HashSHA256(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
