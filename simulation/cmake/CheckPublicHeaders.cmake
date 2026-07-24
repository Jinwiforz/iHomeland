if(NOT IS_DIRECTORY "${IHOMELAND_PUBLIC_HEADER_ROOT}")
    message(FATAL_ERROR "core public header 根不存在")
endif()

file(GLOB_RECURSE public_headers "${IHOMELAND_PUBLIC_HEADER_ROOT}/*.hpp")
if(NOT public_headers)
    message(FATAL_ERROR "core public header 集为空")
endif()

foreach(header IN LISTS public_headers)
    file(READ "${header}" content)
    if(content MATCHES "#[ \t]*include[ \t]*[<\"]([^>\"]*/)?(nlohmann|Jolt|Detour)"
        OR content MATCHES "nlohmann::|JPH::|dtNavMesh|dtCrowd|dtTileCache")
        message(FATAL_ERROR "core public header 暴露第三方类型：${header}")
    endif()
endforeach()
