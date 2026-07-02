package room

import (
	"fmt"
	"sync"
)

// Repository 描述房间运行态存储边界。
type Repository interface {
	Create(room *Room) error
	Update(roomID string, update func(room *Room) error) error
	FindPlayerRoom(playerID string) (*Snapshot, error)
}

// MemoryRepository 是第一阶段单进程内存房间存储。
type MemoryRepository struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

// NewMemoryRepository 创建内存房间存储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{rooms: make(map[string]*Room)}
}

// Create 保存新房间。
func (r *MemoryRepository) Create(room *Room) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rooms[room.ID]; exists {
		return fmt.Errorf("%w: %s", ErrInvalidRoomID, room.ID)
	}
	r.rooms[room.ID] = room
	return nil
}

// Update 在同一锁内读取并修改房间。
func (r *MemoryRepository) Update(roomID string, update func(room *Room) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	room, ok := r.rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	return update(room)
}

// FindPlayerRoom 查找玩家当前所属房间快照。
func (r *MemoryRepository) FindPlayerRoom(playerID string) (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, room := range r.rooms {
		if _, ok := room.Members[playerID]; ok && room.State == RoomStateOpen {
			return room.Snapshot(), nil
		}
	}
	return nil, ErrRoomNotFound
}
