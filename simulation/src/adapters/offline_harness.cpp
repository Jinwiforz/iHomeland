#include "ihomeland/sim/fixture/offline_harness.hpp"

#include "ihomeland/sim/fixture/canonical_json.hpp"

#include <algorithm>
#include <fstream>
#include <set>
#include <utility>

namespace ihomeland::sim {
namespace {

/// IsWithin 判断 candidate 是否位于 root 内，比较规范绝对路径且不依赖字符串前缀。
[[nodiscard]] bool IsWithin(
    const std::filesystem::path& candidate,
    const std::filesystem::path& root) {
    const auto normalized_candidate = std::filesystem::absolute(candidate).lexically_normal();
    const auto normalized_root = std::filesystem::absolute(root).lexically_normal();
    auto candidate_part = normalized_candidate.begin();
    auto root_part = normalized_root.begin();
    for (; root_part != normalized_root.end(); ++root_part, ++candidate_part) {
        if (candidate_part == normalized_candidate.end() || *candidate_part != *root_part) {
            return false;
        }
    }
    return true;
}

/// IsSafeRelative 拒绝绝对、空与父目录逃逸路径。
[[nodiscard]] bool IsSafeRelative(const std::filesystem::path& path) {
    if (path.empty() || path.is_absolute() ||
        path.has_root_name() || path.has_root_directory() ||
        path.native().find(L':') != std::filesystem::path::string_type::npos) {
        return false;
    }
    const auto normalized = path.lexically_normal();
    return normalized.begin() != normalized.end() && *normalized.begin() != "..";
}

}  // namespace

HarnessError::HarnessError(const HarnessErrorCode code, const std::string& message)
    : std::runtime_error(message), code_(code) {}

HarnessErrorCode HarnessError::Code() const noexcept {
    return code_;
}

JsonLineCommandSource::JsonLineCommandSource(std::istream& stream, const std::size_t max_records)
    : stream_(&stream), max_records_(max_records) {
    if (max_records == 0) {
        throw HarnessError(HarnessErrorCode::Capacity, "command source capacity must be positive");
    }
}

std::optional<OfflineCommand> JsonLineCommandSource::Next() {
    if (closed_) {
        throw HarnessError(HarnessErrorCode::Closed, "command source is closed");
    }
    if (emitted_ >= max_records_) {
        throw HarnessError(HarnessErrorCode::Capacity, "command source capacity exhausted");
    }
    std::string line;
    if (!std::getline(*stream_, line)) {
        if (stream_->eof()) {
            return std::nullopt;
        }
        throw HarnessError(HarnessErrorCode::Io, "command source read failed");
    }
    if (line.empty()) {
        throw HarnessError(HarnessErrorCode::Schema, "empty JSONL command is forbidden");
    }
    try {
        auto canonical = CanonicalizeJson(line);
        ++emitted_;
        return OfflineCommand{
            .ordinal = static_cast<std::uint64_t>(emitted_),
            .canonical_json = std::move(canonical.text),
            .sha256 = std::move(canonical.sha256)};
    } catch (const std::invalid_argument&) {
        throw HarnessError(HarnessErrorCode::Schema, "JSONL command is invalid");
    }
}

void JsonLineCommandSource::Close() noexcept {
    closed_ = true;
}

bool JsonLineCommandSource::IsClosed() const noexcept {
    return closed_;
}

/// FileCommandSource::State 同时拥有 file stream 和借用该 stream 的 JSONL source。
class FileCommandSource::State final {
public:
    /// 构造函数先打开只读文件，再建立有界 source。
    State(const std::filesystem::path& path, const std::size_t max_records)
        : stream(path, std::ios::binary), source(stream, max_records) {
        if (!stream) {
            throw HarnessError(HarnessErrorCode::Io, "command file open failed");
        }
    }

    /// stream 必须声明在 source 前，以保证逆序析构时 source 先结束。
    std::ifstream stream;
    /// source 借用同一 State 内的 stream。
    JsonLineCommandSource source;
};

FileCommandSource::FileCommandSource(
    const std::filesystem::path& path,
    const std::size_t max_records)
    : state_(std::make_unique<State>(path, max_records)) {}

FileCommandSource::~FileCommandSource() = default;
FileCommandSource::FileCommandSource(FileCommandSource&&) noexcept = default;
FileCommandSource& FileCommandSource::operator=(FileCommandSource&&) noexcept = default;

std::optional<OfflineCommand> FileCommandSource::Next() {
    if (!state_) {
        throw HarnessError(HarnessErrorCode::Closed, "moved command source is closed");
    }
    return state_->source.Next();
}

void FileCommandSource::Close() noexcept {
    if (state_) {
        state_->source.Close();
        state_->stream.close();
    }
}

RecordedResultSource::RecordedResultSource(
    std::vector<RecordedQueryResult> results,
    const std::size_t max_results)
    : results_(std::move(results)), max_results_(max_results) {
    if (max_results == 0 || results_.size() > max_results) {
        throw HarnessError(HarnessErrorCode::Capacity, "recorded result capacity is invalid");
    }
    std::set<std::string, std::less<>> identities;
    for (auto& result : results_) {
        if (result.query_id.empty() || !identities.insert(result.query_id).second) {
            throw HarnessError(HarnessErrorCode::Schema, "recorded query identity is invalid");
        }
        try {
            result.canonical_result = CanonicalizeJson(result.canonical_result).text;
        } catch (const std::invalid_argument&) {
            throw HarnessError(HarnessErrorCode::Schema, "recorded query result is invalid");
        }
    }
}

RecordedQueryResult RecordedResultSource::Take(const std::string_view query_id) {
    if (closed_) {
        throw HarnessError(HarnessErrorCode::Closed, "recorded result source is closed");
    }
    if (next_ >= max_results_) {
        throw HarnessError(HarnessErrorCode::Capacity, "recorded result capacity exhausted");
    }
    if (next_ >= results_.size() || results_[next_].query_id != query_id) {
        throw HarnessError(HarnessErrorCode::Sequence, "recorded query sequence mismatch");
    }
    return results_[next_++];
}

void RecordedResultSource::Close() noexcept {
    closed_ = true;
    results_.clear();
    next_ = 0;
}

EvidenceSink::EvidenceSink(
    std::filesystem::path output_root,
    std::vector<std::filesystem::path> forbidden_roots,
    const std::size_t max_bytes)
    : output_root_(std::filesystem::absolute(std::move(output_root)).lexically_normal()),
      max_bytes_(max_bytes) {
    if (max_bytes == 0) {
        throw HarnessError(HarnessErrorCode::Capacity, "evidence capacity must be positive");
    }
    for (const auto& forbidden : forbidden_roots) {
        if (IsWithin(output_root_, forbidden)) {
            throw HarnessError(HarnessErrorCode::ForbiddenPath, "evidence root overlaps source corpus");
        }
    }
}

void EvidenceSink::Write(
    const std::filesystem::path& relative_path,
    const std::string_view content) {
    if (closed_) {
        throw HarnessError(HarnessErrorCode::Closed, "evidence sink is closed");
    }
    if (!IsSafeRelative(relative_path)) {
        throw HarnessError(HarnessErrorCode::ForbiddenPath, "evidence path escaped output root");
    }
    if (content.size() > max_bytes_ - std::min(bytes_written_, max_bytes_)) {
        throw HarnessError(HarnessErrorCode::Capacity, "evidence capacity exhausted");
    }
    const auto destination = (output_root_ / relative_path).lexically_normal();
    if (!IsWithin(destination, output_root_)) {
        throw HarnessError(HarnessErrorCode::ForbiddenPath, "evidence path escaped output root");
    }
    std::error_code error;
    std::filesystem::create_directories(destination.parent_path(), error);
    if (error) {
        throw HarnessError(HarnessErrorCode::Io, "evidence directory creation failed");
    }
    const auto temporary = destination.string() + ".partial";
    {
        std::ofstream stream(temporary, std::ios::binary | std::ios::trunc);
        if (!stream || !stream.write(content.data(), static_cast<std::streamsize>(content.size()))) {
            std::filesystem::remove(temporary, error);
            throw HarnessError(HarnessErrorCode::Io, "evidence temporary write failed");
        }
    }
    std::filesystem::rename(temporary, destination, error);
    if (error) {
        std::filesystem::remove(temporary, error);
        throw HarnessError(HarnessErrorCode::Io, "evidence atomic commit failed");
    }
    bytes_written_ += content.size();
}

void EvidenceSink::Close() noexcept {
    closed_ = true;
}

std::size_t EvidenceSink::BytesWritten() const noexcept {
    return bytes_written_;
}

}  // namespace ihomeland::sim
