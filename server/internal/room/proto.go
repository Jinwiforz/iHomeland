package room

import pb "ihomeland/server/internal/protocol/pb/realtime/v1"

// ProtoSnapshot 将房间快照转换为实时协议消息。
func ProtoSnapshot(snapshot *Snapshot) *pb.RoomSnapshot {
	if snapshot == nil {
		return nil
	}
	members := make([]*pb.RoomMemberSnapshot, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		members = append(members, &pb.RoomMemberSnapshot{
			PlayerId:            member.PlayerID,
			Seat:                uint32(member.Seat),
			Team:                protoTeam(member.Team),
			Ready:               member.Ready,
			Host:                member.Host,
			ConnectionState:     protoConnectionState(member.ConnectionState),
			ReconnectDeadlineMs: member.ReconnectDeadline.UnixMilli(),
		})
	}
	return &pb.RoomSnapshot{
		RoomId:       snapshot.RoomID,
		Name:         snapshot.Name,
		State:        protoRoomState(snapshot.State),
		HostPlayerId: snapshot.HostPlayerID,
		Capacity:     uint32(snapshot.Capacity),
		Members:      members,
	}
}

func protoRoomState(state RoomState) pb.RoomState {
	switch state {
	case RoomStateOpen:
		return pb.RoomState_ROOM_STATE_OPEN
	case RoomStateClosed:
		return pb.RoomState_ROOM_STATE_CLOSED
	default:
		return pb.RoomState_ROOM_STATE_UNSPECIFIED
	}
}

func protoConnectionState(state MemberConnectionState) pb.RoomMemberConnectionState {
	switch state {
	case MemberConnectionStateOnline:
		return pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_ONLINE
	case MemberConnectionStateDisconnected:
		return pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_DISCONNECTED
	default:
		return pb.RoomMemberConnectionState_ROOM_MEMBER_CONNECTION_STATE_UNSPECIFIED
	}
}

func protoTeam(team Team) pb.RoomTeam {
	switch team {
	case TeamA:
		return pb.RoomTeam_ROOM_TEAM_A
	case TeamB:
		return pb.RoomTeam_ROOM_TEAM_B
	default:
		return pb.RoomTeam_ROOM_TEAM_UNSPECIFIED
	}
}
