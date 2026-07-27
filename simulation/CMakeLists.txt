cmake_minimum_required(VERSION 4.4)

project(iHomelandSimulation VERSION 0.1.0 LANGUAGES C CXX)

include(CTest)
include(cmake/IHomelandBuild.cmake)
include(cmake/Dependencies.cmake)

set(IHOMELAND_GENERATED_PROTO_ROOT
    "${CMAKE_CURRENT_SOURCE_DIR}/out/generated/proto")
set(IHOMELAND_BATTLE_PROTO_SOURCE
    "${IHOMELAND_GENERATED_PROTO_ROOT}/ihomeland/battle/v1/battle.pb.cc")
set(IHOMELAND_BATTLE_PROTO_HEADER
    "${IHOMELAND_GENERATED_PROTO_ROOT}/ihomeland/battle/v1/battle.pb.h")
if(NOT EXISTS "${IHOMELAND_BATTLE_PROTO_SOURCE}" OR
   NOT EXISTS "${IHOMELAND_BATTLE_PROTO_HEADER}")
    message(FATAL_ERROR
        "C++ battle protocol 未生成；请先运行 tools/proto/proto.ps1 generate")
endif()

add_library(ihomeland_battle_protocol STATIC
    "${IHOMELAND_BATTLE_PROTO_SOURCE}"
    "${IHOMELAND_BATTLE_PROTO_HEADER}"
)
add_library(iHomeland::BattleProtocol ALIAS ihomeland_battle_protocol)
target_include_directories(ihomeland_battle_protocol
    PUBLIC "${IHOMELAND_GENERATED_PROTO_ROOT}"
)
target_include_directories(ihomeland_battle_protocol
    SYSTEM PUBLIC
        "${IHOMELAND_PROTOBUF_SOURCE}/src"
        "${IHOMELAND_PROTOBUF_SOURCE}/third_party/utf8_range"
        "${IHOMELAND_ABSEIL_SOURCE}"
)
target_link_libraries(ihomeland_battle_protocol
    PUBLIC protobuf::libprotobuf-lite
    PUBLIC utf8_validity
)
target_compile_features(ihomeland_battle_protocol PUBLIC cxx_std_20)
target_compile_options(ihomeland_battle_protocol PRIVATE /utf-8)

set(IHOMELAND_CONTROL_BUILD_IDENTITY "" CACHE STRING
    "Exact target identity embedded in the private control child")
string(LENGTH "${IHOMELAND_CONTROL_BUILD_IDENTITY}"
    IHOMELAND_CONTROL_BUILD_IDENTITY_LENGTH)
if(NOT IHOMELAND_CONTROL_BUILD_IDENTITY_LENGTH EQUAL 64 OR
   NOT IHOMELAND_CONTROL_BUILD_IDENTITY MATCHES "^[0-9a-f]+$")
    message(FATAL_ERROR
        "IHOMELAND_CONTROL_BUILD_IDENTITY must be a lowercase SHA-256")
endif()

add_library(ihomeland_sim_core STATIC
    src/core/fixed_tick.cpp
    src/core/navigation_world.cpp
    src/core/sha256.cpp
    src/ecs/entity_registry.cpp
    src/ecs/simulation_world.cpp
    src/ecs/structural_commands.cpp
    src/gameplay/ability.cpp
    src/gameplay/ai.cpp
    src/gameplay/effects.cpp
    src/gameplay/hit_detection.cpp
    src/gameplay/movement.cpp
    src/gameplay/pipeline.cpp
    src/gameplay/projection.cpp
    src/history/history_ring.cpp
    src/simulation/command_ingress.cpp
    src/simulation/identity.cpp
    src/simulation/input_timeline.cpp
    src/simulation/simulation_instance.cpp
    src/simulation/tick_clock.cpp
)
add_library(iHomeland::SimulationCore ALIAS ihomeland_sim_core)
target_include_directories(ihomeland_sim_core
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
ihomeland_configure_project_target(ihomeland_sim_core)
target_link_libraries(ihomeland_sim_core PRIVATE bcrypt)

add_library(ihomeland_sim_fixture_adapter STATIC
    src/adapters/fixture_adapter.cpp
    src/adapters/battle_runtime_config.cpp
    src/adapters/canonical_json.cpp
    src/adapters/offline_harness.cpp
    src/adapters/recorded_navigation_world.cpp
    src/adapters/recorded_physics_world.cpp
    src/adapters/replay_evidence.cpp
    src/qualification/reference_benchmark.cpp
)
target_link_libraries(ihomeland_sim_fixture_adapter
    PUBLIC iHomeland::SimulationCore
    PRIVATE nlohmann_json::nlohmann_json
)
ihomeland_configure_project_target(ihomeland_sim_fixture_adapter)

add_library(ihomeland_sim_jolt_adapter STATIC
    src/adapters/jolt_adapter.cpp
    src/adapters/jolt_physics_world.cpp
)
target_link_libraries(ihomeland_sim_jolt_adapter
    PUBLIC iHomeland::SimulationCore
    PRIVATE Jolt
)
ihomeland_configure_project_target(ihomeland_sim_jolt_adapter)

add_library(ihomeland_sim_detour_adapter STATIC
    src/adapters/detour_adapter.cpp
    src/adapters/detour_navigation_world.cpp
)
target_link_libraries(ihomeland_sim_detour_adapter
    PUBLIC iHomeland::SimulationCore
    PRIVATE RecastNavigation::Detour
)
ihomeland_configure_project_target(ihomeland_sim_detour_adapter)

add_library(ihomeland_battle_crypto_adapter STATIC
    src/transport/crypto_provider.cpp
    src/transport/handshake_cookie.cpp
)
add_library(iHomeland::BattleCrypto ALIAS ihomeland_battle_crypto_adapter)
target_include_directories(ihomeland_battle_crypto_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(ihomeland_battle_crypto_adapter
    PRIVATE iHomeland::Sodium
)
ihomeland_configure_project_target(ihomeland_battle_crypto_adapter)

add_library(ihomeland_battle_handshake_adapter STATIC
    src/transport/authenticated_handshake.cpp
)
add_library(iHomeland::BattleHandshake ALIAS
    ihomeland_battle_handshake_adapter)
target_include_directories(ihomeland_battle_handshake_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(ihomeland_battle_handshake_adapter
    PUBLIC iHomeland::BattleCrypto
)
ihomeland_configure_project_target(
    ihomeland_battle_handshake_adapter)

add_library(ihomeland_battle_secure_datagram_adapter STATIC
    src/transport/secure_datagram.cpp
)
add_library(iHomeland::BattleSecureDatagram ALIAS
    ihomeland_battle_secure_datagram_adapter)
target_include_directories(
    ihomeland_battle_secure_datagram_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_secure_datagram_adapter
    PUBLIC iHomeland::BattleCrypto)
ihomeland_configure_project_target(
    ihomeland_battle_secure_datagram_adapter)

add_library(ihomeland_battle_udp_listener_adapter STATIC
    src/transport/udp_listener.cpp
)
add_library(iHomeland::BattleUdpListener ALIAS
    ihomeland_battle_udp_listener_adapter)
target_include_directories(
    ihomeland_battle_udp_listener_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_udp_listener_adapter
    PUBLIC iHomeland::BattleCrypto
    PRIVATE iHomeland::Asio)
ihomeland_configure_project_target(
    ihomeland_battle_udp_listener_adapter)

add_library(ihomeland_battle_route_adapter STATIC
    src/transport/battle_route.cpp
)
add_library(iHomeland::BattleRoute ALIAS
    ihomeland_battle_route_adapter)
target_include_directories(
    ihomeland_battle_route_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_route_adapter
    PUBLIC
        iHomeland::BattleProtocol
        iHomeland::BattleSecureDatagram)
ihomeland_configure_project_target(
    ihomeland_battle_route_adapter)

add_library(ihomeland_battle_kcp_adapter STATIC
    src/transport/kcp_adapter.cpp
)
add_library(iHomeland::BattleKcp ALIAS
    ihomeland_battle_kcp_adapter)
target_include_directories(
    ihomeland_battle_kcp_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_kcp_adapter
    PUBLIC
        iHomeland::BattleProtocol
        iHomeland::BattleRoute
    PRIVATE iHomeland::Kcp)
ihomeland_configure_project_target(
    ihomeland_battle_kcp_adapter)

add_library(ihomeland_battle_multiplexer_adapter STATIC
    src/transport/authenticated_multiplexer.cpp
)
add_library(iHomeland::BattleMultiplexer ALIAS
    ihomeland_battle_multiplexer_adapter)
target_include_directories(
    ihomeland_battle_multiplexer_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_multiplexer_adapter
    PUBLIC
        iHomeland::BattleRoute
        iHomeland::BattleKcp)
ihomeland_configure_project_target(
    ihomeland_battle_multiplexer_adapter)

add_library(ihomeland_battle_resource_adapter STATIC
    src/transport/resource_governor.cpp
)
add_library(iHomeland::BattleResource ALIAS
    ihomeland_battle_resource_adapter)
target_include_directories(
    ihomeland_battle_resource_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_resource_adapter
    PUBLIC iHomeland::BattleCrypto)
ihomeland_configure_project_target(
    ihomeland_battle_resource_adapter)

add_library(ihomeland_battle_rebind_adapter STATIC
    src/transport/endpoint_rebind.cpp
)
add_library(iHomeland::BattleRebind ALIAS
    ihomeland_battle_rebind_adapter)
target_include_directories(
    ihomeland_battle_rebind_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_rebind_adapter
    PUBLIC iHomeland::BattleSecureDatagram)
ihomeland_configure_project_target(
    ihomeland_battle_rebind_adapter)

add_library(ihomeland_battle_transport_control_adapter STATIC
    src/transport/battle_transport_control.cpp
)
add_library(iHomeland::BattleTransportControl ALIAS
    ihomeland_battle_transport_control_adapter)
target_include_directories(
    ihomeland_battle_transport_control_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_transport_control_adapter
    PUBLIC
        iHomeland::BattleRebind
        iHomeland::BattleUdpListener)
ihomeland_configure_project_target(
    ihomeland_battle_transport_control_adapter)

add_library(ihomeland_battle_session_adapter STATIC
    src/transport/battle_session.cpp
)
add_library(iHomeland::BattleSession ALIAS
    ihomeland_battle_session_adapter)
target_include_directories(
    ihomeland_battle_session_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_session_adapter
    PUBLIC
        iHomeland::SimulationCore
        iHomeland::BattleProtocol
        iHomeland::BattleRoute
        iHomeland::BattleResource)
ihomeland_configure_project_target(
    ihomeland_battle_session_adapter)

add_library(ihomeland_battle_transport_runtime_adapter STATIC
    src/transport/battle_transport_runtime.cpp
)
add_library(iHomeland::BattleTransportRuntime ALIAS
    ihomeland_battle_transport_runtime_adapter)
target_include_directories(
    ihomeland_battle_transport_runtime_adapter
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_transport_runtime_adapter
    PUBLIC
        iHomeland::BattleHandshake
        iHomeland::BattleMultiplexer
        iHomeland::BattleResource
        iHomeland::BattleRebind
        iHomeland::BattleTransportControl
        iHomeland::BattleSession
        iHomeland::BattleUdpListener)
ihomeland_configure_project_target(
    ihomeland_battle_transport_runtime_adapter)

add_library(ihomeland_sim_control_adapter STATIC
    src/control/control_frame.cpp
    src/control/control_server.cpp
    src/control/simulation_node.cpp
)
add_library(iHomeland::SimulationControl ALIAS ihomeland_sim_control_adapter)
target_link_libraries(ihomeland_sim_control_adapter
    PUBLIC iHomeland::SimulationCore
    PRIVATE
        iHomeland::BattleTransportRuntime
        iHomeland::BattleUdpListener
        ihomeland_sim_jolt_adapter
        ihomeland_sim_detour_adapter
        nlohmann_json::nlohmann_json
        bcrypt
)
ihomeland_configure_project_target(ihomeland_sim_control_adapter)

add_executable(ihomeland-sim-server
    apps/sim_server/main.cpp
)
target_link_libraries(ihomeland-sim-server
    PRIVATE
        iHomeland::SimulationControl
        ihomeland_sim_fixture_adapter
        ihomeland_sim_jolt_adapter
        ihomeland_sim_detour_adapter
)
target_compile_definitions(ihomeland-sim-server PRIVATE
    "IHOMELAND_CONTROL_BUILD_IDENTITY=\"${IHOMELAND_CONTROL_BUILD_IDENTITY}\""
    "IHOMELAND_CONTROL_MODEL_MANIFEST=\"65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1\""
    "IHOMELAND_CONTROL_PROFILE_MANIFEST=\"c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424\""
)
ihomeland_configure_project_target(ihomeland-sim-server)

add_executable(ihomeland-sim-replay
    apps/replay/main.cpp
)
target_link_libraries(ihomeland-sim-replay PRIVATE ihomeland_sim_fixture_adapter)
target_compile_definitions(ihomeland-sim-replay PRIVATE
    "IHOMELAND_MODEL_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/model\""
)
ihomeland_configure_project_target(ihomeland-sim-replay)

add_executable(ihomeland-sim-benchmark
    apps/benchmark/main.cpp
)
target_link_libraries(ihomeland-sim-benchmark PRIVATE ihomeland_sim_fixture_adapter)
target_compile_definitions(ihomeland-sim-benchmark PRIVATE
    "IHOMELAND_REFERENCE_BENCHMARK_BUILD=$<STREQUAL:$<CONFIG>,Release>"
)
ihomeland_configure_project_target(ihomeland-sim-benchmark)

add_executable(ihomeland-sim-qualification
    apps/qualification/main.cpp
)
target_link_libraries(ihomeland-sim-qualification
    PRIVATE ihomeland_sim_fixture_adapter nlohmann_json::nlohmann_json
)
target_compile_definitions(ihomeland-sim-qualification PRIVATE
    "IHOMELAND_ASAN_BUILD=$<BOOL:${IHOMELAND_ENABLE_ASAN}>"
    "IHOMELAND_BUILD_MANIFEST_PATH=\"${CMAKE_BINARY_DIR}/ihomeland-build-manifest.json\""
    "IHOMELAND_BUILD_IDENTITY_PATH=\"${CMAKE_BINARY_DIR}/ihomeland-build-identity.json\""
    "IHOMELAND_ASAN_BUILD_IDENTITY_PATH=\"${CMAKE_BINARY_DIR}/../windows-msvc-asan/ihomeland-build-identity.json\""
    "IHOMELAND_QUALIFICATION_GATE_PATH=\"${CMAKE_BINARY_DIR}/qualification-gate-receipt.json\""
    "IHOMELAND_MODEL_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/model\""
    "IHOMELAND_PROFILE_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/network-profile\""
    "IHOMELAND_REPORT_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/reports\""
)
ihomeland_configure_project_target(ihomeland-sim-qualification)

add_library(ihomeland_battle_protocol_client_core OBJECT
    src/qualification/battle_protocol_handshake.cpp
    src/qualification/battle_protocol_secure.cpp
    src/qualification/battle_protocol_raw.cpp
    src/qualification/battle_protocol_kcp.cpp
)
target_include_directories(
    ihomeland_battle_protocol_client_core
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>"
)
target_link_libraries(
    ihomeland_battle_protocol_client_core
    PRIVATE
        iHomeland::BattleCrypto
        iHomeland::BattleProtocol
        iHomeland::Kcp
)
ihomeland_configure_project_target(
    ihomeland_battle_protocol_client_core)

add_executable(ihomeland-battle-protocol-client
    apps/battle_protocol_client/main.cpp
    $<TARGET_OBJECTS:ihomeland_battle_protocol_client_core>
)
target_link_libraries(
    ihomeland-battle-protocol-client
    PRIVATE
        iHomeland::BattleCrypto
        iHomeland::BattleProtocol
        iHomeland::Kcp
        ws2_32
)
target_compile_definitions(
    ihomeland-battle-protocol-client
    PRIVATE
        "IHOMELAND_PROTOCOL_CLIENT_BUILD_IDENTITY=\"${IHOMELAND_CONTROL_BUILD_IDENTITY}\""
)
ihomeland_configure_project_target(
    ihomeland-battle-protocol-client)

# 资格客户端必须保持独立实现；该 closed gate 防止后续便捷链接悄然引入服务端
# transport、simulation 或 gameplay adapter。
get_target_property(
    IHOMELAND_PROTOCOL_CLIENT_LINK_LIBRARIES
    ihomeland-battle-protocol-client
    LINK_LIBRARIES)
get_target_property(
    IHOMELAND_PROTOCOL_CLIENT_CORE_LINK_LIBRARIES
    ihomeland_battle_protocol_client_core
    LINK_LIBRARIES)
set(IHOMELAND_PROTOCOL_CLIENT_ALLOWED_LINK_LIBRARIES
    "iHomeland::BattleCrypto;iHomeland::BattleProtocol;iHomeland::Kcp;ws2_32")
set(IHOMELAND_PROTOCOL_CLIENT_CORE_ALLOWED_LINK_LIBRARIES
    "iHomeland::BattleCrypto;iHomeland::BattleProtocol;iHomeland::Kcp")
if(NOT IHOMELAND_PROTOCOL_CLIENT_LINK_LIBRARIES STREQUAL
       IHOMELAND_PROTOCOL_CLIENT_ALLOWED_LINK_LIBRARIES OR
   NOT IHOMELAND_PROTOCOL_CLIENT_CORE_LINK_LIBRARIES STREQUAL
       IHOMELAND_PROTOCOL_CLIENT_CORE_ALLOWED_LINK_LIBRARIES)
    message(FATAL_ERROR
        "ihomeland-battle-protocol-client link scope 越过 qualification primitive allowlist")
endif()

if(BUILD_TESTING)
    add_executable(
        ihomeland_battle_protocol_client_test
        tests/battle_protocol_client_test.cpp
        $<TARGET_OBJECTS:ihomeland_battle_protocol_client_core>
    )
    target_link_libraries(
        ihomeland_battle_protocol_client_test
        PRIVATE
            iHomeland::BattleCrypto
            iHomeland::BattleProtocol
            iHomeland::Kcp
            iHomeland::BattleKcp
            iHomeland::BattleSecureDatagram
            iHomeland::BattleTransportRuntime
            iHomeland::SimulationControl
    )
    ihomeland_configure_project_target(
        ihomeland_battle_protocol_client_test)
    add_test(
        NAME battle.qualification.protocol-client
        COMMAND ihomeland_battle_protocol_client_test)
    set_tests_properties(
        battle.qualification.protocol-client
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;qualification;crypto;negative;architecture")

    add_executable(ihomeland_battle_protocol_test tests/battle_protocol_test.cpp)
    target_link_libraries(ihomeland_battle_protocol_test
        PRIVATE iHomeland::BattleProtocol nlohmann_json::nlohmann_json)
    target_compile_definitions(ihomeland_battle_protocol_test PRIVATE
        "IHOMELAND_BATTLE_WIRE_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/wire\"")
    ihomeland_configure_project_target(ihomeland_battle_protocol_test)
    add_test(NAME battle.protocol.lite-round-trip COMMAND ihomeland_battle_protocol_test)
    set_tests_properties(battle.protocol.lite-round-trip PROPERTIES LABELS "unit;contract;battle")

    add_executable(ihomeland_battle_crypto_test tests/battle_crypto_test.cpp)
    target_link_libraries(ihomeland_battle_crypto_test
        PRIVATE iHomeland::BattleCrypto nlohmann_json::nlohmann_json)
    target_compile_definitions(ihomeland_battle_crypto_test PRIVATE
        "IHOMELAND_BATTLE_CRYPTO_VECTORS=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/wire/crypto-vectors.json\"")
    ihomeland_configure_project_target(ihomeland_battle_crypto_test)
    add_test(NAME battle.crypto.rfc-parity COMMAND ihomeland_battle_crypto_test)
    set_tests_properties(battle.crypto.rfc-parity PROPERTIES LABELS "unit;contract;battle;crypto;negative")

    add_executable(ihomeland_battle_cookie_test tests/battle_cookie_test.cpp)
    target_link_libraries(ihomeland_battle_cookie_test
        PRIVATE iHomeland::BattleCrypto)
    ihomeland_configure_project_target(ihomeland_battle_cookie_test)
    add_test(NAME battle.handshake.cookie COMMAND ihomeland_battle_cookie_test)
    set_tests_properties(battle.handshake.cookie PROPERTIES LABELS "unit;contract;battle;crypto;negative")

    add_executable(ihomeland_battle_authenticated_handshake_test
        tests/battle_authenticated_handshake_test.cpp)
    target_link_libraries(
        ihomeland_battle_authenticated_handshake_test
        PRIVATE
            iHomeland::BattleHandshake
            iHomeland::SimulationControl)
    ihomeland_configure_project_target(
        ihomeland_battle_authenticated_handshake_test)
    add_test(
        NAME battle.handshake.authenticated
        COMMAND ihomeland_battle_authenticated_handshake_test)
    set_tests_properties(
        battle.handshake.authenticated
        PROPERTIES LABELS
        "unit;contract;integration;battle;crypto;negative")

    add_executable(ihomeland_battle_secure_datagram_test
        tests/battle_secure_datagram_test.cpp)
    target_link_libraries(
        ihomeland_battle_secure_datagram_test
        PRIVATE iHomeland::BattleSecureDatagram)
    ihomeland_configure_project_target(
        ihomeland_battle_secure_datagram_test)
    add_test(
        NAME battle.crypto.secure-datagram
        COMMAND ihomeland_battle_secure_datagram_test)
    set_tests_properties(
        battle.crypto.secure-datagram
        PROPERTIES LABELS
        "unit;contract;integration;battle;crypto;negative;replay")

    add_executable(ihomeland_battle_udp_listener_test
        tests/battle_udp_listener_test.cpp)
    target_link_libraries(
        ihomeland_battle_udp_listener_test
        PRIVATE
            iHomeland::BattleUdpListener
            iHomeland::SimulationControl
            iHomeland::Asio)
    ihomeland_configure_project_target(
        ihomeland_battle_udp_listener_test)
    add_test(
        NAME battle.transport.udp-listener
        COMMAND ihomeland_battle_udp_listener_test)
    set_tests_properties(
        battle.transport.udp-listener
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;negative;architecture")

    add_executable(ihomeland_battle_raw_dispatch_test
        tests/battle_raw_dispatch_test.cpp)
    target_link_libraries(
        ihomeland_battle_raw_dispatch_test
        PRIVATE iHomeland::BattleMultiplexer)
    ihomeland_configure_project_target(
        ihomeland_battle_raw_dispatch_test)
    add_test(
        NAME battle.transport.raw-dispatch
        COMMAND ihomeland_battle_raw_dispatch_test)
    set_tests_properties(
        battle.transport.raw-dispatch
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;negative;replay")

    add_executable(ihomeland_battle_kcp_adapter_test
        tests/battle_kcp_adapter_test.cpp)
    target_link_libraries(
        ihomeland_battle_kcp_adapter_test
        PRIVATE iHomeland::BattleKcp)
    ihomeland_configure_project_target(
        ihomeland_battle_kcp_adapter_test)
    add_test(
        NAME battle.transport.kcp-adapter
        COMMAND ihomeland_battle_kcp_adapter_test)
    set_tests_properties(
        battle.transport.kcp-adapter
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;negative;replay")

    add_executable(ihomeland_battle_resource_governor_test
        tests/battle_resource_governor_test.cpp)
    target_link_libraries(
        ihomeland_battle_resource_governor_test
        PRIVATE iHomeland::BattleResource)
    ihomeland_configure_project_target(
        ihomeland_battle_resource_governor_test)
    add_test(
        NAME battle.transport.resource-governor
        COMMAND ihomeland_battle_resource_governor_test)
    set_tests_properties(
        battle.transport.resource-governor
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;negative;backpressure")

    add_executable(ihomeland_battle_runtime_metrics_test
        tests/battle_runtime_metrics_test.cpp)
    target_link_libraries(
        ihomeland_battle_runtime_metrics_test
        PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(
        ihomeland_battle_runtime_metrics_test)
    add_test(
        NAME battle.observability.runtime-metrics
        COMMAND ihomeland_battle_runtime_metrics_test)
    set_tests_properties(
        battle.observability.runtime-metrics
        PROPERTIES LABELS
        "unit;contract;battle;network;qualification;concurrency;negative")

    add_executable(ihomeland_battle_endpoint_rebind_test
        tests/battle_endpoint_rebind_test.cpp)
    target_link_libraries(
        ihomeland_battle_endpoint_rebind_test
        PRIVATE iHomeland::BattleRebind)
    ihomeland_configure_project_target(
        ihomeland_battle_endpoint_rebind_test)
    add_test(
        NAME battle.transport.endpoint-rebind
        COMMAND ihomeland_battle_endpoint_rebind_test)
    set_tests_properties(
        battle.transport.endpoint-rebind
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;negative;replay")

    add_executable(ihomeland_battle_transport_control_test
        tests/battle_transport_control_test.cpp)
    target_link_libraries(
        ihomeland_battle_transport_control_test
        PRIVATE iHomeland::BattleTransportControl)
    ihomeland_configure_project_target(
        ihomeland_battle_transport_control_test)
    add_test(
        NAME battle.transport.control
        COMMAND ihomeland_battle_transport_control_test)
    set_tests_properties(
        battle.transport.control
        PROPERTIES LABELS
        "unit;contract;integration;battle;network;crypto;negative;replay")

    add_executable(ihomeland_battle_session_test
        tests/battle_session_test.cpp)
    target_link_libraries(
        ihomeland_battle_session_test
        PRIVATE iHomeland::BattleSession)
    ihomeland_configure_project_target(
        ihomeland_battle_session_test)
    add_test(
        NAME battle.session.ingress-replication
        COMMAND ihomeland_battle_session_test)
    set_tests_properties(
        battle.session.ingress-replication
        PROPERTIES LABELS
        "unit;contract;integration;battle;simulation;negative;backpressure")

    add_executable(ihomeland_sim_core_smoke_test tests/core_smoke_test.cpp)
    target_link_libraries(ihomeland_sim_core_smoke_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_core_smoke_test)
    add_test(NAME simulation.core.smoke COMMAND ihomeland_sim_core_smoke_test)
    set_tests_properties(simulation.core.smoke PROPERTIES LABELS "unit;smoke")

    add_executable(ihomeland_sim_ecs_test tests/ecs_test.cpp)
    target_link_libraries(ihomeland_sim_ecs_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_ecs_test)
    add_test(NAME simulation.ecs.registry-storage COMMAND ihomeland_sim_ecs_test)
    set_tests_properties(simulation.ecs.registry-storage PROPERTIES LABELS "unit;negative")

    add_executable(ihomeland_sim_ecs_structural_test tests/ecs_structural_test.cpp)
    target_link_libraries(ihomeland_sim_ecs_structural_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_ecs_structural_test)
    add_test(NAME simulation.ecs.structural COMMAND ihomeland_sim_ecs_structural_test)
    set_tests_properties(simulation.ecs.structural PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_instance_test tests/simulation_instance_test.cpp)
    target_link_libraries(ihomeland_sim_instance_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_instance_test)
    add_test(NAME simulation.instance.lifecycle COMMAND ihomeland_sim_instance_test)
    set_tests_properties(simulation.instance.lifecycle PROPERTIES LABELS "unit;negative;integration;comprehensive")

    add_executable(ihomeland_sim_gameplay_pipeline_test tests/gameplay_pipeline_test.cpp)
    target_link_libraries(ihomeland_sim_gameplay_pipeline_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_gameplay_pipeline_test)
    add_test(NAME simulation.gameplay.pipeline COMMAND ihomeland_sim_gameplay_pipeline_test)
    set_tests_properties(simulation.gameplay.pipeline PROPERTIES LABELS "unit;negative;architecture")

    add_executable(ihomeland_sim_movement_test tests/movement_test.cpp)
    target_link_libraries(ihomeland_sim_movement_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_movement_test)
    add_test(NAME simulation.gameplay.movement COMMAND ihomeland_sim_movement_test)
    set_tests_properties(simulation.gameplay.movement PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_ability_test tests/ability_test.cpp)
    target_link_libraries(ihomeland_sim_ability_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_ability_test)
    add_test(NAME simulation.gameplay.ability COMMAND ihomeland_sim_ability_test)
    set_tests_properties(simulation.gameplay.ability PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_hit_detection_test tests/hit_detection_test.cpp)
    target_link_libraries(ihomeland_sim_hit_detection_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_hit_detection_test)
    add_test(NAME simulation.gameplay.hit-detection COMMAND ihomeland_sim_hit_detection_test)
    set_tests_properties(simulation.gameplay.hit-detection PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_effects_test tests/effects_test.cpp)
    target_link_libraries(ihomeland_sim_effects_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_effects_test)
    add_test(NAME simulation.gameplay.effects-attributes-death COMMAND ihomeland_sim_effects_test)
    set_tests_properties(simulation.gameplay.effects-attributes-death PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_ai_test tests/ai_test.cpp)
    target_link_libraries(ihomeland_sim_ai_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_ai_test)
    add_test(NAME simulation.gameplay.ai COMMAND ihomeland_sim_ai_test)
    set_tests_properties(simulation.gameplay.ai PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_projection_test tests/projection_test.cpp)
    target_link_libraries(ihomeland_sim_projection_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_projection_test)
    add_test(NAME simulation.gameplay.projection COMMAND ihomeland_sim_projection_test)
    set_tests_properties(simulation.gameplay.projection PROPERTIES LABELS "unit;determinism;contract")

    add_executable(ihomeland_sim_history_test tests/history_ring_test.cpp)
    target_link_libraries(ihomeland_sim_history_test PRIVATE iHomeland::SimulationCore)
    ihomeland_configure_project_target(ihomeland_sim_history_test)
    add_test(NAME simulation.history.ring-query COMMAND ihomeland_sim_history_test)
    set_tests_properties(simulation.history.ring-query PROPERTIES LABELS "unit;negative;determinism")

    add_executable(ihomeland_sim_config_test tests/config_test.cpp)
    target_link_libraries(ihomeland_sim_config_test PRIVATE ihomeland_sim_fixture_adapter)
    target_compile_definitions(ihomeland_sim_config_test PRIVATE
        "IHOMELAND_PROFILE_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/network-profile\""
        "IHOMELAND_MODEL_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/model\""
    )
    ihomeland_configure_project_target(ihomeland_sim_config_test)
    add_test(NAME simulation.config.profile COMMAND ihomeland_sim_config_test)
    set_tests_properties(simulation.config.profile PROPERTIES LABELS "contract;unit")

    add_executable(ihomeland_sim_fixture_contract_test tests/fixture_contract_test.cpp)
    target_link_libraries(ihomeland_sim_fixture_contract_test
        PRIVATE ihomeland_sim_fixture_adapter nlohmann_json::nlohmann_json
    )
    target_compile_definitions(ihomeland_sim_fixture_contract_test PRIVATE
        "IHOMELAND_PROFILE_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/network-profile\""
        "IHOMELAND_MODEL_ROOT=\"${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/model\""
    )
    ihomeland_configure_project_target(ihomeland_sim_fixture_contract_test)
    add_test(NAME simulation.fixture.contract COMMAND ihomeland_sim_fixture_contract_test)
    set_tests_properties(simulation.fixture.contract PROPERTIES LABELS "contract;negative;determinism")

    add_executable(ihomeland_sim_recorded_physics_test tests/recorded_physics_world_test.cpp)
    target_link_libraries(ihomeland_sim_recorded_physics_test PRIVATE ihomeland_sim_fixture_adapter)
    ihomeland_configure_project_target(ihomeland_sim_recorded_physics_test)
    add_test(NAME simulation.physics.recorded COMMAND ihomeland_sim_recorded_physics_test)
    set_tests_properties(simulation.physics.recorded PROPERTIES LABELS "contract;negative;determinism")

    add_executable(ihomeland_sim_recorded_navigation_test tests/recorded_navigation_world_test.cpp)
    target_link_libraries(ihomeland_sim_recorded_navigation_test PRIVATE ihomeland_sim_fixture_adapter)
    ihomeland_configure_project_target(ihomeland_sim_recorded_navigation_test)
    add_test(NAME simulation.navigation.recorded COMMAND ihomeland_sim_recorded_navigation_test)
    set_tests_properties(simulation.navigation.recorded PROPERTIES LABELS "contract;negative;determinism")

    add_executable(ihomeland_sim_replay_evidence_test tests/replay_evidence_test.cpp)
    target_link_libraries(ihomeland_sim_replay_evidence_test
        PRIVATE ihomeland_sim_fixture_adapter nlohmann_json::nlohmann_json
    )
    ihomeland_configure_project_target(ihomeland_sim_replay_evidence_test)
    add_test(NAME simulation.evidence.replay COMMAND ihomeland_sim_replay_evidence_test)
    set_tests_properties(simulation.evidence.replay PROPERTIES LABELS "contract;negative;determinism")

    add_executable(ihomeland_sim_adapter_smoke_test tests/adapter_smoke_test.cpp)
    target_link_libraries(ihomeland_sim_adapter_smoke_test
        PRIVATE
            ihomeland_sim_fixture_adapter
            ihomeland_sim_jolt_adapter
            ihomeland_sim_detour_adapter
    )
    ihomeland_configure_project_target(ihomeland_sim_adapter_smoke_test)
    add_test(NAME simulation.adapters.smoke COMMAND ihomeland_sim_adapter_smoke_test)
    set_tests_properties(simulation.adapters.smoke PROPERTIES LABELS "contract;smoke")

    add_executable(ihomeland_sim_jolt_physics_test tests/jolt_physics_world_test.cpp)
    target_link_libraries(ihomeland_sim_jolt_physics_test
        PRIVATE ihomeland_sim_jolt_adapter ihomeland_sim_fixture_adapter
    )
    ihomeland_configure_project_target(ihomeland_sim_jolt_physics_test)
    add_test(NAME simulation.physics.jolt-live COMMAND ihomeland_sim_jolt_physics_test)
    set_tests_properties(simulation.physics.jolt-live PROPERTIES LABELS "contract;negative;determinism;jolt-parity")

    add_executable(ihomeland_sim_detour_navigation_test tests/detour_navigation_world_test.cpp)
    target_link_libraries(ihomeland_sim_detour_navigation_test
        PRIVATE ihomeland_sim_detour_adapter ihomeland_sim_fixture_adapter RecastNavigation::Detour
    )
    ihomeland_configure_project_target(ihomeland_sim_detour_navigation_test)
    add_test(NAME simulation.navigation.detour-live COMMAND ihomeland_sim_detour_navigation_test)
    set_tests_properties(simulation.navigation.detour-live PROPERTIES LABELS "contract;negative;determinism;detour-parity")

    add_executable(ihomeland_sim_control_frame_test tests/control_frame_test.cpp)
    target_link_libraries(
        ihomeland_sim_control_frame_test
        PRIVATE iHomeland::SimulationControl nlohmann_json::nlohmann_json)
    target_compile_definitions(
        ihomeland_sim_control_frame_test
        PRIVATE IHOMELAND_SIMULATION_CONTROL_FIXTURE_ROOT="${CMAKE_SOURCE_DIR}/../shared/contracts/fixtures/simulation-control")
    ihomeland_configure_project_target(ihomeland_sim_control_frame_test)
    add_test(NAME simulation.control.frame COMMAND ihomeland_sim_control_frame_test)
    set_tests_properties(simulation.control.frame PROPERTIES LABELS "unit;contract;negative")

    add_executable(ihomeland_sim_control_node_test tests/simulation_node_test.cpp)
    target_link_libraries(ihomeland_sim_control_node_test PRIVATE iHomeland::SimulationControl)
    ihomeland_configure_project_target(ihomeland_sim_control_node_test)
    add_test(NAME simulation.control.node COMMAND ihomeland_sim_control_node_test)
    set_tests_properties(simulation.control.node PROPERTIES LABELS "unit;integration;negative;determinism")

    add_executable(ihomeland_sim_control_stdio_test tests/control_stdio_test.cpp)
    target_link_libraries(ihomeland_sim_control_stdio_test PRIVATE iHomeland::SimulationControl)
    ihomeland_configure_project_target(ihomeland_sim_control_stdio_test)
    add_test(NAME simulation.control.stdio COMMAND ihomeland_sim_control_stdio_test)
    set_tests_properties(simulation.control.stdio PROPERTIES LABELS "contract;integration;negative")

    add_test(
        NAME simulation.boundary.public_headers
        COMMAND "${CMAKE_COMMAND}"
            "-DIHOMELAND_PUBLIC_HEADER_ROOT=${CMAKE_CURRENT_SOURCE_DIR}/include/ihomeland/sim"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/CheckPublicHeaders.cmake"
    )
    set_tests_properties(simulation.boundary.public_headers PROPERTIES LABELS "architecture;contract")

    add_test(
        NAME simulation.boundary.scope
        COMMAND "${CMAKE_COMMAND}"
            "-DIHOMELAND_SIMULATION_ROOT=${CMAKE_CURRENT_SOURCE_DIR}"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/CheckScopeBoundary.cmake"
    )
    set_tests_properties(simulation.boundary.scope PROPERTIES LABELS "architecture;negative;contract")

    add_test(NAME simulation.server.smoke COMMAND ihomeland-sim-server --smoke)
    set_tests_properties(simulation.server.smoke PROPERTIES LABELS "integration;smoke")

    add_test(NAME simulation.replay.tool COMMAND ihomeland-sim-replay --self-test)
    set_tests_properties(simulation.replay.tool PROPERTIES LABELS "integration;determinism;negative;replay")

    if(CMAKE_BUILD_TYPE STREQUAL "Release")
        add_test(NAME simulation.benchmark.reference COMMAND ihomeland-sim-benchmark --reference)
        set_tests_properties(
            simulation.benchmark.reference
            PROPERTIES LABELS "benchmark;qualification;windows-x64"
        )
    else()
        add_test(NAME simulation.benchmark.smoke COMMAND ihomeland-sim-benchmark --smoke)
        set_tests_properties(
            simulation.benchmark.smoke
            PROPERTIES LABELS "benchmark-smoke"
        )
    endif()

    if(NOT CMAKE_BUILD_TYPE STREQUAL "Release")
        add_test(
            NAME simulation.qualification.reject-nonrelease
            COMMAND ihomeland-sim-qualification --emit-qualified
        )
        set_tests_properties(
            simulation.qualification.reject-nonrelease
            PROPERTIES
                LABELS "architecture;negative"
                WILL_FAIL TRUE
        )
    endif()

    add_test(
        NAME simulation.qualification.labels
        COMMAND "${CMAKE_COMMAND}"
            "-DIHOMELAND_CTEST_FILE=${CMAKE_CURRENT_BINARY_DIR}/CTestTestfile.cmake"
            "-DIHOMELAND_EXPECT_ASAN=${IHOMELAND_ENABLE_ASAN}"
            "-DIHOMELAND_EXPECT_QUALIFICATION=$<STREQUAL:${CMAKE_BUILD_TYPE},Release>"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/CheckQualificationLabels.cmake"
    )
    if(IHOMELAND_ENABLE_ASAN)
        set_tests_properties(simulation.qualification.labels PROPERTIES LABELS "architecture;qualification;asan")
    else()
        set_tests_properties(simulation.qualification.labels PROPERTIES LABELS "architecture;qualification")
    endif()

    add_test(
        NAME simulation.quality.cpp-comments
        COMMAND "${CMAKE_COMMAND}"
            "-DIHOMELAND_SIMULATION_ROOT=${CMAKE_CURRENT_SOURCE_DIR}"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/CheckCppComments.cmake"
    )
    set_tests_properties(simulation.quality.cpp-comments PROPERTIES LABELS "architecture;quality;comments")

    add_test(
        NAME simulation.model.cases
        COMMAND "${CMAKE_COMMAND}"
            "-DIHOMELAND_MODEL_ROOT=${CMAKE_CURRENT_SOURCE_DIR}/../shared/contracts/fixtures/battle/model"
            "-DIHOMELAND_CTEST=${CMAKE_CTEST_COMMAND}"
            "-DIHOMELAND_BUILD_ROOT=${CMAKE_CURRENT_BINARY_DIR}"
            -P "${CMAKE_CURRENT_SOURCE_DIR}/cmake/RunFrozenModelCases.cmake"
    )
    set_tests_properties(simulation.model.cases PROPERTIES LABELS "contract;integration;determinism;model-cases")
endif()

ihomeland_write_build_manifest()
