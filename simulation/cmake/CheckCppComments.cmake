if(NOT DEFINED IHOMELAND_SIMULATION_ROOT OR
   NOT IS_DIRECTORY "${IHOMELAND_SIMULATION_ROOT}")
    message(FATAL_ERROR "C++ comment gate requires simulation source root")
endif()

file(GLOB_RECURSE handwritten_files
    "${IHOMELAND_SIMULATION_ROOT}/include/*.hpp"
    "${IHOMELAND_SIMULATION_ROOT}/src/*.cpp"
    "${IHOMELAND_SIMULATION_ROOT}/apps/*.cpp"
    "${IHOMELAND_SIMULATION_ROOT}/tests/*.cpp"
)
if(NOT handwritten_files)
    message(FATAL_ERROR "C++ comment gate found no handwritten source")
endif()

foreach(source_path IN LISTS handwritten_files)
    file(READ "${source_path}" source_text)
    if(source_text MATCHES "(^|[\r\n])[ \t]*(//|/\\*)[ \t]*(TODO|FIXME|XXX)" OR
       source_text MATCHES "#[ \t]*region|//[ \t]*region")
        message(FATAL_ERROR "forbidden TODO/FIXME/region marker: ${source_path}")
    endif()
    if(source_path MATCHES "/include/" AND
       source_text MATCHES
           "(password|credential|private_key|battle_ticket)[A-Za-z0-9_]*[ \t]*;")
        message(FATAL_ERROR "sensitive field entered a public C++ contract: ${source_path}")
    endif()

    file(STRINGS "${source_path}" source_lines ENCODING UTF-8)
    set(previous_nonempty "")
    set(previous_previous_nonempty "")
    foreach(source_line IN LISTS source_lines)
        string(STRIP "${source_line}" stripped)
        if(stripped MATCHES
               "^(class|struct|enum[ \t]+class)[ \t]+[A-Za-z_][A-Za-z0-9_]*.*\\{$" AND
           NOT "${previous_nonempty}" MATCHES "^///" AND
           NOT ("${previous_nonempty}" MATCHES "^template[ \t]*<" AND
                "${previous_previous_nonempty}" MATCHES "^///"))
            message(FATAL_ERROR
                "C++ type declaration lacks preceding /// documentation: ${source_path}: ${stripped}")
        endif()
        if(source_path MATCHES "/include/" AND
           source_line MATCHES "^    [^ ]" AND
           stripped MATCHES
               "^(std::[A-Za-z0-9_:<>, \t]+|bool|char|float|double|std::u?int[0-9]+_t|[A-Z][A-Za-z0-9_:<>, \t]+)[ \t]+[a-z_][A-Za-z0-9_]*(\\{[^;]*\\})?;$" AND
           NOT "${previous_nonempty}" MATCHES "^///")
            message(FATAL_ERROR
                "C++ public field lacks preceding /// documentation: ${source_path}: ${stripped}")
        endif()
        if(NOT stripped STREQUAL "")
            set(previous_previous_nonempty "${previous_nonempty}")
            set(previous_nonempty "${stripped}")
        endif()
    endforeach()
endforeach()

message(STATUS "C++ /// declaration coverage, TODO/region and sensitive-field gates passed")
