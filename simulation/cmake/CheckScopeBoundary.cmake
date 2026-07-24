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

foreach(source_file IN LISTS scope_files)
    file(READ "${source_file}" content)
    if(content MATCHES "#[ \t]*include[ \t]*[<\"](asio|boost/asio|winsock|sys/socket|grpc|mysql|redis|UnityEngine)")
        message(FATAL_ERROR "B0.3 引入禁止的网络/存储/Unity include：${source_file}")
    endif()
    if(content MATCHES "(^|[^A-Za-z0-9_])(socket|bind|listen|accept|connect|WSAStartup)[ \t\r\n]*\\("
        OR content MATCHES "ikcp_|BattleTicket|battle_ticket|UnityEngine|MonoBehaviour"
        OR content MATCHES "(BattleMessageID|BattleMessageId|battle_message_id)[ \t]*=")
        message(FATAL_ERROR "B0.3 引入禁止的 socket/KCP/ticket/message/Unity 行为：${source_file}")
    endif()
    if(content MATCHES "#[ \t]*include[ \t]*[<\"](internal/|server/|cmd/)"
        OR content MATCHES "(GoRepository|go_repository)")
        message(FATAL_ERROR "B0.3 引入禁止的 Go repository 边界：${source_file}")
    endif()
endforeach()

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
)
    if(NOT dependency_content MATCHES "${required_dependency}")
        message(FATAL_ERROR "B0.3 approved dependency 声明缺失：${required_dependency}")
    endif()
endforeach()
