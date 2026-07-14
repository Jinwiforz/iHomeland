package account

import (
	"errors"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/account"
)

// rowScanner 是 *sql.Row 与测试 scanner 共享的最窄 hydration 边界。
type rowScanner interface {
	// Scan 按 query 声明顺序复制完整 row；缺失或类型错误必须返回失败。
	Scan(...any) error
}

// scanAuthentication 恢复 repository 返回的完整且严格认证记录。
func scanAuthentication(scanner rowScanner) (domain.AuthenticationRecord, error) {
	var accountID string
	var playerID string
	var username string
	var displayName string
	var credential string
	var status string
	var createdAt time.Time
	if err := scanner.Scan(&accountID, &playerID, &username, &displayName, &credential, &status, &createdAt); err != nil {
		return domain.AuthenticationRecord{}, err
	}
	return hydrateAuthentication(accountID, playerID, username, displayName, credential, status, createdAt)
}

// hydrateAuthentication 使用 domain constructors 拒绝非法 identity、文本、状态、PHC 与时间。
func hydrateAuthentication(accountValue string, playerValue string, usernameValue string, displayValue string, credentialValue string, statusValue string, createdAt time.Time) (domain.AuthenticationRecord, error) {
	if createdAt.IsZero() {
		return domain.AuthenticationRecord{}, errors.New("persisted account creation time is invalid")
	}
	_, offset := createdAt.Zone()
	if offset != 0 {
		return domain.AuthenticationRecord{}, errors.New("persisted account creation time is not UTC")
	}
	accountID, err := domain.NewAccountID(accountValue)
	if err != nil {
		return domain.AuthenticationRecord{}, err
	}
	playerID, err := domain.NewPlayerID(playerValue)
	if err != nil {
		return domain.AuthenticationRecord{}, err
	}
	username, err := domain.NewUsername(usernameValue)
	if err != nil || username.String() != usernameValue {
		return domain.AuthenticationRecord{}, errors.New("persisted username is not canonical")
	}
	displayName, err := domain.NewDisplayName(displayValue)
	if err != nil || displayName.String() != displayValue {
		return domain.AuthenticationRecord{}, errors.New("persisted display name is not normalized")
	}
	credential, err := domain.NewCredentialHash(credentialValue)
	if err != nil || validatePHC(credentialValue) != nil {
		return domain.AuthenticationRecord{}, errors.New("persisted credential hash is invalid")
	}
	status, err := decodeAccountStatus(statusValue)
	if err != nil {
		return domain.AuthenticationRecord{}, err
	}
	account, err := domain.NewAccount(accountID, playerID, username, displayName, status, createdAt)
	if err != nil {
		return domain.AuthenticationRecord{}, err
	}
	return domain.AuthenticationRecord{Account: account, Credential: credential}, nil
}

// encodeAccountStatus 映射封闭 domain status，zero/unknown 不进入 SQL。
func encodeAccountStatus(status domain.Status) (string, error) {
	switch status {
	case domain.StatusActive:
		return "active", nil
	case domain.StatusInactive:
		return "inactive", nil
	default:
		return "", errors.New("account status is unknown")
	}
}

// decodeAccountStatus 拒绝未来或损坏枚举，不提供 active fallback。
func decodeAccountStatus(value string) (domain.Status, error) {
	switch value {
	case "active":
		return domain.StatusActive, nil
	case "inactive":
		return domain.StatusInactive, nil
	default:
		return domain.StatusUnspecified, errors.New("persisted account status is unknown")
	}
}
