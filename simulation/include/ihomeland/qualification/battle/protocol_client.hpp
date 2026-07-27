#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <vector>

namespace ihomeland::qualification::battle {

/// PacketKind 是资格客户端允许收发的 closed secure lane。
enum class PacketKind : std::uint8_t {
    /// Raw 承载输入、probe 与 snapshot。
    Raw = 1,
    /// Kcp 承载可靠有序且会过期的 route。
    Kcp = 2,
    /// Control 承载 authenticated rebind/rekey/close。
    Control = 3,
};

/// OpenDisposition 是任何 application dispatch 前的 closed secure decision。
enum class OpenDisposition : std::uint8_t {
    /// Accepted 表示 header、AEAD 与 replay 均已提交。
    Accepted = 1,
    /// InvalidHeader 表示 wire、identity、epoch、direction 或长度不合法。
    InvalidHeader = 2,
    /// AuthenticationFailed 表示 AEAD 认证失败。
    AuthenticationFailed = 3,
    /// Duplicate 表示 sequence 已被当前 window 接受。
    Duplicate = 4,
    /// TooOld 表示 sequence 已离开 256-packet window。
    TooOld = 5,
    /// FutureJump 表示 authenticated sequence 跨越超过一个 window。
    FutureJump = 6,
    /// Closed 表示 session 已进入不可逆终态。
    Closed = 7,
};

/// IsPollReplaySuppressed 判断 poll 是否应静默丢弃已认证但未提交的 replay。
///
/// Duplicate 与 TooOld 是不可靠网络重复、重排后的正常 replay gate 终局；
/// 它们不得进入 application dispatch，也不得伪装成 AEAD 认证失败。
[[nodiscard]] constexpr bool IsPollReplaySuppressed(
    const OpenDisposition disposition) noexcept {
    return disposition == OpenDisposition::Duplicate ||
           disposition == OpenDisposition::TooOld;
}

/// SecurityDatagramMutation 是资格客户端可构造的 authenticated wire 负例。
enum class SecurityDatagramMutation : std::uint8_t {
    /// FutureSequence 使用 current C2S key 产生超过 replay window 的向前跳跃。
    FutureSequence = 1,
    /// TooOldSequence 先合法推进 window，再发送刚离开 window 的旧 packet。
    TooOldSequence = 2,
    /// WrongDirection 使用 S2C key 构造伪装成 C2S 的 authenticated datagram。
    WrongDirection = 3,
};

/// TicketCredential 是唯一允许从 stdin 进入客户端的 secret binding。
///
/// owner 必须在交给 ProtocolHandshake 后立即销毁原始 frame；本类型析构时会清零
/// ticket_secret，且不得序列化到 stdout、日志或异常文本。
struct TicketCredential final {
    /// 默认构造函数创建待 closed validator 填充的零值。
    TicketCredential() = default;

    /// 析构函数不可优化地清零 ticket secret。
    ~TicketCredential();

    TicketCredential(const TicketCredential&) = delete;
    TicketCredential& operator=(const TicketCredential&) = delete;

    /// 移动构造函数转移 material 并立即清零 source。
    TicketCredential(TicketCredential&& source) noexcept;

    /// 移动赋值先清零当前 material，再转移并清零 source。
    TicketCredential& operator=(
        TicketCredential&& source) noexcept;

    /// ticket_id 是 16-byte 非秘密 lookup identity。
    std::array<std::uint8_t, 16> ticket_id;
    /// ticket_secret 是 HTTPS 交付的 256-bit credential。
    std::array<std::uint8_t, 32> ticket_secret;
    /// expires_at_unix_ms 是本地 deadline 上限，等于时禁止继续握手。
    std::uint64_t expires_at_unix_ms;
};

/// Retry 是 stateless cookie response 的公开投影。
struct Retry final {
    /// cookie_epoch 是服务端 30 秒 key time slice。
    std::uint32_t cookie_epoch;
    /// cookie 是绑定 hello 与 source endpoint 的 address proof。
    std::array<std::uint8_t, 16> cookie;
};

/// SessionParameters 是成功 ServerAccept 后唯一可进入 transport owner 的状态。
struct SessionParameters final {
    /// session_seed 派生独立 C2S/S2C traffic 与 rekey key。
    std::array<std::uint8_t, 32> session_seed;
    /// session_id_digest 是 ServerAccept 分配的低敏 routing identity。
    std::array<std::uint8_t, 16> session_id;
    /// battle_session_generation 防止旧 session incarnation 复活。
    std::uint32_t battle_session_generation;
    /// key_epoch 初始值必须为一。
    std::uint32_t key_epoch;
    /// endpoint_generation 初始值必须为一。
    std::uint32_t endpoint_generation;
    /// actor_slot 是 admission 冻结的 0..7 slot。
    std::uint8_t actor_slot;
    /// role 是 owner=1、visitor=2 的 closed projection。
    std::uint8_t role;
    /// binding_fingerprint 是认证解密 ServerAccept 后锁定的 authority binding。
    std::array<std::uint8_t, 32> binding_fingerprint;
};

/// ProtocolHandshake 独立实现 ClientHello/Retry/ClientAuth/ServerAccept。
///
/// 该 owner 只依赖锁定 crypto primitive，不包含或链接 production handshake。
class ProtocolHandshake final {
public:
    /// ClientHelloBytes 是无 padding 的 fixed request。
    static constexpr std::size_t ClientHelloBytes = 88;
    /// RetryBytes 是不放大的 fixed response。
    static constexpr std::size_t RetryBytes = 28;
    /// ClientAuthBytes 是 repeated hello、cookie 与 proof。
    static constexpr std::size_t ClientAuthBytes = 140;
    /// ServerAcceptBytes 是 clear key share、ciphertext 与 tag。
    static constexpr std::size_t ServerAcceptBytes = 156;
    /// HandshakeTimeoutMilliseconds 限制 hello 到 accept 的总时长。
    static constexpr std::uint64_t HandshakeTimeoutMilliseconds = 3'000;

    /// 构造函数复制并锁定 credential，生成单 transcript ephemeral key。
    ProtocolHandshake(
        TicketCredential credential,
        std::uint64_t started_unix_ms);

    /// 析构函数清零 credential、ephemeral scalar 与派生临时 secret。
    ~ProtocolHandshake();

    ProtocolHandshake(const ProtocolHandshake&) = delete;
    ProtocolHandshake& operator=(const ProtocolHandshake&) = delete;

    /// ClientHello 返回当前 transcript 的 byte-exact request。
    [[nodiscard]] std::array<std::uint8_t, ClientHelloBytes>
    ClientHello() const;

    /// MatchesRetryEnvelope 只识别可交给当前 transcript 的 Retry。
    ///
    /// UDP socket 可能在端口复用后收到旧 session 的延迟 datagram；
    /// caller 必须先用该分类器丢弃不相关流量，不能让它关闭当前握手。
    [[nodiscard]] static bool MatchesRetryEnvelope(
        std::span<const std::uint8_t> response) noexcept;

    /// MatchesServerAcceptEnvelope 只识别可进入认证解密的 ServerAccept。
    [[nodiscard]] static bool MatchesServerAcceptEnvelope(
        std::span<const std::uint8_t> response) noexcept;

    /// AcceptRetry closed decode Retry 并生成 byte-exact ClientAuth。
    [[nodiscard]] std::optional<
        std::array<std::uint8_t, ClientAuthBytes>>
    AcceptRetry(
        std::span<const std::uint8_t> response,
        std::uint64_t now_unix_ms);

    /// AcceptServer closed decode、派生 schedule 并认证 ServerAccept。
    [[nodiscard]] std::optional<SessionParameters>
    AcceptServer(
        std::span<const std::uint8_t> response,
        std::uint64_t now_unix_ms);

private:
    struct Impl;
    /// impl_ 独占全部 secret 与状态机数据。
    std::unique_ptr<Impl> impl_;
};

/// SecureIdentity 是每个 48-byte AAD 必须精确携带的 session binding。
struct SecureIdentity final {
    /// session_id_digest 是 session_id 的 SHA-256 前 8 bytes。
    std::array<std::uint8_t, 8> session_id_digest;
    /// battle_session_generation 是当前 session incarnation。
    std::uint32_t battle_session_generation;
    /// endpoint_generation 是当前唯一 authenticated endpoint generation。
    std::uint32_t endpoint_generation;
    /// binding_discriminator 是 binding_fingerprint 前 8 bytes。
    std::array<std::uint8_t, 8> binding_discriminator;
};

/// OpenPacket 保存已认证的低层 lane 与 owned plaintext。
struct OpenPacket final {
    /// disposition 只有 Accepted 才允许消费其余字段。
    OpenDisposition disposition{OpenDisposition::InvalidHeader};
    /// kind 是已认证 secure lane。
    PacketKind kind{PacketKind::Raw};
    /// packet_sequence 是已认证 current/previous epoch sequence。
    std::uint64_t packet_sequence{};
    /// plaintext 只在 Accepted 时存在。
    std::vector<std::uint8_t> plaintext;
};

/// ProtocolSecureChannel 是与服务端实现相互独立的 client traffic owner。
class ProtocolSecureChannel final {
public:
    /// SecureHeaderBytes 是 canonical AAD 宽度。
    static constexpr std::size_t SecureHeaderBytes = 48;
    /// AeadTagBytes 是 detached Poly1305 tag。
    static constexpr std::size_t AeadTagBytes = 16;
    /// MaximumDatagramBytes 是 IPv6/UDP 无 fragmentation ceiling。
    static constexpr std::size_t MaximumDatagramBytes = 1'200;
    /// ReplayWindowPackets 是固定 receive bitmap 与 future jump 上限。
    static constexpr std::size_t ReplayWindowPackets = 256;
    /// RekeyIntervalMilliseconds 是 time-based rollover trigger。
    static constexpr std::uint64_t RekeyIntervalMilliseconds = 600'000;
    /// RekeyPacketLimit 是 packet-based rollover trigger。
    static constexpr std::uint64_t RekeyPacketLimit = 1'048'576;
    /// PreviousEpochOverlapMilliseconds 是旧 receive key 保留窗口。
    static constexpr std::uint64_t PreviousEpochOverlapMilliseconds = 3'000;
    /// RolloverDeadlineMilliseconds 限制 authenticated rekey 协商。
    static constexpr std::uint64_t RolloverDeadlineMilliseconds = 3'000;

    /// 构造函数冻结 exact identity，并派生 client direction keys。
    ProtocolSecureChannel(
        const std::array<std::uint8_t, 32>& session_seed,
        SecureIdentity identity,
        std::uint32_t key_epoch,
        std::uint64_t epoch_started_unix_ms);

    /// 析构函数清零 current/previous traffic 与 rekey material。
    ~ProtocolSecureChannel();

    ProtocolSecureChannel(const ProtocolSecureChannel&) = delete;
    ProtocolSecureChannel& operator=(const ProtocolSecureChannel&) = delete;

    /// Seal 以唯一 C2S nonce 认证保护 payload。
    [[nodiscard]] std::optional<std::vector<std::uint8_t>>
    Seal(
        PacketKind kind,
        std::span<const std::uint8_t> payload,
        std::uint64_t now_unix_ms);

    /// SealSecurityNegative 构造不暴露 key/payload 的真实 authenticated 负例 datagram。
    ///
    /// 该入口只存在于独立 qualification client primitive；它按 mutation 原子推进本地
    /// sequence owner，返回值必须立即发往同一受控 socket且不得序列化到 evidence。
    [[nodiscard]] std::vector<std::vector<std::uint8_t>>
    SealSecurityNegative(
        PacketKind kind,
        std::span<const std::uint8_t> payload,
        SecurityDatagramMutation mutation,
        std::uint64_t now_unix_ms);

    /// Open 认证 S2C packet 后才提交 replay window。
    [[nodiscard]] OpenPacket Open(
        std::span<const std::uint8_t> datagram,
        std::uint64_t now_unix_ms);

    /// RolloverRequired 判断 trigger，并在 deadline/clock 失败时关闭。
    [[nodiscard]] bool RolloverRequired(
        std::uint64_t now_unix_ms);

    /// CommitRollover 用已认证 control nonce 推进 exact next epoch。
    [[nodiscard]] bool CommitRollover(
        const std::array<std::uint8_t, 32>& rekey_nonce,
        std::uint32_t next_epoch,
        std::uint64_t now_unix_ms);

    /// CommitEndpointGeneration 只允许 current 到 current+1。
    [[nodiscard]] bool CommitEndpointGeneration(
        std::uint32_t expected_current,
        std::uint32_t next_generation);

    /// Close 清零 secret 并进入不可逆终态。
    void Close() noexcept;

private:
    struct Impl;
    /// RolloverRequiredLocked 在持锁状态维护 deadline 与 previous overlap。
    [[nodiscard]] bool RolloverRequiredLocked(
        std::uint64_t now_unix_ms);
    /// CloseLocked 在持锁状态清零 key/window 并进入不可逆终态。
    void CloseLocked() noexcept;
    /// impl_ 独占 replay、sequence、identity 与所有 key。
    std::unique_ptr<Impl> impl_;
};

/// RawMessage 是 closed raw route decode 的低敏结果。
struct RawMessage final {
    /// message_id 只能是 3002 full 或 3003 delta。
    std::uint32_t message_id;
    /// application_sequence 是 raw envelope 的 session sequence。
    std::uint64_t application_sequence;
    /// server_tick 是 protobuf 权威 tick。
    std::uint64_t server_tick;
    /// snapshot_sequence 是 snapshot 单调 identity。
    std::uint64_t snapshot_sequence;
    /// baseline_id 是 full 建立或 delta 引用的 baseline。
    std::uint64_t baseline_id;
    /// partition_index 是 bounded snapshot 分片序号。
    std::uint32_t partition_index;
    /// partition_count 是同一 snapshot 的有界总分片数。
    std::uint32_t partition_count;
    /// last_processed_input_tick 是完整 partition set 发布的 actor 确认。
    std::uint64_t last_processed_input_tick;
};

/// RawDecodeDisposition 是 snapshot decoder 的闭合终局，不把正常分区等待误判为缺陷。
enum class RawDecodeDisposition : std::uint8_t {
    /// Accepted 表示完整 snapshot 已原子提交。
    Accepted = 1,
    /// Pending 表示合法分区已加入有界集合，仍等待其余分区。
    Pending = 2,
    /// Discarded 表示旧包或重复分区已安全忽略。
    Discarded = 3,
    /// BaselineGap 表示 delta 引用未知 baseline，调用方应等待 resync。
    BaselineGap = 4,
    /// Rejected 表示 wire、protobuf 或状态单调性违反闭合契约。
    Rejected = 5,
};

/// RawDecodeResult 分离可恢复网络结果与必须 fail closed 的协议缺陷。
struct RawDecodeResult final {
    /// disposition 是本次 snapshot 分区的唯一裁决。
    RawDecodeDisposition disposition;
    /// message 只在 Accepted 时包含完整提交后的低敏投影。
    std::optional<RawMessage> message;
};

/// ProtocolRawLane 独立编码 c2s 并维护 bounded s2c baseline state。
class ProtocolRawLane final {
public:
    /// MaximumPayloadBytes 是 secure payload ceiling。
    static constexpr std::size_t MaximumPayloadBytes = 1'120;
    /// MaximumPartitions 防止恶意 snapshot 建立无界 pending state。
    static constexpr std::uint32_t MaximumPartitions = 32;

    /// 构造函数初始化空 baseline 与严格递增序列。
    ProtocolRawLane();

    /// EncodeInputBundle 编码已由 workload owner构造的 typed input。
    [[nodiscard]] std::vector<std::uint8_t> EncodeInputBundle(
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> protobuf_payload) const;

    /// EncodeProbe 编码 typed probe protobuf。
    [[nodiscard]] std::vector<std::uint8_t> EncodeProbe(
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> protobuf_payload) const;

    /// DecodeSnapshot 验证 route、direction、protobuf 与 baseline transition。
    [[nodiscard]] RawDecodeResult DecodeSnapshot(
        std::span<const std::uint8_t> plaintext);

    /// LatestServerTick 返回最后已接受 snapshot 的权威 tick。
    [[nodiscard]] std::uint64_t LatestServerTick() const noexcept;

    /// LatestSnapshotSequence 返回最后已接受 snapshot sequence。
    [[nodiscard]] std::uint64_t LatestSnapshotSequence() const noexcept;

    /// LastProcessedInputTick 返回最后完整 snapshot 发布的连续输入确认。
    [[nodiscard]] std::uint64_t
    LastProcessedInputTick() const noexcept;

    /// BaselineID 返回最近完整建立的 baseline。
    [[nodiscard]] std::uint64_t BaselineID() const noexcept;

    /// PendingSnapshotSequence 返回正在收集的 bounded snapshot identity。
    [[nodiscard]] std::uint64_t PendingSnapshotSequence() const noexcept;

    /// ResetPendingSnapshot 在 resync/expiry 后丢弃未完成分区，不改变已提交 baseline。
    void ResetPendingSnapshot() noexcept;

private:
    /// EncodeC2S 执行 closed message/size/payload validation。
    [[nodiscard]] static std::vector<std::uint8_t> EncodeC2S(
        std::uint32_t message_id,
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> protobuf_payload);

    /// latest_server_tick_ 只随接受的新 snapshot 前进。
    std::uint64_t latest_server_tick_{};
    /// latest_snapshot_sequence_ 拒绝旧或重复 snapshot。
    std::uint64_t latest_snapshot_sequence_{};
    /// baseline_id_ 只由完整 full snapshot 建立。
    std::uint64_t baseline_id_{};
    /// pending_message_id_ 区分正在收集的 full/delta partitions。
    std::uint32_t pending_message_id_{};
    /// pending_baseline_id_ 绑定正在收集的 snapshot baseline。
    std::uint64_t pending_baseline_id_{};
    /// pending_snapshot_sequence_ 绑定同一 snapshot。
    std::uint64_t pending_snapshot_sequence_{};
    /// pending_server_tick_ 绑定同一 snapshot 的权威 tick。
    std::uint64_t pending_server_tick_{};
    /// pending_partition_count_ 冻结本轮 snapshot partition count。
    std::uint32_t pending_partition_count_{};
    /// pending_last_processed_input_tick_ 冻结本轮全部 partition 的确认。
    std::uint64_t
        pending_last_processed_input_tick_{};
    /// pending_partition_bitmap_ 在 32-bit hard cap 内跟踪已接收分片。
    std::uint32_t pending_partition_bitmap_{};
    /// last_processed_input_tick_ 只在完整 snapshot 后单调发布。
    std::uint64_t last_processed_input_tick_{};
};

/// KcpMessage 是 reassembly 后通过 closed route/protobuf 校验的结果。
struct KcpMessage final {
    /// message_id 只能是 3004、3005 或 3007。
    std::uint32_t message_id;
    /// application_sequence 是 session 内可靠消息序列。
    std::uint64_t application_sequence;
    /// server_tick 是 typed payload 的权威 tick。
    std::uint64_t server_tick;
};

/// KcpCloseReason 是 client KCP 不可逆终态的低敏分类。
enum class KcpCloseReason : std::uint8_t {
    /// None 表示仍可继续推进。
    None = 0,
    /// Clock 表示 absolute clock 回退或超出 uint32 runtime。
    Clock = 1,
    /// Output 表示 primitive 输出越过 segment ceiling 或分配失败。
    Output = 2,
    /// Primitive 表示 KCP send/input/recv 返回错误。
    Primitive = 3,
    /// InflightExpiry 表示尚未被 peer ACK 的发送消息到达 route deadline。
    InflightExpiry = 4,
    /// Protocol 表示 reassembled route 不符合 closed contract。
    Protocol = 5,
};

/// ProtocolKcpStatus 是独立客户端可公开到资格 evidence 的低敏状态快照。
struct ProtocolKcpStatus final {
    /// close_reason 是当前不可逆终态；None 表示 lane 仍可推进。
    KcpCloseReason close_reason;
    /// queued_messages 是尚未提交给 KCP primitive 的 application message 数。
    std::size_t queued_messages;
    /// inflight_messages 是已提交但尚未完成 ACK reconciliation 的 message 数。
    std::size_t inflight_messages;
    /// waiting_segments 是 KCP send queue 与 send buffer 的当前 segment 数。
    std::size_t waiting_segments;
    /// input_datagrams 是通过 secure open 后进入 KCP primitive 的累计 datagram 数。
    std::uint64_t input_datagrams;
    /// input_ack_commands 是有效入站 datagram 中的累计 KCP ACK command 数。
    std::uint64_t input_ack_commands;
    /// input_push_commands 是有效入站 datagram 中的累计 KCP PUSH command 数。
    std::uint64_t input_push_commands;
    /// output_datagrams 是 KCP primitive 交给 secure lane 的累计 datagram 数。
    std::uint64_t output_datagrams;
    /// reconciled_messages 是因 peer ACK 从 inflight 集合回收的累计 message 数。
    std::uint64_t reconciled_messages;
};

/// ProtocolKcpLane 直接拥有锁定 KCP primitive，不包含 production adapter。
class ProtocolKcpLane final {
public:
    /// UpdateIntervalMilliseconds 是固定 KCP update cadence。
    static constexpr std::uint32_t UpdateIntervalMilliseconds = 10;
    /// WindowSegments 是 send/receive window hard cap。
    static constexpr std::uint32_t WindowSegments = 64;
    /// FastResend 是固定 fast resend threshold。
    static constexpr std::uint32_t FastResend = 2;
    /// MinimumRtoMilliseconds 是 adaptive RTO 下限。
    static constexpr std::uint32_t MinimumRtoMilliseconds = 30;
    /// MaximumRtoMilliseconds 是 adaptive RTO 上限。
    static constexpr std::uint32_t MaximumRtoMilliseconds = 200;
    /// MaximumSegmentPayloadBytes 是 KCP segment data ceiling。
    static constexpr std::size_t MaximumSegmentPayloadBytes = 1'000;
    /// QueueItems 是 application + inflight hard cap。
    static constexpr std::size_t QueueItems = 64;
    /// ReliableEventExpiryMilliseconds 是3004/3005业务价值窗口。
    static constexpr std::uint64_t ReliableEventExpiryMilliseconds = 500;
    /// ResyncExpiryMilliseconds 是3006/3007恢复事务窗口。
    static constexpr std::uint64_t ResyncExpiryMilliseconds = 2'250;

    /// 构造函数创建 exact nonzero conversation handle。
    explicit ProtocolKcpLane(std::uint32_t conversation);

    /// 析构函数释放 KCP handle 与有界 pending state。
    ~ProtocolKcpLane();

    ProtocolKcpLane(const ProtocolKcpLane&) = delete;
    ProtocolKcpLane& operator=(const ProtocolKcpLane&) = delete;

    /// QueueResync 编码 typed 3006 request 并加入有界发送队列。
    [[nodiscard]] bool QueueResync(
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> protobuf_payload,
        std::uint64_t now_unix_ms);

    /// Input 验证一个完整 KCP segment 并收取所有完整消息。
    [[nodiscard]] bool Input(
        std::span<const std::uint8_t> segment,
        std::uint64_t now_unix_ms);

    /// Update 推进 fixed cadence、expiry 与 pending output。
    void Update(std::uint64_t now_unix_ms);

    /// TakeSegments 取得本轮需要进入 secure KCP lane 的 segments。
    [[nodiscard]] std::vector<std::vector<std::uint8_t>>
    TakeSegments();

    /// TakeMessages 取得已通过 closed typed validation 的 s2c 消息。
    [[nodiscard]] std::vector<KcpMessage> TakeMessages();

    /// Closed 返回任何 primitive/clock/parse failure 后的稳定终态。
    [[nodiscard]] bool Closed() const noexcept;

    /// CloseReason 返回不含 payload/identity 的稳定终态分类。
    [[nodiscard]] KcpCloseReason CloseReason() const noexcept;

    /// Status 原子返回 ACK、queue 与 reconciliation 的低敏累计/当前状态。
    [[nodiscard]] ProtocolKcpStatus Status() const noexcept;

private:
    struct Impl;
    /// impl_ 隔离第三方 C handle 与 client-owned queue。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::qualification::battle
