// Package gateway 负责实时连接入口、连接级 session 和协议分发。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"

	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

const (
	defaultWriteTimeout = 5 * time.Second
)

// CloseReason 描述网关关闭连接的稳定原因。
type CloseReason string

const (
	// CloseReasonClientClosed 表示客户端主动断开或正常关闭。
	CloseReasonClientClosed CloseReason = "client_closed"
	// CloseReasonServerClosed 表示服务端上下文结束。
	CloseReasonServerClosed CloseReason = "server_closed"
	// CloseReasonProtocolError 表示连接收到无法继续处理的协议错误。
	CloseReasonProtocolError CloseReason = "protocol_error"
	// CloseReasonIdleTimeout 表示连接超过空闲超时时间。
	CloseReasonIdleTimeout CloseReason = "idle_timeout"
	// CloseReasonWriteFailed 表示服务端写响应失败。
	CloseReasonWriteFailed CloseReason = "write_failed"
)

// Config 描述网关运行参数。
type Config struct {
	IdleTimeout time.Duration
}

// SessionSnapshot 是传递给业务分发边界的连接级只读视图。
type SessionSnapshot struct {
	ConnectionID    string
	RemoteAddr      string
	ProtocolVersion uint32
	CreatedAt       time.Time
	LastActiveAt    time.Time
}

// DispatchRequest 描述交给业务模块处理的实时消息。
type DispatchRequest struct {
	Session  SessionSnapshot
	Envelope *pb.Envelope
}

// Dispatcher 是网关与后续业务模块之间的分发边界。
type Dispatcher interface {
	Dispatch(ctx context.Context, req DispatchRequest) (*pb.Envelope, error)
}

// Server 管理 WebSocket 连接生命周期和基础系统消息。
type Server struct {
	cfg        Config
	log        *slog.Logger
	dispatcher Dispatcher
	now        func() time.Time

	nextConnectionID uint64

	mu       sync.Mutex
	sessions map[string]*Session
}

// Session 描述单个 WebSocket 连接的运行态元数据。
type Session struct {
	connectionID    string
	remoteAddr      string
	protocolVersion uint32
	createdAt       time.Time
	lastActiveAt    time.Time
	closeReason     CloseReason
	nextSequence    uint64
}

// NewServer 创建实时网关服务。
func NewServer(cfg Config, log *slog.Logger, dispatcher Dispatcher) (*Server, error) {
	if cfg.IdleTimeout <= 0 {
		return nil, errors.New("gateway idle timeout must be positive")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:        cfg,
		log:        log.With("component", "gateway"),
		dispatcher: dispatcher,
		now:        time.Now,
		sessions:   make(map[string]*Session),
	}, nil
}

// RegisterRoutes 注册 WebSocket 网关路由。
func RegisterRoutes(router gin.IRouter, server *Server) {
	router.GET("/ws", func(c *gin.Context) {
		server.Handle(c.Writer, c.Request)
	})
}

// Handle 处理 WebSocket 升级和连接生命周期。
func (s *Server) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		s.log.Warn("websocket accept failed", "operation", "accept", "error", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "connection closed")

	session := s.registerSession(r.RemoteAddr)
	closeReason := CloseReasonClientClosed
	defer func() {
		s.unregisterSession(session.connectionID, closeReason)
		s.log.Info(
			"websocket connection closed",
			"operation", "close",
			"connection_id", session.connectionID,
			"remote_addr", session.remoteAddr,
			"close_reason", closeReason,
		)
	}()

	s.log.Info(
		"websocket connection opened",
		"operation", "open",
		"connection_id", session.connectionID,
		"remote_addr", session.remoteAddr,
	)

	for {
		readCtx, cancel := context.WithTimeout(r.Context(), s.cfg.IdleTimeout)
		messageType, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			closeReason = classifyReadCloseReason(r.Context(), err)
			if closeReason == CloseReasonIdleTimeout {
				_ = conn.Close(websocket.StatusPolicyViolation, "idle timeout")
				s.log.Warn("websocket idle timeout", "operation", "read", "connection_id", session.connectionID)
			}
			return
		}

		if messageType != websocket.MessageBinary {
			closeReason = CloseReasonProtocolError
			s.log.Warn("websocket non-binary message", "operation", "read", "connection_id", session.connectionID)
			_ = conn.Close(websocket.StatusUnsupportedData, "binary protobuf envelope required")
			return
		}

		if err := s.handleEnvelope(r.Context(), conn, session, data); err != nil {
			closeReason = err.closeReason
			if err.closeConnection {
				_ = conn.Close(err.status, err.reason)
				return
			}
		}
	}
}

// SessionCount 返回当前注册的连接级 session 数量，供测试和诊断使用。
func (s *Server) SessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) registerSession(remoteAddr string) *Session {
	now := s.now()
	id := fmt.Sprintf("conn-%d", atomic.AddUint64(&s.nextConnectionID, 1))
	session := &Session{
		connectionID: id,
		remoteAddr:   remoteAddr,
		createdAt:    now,
		lastActiveAt: now,
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	return session
}

func (s *Server) unregisterSession(connectionID string, reason CloseReason) {
	s.mu.Lock()
	if session, ok := s.sessions[connectionID]; ok {
		session.closeReason = reason
		delete(s.sessions, connectionID)
	}
	s.mu.Unlock()
}

func (s *Server) touchSession(session *Session, protocolVersion uint32) {
	s.mu.Lock()
	session.lastActiveAt = s.now()
	session.protocolVersion = protocolVersion
	s.mu.Unlock()
}

func (s *Server) snapshot(session *Session) SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionSnapshot{
		ConnectionID:    session.connectionID,
		RemoteAddr:      session.remoteAddr,
		ProtocolVersion: session.protocolVersion,
		CreatedAt:       session.createdAt,
		LastActiveAt:    session.lastActiveAt,
	}
}

func (s *Server) nextSessionSequence(session *Session) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	session.nextSequence++
	return session.nextSequence
}

type handlingError struct {
	closeConnection bool
	closeReason     CloseReason
	status          websocket.StatusCode
	reason          string
}

func (s *Server) handleEnvelope(ctx context.Context, conn *websocket.Conn, session *Session, data []byte) *handlingError {
	envelope := &pb.Envelope{}
	if err := proto.Unmarshal(data, envelope); err != nil {
		s.log.Warn("websocket envelope unmarshal failed", "operation", "decode", "connection_id", session.connectionID, "error", err)
		if writeErr := s.writeError(ctx, conn, session, "", protocol.ErrorCodePayloadInvalid, "payload invalid", "envelope decode failed"); writeErr != nil {
			return writeFailure(writeErr)
		}
		return nil
	}

	s.touchSession(session, envelope.GetProtocolVersion())

	if err := protocol.CheckVersion(envelope.GetProtocolVersion()); err != nil {
		versionErr := err.(protocol.VersionError)
		response, buildErr := protocol.BuildEnvelope(protocol.BuildOptions{
			ProtocolVersion: protocol.MaxSupportedVersion,
			MessageID:       protocol.MessageIDProtocolVersionUnsupported,
			RequestID:       envelope.GetRequestId(),
			Sequence:        s.nextSessionSequence(session),
			Timestamp:       s.now(),
		}, versionErr.Response())
		if buildErr != nil {
			return writeFailure(buildErr)
		}
		if writeErr := s.writeEnvelope(ctx, conn, response); writeErr != nil {
			return writeFailure(writeErr)
		}
		s.log.Warn("websocket protocol version unsupported", "operation", "version_check", "connection_id", session.connectionID, "protocol_version", envelope.GetProtocolVersion())
		return &handlingError{
			closeConnection: true,
			closeReason:     CloseReasonProtocolError,
			status:          websocket.StatusPolicyViolation,
			reason:          "protocol version unsupported",
		}
	}

	messageID := protocol.MessageID(envelope.GetMessageId())
	if !protocol.IsSystemMessageID(messageID) {
		return s.dispatchBusiness(ctx, conn, session, envelope)
	}

	message, err := protocol.DecodeEnvelope(envelope, true)
	if err != nil {
		if writeErr := s.writeProtocolError(ctx, conn, session, envelope.GetRequestId(), err); writeErr != nil {
			return writeFailure(writeErr)
		}
		return nil
	}

	switch msg := message.(type) {
	case *pb.HeartbeatRequest:
		response, buildErr := protocol.BuildEnvelope(protocol.BuildOptions{
			ProtocolVersion: protocol.MaxSupportedVersion,
			MessageID:       protocol.MessageIDHeartbeatResponse,
			RequestID:       envelope.GetRequestId(),
			Sequence:        s.nextSessionSequence(session),
			Timestamp:       s.now(),
		}, &pb.HeartbeatResponse{
			ClientTimeMs: msg.GetClientTimeMs(),
			ServerTimeMs: s.now().UnixMilli(),
		})
		if buildErr != nil {
			return writeFailure(buildErr)
		}
		if writeErr := s.writeEnvelope(ctx, conn, response); writeErr != nil {
			return writeFailure(writeErr)
		}
	default:
		if writeErr := s.writeError(ctx, conn, session, envelope.GetRequestId(), protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", fmt.Sprintf("message id %d is not accepted by gateway", messageID)); writeErr != nil {
			return writeFailure(writeErr)
		}
	}

	return nil
}

func (s *Server) dispatchBusiness(ctx context.Context, conn *websocket.Conn, session *Session, envelope *pb.Envelope) *handlingError {
	if envelope.GetRequestId() == "" {
		if err := s.writeError(ctx, conn, session, "", protocol.ErrorCodeRequestIDRequired, "request id required", "request_id is required"); err != nil {
			return writeFailure(err)
		}
		return nil
	}
	if s.dispatcher == nil {
		if err := s.writeError(ctx, conn, session, envelope.GetRequestId(), protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", fmt.Sprintf("message id %d has no handler", envelope.GetMessageId())); err != nil {
			return writeFailure(err)
		}
		return nil
	}

	response, err := s.dispatcher.Dispatch(ctx, DispatchRequest{
		Session:  s.snapshot(session),
		Envelope: envelope,
	})
	if err != nil {
		s.log.Warn("websocket dispatch failed", "operation", "dispatch", "connection_id", session.connectionID, "message_id", envelope.GetMessageId(), "error", err)
		if writeErr := s.writeError(ctx, conn, session, envelope.GetRequestId(), protocol.ErrorCodeMessageIDUnsupported, "message dispatch failed", "dispatcher returned error"); writeErr != nil {
			return writeFailure(writeErr)
		}
		return nil
	}
	if response != nil {
		if writeErr := s.writeEnvelope(ctx, conn, response); writeErr != nil {
			return writeFailure(writeErr)
		}
	}
	return nil
}

func (s *Server) writeProtocolError(ctx context.Context, conn *websocket.Conn, session *Session, requestID string, err error) error {
	switch {
	case errors.Is(err, protocol.ErrRequestIDRequired):
		return s.writeError(ctx, conn, session, requestID, protocol.ErrorCodeRequestIDRequired, "request id required", "request_id is required")
	case errors.Is(err, protocol.ErrMessageIDUnsupported):
		return s.writeError(ctx, conn, session, requestID, protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", err.Error())
	case errors.Is(err, protocol.ErrPayloadInvalid):
		return s.writeError(ctx, conn, session, requestID, protocol.ErrorCodePayloadInvalid, "payload invalid", err.Error())
	default:
		return s.writeError(ctx, conn, session, requestID, protocol.ErrorCodePayloadInvalid, "payload invalid", err.Error())
	}
}

func (s *Server) writeError(ctx context.Context, conn *websocket.Conn, session *Session, requestID string, code protocol.ErrorCode, message string, detail string) error {
	envelope, err := protocol.BuildErrorEnvelope(requestID, s.nextSessionSequence(session), code, message, detail)
	if err != nil {
		return err
	}
	s.log.Warn(
		"websocket protocol error",
		"operation", "write_error",
		"connection_id", session.connectionID,
		"request_id", requestID,
		"error_code", code,
		"detail", detail,
	)
	return s.writeEnvelope(ctx, conn, envelope)
}

func (s *Server) writeEnvelope(ctx context.Context, conn *websocket.Conn, envelope *pb.Envelope) error {
	data, err := proto.Marshal(envelope)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageBinary, data)
}

func classifyReadCloseReason(ctx context.Context, err error) CloseReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return CloseReasonIdleTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return CloseReasonServerClosed
	}
	if websocket.CloseStatus(err) != -1 {
		return CloseReasonClientClosed
	}
	return CloseReasonProtocolError
}

func writeFailure(err error) *handlingError {
	return &handlingError{
		closeConnection: true,
		closeReason:     CloseReasonWriteFailed,
		status:          websocket.StatusInternalError,
		reason:          "write failed",
	}
}
