package ops

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 将基础运维接口注册到 Gin engine。
func RegisterRoutes(router gin.IRoutes, paths VersionPaths, log *slog.Logger) {
	router.GET("/healthz", healthHandler)
	router.GET("/readyz", readyHandler)
	router.GET("/version", versionHandler(paths, log))
}

func healthHandler(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func readyHandler(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status": "ready",
	})
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
