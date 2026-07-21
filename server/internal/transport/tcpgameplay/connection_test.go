package tcpgameplay

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// TestReadFrameRefreshesIdleDeadline 验证每个完整C2S frame都从成功I/O时刻重新建立idle窗口。
func TestReadFrameRefreshesIdleDeadline(t *testing.T) {
	t.Parallel()
	serverSocket, clientSocket := net.Pipe()
	defer serverSocket.Close()
	defer clientSocket.Close()
	registry := &Registry{config: Config{
		Policy: config.GameplayTCPPolicy{
			IdleTimeout: 500 * time.Millisecond,
			ReadTimeout: 500 * time.Millisecond,
		},
		FrameBytes: 256,
	}}
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[:4], 1)
	frame[4] = 1
	written := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		if _, err := clientSocket.Write(frame); err != nil {
			written <- err
			return
		}
		time.Sleep(300 * time.Millisecond)
		_, err := clientSocket.Write(frame)
		written <- err
	}()

	for index := 0; index < 2; index++ {
		payload, err := registry.readFrame(context.Background(), serverSocket)
		if err != nil || len(payload) != 1 {
			t.Fatalf("readFrame(%d) payload=%v err=%v", index, payload, err)
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}
