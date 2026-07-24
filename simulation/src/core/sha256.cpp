#include "ihomeland/sim/core/sha256.hpp"

#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <bcrypt.h>

#include <array>
#include <fstream>
#include <limits>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {
namespace {

/// BCryptAlgorithm 在异常路径上关闭 Windows CNG algorithm provider。
class BCryptAlgorithm final {
public:
    /// 构造函数打开 SHA-256 provider。
    BCryptAlgorithm() {
        if (BCryptOpenAlgorithmProvider(&handle_, BCRYPT_SHA256_ALGORITHM, nullptr, 0) < 0) {
            throw std::runtime_error("SHA-256 provider initialization failed");
        }
    }

    /// 析构函数释放 provider handle。
    ~BCryptAlgorithm() {
        if (handle_ != nullptr) {
            BCryptCloseAlgorithmProvider(handle_, 0);
        }
    }

    BCryptAlgorithm(const BCryptAlgorithm&) = delete;
    BCryptAlgorithm& operator=(const BCryptAlgorithm&) = delete;

    /// Handle 返回只在 owner 生命周期内有效的 provider handle。
    [[nodiscard]] BCRYPT_ALG_HANDLE Handle() const noexcept { return handle_; }

private:
    /// handle_ 由当前 owner 唯一关闭。
    BCRYPT_ALG_HANDLE handle_{nullptr};
};

/// ToHex 把固定 32-byte digest 编码成不受 locale 影响的 lowercase hex。
[[nodiscard]] std::string ToHex(const std::array<std::byte, 32>& digest) {
    constexpr char digits[] = "0123456789abcdef";
    std::string result(64, '0');
    for (std::size_t index = 0; index < digest.size(); ++index) {
        const auto value = std::to_integer<unsigned int>(digest[index]);
        result[index * 2] = digits[value >> 4U];
        result[index * 2 + 1] = digits[value & 0x0fU];
    }
    return result;
}

}  // namespace

std::string Sha256Bytes(const std::span<const std::byte> bytes) {
    if (bytes.size() > std::numeric_limits<ULONG>::max()) {
        throw std::length_error("SHA-256 input exceeds CNG single-call limit");
    }
    BCryptAlgorithm algorithm;
    std::array<std::byte, 32> digest{};
    BCRYPT_HASH_HANDLE hash = nullptr;
    if (BCryptCreateHash(algorithm.Handle(), &hash, nullptr, 0, nullptr, 0, 0) < 0) {
        throw std::runtime_error("SHA-256 hash initialization failed");
    }
    try {
        if ((!bytes.empty() &&
             BCryptHashData(
                 hash,
                 reinterpret_cast<PUCHAR>(const_cast<std::byte*>(bytes.data())),
                 static_cast<ULONG>(bytes.size()),
                 0) < 0) ||
            BCryptFinishHash(
                hash,
                reinterpret_cast<PUCHAR>(digest.data()),
                static_cast<ULONG>(digest.size()),
                0) < 0) {
            throw std::runtime_error("SHA-256 hashing failed");
        }
    } catch (...) {
        BCryptDestroyHash(hash);
        throw;
    }
    BCryptDestroyHash(hash);
    return ToHex(digest);
}

std::string Sha256Text(const std::string_view text) {
    return Sha256Bytes(std::as_bytes(std::span{text.data(), text.size()}));
}

std::string Sha256File(const std::filesystem::path& path) {
    std::ifstream stream(path, std::ios::binary | std::ios::ate);
    if (!stream) {
        throw std::runtime_error("SHA-256 source file open failed");
    }
    const auto length = stream.tellg();
    if (length < 0) {
        throw std::runtime_error("SHA-256 source file length failed");
    }
    stream.seekg(0, std::ios::beg);
    std::vector<std::byte> bytes(static_cast<std::size_t>(length));
    if (!bytes.empty() &&
        !stream.read(reinterpret_cast<char*>(bytes.data()), static_cast<std::streamsize>(bytes.size()))) {
        throw std::runtime_error("SHA-256 source file read failed");
    }
    return Sha256Bytes(bytes);
}

}  // namespace ihomeland::sim
