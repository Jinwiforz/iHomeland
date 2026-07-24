#include "ihomeland/sim/control/control_frame.hpp"
#include "ihomeland/sim/control/control_server.hpp"

#include <sstream>
#include <stdexcept>
#include <string>

namespace {

/// Require 把 stdio integration 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// AppendFrame 向 binary input 追加一个 length-prefixed frame。
void AppendFrame(
    std::stringstream& stream,
    const ihomeland::sim::ControlFrame& frame) {
    ihomeland::sim::ControlFrameCodec::Write(stream, frame);
}

/// BuildBinding 返回固定 B0.3 qualification identity。
[[nodiscard]] ihomeland::sim::ControlBuildBinding BuildBinding() {
    return {
        .build_identity = std::string(64, 'a'),
        .model_manifest = std::string(64, 'b'),
        .profile_manifest = std::string(64, 'c'),
        .platform_qualification = "implementation-qualified-windows-x64",
    };
}

/// Frame 构造同一 session 的测试请求。
[[nodiscard]] ihomeland::sim::ControlFrame Frame(
    const std::string& sequence,
    const std::string& request_id,
    const std::string& kind,
    const std::string& payload) {
    return {
        .schema_version = "simulation-control-v1",
        .session_nonce = std::string(64, '1'),
        .sequence = sequence,
        .request_id = request_id,
        .kind = kind,
        .payload_json = payload,
    };
}

/// TestHealthyShutdown 验证 hello、health、shutdown 共用双向 sequence。
void TestHealthyShutdown() {
    std::stringstream input(
        std::ios::in | std::ios::out | std::ios::binary);
    AppendFrame(
        input,
        Frame(
            "1",
            "sctl_stdio_hello_0001",
            "node.hello.challenge",
            R"({"actorCapacity":8,"expectedBuildIdentity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expectedModelManifest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","expectedProfileManifest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","instanceCapacity":2,"runtimeNodeId":"rnode_stdio_test","simulationNodeId":"snode_stdio_test"})"));
    AppendFrame(
        input,
        Frame(
            "3",
            "sctl_stdio_health_0001",
            "node.health.query",
            R"({"simulationNodeId":"snode_stdio_test"})"));
    AppendFrame(
        input,
        Frame(
            "5",
            "sctl_stdio_stop_00001",
            "node.shutdown",
            R"({"deadlineMs":"1000"})"));
    input.seekg(0);
    std::stringstream output(
        std::ios::in | std::ios::out | std::ios::binary);
    std::ostringstream diagnostics;
    Require(
        ihomeland::sim::RunControlStdio(
            input,
            output,
            diagnostics,
            BuildBinding()) == 0,
        "valid control session failed");
    Require(diagnostics.str().empty(), "valid session wrote diagnostics");

    output.seekg(0);
    ihomeland::sim::ControlFrame receipt;
    Require(
        ihomeland::sim::ControlFrameCodec::Read(output, receipt) &&
            receipt.kind == "node.hello.receipt" &&
            receipt.sequence == "2",
        "hello receipt was invalid");
    Require(
        ihomeland::sim::ControlFrameCodec::Read(output, receipt) &&
            receipt.kind == "node.health.receipt" &&
            receipt.sequence == "4",
        "health receipt was invalid");
    Require(
        ihomeland::sim::ControlFrameCodec::Read(output, receipt) &&
            receipt.kind == "node.stopped" &&
            receipt.sequence == "6",
        "shutdown receipt was invalid");
    Require(
        !ihomeland::sim::ControlFrameCodec::Read(output, receipt),
        "stdout contained non-frame bytes");
}

/// TestBuildDrift 验证 hello qualification mismatch fail closed。
void TestBuildDrift() {
    std::stringstream input(
        std::ios::in | std::ios::out | std::ios::binary);
    AppendFrame(
        input,
        Frame(
            "1",
            "sctl_stdio_drift_0001",
            "node.hello.challenge",
            R"({"actorCapacity":8,"expectedBuildIdentity":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","expectedModelManifest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","expectedProfileManifest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","instanceCapacity":2,"runtimeNodeId":"rnode_stdio_test","simulationNodeId":"snode_stdio_test"})"));
    input.seekg(0);
    std::stringstream output(
        std::ios::in | std::ios::out | std::ios::binary);
    std::ostringstream diagnostics;
    Require(
        ihomeland::sim::RunControlStdio(
            input,
            output,
            diagnostics,
            BuildBinding()) == 1,
        "build drift was accepted");
    Require(output.str().empty(), "failed hello emitted a receipt");
    Require(
        diagnostics.str().find("build binding drifted") != std::string::npos,
        "build drift lacked low-sensitive diagnostic");
}

}  // namespace

/// main 运行私有 stdio control integration 回归。
int main() {
    try {
        TestHealthyShutdown();
        TestBuildDrift();
        return 0;
    } catch (const std::exception&) {
        return 1;
    }
}
