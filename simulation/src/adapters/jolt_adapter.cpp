#include "ihomeland/sim/core/adapter_smoke.hpp"

#include <Jolt/Jolt.h>
#include <Jolt/Math/Vec3.h>

#include <cmath>

namespace ihomeland::sim {

bool RunJoltSmoke() {
    const JPH::Vec3 vector{3.0F, 0.0F, 4.0F};
    return std::abs(vector.Length() - 5.0F) < 0.0001F;
}

}  // namespace ihomeland::sim
