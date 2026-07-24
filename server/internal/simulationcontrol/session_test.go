package simulationcontrol

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
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
