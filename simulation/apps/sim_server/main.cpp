#include "ihomeland/sim/core/adapter_smoke.hpp"
#include "ihomeland/sim/core/fixed_tick.hpp"
#include "ihomeland/sim/control/control_server.hpp"

#include <fcntl.h>
#include <io.h>

#include <cstdio>
#include <charconv>
#include <iostream>
#include <optional>
#include <string>
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

/// ParsePort 严格解析十进制 UDP port，不接受符号、空白或尾随字符。
[[nodiscard]] std::optional<std::uint16_t> ParsePort(
    const std::string_view value) noexcept {
    unsigned int parsed = 0;
    const auto result = std::from_chars(
        value.data(), value.data() + value.size(), parsed);
    if (result.ec != std::errc{} ||
        result.ptr != value.data() + value.size() ||
        parsed == 0 || parsed > 65535) {
        return std::nullopt;
    }
    return static_cast<std::uint16_t>(parsed);
}

/// ParseListenerIdentity 严格解析 128-bit lowercase hex listener identity。
[[nodiscard]] std::optional<std::array<std::uint8_t, 16>>
ParseListenerIdentity(const std::string_view value) noexcept {
    if (value.size() != 32) {
        return std::nullopt;
    }
    std::array<std::uint8_t, 16> result{};
    for (std::size_t index = 0; index < result.size(); ++index) {
        unsigned int byte = 0;
        const auto begin = value.data() + index * 2;
        const auto parsed = std::from_chars(begin, begin + 2, byte, 16);
        if (parsed.ec != std::errc{} || parsed.ptr != begin + 2 ||
            byte > 255) {
            return std::nullopt;
        }
        result[index] = static_cast<std::uint8_t>(byte);
    }
    if (result == std::array<std::uint8_t, 16>{}) {
        return std::nullopt;
    }
    return result;
}

}  // namespace

/// main 提供离线 smoke 与由 Go 显式配置的私有 control/唯一 UDP listener 入口。
int main(const int argument_count, const char* const arguments[]) {
    if (argument_count == 2 && std::string_view{arguments[1]} == "--smoke") {
        return RunSmoke();
    }
    if ((argument_count == 2 || argument_count == 12) &&
        std::string_view{arguments[1]} == "--control-stdio") {
        if (!ConfigureControlStdioBinary()) {
            std::cerr << "simulation control stdio binary mode failed\n";
            return 1;
        }
        std::optional<ihomeland::sim::BattleUdpListenerConfig> listener;
        if (argument_count == 12) {
            if (std::string_view{arguments[2]} != "--battle-udp-bind-host" ||
                std::string_view{arguments[4]} != "--battle-udp-bind-port" ||
                std::string_view{arguments[6]} != "--battle-udp-advertised-host" ||
                std::string_view{arguments[8]} != "--battle-udp-advertised-port" ||
                std::string_view{arguments[10]} != "--battle-listener-identity") {
                std::cerr << "simulation battle UDP arguments are invalid\n";
                return 2;
            }
            const auto bind_port = ParsePort(arguments[5]);
            const auto advertised_port = ParsePort(arguments[9]);
            const auto identity = ParseListenerIdentity(arguments[11]);
            if (!bind_port || !advertised_port || !identity) {
                std::cerr << "simulation battle UDP values are invalid\n";
                return 2;
            }
            listener = ihomeland::sim::BattleUdpListenerConfig{
                .bind_endpoint = {arguments[3], *bind_port},
                .advertised_endpoint = {arguments[7], *advertised_port},
                .listener_identity = *identity,
                .mode = ihomeland::sim::BattleUdpListenerMode::Production,
            };
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
            },
            listener ? &*listener : nullptr);
    }
    std::cerr << "usage: ihomeland-sim-server --smoke|--control-stdio [battle UDP config]\n";
    return 2;
}
