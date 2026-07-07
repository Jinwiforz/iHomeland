package app

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"ihomeland/server/internal/account"
	"ihomeland/server/internal/gateway"
	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

type accountDispatcher struct {
	service *account.Service
}

func newAccountDispatcher(service *account.Service) *accountDispatcher {
	return &accountDispatcher{service: service}
}

func (d *accountDispatcher) Dispatch(ctx context.Context, req gateway.DispatchRequest) (*pb.Envelope, error) {
	message, err := protocol.DecodeEnvelope(req.Envelope, true)
	if err != nil {
		return protocol.BuildErrorEnvelope(req.Envelope.GetRequestId(), req.Envelope.GetSequence(), protocolErrorCode(err), "account request invalid", err.Error())
	}

	switch msg := message.(type) {
	case *pb.RegisterRequest:
		result, err := d.service.Register(ctx, account.RegisterRequest{
			Account:      msg.GetAccount(),
			Password:     msg.GetPassword(),
			DisplayName:  msg.GetDisplayName(),
			ConnectionID: req.Session.ConnectionID,
		})
		if err != nil {
			return d.accountError(req.Envelope, err)
		}
		bindIdentity(req, result)
		return buildAccountEnvelope(req.Envelope, protocol.MessageIDRegisterResponse, &pb.RegisterResponse{
			Player:       protoPlayer(result.Player),
			SessionToken: result.Session.Token,
			ExpiresAtMs:  result.Session.ExpiresAt.UnixMilli(),
		})
	case *pb.LoginRequest:
		result, err := d.service.Login(ctx, account.LoginRequest{
			Account:      msg.GetAccount(),
			Password:     msg.GetPassword(),
			ConnectionID: req.Session.ConnectionID,
		})
		if err != nil {
			return d.accountError(req.Envelope, err)
		}
		bindIdentity(req, result)
		return buildAccountEnvelope(req.Envelope, protocol.MessageIDLoginResponse, &pb.LoginResponse{
			Player:       protoPlayer(result.Player),
			SessionToken: result.Session.Token,
			ExpiresAtMs:  result.Session.ExpiresAt.UnixMilli(),
		})
	case *pb.LogoutRequest:
		if err := d.service.Logout(ctx, account.LogoutRequest{SessionToken: msg.GetSessionToken()}); err != nil {
			return d.accountError(req.Envelope, err)
		}
		if req.ClearIdentity != nil {
			req.ClearIdentity()
		}
		return buildAccountEnvelope(req.Envelope, protocol.MessageIDLogoutResponse, &pb.LogoutResponse{Success: true})
	case *pb.ResumeSessionRequest:
		result, err := d.service.ResumeSession(ctx, account.ResumeSessionRequest{
			SessionToken: msg.GetSessionToken(),
			ConnectionID: req.Session.ConnectionID,
		})
		if err != nil {
			return d.accountError(req.Envelope, err)
		}
		bindIdentity(req, result)
		return buildAccountEnvelope(req.Envelope, protocol.MessageIDResumeSessionResponse, &pb.ResumeSessionResponse{
			Player:       protoPlayer(result.Player),
			SessionToken: result.Session.Token,
			ExpiresAtMs:  result.Session.ExpiresAt.UnixMilli(),
		})
	case *pb.GetCurrentPlayerRequest:
		if req.Session.PlayerID == "" {
			return buildAccountEnvelope(req.Envelope, protocol.MessageIDGetCurrentPlayerResponse, &pb.GetCurrentPlayerResponse{Authenticated: false})
		}
		return buildAccountEnvelope(req.Envelope, protocol.MessageIDGetCurrentPlayerResponse, &pb.GetCurrentPlayerResponse{
			Player: &pb.PlayerProfile{
				PlayerId: req.Session.PlayerID,
			},
			Authenticated: true,
		})
	default:
		return protocol.BuildErrorEnvelope(req.Envelope.GetRequestId(), req.Envelope.GetSequence(), protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", fmt.Sprintf("message id %d is not handled by account", req.Envelope.GetMessageId()))
	}
}

func (d *accountDispatcher) accountError(envelope *pb.Envelope, err error) (*pb.Envelope, error) {
	code := accountErrorCode(err)
	return protocol.BuildErrorEnvelope(envelope.GetRequestId(), envelope.GetSequence(), code, accountErrorMessage(code), err.Error())
}

func buildAccountEnvelope(request *pb.Envelope, messageID protocol.MessageID, message proto.Message) (*pb.Envelope, error) {
	return protocol.BuildEnvelope(protocol.BuildOptions{
		ProtocolVersion: protocol.MaxSupportedVersion,
		MessageID:       messageID,
		RequestID:       request.GetRequestId(),
		Sequence:        request.GetSequence(),
	}, message)
}

func accountErrorCode(err error) protocol.ErrorCode {
	switch {
	case errors.Is(err, account.ErrAccountAlreadyExists):
		return protocol.ErrorCodeAccountAlreadyExists
	case errors.Is(err, account.ErrCredentialInvalid):
		return protocol.ErrorCodeAccountCredentialInvalid
	case errors.Is(err, account.ErrSessionInvalid):
		return protocol.ErrorCodeSessionInvalid
	case errors.Is(err, account.ErrUnauthenticated):
		return protocol.ErrorCodeUnauthenticated
	default:
		return protocol.ErrorCodePayloadInvalid
	}
}

func accountErrorMessage(code protocol.ErrorCode) string {
	switch code {
	case protocol.ErrorCodeAccountAlreadyExists:
		return "account already exists"
	case protocol.ErrorCodeAccountCredentialInvalid:
		return "credential invalid"
	case protocol.ErrorCodeSessionInvalid:
		return "session invalid"
	case protocol.ErrorCodeUnauthenticated:
		return "unauthenticated"
	default:
		return "account request failed"
	}
}

func bindIdentity(req gateway.DispatchRequest, result account.AuthResult) {
	if req.BindIdentity == nil {
		return
	}
	req.BindIdentity(result.Player.PlayerID, result.Session.Token)
}

func protoPlayer(player account.PlayerProfile) *pb.PlayerProfile {
	return &pb.PlayerProfile{
		PlayerId:    player.PlayerID,
		Account:     player.Account,
		DisplayName: player.DisplayName,
	}
}
