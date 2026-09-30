package veyra

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var (
	ErrSessionMissingAuthorization = errors.New("veyra session: missing authorization")
	ErrSessionInvalidAuthorization = errors.New("veyra session: invalid authorization")
	ErrSessionInvalidToken         = errors.New("veyra session: invalid token")
	ErrSessionExpiredToken         = errors.New("veyra session: expired token")
	ErrSessionUserNotFound         = errors.New("veyra session: user not found")
	ErrSessionInactiveUser         = errors.New("veyra session: inactive user")
	ErrSessionRevokedToken         = errors.New("veyra session: revoked token")
	ErrSessionUnavailable          = errors.New("veyra session: unavailable")
)

type TokenValidator interface {
	ValidateToken(tokenString string) (*service.JWTClaims, error)
}

type SessionUserReader interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
}

type SessionActivityToucher interface {
	TouchLastActiveForUser(ctx context.Context, user *service.User)
}

type SessionAdapter struct {
	Auth     TokenValidator
	Users    SessionUserReader
	Activity SessionActivityToucher
}

type Session struct {
	UserID      int64
	Email       string
	Role        string
	Balance     float64
	Concurrency int
}

func ExtractBearerToken(authHeader string) (string, error) {
	if strings.TrimSpace(authHeader) == "" {
		return "", ErrSessionMissingAuthorization
	}
	parts := strings.SplitN(strings.TrimSpace(authHeader), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrSessionInvalidAuthorization
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", ErrSessionInvalidAuthorization
	}
	return token, nil
}

func (a SessionAdapter) ResolveBearer(ctx context.Context, authHeader string) (*Session, error) {
	token, err := ExtractBearerToken(authHeader)
	if err != nil {
		return nil, err
	}
	return a.ResolveToken(ctx, token)
}

func (a SessionAdapter) ResolveToken(ctx context.Context, token string) (*Session, error) {
	if a.Auth == nil || a.Users == nil {
		return nil, ErrSessionUnavailable
	}
	claims, err := a.Auth.ValidateToken(strings.TrimSpace(token))
	if err != nil {
		if errors.Is(err, service.ErrTokenExpired) {
			return nil, ErrSessionExpiredToken
		}
		return nil, ErrSessionInvalidToken
	}
	user, err := a.Users.GetByID(ctx, claims.UserID)
	if err != nil || user == nil {
		return nil, ErrSessionUserNotFound
	}
	if !user.IsActive() {
		return nil, ErrSessionInactiveUser
	}
	if claims.TokenVersion != user.TokenVersion {
		return nil, ErrSessionRevokedToken
	}
	if a.Activity != nil {
		a.Activity.TouchLastActiveForUser(ctx, user)
	}
	return &Session{
		UserID:      user.ID,
		Email:       user.Email,
		Role:        user.Role,
		Balance:     user.Balance,
		Concurrency: user.Concurrency,
	}, nil
}
