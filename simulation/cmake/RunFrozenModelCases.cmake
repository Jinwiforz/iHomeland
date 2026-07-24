if(NOT DEFINED IHOMELAND_MODEL_ROOT OR
   NOT IS_DIRECTORY "${IHOMELAND_MODEL_ROOT}" OR
   NOT DEFINED IHOMELAND_CTEST OR
   NOT EXISTS "${IHOMELAND_CTEST}" OR
   NOT DEFINED IHOMELAND_BUILD_ROOT OR
   NOT IS_DIRECTORY "${IHOMELAND_BUILD_ROOT}")
    message(FATAL_ERROR "frozen model case runner requires model, CTest and build roots")
endif()

file(READ "${IHOMELAND_MODEL_ROOT}/manifest.json" model_manifest)
string(JSON case_count LENGTH "${model_manifest}" cases)
if(NOT case_count EQUAL 10)
    message(FATAL_ERROR "frozen model case inventory must contain exactly 10 cases")
endif()

math(EXPR last_case_index "${case_count} - 1")
foreach(case_index RANGE 0 ${last_case_index})
    string(JSON case_id GET "${model_manifest}" cases ${case_index} case_id)
    string(JSON relative_path GET "${model_manifest}" cases ${case_index} path)
    string(JSON expected_sha256 GET "${model_manifest}" cases ${case_index} file_sha256)
    set(case_path "${IHOMELAND_MODEL_ROOT}/${relative_path}")
    if(NOT EXISTS "${case_path}")
        message(FATAL_ERROR "frozen model case is missing: ${case_id}")
    endif()
    file(SHA256 "${case_path}" actual_sha256)
    if(NOT actual_sha256 STREQUAL expected_sha256)
        message(FATAL_ERROR "frozen model case digest drifted: ${case_id}")
    endif()

    if(case_id STREQUAL "bounded-capacity-and-tick-debt")
        set(owner_regex "simulation.ecs.structural|simulation.instance.lifecycle")
    elseif(case_id STREQUAL "bounded-lag-compensation")
        set(owner_regex "simulation.history.ring-query")
    elseif(case_id STREQUAL "command-authority-and-order")
        set(owner_regex "simulation.instance.lifecycle")
    elseif(case_id STREQUAL "damage-overflow-config")
        set(owner_regex "simulation.gameplay.effects-attributes-death|simulation.gameplay.movement")
    elseif(case_id STREQUAL "deterministic-target-and-boss")
        set(owner_regex "simulation.gameplay.ai|simulation.navigation.recorded")
    elseif(case_id STREQUAL "effect-damage-death")
        set(owner_regex "simulation.gameplay.effects-attributes-death")
    elseif(case_id STREQUAL "fan-projectile-hit")
        set(owner_regex "simulation.gameplay.ability|simulation.gameplay.hit-detection")
    elseif(case_id STREQUAL "kinematic-jump-and-collision")
        set(owner_regex "simulation.gameplay.movement|simulation.physics.recorded")
    elseif(case_id STREQUAL "tick-mapping-and-generation")
        set(owner_regex "simulation.instance.lifecycle")
    elseif(case_id STREQUAL "weapon-ability-and-sword")
        set(owner_regex "simulation.gameplay.ability|simulation.gameplay.hit-detection|simulation.gameplay.effects-attributes-death")
    else()
        message(FATAL_ERROR "frozen model case has no implementation owner: ${case_id}")
    endif()

    execute_process(
        COMMAND "${IHOMELAND_CTEST}"
            --test-dir "${IHOMELAND_BUILD_ROOT}"
            --output-on-failure
            -R "^(${owner_regex})$"
        RESULT_VARIABLE case_result
        OUTPUT_VARIABLE case_output
        ERROR_VARIABLE case_error
    )
    if(NOT case_result EQUAL 0)
        message(FATAL_ERROR
            "frozen model case failed: ${case_id}\n${case_output}\n${case_error}")
    endif()
    message(STATUS "frozen model case passed: ${case_id}")
endforeach()

message(STATUS "all 10 frozen model cases passed through their implementation owners")
