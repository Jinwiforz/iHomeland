# 该入口验证 client battle runtime 的冻结进入身份，不启动 Unity 或 production listener。
[CmdletBinding()]
param(
    [ValidateSet("validate", "native-build", "unity-tests", "player-targeted")]
    [string]$Action = "validate",

    # ManifestPath 只供 failure regression 注入临时 manifest；其 source path 仍限制在仓库内。
    [string]$ManifestPath,

    # NativePreset 固定为无 sanitizer 的 Windows x64 Release producer。
    [ValidateSet("windows-msvc-release", "windows-msvc-ci")]
    [string]$NativePreset = "windows-msvc-release",

    # UnityEditorPath 只供显式 Unity action 使用，必须匹配 ProjectVersion。
    [string]$UnityEditorPath = "",

    # PlayerTargetedTimeoutSeconds 覆盖两次build、smoke和真实双Player场景。
    [ValidateRange(900, 2400)]
    [int]$PlayerTargetedTimeoutSeconds = 1800
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$DefaultManifestPath = Join-Path $RepositoryRoot (
    "shared\contracts\fixtures\client-battle-runtime\source-manifest.json")
$ResolvedManifestPath = if ([string]::IsNullOrWhiteSpace($ManifestPath)) {
    $DefaultManifestPath
}
else {
    [System.IO.Path]::GetFullPath($ManifestPath)
}

# Assert-ExactProperties 使 JSON object 保持 closed，防止未消费字段伪装为已治理输入。
function Assert-ExactProperties {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Object,
        [Parameter(Mandatory = $true)]
        [string[]]$Expected,
        [Parameter(Mandatory = $true)]
        [string]$Context
    )

    $actual = @($Object.PSObject.Properties.Name | Sort-Object)
    $wanted = @($Expected | Sort-Object)
    if (($actual -join "`n") -cne ($wanted -join "`n")) {
        throw "$Context properties are not closed"
    }
}

# Assert-CanonicalSha256 拒绝非 lowercase canonical digest。
function Assert-CanonicalSha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Value,
        [Parameter(Mandatory = $true)]
        [string]$Context
    )

    if ($Value -cnotmatch '^[0-9a-f]{64}$') {
        throw "$Context is not a canonical SHA-256"
    }
}

# Resolve-RepositoryFile 拒绝绝对路径与越出仓库的 source binding。
function Resolve-RepositoryFile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RelativePath
    )

    if ([System.IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath.Contains("..") -or
        $RelativePath.Contains("\")) {
        throw "source path is not canonical: $RelativePath"
    }
    $fullPath = [System.IO.Path]::GetFullPath(
        (Join-Path $RepositoryRoot $RelativePath))
    $rootPrefix = $RepositoryRoot.TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar) +
        [System.IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith(
            $rootPrefix,
            [System.StringComparison]::OrdinalIgnoreCase) -or
        -not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        throw "source file is missing or outside repository: $RelativePath"
    }
    return $fullPath
}

# Get-Sha256 返回 source file 的 lowercase byte-exact digest。
function Get-Sha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $stream = [System.IO.File]::OpenRead($Path)
    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString(
                $algorithm.ComputeHash($stream))).
            Replace("-", "").
            ToLowerInvariant()
    }
    finally {
        $algorithm.Dispose()
        $stream.Dispose()
    }
}

# Resolve-LockedUnityEditor 解析显式或环境路径并绑定项目冻结Editor版本。
function Resolve-LockedUnityEditor {
    $resolved = $UnityEditorPath
    if ([string]::IsNullOrWhiteSpace($resolved)) {
        $resolved = [Environment]::GetEnvironmentVariable(
            "IHOMELAND_UNITY_EDITOR")
    }
    if ([string]::IsNullOrWhiteSpace($resolved) -or
        -not [System.IO.Path]::IsPathFullyQualified($resolved) -or
        -not (Test-Path -LiteralPath $resolved -PathType Leaf) -or
        [System.IO.Path]::GetFileName($resolved) -cne "Unity.exe") {
        throw (
            "Unity action requires an absolute UnityEditorPath or " +
            "IHOMELAND_UNITY_EDITOR pointing to Unity.exe")
    }

    $projectVersionPath = Join-Path $RepositoryRoot (
        "client\ProjectSettings\ProjectVersion.txt")
    $projectVersion = Get-Content -LiteralPath $projectVersionPath `
        -Encoding UTF8 |
        Select-Object -First 1
    if ($projectVersion -cne "m_EditorVersion: 6000.5.2f1" -or
        [System.IO.Path]::GetFullPath($resolved) -notmatch
            '[\\/]6000\.5\.2f1[\\/]') {
        throw "Unity Editor is not the frozen 6000.5.2f1"
    }
    return [System.IO.Path]::GetFullPath($resolved)
}

# Invoke-UnityBuild 使用精确Editor PID执行唯一Windows build owner并设置有界deadline。
function Invoke-UnityBuild {
    param(
        [Parameter(Mandatory = $true)][string]$EditorPath,
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$OutputRoot,
        [Parameter(Mandatory = $true)][string]$LogPath,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )

    $arguments = @(
        "-batchmode",
        "-nographics",
        "-quit",
        "-projectPath", (Join-Path $RepositoryRoot "client"),
        "-executeMethod", $Method,
        "-ihomelandBuildOutput", $OutputRoot,
        "-logFile", $LogPath)
    $process = Start-Process `
        -FilePath $EditorPath `
        -ArgumentList $arguments `
        -PassThru `
        -WindowStyle Hidden
    try {
        if (-not $process.WaitForExit($TimeoutMilliseconds)) {
            throw "Unity Player build deadline elapsed: $Method"
        }
        if ($process.ExitCode -ne 0) {
            throw "Unity Player build failed: $Method"
        }
    }
    finally {
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit(30000) | Out-Null
        }
    }
}

# Get-UniquePlayer 返回指定build root下唯一Windows Player。
function Get-UniquePlayer {
    param([Parameter(Mandatory = $true)][string]$BuildRoot)

    $players = @(
        Get-ChildItem `
            -LiteralPath $BuildRoot `
            -Filter "iHomeland.exe" `
            -File `
            -Recurse
    )
    if ($players.Count -ne 1) {
        throw "Windows Player executable is missing or ambiguous: $BuildRoot"
    }
    return $players[0].FullName
}

# Assert-PackagedNativePlugin 验证Player只打包一个与current producer同digest的DLL。
function Assert-PackagedNativePlugin {
    param(
        [Parameter(Mandatory = $true)][string]$BuildRoot,
        [Parameter(Mandatory = $true)][string]$ExpectedDigest
    )

    $plugins = @(
        Get-ChildItem `
            -LiteralPath $BuildRoot `
            -Filter "ihomeland_client_battle_native.dll" `
            -File `
            -Recurse
    )
    if ($plugins.Count -ne 1 -or
        (Get-Sha256 $plugins[0].FullName) -cne $ExpectedDigest) {
        throw "Windows Player native plugin package identity drifted"
    }
}

# Invoke-PlayerBuildSmoke 观察App Scope就绪后只停止当前精确Player PID。
function Invoke-PlayerBuildSmoke {
    param(
        [Parameter(Mandatory = $true)][string]$PlayerPath,
        [Parameter(Mandatory = $true)][string]$LogPath,
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [string[]]$AdditionalArguments
    )

    $arguments = @(
        "-batchmode",
        "-nographics",
        "-logFile", $LogPath) + $AdditionalArguments
    $process = Start-Process `
        -FilePath $PlayerPath `
        -ArgumentList $arguments `
        -PassThru `
        -WindowStyle Hidden
    try {
        $deadline = [DateTime]::UtcNow.AddSeconds(90)
        while ([DateTime]::UtcNow -lt $deadline) {
            if ($process.HasExited) {
                throw "Windows Player exited before App Scope became ready"
            }
            if (Test-Path -LiteralPath $LogPath -PathType Leaf) {
                if (Select-String `
                    -LiteralPath $LogPath `
                    -SimpleMatch "[IHOMELAND_APP] state=running" `
                    -Quiet) {
                    return
                }
            }
            Start-Sleep -Milliseconds 250
        }
        throw "Windows Player smoke deadline elapsed"
    }
    finally {
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            if (-not $process.WaitForExit(30000)) {
                throw "Windows Player exact PID cleanup deadline elapsed"
            }
        }
    }
}

# ConvertTo-BinarySearchTexts 为二进制token扫描同时覆盖UTF-8/ASCII与任意字节对齐的UTF-16LE。
function ConvertTo-BinarySearchTexts {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes
    )

    $texts = @(
        [Text.Encoding]::ASCII.GetString($Bytes),
        [Text.Encoding]::Unicode.GetString($Bytes))
    if ($Bytes.Length -gt 1) {
        $texts += [Text.Encoding]::Unicode.GetString(
            $Bytes,
            1,
            $Bytes.Length - 1)
    }
    return $texts
}

# Assert-ReleaseDiagnosticSurfaceRemoved 验证Release binary不包含Development资格入口。
function Assert-ReleaseDiagnosticSurfaceRemoved {
    param(
        [Parameter(Mandatory = $true)][string]$DevelopmentRoot,
        [Parameter(Mandatory = $true)][string]$ReleaseRoot
    )

    $developmentMarkerFound = $false
    foreach ($binary in @(
        Get-ChildItem -LiteralPath $DevelopmentRoot -Recurse -File |
            Where-Object { $_.Extension -in @(".exe", ".dll") }
    )) {
        $bytes = [IO.File]::ReadAllBytes($binary.FullName)
        $texts = ConvertTo-BinarySearchTexts $bytes
        if (@($texts | Where-Object {
                    $_.IndexOf(
                        "client-battle-runtime",
                        [StringComparison]::Ordinal) -ge 0
                }).Count -ne 0) {
            $developmentMarkerFound = $true
            break
        }
    }
    if (-not $developmentMarkerFound) {
        throw "Development Player qualification surface is missing"
    }

    $forbidden = @(
        "IHOMELAND_QUALIFICATION",
        "-ihomelandQualification",
        "client-battle-runtime",
        "TryCaptureQualificationSnapshot",
        "InjectQualificationTransportDisconnect")
    foreach ($binary in @(
        Get-ChildItem -LiteralPath $ReleaseRoot -Recurse -File |
            Where-Object { $_.Extension -in @(".exe", ".dll") }
    )) {
        $bytes = [IO.File]::ReadAllBytes($binary.FullName)
        $texts = ConvertTo-BinarySearchTexts $bytes
        foreach ($token in $forbidden) {
            if (@($texts | Where-Object {
                        $_.IndexOf(
                            $token,
                            [StringComparison]::Ordinal) -ge 0
                    }).Count -ne 0) {
                throw "Release diagnostic surface scan failed: $token"
            }
        }
    }
}

# Assert-NoTrackedGeneratedArtifacts 拒绝把可重建Unity projection或native plugin提交。
function Assert-NoTrackedGeneratedArtifacts {
    $tracked = @(
        & git -C $RepositoryRoot ls-files -- "client/Assets/App/Generated")
    if ($LASTEXITCODE -ne 0 -or $tracked.Count -ne 0) {
        throw "tracked Unity generated artifact is forbidden"
    }
}

# Get-MessageBlock 从 proto source 截取单个顶层 message，供 field presence contract 检查。
function Get-MessageBlock {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ProtoText,
        [Parameter(Mandatory = $true)]
        [string]$MessageName
    )

    $match = [regex]::Match(
        $ProtoText,
        "(?ms)^message\s+$([regex]::Escape($MessageName))\s*\{(?<body>.*?)^\}")
    if (-not $match.Success) {
        throw "battle protobuf message is missing: $MessageName"
    }
    return $match.Groups["body"].Value
}

# Assert-SourceManifest 验证 baseline shape、source digest 与精确 runtime entry policy。
function Assert-SourceManifest {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Manifest
    )

    Assert-ExactProperties $Manifest @(
        "formatVersion",
        "runtimeVersion",
        "documentKind",
        "owner",
        "baseline",
        "upstreamSources",
        "entryPolicy") "source manifest"
    if ($Manifest.formatVersion -ne 1 -or
        $Manifest.runtimeVersion -cne "client-battle-runtime-v1" -or
        $Manifest.documentKind -cne "source-manifest" -or
        $Manifest.owner -cne "client-battle-runtime") {
        throw "source manifest identity drifted"
    }

    Assert-ExactProperties $Manifest.baseline @(
        "sourceCommit",
        "sourceState",
        "unityVersion",
        "clientQualificationVersion",
        "clientContractSha256") "baseline"
    if ([string]$Manifest.baseline.sourceCommit -cnotmatch '^[0-9a-f]{40}$' -or
        $Manifest.baseline.sourceState -cne "clean" -or
        $Manifest.baseline.unityVersion -cne "6000.5.2f1" -or
        $Manifest.baseline.clientQualificationVersion -cne "client-v1") {
        throw "client baseline identity drifted"
    }
    Assert-CanonicalSha256 (
        [string]$Manifest.baseline.clientContractSha256) "client contract baseline"
    & git -C $RepositoryRoot cat-file -e (
        "$($Manifest.baseline.sourceCommit)^{commit}") 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "baseline source commit does not exist"
    }
    $projectVersion = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "client\ProjectSettings\ProjectVersion.txt") `
        -Raw -Encoding UTF8
    if ($projectVersion -notmatch '(?m)^m_EditorVersion:\s*6000\.5\.2f1\s*$') {
        throw "Unity Editor version drifted from runtime baseline"
    }

    $expectedSources = [ordered]@{
        "battle-model" = "battle-model-v1"
        "battle-network-profile" = "battle-network-profile-v2"
        "battle-wire" = "battle-wire-v1"
        "battle-route-registry" = "routes-3000-3007"
        "battle-protobuf" = "generated-parity-source"
        "battle-network-qualification" = "development-readiness-corpus"
        "battle-network-development-readiness" =
            "representative-development-readiness-passed"
    }
    if (@($Manifest.upstreamSources).Count -ne $expectedSources.Count) {
        throw "upstream source registry is not closed"
    }
    $sourcePaths = [System.Collections.Generic.HashSet[string]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($source in @($Manifest.upstreamSources)) {
        Assert-ExactProperties $source @(
            "id",
            "path",
            "sha256",
            "requiredConclusion") "upstream source"
        $id = [string]$source.id
        if (-not $expectedSources.Contains($id) -or
            [string]$source.requiredConclusion -cne $expectedSources[$id]) {
            throw "upstream source identity drifted: $id"
        }
        if (-not $sourcePaths.Add([string]$source.path)) {
            throw "upstream source path is duplicated"
        }
        Assert-CanonicalSha256 ([string]$source.sha256) "upstream source $id"
        $sourcePath = Resolve-RepositoryFile ([string]$source.path)
        if ((Get-Sha256 $sourcePath) -cne [string]$source.sha256) {
            throw "upstream source digest drifted: $id"
        }
    }
}

# Assert-ProfilePolicy 验证 managed runtime 只能消费 current profile 的精确 cadence 与容量。
function Assert-ProfilePolicy {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Policy
    )

    $profilePath = Join-Path $RepositoryRoot (
        "shared\contracts\fixtures\battle\network-profile\profile.json")
    $profile = Get-Content -LiteralPath $profilePath -Raw -Encoding UTF8 |
        ConvertFrom-Json
    if ($profile.profile_version -cne [string]$Policy.profileVersion -or
        $Policy.profileVersion -cne "battle-network-profile-v2") {
        throw "old or mismatched battle network profile"
    }
    $parameters = @{}
    foreach ($parameter in @($profile.parameters)) {
        $parameters[[string]$parameter.id] = $parameter.value
    }
    $expected = [ordered]@{
        "simulation-step-ns" =
            ([int64]$Policy.simulationTickMilliseconds * 1000000)
        "input-step-ns" =
            ([int64]$Policy.inputCadenceMilliseconds * 1000000)
        "input-early-window-ticks" =
            [int]$Policy.inputLeadSimulationTicks
        "snapshot-interval-ticks" =
            [int](1000 / (
                [int]$Policy.snapshotCadenceHertz *
                [int]$Policy.simulationTickMilliseconds))
        "queue-capacity-items" = [int]$Policy.sessionQueueItems
    }
    foreach ($entry in $expected.GetEnumerator()) {
        if (-not $parameters.ContainsKey($entry.Key) -or
            [int64]$parameters[$entry.Key] -ne [int64]$entry.Value) {
            throw "battle profile parameter drifted: $($entry.Key)"
        }
    }
    if ([int]$profile.mtu_budget.max_datagram_bytes -ne [int]$Policy.mtuBytes -or
        [int]$profile.kcp_profile.queue_limit_messages -ne
            [int]$Policy.kcpQueueItems -or
        [int]$profile.kcp_profile.maximum_route_expiry_us -ne
            ([int]$Policy.resyncExpiryMilliseconds * 1000)) {
        throw "battle profile MTU, KCP queue, or expiry drifted"
    }
}

# Assert-RoutePolicy 校验 3000-3007 唯一 lane、方向、payload 与 sender expiry。
function Assert-RoutePolicy {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Policy
    )

    $routes = (
        Get-Content -LiteralPath (
            Join-Path $RepositoryRoot "shared\contracts\registry\routes.json") `
            -Raw -Encoding UTF8 |
        ConvertFrom-Json).routes
    $actualBattleRoutes = @($routes | Where-Object {
            [int]$_.messageId -ge 3000 -and [int]$_.messageId -le 3007
        })
    if ($actualBattleRoutes.Count -ne 8 -or @($Policy.routes).Count -ne 8) {
        throw "battle route registry is not closed"
    }
    foreach ($expected in @($Policy.routes)) {
        Assert-ExactProperties $expected @(
            "messageId",
            "lane",
            "direction",
            "maxPayloadSize",
            "expiryMs") "entry route"
        $matches = @($actualBattleRoutes | Where-Object {
                [int]$_.messageId -eq [int]$expected.messageId
            })
        if ($matches.Count -ne 1) {
            throw "battle route is missing or duplicated: $($expected.messageId)"
        }
        $actual = $matches[0]
        $direction = if ([int]$actual.messageId -in @(3000, 3001, 3006)) {
            "CLIENT_TO_SERVER"
        }
        else {
            "SERVER_TO_CLIENT"
        }
        if ([string]$actual.channel -cne "UDP" -or
            [string]$actual.lane -cne [string]$expected.lane -or
            $direction -cne [string]$expected.direction -or
            [int]$actual.maxSize -ne [int]$Policy.mtuBytes -or
            [int]$actual.maxPayloadSize -ne [int]$expected.maxPayloadSize -or
            [int]$actual.expiryMs -ne [int]$expected.expiryMs) {
            throw "battle route policy drifted: $($expected.messageId)"
        }
    }
    foreach ($resyncId in @(3006, 3007)) {
        $route = @($actualBattleRoutes | Where-Object {
                [int]$_.messageId -eq $resyncId
            })[0]
        if ([int]$route.expiryMs -ne 2250 -or
            [int]$Policy.resyncExpiryMilliseconds -ne 2250) {
            throw "resync route $resyncId uses stale 500 ms expiry"
        }
    }
}

# Assert-SnapshotAcknowledgement 验证 full/delta 都声明 field 7 且 golden 含显式零值。
function Assert-SnapshotAcknowledgement {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Policy
    )

    Assert-ExactProperties $Policy @(
        "fieldName",
        "fieldNumber",
        "requiredMessages",
        "presencePolicy") "snapshot acknowledgement"
    if ($Policy.fieldName -cne "last_processed_input_tick" -or
        [int]$Policy.fieldNumber -ne 7 -or
        $Policy.presencePolicy -cne "wire-tag-required-including-zero") {
        throw "snapshot acknowledgement policy drifted"
    }
    $requiredMessages = @($Policy.requiredMessages)
    if ($requiredMessages.Count -ne 2 -or
        @($requiredMessages | Sort-Object) -join "`n" -cne
            (@("BattleDeltaSnapshot", "BattleFullSnapshot") -join "`n")) {
        throw "snapshot acknowledgement messages are not closed"
    }
    $protoText = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "shared\proto\ihomeland\battle\v1\battle.proto") `
        -Raw -Encoding UTF8
    foreach ($messageName in @($Policy.requiredMessages)) {
        $block = Get-MessageBlock $protoText ([string]$messageName)
        if ($block -notmatch (
                '(?m)^\s*uint64\s+last_processed_input_tick\s*=\s*7\s*;\s*$')) {
            throw "snapshot acknowledgement field 7 is missing: $messageName"
        }
    }
    $golden = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "shared\contracts\fixtures\battle\wire\canonical-golden.json")) `
        -Raw -Encoding UTF8
    foreach ($vector in @(
            "battle-full-snapshot-ack-zero-v1",
            "battle-delta-snapshot-ack-zero-v1")) {
        if ($golden -notmatch [regex]::Escape($vector)) {
            throw "snapshot acknowledgement golden is missing: $vector"
        }
    }
}

# Assert-SnapshotEntityStateParity 锁定 wire corpus、C++ producer/client 与 C# consumer 的同一 registry。
function Assert-SnapshotEntityStateParity {
    $layout = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "shared\contracts\fixtures\battle\wire\wire-layout.json")) `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $state = $layout.snapshot_entity_state
    Assert-ExactProperties $state @(
        "message_ids",
        "transform_message",
        "required_scalar_fields",
        "scalar_presence",
        "delta_state_mask",
        "state_flags") "snapshot entity state"
    $fieldNames = @($state.required_scalar_fields | ForEach-Object {
            [string]$_.name
        })
    $expectedFields = @(
        "position_x_mm",
        "position_y_mm",
        "position_z_mm",
        "yaw_millidegrees",
        "velocity_x_mm_per_second",
        "velocity_y_mm_per_second",
        "velocity_z_mm_per_second")
    if (($fieldNames -join "`n") -cne ($expectedFields -join "`n") -or
        [int]$state.delta_state_mask.transform -ne 1 -or
        [int]$state.delta_state_mask.health_milli -ne 2 -or
        [int]$state.delta_state_mask.state_flags -ne 4 -or
        [int]$state.delta_state_mask.known_mask -ne 7 -or
        [uint32]$state.state_flags.phase_mask -ne 0x0000000f -or
        [uint32]$state.state_flags.grounded_flag -ne 0x00000010 -or
        [uint64]$state.state_flags.dead_flag -ne 2147483648 -or
        [uint64]$state.state_flags.known_mask -ne 2147483679) {
        throw "snapshot entity state registry drifted"
    }

    $protoText = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "shared\proto\ihomeland\battle\v1\battle.proto")) `
        -Raw -Encoding UTF8
    $transformBlock = Get-MessageBlock $protoText "QuantizedTransform"
    for ($index = 0; $index -lt $expectedFields.Count; $index++) {
        $fieldNumber = $index + 1
        $pattern =
            "(?m)^\s*sint32\s+$($expectedFields[$index])\s*=\s*$fieldNumber\s*;\s*$"
        if ($transformBlock -notmatch $pattern) {
            throw "snapshot transform field drifted: $($expectedFields[$index])"
        }
    }

    $projectionHeader = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "simulation\include\ihomeland\sim\gameplay\projection.hpp")) `
        -Raw -Encoding UTF8
    foreach ($pattern in @(
            'PhaseMask\s*=\s*0x0000000fU\s*;',
            'Grounded\s*=\s*0x00000010U\s*;',
            'Dead\s*=\s*0x80000000U\s*;',
            'KnownMask\s*=\s*[\r\n\s]*PhaseMask\s*\|\s*Grounded\s*\|\s*Dead\s*;')) {
        if ($projectionHeader -notmatch $pattern) {
            throw "C++ snapshot state flag registry drifted"
        }
    }

    $producer = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "simulation\src\transport\battle_session.cpp")) `
        -Raw -Encoding UTF8
    foreach ($token in @(
            "set_position_x_mm",
            "set_position_y_mm",
            "set_position_z_mm",
            "set_yaw_millidegrees",
            "set_velocity_x_mm_per_second",
            "set_velocity_y_mm_per_second",
            "set_velocity_z_mm_per_second",
            "Grounded",
            "PhaseMask")) {
        if ($producer -notmatch [regex]::Escape($token)) {
            throw "C++ snapshot producer parity is missing: $token"
        }
    }

    $protocolClient = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "simulation\src\qualification\battle_protocol_raw.cpp")) `
        -Raw -Encoding UTF8
    foreach ($token in @(
            "has_position_x_mm",
            "has_position_y_mm",
            "has_position_z_mm",
            "has_yaw_millidegrees",
            "has_velocity_x_mm_per_second",
            "has_velocity_y_mm_per_second",
            "has_velocity_z_mm_per_second",
            "KnownMask")) {
        if ($protocolClient -notmatch [regex]::Escape($token)) {
            throw "C++ protocol client snapshot parity is missing: $token"
        }
    }

    $clientModel = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "client\Assets\App\Scripts\Application\Battle\" +
            "ClientBattleGameplayModels.cs")) -Raw -Encoding UTF8
    foreach ($pattern in @(
            'PhaseStateMask\s*=\s*0x0000000f\s*;',
            'GroundedStateFlag\s*=\s*0x00000010\s*;',
            'DeadStateFlag\s*=\s*0x80000000\s*;',
            'KnownStateFlags\s*=\s*[\r\n\s]*PhaseStateMask\s*\|\s*GroundedStateFlag\s*\|\s*DeadStateFlag\s*;',
            'Grounded\s*=>\s*[\r\n\s]*\(StateFlags\s*&\s*GroundedStateFlag\)\s*!=\s*0\s*;')) {
        if ($clientModel -notmatch $pattern) {
            throw "C# snapshot state flag registry drifted"
        }
    }

    $clientAdapter = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "client\Assets\App\Scripts\Infrastructure\Battle\" +
            "ClientBattleProtocolAdapter.cs")) -Raw -Encoding UTF8
    foreach ($token in @(
            "HasPositionXMm",
            "HasPositionYMm",
            "HasPositionZMm",
            "HasYawMillidegrees",
            "HasVelocityXMmPerSecond",
            "HasVelocityYMmPerSecond",
            "HasVelocityZMmPerSecond",
            "ClientBattleEntityState.KnownStateFlags")) {
        if ($clientAdapter -notmatch [regex]::Escape($token)) {
            throw "C# snapshot consumer parity is missing: $token"
        }
    }

    $runtimeCoordinator = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "client\Assets\App\Scripts\Application\Battle\" +
            "ClientBattleRuntimeCoordinator.cs")) -Raw -Encoding UTF8
    if ($runtimeCoordinator -notmatch
        'var\s+grounded\s*=\s*local\.Grounded\s*;' -or
        $runtimeCoordinator -match
        'var\s+grounded\s*=\s*false\s*;') {
        throw "C# runtime does not consume authority grounded directly"
    }
}

# Assert-DevelopmentReadiness 保持代表性开发门与最终资格显式授权边界分离。
function Assert-DevelopmentReadiness {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Readiness
    )

    Assert-ExactProperties $Readiness @(
        "qualityCheckId",
        "archivedTask",
        "finalQualificationRequiredForDevelopment") "development readiness"
    if ($Readiness.qualityCheckId -cne "battle-qualification-representative" -or
        $Readiness.archivedTask -cne "6.6" -or
        [bool]$Readiness.finalQualificationRequiredForDevelopment) {
        throw "development readiness identity drifted"
    }
    $catalog = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "tools\quality\catalog.json") -Raw -Encoding UTF8 |
        ConvertFrom-Json
    if (@($catalog.checks | Where-Object {
                $_.id -ceq $Readiness.qualityCheckId -and
                $_.class -ceq "targeted-expensive"
            }).Count -ne 1) {
        throw "representative development readiness check is unavailable"
    }
    $archivedTasks = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "openspec\changes\archive\2026-07-27-qualify-battle-network\tasks.md")) `
        -Raw -Encoding UTF8
    if ($archivedTasks -notmatch (
            '(?m)^- \[x\] 6\.6 .*代表性 evidence.*均可裁决')) {
        throw "representative development readiness evidence is incomplete"
    }
}

# Assert-DependencyPolicy 验证 Cinemachine 与 client native consumer 只使用登记的精确依赖。
function Assert-DependencyPolicy {
    $versionsPath = Join-Path $RepositoryRoot "versions.yaml"
    $versionsText = Get-Content -LiteralPath $versionsPath -Raw -Encoding UTF8
    foreach ($required in @(
            'cinemachine:',
            'version: "3.1.7"',
            'package_id: "com.unity.cinemachine"',
            'source: "https://packages.unity.com"',
            'selection: "2026-07-29 latest stable；preview 版本禁止进入 production manifest。"',
            'owner: "client-cinemachine-camera-host"',
            'native_battle_adapter:',
            'abi_version: "ihomeland-client-battle-native-v1"',
            'c_abi_owner: "client-battle-native-interop"',
            'platform: "windows_x86_64"',
            'fallback_policy: "forbidden"',
            'library: "libsodium_cpp"',
            'consumer: "client-battle-native-crypto"',
            'library: "kcp_cpp"',
            'consumer: "client-battle-native-kcp"')) {
        if ($versionsText -notmatch [regex]::Escape($required)) {
            throw "client dependency governance is missing: $required"
        }
    }
    $packageManifest = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "client\Packages\manifest.json") `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    if ($packageManifest.dependencies."com.unity.cinemachine" -cne "3.1.7") {
        throw "Unity manifest does not lock Cinemachine 3.1.7"
    }
    if ($versionsText -match '(?mi)^\s*(system|nuget)_fallback\s*:\s*(true|allowed)') {
        throw "client native dependency fallback is enabled"
    }

    $nativeSource = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot (
            "simulation\client_native\src\" +
            "ihomeland_client_battle_native.cpp")) -Raw -Encoding UTF8
    $nativeProfile = [ordered]@{
        "KcpHeaderBytes" = 24
        "KcpMessageBytes" = 1000
        "KcpOutputQueueItems" = 64
        "KcpWindowSegments" = 64
        "KcpUpdateMilliseconds" = 10
        "KcpFastResend" = 2
        "KcpMinimumRtoMilliseconds" = 30
        "KcpMaximumRtoMilliseconds" = 200
        "KcpDeadLinkRetransmits" = 10
    }
    foreach ($entry in $nativeProfile.GetEnumerator()) {
        $pattern = (
            '\b' +
            [regex]::Escape([string]$entry.Key) +
            '\s*=\s*' +
            [regex]::Escape([string]$entry.Value) +
            '\s*;')
        if ($nativeSource -notmatch $pattern) {
            throw "client native KCP profile drifted: $($entry.Key)"
        }
    }
}

# Assert-BaselineCharacterization 锁定 B0.7 开始前的资产、测试与 Player build 表面。
function Assert-BaselineCharacterization {
    $path = Join-Path $RepositoryRoot (
        "shared\contracts\fixtures\client-battle-runtime\" +
        "baseline-characterization.json")
    $characterization = Get-Content -LiteralPath $path -Raw -Encoding UTF8 |
        ConvertFrom-Json
    Assert-ExactProperties $characterization @(
        "formatVersion",
        "documentKind",
        "sourceCommit",
        "assets",
        "testSurface",
        "buildSurface") "baseline characterization"
    if ([int]$characterization.formatVersion -ne 1 -or
        [string]$characterization.documentKind -cne
            "client-battle-runtime-baseline-characterization" -or
        [string]$characterization.sourceCommit -cne
            "f42d692562aea31fda2e586248cb64e03f1be857") {
        throw "client battle runtime baseline characterization drifted"
    }

    $assetPaths = [System.Collections.Generic.HashSet[string]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($asset in @($characterization.assets)) {
        Assert-ExactProperties $asset @(
            "kind",
            "path",
            "gitBlob") "baseline characterized asset"
        if ([string]$asset.kind -cnotmatch (
                '^(scene|product-prefab|product-uxml|product-uss|' +
                'asmdef|owner-registry)$') -or
            [string]$asset.gitBlob -cnotmatch '^[0-9a-f]{40}$' -or
            -not $assetPaths.Add([string]$asset.path)) {
            throw "baseline characterized asset is invalid"
        }

        Resolve-RepositoryFile ([string]$asset.path) | Out-Null
        $actualBlob = & git -C $RepositoryRoot rev-parse (
            "$($characterization.sourceCommit):$($asset.path)") 2>$null
        if ($LASTEXITCODE -ne 0 -or
            [string]$actualBlob -cne [string]$asset.gitBlob) {
            throw "baseline characterized asset identity drifted: $($asset.path)"
        }
    }
    if ($assetPaths.Count -ne 15) {
        throw "baseline characterized asset registry is not closed"
    }

    Assert-ExactProperties $characterization.testSurface @(
        "editModeSourceFiles",
        "playModeSourceFiles") "baseline test surface"
    $editSources = @(& git -C $RepositoryRoot ls-tree -r --name-only (
            [string]$characterization.sourceCommit) -- (
            "client/Assets/App/Tests/EditMode") |
        Where-Object { $_ -cmatch '\.cs$' }).Count
    $playSources = @(& git -C $RepositoryRoot ls-tree -r --name-only (
            [string]$characterization.sourceCommit) -- (
            "client/Assets/App/Tests/PlayMode") |
        Where-Object { $_ -cmatch '\.cs$' }).Count
    if ($LASTEXITCODE -ne 0 -or
        $editSources -ne [int]$characterization.testSurface.editModeSourceFiles -or
        $playSources -ne [int]$characterization.testSurface.playModeSourceFiles -or
        $editSources -ne 31 -or
        $playSources -ne 4) {
        throw "baseline EditMode/PlayMode characterization drifted"
    }

    Assert-ExactProperties $characterization.buildSurface @(
        "owner",
        "development",
        "release",
        "releaseDiagnosticsForbidden") "baseline build surface"
    if ([string]$characterization.buildSurface.owner -cne
            "ClientDevelopmentBuild" -or
        -not [bool]$characterization.buildSurface.development -or
        -not [bool]$characterization.buildSurface.release -or
        -not [bool]$characterization.buildSurface.releaseDiagnosticsForbidden) {
        throw "baseline Development/Release build characterization drifted"
    }

    $qualificationDocument = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "docs\client-v1-qualification.md") `
        -Raw -Encoding UTF8
    if ($qualificationDocument -notmatch (
            '`ClientDevelopmentBuild` 是当前唯一 Windows build owner') -or
        $qualificationDocument -notmatch (
            'Development 与 Release 均进入同一个实现') -or
        $qualificationDocument -notmatch (
            'Release 编译时不包含')) {
        throw "baseline Windows build owner documentation drifted"
    }

    foreach ($asset in @($characterization.assets | Where-Object {
                [string]$_.kind -cmatch '^product-'
            })) {
        $currentBlob = & git -C $RepositoryRoot hash-object -- (
            [string]$asset.path)
        if ($LASTEXITCODE -ne 0 -or
            [string]$currentBlob -cne [string]$asset.gitBlob) {
            throw "B0.7 changed an existing product UI asset: $($asset.path)"
        }
    }
}

# Assert-ClientSourcePolicy 验证新增owners、asmdef DAG与secret边界没有绕开既有架构。
function Assert-ClientSourcePolicy {
    $ownerRegistryPath = Join-Path $RepositoryRoot (
        "client\Architecture\owner-registry.json")
    $ownerRegistry = Get-Content -LiteralPath $ownerRegistryPath `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $expectedOwners = [ordered]@{
        "battle-connection" =
            "client/Assets/App/Scripts/Infrastructure/Battle/BattleNetworkClient.cs"
        "battle-runtime" =
            "client/Assets/App/Scripts/Application/Battle/ClientBattleRuntimeCoordinator.cs"
        "gameplay-replica" =
            "client/Assets/App/Scripts/Application/Battle/GameplayReplica.cs"
        "gameplay-prediction" =
            "client/Assets/App/Scripts/Application/Battle/GameplayPrediction.cs"
        "gameplay-interpolation" =
            "client/Assets/App/Scripts/Application/Battle/GameplayInterpolation.cs"
    }
    foreach ($entry in $expectedOwners.GetEnumerator()) {
        $matches = @($ownerRegistry.owners | Where-Object {
                [string]$_.id -ceq $entry.Key
            })
        if ($matches.Count -ne 1 -or
            [string]$matches[0].source -cne $entry.Value) {
            throw "client battle owner registry drifted: $($entry.Key)"
        }
        Resolve-RepositoryFile ([string]$matches[0].source) | Out-Null
    }

    $applicationAsmdefPath = Join-Path $RepositoryRoot (
        "client\Assets\App\Scripts\Application\" +
        "IHomeland.Client.Application.asmdef")
    $applicationAsmdef = Get-Content -LiteralPath $applicationAsmdefPath `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    if ([string]$applicationAsmdef.name -cne "IHomeland.Client.Application" -or
        -not [bool]$applicationAsmdef.noEngineReferences -or
        @($applicationAsmdef.references).Count -ne 1 -or
        [string]$applicationAsmdef.references[0] -cne
            "IHomeland.Client.Foundation") {
        throw "Application asmdef DAG no longer excludes Unity/Infrastructure"
    }

    $infrastructureAsmdefPath = Join-Path $RepositoryRoot (
        "client\Assets\App\Scripts\Infrastructure\" +
        "IHomeland.Client.Infrastructure.asmdef")
    $infrastructureAsmdef = Get-Content -LiteralPath $infrastructureAsmdefPath `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $expectedInfrastructureReferences = @(
        "IHomeland.Client.Application",
        "IHomeland.Client.Foundation",
        "IHomeland.Client.Protocol.Generated")
    $actualInfrastructureReferences = @(
        $infrastructureAsmdef.references | Sort-Object)
    if (($actualInfrastructureReferences -join "`n") -cne
        (($expectedInfrastructureReferences | Sort-Object) -join "`n")) {
        throw "Infrastructure asmdef DAG drifted"
    }

    $applicationBattlePath = Join-Path $RepositoryRoot (
        "client\Assets\App\Scripts\Application\Battle")
    foreach ($source in Get-ChildItem -LiteralPath $applicationBattlePath `
                 -Filter *.cs -File) {
        $text = Get-Content -LiteralPath $source.FullName -Raw -Encoding UTF8
        if ($text -match '(?m)^\s*using\s+(UnityEngine|IHomeland\.Client\.Infrastructure|IHomeland\.Protocol)' -or
            $text -match '\b(UnityEngine|Google\.Protobuf)\b') {
            throw "Application battle source crossed its compile-time boundary: $($source.Name)"
        }
    }

    $sourceRoots = @(
        (Join-Path $RepositoryRoot "client\Assets\App\Scripts\Application\Battle"),
        (Join-Path $RepositoryRoot "client\Assets\App\Scripts\Infrastructure\Battle"))
    foreach ($root in $sourceRoots) {
        foreach ($source in Get-ChildItem -LiteralPath $root -Filter *.cs -File) {
            $text = Get-Content -LiteralPath $source.FullName -Raw -Encoding UTF8
            if ($text -match (
                    '(?im)^\s*(UnityEngine\.)?Debug\.(Log|LogWarning|LogError).*' +
                    '(ticket|secret|cookie|nonce|proof|key|binding|endpoint|payload)') -or
                $text -match (
                    '(?im)^\s*(Console|Trace)\..*' +
                    '(ticket|secret|cookie|nonce|proof|key|binding|endpoint|payload)')) {
                throw "client battle source can log credential-bearing material: $($source.Name)"
            }
        }
    }
}

# Assert-UnityMaterializedPolicy 验证只可由 current Editor 生成或保存的 package、meta与Scene接线。
function Assert-UnityMaterializedPolicy {
    $packageLockPath = Join-Path $RepositoryRoot (
        "client\Packages\packages-lock.json")
    if (-not (Test-Path -LiteralPath $packageLockPath -PathType Leaf)) {
        throw "Unity packages-lock does not contain exact Cinemachine 3.1.7"
    }
    $packageLock = Get-Content -LiteralPath $packageLockPath `
        -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $cinemachineProperty = if ($null -eq $packageLock.dependencies) {
        $null
    }
    else {
        $packageLock.dependencies.PSObject.Properties[
            "com.unity.cinemachine"]
    }
    $cinemachine = if ($null -eq $cinemachineProperty) {
        $null
    }
    else {
        $cinemachineProperty.Value
    }
    if ($null -eq $cinemachine -or
        [string]$cinemachine.version -cne "3.1.7" -or
        [int]$cinemachine.depth -ne 0 -or
        [string]$cinemachine.source -cne "registry" -or
        [string]$cinemachine.url -cne "https://packages.unity.com") {
        throw "Unity packages-lock does not contain exact Cinemachine 3.1.7"
    }

    $inputPath = Join-Path $RepositoryRoot (
        "client\Assets\InputSystem_Actions.inputactions")
    $inputAsset = Get-Content -LiteralPath $inputPath -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $playerMaps = @($inputAsset.maps | Where-Object {
            [string]$_.name -ceq "Player"
        })
    if ($playerMaps.Count -ne 1) {
        throw "Unity Input Actions must contain one exact Player map"
    }
    $requiredActions = [ordered]@{
        "Move" = @("Value", "Vector2")
        "Aim" = @("Value", "Vector2")
        "Jump" = @("Button", "Button")
        "Primary" = @("Button", "Button")
        "Secondary" = @("Button", "Button")
        "Interact" = @("Button", "Button")
    }
    foreach ($entry in $requiredActions.GetEnumerator()) {
        $matches = @($playerMaps[0].actions | Where-Object {
                [string]$_.name -ceq $entry.Key
            })
        $actualExpectedControlType = if ($matches.Count -eq 1) {
            [string]$matches[0].expectedControlType
        }
        else {
            ""
        }
        $expectedControlMatches =
            $actualExpectedControlType -ceq $entry.Value[1] -or
            ($entry.Value[0] -ceq "Button" -and
             [string]::IsNullOrEmpty($actualExpectedControlType))
        if ($matches.Count -ne 1 -or
            [string]$matches[0].type -cne $entry.Value[0] -or
            -not $expectedControlMatches) {
            throw (
                "Unity battle input action drifted: " +
                "$($entry.Key)/$($entry.Value[0])/$($entry.Value[1])")
        }
    }

    $unityOwnedSources = @(
        "client\Assets\App\Scripts\Application\Battle",
        "client\Assets\App\Scripts\Infrastructure\Battle",
        "client\Assets\App\Scripts\Application\World\ClientBattleWorldTargetSource.cs",
        "client\Assets\App\Scripts\Core\Composition\BattleComposition.cs",
        "client\Assets\App\Scripts\Presentation\Hosts\ClientBattleInputContracts.cs",
        "client\Assets\App\Scripts\Scenes\PersonalWorld\ClientActorViewRegistry.cs",
        "client\Assets\App\Scripts\Scenes\PersonalWorld\ClientBattleHudHost.cs",
        "client\Assets\App\Scripts\Scenes\PersonalWorld\ClientBattleSceneHost.cs",
        "client\Assets\App\Scripts\Scenes\PersonalWorld\CinemachineCameraHost.cs",
        "client\Assets\App\Tests\EditMode\ClientBattleInputContractTests.cs",
        "client\Assets\App\Tests\EditMode\ClientBattleRuntimeTests.cs"
    )
    $sourcePaths = [System.Collections.Generic.List[string]]::new()
    foreach ($relative in $unityOwnedSources) {
        $path = Join-Path $RepositoryRoot $relative
        if (Test-Path -LiteralPath $path -PathType Container) {
            foreach ($source in Get-ChildItem -LiteralPath $path -Filter *.cs -File) {
                $sourcePaths.Add($source.FullName)
            }
        }
        elseif (Test-Path -LiteralPath $path -PathType Leaf) {
            $sourcePaths.Add($path)
        }
        else {
            throw "Unity battle source is missing: $relative"
        }
    }
    foreach ($sourcePath in $sourcePaths) {
        if (-not (Test-Path -LiteralPath "$sourcePath.meta" -PathType Leaf)) {
            throw (
                "Unity Editor has not materialized source meta: " +
                [System.IO.Path]::GetRelativePath($RepositoryRoot, $sourcePath))
        }
    }

    $scenePath = Join-Path $RepositoryRoot (
        "client\Assets\App\Scenes\PersonalWorldScene.unity")
    $sceneText = Get-Content -LiteralPath $scenePath -Raw -Encoding UTF8
    foreach ($hostName in @(
            "ClientBattleSceneHost",
            "ClientActorViewRegistry",
            "ClientBattleHudHost",
            "CinemachineCameraHost")) {
        $sourcePath = $sourcePaths | Where-Object {
            [System.IO.Path]::GetFileNameWithoutExtension($_) -ceq $hostName
        } | Select-Object -First 1
        $metaText = Get-Content -LiteralPath "$sourcePath.meta" -Raw -Encoding UTF8
        $guid = [regex]::Match(
            $metaText,
            '(?m)^guid:\s*(?<value>[0-9a-f]{32})\s*$')
        $hostCount = if ($guid.Success) {
            [regex]::Matches(
                $sceneText,
                [regex]::Escape($guid.Groups["value"].Value)).Count
        }
        else {
            0
        }
        if ($hostCount -ne 1) {
            throw "PersonalWorldScene host must be unique: $hostName"
        }
    }
    foreach ($field in @(
            "_battleHost",
            "_actors",
            "_hud",
            "_camera",
            "_actorPrefab",
            "_actorRoot",
            "_root",
            "_statusText",
            "_healthText",
            "_explorationRig",
            "_meleeRig",
            "_rangedAimRig",
            "_cinematicRig",
            "_followProxy",
            "_impulseSource")) {
        if ($sceneText -notmatch (
                '(?m)^\s*' + [regex]::Escape($field) +
                ':\s*\{fileID:\s*(?!0(?:[,}]))\d+')) {
            throw "PersonalWorldScene reference is missing: $field"
        }
    }

    $bootstrapPath = Join-Path $RepositoryRoot (
        "client\Assets\App\Scenes\BootstrapScene.unity")
    $bootstrapText = Get-Content -LiteralPath $bootstrapPath -Raw -Encoding UTF8
    $actorPrefabPath = Join-Path $RepositoryRoot (
        "client\Assets\App\Prefabs\Battle\GenericActor.prefab")
    $actorPrefabText = Get-Content -LiteralPath $actorPrefabPath -Raw -Encoding UTF8
    foreach ($asset in @(
            [pscustomobject]@{
                Name = "BootstrapScene"
                Text = $bootstrapText
            },
            [pscustomobject]@{
                Name = "PersonalWorldScene"
                Text = $sceneText
            },
            [pscustomobject]@{
                Name = "GenericActor"
                Text = $actorPrefabText
            })) {
        if ($asset.Text -match (
                '(?m)^\s*m_Script:\s*\{fileID:\s*0(?:[,}])')) {
            throw "$($asset.Name) contains a missing script"
        }
        if ($asset.Text -match (
                '(?im)^\s*m_EditorClassIdentifier:.*Generated')) {
            throw "$($asset.Name) serializes a generated script"
        }
    }

    if ([regex]::Matches($sceneText, '(?m)^--- !u!20\s').Count -ne 1 -or
        [regex]::Matches($sceneText, '(?m)^--- !u!81\s').Count -ne 1) {
        throw "PersonalWorldScene must own exactly one Camera and AudioListener"
    }
    $eventSystemPattern =
        '(?m)^\s*m_EditorClassIdentifier:\s*' +
        'UnityEngine\.UI::UnityEngine\.EventSystems\.EventSystem\s*$'
    if ([regex]::Matches($bootstrapText, $eventSystemPattern).Count -ne 1 -or
        [regex]::Matches($sceneText, $eventSystemPattern).Count -ne 0) {
        throw "BootstrapScene must remain the unique EventSystem owner"
    }

    foreach ($bootstrapOwner in @(
            [pscustomobject]@{
                Source = "client\Assets\App\Scripts\Core\Bootstrap\AppBootstrap.cs"
                Name = "AppBootstrap"
            },
            [pscustomobject]@{
                Source = "client\Assets\App\Scripts\Core\Bootstrap\AppRoot.cs"
                Name = "AppRoot"
            },
            [pscustomobject]@{
                Source = "client\Assets\App\Scripts\Presentation\Hosts\ClientUiHostRoot.cs"
                Name = "ClientUiHostRoot"
            })) {
        $ownerMeta = Get-Content -LiteralPath (
            (Join-Path $RepositoryRoot $bootstrapOwner.Source) + ".meta") `
            -Raw -Encoding UTF8
        $ownerGuid = [regex]::Match(
            $ownerMeta,
            '(?m)^guid:\s*(?<value>[0-9a-f]{32})\s*$')
        $ownerCount = if ($ownerGuid.Success) {
            [regex]::Matches(
                $bootstrapText,
                [regex]::Escape($ownerGuid.Groups["value"].Value)).Count
        }
        else {
            0
        }
        if ($ownerCount -ne 1) {
            throw "BootstrapScene owner must be unique: $($bootstrapOwner.Name)"
        }
    }

    if ([regex]::Matches(
            $actorPrefabText,
            '(?m)^--- !u!23\s').Count -ne 1 -or
        [regex]::Matches(
            $actorPrefabText,
            '(?m)^--- !u!114\s').Count -ne 0) {
        throw "GenericActor must contain one Renderer and no MonoBehaviour owner"
    }
}

if (-not (Test-Path -LiteralPath $ResolvedManifestPath -PathType Leaf)) {
    throw "client battle runtime source manifest is missing"
}
$manifest = Get-Content -LiteralPath $ResolvedManifestPath -Raw -Encoding UTF8 |
    ConvertFrom-Json
Assert-SourceManifest $manifest
Assert-ExactProperties $manifest.entryPolicy @(
    "profileVersion",
    "simulationTickMilliseconds",
    "inputCadenceMilliseconds",
    "inputLeadSimulationTicks",
    "snapshotCadenceHertz",
    "mtuBytes",
    "sessionQueueItems",
    "kcpQueueItems",
    "resyncExpiryMilliseconds",
    "snapshotAcknowledgement",
    "routes",
    "developmentReadiness") "entry policy"
Assert-ProfilePolicy $manifest.entryPolicy
Assert-RoutePolicy $manifest.entryPolicy
Assert-SnapshotAcknowledgement $manifest.entryPolicy.snapshotAcknowledgement
Assert-SnapshotEntityStateParity
Assert-DevelopmentReadiness $manifest.entryPolicy.developmentReadiness
Assert-DependencyPolicy
Assert-BaselineCharacterization
Assert-ClientSourcePolicy
if ($Action -eq "validate") {
    Write-Host "[PASS] client battle runtime entry validated."
    return
}

if ($Action -eq "unity-tests") {
    Assert-UnityMaterializedPolicy
    $resolvedUnityEditor = Resolve-LockedUnityEditor

    $runID = [guid]::NewGuid().ToString("N")
    $runDirectory = Join-Path $RepositoryRoot (
        ".local\client-battle-runtime-unity\$runID")
    [System.IO.Directory]::CreateDirectory($runDirectory) | Out-Null
    $resultsPath = Join-Path $runDirectory "editmode-results.xml"
    $logPath = Join-Path $runDirectory "unity.log"
    $arguments = @(
        "-batchmode",
        "-nographics",
        "-projectPath", (Join-Path $RepositoryRoot "client"),
        "-runTests",
        "-testPlatform", "EditMode",
        "-testFilter",
        (
            "IHomeland.Client.Tests.EditMode.ClientBattleRuntimeTests;" +
            "IHomeland.Client.Tests.EditMode.ClientBattleInputContractTests"
        ),
        "-testResults", $resultsPath,
        "-logFile", $logPath)
    $process = Start-Process `
        -FilePath $resolvedUnityEditor `
        -ArgumentList $arguments `
        -PassThru `
        -Wait `
        -WindowStyle Hidden
    if ($process.ExitCode -ne 0 -or
        -not (Test-Path -LiteralPath $resultsPath -PathType Leaf)) {
        throw "client battle runtime Unity EditMode tests failed"
    }

    [xml]$results = Get-Content -LiteralPath $resultsPath -Raw -Encoding UTF8
    $testRun = $results.SelectSingleNode("/test-run")
    if ($null -eq $testRun -or
        [string]$testRun.result -cne "Passed" -or
        [int]$testRun.failed -ne 0 -or
        [int]$testRun.passed -le 0) {
        throw "client battle runtime Unity EditMode results are not passing"
    }

    $playModeResultsPath = Join-Path $runDirectory "playmode-results.xml"
    $playModeLogPath = Join-Path $runDirectory "unity-playmode.log"
    $playModeArguments = @(
        "-batchmode",
        "-nographics",
        "-projectPath", (Join-Path $RepositoryRoot "client"),
        "-runTests",
        "-testPlatform", "PlayMode",
        "-testFilter",
        "IHomeland.Client.Tests.PlayMode.PersonalWorldSceneContextPlayModeTests",
        "-testResults", $playModeResultsPath,
        "-logFile", $playModeLogPath)
    $playModeProcess = Start-Process `
        -FilePath $resolvedUnityEditor `
        -ArgumentList $playModeArguments `
        -PassThru `
        -Wait `
        -WindowStyle Hidden
    if ($playModeProcess.ExitCode -ne 0 -or
        -not (
            Test-Path -LiteralPath $playModeResultsPath -PathType Leaf
        )) {
        throw "client battle runtime Unity PlayMode tests failed"
    }

    [xml]$playModeResults = Get-Content `
        -LiteralPath $playModeResultsPath `
        -Raw `
        -Encoding UTF8
    $playModeRun = $playModeResults.SelectSingleNode("/test-run")
    if ($null -eq $playModeRun -or
        [string]$playModeRun.result -cne "Passed" -or
        [int]$playModeRun.failed -ne 0 -or
        [int]$playModeRun.passed -le 0) {
        throw "client battle runtime Unity PlayMode results are not passing"
    }

    Write-Host (
        "[PASS] client battle runtime Unity EditMode/PlayMode tests passed: " +
        "edit=$($testRun.passed) play=$($playModeRun.passed)")
    return
}

$trackedFallback = @(
    & git -C $RepositoryRoot ls-files -- (
        "client/Assets/App/Generated/Plugins/x86_64"))
if ($LASTEXITCODE -ne 0) {
    throw "tracked client native fallback cannot be inspected"
}
if ($trackedFallback.Count -ne 0) {
    throw "tracked client native fallback is forbidden"
}

$cppTool = Join-Path $RepositoryRoot "tools\cpp\cpp.ps1"
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $cppTool `
    configure `
    -Preset $NativePreset
if ($LASTEXITCODE -ne 0) {
    throw "client native CMake configure failed"
}
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $cppTool `
    build `
    -Preset $NativePreset
if ($LASTEXITCODE -ne 0) {
    throw "client native CMake build failed"
}
$binaryPath = Join-Path $RepositoryRoot (
    "simulation\out\build\$NativePreset\client-native\" +
    "ihomeland_client_battle_native.dll")
if (-not (Test-Path -LiteralPath $binaryPath -PathType Leaf)) {
    throw "client native build did not produce the exact Windows x64 plugin"
}
$firstDigest = Get-Sha256 $binaryPath
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $cppTool `
    build `
    -Preset $NativePreset
if ($LASTEXITCODE -ne 0) {
    throw "client native consecutive rebuild failed"
}
$secondDigest = Get-Sha256 $binaryPath
if ($secondDigest -cne $firstDigest) {
    throw "client native consecutive rebuild digest parity failed"
}
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $cppTool `
    test `
    -Preset $NativePreset `
    -TestRegex '^client\.battle-native\.abi$'
if ($LASTEXITCODE -ne 0) {
    throw "client native ABI test failed"
}
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $cppTool `
    test `
    -Preset $NativePreset `
    -TestRegex (
        '^battle\.(qualification\.protocol-client|' +
        'handshake\.authenticated|crypto\.secure-datagram|' +
        'transport\.(udp-listener|raw-dispatch|kcp-adapter|' +
        'resource-governor|endpoint-rebind|control))$')
if ($LASTEXITCODE -ne 0) {
    throw "client battle real C++ child contract tests failed"
}

$pluginDirectory = Join-Path $RepositoryRoot (
    "client\Assets\App\Generated\Plugins\x86_64")
$pluginPath = Join-Path $pluginDirectory "ihomeland_client_battle_native.dll"
[System.IO.Directory]::CreateDirectory($pluginDirectory) | Out-Null
Copy-Item -LiteralPath $binaryPath -Destination $pluginPath -Force
$ignored = & git -C $RepositoryRoot check-ignore $pluginPath
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($ignored)) {
    throw "generated client native plugin path is not ignored"
}
$digest = Get-Sha256 $pluginPath
Write-Host (
    "[PASS] client battle native plugin rebuilt and copied: " +
    "$NativePreset sha256=$digest parity=$secondDigest")
if ($Action -eq "native-build") {
    return
}

Assert-UnityMaterializedPolicy
Assert-NoTrackedGeneratedArtifacts
$resolvedUnityEditor = Resolve-LockedUnityEditor
$playerRunID = [guid]::NewGuid().ToString("N")
$playerRunDirectory = Join-Path $RepositoryRoot (
    ".local\client-battle-runtime-player\$playerRunID")
$developmentRoot = Join-Path $playerRunDirectory "development"
$releaseRoot = Join-Path $playerRunDirectory "release"
[IO.Directory]::CreateDirectory($playerRunDirectory) | Out-Null
$playerDeadline = [DateTime]::UtcNow.AddSeconds(
    $PlayerTargetedTimeoutSeconds)

$remaining = [int][Math]::Min(
    [int]::MaxValue,
    [Math]::Max(
        1,
        ($playerDeadline - [DateTime]::UtcNow).TotalMilliseconds))
Invoke-UnityBuild `
    -EditorPath $resolvedUnityEditor `
    -Method (
        "IHomeland.Client.Editor.ClientDevelopmentBuild." +
        "BuildWindowsDevelopment") `
    -OutputRoot $developmentRoot `
    -LogPath (Join-Path $playerRunDirectory "development-build.unity.log") `
    -TimeoutMilliseconds $remaining

$remaining = [int][Math]::Min(
    [int]::MaxValue,
    [Math]::Max(
        1,
        ($playerDeadline - [DateTime]::UtcNow).TotalMilliseconds))
Invoke-UnityBuild `
    -EditorPath $resolvedUnityEditor `
    -Method (
        "IHomeland.Client.Editor.ClientDevelopmentBuild." +
        "BuildWindowsRelease") `
    -OutputRoot $releaseRoot `
    -LogPath (Join-Path $playerRunDirectory "release-build.unity.log") `
    -TimeoutMilliseconds $remaining

$developmentPlayer = Get-UniquePlayer -BuildRoot $developmentRoot
$releasePlayer = Get-UniquePlayer -BuildRoot $releaseRoot
Assert-PackagedNativePlugin `
    -BuildRoot $developmentRoot `
    -ExpectedDigest $digest
Assert-PackagedNativePlugin `
    -BuildRoot $releaseRoot `
    -ExpectedDigest $digest
Assert-ReleaseDiagnosticSurfaceRemoved `
    -DevelopmentRoot $developmentRoot `
    -ReleaseRoot $releaseRoot

$smokeStorage = Join-Path $playerRunDirectory "smoke-player-storage"
[IO.Directory]::CreateDirectory($smokeStorage) | Out-Null
Invoke-PlayerBuildSmoke `
    -PlayerPath $developmentPlayer `
    -LogPath (Join-Path $playerRunDirectory "development-smoke.player.log") `
    -AdditionalArguments @(
        "-ihomelandDataProfile",
        "qualification-battle-smoke",
        "-ihomelandQualificationStorageRoot",
        $smokeStorage)
Invoke-PlayerBuildSmoke `
    -PlayerPath $releasePlayer `
    -LogPath (Join-Path $playerRunDirectory "release-smoke.player.log") `
    -AdditionalArguments @()

$localHarness = Join-Path $RepositoryRoot (
    "tools\client-qualification\client-qualification-local.ps1")
& powershell.exe `
    -NoProfile `
    -ExecutionPolicy Bypass `
    -File $localHarness `
    -Action battle `
    -RunId $playerRunID `
    -UnityEditorPath $resolvedUnityEditor `
    -TimeoutSeconds $PlayerTargetedTimeoutSeconds
if ($LASTEXITCODE -ne 0) {
    throw "client battle runtime real Player harness failed"
}

Assert-NoTrackedGeneratedArtifacts
Write-Host (
    "[PASS] client battle runtime Windows Players and real process " +
    "harness passed: run-id=$playerRunID")
