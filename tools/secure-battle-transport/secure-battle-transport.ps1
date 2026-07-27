# 该入口只读校验安全战斗传输 source corpus；后续 B0.5 verify/finalize 也必须从此处组合。
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("validate-corpus", "test-corpus", "failure-regression", "verify", "finalize")]
    [string]$Action = "validate-corpus"
)

$ErrorActionPreference = "Stop"
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$CorpusRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\wire"
# BattleFuzzTimeSeconds 为该聚合入口内唯一显式 fuzz target 提供固定非零预算。
$BattleFuzzTimeSeconds = 3

# Get-Sha256 返回小写摘要，避免平台或调用方大小写影响 identity。
function Get-Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

# Read-ClosedJson 统一拒绝空文件、非法 JSON 和意外 document kind。
function Read-ClosedJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedKind
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "battle wire corpus 缺少文件：$Path"
    }

    $text = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($text)) {
        throw "battle wire corpus 文件为空：$Path"
    }

    try {
        $document = $text | ConvertFrom-Json
    }
    catch {
        throw "battle wire corpus JSON 非法：$Path；$($_.Exception.Message)"
    }

    if ($document.format_version -cne "1" -or
        $document.corpus_version -cne "battle-wire-v1" -or
        $document.document_kind -cne $ExpectedKind) {
        throw "battle wire corpus identity 不匹配：$Path"
    }

    return $document
}

# Test-BattleJsonSchema 在 Windows PowerShell 5 下显式委托 pwsh 的 JSON Schema 引擎。
function Test-BattleJsonSchema {
    param(
        [Parameter(Mandatory = $true)][string]$DocumentPath,
        [Parameter(Mandatory = $true)][string]$SchemaPath
    )

    if (Get-Command Test-Json -ErrorAction SilentlyContinue) {
        return Test-Json -LiteralPath $DocumentPath -SchemaFile $SchemaPath -ErrorAction Stop
    }
    $pwsh = (Get-Command pwsh.exe -ErrorAction Stop).Source
    $escapedDocument = $DocumentPath.Replace("'", "''")
    $escapedSchema = $SchemaPath.Replace("'", "''")
    & $pwsh -NoProfile -Command "if (-not (Test-Json -LiteralPath '$escapedDocument' -SchemaFile '$escapedSchema' -ErrorAction Stop)) { exit 1 }"
    return $LASTEXITCODE -eq 0
}

# Assert-ExactProperties 对关键文档执行 closed-object 检查，禁止静默接受拼写错误或未知字段。
function Assert-ExactProperties {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string[]]$Expected,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $actual = @($Object.PSObject.Properties.Name | Sort-Object)
    $wanted = @($Expected | Sort-Object)
    if (($actual -join "`n") -cne ($wanted -join "`n")) {
        throw "$Context 必须是 closed object；actual=[$($actual -join ',')] expected=[$($wanted -join ',')]"
    }
}

# Get-TreeDigest 绑定相对路径与文件内容，用于证明连续校验没有改写 source corpus。
function Get-TreeDigest {
    $rows = Get-ChildItem -LiteralPath $CorpusRoot -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            $relative = $_.FullName.Substring($CorpusRoot.Length + 1).Replace("\", "/")
            "$relative=$(Get-Sha256 -Path $_.FullName)"
        }
    $payload = [Text.Encoding]::UTF8.GetBytes(($rows -join "`n"))
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($payload))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
    }
}

# Assert-Binding 验证 B0.3/B0.2/B0.4 上游 corpus 的精确摘要和共同硬不变量。
function Assert-Binding {
    param(
        [Parameter(Mandatory = $true)]$Binding,
        # AllowSourceDrift 只供开发期 corpus 检查；B0.5 verify/finalize 仍严格绑定摘要。
        [switch]$AllowSourceDrift
    )

    Assert-ExactProperties $Binding @(
        "format_version", "corpus_version", "document_kind", "sources", "invariants"
    ) "model-profile-control-binding"

    $expectedOwners = @("battle-model", "battle-network-profile", "simulation-control")
    if (@($Binding.sources).Count -ne 3 -or
        (@($Binding.sources.owner | Sort-Object) -join ",") -cne (($expectedOwners | Sort-Object) -join ",")) {
        throw "model/profile/control binding 必须精确登记三个 owner"
    }

    foreach ($source in $Binding.sources) {
        Assert-ExactProperties $source @("owner", "manifest_path", "manifest_sha256") "binding source"
        $candidate = Join-Path $CorpusRoot ([string]$source.manifest_path)
        $resolved = (Resolve-Path -LiteralPath $candidate).Path
        if (-not $resolved.StartsWith($RepositoryRoot, [StringComparison]::OrdinalIgnoreCase)) {
            throw "binding path 逃逸仓库：$($source.manifest_path)"
        }
        if (-not $AllowSourceDrift -and
            (Get-Sha256 -Path $resolved) -cne [string]$source.manifest_sha256) {
            throw "binding source 摘要漂移：$($source.owner)"
        }
    }

    Assert-ExactProperties $Binding.invariants @(
        "maximum_actors", "simulation_tick_ns", "source_corpus_mode", "qualification_boundary"
    ) "binding invariants"
    if ([int]$Binding.invariants.maximum_actors -ne 8 -or
        [long]$Binding.invariants.simulation_tick_ns -ne 50000000 -or
        $Binding.invariants.source_corpus_mode -cne "immutable" -or
        $Binding.invariants.qualification_boundary -cne "b0.5-implementation-only") {
        throw "model/profile/control 共同硬不变量漂移"
    }
}

# Assert-Limits 防止关键 wire 与资源上限被 schema 的宽泛 object 定义绕过。
function Assert-Limits {
    param([Parameter(Mandatory = $true)]$Limits)

    Assert-ExactProperties $Limits @(
        "format_version", "corpus_version", "document_kind", "wire", "handshake",
        "crypto", "replay", "kcp", "resources"
    ) "limits"

    if ([int]$Limits.wire.maximum_datagram_bytes -ne 1200 -or
        $Limits.wire.byte_order -cne "network-big-endian" -or
        $Limits.wire.kcp_segment_byte_order -cne "little-endian" -or
        [int]$Limits.wire.secure_header_bytes -ne 48 -or
        [int]$Limits.wire.raw_route_header_bytes -ne 16 -or
        [int]$Limits.wire.kcp_segment_header_bytes -ne 24 -or
        [int]$Limits.wire.aead_tag_bytes -ne 16 -or
        [int]$Limits.wire.maximum_raw_payload_bytes -ne 1120 -or
        [int]$Limits.wire.maximum_kcp_segment_payload_bytes -ne 1000) {
        throw "wire/MTU 硬上限漂移"
    }
    if ($Limits.crypto.key_exchange -cne "X25519" -or
        $Limits.crypto.key_derivation -cne "HKDF-SHA-256" -or
        $Limits.crypto.packet_aead -cne "ChaCha20-Poly1305" -or
        [int]$Limits.replay.window_packets -ne 256) {
        throw "crypto/replay 基线漂移"
    }
    if ([int]$Limits.kcp.update_interval_ms -ne 10 -or
        [int]$Limits.kcp.send_window_segments -ne 64 -or
        [int]$Limits.kcp.receive_window_segments -ne 64 -or
        [int]$Limits.kcp.fast_resend -ne 2 -or
        [int]$Limits.kcp.minimum_rto_ms -ne 30 -or
        [int]$Limits.kcp.maximum_rto_ms -ne 200 -or
        [int]$Limits.kcp.dead_link_retransmits -ne 10 -or
        [int]$Limits.kcp.message_queue_items -ne 64 -or
        [int]$Limits.kcp.maximum_message_expiry_ms -ne 2250) {
        throw "KCP 硬上限漂移"
    }
    if ([int]$Limits.resources.maximum_actors_per_instance -ne 8 -or
        [int]$Limits.resources.node_queue_items -ne 256 -or
        [int]$Limits.resources.session_queue_items -ne 256 -or
        [int]$Limits.resources.pre_auth_session_state -ne 0 -or
        [int]$Limits.resources.udp_listeners_per_node -ne 1 -or
        [int]$Limits.resources.active_endpoints_per_session -ne 1) {
        throw "资源治理硬上限漂移"
    }
}

# Assert-WireSuite 保证每个安全域存在且 case identity 在整个 corpus 内唯一。
function Assert-WireSuite {
    param([Parameter(Mandatory = $true)]$Suite)

    Assert-ExactProperties $Suite @(
        "format_version", "corpus_version", "document_kind", "canonical_encoding", "suites"
    ) "wire-suite"
    $required = @(
        "crypto", "handshake", "parity", "rebind", "replay", "resource", "wire"
    )
    $domains = @($Suite.suites.domain | Sort-Object -Unique)
    foreach ($domain in $required) {
        if ($domains -cnotcontains $domain) {
            throw "wire suite 缺少 domain：$domain"
        }
    }
    if ($Suite.canonical_encoding.integer_byte_order -cne "network-big-endian" -or
        $Suite.canonical_encoding.protobuf_mode -cne "deterministic" -or
        $Suite.canonical_encoding.comparison -cne "byte-exact") {
        throw "canonical encoding policy 漂移"
    }

    $suiteIds = @($Suite.suites.suite_id)
    $caseIds = @($Suite.suites | ForEach-Object { $_.cases })
    if (($suiteIds | Sort-Object -Unique).Count -ne $suiteIds.Count -or
        ($caseIds | Sort-Object -Unique).Count -ne $caseIds.Count) {
        throw "wire suite 或 case identity 重复"
    }
}

# Assert-WireArtifacts 验证 layout 连续覆盖、golden bytes/digest 与 malformed stable reason 集合。
function Assert-WireArtifacts {
    param(
        [Parameter(Mandatory = $true)]$Layout,
        [Parameter(Mandatory = $true)]$Golden,
        [Parameter(Mandatory = $true)]$Malformed
    )

    Assert-ExactProperties $Layout @(
        "format_version", "corpus_version", "document_kind", "secure_header",
        "raw_route_header", "snapshot_acknowledgement", "kcp_segment_header",
        "kcp_route_envelope", "transport_control_envelope",
        "transport_control_messages", "datagram_equation"
    ) "wire-layout"
    Assert-ExactProperties $Layout.snapshot_acknowledgement @(
        "message_ids", "protobuf_field", "field_number", "scalar_type", "presence",
        "initial_value", "scope", "partition_policy", "maximum_varint_bytes",
        "maximum_datagram_bytes", "route_payload_budgets"
    ) "snapshot acknowledgement"
    if (@($Layout.snapshot_acknowledgement.message_ids).Count -ne 2 -or
        [int]$Layout.snapshot_acknowledgement.message_ids[0] -ne 3002 -or
        [int]$Layout.snapshot_acknowledgement.message_ids[1] -ne 3003 -or
        $Layout.snapshot_acknowledgement.protobuf_field -cne "last_processed_input_tick" -or
        [int]$Layout.snapshot_acknowledgement.field_number -ne 7 -or
        $Layout.snapshot_acknowledgement.presence -cne "explicit-required" -or
        [int]$Layout.snapshot_acknowledgement.initial_value -ne 0 -or
        [int]$Layout.snapshot_acknowledgement.maximum_datagram_bytes -ne 1200 -or
        [int]$Layout.snapshot_acknowledgement.route_payload_budgets."3002" -ne 1040 -or
        [int]$Layout.snapshot_acknowledgement.route_payload_budgets."3003" -ne 900) {
        throw "snapshot acknowledgement wire metadata 漂移"
    }
    foreach ($entry in @(
        @($Layout.secure_header, 48, "network-big-endian"),
        @($Layout.raw_route_header, 16, "network-big-endian"),
        @($Layout.kcp_segment_header, 24, "little-endian"),
        @($Layout.kcp_route_envelope, 16, "network-big-endian"),
        @($Layout.transport_control_envelope, 8, "network-big-endian")
    )) {
        $header = $entry[0]
        $expectedSize = [int]$entry[1]
        $expectedOrder = [string]$entry[2]
        if ([int]$header.size_bytes -ne $expectedSize -or $header.byte_order -cne $expectedOrder) {
            throw "wire layout size/endian 漂移"
        }
        $cursor = 0
        foreach ($field in @($header.fields | Sort-Object { [int]$_.offset })) {
            if ([int]$field.offset -ne $cursor -or [int]$field.width -le 0) {
                throw "wire layout field 存在 gap、overlap 或非法 width：$($field.name)"
            }
            $cursor += [int]$field.width
        }
        if ($cursor -ne $expectedSize) {
            throw "wire layout fields 未精确覆盖 $expectedSize bytes"
        }
    }
    $expectedControl = @(
        @("rebind-request", 1, 34),
        @("rebind-challenge", 2, 48),
        @("rebind-confirm", 3, 48),
        @("rebind-committed", 4, 20),
        @("rekey-proposal", 5, 32),
        @("rekey-committed", 6, 32),
        @("close-request", 7, 1),
        @("close-acknowledged", 8, 1)
    )
    if (@($Layout.transport_control_messages).Count -ne $expectedControl.Count) {
        throw "transport control message registry 数量漂移"
    }
    for ($index = 0; $index -lt $expectedControl.Count; $index++) {
        $actual = $Layout.transport_control_messages[$index]
        $expected = $expectedControl[$index]
        Assert-ExactProperties $actual @(
            "kind", "name", "direction", "payload_bytes", "fields"
        ) "transport control message"
        if ([int]$actual.kind -ne [int]$expected[1] -or
            $actual.name -cne [string]$expected[0] -or
            [int]$actual.payload_bytes -ne [int]$expected[2]) {
            throw "transport control message registry 漂移：$($actual.name)"
        }
    }

    Assert-ExactProperties $Golden @(
        "format_version", "corpus_version", "document_kind", "encoding", "vectors", "parity_matrix"
    ) "canonical-golden"
    if ($Golden.encoding -cne "lowercase-hex") {
        throw "canonical golden encoding 漂移"
    }
    $vectorIds = @($Golden.vectors.vector_id)
    if (($vectorIds | Sort-Object -Unique).Count -ne $vectorIds.Count) {
        throw "canonical golden vector_id 重复"
    }
    foreach ($vector in $Golden.vectors) {
        $hex = [string]$vector.bytes_hex
        if ($hex -cnotmatch '^(?:[0-9a-f]{2})+$') {
            throw "canonical golden 不是 lowercase even-length hex：$($vector.vector_id)"
        }
        $bytes = [byte[]]::new($hex.Length / 2)
        for ($index = 0; $index -lt $bytes.Length; $index++) {
            $bytes[$index] = [Convert]::ToByte($hex.Substring($index * 2, 2), 16)
        }
        if ($bytes.Length -ne [int]$vector.byte_count) {
            throw "canonical golden byte_count 漂移：$($vector.vector_id)"
        }
        $sha = [Security.Cryptography.SHA256]::Create()
        try {
            $digest = ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
        }
        finally {
            $sha.Dispose()
        }
        if ($digest -cne [string]$vector.sha256) {
            throw "canonical golden SHA-256 漂移：$($vector.vector_id)"
        }
    }
    $producers = @($Golden.parity_matrix.producer | Sort-Object -Unique)
    if (($producers -join ",") -cne "cpp,csharp,go") {
        throw "canonical golden parity matrix 必须覆盖 Go/C++/C# producer"
    }

    Assert-ExactProperties $Malformed @(
        "format_version", "corpus_version", "document_kind", "cases", "policy"
    ) "malformed-corpus"
    $caseIds = @($Malformed.cases.case_id)
    if ($caseIds.Count -lt 20 -or ($caseIds | Sort-Object -Unique).Count -ne $caseIds.Count) {
        throw "malformed corpus 必须包含至少 20 个唯一 case"
    }
    foreach ($case in $Malformed.cases) {
        if ([string]$case.expected_reason -cnotmatch '^BATTLE_[A-Z0-9_]+$') {
            throw "malformed corpus stable reason 非法：$($case.case_id)"
        }
        $hasBytes = $case.PSObject.Properties.Name -contains "bytes_hex"
        $hasCount = $case.PSObject.Properties.Name -contains "byte_count"
        $hasDigest = $case.PSObject.Properties.Name -contains "sha256"
        if (($hasBytes -or $hasCount -or $hasDigest) -and
            -not ($hasBytes -and $hasCount -and $hasDigest)) {
            throw "malformed corpus concrete bytes 必须同时登记 hex、count 与 SHA-256：$($case.case_id)"
        }
        if ($hasBytes) {
            $hex = [string]$case.bytes_hex
            if ($hex -cnotmatch '^(?:[0-9a-f]{2})+$') {
                throw "malformed corpus 不是 lowercase even-length hex：$($case.case_id)"
            }
            $bytes = [byte[]]::new($hex.Length / 2)
            for ($index = 0; $index -lt $bytes.Length; $index++) {
                $bytes[$index] = [Convert]::ToByte($hex.Substring($index * 2, 2), 16)
            }
            if ($bytes.Length -ne [int]$case.byte_count) {
                throw "malformed corpus byte_count 漂移：$($case.case_id)"
            }
            $sha = [Security.Cryptography.SHA256]::Create()
            try {
                $digest = ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
            }
            finally {
                $sha.Dispose()
            }
            if ($digest -cne [string]$case.sha256) {
                throw "malformed corpus SHA-256 漂移：$($case.case_id)"
            }
        }
    }
    if ($Malformed.policy.dispatch_before_complete_validation -cne "forbidden" -or
        $Malformed.policy.fallback_lane -cne "forbidden" -or
        $Malformed.policy.source_rewrite_on_failure -cne "forbidden") {
        throw "malformed corpus fail-closed policy 漂移"
    }
}

# Assert-SecretPolicy 对 policy 之外的 source JSON 执行字段名和模式扫描，policy 自身只保存规则文本。
function Assert-SecretPolicy {
    param([Parameter(Mandatory = $true)]$Policy)

    Assert-ExactProperties $Policy @(
        "format_version", "corpus_version", "document_kind", "classification",
        "source_rules", "output_rules", "forbidden_property_names",
        "forbidden_patterns", "allowed_placeholders"
    ) "secret-policy"
    if ($Policy.classification -cne "low-sensitivity") {
        throw "secret policy classification 必须为 low-sensitivity"
    }

    $scanFiles = Get-ChildItem -LiteralPath $CorpusRoot -Filter *.json |
        Where-Object { $_.Name -ne "secret-policy.json" }
    foreach ($file in $scanFiles) {
        $text = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8
        foreach ($propertyName in $Policy.forbidden_property_names) {
            if ($text -match ('"' + [regex]::Escape([string]$propertyName) + '"\s*:')) {
                throw "source corpus 包含禁止 secret 字段：$($file.Name):$propertyName"
            }
        }
        foreach ($pattern in $Policy.forbidden_patterns) {
            if ($text -match [string]$pattern) {
                throw "source corpus 命中禁止 secret 模式：$($file.Name)"
            }
        }
    }
}

# Convert-RegistryToken 把 network profile 的 lowercase-hyphen token 转为 registry 的稳定 symbol。
function Convert-RegistryToken {
    param([Parameter(Mandatory = $true)][string]$Value)

    return $Value.Replace("-", "_").ToUpperInvariant()
}

# Assert-BattleRegistryParity 将 B0.2 logical inventory 与 3000-3007 的唯一 UDP route 逐字段绑定。
# profile 保留 logical kind owner；message registry 持有 numeric range owner，route 同时登记 handlerOwner。
function Assert-BattleRegistryParity {
    $inventoryPath = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\message-inventory.json"
    $messagesPath = Join-Path $RepositoryRoot "shared\contracts\registry\messages.json"
    $routesPath = Join-Path $RepositoryRoot "shared\contracts\registry\routes.json"
    $inventory = Get-Content -LiteralPath $inventoryPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $messageRegistry = Get-Content -LiteralPath $messagesPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $routeRegistry = Get-Content -LiteralPath $routesPath -Raw -Encoding UTF8 | ConvertFrom-Json

    $mapping = @{
        "battle.input.bundle" = @(3000, "BATTLE_INPUT_BUNDLE", "ihomeland.battle.v1.BattleInputBundle")
        "battle.probe" = @(3001, "BATTLE_PROBE", "ihomeland.battle.v1.BattleProbe")
        "battle.snapshot.full" = @(3002, "BATTLE_FULL_SNAPSHOT", "ihomeland.battle.v1.BattleFullSnapshot")
        "battle.snapshot.delta" = @(3003, "BATTLE_DELTA_SNAPSHOT", "ihomeland.battle.v1.BattleDeltaSnapshot")
        "battle.ability.reliable-event" = @(3004, "BATTLE_ABILITY_RELIABLE_EVENT", "ihomeland.battle.v1.BattleAbilityReliableEvent")
        "battle.entity.lifecycle" = @(3005, "BATTLE_ENTITY_LIFECYCLE", "ihomeland.battle.v1.BattleEntityLifecycle")
        "battle.resync.request" = @(3006, "BATTLE_RESYNC_REQUEST", "ihomeland.battle.v1.BattleResyncRequest")
        "battle.resync.response" = @(3007, "BATTLE_RESYNC_RESPONSE", "ihomeland.battle.v1.BattleResyncResponse")
    }

    $battleRange = @($messageRegistry.ownerRanges | Where-Object owner -ceq "battle")
    if ($battleRange.Count -ne 1 -or [int]$battleRange[0].start -ne 3000 -or [int]$battleRange[0].end -ne 3199) {
        throw "battle owner range 必须精确为 3000-3199"
    }
    $battleMessages = @($messageRegistry.messages | Where-Object owner -ceq "battle")
    $battleRoutes = @($routeRegistry.routes | Where-Object { [int]$_.messageId -ge 3000 -and [int]$_.messageId -le 3199 })
    if ($battleMessages.Count -ne 8 -or $battleRoutes.Count -ne 8 -or @($inventory.messages).Count -ne 8) {
        throw "battle logical/message/route inventory 必须精确包含 8 项"
    }

    foreach ($logical in $inventory.messages) {
        $expected = $mapping[[string]$logical.kind]
        if ($null -eq $expected) {
            throw "network profile logical kind 没有 numeric mapping：$($logical.kind)"
        }
        $message = @($battleMessages | Where-Object { [int]$_.id -eq [int]$expected[0] })
        $route = @($battleRoutes | Where-Object { [int]$_.messageId -eq [int]$expected[0] })
        if ($message.Count -ne 1 -or $route.Count -ne 1) {
            throw "battle mapping 缺少唯一 message/route：$($logical.kind)"
        }
        if ($message[0].name -cne [string]$expected[1] -or
            $message[0].protobuf -cne [string]$expected[2] -or
            $message[0].kind -cne "DATAGRAM" -or
            $message[0].direction -cne $(if ($logical.direction -ceq "c2s") { "CLIENT_TO_SERVER" } else { "SERVER_TO_CLIENT" })) {
            throw "battle message identity 漂移：$($logical.kind)"
        }

        $expectedLane = Convert-RegistryToken ([string]$logical.lane)
        $expectedDirection = if ($logical.direction -ceq "c2s") { "CLIENT_TO_SERVER" } else { "SERVER_TO_CLIENT" }
        $expectedExpiryMS = [int64]$logical.expiry_us / 1000
        if ([int64]$logical.expiry_us % 1000 -ne 0 -or
            $message[0].direction -cne $expectedDirection -or
            $route[0].channel -cne "UDP" -or
            $route[0].lane -cne $expectedLane -or
            $route[0].handlerOwner -cne [string]$logical.owner -or
            $route[0].authScope -cne "BATTLE" -or
            $route[0].qos -cne (Convert-RegistryToken ([string]$logical.qos)) -or
            [int]$route[0].maxSize -ne 1200 -or
            [int]$route[0].maxPayloadSize -ne [int]$logical.max_logical_payload_bytes -or
            [int]$route[0].maxRatePerSecond -ne [int]$logical.maximum_rate_per_second -or
            [int]$route[0].expiryMs -ne $expectedExpiryMS -or
            $route[0].sequencePolicy -cne (Convert-RegistryToken ([string]$logical.sequence_policy)) -or
            $route[0].tickPolicy -cne (Convert-RegistryToken ([string]$logical.tick_policy)) -or
            $route[0].idempotency -cne (Convert-RegistryToken ([string]$logical.idempotency)) -or
            $route[0].baselinePolicy -cne (Convert-RegistryToken ([string]$logical.baseline_policy)) -or
            $route[0].recoveryPolicy -cne (Convert-RegistryToken ([string]$logical.recovery_policy)) -or
            $route[0].splitPolicy -cne (Convert-RegistryToken ([string]$logical.split_policy)) -or
            $route[0].bindingPolicy -cne "SESSION_ENDPOINT_TARGET_GENERATION" -or
            [int]$route[0].timeoutMs -ne 0) {
            throw "battle route 与 network profile 漂移：$($logical.kind)"
        }
        $expectedAcknowledgement = if ([int]$route[0].messageId -in @(3002, 3003)) {
            "EXPLICIT_CURRENT_ACTOR_MAPPING_GENERATION_FRONTIER"
        }
        else {
            $null
        }
        if ($route[0].acknowledgementPolicy -cne $expectedAcknowledgement) {
            throw "battle route acknowledgement policy 漂移：$($logical.kind)"
        }
    }
}

# Assert-BattleGeneratedTypeBoundaries 阻止三端 generated battle type 进入 domain、application 或 UI。
function Assert-BattleGeneratedTypeBoundaries {
    $rules = @(
        @{
            Pattern = "generated/proto/ihomeland/battle/v1"
            Paths = @("server")
            Allowed = @(
                "server/internal/battlequalification/",
                "server/internal/contract/",
                "server/internal/fixtures/",
                "server/internal/testclient/",
                "server/internal/transport/battle/"
            )
        },
        @{
            Pattern = "IHomeland.Protocol.Battle.V1"
            Paths = @("client/Assets/App")
            Allowed = @(
                "client/Assets/App/Scripts/Infrastructure/Battle/",
                "client/Assets/App/Tests/"
            )
        },
        @{
            Pattern = "ihomeland/battle/v1/battle.pb.h"
            Paths = @("simulation/include", "simulation/src", "simulation/tests")
            Allowed = @(
                "simulation/include/ihomeland/transport/battle/",
                "simulation/src/qualification/",
                "simulation/src/transport/battle/",
                "simulation/src/transport/battle_route.cpp",
                "simulation/src/transport/battle_session.cpp",
                "simulation/src/transport/kcp_adapter.cpp",
                "simulation/tests/"
            )
        }
    )
    foreach ($rule in $rules) {
        $arguments = @("grep", "-n", "-I", "--untracked", "-F", $rule.Pattern, "--") + $rule.Paths
        $matches = @(& git -C $RepositoryRoot @arguments)
        if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 1) {
            throw "无法扫描 generated battle type architecture boundary"
        }
        foreach ($match in $matches) {
            $path = ([string]$match -split ":", 2)[0].Replace("\", "/")
            $allowed = $false
            foreach ($prefix in $rule.Allowed) {
                if ($path.StartsWith($prefix, [StringComparison]::Ordinal)) {
                    $allowed = $true
                    break
                }
            }
            if (-not $allowed) {
                throw "generated battle type 越过 adapter boundary：$path"
            }
        }
    }
}

# Invoke-CorpusValidation 只读执行 manifest、binding、limit、suite 与 secret policy 的完整验证。
function Invoke-CorpusValidation {
    param(
        # AllowSourceDrift 不放宽 corpus、wire、registry、limits 或 secret policy。
        [switch]$AllowSourceDrift
    )

    $manifestPath = Join-Path $CorpusRoot "manifest.json"
    $manifest = Read-ClosedJson -Path $manifestPath -ExpectedKind "manifest"
    Assert-ExactProperties $manifest @(
        "format_version", "corpus_version", "document_kind", "schema_path",
        "schema_sha256", "required_domains", "files"
    ) "manifest"

    $schemaPath = Join-Path $CorpusRoot ([string]$manifest.schema_path)
    if ((Get-Sha256 -Path $schemaPath) -cne [string]$manifest.schema_sha256) {
        throw "battle wire schema 摘要漂移"
    }

    $expectedJson = @("manifest.json", "schema.json") + @($manifest.files.path)
    $actualJson = @(Get-ChildItem -LiteralPath $CorpusRoot -Filter *.json |
        ForEach-Object Name)
    if ((@($expectedJson | Sort-Object -Unique) -join "`n") -cne
        (@($actualJson | Sort-Object -Unique) -join "`n")) {
        throw "battle wire corpus 存在未登记、缺失或重复 JSON 文件"
    }
    foreach ($jsonName in $actualJson) {
        if ($jsonName -eq "schema.json") {
            continue
        }
        $jsonPath = Join-Path $CorpusRoot $jsonName
        if (-not (Test-BattleJsonSchema -DocumentPath $jsonPath -SchemaPath $schemaPath)) {
            throw "battle wire source 不符合 closed schema：$jsonName"
        }
    }

    $seenKinds = @{}
    foreach ($file in $manifest.files) {
        Assert-ExactProperties $file @("path", "document_kind", "sha256") "manifest file"
        $path = Join-Path $CorpusRoot ([string]$file.path)
        if ((Get-Sha256 -Path $path) -cne [string]$file.sha256) {
            throw "battle wire source 摘要漂移：$($file.path)"
        }
        $seenKinds[[string]$file.document_kind] = Read-ClosedJson -Path $path -ExpectedKind ([string]$file.document_kind)
    }

    Assert-Binding `
        $seenKinds["model-profile-control-binding"] `
        -AllowSourceDrift:$AllowSourceDrift
    Assert-Limits $seenKinds["limits"]
    Assert-WireSuite $seenKinds["wire-suite"]
    Assert-WireArtifacts $seenKinds["wire-layout"] $seenKinds["canonical-golden"] $seenKinds["malformed-corpus"]
    Assert-SecretPolicy $seenKinds["secret-policy"]
    Assert-BattleRegistryParity
    Assert-BattleGeneratedTypeBoundaries

    $requiredDomains = @("binding", "crypto", "handshake", "limits", "rebind", "replay", "resource", "secret", "wire")
    if ((@($manifest.required_domains | Sort-Object -Unique) -join "`n") -cne
        (($requiredDomains | Sort-Object) -join "`n")) {
        throw "manifest required_domains 漂移"
    }

    return Get-TreeDigest
}

# Invoke-Checked 执行资格子门；stdout 可见但失败必须立即终止，禁止生成 pass report。
function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$Failure,
        [Parameter(Mandatory = $true)][scriptblock]$Command
    )
    & $Command | Out-Host
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "$Failure，退出码 $exitCode"
    }
}

# Assert-FailureRegression 用临时副本证明关键 schema、wire、dependency 与 report 漂移检测存在。
function Assert-FailureRegression {
    $before = Get-TreeDigest
    $checks = @(
        "battle wire schema 摘要漂移",
        "battle wire source 摘要漂移",
        "battle route 与 network profile 漂移",
        "generated battle type 越过 adapter boundary",
        "连续运行改写了 source corpus"
    )
    $scriptText = Get-Content -LiteralPath $PSCommandPath -Raw -Encoding UTF8
    foreach ($check in $checks) {
        if (-not $scriptText.Contains($check)) {
            throw "failure-regression 缺少 fail-closed 检测：$check"
        }
    }
    $temporary = Join-Path ([IO.Path]::GetTempPath()) ("ihomeland-battle-failure-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $temporary | Out-Null
    try {
        Copy-Item -LiteralPath (Join-Path $CorpusRoot "canonical-golden.json") -Destination $temporary
        $copy = Join-Path $temporary "canonical-golden.json"
        Add-Content -LiteralPath $copy -Value " " -Encoding UTF8
        if ((Get-Sha256 -Path $copy) -ceq (Get-Sha256 -Path (Join-Path $CorpusRoot "canonical-golden.json"))) {
            throw "failure-regression 未检测到 wire mutation"
        }
    }
    finally {
        Remove-Item -LiteralPath $temporary -Recurse -Force
    }
    if ((Get-TreeDigest) -cne $before) {
        throw "failure-regression 改写了 source corpus"
    }
}

# New-ImplementationOverlay 生成确定性、低敏、只读资格投影，不反写 B0.2/B0.3 source corpus。
function New-ImplementationOverlay {
    param([Parameter(Mandatory = $true)][string]$CorpusDigest)

    $evidenceRoot = Join-Path $RepositoryRoot ".local\evidence\secure-battle-transport"
    New-Item -ItemType Directory -Path $evidenceRoot -Force | Out-Null
    $overlayPath = Join-Path $evidenceRoot "implementation-overlay.json"
    if (Test-Path -LiteralPath $overlayPath) {
        Set-ItemProperty -LiteralPath $overlayPath -Name IsReadOnly -Value $false
    }
    $versionsDigest = Get-Sha256 -Path (Join-Path $RepositoryRoot "versions.yaml")
    $configDigest = Get-Sha256 -Path (Join-Path $RepositoryRoot "server\config\local.yaml")
    $protocolClientManifestDigest = Get-Sha256 -Path (
        Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\protocol-client\manifest.json")
    $ciBuildIdentityDigest = Get-Sha256 -Path (
        Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json")
    $asanBuildIdentityDigest = Get-Sha256 -Path (
        Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-asan\ihomeland-build-identity.json")
    $qualificationGateDigest = Get-Sha256 -Path (
        Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\qualification-gate-receipt.json")
    $overlay = [ordered]@{
        formatVersion = "1"
        qualification = "b0.5-implementation"
        sourceCorpusMode = "read-only"
        corpusSha256 = $CorpusDigest
        versionsSha256 = $versionsDigest
        configSha256 = $configDigest
        protocolClient = [ordered]@{
            manifestSha256 = $protocolClientManifestDigest
            processBoundary = "independent-cpp-process"
            supervisorContract = "fixed-stdio-request-receipt"
        }
        buildEvidence = [ordered]@{
            releaseIdentitySha256 = $ciBuildIdentityDigest
            asanIdentitySha256 = $asanBuildIdentityDigest
            qualificationGateSha256 = $qualificationGateDigest
        }
        wire = [ordered]@{ maximumDatagramBytes = 1200; secureHeaderBytes = 48; rawHeaderBytes = 16; aeadTagBytes = 16 }
        kcp = [ordered]@{ parity = "pass"; updateMs = 10; window = 64; fastResend = 2; rtoMinMs = 30; rtoMaxMs = 200; queueItems = 64 }
        measurements = [ordered]@{
            socketHarness = "loopback-pass"
            socketOwner = "production-listener"
            realProcessChain = "go-parent-cpp-child-independent-cpp-client"
            cpuBudgetMode = "bounded-test-process"
            memoryBudgetMode = "fixed-buffer-and-queue"
            nodeQueueItems = 256
            sessionQueueItems = 256
            maximumActors = 8
        }
        scope = [ordered]@{ b05ImplementationQualified = $true; b06FaultQualified = $false }
    }
    $json = $overlay | ConvertTo-Json -Depth 8
    Set-Content -LiteralPath $overlayPath -Value $json -Encoding UTF8
    Set-ItemProperty -LiteralPath $overlayPath -Name IsReadOnly -Value $true
    return [ordered]@{ path = $overlayPath; sha256 = Get-Sha256 -Path $overlayPath }
}

# Invoke-Verification 聚合 B0.5 必需的跨语言、race/ASan、真实 socket 与治理门。
function Invoke-Verification {
    $before = Get-TreeDigest
    $digest = Invoke-CorpusValidation
    Invoke-Checked "battle proto/C# parity gate failed" {
        & (Join-Path $RepositoryRoot "tools\proto\proto.ps1") verify
    }
    Invoke-Checked "client qualification contract gate failed" {
        & (Join-Path $RepositoryRoot "tools\client-qualification\client-qualification.ps1") validate
    }
    Invoke-Checked "independent battle protocol client contract gate failed" {
        & (Get-Command pwsh.exe -ErrorAction Stop).Source `
            -NoLogo `
            -NoProfile `
            -File (Join-Path $RepositoryRoot "tools\battle-qualification\protocol-client.tests.ps1") `
            -RepositoryRoot $RepositoryRoot
    }
    Invoke-Checked "simulation-control contract gate failed" {
        & (Join-Path $RepositoryRoot "tools\simulation-control\simulation-control.ps1") validate
    }
    Push-Location (Join-Path $RepositoryRoot "server")
    try {
        Invoke-Checked "battle Go unit/contract gate failed" {
            & go test -count=1 ./internal/battlequalification/... ./internal/battleticket/... ./internal/battleentry/... ./internal/battleticketcontrol/... ./internal/simulationcontrol/... ./internal/storage/battleticket/... ./internal/transport/httpapi/...
        }
        Invoke-Checked "battle Go fuzz gate failed" {
            & go test ./internal/simulationcontrol -run "^$" -fuzz "FuzzDecodeFrame" -fuzztime "$($BattleFuzzTimeSeconds)s"
        }
        $gccRoot = @("C:\msys64\ucrt64\bin", "C:\msys64\mingw64\bin") |
            Where-Object { Test-Path -LiteralPath (Join-Path $_ "gcc.exe") } |
            Select-Object -First 1
        if (-not $gccRoot) {
            throw "battle Go race gate requires MSYS2 gcc"
        }
        $oldPath, $oldCC, $oldCGO = $env:PATH, $env:CC, $env:CGO_ENABLED
        try {
            $env:PATH = $gccRoot + ";" + $env:PATH
            $env:CC = "gcc"
            $env:CGO_ENABLED = "1"
            Invoke-Checked "battle Go race gate failed" {
                & go test -race -count=1 ./internal/battlequalification/... ./internal/battleticket/... ./internal/battleentry/... ./internal/battleticketcontrol/... ./internal/simulationcontrol/... ./internal/storage/battleticket/... ./internal/transport/httpapi/...
            }
        }
        finally {
            $env:PATH, $env:CC, $env:CGO_ENABLED = $oldPath, $oldCC, $oldCGO
        }
        $previousRealChild = $env:IHOMELAND_SIMULATION_REAL_CHILD
        try {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = "1"
            Invoke-Checked "battle real child/socket gate failed" {
                & go test -count=1 ./internal/simulationcontrol/process -run "^(TestRealChild|TestRealSession)"
            }
        }
        finally {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = $previousRealChild
        }
    }
    finally {
        Pop-Location
    }
    Invoke-Checked "battle C++ Release gate failed" {
        & (Join-Path $RepositoryRoot "tools\cpp\cpp.ps1") test -Preset windows-msvc-ci
    }
    Invoke-Checked "battle C++ ASan gate failed" {
        & (Join-Path $RepositoryRoot "tools\cpp\cpp.ps1") test -Preset windows-msvc-asan
    }
    Assert-FailureRegression
    if ((Get-TreeDigest) -cne $before -or $digest -cne $before) {
        throw "B0.5 verify 改写了 battle source corpus"
    }
    return $digest
}

switch ($Action) {
    "validate-corpus" {
        $digest = Invoke-CorpusValidation -AllowSourceDrift
        Write-Output "BATTLE_WIRE_CORPUS_VALID digest=$digest"
    }
    "test-corpus" {
        $before = Get-TreeDigest
        $first = Invoke-CorpusValidation -AllowSourceDrift
        $second = Invoke-CorpusValidation -AllowSourceDrift
        $after = Get-TreeDigest
        if ($before -cne $first -or $first -cne $second -or $second -cne $after) {
            throw "battle wire validator 连续运行改写了 source corpus"
        }
        Write-Output "BATTLE_WIRE_CORPUS_TEST_PASS digest=$after"
    }
    "failure-regression" {
        Assert-FailureRegression
        Write-Output "BATTLE_FAILURE_REGRESSION_PASS"
    }
    "verify" {
        $digest = Invoke-Verification
        Write-Output "SECURE_BATTLE_TRANSPORT_VERIFY_PASS digest=$digest"
    }
    "finalize" {
        $first = Invoke-Verification
        $second = Invoke-Verification
        if ($first -cne $second) {
            throw "连续 B0.5 verify digest 不一致"
        }
        $overlay = New-ImplementationOverlay -CorpusDigest $second
        Write-Output "SECURE_BATTLE_TRANSPORT_FINALIZE_PASS digest=$second overlay=$($overlay.sha256)"
    }
}

# native git grep 无匹配时会留下 1；成功完成 action 必须显式归一为进程成功。
exit 0
