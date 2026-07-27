# 该入口只读验证和重放 battle network profile；它不实现 gameplay、真实 KCP 或任何 listener。
[CmdletBinding()]
param(
    # Action 只允许完整验证或确定性模拟，避免调用方绕过 model binding 与 qualification 门。
    [ValidateSet("validate", "simulate")]
    [string]$Action = "validate",
    # ProfileRoot 仅用于失败回归的隔离副本；默认值指向仓库内唯一 profile corpus。
    [string]$ProfileRoot = "",
    # ModelRoot 允许测试显式提供绑定模型副本；默认值指向仓库内 battle model。
    [string]$ModelRoot = ""
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if ([string]::IsNullOrWhiteSpace($ProfileRoot)) {
    $ProfileRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile"
}
if ([string]::IsNullOrWhiteSpace($ModelRoot)) {
    $ModelRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model"
}
if (-not (Test-Path -LiteralPath $ProfileRoot -PathType Container)) {
    throw "battle network profile root is missing"
}
if (-not (Test-Path -LiteralPath $ModelRoot -PathType Container)) {
    throw "battle model root is missing"
}
$ProfileRoot = (Resolve-Path -LiteralPath $ProfileRoot).Path
$ModelRoot = (Resolve-Path -LiteralPath $ModelRoot).Path
$BattleModelValidator = Join-Path $RepositoryRoot "tools\battle-model\battle-model.ps1"
$Utf8Strict = [System.Text.UTF8Encoding]::new($false, $true)
$Utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$Ordinal = [System.StringComparer]::Ordinal
$ProfileVersion = "battle-network-profile-v2"
$FormatVersion = "2"
$ReliableRouteExpiryMicroseconds = [int64]500000
$ResyncRouteExpiryMicroseconds = [int64]2250000
$MaximumRouteExpiryMicroseconds = $ResyncRouteExpiryMicroseconds
$RequiredPhases = @(
    "boss-burst",
    "combat-heavy",
    "disconnect-drain",
    "idle",
    "movement-heavy"
)
$RequiredWorkloads = @(
    "default-coop",
    "solo-owner",
    "visit-capacity-compatibility"
)
$AllowedClassifications = @(
    "implementation_required",
    "profile_qualified",
    "target_budget"
)
$AllowedLanes = @("kcp", "raw")
$AllowedUnits = @(
    "bytes",
    "bytes_per_second",
    "copies",
    "input_ticks",
    "items",
    "microseconds",
    "millidegrees",
    "millimeters",
    "nanoseconds",
    "snapshots",
    "status",
    "ticks"
)

# Fail-BattleNetworkProfile 使用稳定低敏前缀终止，不回显 corpus 内容或本机目录。
function Fail-BattleNetworkProfile {
    param([string]$Message)
    throw "battle network profile validation failed: $Message"
}

# Get-RelativeProfilePath 把诊断限制为 profile 根相对路径并拒绝路径逃逸。
function Get-RelativeProfilePath {
    param([string]$Path)
    $root = [System.IO.Path]::GetFullPath($ProfileRoot).TrimEnd('\') + '\'
    $full = [System.IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        Fail-BattleNetworkProfile "path escaped profile root"
    }
    return $full.Substring($root.Length).Replace('\', '/')
}

# Resolve-ProfilePath 只解析 profile 根内的仓库相对路径。
function Resolve-ProfilePath {
    param([string]$RelativePath)
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [System.IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath -match '(^|/)\.\.(/|$)') {
        Fail-BattleNetworkProfile "profile path is not root relative"
    }
    $full = [System.IO.Path]::GetFullPath(
        (Join-Path $ProfileRoot $RelativePath.Replace('/', '\')))
    $null = Get-RelativeProfilePath $full
    return $full
}

# Read-CanonicalText 验证 strict UTF-8、LF 和单一末尾换行后返回文本。
function Read-CanonicalText {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail-BattleNetworkProfile "required file is missing"
    }
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -ge 3 -and
        $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        Fail-BattleNetworkProfile "$(Get-RelativeProfilePath $Path) contains UTF-8 BOM"
    }
    if ($bytes -contains 13) {
        Fail-BattleNetworkProfile "$(Get-RelativeProfilePath $Path) contains CR line endings"
    }
    try {
        $text = $Utf8Strict.GetString($bytes)
    } catch {
        Fail-BattleNetworkProfile "$(Get-RelativeProfilePath $Path) is not strict UTF-8"
    }
    if (-not $text.EndsWith("`n") -or $text.EndsWith("`n`n")) {
        Fail-BattleNetworkProfile "$(Get-RelativeProfilePath $Path) must end with one LF"
    }
    return $text
}

# Read-JsonDocument 读取 canonical JSON，并把解析失败收敛为稳定相对路径。
function Read-JsonDocument {
    param([string]$Path)
    $text = Read-CanonicalText $Path
    try {
        return $text | ConvertFrom-Json
    } catch {
        Fail-BattleNetworkProfile "$(Get-RelativeProfilePath $Path) is not valid JSON"
    }
}

# Get-Sha256Bytes 返回 bytes 的 lowercase SHA-256。
function Get-Sha256Bytes {
    param([byte[]]$Bytes)
    $hasher = [System.Security.Cryptography.SHA256]::Create()
    try {
        return -join ($hasher.ComputeHash($Bytes) | ForEach-Object {
                $_.ToString("x2")
            })
    } finally {
        $hasher.Dispose()
    }
}

# Get-Sha256File 返回文件原始 canonical bytes 的 lowercase SHA-256。
function Get-Sha256File {
    param([string]$Path)
    return Get-Sha256Bytes ([System.IO.File]::ReadAllBytes($Path))
}

# Get-Sha256Text 返回 UTF-8 无 BOM 文本的 lowercase SHA-256。
function Get-Sha256Text {
    param([string]$Text)
    return Get-Sha256Bytes ($Utf8NoBom.GetBytes($Text))
}

# Assert-ClosedProperties 拒绝未登记字段并验证必填字段。
function Assert-ClosedProperties {
    param(
        [object]$Value,
        [string[]]$Allowed,
        [string[]]$Required,
        [string]$Context
    )
    if ($null -eq $Value -or $Value -isnot [pscustomobject]) {
        Fail-BattleNetworkProfile "$Context must be an object"
    }
    $actual = @($Value.PSObject.Properties.Name)
    if (@($actual | Where-Object { $_ -notin $Allowed }).Count -gt 0) {
        Fail-BattleNetworkProfile "$Context contains an unknown field"
    }
    if (@($Required | Where-Object { $_ -notin $actual }).Count -gt 0) {
        Fail-BattleNetworkProfile "$Context is missing a required field"
    }
}

# Assert-DocumentEnvelope 验证所有 profile 数据文件共享的版本与文档类型。
function Assert-DocumentEnvelope {
    param([object]$Document, [string]$Kind, [string]$Context)
    if ([string]$Document.format_version -cne $FormatVersion -or
        [string]$Document.profile_version -cne $ProfileVersion -or
        [string]$Document.document_kind -cne $Kind) {
        Fail-BattleNetworkProfile "$Context envelope differs"
    }
}

# Assert-UniqueSortedStrings 验证稳定字符串列表严格 ordinal 递增且无空值。
function Assert-UniqueSortedStrings {
    param([object[]]$Values, [string]$Context)
    $previous = $null
    foreach ($value in @($Values)) {
        $current = [string]$value
        if ([string]::IsNullOrWhiteSpace($current) -or
            ($null -ne $previous -and $Ordinal.Compare($previous, $current) -ge 0)) {
            Fail-BattleNetworkProfile "$Context is not unique ordinal order"
        }
        $previous = $current
    }
}

# Assert-UniqueSortedObjects 验证对象列表的稳定 key 严格 ordinal 递增。
function Assert-UniqueSortedObjects {
    param([object[]]$Values, [scriptblock]$Selector, [string]$Context)
    $previous = $null
    foreach ($value in @($Values)) {
        $current = [string](& $Selector $value)
        if ([string]::IsNullOrWhiteSpace($current) -or
            ($null -ne $previous -and $Ordinal.Compare($previous, $current) -ge 0)) {
            Fail-BattleNetworkProfile "$Context is not unique ordinal order"
        }
        $previous = $current
    }
}

# Assert-ExactSet 验证集合与 required inventory 完全一致。
function Assert-ExactSet {
    param([object[]]$Actual, [string[]]$Expected, [string]$Context)
    $left = @($Actual | ForEach-Object { [string]$_ } | Sort-Object)
    $right = @($Expected | Sort-Object)
    if (($left -join "`n") -cne ($right -join "`n")) {
        Fail-BattleNetworkProfile "$Context differs"
    }
}

# Assert-NonNegativeInteger 拒绝负数、浮点和超过 JSON 安全整数的预算。
function Assert-NonNegativeInteger {
    param([object]$Value, [string]$Context, [bool]$AllowZero = $true)
    $isInteger = $Value -is [byte] -or $Value -is [int16] -or
        $Value -is [int32] -or $Value -is [int64] -or
        $Value -is [uint16] -or $Value -is [uint32] -or
        $Value -is [uint64]
    if (-not $isInteger -or [decimal]$Value -lt 0 -or
        [decimal]$Value -gt 9007199254740991 -or
        (-not $AllowZero -and [decimal]$Value -eq 0)) {
        Fail-BattleNetworkProfile "$Context is outside the integer range"
    }
}

# Assert-NoForbiddenProfileData 拒绝 secret、个人资料、production wire 占位和本机绝对路径字段。
function Assert-NoForbiddenProfileData {
    param([string]$Text, [string]$Context)
    $forbiddenField =
        '(?i)"(?:ticket|credential|password|token|aead[_-]?key|nonce|username|' +
        'email|player[_-]?id|session[_-]?id|endpoint|ip[_-]?address|' +
        'udp[_-]?port|numeric[_-]?message[_-]?id|generated[_-]?wire|' +
        'absolute[_-]?path)"\s*:'
    $absoluteValue = '(?m)"[^"]+"\s*:\s*"[A-Za-z]:\\'
    if ($Text -match $forbiddenField -or $Text -match $absoluteValue) {
        Fail-BattleNetworkProfile "$Context contains forbidden data"
    }
}

# Get-ParameterRecord 按稳定 ID 取得唯一参数，缺失或重复均失败。
function Get-ParameterRecord {
    param([object]$Profile, [string]$Id)
    $records = @($Profile.parameters | Where-Object { $_.id -ceq $Id })
    if ($records.Count -ne 1) {
        Fail-BattleNetworkProfile "profile parameter lookup is not unique"
    }
    return $records[0]
}

# Get-ParameterValue 返回已经过整数门验证的参数值。
function Get-ParameterValue {
    param([object]$Profile, [string]$Id)
    return [int64](Get-ParameterRecord $Profile $Id).value
}

# Get-WorkloadPlayers 返回 profile 内唯一 workload 的 actor 数。
function Get-WorkloadPlayers {
    param([object]$Profile, [string]$WorkloadId)
    $records = @($Profile.workloads | Where-Object {
            $_.workload_id -ceq $WorkloadId
        })
    if ($records.Count -ne 1) {
        Fail-BattleNetworkProfile "profile workload lookup is not unique"
    }
    return [int]$records[0].players
}

# Get-PhaseWeight 为纯网络 byte/event 模型提供固定负载倍率，不执行 gameplay 规则。
function Get-PhaseWeight {
    param([string]$Phase)
    switch ($Phase) {
        "idle" { 1 }
        "movement-heavy" { 2 }
        "combat-heavy" { 3 }
        "boss-burst" { 4 }
        "disconnect-drain" { 2 }
        default { Fail-BattleNetworkProfile "unknown workload phase" }
    }
}

# Get-StableSeed 把场景 key 映射为固定 32-bit seed，避免依赖进程 hash 随机化。
function Get-StableSeed {
    param([uint64]$BaseSeed, [string]$Key)
    $digest = Get-Sha256Text $Key
    $prefix = $digest.Substring(0, 8)
    $keySeed = [Convert]::ToUInt32($prefix, 16)
    return ([uint64]$BaseSeed + [uint64]$keySeed) % [uint64]4294967296
}

# Get-NextRandom 实现固定 LCG32，并通过引用显式持有每个场景的独立随机流。
function Get-NextRandom {
    param([ref]$State)
    $next = (([uint64]1664525 * [uint64]$State.Value) +
            [uint64]1013904223) % [uint64]4294967296
    $State.Value = $next
    return [uint32]$next
}

# New-NetworkCopy 根据 fault 参数产生一个可稳定排序的逻辑投递副本。
function New-NetworkCopy {
    param(
        [object]$Scenario,
        [ref]$RandomState,
        [string]$EventId,
        [string]$Direction,
        [string]$Lane,
        [int64]$Sequence,
        [int]$CopyIndex,
        [int64]$SendTimeUs,
        [int]$PayloadBytes,
        [int64]$DeadlineUs,
        [bool]$ForceRetry = $false
    )
    $lossRoll = [int]([uint64](Get-NextRandom $RandomState) % 100)
    $jitterRange = ([int64]$Scenario.jitter_us * 2) + 1
    $jitter = 0
    if ($jitterRange -gt 1) {
        $jitter = ([int64](Get-NextRandom $RandomState) % $jitterRange) -
            [int64]$Scenario.jitter_us
    }
    $reorder = ([int]([uint64](Get-NextRandom $RandomState) % 100)) -lt
        [int]$Scenario.reorder_percent
    $burstDrop = $false
    if ([int]$Scenario.burst_interval -gt 0) {
        $burstOffset = [int]($Sequence % [int]$Scenario.burst_interval)
        $burstDrop = $burstOffset -lt [int]$Scenario.burst_length
    }
    $dropped = -not $ForceRetry -and (
        $lossRoll -lt [int]$Scenario.loss_percent -or $burstDrop)
    $delayUs = [int64]$Scenario.base_latency_us + $jitter
    if ($reorder) {
        $delayUs = [Math]::Max(0, $delayUs - 20000)
    }
    return [pscustomobject]@{
        EventId = $EventId
        Direction = $Direction
        Lane = $Lane
        Sequence = $Sequence
        CopyIndex = $CopyIndex
        SendTimeUs = $SendTimeUs
        DeliveryTimeUs = $SendTimeUs + $delayUs
        PayloadBytes = $PayloadBytes
        DeadlineUs = $DeadlineUs
        Dropped = [bool]$dropped
        DuplicateRequested = (
            ([int]([uint64](Get-NextRandom $RandomState) % 100)) -lt
            [int]$Scenario.duplicate_percent)
    }
}

# Add-LogicalEvent 生成 raw redundancy/duplicate 或有限 KCP retry，返回全部发送副本。
function Add-LogicalEvent {
    param(
        [System.Collections.ArrayList]$Copies,
        [object]$Scenario,
        [ref]$RandomState,
        [string]$EventId,
        [string]$Direction,
        [string]$Lane,
        [int64]$Sequence,
        [int64]$SendTimeUs,
        [int]$PayloadBytes,
        [int64]$ExpiryUs,
        [int]$Redundancy,
        [object]$KcpProfile
    )
    $copyIndex = 0
    $deliveredAttempt = $false
    $attemptLimit = if ($Lane -eq "kcp") {
        [Math]::Min(4, [int]$KcpProfile.dead_link_retries)
    } else {
        $Redundancy
    }
    for ($attempt = 0; $attempt -lt $attemptLimit; $attempt++) {
        $attemptSend = $SendTimeUs
        if ($Lane -eq "kcp") {
            $attemptSend += [int64]$attempt * [int64]$KcpProfile.rto_min_us
        }
        $copy = New-NetworkCopy $Scenario $RandomState $EventId $Direction `
            $Lane $Sequence $copyIndex $attemptSend $PayloadBytes `
            ($SendTimeUs + $ExpiryUs) $deliveredAttempt
        $Copies.Add($copy) | Out-Null
        $copyIndex++
        if (-not $copy.Dropped) {
            $deliveredAttempt = $true
            if ($Lane -eq "kcp") {
                break
            }
        }
        if ($copy.DuplicateRequested -and -not $copy.Dropped) {
            $duplicate = [pscustomobject]@{
                EventId = $copy.EventId
                Direction = $copy.Direction
                Lane = $copy.Lane
                Sequence = $copy.Sequence
                CopyIndex = $copyIndex
                SendTimeUs = $copy.SendTimeUs
                DeliveryTimeUs = $copy.DeliveryTimeUs + 1000
                PayloadBytes = $copy.PayloadBytes
                DeadlineUs = $copy.DeadlineUs
                Dropped = $false
                DuplicateRequested = $false
            }
            $Copies.Add($duplicate) | Out-Null
            $copyIndex++
        }
    }
}

# Invoke-ScenarioSimulation 重放一个 workload/phase 的有限逻辑事件并聚合网络指标。
function Invoke-ScenarioSimulation {
    param(
        [object]$Profile,
        [object]$Scenario,
        [string]$WorkloadId,
        [string]$Phase,
        [uint64]$BaseSeed
    )
    $players = Get-WorkloadPlayers $Profile $WorkloadId
    # compatibility workload 只重放至多八条同构玩家流，再按实际人数扩展 instance bytes；
    # 这样保持 per-player fault 行为与容量预算，同时避免 profile 工具把 33 人复制成性能基准。
    $simulatedPlayers = [Math]::Min($players, 8)
    $phaseWeight = Get-PhaseWeight $Phase
    $simulationStepUs = [int64]((Get-ParameterValue $Profile "simulation-step-ns") / 1000)
    $inputStepUs = [int64]((Get-ParameterValue $Profile "input-step-ns") / 1000)
    $inputPerSimulationTick = [int]($simulationStepUs / $inputStepUs)
    $snapshotInterval = [int](Get-ParameterValue $Profile "snapshot-interval-ticks")
    $fullInterval = [int](Get-ParameterValue $Profile "full-baseline-interval-snapshots")
    $inputRedundancy = [int](Get-ParameterValue $Profile "input-bundle-redundancy")
    $seedKey = "$($Scenario.scenario_id)|$WorkloadId|$Phase"
    $randomState = Get-StableSeed $BaseSeed $seedKey
    $copies = [System.Collections.ArrayList]::new()
    $sequence = [int64]0
    $originalReliableBytes = [int64]0

    for ($tick = 1; $tick -le [int]$Scenario.duration_ticks; $tick++) {
        $tickStartUs = [int64]($tick - 1) * $simulationStepUs
        for ($player = 1; $player -le $simulatedPlayers; $player++) {
            for ($sample = 0; $sample -lt $inputPerSimulationTick; $sample++) {
                $sequence++
                $inputSendUs = $tickStartUs + ([int64]$sample * $inputStepUs)
                Add-LogicalEvent $copies $Scenario ([ref]$randomState) `
                    "input-$player-$tick-$sample" "c2s" "raw" $sequence `
                    $inputSendUs 160 300000 $inputRedundancy $Profile.kcp_profile
            }
        }
        if (($tick % $snapshotInterval) -eq 0) {
            $snapshotOrdinal = [int]($tick / $snapshotInterval)
            $isFull = ($snapshotOrdinal % $fullInterval) -eq 0
            $snapshotPayload = if ($isFull) {
                [Math]::Min(1040, 200 + ($players * 30 * $phaseWeight))
            } else {
                [Math]::Min(900, 120 + ($players * 20 * $phaseWeight))
            }
            for ($player = 1; $player -le $simulatedPlayers; $player++) {
                $sequence++
                $kind = if ($isFull) { "full" } else { "delta" }
                Add-LogicalEvent $copies $Scenario ([ref]$randomState) `
                    "snapshot-$kind-$player-$tick" "s2c" "raw" $sequence `
                    $tickStartUs $snapshotPayload 500000 1 $Profile.kcp_profile
            }
        }
        if (($tick % 10) -eq 0) {
            for ($player = 1; $player -le $simulatedPlayers; $player++) {
                $sequence++
                $payload = 256
                $originalReliableBytes += $payload
                Add-LogicalEvent $copies $Scenario ([ref]$randomState) `
                    "reliable-$player-$tick" "s2c" "kcp" $sequence `
                    $tickStartUs $payload `
                    $ReliableRouteExpiryMicroseconds 1 `
                    $Profile.kcp_profile
            }
        }
    }

    $ordered = @($copies | Sort-Object `
        @{ Expression = { [int64]$_.DeliveryTimeUs } },
        @{ Expression = { [string]$_.Direction } },
        @{ Expression = { [string]$_.Lane } },
        @{ Expression = { [int64]$_.Sequence } },
        @{ Expression = { [int]$_.CopyIndex } })
    $bucketCounts = @{}
    $deliveredEvents = @{}
    $expiredEvents = @{}
    $bytesUp = [int64]0
    $bytesDown = [int64]0
    $reliableSentBytes = [int64]0
    $droppedCopies = 0
    $queueRejectedCopies = 0
    $duplicateSuppressed = 0
    $maxAgeUs = [int64]0
    $queueHighWatermark = 0
    foreach ($copy in $ordered) {
        if ($copy.Direction -eq "c2s") {
            $bytesUp += [int64]$copy.PayloadBytes
        } else {
            $bytesDown += [int64]$copy.PayloadBytes
        }
        if ($copy.Lane -eq "kcp") {
            $reliableSentBytes += [int64]$copy.PayloadBytes
        }
        if ($copy.Dropped) {
            $droppedCopies++
            continue
        }
        $bucket = [string][Math]::Floor(
            [decimal]$copy.DeliveryTimeUs / [decimal]10000)
        if (-not $bucketCounts.ContainsKey($bucket)) {
            $bucketCounts[$bucket] = 0
        }
        $bucketCounts[$bucket]++
        $queueHighWatermark = [Math]::Max(
            $queueHighWatermark, [int]$bucketCounts[$bucket])
        if ([int]$bucketCounts[$bucket] -gt [int]$Scenario.queue_limit) {
            $queueRejectedCopies++
            continue
        }
        if ([int64]$copy.DeliveryTimeUs -gt [int64]$copy.DeadlineUs) {
            $expiredEvents[$copy.EventId] = $true
            continue
        }
        if ($deliveredEvents.ContainsKey($copy.EventId)) {
            $duplicateSuppressed++
            continue
        }
        $deliveredEvents[$copy.EventId] = $true
        $age = [int64]$copy.DeliveryTimeUs - [int64]$copy.SendTimeUs
        $maxAgeUs = [Math]::Max($maxAgeUs, $age)
    }

    $durationUs = [int64]$Scenario.duration_ticks * $simulationStepUs
    $durationSeconds = [decimal]$durationUs / [decimal]1000000
    $upPerPlayer = [int64][Math]::Ceiling(
        ([decimal]$bytesUp / [decimal]$simulatedPlayers) / $durationSeconds)
    $downPerPlayer = [int64][Math]::Ceiling(
        ([decimal]$bytesDown / [decimal]$simulatedPlayers) / $durationSeconds)
    $downPerInstance = [int64]$downPerPlayer * [int64]$players
    $amplificationBasisPoints = 10000
    if ($originalReliableBytes -gt 0) {
        $amplificationBasisPoints = [int64][Math]::Ceiling(
            ([decimal]$reliableSentBytes / [decimal]$originalReliableBytes) *
            [decimal]10000)
    }
    $baselineRecoveryUs = 0
    if ("baseline-gap" -in @($Scenario.impairments)) {
        $baselineRecoveryUs = (
            [int64]$fullInterval * [int64]$snapshotInterval *
            $simulationStepUs) + [int64]$Scenario.base_latency_us +
            [int64]$Scenario.jitter_us
    }
    $outcome = if ($players -gt [int]$Profile.capacity.qualified_max_players) {
        "capacity-gated"
    } else {
        "qualified"
    }
    return [pscustomobject][ordered]@{
        scenario_id = [string]$Scenario.scenario_id
        workload_id = $WorkloadId
        phase = $Phase
        outcome = $outcome
        events_delivered = [int]$deliveredEvents.Count
        dropped_copies = [int]$droppedCopies
        queue_rejected_copies = [int]$queueRejectedCopies
        expired_events = [int]$expiredEvents.Count
        duplicate_suppressed = [int]$duplicateSuppressed
        queue_high_watermark = [int][Math]::Min(
            $queueHighWatermark, [int]$Scenario.queue_limit)
        max_age_us = $maxAgeUs
        baseline_recovery_us = [int64]$baselineRecoveryUs
        up_bytes_per_player_second = $upPerPlayer
        down_bytes_per_player_second = $downPerPlayer
        down_bytes_per_instance_second = $downPerInstance
        kcp_amplification_basis_points = $amplificationBasisPoints
    }
}

# Get-CandidateEvaluation 以固定 hard gates 与 score 评估 cadence/window 候选。
function Get-CandidateEvaluation {
    param([object]$Profile)
    $evaluations = @()
    foreach ($candidate in @($Profile.candidates)) {
        $reason = "qualified"
        $disposition = "qualified"
        if ([int64]$candidate.simulation_step_ns -gt 50000000) {
            $disposition = "rejected"
            $reason = "simulation-step-budget"
        } elseif (
            ([int64]$candidate.simulation_step_ns %
                [int64]$candidate.input_step_ns) -ne 0) {
            $disposition = "rejected"
            $reason = "non-integral-input-ratio"
        } elseif (
            ([int64]$candidate.snapshot_interval_ticks *
                [int64]$candidate.simulation_step_ns) -gt 100000000) {
            $disposition = "rejected"
            $reason = "snapshot-age-budget"
        }
        $score = [int64]$candidate.snapshot_interval_ticks *
            [int64]$candidate.simulation_step_ns +
            [int64]$candidate.history_window_ticks * 1000
        $evaluations += [pscustomobject][ordered]@{
            candidate_id = [string]$candidate.candidate_id
            disposition = $disposition
            reason = $reason
            score = $score
        }
    }
    $qualified = @($evaluations | Where-Object {
            $_.disposition -eq "qualified"
        } | Sort-Object score, candidate_id)
    if ($qualified.Count -eq 0) {
        Fail-BattleNetworkProfile "no cadence candidate passed hard gates"
    }
    $selectedId = [string]$qualified[0].candidate_id
    foreach ($evaluation in $evaluations) {
        if ($evaluation.candidate_id -eq $selectedId) {
            $evaluation.disposition = "selected"
            $evaluation.reason = "lowest-qualified-score"
        } elseif ($evaluation.disposition -eq "qualified") {
            $evaluation.disposition = "rejected"
            $evaluation.reason = "dominated-qualified-score"
        }
    }
    return @($evaluations | Sort-Object candidate_id)
}

# Get-SourceDigest 绑定所有非派生 profile 输入，排除 manifest 与 qualification report 的循环摘要。
function Get-SourceDigest {
    param([string[]]$CasePaths)
    $sourcePaths = @(
        "fault-matrix.json",
        "message-inventory.json",
        "model-binding.json",
        "profile.json"
    ) + @($CasePaths | Sort-Object)
    $records = foreach ($relative in $sourcePaths) {
        $path = Resolve-ProfilePath $relative
        "{0}:{1}" -f $relative, (Get-Sha256File $path)
    }
    return Get-Sha256Text ($records -join "`n")
}

# New-QualificationReport 重放完整矩阵并创建低敏、稳定顺序的内存报告。
function New-QualificationReport {
    param(
        [object]$Profile,
        [object]$FaultMatrix,
        [string[]]$CasePaths
    )
    $candidateResults = @(Get-CandidateEvaluation $Profile)
    $results = @()
    foreach ($scenario in @($FaultMatrix.scenarios)) {
        foreach ($workload in @($scenario.workloads)) {
            foreach ($phase in @($scenario.phases)) {
                $result = Invoke-ScenarioSimulation $Profile $scenario `
                    ([string]$workload) ([string]$phase) `
                    ([uint64]$FaultMatrix.seed)
                if ($result.outcome -cne [string]$scenario.expected_outcome) {
                    Fail-BattleNetworkProfile "scenario outcome differs"
                }
                $results += $result
            }
        }
    }
    $results = @($results | Sort-Object scenario_id, workload_id, phase)
    $resultJson = $results | ConvertTo-Json -Compress -Depth 20
    $implementationRequired = @(
        $Profile.parameters |
        Where-Object { $_.classification -eq "implementation_required" } |
        ForEach-Object { [string]$_.id })
    return [pscustomobject][ordered]@{
        format_version = $FormatVersion
        profile_version = $ProfileVersion
        document_kind = "qualification-report"
        tool_version = [string]$Profile.tool_version
        profile_status = "qualified"
        source_digest = Get-SourceDigest $CasePaths
        seed = [int64]$FaultMatrix.seed
        candidate_results = $candidateResults
        coverage = [pscustomobject][ordered]@{
            impairments = @($FaultMatrix.required_impairments)
            workloads = @($FaultMatrix.required_workloads)
            phases = @($FaultMatrix.required_phases)
        }
        thresholds = [pscustomobject][ordered]@{
            default_players_minimum = 5
            qualified_players_maximum = [int]$Profile.capacity.qualified_max_players
            input_age_maximum_us = (
                Get-ParameterValue $Profile "input-late-window-ticks") *
                ((Get-ParameterValue $Profile "simulation-step-ns") / 1000)
            baseline_recovery_maximum_us = (
                Get-ParameterValue $Profile "maximum-baseline-age-ticks") *
                ((Get-ParameterValue $Profile "simulation-step-ns") / 1000)
            up_bytes_per_player_second_maximum = Get-ParameterValue `
                $Profile "bandwidth-up-per-player-target"
            down_bytes_per_player_second_maximum = Get-ParameterValue `
                $Profile "bandwidth-down-per-player-target"
            down_bytes_per_instance_second_maximum = Get-ParameterValue `
                $Profile "bandwidth-down-per-instance-target"
            kcp_amplification_basis_points_maximum = 30000
        }
        capacity = [pscustomobject][ordered]@{
            qualified_default_players = [int]$Profile.capacity.qualified_default_players
            qualified_max_players = [int]$Profile.capacity.qualified_max_players
            evaluated_max_players = [int]$Profile.capacity.evaluated_max_players
            visit_configured_max_players = [int]$Profile.capacity.visit_configured_max_players
            capacity_gate_required = [bool]$Profile.capacity.capacity_gate_required
            capacity_gate_owner = [string]$Profile.capacity.capacity_gate_owner
            capacity_gate_status = [string]$Profile.capacity.capacity_gate_status
        }
        implementation_required = $implementationRequired
        result_count = [int]$results.Count
        results = $results
        result_digest = Get-Sha256Text $resultJson
        missing = @()
        skipped = @()
        stale = @()
        unclassified = @()
    }
}

# Assert-ProfileStructure 验证 profile、参数分类、candidate、MTU/KCP 和容量关系。
function Assert-ProfileStructure {
    param([object]$Profile)
    Assert-ClosedProperties $Profile @(
        "format_version", "profile_version", "document_kind", "tool_version",
        "profile_status", "selected_candidate_id", "candidates", "parameters",
        "mtu_budget", "kcp_profile", "capacity", "workloads", "report_path"
    ) @(
        "format_version", "profile_version", "document_kind", "tool_version",
        "profile_status", "selected_candidate_id", "candidates", "parameters",
        "mtu_budget", "kcp_profile", "capacity", "workloads", "report_path"
    ) "profile"
    Assert-DocumentEnvelope $Profile "profile" "profile"
    if ([string]$Profile.profile_status -cne "qualified") {
        Fail-BattleNetworkProfile "profile status is not qualified"
    }
    Assert-UniqueSortedObjects @($Profile.candidates) {
        param($candidate) $candidate.candidate_id
    } "profile candidates"
    foreach ($candidate in @($Profile.candidates)) {
        Assert-ClosedProperties $candidate @(
            "candidate_id", "simulation_step_ns", "input_step_ns",
            "snapshot_interval_ticks", "history_window_ticks",
            "expected_disposition", "expected_reason"
        ) @(
            "candidate_id", "simulation_step_ns", "input_step_ns",
            "snapshot_interval_ticks", "history_window_ticks",
            "expected_disposition", "expected_reason"
        ) "profile candidate"
        Assert-NonNegativeInteger $candidate.simulation_step_ns `
            "candidate simulation step" $false
        Assert-NonNegativeInteger $candidate.input_step_ns `
            "candidate input step" $false
    }
    Assert-UniqueSortedObjects @($Profile.parameters) {
        param($parameter) $parameter.id
    } "profile parameters"
    foreach ($parameter in @($Profile.parameters)) {
        Assert-ClosedProperties $parameter @(
            "id", "value", "unit", "classification", "workloads", "evidence"
        ) @(
            "id", "value", "unit", "classification", "workloads", "evidence"
        ) "profile parameter"
        Assert-NonNegativeInteger $parameter.value "profile parameter value"
        if ([string]$parameter.unit -notin $AllowedUnits -or
            [string]$parameter.classification -notin $AllowedClassifications) {
            Fail-BattleNetworkProfile "profile parameter metadata differs"
        }
        $parameterId = [string]$parameter.id
        if ($parameterId -match '(?:measurement|parity)$') {
            if ([string]$parameter.classification -cne
                    "implementation_required" -or
                [int64]$parameter.value -ne 0) {
                Fail-BattleNetworkProfile "implementation evidence is misclassified"
            }
        } elseif ($parameterId -match '-target$' -and
            [string]$parameter.classification -cne "target_budget") {
            Fail-BattleNetworkProfile "target budget is misclassified"
        }
        Assert-UniqueSortedStrings @($parameter.workloads) `
            "profile parameter workloads"
        Assert-UniqueSortedStrings @($parameter.evidence) `
            "profile parameter evidence"
    }
    $evaluations = @(Get-CandidateEvaluation $Profile)
    foreach ($expected in @($Profile.candidates)) {
        $actual = @($evaluations | Where-Object {
                $_.candidate_id -ceq $expected.candidate_id
            })
        if ($actual.Count -ne 1 -or
            [string]$actual[0].disposition -cne
                [string]$expected.expected_disposition -or
            [string]$actual[0].reason -cne [string]$expected.expected_reason) {
            Fail-BattleNetworkProfile "candidate evidence differs"
        }
    }
    if ([string]$Profile.selected_candidate_id -cne "candidate-20hz" -or
        (Get-ParameterValue $Profile "simulation-step-ns") -ne 50000000 -or
        (Get-ParameterValue $Profile "input-step-ns") -ne 25000000) {
        Fail-BattleNetworkProfile "selected cadence differs"
    }
    if ((Get-ParameterValue $Profile "history-window-ticks") -lt
        ((Get-ParameterValue $Profile "input-late-window-ticks") +
            (Get-ParameterValue $Profile "input-gap-expiry-ticks"))) {
        Fail-BattleNetworkProfile "history window is smaller than input recovery"
    }
    Assert-ClosedProperties $Profile.mtu_budget @(
        "max_datagram_bytes", "ip_udp_overhead_bytes",
        "secure_session_header_bytes", "aead_tag_bytes",
        "raw_lane_header_bytes", "kcp_lane_header_bytes",
        "raw_max_logical_payload_bytes", "kcp_max_logical_payload_bytes",
        "fragmentation_policy", "wire_parity_status"
    ) @(
        "max_datagram_bytes", "ip_udp_overhead_bytes",
        "secure_session_header_bytes", "aead_tag_bytes",
        "raw_lane_header_bytes", "kcp_lane_header_bytes",
        "raw_max_logical_payload_bytes", "kcp_max_logical_payload_bytes",
        "fragmentation_policy", "wire_parity_status"
    ) "MTU budget"
    $mtu = $Profile.mtu_budget
    $rawPayload = [int64]$mtu.max_datagram_bytes -
        [int64]$mtu.ip_udp_overhead_bytes -
        [int64]$mtu.secure_session_header_bytes -
        [int64]$mtu.aead_tag_bytes -
        [int64]$mtu.raw_lane_header_bytes
    $kcpPayload = [int64]$mtu.max_datagram_bytes -
        [int64]$mtu.ip_udp_overhead_bytes -
        [int64]$mtu.secure_session_header_bytes -
        [int64]$mtu.aead_tag_bytes -
        [int64]$mtu.kcp_lane_header_bytes
    if ($rawPayload -ne [int64]$mtu.raw_max_logical_payload_bytes -or
        $kcpPayload -ne [int64]$mtu.kcp_max_logical_payload_bytes -or
        [string]$mtu.fragmentation_policy -cne "forbidden" -or
        [string]$mtu.wire_parity_status -cne "implementation-required") {
        Fail-BattleNetworkProfile "MTU budget arithmetic differs"
    }
    Assert-ClosedProperties $Profile.kcp_profile @(
        "conversation_scope", "update_interval_us", "no_delay",
        "send_window_segments", "receive_window_segments", "fast_resend",
        "rto_min_us", "rto_max_us", "dead_link_retries",
        "segment_ceiling_bytes", "message_ceiling_bytes",
        "queue_limit_messages", "maximum_route_expiry_us",
        "sender_expiry_policy", "receiver_reassembly_policy",
        "caller_override_policy",
        "adapter_parity_status"
    ) @(
        "conversation_scope", "update_interval_us", "no_delay",
        "send_window_segments", "receive_window_segments", "fast_resend",
        "rto_min_us", "rto_max_us", "dead_link_retries",
        "segment_ceiling_bytes", "message_ceiling_bytes",
        "queue_limit_messages", "maximum_route_expiry_us",
        "sender_expiry_policy", "receiver_reassembly_policy",
        "caller_override_policy",
        "adapter_parity_status"
    ) "KCP profile"
    if ([int]$Profile.kcp_profile.segment_ceiling_bytes -gt $kcpPayload -or
        [int]$Profile.kcp_profile.message_ceiling_bytes -gt $kcpPayload -or
        [int]$Profile.kcp_profile.rto_min_us -gt
            [int]$Profile.kcp_profile.rto_max_us -or
        [int]$Profile.kcp_profile.queue_limit_messages -le 0 -or
        [int]$Profile.kcp_profile.queue_limit_messages -gt 256 -or
        [string]$Profile.kcp_profile.adapter_parity_status -cne
            "implementation-required") {
        Fail-BattleNetworkProfile "KCP profile budget differs"
    }
    if ([int64]$Profile.kcp_profile.maximum_route_expiry_us -ne
            $MaximumRouteExpiryMicroseconds -or
        [string]$Profile.kcp_profile.sender_expiry_policy -cne
            "immutable-route-enqueue-deadline" -or
        [string]$Profile.kcp_profile.receiver_reassembly_policy -cne
            "kcp-window-and-session-lifecycle" -or
        [string]$Profile.kcp_profile.caller_override_policy -cne
            "forbidden") {
        Fail-BattleNetworkProfile "KCP expiry ownership differs"
    }
    Assert-ClosedProperties $Profile.capacity @(
        "qualified_default_players", "qualified_max_players",
        "evaluated_max_players", "visit_configured_max_players",
        "capacity_gate_required", "capacity_gate_owner",
        "capacity_gate_status"
    ) @(
        "qualified_default_players", "qualified_max_players",
        "evaluated_max_players", "visit_configured_max_players",
        "capacity_gate_required", "capacity_gate_owner",
        "capacity_gate_status"
    ) "capacity"
    if ([int]$Profile.capacity.qualified_default_players -lt 5 -or
        [int]$Profile.capacity.qualified_max_players -lt 5 -or
        [int]$Profile.capacity.evaluated_max_players -ne 33 -or
        [int]$Profile.capacity.visit_configured_max_players -ne 33 -or
        -not [bool]$Profile.capacity.capacity_gate_required -or
        [string]$Profile.capacity.capacity_gate_status -cne
            "required-not-implemented") {
        Fail-BattleNetworkProfile "capacity gate differs"
    }
    Assert-UniqueSortedObjects @($Profile.workloads) {
        param($workload) $workload.workload_id
    } "profile workloads"
    Assert-ExactSet @($Profile.workloads.workload_id) $RequiredWorkloads `
        "profile workload inventory"
}

# Assert-MessageInventory 验证 logical kind 的唯一 lane、payload 和 snapshot/KCP 禁止项。
function Assert-MessageInventory {
    param([object]$Inventory, [object]$Profile)
    Assert-ClosedProperties $Inventory @(
        "format_version", "profile_version", "document_kind", "owner",
        "numeric_registry_status", "messages"
    ) @(
        "format_version", "profile_version", "document_kind", "owner",
        "numeric_registry_status", "messages"
    ) "message inventory"
    Assert-DocumentEnvelope $Inventory "message-inventory" "message inventory"
    if ([string]$Inventory.owner -cne "battle-network-profile" -or
        [string]$Inventory.numeric_registry_status -cne "not-allocated") {
        Fail-BattleNetworkProfile "message inventory ownership differs"
    }
    Assert-UniqueSortedObjects @($Inventory.messages) {
        param($message) $message.kind
    } "message inventory kinds"
    foreach ($message in @($Inventory.messages)) {
        Assert-ClosedProperties $message @(
            "kind", "owner", "direction", "lane", "qos",
            "max_logical_payload_bytes", "maximum_rate_per_second",
            "expiry_us", "sequence_policy", "tick_policy", "idempotency",
            "baseline_policy", "recovery_policy", "split_policy"
        ) @(
            "kind", "owner", "direction", "lane", "qos",
            "max_logical_payload_bytes", "maximum_rate_per_second",
            "expiry_us", "sequence_policy", "tick_policy", "idempotency",
            "baseline_policy", "recovery_policy", "split_policy"
        ) "message inventory entry"
        if ([string]$message.lane -notin $AllowedLanes -or
            [string]$message.direction -notin @("c2s", "s2c")) {
            Fail-BattleNetworkProfile "message lane or direction differs"
        }
        $laneLimit = if ($message.lane -eq "raw") {
            [int]$Profile.mtu_budget.raw_max_logical_payload_bytes
        } else {
            [int]$Profile.mtu_budget.kcp_max_logical_payload_bytes
        }
        if ([int]$message.max_logical_payload_bytes -gt $laneLimit) {
            Fail-BattleNetworkProfile "message payload exceeds lane budget"
        }
        if ([string]$message.kind -match '^battle\.snapshot\.' -and
            [string]$message.lane -ne "raw") {
            Fail-BattleNetworkProfile "snapshot must not use KCP"
        }
        if ([string]$message.kind -in @("battle.input.bundle", "battle.probe") -and
            [string]$message.lane -ne "raw") {
            Fail-BattleNetworkProfile "replaceable message must use raw lane"
        }
        if ([string]$message.lane -eq "kcp" -and
            ([int64]$message.expiry_us -le 0 -or
                [int64]$message.expiry_us -gt
                    [int64]$Profile.kcp_profile.maximum_route_expiry_us -or
                [string]$message.recovery_policy -ceq
                    "deliver-after-expiry")) {
            Fail-BattleNetworkProfile "reliable expiry policy differs"
        }
        $expectedExpiry = switch ([string]$message.kind) {
            "battle.ability.reliable-event" {
                $ReliableRouteExpiryMicroseconds
                break
            }
            "battle.entity.lifecycle" {
                $ReliableRouteExpiryMicroseconds
                break
            }
            "battle.resync.request" {
                $ResyncRouteExpiryMicroseconds
                break
            }
            "battle.resync.response" {
                $ResyncRouteExpiryMicroseconds
                break
            }
            default {
                $null
            }
        }
        if ($null -ne $expectedExpiry -and
            [int64]$message.expiry_us -ne [int64]$expectedExpiry) {
            Fail-BattleNetworkProfile "KCP route expiry differs"
        }
    }
}

# Assert-FaultMatrix 验证固定 seed、场景排序和 impairment/workload/phase 全覆盖。
function Assert-FaultMatrix {
    param([object]$Matrix)
    Assert-ClosedProperties $Matrix @(
        "format_version", "profile_version", "document_kind", "prng",
        "seed", "required_impairments", "required_workloads",
        "required_phases", "scenarios"
    ) @(
        "format_version", "profile_version", "document_kind", "prng",
        "seed", "required_impairments", "required_workloads",
        "required_phases", "scenarios"
    ) "fault matrix"
    Assert-DocumentEnvelope $Matrix "fault-matrix" "fault matrix"
    if ([string]$Matrix.prng -cne "lcg32-numerical-recipes") {
        Fail-BattleNetworkProfile "fault matrix PRNG differs"
    }
    Assert-NonNegativeInteger $Matrix.seed "fault matrix seed" $false
    Assert-UniqueSortedStrings @($Matrix.required_impairments) `
        "required impairments"
    Assert-UniqueSortedStrings @($Matrix.required_workloads) `
        "required workloads"
    Assert-UniqueSortedStrings @($Matrix.required_phases) "required phases"
    Assert-ExactSet @($Matrix.required_workloads) $RequiredWorkloads `
        "required workload coverage"
    Assert-ExactSet @($Matrix.required_phases) $RequiredPhases `
        "required phase coverage"
    Assert-UniqueSortedObjects @($Matrix.scenarios) {
        param($scenario) $scenario.scenario_id
    } "fault scenarios"
    $coveredImpairments = @{}
    $coveredPairs = @{}
    foreach ($scenario in @($Matrix.scenarios)) {
        Assert-ClosedProperties $scenario @(
            "scenario_id", "workloads", "phases", "impairments",
            "duration_ticks", "base_latency_us", "jitter_us",
            "loss_percent", "duplicate_percent", "reorder_percent",
            "burst_interval", "burst_length", "queue_limit",
            "expected_outcome"
        ) @(
            "scenario_id", "workloads", "phases", "impairments",
            "duration_ticks", "base_latency_us", "jitter_us",
            "loss_percent", "duplicate_percent", "reorder_percent",
            "burst_interval", "burst_length", "queue_limit",
            "expected_outcome"
        ) "fault scenario"
        Assert-UniqueSortedStrings @($scenario.workloads) `
            "fault scenario workloads"
        Assert-UniqueSortedStrings @($scenario.phases) "fault scenario phases"
        Assert-UniqueSortedStrings @($scenario.impairments) `
            "fault scenario impairments"
        foreach ($field in @(
                "duration_ticks", "base_latency_us", "jitter_us",
                "loss_percent", "duplicate_percent", "reorder_percent",
                "burst_interval", "burst_length", "queue_limit")) {
            Assert-NonNegativeInteger $scenario.$field "fault scenario integer"
        }
        if ([int]$scenario.duration_ticks -eq 0 -or
            [int]$scenario.queue_limit -eq 0 -or
            [int]$scenario.loss_percent -gt 100 -or
            [int]$scenario.duplicate_percent -gt 100 -or
            [int]$scenario.reorder_percent -gt 100 -or
            [string]$scenario.expected_outcome -notin @(
                "capacity-gated", "qualified")) {
            Fail-BattleNetworkProfile "fault scenario bounds differ"
        }
        foreach ($impairment in @($scenario.impairments)) {
            $coveredImpairments[[string]$impairment] = $true
        }
        foreach ($workload in @($scenario.workloads)) {
            foreach ($phase in @($scenario.phases)) {
                $coveredPairs["$workload|$phase"] = $true
            }
        }
    }
    Assert-ExactSet @($coveredImpairments.Keys) @($Matrix.required_impairments) `
        "fault impairment coverage"
    foreach ($workload in $RequiredWorkloads) {
        foreach ($phase in $RequiredPhases) {
            if (-not $coveredPairs.ContainsKey("$workload|$phase")) {
                Fail-BattleNetworkProfile "fault workload phase coverage is missing"
            }
        }
    }
}

# Assert-ModelBinding 验证 model validator、manifest/assumptions 与每个 case 摘要无漂移。
function Assert-ModelBinding {
    param([object]$Binding)
    Assert-ClosedProperties $Binding @(
        "format_version", "profile_version", "document_kind",
        "model_format_version", "model_version", "manifest_path",
        "manifest_sha256", "assumptions_path", "assumptions_sha256",
        "required_requirements", "cases"
    ) @(
        "format_version", "profile_version", "document_kind",
        "model_format_version", "model_version", "manifest_path",
        "manifest_sha256", "assumptions_path", "assumptions_sha256",
        "required_requirements", "cases"
    ) "model binding"
    Assert-DocumentEnvelope $Binding "model-binding" "model binding"
    if ([string]$Binding.model_format_version -cne "1" -or
        [string]$Binding.model_version -cne "battle-model-v1" -or
        [string]$Binding.manifest_path -cne "../model/manifest.json" -or
        [string]$Binding.assumptions_path -cne "../model/assumptions.json") {
        Fail-BattleNetworkProfile "model binding identity differs"
    }
    $modelManifestPath = Join-Path $ModelRoot "manifest.json"
    $modelAssumptionsPath = Join-Path $ModelRoot "assumptions.json"
    if ((Get-Sha256File $modelManifestPath) -cne
            [string]$Binding.manifest_sha256 -or
        (Get-Sha256File $modelAssumptionsPath) -cne
            [string]$Binding.assumptions_sha256) {
        Fail-BattleNetworkProfile "model source digest differs"
    }
    $modelManifest = Get-Content -Raw -Encoding UTF8 `
        -LiteralPath $modelManifestPath | ConvertFrom-Json
    Assert-UniqueSortedStrings @($Binding.required_requirements) `
        "model binding requirements"
    if ((@($Binding.required_requirements) | ConvertTo-Json -Compress) -cne
        (@($modelManifest.required_requirements) | ConvertTo-Json -Compress)) {
        Fail-BattleNetworkProfile "model requirement binding differs"
    }
    Assert-UniqueSortedObjects @($Binding.cases) {
        param($case) $case.case_id
    } "model binding cases"
    if (@($Binding.cases).Count -ne @($modelManifest.cases).Count) {
        Fail-BattleNetworkProfile "model case binding count differs"
    }
    foreach ($case in @($Binding.cases)) {
        Assert-ClosedProperties $case @("case_id", "path", "file_sha256") `
            @("case_id", "path", "file_sha256") "model binding case"
        $entry = @($modelManifest.cases | Where-Object {
                $_.case_id -ceq $case.case_id
            })
        if ($entry.Count -ne 1 -or
            [string]$entry[0].path -cne [string]$case.path -or
            [string]$entry[0].file_sha256 -cne [string]$case.file_sha256) {
            Fail-BattleNetworkProfile "model case binding differs"
        }
        $modelCasePath = Join-Path $ModelRoot (
            [string]$case.path).Replace('/', '\')
        if ((Get-Sha256File $modelCasePath) -cne [string]$case.file_sha256) {
            Fail-BattleNetworkProfile "model case source digest differs"
        }
    }
}

# Assert-CaseCorpus 验证 profile cases 的闭合结构、场景引用和 requirement coverage。
function Assert-CaseCorpus {
    param(
        [object]$Manifest,
        [object]$FaultMatrix
    )
    $actualPaths = @(
        Get-ChildItem -LiteralPath (Join-Path $ProfileRoot "cases") `
            -Recurse -File -Filter "*.json" |
        ForEach-Object { Get-RelativeProfilePath $_.FullName } |
        Sort-Object)
    $manifestPaths = @($Manifest.cases | ForEach-Object {
            [string]$_.path
        })
    if (($actualPaths -join "`n") -cne ($manifestPaths -join "`n")) {
        Fail-BattleNetworkProfile "case path inventory differs"
    }
    $scenarioIds = @($FaultMatrix.scenarios.scenario_id)
    $coveredRequirements = @{}
    foreach ($entry in @($Manifest.cases)) {
        Assert-ClosedProperties $entry @(
            "case_id", "path", "category", "requirements", "workloads",
            "phases", "file_sha256"
        ) @(
            "case_id", "path", "category", "requirements", "workloads",
            "phases", "file_sha256"
        ) "manifest case"
        $path = Resolve-ProfilePath ([string]$entry.path)
        $caseText = Read-CanonicalText $path
        Assert-NoForbiddenProfileData $caseText ([string]$entry.path)
        $case = $caseText | ConvertFrom-Json
        Assert-ClosedProperties $case @(
            "format_version", "profile_version", "document_kind", "case_id",
            "category", "requirements", "scenarios", "workloads",
            "phases", "assertions"
        ) @(
            "format_version", "profile_version", "document_kind", "case_id",
            "category", "requirements", "scenarios", "workloads",
            "phases", "assertions"
        ) "profile case"
        Assert-DocumentEnvelope $case "case" "profile case"
        foreach ($arrayName in @(
                "requirements", "scenarios", "workloads", "phases",
                "assertions")) {
            Assert-UniqueSortedStrings @($case.$arrayName) `
                "profile case $arrayName"
        }
        if ([string]$case.case_id -cne [string]$entry.case_id -or
            [string]$case.category -cne [string]$entry.category -or
            (@($case.requirements) | ConvertTo-Json -Compress) -cne
                (@($entry.requirements) | ConvertTo-Json -Compress) -or
            (@($case.workloads) | ConvertTo-Json -Compress) -cne
                (@($entry.workloads) | ConvertTo-Json -Compress) -or
            (@($case.phases) | ConvertTo-Json -Compress) -cne
                (@($entry.phases) | ConvertTo-Json -Compress)) {
            Fail-BattleNetworkProfile "case manifest projection differs"
        }
        if ((Get-Sha256File $path) -cne [string]$entry.file_sha256) {
            Fail-BattleNetworkProfile "case file digest differs"
        }
        foreach ($scenario in @($case.scenarios)) {
            if ([string]$scenario -notin $scenarioIds) {
                Fail-BattleNetworkProfile "case scenario reference is missing"
            }
        }
        foreach ($requirement in @($case.requirements)) {
            $coveredRequirements[[string]$requirement] = $true
        }
    }
    Assert-ExactSet @($coveredRequirements.Keys) `
        @($Manifest.required_requirements) "profile requirement coverage"
}

# Assert-Manifest 验证 schema、source/report 文件、case 投影和所有 digest。
function Assert-Manifest {
    param([object]$Manifest)
    Assert-ClosedProperties $Manifest @(
        "format_version", "profile_version", "document_kind", "schema_path",
        "schema_sha256", "required_requirements", "files", "cases"
    ) @(
        "format_version", "profile_version", "document_kind", "schema_path",
        "schema_sha256", "required_requirements", "files", "cases"
    ) "manifest"
    Assert-DocumentEnvelope $Manifest "manifest" "manifest"
    if ([string]$Manifest.schema_path -cne "schema.json" -or
        (Get-Sha256File (Resolve-ProfilePath "schema.json")) -cne
            [string]$Manifest.schema_sha256) {
        Fail-BattleNetworkProfile "schema digest differs"
    }
    Assert-UniqueSortedStrings @($Manifest.required_requirements) `
        "manifest required requirements"
    Assert-UniqueSortedObjects @($Manifest.files) {
        param($file) $file.path
    } "manifest files"
    $expectedFiles = @(
        "fault-matrix.json",
        "message-inventory.json",
        "model-binding.json",
        "profile.json",
        "reports/qualification.json"
    )
    Assert-ExactSet @($Manifest.files.path) $expectedFiles `
        "manifest file inventory"
    foreach ($file in @($Manifest.files)) {
        Assert-ClosedProperties $file @("path", "document_kind", "file_sha256") `
            @("path", "document_kind", "file_sha256") "manifest file"
        $path = Resolve-ProfilePath ([string]$file.path)
        if ((Get-Sha256File $path) -cne [string]$file.file_sha256) {
            Fail-BattleNetworkProfile "manifest file digest differs"
        }
    }
    Assert-UniqueSortedObjects @($Manifest.cases) {
        param($case) $case.path
    } "manifest cases"
    $caseIds = @($Manifest.cases | ForEach-Object { [string]$_.case_id })
    if (@($caseIds | Select-Object -Unique).Count -ne $caseIds.Count) {
        Fail-BattleNetworkProfile "manifest case IDs are not unique"
    }
}

# Assert-QualificationReport 重算全部结果并要求 canonical evidence 完整一致。
function Assert-QualificationReport {
    param(
        [object]$Report,
        [object]$Expected
    )
    Assert-ClosedProperties $Report @(
        "format_version", "profile_version", "document_kind", "tool_version",
        "profile_status", "source_digest", "seed", "candidate_results",
        "coverage", "thresholds", "capacity", "implementation_required",
        "result_count", "results", "result_digest", "missing", "skipped",
        "stale", "unclassified"
    ) @(
        "format_version", "profile_version", "document_kind", "tool_version",
        "profile_status", "source_digest", "seed", "candidate_results",
        "coverage", "thresholds", "capacity", "implementation_required",
        "result_count", "results", "result_digest", "missing", "skipped",
        "stale", "unclassified"
    ) "qualification report"
    Assert-DocumentEnvelope $Report "qualification-report" `
        "qualification report"
    if ([string]$Report.profile_status -cne "qualified" -or
        @($Report.missing).Count -ne 0 -or
        @($Report.skipped).Count -ne 0 -or
        @($Report.stale).Count -ne 0 -or
        @($Report.unclassified).Count -ne 0) {
        Fail-BattleNetworkProfile "qualification report is incomplete"
    }
    $actualJson = $Report | ConvertTo-Json -Compress -Depth 100
    $expectedJson = $Expected | ConvertTo-Json -Compress -Depth 100
    if ($actualJson -cne $expectedJson) {
        Fail-BattleNetworkProfile "qualification report differs from simulation"
    }
    foreach ($result in @($Report.results)) {
        if ([string]$result.outcome -eq "qualified") {
            if ([int64]$result.max_age_us -gt
                    [int64]$Report.thresholds.input_age_maximum_us -or
                [int64]$result.baseline_recovery_us -gt
                    [int64]$Report.thresholds.baseline_recovery_maximum_us -or
                [int64]$result.up_bytes_per_player_second -gt
                    [int64]$Report.thresholds.up_bytes_per_player_second_maximum -or
                [int64]$result.down_bytes_per_player_second -gt
                    [int64]$Report.thresholds.down_bytes_per_player_second_maximum -or
                [int64]$result.down_bytes_per_instance_second -gt
                    [int64]$Report.thresholds.down_bytes_per_instance_second_maximum -or
                [int64]$result.kcp_amplification_basis_points -gt
                    [int64]$Report.thresholds.kcp_amplification_basis_points_maximum) {
                Fail-BattleNetworkProfile "qualified scenario exceeds threshold"
            }
        }
    }
}

# Invoke-BattleNetworkProfileValidation 组合所有只读 gates，并验证真实 report 可由 source 重放。
function Invoke-BattleNetworkProfileValidation {
    if (-not (Test-Path -LiteralPath $BattleModelValidator -PathType Leaf)) {
        Fail-BattleNetworkProfile "battle model validator is missing"
    }
    $modelOutput = @(& $BattleModelValidator -Action validate `
            -ModelRoot $ModelRoot 2>&1)
    if ($modelOutput.Count -eq 0) {
        Fail-BattleNetworkProfile "battle model validator returned no evidence"
    }
    $manifestPath = Resolve-ProfilePath "manifest.json"
    $manifestText = Read-CanonicalText $manifestPath
    Assert-NoForbiddenProfileData $manifestText "manifest.json"
    $manifest = $manifestText | ConvertFrom-Json
    Assert-Manifest $manifest

    $schemaText = Read-CanonicalText (Resolve-ProfilePath "schema.json")
    $schema = $schemaText | ConvertFrom-Json
    if ([string]$schema.'$schema' -cne
            "https://json-schema.org/draft/2020-12/schema" -or
        [bool]$schema.additionalProperties -ne $false -or
        $null -eq $schema.'$defs') {
        Fail-BattleNetworkProfile "schema root is not closed"
    }

    $bindingText = Read-CanonicalText (Resolve-ProfilePath "model-binding.json")
    Assert-NoForbiddenProfileData $bindingText "model-binding.json"
    $binding = $bindingText | ConvertFrom-Json
    Assert-ModelBinding $binding

    $profileText = Read-CanonicalText (Resolve-ProfilePath "profile.json")
    Assert-NoForbiddenProfileData $profileText "profile.json"
    $profile = $profileText | ConvertFrom-Json
    Assert-ProfileStructure $profile

    $inventoryText = Read-CanonicalText (
        Resolve-ProfilePath "message-inventory.json")
    Assert-NoForbiddenProfileData $inventoryText "message-inventory.json"
    $inventory = $inventoryText | ConvertFrom-Json
    Assert-MessageInventory $inventory $profile

    $matrixText = Read-CanonicalText (Resolve-ProfilePath "fault-matrix.json")
    Assert-NoForbiddenProfileData $matrixText "fault-matrix.json"
    $matrix = $matrixText | ConvertFrom-Json
    Assert-FaultMatrix $matrix
    Assert-CaseCorpus $manifest $matrix

    $casePaths = @($manifest.cases | ForEach-Object { [string]$_.path })
    $expectedReport = New-QualificationReport $profile $matrix $casePaths
    $reportText = Read-CanonicalText (
        Resolve-ProfilePath ([string]$profile.report_path))
    Assert-NoForbiddenProfileData $reportText ([string]$profile.report_path)
    $report = $reportText | ConvertFrom-Json
    Assert-QualificationReport $report $expectedReport

    Write-Output (
        "[OK] Battle network profile validation passed: {0} cases, {1} scenarios, {2} results." -f
        @($manifest.cases).Count, @($matrix.scenarios).Count,
        [int]$report.result_count)
}

# Invoke-BattleNetworkProfileSimulation 只读重放 source inputs，并把 canonical report 输出到 stdout。
function Invoke-BattleNetworkProfileSimulation {
    $profile = Read-JsonDocument (Resolve-ProfilePath "profile.json")
    $matrix = Read-JsonDocument (Resolve-ProfilePath "fault-matrix.json")
    Assert-ProfileStructure $profile
    Assert-FaultMatrix $matrix
    $casePaths = @(
        Get-ChildItem -LiteralPath (Join-Path $ProfileRoot "cases") `
            -Recurse -File -Filter "*.json" |
        ForEach-Object { Get-RelativeProfilePath $_.FullName } |
        Sort-Object)
    $report = New-QualificationReport $profile $matrix $casePaths
    $json = $report | ConvertTo-Json -Depth 100
    Write-Output $json
}

if ($Action -eq "validate") {
    Invoke-BattleNetworkProfileValidation
} elseif ($Action -eq "simulate") {
    Invoke-BattleNetworkProfileSimulation
}
