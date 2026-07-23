# 该入口只读验证 battle simulation model corpus；它不执行 gameplay 规则或启动任何运行时资源。
[CmdletBinding()]
param(
    # Action 保留单一非交互验证动作，避免调用方绕过完整门禁。
    [ValidateSet("validate")]
    [string]$Action = "validate",
    # ModelRoot 允许失败回归验证隔离副本；默认值指向仓库内唯一模型目录。
    [string]$ModelRoot = ""
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if ([string]::IsNullOrWhiteSpace($ModelRoot)) {
    $ModelRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model"
}
if (-not (Test-Path -LiteralPath $ModelRoot -PathType Container)) {
    throw "battle model root is missing"
}
$ModelRoot = (Resolve-Path -LiteralPath $ModelRoot).Path
$ManifestPath = Join-Path $ModelRoot "manifest.json"
$SchemaPath = Join-Path $ModelRoot "schema.json"
$AssumptionsPath = Join-Path $ModelRoot "assumptions.json"
$Utf8Strict = [System.Text.UTF8Encoding]::new($false, $true)
$Utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$Ordinal = [System.StringComparer]::Ordinal

# Fail-BattleModel 使用稳定低敏前缀终止验证，不回显文件内容或本机目录。
function Fail-BattleModel {
    param([string]$Message)
    throw "battle model validation failed: $Message"
}

# Get-RelativeModelPath 把诊断限制为模型根相对路径，并拒绝路径逃逸。
function Get-RelativeModelPath {
    param([string]$Path)
    $root = [System.IO.Path]::GetFullPath($ModelRoot).TrimEnd('\') + '\'
    $full = [System.IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        Fail-BattleModel "path escaped model root"
    }
    return $full.Substring($root.Length).Replace('\', '/')
}

# Resolve-ModelPath 解析 manifest 相对路径并保证结果仍在模型根内。
function Resolve-ModelPath {
    param([string]$RelativePath)
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [System.IO.Path]::IsPathRooted($RelativePath)) {
        Fail-BattleModel "manifest path is not repository relative"
    }
    $full = [System.IO.Path]::GetFullPath((Join-Path $ModelRoot $RelativePath))
    $null = Get-RelativeModelPath $full
    return $full
}

# Read-CanonicalText 验证 UTF-8 无 BOM、LF 和单一末尾换行后返回文本。
function Read-CanonicalText {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail-BattleModel "required file is missing"
    }
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -ge 3 -and
        $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        Fail-BattleModel "$(Get-RelativeModelPath $Path) contains UTF-8 BOM"
    }
    if ($bytes -contains 13) {
        Fail-BattleModel "$(Get-RelativeModelPath $Path) contains CR line endings"
    }
    try {
        $text = $Utf8Strict.GetString($bytes)
    }
    catch {
        Fail-BattleModel "$(Get-RelativeModelPath $Path) is not strict UTF-8"
    }
    if (-not $text.EndsWith("`n") -or $text.EndsWith("`n`n")) {
        Fail-BattleModel "$(Get-RelativeModelPath $Path) must end with one LF"
    }
    return $text
}

# Read-JsonDocument 读取 canonical JSON，并把解析失败收敛为稳定相对路径。
function Read-JsonDocument {
    param([string]$Path)
    $text = Read-CanonicalText $Path
    try {
        return $text | ConvertFrom-Json
    }
    catch {
        Fail-BattleModel "$(Get-RelativeModelPath $Path) is not valid JSON"
    }
}

# Get-Sha256Bytes 返回原始 bytes 的 lowercase SHA-256。
function Get-Sha256Bytes {
    param([byte[]]$Bytes)
    $hasher = [System.Security.Cryptography.SHA256]::Create()
    try {
        return -join ($hasher.ComputeHash($Bytes) | ForEach-Object { $_.ToString("x2") })
    }
    finally {
        $hasher.Dispose()
    }
}

# Get-Sha256Text 返回 canonical UTF-8 文本的 lowercase SHA-256。
function Get-Sha256Text {
    param([string]$Text)
    return Get-Sha256Bytes ($Utf8NoBom.GetBytes($Text))
}

# Get-Sha256File 返回文件原始 bytes 的 lowercase SHA-256。
function Get-Sha256File {
    param([string]$Path)
    return Get-Sha256Bytes ([System.IO.File]::ReadAllBytes($Path))
}

# Assert-ClosedProperties 拒绝未登记字段，并可同时验证必填字段。
function Assert-ClosedProperties {
    param(
        [object]$Value,
        [string[]]$Allowed,
        [string[]]$Required,
        [string]$Context
    )
    if ($null -eq $Value -or $Value -isnot [pscustomobject]) {
        Fail-BattleModel "$Context must be an object"
    }
    $actual = @($Value.PSObject.Properties.Name)
    $unknown = @($actual | Where-Object { $_ -notin $Allowed })
    $missing = @($Required | Where-Object { $_ -notin $actual })
    if ($unknown.Count -gt 0) {
        Fail-BattleModel "$Context contains an unknown field"
    }
    if ($missing.Count -gt 0) {
        Fail-BattleModel "$Context is missing a required field"
    }
}

# Test-JsonScalarEqual 比较 schema const/enum 的 JSON 标量语义。
function Test-JsonScalarEqual {
    param([object]$Left, [object]$Right)
    $leftJson = $Left | ConvertTo-Json -Compress -Depth 10
    $rightJson = $Right | ConvertTo-Json -Compress -Depth 10
    return $leftJson -ceq $rightJson
}

# Resolve-SchemaReference 只允许 schema 内部 $defs 引用，避免外部读取或网络解析。
function Resolve-SchemaReference {
    param([string]$Reference, [object]$RootSchema)
    if ($Reference -notmatch '^#/\$defs/([A-Za-z][A-Za-z0-9]*)$') {
        Fail-BattleModel "schema contains an unsupported reference"
    }
    $name = $Matches[1]
    $definition = $RootSchema.'$defs'.PSObject.Properties[$name]
    if ($null -eq $definition) {
        Fail-BattleModel "schema reference is missing"
    }
    return $definition.Value
}

# Assert-SchemaValue 实现 corpus 使用的 JSON Schema 闭合子集，不执行 gameplay 计算。
function Assert-SchemaValue {
    param(
        [object]$Value,
        [object]$Schema,
        [object]$RootSchema,
        [string]$Context
    )
    $schemaFields = @($Schema.PSObject.Properties.Name)
    if ($schemaFields -contains '$ref') {
        $resolved = Resolve-SchemaReference ([string]$Schema.'$ref') $RootSchema
        Assert-SchemaValue $Value $resolved $RootSchema $Context
        return
    }
    if ($schemaFields -contains 'const' -and
        -not (Test-JsonScalarEqual $Value $Schema.const)) {
        Fail-BattleModel "$Context does not match schema const"
    }
    if ($schemaFields -contains 'enum') {
        $matched = $false
        foreach ($candidate in @($Schema.enum)) {
            if (Test-JsonScalarEqual $Value $candidate) {
                $matched = $true
                break
            }
        }
        if (-not $matched) {
            Fail-BattleModel "$Context does not match schema enum"
        }
    }
    if ($schemaFields -contains 'type') {
        $type = [string]$Schema.type
        $validType = switch ($type) {
            "object" { $Value -is [pscustomobject] }
            "array" { $Value -is [System.Array] }
            "string" { $Value -is [string] }
            "integer" {
                $Value -is [byte] -or $Value -is [int16] -or $Value -is [int32] -or
                $Value -is [int64] -or $Value -is [uint16] -or $Value -is [uint32] -or
                $Value -is [uint64]
            }
            "boolean" { $Value -is [bool] }
            default { $false }
        }
        if (-not $validType) {
            Fail-BattleModel "$Context has the wrong JSON type"
        }
    }
    if ($Value -is [string]) {
        if ($schemaFields -contains 'minLength' -and
            $Value.Length -lt [int]$Schema.minLength) {
            Fail-BattleModel "$Context is shorter than schema minimum"
        }
        if ($schemaFields -contains 'pattern' -and
            -not [regex]::IsMatch($Value, [string]$Schema.pattern)) {
            Fail-BattleModel "$Context does not match schema pattern"
        }
    }
    if ($Value -is [byte] -or $Value -is [int16] -or $Value -is [int32] -or
        $Value -is [int64] -or $Value -is [uint16] -or $Value -is [uint32] -or
        $Value -is [uint64]) {
        if ($schemaFields -contains 'minimum' -and
            [decimal]$Value -lt [decimal]$Schema.minimum) {
            Fail-BattleModel "$Context is below schema minimum"
        }
        if ($schemaFields -contains 'maximum' -and
            [decimal]$Value -gt [decimal]$Schema.maximum) {
            Fail-BattleModel "$Context is above schema maximum"
        }
    }
    if ($Value -is [System.Array]) {
        $items = @($Value)
        if ($schemaFields -contains 'minItems' -and
            $items.Count -lt [int]$Schema.minItems) {
            Fail-BattleModel "$Context has too few items"
        }
        if ($schemaFields -contains 'maxItems' -and
            $items.Count -gt [int]$Schema.maxItems) {
            Fail-BattleModel "$Context has too many items"
        }
        if ($schemaFields -contains 'uniqueItems' -and [bool]$Schema.uniqueItems) {
            $seen = @{}
            foreach ($item in $items) {
                $key = $item | ConvertTo-Json -Compress -Depth 100
                if ($seen.ContainsKey($key)) {
                    Fail-BattleModel "$Context contains duplicate items"
                }
                $seen[$key] = $true
            }
        }
        if ($schemaFields -contains 'items') {
            for ($index = 0; $index -lt $items.Count; $index++) {
                Assert-SchemaValue $items[$index] $Schema.items $RootSchema "$Context[$index]"
            }
        }
    }
    if ($Value -is [pscustomobject]) {
        $allowed = @()
        if ($schemaFields -contains 'properties') {
            $allowed = @($Schema.properties.PSObject.Properties.Name)
        }
        $required = @()
        if ($schemaFields -contains 'required') {
            $required = @($Schema.required | ForEach-Object { [string]$_ })
        }
        if ($schemaFields -contains 'additionalProperties' -and
            -not [bool]$Schema.additionalProperties) {
            Assert-ClosedProperties $Value $allowed $required $Context
        }
        else {
            $missing = @($required | Where-Object {
                    $_ -notin @($Value.PSObject.Properties.Name)
                })
            if ($missing.Count -gt 0) {
                Fail-BattleModel "$Context is missing a required field"
            }
        }
        foreach ($property in @($Value.PSObject.Properties)) {
            $propertySchema = $Schema.properties.PSObject.Properties[$property.Name]
            if ($null -ne $propertySchema) {
                Assert-SchemaValue $property.Value $propertySchema.Value $RootSchema `
                    "$Context.$($property.Name)"
            }
        }
    }
}

# Assert-UniqueSortedStrings 验证稳定字符串列表严格递增且无重复。
function Assert-UniqueSortedStrings {
    param([object[]]$Values, [string]$Context)
    $previous = $null
    foreach ($value in @($Values)) {
        $current = [string]$value
        if ([string]::IsNullOrWhiteSpace($current)) {
            Fail-BattleModel "$Context contains an empty value"
        }
        if ($null -ne $previous -and $Ordinal.Compare($previous, $current) -ge 0) {
            Fail-BattleModel "$Context is not unique ordinal order"
        }
        $previous = $current
    }
}

# Assert-UniqueStrings 验证概念顺序列表没有空值或重复，但不把展示顺序改写为字典序。
function Assert-UniqueStrings {
    param([object[]]$Values, [string]$Context)
    $seen = @{}
    foreach ($value in @($Values)) {
        $current = [string]$value
        if ([string]::IsNullOrWhiteSpace($current) -or $seen.ContainsKey($current)) {
            Fail-BattleModel "$Context contains an empty or duplicate value"
        }
        $seen[$current] = $true
    }
}

# Assert-UniqueSortedObjects 验证对象列表由 selector 产生的稳定 key 严格递增。
function Assert-UniqueSortedObjects {
    param([object[]]$Values, [scriptblock]$Selector, [string]$Context)
    $previous = $null
    foreach ($value in @($Values)) {
        $current = [string](& $Selector $value)
        if ([string]::IsNullOrWhiteSpace($current)) {
            Fail-BattleModel "$Context contains an empty key"
        }
        if ($null -ne $previous -and $Ordinal.Compare($previous, $current) -ge 0) {
            Fail-BattleModel "$Context is not unique ordinal order"
        }
        $previous = $current
    }
}

# Assert-SequentialOrders 验证 expected collection 的显式 order 从 1 连续递增。
function Assert-SequentialOrders {
    param([object[]]$Values, [string]$Context)
    $expectedOrder = 1
    foreach ($value in @($Values)) {
        if ([int64]$value.order -ne $expectedOrder) {
            Fail-BattleModel "$Context order is not contiguous"
        }
        $expectedOrder++
    }
}

# Assert-JsonEquivalent 验证 manifest 投影与 case 源字段完全一致。
function Assert-JsonEquivalent {
    param([object]$Left, [object]$Right, [string]$Context)
    $leftJson = $Left | ConvertTo-Json -Compress -Depth 100
    $rightJson = $Right | ConvertTo-Json -Compress -Depth 100
    if ($leftJson -cne $rightJson) {
        Fail-BattleModel "$Context differs"
    }
}

# Assert-NoForbiddenModelData 拒绝模型 JSON 中出现 wire、凭据、个人身份或第三方对象字段。
function Assert-NoForbiddenModelData {
    param([string]$Text, [string]$Context)
    $forbiddenField = '(?i)"(?:message[_-]?id|lane|ticket|credential|password|token|nonce|aead[_-]?key|username|email|player[_-]?id|session[_-]?id|endpoint|ip|port|packet|wire)"\s*:'
    $forbiddenType = '(?i)\b(?:jolt|detour|asio|kcp|unityengine)\b'
    if ($Text -match $forbiddenField -or $Text -match $forbiddenType) {
        Fail-BattleModel "$Context contains forbidden data"
    }
}

# Assert-ActorCollection 验证 actor identity 与 tag 使用稳定顺序。
function Assert-ActorCollection {
    param([object[]]$Actors, [string]$Context)
    Assert-UniqueSortedObjects @($Actors) { param($actor) $actor.actor_id } "$Context actors"
    foreach ($actor in @($Actors)) {
        Assert-UniqueSortedStrings @($actor.tags) "$Context actor tags"
    }
}

# Assert-EntityCollection 验证非 actor entity identity 使用稳定顺序。
function Assert-EntityCollection {
    param([object[]]$Entities, [string]$Context)
    Assert-UniqueSortedObjects @($Entities) { param($entity) $entity.entity_id } `
        "$Context entities"
}

# Get-CommandOrderKey 生成与模型规范相同的 command 裁决 key；arrival 仅作完全相同输入的最后 tie-break。
function Get-CommandOrderKey {
    param([object]$Command)
    return "{0:D20}|{1}|{2:D20}|{3:D20}|{4}|{5:D20}" -f
        [int64]$Command.target_tick,
        [string]$Command.actor_id,
        [int64]$Command.input_tick,
        [int64]$Command.sequence,
        [string]$Command.kind,
        [int64]$Command.arrival_order
}

# Assert-Mapping 验证 current generation command 使用冻结整数公式映射目标 Tick。
function Assert-Mapping {
    param([object]$Case, [string]$Context)
    $epoch = $Case.initial_state.mapping_epoch
    foreach ($command in @($Case.commands)) {
        $disposition = "accepted"
        if (@($command.PSObject.Properties.Name) -contains "ingress_disposition") {
            $disposition = [string]$command.ingress_disposition
        }
        if ($disposition -ne "accepted" -or
            [int64]$command.assignment_generation -ne
                [int64]$Case.initial_state.assignment_generation -or
            [int64]$command.mapping_epoch_id -ne [int64]$epoch.epoch_id) {
            continue
        }
        if ([int64]$command.input_tick -lt [int64]$epoch.base_input_tick) {
            Fail-BattleModel "$Context maps an input before epoch base"
        }
        $delta = [decimal]([int64]$command.input_tick - [int64]$epoch.base_input_tick)
        $scaled = $delta * [decimal][int64]$epoch.input_step_ns
        $offset = [decimal]::Floor($scaled / [decimal][int64]$epoch.simulation_step_ns)
        $mapped = [decimal][int64]$epoch.base_simulation_tick + $offset
        if ($mapped -gt [decimal][int64]::MaxValue -or
            [int64]$command.target_tick -ne [int64]$mapped) {
            Fail-BattleModel "$Context command target Tick differs from mapping"
        }
    }
}

# Assert-TraceSemantics 验证记录化 port 输入的 identity、排序与独立摘要。
function Assert-TraceSemantics {
    param([object]$Case, [string]$Context)
    $traces = $Case.adapter_traces
    Assert-UniqueSortedObjects @($traces.physics_queries) {
        param($query) $query.query_id
    } "$Context physics queries"
    foreach ($query in @($traces.physics_queries)) {
        Assert-UniqueSortedObjects @($query.hits) {
            param($hit)
            "{0:D7}|{1}|{2}" -f [int64]$hit.fraction_millionths,
                [string]$hit.collider_id, [string]$hit.subshape_id
        } "$Context physics hits"
    }
    Assert-UniqueSortedObjects @($traces.navigation_results) {
        param($query) $query.query_id
    } "$Context navigation results"
    Assert-UniqueSortedObjects @($traces.random_streams) {
        param($stream)
        "$($stream.system_id)|$($stream.entity_id)|$($stream.stream_id)"
    } "$Context random streams"
    if (@($traces.PSObject.Properties.Name) -contains "ai_perceptions") {
        Assert-UniqueSortedObjects @($traces.ai_perceptions) {
            param($perception)
            "{0:D20}|{1}" -f [int64]$perception.tick, [string]$perception.actor_id
        } "$Context AI perceptions"
        foreach ($perception in @($traces.ai_perceptions)) {
            Assert-UniqueSortedObjects @($perception.candidates) {
                param($candidate) $candidate.target_actor_id
            } "$Context AI candidates"
        }
    }
    if (@($traces.PSObject.Properties.Name) -contains "ai_lifecycle_traces") {
        Assert-UniqueSortedObjects @($traces.ai_lifecycle_traces) {
            param($trace) $trace.actor_id
        } "$Context AI lifecycle traces"
    }
    if (@($traces.PSObject.Properties.Name) -contains "history_frames") {
        Assert-UniqueSortedObjects @($traces.history_frames) {
            param($frame) "{0:D20}" -f [int64]$frame.server_tick
        } "$Context history frames"
        foreach ($frame in @($traces.history_frames)) {
            Assert-UniqueSortedObjects @($frame.actors) {
                param($actor) $actor.actor_id
            } "$Context history actors"
            foreach ($actor in @($frame.actors)) {
                Assert-UniqueSortedStrings @($actor.tags) "$Context history actor tags"
            }
        }
    }
    if (@($traces.PSObject.Properties.Name) -contains "history_queries") {
        Assert-UniqueSortedObjects @($traces.history_queries) {
            param($query) "{0:D20}|{1}" -f [int64]$query.current_tick,
                [string]$query.query_id
        } "$Context history queries"
    }
    $traceFields = @($traces.PSObject.Properties.Name)
    $hasQueryOutput = $traceFields -contains "canonical_query_output"
    $hasQueryHash = $traceFields -contains "canonical_query_sha256"
    if ($hasQueryOutput -ne $hasQueryHash) {
        Fail-BattleModel "$Context query digest pair is incomplete"
    }
    if ($hasQueryOutput) {
        $actual = Get-Sha256Text ([string]$traces.canonical_query_output)
        if ($actual -cne [string]$traces.canonical_query_sha256) {
            Fail-BattleModel "$Context query digest differs"
        }
    }
}

# Assert-ExpectedSemantics 验证 checkpoint/event/rejection/capacity 顺序和 canonical 摘要。
function Assert-ExpectedSemantics {
    param([object]$Case, [string]$Context)
    $expected = $Case.expected
    Assert-SequentialOrders @($expected.query_sequence) "$Context query sequence"
    Assert-SequentialOrders @($expected.state_checkpoints) "$Context state checkpoints"
    Assert-SequentialOrders @($expected.events) "$Context events"
    Assert-SequentialOrders @($expected.rejections) "$Context rejections"
    foreach ($checkpoint in @($expected.state_checkpoints)) {
        Assert-ActorCollection @($checkpoint.actors) "$Context checkpoint"
        Assert-EntityCollection @($checkpoint.entities) "$Context checkpoint"
    }
    if (@($expected.PSObject.Properties.Name) -contains "capacity_trials") {
        Assert-UniqueSortedObjects @($expected.capacity_trials) {
            param($trial) $trial.kind
        } "$Context capacity trials"
    }
    $actual = Get-Sha256Text ([string]$expected.canonical_output)
    if ($actual -cne [string]$expected.canonical_sha256) {
        Fail-BattleModel "$Context canonical digest differs"
    }
}

# Assert-CaseSemantics 验证 schema 之外的跨字段不变量和稳定顺序。
function Assert-CaseSemantics {
    param([object]$Case, [object]$Schema, [string]$Context)
    Assert-SchemaValue $Case $Schema $Schema $Context
    Assert-UniqueSortedStrings @($Case.requirements) "$Context requirements"
    Assert-UniqueSortedStrings @($Case.coverage.positive) "$Context positive coverage"
    Assert-UniqueSortedStrings @($Case.coverage.negative) "$Context negative coverage"
    Assert-UniqueSortedStrings @($Case.coverage.determinism) `
        "$Context determinism coverage"
    Assert-ActorCollection @($Case.initial_state.actors) "$Context initial state"
    Assert-EntityCollection @($Case.initial_state.entities) "$Context initial state"
    Assert-UniqueSortedObjects @($Case.commands) {
        param($command) Get-CommandOrderKey $command
    } "$Context commands"
    Assert-Mapping $Case $Context
    Assert-TraceSemantics $Case $Context
    Assert-ExpectedSemantics $Case $Context
}

# Assert-Assumptions 验证 workload 事实、假设和待测输出没有混淆为资格结论。
function Assert-Assumptions {
    param([object]$Assumptions)
    Assert-ClosedProperties $Assumptions @(
        "format_version", "model_version", "ownership", "visit_capacity",
        "workloads", "profile_outputs", "measurement_rules") @(
        "format_version", "model_version", "ownership", "visit_capacity",
        "workloads", "profile_outputs", "measurement_rules") "assumptions"
    if ($Assumptions.format_version -cne "1" -or
        $Assumptions.model_version -cne "battle-model-v1") {
        Fail-BattleModel "assumptions version differs"
    }
    Assert-ClosedProperties $Assumptions.ownership @(
        "owner", "consumer", "qualification_state", "source_facts") @(
        "owner", "consumer", "qualification_state", "source_facts") `
        "assumptions ownership"
    if ($Assumptions.ownership.owner -cne "battle-simulation-model" -or
        $Assumptions.ownership.consumer -cne "battle-network-profile" -or
        $Assumptions.ownership.qualification_state -cne "unmeasured") {
        Fail-BattleModel "assumptions ownership differs"
    }
    Assert-UniqueStrings @($Assumptions.ownership.source_facts) `
        "assumptions source facts"
    Assert-ClosedProperties $Assumptions.visit_capacity @(
        "owner_count", "default_visitor_count",
        "maximum_configurable_visitor_count", "profile_capacity_gate_required") @(
        "owner_count", "default_visitor_count",
        "maximum_configurable_visitor_count", "profile_capacity_gate_required") `
        "visit capacity"
    $capacityFacts = @(
        @($Assumptions.visit_capacity.owner_count, 1),
        @($Assumptions.visit_capacity.default_visitor_count, 4),
        @($Assumptions.visit_capacity.maximum_configurable_visitor_count, 32)
    )
    foreach ($fact in $capacityFacts) {
        Assert-ClosedProperties $fact[0] @("value", "unit", "classification") `
            @("value", "unit", "classification") "visit capacity fact"
        if ([int]$fact[0].value -ne [int]$fact[1] -or
            $fact[0].unit -cne "actors" -or
            $fact[0].classification -cne "hard_contract") {
            Fail-BattleModel "visit capacity fact differs"
        }
    }
    if (-not [bool]$Assumptions.visit_capacity.profile_capacity_gate_required) {
        Fail-BattleModel "profile capacity gate was disabled"
    }
    Assert-UniqueStrings @($Assumptions.workloads.id) "workload IDs"
    $expectedWorkloads = @("solo-owner", "default-coop", "visit-capacity-compatibility")
    if (@($Assumptions.workloads).Count -ne $expectedWorkloads.Count) {
        Fail-BattleModel "workload inventory differs"
    }
    foreach ($expectedWorkload in $expectedWorkloads) {
        if ($expectedWorkload -notin @($Assumptions.workloads.id)) {
            Fail-BattleModel "workload inventory differs"
        }
    }
    foreach ($workload in @($Assumptions.workloads)) {
        Assert-ClosedProperties $workload @(
            "id", "classification", "players", "ordinary_monsters", "bosses",
            "peak_projectiles", "peak_active_effects",
            "peak_physics_queries_per_tick", "peak_navigation_queries_per_tick",
            "peak_events_per_tick", "phases") @(
            "id", "classification", "players", "ordinary_monsters", "bosses",
            "peak_projectiles", "peak_active_effects",
            "peak_physics_queries_per_tick", "peak_navigation_queries_per_tick",
            "peak_events_per_tick", "phases") "workload"
        if ($workload.classification -cne "assumption") {
            Fail-BattleModel "workload was promoted beyond assumption"
        }
        foreach ($field in @(
                "players", "ordinary_monsters", "bosses", "peak_projectiles",
                "peak_active_effects", "peak_physics_queries_per_tick",
                "peak_navigation_queries_per_tick", "peak_events_per_tick")) {
            if ([int64]$workload.$field -lt 1) {
                Fail-BattleModel "workload contains a non-positive dimension"
            }
        }
        Assert-JsonEquivalent @($workload.phases) @(
            "idle", "movement-heavy", "combat-heavy", "boss-burst",
            "disconnect-drain") "workload phases"
    }
    Assert-UniqueStrings @($Assumptions.profile_outputs.id) "profile outputs"
    foreach ($output in @($Assumptions.profile_outputs)) {
        Assert-ClosedProperties $output @(
            "id", "unit", "classification", "status") @(
            "id", "unit", "classification", "status") "profile output"
        if ($output.classification -cne "profile_output" -or
            $output.status -cne "unmeasured") {
            Fail-BattleModel "profile output claims an unmeasured qualification"
        }
    }
    Assert-UniqueStrings @($Assumptions.measurement_rules) "measurement rules"
}

# Assert-ManifestShape 验证 manifest、entry 与 supplemental coverage 都是闭合结构。
function Assert-ManifestShape {
    param([object]$Manifest)
    Assert-ClosedProperties $Manifest @(
        "format_version", "model_version", "schema_path", "assumptions_path",
        "required_requirements", "supplemental_coverage", "cases") @(
        "format_version", "model_version", "schema_path", "assumptions_path",
        "required_requirements", "supplemental_coverage", "cases") "manifest"
    if ($Manifest.format_version -cne "1" -or
        $Manifest.model_version -cne "battle-model-v1" -or
        $Manifest.schema_path -cne "schema.json" -or
        $Manifest.assumptions_path -cne "assumptions.json") {
        Fail-BattleModel "manifest header differs"
    }
    Assert-UniqueSortedStrings @($Manifest.required_requirements) `
        "required requirements"
    Assert-UniqueSortedObjects @($Manifest.supplemental_coverage) {
        param($entry) $entry.requirement
    } "supplemental coverage"
    foreach ($entry in @($Manifest.supplemental_coverage)) {
        Assert-ClosedProperties $entry @("requirement", "paths") `
            @("requirement", "paths") "supplemental coverage entry"
        Assert-UniqueSortedStrings @($entry.paths) "supplemental coverage paths"
        foreach ($path in @($entry.paths)) {
            $full = Resolve-ModelPath ([string]$path)
            if (-not (Test-Path -LiteralPath $full -PathType Leaf)) {
                Fail-BattleModel "supplemental coverage path is missing"
            }
        }
    }
    Assert-UniqueSortedObjects @($Manifest.cases) {
        param($entry) $entry.case_id
    } "manifest cases"
    foreach ($entry in @($Manifest.cases)) {
        Assert-ClosedProperties $entry @(
            "case_id", "path", "category", "model_version", "config_version",
            "file_sha256", "requirements", "coverage") @(
            "case_id", "path", "category", "model_version", "config_version",
            "file_sha256", "requirements", "coverage") "manifest case"
        Assert-UniqueSortedStrings @($entry.requirements) `
            "manifest case requirements"
        Assert-ClosedProperties $entry.coverage @(
            "positive", "negative", "determinism") @(
            "positive", "negative", "determinism") "manifest case coverage"
        Assert-UniqueSortedStrings @($entry.coverage.positive) `
            "manifest positive coverage"
        Assert-UniqueSortedStrings @($entry.coverage.negative) `
            "manifest negative coverage"
        Assert-UniqueSortedStrings @($entry.coverage.determinism) `
            "manifest determinism coverage"
    }
}

# Invoke-BattleModelValidation 执行完整只读门，并输出不含路径或时间的稳定摘要。
function Invoke-BattleModelValidation {
    $manifestText = Read-CanonicalText $ManifestPath
    $schemaText = Read-CanonicalText $SchemaPath
    $assumptionsText = Read-CanonicalText $AssumptionsPath
    Assert-NoForbiddenModelData $manifestText "manifest"
    Assert-NoForbiddenModelData $assumptionsText "assumptions"
    $manifest = $manifestText | ConvertFrom-Json
    $schema = $schemaText | ConvertFrom-Json
    $assumptions = $assumptionsText | ConvertFrom-Json
    Assert-ManifestShape $manifest
    Assert-Assumptions $assumptions

    $actualCasePaths = @(
        Get-ChildItem -LiteralPath (Join-Path $ModelRoot "cases") `
            -Filter "*.json" -File -Recurse |
        ForEach-Object { Get-RelativeModelPath $_.FullName } |
        Sort-Object)
    $manifestCasePaths = @(
        $manifest.cases |
        ForEach-Object { [string]$_.path } |
        Sort-Object)
    Assert-JsonEquivalent $actualCasePaths $manifestCasePaths "case path inventory"

    $coveredRequirements = @{}
    foreach ($entry in @($manifest.supplemental_coverage)) {
        $coveredRequirements[[string]$entry.requirement] = $true
    }
    foreach ($entry in @($manifest.cases)) {
        $path = Resolve-ModelPath ([string]$entry.path)
        $text = Read-CanonicalText $path
        Assert-NoForbiddenModelData $text "case"
        $case = $text | ConvertFrom-Json
        $context = "case $($entry.case_id)"
        Assert-CaseSemantics $case $schema $context
        if ($case.case_id -cne $entry.case_id -or
            $case.category -cne $entry.category -or
            $case.model_version -cne $entry.model_version -or
            $case.config_version -cne $entry.config_version) {
            Fail-BattleModel "$context manifest identity differs"
        }
        Assert-JsonEquivalent @($case.requirements) @($entry.requirements) `
            "$context requirement projection"
        Assert-JsonEquivalent $case.coverage $entry.coverage `
            "$context coverage projection"
        $fileHash = Get-Sha256File $path
        if ($fileHash -cne [string]$entry.file_sha256) {
            Fail-BattleModel "$context file digest differs"
        }
        foreach ($requirement in @($case.requirements)) {
            $coveredRequirements[[string]$requirement] = $true
        }
    }
    foreach ($requirement in @($manifest.required_requirements)) {
        if (-not $coveredRequirements.ContainsKey([string]$requirement)) {
            Fail-BattleModel "required requirement coverage is missing"
        }
    }
    foreach ($requirement in @($coveredRequirements.Keys)) {
        if ($requirement -notin @($manifest.required_requirements)) {
            Fail-BattleModel "unknown requirement coverage is present"
        }
    }
    Write-Output (
        "[OK] Battle model validation passed: {0} cases, {1} requirements." -f
        @($manifest.cases).Count, @($manifest.required_requirements).Count)
}

if ($Action -eq "validate") {
    Invoke-BattleModelValidation
}
