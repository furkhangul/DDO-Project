package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"konakpro/internal/auditlog"
	"konakpro/internal/db"
	"konakpro/internal/platform/apperr"
	"konakpro/internal/platform/store"
)

type Service struct {
	Store      *store.Store
	Secret     []byte
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Now        func() time.Time
	decoy      string
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type Claims struct {
	Role string `json:"role"`
	Name string `json:"name"`
	jwt.RegisteredClaims
}

type Principal struct {
	UserID int64
	Role   db.UserRole
	Name   string
}

func (p Principal) Actor() auditlog.Actor {
	id := p.UserID
	return auditlog.Actor{ID: &id, Label: p.Name}
}

func New(s *store.Store, secret string, accessTTL, refreshTTL time.Duration) *Service {
	decoy, err := HashPassword(uuid.NewString())
	if err != nil {
		panic(err)
	}
	return &Service{Store: s, Secret: []byte(secret), AccessTTL: accessTTL, RefreshTTL: refreshTTL, Now: time.Now, decoy: decoy}
}

var errBadCredentials = apperr.Unauthorized("E-posta veya parola hatalı")

func (s *Service) Login(ctx context.Context, email, password string) (Tokens, db.User, error) {
	user, err := s.Store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if store.IsNotFound(err) {
		_, _ = VerifyPassword(s.decoy, password)
		return Tokens{}, db.User{}, errBadCredentials
	}
	if err != nil {
		return Tokens{}, db.User{}, err
	}
	ok, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return Tokens{}, db.User{}, err
	}
	if !ok {
		return Tokens{}, db.User{}, errBadCredentials
	}
	tokens, err := s.issue(ctx, s.Store.Queries, user, uuid.New())
	return tokens, user, err
}

func (s *Service) Refresh(ctx context.Context, raw string) (Tokens, error) {
	var tokens Tokens
	var reused bool
	var family uuid.UUID
	err := s.Store.InTx(ctx, func(q *db.Queries) error {
		row, err := q.LockRefreshToken(ctx, hashToken(raw))
		if store.IsNotFound(err) {
			return apperr.Unauthorized("Oturum yenilenemedi")
		}
		if err != nil {
			return err
		}
		if row.RevokedAt != nil || s.Now().After(row.ExpiresAt) {
			return apperr.Unauthorized("Oturum süresi doldu")
		}
		if row.UsedAt != nil {
			reused, family = true, row.FamilyID
			return nil
		}
		user, err := q.GetUserByID(ctx, row.UserID)
		if err != nil {
			return apperr.Unauthorized("Kullanıcı etkin değil")
		}
		if err := q.MarkRefreshTokenUsed(ctx, row.ID); err != nil {
			return err
		}
		tokens, err = s.issue(ctx, q, user, row.FamilyID)
		return err
	})
	if err != nil {
		return Tokens{}, err
	}
	if reused {
		if err := s.Store.RevokeRefreshFamily(ctx, family); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, apperr.Unauthorized("Oturum yeniden kullanım nedeniyle kapatıldı")
	}
	return tokens, nil
}

func (s *Service) Logout(ctx context.Context, q *db.Queries, raw string) error {
	row, err := q.LockRefreshToken(ctx, hashToken(raw))
	if store.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return q.RevokeRefreshFamily(ctx, row.FamilyID)
}

func (s *Service) issue(ctx context.Context, q *db.Queries, user db.User, family uuid.UUID) (Tokens, error) {
	now := s.Now()
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Role: string(user.Role),
		Name: user.FullName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(user.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.AccessTTL)),
		},
	}).SignedString(s.Secret)
	if err != nil {
		return Tokens{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Tokens{}, err
	}
	refresh := base64.RawURLEncoding.EncodeToString(raw)
	if err := q.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		UserID: user.ID, FamilyID: family, TokenHash: hashToken(refresh), ExpiresAt: now.Add(s.RefreshTTL),
	}); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(s.AccessTTL.Seconds())}, nil
}

func (s *Service) Parse(token string) (Principal, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("beklenmeyen imza yöntemi")
		}
		return s.Secret, nil
	}, jwt.WithTimeFunc(s.Now))
	if err != nil || !parsed.Valid {
		return Principal{}, apperr.Unauthorized("Oturum geçersiz veya süresi dolmuş")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return Principal{}, apperr.Unauthorized("Oturum geçersiz")
	}
	return Principal{UserID: id, Role: db.UserRole(claims.Role), Name: claims.Name}, nil
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
