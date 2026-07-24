#pragma once

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <istream>
#include <memory>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace ihomeland::sim {

/// HarnessErrorCode 是所有离线 source/sink 共享的稳定失败分类。
enum class HarnessErrorCode : std::uint8_t {
    /// Closed 表示调用发生在显式关闭之后。
    Closed,
    /// Capacity 表示记录数或 bytes 的 hard capacity 已耗尽。
    Capacity,
    /// Io 表示文件或 stream 无法完成请求。
    Io,
    /// Schema 表示输入不是有效的闭合 JSON record。
    Schema,
    /// Sequence 表示 recorded source 的 query identity 或顺序不匹配。
    Sequence,
    /// ForbiddenPath 表示输出路径逃逸或覆盖只读 source corpus。
    ForbiddenPath,
};

/// HarnessError 携带稳定错误码且不回显 command/evidence 内容。
class HarnessError final : public std::runtime_error {
public:
    /// 构造函数保存 code 与低敏诊断。
    HarnessError(HarnessErrorCode code, const std::string& message);

    /// Code 返回可由测试和 qualification gate 判定的错误类别。
    [[nodiscard]] HarnessErrorCode Code() const noexcept;

private:
    /// code_ 在异常构造后不可变。
    HarnessErrorCode code_;
};

/// OfflineCommand 是 file/stdin source 产生的规范 command record。
struct OfflineCommand final {
    /// ordinal 是 source 内从 1 开始的稳定读取序号。
    std::uint64_t ordinal;
    /// canonical_json 是排序 token stream，不携带输入空白。
    std::string canonical_json;
    /// sha256 绑定 canonical_json 的 UTF-8 bytes。
    std::string sha256;
};

/// JsonLineCommandSource 从外部拥有的 stream 读取有界 JSONL，适用于 stdin 与测试 loopback。
class JsonLineCommandSource final {
public:
    /// 构造函数绑定 stream，max_records 为不可增长的 hard capacity。
    JsonLineCommandSource(std::istream& stream, std::size_t max_records);

    /// Next 返回下一条 command；干净 EOF 返回 nullopt，容量、schema 与 I/O 失败抛出 HarnessError。
    [[nodiscard]] std::optional<OfflineCommand> Next();

    /// Close 幂等关闭 source；关闭后 Next 稳定失败且不再读取 stream。
    void Close() noexcept;

    /// IsClosed 返回显式关闭状态，不把自然 EOF 等同于 close。
    [[nodiscard]] bool IsClosed() const noexcept;

private:
    /// stream_ 由调用方拥有且必须长于当前 source。
    std::istream* stream_;
    /// max_records_ 是本 source 生命周期内允许产生的最大 record 数。
    std::size_t max_records_;
    /// emitted_ 记录已经成功产生的 records。
    std::size_t emitted_{0};
    /// closed_ 阻止关闭后的读取。
    bool closed_{false};
};

/// FileCommandSource 拥有文件 stream，并复用与 stdin 相同的 JSONL 语义。
class FileCommandSource final {
public:
    /// 构造函数只读打开 path，失败时不创建文件。
    FileCommandSource(const std::filesystem::path& path, std::size_t max_records);
    /// 析构函数关闭内部 stream。
    ~FileCommandSource();

    FileCommandSource(const FileCommandSource&) = delete;
    FileCommandSource& operator=(const FileCommandSource&) = delete;
    FileCommandSource(FileCommandSource&&) noexcept;
    FileCommandSource& operator=(FileCommandSource&&) noexcept;

    /// Next 返回下一条规范 command 或自然 EOF。
    [[nodiscard]] std::optional<OfflineCommand> Next();

    /// Close 幂等关闭文件 source。
    void Close() noexcept;

private:
    /// State 隐藏文件 stream 实现，避免 public contract 暴露 parser 或平台类型。
    class State;
    /// state_ 唯一拥有文件和 JSONL source。
    std::unique_ptr<State> state_;
};

/// RecordedQueryResult 是 physics/navigation 记录化 source 的项目内规范值。
struct RecordedQueryResult final {
    /// query_id 是 fixture 中唯一且顺序稳定的 query identity。
    std::string query_id;
    /// canonical_result 是已规范化的项目 value JSON，不包含第三方类型。
    std::string canonical_result;
};

/// RecordedResultSource 以严格登记顺序提供有界 physics/navigation result。
class RecordedResultSource final {
public:
    /// 构造函数拒绝空/重复 query_id，并固定最大消费容量。
    RecordedResultSource(std::vector<RecordedQueryResult> results, std::size_t max_results);

    /// Take 只消费下一个预期 query；错序、超容量或关闭均 fail closed。
    [[nodiscard]] RecordedQueryResult Take(std::string_view query_id);

    /// Close 幂等清空未消费 result，并阻止后续 Take。
    void Close() noexcept;

private:
    /// results_ 保存构造时规范化后的固定顺序结果。
    std::vector<RecordedQueryResult> results_;
    /// max_results_ 是本 source 的 hard capacity。
    std::size_t max_results_;
    /// next_ 指向下一条允许消费的 result。
    std::size_t next_{0};
    /// closed_ 阻止生命周期结束后的消费。
    bool closed_{false};
};

/// EvidenceSink 只向明确 output root 写入有界低敏文件，并保护 source corpus。
class EvidenceSink final {
public:
    /// 构造函数验证 output root 不位于任一 forbidden source root 内并固定总 bytes 上限。
    EvidenceSink(
        std::filesystem::path output_root,
        std::vector<std::filesystem::path> forbidden_roots,
        std::size_t max_bytes);

    /// Write 写入相对路径；逃逸、覆盖、超容量、关闭或 I/O 失败均不产生部分文件。
    void Write(const std::filesystem::path& relative_path, std::string_view content);

    /// Close 幂等关闭 sink；关闭后 Write 稳定失败。
    void Close() noexcept;

    /// BytesWritten 返回成功提交的总 bytes，不包含失败尝试。
    [[nodiscard]] std::size_t BytesWritten() const noexcept;

private:
    /// output_root_ 是构造时规范化的唯一可写根。
    std::filesystem::path output_root_;
    /// max_bytes_ 是所有 evidence 文件合计 hard capacity。
    std::size_t max_bytes_;
    /// bytes_written_ 只在完整文件提交后增加。
    std::size_t bytes_written_{0};
    /// closed_ 阻止生命周期结束后的写入。
    bool closed_{false};
};

}  // namespace ihomeland::sim
