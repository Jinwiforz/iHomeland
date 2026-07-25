if(NOT IS_DIRECTORY "${IHOMELAND_SIMULATION_ROOT}")
    message(FATAL_ERROR "simulation scope gate 根不存在")
endif()

file(GLOB_RECURSE scope_files
    "${IHOMELAND_SIMULATION_ROOT}/include/*.hpp"
    "${IHOMELAND_SIMULATION_ROOT}/src/*.cpp"
    "${IHOMELAND_SIMULATION_ROOT}/apps/*.cpp"
)
if(NOT scope_files)
    message(FATAL_ERROR "simulation scope gate 文件集为空")
endif()

set(approved_udp_adapter
    "${IHOMELAND_SIMULATION_ROOT}/src/transport/udp_listener.cpp")
cmake_path(NORMAL_PATH approved_udp_adapter)
set(approved_kcp_adapter
    "${IHOMELAND_SIMULATION_ROOT}/src/transport/kcp_adapter.cpp")
cmake_path(NORMAL_PATH approved_kcp_adapter)
set(udp_socket_owner_count 0)
set(kcp_owner_count 0)

foreach(source_file IN LISTS scope_files)
    cmake_path(NORMAL_PATH source_file)
    file(READ "${source_file}" content)
    if(content MATCHES "#[ \t]*include[ \t]*[<\"](asio|boost/asio|winsock|sys/socket|grpc|mysql|redis|UnityEngine)")
        if(NOT source_file STREQUAL approved_udp_adapter OR
           NOT content MATCHES "#[ \t]*include[ \t]*<asio\\.hpp>")
            message(FATAL_ERROR "B0.5 网络 include 越过唯一 UDP adapter：${source_file}")
        endif()
    endif()
    if(content MATCHES "(^|[^A-Za-z0-9_])(socket|bind|listen|accept|connect|WSAStartup)[ \t\r\n]*\\("
        OR content MATCHES "asio::ip::udp::socket")
        if(NOT source_file STREQUAL approved_udp_adapter)
            message(FATAL_ERROR "B0.5 socket 行为越过唯一 UDP adapter：${source_file}")
        endif()
        math(EXPR udp_socket_owner_count "${udp_socket_owner_count} + 1")
    endif()
    if(content MATCHES "ikcp_")
        if(NOT source_file STREQUAL approved_kcp_adapter OR
           NOT content MATCHES "#[ \t]*include[ \t]*<ikcp\\.h>")
            message(FATAL_ERROR "B0.5 KCP 行为越过唯一 adapter：${source_file}")
        endif()
        math(EXPR kcp_owner_count "${kcp_owner_count} + 1")
    endif()
    if(content MATCHES "UnityEngine|MonoBehaviour"
        OR content MATCHES "(BattleMessageID|BattleMessageId|battle_message_id)[ \t]*=")
        message(FATAL_ERROR "B0.5 引入禁止的 message/Unity 行为：${source_file}")
    endif()
    if((content MATCHES "BattleTicket|battle_ticket")
        AND source_file MATCHES "/(core|gameplay)/")
        message(FATAL_ERROR "B0.5 BattleTicket authority 越界进入 simulation core/gameplay：${source_file}")
    endif()
    if(content MATCHES "#[ \t]*include[ \t]*[<\"](internal/|server/|cmd/)"
        OR content MATCHES "(GoRepository|go_repository)")
        message(FATAL_ERROR "B0.3 引入禁止的 Go repository 边界：${source_file}")
    endif()
endforeach()

if(NOT EXISTS "${approved_udp_adapter}" OR
   NOT udp_socket_owner_count EQUAL 1)
    message(FATAL_ERROR
        "B0.5 必须且只能由 src/transport/udp_listener.cpp 拥有 UDP socket")
endif()
if(NOT EXISTS "${approved_kcp_adapter}" OR
   NOT kcp_owner_count EQUAL 1)
    message(FATAL_ERROR
        "B0.5 必须且只能由 src/transport/kcp_adapter.cpp 拥有 KCP handle")
endif()

set(dependency_file "${IHOMELAND_SIMULATION_ROOT}/cmake/Dependencies.cmake")
file(READ "${dependency_file}" dependency_content)
if(dependency_content MATCHES "(FetchContent_Declare|find_package)[ \t\r\n]*\\("
    OR dependency_content MATCHES "add_subdirectory[ \t\r\n]*\\([^\\r\\n]*(asio|grpc|boost|mysql|redis|openssl)")
    message(FATAL_ERROR "B0.3 引入未批准 dependency：${dependency_file}")
endif()

foreach(required_dependency IN ITEMS
    "IHOMELAND_JOLT_SOURCE"
    "IHOMELAND_RECAST_SOURCE"
    "IHOMELAND_JSON_SOURCE"
    "IHOMELAND_ASIO_SOURCE"
)
    if(NOT dependency_content MATCHES "${required_dependency}")
        message(FATAL_ERROR "B0.3 approved dependency 声明缺失：${required_dependency}")
    endif()
endforeach()
