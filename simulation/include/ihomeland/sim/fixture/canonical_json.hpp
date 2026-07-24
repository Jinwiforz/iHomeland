#pragma once

#include <string>
#include <string_view>

namespace ihomeland::sim {

/// CanonicalJsonDocument 保存规范单行 JSON token stream 及其 lowercase SHA-256。
struct CanonicalJsonDocument final {
    /// text 使用 UTF-8、排序 object key、最短稳定 number 表达且不附加换行。
    std::string text;
    /// sha256 只绑定 text 的原始 UTF-8 bytes。
    std::string sha256;
};

/// CanonicalizeJson 严格解析 JSON 并生成不受输入空白、键顺序或 locale 影响的规范 token stream。
[[nodiscard]] CanonicalJsonDocument CanonicalizeJson(std::string_view source);

}  // namespace ihomeland::sim
