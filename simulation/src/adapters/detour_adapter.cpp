#include "ihomeland/sim/core/adapter_smoke.hpp"

#include <DetourNavMesh.h>

namespace ihomeland::sim {

bool RunDetourSmoke() {
    dtNavMesh* const nav_mesh = dtAllocNavMesh();
    if (nav_mesh == nullptr) {
        return false;
    }
    dtFreeNavMesh(nav_mesh);
    return true;
}

}  // namespace ihomeland::sim
