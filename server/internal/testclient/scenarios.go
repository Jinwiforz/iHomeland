package testclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
)

// ScenarioRuntime 是场景可见的公开网络入口与证据状态。
type ScenarioRuntime struct {
	// RepositoryRoot 是读取 committed contract sources 的绝对仓库根。
	RepositoryRoot string
	// HTTP 是只连接独立 cmd/server 的严格公开 API client。
	HTTP *HTTPClient
	// TLSConfig 只信任本次资格运行的临时 CA，并固定 TLS 1.3。
	TLSConfig *tls.Config
	// Bootstrap 是从 GET /v1/config 读取的 endpoint/limit 快照。
	Bootstrap BootstrapProjection
	// Evidence 是分层 gate 已成功产生的稳定证据 ID 集合。
	Evidence map[string]bool
	// Faults 通过资格入口请求 owner 校验的封闭 process/storage 故障。
	Faults *FileFaultController
}

// ScenarioFunction 执行一个 manifest ID 对应的唯一资格场景。
type ScenarioFunction func(context.Context, *ScenarioRuntime) error

// accountCredential 把 actor 与登录所需材料限制在单场景调用栈。
type accountCredential struct {
	// actor 保存当前 session 和公开 identity。
	actor *Actor
	// username 是只在恢复/重新登录场景使用的随机测试账号名。
	username string
	// password 默认脱敏并在场景结束清除。
	password *Secret
}

// runAuthSessionLifecycle 验证 register/login/refresh/config/logout 与旧 bearer 失效。
func runAuthSessionLifecycle(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	password, err := account.password.Reveal()
	if err != nil {
		return err
	}
	login, err := runtime.HTTP.Login(ctx, LoginRequest{Username: account.username, Password: password})
	if err != nil || login.Account.AccountID != account.actor.AccountID || login.Session.SessionID == account.actor.SessionID {
		return errors.New("login did not create a distinct session lineage")
	}
	account.actor.ClearCredentials()
	if err := applyAuthResponse(account.actor, &login); err != nil {
		return err
	}
	rotated, err := runtime.HTTP.Refresh(ctx, account.actor.RefreshToken)
	if err != nil {
		return err
	}
	oldAccess := account.actor.AccessToken
	oldRefresh := account.actor.RefreshToken
	newAccess, err := NewSecret(rotated.AccessToken)
	rotated.AccessToken = ""
	if err != nil {
		return err
	}
	newRefresh, err := NewSecret(rotated.RefreshToken)
	rotated.RefreshToken = ""
	if err != nil {
		newAccess.Clear()
		return err
	}
	account.actor.AccessToken, account.actor.RefreshToken = newAccess, newRefresh
	oldAccess.Clear()
	oldRefresh.Clear()
	if err := runtime.HTTP.Logout(ctx, account.actor.AccessToken); err != nil {
		return err
	}
	if _, err := runtime.HTTP.WorldBootstrap(ctx, account.actor.AccessToken); err == nil {
		return errors.New("logout left old bearer authorized")
	}
	return nil
}

// runWSSSessionInvalidation 验证 WSS ticket/subprotocol 后 logout 定向推送 504 并关闭旧 session。
func runWSSSessionInvalidation(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	connection, err := dialScenarioWSS(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(connection); err != nil {
		_ = connection.Close()
		return err
	}
	if err := runtime.HTTP.Logout(ctx, account.actor.AccessToken); err != nil {
		return err
	}
	message, err := waitMessage(ctx, connection.Messages(), 504)
	if err != nil {
		return err
	}
	push, ok := message.Payload.(*controlv1.SessionInvalidatedPush)
	if !ok || push.GetSessionEpoch() <= account.actor.SessionEpoch || push.GetReasonKey() == "" {
		return errors.New("WSS session invalidation push is incomplete")
	}
	select {
	case terminalErr := <-connection.Terminal():
		if websocket.CloseStatus(terminalErr) != websocket.StatusPolicyViolation {
			return errors.New("WSS session invalidation did not close with policy violation")
		}
	case <-ctx.Done():
		return errors.New("WSS session invalidation close timed out")
	}
	return nil
}

// runOwnWorldBootstrap 验证注册账号首次创建并重复解析同一 PersonalWorld/assignment。
func runOwnWorldBootstrap(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	first, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil || first.Assignment == nil {
		return errors.New("first own-world bootstrap omitted assignment")
	}
	second, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	if first.World.PersonalWorldID != second.World.PersonalWorldID || first.World.OwnerPlayerID != second.World.OwnerPlayerID || first.Assignment.WorldInstanceID != second.Assignment.WorldInstanceID || first.Assignment.Generation != second.Assignment.Generation {
		return errors.New("repeated own-world bootstrap changed current identity")
	}
	return nil
}

// runOwnWorldConcurrentBootstrap 验证并发 bootstrap 收敛为一个持久世界和 current assignment。
func runOwnWorldConcurrentBootstrap(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	const requests = 8
	results := make(chan WorldBootstrapResponse, requests)
	errorsChannel := make(chan error, requests)
	var group sync.WaitGroup
	for index := 0; index < requests; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			response, requestErr := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
			if requestErr != nil {
				errorsChannel <- requestErr
				return
			}
			results <- response
		}()
	}
	group.Wait()
	close(results)
	close(errorsChannel)
	if err := <-errorsChannel; err != nil {
		return err
	}
	var baseline WorldBootstrapResponse
	for result := range results {
		if baseline.World.PersonalWorldID == "" {
			baseline = result
			continue
		}
		if result.World.PersonalWorldID != baseline.World.PersonalWorldID || result.Assignment == nil || baseline.Assignment == nil || result.Assignment.WorldInstanceID != baseline.Assignment.WorldInstanceID || result.Assignment.Generation != baseline.Assignment.Generation {
			return errors.New("concurrent own-world bootstrap did not converge")
		}
	}
	return nil
}

// runOwnWorldAdmissionSnapshot 验证 ticket/admission 单次握手后的 TCP world snapshot 与 HTTP target 一致。
func runOwnWorldAdmissionSnapshot(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	bootstrap, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	client, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(client); err != nil {
		_ = client.Close()
		return err
	}
	message, err := client.Request(ctx, 2000, new(worldv1.WorldSnapshotRequest))
	if err != nil {
		return err
	}
	response, ok := message.Payload.(*worldv1.WorldSnapshotResponse)
	if !ok || response.GetSnapshot() == nil || response.GetSnapshot().GetWorld() == nil || response.GetSnapshot().GetWorld().GetPersonalWorldId() != bootstrap.World.PersonalWorldID {
		return errors.New("TCP world snapshot target drifted from HTTP bootstrap")
	}
	return nil
}

// registerScenarioActor 创建随机账号并把 raw token 转移到默认脱敏 actor 容器。
func registerScenarioActor(scenario *ScenarioContext, runtime *ScenarioRuntime) (accountCredential, error) {
	if scenario == nil || runtime == nil || runtime.HTTP == nil {
		return accountCredential{}, errors.New("qualification scenario runtime is incomplete")
	}
	actor, err := scenario.AddActor()
	if err != nil {
		return accountCredential{}, err
	}
	identity, err := NewIdentity("user", 8)
	if err != nil {
		return accountCredential{}, err
	}
	passwordText, err := NewIdentity("password", 16)
	if err != nil {
		return accountCredential{}, err
	}
	password, err := NewSecret(passwordText)
	if err != nil {
		return accountCredential{}, err
	}
	if err := scenario.Track(password); err != nil {
		password.Clear()
		return accountCredential{}, err
	}
	response, err := runtime.HTTP.Register(scenario, RegisterRequest{Username: identity, Password: passwordText, DisplayName: "资格玩家"})
	if err != nil {
		password.Clear()
		return accountCredential{}, err
	}
	if err := applyAuthResponse(actor, &response); err != nil {
		password.Clear()
		return accountCredential{}, err
	}
	return accountCredential{actor: actor, username: identity, password: password}, nil
}

// applyAuthResponse 把 HTTP raw token 转移到 actor，并立即清空 DTO 中可控的 credential 引用。
func applyAuthResponse(actor *Actor, response *AuthResponse) error {
	if actor == nil || response == nil {
		return errors.New("qualification auth response target is invalid")
	}
	access, err := NewSecret(response.Tokens.AccessToken)
	response.Tokens.AccessToken = ""
	if err != nil {
		return err
	}
	refresh, err := NewSecret(response.Tokens.RefreshToken)
	response.Tokens.RefreshToken = ""
	if err != nil {
		access.Clear()
		return err
	}
	actor.AccountID = response.Account.AccountID
	actor.SessionID = response.Session.SessionID
	actor.SessionEpoch = response.Session.SessionEpoch
	actor.AccessToken = access
	actor.RefreshToken = refresh
	return nil
}

// dialScenarioWSS 从 ticket response 精确使用 advertised endpoint 建立 control connection。
func dialScenarioWSS(ctx context.Context, runtime *ScenarioRuntime, actor *Actor) (*WSSClient, error) {
	ticketResponse, err := runtime.HTTP.IssueTicket(ctx, actor.AccessToken, TicketRequest{Channel: "WSS"})
	if err != nil {
		return nil, err
	}
	if !endpointAdvertised(runtime.Bootstrap, ticketResponse.Endpoint) {
		return nil, errors.New("WSS ticket endpoint was not advertised by public config")
	}
	ticket, err := NewSecret(ticketResponse.Ticket)
	ticketResponse.Ticket = ""
	if err != nil {
		return nil, err
	}
	return DialWSS(ctx, ticketResponse.Endpoint, ticket, runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
}

// dialOwnWorldTCP 签发独立 TLS_TCP ticket 与 OWN_WORLD admission 后建立 gameplay connection。
func dialOwnWorldTCP(ctx context.Context, runtime *ScenarioRuntime, actor *Actor) (*TCPClient, error) {
	ticketResponse, err := runtime.HTTP.IssueTicket(ctx, actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return nil, err
	}
	idempotencyKey, err := NewIdentity("admission", 16)
	if err != nil {
		return nil, err
	}
	admissionResponse, err := runtime.HTTP.IssueWorldAdmission(ctx, actor.AccessToken, idempotencyKey, WorldAdmissionRequest{Kind: "OWN_WORLD"})
	if err != nil {
		return nil, err
	}
	if !sameEndpoint(ticketResponse.Endpoint, admissionResponse.Endpoint) || !endpointAdvertised(runtime.Bootstrap, ticketResponse.Endpoint) {
		return nil, errors.New("OWN_WORLD ticket/admission endpoint binding drifted")
	}
	ticket, admission, err := newCredentialPair(ticketResponse.Ticket, admissionResponse.Credential)
	ticketResponse.Ticket = ""
	admissionResponse.Credential = ""
	if err != nil {
		return nil, err
	}
	return DialTCP(ctx, admissionResponse.Endpoint, ticket, admission, "OWN_WORLD", runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
}

// newCredentialPair 把同一次 realtime handshake 的 raw ticket/admission 一并转入默认脱敏容器。
func newCredentialPair(ticketValue, admissionValue string) (*Secret, *Secret, error) {
	ticket, err := NewSecret(ticketValue)
	if err != nil {
		return nil, nil, err
	}
	admission, err := NewSecret(admissionValue)
	if err != nil {
		ticket.Clear()
		return nil, nil, err
	}
	return ticket, admission, nil
}

// waitMessage 等待 stream 中精确 message ID，忽略同 channel 的其他合法 push。
func waitMessage(ctx context.Context, stream <-chan DecodedRealtimeMessage, messageID uint32) (DecodedRealtimeMessage, error) {
	for {
		select {
		case message, open := <-stream:
			if !open {
				return DecodedRealtimeMessage{}, errors.New("qualification realtime stream closed before expected message")
			}
			if message.MessageID == messageID {
				return message, nil
			}
		case <-ctx.Done():
			return DecodedRealtimeMessage{}, context.Cause(ctx)
		}
	}
}

// endpointAdvertised 检查 runtime endpoint 只来自公开 bootstrap config。
func endpointAdvertised(bootstrap BootstrapProjection, endpoint Endpoint) bool {
	for _, advertised := range bootstrap.Endpoints {
		if sameEndpoint(advertised, endpoint) {
			return true
		}
	}
	return false
}

// sameEndpoint 比较公开 channel、host 与 port 的完整值。
func sameEndpoint(left, right Endpoint) bool {
	return left.Channel == right.Channel && left.Host == right.Host && left.Port == right.Port
}

// worldBootstrapEventually 只重试幂等 bootstrap 的公开 retryable 瞬时失败，并受父场景 deadline 约束。
func worldBootstrapEventually(ctx context.Context, client *HTTPClient, accessToken *Secret) (WorldBootstrapResponse, error) {
	for {
		response, err := client.WorldBootstrap(ctx, accessToken)
		if err == nil {
			return response, nil
		}
		var publicError PublicError
		if !errors.As(err, &publicError) || !publicError.Retryable {
			return WorldBootstrapResponse{}, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return WorldBootstrapResponse{}, context.Cause(ctx)
		case <-timer.C:
		}
	}
}

// requireRevisionIncrease 检查 mutation 每次首次提交只需公开保证的单调增加。
func requireRevisionIncrease(before, after uint64) error {
	if before == 0 || after <= before {
		return fmt.Errorf("visit revision did not increase: before=%d after=%d", before, after)
	}
	return nil
}

// scenarioDeadline 为单个异步 push/poll 创建不超过父场景的短预算。
func scenarioDeadline(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, duration)
}
