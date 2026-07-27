package gateway

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// battleTicketIDBytes 是 ClientHello 公开 opaque ticket identity 宽度。
	battleTicketIDBytes = 16
	// clientHelloBytes 是 canonical IHBH datagram 宽度。
	clientHelloBytes = 88
	// handshakeTicketOffset 是 ClientHello ticket identity 的 byte offset。
	handshakeTicketOffset = 8
	// secureHeaderBytes 是 B0.5 canonical AAD 宽度。
	secureHeaderBytes = 48
	// secureVersionOffset 是公开 wire version 的 byte offset。
	secureVersionOffset = 4
	// secureKindOffset 是公开 packet kind 的 byte offset。
	secureKindOffset = 5
	// secureSessionDigestOffset 是公开 8-byte routing digest 的起点。
	secureSessionDigestOffset = 8
	// secureSessionDigestBytes 是公开 routing digest 宽度。
	secureSessionDigestBytes = 8
	// secureWireVersion 是 B0.5 已冻结版本。
	secureWireVersion = 1
	// correlationBytes 控制 evidence 中二次摘要的固定宽度。
	correlationBytes = 8
)

// allZero 报告固定宽度公开 identity 是否无效。
func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

// clientHelloTicketID 只从 canonical ClientHello 读取公开 lookup identity。
func clientHelloTicketID(payload []byte) ([battleTicketIDBytes]byte, bool) {
	var ticketID [battleTicketIDBytes]byte
	if len(payload) != clientHelloBytes ||
		string(payload[:4]) != "IHBH" ||
		payload[secureVersionOffset] != secureWireVersion ||
		payload[secureKindOffset] != 1 ||
		payload[6] != 0 || payload[7] != 0 {
		return ticketID, false
	}
	copy(
		ticketID[:],
		payload[handshakeTicketOffset:handshakeTicketOffset+battleTicketIDBytes],
	)
	return ticketID, !allZero(ticketID[:])
}

// secureSessionDigest 只读取 canonical secure header 的公开 routing handle。
func secureSessionDigest(payload []byte) ([secureSessionDigestBytes]byte, bool) {
	var digest [secureSessionDigestBytes]byte
	if len(payload) < secureHeaderBytes ||
		string(payload[:4]) != "IHBT" ||
		payload[secureVersionOffset] != secureWireVersion {
		return digest, false
	}
	copy(
		digest[:],
		payload[secureSessionDigestOffset:secureSessionDigestOffset+secureSessionDigestBytes],
	)
	return digest, !allZero(digest[:])
}

// classifyPacket 只检查公开 magic/version/kind，不解析或返回 protected payload。
func classifyPacket(payload []byte) (PublicPacketKind, string) {
	if len(payload) >= 4 {
		switch string(payload[:4]) {
		case "IHBH", "IHBR", "IHBA", "IHBS":
			return PublicPacketHandshake, ""
		case "IHBT":
			if len(payload) < secureHeaderBytes ||
				payload[secureVersionOffset] != secureWireVersion {
				return PublicPacketUnknown, ""
			}
			kind := PublicPacketUnknown
			switch payload[secureKindOffset] {
			case 1:
				kind = PublicPacketRaw
			case 2:
				kind = PublicPacketKCP
			case 3:
				kind = PublicPacketControl
			default:
				return PublicPacketUnknown, ""
			}
			digest := sha256.Sum256(payload[secureSessionDigestOffset : secureSessionDigestOffset+secureSessionDigestBytes])
			return kind, hex.EncodeToString(digest[:correlationBytes])
		}
	}
	return PublicPacketUnknown, ""
}

// metadataForPacket 在不复制 payload 的前提下生成立即终结 evidence。
func metadataForPacket(packet Packet, disposition Disposition) Metadata {
	kind, correlation := classifyPacket(packet.Payload)
	return Metadata{
		Direction:         packet.Direction,
		ClientSlot:        packet.ClientSlot,
		MappingGeneration: packet.MappingGeneration,
		ReceiveSequence:   packet.ReceiveSequence,
		CopyIndex:         0,
		LengthBytes:       len(packet.Payload),
		PacketKind:        kind,
		Correlation:       correlation,
		ReceivedAt:        packet.ReceivedAt,
		DueAt:             packet.ReceivedAt,
		Disposition:       disposition,
	}
}
