package ops

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"ihomeland/server/internal/infra"
)

const readyCheckTimeout = 800 * time.Millisecond

// DependencyChecker 检查基础设施依赖是否具备处理请求的条件。
type DependencyChecker interface {
	Check(ctx context.Context) []infra.Status
}

// RegisterRoutes 将基础运维接口注册到 Gin engine。
func RegisterRoutes(router gin.IRoutes, paths VersionPaths, checker DependencyChecker, log *slog.Logger) {
	router.GET("/healthz", healthHandler)
	router.GET("/readyz", readyHandler(checker))
	router.GET("/version", versionHandler(paths, log))
}

func healthHandler(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func readyHandler(checker DependencyChecker) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		dependencies := checkDependencies(ctx.Request.Context(), checker)
		statusCode := http.StatusOK
		status := "ready"
		for _, dependency := range dependencies {
			if !dependency.Ready {
				statusCode = http.StatusServiceUnavailable
				status = "not_ready"
				break
			}
		}

		ctx.JSON(statusCode, gin.H{
			"dependencies": dependencies,
			"status":       status,
		})
	}
}

func checkDependencies(ctx context.Context, checker DependencyChecker) []infra.Status {
	if checker == nil {
		return []infra.Status{}
	}
	checkCtx, cancel := context.WithTimeout(ctx, readyCheckTimeout)
	defer cancel()
	return checker.Check(checkCtx)
}

func versionHandler(paths VersionPaths, log *slog.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		version, err := LoadVersion(paths)
		if err != nil {
			log.Error("load version failed", "error", err)
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"error": "VERSION_UNAVAILABLE",
			})
			return
		}
		ctx.JSON(http.StatusOK, version)
	}
}
