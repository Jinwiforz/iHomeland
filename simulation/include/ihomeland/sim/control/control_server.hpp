#pragma once

#include "ihomeland/sim/transport/udp_listener.hpp"

#include <iosfwd>
#include <string>

namespace ihomeland::sim {

/// ControlBuildBinding 是 Go hello 必须匹配的 B0.3 资格 identity。
struct ControlBuildBinding final {
    /// build_identity 是 Release/ASan 同源资格中的 Release identity digest。
    std::string build_identity;
    /// model_manifest 是冻结 battle model manifest digest。
    std::string model_manifest;
    /// profile_manifest 是冻结 network profile manifest digest。
    std::string profile_manifest;
    /// platform_qualification 固定 Windows x64 implementation 结论。
    std::string platform_qualification;
};

/// RunControlStdio 在给定 streams 上运行唯一私有 control session。
///
/// input/output 只承载 length-prefixed frame，diagnostics 只承载低敏失败原因。
[[nodiscard]] int RunControlStdio(
    std::istream& input,
    std::ostream& output,
    std::ostream& diagnostics,
    const ControlBuildBinding& build,
    const BattleUdpListenerConfig* battle_listener = nullptr);

}  // namespace ihomeland::sim
