package room

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultReconnectTTL = 30 * time.Second
)

// Service 提供自定义房间大厅业务入口。
type Service struct {
	repo         Repository
	now          func() time.Time
	reconnectTTL time.Duration
	nextRoomID   uint64

	mu          sync.Mutex
	connections map[string]connectionBinding
}

type connectionBinding struct {
	playerID string
	roomID   string
}

// Config 描述房间服务配置。
type Config struct {
	ReconnectTTL time.Duration
}

// CreateRoomRequest 描述创建房间输入。
type CreateRoomRequest struct {
	PlayerID string
	RoomName string
	Capacity int
}

// JoinRoomRequest 描述加入房间输入。
type JoinRoomRequest struct {
	PlayerID string
	RoomID   string
}

// SetReadyRequest 描述设置准备状态输入。
type SetReadyRequest struct {
	PlayerID string
	RoomID   string
	Ready    bool
}

// LeaveRoomRequest 描述退出房间输入。
type LeaveRoomRequest struct {
	PlayerID string
	RoomID   string
}

// TransferHostRequest 描述转移房主输入。
type TransferHostRequest struct {
	PlayerID       string
	RoomID         string
	TargetPlayerID string
}

// ReconnectMemberRequest 描述重连恢复输入。
type ReconnectMemberRequest struct {
	PlayerID string
	RoomID   string
}

// NewService 创建房间服务。
func NewService(repo Repository, cfg Config) *Service {
	reconnectTTL := cfg.ReconnectTTL
	if reconnectTTL <= 0 {
		reconnectTTL = defaultReconnectTTL
	}
	return &Service{
		repo:         repo,
		now:          time.Now,
		reconnectTTL: reconnectTTL,
		connections:  make(map[string]connectionBinding),
	}
}

// CreateRoom 创建自定义房间。
func (s *Service) CreateRoom(ctx context.Context, req CreateRoomRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roomID := fmt.Sprintf("room-%d", atomic.AddUint64(&s.nextRoomID, 1))
	room, err := NewRoom(roomID, req.RoomName, req.PlayerID, req.Capacity, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(room); err != nil {
		return nil, err
	}
	return room.Snapshot(), nil
}

// JoinRoom 将玩家加入房间。
func (s *Service) JoinRoom(ctx context.Context, req JoinRoomRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot *Snapshot
	err := s.repo.Update(req.RoomID, func(room *Room) error {
		var err error
		snapshot, err = room.Join(req.PlayerID, s.now())
		return err
	})
	return snapshot, err
}

// SetReady 设置成员准备状态。
func (s *Service) SetReady(ctx context.Context, req SetReadyRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot *Snapshot
	err := s.repo.Update(req.RoomID, func(room *Room) error {
		var err error
		snapshot, err = room.SetReady(req.PlayerID, req.Ready, s.now())
		return err
	})
	return snapshot, err
}

// LeaveRoom 将成员移出房间。
func (s *Service) LeaveRoom(ctx context.Context, req LeaveRoomRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot *Snapshot
	err := s.repo.Update(req.RoomID, func(room *Room) error {
		var err error
		snapshot, err = room.Leave(req.PlayerID, s.now())
		return err
	})
	return snapshot, err
}

// TransferHost 转移房主。
func (s *Service) TransferHost(ctx context.Context, req TransferHostRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot *Snapshot
	err := s.repo.Update(req.RoomID, func(room *Room) error {
		var err error
		snapshot, err = room.TransferHost(req.PlayerID, req.TargetPlayerID, s.now())
		return err
	})
	return snapshot, err
}

// ReconnectMember 恢复断线保留期内的成员身份。
func (s *Service) ReconnectMember(ctx context.Context, req ReconnectMemberRequest) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var snapshot *Snapshot
	err := s.repo.Update(req.RoomID, func(room *Room) error {
		var err error
		snapshot, err = room.Reconnect(req.PlayerID, s.now())
		return err
	})
	return snapshot, err
}

// BindConnection 记录连接与房间成员的运行态关系。
func (s *Service) BindConnection(connectionID string, playerID string, roomID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[connectionID] = connectionBinding{playerID: playerID, roomID: roomID}
}

// DisconnectConnection 根据连接 ID 标记成员断线。
func (s *Service) DisconnectConnection(ctx context.Context, connectionID string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	binding, ok := s.connections[connectionID]
	if ok {
		delete(s.connections, connectionID)
	}
	s.mu.Unlock()
	if !ok {
		return nil, ErrMemberNotFound
	}
	return s.DisconnectMember(ctx, binding.roomID, binding.playerID)
}

// DisconnectMember 标记成员断线。
func (s *Service) DisconnectMember(ctx context.Context, roomID string, playerID string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := s.now()
	deadline := now.Add(s.reconnectTTL)
	var snapshot *Snapshot
	err := s.repo.Update(roomID, func(room *Room) error {
		var err error
		snapshot, err = room.Disconnect(playerID, deadline, now)
		return err
	})
	return snapshot, err
}
