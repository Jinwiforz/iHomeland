#pragma once

#include <cstddef>
#include <cstdint>
#include <iosfwd>
#include <string>
#include <string_view>

namespace ihomeland::sim {

/// ControlFrame 是私有 Go/C++ stdio 契约的不可变语义 envelope。
struct ControlFrame final {
    /// schema_version 固定为 simulation-control-v1。
    std::string schema_version;
    /// session_nonce 绑定单次 child process bootstrap。
    std::string session_nonce;
    /// sequence 使用规范 uint64 十进制文本。
    std::string sequence;
    /// request_id 关联一次 request/receipt 或 result handshake。
    std::string request_id;
    /// kind 必须存在于冻结 control inventory。
    std::string kind;
    /// payload_json 是 canonical object JSON，不暴露 parser 类型。
    std::string payload_json;
};

/// ControlFrameCodec 编解码 4-byte big-endian length-prefixed canonical JSON。
class ControlFrameCodec final {
public:
    /// MaximumFrameBytes 是包含 JSON payload 的 hard limit。
    static constexpr std::size_t MaximumFrameBytes = 65'536;

    /// ParsePayload 严格解析单个 JSON payload 并拒绝 closed-schema 违约。
    [[nodiscard]] static ControlFrame ParsePayload(std::string_view payload);

    /// EncodePayload 生成 key 排序且无尾随换行的 canonical JSON。
    [[nodiscard]] static std::string EncodePayload(const ControlFrame& frame);

    /// Read 从 stream 读取一个完整 frame；clean EOF 返回 false，partial EOF 抛错。
    [[nodiscard]] static bool Read(std::istream& input, ControlFrame& frame);

    /// Write 写入一个完整 frame 并在任何 partial/write failure 时抛错。
    static void Write(std::ostream& output, const ControlFrame& frame);
};

/// ControlSequence 验证单次 session 的 nonce 和双向单调 sequence。
class ControlSequence final {
public:
    /// 构造函数要求 256-bit lowercase hex nonce。
    explicit ControlSequence(std::string nonce);

    /// AcceptInbound 要求 frame nonce 相同且 sequence 精确递增一。
    void AcceptInbound(const ControlFrame& frame);

    /// MakeOutbound 创建下一个带相同 nonce 的 frame。
    [[nodiscard]] ControlFrame MakeOutbound(
        std::string request_id,
        std::string kind,
        std::string payload_json);

    /// Nonce 返回本 session 的低敏测试投影；生产日志不得输出它。
    [[nodiscard]] const std::string& Nonce() const noexcept;

private:
    /// nonce_ 在 session 生命周期内不可变。
    std::string nonce_;
    /// sequence_ 是双向请求/响应共享的最后 frame sequence。
    std::uint64_t sequence_{0};
};

}  // namespace ihomeland::sim
