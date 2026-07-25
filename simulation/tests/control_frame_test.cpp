#include "ihomeland/sim/control/control_frame.hpp"

#include <nlohmann/json.hpp>

#include <array>
#include <cstdint>
#include <fstream>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>

namespace {

/// Require 把 control frame 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireFailure 要求动作 fail closed。
template <typename Action>
void RequireFailure(Action action, const char* message) {
    try {
        action();
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error(message);
}

/// TestFrame 返回固定 canonical frame。
[[nodiscard]] ihomeland::sim::ControlFrame TestFrame() {
    return {
        .schema_version = "simulation-control-v1",
        .session_nonce = std::string(64, 'a'),
        .sequence = "1",
        .request_id = "sctl_frame_test_00001",
        .kind = "node.health.query",
        .payload_json = R"({"simulationNodeId":"snode_frame_test"})",
    };
}

/// TestRoundTrip 验证 canonical payload 与 length prefix 往返。
void TestRoundTrip() {
    const auto frame = TestFrame();
    const auto payload = ihomeland::sim::ControlFrameCodec::EncodePayload(frame);
    const auto parsed = ihomeland::sim::ControlFrameCodec::ParsePayload(payload);
    Require(parsed.payload_json == frame.payload_json, "canonical payload changed");

    std::stringstream stream(
        std::ios::in | std::ios::out | std::ios::binary);
    ihomeland::sim::ControlFrameCodec::Write(stream, frame);
    stream.seekg(0);
    ihomeland::sim::ControlFrame decoded;
    Require(
        ihomeland::sim::ControlFrameCodec::Read(stream, decoded),
        "length-prefixed frame was not read");
    Require(decoded.request_id == frame.request_id, "request identity changed");
    Require(
        !ihomeland::sim::ControlFrameCodec::Read(stream, decoded),
        "clean EOF was not reported");
}

/// TestCanonicalGolden 验证全部冻结 Go/C++ control frame bytes 完全一致。
void TestCanonicalGolden() {
    const auto path =
        std::string(IHOMELAND_SIMULATION_CONTROL_FIXTURE_ROOT) +
        "/canonical-golden.json";
    std::ifstream input(path, std::ios::binary);
    Require(input.good(), "control canonical golden cannot be opened");
    const auto document = nlohmann::json::parse(input);
    Require(
        document.at("frames").size() >= 4,
        "control canonical golden coverage is incomplete");
    for (const auto& golden : document.at("frames")) {
        const auto canonical = golden.at("canonicalJson").get<std::string>();
        const auto frame =
            ihomeland::sim::ControlFrameCodec::ParsePayload(canonical);
        Require(
            ihomeland::sim::ControlFrameCodec::EncodePayload(frame) ==
                canonical,
            "C++ control frame codec drifted from canonical golden");
        Require(
            canonical.size() ==
                golden.at("payloadBytes").get<std::size_t>(),
            "C++ control frame golden byte length drifted");
    }
}

/// TestMalformedFrames 覆盖 partial、trailing、污染、unknown 与 write failure。
void TestMalformedFrames() {
    const auto payload =
        ihomeland::sim::ControlFrameCodec::EncodePayload(TestFrame());
    RequireFailure(
        [&] {
            static_cast<void>(
                ihomeland::sim::ControlFrameCodec::ParsePayload(payload + " "));
        },
        "trailing bytes were accepted");
    RequireFailure(
        [&] {
            auto unknown = TestFrame();
            unknown.kind = "node.unknown";
            static_cast<void>(
                ihomeland::sim::ControlFrameCodec::EncodePayload(unknown));
        },
        "unknown kind was accepted");

    std::stringstream partial_prefix(std::string("\0\0", 2));
    ihomeland::sim::ControlFrame frame;
    RequireFailure(
        [&] {
            static_cast<void>(
                ihomeland::sim::ControlFrameCodec::Read(partial_prefix, frame));
        },
        "partial prefix was accepted");

    std::string partial_payload{"\0\0\0\x08{}", 6};
    std::stringstream partial_body(partial_payload);
    RequireFailure(
        [&] {
            static_cast<void>(
                ihomeland::sim::ControlFrameCodec::Read(partial_body, frame));
        },
        "partial payload was accepted");

    std::stringstream contaminated("diagnostic text on stdout");
    RequireFailure(
        [&] {
            static_cast<void>(
                ihomeland::sim::ControlFrameCodec::Read(contaminated, frame));
        },
        "stdout contamination was accepted");

    std::ostringstream failed;
    failed.setstate(std::ios::badbit);
    RequireFailure(
        [&] {
            ihomeland::sim::ControlFrameCodec::Write(failed, TestFrame());
        },
        "write failure was ignored");
}

/// TestSessionSequence 验证 nonce 与共享双向 sequence 精确递增。
void TestSessionSequence() {
    ihomeland::sim::ControlSequence sequence(std::string(64, 'a'));
    sequence.AcceptInbound(TestFrame());
    const auto response = sequence.MakeOutbound(
        "sctl_frame_test_00001",
        "node.health.receipt",
        R"({"healthy":true})");
    Require(response.sequence == "2", "outbound sequence did not advance");

    auto wrong_sequence = TestFrame();
    wrong_sequence.sequence = "4";
    RequireFailure(
        [&] { sequence.AcceptInbound(wrong_sequence); },
        "sequence gap was accepted");
    auto wrong_nonce = TestFrame();
    wrong_nonce.sequence = "3";
    wrong_nonce.session_nonce = std::string(64, 'b');
    RequireFailure(
        [&] { sequence.AcceptInbound(wrong_nonce); },
        "wrong nonce was accepted");
}

}  // namespace

/// main 运行 control frame closed-contract 回归。
int main() {
    try {
        TestRoundTrip();
        TestCanonicalGolden();
        TestMalformedFrames();
        TestSessionSequence();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
