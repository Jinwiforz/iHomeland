#include "ihomeland/sim/config/gameplay_package.hpp"
#include "ihomeland/sim/config/personal_world_arena.hpp"
#include "ihomeland/sim/gameplay/production_encounter.hpp"

#include <iostream>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

constexpr auto ConfigIdentity = "d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b";
constexpr auto NavigationIdentity = "14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f";
constexpr auto PhysicsIdentity = "64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e";
constexpr auto WireIdentity = "9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432";
constexpr auto ModelIdentity = "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1";
constexpr auto ProfileIdentity = "c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424";

void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

[[nodiscard]] std::shared_ptr<const ihomeland::sim::GameplayPackageCatalog> Catalog() {
    auto catalog = ihomeland::sim::LoadGameplayPackageCatalog(
        IHOMELAND_GAMEPLAY_PACKAGE_ROOT,
        "personal-world-combat-v1",
        ConfigIdentity,
        NavigationIdentity,
        PhysicsIdentity,
        WireIdentity,
        ModelIdentity,
        ProfileIdentity);
    catalog.arena = std::make_shared<const ihomeland::sim::PersonalWorldArenaCatalog>(
        ihomeland::sim::LoadPersonalWorldArenaCatalog(
            IHOMELAND_PERSONAL_WORLD_ARENA_ROOT,
            catalog.binding.map_id,
            catalog.binding.map_content_identity,
            catalog.binding.navigation_identity,
            catalog.binding.physics_identity));
    return std::make_shared<const ihomeland::sim::GameplayPackageCatalog>(std::move(catalog));
}

[[nodiscard]] std::vector<ihomeland::sim::StateProjectionToken> PlayerStates(
    const std::size_t count,
    const std::int64_t x,
    const std::int64_t z) {
    std::vector<ihomeland::sim::StateProjectionToken> states;
    for (std::size_t index = 0; index < count; ++index) {
        states.push_back({
            .actor_id = index + 1,
            .x_mm = x + static_cast<std::int64_t>(index * 20),
            .y_mm = 0,
            .z_mm = z,
            .health_scaled = 100'000,
            .phase = 0,
            .alive = true,
            .grounded = true,
            .archetype_id = 1,
            .equipped_weapon_id = 101,
            .max_health_scaled = 100'000});
    }
    return states;
}

[[nodiscard]] std::vector<std::uint64_t> ActivePlayers(
    const std::size_t count) {
    std::vector<std::uint64_t> actors;
    actors.reserve(count);
    for (std::size_t index = 0; index < count; ++index) {
        actors.push_back(index + 1);
    }
    return actors;
}

[[nodiscard]] ihomeland::sim::IngressCommand Primary(
    const std::uint64_t actor,
    const std::uint64_t tick) {
    return {
        .target_tick = tick,
        .actor_id = actor,
        .input_tick = tick,
        .stable_sequence = tick * 10 + actor,
        .kind = static_cast<std::uint8_t>(ihomeland::sim::GameplayCommandKind::ActivateAbility),
        .canonical_payload = "3|1"};
}

[[nodiscard]] ihomeland::sim::IngressCommand SwitchWeapon(
    const std::uint64_t actor,
    const std::uint64_t tick) {
    return {
        .target_tick = tick,
        .actor_id = actor,
        .input_tick = tick,
        .stable_sequence = tick * 10 + actor,
        .kind = static_cast<std::uint8_t>(ihomeland::sim::GameplayCommandKind::SwitchWeapon),
        .canonical_payload = "2"};
}

[[nodiscard]] ihomeland::sim::Vector3Mm SoloKitePosition(const std::uint64_t tick) {
    constexpr std::int64_t Minimum = -28'000;
    constexpr std::int64_t Maximum = 28'000;
    constexpr std::int64_t Edge = Maximum - Minimum;
    constexpr std::int64_t StepPerTick = 250;
    const auto distance = static_cast<std::int64_t>((tick * StepPerTick) % (Edge * 4));
    if (distance < Edge) {
        return {.x = Minimum + distance, .y = 0, .z = Minimum};
    }
    if (distance < Edge * 2) {
        return {.x = Maximum, .y = 0, .z = Minimum + distance - Edge};
    }
    if (distance < Edge * 3) {
        return {.x = Maximum - (distance - Edge * 2), .y = 0, .z = Maximum};
    }
    return {.x = Minimum, .y = 0, .z = Maximum - (distance - Edge * 3)};
}

void TestSoloSwordFanAndProjectile() {
    const auto catalog = Catalog();
    auto physics = ihomeland::sim::CreatePersonalWorldPhysicsWorld(*catalog->arena);
    auto navigation = ihomeland::sim::CreatePersonalWorldNavigationWorld(*catalog->arena);
    ihomeland::sim::ProductionEncounterRuntime runtime(catalog, 1, 7, physics, navigation);
    auto players = PlayerStates(1, 6000, 4000);
    const auto active = ActivePlayers(1);
    auto snapshot = runtime.Commit(1, std::vector{Primary(1, 1)}, players, active);
    snapshot = runtime.Commit(2, {}, players, active);
    snapshot = runtime.Commit(3, {}, players, active);
    const auto monster = std::ranges::find(snapshot.states, std::uint64_t{1001}, &ihomeland::sim::StateProjectionToken::actor_id);
    Require(monster != snapshot.states.end() && monster->health_scaled == 12'000,
            "sword authority damage did not commit once");
    const auto switch_weapon = SwitchWeapon(1, 4);
    snapshot = runtime.Commit(4, std::span<const ihomeland::sim::IngressCommand>(&switch_weapon, 1), players, active);
    const auto switched_player = std::ranges::find(snapshot.states, std::uint64_t{1}, &ihomeland::sim::StateProjectionToken::actor_id);
    Require(switched_player != snapshot.states.end() && switched_player->equipped_weapon_id == 102,
            "weapon switch did not commit fan grant");
    for (std::uint64_t tick = 5; tick < 11; ++tick) {
        snapshot = runtime.Commit(tick, {}, players, active);
    }
    snapshot = runtime.Commit(11, std::vector{Primary(1, 11)}, players, active);
    Require(std::ranges::any_of(snapshot.ability_events, [](const auto& event) {
                return event.source_actor_id == 1 && event.ability_id == 202 &&
                       event.phase == ihomeland::sim::CombatAbilityEventPhase::Started;
            }),
            "fan primary request was not accepted");
    snapshot = runtime.Commit(12, {}, players, active);
    snapshot = runtime.Commit(13, {}, players, active);
    Require(std::ranges::any_of(snapshot.states, [](const auto& state) { return state.archetype_id == 301; }),
            "fan projectile was not deferred-spawned");
    snapshot = runtime.Commit(14, {}, players, active);
    const auto defeated = std::ranges::find(snapshot.states, std::uint64_t{1001}, &ihomeland::sim::StateProjectionToken::actor_id);
    Require(defeated != snapshot.states.end() && !defeated->alive && defeated->health_scaled == 0,
            "fan projectile did not commit first target death");
}

void TestSafeSpawnRespectsAcquireRange() {
    const auto catalog = Catalog();
    ihomeland::sim::ProductionEncounterRuntime runtime(
        catalog,
        1,
        17,
        ihomeland::sim::CreatePersonalWorldPhysicsWorld(*catalog->arena),
        ihomeland::sim::CreatePersonalWorldNavigationWorld(*catalog->arena));
    const auto players = PlayerStates(1, -12'000, -12'000);
    const auto active = ActivePlayers(1);
    ihomeland::sim::ProductionEncounterSnapshot snapshot;
    for (std::uint64_t tick = 1; tick <= 180; ++tick) {
        snapshot = runtime.Commit(tick, {}, players, active);
    }
    const auto player = std::ranges::find(
        snapshot.states,
        std::uint64_t{1},
        &ihomeland::sim::StateProjectionToken::actor_id);
    const auto monster = std::ranges::find(
        snapshot.states,
        std::uint64_t{1001},
        &ihomeland::sim::StateProjectionToken::actor_id);
    const auto boss = std::ranges::find(
        snapshot.states,
        std::uint64_t{2001},
        &ihomeland::sim::StateProjectionToken::actor_id);
    Require(
        player != snapshot.states.end() &&
            player->alive &&
            player->health_scaled == 100'000 &&
            monster != snapshot.states.end() &&
            monster->x_mm == 6'000 &&
            monster->z_mm == 4'000 &&
            boss != snapshot.states.end() &&
            boss->x_mm == 15'000 &&
            boss->z_mm == 15'000,
        "AI crossed configured acquire range into the safe spawn");
}

void TestParticipationGatesAiDamageWithoutReviving() {
    const auto catalog = Catalog();
    ihomeland::sim::ProductionEncounterRuntime runtime(
        catalog,
        1,
        37,
        ihomeland::sim::CreatePersonalWorldPhysicsWorld(*catalog->arena),
        ihomeland::sim::CreatePersonalWorldNavigationWorld(*catalog->arena));
    const auto players = PlayerStates(1, 6'000, 4'000);
    const auto active = ActivePlayers(1);
    const std::vector<std::uint64_t> inactive;
    ihomeland::sim::ProductionEncounterSnapshot snapshot;
    for (std::uint64_t tick = 1; tick <= 240; ++tick) {
        const auto commands = tick == 1
            ? std::vector{SwitchWeapon(1, tick)}
            : std::vector<ihomeland::sim::IngressCommand>{};
        snapshot = runtime.Commit(
            tick,
            commands,
            players,
            inactive);
    }
    auto player = std::ranges::find(
        snapshot.states,
        std::uint64_t{1},
        &ihomeland::sim::StateProjectionToken::actor_id);
    Require(
        player != snapshot.states.end() &&
            player->alive &&
            player->health_scaled == 100'000 &&
            player->equipped_weapon_id == 101,
        "inactive player slot consumed intent or AI damage");

    for (std::uint64_t tick = 241;
         tick <= 520;
         ++tick) {
        snapshot = runtime.Commit(
            tick,
            {},
            players,
            active);
        player = std::ranges::find(
            snapshot.states,
            std::uint64_t{1},
            &ihomeland::sim::StateProjectionToken::actor_id);
        if (player != snapshot.states.end() &&
            !player->alive) {
            break;
        }
    }
    Require(
        player != snapshot.states.end() &&
            !player->alive &&
            player->health_scaled == 0,
        "active player did not become an AI target");
    snapshot = runtime.Commit(
        snapshot.server_tick + 1,
        {},
        players,
        inactive);
    snapshot = runtime.Commit(
        snapshot.server_tick + 1,
        {},
        players,
        active);
    player = std::ranges::find(
        snapshot.states,
        std::uint64_t{1},
        &ihomeland::sim::StateProjectionToken::actor_id);
    Require(
        player != snapshot.states.end() &&
            !player->alive &&
            player->health_scaled == 0,
        "session reactivation revived a dead authority actor");
}

void TestSoloBossPhaseAndDeathWhileKiting() {
    const auto catalog = Catalog();
    ihomeland::sim::ProductionEncounterRuntime runtime(
        catalog,
        1,
        77,
        ihomeland::sim::CreatePersonalWorldPhysicsWorld(*catalog->arena),
        ihomeland::sim::CreatePersonalWorldNavigationWorld(*catalog->arena));
    ihomeland::sim::ProductionEncounterSnapshot snapshot;
    const auto active = ActivePlayers(1);
    std::uint32_t maximum_boss_phase = 0;
    for (std::uint64_t tick = 1; tick <= 900; ++tick) {
        const auto position = tick == 1
            ? ihomeland::sim::Vector3Mm{.x = 14'000, .y = 0, .z = 15'000}
            : SoloKitePosition(tick);
        auto players = PlayerStates(1, position.x, position.z);
        std::vector<ihomeland::sim::IngressCommand> commands;
        if (tick == 1) {
            commands.push_back(SwitchWeapon(1, tick));
        } else if (tick == 2 || (tick > 2 && (tick - 2) % 14 == 0)) {
            commands.push_back(Primary(1, tick));
        }
        snapshot = runtime.Commit(tick, commands, players, active);
        const auto boss = std::ranges::find(
            snapshot.states,
            std::uint64_t{2001},
            &ihomeland::sim::StateProjectionToken::actor_id);
        if (boss != snapshot.states.end()) {
            maximum_boss_phase = std::max(maximum_boss_phase, boss->phase);
        }
        if (snapshot.encounter_complete) {
            break;
        }
    }
    const auto boss = std::ranges::find(
        snapshot.states,
        std::uint64_t{2001},
        &ihomeland::sim::StateProjectionToken::actor_id);
    Require(boss != snapshot.states.end() && !boss->alive && boss->health_scaled == 0,
            "solo fan kiting did not commit Boss death");
    Require(maximum_boss_phase >= 2,
            "solo fan kiting did not publish Boss phase progression");
    Require(snapshot.encounter_complete,
            "solo fan kiting did not publish encounter complete");
}

void TestCoopBossDeathAndDeterminism() {
    const auto run = [] {
        const auto catalog = Catalog();
        ihomeland::sim::ProductionEncounterRuntime runtime(
            catalog,
            8,
            99,
            ihomeland::sim::CreatePersonalWorldPhysicsWorld(*catalog->arena),
            ihomeland::sim::CreatePersonalWorldNavigationWorld(*catalog->arena));
        auto players = PlayerStates(8, 14'000, 15'000);
        const auto active = ActivePlayers(8);
        ihomeland::sim::ProductionEncounterSnapshot snapshot;
        for (std::uint64_t tick = 1; tick <= 24; ++tick) {
            std::vector<ihomeland::sim::IngressCommand> commands;
            if (tick == 1 || tick == 11 || tick == 21) {
                for (std::uint64_t actor = 1; actor <= 8; ++actor) {
                    commands.push_back(Primary(actor, tick));
                }
            }
            snapshot = runtime.Commit(tick, commands, players, active);
        }
        const auto boss = std::ranges::find(snapshot.states, std::uint64_t{2001}, &ihomeland::sim::StateProjectionToken::actor_id);
        Require(boss != snapshot.states.end() && !boss->alive && snapshot.encounter_complete,
                "cooperative Boss defeat did not commit once");
        return snapshot.states;
    };
    const auto first = run();
    const auto second = run();
    Require(first == second, "production encounter canonical state drifted across runs");
}

}  // namespace

int main() {
    try {
        TestSoloSwordFanAndProjectile();
        TestSafeSpawnRespectsAcquireRange();
        TestParticipationGatesAiDamageWithoutReviving();
        TestSoloBossPhaseAndDeathWhileKiting();
        TestCoopBossDeathAndDeterminism();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
