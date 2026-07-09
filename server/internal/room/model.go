// Package room 实现第一阶段自定义房间大厅业务。
package room

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// MinCapacity 是自定义房间允许的最小容量。
	MinCapacity = 2
	// MaxCapacity 是第一阶段自定义房间允许的最大容量。
	MaxCapacity = 8
)

var (
	// ErrInvalidPlayerID 表示玩家身份为空或非法。
	ErrInvalidPlayerID = errors.New("invalid player id")
	// ErrInvalidRoomID 表示房间 ID 为空或非法。
	ErrInvalidRoomID = errors.New("invalid room id")
	// ErrInvalidRoomName 表示房间名称为空或非法。
	ErrInvalidRoomName = errors.New("invalid room name")
	// ErrInvalidCapacity 表示房间容量不在允许范围内。
	ErrInvalidCapacity = errors.New("invalid room capacity")
	// ErrRoomNotFound 表示目标房间不存在。
	ErrRoomNotFound = errors.New("room not found")
	// ErrRoomClosed 表示房间已关闭，不能继续执行大厅操作。
	ErrRoomClosed = errors.New("room closed")
	// ErrRoomFull 表示房间已满。
	ErrRoomFull = errors.New("room full")
	// ErrRoomNotReady 表示房间尚未满足开始条件。
	ErrRoomNotReady = errors.New("room not ready")
	// ErrMemberNotFound 表示玩家不是房间成员。
	ErrMemberNotFound = errors.New("room member not found")
	// ErrPermissionDenied 表示调用方没有执行该操作的权限。
	ErrPermissionDenied = errors.New("room permission denied")
	// ErrReconnectExpired 表示玩家重连资格不存在或已过期。
	ErrReconnectExpired = errors.New("room reconnect expired")
)

// RoomState 描述房间生命周期。
type RoomState string

const (
	// RoomStateOpen 表示房间可加入并可进行大厅操作。
	RoomStateOpen RoomState = "open"
	// RoomStateStarted 表示房间已通过第一阶段开始闸门，不再接受大厅变更。
	RoomStateStarted RoomState = "started"
	// RoomStateClosed 表示房间已关闭或解散。
	RoomStateClosed RoomState = "closed"
)

// MemberConnectionState 描述成员连接状态。
type MemberConnectionState string

const (
	// MemberConnectionStateOnline 表示成员当前在线。
	MemberConnectionStateOnline MemberConnectionState = "online"
	// MemberConnectionStateDisconnected 表示成员断线但仍处于保留期内。
	MemberConnectionStateDisconnected MemberConnectionState = "disconnected"
)

// Team 描述第一阶段房间阵营。
type Team string

const (
	// TeamA 表示 A 阵营。
	TeamA Team = "A"
	// TeamB 表示 B 阵营。
	TeamB Team = "B"
)

// Room 描述自定义房间内部状态，所有修改必须通过方法完成。
type Room struct {
	ID           string
	Name         string
	State        RoomState
	HostPlayerID string
	Capacity     int
	Members      map[string]*Member
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Member 描述房间成员内部状态。
type Member struct {
	PlayerID            string
	Seat                int
	Team                Team
	Ready               bool
	ConnectionState     MemberConnectionState
	ReconnectDeadline   time.Time
	JoinedAt            time.Time
	LastConnectionAt    time.Time
	LastDisconnectionAt time.Time
}

// Snapshot 是房间状态的只读视图。
type Snapshot struct {
	RoomID       string
	Name         string
	State        RoomState
	HostPlayerID string
	Capacity     int
	Members      []MemberSnapshot
}

// MemberSnapshot 是房间成员状态的只读视图。
type MemberSnapshot struct {
	PlayerID          string
	Seat              int
	Team              Team
	Ready             bool
	Host              bool
	ConnectionState   MemberConnectionState
	ReconnectDeadline time.Time
}

// NewRoom 创建开放状态房间，并把创建者设为房主。
func NewRoom(id string, name string, hostPlayerID string, capacity int, now time.Time) (*Room, error) {
	if err := validateRoomID(id); err != nil {
		return nil, err
	}
	if err := validateRoomName(name); err != nil {
		return nil, err
	}
	if err := validatePlayerID(hostPlayerID); err != nil {
		return nil, err
	}
	if err := validateCapacity(capacity); err != nil {
		return nil, err
	}

	room := &Room{
		ID:           id,
		Name:         strings.TrimSpace(name),
		State:        RoomStateOpen,
		HostPlayerID: strings.TrimSpace(hostPlayerID),
		Capacity:     capacity,
		Members:      make(map[string]*Member),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := room.addMember(hostPlayerID, now); err != nil {
		return nil, err
	}
	return room, nil
}

// Join 将玩家加入房间；重复加入返回现有成员身份。
func (r *Room) Join(playerID string, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	if err := validatePlayerID(playerID); err != nil {
		return nil, err
	}
	if member, ok := r.Members[strings.TrimSpace(playerID)]; ok {
		member.ConnectionState = MemberConnectionStateOnline
		member.LastConnectionAt = now
		member.ReconnectDeadline = time.Time{}
		r.UpdatedAt = now
		return r.Snapshot(), nil
	}
	if len(r.Members) >= r.Capacity {
		return nil, ErrRoomFull
	}
	if _, err := r.addMember(playerID, now); err != nil {
		return nil, err
	}
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// SetReady 设置成员准备状态。
func (r *Room) SetReady(playerID string, ready bool, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	member, err := r.onlineMember(playerID)
	if err != nil {
		return nil, err
	}
	member.Ready = ready
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// Start 校验第一阶段开始闸门并把房间置为已开始状态。
func (r *Room) Start(actorPlayerID string, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	actorPlayerID = strings.TrimSpace(actorPlayerID)
	if err := validatePlayerID(actorPlayerID); err != nil {
		return nil, err
	}
	if actorPlayerID != r.HostPlayerID {
		return nil, ErrPermissionDenied
	}
	if _, err := r.onlineMember(actorPlayerID); err != nil {
		return nil, err
	}
	for _, member := range r.Members {
		if member.ConnectionState != MemberConnectionStateOnline {
			return nil, ErrRoomNotReady
		}
		if member.PlayerID != r.HostPlayerID && !member.Ready {
			return nil, ErrRoomNotReady
		}
	}
	r.State = RoomStateStarted
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// Leave 移除成员；房主离开时按座位顺序转移房主，最后成员离开时关闭房间。
func (r *Room) Leave(playerID string, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	playerID = strings.TrimSpace(playerID)
	if _, ok := r.Members[playerID]; !ok {
		return nil, ErrMemberNotFound
	}
	delete(r.Members, playerID)
	if len(r.Members) == 0 {
		r.State = RoomStateClosed
		r.HostPlayerID = ""
		r.UpdatedAt = now
		return r.Snapshot(), nil
	}
	if r.HostPlayerID == playerID {
		r.HostPlayerID = r.firstMemberBySeat().PlayerID
	}
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// TransferHost 将房主转移给在线成员。
func (r *Room) TransferHost(actorPlayerID string, targetPlayerID string, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	actorPlayerID = strings.TrimSpace(actorPlayerID)
	targetPlayerID = strings.TrimSpace(targetPlayerID)
	if actorPlayerID != r.HostPlayerID {
		return nil, ErrPermissionDenied
	}
	target, err := r.onlineMember(targetPlayerID)
	if err != nil {
		return nil, err
	}
	r.HostPlayerID = target.PlayerID
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// Disconnect 将成员标记为断线并记录重连截止时间。
func (r *Room) Disconnect(playerID string, deadline time.Time, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	member, err := r.member(playerID)
	if err != nil {
		return nil, err
	}
	member.ConnectionState = MemberConnectionStateDisconnected
	member.Ready = false
	member.ReconnectDeadline = deadline
	member.LastDisconnectionAt = now
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// Reconnect 恢复断线保留期内的成员身份。
func (r *Room) Reconnect(playerID string, now time.Time) (*Snapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	member, err := r.member(playerID)
	if err != nil {
		return nil, err
	}
	if member.ConnectionState != MemberConnectionStateDisconnected || member.ReconnectDeadline.IsZero() || now.After(member.ReconnectDeadline) {
		return nil, ErrReconnectExpired
	}
	member.ConnectionState = MemberConnectionStateOnline
	member.ReconnectDeadline = time.Time{}
	member.LastConnectionAt = now
	r.UpdatedAt = now
	return r.Snapshot(), nil
}

// Snapshot 返回按座位排序的房间只读视图。
func (r *Room) Snapshot() *Snapshot {
	members := make([]MemberSnapshot, 0, len(r.Members))
	for _, member := range r.Members {
		members = append(members, MemberSnapshot{
			PlayerID:          member.PlayerID,
			Seat:              member.Seat,
			Team:              member.Team,
			Ready:             member.Ready,
			Host:              member.PlayerID == r.HostPlayerID,
			ConnectionState:   member.ConnectionState,
			ReconnectDeadline: member.ReconnectDeadline,
		})
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].Seat < members[j].Seat
	})
	return &Snapshot{
		RoomID:       r.ID,
		Name:         r.Name,
		State:        r.State,
		HostPlayerID: r.HostPlayerID,
		Capacity:     r.Capacity,
		Members:      members,
	}
}

func (r *Room) addMember(playerID string, now time.Time) (*Member, error) {
	playerID = strings.TrimSpace(playerID)
	if err := validatePlayerID(playerID); err != nil {
		return nil, err
	}
	seat, err := r.nextSeat()
	if err != nil {
		return nil, err
	}
	member := &Member{
		PlayerID:         playerID,
		Seat:             seat,
		Team:             teamForSeat(seat),
		ConnectionState:  MemberConnectionStateOnline,
		JoinedAt:         now,
		LastConnectionAt: now,
	}
	r.Members[playerID] = member
	return member, nil
}

func (r *Room) nextSeat() (int, error) {
	for seat := 1; seat <= r.Capacity; seat++ {
		used := false
		for _, member := range r.Members {
			if member.Seat == seat {
				used = true
				break
			}
		}
		if !used {
			return seat, nil
		}
	}
	return 0, ErrRoomFull
}

func (r *Room) firstMemberBySeat() *Member {
	var first *Member
	for _, member := range r.Members {
		if first == nil || member.Seat < first.Seat {
			first = member
		}
	}
	return first
}

func (r *Room) ensureOpen() error {
	if r.State != RoomStateOpen {
		return ErrRoomClosed
	}
	return nil
}

func (r *Room) onlineMember(playerID string) (*Member, error) {
	member, err := r.member(playerID)
	if err != nil {
		return nil, err
	}
	if member.ConnectionState != MemberConnectionStateOnline {
		return nil, ErrMemberNotFound
	}
	return member, nil
}

func (r *Room) member(playerID string) (*Member, error) {
	playerID = strings.TrimSpace(playerID)
	if err := validatePlayerID(playerID); err != nil {
		return nil, err
	}
	member, ok := r.Members[playerID]
	if !ok {
		return nil, ErrMemberNotFound
	}
	return member, nil
}

func validateRoomID(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidRoomID
	}
	return nil
}

func validateRoomName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrInvalidRoomName
	}
	return nil
}

func validatePlayerID(playerID string) error {
	if strings.TrimSpace(playerID) == "" {
		return ErrInvalidPlayerID
	}
	return nil
}

func validateCapacity(capacity int) error {
	if capacity < MinCapacity || capacity > MaxCapacity {
		return fmt.Errorf("%w: %d", ErrInvalidCapacity, capacity)
	}
	return nil
}

func teamForSeat(seat int) Team {
	if seat%2 == 0 {
		return TeamB
	}
	return TeamA
}
