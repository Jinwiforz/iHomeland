package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
	"github.com/jinwiforz/ihomeland/server/internal/storage/tlsconfig"
)

// preparedStorage 保存 bootstrap 已解析且尚未转移给 component 的 secret/TLS 内存对象。
type preparedStorage struct {
	// mysqlPassword 只在 MySQL driver config 构造时暴露，随后清零 backing bytes。
	mysqlPassword secret.Value
	// redisPassword 只在 Redis client options 构造时暴露，随后清零 backing bytes。
	redisPassword secret.Value
	// mysqlTLS 是经过 CA/server name 验证且可选包含 mTLS identity 的只读配置。
	mysqlTLS *tls.Config
	// redisTLS 是经过 CA/server name 验证且可选包含 mTLS identity 的只读配置。
	redisTLS *tls.Config
}

// prepareStorage 在 logger、listener 与 client 创建前解析全部 secret 并构造 TLS policy。
func prepareStorage(ctx context.Context, settings config.Storage, provider secret.Provider) (preparedStorage, error) {
	mysqlPassword, err := resolveSecret(ctx, provider, settings.MySQL.PasswordSecret, "storage.mysql.passwordSecret")
	if err != nil {
		return preparedStorage{}, err
	}
	redisPassword := secret.Value{}
	succeeded := false
	defer func() {
		if !succeeded {
			mysqlPassword.Destroy()
			redisPassword.Destroy()
		}
	}()
	redisPassword, err = resolveSecret(ctx, provider, settings.Redis.PasswordSecret, "storage.redis.passwordSecret")
	if err != nil {
		return preparedStorage{}, err
	}
	mysqlKey, err := resolveOptionalSecret(ctx, provider, settings.MySQL.TLS.ClientKeySecret, "storage.mysql.tls.clientKeySecret")
	if err != nil {
		return preparedStorage{}, err
	}
	if mysqlKey != nil {
		defer mysqlKey.Destroy()
	}
	redisKey, err := resolveOptionalSecret(ctx, provider, settings.Redis.TLS.ClientKeySecret, "storage.redis.tls.clientKeySecret")
	if err != nil {
		return preparedStorage{}, err
	}
	if redisKey != nil {
		defer redisKey.Destroy()
	}
	mysqlTLS, err := tlsconfig.Build(settings.MySQL.TLS, mysqlKey)
	if err != nil {
		return preparedStorage{}, fmt.Errorf("prepare storage.mysql.tls: %w", err)
	}
	redisTLS, err := tlsconfig.Build(settings.Redis.TLS, redisKey)
	if err != nil {
		return preparedStorage{}, fmt.Errorf("prepare storage.redis.tls: %w", err)
	}
	succeeded = true
	return preparedStorage{mysqlPassword: mysqlPassword, redisPassword: redisPassword, mysqlTLS: mysqlTLS, redisTLS: redisTLS}, nil
}

// resolveSecret 将配置 reference 转为脱敏 Value，错误使用稳定配置键而不包含 material。
func resolveSecret(ctx context.Context, provider secret.Provider, raw string, key string) (secret.Value, error) {
	reference, err := secret.ParseReference(raw)
	if err != nil {
		return secret.Value{}, fmt.Errorf("%s is invalid", key)
	}
	value, err := provider.Resolve(ctx, reference)
	if err != nil {
		return secret.Value{}, fmt.Errorf("resolve %s: %w", key, err)
	}
	return value, nil
}

// resolveOptionalSecret 只为已配置 mTLS key 访问 provider；空 reference 不产生 I/O。
func resolveOptionalSecret(ctx context.Context, provider secret.Provider, raw string, key string) (*secret.Value, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := resolveSecret(ctx, provider, raw, key)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// validateSecretProvider 防止 Composition Root 在 secret 边界缺失时继续创建资源。
func validateSecretProvider(provider secret.Provider) error {
	if provider == nil {
		return errors.New("secret provider is required")
	}
	return nil
}
