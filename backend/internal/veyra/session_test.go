package veyra

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type sessionAuthStub struct {
	claims *service.JWTClaims
	err    error
}

func (s sessionAuthStub) ValidateToken(string) (*service.JWTClaims, error) {
	return s.claims, s.err
}

type sessionUserStub struct {
	user *service.User
	err  error
}

func (s sessionUserStub) GetByID(context.Context, int64) (*service.User, error) {
	return s.user, s.err
}

type sessionTouchStub struct {
	count int
}

func (s *sessionTouchStub) TouchLastActiveForUser(context.Context, *service.User) {
	s.count++
}

func TestExtractBearerToken(t *testing.T) {
	token, err := ExtractBearerToken("Bearer abc.def")
	require.NoError(t, err)
	require.Equal(t, "abc.def", token)

	_, err = ExtractBearerToken("")
	require.ErrorIs(t, err, ErrSessionMissingAuthorization)

	_, err = ExtractBearerToken("Basic abc")
	require.ErrorIs(t, err, ErrSessionInvalidAuthorization)

	_, err = ExtractBearerToken("Bearer ")
	require.ErrorIs(t, err, ErrSessionInvalidAuthorization)
}

func TestSessionAdapterResolveBearer(t *testing.T) {
	touch := &sessionTouchStub{}
	adapter := SessionAdapter{
		Auth: sessionAuthStub{claims: &service.JWTClaims{
			UserID:       42,
			Email:        "from-token@example.com",
			Role:         service.RoleUser,
			TokenVersion: 7,
		}},
		Users: sessionUserStub{user: &service.User{
			ID:           42,
			Email:        "fresh@example.com",
			Role:         service.RoleUser,
			Balance:      12.5,
			Concurrency:  3,
			Status:       service.StatusActive,
			TokenVersion: 7,
		}},
		Activity: touch,
	}

	session, err := adapter.ResolveBearer(context.Background(), "Bearer token-value")
	require.NoError(t, err)
	require.Equal(t, int64(42), session.UserID)
	require.Equal(t, "fresh@example.com", session.Email)
	require.Equal(t, 12.5, session.Balance)
	require.Equal(t, 3, session.Concurrency)
	require.Equal(t, 1, touch.count)
}

func TestSessionAdapterRejectsInvalidStates(t *testing.T) {
	tests := []struct {
		name string
		auth TokenValidator
		user SessionUserReader
		want error
	}{
		{
			name: "missing dependencies",
			want: ErrSessionUnavailable,
		},
		{
			name: "expired token",
			auth: sessionAuthStub{err: service.ErrTokenExpired},
			user: sessionUserStub{},
			want: ErrSessionExpiredToken,
		},
		{
			name: "invalid token",
			auth: sessionAuthStub{err: service.ErrInvalidToken},
			user: sessionUserStub{},
			want: ErrSessionInvalidToken,
		},
		{
			name: "user not found",
			auth: sessionAuthStub{claims: &service.JWTClaims{UserID: 1, TokenVersion: 1}},
			user: sessionUserStub{err: errors.New("not found")},
			want: ErrSessionUserNotFound,
		},
		{
			name: "inactive user",
			auth: sessionAuthStub{claims: &service.JWTClaims{UserID: 1, TokenVersion: 1}},
			user: sessionUserStub{user: &service.User{ID: 1, Status: "disabled", TokenVersion: 1}},
			want: ErrSessionInactiveUser,
		},
		{
			name: "revoked token",
			auth: sessionAuthStub{claims: &service.JWTClaims{UserID: 1, TokenVersion: 1}},
			user: sessionUserStub{user: &service.User{ID: 1, Status: service.StatusActive, TokenVersion: 2}},
			want: ErrSessionRevokedToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := SessionAdapter{Auth: tt.auth, Users: tt.user}
			_, err := adapter.ResolveToken(context.Background(), "token")
			require.ErrorIs(t, err, tt.want)
		})
	}
}
