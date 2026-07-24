if(NOT DEFINED IHOMELAND_CTEST_FILE OR
   NOT EXISTS "${IHOMELAND_CTEST_FILE}")
    message(FATAL_ERROR "qualification label gate requires generated CTestTestfile.cmake")
endif()

file(READ "${IHOMELAND_CTEST_FILE}" ctest_source)
set(required_labels
    unit
    contract
    integration
    negative
    determinism
    jolt-parity
    detour-parity
    comprehensive
    replay
    model-cases
)
if(IHOMELAND_EXPECT_QUALIFICATION)
    list(APPEND required_labels benchmark qualification windows-x64)
endif()
if(IHOMELAND_EXPECT_ASAN)
    list(APPEND required_labels asan)
endif()
foreach(required_label IN LISTS required_labels)
    string(REGEX MATCH
        "LABELS \"([^\"]*;)?${required_label}(;[^\"]*)?\""
        label_registration
        "${ctest_source}"
    )
    if(label_registration STREQUAL "")
        message(FATAL_ERROR "qualification CTest label missing: ${required_label}")
    endif()
endforeach()

if(ctest_source MATCHES "SKIP_RETURN_CODE|DISABLED[ \t]+TRUE")
    message(FATAL_ERROR "qualification suite contains skipped or disabled tests")
endif()

message(STATUS "qualification CTest labels are complete and no test is skipped/disabled")
