#include "ihomeland/sim/qualification/reference_benchmark.hpp"

#include <iostream>
#include <stdexcept>
#include <string_view>

/// main 在 Release 裁决 reference budget，Debug/ASan 只执行同 workload smoke。
int main(const int argument_count, const char* const arguments[]) {
    try {
        if (argument_count != 2) {
            throw std::invalid_argument(
                "usage: ihomeland-sim-benchmark --reference|--smoke");
        }
        const auto action = std::string_view(arguments[1]);
        if (action != "--reference" && action != "--smoke") {
            throw std::invalid_argument(
                "usage: ihomeland-sim-benchmark --reference|--smoke");
        }
        if (action == "--reference" &&
            IHOMELAND_REFERENCE_BENCHMARK_BUILD != 1) {
            throw std::runtime_error(
                "reference benchmark requires fixed Release build");
        }
        const auto mode = action == "--reference"
            ? ihomeland::sim::ReferenceBenchmarkMode::ReleaseQualification
            : ihomeland::sim::ReferenceBenchmarkMode::DiagnosticSmoke;
        const auto report = ihomeland::sim::RunReferenceBenchmark(mode);
        std::cout << report.canonical_json << '\n';
        return action == "--smoke" || report.qualified ? 0 : 1;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
