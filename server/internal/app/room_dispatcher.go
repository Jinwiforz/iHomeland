package app

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"ihomeland/server/internal/gateway"
	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
	"ihomeland/server/internal/room"
)

type roomDispatcher struct {
	service *room.Service
}

func newRoomDispatcher(service *room.Service) *roomDispatcher {
	return &roomDispatcher{service: service}
}

func (d *roomDispatcher) Dispatch(ctx context.Context, req gateway.DispatchRequest) (*pb.Envelope, error) {
	message, err := protocol.DecodeEnvelope(req.Envelope, true)
	if err != nil {
		return protocol.BuildErrorEnvelope(req.Envelope.GetRequestId(), req.Envelope.GetSequence(), protocolErrorCode(err), "room request invalid", err.Error())
	}

	switch msg := message.(type) {
	case *pb.CreateRoomRequest:
		snapshot, err := d.service.CreateRoom(ctx, room.CreateRoomRequest{
			PlayerID: msg.GetPlayerId(),
			RoomName: msg.GetRoomName(),
			Capacity: int(msg.GetCapacity()),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		d.service.BindConnection(req.Session.ConnectionID, msg.GetPlayerId(), snapshot.RoomID)
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDCreateRoomResponse, &pb.CreateRoomResponse{Room: room.ProtoSnapshot(snapshot)})
	case *pb.JoinRoomRequest:
		snapshot, err := d.service.JoinRoom(ctx, room.JoinRoomRequest{
			PlayerID: msg.GetPlayerId(),
			RoomID:   msg.GetRoomId(),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		d.service.BindConnection(req.Session.ConnectionID, msg.GetPlayerId(), snapshot.RoomID)
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDJoinRoomResponse, &pb.JoinRoomResponse{Room: room.ProtoSnapshot(snapshot)})
	case *pb.SetReadyRequest:
		snapshot, err := d.service.SetReady(ctx, room.SetReadyRequest{
			PlayerID: msg.GetPlayerId(),
			RoomID:   msg.GetRoomId(),
			Ready:    msg.GetReady(),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		d.service.BindConnection(req.Session.ConnectionID, msg.GetPlayerId(), snapshot.RoomID)
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDSetReadyResponse, &pb.SetReadyResponse{Room: room.ProtoSnapshot(snapshot)})
	case *pb.LeaveRoomRequest:
		snapshot, err := d.service.LeaveRoom(ctx, room.LeaveRoomRequest{
			PlayerID: msg.GetPlayerId(),
			RoomID:   msg.GetRoomId(),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDLeaveRoomResponse, &pb.LeaveRoomResponse{Room: room.ProtoSnapshot(snapshot)})
	case *pb.TransferHostRequest:
		snapshot, err := d.service.TransferHost(ctx, room.TransferHostRequest{
			PlayerID:       msg.GetPlayerId(),
			RoomID:         msg.GetRoomId(),
			TargetPlayerID: msg.GetTargetPlayerId(),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDTransferHostResponse, &pb.TransferHostResponse{Room: room.ProtoSnapshot(snapshot)})
	case *pb.ReconnectRoomRequest:
		snapshot, err := d.service.ReconnectMember(ctx, room.ReconnectMemberRequest{
			PlayerID: msg.GetPlayerId(),
			RoomID:   msg.GetRoomId(),
		})
		if err != nil {
			return d.roomError(req.Envelope, err)
		}
		d.service.BindConnection(req.Session.ConnectionID, msg.GetPlayerId(), snapshot.RoomID)
		return buildRoomEnvelope(req.Envelope, protocol.MessageIDReconnectRoomResponse, &pb.ReconnectRoomResponse{Room: room.ProtoSnapshot(snapshot)})
	default:
		return protocol.BuildErrorEnvelope(req.Envelope.GetRequestId(), req.Envelope.GetSequence(), protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", fmt.Sprintf("message id %d is not handled by room", req.Envelope.GetMessageId()))
	}
}

func (d *roomDispatcher) OnDisconnect(ctx context.Context, session gateway.SessionSnapshot) {
	_, _ = d.service.DisconnectConnection(ctx, session.ConnectionID)
}

func (d *roomDispatcher) roomError(envelope *pb.Envelope, err error) (*pb.Envelope, error) {
	return protocol.BuildErrorEnvelope(envelope.GetRequestId(), envelope.GetSequence(), protocol.ErrorCodePayloadInvalid, "room request failed", roomErrorDetail(err))
}

func buildRoomEnvelope(request *pb.Envelope, messageID protocol.MessageID, message proto.Message) (*pb.Envelope, error) {
	return protocol.BuildEnvelope(protocol.BuildOptions{
		ProtocolVersion: protocol.MaxSupportedVersion,
		MessageID:       messageID,
		RequestID:       request.GetRequestId(),
		Sequence:        request.GetSequence(),
	}, message)
}

func protocolErrorCode(err error) protocol.ErrorCode {
	switch {
	case errors.Is(err, protocol.ErrRequestIDRequired):
		return protocol.ErrorCodeRequestIDRequired
	case errors.Is(err, protocol.ErrMessageIDUnsupported):
		return protocol.ErrorCodeMessageIDUnsupported
	default:
		return protocol.ErrorCodePayloadInvalid
	}
}

func roomErrorDetail(err error) string {
	return err.Error()
}
