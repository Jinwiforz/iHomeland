// Package account 实现第一阶段账号注册、登录和会话恢复边界。
package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ihomeland/server/internal/storage"
)

const (
	defaultSessionTTL = 24 * time.Hour
	passwordHashCost  = bcrypt.DefaultCost
)

var (
	// ErrAccountAlreadyExists 表示注册账号已存在。
	ErrAccountAlreadyExists = errors.New("account already exists")
	// ErrCredentialInvalid 表示账号或密码无效。
	ErrCredentialInvalid = errors.New("account credential invalid")
	// ErrSessionInvalid 表示 session token 无效或已过期。
	ErrSessionInvalid = errors.New("account session invalid")
	// ErrUnauthenticated 表示请求缺少已登录身份。
	ErrUnauthenticated = errors.New("account unauthenticated")
)

// PlayerProfile 描述账号服务返回给协议层的玩家资料。
type PlayerProfile struct {
	PlayerID    string
	Account     string
	DisplayName string
}

// Session 描述账号服务签发的短期会话。
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// AuthResult 描述注册、登录或恢复会话成功后的身份结果。
type AuthResult struct {
	Player  PlayerProfile
	Session Session
}

// RegisterRequest 描述账号注册输入。
type RegisterRequest struct {
	Account      string
	Password     string
	DisplayName  string
	ConnectionID string
}

// LoginRequest 描述账号登录输入。
type LoginRequest struct {
	Account      string
	Password     string
	ConnectionID string
}

// LogoutRequest 描述账号登出输入。
type LogoutRequest struct {
	SessionToken string
}

// ResumeSessionRequest 描述会话恢复输入。
type ResumeSessionRequest struct {
	SessionToken string
	ConnectionID string
}

// Config 描述账号服务依赖。
type Config struct {
	PlayerProfiles storage.PlayerProfileRepository
	Sessions       storage.AccountSessionCache
	SessionTTL     time.Duration
	Now            func() time.Time
	TokenGenerator func() (string, error)
}

// Service 提供第一阶段账号注册、登录、登出和会话恢复能力。
type Service struct {
	players        storage.PlayerProfileRepository
	sessions       storage.AccountSessionCache
	sessionTTL     time.Duration
	now            func() time.Time
	tokenGenerator func() (string, error)
}

// NewService 创建账号服务。
func NewService(cfg Config) (*Service, error) {
	if cfg.PlayerProfiles == nil || cfg.Sessions == nil {
		return nil, storage.ErrInvalidArgument
	}
	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	tokenGenerator := cfg.TokenGenerator
	if tokenGenerator == nil {
		tokenGenerator = generateSessionToken
	}
	return &Service{
		players:        cfg.PlayerProfiles,
		sessions:       cfg.Sessions,
		sessionTTL:     sessionTTL,
		now:            now,
		tokenGenerator: tokenGenerator,
	}, nil
}

// Register 创建账号并签发会话。
func (s *Service) Register(ctx context.Context, req RegisterRequest) (AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return AuthResult{}, err
	}
	accountName, password, displayName, err := normalizeCredentials(req.Account, req.Password, req.DisplayName)
	if err != nil {
		return AuthResult{}, err
	}
	if _, err := s.players.GetPlayerProfileByAccount(ctx, accountName); err == nil {
		return AuthResult{}, ErrAccountAlreadyExists
	} else if !errors.Is(err, storage.ErrNotFound) {
		return AuthResult{}, err
	}

	playerID, err := generatePlayerID()
	if err != nil {
		return AuthResult{}, err
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return AuthResult{}, err
	}
	now := s.now()
	profile := storage.PlayerProfile{
		PlayerID:     playerID,
		AccountName:  accountName,
		PasswordHash: passwordHash,
		DisplayName:  displayName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.players.CreatePlayerProfile(ctx, profile); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return AuthResult{}, ErrAccountAlreadyExists
		}
		return AuthResult{}, err
	}
	return s.issueSession(ctx, profile, req.ConnectionID)
}

// Login 校验账号密码并签发会话。
func (s *Service) Login(ctx context.Context, req LoginRequest) (AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return AuthResult{}, err
	}
	accountName, password, _, err := normalizeCredentials(req.Account, req.Password, "")
	if err != nil {
		return AuthResult{}, err
	}
	profile, err := s.players.GetPlayerProfileByAccount(ctx, accountName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AuthResult{}, ErrCredentialInvalid
		}
		return AuthResult{}, err
	}
	if !verifyPassword(profile.PasswordHash, password) {
		return AuthResult{}, ErrCredentialInvalid
	}
	return s.issueSession(ctx, profile, req.ConnectionID)
}

// Logout 失效账号会话。
func (s *Service) Logout(ctx context.Context, req LogoutRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token := strings.TrimSpace(req.SessionToken)
	if token == "" {
		return ErrSessionInvalid
	}
	return s.sessions.DeleteAccountSession(ctx, token)
}

// ResumeSession 恢复有效账号会话。
func (s *Service) ResumeSession(ctx context.Context, req ResumeSessionRequest) (AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return AuthResult{}, err
	}
	token := strings.TrimSpace(req.SessionToken)
	if token == "" {
		return AuthResult{}, ErrSessionInvalid
	}
	session, err := s.sessions.GetAccountSession(ctx, token)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AuthResult{}, ErrSessionInvalid
		}
		return AuthResult{}, err
	}
	if !session.ExpiresAt.After(s.now()) {
		_ = s.sessions.DeleteAccountSession(ctx, token)
		return AuthResult{}, ErrSessionInvalid
	}
	profile, err := s.players.GetPlayerProfileByID(ctx, session.PlayerID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AuthResult{}, ErrSessionInvalid
		}
		return AuthResult{}, err
	}
	session.ConnectionID = strings.TrimSpace(req.ConnectionID)
	if err := s.sessions.SetAccountSession(ctx, session, session.ExpiresAt.Sub(s.now())); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		Player:  playerFromStorage(profile),
		Session: Session{Token: session.SessionToken, ExpiresAt: session.ExpiresAt},
	}, nil
}

func (s *Service) issueSession(ctx context.Context, profile storage.PlayerProfile, connectionID string) (AuthResult, error) {
	token, err := s.tokenGenerator()
	if err != nil {
		return AuthResult{}, err
	}
	now := s.now()
	expiresAt := now.Add(s.sessionTTL)
	session := storage.AccountSession{
		SessionToken: token,
		PlayerID:     profile.PlayerID,
		AccountName:  profile.AccountName,
		IssuedAt:     now,
		ExpiresAt:    expiresAt,
		ConnectionID: strings.TrimSpace(connectionID),
	}
	if err := s.sessions.SetAccountSession(ctx, session, s.sessionTTL); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		Player:  playerFromStorage(profile),
		Session: Session{Token: token, ExpiresAt: expiresAt},
	}, nil
}

func normalizeCredentials(account string, password string, displayName string) (string, string, string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	password = strings.TrimSpace(password)
	displayName = strings.TrimSpace(displayName)
	if account == "" || password == "" {
		return "", "", "", ErrCredentialInvalid
	}
	if displayName == "" {
		displayName = account
	}
	return account, password, displayName, nil
}

func playerFromStorage(profile storage.PlayerProfile) PlayerProfile {
	return PlayerProfile{
		PlayerID:    profile.PlayerID,
		Account:     profile.AccountName,
		DisplayName: profile.DisplayName,
	}
}

func generatePlayerID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate player id: %w", err)
	}
	return "player-" + hex.EncodeToString(data[:]), nil
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func verifyPassword(passwordHash string, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) == nil
}

func generateSessionToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
