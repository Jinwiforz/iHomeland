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

/// ControlOptions 保存解析后且尚未做语义校验的本机 control 选项。
struct ControlOptions final {
    /// bind_host 是 listener 实际绑定的数字地址。
    std::optional<std::string> bind_host;
    /// bind_port 是 listener 实际绑定的固定端口文本。
    std::optional<std::string> bind_port;
    /// advertised_host 是 BattleTicket 对外声明的数字地址。
    std::optional<std::string> advertised_host;
    /// advertised_port 是 BattleTicket 对外声明的固定端口文本。
    std::optional<std::string> advertised_port;
    /// listener_identity 是不可复活 listener identity 文本。
    std::optional<std::string> listener_identity;
    /// qualification_run_id 仅由 B0.6 资格入口显式传入。
    std::optional<std::string> qualification_run_id;
};

/// AssignOption 拒绝重复选项，避免命令行顺序覆盖安全边界。
[[nodiscard]] bool AssignOption(
    std::optional<std::string>& target,
    const std::string_view value) {
    if (target.has_value()) {
        return false;
    }
    target.emplace(value);
    return true;
}

/// ParseControlOptions 解析闭合 name/value 集合并拒绝未知或缺值选项。
[[nodiscard]] std::optional<ControlOptions> ParseControlOptions(
    const int argument_count,
    const char* const arguments[]) {
    ControlOptions options;
    for (int index = 2; index < argument_count; index += 2) {
        if (index + 1 >= argument_count) {
            return std::nullopt;
        }
        const auto name = std::string_view{arguments[index]};
        const auto value = std::string_view{arguments[index + 1]};
        bool assigned = false;
        if (name == "--battle-udp-bind-host") {
            assigned = AssignOption(options.bind_host, value);
        } else if (name == "--battle-udp-bind-port") {
            assigned = AssignOption(options.bind_port, value);
        } else if (name == "--battle-udp-advertised-host") {
            assigned = AssignOption(options.advertised_host, value);
        } else if (name == "--battle-udp-advertised-port") {
            assigned = AssignOption(options.advertised_port, value);
        } else if (name == "--battle-listener-identity") {
            assigned = AssignOption(options.listener_identity, value);
        } else if (name == "--battle-qualification-run-id") {
            assigned =
                AssignOption(options.qualification_run_id, value);
        }
        if (!assigned) {
            return std::nullopt;
        }
    }
    const auto listener_fields =
        static_cast<unsigned int>(options.bind_host.has_value()) +
        static_cast<unsigned int>(options.bind_port.has_value()) +
        static_cast<unsigned int>(options.advertised_host.has_value()) +
        static_cast<unsigned int>(options.advertised_port.has_value()) +
        static_cast<unsigned int>(options.listener_identity.has_value());
    constexpr unsigned int listener_field_count = 5;
    if (listener_fields != 0 &&
        listener_fields != listener_field_count) {
        return std::nullopt;
    }
    return options;
}

}  // namespace

/// main 提供离线 smoke 与由 Go 显式配置的私有 control/唯一 UDP listener 入口。
int main(const int argument_count, const char* const arguments[]) {
    if (argument_count == 2 && std::string_view{arguments[1]} == "--smoke") {
        return RunSmoke();
    }
    if (argument_count >= 2 &&
        std::string_view{arguments[1]} == "--control-stdio") {
        if (!ConfigureControlStdioBinary()) {
            std::cerr << "simulation control stdio binary mode failed\n";
            return 1;
        }
        const auto options =
            ParseControlOptions(argument_count, arguments);
        if (!options) {
            std::cerr << "simulation control arguments are invalid\n";
            return 2;
        }
        std::optional<ihomeland::sim::BattleUdpListenerConfig> listener;
        if (options->bind_host) {
            const auto bind_port = ParsePort(*options->bind_port);
            const auto advertised_port =
                ParsePort(*options->advertised_port);
            const auto identity =
                ParseListenerIdentity(*options->listener_identity);
            if (!bind_port || !advertised_port || !identity) {
                std::cerr << "simulation battle UDP values are invalid\n";
                return 2;
            }
            listener = ihomeland::sim::BattleUdpListenerConfig{
                .bind_endpoint = {*options->bind_host, *bind_port},
                .advertised_endpoint =
                    {*options->advertised_host, *advertised_port},
                .listener_identity = *identity,
                .mode = ihomeland::sim::BattleUdpListenerMode::Production,
            };
        }
        std::optional<ihomeland::sim::QualificationControlConfig>
            qualification;
        if (options->qualification_run_id) {
            qualification.emplace(
                ihomeland::sim::QualificationControlConfig{
                    .run_id = *options->qualification_run_id,
                });
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
            listener ? &*listener : nullptr,
            qualification ? &*qualification : nullptr);
    }
    std::cerr
        << "usage: ihomeland-sim-server --smoke|--control-stdio "
           "[battle UDP config] [--battle-qualification-run-id <run>]\n";
    return 2;
}
