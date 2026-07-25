package simulationcontrol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sessionPipeCloser 关闭 parent 持有的两个 pipe ends。
type sessionPipeCloser struct {
	// writer 是 parent 到 child 的写端。
	writer io.Closer
	// reader 是 child 到 parent 的读端。
	reader io.Closer
	// once 保证幂等。
	once sync.Once
}

// Close 关闭两个 ends。
func (closer *sessionPipeCloser) Close() error {
	var result error
	closer.once.Do(func() {
		result = errors.Join(closer.writer.Close(), closer.reader.Close())
	})
	return result
}

// TestSessionCancelAfterSendConsumesReceipt 验证 caller cancel 不会把旧 receipt 留给下一请求。
func TestSessionCancelAfterSendConsumesReceipt(t *testing.T) {
	t.Parallel()
	parentToChildReader, parentToChildWriter := io.Pipe()
	childToParentReader, childToParentWriter := io.Pipe()
	defer childToParentWriter.Close()
	nonce, _ := NewDigest(strings.Repeat("1", 64))
	session, err := NewSession(
		childToParentReader,
		parentToChildWriter,
		&sessionPipeCloser{writer: parentToChildWriter, reader: childToParentReader},
		nonce,
		time.Second,
		NewProposalInbox(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	firstReceived := make(chan struct{})
	releaseFirst := make(chan struct{})
	childDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(parentToChildReader)
		for index := 0; index < 2; index++ {
			request, err := DecodeFrame(reader)
			if err != nil {
				childDone <- err
				return
			}
			if index == 0 {
				close(firstReceived)
				<-releaseFirst
			}
			response, err := NewFrame(
				"node.health.receipt",
				struct {
					Healthy bool `json:"healthy"`
				}{Healthy: true},
				request.RequestID,
				request.Sequence+1,
				nonce,
			)
			if err == nil {
				err = WriteFrame(childToParentWriter, response)
			}
			if err != nil {
				childDone <- err
				return
			}
		}
		childDone <- nil
	}()
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	firstID, _ := NewRequestID("sctl_session_cancel_0001")
	go func() {
		_, callErr := session.Call(
			firstContext,
			firstID,
			"node.health.query",
			struct {
				SimulationNodeID string `json:"simulationNodeId"`
			}{SimulationNodeID: "snode_session_test"},
			"node.health.receipt",
		)
		firstResult <- callErr
	}()
	<-firstReceived
	cancelFirst()
	close(releaseFirst)
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Call error = %v", err)
	}
	secondID, _ := NewRequestID("sctl_session_cancel_0002")
	if _, err := session.Call(
		context.Background(),
		secondID,
		"node.health.query",
		struct {
			SimulationNodeID string `json:"simulationNodeId"`
		}{SimulationNodeID: "snode_session_test"},
		"node.health.receipt",
	); err != nil {
		t.Fatalf("second Call: %v", err)
	}
	if err := <-childDone; err != nil {
		t.Fatal(err)
	}
}

// TestSessionSerializesConcurrentCalls 验证并发 caller 仍产生完整交替 sequence。
func TestSessionSerializesConcurrentCalls(t *testing.T) {
	t.Parallel()
	parentToChildReader, parentToChildWriter := io.Pipe()
	childToParentReader, childToParentWriter := io.Pipe()
	nonce, _ := NewDigest(strings.Repeat("2", 64))
	session, err := NewSession(
		childToParentReader,
		parentToChildWriter,
		&sessionPipeCloser{writer: parentToChildWriter, reader: childToParentReader},
		nonce,
		time.Second,
		NewProposalInbox(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	const calls = 8
	childDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(parentToChildReader)
		for range calls {
			request, err := DecodeFrame(reader)
			if err != nil {
				childDone <- err
				return
			}
			response, err := NewFrame(
				"node.health.receipt",
				struct {
					Healthy bool `json:"healthy"`
				}{Healthy: true},
				request.RequestID,
				request.Sequence+1,
				nonce,
			)
			if err == nil {
				err = WriteFrame(childToParentWriter, response)
			}
			if err != nil {
				childDone <- err
				return
			}
		}
		childDone <- nil
	}()
	var wait sync.WaitGroup
	failures := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			requestID, _ := NewRequestID("sctl_concurrent_call_000" + string(rune('0'+index)))
			_, err := session.Call(
				context.Background(),
				requestID,
				"node.health.query",
				struct {
					SimulationNodeID string `json:"simulationNodeId"`
				}{SimulationNodeID: "snode_session_test"},
				"node.health.receipt",
			)
			failures <- err
		}(index)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("concurrent Call: %v", err)
		}
	}
	if err := <-childDone; err != nil {
		t.Fatal(err)
	}
}

// TestSessionHighPriorityBypassesQueuedTicketCalls 验证 install burst 后 health 先取得下一 turn。
func TestSessionHighPriorityBypassesQueuedTicketCalls(t *testing.T) {
	t.Parallel()
	parentToChildReader, parentToChildWriter := io.Pipe()
	childToParentReader, childToParentWriter := io.Pipe()
	nonce, _ := NewDigest(strings.Repeat("3", 64))
	session, err := NewSession(
		childToParentReader, parentToChildWriter,
		&sessionPipeCloser{writer: parentToChildWriter, reader: childToParentReader},
		nonce, time.Second, NewProposalInbox(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	order := make(chan string, 3)
	childDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(parentToChildReader)
		for index := 0; index < 3; index++ {
			request, readErr := DecodeFrame(reader)
			if readErr != nil {
				childDone <- readErr
				return
			}
			order <- request.Kind
			if index == 0 {
				close(firstEntered)
				<-releaseFirst
			}
			receiptKind := "battle.ticket.installed"
			if request.Kind == "node.health.query" {
				receiptKind = "node.health.receipt"
			}
			response, responseErr := NewFrame(receiptKind, struct {
				Accepted bool `json:"accepted"`
			}{Accepted: true}, request.RequestID, request.Sequence+1, nonce)
			if responseErr == nil {
				responseErr = WriteFrame(childToParentWriter, response)
			}
			if responseErr != nil {
				childDone <- responseErr
				return
			}
		}
		childDone <- nil
	}()
	var completed atomic.Int32
	call := func(id string, kind string, receipt string) {
		requestID, _ := NewRequestID(id)
		if _, callErr := session.Call(context.Background(), requestID, kind,
			struct {
				Value string `json:"value"`
			}{Value: "fixture"}, receipt); callErr == nil {
			completed.Add(1)
		}
	}
	go call("sctl_ticket_priority_0001", "battle.ticket.install", "battle.ticket.installed")
	<-firstEntered
	go call("sctl_ticket_priority_0002", "battle.ticket.install", "battle.ticket.installed")
	waitLaneCounts(t, session, 0, 1)
	go call("sctl_health_priority_0001", "node.health.query", "node.health.receipt")
	waitLaneCounts(t, session, 1, 1)
	close(releaseFirst)
	if first, second, third := <-order, <-order, <-order; first != "battle.ticket.install" ||
		second != "node.health.query" || third != "battle.ticket.install" {
		t.Fatalf("control lane order=%s,%s,%s", first, second, third)
	}
	if err := <-childDone; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for completed.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if completed.Load() != 3 {
		t.Fatalf("completed calls=%d", completed.Load())
	}
}

// TestSessionTicketQueueHasHardLimit 验证第 65 个等待 install 稳定 backpressure。
func TestSessionTicketQueueHasHardLimit(t *testing.T) {
	t.Parallel()
	parentToChildReader, parentToChildWriter := io.Pipe()
	childToParentReader, childToParentWriter := io.Pipe()
	defer childToParentWriter.Close()
	nonce, _ := NewDigest(strings.Repeat("4", 64))
	session, err := NewSession(
		childToParentReader, parentToChildWriter,
		&sessionPipeCloser{writer: parentToChildWriter, reader: childToParentReader},
		nonce, 5*time.Second, NewProposalInbox(),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstEntered := make(chan struct{})
	go func() {
		reader := bufio.NewReader(parentToChildReader)
		if _, readErr := DecodeFrame(reader); readErr == nil {
			close(firstEntered)
		}
	}()
	var wait sync.WaitGroup
	startCall := func(index int) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			requestID, _ := NewRequestID(fmt.Sprintf("sctl_ticket_queue_%016d", index))
			_, _ = session.Call(context.Background(), requestID, "battle.ticket.install",
				map[string]string{"value": "fixture"}, "battle.ticket.installed")
		}()
	}
	startCall(0)
	<-firstEntered
	for index := 1; index <= TicketRequestQueueLimit; index++ {
		startCall(index)
	}
	waitLaneCounts(t, session, 0, TicketRequestQueueLimit)
	overflowID, _ := NewRequestID("sctl_ticket_queue_overflow")
	_, overflowErr := session.Call(context.Background(), overflowID, "battle.ticket.install",
		map[string]string{"value": "fixture"}, "battle.ticket.installed")
	var failure *SessionError
	if !errors.As(overflowErr, &failure) || failure.Kind != SessionErrorBackpressure {
		t.Fatalf("ticket queue overflow error=%v", overflowErr)
	}
	_ = session.Close()
	wait.Wait()
}

// waitLaneCounts 等待并发 caller 全部登记到预期队列，超时即测试失败。
func waitLaneCounts(t *testing.T, session *Session, high int, ticket int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.laneMutex.Lock()
		highWaiting, ticketWaiting := session.highWaiting, session.ticketWaiting
		session.laneMutex.Unlock()
		if highWaiting == high && ticketWaiting == ticket {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("lane counts did not converge to high=%d ticket=%d", high, ticket)
}

// TestTicketLowPriorityClassification 保证 revoke、health、drain、stop 与 shutdown 永不落入 ticket lane。
func TestTicketLowPriorityClassification(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"battle.ticket.install", "battle.ticket.status.query"} {
		if !ticketLowPriority(kind) {
			t.Fatalf("%s did not enter low-priority ticket lane", kind)
		}
	}
	for _, kind := range []string{
		"battle.ticket.revoke", "battle.session.revoke", "node.health.query",
		"instance.drain", "instance.stop", "node.shutdown", "result.ack",
	} {
		if ticketLowPriority(kind) {
			t.Fatalf("%s incorrectly entered low-priority ticket lane", kind)
		}
	}
}
