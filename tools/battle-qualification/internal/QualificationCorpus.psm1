Set-StrictMode -Version Latest

$script:QualificationVersion = "battle-network-qualification-v1"
$script:SimulationTickNanoseconds = 50000000
$script:InputTickNanoseconds = 25000000
$script:SnapshotIntervalTicks = 2
$script:OwnerActors = 1
$script:DefaultBattleActors = 5
$script:MaximumBattleActors = 8
$script:OverflowProbeActors = $script:MaximumBattleActors + 1
$script:CompatibilityPlayers = 33
$script:FaultMinimumWarmupMilliseconds = 5000
$script:FaultMinimumMeasurementMilliseconds = 60000
$script:FaultMinimumCleanupMilliseconds = 15000
$script:GatewayMaximumDatagramBytes = 1200
$script:GatewayGlobalQueueItems = 4096
$script:GatewayIngressQueueItems = 4096
$script:GatewayEvidenceQueueItems = 32768
$script:GatewayPacketLifetimeMilliseconds = 500
$script:GatewayReorderAdvanceMicroseconds = 10000
$script:GatewaySchedulerPollMicroseconds = 1000
$script:GatewayMappingLateDeliveryMilliseconds = 3000
$script:MaximumAttackSenders = 64
$script:MaximumAttackPacketsPerSecondPerSender = 1000
$script:MaximumAttackDurationMilliseconds = 60000
$script:MaximumAttackBytes = 134217728
$script:SoakDurationMilliseconds = 1800000
$script:SoakRekeyIntervalMilliseconds = 600000
$script:SoakMinimumObservedRekeys = 2
$script:SoakMinimumCleanupMilliseconds = 30000
$script:SessionQueueItems = 256
$script:HardTickDebt = 4
$script:SteadyStateWorkingSetGrowthBytes = 8388608
$script:RequiredDomains = @(
    "capacity",
    "entry-evidence",
    "fault-matrix",
    "lifecycle",
    "metrics",
    "redaction",
    "reproducibility",
    "security",
    "soak"
)
$script:RequiredSecurityCases = @(
    "aad-tamper",
    "ciphertext-tamper",
    "cookie-less-amplification",
    "future-sequence",
    "kcp-expiry",
    "malformed-flood",
    "old-epoch",
    "oversize-datagram",
    "proof-forgery",
    "rate-exhaustion",
    "rebind-hijack",
    "replay-duplicate",
    "spoofed-source",
    "ticket-replay",
    "too-old-sequence",
    "wrong-direction",
    "wrong-lane"
)
$script:RequiredLifecycleCases = @(
    "assignment-replacement",
    "child-crash-restart",
    "go-restart",
    "network-pause-resume",
    "owner-grace-expiry",
    "session-epoch-invalidation",
    "shutdown-drain-deadline",
    "valid-endpoint-rebind",
    "visitor-leave",
    "visitor-reconnect"
)

# Get-QualificationSha256 统一使用小写 SHA-256，避免各入口产生不同 identity。
function Get-QualificationSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Get-QualificationRelativePath 为 Windows PowerShell 5.1 提供受根目录约束的相对路径。
function Get-QualificationRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $rootPrefix = [IO.Path]::GetFullPath($Root).TrimEnd("\", "/") + [IO.Path]::DirectorySeparatorChar
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    if (-not $resolvedPath.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "qualification file 逃逸根目录"
    }
    return $resolvedPath.Substring($rootPrefix.Length).Replace("\", "/")
}

# Get-QualificationTreeDigest 绑定相对路径与文件字节，用于证明校验器不改写 source。
function Get-QualificationTreeDigest {
    param([Parameter(Mandatory = $true)][string]$QualificationRoot)

    $resolvedRoot = (Resolve-Path -LiteralPath $QualificationRoot).Path
    $records = Get-ChildItem -LiteralPath $resolvedRoot -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            $relative = Get-QualificationRelativePath -Root $resolvedRoot -Path $_.FullName
            "$relative`0$(Get-QualificationSha256 -Path $_.FullName)"
        }
    $bytes = [Text.Encoding]::UTF8.GetBytes(($records -join "`n"))
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $hash = $sha256.ComputeHash($bytes)
        return ([BitConverter]::ToString($hash) -replace "-", "").ToLowerInvariant()
    }
    finally {
        $sha256.Dispose()
    }
}

# Read-QualificationJson 以 UTF-8 读取并拒绝空文档。
function Read-QualificationJson {
    param([Parameter(Mandatory = $true)][string]$Path)

    $text = Get-Content -LiteralPath $Path -Raw -Encoding utf8
    if ([string]::IsNullOrWhiteSpace($text)) {
        throw "qualification source 不能为空：$Path"
    }
    return $text | ConvertFrom-Json
}

# Assert-QualificationJsonSchema 将底层 validator 差异收敛为稳定失败类别。
function Assert-QualificationJsonSchema {
    param(
        [Parameter(Mandatory = $true)][string]$DocumentPath,
        [Parameter(Mandatory = $true)][string]$SchemaPath,
        [Parameter(Mandatory = $true)][string]$Context
    )

    try {
        $valid = Test-Json -LiteralPath $DocumentPath -SchemaFile $SchemaPath -ErrorAction Stop
    }
    catch {
        throw "$Context 不符合 closed schema"
    }
    if (-not $valid) {
        throw "$Context 不符合 closed schema"
    }
}

# Resolve-QualificationPath 阻止 manifest 或 binding 通过相对路径逃逸指定根目录。
function Resolve-QualificationPath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    if ([IO.Path]::IsPathRooted($RelativePath) -or $RelativePath.Contains("\")) {
        throw "qualification path 必须是规范仓库相对路径：$RelativePath"
    }
    $rootPrefix = [IO.Path]::GetFullPath($Root).TrimEnd("\", "/") + [IO.Path]::DirectorySeparatorChar
    $candidate = [IO.Path]::GetFullPath((Join-Path $Root $RelativePath))
    if (-not $candidate.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "qualification path 逃逸根目录：$RelativePath"
    }
    return $candidate
}

# Assert-QualificationSequence 比较集合前先稳定排序并拒绝重复。
function Assert-QualificationSequence {
    param(
        [Parameter(Mandatory = $true)][object[]]$Actual,
        [Parameter(Mandatory = $true)][object[]]$Expected,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $actualText = @($Actual | ForEach-Object { [string]$_ } | Sort-Object)
    $expectedText = @($Expected | ForEach-Object { [string]$_ } | Sort-Object)
    if (@($actualText | Select-Object -Unique).Count -ne $actualText.Count -or
        ($actualText -join "`n") -cne ($expectedText -join "`n")) {
        throw "$Context coverage 漂移"
    }
}

# Assert-QualificationSchemaClosed 递归检查所有 object schema 均显式关闭未知字段。
function Assert-QualificationSchemaClosed {
    param(
        [Parameter(Mandatory = $true)]$Node,
        [Parameter(Mandatory = $true)][string]$Context
    )

    if ($null -eq $Node) {
        return
    }
    if ($Node -is [Collections.IDictionary] -or $Node -is [PSCustomObject]) {
        $properties = @($Node.PSObject.Properties)
        $typeProperty = $properties | Where-Object Name -ceq "type"
        if ($typeProperty -and [string]$typeProperty.Value -ceq "object") {
            $closed = $properties | Where-Object Name -ceq "additionalProperties"
            if (-not $closed -or [bool]$closed.Value -ne $false) {
                throw "$Context 存在未关闭 object schema"
            }
        }
        foreach ($property in $properties) {
            Assert-QualificationSchemaClosed -Node $property.Value -Context $Context
        }
        return
    }
    if ($Node -is [Collections.IEnumerable] -and $Node -isnot [string]) {
        foreach ($item in $Node) {
            Assert-QualificationSchemaClosed -Node $item -Context $Context
        }
    }
}

# Assert-NoQualificationSensitiveData 扫描 source 属性名与绝对路径，避免低敏边界被 schema 绕过。
function Assert-NoQualificationSensitiveData {
    param(
        [Parameter(Mandatory = $true)][string]$QualificationRoot,
        [Parameter(Mandatory = $true)][object[]]$JsonPaths
    )

    $forbiddenProperty = '(?i)^(raw_?ticket|credential|proof_?key|traffic_?key|cookie_?key|player_?id|remote_?endpoint|payload_?dump|process_?id|run_?directory)$'
    foreach ($path in $JsonPaths) {
        $text = Get-Content -LiteralPath $path -Raw -Encoding utf8
        if ($text -match '(?m)([A-Za-z]:\\|/home/|/Users/)' -or
            $text -match '(?<![0-9])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![0-9])') {
            throw "qualification source 包含本机路径或 IP：$([IO.Path]::GetFileName($path))"
        }
        $document = $text | ConvertFrom-Json
        $pending = [Collections.Generic.Stack[object]]::new()
        $pending.Push($document)
        while ($pending.Count -gt 0) {
            $value = $pending.Pop()
            if ($null -eq $value) {
                continue
            }
            if ($value -is [PSCustomObject]) {
                foreach ($property in $value.PSObject.Properties) {
                    if ($property.Name -match $forbiddenProperty) {
                        throw "qualification source 包含禁止敏感字段：$($property.Name)"
                    }
                    if ($null -ne $property.Value) {
                        $pending.Push($property.Value)
                    }
                }
            }
            elseif ($value -is [Collections.IEnumerable] -and $value -isnot [string]) {
                foreach ($item in $value) {
                    if ($null -ne $item) {
                        $pending.Push($item)
                    }
                }
            }
        }
    }
}

# Assert-UpstreamBinding 验证每个上游 source 与冻结 receipt，不接受缺失或陈旧 evidence。
function Assert-UpstreamBinding {
    param(
        [Parameter(Mandatory = $true)]$Binding,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        # AllowSourceDrift 只供 tooling validate/diagnose；最终 verify/soak/finalize 仍严格绑定。
        [switch]$AllowSourceDrift
    )

    $expectedOwners = @(
        "battle-model",
        "battle-network-profile",
        "battle-runtime-config",
        "battle-wire",
        "dependency-toolchain",
        "secure-transport-audit",
        "simulation-control"
    )
    Assert-QualificationSequence -Actual @($Binding.sources.owner) -Expected $expectedOwners -Context "upstream owner"
    foreach ($source in $Binding.sources) {
        $path = Resolve-QualificationPath -Root $RepositoryRoot -RelativePath ([string]$source.path)
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "upstream source 缺失：$($source.owner)"
        }
        if (-not $AllowSourceDrift -and
            (Get-QualificationSha256 -Path $path) -cne [string]$source.sha256) {
            throw "upstream source 摘要漂移：$($source.owner)"
        }
    }
    if ([string]$Binding.invariants.platform -cne "windows-x64" -or
        [string]$Binding.invariants.environmentClass -cne "controlled-local-fault-gateway" -or
        [string]$Binding.invariants.sourceCorpusMode -cne "read-only" -or
        [int]$Binding.invariants.maximumBattleActors -ne $script:MaximumBattleActors -or
        [int64]$Binding.invariants.simulationTickNanoseconds -ne $script:SimulationTickNanoseconds -or
        [bool]$Binding.invariants.publicInternetQualified) {
        throw "upstream qualification invariants 漂移"
    }
}

# Assert-FaultExecution 将每个真实 scenario 精确绑定到 B0.2 source shape。
function Assert-FaultExecution {
    param(
        [Parameter(Mandatory = $true)]$Execution,
        [Parameter(Mandatory = $true)]$ProfileMatrix
    )

    if ([int64]$Execution.seed -ne [int64]$ProfileMatrix.seed -or
        [string]$Execution.prng -cne [string]$ProfileMatrix.prng) {
        throw "fault seed 或 PRNG 漂移"
    }
    Assert-QualificationSequence -Actual @($Execution.requiredDirections) -Expected @("downlink", "uplink") -Context "fault direction"
    $gateway = $Execution.gatewayPolicy
    if ([int]$gateway.maximumDatagramBytes -ne $script:GatewayMaximumDatagramBytes -or
        [int]$gateway.globalQueueItems -ne $script:GatewayGlobalQueueItems -or
        [int]$gateway.ingressQueueItems -ne $script:GatewayIngressQueueItems -or
        [int]$gateway.evidenceQueueItems -ne $script:GatewayEvidenceQueueItems -or
        [int]$gateway.packetLifetimeMilliseconds -ne $script:GatewayPacketLifetimeMilliseconds -or
        [int]$gateway.reorderAdvanceMicroseconds -ne $script:GatewayReorderAdvanceMicroseconds -or
        [int]$gateway.schedulerPollMicroseconds -ne $script:GatewaySchedulerPollMicroseconds -or
        [int]$gateway.mappingLateDeliveryMilliseconds -ne $script:GatewayMappingLateDeliveryMilliseconds -or
        [string]$gateway.bandwidthMode -cne "measure-only-no-shaping") {
        throw "fault gateway policy 漂移"
    }
    if ([int]$Execution.executionPolicy.warmupMilliseconds -lt $script:FaultMinimumWarmupMilliseconds -or
        [int]$Execution.executionPolicy.measurementMilliseconds -lt $script:FaultMinimumMeasurementMilliseconds -or
        [int]$Execution.executionPolicy.scenarioDeadlineMilliseconds -le
            ([int]$Execution.executionPolicy.warmupMilliseconds +
             [int]$Execution.executionPolicy.measurementMilliseconds) -or
        [int]$Execution.executionPolicy.cleanupMilliseconds -lt $script:FaultMinimumCleanupMilliseconds -or
        [string]$Execution.executionPolicy.faultShapePolicy -cne "repeat-source-pattern" -or
        [string]$Execution.executionPolicy.payloadPolicy -cne "opaque" -or
        [string]$Execution.executionPolicy.unsupportedPolicy -cne "fail-closed") {
        throw "fault execution budget 或 policy 被降低"
    }
    Assert-QualificationSequence -Actual @($Execution.scenarios.sourceScenarioId) -Expected @($ProfileMatrix.scenarios.scenario_id) -Context "fault scenario"
    $bySource = @{}
    foreach ($scenario in $Execution.scenarios) {
        if ($bySource.ContainsKey([string]$scenario.sourceScenarioId)) {
            throw "fault scenario source 重复"
        }
        $bySource[[string]$scenario.sourceScenarioId] = $scenario
    }
    foreach ($source in $ProfileMatrix.scenarios) {
        $actual = $bySource[[string]$source.scenario_id]
        if ([int]$actual.sourceDurationTicks -ne [int]$source.duration_ticks -or
            [int]$actual.actorCount -ne $script:DefaultBattleActors -or -not [bool]$actual.mandatory) {
            throw "fault scenario 执行边界漂移：$($source.scenario_id)"
        }
        Assert-QualificationSequence -Actual @($actual.sourceWorkloads) -Expected @($source.workloads) -Context "fault workload $($source.scenario_id)"
        Assert-QualificationSequence -Actual @($actual.sourcePhases) -Expected @($source.phases) -Context "fault phase $($source.scenario_id)"
        Assert-QualificationSequence -Actual @($actual.impairments) -Expected @($source.impairments) -Context "fault impairment $($source.scenario_id)"
    }
}

# Assert-WorkloadsAndMessages 验证容量层级和八类 logical message 与 B0.2 完全一致。
function Assert-WorkloadsAndMessages {
    param(
        [Parameter(Mandatory = $true)]$Workloads,
        [Parameter(Mandatory = $true)]$Profile,
        [Parameter(Mandatory = $true)]$MessageInventory
    )

    if ([int64]$Workloads.simulationTickNanoseconds -ne $script:SimulationTickNanoseconds -or
        [int64]$Workloads.inputTickNanoseconds -ne $script:InputTickNanoseconds -or
        [int]$Workloads.snapshotIntervalTicks -ne $script:SnapshotIntervalTicks) {
        throw "workload cadence 漂移"
    }
    $expectedActors = @{
        "solo-owner" = $script:OwnerActors
        "default-coop" = $script:DefaultBattleActors
        "default-capacity" = $script:DefaultBattleActors
        "qualified-capacity" = $script:MaximumBattleActors
        "overflow-probe" = $script:OverflowProbeActors
        "visit-capacity-compatibility" = $script:MaximumBattleActors
    }
    Assert-QualificationSequence -Actual @($Workloads.workloads.workloadId) -Expected @($expectedActors.Keys) -Context "workload"
    foreach ($workload in $Workloads.workloads) {
        if ([int]$workload.battleActorCount -ne [int]$expectedActors[[string]$workload.workloadId] -or
            [int]$workload.ownerCount -ne $script:OwnerActors) {
            throw "workload actor 边界漂移：$($workload.workloadId)"
        }
    }
    if ([int]$Profile.capacity.qualified_default_players -ne $script:DefaultBattleActors -or
        [int]$Profile.capacity.qualified_max_players -ne $script:MaximumBattleActors -or
        [int]$Profile.capacity.evaluated_max_players -ne $script:CompatibilityPlayers -or
        -not [bool]$Profile.capacity.capacity_gate_required) {
        throw "profile capacity binding 漂移"
    }
    Assert-QualificationSequence -Actual @($Workloads.messageCadence.logicalKind) -Expected @($MessageInventory.messages.kind) -Context "message cadence"
    $profileMessages = @{}
    foreach ($message in $MessageInventory.messages) {
        $profileMessages[[string]$message.kind] = $message
    }
    foreach ($message in $Workloads.messageCadence) {
        $source = $profileMessages[[string]$message.logicalKind]
        $expectedDirection = if ([string]$source.direction -ceq "c2s") { "uplink" } else { "downlink" }
        if ([string]$message.lane -cne [string]$source.lane -or
            [string]$message.direction -cne $expectedDirection -or
            [int]$message.maximumRatePerSecond -ne [int]$source.maximum_rate_per_second -or
            [int]$message.maximumLogicalBytes -ne [int]$source.max_logical_payload_bytes) {
            throw "message cadence 漂移：$($message.logicalKind)"
        }
    }
}

# Assert-Metrics 验证所有硬预算均来自冻结 profile/model/B0.5 evidence。
function Assert-Metrics {
    param(
        [Parameter(Mandatory = $true)]$Metrics,
        [Parameter(Mandatory = $true)]$Profile,
        [Parameter(Mandatory = $true)]$ProfileReport
    )

    Assert-QualificationSequence -Actual @($Metrics.sources) -Expected @("client", "control-snapshot", "fault-gateway", "process-sampler") -Context "metric source"
    if (-not [bool]$Metrics.comparisonPolicy.bothRunsMustPassBudget -or
        [string]$Metrics.comparisonPolicy.aggregation -cne "worst-case" -or
        [string]$Metrics.comparisonPolicy.reproducibilityFailurePolicy -cne "not-qualified" -or
        [string]$Metrics.comparisonPolicy.missingSamplePolicy -cne "not-qualified") {
        throw "metric comparison policy 漂移"
    }
    $parameters = @{}
    foreach ($parameter in $Profile.parameters) {
        $parameters[[string]$parameter.id] = [int64]$parameter.value
    }
    $profileKcpMaximum = [int64]($ProfileReport.results |
        Measure-Object -Property kcp_amplification_basis_points -Maximum).Maximum
    $expectedMaximums = @{
        "uplink-bytes-per-player-second" = $parameters["bandwidth-up-per-player-target"]
        "downlink-bytes-per-player-second" = $parameters["bandwidth-down-per-player-target"]
        "downlink-bytes-per-instance-second" = $parameters["bandwidth-down-per-instance-target"]
        "delivery-age" = $parameters["maximum-extrapolation-us"]
        "baseline-recovery" = (
            $parameters["maximum-baseline-age-ticks"] * $parameters["simulation-step-ns"] / 1000
        )
        "kcp-amplification" = $profileKcpMaximum
        "ingress-queue-items" = $parameters["queue-capacity-items"]
        "egress-queue-items" = $script:SessionQueueItems
        "kcp-queue-messages" = [int64]$Profile.kcp_profile.queue_limit_messages
        "cpu-per-simulation-tick" = $parameters["cpu-per-tick-target"]
        "hard-tick-debt" = $script:HardTickDebt
        "instance-memory" = $parameters["memory-per-instance-target"]
        "history-memory" = $parameters["history-memory-target"]
        "process-working-set-growth" = $script:SteadyStateWorkingSetGrowthBytes
    }
    $expectedTolerances = @{
        "uplink-bytes-per-player-second" = 1000
        "downlink-bytes-per-player-second" = 1000
        "downlink-bytes-per-instance-second" = 1000
        "delivery-age" = 1000
        "baseline-recovery" = 1000
        "kcp-amplification" = 1000
        "ingress-queue-items" = 0
        "egress-queue-items" = 0
        "kcp-queue-messages" = 0
        "cpu-per-simulation-tick" = 2000
        "hard-tick-debt" = 0
        "instance-memory" = 500
        "history-memory" = 500
        "process-working-set-growth" = 1000
    }
    $expectedSources = @{
        "uplink-bytes-per-player-second" = "fault-gateway"
        "downlink-bytes-per-player-second" = "fault-gateway"
        "downlink-bytes-per-instance-second" = "fault-gateway"
        "delivery-age" = "fault-gateway"
        "baseline-recovery" = "client"
        "kcp-amplification" = "control-snapshot"
        "ingress-queue-items" = "control-snapshot"
        "egress-queue-items" = "control-snapshot"
        "kcp-queue-messages" = "control-snapshot"
        "cpu-per-simulation-tick" = "control-snapshot"
        "hard-tick-debt" = "control-snapshot"
        "instance-memory" = "control-snapshot"
        "history-memory" = "control-snapshot"
        "process-working-set-growth" = "process-sampler"
    }
    $expectedMethods = @{
        "uplink-bytes-per-player-second" = "delivered-uplink-gateway-bytes-divided-by-window-and-actors"
        "downlink-bytes-per-player-second" = "delivered-downlink-gateway-bytes-divided-by-window-and-actors"
        "downlink-bytes-per-instance-second" = "delivered-gateway-bytes-divided-by-window"
        "delivery-age" = "maximum-gateway-delivery-age"
        "baseline-recovery" = "maximum-resync-to-successor-baseline"
        "kcp-amplification" = "kcp-egress-xmit-divided-by-first-transmissions"
        "ingress-queue-items" = "maximum-control-ingress-queue-high-watermark"
        "egress-queue-items" = "maximum-control-egress-queue-high-watermark"
        "kcp-queue-messages" = "maximum-control-kcp-queue-high-watermark"
        "cpu-per-simulation-tick" = "maximum-tick-duration-ns-to-us-ceiling"
        "hard-tick-debt" = "maximum-control-tick-debt-high-watermark"
        "instance-memory" = "maximum-control-accounted-instance-bytes"
        "history-memory" = "maximum-control-accounted-history-bytes"
        "process-working-set-growth" = "maximum-end-minus-start-positive"
    }
    Assert-QualificationSequence -Actual @($Metrics.metrics.metricId) -Expected @($expectedMaximums.Keys) -Context "metric"
    foreach ($metric in $Metrics.metrics) {
        $metricId = [string]$metric.metricId
        if ([int64]$metric.maximum -ne [int64]$expectedMaximums[$metricId]) {
            throw "metric budget 漂移：$metricId"
        }
        if ([int]$metric.reproducibilityToleranceBasisPoints -ne [int]$expectedTolerances[$metricId]) {
            throw "metric 复现容差漂移：$metricId"
        }
        if ([string]$metric.source -cne [string]$expectedSources[$metricId]) {
            throw "metric source 漂移：$metricId"
        }
        if ([string]$metric.method -cne [string]$expectedMethods[$metricId]) {
            throw "metric method 漂移：$metricId"
        }
    }
}

# Assert-LifecycleSecurity 验证攻击、生命周期与 soak 不能被静默删减。
function Assert-LifecycleSecurity {
    param([Parameter(Mandatory = $true)]$LifecycleSecurity)

    Assert-QualificationSequence -Actual @($LifecycleSecurity.securityCases) -Expected $script:RequiredSecurityCases -Context "security case"
    Assert-QualificationSequence -Actual @($LifecycleSecurity.lifecycleCases) -Expected $script:RequiredLifecycleCases -Context "lifecycle case"
    if ([int]$LifecycleSecurity.attackBudget.maximumSenders -gt $script:MaximumAttackSenders -or
        [int]$LifecycleSecurity.attackBudget.maximumPacketsPerSecondPerSender -gt $script:MaximumAttackPacketsPerSecondPerSender -or
        [int]$LifecycleSecurity.attackBudget.maximumDurationMilliseconds -gt $script:MaximumAttackDurationMilliseconds -or
        [int64]$LifecycleSecurity.attackBudget.maximumTotalBytes -gt $script:MaximumAttackBytes -or
        [int]$LifecycleSecurity.attackBudget.legitimateActorCount -ne $script:DefaultBattleActors -or
        [string]$LifecycleSecurity.attackBudget.availabilityPolicy -cne "legitimate-workload-remains-within-budget") {
        throw "security attack budget 不安全"
    }
    if ([int64]$LifecycleSecurity.soak.durationMilliseconds -ne $script:SoakDurationMilliseconds -or
        [int64]$LifecycleSecurity.soak.rekeyIntervalMilliseconds -ne $script:SoakRekeyIntervalMilliseconds -or
        [int]$LifecycleSecurity.soak.minimumObservedRekeys -lt $script:SoakMinimumObservedRekeys -or
        [int]$LifecycleSecurity.soak.cleanupMilliseconds -lt $script:SoakMinimumCleanupMilliseconds -or
        @($LifecycleSecurity.soak.requiredInvariants).Count -lt 11) {
        throw "lifecycle soak coverage 被降低"
    }
}

# Invoke-BattleQualificationCorpusValidation 执行全部只读 gate 并返回稳定 tree digest。
function Invoke-BattleQualificationCorpusValidation {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$QualificationRoot,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        # AllowSourceDrift 不放宽 corpus、schema、coverage 或预算，只解耦开发工作区与旧报告摘要。
        [switch]$AllowSourceDrift
    )

    $resolvedQualificationRoot = (Resolve-Path -LiteralPath $QualificationRoot).Path
    $resolvedRepositoryRoot = (Resolve-Path -LiteralPath $RepositoryRoot).Path
    $before = Get-QualificationTreeDigest -QualificationRoot $resolvedQualificationRoot
    $manifestPath = Join-Path $resolvedQualificationRoot "manifest.json"
    $schemaPath = Join-Path $resolvedQualificationRoot "schema.json"
    $reportSchemaPath = Join-Path $resolvedQualificationRoot "report.schema.json"
    $scenarioEvidenceSchemaPath = Join-Path $resolvedQualificationRoot "scenario-evidence.schema.json"
    $scenarioFailureEvidenceSchemaPath = Join-Path $resolvedQualificationRoot "scenario-failure-evidence.schema.json"
    $admissionEvidenceSchemaPath = Join-Path $resolvedQualificationRoot "admission-evidence.schema.json"
    $gateEvidenceSchemaPath = Join-Path $resolvedQualificationRoot "gate-evidence.schema.json"
    $runEvidenceSchemaPath = Join-Path $resolvedQualificationRoot "run-evidence.schema.json"
    $profileOverlaySchemaPath = Join-Path $resolvedQualificationRoot "profile-overlay.schema.json"
    $manifest = Read-QualificationJson -Path $manifestPath
    $schema = Read-QualificationJson -Path $schemaPath
    $reportSchema = Read-QualificationJson -Path $reportSchemaPath
    $scenarioEvidenceSchema = Read-QualificationJson -Path $scenarioEvidenceSchemaPath
    $scenarioFailureEvidenceSchema = Read-QualificationJson -Path $scenarioFailureEvidenceSchemaPath
    $admissionEvidenceSchema = Read-QualificationJson -Path $admissionEvidenceSchemaPath
    $gateEvidenceSchema = Read-QualificationJson -Path $gateEvidenceSchemaPath
    $runEvidenceSchema = Read-QualificationJson -Path $runEvidenceSchemaPath
    $profileOverlaySchema = Read-QualificationJson -Path $profileOverlaySchemaPath

    Assert-QualificationJsonSchema -DocumentPath $manifestPath -SchemaPath $schemaPath -Context "qualification manifest"
    Assert-QualificationSchemaClosed -Node $schema -Context "source schema"
    Assert-QualificationSchemaClosed -Node $reportSchema -Context "report schema"
    Assert-QualificationSchemaClosed -Node $scenarioEvidenceSchema -Context "scenario evidence schema"
    Assert-QualificationSchemaClosed -Node $scenarioFailureEvidenceSchema -Context "scenario failure evidence schema"
    Assert-QualificationSchemaClosed -Node $admissionEvidenceSchema -Context "admission evidence schema"
    Assert-QualificationSchemaClosed -Node $gateEvidenceSchema -Context "gate evidence schema"
    Assert-QualificationSchemaClosed -Node $runEvidenceSchema -Context "run evidence schema"
    Assert-QualificationSchemaClosed -Node $profileOverlaySchema -Context "profile overlay schema"
    if ([string]$manifest.qualificationVersion -cne $script:QualificationVersion -or
        [string]$manifest.documentKind -cne "manifest") {
        throw "qualification manifest identity 无效"
    }
    Assert-QualificationSequence -Actual @($manifest.requiredDomains) -Expected $script:RequiredDomains -Context "required domain"
    if ((Get-QualificationSha256 -Path $schemaPath) -cne [string]$manifest.schemaSha256 -or
        (Get-QualificationSha256 -Path $reportSchemaPath) -cne [string]$manifest.reportSchemaSha256 -or
        (Get-QualificationSha256 -Path $scenarioEvidenceSchemaPath) -cne [string]$manifest.scenarioEvidenceSchemaSha256 -or
        (Get-QualificationSha256 -Path $scenarioFailureEvidenceSchemaPath) -cne [string]$manifest.scenarioFailureEvidenceSchemaSha256 -or
        (Get-QualificationSha256 -Path $admissionEvidenceSchemaPath) -cne [string]$manifest.admissionEvidenceSchemaSha256 -or
        (Get-QualificationSha256 -Path $gateEvidenceSchemaPath) -cne [string]$manifest.gateEvidenceSchemaSha256 -or
        (Get-QualificationSha256 -Path $runEvidenceSchemaPath) -cne [string]$manifest.runEvidenceSchemaSha256 -or
        (Get-QualificationSha256 -Path $profileOverlaySchemaPath) -cne [string]$manifest.profileOverlaySchemaSha256) {
        throw "qualification schema 摘要漂移"
    }

    $actualFiles = Get-ChildItem -LiteralPath $resolvedQualificationRoot -Recurse -File |
        ForEach-Object {
            Get-QualificationRelativePath -Root $resolvedQualificationRoot -Path $_.FullName
        } |
        Sort-Object
    $expectedFiles = @("manifest.json") + @($manifest.files.path)
    Assert-QualificationSequence -Actual $actualFiles -Expected $expectedFiles -Context "qualification file inventory"
    foreach ($file in $manifest.files) {
        $path = Resolve-QualificationPath -Root $resolvedQualificationRoot -RelativePath ([string]$file.path)
        if ((Get-QualificationSha256 -Path $path) -cne [string]$file.sha256) {
            throw "qualification source 摘要漂移：$($file.path)"
        }
    }

    $sourceDocuments = @(
        "fault-execution.json",
        "lifecycle-security.json",
        "metrics.json",
        "upstream-binding.json",
        "workloads.json"
    )
    foreach ($file in $sourceDocuments) {
        $path = Join-Path $resolvedQualificationRoot $file
        Assert-QualificationJsonSchema -DocumentPath $path -SchemaPath $schemaPath -Context "qualification source：$file"
    }
    $jsonPaths = @($actualFiles | Where-Object { $_.EndsWith(".json", [StringComparison]::Ordinal) } |
        ForEach-Object { Join-Path $resolvedQualificationRoot $_ })
    Assert-NoQualificationSensitiveData -QualificationRoot $resolvedQualificationRoot -JsonPaths $jsonPaths

    $binding = Read-QualificationJson -Path (Join-Path $resolvedQualificationRoot "upstream-binding.json")
    $execution = Read-QualificationJson -Path (Join-Path $resolvedQualificationRoot "fault-execution.json")
    $workloads = Read-QualificationJson -Path (Join-Path $resolvedQualificationRoot "workloads.json")
    $metrics = Read-QualificationJson -Path (Join-Path $resolvedQualificationRoot "metrics.json")
    $lifecycleSecurity = Read-QualificationJson -Path (Join-Path $resolvedQualificationRoot "lifecycle-security.json")
    $profileRoot = Join-Path $resolvedRepositoryRoot "shared/contracts/fixtures/battle/network-profile"
    $profile = Read-QualificationJson -Path (Join-Path $profileRoot "profile.json")
    $profileMatrix = Read-QualificationJson -Path (Join-Path $profileRoot "fault-matrix.json")
    $profileMessages = Read-QualificationJson -Path (Join-Path $profileRoot "message-inventory.json")
    $profileReport = Read-QualificationJson -Path (Join-Path $profileRoot "reports/qualification.json")

    Assert-UpstreamBinding `
        -Binding $binding `
        -RepositoryRoot $resolvedRepositoryRoot `
        -AllowSourceDrift:$AllowSourceDrift
    Assert-FaultExecution -Execution $execution -ProfileMatrix $profileMatrix
    Assert-WorkloadsAndMessages -Workloads $workloads -Profile $profile -MessageInventory $profileMessages
    Assert-Metrics -Metrics $metrics -Profile $profile -ProfileReport $profileReport
    Assert-LifecycleSecurity -LifecycleSecurity $lifecycleSecurity

    $after = Get-QualificationTreeDigest -QualificationRoot $resolvedQualificationRoot
    if ($after -cne $before) {
        throw "qualification validator 改写了 source corpus"
    }
    return $after
}

Export-ModuleMember -Function @(
    "Get-QualificationSha256",
    "Get-QualificationTreeDigest",
    "Invoke-BattleQualificationCorpusValidation"
)
