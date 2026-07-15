package httpapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
)

// endpointResponse 是冻结OpenAPI Endpoint投影。
type endpointResponse struct {
	// Channel 是公开协议登记的实时传输枚举。
	Channel string `json:"channel"`
	// Host 是部署配置提供的客户端可见主机名。
	Host string `json:"host"`
	// Port 是客户端连接端口。
	Port uint16 `json:"port"`
}

// errorResponse 是所有失败共用的稳定安全结构。
type errorResponse struct {
	// Code 是errors.json登记的稳定数值错误码。
	Code uint32 `json:"code"`
	// MessageKey 是客户端本地化使用的稳定消息键。
	MessageKey string `json:"messageKey"`
	// RequestID 是本次请求的安全correlation identity。
	RequestID string `json:"requestId"`
	// Retryable 表示相同语义请求稍后是否可能成功。
	Retryable bool `json:"retryable"`
}

// registerRequest 是registerAccount唯一允许的closed JSON object。
type registerRequest struct {
	// Username 是用户选择的登录名。
	Username string `json:"username"`
	// Password 是只传递给Account owner的原始凭据。
	Password string `json:"password"`
	// DisplayName 是玩家公开显示名。
	DisplayName string `json:"displayName"`
}

// loginRequest 是loginAccount唯一允许的closed JSON object。
type loginRequest struct {
	// Username 是用户登录名。
	Username string `json:"username"`
	// Password 是只传递给Account owner的原始凭据。
	Password string `json:"password"`
}

// refreshRequest 是refreshSession唯一允许的closed JSON object。
type refreshRequest struct {
	// RefreshToken 是待轮换的opaque bearer。
	RefreshToken string `json:"refreshToken"`
}

// ticketRequest 是issueConnectionTicket唯一允许的closed JSON object。
type ticketRequest struct {
	// Channel 是客户端选择的已登记实时传输枚举。
	Channel string `json:"channel"`
}

// acceptVisitInviteRequest 是acceptVisitInvite唯一允许的closed JSON object。
type acceptVisitInviteRequest struct {
	// ExpectedRevision 是客户端最后观察到的VisitSession revision。
	ExpectedRevision uint64 `json:"expectedRevision"`
}

// worldAdmissionRequest 是issueWorldAdmission唯一允许的closed JSON object。
type worldAdmissionRequest struct {
	// Kind 选择OWN_WORLD或VISIT_WORLD权威解析路径。
	Kind string `json:"kind"`
	// VisitSessionID 只在VISIT_WORLD时标识待解析的权威membership。
	VisitSessionID string `json:"visitSessionId,omitempty"`
}

// decodeJSON 强制media type、上限、closed object、单值JSON与重复key拒绝。
func decodeJSON(request *http.Request, limit int64, target any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || request.Body == nil || limit <= 0 {
		return errors.New("request body media type is invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || len(bytes.TrimSpace(raw)) == 0 || !utf8.Valid(raw) {
		return errors.New("request body size is invalid")
	}
	if err := rejectInvalidUnicodeEscapes(raw); err != nil {
		return err
	}
	if err := rejectDuplicateObjectKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body schema is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("request body contains trailing JSON")
	}
	return nil
}

// rejectInvalidUnicodeEscapes 拒绝JSON string中的孤立UTF-16 surrogate。
func rejectInvalidUnicodeEscapes(raw []byte) error {
	insideString := false
	for index := 0; index < len(raw); index++ {
		if raw[index] == '"' {
			insideString = !insideString
			continue
		}
		if !insideString || raw[index] != '\\' {
			continue
		}
		index++
		if index >= len(raw) {
			return errors.New("request body contains invalid string escape")
		}
		if raw[index] != 'u' {
			continue
		}
		value, ok := parseHexQuad(raw, index+1)
		if !ok {
			return errors.New("request body contains invalid Unicode escape")
		}
		index += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return errors.New("request body contains isolated Unicode surrogate")
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
			return errors.New("request body contains isolated Unicode surrogate")
		}
		low, validLow := parseHexQuad(raw, index+3)
		if !validLow || low < 0xdc00 || low > 0xdfff {
			return errors.New("request body contains isolated Unicode surrogate")
		}
		index += 6
	}
	return nil
}

// parseHexQuad 解析JSON `\\u`之后的四位ASCII十六进制值。
func parseHexQuad(raw []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(raw) {
		return 0, false
	}
	var value uint16
	for _, character := range raw[start : start+4] {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// rejectDuplicateObjectKeys 遍历JSON token并拒绝任意深度object重复field。
func rejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walkValue func() error
	walkValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("request body is invalid JSON")
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				key, ok := keyToken.(string)
				if keyErr != nil || !ok {
					return errors.New("request body object is invalid")
				}
				if _, exists := seen[key]; exists {
					return errors.New("request body contains duplicate field")
				}
				seen[key] = struct{}{}
				if err := walkValue(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walkValue(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("request body delimiter is invalid")
		}
	}
	if err := walkValue(); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("request body contains trailing value")
	}
	return nil
}

// requireNoBody 拒绝bodyless operation携带任何非空payload。
func requireNoBody(request *http.Request) error {
	if request.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil || len(raw) != 0 {
		return errors.New("operation does not accept a request body")
	}
	return nil
}

// versionProjection 集中编码构建与协议兼容信息。
func versionProjection(serverVersion string, protocolVersion uint32, minimumClientVersion string) map[string]any {
	return map[string]any{"protocolVersion": protocolVersion, "minimumClientVersion": minimumClientVersion, "serverVersion": serverVersion}
}

// bootstrapConfigProjection 集中编码advertised endpoints与公开资源上限。
func bootstrapConfigProjection(endpoints []session.Endpoint, httpBodyBytes int, realtimeFrameBytes int) map[string]any {
	projectedEndpoints := make([]endpointResponse, 0, len(endpoints))
	for _, endpoint := range endpoints {
		projectedEndpoints = append(projectedEndpoints, endpointProjection(endpoint))
	}
	return map[string]any{"endpoints": projectedEndpoints, "limits": map[string]any{"httpBodyBytes": httpBodyBytes, "realtimeFrameBytes": realtimeFrameBytes}}
}

// endpointProjection 将受信session endpoint映射为冻结协议枚举。
func endpointProjection(endpoint session.Endpoint) endpointResponse {
	channel := "WSS"
	if endpoint.Channel() == session.ChannelTLSTCP {
		channel = "TLS_TCP"
	}
	return endpointResponse{Channel: channel, Host: endpoint.Host(), Port: endpoint.Port()}
}

// authProjection 仅编码Account与Session owner允许公开的字段。
func authProjection(result account.AuthResult, endpoints []session.Endpoint) map[string]any {
	projectedEndpoints := make([]endpointResponse, 0, len(endpoints))
	for _, endpoint := range endpoints {
		projectedEndpoints = append(projectedEndpoints, endpointProjection(endpoint))
	}
	return map[string]any{
		"account": map[string]any{"accountId": result.Account.AccountID().String(), "displayName": result.Account.DisplayName().String(), "createdAtMs": unixMilliseconds(result.Account.CreatedAt())},
		"session": map[string]any{"sessionId": result.Session.SessionID.String(), "sessionEpoch": uint64(result.Session.Epoch), "expiresAtMs": unixMilliseconds(result.Session.ExpiresAt)},
		"tokens":  tokenProjection(result.Session.Tokens), "endpoints": projectedEndpoints,
	}
}

// tokenProjection 在最短作用域读取raw token并转换expiry精度。
func tokenProjection(pair session.TokenPair) map[string]any {
	return map[string]any{"accessToken": pair.Access.Reveal(), "refreshToken": pair.Refresh.Reveal(), "accessExpiresAtMs": unixMilliseconds(pair.AccessExpiresAt), "refreshExpiresAtMs": unixMilliseconds(pair.RefreshExpiresAt)}
}

// ticketProjection 使用固定小写hex编码16-byte nonce，精确满足公开ticket字符与长度边界。
func ticketProjection(ticket session.ConnectionTicket) map[string]any {
	nonce := ticket.Nonce.Bytes()
	scopes := make([]string, 0, ticket.Scopes.Len())
	for _, scope := range ticket.Scopes.Values() {
		if scope == session.ScopeControl {
			scopes = append(scopes, "CONTROL")
		} else if scope == session.ScopeGameplay {
			scopes = append(scopes, "GAMEPLAY")
		}
	}
	return map[string]any{"ticket": hex.EncodeToString(nonce[:]), "endpoint": endpointProjection(ticket.Endpoint), "scopes": scopes, "expiresAtMs": unixMilliseconds(ticket.ExpiresAt)}
}

// bootstrapProjection 只公开world与assignment协议白名单字段。
func bootstrapProjection(result worldentry.BootstrapResult) map[string]any {
	world := result.World
	response := map[string]any{"world": map[string]any{"personalWorldId": world.ID().String(), "ownerPlayerId": world.OwnerID().String(), "lifecycle": strings.ToUpper(world.Lifecycle().String()), "revision": uint64(world.Revision()), "createdAtMs": unixMilliseconds(world.CreatedAt())}}
	if result.Assignment.Valid() {
		response["assignment"] = map[string]any{"personalWorldId": result.Assignment.WorldID.String(), "worldInstanceId": result.Assignment.InstanceID, "endpoint": endpointProjection(result.Assignment.Endpoint), "generation": result.Assignment.Generation, "leaseExpiresAtMs": unixMilliseconds(result.Assignment.LeaseExpiresAt)}
	}
	return response
}

// reservationProjection 只公开VisitSession reservation的协议白名单字段。
func reservationProjection(result worldentry.ReservationResult) map[string]any {
	return map[string]any{"reservation": map[string]any{"visitSessionId": result.VisitSessionID.Value(), "revision": uint64(result.Revision), "reservationExpiresAtMs": unixMilliseconds(result.ExpiresAt)}}
}

// admissionProjection 省略binding、assignment、session lineage与幂等identity。
func admissionProjection(result worldentry.AdmissionResult) map[string]any {
	return map[string]any{"credential": result.Credential.Value(), "endpoint": endpointProjection(result.Endpoint), "role": strings.ToUpper(result.Role.String()), "purpose": strings.ToUpper(result.Purpose.String()), "expiresAtMs": unixMilliseconds(result.ExpiresAt)}
}

// unixMilliseconds 按协议要求确定性向下转换UTC微秒时间。
func unixMilliseconds(value time.Time) int64 { return value.UTC().UnixMilli() }
