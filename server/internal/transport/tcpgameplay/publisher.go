package tcpgameplay

import (
	"context"
	"errors"

	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"google.golang.org/protobuf/proto"
)

// DeliveryResult 汇总一次窄target投递，不暴露身份或连接集合。
type DeliveryResult struct {
	// Matched 是受信target快照中的连接数。
	Matched int
	// Enqueued 是成功进入有界队列的连接数。
	Enqueued int
	// Closed 是已发起关闭的连接数，不表示peer已完成关闭握手。
	Closed int
}

// Publisher 只向registry索引解析出的受信target投递三个登记PUSH。
type Publisher struct {
	// registry 持有索引、状态、codec与唯一socket writer。
	registry *Registry
	// observer 只记录message ID与稳定结果。
	observer Observer
}

// NewPublisher 构造不持有socket的typed publisher。
func NewPublisher(registry *Registry, observer Observer) (*Publisher, error) {
	if registry == nil || observer == nil {
		return nil, errors.New("tcp gameplay publisher dependencies are incomplete")
	}
	return &Publisher{registry: registry, observer: observer}, nil
}

// PublishConnection 向单个ConnectionID投递且仍校验payload target。
func (publisher *Publisher) PublishConnection(connectionID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	entry, err := publisher.registry.Entry(connectionID)
	if err != nil {
		return DeliveryResult{}, err
	}
	return publisher.publish([]*connection{entry}, messageID, payload)
}

// PublishPlayer 向认证PlayerID的全部当前连接投递。
func (publisher *Publisher) PublishPlayer(playerID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	return publisher.publishIndex(publisher.registry.byPlayer, playerID, messageID, payload)
}

// PublishWorld 向PersonalWorldID绑定的全部当前连接投递。
func (publisher *Publisher) PublishWorld(worldID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	return publisher.publishIndex(publisher.registry.byWorld, worldID, messageID, payload)
}

// PublishVisit 向VisitSessionID绑定的全部当前连接投递。
func (publisher *Publisher) PublishVisit(visitID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	return publisher.publishIndex(publisher.registry.byVisit, visitID, messageID, payload)
}

// PublishVisitor 只投递VisitSessionID与VisitorID两个受信索引的交集。
func (publisher *Publisher) PublishVisitor(visitID string, visitorID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if visitID == "" || visitorID == "" {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	publisher.registry.mu.Lock()
	if publisher.registry.stopped {
		publisher.registry.mu.Unlock()
		return DeliveryResult{}, ErrQueueClosed
	}
	visitConnections := publisher.registry.byVisit[visitID]
	playerConnections := publisher.registry.byPlayer[visitorID]
	entries := make([]*connection, 0)
	for id := range visitConnections {
		if _, matchesPlayer := playerConnections[id]; matchesPlayer {
			if entry := publisher.registry.connections[id]; entry != nil {
				entries = append(entries, entry)
			}
		}
	}
	publisher.registry.mu.Unlock()
	if len(entries) == 0 {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	return publisher.publish(entries, messageID, payload)
}

// publishIndex 在registry锁内只复制entry指针快照，实际编码与入队不持有全局锁。
func (publisher *Publisher) publishIndex(index map[string]map[string]struct{}, key string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if key == "" {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	publisher.registry.mu.Lock()
	if publisher.registry.stopped {
		publisher.registry.mu.Unlock()
		return DeliveryResult{}, ErrQueueClosed
	}
	ids := index[key]
	entries := make([]*connection, 0, len(ids))
	for id := range ids {
		if entry := publisher.registry.connections[id]; entry != nil {
			entries = append(entries, entry)
		}
	}
	publisher.registry.mu.Unlock()
	if len(entries) == 0 {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	return publisher.publish(entries, messageID, payload)
}

// publish 逐连接校验target，slow consumer只关闭受影响连接且不跳过其他目标。
func (publisher *Publisher) publish(entries []*connection, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if messageID != 2002 && messageID != 2121 && messageID != 2122 {
		return DeliveryResult{}, errors.New("tcp gameplay push message is not allowed")
	}
	result := DeliveryResult{Matched: len(entries)}
	var resultErr error
	for _, entry := range entries {
		if err := validatePushTarget(entry, messageID, payload); err != nil {
			resultErr = errors.Join(resultErr, err)
			publisher.observer.ObserveTCPPush(messageID, "target_rejected")
			continue
		}
		if messageID == 2122 {
			if err := publisher.enqueueSafeReturn(entry, payload); err != nil {
				resultErr = errors.Join(resultErr, err)
				result.Closed++
				continue
			}
			result.Enqueued++
			result.Closed++
			continue
		}
		if err := entry.enqueuePush(messageID, payload, false); err != nil {
			resultErr = errors.Join(resultErr, err)
			publisher.observer.ObserveTCPPush(messageID, "queue_rejected")
			if errors.Is(err, ErrQueueFull) {
				publisher.observer.ObserveTCPClose("slow_consumer")
				entry.closeOnce.Do(func() { _ = entry.socket.Close() })
				result.Closed++
			}
			continue
		}
		result.Enqueued++
		publisher.observer.ObserveTCPPush(messageID, "enqueued")
	}
	return result, resultErr
}

// enqueueSafeReturn 原子阻止旧target新mutation，并让唯一writer在写出后关闭连接。
func (publisher *Publisher) enqueueSafeReturn(entry *connection, payload proto.Message) error {
	entry.mu.Lock()
	if entry.state != ConnectionStateActive {
		entry.mu.Unlock()
		return errors.New("tcp gameplay safe return requires active binding")
	}
	entry.state = ConnectionStateReturning
	entry.closeClass = CloseClassApplicationReturn
	if entry.dispatching {
		for _, pending := range entry.pendingPushes {
			if pending.closeAfter {
				entry.mu.Unlock()
				return errors.New("tcp gameplay safe return is already pending")
			}
		}
		estimatedBytes := proto.Size(payload) + 256
		if len(entry.pendingPushes) >= cap(entry.queue.items) || entry.pendingPushBytes+estimatedBytes > entry.queue.byteLimit {
			entry.mu.Unlock()
			return ErrQueueFull
		}
		entry.pendingPushes = append(entry.pendingPushes, pendingDispatchPush{messageID: 2122, payload: proto.Clone(payload), closeAfter: true})
		entry.pendingPushBytes += estimatedBytes
		entry.mu.Unlock()
		publisher.observer.ObserveTCPPush(2122, "enqueued")
		return nil
	}
	entry.mu.Unlock()
	if err := entry.enqueuePush(2122, payload, true); err != nil {
		entry.closeOnce.Do(func() { _ = entry.socket.Close() })
		return err
	}
	publisher.observer.ObserveTCPPush(2122, "enqueued")
	return nil
}

// validatePushTarget 要求payload中的公开target与当前受信connection binding一致。
func validatePushTarget(entry *connection, messageID uint32, payload proto.Message) error {
	if entry == nil || payload == nil {
		return errors.New("tcp gameplay push target is invalid")
	}
	switch messageID {
	case 2002:
		message, ok := payload.(*worldv1.WorldSnapshotPush)
		if !ok || message.GetSnapshot() == nil || message.GetSnapshot().GetWorld() == nil || message.GetSnapshot().GetWorld().GetPersonalWorldId() != entry.worldID {
			return errors.New("tcp gameplay world push target mismatch")
		}
	case 2121:
		message, ok := payload.(*visitv1.VisitSnapshotPush)
		if !ok || message.GetSnapshot() == nil {
			return errors.New("tcp gameplay visit push target mismatch")
		}
		snapshot := message.GetSnapshot()
		visitorTarget := entry.visitID != "" && snapshot.GetVisitSessionId() == entry.visitID
		ownerTarget := entry.visitID == "" && snapshot.GetOwnerPlayerId() == entry.playerID && snapshot.GetAssignment() != nil && snapshot.GetAssignment().GetPersonalWorldId() == entry.worldID
		if !visitorTarget && !ownerTarget {
			return errors.New("tcp gameplay visit push target mismatch")
		}
	case 2122:
		message, ok := payload.(*visitv1.VisitSafeReturnPush)
		if !ok || message.GetDirective() == nil || message.GetDirective().GetVisitSessionId() != entry.visitID || message.GetDirective().GetVisitorId() != entry.playerID {
			return errors.New("tcp gameplay safe return target mismatch")
		}
	default:
		return errors.New("tcp gameplay push message is not allowed")
	}
	return nil
}

// Invalidate 实现Session提交后的旧epoch连接清理；没有活动连接视为幂等成功。
func (registry *Registry) Invalidate(ctx context.Context, invalidation session.Invalidation) error {
	if ctx == nil || !invalidation.SessionID.Valid() || !invalidation.Epoch.Valid() || invalidation.Reason == session.InvalidationReasonUnspecified {
		return errors.New("tcp gameplay invalidation is invalid")
	}
	registry.mu.Lock()
	ids := registry.bySession[invalidation.SessionID.String()]
	entries := make([]*connection, 0, len(ids))
	notStarted := make([]*connection, 0, len(ids))
	for id := range ids {
		entry := registry.connections[id]
		if entry != nil && uint64(entry.auth.Epoch()) < uint64(invalidation.Epoch) {
			entry.startRejected = true
			entry.mu.Lock()
			entry.closeClass = CloseClassInvalidated
			entry.mu.Unlock()
			entries = append(entries, entry)
			if !entry.started {
				notStarted = append(notStarted, entry)
			}
		}
	}
	registry.mu.Unlock()
	for _, entry := range entries {
		entry.queue.close()
		entry.closeOnce.Do(func() { _ = entry.socket.Close() })
	}
	for _, entry := range notStarted {
		registry.Remove(entry.id)
	}
	for _, entry := range entries {
		select {
		case <-entry.done:
		case <-ctx.Done():
			registry.observer.ObserveTCPInvalidation("deadline")
			return context.Cause(ctx)
		}
	}
	registry.observer.ObserveTCPInvalidation("closed")
	return nil
}
