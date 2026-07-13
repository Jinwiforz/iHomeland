package session

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestTicketNonceRoundTripAndRedaction 验证 16-byte 协议边界、digest 与默认脱敏行为。
func TestTicketNonceRoundTripAndRedaction(t *testing.T) {
	nonce, err := NewTicketNonce(new(deterministicSecretGenerator))
	if err != nil {
		t.Fatal(err)
	}
	raw := nonce.Bytes()
	if len(raw) != ticketNonceBytes {
		t.Fatalf("ticket nonce length changed: %d", len(raw))
	}
	parsed, err := ParseTicketNonce(raw[:])
	if err != nil || parsed.Digest() != nonce.Digest() {
		t.Fatalf("ticket nonce round trip failed: %v", err)
	}
	formatted := fmt.Sprintf("%v %#v", nonce, nonce)
	if formatted != ticketNoncePlaceholder+" "+ticketNoncePlaceholder || strings.Contains(formatted, fmt.Sprintf("%x", raw)) {
		t.Fatalf("ticket nonce formatting exposed credential material: %s", formatted)
	}
}

// TestConnectionTicketFormattingHidesNonce 验证完整 domain ticket 也不会递归展开 nonce。
func TestConnectionTicketFormattingHidesNonce(t *testing.T) {
	nonce, err := NewTicketNonce(new(deterministicSecretGenerator))
	if err != nil {
		t.Fatal(err)
	}
	ticket := ConnectionTicket{SessionID: SessionID{value: "ses_fixture"}, Epoch: InitialEpoch, Channel: ChannelWSS, Nonce: nonce}
	raw := nonce.Bytes()
	formatted := fmt.Sprintf("%v %#v %+v", ticket, ticket, ticket)
	if strings.Contains(formatted, fmt.Sprintf("%x", raw)) || strings.Contains(formatted, ticketNoncePlaceholder) {
		t.Fatalf("connection ticket formatting exposed or redundantly rendered nonce: %s", formatted)
	}
}

// TestTicketNonceRejectsZeroValue 防止未初始化 nonce 成为可消费凭据。
func TestTicketNonceRejectsZeroValue(t *testing.T) {
	if _, err := NewTicketNonce(nil); err == nil {
		t.Fatal("nil ticket nonce generator was accepted")
	}
	if _, err := NewTicketNonce(zeroSecretGenerator{}); err == nil {
		t.Fatal("all-zero generated ticket nonce was accepted")
	}
	if _, err := ParseTicketNonce(make([]byte, ticketNonceBytes)); err == nil {
		t.Fatal("zero ticket nonce was accepted")
	}
	if (TicketNonce{}).Valid() {
		t.Fatal("zero ticket nonce reported valid")
	}
}

// FuzzParseTicketNonce 验证任意协议 bytes 只能得到固定长度副本或稳定错误。
func FuzzParseTicketNonce(f *testing.F) {
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{1}, ticketNonceBytes))
	f.Add(bytes.Repeat([]byte{2}, ticketNonceBytes+1))
	f.Fuzz(func(t *testing.T, raw []byte) {
		nonce, err := ParseTicketNonce(raw)
		if err != nil {
			if err.Error() != "ticket nonce length is invalid" && err.Error() != "ticket nonce value is invalid" {
				t.Fatalf("ticket nonce returned unstable error: %q", err.Error())
			}
			return
		}
		copyOfRaw := append([]byte(nil), raw...)
		for index := range raw {
			raw[index] = 0
		}
		if nonce.Bytes() != [ticketNonceBytes]byte(copyOfRaw) {
			t.Fatal("ticket nonce retained caller buffer instead of copying")
		}
	})
}
