cmake_minimum_required(VERSION 3.28)

project(iHomelandSimulation LANGUAGES CXX)

add_executable(ihomeland_simulation
    src/main.cpp
)

target_compile_features(ihomeland_simulation PRIVATE cxx_std_20)

if(MSVC)
    target_compile_options(ihomeland_simulation PRIVATE /W4)
else()
    target_compile_options(ihomeland_simulation PRIVATE -Wall -Wextra -Wpedantic)
endif()

set_target_properties(ihomeland_simulation PROPERTIES
    CXX_EXTENSIONS OFF
    COMPILE_WARNING_AS_ERROR ON
)
