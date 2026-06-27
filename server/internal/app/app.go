// Package app 负责组装服务端运行所需依赖。
package app

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"ihomeland/server/internal/config"
	"ihomeland/server/internal/ops"
)

// NewHTTPServer 创建已注册基础控制面接口的 HTTP server。
func NewHTTPServer(cfg config.Config, log *slog.Logger) (*http.Server, error) {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())

	ops.RegisterRoutes(router, ops.VersionPaths{
		Release: cfg.ReleasePath,
		Server:  cfg.ServerVersionPath,
		Client:  cfg.ClientVersionPath,
	}, log)

	return &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
	}, nil
}
