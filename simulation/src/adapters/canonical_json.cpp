#include "ihomeland/sim/fixture/canonical_json.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <nlohmann/json.hpp>

#include <stdexcept>

namespace ihomeland::sim {

CanonicalJsonDocument CanonicalizeJson(const std::string_view source) {
    try {
        const auto value = nlohmann::json::parse(source, nullptr, true, true);
        const auto text = value.dump(
            -1,
            ' ',
            false,
            nlohmann::json::error_handler_t::strict);
        return CanonicalJsonDocument{.text = text, .sha256 = Sha256Text(text)};
    } catch (const nlohmann::json::exception&) {
        throw std::invalid_argument("canonical JSON parsing failed");
    }
}

}  // namespace ihomeland::sim
