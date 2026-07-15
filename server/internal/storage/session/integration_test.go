//go:build storage_integration

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/jinwiforz/ihomeland/server/internal/session"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// integrationObserver 丢弃不包含identity的固定存储结果。
type integrationObserver struct{}

// RecordStorageOperation 满足production Store observer契约。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// responseLossFault 在目标Lua请求写入后阻塞读取，确定性制造提交结果未知。
type responseLossFault struct {
	// commandWritten 表示目标命令已经交给TCP socket。
	commandWritten chan struct{}
	// releaseRead 由测试确认权威状态落地后关闭。
	releaseRead chan struct{}
	// writeOnce 防止连接重建时重复关闭同步屏障。
	writeOnce sync.Once
	// releaseOnce 保证清理路径可以重复释放读取。
	releaseOnce sync.Once
	// dropping 标记后续读取必须返回注入错误。
	dropping atomic.Bool
}

// responseLossConn 只对EVAL/EVALSHA响应注入丢失。
type responseLossConn struct {
	net.Conn
	// fault 由测试client全部连接共享。
	fault *responseLossFault
}

// Write 在完整请求写入后建立request-sent同步屏障。
func (connection *responseLossConn) Write(payload []byte) (int, error) {
	written, err := connection.Conn.Write(payload)
	upper := bytes.ToUpper(payload[:written])
	if err == nil && (bytes.Contains(upper, []byte("\r\nEVAL\r\n")) || bytes.Contains(upper, []byte("\r\nEVALSHA\r\n"))) {
		connection.fault.dropping.Store(true)
		connection.fault.writeOnce.Do(func() { close(connection.fault.commandWritten) })
	}
	return written, err
}

// Read 在目标Lua请求后丢弃服务端response，不暴露任何key或value。
func (connection *responseLossConn) Read(payload []byte) (int, error) {
	if connection.fault.dropping.Load() {
		<-connection.fault.releaseRead
		return 0, errors.New("injected redis response loss")
	}
	return connection.Conn.Read(payload)
}

// release 幂等解除可能阻塞的测试读取。
func (fault *responseLossFault) release() {
	fault.releaseOnce.Do(func() { close(fault.releaseRead) })
}

// TestSessionStoreIntegrationLifecycle 覆盖create、access、refresh、ticket与幂等失效主路径。
func TestSessionStoreIntegrationLifecycle(t *testing.T) {
	store, client := openIntegrationStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	bundle := integrationBundle(t, "lifecycle", domain.Principal{}, now)
	createOutcome, createErr := store.Create(context.Background(), bundle)
	requireOutcome(t, createOutcome, createErr, domain.StoreOutcomeApplied)

	snapshot, outcome, err := store.ResolveAccess(context.Background(), bundle.Access.Digest, now)
	if err != nil || outcome != domain.StoreOutcomeApplied || snapshot.SessionID != bundle.Session.ID || snapshot.Principal.AccountID() != bundle.Session.Principal.AccountID() || !snapshot.AccessExpiresAt.Equal(bundle.Access.ExpiresAt) || !snapshot.SessionExpiresAt.Equal(bundle.Session.ExpiresAt) {
		t.Fatalf("resolve access: outcome=%v snapshot=%+v err=%v", outcome, snapshot, err)
	}
	if _, expiredOutcome, expiredErr := store.ResolveAccess(context.Background(), bundle.Access.Digest, bundle.Access.ExpiresAt); expiredErr != nil || expiredOutcome != domain.StoreOutcomeExpired {
		t.Fatalf("access expiry boundary: outcome=%v err=%v", expiredOutcome, expiredErr)
	}

	newAccess := integrationDigest(t, domain.SecretKindAccess, "rotated-access")
	newRefresh := integrationDigest(t, domain.SecretKindRefresh, "rotated-refresh")
	rotation := domain.Rotation{
		PresentedRefresh: bundle.Refresh.Digest,
		Access:           domain.TokenRecord{Digest: newAccess, ExpiresAt: now.Add(20 * time.Minute)},
		Refresh:          domain.TokenRecord{Digest: newRefresh, ExpiresAt: bundle.Session.ExpiresAt.Add(time.Hour)},
		Now:              now,
	}
	rotated, _, outcome, err := store.RotateRefresh(context.Background(), rotation)
	if err != nil || outcome != domain.StoreOutcomeApplied || !rotated.RefreshExpiresAt.Equal(bundle.Session.ExpiresAt) {
		t.Fatalf("rotate refresh: outcome=%v snapshot=%+v err=%v", outcome, rotated, err)
	}
	if _, oldOutcome, oldErr := store.ResolveAccess(context.Background(), bundle.Access.Digest, now); oldErr != nil || oldOutcome != domain.StoreOutcomeNotFound {
		t.Fatalf("old access survived rotation: outcome=%v err=%v", oldOutcome, oldErr)
	}
	_, invalidation, replayOutcome, err := store.RotateRefresh(context.Background(), rotation)
	if err != nil || replayOutcome != domain.StoreOutcomeReplayed || invalidation.Epoch != domain.InitialEpoch+1 || invalidation.Reason != domain.InvalidationReasonRefreshReplay {
		t.Fatalf("refresh replay: outcome=%v invalidation=%+v err=%v", replayOutcome, invalidation, err)
	}
	if _, invalidatedOutcome, invalidatedErr := store.ResolveAccess(context.Background(), newAccess, now); invalidatedErr != nil || invalidatedOutcome != domain.StoreOutcomeInvalidated {
		t.Fatalf("replay did not revoke new access: outcome=%v err=%v", invalidatedOutcome, invalidatedErr)
	}

	ticketBundle := integrationBundle(t, "ticket", domain.Principal{}, now)
	ticketCreateOutcome, ticketCreateErr := store.Create(context.Background(), ticketBundle)
	requireOutcome(t, ticketCreateOutcome, ticketCreateErr, domain.StoreOutcomeApplied)
	endpoint, _ := domain.NewEndpoint(domain.ChannelWSS, "control.example.invalid", 443)
	wrongEndpoint, _ := domain.NewEndpoint(domain.ChannelWSS, "wrong.example.invalid", 443)
	scopes, _ := domain.NewScopeSet(domain.ScopeControl)
	ticket := domain.TicketRecord{Digest: integrationTicketDigest(t, "ticket"), SessionID: ticketBundle.Session.ID,
		Epoch: domain.InitialEpoch, Channel: domain.ChannelWSS, Endpoint: endpoint, Scopes: scopes, ExpiresAt: now.Add(30 * time.Second)}
	issueOutcome, issueErr := store.IssueTicket(context.Background(), ticket, now)
	requireOutcome(t, issueOutcome, issueErr, domain.StoreOutcomeApplied)
	tcpEndpoint, _ := domain.NewEndpoint(domain.ChannelTLSTCP, "game.example.invalid", 4433)
	if _, wrongChannelOutcome, wrongChannelErr := store.ConsumeTicket(context.Background(), ticket.Digest, domain.ChannelTLSTCP, tcpEndpoint, now); wrongChannelErr != nil || wrongChannelOutcome != domain.StoreOutcomeNotFound {
		t.Fatalf("wrong channel consumed ticket: outcome=%v err=%v", wrongChannelOutcome, wrongChannelErr)
	}
	if _, wrongOutcome, wrongErr := store.ConsumeTicket(context.Background(), ticket.Digest, domain.ChannelWSS, wrongEndpoint, now); wrongErr != nil || wrongOutcome != domain.StoreOutcomeNotFound {
		t.Fatalf("wrong endpoint consumed ticket: outcome=%v err=%v", wrongOutcome, wrongErr)
	}
	ticketSnapshot, ticketOutcome, err := store.ConsumeTicket(context.Background(), ticket.Digest, domain.ChannelWSS, endpoint, now)
	if err != nil || ticketOutcome != domain.StoreOutcomeApplied || !ticketSnapshot.Scopes.Has(domain.ScopeControl) {
		t.Fatalf("consume ticket: outcome=%v snapshot=%+v err=%v", ticketOutcome, ticketSnapshot, err)
	}
	if _, repeatedOutcome, repeatedErr := store.ConsumeTicket(context.Background(), ticket.Digest, domain.ChannelWSS, endpoint, now); repeatedErr != nil || repeatedOutcome != domain.StoreOutcomeReplayed {
		t.Fatalf("ticket replay: outcome=%v err=%v", repeatedOutcome, repeatedErr)
	}
	staleTicket := ticket
	staleTicket.Digest = integrationTicketDigest(t, "stale-ticket")
	staleTicket.Epoch = domain.InitialEpoch + 1
	if staleOutcome, staleErr := store.IssueTicket(context.Background(), staleTicket, now); staleErr != nil || staleOutcome != domain.StoreOutcomeEpochMismatch {
		t.Fatalf("stale epoch ticket: outcome=%v err=%v", staleOutcome, staleErr)
	}

	firstInvalidation, firstOutcome, err := store.InvalidateSession(context.Background(), ticketBundle.Session.ID, domain.InvalidationReasonLogout)
	if err != nil || firstOutcome != domain.StoreOutcomeApplied {
		t.Fatalf("invalidate session: outcome=%v invalidation=%+v err=%v", firstOutcome, firstInvalidation, err)
	}
	secondInvalidation, secondOutcome, err := store.InvalidateSession(context.Background(), ticketBundle.Session.ID, domain.InvalidationReasonForcedLogout)
	if err != nil || secondOutcome != domain.StoreOutcomeInvalidated || secondInvalidation != firstInvalidation {
		t.Fatalf("repeat invalidation changed fact: outcome=%v first=%+v second=%+v err=%v", secondOutcome, firstInvalidation, secondInvalidation, err)
	}

	_ = client
}

// TestSessionStoreIntegrationConcurrencyCorruptionRestartAndFlush 覆盖原子竞争、损坏、持久化重启与灾难清空。
func TestSessionStoreIntegrationConcurrencyCorruptionRestartAndFlush(t *testing.T) {
	store, client := openIntegrationStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	bundle := integrationBundle(t, "refresh-race", domain.Principal{}, now)
	createOutcome, createErr := store.Create(context.Background(), bundle)
	requireOutcome(t, createOutcome, createErr, domain.StoreOutcomeApplied)
	const concurrency = 16
	rotations := make([]domain.Rotation, concurrency)
	for index := range rotations {
		label := strconv.Itoa(index)
		rotations[index] = domain.Rotation{PresentedRefresh: bundle.Refresh.Digest,
			Access:  domain.TokenRecord{Digest: integrationDigest(t, domain.SecretKindAccess, "race-access-"+label), ExpiresAt: now.Add(10 * time.Minute)},
			Refresh: domain.TokenRecord{Digest: integrationDigest(t, domain.SecretKindRefresh, "race-refresh-"+label), ExpiresAt: now.Add(20 * time.Minute)}, Now: now}
	}
	var rotationIndex atomic.Uint32
	refreshOutcomes := concurrentOutcomes(t, concurrency, func() (domain.StoreOutcome, error) {
		rotation := rotations[int(rotationIndex.Add(1))-1]
		_, _, result, callErr := store.RotateRefresh(context.Background(), rotation)
		return result, callErr
	})
	requireOutcomeCounts(t, refreshOutcomes, map[domain.StoreOutcome]int{domain.StoreOutcomeApplied: 1, domain.StoreOutcomeReplayed: concurrency - 1})

	ticketBundle := integrationBundle(t, "ticket-race", domain.Principal{}, now)
	ticketCreateOutcome, ticketCreateErr := store.Create(context.Background(), ticketBundle)
	requireOutcome(t, ticketCreateOutcome, ticketCreateErr, domain.StoreOutcomeApplied)
	endpoint, _ := domain.NewEndpoint(domain.ChannelTLSTCP, "game.example.invalid", 4433)
	scopes, _ := domain.NewScopeSet(domain.ScopeGameplay)
	ticket := domain.TicketRecord{Digest: integrationTicketDigest(t, "ticket-race"), SessionID: ticketBundle.Session.ID,
		Epoch: domain.InitialEpoch, Channel: domain.ChannelTLSTCP, Endpoint: endpoint, Scopes: scopes, ExpiresAt: now.Add(30 * time.Second)}
	issueOutcome, issueErr := store.IssueTicket(context.Background(), ticket, now)
	requireOutcome(t, issueOutcome, issueErr, domain.StoreOutcomeApplied)
	ticketOutcomes := concurrentOutcomes(t, concurrency, func() (domain.StoreOutcome, error) {
		_, result, callErr := store.ConsumeTicket(context.Background(), ticket.Digest, domain.ChannelTLSTCP, endpoint, now)
		return result, callErr
	})
	requireOutcomeCounts(t, ticketOutcomes, map[domain.StoreOutcome]int{domain.StoreOutcomeApplied: 1, domain.StoreOutcomeReplayed: concurrency - 1})

	invalidationBundle := integrationBundle(t, "invalidation-race", domain.Principal{}, now)
	invalidationCreateOutcome, invalidationCreateErr := store.Create(context.Background(), invalidationBundle)
	requireOutcome(t, invalidationCreateOutcome, invalidationCreateErr, domain.StoreOutcomeApplied)
	type invalidationCall struct {
		fact    domain.Invalidation
		outcome domain.StoreOutcome
		err     error
	}
	invalidationCalls := make(chan invalidationCall, 2)
	startInvalidation := make(chan struct{})
	for _, reason := range []domain.InvalidationReason{domain.InvalidationReasonLogout, domain.InvalidationReasonForcedLogout} {
		go func(currentReason domain.InvalidationReason) {
			<-startInvalidation
			fact, result, callErr := store.InvalidateSession(context.Background(), invalidationBundle.Session.ID, currentReason)
			invalidationCalls <- invalidationCall{fact: fact, outcome: result, err: callErr}
		}(reason)
	}
	close(startInvalidation)
	firstCall := <-invalidationCalls
	secondCall := <-invalidationCalls
	if firstCall.err != nil || secondCall.err != nil || firstCall.fact != secondCall.fact {
		t.Fatalf("concurrent invalidation did not converge: first=%+v second=%+v", firstCall, secondCall)
	}
	requireOutcomeCounts(t, []domain.StoreOutcome{firstCall.outcome, secondCall.outcome}, map[domain.StoreOutcome]int{domain.StoreOutcomeApplied: 1, domain.StoreOutcomeInvalidated: 1})

	principal, _ := domain.NewPrincipal("acc_principal", "ply_principal")
	for _, suffix := range []string{"principal-a", "principal-b"} {
		principalOutcome, principalErr := store.Create(context.Background(), integrationBundle(t, suffix, principal, now))
		requireOutcome(t, principalOutcome, principalErr, domain.StoreOutcomeApplied)
	}
	type principalCall struct {
		facts []domain.Invalidation
		err   error
	}
	principalCalls := make(chan principalCall, 2)
	startPrincipal := make(chan struct{})
	for _, reason := range []domain.InvalidationReason{domain.InvalidationReasonPrincipalBan, domain.InvalidationReasonForcedLogout} {
		go func(currentReason domain.InvalidationReason) {
			<-startPrincipal
			facts, callErr := store.InvalidatePrincipal(context.Background(), principal, currentReason)
			principalCalls <- principalCall{facts: facts, err: callErr}
		}(reason)
	}
	close(startPrincipal)
	firstPrincipal := <-principalCalls
	secondPrincipal := <-principalCalls
	for _, call := range []*principalCall{&firstPrincipal, &secondPrincipal} {
		if call.err != nil || len(call.facts) != 2 {
			t.Fatalf("concurrent principal invalidation: count=%d err=%v", len(call.facts), call.err)
		}
		sort.Slice(call.facts, func(left, right int) bool {
			return call.facts[left].SessionID.String() < call.facts[right].SessionID.String()
		})
	}
	for index := range firstPrincipal.facts {
		if firstPrincipal.facts[index] != secondPrincipal.facts[index] {
			t.Fatalf("principal invalidation did not converge: first=%+v second=%+v", firstPrincipal.facts, secondPrincipal.facts)
		}
	}
	principalRelogin := integrationBundle(t, "principal-relogin", principal, now)
	principalReloginOutcome, principalReloginErr := store.Create(context.Background(), principalRelogin)
	requireOutcome(t, principalReloginOutcome, principalReloginErr, domain.StoreOutcomeApplied)
	principalKey, _ := store.principalKey(principal)
	indexedSessions, err := client.HGet(context.Background(), principalKey.Value(), "sessions").Result()
	if err != nil || indexedSessions != principalRelogin.Session.ID.String() {
		t.Fatalf("principal index retained invalidated sessions: sessions=%q err=%v", indexedSessions, err)
	}

	limitPrincipal, _ := domain.NewPrincipal("acc_limit", "ply_limit")
	for index := range maximumPrincipalSessions {
		suffix := "limit-" + twoDigits(index)
		limitOutcome, limitErr := store.Create(context.Background(), integrationBundle(t, suffix, limitPrincipal, now))
		requireOutcome(t, limitOutcome, limitErr, domain.StoreOutcomeApplied)
	}
	overflowOutcome, overflowErr := store.Create(context.Background(), integrationBundle(t, "limit-overflow", limitPrincipal, now))
	requireOutcome(t, overflowOutcome, overflowErr, domain.StoreOutcomeConflict)

	corrupt := integrationBundle(t, "corrupt", domain.Principal{}, now)
	corruptCreateOutcome, corruptCreateErr := store.Create(context.Background(), corrupt)
	requireOutcome(t, corruptCreateOutcome, corruptCreateErr, domain.StoreOutcomeApplied)
	accessKey, _, _ := store.digestKey(sessionAccessDefinitionName, corrupt.Access.Digest)
	if err := client.HDel(context.Background(), accessKey.Value(), "epoch").Err(); err != nil {
		t.Fatal(err)
	}
	if _, corruptOutcome, corruptErr := store.ResolveAccess(context.Background(), corrupt.Access.Digest, now); corruptErr == nil || corruptOutcome != domain.StoreOutcomeUnspecified {
		t.Fatalf("corrupt access did not fail closed: outcome=%v err=%v", corruptOutcome, corruptErr)
	}

	responseLossBundle := integrationBundle(t, "response-loss", domain.Principal{}, now)
	faultStore, faultClient, fault := openResponseLossStore(t)
	defer func() {
		fault.release()
		_ = faultClient.Close()
	}()
	result := make(chan error, 1)
	go func() {
		_, callErr := faultStore.Create(context.Background(), responseLossBundle)
		result <- callErr
	}()
	select {
	case <-fault.commandWritten:
	case <-time.After(5 * time.Second):
		t.Fatal("未观察到Session Lua请求写入")
	}
	recordKey, _ := store.recordKey(responseLossBundle.Session.ID)
	waitForHash(t, client, recordKey.Value())
	fault.release()
	if callErr := <-result; callErr == nil {
		t.Fatal("response loss被错误报告为成功")
	}
	retryOutcome, retryErr := store.Create(context.Background(), responseLossBundle)
	requireOutcome(t, retryOutcome, retryErr, domain.StoreOutcomeConflict)

	restart := integrationBundle(t, "restart", domain.Principal{}, now)
	restartCreateOutcome, restartCreateErr := store.Create(context.Background(), restart)
	requireOutcome(t, restartCreateOutcome, restartCreateErr, domain.StoreOutcomeApplied)
	restartAccess := integrationDigest(t, domain.SecretKindAccess, "restart-rotated-access")
	restartRefresh := integrationDigest(t, domain.SecretKindRefresh, "restart-rotated-refresh")
	restartRotation := domain.Rotation{PresentedRefresh: restart.Refresh.Digest,
		Access:  domain.TokenRecord{Digest: restartAccess, ExpiresAt: now.Add(20 * time.Minute)},
		Refresh: domain.TokenRecord{Digest: restartRefresh, ExpiresAt: now.Add(40 * time.Minute)}, Now: now}
	_, _, restartRotationOutcome, restartRotationErr := store.RotateRefresh(context.Background(), restartRotation)
	requireOutcome(t, restartRotationOutcome, restartRotationErr, domain.StoreOutcomeApplied)
	restartEndpoint, _ := domain.NewEndpoint(domain.ChannelWSS, "restart.example.invalid", 443)
	restartScopes, _ := domain.NewScopeSet(domain.ScopeControl)
	restartTicket := domain.TicketRecord{Digest: integrationTicketDigest(t, "restart-ticket"), SessionID: restart.Session.ID,
		Epoch: domain.InitialEpoch, Channel: domain.ChannelWSS, Endpoint: restartEndpoint, Scopes: restartScopes, ExpiresAt: now.Add(30 * time.Second)}
	restartIssueOutcome, restartIssueErr := store.IssueTicket(context.Background(), restartTicket, now)
	requireOutcome(t, restartIssueOutcome, restartIssueErr, domain.StoreOutcomeApplied)
	_, restartConsumeOutcome, restartConsumeErr := store.ConsumeTicket(context.Background(), restartTicket.Digest, domain.ChannelWSS, restartEndpoint, now)
	requireOutcome(t, restartConsumeOutcome, restartConsumeErr, domain.StoreOutcomeApplied)
	restartIntegrationRedis(t, client)
	if _, restartOutcome, restartErr := store.ResolveAccess(context.Background(), restartAccess, now); restartErr != nil || restartOutcome != domain.StoreOutcomeApplied {
		t.Fatalf("restart lost redis state: outcome=%v err=%v", restartOutcome, restartErr)
	}
	if _, replayTicketOutcome, replayTicketErr := store.ConsumeTicket(context.Background(), restartTicket.Digest, domain.ChannelWSS, restartEndpoint, now); replayTicketErr != nil || replayTicketOutcome != domain.StoreOutcomeReplayed {
		t.Fatalf("restart lost consumed ticket marker: outcome=%v err=%v", replayTicketOutcome, replayTicketErr)
	}
	_, restartInvalidation, restartReplayOutcome, restartReplayErr := store.RotateRefresh(context.Background(), restartRotation)
	if restartReplayErr != nil || restartReplayOutcome != domain.StoreOutcomeReplayed || restartInvalidation.Reason != domain.InvalidationReasonRefreshReplay {
		t.Fatalf("restart lost refresh tombstone: outcome=%v invalidation=%+v err=%v", restartReplayOutcome, restartInvalidation, restartReplayErr)
	}
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if _, flushedOutcome, flushedErr := store.ResolveAccess(context.Background(), restartAccess, now); flushedErr != nil || flushedOutcome != domain.StoreOutcomeNotFound {
		t.Fatalf("flush did not fail closed: outcome=%v err=%v", flushedOutcome, flushedErr)
	}
	relogin := integrationBundle(t, "relogin-after-flush", restart.Session.Principal, now)
	reloginOutcome, reloginErr := store.Create(context.Background(), relogin)
	requireOutcome(t, reloginOutcome, reloginErr, domain.StoreOutcomeApplied)
	if _, resolvedOutcome, resolvedErr := store.ResolveAccess(context.Background(), relogin.Access.Digest, now); resolvedErr != nil || resolvedOutcome != domain.StoreOutcomeApplied {
		t.Fatalf("new login after flush is unusable: outcome=%v err=%v", resolvedOutcome, resolvedErr)
	}
}

// openIntegrationStore 连接统一harness Redis并构造production adapter。
func openIntegrationStore(t *testing.T) (*Store, *redisclient.Client) {
	t.Helper()
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password),
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("integration", registry)
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(client, keyspace, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	return store, client
}

// openResponseLossStore 构造单连接、禁用retry且只丢弃Lua response的测试adapter。
func openResponseLossStore(t *testing.T) (*Store, *redisclient.Client, *responseLossFault) {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	fault := &responseLossFault{commandWritten: make(chan struct{}), releaseRead: make(chan struct{})}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	client := redisclient.NewClient(&redisclient.Options{Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password),
		DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		PoolSize: 1, MaxRetries: -1, MinRetryBackoff: -1, MaxRetryBackoff: -1,
		Dialer: func(ctx context.Context, network string, address string) (net.Conn, error) {
			connection, dialErr := dialer.DialContext(ctx, network, address)
			if dialErr != nil {
				return nil, dialErr
			}
			return &responseLossConn{Conn: connection, fault: fault}, nil
		}})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	registry, err := storageredis.NewRegistry(Definitions())
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("integration", registry)
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	store, err := New(client, keyspace, integrationObserver{})
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return store, client, fault
}

// integrationBundle 构造digest互不冲突的完整session写入。
func integrationBundle(t *testing.T, suffix string, principal domain.Principal, now time.Time) domain.SessionBundle {
	t.Helper()
	id, err := domain.NewSessionID("ses_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Valid() {
		identitySuffix := strings.NewReplacer("-", "", "_", "", ".", "").Replace(suffix)
		principal, err = domain.NewPrincipal("acc_"+identitySuffix, "ply_"+identitySuffix)
		if err != nil {
			t.Fatal(err)
		}
	}
	sessionExpiry := now.Add(time.Hour)
	return domain.SessionBundle{
		Session: domain.SessionRecord{ID: id, Principal: principal, Epoch: domain.InitialEpoch, Status: domain.StatusActive, ExpiresAt: sessionExpiry},
		Access: domain.TokenRecord{Digest: integrationDigest(t, domain.SecretKindAccess, suffix+"-access"), SessionID: id,
			Epoch: domain.InitialEpoch, ExpiresAt: now.Add(10 * time.Minute)},
		Refresh: domain.TokenRecord{Digest: integrationDigest(t, domain.SecretKindRefresh, suffix+"-refresh"), SessionID: id,
			Epoch: domain.InitialEpoch, ExpiresAt: now.Add(30 * time.Minute)},
	}
}

// integrationDigest 从测试标签生成合法opaque credential并只返回摘要。
func integrationDigest(t *testing.T, kind domain.SecretKind, label string) domain.Digest {
	t.Helper()
	material := sha256.Sum256([]byte(label))
	prefix := "ih_at_"
	if kind == domain.SecretKindRefresh {
		prefix = "ih_rt_"
	}
	secret, err := domain.ParseSecret(kind, prefix+base64.RawURLEncoding.EncodeToString(material[:]))
	if err != nil {
		t.Fatal(err)
	}
	return secret.Digest()
}

// integrationTicketDigest 从测试标签生成固定长度nonce摘要。
func integrationTicketDigest(t *testing.T, label string) domain.Digest {
	t.Helper()
	material := sha256.Sum256([]byte(label))
	nonce, err := domain.ParseTicketNonce(material[:16])
	if err != nil {
		t.Fatal(err)
	}
	return nonce.Digest()
}

// twoDigits 返回principal容量测试使用的稳定两位后缀。
func twoDigits(value int) string {
	encoded := strconv.Itoa(value)
	if value < 10 {
		return "0" + encoded
	}
	return encoded
}

// requireOutcome 断言不携带projection的Store调用结果。
func requireOutcome(t *testing.T, outcome domain.StoreOutcome, err error, expected domain.StoreOutcome) {
	t.Helper()
	if err != nil || outcome != expected {
		t.Fatalf("store outcome=%v, want=%v, err=%v", outcome, expected, err)
	}
}

// concurrentOutcomes 同步启动固定数量调用并收集结果。
func concurrentOutcomes(t *testing.T, count int, call func() (domain.StoreOutcome, error)) []domain.StoreOutcome {
	t.Helper()
	start := make(chan struct{})
	results := make(chan domain.StoreOutcome, count)
	var group sync.WaitGroup
	for range count {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			outcome, err := call()
			if err != nil {
				t.Errorf("concurrent store call failed: %v", err)
				results <- domain.StoreOutcomeUnspecified
				return
			}
			results <- outcome
		}()
	}
	close(start)
	group.Wait()
	close(results)
	collected := make([]domain.StoreOutcome, 0, count)
	for outcome := range results {
		collected = append(collected, outcome)
	}
	return collected
}

// requireOutcomeCounts 断言并发结果精确收敛到预期分布。
func requireOutcomeCounts(t *testing.T, outcomes []domain.StoreOutcome, expected map[domain.StoreOutcome]int) {
	t.Helper()
	actual := make(map[domain.StoreOutcome]int)
	for _, outcome := range outcomes {
		actual[outcome]++
	}
	for outcome, count := range expected {
		if actual[outcome] != count {
			t.Fatalf("concurrent outcomes=%v, want %v count=%d", actual, outcome, count)
		}
	}
}

// waitForHash 有界等待response-loss请求已在权威Redis创建目标Hash。
func waitForHash(t *testing.T, client *redisclient.Client, key string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		exists, err := client.Exists(ctx, key).Result()
		if err == nil && exists == 1 {
			return
		}
		if err != nil {
			t.Fatalf("查询Session mutation结果失败: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("deadline内未观察到Session mutation结果")
		case <-ticker.C:
		}
	}
}

// restartIntegrationRedis 重启harness container并等待现有client恢复。
func restartIntegrationRedis(t *testing.T, client *redisclient.Client) {
	t.Helper()
	command := exec.Command("docker", "restart", os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart redis container: %v (%s)", err, output)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("Redis did not recover before deadline")
}
