package simulationcontrol

import (
	"errors"
	"sync"
)

// ProposalInbox 是 reader 与 result coordinator 之间的有界内存接缝。
type ProposalInbox struct {
	// mutex 保护 queue。
	mutex sync.Mutex
	// queue 保存尚未交给 coordinator 的 immutable proposal 副本。
	queue []ResultProposal
}

// NewProposalInbox 创建预留到 hard limit 的空 inbox。
func NewProposalInbox() *ProposalInbox {
	return &ProposalInbox{queue: make([]ResultProposal, 0, PendingRequestLimit)}
}

// OfferResult 验证 proposal 并在容量耗尽时 fail closed。
func (inbox *ProposalInbox) OfferResult(proposal ResultProposal) error {
	if inbox == nil || proposal.Validate() != nil {
		return errors.New("simulation proposal inbox input is invalid")
	}
	inbox.mutex.Lock()
	defer inbox.mutex.Unlock()
	for _, existing := range inbox.queue {
		if existing.ResultID != proposal.ResultID {
			continue
		}
		if existing == proposal {
			return nil
		}
		return errors.New("simulation proposal inbox result identity conflicts")
	}
	if len(inbox.queue) >= PendingRequestLimit {
		return errors.New("simulation proposal inbox capacity exhausted")
	}
	inbox.queue = append(inbox.queue, proposal)
	return nil
}

// Peek 返回最早 proposal 的副本但保留所有权；空 inbox 返回 false。
//
// Consumer 只有在 receipt 与 ack 都成功后才能调用 Discard，避免短暂存储或
// control failure 把尚未终结的 proposal 从 Go 侧观测队列静默移除。
func (inbox *ProposalInbox) Peek() (ResultProposal, bool) {
	if inbox == nil {
		return ResultProposal{}, false
	}
	inbox.mutex.Lock()
	defer inbox.mutex.Unlock()
	if len(inbox.queue) == 0 {
		return ResultProposal{}, false
	}
	return inbox.queue[0], true
}

// Discard 移除已终结的队首 proposal；队列变化或乱序消费时 fail closed。
func (inbox *ProposalInbox) Discard(proposal ResultProposal) error {
	if inbox == nil || proposal.Validate() != nil {
		return errors.New("simulation proposal inbox discard input is invalid")
	}
	inbox.mutex.Lock()
	defer inbox.mutex.Unlock()
	if len(inbox.queue) == 0 || inbox.queue[0] != proposal {
		return errors.New("simulation proposal inbox head changed")
	}
	copy(inbox.queue, inbox.queue[1:])
	inbox.queue = inbox.queue[:len(inbox.queue)-1]
	return nil
}

// Len 返回当前有界积压快照。
func (inbox *ProposalInbox) Len() int {
	if inbox == nil {
		return 0
	}
	inbox.mutex.Lock()
	defer inbox.mutex.Unlock()
	return len(inbox.queue)
}

var _ ProposalHandler = (*ProposalInbox)(nil)
