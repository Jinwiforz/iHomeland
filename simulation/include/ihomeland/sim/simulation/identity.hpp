#pragma once

#include <array>
#include <cstdint>
#include <string>
#include <string_view>

namespace ihomeland::sim {

/// Identity128 是不依赖 UUID 库的固定 128-bit canonical identity。
class Identity128 final {
public:
    /// ParseLowerHex 从 32 个 lowercase hex 字符构造非零 identity。
    [[nodiscard]] static Identity128 ParseLowerHex(std::string_view text);

    /// ToLowerHex 返回固定 32 字符 canonical token。
    [[nodiscard]] std::string ToLowerHex() const;

    /// operator== 比较完整 128-bit value。
    [[nodiscard]] bool operator==(const Identity128&) const noexcept = default;

private:
    /// 构造函数只接受已经验证的 bytes。
    explicit Identity128(std::array<std::uint8_t, 16> bytes);

    /// bytes_ 以网络显示顺序保存，不依赖主机 endian。
    std::array<std::uint8_t, 16> bytes_;
};

/// AssignmentStamp 不可变绑定 world generation、lease fence 与 runtime node。
class AssignmentStamp final {
public:
    /// 构造函数拒绝零 generation/fence，并计算完整 fingerprint。
    AssignmentStamp(
        Identity128 world_instance_id,
        std::uint64_t generation,
        std::uint64_t lease_fence,
        Identity128 runtime_node_id);

    /// WorldInstanceId 返回不可变 world identity。
    [[nodiscard]] const Identity128& WorldInstanceId() const noexcept;

    /// Generation 返回 assignment generation。
    [[nodiscard]] std::uint64_t Generation() const noexcept;

    /// LeaseFence 返回阻止旧 owner 写入的单调 fence。
    [[nodiscard]] std::uint64_t LeaseFence() const noexcept;

    /// RuntimeNodeId 返回本 assignment 的 runtime owner。
    [[nodiscard]] const Identity128& RuntimeNodeId() const noexcept;

    /// Fingerprint 返回完整字段的 lowercase SHA-256，不包含 secret。
    [[nodiscard]] const std::string& Fingerprint() const noexcept;

private:
    /// world_instance_id_ 是 PersonalWorld admission 选择的运行 world。
    Identity128 world_instance_id_;
    /// generation_ 在每次重新 assignment 时单调增加。
    std::uint64_t generation_;
    /// lease_fence_ 阻止旧 runtime owner 继续提交结果。
    std::uint64_t lease_fence_;
    /// runtime_node_id_ 标识本地实例被分配到的 node。
    Identity128 runtime_node_id_;
    /// fingerprint_ 绑定以上全部字段的 canonical digest。
    std::string fingerprint_;
};

/// BuildConfigIdentity 不可变绑定 runtime 消费的全部非 secret build/config source。
class BuildConfigIdentity final {
public:
    /// 构造函数要求每个 identity 都是 canonical lowercase SHA-256。
    BuildConfigIdentity(
        std::string build,
        std::string model,
        std::string profile,
        std::string config,
        std::string navigation,
        std::string physics);

    /// Build 返回 compiler/dependency/flags target identity。
    [[nodiscard]] const std::string& Build() const noexcept;

    /// Model 返回 battle model manifest digest。
    [[nodiscard]] const std::string& Model() const noexcept;

    /// Profile 返回 battle network profile manifest digest。
    [[nodiscard]] const std::string& Profile() const noexcept;

    /// Config 返回实例 checked configuration digest。
    [[nodiscard]] const std::string& Config() const noexcept;

    /// Navigation 返回 nav asset/config digest。
    [[nodiscard]] const std::string& Navigation() const noexcept;

    /// Physics 返回 physics adapter/config digest。
    [[nodiscard]] const std::string& Physics() const noexcept;

    /// Fingerprint 返回六个 digest 的 canonical length-delimited SHA-256。
    [[nodiscard]] const std::string& Fingerprint() const noexcept;

private:
    /// build_ 绑定 exact toolchain/dependency/flags。
    std::string build_;
    /// model_ 绑定冻结 model corpus。
    std::string model_;
    /// profile_ 绑定冻结 network profile。
    std::string profile_;
    /// config_ 绑定 checked runtime configuration。
    std::string config_;
    /// navigation_ 绑定版本化 nav input。
    std::string navigation_;
    /// physics_ 绑定 physics adapter/config。
    std::string physics_;
    /// fingerprint_ 绑定全部字段且不包含路径。
    std::string fingerprint_;
};

/// SimulationInstanceIdentity 不可变绑定单个实例及其 assignment/mapping/build 时间线。
class SimulationInstanceIdentity final {
public:
    /// 构造函数拒绝零 mapping generation，并计算完整 fingerprint。
    SimulationInstanceIdentity(
        Identity128 instance_id,
        AssignmentStamp assignment,
        std::uint64_t mapping_generation,
        BuildConfigIdentity build_config);

    /// InstanceId 返回进程内外都稳定的实例 identity。
    [[nodiscard]] const Identity128& InstanceId() const noexcept;

    /// Assignment 返回完整不可变 AssignmentStamp。
    [[nodiscard]] const AssignmentStamp& Assignment() const noexcept;

    /// MappingGeneration 返回 InputTick mapping epoch generation。
    [[nodiscard]] std::uint64_t MappingGeneration() const noexcept;

    /// BuildConfig 返回全部 source/build identity。
    [[nodiscard]] const BuildConfigIdentity& BuildConfig() const noexcept;

    /// Fingerprint 返回实例时间线完整绑定的 lowercase SHA-256。
    [[nodiscard]] const std::string& Fingerprint() const noexcept;

private:
    /// instance_id_ 区分同一 assignment 上的不同进程实例。
    Identity128 instance_id_;
    /// assignment_ 绑定 world、generation、fence 与 node。
    AssignmentStamp assignment_;
    /// mapping_generation_ 变化时旧 InputTick 全部 stale。
    std::uint64_t mapping_generation_;
    /// build_config_ 绑定运行规则和 adapter source。
    BuildConfigIdentity build_config_;
    /// fingerprint_ 是完整实例时间线 identity。
    std::string fingerprint_;
};

}  // namespace ihomeland::sim
