#include "ihomeland/sim/simulation/command_ingress.hpp"
#include "ihomeland/sim/simulation/input_timeline.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <algorithm>
#include <array>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <iostream>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace {

using namespace std::chrono_literals;

/// Require 把 lifecycle 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestIdentity 返回固定但互不相等的非零 128-bit identity。
[[nodiscard]] ihomeland::sim::Identity128 TestIdentity(const char suffix) {
    return ihomeland::sim::Identity128::ParseLowerHex(
        std::string("00112233445566778899aabbccddee0") + suffix);
}

/// TestInstanceIdentity 构造完整 assignment/mapping/build binding。
[[nodiscard]] ihomeland::sim::SimulationInstanceIdentity TestInstanceIdentity() {
    const std::string digest_a(64, 'a');
    const std::string digest_b(64, 'b');
    const std::string digest_c(64, 'c');
    const std::string digest_d(64, 'd');
    const std::string digest_e(64, 'e');
    const std::string digest_f(64, 'f');
    return ihomeland::sim::SimulationInstanceIdentity(
        TestIdentity('1'),
        ihomeland::sim::AssignmentStamp(TestIdentity('2'), 7, 9, TestIdentity('3')),
        11,
        ihomeland::sim::BuildConfigIdentity(
            digest_a,
            digest_b,
            digest_c,
            digest_d,
            digest_e,
            digest_f));
}

/// TestCommand 构造只含安全连续 intent 的完整 command envelope。
[[nodiscard]] ihomeland::sim::GameplayCommand TestCommand(
    const ihomeland::sim::SimulationInstance& instance,
    const std::uint64_t input_tick,
    const std::uint64_t sequence) {
    return {
        .assignment_fingerprint = instance.Identity().Assignment().Fingerprint(),
        .mapping_generation = instance.Identity().MappingGeneration(),
        .actor_id = 42,
        .input_tick = input_tick,
        .sequence = sequence,
        .expires_at_tick = 10,
        .kind = ihomeland::sim::GameplayCommandKind::ContinuousIntentSample,
        .payload = ihomeland::sim::ContinuousIntentPayload{
            .move_x_permille = 100,
            .move_y_permille = -100}};
}

/// TestIdentityBinding 验证完整 fingerprint 稳定且任一 generation 变化都会改变结果。
void TestIdentityBinding() {
    const auto first = TestInstanceIdentity();
    const auto repeated = TestInstanceIdentity();
    Require(first.Fingerprint() == repeated.Fingerprint(), "instance fingerprint is unstable");
    const auto changed = ihomeland::sim::SimulationInstanceIdentity(
        TestIdentity('1'),
        ihomeland::sim::AssignmentStamp(TestIdentity('2'), 8, 9, TestIdentity('3')),
        11,
        first.BuildConfig());
    Require(first.Fingerprint() != changed.Fingerprint(), "assignment generation was not fingerprinted");
    Require(first.Assignment().Fingerprint().size() == 64, "assignment fingerprint is not SHA-256");
}

/// TestStartupRollback 验证中间 stage 失败只回滚已成功 steps 且顺序相反。
void TestStartupRollback() {
    auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
    std::vector<std::string> events;
    ihomeland::sim::SimulationInstance instance(
        TestInstanceIdentity(),
        {.tick_step = 50ms, .inbox_capacity = 4, .hard_tick_debt = 2},
        clock,
        [](const ihomeland::sim::TickObservation&) {});
    const std::vector<ihomeland::sim::StartupStep> steps{
        {
            .stage = ihomeland::sim::StartupStage::Fixture,
            .initialize = [&] { events.push_back("init-fixture"); },
            .rollback = [&] { events.push_back("rollback-fixture"); }},
        {
            .stage = ihomeland::sim::StartupStage::Ecs,
            .initialize = [&] { events.push_back("init-ecs"); },
            .rollback = [&] { events.push_back("rollback-ecs"); }},
        {
            .stage = ihomeland::sim::StartupStage::Physics,
            .initialize = [&] {
                events.push_back("init-physics");
                throw std::runtime_error("injected");
            },
            .rollback = [&] { events.push_back("rollback-physics"); }}};
    try {
        instance.Start(steps);
        throw std::runtime_error("startup failure was accepted");
    } catch (const ihomeland::sim::InstanceError& error) {
        Require(
            error.Code() == ihomeland::sim::InstanceErrorCode::Startup,
            "startup failure code drifted");
    }
    Require(
        events == std::vector<std::string>{
                      "init-fixture",
                      "init-ecs",
                      "init-physics",
                      "rollback-ecs",
                      "rollback-fixture"},
        "startup rollback order drifted");
    Require(
        instance.State() == ihomeland::sim::SimulationInstanceState::Failed,
        "startup failure did not enter Failed");
}

/// TestWorkerBoundary 验证完整 batch、唯一 worker、排序、下一 Tick 可见性、容量与 drain。
void TestWorkerBoundary() {
    auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
    std::mutex observation_mutex;
    std::condition_variable observation_condition;
    std::condition_variable release_condition;
    std::vector<std::vector<std::uint64_t>> observed;
    std::thread::id worker_id;
    bool first_entered = false;
    bool release_first = false;
    std::vector<std::string> resources;
    ihomeland::sim::SimulationInstance instance(
        TestInstanceIdentity(),
        {.tick_step = 50ms, .inbox_capacity = 3, .hard_tick_debt = 2},
        clock,
        [&](const ihomeland::sim::TickObservation& observation) {
            std::unique_lock lock(observation_mutex);
            worker_id = std::this_thread::get_id();
            std::vector<std::uint64_t> sequences;
            for (const auto& command : observation.commands) {
                sequences.push_back(command.stable_sequence);
            }
            observed.push_back(std::move(sequences));
            if (observation.tick == 1) {
                first_entered = true;
                observation_condition.notify_all();
                release_condition.wait(lock, [&] { return release_first; });
            }
            observation_condition.notify_all();
        });
    const std::vector<ihomeland::sim::StartupStep> steps{
        {
            .stage = ihomeland::sim::StartupStage::Fixture,
            .initialize = [&] { resources.push_back("init-a"); },
            .rollback = [&] { resources.push_back("rollback-a"); }},
        {
            .stage = ihomeland::sim::StartupStage::Ecs,
            .initialize = [&] { resources.push_back("init-b"); },
            .rollback = [&] { resources.push_back("rollback-b"); }}};
    instance.Start(steps);
    ihomeland::sim::CommandIngress ingress(
        instance,
        {
            .generation = 11,
            .base_input_tick = 1,
            .base_simulation_tick = 1,
            .input_step_ns = 25'000'000,
            .simulation_step_ns = 50'000'000,
            .early_window_ticks = 2,
            .late_window_ticks = 6},
        {42},
        16);
    Require(ingress.Submit(TestCommand(instance, 2, 3)).accepted,
            "first producer command was rejected");
    Require(ingress.Submit(TestCommand(instance, 1, 1)).accepted,
            "second producer command was rejected");
    Require(ingress.Submit(TestCommand(instance, 2, 2)).accepted,
            "third producer command was rejected");
    Require(
        ingress.Submit(TestCommand(instance, 3, 4)).rejection ==
            ihomeland::sim::CommandRejection::Capacity,
            "inbox expanded past hard capacity");
    clock->Advance();
    {
        std::unique_lock lock(observation_mutex);
        Require(
            observation_condition.wait_for(lock, 2s, [&] { return first_entered; }),
            "manual Tick did not reach observer");
    }
    Require(ingress.Submit(TestCommand(instance, 3, 4)).accepted,
            "command submitted during Tick was not queued");
    {
        std::lock_guard lock(observation_mutex);
        release_first = true;
    }
    release_condition.notify_all();
    clock->Advance();
    {
        std::unique_lock lock(observation_mutex);
        Require(
            observation_condition.wait_for(lock, 2s, [&] { return observed.size() == 2; }),
            "second manual Tick did not complete");
    }
    Require(worker_id != std::this_thread::get_id(), "observer ran on producer thread");
    Require(observed[0] == std::vector<std::uint64_t>{1, 2, 3}, "Tick batch order drifted");
    Require(observed[1] == std::vector<std::uint64_t>{4}, "mid-Tick submit leaked into current batch");
    for (std::uint32_t attempt = 0; attempt < 10'000 && instance.CommittedTick() != 2; ++attempt) {
        std::this_thread::yield();
    }
    Require(instance.CommittedTick() == 2, "committed Tick accounting drifted");
    Require(instance.InboxHighWatermark() == 3, "inbox high-watermark drifted");
    Require(
        instance.ReservedBytes() > sizeof(instance),
        "instance reserved memory accounting omitted bounded containers");

    instance.BeginDrain();
    Require(
        ingress.Submit(TestCommand(instance, 4, 5)).rejection ==
            ihomeland::sim::CommandRejection::InstanceClosed,
            "draining instance accepted producer command");
    clock->Advance();
    Require(instance.Stop(2s), "bounded drain did not stop");
    Require(
        resources == std::vector<std::string>{"init-a", "init-b", "rollback-b", "rollback-a"},
        "normal stop rollback order drifted");
}

/// TestCommandBoundaryRejections 覆盖 assignment/mapping/actor/window/duplicate/payload/capacity/expiry。
void TestCommandBoundaryRejections() {
    auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
    ihomeland::sim::SimulationInstance instance(
        TestInstanceIdentity(),
        {.tick_step = 50ms, .inbox_capacity = 8, .hard_tick_debt = 4},
        clock,
        [](const ihomeland::sim::TickObservation&) {});
    const std::vector<ihomeland::sim::StartupStep> steps{{
        .stage = ihomeland::sim::StartupStage::Fixture,
        .initialize = [] {},
        .rollback = [] {}}};
    instance.Start(steps);
    ihomeland::sim::CommandIngress ingress(
        instance,
        {
            .generation = 11,
            .base_input_tick = 1,
            .base_simulation_tick = 1,
            .input_step_ns = 25'000'000,
            .simulation_step_ns = 50'000'000,
            .early_window_ticks = 2,
            .late_window_ticks = 0},
        {42},
        2);

    auto command = TestCommand(instance, 1, 1);
    auto invalid = command;
    invalid.assignment_fingerprint = std::string(64, '0');
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::StaleAssignment,
        "stale assignment was accepted");
    invalid = command;
    invalid.mapping_generation = 12;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::StaleMapping,
        "stale mapping was accepted");
    invalid = command;
    invalid.actor_id = 99;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::ActorBinding,
        "foreign actor binding was accepted");
    invalid = command;
    invalid.input_tick = 0;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::InvalidTick,
        "zero InputTick was accepted");
    invalid = command;
    invalid.sequence = 0;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::InvalidSequence,
        "zero sequence was accepted");
    invalid = command;
    invalid.input_tick = 100;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::TooEarly,
        "far future InputTick was accepted");
    invalid = command;
    invalid.input_tick = std::numeric_limits<std::uint64_t>::max();
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::ArithmeticOverflow,
        "InputTick mapping overflow was accepted");
    invalid = command;
    invalid.kind = ihomeland::sim::GameplayCommandKind::JumpPressed;
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::UnsafePayload,
        "kind/payload mismatch was accepted");
    invalid = command;
    invalid.payload = ihomeland::sim::ContinuousIntentPayload{
        .move_x_permille = 1001,
        .move_y_permille = 0};
    Require(
        ingress.Submit(invalid).rejection == ihomeland::sim::CommandRejection::UnsafePayload,
        "unsafe movement range was accepted");

    Require(ingress.Submit(command).accepted, "valid boundary command was rejected");
    Require(
        ingress.Submit(command).rejection == ihomeland::sim::CommandRejection::Duplicate,
        "duplicate command was accepted");
    Require(ingress.Submit(TestCommand(instance, 1, 2)).accepted, "second dedupe entry was rejected");
    Require(
        ingress.Submit(TestCommand(instance, 2, 3)).rejection ==
            ihomeland::sim::CommandRejection::Capacity,
        "dedupe hard capacity expanded");

    clock->Advance(2);
    for (std::uint32_t attempt = 0; attempt < 10'000 && instance.CommittedTick() != 2; ++attempt) {
        std::this_thread::yield();
    }
    Require(instance.CommittedTick() == 2, "boundary expiry setup did not advance");
    Require(
        ingress.Submit(TestCommand(instance, 1, 4)).rejection ==
            ihomeland::sim::CommandRejection::Expired,
        "expired InputTick was accepted");
    instance.BeginDrain();
    clock->Advance();
    Require(instance.Stop(2s), "boundary instance did not drain");
}

/// TimelineCommand 构造已经通过 ingress 的 normalized command。
[[nodiscard]] ihomeland::sim::IngressCommand TimelineCommand(
    const std::uint64_t target_tick,
    const std::uint64_t input_tick,
    const std::uint64_t sequence,
    const ihomeland::sim::GameplayCommandKind kind,
    std::string payload) {
    return {
        .target_tick = target_tick,
        .actor_id = 42,
        .input_tick = input_tick,
        .stable_sequence = sequence,
        .kind = static_cast<std::uint8_t>(kind),
        .canonical_payload = std::move(payload)};
}

/// TestInputTimeline 验证 arrival reorder、continuous fold/hold/neutral、离散边沿和 gap expiry。
void TestInputTimeline() {
    const ihomeland::sim::InputMappingConfig mapping{
        .generation = 11,
        .base_input_tick = 1,
        .base_simulation_tick = 1,
        .input_step_ns = 25'000'000,
        .simulation_step_ns = 50'000'000,
        .early_window_ticks = 2,
        .late_window_ticks = 6};
    const std::vector<ihomeland::sim::IngressCommand> ordered{
        TimelineCommand(1, 1, 1, ihomeland::sim::GameplayCommandKind::ContinuousIntentSample, "0|100|0"),
        TimelineCommand(1, 2, 2, ihomeland::sim::GameplayCommandKind::ContinuousIntentSample, "0|200|0"),
        TimelineCommand(1, 2, 3, ihomeland::sim::GameplayCommandKind::JumpPressed, "1"),
    };
    auto reversed = ordered;
    std::reverse(reversed.begin(), reversed.end());
    ihomeland::sim::InputTimeline first(mapping, {42}, 2, 1, 8);
    ihomeland::sim::InputTimeline second(mapping, {42}, 2, 1, 8);
    const auto first_tick = first.Resolve(1, ordered);
    const auto reordered_tick = second.Resolve(1, reversed);
    Require(
        first_tick[0].continuous_payload == "0|200|0" &&
            reordered_tick[0].continuous_payload == first_tick[0].continuous_payload,
        "continuous fold depends on arrival order");
    Require(
        first_tick[0].discrete_sequences == std::vector<std::uint64_t>{3} &&
            reordered_tick[0].discrete_sequences == first_tick[0].discrete_sequences,
        "discrete edge depends on arrival order");
    Require(first_tick[0].last_processed_input_tick == 2, "continuous confirmation frontier drifted");

    const std::vector<ihomeland::sim::IngressCommand> tick_two{
        TimelineCommand(2, 4, 4, ihomeland::sim::GameplayCommandKind::ContinuousIntentSample, "0|400|0")};
    const auto second_tick = first.Resolve(2, tick_two);
    Require(
        second_tick[0].continuous_payload == "0|400|0" &&
            second_tick[0].last_processed_input_tick == 2,
        "pending InputTick gap was silently confirmed");
    Require(first.Resolve(3, {})[0].held, "continuous sample was not held inside policy");
    const auto gap_expired = first.Resolve(4, {});
    Require(
        gap_expired[0].last_processed_input_tick == 4 && gap_expired[0].held,
        "gap expiry did not advance contiguous confirmation");
    const auto frozen =
        first.FreezeAcknowledgements();
    Require(
        frozen.size() == 1 &&
            frozen[0].actor_id == 42 &&
            frozen[0].mapping_generation == 11 &&
            frozen[0].last_processed_input_tick == 4,
        "input acknowledgement projection was not immutable and generation scoped");
    const auto neutral = first.Resolve(5, {});
    Require(
        neutral[0].continuous_payload == "0|0|0" && !neutral[0].held &&
            neutral[0].discrete_sequences.empty(),
        "continuous hold did not return to neutral");
    auto successor = mapping;
    successor.generation = 12;
    first.ReplaceMapping(successor);
    const auto reset = first.Resolve(1, {});
    const auto successor_projection =
        first.FreezeAcknowledgements();
    Require(
        reset[0].last_processed_input_tick == 0 &&
            reset[0].continuous_payload == "0|0|0" &&
            successor_projection[0]
                    .mapping_generation ==
                12 &&
            successor_projection[0]
                    .last_processed_input_tick ==
                0,
        "input timeline successor retained old generation state");
}

/// TestInputAcknowledgementStore 验证跨线程投影只接受完整同 generation 单调集合。
void TestInputAcknowledgementStore() {
    ihomeland::sim::InputAcknowledgementStore
        store(11, {7, 3});
    constexpr std::uint64_t InitialServerTick = 5;
    constexpr std::uint64_t FinalServerTick = 1'000;
    const std::array<
        ihomeland::sim::
            InputAcknowledgementProjection,
        2> initial{{
        {3, 11, InitialServerTick * 2},
        {7, 11, InitialServerTick * 3},
    }};
    store.Publish(InitialServerTick, initial);
    const auto projection =
        store.Freeze(7, 11);
    Require(
        projection.has_value() &&
            projection->server_tick ==
                InitialServerTick &&
            projection->acknowledgement
                    .last_processed_input_tick ==
                InitialServerTick * 3 &&
            !store.Freeze(7, 12).has_value(),
        "input acknowledgement store exposed stale generation");
    std::atomic<bool> publishing{true};
    std::atomic<bool> consistent{true};
    std::jthread publisher([&] {
        for (std::uint64_t server_tick =
                 InitialServerTick + 1;
             server_tick <= FinalServerTick;
             ++server_tick) {
            const std::array<
                ihomeland::sim::
                    InputAcknowledgementProjection,
                2> next{{
                {3, 11, server_tick * 2},
                {7, 11, server_tick * 3},
            }};
            store.Publish(server_tick, next);
        }
        publishing.store(
            false,
            std::memory_order_release);
    });
    while (publishing.load(
        std::memory_order_acquire)) {
        const auto snapshot =
            store.Freeze(7, 11);
        if (!snapshot.has_value() ||
            snapshot->acknowledgement
                    .last_processed_input_tick !=
                snapshot->server_tick * 3) {
            consistent.store(
                false,
                std::memory_order_relaxed);
            break;
        }
    }
    publisher.join();
    Require(
        consistent.load(
            std::memory_order_relaxed),
        "input acknowledgement snapshot combined different commits");
    const std::array<
        ihomeland::sim::
            InputAcknowledgementProjection,
        2> regressed{{
        {3, 11, 3},
        {7, 11, 6},
    }};
    bool rejected = false;
    try {
        store.Publish(
            FinalServerTick + 1,
            regressed);
    } catch (const std::invalid_argument&) {
        rejected = true;
    }
    Require(
        rejected,
        "input acknowledgement store accepted regression");
    rejected = false;
    try {
        store.Publish(
            FinalServerTick,
            initial);
    } catch (const std::invalid_argument&) {
        rejected = true;
    }
    Require(
        rejected,
        "input acknowledgement store accepted stale server Tick");
}

/// TestHardTickDebt 验证显式 clock backlog 超过 hard limit 时实例 fail closed。
void TestHardTickDebt() {
    auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
    std::uint32_t observed_ticks = 0;
    ihomeland::sim::SimulationInstance instance(
        TestInstanceIdentity(),
        {.tick_step = 50ms, .inbox_capacity = 2, .hard_tick_debt = 1},
        clock,
        [&](const ihomeland::sim::TickObservation&) { ++observed_ticks; });
    const std::vector<ihomeland::sim::StartupStep> steps{{
        .stage = ihomeland::sim::StartupStage::Fixture,
        .initialize = [] {},
        .rollback = [] {}}};
    instance.Start(steps);
    clock->Advance(3);
    for (std::uint32_t attempt = 0;
         attempt < 10'000 &&
         instance.State() != ihomeland::sim::SimulationInstanceState::Failed;
         ++attempt) {
        std::this_thread::yield();
    }
    Require(
        instance.State() == ihomeland::sim::SimulationInstanceState::Failed &&
            observed_ticks == 0,
        "hard Tick debt did not fail before gameplay callback");
    Require(!instance.Stop(1s), "failed debt instance reported normal stop");
}

/// TestStopDeadline 验证无 Tick credit 时 deadline failure 终止 worker 并释放资源。
void TestStopDeadline() {
    auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
    std::uint32_t rollback_count = 0;
    ihomeland::sim::SimulationInstance instance(
        TestInstanceIdentity(),
        {.tick_step = 50ms, .inbox_capacity = 1, .hard_tick_debt = 1},
        clock,
        [](const ihomeland::sim::TickObservation&) {});
    const std::vector<ihomeland::sim::StartupStep> steps{{
        .stage = ihomeland::sim::StartupStage::Fixture,
        .initialize = [] {},
        .rollback = [&] { ++rollback_count; }}};
    instance.Start(steps);
    Require(!instance.Stop(0ms), "zero drain deadline unexpectedly succeeded");
    Require(
        instance.State() == ihomeland::sim::SimulationInstanceState::Failed,
        "deadline failure did not enter Failed");
    Require(rollback_count == 1, "deadline failure did not release resources once");
}

/// TestRepeatedInstances 验证连续创建、运行、drain 与销毁不会残留跨实例状态。
void TestRepeatedInstances() {
    std::uint32_t initialized = 0;
    std::uint32_t rolled_back = 0;
    for (std::uint32_t iteration = 0; iteration < 32; ++iteration) {
        auto clock = std::make_shared<ihomeland::sim::ManualTickClock>();
        ihomeland::sim::SimulationInstance instance(
            TestInstanceIdentity(),
            {.tick_step = 50ms, .inbox_capacity = 2, .hard_tick_debt = 2},
            clock,
            [](const ihomeland::sim::TickObservation&) {});
        const std::vector<ihomeland::sim::StartupStep> steps{{
            .stage = ihomeland::sim::StartupStage::Fixture,
            .initialize = [&] { ++initialized; },
            .rollback = [&] { ++rolled_back; }}};
        instance.Start(steps);
        instance.BeginDrain();
        clock->Advance(1);
        Require(instance.Stop(1s), "repeated instance did not drain");
        Require(
            instance.State() ==
                ihomeland::sim::SimulationInstanceState::Stopped,
            "repeated instance did not reach Stopped");
    }
    Require(
        initialized == 32 && rolled_back == 32,
        "repeated instances leaked or duplicated startup resources");
}

/// TestBoundedInboxDrain 验证预留不足时不改变 ring，成功时按 FIFO 追加且不扩容。
void TestBoundedInboxDrain() {
    ihomeland::sim::BoundedInbox<int> inbox(2);
    Require(
        inbox.TryPush(1) == ihomeland::sim::InboxPushResult::Accepted &&
            inbox.TryPush(2) == ihomeland::sim::InboxPushResult::Accepted,
        "bounded inbox setup failed");
    std::vector<int> insufficient;
    insufficient.reserve(1);
    bool capacity_rejected = false;
    try {
        inbox.DrainInto(insufficient);
    } catch (const std::length_error&) {
        capacity_rejected = true;
    }
    Require(
        capacity_rejected && inbox.Size() == 2,
        "insufficient drain destination partially consumed inbox");

    std::vector<int> destination;
    destination.reserve(3);
    destination.push_back(9);
    const auto capacity_before = destination.capacity();
    inbox.DrainInto(destination);
    Require(
        destination == std::vector<int>({9, 1, 2}) &&
            destination.capacity() == capacity_before &&
            inbox.Empty(),
        "bounded inbox drain allocated, reordered, or retained values");
}

}  // namespace

/// main 执行 identity、startup rollback、worker/inbox boundary 与 deadline 回归。
int main() {
    try {
        TestIdentityBinding();
        TestStartupRollback();
        TestWorkerBoundary();
        TestCommandBoundaryRejections();
        TestInputTimeline();
        TestInputAcknowledgementStore();
        TestHardTickDebt();
        TestStopDeadline();
        TestRepeatedInstances();
        TestBoundedInboxDrain();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
