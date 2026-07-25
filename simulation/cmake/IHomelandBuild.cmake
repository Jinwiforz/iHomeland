include_guard(GLOBAL)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>DLL")
set(CMAKE_POSITION_INDEPENDENT_CODE OFF)

option(IHOMELAND_ENABLE_ASAN "为项目 target 启用 AddressSanitizer。" OFF)
option(IHOMELAND_WARNINGS_AS_ERRORS "把项目 target 的 compiler warning 视为错误。" ON)
set(IHOMELAND_VISUAL_STUDIO_VERSION "" CACHE STRING "锁定的 Visual Studio Build Tools 产品版本。")
set(IHOMELAND_MSVC_TOOLSET_VERSION "" CACHE STRING "锁定的 MSVC toolset 文件版本。")
set(IHOMELAND_WINDOWS_SDK_PACKAGE_VERSION "" CACHE STRING "锁定的 Windows SDK 包版本。")
set(IHOMELAND_MSC_VER "" CACHE STRING "锁定的 _MSC_VER。")

if(IHOMELAND_ENABLE_ASAN)
    # Protobuf/Abseil 与项目 target 必须使用相同的 MSVC STL annotation ABI。
    # ASan 仍覆盖 heap/stack/global；这里只关闭无法跨未注解静态库混用的容器 annotation。
    add_compile_definitions(_DISABLE_VECTOR_ANNOTATION _DISABLE_STRING_ANNOTATION)
endif()

if(NOT MSVC OR NOT MSVC_VERSION EQUAL IHOMELAND_MSC_VER)
    message(FATAL_ERROR "CMake 实际 compiler 不符合锁定的 _MSC_VER=${IHOMELAND_MSC_VER}")
endif()

# ihomeland_configure_project_target 固定项目 target 的语言、诊断和 sanitizer；第三方 target 不继承这些选项。
function(ihomeland_configure_project_target target_name)
    target_compile_features(${target_name} PUBLIC cxx_std_20)
    if(MSVC)
        target_compile_options(
            ${target_name}
            PRIVATE /W4 /permissive- /Zc:__cplusplus /Zc:preprocessor /EHsc /utf-8
        )
        if(IHOMELAND_WARNINGS_AS_ERRORS)
            target_compile_options(${target_name} PRIVATE /WX)
        endif()
        if(IHOMELAND_ENABLE_ASAN)
            target_compile_options(${target_name} PRIVATE /fsanitize=address)
        endif()
    else()
        message(FATAL_ERROR "B0.3 只支持锁定的 Windows MSVC reference toolchain")
    endif()
endfunction()

# ihomeland_write_build_manifest 生成不含用户名、绝对路径和 secret 的低敏 configure identity。
function(ihomeland_write_build_manifest)
    set(manifest_path "${CMAKE_BINARY_DIR}/ihomeland-build-manifest.json")
    if(IHOMELAND_WARNINGS_AS_ERRORS)
        set(warnings_as_errors_json true)
    else()
        set(warnings_as_errors_json false)
    endif()
    if(IHOMELAND_ENABLE_ASAN)
        set(asan_json true)
    else()
        set(asan_json false)
    endif()
    file(GENERATE
        OUTPUT "${manifest_path}"
        CONTENT
"{
  \"schema_version\": 1,
  \"project_version\": \"${PROJECT_VERSION}\",
  \"build_type\": \"${CMAKE_BUILD_TYPE}\",
  \"generator\": \"${CMAKE_GENERATOR}\",
  \"generator_platform\": \"${CMAKE_GENERATOR_PLATFORM}\",
  \"generator_toolset\": \"${CMAKE_GENERATOR_TOOLSET}\",
  \"cxx_compiler_id\": \"${CMAKE_CXX_COMPILER_ID}\",
  \"cxx_compiler_version\": \"${CMAKE_CXX_COMPILER_VERSION}\",
  \"visual_studio\": \"${IHOMELAND_VISUAL_STUDIO_VERSION}\",
  \"msvc_toolset\": \"${IHOMELAND_MSVC_TOOLSET_VERSION}\",
  \"compiler_ui_language\": \"$ENV{PreferredUILang}\",
  \"compiler_ui_lcid\": \"$ENV{VSLANG}\",
  \"windows_sdk_package\": \"${IHOMELAND_WINDOWS_SDK_PACKAGE_VERSION}\",
  \"windows_sdk_headers\": \"${CMAKE_SYSTEM_VERSION}\",
  \"cxx_standard\": 20,
  \"msvc_runtime\": \"dynamic\",
  \"compile_policy\": \"/W4;/WX;/permissive-;/Zc:__cplusplus;/Zc:preprocessor;/EHsc;/utf-8\",
  \"dependencies_disconnected\": true,
  \"warnings_as_errors\": ${warnings_as_errors_json},
  \"asan\": ${asan_json},
  \"dependencies\": {
    \"jolt\": \"5.5.0\",
    \"recast_detour\": \"1.6.0\",
    \"nlohmann_json\": \"3.12.0\",
    \"asio\": \"1.38.2\",
    \"kcp\": \"2.1.1\",
    \"libsodium\": \"1.0.22\",
    \"abseil\": \"20250512.1\",
    \"protobuf\": \"35.0\"
  }
}
")
endfunction()
