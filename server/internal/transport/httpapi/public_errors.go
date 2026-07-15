package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// SafeErrorKind 是共享public listener在upgrade前允许公开的封闭错误集合。
type SafeErrorKind uint8

const (
	// SafeErrorValidation 表示请求结构或握手契约无效。
	SafeErrorValidation SafeErrorKind = iota + 1
	// SafeErrorUnauthenticated 表示ticket缺失、失效、过期或已消费。
	SafeErrorUnauthenticated
	// SafeErrorForbidden 表示已认证资格不允许目标操作。
	SafeErrorForbidden
	// SafeErrorRateLimited 表示pre-auth资源预算耗尽。
	SafeErrorRateLimited
	// SafeErrorDependencyUnavailable 表示权威依赖暂不可用。
	SafeErrorDependencyUnavailable
)

// WriteSafeError 复用HTTP ErrorResponse编码upgrade前失败，不回显backend cause。
func WriteSafeError(response http.ResponseWriter, request *http.Request, kind SafeErrorKind) {
	item := validationError
	switch kind {
	case SafeErrorUnauthenticated:
		item = unauthenticated
	case SafeErrorForbidden:
		item = forbidden
	case SafeErrorRateLimited:
		item = rateLimited
	case SafeErrorDependencyUnavailable:
		item = dependencyError
	}
	requestID := request.Header.Get("X-Request-ID")
	if !safeRequestID(requestID) {
		var material [16]byte
		if _, err := rand.Read(material[:]); err != nil {
			requestID = "request-id-unavailable"
		} else {
			requestID = hex.EncodeToString(material[:])
		}
	}
	response.Header().Set("X-Request-ID", requestID)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(item.Status)
	_ = json.NewEncoder(response).Encode(errorResponse{Code: item.Code, MessageKey: item.MessageKey, RequestID: requestID, Retryable: item.Retryable})
}
