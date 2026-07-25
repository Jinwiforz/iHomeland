include_guard(GLOBAL)

set(IHOMELAND_JOLT_SOURCE "" CACHE PATH "锁定的 Jolt Physics source 根。")
set(IHOMELAND_RECAST_SOURCE "" CACHE PATH "锁定的 RecastNavigation source 根。")
set(IHOMELAND_JSON_SOURCE "" CACHE PATH "锁定的 nlohmann/json source 根。")
set(IHOMELAND_ASIO_SOURCE "" CACHE PATH "锁定的 standalone Asio source 根。")
set(IHOMELAND_KCP_SOURCE "" CACHE PATH "锁定的 KCP source 根。")
set(IHOMELAND_LIBSODIUM_SOURCE "" CACHE PATH "锁定的 libsodium source 根。")
set(IHOMELAND_ABSEIL_SOURCE "" CACHE PATH "锁定的 Abseil source 根。")
set(IHOMELAND_PROTOBUF_SOURCE "" CACHE PATH "锁定的 C++ Protobuf source 根。")

foreach(required_path IN ITEMS
    IHOMELAND_JOLT_SOURCE
    IHOMELAND_RECAST_SOURCE
    IHOMELAND_JSON_SOURCE
    IHOMELAND_ASIO_SOURCE
    IHOMELAND_KCP_SOURCE
    IHOMELAND_LIBSODIUM_SOURCE
    IHOMELAND_ABSEIL_SOURCE
    IHOMELAND_PROTOBUF_SOURCE)
    if(NOT IS_DIRECTORY "${${required_path}}")
        message(FATAL_ERROR "${required_path} 必须指向 bootstrap 已验证的本地 source")
    endif()
endforeach()

set(FETCHCONTENT_FULLY_DISCONNECTED ON CACHE BOOL "" FORCE)
set(BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)

# standalone Asio 只通过该 header-only target 暴露给 node-global UDP adapter。
add_library(ihomeland_asio INTERFACE)
add_library(iHomeland::Asio ALIAS ihomeland_asio)
target_include_directories(ihomeland_asio
    SYSTEM INTERFACE
        "${IHOMELAND_ASIO_SOURCE}/include")
target_compile_definitions(ihomeland_asio
    INTERFACE
        ASIO_STANDALONE
        ASIO_NO_DEPRECATED
        _WIN32_WINNT=0x0A00)
target_link_libraries(ihomeland_asio INTERFACE ws2_32)

# KCP 2.1.1 由项目 adapter 私有消费，第三方 handle 不进入公开契约。
add_library(ihomeland_kcp STATIC
    "${IHOMELAND_KCP_SOURCE}/ikcp.c")
add_library(iHomeland::Kcp ALIAS ihomeland_kcp)
target_include_directories(ihomeland_kcp
    SYSTEM PUBLIC
        "${IHOMELAND_KCP_SOURCE}")

set(OVERRIDE_CXX_FLAGS OFF CACHE BOOL "" FORCE)
set(CROSS_PLATFORM_DETERMINISTIC ON CACHE BOOL "" FORCE)
set(INTERPROCEDURAL_OPTIMIZATION OFF CACHE BOOL "" FORCE)
set(FLOATING_POINT_EXCEPTIONS_ENABLED OFF CACHE BOOL "" FORCE)
set(CPP_EXCEPTIONS_ENABLED OFF CACHE BOOL "" FORCE)
set(CPP_RTTI_ENABLED OFF CACHE BOOL "" FORCE)
set(ENABLE_ALL_WARNINGS OFF CACHE BOOL "" FORCE)
set(DEBUG_RENDERER_IN_DEBUG_AND_RELEASE OFF CACHE BOOL "" FORCE)
set(PROFILER_IN_DEBUG_AND_RELEASE OFF CACHE BOOL "" FORCE)
set(ENABLE_OBJECT_STREAM OFF CACHE BOOL "" FORCE)
set(ENABLE_INSTALL OFF CACHE BOOL "" FORCE)
set(USE_STATIC_MSVC_RUNTIME_LIBRARY OFF CACHE BOOL "" FORCE)
add_subdirectory("${IHOMELAND_JOLT_SOURCE}/Build" "${CMAKE_BINARY_DIR}/third_party/jolt" EXCLUDE_FROM_ALL)
if(IHOMELAND_ENABLE_ASAN)
    # Jolt 与调用方必须使用相同的 MSVC ASan/STL annotation ABI，否则链接期会拒绝混用。
    target_compile_options(Jolt PRIVATE /fsanitize=address)
endif()

set(JSON_BuildTests OFF CACHE BOOL "" FORCE)
set(JSON_Install OFF CACHE BOOL "" FORCE)
set(JSON_ImplicitConversions OFF CACHE BOOL "" FORCE)
set(JSON_GlobalUDLs OFF CACHE BOOL "" FORCE)
add_subdirectory("${IHOMELAND_JSON_SOURCE}" "${CMAKE_BINARY_DIR}/third_party/json" EXCLUDE_FROM_ALL)

# libsodium 没有 CMake project；锁定的 MSVC project 是该 release 对 Windows source set 的权威清单。
set(IHOMELAND_LIBSODIUM_MSVC_PROJECT
    "${IHOMELAND_LIBSODIUM_SOURCE}/builds/msvc/vs2022/libsodium/libsodium.vcxproj")
file(READ "${IHOMELAND_LIBSODIUM_MSVC_PROJECT}" IHOMELAND_LIBSODIUM_PROJECT_XML)
string(REGEX MATCHALL
    "<ClCompile Include=\"[^\"]+\""
    IHOMELAND_LIBSODIUM_SOURCE_ENTRIES
    "${IHOMELAND_LIBSODIUM_PROJECT_XML}")
set(IHOMELAND_LIBSODIUM_SOURCES)
foreach(source_entry IN LISTS IHOMELAND_LIBSODIUM_SOURCE_ENTRIES)
    string(REGEX REPLACE
        "^<ClCompile Include=\"\\.\\.\\\\\\.\\.\\\\\\.\\.\\\\\\.\\.\\\\([^\"]+)\"$"
        "\\1"
        source_relative
        "${source_entry}")
    string(REPLACE "\\" "/" source_relative "${source_relative}")
    list(APPEND IHOMELAND_LIBSODIUM_SOURCES
        "${IHOMELAND_LIBSODIUM_SOURCE}/${source_relative}")
endforeach()
if(NOT IHOMELAND_LIBSODIUM_SOURCES)
    message(FATAL_ERROR "锁定的 libsodium MSVC source set 为空")
endif()
set(IHOMELAND_LIBSODIUM_GENERATED_INCLUDE
    "${CMAKE_BINARY_DIR}/third_party/libsodium/include")
file(MAKE_DIRECTORY
    "${IHOMELAND_LIBSODIUM_GENERATED_INCLUDE}/sodium")
configure_file(
    "${IHOMELAND_LIBSODIUM_SOURCE}/builds/msvc/version.h"
    "${IHOMELAND_LIBSODIUM_GENERATED_INCLUDE}/sodium/version.h"
    COPYONLY)
add_library(ihomeland_sodium STATIC ${IHOMELAND_LIBSODIUM_SOURCES})
add_library(iHomeland::Sodium ALIAS ihomeland_sodium)
target_include_directories(ihomeland_sodium
    SYSTEM PUBLIC
        "${IHOMELAND_LIBSODIUM_GENERATED_INCLUDE}"
        "${IHOMELAND_LIBSODIUM_GENERATED_INCLUDE}/sodium"
        "${IHOMELAND_LIBSODIUM_SOURCE}/src/libsodium/include"
        "${IHOMELAND_LIBSODIUM_SOURCE}/src/libsodium/include/sodium")
target_compile_definitions(ihomeland_sodium
    PUBLIC
        SODIUM_STATIC
    PRIVATE
        inline=__inline
        NATIVE_LITTLE_ENDIAN
        _CRT_SECURE_NO_WARNINGS
        _LIB)
target_compile_options(ihomeland_sodium PRIVATE /wd4146 /wd4244)
target_link_libraries(ihomeland_sodium PRIVATE advapi32)

set(ABSL_PROPAGATE_CXX_STD ON CACHE BOOL "" FORCE)
set(ABSL_MSVC_STATIC_RUNTIME OFF CACHE BOOL "" FORCE)
set(ABSL_BUILD_TESTING OFF CACHE BOOL "" FORCE)
set(ABSL_BUILD_TEST_HELPERS OFF CACHE BOOL "" FORCE)
set(ABSL_ENABLE_INSTALL OFF CACHE BOOL "" FORCE)
add_subdirectory("${IHOMELAND_ABSEIL_SOURCE}" "${CMAKE_BINARY_DIR}/third_party/abseil" EXCLUDE_FROM_ALL)

set(protobuf_INSTALL OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_TESTS OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_CONFORMANCE OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_PROTOBUF_BINARIES ON CACHE BOOL "" FORCE)
set(protobuf_BUILD_PROTOC_BINARIES OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_LIBPROTOC OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_LIBUPB OFF CACHE BOOL "" FORCE)
set(protobuf_BUILD_SHARED_LIBS OFF CACHE BOOL "" FORCE)
set(protobuf_MSVC_STATIC_RUNTIME OFF CACHE BOOL "" FORCE)
set(protobuf_WITH_ZLIB OFF CACHE BOOL "" FORCE)
set(protobuf_LOCAL_DEPENDENCIES_ONLY ON CACHE BOOL "" FORCE)
set(protobuf_FORCE_FETCH_DEPENDENCIES OFF CACHE BOOL "" FORCE)
add_subdirectory("${IHOMELAND_PROTOBUF_SOURCE}" "${CMAKE_BINARY_DIR}/third_party/protobuf" EXCLUDE_FROM_ALL)

set(SOVERSION 1)
set(LIB_VERSION 1.6.0)
set(RECASTNAVIGATION_DT_POLYREF64 OFF)
set(RECASTNAVIGATION_DT_VIRTUAL_QUERYFILTER OFF)
include(GNUInstallDirs)
add_subdirectory("${IHOMELAND_RECAST_SOURCE}/Detour" "${CMAKE_BINARY_DIR}/third_party/recast/Detour" EXCLUDE_FROM_ALL)
