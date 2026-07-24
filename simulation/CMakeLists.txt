cmake_minimum_required(VERSION 4.4)

project(iHomelandSimulation VERSION 0.1.0 LANGUAGES CXX)

include(CTest)
include(cmake/IHomelandBuild.cmake)
include(cmake/Dependencies.cmake)

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

add_library(ihomeland_sim_control_adapter STATIC
    src/control/control_frame.cpp
    src/control/control_server.cpp
    src/control/simulation_node.cpp
)
add_library(iHomeland::SimulationControl ALIAS ihomeland_sim_control_adapter)
target_link_libraries(ihomeland_sim_control_adapter
    PUBLIC iHomeland::SimulationCore
    PRIVATE
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
    "IHOMELAND_CONTROL_PROFILE_MANIFEST=\"ca8d0b85e2f1b57d2209e4f376a174c89833ff30b7b3dd694d26c17408be341f\""
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

if(BUILD_TESTING)
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
    target_link_libraries(ihomeland_sim_control_frame_test PRIVATE iHomeland::SimulationControl)
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
