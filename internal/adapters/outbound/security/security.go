package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"

	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
)

// RealClock implements port.Clock using system UTC time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// Argon2Hasher hashes passwords using Argon2id tuned for high concurrency.
type Argon2Hasher struct{}

func (Argon2Hasher) Hash(plain string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		iters  = 1
		memKB  = 16 * 1024
		thread = 1
		keyLen = 32
	)
	key := argon2.IDKey([]byte(plain), salt, iters, memKB, thread, keyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s",
		iters, memKB, thread,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (Argon2Hasher) Verify(plain, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	var iters, memKB, thread uint32
	if _, err := fmt.Sscanf(parts[1]+" "+parts[2]+" "+parts[3], "%d %d %d", &iters, &memKB, &thread); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	wantKey, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	gotKey := argon2.IDKey([]byte(plain), salt, iters, memKB, uint8(thread), uint32(len(wantKey)))
	return subtle.ConstantTimeCompare(gotKey, wantKey) == 1
}

type jwtPayload struct {
	UserID         string    `json:"uid"`
	OrganizationID string    `json:"oid"`
	MemberID       string    `json:"mid,omitempty"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           auth.Role `json:"role"`
	SessionVersion int64     `json:"sv"`
	jwt.RegisteredClaims
}

// JWTTokenService issues HS256 access tokens and cryptographically random opaque tokens.
type JWTTokenService struct {
	secret []byte
}

func NewJWTTokenService(secret string) *JWTTokenService {
	return &JWTTokenService{secret: []byte(secret)}
}

func (s *JWTTokenService) IssueAccessToken(c auth.Claims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	payload := jwtPayload{
		UserID:         c.UserID,
		OrganizationID: c.OrganizationID,
		MemberID:       c.MemberID,
		Email:          c.Email,
		Name:           c.Name,
		Role:           c.Role,
		SessionVersion: c.SessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   c.UserID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "pantara-back",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, payload)
	return tok.SignedString(s.secret)
}

func (s *JWTTokenService) VerifyAccessToken(tokenStr string) (auth.Claims, error) {
	var payload jwtPayload
	parsed, err := jwt.ParseWithClaims(tokenStr, &payload, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil || !parsed.Valid {
		return auth.Claims{}, fmt.Errorf("invalid token: %w", err)
	}
	return auth.Claims{
		UserID:         payload.UserID,
		OrganizationID: payload.OrganizationID,
		MemberID:       payload.MemberID,
		Email:          payload.Email,
		Name:           payload.Name,
		Role:           payload.Role,
		SessionVersion: payload.SessionVersion,
	}, nil
}

func (s *JWTTokenService) NewOpaqueToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, s.HashOpaqueToken(raw), nil
}

func (s *JWTTokenService) HashOpaqueToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
