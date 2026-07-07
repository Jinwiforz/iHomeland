package app

import (
	"context"
	"fmt"

	"ihomeland/server/internal/gateway"
	"ihomeland/server/internal/protocol"
	pb "ihomeland/server/internal/protocol/pb/realtime/v1"
)

type realtimeDispatcher struct {
	account *accountDispatcher
	room    *roomDispatcher
}

func newRealtimeDispatcher(account *accountDispatcher, room *roomDispatcher) *realtimeDispatcher {
	return &realtimeDispatcher{
		account: account,
		room:    room,
	}
}

func (d *realtimeDispatcher) Dispatch(ctx context.Context, req gateway.DispatchRequest) (*pb.Envelope, error) {
	messageID := protocol.MessageID(req.Envelope.GetMessageId())
	switch {
	case protocol.IsAccountMessageID(messageID) && d.account != nil:
		return d.account.Dispatch(ctx, req)
	case protocol.IsRoomMessageID(messageID) && d.room != nil:
		return d.room.Dispatch(ctx, req)
	default:
		return protocol.BuildErrorEnvelope(req.Envelope.GetRequestId(), req.Envelope.GetSequence(), protocol.ErrorCodeMessageIDUnsupported, "message id unsupported", fmt.Sprintf("message id %d has no handler", req.Envelope.GetMessageId()))
	}
}

func (d *realtimeDispatcher) OnDisconnect(ctx context.Context, session gateway.SessionSnapshot) {
	if d.room == nil {
		return
	}
	d.room.OnDisconnect(ctx, session)
}
