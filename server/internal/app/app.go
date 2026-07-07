// Package app 负责组装服务端运行所需依赖。
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"ihomeland/server/internal/account"
	"ihomeland/server/internal/config"
	"ihomeland/server/internal/gateway"
	"ihomeland/server/internal/infra"
	"ihomeland/server/internal/ops"
	"ihomeland/server/internal/room"
	"ihomeland/server/internal/storage"
)

const dependencyPingTimeout = 5 * time.Second

// NewHTTPServer 创建已注册基础控制面接口的 HTTP server。
func NewHTTPServer(cfg config.Config, log *slog.Logger) (*http.Server, error) {
	deps, err := newRuntimeDependencies(cfg)
	if err != nil {
		return nil, err
	}
	server, err := newHTTPServer(cfg, log, deps)
	if err != nil {
		_ = deps.Close()
		return nil, err
	}
	server.RegisterOnShutdown(func() {
		if err := deps.Close(); err != nil && log != nil {
			log.Warn("close runtime dependencies failed", "error", err)
		}
	})
	return server, nil
}

type serverDependencies struct {
	playerProfiles  storage.PlayerProfileRepository
	accountSessions storage.AccountSessionCache
	closers         []func() error
}

func (d serverDependencies) Close() error {
	var result error
	for _, closeFn := range d.closers {
		if closeFn == nil {
			continue
		}
		if err := closeFn(); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func newHTTPServer(cfg config.Config, log *slog.Logger, deps serverDependencies) (*http.Server, error) {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())

	accountService, err := account.NewService(account.Config{
		PlayerProfiles: deps.playerProfiles,
		Sessions:       deps.accountSessions,
	})
	if err != nil {
		return nil, err
	}
	roomService := room.NewService(room.NewMemoryRepository(), room.Config{})
	dispatcher := newRealtimeDispatcher(newAccountDispatcher(accountService), newRoomDispatcher(roomService))
	gatewayServer, err := gateway.NewServer(gateway.Config{
		IdleTimeout: cfg.Gateway.IdleTimeout,
	}, log, dispatcher)
	if err != nil {
		return nil, err
	}

	ops.RegisterRoutes(router, ops.VersionPaths{
		Release: cfg.ReleasePath,
		Server:  cfg.ServerVersionPath,
		Client:  cfg.ClientVersionPath,
	}, infra.NewTCPChecker(cfg), log)
	gateway.RegisterRoutes(router, gatewayServer)

	return &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}, nil
}

func newRuntimeDependencies(cfg config.Config) (serverDependencies, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dependencyPingTimeout)
	defer cancel()

	db, err := openMySQL(cfg.MySQL)
	if err != nil {
		return serverDependencies{}, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return serverDependencies{}, fmt.Errorf("ping mysql: %w", err)
	}
	playerProfiles, err := storage.NewMySQLPlayerProfileRepository(sqlDBExecutor{db: db})
	if err != nil {
		_ = db.Close()
		return serverDependencies{}, err
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:        cfg.Redis.Addr,
		Password:    cfg.Redis.Password,
		DB:          cfg.Redis.DB,
		DialTimeout: cfg.Redis.DialTimeout,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		_ = db.Close()
		_ = redisClient.Close()
		return serverDependencies{}, fmt.Errorf("ping redis: %w", err)
	}
	keys, err := storage.NewRedisKeys(cfg.Env)
	if err != nil {
		_ = db.Close()
		_ = redisClient.Close()
		return serverDependencies{}, err
	}
	accountSessions, err := storage.NewRedisAccountSessionCache(redisClientAdapter{client: redisClient}, keys)
	if err != nil {
		_ = db.Close()
		_ = redisClient.Close()
		return serverDependencies{}, err
	}

	return serverDependencies{
		playerProfiles:  playerProfiles,
		accountSessions: accountSessions,
		closers: []func() error{
			db.Close,
			redisClient.Close,
		},
	}, nil
}

func openMySQL(cfg config.MySQLConfig) (*sql.DB, error) {
	mysqlCfg := mysql.NewConfig()
	mysqlCfg.Net = "tcp"
	mysqlCfg.Addr = cfg.Addr
	mysqlCfg.User = cfg.User
	mysqlCfg.Passwd = cfg.Password
	mysqlCfg.DBName = cfg.Database
	mysqlCfg.ParseTime = true
	mysqlCfg.Collation = "utf8mb4_unicode_ci"
	mysqlCfg.Loc = time.UTC

	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	return db, nil
}

type redisClientAdapter struct {
	client *redis.Client
}

type sqlDBExecutor struct {
	db *sql.DB
}

func (e sqlDBExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return e.db.ExecContext(ctx, query, args...)
}

func (e sqlDBExecutor) QueryRowContext(ctx context.Context, query string, args ...any) storage.SQLRow {
	return e.db.QueryRowContext(ctx, query, args...)
}

func (a redisClientAdapter) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return a.client.Set(ctx, key, value, ttl).Err()
}

func (a redisClientAdapter) Del(ctx context.Context, key string) error {
	return a.client.Del(ctx, key).Err()
}

func (a redisClientAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := a.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return value, nil
}
