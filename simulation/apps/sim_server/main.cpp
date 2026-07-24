#include "ihomeland/sim/core/adapter_smoke.hpp"
#include "ihomeland/sim/core/fixed_tick.hpp"
#include "ihomeland/sim/control/control_server.hpp"

#include <fcntl.h>
#include <io.h>

#include <cstdio>
#include <iostream>
#include <string_view>

namespace {

/// RunSmoke 执行无 listener 的真实 dependency 与固定 Tick smoke。
[[nodiscard]] int RunSmoke() {
    const auto fixture = ihomeland::sim::RunFixtureSmoke(R"({"simulation_tick_ms":50})");
    ihomeland::sim::FixedTick fixed_tick{{
        .simulation_tick_ms = fixture.simulation_tick_ms,
        .input_tick_hz = 40,
    }};
    const auto state = fixed_tick.Advance();
    if (state.committed_tick != 1 || state.elapsed_ms != 50 || !ihomeland::sim::RunJoltSmoke() ||
        !ihomeland::sim::RunDetourSmoke()) {
        return 1;
    }
    std::cout << R"({"status":"ok","tick":1,"elapsed_ms":50})" << '\n';
    return 0;
}

/// ConfigureControlStdioBinary 禁止 Windows 文本模式改写 length prefix 中的 CR/LF 字节。
[[nodiscard]] bool ConfigureControlStdioBinary() noexcept {
    return _setmode(_fileno(stdin), _O_BINARY) != -1 &&
           _setmode(_fileno(stdout), _O_BINARY) != -1;
}

}  // namespace

/// main 提供离线 smoke 与私有继承 stdio control 入口，不创建 listener 或 production port。
int main(const int argument_count, const char* const arguments[]) {
    if (argument_count == 2 && std::string_view{arguments[1]} == "--smoke") {
        return RunSmoke();
    }
    if (argument_count == 2 &&
        std::string_view{arguments[1]} == "--control-stdio") {
        if (!ConfigureControlStdioBinary()) {
            std::cerr << "simulation control stdio binary mode failed\n";
            return 1;
        }
        return ihomeland::sim::RunControlStdio(
            std::cin,
            std::cout,
            std::cerr,
            ihomeland::sim::ControlBuildBinding{
                .build_identity = IHOMELAND_CONTROL_BUILD_IDENTITY,
                .model_manifest = IHOMELAND_CONTROL_MODEL_MANIFEST,
                .profile_manifest = IHOMELAND_CONTROL_PROFILE_MANIFEST,
                .platform_qualification =
                    "implementation-qualified-windows-x64",
            });
    }
    std::cerr << "usage: ihomeland-sim-server --smoke|--control-stdio\n";
    return 2;
}
