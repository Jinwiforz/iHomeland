package tcpgameplay

import (
	"context"
	"errors"

	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// Handshake 按ticket后admission的固定顺序建立不可伪造连接资格。
type Handshake struct {
	// tickets 原子消费一次性TLS_TCP ConnectionTicket。
	tickets TicketConsumer
	// admissions 原子消费或用同一identity恢复WorldAdmission资格。
	admissions AdmissionVerifier
	// endpoint 是配置owner提供的受信advertised TLS_TCP地址。
	endpoint session.Endpoint
	// observer 只记录稳定阶段与结果。
	observer Observer
}

// NewHandshake 构造无listener、无后台任务的双credential认证器。
func NewHandshake(tickets TicketConsumer, admissions AdmissionVerifier, endpoint session.Endpoint, observer Observer) (*Handshake, error) {
	if tickets == nil || admissions == nil || observer == nil || !endpoint.Valid() || endpoint.Channel() != session.ChannelTLSTCP {
		return nil, errors.New("tcp gameplay handshake dependencies are incomplete")
	}
	return &Handshake{tickets: tickets, admissions: admissions, endpoint: endpoint, observer: observer}, nil
}

// Authenticate 消费preface并返回只读AuthContext与Qualification；失败不会补偿已提交credential。
func (handshake *Handshake) Authenticate(ctx context.Context, preface Preface, reserved *reservation) (session.AuthContext, worldadmission.Qualification, error) {
	if handshake == nil || !preface.Valid() || reserved == nil {
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay handshake input is invalid")
	}
	auth, err := handshake.tickets.ConsumeTicket(ctx, preface.Ticket(), session.ChannelTLSTCP, handshake.endpoint)
	if err != nil {
		handshake.observer.ObserveTCPHandshake("ticket", "rejected")
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay ticket rejected")
	}
	if !auth.Valid() || auth.Channel() != session.ChannelTLSTCP || !auth.HasScope(session.ScopeGameplay) {
		handshake.observer.ObserveTCPHandshake("ticket", "scope_rejected")
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay ticket scope rejected")
	}
	handshake.observer.ObserveTCPHandshake("ticket", "accepted")
	consumeID, err := reserved.ConsumeID()
	if err != nil {
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay consume identity rejected")
	}
	qualification, err := handshake.admissions.Verify(ctx, preface.Admission(), consumeID, auth, handshake.endpoint, preface.Purpose())
	if err != nil {
		handshake.observer.ObserveTCPHandshake("admission", "rejected_after_ticket")
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay admission rejected")
	}
	if !qualification.Valid() {
		handshake.observer.ObserveTCPHandshake("admission", "dependency_defect")
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay admission result invalid")
	}
	binding := qualification.Binding()
	if binding.Purpose() != preface.Purpose() || !binding.Endpoint().Equal(handshake.endpoint) || binding.SessionID() != auth.SessionID() ||
		binding.Epoch() != auth.Epoch() || binding.PlayerID().String() != auth.Principal().PlayerID() {
		handshake.observer.ObserveTCPHandshake("admission", "binding_rejected")
		return session.AuthContext{}, worldadmission.Qualification{}, errors.New("tcp gameplay admission binding rejected")
	}
	handshake.observer.ObserveTCPHandshake("admission", "accepted")
	return auth, qualification, nil
}

// Reverify 使用相同ConnectionID消费身份恢复Join/Reconnect的response-loss结果。
func (handshake *Handshake) Reverify(ctx context.Context, entry *connection, credential worldadmission.Credential, purpose worldadmission.Purpose) (worldadmission.Qualification, error) {
	if handshake == nil || entry == nil || !credential.Valid() || (purpose != worldadmission.PurposeJoin && purpose != worldadmission.PurposeReconnect) {
		return worldadmission.Qualification{}, errors.New("tcp gameplay admission retry input is invalid")
	}
	consumeID, err := worldadmission.NewConsumeID(entry.id)
	if err != nil {
		return worldadmission.Qualification{}, errors.New("tcp gameplay consume identity rejected")
	}
	qualification, err := handshake.admissions.Verify(ctx, credential, consumeID, entry.auth, handshake.endpoint, purpose)
	if err != nil || !qualification.Valid() || !qualification.Binding().Equal(entry.qualification.Binding()) {
		return worldadmission.Qualification{}, errors.New("tcp gameplay admission retry rejected")
	}
	return qualification, nil
}
