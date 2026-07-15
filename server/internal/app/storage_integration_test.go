//go:build storage_integration

package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	storageall "github.com/jinwiforz/ihomeland/server/internal/storage"
	storagepersonalworld "github.com/jinwiforz/ihomeland/server/internal/storage/personalworld"
	storageplacement "github.com/jinwiforz/ihomeland/server/internal/storage/placement"
	storagesession "github.com/jinwiforz/ihomeland/server/internal/storage/session"
	storagevisitsession "github.com/jinwiforz/ihomeland/server/internal/storage/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/transport/tcpgameplay"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	redisclient "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// TestStorageRequiredReadinessDrainingAndProcessRecovery 验证 storage 启动/运行失败、逆序关闭和新进程恢复。
func TestStorageRequiredReadinessDrainingAndProcessRecovery(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	t.Setenv("IHOMELAND_WORLD_ADMISSION_KEY", "storage-integration-admission-key-material")
	diagnosticAddress := reserveAddress(t)
	publicAddress := reserveAddress(t)
	configPath := writeIntegrationConfig(t, diagnosticAddress, publicAddress)
	redisContainer := os.Getenv("IHOMELAND_TEST_REDIS_CONTAINER")

	runDocker(t, "stop", "--time", "1", redisContainer)
	failed := Run(context.Background(), integrationOptions(configPath))
	if failed.Kind != ResultStartupError {
		t.Fatalf("missing Redis result = %+v", failed)
	}
	assertAddressReusable(t, diagnosticAddress)
	runDocker(t, "start", redisContainer)
	waitForContainerHealthy(t, redisContainer)

	ctx, cancel := context.WithCancelCause(context.Background())
	resultChannel := make(chan Result, 1)
	go func() { resultChannel <- Run(ctx, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	runDocker(t, "stop", "--time", "1", redisContainer)
	select {
	case result := <-resultChannel:
		if result.Kind != ResultRuntimeError {
			t.Fatalf("probe failure result = %+v", result)
		}
	case <-time.After(10 * time.Second):
		cancel(context.DeadlineExceeded)
		t.Fatal("required Redis probe 未触发单向 draining")
	}
	cancel(ErrSignalShutdown)
	assertAddressReusable(t, diagnosticAddress)
	runDocker(t, "start", redisContainer)
	waitForContainerHealthy(t, redisContainer)

	recoveryContext, stopRecovery := context.WithCancelCause(context.Background())
	recoveryResult := make(chan Result, 1)
	go func() { recoveryResult <- Run(recoveryContext, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	stopRecovery(ErrSignalShutdown)
	if result := <-recoveryResult; result.Kind != ResultClean {
		t.Fatalf("recovered process result = %+v", result)
	}
	assertAddressReusable(t, diagnosticAddress)
}

// TestPublicHTTPProductionGraphHitsAllOperations 验证10个冻结operation穿过真实listener与production storage graph。
func TestPublicHTTPProductionGraphHitsAllOperations(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	t.Setenv("IHOMELAND_WORLD_ADMISSION_KEY", "http-integration-admission-key-material")
	diagnosticAddress, publicAddress := reserveAddress(t), reserveAddress(t)
	configPath := writeIntegrationConfig(t, diagnosticAddress, publicAddress)
	certificatePath, privateKey := writeTestCertificate(t)
	t.Setenv("IHOMELAND_TEST_TCP_PRIVATE_KEY", string(privateKey))
	enableIntegrationTLS(t, configPath, certificatePath, "IHOMELAND_TEST_TCP_PRIVATE_KEY")
	client := newIntegrationTLSClient(t, certificatePath)
	gameplayTLS := newIntegrationTLSConfig(t, certificatePath)
	ctx, stop := context.WithCancelCause(context.Background())
	resultChannel := make(chan Result, 1)
	go func() { resultChannel <- Run(ctx, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	_, publicPort, err := net.SplitHostPort(publicAddress)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "https://localhost:" + publicPort
	request := func(method string, url string, body string, authorization string, status int) map[string]any {
		return requestJSONWithClient(t, client, method, url, body, authorization, status)
	}
	requestIdempotent := func(method string, url string, body string, authorization string, key string, status int) map[string]any {
		return requestJSONWithClientAndIdempotency(t, client, method, url, body, authorization, key, status)
	}

	request(http.MethodGet, baseURL+"/v1/version", "", "", http.StatusOK)
	request(http.MethodGet, baseURL+"/v1/config", "", "", http.StatusOK)
	visitor := request(http.MethodPost, baseURL+"/v1/auth/register", `{"username":"http-visitor","password":"integration-password","displayName":"HTTP Visitor"}`, "", http.StatusCreated)
	if visitor["tokens"] == nil {
		t.Fatalf("register response omitted tokens: %#v", visitor)
	}
	loggedIn := request(http.MethodPost, baseURL+"/v1/auth/login", `{"username":"http-visitor","password":"integration-password"}`, "", http.StatusOK)
	loginTokens := loggedIn["tokens"].(map[string]any)
	refreshed := request(http.MethodPost, baseURL+"/v1/auth/refresh", `{"refreshToken":"`+loginTokens["refreshToken"].(string)+`"}`, "", http.StatusOK)
	visitorAccess := refreshed["accessToken"].(string)
	visitorAuthorization := "Bearer " + visitorAccess
	visitorTicket := request(http.MethodPost, baseURL+"/v1/session/tickets", `{"channel":"TLS_TCP"}`, visitorAuthorization, http.StatusCreated)
	visitorBootstrap := request(http.MethodGet, baseURL+"/v1/world/bootstrap", "", visitorAuthorization, http.StatusOK)

	owner := request(http.MethodPost, baseURL+"/v1/auth/register", `{"username":"http-owner","password":"integration-password","displayName":"HTTP Owner"}`, "", http.StatusCreated)
	ownerTokens := owner["tokens"].(map[string]any)
	ownerAccess := ownerTokens["accessToken"].(string)
	ownerAuthorization := "Bearer " + ownerAccess
	ownerBootstrap := request(http.MethodGet, baseURL+"/v1/world/bootstrap", "", ownerAuthorization, http.StatusOK)

	visitID, inviteID, revision := seedHTTPVisit(t, visitorBootstrap, ownerBootstrap, visitorAccess, ownerAccess)
	ownerTicket := request(http.MethodPost, baseURL+"/v1/session/tickets", `{"channel":"TLS_TCP"}`, ownerAuthorization, http.StatusCreated)
	ownerAdmission := requestIdempotent(http.MethodPost, baseURL+"/v1/world/admissions", `{"kind":"OWN_WORLD"}`, ownerAuthorization, "integration-own-admission-1", http.StatusCreated)
	exerciseTCPOwnWorld(t, gameplayTLS, ownerTicket, ownerAdmission, ownerBootstrap)
	acceptPath := baseURL + "/v1/visits/" + visitID + "/invites/" + inviteID + "/accept"
	acceptBody := fmt.Sprintf(`{"expectedRevision":%d}`, revision)
	accepted := requestIdempotent(http.MethodPost, acceptPath, acceptBody, visitorAuthorization, "integration-accept-key-01", http.StatusOK)
	replayed := requestIdempotent(http.MethodPost, acceptPath, acceptBody, visitorAuthorization, "integration-accept-key-01", http.StatusOK)
	if fmt.Sprint(accepted) != fmt.Sprint(replayed) {
		t.Fatalf("accept replay drifted: first=%#v replay=%#v", accepted, replayed)
	}
	requestIdempotent(http.MethodPost, acceptPath, fmt.Sprintf(`{"expectedRevision":%d}`, revision+1), visitorAuthorization, "integration-accept-key-01", http.StatusConflict)
	admissionBody := `{"kind":"VISIT_WORLD","visitSessionId":"` + visitID + `"}`
	issuedAdmission := requestIdempotent(http.MethodPost, baseURL+"/v1/world/admissions", admissionBody, visitorAuthorization, "integration-admission-key-1", http.StatusCreated)
	replayedAdmission := requestIdempotent(http.MethodPost, baseURL+"/v1/world/admissions", admissionBody, visitorAuthorization, "integration-admission-key-1", http.StatusCreated)
	if issuedAdmission["credential"] != replayedAdmission["credential"] || issuedAdmission["expiresAtMs"] != replayedAdmission["expiresAtMs"] {
		t.Fatalf("admission replay drifted: first=%#v replay=%#v", issuedAdmission, replayedAdmission)
	}
	gameplayConnection := exerciseTCPJoin(t, gameplayTLS, visitorTicket, issuedAdmission, accepted, visitID)
	reconnectRevision := transitionHTTPVisitorToReconnect(t, visitorAccess, ownerBootstrap, visitID)
	_ = gameplayConnection.Close()
	reconnectTicket := request(http.MethodPost, baseURL+"/v1/session/tickets", `{"channel":"TLS_TCP"}`, visitorAuthorization, http.StatusCreated)
	reconnectAdmission := requestIdempotent(http.MethodPost, baseURL+"/v1/world/admissions", admissionBody, visitorAuthorization, "integration-reconnect-admission-1", http.StatusCreated)
	if reconnectAdmission["purpose"] != "RECONNECT" {
		t.Fatalf("reconnect admission purpose=%v", reconnectAdmission["purpose"])
	}
	gameplayConnection = exerciseTCPReconnect(t, gameplayTLS, reconnectTicket, reconnectAdmission, reconnectRevision, visitID)
	request(http.MethodPost, baseURL+"/v1/auth/logout", "", visitorAuthorization, http.StatusNoContent)
	_ = gameplayConnection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := gameplayConnection.Read(make([]byte, 1)); err == nil {
		t.Fatal("logout did not close old TCP gameplay connection")
	}
	_ = gameplayConnection.Close()
	request(http.MethodGet, baseURL+"/v1/world/bootstrap", "", visitorAuthorization, http.StatusUnauthorized)

	stop(ErrSignalShutdown)
	if result := <-resultChannel; result.Kind != ResultClean {
		t.Fatalf("public HTTP integration shutdown=%+v", result)
	}
}

// exerciseTCPOwnWorld 穿过真实listener与双credential握手读取Owner的PersonalWorld快照。
func exerciseTCPOwnWorld(t *testing.T, tlsConfig *tls.Config, ticket map[string]any, admission map[string]any, bootstrap map[string]any) {
	t.Helper()
	connection := dialTCPGameplay(t, tlsConfig, ticket)
	defer connection.Close()
	preface, err := tcpgameplay.EncodePreface(ticket["ticket"].(string), admission["credential"].(string), worldadmission.PurposeOwnWorld)
	if err != nil {
		t.Fatal(err)
	}
	kind := commonv1.MessageKind_MESSAGE_KIND_REQUEST
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2000), Kind: &kind,
		RequestId: bytes.Repeat([]byte{0x20}, 16), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(time.Now().UnixMilli()),
	}.Build()
	writeTCPPrefaceAndEnvelope(t, connection, preface, envelope)
	responseEnvelope, err := protocol.UnmarshalEnvelope(readTCPFrame(t, connection))
	if err != nil || responseEnvelope.GetMessageId() != 2001 || responseEnvelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_RESPONSE || !bytes.Equal(responseEnvelope.GetRequestId(), envelope.GetRequestId()) {
		t.Fatalf("unexpected TCP OWN_WORLD response: envelope=%v err=%v", responseEnvelope, err)
	}
	response := new(worldv1.WorldSnapshotResponse)
	if err := proto.Unmarshal(responseEnvelope.GetPayload(), response); err != nil {
		t.Fatal(err)
	}
	world := bootstrap["world"].(map[string]any)
	if response.GetSnapshot() == nil || response.GetSnapshot().GetWorld() == nil || response.GetSnapshot().GetWorld().GetPersonalWorldId() != world["personalWorldId"] {
		t.Fatalf("TCP OWN_WORLD response target drifted: %v", response)
	}
}

// exerciseTCPJoin 穿过真实listener、Redis ticket/admission与VisitSession store执行一次JOIN。
func exerciseTCPJoin(t *testing.T, tlsConfig *tls.Config, ticket map[string]any, admission map[string]any, accepted map[string]any, visitID string) net.Conn {
	t.Helper()
	connection := dialTCPGameplay(t, tlsConfig, ticket)
	preface, err := tcpgameplay.EncodePreface(ticket["ticket"].(string), admission["credential"].(string), worldadmission.PurposeJoin)
	if err != nil {
		t.Fatal(err)
	}
	reservation := accepted["reservation"].(map[string]any)
	command := visitv1.VisitJoinCommand_builder{
		AdmissionCredential: proto.String(admission["credential"].(string)),
		ExpectedRevision:    proto.Uint64(uint64(reservation["revision"].(float64))),
	}.Build()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	kind := commonv1.MessageKind_MESSAGE_KIND_COMMAND
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2109), Kind: &kind,
		CommandId: bytes.Repeat([]byte{0x21}, 16), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(time.Now().UnixMilli()), Payload: payload,
	}.Build()
	// 单次write故意合并preface与首个业务frame，验证server不会把TCP read边界当作消息边界。
	writeTCPPrefaceAndEnvelope(t, connection, preface, envelope)
	responseBytes := readTCPFrame(t, connection)
	responseEnvelope, err := protocol.UnmarshalEnvelope(responseBytes)
	if err != nil || responseEnvelope.GetMessageId() != 2110 || responseEnvelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_RESPONSE || !bytes.Equal(responseEnvelope.GetCommandId(), envelope.GetCommandId()) {
		t.Fatalf("unexpected TCP JOIN response: envelope=%v err=%v", responseEnvelope, err)
	}
	response := new(visitv1.VisitJoinResponse)
	if err := proto.Unmarshal(responseEnvelope.GetPayload(), response); err != nil {
		t.Fatal(err)
	}
	if response.GetResult() == nil || response.GetResult().GetSnapshot() == nil || response.GetResult().GetSnapshot().GetVisitSessionId() != visitID {
		t.Fatalf("TCP JOIN response target drifted: %v", response)
	}
	foundVisitor := false
	for _, visitor := range response.GetResult().GetSnapshot().GetVisitors() {
		if visitor.GetState() == visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_JOINED {
			foundVisitor = true
		}
	}
	if !foundVisitor {
		t.Fatal("TCP JOIN response omitted joined visitor")
	}
	return connection
}

// exerciseTCPReconnect 使用新ticket与RECONNECT admission恢复Visitor binding。
func exerciseTCPReconnect(t *testing.T, tlsConfig *tls.Config, ticket map[string]any, admission map[string]any, revision uint64, visitID string) net.Conn {
	t.Helper()
	connection := dialTCPGameplay(t, tlsConfig, ticket)
	preface, err := tcpgameplay.EncodePreface(ticket["ticket"].(string), admission["credential"].(string), worldadmission.PurposeReconnect)
	if err != nil {
		t.Fatal(err)
	}
	command := visitv1.VisitReconnectCommand_builder{
		AdmissionCredential: proto.String(admission["credential"].(string)),
		ExpectedRevision:    proto.Uint64(revision),
	}.Build()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	kind := commonv1.MessageKind_MESSAGE_KIND_COMMAND
	envelope := commonv1.ReliableEnvelope_builder{
		ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(2115), Kind: &kind,
		CommandId: bytes.Repeat([]byte{0x22}, 16), Sequence: proto.Uint64(1), TimestampMs: proto.Int64(time.Now().UnixMilli()), Payload: payload,
	}.Build()
	writeTCPPrefaceAndEnvelope(t, connection, preface, envelope)
	responseEnvelope, err := protocol.UnmarshalEnvelope(readTCPFrame(t, connection))
	if err != nil || responseEnvelope.GetMessageId() != 2116 || responseEnvelope.GetKind() != commonv1.MessageKind_MESSAGE_KIND_RESPONSE || !bytes.Equal(responseEnvelope.GetCommandId(), envelope.GetCommandId()) {
		t.Fatalf("unexpected TCP RECONNECT response: envelope=%v err=%v", responseEnvelope, err)
	}
	response := new(visitv1.VisitReconnectResponse)
	if err := proto.Unmarshal(responseEnvelope.GetPayload(), response); err != nil {
		t.Fatal(err)
	}
	if response.GetResult() == nil || response.GetResult().GetSnapshot() == nil || response.GetResult().GetSnapshot().GetVisitSessionId() != visitID {
		t.Fatalf("TCP RECONNECT response target drifted: %v", response)
	}
	return connection
}

// dialTCPGameplay 使用ticket公开的受信advertised endpoint连接真实gameplay listener。
func dialTCPGameplay(t *testing.T, tlsConfig *tls.Config, ticket map[string]any) net.Conn {
	t.Helper()
	endpoint := ticket["endpoint"].(map[string]any)
	address := net.JoinHostPort(endpoint["host"].(string), fmt.Sprintf("%.0f", endpoint["port"].(float64)))
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	connection, err := tls.DialWithDialer(dialer, "tcp", address, tlsConfig.Clone())
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

// writeTCPPrefaceAndEnvelope 在一个write中验证preface与首个业务frame的粘包解析。
func writeTCPPrefaceAndEnvelope(t *testing.T, connection net.Conn, preface []byte, envelope *commonv1.ReliableEnvelope) {
	t.Helper()
	encoded, err := protocol.MarshalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := protocol.EncodeFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(append(preface, frame...)); err != nil {
		t.Fatal(err)
	}
}

// readTCPFrame 有界读取单个4-byte大端length-prefixed response。
func readTCPFrame(t *testing.T, connection net.Conn) []byte {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	var prefix [4]byte
	if _, err := io.ReadFull(connection, prefix[:]); err != nil {
		t.Fatal(err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > protocol.MaximumFrameSize {
		t.Fatalf("invalid TCP response frame length %d", length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(connection, payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// TestPublicWebSocketControlConsumesTicketAndInvalidates 验证真实Redis ticket、WSS upgrade、重放拒绝与logout关闭。
func TestPublicWebSocketControlConsumesTicketAndInvalidates(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	t.Setenv("IHOMELAND_WORLD_ADMISSION_KEY", "wss-integration-admission-key-material")
	diagnosticAddress, publicAddress := reserveAddress(t), reserveAddress(t)
	configPath := writeIntegrationConfig(t, diagnosticAddress, publicAddress)
	certificatePath, privateKey := writeTestCertificate(t)
	t.Setenv("IHOMELAND_TEST_WSS_PRIVATE_KEY", string(privateKey))
	enableIntegrationTLS(t, configPath, certificatePath, "IHOMELAND_TEST_WSS_PRIVATE_KEY")
	client := newIntegrationTLSClient(t, certificatePath)
	ctx, stop := context.WithCancelCause(context.Background())
	resultChannel := make(chan Result, 1)
	go func() { resultChannel <- Run(ctx, integrationOptions(configPath)) }()
	waitForReady(t, diagnosticAddress)
	_, publicPort, err := net.SplitHostPort(publicAddress)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "https://localhost:" + publicPort
	websocketURL := "wss://localhost:" + publicPort + "/v1/control"

	registered := requestJSONWithClient(t, client, http.MethodPost, baseURL+"/v1/auth/register", `{"username":"wss-control","password":"integration-password","displayName":"WSS Control"}`, "", http.StatusCreated)
	tokens := registered["tokens"].(map[string]any)
	authorization := "Bearer " + tokens["accessToken"].(string)
	ticket := requestJSONWithClient(t, client, http.MethodPost, baseURL+"/v1/session/tickets", `{"channel":"WSS"}`, authorization, http.StatusCreated)
	rawTicket := ticket["ticket"].(string)

	header := http.Header{}
	header.Set("Authorization", "Ticket "+rawTicket)
	type dialResult struct {
		connection *websocket.Conn
		response   *http.Response
		err        error
	}
	dials := make(chan dialResult, 2)
	for range 2 {
		go func() {
			connection, response, err := websocket.Dial(t.Context(), websocketURL, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header, Subprotocols: []string{"ihomeland.control.v1"}})
			dials <- dialResult{connection: connection, response: response, err: err}
		}()
	}
	var connection *websocket.Conn
	accepted, rejected := 0, 0
	for range 2 {
		result := <-dials
		if result.err == nil {
			accepted++
			connection = result.connection
		} else if integrationResponseStatus(result.response) == http.StatusUnauthorized {
			rejected++
		} else {
			t.Fatalf("unexpected concurrent WSS result: status=%d err=%v", integrationResponseStatus(result.response), result.err)
		}
		if result.response != nil && result.response.Body != nil {
			_ = result.response.Body.Close()
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("atomic ticket consume results: accepted=%d rejected=%d", accepted, rejected)
	}
	defer connection.CloseNow()
	if connection.Subprotocol() != "ihomeland.control.v1" {
		t.Fatalf("negotiated subprotocol=%q", connection.Subprotocol())
	}

	type readResult struct {
		messageType websocket.MessageType
		encoded     []byte
		err         error
	}
	reads := make(chan readResult, 1)
	go func() {
		readContext, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRead()
		messageType, encoded, err := connection.Read(readContext)
		reads <- readResult{messageType: messageType, encoded: encoded, err: err}
	}()
	requestJSONWithClient(t, client, http.MethodPost, baseURL+"/v1/auth/logout", "", authorization, http.StatusNoContent)
	read := <-reads
	messageType, encoded, err := read.messageType, read.encoded, read.err
	if err != nil || messageType != websocket.MessageBinary {
		t.Fatalf("session invalidation push read failed: type=%v err=%v", messageType, err)
	}
	envelope, err := protocol.UnmarshalEnvelope(encoded)
	if err != nil || envelope.GetMessageId() != 504 || envelope.GetSequence() != 1 {
		t.Fatalf("session invalidation envelope drifted: envelope=%v err=%v", envelope, err)
	}

	shutdownAccount := requestJSONWithClient(t, client, http.MethodPost, baseURL+"/v1/auth/register", `{"username":"wss-shutdown","password":"integration-password","displayName":"WSS Shutdown"}`, "", http.StatusCreated)
	shutdownAuthorization := "Bearer " + shutdownAccount["tokens"].(map[string]any)["accessToken"].(string)
	shutdownTicket := requestJSONWithClient(t, client, http.MethodPost, baseURL+"/v1/session/tickets", `{"channel":"WSS"}`, shutdownAuthorization, http.StatusCreated)["ticket"].(string)
	shutdownHeader := http.Header{}
	shutdownHeader.Set("Authorization", "Ticket "+shutdownTicket)
	shutdownConnection, shutdownResponse, err := websocket.Dial(t.Context(), websocketURL, &websocket.DialOptions{HTTPClient: client, HTTPHeader: shutdownHeader, Subprotocols: []string{"ihomeland.control.v1"}})
	if err != nil {
		t.Fatalf("shutdown WSS connection failed: status=%d err=%v", integrationResponseStatus(shutdownResponse), err)
	}
	defer shutdownConnection.CloseNow()

	stop(ErrSignalShutdown)
	if result := <-resultChannel; result.Kind != ResultClean {
		t.Fatalf("WSS integration shutdown=%+v", result)
	}
	closeContext, cancelClose := context.WithTimeout(context.Background(), time.Second)
	_, _, closeErr := shutdownConnection.Read(closeContext)
	cancelClose()
	if websocket.CloseStatus(closeErr) != websocket.StatusGoingAway {
		t.Fatalf("shutdown close status=%v err=%v", websocket.CloseStatus(closeErr), closeErr)
	}
}

// integrationStorageObserver 丢弃adapter低基数观测；测试直接断言公开协议结果。
type integrationStorageObserver struct{}

// RecordStorageOperation 满足production storage adapter observer契约。
func (integrationStorageObserver) RecordStorageOperation(string, string, string) {}

// integrationOwnedWorldReader 返回HTTP已创建并经MySQL重新解析的Owner world事实。
type integrationOwnedWorldReader struct {
	// snapshot 是Owner的active primary world。
	snapshot personalworld.Snapshot
}

// ResolveOwnedWorld 只允许对应Owner解析预设world。
func (reader integrationOwnedWorldReader) ResolveOwnedWorld(_ context.Context, ownerID account.PlayerID) (personalworld.Snapshot, visitsession.OwnedWorldOutcome, error) {
	if reader.snapshot.OwnerID() != ownerID {
		return personalworld.Snapshot{}, visitsession.OwnedWorldOutcomeNotFound, nil
	}
	return reader.snapshot, visitsession.OwnedWorldOutcomeFound, nil
}

// integrationAssignmentReader 返回已通过production placement store激活的current assignment。
type integrationAssignmentReader struct {
	// snapshot 是测试在MySQL/Redis中提交的active assignment。
	snapshot placement.AssignmentSnapshot
}

// ResolveCurrent 只允许matching PersonalWorld读取预设current assignment。
func (reader integrationAssignmentReader) ResolveCurrent(_ context.Context, worldID personalworld.PersonalWorldID, _ time.Time) (placement.AssignmentSnapshot, visitsession.AssignmentOutcome, error) {
	if reader.snapshot.WorldID() != worldID {
		return placement.AssignmentSnapshot{}, visitsession.AssignmentOutcomeNotFound, nil
	}
	return reader.snapshot, visitsession.AssignmentOutcomeFound, nil
}

// seedHTTPVisit 使用production MySQL/Redis adapters创建active assignment、VisitSession与定向邀请。
func seedHTTPVisit(t *testing.T, visitorBootstrap map[string]any, ownerBootstrap map[string]any, visitorAccess string, ownerAccess string) (string, string, uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := openHTTPIntegrationDB(t)
	defer func() { _ = db.Close() }()
	client := openHTTPIntegrationRedis(t)
	defer func() { _ = client.Close() }()
	keyspace, err := storageall.NewRedisKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	observer := integrationStorageObserver{}
	policy := config.Default().PublicAPI
	worldData := ownerBootstrap["world"].(map[string]any)
	worldID, err := personalworld.NewPersonalWorldID(worldData["personalWorldId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := account.NewPlayerID(worldData["ownerPlayerId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	visitorData := visitorBootstrap["world"].(map[string]any)
	visitorID, err := account.NewPlayerID(visitorData["ownerPlayerId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	worldRepository, err := storagepersonalworld.New(db, observer)
	if err != nil {
		t.Fatal(err)
	}
	worldService, err := personalworld.NewService(worldRepository, SystemClock{}, RandomIDGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	worldResult, err := worldService.EnsurePrimaryWorld(ctx, ownerID)
	if err != nil || worldResult.World().ID() != worldID {
		t.Fatalf("resolve owner world: result=%v err=%v", worldResult.Valid(), err)
	}
	placementStore, err := storageplacement.New(db, client, keyspace, policy.PlacementReplayTTL, observer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	instanceID, _ := placement.NewWorldInstanceID("winst_httpIntegration")
	nodeID, _ := placement.NewRuntimeNodeID("rnode_httpIntegration")
	candidate, err := placement.NewAssignmentCandidate(worldID, instanceID, nodeID, now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	acquireRequest, _ := placement.NewAcquireRequest(candidate, now)
	starting, outcome, err := placementStore.Acquire(ctx, acquireRequest)
	if err != nil || outcome != placement.StoreOutcomeApplied {
		t.Fatalf("acquire HTTP assignment: outcome=%v err=%v", outcome, err)
	}
	activateRequest, _ := placement.NewStampRequest(starting.Stamp(), now)
	active, outcome, err := placementStore.Activate(ctx, activateRequest)
	if err != nil || outcome != placement.StoreOutcomeApplied {
		t.Fatalf("activate HTTP assignment: outcome=%v err=%v", outcome, err)
	}
	sessionStore, err := storagesession.New(client, keyspace, observer)
	if err != nil {
		t.Fatal(err)
	}
	wss, _ := session.NewEndpoint(session.ChannelWSS, policy.Endpoints.WSS.Host, uint16(policy.Endpoints.WSS.Port))
	tlsTCP, _ := session.NewEndpoint(session.ChannelTLSTCP, policy.Endpoints.TLSTCP.Host, uint16(policy.Endpoints.TLSTCP.Port))
	endpoints, _ := session.NewStaticEndpointProvider(wss, tlsTCP)
	sessionPolicy, _ := session.NewPolicy(policy.Session.TicketTTL, policy.Session.AccessTTL, policy.Session.RefreshTTL, policy.Session.SessionTTL)
	sessionService, err := session.NewService(sessionStore, endpoints, session.NoActiveRealtimeConnections{}, SystemClock{}, RandomIDGenerator{}, session.CryptoSecretGenerator{}, sessionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	ownerAuth, err := sessionService.AuthenticateHTTPS(ctx, ownerAccess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionService.AuthenticateHTTPS(ctx, visitorAccess); err != nil {
		t.Fatal(err)
	}
	visitStore, err := storagevisitsession.New(client, keyspace, SystemClock{}, policy.VisitSession.ReplayRetention, observer)
	if err != nil {
		t.Fatal(err)
	}
	capacity, _ := visitsession.NewCapacity(uint8(policy.VisitSession.Capacity))
	visitPolicy, _ := visitsession.NewPolicy(capacity, policy.VisitSession.SessionLifetime, policy.VisitSession.InviteLifetime, policy.VisitSession.ReservationLifetime, policy.VisitSession.OwnerGrace, policy.VisitSession.VisitorReconnectGrace)
	visitService, err := visitsession.NewService(visitStore, integrationOwnedWorldReader{snapshot: worldResult.World().Snapshot()}, integrationAssignmentReader{snapshot: active}, SystemClock{}, RandomIDGenerator{}, visitPolicy)
	if err != nil {
		t.Fatal(err)
	}
	bindingID, _ := visitsession.NewConnectionBindingID("vbind_httpIntegrationOwner")
	openCommand, _ := visitsession.NewCommandID("vcmd_httpIntegrationOpen")
	opened, err := visitService.Open(ctx, ownerAuth.AuthContext(), bindingID, openCommand)
	if err != nil {
		t.Fatal(err)
	}
	inviteCommand, _ := visitsession.NewCommandID("vcmd_httpIntegrationInvite")
	created, err := visitService.CreateInvite(ctx, ownerAuth.AuthContext(), visitorID, now.Add(3*time.Minute), opened.Snapshot().Revision(), inviteCommand)
	if err != nil {
		t.Fatal(err)
	}
	return created.Snapshot().ID().Value(), created.Invite().ID().Value(), uint64(created.Snapshot().Revision())
}

// transitionHTTPVisitorToReconnect 通过production store建立真实reconnecting事实，供TCP恢复wire验收使用。
// 当前切片尚未把socket close编排为VisitSession command，因此测试显式执行该application边界。
func transitionHTTPVisitorToReconnect(t *testing.T, visitorAccess string, ownerBootstrap map[string]any, visitID string) uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := openHTTPIntegrationRedis(t)
	defer func() { _ = client.Close() }()
	keyspace, err := storageall.NewRedisKeyspace("local")
	if err != nil {
		t.Fatal(err)
	}
	policy := config.Default().PublicAPI
	observer := integrationStorageObserver{}
	sessionStore, err := storagesession.New(client, keyspace, observer)
	if err != nil {
		t.Fatal(err)
	}
	wss, _ := session.NewEndpoint(session.ChannelWSS, policy.Endpoints.WSS.Host, uint16(policy.Endpoints.WSS.Port))
	tlsTCP, _ := session.NewEndpoint(session.ChannelTLSTCP, policy.Endpoints.TLSTCP.Host, uint16(policy.Endpoints.TLSTCP.Port))
	endpoints, _ := session.NewStaticEndpointProvider(wss, tlsTCP)
	sessionPolicy, _ := session.NewPolicy(policy.Session.TicketTTL, policy.Session.AccessTTL, policy.Session.RefreshTTL, policy.Session.SessionTTL)
	sessions, err := session.NewService(sessionStore, endpoints, session.NoActiveRealtimeConnections{}, SystemClock{}, RandomIDGenerator{}, session.CryptoSecretGenerator{}, sessionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := sessions.AuthenticateHTTPS(ctx, visitorAccess)
	if err != nil {
		t.Fatal(err)
	}
	visitStore, err := storagevisitsession.New(client, keyspace, SystemClock{}, policy.VisitSession.ReplayRetention, observer)
	if err != nil {
		t.Fatal(err)
	}
	capacity, _ := visitsession.NewCapacity(uint8(policy.VisitSession.Capacity))
	visitPolicy, _ := visitsession.NewPolicy(capacity, policy.VisitSession.SessionLifetime, policy.VisitSession.InviteLifetime, policy.VisitSession.ReservationLifetime, policy.VisitSession.OwnerGrace, policy.VisitSession.VisitorReconnectGrace)
	visits, err := visitsession.NewService(visitStore, integrationOwnedWorldReader{}, integrationAssignmentReader{}, SystemClock{}, RandomIDGenerator{}, visitPolicy)
	if err != nil {
		t.Fatal(err)
	}
	worldID, err := personalworld.NewPersonalWorldID(ownerBootstrap["world"].(map[string]any)["personalWorldId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := visits.ResolveActive(ctx, worldID)
	if err != nil || !found || snapshot.ID().Value() != visitID {
		t.Fatalf("resolve reconnect visit: found=%v snapshot=%v err=%v", found, snapshot.Valid(), err)
	}
	playerID, err := account.NewPlayerID(authenticated.AuthContext().Principal().PlayerID())
	if err != nil {
		t.Fatal(err)
	}
	var bindingID visitsession.ConnectionBindingID
	for _, membership := range snapshot.Memberships() {
		if membership.VisitorID() == playerID && membership.State() == visitsession.MembershipStateJoined {
			bindingID = membership.BindingID()
			break
		}
	}
	if !bindingID.Valid() {
		t.Fatal("joined Visitor binding was not persisted before reconnect transition")
	}
	commandID, _ := visitsession.NewCommandID("vcmd_httpIntegrationDisconnect")
	result, err := visits.VisitorDisconnect(ctx, authenticated.AuthContext(), snapshot.ID(), bindingID, time.Now().UTC().Add(30*time.Second), snapshot.Revision(), commandID)
	if err != nil {
		t.Fatalf("transition Visitor to reconnecting: err=%v", err)
	}
	reconnecting := false
	for _, membership := range result.Snapshot().Memberships() {
		if membership.VisitorID() == playerID && membership.State() == visitsession.MembershipStateReconnecting {
			reconnecting = true
		}
	}
	if !reconnecting {
		t.Fatal("Visitor disconnect did not persist reconnecting membership")
	}
	return uint64(result.Snapshot().Revision())
}

// openHTTPIntegrationDB 使用harness file secret连接隔离MySQL。
func openHTTPIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	driverConfig := mysqldriver.NewConfig()
	driverConfig.User = "ihomeland"
	driverConfig.Passwd = string(password)
	driverConfig.Net = "tcp"
	driverConfig.Addr = os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS")
	driverConfig.DBName = "ihomeland"
	driverConfig.Timeout = 3 * time.Second
	driverConfig.ParseTime = true
	driverConfig.Loc = time.UTC
	driverConfig.Params = map[string]string{"time_zone": "'+00:00'", "sql_mode": "'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'"}
	connector, err := mysqldriver.NewConnector(driverConfig)
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

// openHTTPIntegrationRedis 使用harness file secret连接隔离Redis且禁用自动重试。
func openHTTPIntegrationRedis(t *testing.T) *redisclient.Client {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), Password: string(password), DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxRetries: -1})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	return client
}

// requestJSON 执行有界integration请求并返回object响应；204返回空object。
func requestJSON(t *testing.T, method string, url string, body string, authorization string, expectedStatus int) map[string]any {
	t.Helper()
	return requestJSONWithIdempotency(t, method, url, body, authorization, "", expectedStatus)
}

// requestJSONWithClient 使用调用方提供的TLS策略执行无幂等键HTTP操作。
func requestJSONWithClient(t *testing.T, client *http.Client, method string, url string, body string, authorization string, expectedStatus int) map[string]any {
	t.Helper()
	return requestJSONWithClientAndIdempotency(t, client, method, url, body, authorization, "", expectedStatus)
}

// requestJSONWithIdempotency 增加可选Bearer与Idempotency-Key并验证状态。
func requestJSONWithIdempotency(t *testing.T, method string, url string, body string, authorization string, idempotencyKey string, expectedStatus int) map[string]any {
	t.Helper()
	return requestJSONWithClientAndIdempotency(t, &http.Client{Timeout: 5 * time.Second}, method, url, body, authorization, idempotencyKey, expectedStatus)
}

// requestJSONWithClientAndIdempotency 统一编码请求并验证安全响应状态。
func requestJSONWithClientAndIdempotency(t *testing.T, client *http.Client, method string, url string, body string, authorization string, idempotencyKey string, expectedStatus int) map[string]any {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	request.Header.Set("X-Request-ID", "storage-integration-request")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status=%d want=%d body=%s", method, url, response.StatusCode, expectedStatus, data)
	}
	if expectedStatus == http.StatusNoContent {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

// integrationOptions 使用真实 production graph，只将日志丢弃以保持测试输出稳定。
func integrationOptions(configPath string) Options {
	return Options{ConfigPath: configPath, Output: io.Discard, BuildInfo: buildinfo.Current()}
}

// writeIntegrationConfig 将 harness endpoint 与 file secret reference 写入测试私有临时目录。
func writeIntegrationConfig(t *testing.T, diagnosticAddress string, publicAddress string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.yaml")
	gameplayAddress := reserveAddress(t)
	body := fmt.Sprintf(`environment: local
runtime:
  startupTimeout: 3s
  shutdownTimeout: 3s
diagnostic:
  address: %s
publicApi:
  address: %s
  endpoints:
    wss:
      host: 127.0.0.1
      port: %s
    tlsTcp:
      host: 127.0.0.1
      port: %s
  websocketControl:
    allowedHosts:
      - %s
      - localhost:%s
    closeTimeout: 200ms
  gameplayTcp:
    address: %s
    closeTimeout: 200ms
    shutdownTimeout: 1s
storage:
  mysql:
    address: %s
    passwordSecret: 'file:%s'
    probe:
      interval: 100ms
      timeout: 50ms
      failureThreshold: 2
  redis:
    address: %s
    passwordSecret: 'file:%s'
    probe:
      interval: 100ms
      timeout: 50ms
      failureThreshold: 2
`, diagnosticAddress, publicAddress, integrationPort(t, publicAddress), integrationPort(t, gameplayAddress), publicAddress, integrationPort(t, publicAddress), gameplayAddress, os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS"), os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"), os.Getenv("IHOMELAND_TEST_REDIS_ADDRESS"), os.Getenv("IHOMELAND_TEST_REDIS_PASSWORD_FILE"))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// integrationPort 返回临时listener端口，保证advertised WSS/TLS_TCP endpoint与实际入口一致。
func integrationPort(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// integrationResponseStatus 安全读取可能为空的WebSocket handshake response。
func integrationResponseStatus(response *http.Response) int {
	if response == nil {
		return 0
	}
	return response.StatusCode
}

// enableIntegrationTLS 将临时identity接入单条storage公开进程及gameplay TCP配置。
func enableIntegrationTLS(t *testing.T, path string, certificatePath string, privateKeyEnvironment string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("publicApi:\n")
	tlsPolicy := []byte(fmt.Sprintf("publicApi:\n  tls:\n    enabled: true\n    certificateFile: '%s'\n    privateKeySecret: env:%s\n", strings.ReplaceAll(certificatePath, "'", "''"), privateKeyEnvironment))
	updated := bytes.Replace(body, marker, tlsPolicy, 1)
	if bytes.Equal(updated, body) {
		t.Fatal("publicApi marker is missing from integration config")
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newIntegrationTLSClient 只信任本测试临时自签名证书并强制TLS 1.3。
func newIntegrationTLSClient(t *testing.T, certificatePath string) *http.Client {
	t.Helper()
	return &http.Client{Transport: &http.Transport{TLSClientConfig: newIntegrationTLSConfig(t, certificatePath)}, Timeout: 5 * time.Second}
}

// newIntegrationTLSConfig 只信任本测试临时自签名证书并强制TLS 1.3与localhost identity。
func newIntegrationTLSConfig(t *testing.T, certificatePath string) *tls.Config {
	t.Helper()
	certificate, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("append integration certificate failed")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"}
}

// reserveAddress 让 OS 选择 loopback 端口后立即释放，供单进程 listener rollback 验收使用。
func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// waitForReady 有界轮询 diagnostic readyz，不把 listener 启动等同于 graph ready。
func waitForReady(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/readyz")
		if err == nil {
			// 测试只消费状态码；关闭只读响应失败不改变 readiness 断言。
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("process did not become ready")
}

// assertAddressReusable 证明 diagnostic 在逆序关闭的最后阶段已释放端口。
func assertAddressReusable(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("diagnostic listener 未最后关闭：%v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("关闭复用性探针 listener 失败：%v", err)
	}
}

// runDocker 只执行 harness 已登记 container 的 stop/start fault injection。
func runDocker(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("docker", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker operation failed: %v (%s)", err, output)
	}
}

// waitForContainerHealthy 在新进程恢复前确认 Docker dependency 已完成自身初始化。
func waitForContainerHealthy(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.Command("docker", "inspect", "--format", "{{.State.Health.Status}}", name)
		output, err := command.Output()
		if err == nil && string(output) == "healthy\n" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("container did not become healthy")
}
