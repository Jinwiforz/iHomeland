#include "ihomeland/sim/control/control_frame.hpp"

#include <nlohmann/json.hpp>

#include <array>
#include <charconv>
#include <cstdint>
#include <istream>
#include <limits>
#include <ostream>
#include <regex>
#include <set>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;

/// IsKnownKind 验证 control inventory 的闭合集合。
[[nodiscard]] bool IsKnownKind(const std::string_view kind) {
    static const std::set<std::string, std::less<>> kinds{
        "battle_qualification_snapshot_receipt",
        "battle_qualification_snapshot_request",
        "battle.session.closed",
        "battle.session.revoke",
        "battle.ticket.install",
        "battle.ticket.installed",
        "battle.ticket.revoke",
        "battle.ticket.revoked",
        "battle.ticket.status.query",
        "battle.ticket.status.receipt",
        "instance.drain",
        "instance.drained",
        "instance.ready",
        "instance.start",
        "instance.status.query",
        "instance.status.receipt",
        "instance.stop",
        "instance.stopped",
        "node.health.query",
        "node.health.receipt",
        "node.hello.challenge",
        "node.hello.receipt",
        "node.listener.status.query",
        "node.listener.status.receipt",
        "node.shutdown",
        "node.stopped",
        "result.ack",
        "result.proposal",
    };
    return kinds.contains(kind);
}

/// ParseJsonRejectingDuplicates 严格解析 UTF-8 并拒绝任意 object 的重复成员。
[[nodiscard]] Json ParseJsonRejectingDuplicates(const std::string_view source) {
    bool duplicate = false;
    std::vector<std::set<std::string>> object_keys;
    const auto callback = [&](const int, const Json::parse_event_t event, Json& parsed) {
        if (event == Json::parse_event_t::object_start) {
            object_keys.emplace_back();
        } else if (event == Json::parse_event_t::key) {
            if (object_keys.empty() ||
                !object_keys.back().insert(parsed.get<std::string>()).second) {
                duplicate = true;
            }
        } else if (event == Json::parse_event_t::object_end) {
            if (object_keys.empty()) {
                throw std::invalid_argument("control JSON object stack is invalid");
            }
            object_keys.pop_back();
        }
        return true;
    };
    try {
        auto value = Json::parse(source, callback, true, true);
        if (duplicate) {
            throw std::invalid_argument("control JSON contains duplicate member");
        }
        return value;
    } catch (const Json::exception&) {
        throw std::invalid_argument("control JSON parsing failed");
    }
}

/// RequireClosedObject 验证 object 的 required/allowed 字段精确一致。
void RequireClosedObject(
    const Json& value,
    const std::set<std::string, std::less<>>& fields,
    const char* context) {
    if (!value.is_object() || value.size() != fields.size()) {
        throw std::invalid_argument(std::string(context) + " is not a closed object");
    }
    for (const auto& [name, ignored] : value.items()) {
        static_cast<void>(ignored);
        if (!fields.contains(name)) {
            throw std::invalid_argument(std::string(context) + " has unknown field");
        }
    }
}

/// ParseDecimal 解析无前导零 uint64 文本。
[[nodiscard]] std::uint64_t ParseDecimal(
    const std::string_view text,
    const char* context) {
    if (text.empty() || (text.size() > 1 && text.front() == '0')) {
        throw std::invalid_argument(std::string(context) + " is not canonical decimal");
    }
    std::uint64_t value = 0;
    const auto [end, error] =
        std::from_chars(text.data(), text.data() + text.size(), value);
    if (error != std::errc{} || end != text.data() + text.size()) {
        throw std::invalid_argument(std::string(context) + " exceeds uint64");
    }
    return value;
}

/// RequireFrameFields 验证 frame 公共字段及 closed payload object。
void RequireFrameFields(const ControlFrame& frame) {
    static const std::regex nonce_pattern{"^[0-9a-f]{64}$"};
    static const std::regex request_pattern{"^sctl_[A-Za-z0-9_-]{16,80}$"};
    if (frame.schema_version != "simulation-control-v1" ||
        !std::regex_match(frame.session_nonce, nonce_pattern) ||
        !std::regex_match(frame.request_id, request_pattern) ||
        !IsKnownKind(frame.kind)) {
        throw std::invalid_argument("control frame identity is invalid");
    }
    static_cast<void>(ParseDecimal(frame.sequence, "control frame sequence"));
    const auto payload = ParseJsonRejectingDuplicates(frame.payload_json);
    if (!payload.is_object()) {
        throw std::invalid_argument("control frame payload must be object");
    }
    if (payload.dump() != frame.payload_json) {
        throw std::invalid_argument("control frame payload is not canonical");
    }
}

}  // namespace

ControlFrame ControlFrameCodec::ParsePayload(const std::string_view payload) {
    if (payload.empty() || payload.size() > MaximumFrameBytes) {
        throw std::length_error("control frame payload size is invalid");
    }
    const auto value = ParseJsonRejectingDuplicates(payload);
    static const std::set<std::string, std::less<>> fields{
        "kind", "payload", "requestId", "schemaVersion", "sequence", "sessionNonce"};
    RequireClosedObject(value, fields, "control frame");
    ControlFrame frame{
        .schema_version = value.at("schemaVersion").get<std::string>(),
        .session_nonce = value.at("sessionNonce").get<std::string>(),
        .sequence = value.at("sequence").get<std::string>(),
        .request_id = value.at("requestId").get<std::string>(),
        .kind = value.at("kind").get<std::string>(),
        .payload_json = value.at("payload").dump(),
    };
    RequireFrameFields(frame);
    if (EncodePayload(frame) != payload) {
        throw std::invalid_argument("control frame is not canonical JSON");
    }
    return frame;
}

std::string ControlFrameCodec::EncodePayload(const ControlFrame& frame) {
    RequireFrameFields(frame);
    Json value = Json::object();
    value["kind"] = frame.kind;
    value["payload"] = ParseJsonRejectingDuplicates(frame.payload_json);
    value["requestId"] = frame.request_id;
    value["schemaVersion"] = frame.schema_version;
    value["sequence"] = frame.sequence;
    value["sessionNonce"] = frame.session_nonce;
    const auto encoded = value.dump();
    if (encoded.size() > MaximumFrameBytes) {
        throw std::length_error("control frame exceeds hard limit");
    }
    return encoded;
}

bool ControlFrameCodec::Read(std::istream& input, ControlFrame& frame) {
    std::array<unsigned char, 4> prefix{};
    input.read(reinterpret_cast<char*>(prefix.data()), prefix.size());
    const auto prefix_bytes = input.gcount();
    if (prefix_bytes == 0 && input.eof()) {
        return false;
    }
    if (prefix_bytes != static_cast<std::streamsize>(prefix.size())) {
        throw std::runtime_error("control frame prefix ended partially");
    }
    const auto length =
        (static_cast<std::uint32_t>(prefix[0]) << 24U) |
        (static_cast<std::uint32_t>(prefix[1]) << 16U) |
        (static_cast<std::uint32_t>(prefix[2]) << 8U) |
        static_cast<std::uint32_t>(prefix[3]);
    if (length == 0 || length > MaximumFrameBytes) {
        throw std::length_error("control frame prefix exceeds hard limit");
    }
    std::string payload(length, '\0');
    input.read(payload.data(), static_cast<std::streamsize>(length));
    if (input.gcount() != static_cast<std::streamsize>(length)) {
        throw std::runtime_error("control frame payload ended partially");
    }
    frame = ParsePayload(payload);
    return true;
}

void ControlFrameCodec::Write(std::ostream& output, const ControlFrame& frame) {
    const auto payload = EncodePayload(frame);
    const auto length = static_cast<std::uint32_t>(payload.size());
    const std::array<char, 4> prefix{
        static_cast<char>((length >> 24U) & 0xffU),
        static_cast<char>((length >> 16U) & 0xffU),
        static_cast<char>((length >> 8U) & 0xffU),
        static_cast<char>(length & 0xffU),
    };
    output.write(prefix.data(), static_cast<std::streamsize>(prefix.size()));
    output.write(payload.data(), static_cast<std::streamsize>(payload.size()));
    output.flush();
    if (!output.good()) {
        throw std::runtime_error("control frame write failed");
    }
}

ControlSequence::ControlSequence(std::string nonce)
    : nonce_(std::move(nonce)) {
    static const std::regex nonce_pattern{"^[0-9a-f]{64}$"};
    if (!std::regex_match(nonce_, nonce_pattern)) {
        throw std::invalid_argument("control session nonce is invalid");
    }
}

void ControlSequence::AcceptInbound(const ControlFrame& frame) {
    const auto sequence = ParseDecimal(frame.sequence, "control inbound sequence");
    if (frame.session_nonce != nonce_ ||
        sequence_ == std::numeric_limits<std::uint64_t>::max() ||
        sequence != sequence_ + 1) {
        throw std::invalid_argument("control inbound session or sequence mismatch");
    }
    sequence_ = sequence;
}

ControlFrame ControlSequence::MakeOutbound(
    std::string request_id,
    std::string kind,
    std::string payload_json) {
    if (sequence_ == std::numeric_limits<std::uint64_t>::max()) {
        throw std::overflow_error("control outbound sequence exhausted");
    }
    ++sequence_;
    ControlFrame frame{
        .schema_version = "simulation-control-v1",
        .session_nonce = nonce_,
        .sequence = std::to_string(sequence_),
        .request_id = std::move(request_id),
        .kind = std::move(kind),
        .payload_json = std::move(payload_json),
    };
    RequireFrameFields(frame);
    return frame;
}

const std::string& ControlSequence::Nonce() const noexcept {
    return nonce_;
}

}  // namespace ihomeland::sim
