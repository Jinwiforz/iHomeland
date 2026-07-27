#include "ihomeland/sim/transport/resource_governor.hpp"
#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"

#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>

namespace {

/// Require 把resource gate漂移转换为test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Endpoint 返回稳定canonical remote。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint
Endpoint(const std::uint8_t suffix) {
    auto endpoint =
        ihomeland::sim::BattleRemoteEndpoint{};
    endpoint.address[0] = 0x20;
    endpoint.address[1] = 0x01;
    endpoint.address[15] = suffix;
    endpoint.port = 40'000;
    return endpoint;
}

/// TestPreAuthFixedState 验证pre-auth仅使用固定IP table且端口不绕过限流。
void TestPreAuthFixedState() {
    auto governor =
        ihomeland::sim::BattleResourceGovernor{};
    for (std::uint32_t index = 0;
         index < 40;
         ++index) {
        auto endpoint = Endpoint(1);
        endpoint.port =
            static_cast<std::uint16_t>(40'000 + index);
        Require(
            governor.AllowPreAuthIp(
                endpoint,
                1'000).allowed,
            "pre-auth burst rejected below 40");
    }
    Require(
        governor.AllowPreAuthIp(
            Endpoint(1),
            1'000).rejection ==
            ihomeland::sim::BattleResourceRejection::
                IpRate,
        "source port bypassed per-IP rate");
    Require(
        governor.AllowPreAuthIp(
            Endpoint(1),
            1'050).allowed,
        "integer token refill did not restore one request");

    auto capacity =
        ihomeland::sim::BattleResourceGovernor{};
    for (std::uint16_t index = 1;
         index <= 256;
         ++index) {
        auto endpoint = Endpoint(
            static_cast<std::uint8_t>(index));
        endpoint.address[14] =
            static_cast<std::uint8_t>(index >> 8U);
        Require(
            capacity.AllowPreAuthIp(
                endpoint,
                2'000).allowed,
            "fixed pre-auth registry rejected below cap");
    }
    auto overflow = Endpoint(1);
    overflow.address[13] = 1;
    Require(
        capacity.AllowPreAuthIp(
            overflow,
            2'000).rejection ==
            ihomeland::sim::BattleResourceRejection::
                RegistryCapacity,
        "pre-auth registry dynamically expanded");
}

/// TestAuthenticatedRates 验证ticket/session/message/instance分层token bucket。
void TestAuthenticatedRates() {
    auto governor =
        ihomeland::sim::BattleResourceGovernor{};
    for (std::uint32_t index = 0;
         index < 8;
         ++index) {
        Require(
            governor.AllowTicket(1, 3'000).allowed,
            "ticket burst rejected below 8");
    }
    Require(
        governor.AllowTicket(1, 3'000).rejection ==
            ihomeland::sim::BattleResourceRejection::
                TicketRate,
        "ticket rate exceeded burst");

    for (std::uint32_t index = 0;
         index < 4;
         ++index) {
        Require(
            governor.AllowMessage(
                11,
                22,
                3006,
                2,
                4'000).allowed,
            "message burst rejected below route cap");
    }
    Require(
        governor.AllowMessage(
            11,
            22,
            3006,
            2,
            4'000).rejection ==
            ihomeland::sim::BattleResourceRejection::
                MessageRate,
        "route-specific message rate was not enforced");
}

/// TestHardBudgets 验证node/session ingress/egress都固定为256 items。
void TestHardBudgets() {
    auto runtime_metrics =
        ihomeland::sim::BattleRuntimeMetrics{};
    auto governor =
        ihomeland::sim::BattleResourceGovernor{
            &runtime_metrics};
    Require(
        governor.ReserveIngress(1, 256).allowed &&
            governor.ReserveIngress(1, 1).rejection ==
                ihomeland::sim::
                    BattleResourceRejection::
                        IngressBudget,
        "session ingress exceeded 256-item cap");
    governor.ReleaseIngress(1, 128);
    Require(
        governor.ReserveIngress(1, 128).allowed,
        "released ingress capacity was not reusable");
    governor.ReleaseIngress(1, 256);

    Require(
        governor.ReserveEgress(1, 200).allowed &&
            governor.ReserveEgress(2, 56).allowed &&
            governor.ReserveEgress(3, 1).rejection ==
                ihomeland::sim::
                    BattleResourceRejection::
                        EgressBudget,
        "node egress exceeded 256-item cap");
    const auto metrics = governor.Metrics();
    Require(
        metrics.ingress_high_watermark == 256 &&
            metrics.egress_high_watermark == 256 &&
            metrics.rejected.at(
                static_cast<std::size_t>(
                    ihomeland::sim::
                        BattleResourceRejection::
                            EgressBudget)) == 1,
        "resource metrics exposed wrong low-cardinality accounting");
    const auto runtime = runtime_metrics.Snapshot();
    Require(
        runtime.ingress_queue_high_watermark == 256 &&
            runtime.egress_queue_high_watermark == 256 &&
            runtime.rejected_packets == 2,
        "resource governor did not update runtime metrics owner");
}

/// TestSessionOwnerCleanup 验证terminal session归还遗留budget且不泄漏registry。
void TestSessionOwnerCleanup() {
    auto governor =
        ihomeland::sim::BattleResourceGovernor{};
    {
        auto session =
            ihomeland::sim::
                BattleSessionResourceGovernor(
                    governor,
                    11,
                    22);
        Require(
            session.ReserveIngress(128).allowed &&
                session.ReserveEgress(256).allowed,
            "session owner failed to reserve hard budget");
    }
    {
        auto successor =
            ihomeland::sim::
                BattleSessionResourceGovernor(
                    governor,
                    12,
                    22);
        Require(
            successor.ReserveIngress(256).allowed &&
                successor.ReserveEgress(256).allowed,
            "terminal session leaked node budget");
    }
}

}  // namespace

int main() {
    TestPreAuthFixedState();
    TestAuthenticatedRates();
    TestHardBudgets();
    TestSessionOwnerCleanup();
    return 0;
}
