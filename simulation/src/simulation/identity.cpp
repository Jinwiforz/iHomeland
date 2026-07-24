#include "ihomeland/sim/simulation/identity.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <algorithm>
#include <charconv>
#include <stdexcept>
#include <utility>

namespace ihomeland::sim {
namespace {

/// RequireDigest 验证 canonical lowercase SHA-256。
void RequireDigest(const std::string& digest) {
    if (digest.size() != 64 ||
        !std::all_of(digest.begin(), digest.end(), [](const char value) {
            return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f');
        })) {
        throw std::invalid_argument("identity digest must be lowercase SHA-256");
    }
}

/// AppendToken 使用显式长度分隔字段，避免字符串拼接歧义。
void AppendToken(std::string& target, const std::string_view token) {
    target += std::to_string(token.size());
    target.push_back(':');
    target.append(token);
    target.push_back('|');
}

/// AssignmentFingerprint 计算完整 AssignmentStamp digest。
[[nodiscard]] std::string AssignmentFingerprint(
    const Identity128& world,
    const std::uint64_t generation,
    const std::uint64_t fence,
    const Identity128& node) {
    std::string token;
    AppendToken(token, world.ToLowerHex());
    AppendToken(token, std::to_string(generation));
    AppendToken(token, std::to_string(fence));
    AppendToken(token, node.ToLowerHex());
    return Sha256Text(token);
}

}  // namespace

Identity128::Identity128(std::array<std::uint8_t, 16> bytes)
    : bytes_(bytes) {}

Identity128 Identity128::ParseLowerHex(const std::string_view text) {
    if (text.size() != 32) {
        throw std::invalid_argument("Identity128 must contain 32 lowercase hex characters");
    }
    std::array<std::uint8_t, 16> bytes{};
    bool any_nonzero = false;
    for (std::size_t index = 0; index < bytes.size(); ++index) {
        const auto high = text[index * 2];
        const auto low = text[index * 2 + 1];
        const auto decode = [](const char value) -> std::uint8_t {
            if (value >= '0' && value <= '9') {
                return static_cast<std::uint8_t>(value - '0');
            }
            if (value >= 'a' && value <= 'f') {
                return static_cast<std::uint8_t>(value - 'a' + 10);
            }
            throw std::invalid_argument("Identity128 must use lowercase hex");
        };
        bytes[index] = static_cast<std::uint8_t>((decode(high) << 4U) | decode(low));
        any_nonzero = any_nonzero || bytes[index] != 0;
    }
    if (!any_nonzero) {
        throw std::invalid_argument("Identity128 zero value is invalid");
    }
    return Identity128(bytes);
}

std::string Identity128::ToLowerHex() const {
    constexpr char digits[] = "0123456789abcdef";
    std::string result(32, '0');
    for (std::size_t index = 0; index < bytes_.size(); ++index) {
        result[index * 2] = digits[bytes_[index] >> 4U];
        result[index * 2 + 1] = digits[bytes_[index] & 0x0fU];
    }
    return result;
}

AssignmentStamp::AssignmentStamp(
    Identity128 world_instance_id,
    const std::uint64_t generation,
    const std::uint64_t lease_fence,
    Identity128 runtime_node_id)
    : world_instance_id_(std::move(world_instance_id)),
      generation_(generation),
      lease_fence_(lease_fence),
      runtime_node_id_(std::move(runtime_node_id)),
      fingerprint_(AssignmentFingerprint(
          world_instance_id_,
          generation_,
          lease_fence_,
          runtime_node_id_)) {
    if (generation == 0 || lease_fence == 0) {
        throw std::invalid_argument("assignment generation and fence must be nonzero");
    }
}

const Identity128& AssignmentStamp::WorldInstanceId() const noexcept { return world_instance_id_; }
std::uint64_t AssignmentStamp::Generation() const noexcept { return generation_; }
std::uint64_t AssignmentStamp::LeaseFence() const noexcept { return lease_fence_; }
const Identity128& AssignmentStamp::RuntimeNodeId() const noexcept { return runtime_node_id_; }
const std::string& AssignmentStamp::Fingerprint() const noexcept { return fingerprint_; }

BuildConfigIdentity::BuildConfigIdentity(
    std::string build,
    std::string model,
    std::string profile,
    std::string config,
    std::string navigation,
    std::string physics)
    : build_(std::move(build)),
      model_(std::move(model)),
      profile_(std::move(profile)),
      config_(std::move(config)),
      navigation_(std::move(navigation)),
      physics_(std::move(physics)) {
    RequireDigest(build_);
    RequireDigest(model_);
    RequireDigest(profile_);
    RequireDigest(config_);
    RequireDigest(navigation_);
    RequireDigest(physics_);
    std::string token;
    AppendToken(token, build_);
    AppendToken(token, model_);
    AppendToken(token, profile_);
    AppendToken(token, config_);
    AppendToken(token, navigation_);
    AppendToken(token, physics_);
    fingerprint_ = Sha256Text(token);
}

const std::string& BuildConfigIdentity::Build() const noexcept { return build_; }
const std::string& BuildConfigIdentity::Model() const noexcept { return model_; }
const std::string& BuildConfigIdentity::Profile() const noexcept { return profile_; }
const std::string& BuildConfigIdentity::Config() const noexcept { return config_; }
const std::string& BuildConfigIdentity::Navigation() const noexcept { return navigation_; }
const std::string& BuildConfigIdentity::Physics() const noexcept { return physics_; }
const std::string& BuildConfigIdentity::Fingerprint() const noexcept { return fingerprint_; }

SimulationInstanceIdentity::SimulationInstanceIdentity(
    Identity128 instance_id,
    AssignmentStamp assignment,
    const std::uint64_t mapping_generation,
    BuildConfigIdentity build_config)
    : instance_id_(std::move(instance_id)),
      assignment_(std::move(assignment)),
      mapping_generation_(mapping_generation),
      build_config_(std::move(build_config)) {
    if (mapping_generation == 0) {
        throw std::invalid_argument("mapping generation must be nonzero");
    }
    std::string token;
    AppendToken(token, instance_id_.ToLowerHex());
    AppendToken(token, assignment_.Fingerprint());
    AppendToken(token, std::to_string(mapping_generation_));
    AppendToken(token, build_config_.Fingerprint());
    fingerprint_ = Sha256Text(token);
}

const Identity128& SimulationInstanceIdentity::InstanceId() const noexcept { return instance_id_; }
const AssignmentStamp& SimulationInstanceIdentity::Assignment() const noexcept { return assignment_; }
std::uint64_t SimulationInstanceIdentity::MappingGeneration() const noexcept { return mapping_generation_; }
const BuildConfigIdentity& SimulationInstanceIdentity::BuildConfig() const noexcept { return build_config_; }
const std::string& SimulationInstanceIdentity::Fingerprint() const noexcept { return fingerprint_; }

}  // namespace ihomeland::sim
