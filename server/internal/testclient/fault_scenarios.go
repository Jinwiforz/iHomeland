package testclient

import (
	"context"
	"errors"
	"time"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
)

// runServerProcessRestart 验证独立进程 replacement 后持久世界保留、assignment 前进且旧连接/资格 fail closed。
func runServerProcessRestart(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	before, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	active, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(active); err != nil {
		return err
	}
	ticket, admission, endpoint, err := issueUnusedOwnCredentials(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	defer ticket.Clear()
	defer admission.Clear()
	if runtime.Faults == nil || runtime.Faults.Execute(ctx, "server-restart") != nil {
		return errors.New("independent server restart fault failed")
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, 2*time.Second)
	_, probeErr := active.Request(probeContext, 2000, newWorldSnapshotRequest())
	cancelProbe()
	if probeErr == nil {
		return errors.New("old gameplay connection survived server replacement")
	}
	after, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	if before.World.PersonalWorldID != after.World.PersonalWorldID || before.Assignment == nil || after.Assignment == nil || before.Assignment.WorldInstanceID == after.Assignment.WorldInstanceID || after.Assignment.Generation <= before.Assignment.Generation {
		return errors.New("server replacement did not advance current assignment")
	}
	return requireRejectedTCP(ctx, runtime, endpoint, ticket, admission, "OWN_WORLD")
}

// runRedisLossRecovery 验证 Redis 丢失不从 MySQL/client memory 复活 session、资格、VisitSession 或 assignment。
func runRedisLossRecovery(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	before, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	owner, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(owner); err != nil {
		return err
	}
	openedMessage, err := owner.Command(ctx, 2103, visitv1.VisitOpenCommand_builder{}.Build())
	if err != nil {
		return err
	}
	opened, ok := openedMessage.Payload.(*visitv1.VisitOpenResponse)
	if !ok || opened.GetSnapshot() == nil {
		return errors.New("Redis loss setup omitted VisitSession")
	}
	ticket, admission, endpoint, err := issueUnusedOwnCredentials(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	defer ticket.Clear()
	defer admission.Clear()
	if runtime.Faults == nil || runtime.Faults.Execute(ctx, "redis-flush") != nil {
		return errors.New("Redis flush fault failed")
	}
	if _, err := runtime.HTTP.WorldBootstrap(ctx, account.actor.AccessToken); err == nil {
		return errors.New("Redis loss left old session authorized")
	}
	if err := requireRejectedTCP(ctx, runtime, endpoint, ticket, admission, "OWN_WORLD"); err != nil {
		return err
	}
	password, err := account.password.Reveal()
	if err != nil {
		return err
	}
	login, err := runtime.HTTP.Login(ctx, LoginRequest{Username: account.username, Password: password})
	if err != nil {
		return err
	}
	account.actor.ClearCredentials()
	if err := applyAuthResponse(account.actor, &login); err != nil {
		return err
	}
	after, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	if after.World.PersonalWorldID != before.World.PersonalWorldID || after.Assignment == nil || before.Assignment == nil || after.Assignment.WorldInstanceID == before.Assignment.WorldInstanceID {
		return errors.New("Redis recovery did not rebuild from persistent world only")
	}
	recovered, err := dialOwnWorldTCP(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	if err := scenario.Track(recovered); err != nil {
		return err
	}
	if _, err := recovered.Request(ctx, 2119, visitv1.VisitSnapshotRequest_builder{}.Build()); err == nil {
		return errors.New("Redis loss restored old VisitSession")
	}
	return nil
}

// runMySQLRestartRecovery 验证 MySQL restart 后持久世界恢复，已消费 Redis 资格不会复活。
func runMySQLRestartRecovery(ctx context.Context, runtime *ScenarioRuntime) (resultErr error) {
	scenario, err := NewScenarioContext(ctx)
	if err != nil {
		return err
	}
	defer closeScenario(scenario, &resultErr)
	account, err := registerScenarioActor(scenario, runtime)
	if err != nil {
		return err
	}
	before, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	ticket, admission, endpoint, err := issueUnusedOwnCredentials(ctx, runtime, account.actor)
	if err != nil {
		return err
	}
	consumedTicket, err := copySecret(ticket)
	if err != nil {
		return err
	}
	consumedAdmission, err := copySecret(admission)
	if err != nil {
		consumedTicket.Clear()
		return err
	}
	connection, err := DialTCP(ctx, endpoint, ticket, admission, "OWN_WORLD", runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		return err
	}
	_ = connection.Close()
	defer consumedTicket.Clear()
	defer consumedAdmission.Clear()
	if runtime.Faults == nil || runtime.Faults.Execute(ctx, "mysql-restart") != nil {
		return errors.New("MySQL restart fault failed")
	}
	after, err := worldBootstrapEventually(ctx, runtime.HTTP, account.actor.AccessToken)
	if err != nil {
		return err
	}
	if after.World.PersonalWorldID != before.World.PersonalWorldID || after.World.Revision != before.World.Revision {
		return errors.New("MySQL restart did not recover committed PersonalWorld")
	}
	return requireRejectedTCP(ctx, runtime, endpoint, consumedTicket, consumedAdmission, "OWN_WORLD")
}

// issueUnusedOwnCredentials 签发尚未消费的同 endpoint ticket/admission，并立即清空 raw response 字段。
func issueUnusedOwnCredentials(ctx context.Context, runtime *ScenarioRuntime, actor *Actor) (*Secret, *Secret, Endpoint, error) {
	ticketResponse, err := runtime.HTTP.IssueTicket(ctx, actor.AccessToken, TicketRequest{Channel: "TLS_TCP"})
	if err != nil {
		return nil, nil, Endpoint{}, err
	}
	key, err := NewIdentity("admission", 16)
	if err != nil {
		return nil, nil, Endpoint{}, err
	}
	admissionResponse, err := runtime.HTTP.IssueWorldAdmission(ctx, actor.AccessToken, key, WorldAdmissionRequest{Kind: "OWN_WORLD"})
	if err != nil {
		return nil, nil, Endpoint{}, err
	}
	if !sameEndpoint(ticketResponse.Endpoint, admissionResponse.Endpoint) {
		return nil, nil, Endpoint{}, errors.New("fault scenario credential endpoints drifted")
	}
	ticket, err := NewSecret(ticketResponse.Ticket)
	ticketResponse.Ticket = ""
	if err != nil {
		return nil, nil, Endpoint{}, err
	}
	admission, err := NewSecret(admissionResponse.Credential)
	admissionResponse.Credential = ""
	if err != nil {
		ticket.Clear()
		return nil, nil, Endpoint{}, err
	}
	return ticket, admission, admissionResponse.Endpoint, nil
}

// copySecret 复制尚未消费的 secret，调用方必须立即安排双方清除。
func copySecret(source *Secret) (*Secret, error) {
	value, err := source.Reveal()
	if err != nil {
		return nil, err
	}
	return NewSecret(value)
}

// requireRejectedTCP 允许握手或首个 frame 任一阶段拒绝，但禁止旧资格获得业务响应。
func requireRejectedTCP(ctx context.Context, runtime *ScenarioRuntime, endpoint Endpoint, ticket, admission *Secret, purpose string) error {
	client, err := DialTCP(ctx, endpoint, ticket, admission, purpose, runtime.TLSConfig, runtime.Bootstrap.Limits.RealtimeFrameBytes)
	if err != nil {
		return nil
	}
	defer client.Close()
	probeContext, cancelProbe := context.WithTimeout(ctx, 2*time.Second)
	_, probeErr := client.Request(probeContext, 2000, newWorldSnapshotRequest())
	cancelProbe()
	if probeErr == nil {
		return errors.New("stale gameplay qualification was accepted")
	}
	return nil
}
