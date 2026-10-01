package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	accessTokenTTL  = 30 * time.Minute
	refreshTokenTTL = 7 * 24 * time.Hour
)

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RefreshInput struct {
	RefreshToken string `json:"refreshToken"`
}

type AuthenticatedUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

type Session struct {
	AccessToken  string             `json:"accessToken"`
	RefreshToken string             `json:"refreshToken"`
	TokenType    string             `json:"tokenType"`
	ExpiresIn    int                `json:"expiresIn"`
	User         *AuthenticatedUser `json:"user,omitempty"`
}

type AuthUsecase interface {
	Login(ctx context.Context, input Credentials) (*Session, error)
	Refresh(ctx context.Context, input RefreshInput) (*Session, error)
	Logout(ctx context.Context, userID, sessionID string, input RefreshInput) error
}

type authUsecase struct {
	repo  AuthRepository
	token contract.Token
}

func NewAuthUsecase(repo AuthRepository, token contract.Token) AuthUsecase {
	return &authUsecase{repo: repo, token: token}
}

func (u *authUsecase) Login(ctx context.Context, input Credentials) (*Session, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	var details []FieldError
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		details = append(details, FieldError{Field: "email", Message: "A valid email is required."})
	}
	if strings.TrimSpace(input.Password) == "" {
		details = append(details, FieldError{Field: "password", Message: "Password is required."})
	}
	if len(details) != 0 {
		return nil, validationError(details)
	}
	user, err := u.repo.FindActiveUserByEmail(ctx, email)
	if err != nil {
		return nil, normalizeAuthError(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		return nil, errs.ErrUnauthorized
	}
	return u.newSession(ctx, user)
}

func (u *authUsecase) Refresh(ctx context.Context, input RefreshInput) (*Session, error) {
	if strings.TrimSpace(input.RefreshToken) == "" {
		return nil, validationError([]FieldError{{Field: "refreshToken", Message: "Refresh token is required."}})
	}
	oldHash := hashToken(input.RefreshToken)
	now := time.Now().UTC()
	user, old, err := u.repo.FindUserByRefreshToken(ctx, oldHash, now)
	if err != nil {
		return nil, normalizeAuthError(err)
	}
	raw, err := newRefreshToken()
	if err != nil {
		return nil, errs.Internal("could not issue a session")
	}
	session, err := u.session(user, old.SessionID, raw, false)
	if err != nil {
		return nil, err
	}
	replacement := &RefreshToken{SessionID: old.SessionID, TokenHash: hashToken(raw), ExpiresAt: now.Add(refreshTokenTTL)}
	if err := u.repo.RotateRefreshToken(ctx, oldHash, user, replacement, now); err != nil {
		return nil, normalizeAuthError(err)
	}
	return session, nil
}

func (u *authUsecase) Logout(ctx context.Context, userID, sessionID string, input RefreshInput) error {
	if strings.TrimSpace(input.RefreshToken) == "" {
		return validationError([]FieldError{{Field: "refreshToken", Message: "Refresh token is required."}})
	}
	if err := u.repo.RevokeSession(ctx, hashToken(input.RefreshToken), userID, sessionID, time.Now().UTC()); err != nil {
		return normalizeAuthError(err)
	}
	return nil
}

func (u *authUsecase) newSession(ctx context.Context, user *User) (*Session, error) {
	raw, err := newRefreshToken()
	if err != nil {
		return nil, errs.Internal("could not issue a session")
	}
	sessionID := uuid.NewString()
	session, err := u.session(user, sessionID, raw, true)
	if err != nil {
		return nil, err
	}
	if err := u.repo.CreateSession(ctx,
		&AuthSession{ID: sessionID, UserID: user.ID, TenantID: user.TenantID},
		&RefreshToken{UserID: user.ID, SessionID: sessionID, TokenHash: hashToken(raw), ExpiresAt: time.Now().UTC().Add(refreshTokenTTL)},
	); err != nil {
		return nil, errs.Internal("could not issue a session")
	}
	return session, nil
}

func (u *authUsecase) session(user *User, sessionID, refreshToken string, includeUser bool) (*Session, error) {
	if !user.Role.Valid() || user.ID == "" || user.TenantID == "" {
		return nil, errs.Internal("account is not configured for authentication")
	}
	accessToken, err := u.token.Sign(contract.Claims{
		claimUserID: user.ID, claimTenantID: user.TenantID, claimRole: string(user.Role), claimSessionID: sessionID,
	}, accessTokenTTL)
	if err != nil {
		return nil, errs.Internal("could not issue a session")
	}
	session := &Session{
		AccessToken: accessToken, RefreshToken: refreshToken,
		TokenType: "Bearer", ExpiresIn: int(accessTokenTTL.Seconds()),
	}
	if includeUser {
		session.User = &AuthenticatedUser{ID: user.ID, Email: user.Email, Role: user.Role}
	}
	return session, nil
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func validationError(details []FieldError) error {
	err := errs.New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", "The request is invalid.")
	err.Data = details
	return err
}

func normalizeAuthError(err error) error {
	if appErr, ok := errs.IsAppError(err); ok && appErr.Code == "UNAUTHORIZED" {
		return errs.ErrUnauthorized
	}
	return err
}

func newRefreshToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
