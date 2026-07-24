#pragma once

#include <cstddef>
#include <filesystem>
#include <span>
#include <string>
#include <string_view>

namespace ihomeland::sim {

/// Sha256Bytes 对给定 bytes 计算 lowercase SHA-256，输入边界不受 locale 影响。
[[nodiscard]] std::string Sha256Bytes(std::span<const std::byte> bytes);

/// Sha256Text 按调用方提供的 UTF-8 bytes 计算 lowercase SHA-256，不执行换行或 Unicode 归一化。
[[nodiscard]] std::string Sha256Text(std::string_view text);

/// Sha256File 对文件原始 bytes 计算 lowercase SHA-256，并在读取失败时抛出异常。
[[nodiscard]] std::string Sha256File(const std::filesystem::path& path);

}  // namespace ihomeland::sim
