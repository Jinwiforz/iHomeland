#requires -Version 7.0

# 该入口是 B0.6 controlled network qualification 的唯一环境、运行与 cleanup owner。
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("validate", "diagnose", "verify", "soak", "finalize")]
    [string]$Action = "validate",

    # Scenario 只用于 diagnose，必须来自 fault-execution overlay。
    [string]$Scenario = "real-clean-default",

    # TimeoutSeconds 是 action 的全局预算；每个 scenario 仍使用 manifest deadline。
    [ValidateRange(60, 14400)]
    [int]$TimeoutSeconds = 7200,

    # RunA、RunB 与 SoakRun 仅供 finalize 精确选择 evidence owner。
    [string]$RunA = "",
    [string]$RunB = "",
    [string]$SoakRun = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$CorpusRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\qualification"
$LocalRoot = Join-Path $RepositoryRoot ".local\battle-qualification"
$BuildCacheRoot = Join-Path $RepositoryRoot ".local\battle-qualification-build-cache"
$GoTool = Join-Path $RepositoryRoot "tools\go\go.ps1"
$CppTool = Join-Path $RepositoryRoot "tools\cpp\cpp.ps1"
$EnvironmentModule = Join-Path $PSScriptRoot "internal\QualificationEnvironment.psm1"
$CorpusModule = Join-Path $PSScriptRoot "internal\QualificationCorpus.psm1"
$BuildCacheModule = Join-Path $PSScriptRoot "internal\QualificationBuildCache.psm1"
$Deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
$ProcessLifecycleExitBudgetMilliseconds = 15000
Import-Module $EnvironmentModule -Force
Import-Module $CorpusModule -Force
Import-Module $BuildCacheModule -Force

# Write-Stage 输出稳定低敏阶段，不打印 endpoint、PID 或 absolute artifact path。
function Write-Stage {
    param([Parameter(Mandatory = $true)][string]$Name)
    Write-Host "[B0.6] $Name"
}

# Assert-Remaining 在开始新的昂贵阶段前执行全局 deadline gate。
function Assert-Remaining {
    if ([DateTime]::UtcNow -ge $Deadline) {
        throw "battle qualification global deadline exceeded"
    }
}

# New-FailedCleanupEvidence 建立 fail-closed cleanup 结果，不以未知值伪装为零。
function New-FailedCleanupEvidence {
    return [pscustomobject][ordered]@{
        disposition = "failed"
        remainingProcesses = 1
        remainingListeners = 1
        remainingContainers = 1
        reusableCredentials = 1
    }
}

# Get-CombinedDigest 对命名文件摘要做 canonical SHA-256，避免把本机路径写入 evidence。
function Get-CombinedDigest {
    param(
        [Parameter(Mandatory = $true)][hashtable]$Files
    )
    $lines = foreach ($name in @($Files.Keys | Sort-Object)) {
        $path = [string]$Files[$name]
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "battle qualification identity input missing: $name"
        }
        $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        "$name=$hash"
    }
    $bytes = [Text.Encoding]::UTF8.GetBytes(($lines -join "`n") + "`n")
    return [Convert]::ToHexString(
        [Security.Cryptography.SHA256]::HashData($bytes)
    ).ToLowerInvariant()
}

# Get-TextDigest 对稳定环境描述做 SHA-256，不把原文写入 report。
function Get-TextDigest {
    param([Parameter(Mandatory = $true)][string]$Value)
    return [Convert]::ToHexString(
        [Security.Cryptography.SHA256]::HashData(
            [Text.Encoding]::UTF8.GetBytes($Value)
        )
    ).ToLowerInvariant()
}

# Get-SourceDigest 绑定 tracked 与未忽略 source；长期资格报告不是候选源码，避免报告自绑定。
function Get-SourceDigest {
    $paths = @(
        & git -C $RepositoryRoot ls-files --cached --others --exclude-standard
    )
    if ($LASTEXITCODE -ne 0) {
        throw "battle qualification source inventory failed"
    }
    $selected = @($paths | Where-Object {
        (
            $_ -match '^(server|simulation|shared/contracts|tools/battle-qualification)/' -or
            $_ -ceq 'versions.yaml'
        ) -and
        $_ -notmatch '^shared/contracts/evidence/'
    } | Sort-Object -Unique)
    if ($selected.Count -eq 0) {
        throw "battle qualification source inventory is empty"
    }
    $files = @{}
    foreach ($relativePath in $selected) {
        $files[$relativePath] = Join-Path $RepositoryRoot (
            $relativePath.Replace("/", "\")
        )
    }
    return Get-CombinedDigest -Files $files
}

# Get-RunIdentity 生成两次 verify 与 soak 必须逐字段完全相同的冻结 identity。
function Get-RunIdentity {
    param(
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)]$Cpp
    )
    $buildRoot = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci"
    $environmentMaterial = @(
        "windows-x64",
        "controlled-local-fault-gateway",
        [Environment]::Is64BitProcess,
        [Environment]::OSVersion.VersionString
    ) -join "|"
    return [pscustomobject][ordered]@{
        sourceSha256 = Get-SourceDigest
        binarySha256 = Get-CombinedDigest -Files @{
            "battle-protocol-client" = $Cpp.Client
            "battle-qualification-tool" = Join-Path $RunDirectory "battlequalificationtool.exe"
            "qualification-tool" = Join-Path $RunDirectory "qualificationtool.exe"
            "server" = Join-Path $RunDirectory "server.exe"
        }
        toolchainSha256 = Get-CombinedDigest -Files @{
            "build-identity" = Join-Path $buildRoot "ihomeland-build-identity.json"
            "qualification-receipt" = Join-Path $buildRoot "qualification-gate-receipt.json"
        }
        dependencySha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "versions.yaml") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        modelSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model\manifest.json") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        profileSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\manifest.json") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        controlSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\simulation-control\manifest.json") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        wireSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\wire\manifest.json") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        configSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $RepositoryRoot "server\config\simulation-control.example.yaml") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        environmentSha256 = Get-TextDigest -Value $environmentMaterial
        faultSha256 = Get-CombinedDigest -Files @{
            "fault-execution" = Join-Path $CorpusRoot "fault-execution.json"
            "lifecycle-security" = Join-Path $CorpusRoot "lifecycle-security.json"
        }
        workloadSha256 = (Get-FileHash `
            -LiteralPath (Join-Path $CorpusRoot "workloads.json") `
            -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

# Invoke-Native 执行项目入口并把非零退出统一收敛为阶段失败。
function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Category
    )
    Assert-Remaining
    $output = @(& $FilePath @Arguments)
    $exitCode = $LASTEXITCODE
    foreach ($line in $output) {
        Write-Host $line
    }
    if ($exitCode -ne 0) {
        throw "battle qualification $Category failed"
    }
}

# Invoke-EntryGate 在构建和启动进程前执行只读 identity/coverage gate。
function Invoke-EntryGate {
    param(
        # AllowSourceDrift 仅用于 tooling validate/diagnose；资格动作必须匹配冻结上游摘要。
        [switch]$AllowSourceDrift
    )

    Write-Stage "entry gate"
    $before = Get-QualificationTreeDigest -QualificationRoot $CorpusRoot
    $validated = Invoke-BattleQualificationCorpusValidation `
        -QualificationRoot $CorpusRoot `
        -RepositoryRoot $RepositoryRoot `
        -AllowSourceDrift:$AllowSourceDrift
    if ($validated -cne $before) {
        throw "battle qualification validator changed source corpus"
    }
}

# Invoke-RegressionGate 在完整 matrix 后执行 corpus、client 与 scope failure regression。
function Invoke-RegressionGate {
    param(
        # AllowSourceDrift 仅供静态 tooling validate；完整 verify 保持严格 upstream identity。
        [switch]$AllowSourceDrift
    )

    Write-Stage "regression gate"
    $corpusArguments = @(
        "-NoProfile", "-File",
        (Join-Path $PSScriptRoot "qualification-corpus.tests.ps1")
    )
    if ($AllowSourceDrift) {
        $corpusArguments += "-AllowSourceDrift"
    }
    Invoke-Native `
        -FilePath (Get-Command pwsh.exe -ErrorAction Stop).Source `
        -Arguments $corpusArguments `
        -Category "corpus regression"
    $ciClient = Join-Path $RepositoryRoot `
        "simulation\out\build\windows-msvc-ci\ihomeland-battle-protocol-client.exe"
    if (Test-Path -LiteralPath $ciClient -PathType Leaf) {
        Invoke-Native `
            -FilePath (Get-Command pwsh.exe -ErrorAction Stop).Source `
            -Arguments @(
                "-NoProfile", "-File",
                (Join-Path $PSScriptRoot "protocol-client.tests.ps1"),
                "-RepositoryRoot", $RepositoryRoot,
                "-ClientPath", $ciClient
            ) `
            -Category "protocol client contract"
    }
    Invoke-Native `
        -FilePath (Get-Command pwsh.exe -ErrorAction Stop).Source `
        -Arguments @(
            "-NoProfile", "-File",
            (Join-Path $PSScriptRoot "scope.tests.ps1"),
            "-RepositoryRoot", $RepositoryRoot
        ) `
        -Category "scope and secret"
    Invoke-Native `
        -FilePath (Get-Command pwsh.exe -ErrorAction Stop).Source `
        -Arguments @(
            "-NoProfile", "-File",
            (Join-Path $PSScriptRoot "qualification-build-cache.tests.ps1")
        ) `
        -Category "diagnostic build cache"
}

# New-RunDirectory 建立并验证 ignored local run owner。
function New-RunDirectory {
    $runId = "bqrun_" + [guid]::NewGuid().ToString("N")
    $rootPrefix = [IO.Path]::GetFullPath($LocalRoot).TrimEnd("\") + "\"
    $runDirectory = [IO.Path]::GetFullPath((Join-Path $LocalRoot $runId))
    if (-not $runDirectory.StartsWith($rootPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "battle qualification run directory escaped local root"
    }
    [void](New-Item -ItemType Directory -Path $runDirectory -Force)
    return [pscustomobject]@{ Id = $runId; Directory = $runDirectory }
}

# Invoke-GoBuildSet 把三个 Go composition roots 构建到指定目录，不解释其内部依赖。
function Invoke-GoBuildSet {
    param([Parameter(Mandatory = $true)][string]$OutputDirectory)

    Invoke-Native -FilePath "powershell.exe" `
        -Arguments @(
            "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $GoTool,
            "build", ("-o=" + (Join-Path $OutputDirectory "server.exe")), "./cmd/server"
        ) `
        -Category "Go server build"
    Invoke-Native -FilePath "powershell.exe" `
        -Arguments @(
            "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $GoTool,
            "build", ("-o=" + (Join-Path $OutputDirectory "qualificationtool.exe")),
            "./cmd/qualificationtool"
        ) `
        -Category "Go TLS tool build"
    Invoke-Native -FilePath "powershell.exe" `
        -Arguments @(
            "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $GoTool,
            "build", ("-o=" + (Join-Path $OutputDirectory "battlequalificationtool.exe")),
            "./cmd/battlequalificationtool"
        ) `
        -Category "Go battle runner build"
}

# Get-DiagnosticBuildCache 只为非资格 diagnose 复用同源 Go binaries；最终 verify/soak 不消费。
function Get-DiagnosticBuildCache {
    $sourceDigest = Get-SourceDigest
    $goToolDigest = (
        Get-FileHash -LiteralPath $GoTool -Algorithm SHA256
    ).Hash.ToLowerInvariant()
    $cacheMaterial = [Text.Encoding]::UTF8.GetBytes(
        "source=$sourceDigest`ngo-tool=$goToolDigest`n"
    )
    $cacheKey = [Convert]::ToHexString(
        [Security.Cryptography.SHA256]::HashData($cacheMaterial)
    ).ToLowerInvariant()
    $cacheDirectory = Join-Path $BuildCacheRoot $cacheKey
    $binaryNames = @(
        "server.exe",
        "qualificationtool.exe",
        "battlequalificationtool.exe"
    )
    if (Test-QualificationBuildCacheReceipt `
        -CacheDirectory $cacheDirectory `
        -CacheKey $cacheKey `
        -SourceSha256 $sourceDigest `
        -BinaryNames $binaryNames) {
        Write-Stage "reuse diagnostic binary cache"
        return [pscustomobject]@{
            Directory = $cacheDirectory
            BinaryNames = $binaryNames
        }
    }

    [void](New-Item -ItemType Directory -Path $BuildCacheRoot -Force)
    $stagingDirectory = Join-Path $BuildCacheRoot (
        ".staging-" + [guid]::NewGuid().ToString("N")
    )
    Assert-QualificationBuildCachePath `
        -Path $stagingDirectory `
        -CacheRoot $BuildCacheRoot
    [void](New-Item -ItemType Directory -Path $stagingDirectory -Force)
    try {
        Invoke-GoBuildSet -OutputDirectory $stagingDirectory
        Write-QualificationBuildCacheReceipt `
            -CacheDirectory $stagingDirectory `
            -CacheKey $cacheKey `
            -SourceSha256 $sourceDigest `
            -BinaryNames $binaryNames
        if (Test-Path -LiteralPath $cacheDirectory) {
            Assert-QualificationBuildCachePath `
                -Path $cacheDirectory `
                -CacheRoot $BuildCacheRoot
            Remove-Item -LiteralPath $cacheDirectory -Recurse -Force
        }
        Move-Item -LiteralPath $stagingDirectory -Destination $cacheDirectory
        $stagingDirectory = ""
    }
    finally {
        if ($stagingDirectory -and (Test-Path -LiteralPath $stagingDirectory)) {
            Assert-QualificationBuildCachePath `
                -Path $stagingDirectory `
                -CacheRoot $BuildCacheRoot
            Remove-Item -LiteralPath $stagingDirectory -Recurse -Force
        }
    }
    return [pscustomobject]@{
        Directory = $cacheDirectory
        BinaryNames = $binaryNames
    }
}

# Invoke-Build 增量构建 exact C++ target；只有 diagnose 可复用内容寻址的 Go binary cache。
function Invoke-Build {
    param(
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [switch]$UseDiagnosticCache
    )
    Write-Stage "build exact binaries"
    Invoke-Native -FilePath "powershell.exe" `
        -Arguments @(
            "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $CppTool,
            "build", "-Preset", "windows-msvc-ci"
        ) `
        -Category "C++ build"
    if ($UseDiagnosticCache) {
        $cache = Get-DiagnosticBuildCache
        New-QualificationBuildCacheLinks `
            -CacheDirectory $cache.Directory `
            -RunDirectory $RunDirectory `
            -BinaryNames $cache.BinaryNames
        return
    }
    Invoke-GoBuildSet -OutputDirectory $RunDirectory
}

# Get-CppIdentity 返回 exact executable 与编译期 receipt 共用的 target identity。
function Get-CppIdentity {
    $buildRoot = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci"
    $identity = Get-Content `
        -LiteralPath (Join-Path $buildRoot "ihomeland-build-identity.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    if ([string]$identity.target_identity -notmatch '^[0-9a-f]{64}$') {
        throw "battle qualification C++ identity receipt is invalid"
    }
    $client = Join-Path $buildRoot "ihomeland-battle-protocol-client.exe"
    if (-not (Test-Path -LiteralPath $client -PathType Leaf)) {
        throw "battle qualification protocol client is missing"
    }
    return [pscustomobject]@{
        Identity = [string]$identity.target_identity
        Client = $client
    }
}

# Invoke-Scenario 执行单个 scenario 并立即用 tracked closed schema 验证 evidence。
function Invoke-Scenario {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)]$Cpp,
        [Parameter(Mandatory = $true)][string]$ScenarioId,
        [switch]$SoakMode,
        [switch]$SecurityMode,
        [switch]$GatewayLifecycleMode,
        [string]$WorkloadId = ""
    )
    Assert-Remaining
    $evidenceScenarioId = if ($SecurityMode) {
        "security-" + $ScenarioId
    }
    elseif ($GatewayLifecycleMode) {
        "lifecycle-" + $ScenarioId
    }
    else {
        $ScenarioId
    }
    Write-Stage "scenario $evidenceScenarioId"
    $evidencePath = Join-Path $Run.Directory ($evidenceScenarioId + ".json")
    $runnerAction = if ($SoakMode) {
        "run-soak"
    }
    elseif ($SecurityMode) {
        "run-security"
    }
    elseif ($GatewayLifecycleMode) {
        "run-gateway-lifecycle"
    }
    elseif ($WorkloadId) {
        "run-capacity"
    }
    else {
        "run-scenario"
    }
    $runnerArguments = @(
        $runnerAction,
        "-base-url", "https://localhost:$($Environment.PublicPort)",
        "-ca-file", (Join-Path $Run.Directory "server-cert.pem"),
        "-repository-root", $RepositoryRoot,
        "-protocol-client", $Cpp.Client,
        "-build-identity", $Cpp.Identity,
        "-backend", "127.0.0.1:$($Environment.BackendPort)",
        "-frontend-bind", "127.0.0.1:$($Environment.FrontendPort)",
        "-diagnostic-url", "http://127.0.0.1:$($Environment.DiagnosticPort)/metrics",
        "-evidence", $evidencePath
    )
    if ($WorkloadId) {
        $runnerArguments += @("-workload", $WorkloadId)
    }
    else {
        $runnerArguments += @("-scenario", $ScenarioId)
    }
    try {
        Invoke-Native `
            -FilePath (Join-Path $Run.Directory "battlequalificationtool.exe") `
            -Arguments $runnerArguments `
            -Category "scenario"
    }
    catch {
        if (Test-Path -LiteralPath $evidencePath -PathType Leaf) {
            $failureSchema = Join-Path $CorpusRoot "scenario-failure-evidence.schema.json"
            if (-not (Test-Json `
                -LiteralPath $evidencePath `
                -SchemaFile $failureSchema `
                -ErrorAction Stop)) {
                throw "battle qualification scenario failure evidence schema failed"
            }
            $failureText = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8
            if ($failureText -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
                throw "battle qualification scenario failure evidence crossed low-sensitive boundary"
            }
            Write-Stage "failure evidence $evidenceScenarioId"
        }
        throw
    }
    $evidenceSchema = if ($SecurityMode -or $GatewayLifecycleMode) {
        "gate-evidence.schema.json"
    }
    else {
        "scenario-evidence.schema.json"
    }
    if (-not (Test-Json `
        -LiteralPath $evidencePath `
        -SchemaFile (Join-Path $CorpusRoot $evidenceSchema) `
        -ErrorAction Stop)) {
        throw "battle qualification scenario evidence schema failed"
    }
    $document = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8 |
        ConvertFrom-Json
    $expectedKind = if ($SoakMode) {
        "soak-run-evidence"
    }
    elseif ($SecurityMode) {
        "security-availability-evidence"
    }
    elseif ($GatewayLifecycleMode) {
        "lifecycle-evidence"
    }
    else {
        "scenario-run-evidence"
    }
    if ([string]$document.evidenceKind -cne $expectedKind) {
        throw "battle qualification evidence kind drifted"
    }
    $text = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8
    if ($text -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
        throw "battle qualification scenario evidence crossed low-sensitive boundary"
    }
    return [pscustomobject]@{
        ScenarioId = $evidenceScenarioId
        WorkloadId = [string]$document.workloadId
        EvidenceKind = [string]$document.evidenceKind
        Sha256 = (Get-FileHash -LiteralPath $evidencePath -Algorithm SHA256).Hash.ToLowerInvariant()
        Disposition = "passed"
    }
}

# Invoke-GatewayLifecycleMatrix 执行 manifest 分配给 fault-gateway 的迁移。
function Invoke-GatewayLifecycleMatrix {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)]$Cpp
    )
    $policy = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "lifecycle-security.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    foreach ($caseId in @($policy.lifecycleCases)) {
        $owner = [string]$policy.lifecycleOwners.PSObject.Properties[
            [string]$caseId
        ].Value
        if ($owner -cne "fault-gateway") {
            continue
        }
        Restart-BattleQualificationServer `
            -Environment $Environment `
            -Deadline $Deadline
        Write-Output (Invoke-Scenario `
            -Run $Run `
            -Environment $Environment `
            -Cpp $Cpp `
            -ScenarioId ([string]$caseId) `
            -GatewayLifecycleMode)
    }
}

# Invoke-SecurityMatrix 从 tracked inventory 顺序执行本机协议拒绝一致性用例。
function Invoke-SecurityMatrix {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)]$Cpp
    )
    $policy = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "lifecycle-security.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    foreach ($caseId in @($policy.securityCases)) {
        Restart-BattleQualificationServer `
            -Environment $Environment `
            -Deadline $Deadline
        Write-Output (Invoke-Scenario `
            -Run $Run `
            -Environment $Environment `
            -Cpp $Cpp `
            -ScenarioId ([string]$caseId) `
            -SecurityMode)
    }
}

# Invoke-BusinessLifecycle 执行单个 public-protocol owner 场景并校验低敏证据。
function Invoke-BusinessLifecycle {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)][string]$CaseId
    )
    Assert-Remaining
    $scenarioId = "lifecycle-" + $CaseId
    Write-Stage "scenario $scenarioId"
    $evidencePath = Join-Path $Run.Directory ($scenarioId + ".json")
    Invoke-Native `
        -FilePath (Join-Path $Run.Directory "battlequalificationtool.exe") `
        -Arguments @(
            "run-business-lifecycle",
            "-base-url", "https://localhost:$($Environment.PublicPort)",
            "-ca-file", (Join-Path $Run.Directory "server-cert.pem"),
            "-repository-root", $RepositoryRoot,
            "-case", $CaseId,
            "-evidence", $evidencePath
        ) `
        -Category "business lifecycle"
    if (-not (Test-Json `
        -LiteralPath $evidencePath `
        -SchemaFile (Join-Path $CorpusRoot "gate-evidence.schema.json") `
        -ErrorAction Stop)) {
        throw "battle qualification lifecycle evidence schema failed"
    }
    $document = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8 |
        ConvertFrom-Json
    if (
        [string]$document.evidenceKind -cne "lifecycle-evidence" -or
        [string]$document.scenarioId -cne $scenarioId
    ) {
        throw "battle qualification lifecycle evidence identity drifted"
    }
    $text = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8
    if ($text -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
        throw "battle qualification lifecycle evidence crossed low-sensitive boundary"
    }
    return [pscustomobject]@{
        ScenarioId = $scenarioId
        WorkloadId = [string]$document.workloadId
        EvidenceKind = [string]$document.evidenceKind
        Sha256 = (Get-FileHash -LiteralPath $evidencePath -Algorithm SHA256).Hash.ToLowerInvariant()
        Disposition = "passed"
    }
}

# Invoke-PublicLifecycleMatrix 只执行 manifest 明确分配给 public-protocol 的 case。
function Invoke-PublicLifecycleMatrix {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment
    )
    $policy = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "lifecycle-security.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    foreach ($caseId in @($policy.lifecycleCases)) {
        $owner = [string]$policy.lifecycleOwners.PSObject.Properties[
            [string]$caseId
        ].Value
        if ($owner -cne "public-protocol") {
            continue
        }
        Restart-BattleQualificationServer `
            -Environment $Environment `
            -Deadline $Deadline
        Write-Output (Invoke-BusinessLifecycle `
            -Run $Run `
            -Environment $Environment `
            -CaseId ([string]$caseId))
    }
}

# Get-OwnedSimulationChild 解析当前 Go parent 唯一直接拥有的 exact C++ child。
function Get-OwnedSimulationChild {
    param([Parameter(Mandatory = $true)]$Environment)
    if (-not $Environment.Process -or $Environment.Process.HasExited) {
        throw "battle qualification Go process is unavailable"
    }
    $expectedName = [IO.Path]::GetFileName(
        (Join-Path $RepositoryRoot `
            "simulation\out\build\windows-msvc-ci\ihomeland-sim-server.exe")
    )
    $children = @(
        Get-CimInstance -ClassName Win32_Process `
            -Filter ("ParentProcessId = " + [int]$Environment.Process.Id) |
            Where-Object { [string]$_.Name -ceq $expectedName }
    )
    if ($children.Count -ne 1) {
        throw "battle qualification exact C++ child ownership is ambiguous"
    }
    return Get-Process -Id ([int]$children[0].ProcessId) -ErrorAction Stop
}

# Wait-OwnedServerExit 验证 exact Go process 在生命周期预算内完成 drain/shutdown。
function Wait-OwnedServerExit {
    param([Parameter(Mandatory = $true)]$Environment)
    if (-not $Environment.Process.WaitForExit(
        $ProcessLifecycleExitBudgetMilliseconds
    )) {
        throw "battle qualification Go process exceeded drain/shutdown deadline"
    }
}

# Assert-SupervisedShutdownEvidence 验证真实 root lifecycle 进入 draining 并完成停止。
function Assert-SupervisedShutdownEvidence {
    param([Parameter(Mandatory = $true)]$Environment)
    $logPath = [string]$Environment.StandardOutputPath
    if (-not (Test-Path -LiteralPath $logPath -PathType Leaf)) {
        throw "battle qualification shutdown lifecycle log is missing"
    }
    $logText = Get-Content -LiteralPath $logPath -Raw -Encoding utf8
    if (
        $logText -notmatch '"msg":"server runtime draining"' -or
        (
            $logText -notmatch '"msg":"server runtime stopped after fatal error"' -and
            $logText -notmatch '"msg":"server runtime shutdown failed"'
        )
    ) {
        throw "battle qualification shutdown lifecycle evidence is incomplete"
    }
}

# Invoke-ProcessLifecycleFault 执行 manifest 注册的精确 process-supervisor 动作。
function Invoke-ProcessLifecycleFault {
    param(
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)][string]$CaseId
    )
    $predecessorId = [int]$Environment.Process.Id
    $predecessorStarted = $Environment.Process.StartTime.ToUniversalTime()
    switch ($CaseId) {
        { $_ -in @("assignment-replacement", "go-restart") } {
            Restart-BattleQualificationServer `
                -Environment $Environment `
                -Deadline $Deadline
            if (
                [int]$Environment.Process.Id -eq $predecessorId -and
                $Environment.Process.StartTime.ToUniversalTime() -eq
                    $predecessorStarted
            ) {
                throw "battle qualification Go process incarnation was not replaced"
            }
        }
        "child-crash-restart" {
            $predecessorChild = Get-OwnedSimulationChild -Environment $Environment
            $predecessorChildId = [int]$predecessorChild.Id
            Stop-Process -Id $predecessorChildId -Force -ErrorAction Stop
            if (-not $predecessorChild.WaitForExit(
                $ProcessLifecycleExitBudgetMilliseconds
            )) {
                throw "battle qualification predecessor C++ child did not exit"
            }
            Wait-OwnedServerExit -Environment $Environment
            Assert-SupervisedShutdownEvidence -Environment $Environment
            Restart-BattleQualificationServer `
                -Environment $Environment `
                -Deadline $Deadline
            $successorChild = Get-OwnedSimulationChild -Environment $Environment
            if (
                [int]$successorChild.Id -eq $predecessorChildId -and
                $successorChild.StartTime.ToUniversalTime() -eq
                    $predecessorChild.StartTime.ToUniversalTime()
            ) {
                throw "battle qualification C++ child incarnation was not replaced"
            }
        }
        "shutdown-drain-deadline" {
            $predecessorChild = Get-OwnedSimulationChild -Environment $Environment
            Stop-Process -Id $predecessorChild.Id -Force -ErrorAction Stop
            if (-not $predecessorChild.WaitForExit(
                $ProcessLifecycleExitBudgetMilliseconds
            )) {
                throw "battle qualification shutdown trigger child did not exit"
            }
            Wait-OwnedServerExit -Environment $Environment
            Assert-SupervisedShutdownEvidence -Environment $Environment
        }
        default {
            throw "battle qualification process lifecycle case is unsupported"
        }
    }
}

# Write-ProcessFaultResponse 原子发布低敏 process checkpoint 结果。
function Write-ProcessFaultResponse {
    param(
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][uint64]$Sequence,
        [Parameter(Mandatory = $true)][string]$Outcome
    )
    $path = Join-Path $RunDirectory "fault-response.json"
    $temporary = $path + ".tmp"
    $body = [ordered]@{
        schemaVersion = 1
        sequence = $Sequence
        outcome = $Outcome
    } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText(
        $temporary,
        $body + "`n",
        [Text.UTF8Encoding]::new($false)
    )
    Move-Item -LiteralPath $temporary -Destination $path -Force
}

# Invoke-ProcessLifecycle 并发服务单槽 checkpoint，并在 child 退出后校验证据。
function Invoke-ProcessLifecycle {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)][string]$CaseId
    )
    Assert-Remaining
    $scenarioId = "lifecycle-" + $CaseId
    Write-Stage "scenario $scenarioId"
    $evidencePath = Join-Path $Run.Directory ($scenarioId + ".json")
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = Join-Path $Run.Directory "battlequalificationtool.exe"
    foreach ($argument in @(
        "run-process-lifecycle",
        "-base-url", "https://localhost:$($Environment.PublicPort)",
        "-ca-file", (Join-Path $Run.Directory "server-cert.pem"),
        "-repository-root", $RepositoryRoot,
        "-case", $CaseId,
        "-control-directory", $Run.Directory,
        "-evidence", $evidencePath
    )) {
        [void]$startInfo.ArgumentList.Add([string]$argument)
    }
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw "battle qualification process lifecycle client failed to start"
    }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $lastSequence = [uint64]0
    $ownerFailure = $null
    try {
        while (-not $process.HasExited) {
            Assert-Remaining
            $requestPath = Join-Path $Run.Directory "fault-request.json"
            if (Test-Path -LiteralPath $requestPath -PathType Leaf) {
                try {
                    $requestText = [IO.File]::ReadAllText(
                        $requestPath,
                        [Text.Encoding]::UTF8
                    )
                }
                catch [IO.IOException] {
                    Start-Sleep -Milliseconds 25
                    continue
                }
                $request = $requestText | ConvertFrom-Json
                $sequence = [uint64]$request.sequence
                if (
                    [int]$request.schemaVersion -ne 1 -or
                    $sequence -lt $lastSequence -or
                    [string]$request.kind -cne $CaseId
                ) {
                    throw "battle qualification process checkpoint is invalid"
                }
                if ($sequence -eq $lastSequence) {
                    Start-Sleep -Milliseconds 50
                    continue
                }
                $lastSequence = $sequence
                try {
                    Invoke-ProcessLifecycleFault `
                        -Environment $Environment `
                        -CaseId $CaseId
                    Write-ProcessFaultResponse `
                        -RunDirectory $Run.Directory `
                        -Sequence $sequence `
                        -Outcome "pass"
                }
                catch {
                    $ownerFailure = $_
                    Write-ProcessFaultResponse `
                        -RunDirectory $Run.Directory `
                        -Sequence $sequence `
                        -Outcome "fail"
                }
            }
            Start-Sleep -Milliseconds 50
        }
        [void]$process.WaitForExit()
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        if ($ownerFailure) {
            throw "battle qualification process owner action failed"
        }
        if ($process.ExitCode -ne 0 -or $lastSequence -ne [uint64]1) {
            throw "battle qualification process lifecycle failed: $stderr"
        }
        if ($stdout -notmatch 'battle-lifecycle-passed') {
            throw "battle qualification process lifecycle receipt is missing"
        }
    }
    finally {
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            [void]$process.WaitForExit(
                $ProcessLifecycleExitBudgetMilliseconds
            )
        }
        foreach ($checkpoint in @("fault-request.json", "fault-response.json")) {
            $checkpointPath = Join-Path $Run.Directory $checkpoint
            if (Test-Path -LiteralPath $checkpointPath) {
                Remove-Item -LiteralPath $checkpointPath -Force
            }
        }
        $process.Dispose()
    }
    if (-not (Test-Json `
        -LiteralPath $evidencePath `
        -SchemaFile (Join-Path $CorpusRoot "gate-evidence.schema.json") `
        -ErrorAction Stop)) {
        throw "battle qualification process lifecycle evidence schema failed"
    }
    $document = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8 |
        ConvertFrom-Json
    if (
        [string]$document.evidenceKind -cne "lifecycle-evidence" -or
        [string]$document.scenarioId -cne $scenarioId
    ) {
        throw "battle qualification process lifecycle identity drifted"
    }
    $text = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8
    if ($text -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
        throw "battle qualification process lifecycle crossed low-sensitive boundary"
    }
    return [pscustomobject]@{
        ScenarioId = $scenarioId
        WorkloadId = [string]$document.workloadId
        EvidenceKind = [string]$document.evidenceKind
        Sha256 = (Get-FileHash -LiteralPath $evidencePath `
            -Algorithm SHA256).Hash.ToLowerInvariant()
        Disposition = "passed"
    }
}

# Invoke-ProcessLifecycleMatrix 只执行 manifest 分配给 process-supervisor 的 case。
function Invoke-ProcessLifecycleMatrix {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment
    )
    $policy = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "lifecycle-security.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    foreach ($caseId in @($policy.lifecycleCases)) {
        $owner = [string]$policy.lifecycleOwners.PSObject.Properties[
            [string]$caseId
        ].Value
        if ($owner -cne "process-supervisor") {
            continue
        }
        Restart-BattleQualificationServer `
            -Environment $Environment `
            -Deadline $Deadline
        Write-Output (Invoke-ProcessLifecycle `
            -Run $Run `
            -Environment $Environment `
            -CaseId ([string]$caseId))
    }
}

# Invoke-AdmissionCapacity 验证 9 actor gate 与 33 member compatibility。
function Invoke-AdmissionCapacity {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment
    )
    Write-Stage "admission capacity"
    $evidencePath = Join-Path $Run.Directory "admission-capacity.json"
    Invoke-Native `
        -FilePath (Join-Path $Run.Directory "battlequalificationtool.exe") `
        -Arguments @(
            "run-admission-capacity",
            "-base-url", "https://localhost:$($Environment.PublicPort)",
            "-ca-file", (Join-Path $Run.Directory "server-cert.pem"),
            "-repository-root", $RepositoryRoot,
            "-evidence", $evidencePath
        ) `
        -Category "admission capacity"
    if (-not (Test-Json `
        -LiteralPath $evidencePath `
        -SchemaFile (Join-Path $CorpusRoot "admission-evidence.schema.json") `
        -ErrorAction Stop)) {
        throw "battle qualification admission evidence schema failed"
    }
    return [pscustomobject]@{
        ScenarioId = "admission-capacity"
        WorkloadId = "visit-capacity-compatibility"
        EvidenceKind = "admission-capacity-evidence"
        Sha256 = (Get-FileHash -LiteralPath $evidencePath -Algorithm SHA256).Hash.ToLowerInvariant()
        Disposition = "passed"
    }
}

# Invoke-SoakRun 运行 tracked 30 分钟窗口，并保留独立 cleanup owner。
function Invoke-SoakRun {
    $run = New-RunDirectory
    $environment = $null
    $results = @()
    $identity = $null
    $cleanup = New-FailedCleanupEvidence
    try {
        Invoke-Build -RunDirectory $run.Directory
        $cpp = Get-CppIdentity
        $identity = Get-RunIdentity -RunDirectory $run.Directory -Cpp $cpp
        $environment = Start-BattleQualificationEnvironment `
            -RepositoryRoot $RepositoryRoot `
            -RunDirectory $run.Directory `
            -RunId $run.Id `
            -ServerBinary (Join-Path $run.Directory "server.exe") `
            -QualificationTool (Join-Path $run.Directory "qualificationtool.exe") `
            -Deadline $Deadline
        $results += Invoke-Scenario `
            -Run $run -Environment $environment -Cpp $cpp `
            -ScenarioId "real-clean-default" -SoakMode
        $cleanup = Stop-BattleQualificationEnvironment `
            -RepositoryRoot $RepositoryRoot `
            -Environment $environment
        $environment = $null
        Write-RunIndex -Run $run -Kind "soak" -Identity $identity `
            -Results $results -Cleanup $cleanup
        Write-Host "RunId=$($run.Id)"
    }
    finally {
        if ($environment) {
            try {
                $cleanup = Stop-BattleQualificationEnvironment `
                    -RepositoryRoot $RepositoryRoot `
                    -Environment $environment
            }
            catch {
                $cleanup = New-FailedCleanupEvidence
            }
        }
        if (-not (Test-Path -LiteralPath (Join-Path $run.Directory "run.json"))) {
            Write-RunIndex -Run $run -Kind "soak" -Identity $identity `
                -Results $results -Cleanup $cleanup
        }
    }
}

# Write-RunIndex 只登记 scenario identity/digest 与 cleanup outcome。
function Write-RunIndex {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)][string]$Kind,
        $Identity,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$Results,
        [Parameter(Mandatory = $true)]$Cleanup
    )
    $body = [ordered]@{
        schemaVersion = 1
        qualificationVersion = "battle-network-qualification-v1"
        runKind = $Kind
        runId = $Run.Id
        identity = $Identity
        scenarios = @($Results)
        cleanup = $Cleanup
    }
    [IO.File]::WriteAllText(
        (Join-Path $Run.Directory "run.json"),
        (($body | ConvertTo-Json -Depth 6) + "`n"),
        [Text.UTF8Encoding]::new($false)
    )
    $runPath = Join-Path $Run.Directory "run.json"
    if (-not (Test-Json `
        -LiteralPath $runPath `
        -SchemaFile (Join-Path $CorpusRoot "run-evidence.schema.json") `
        -ErrorAction Stop)) {
        throw "battle qualification run evidence schema failed"
    }
    $runText = Get-Content -LiteralPath $runPath -Raw -Encoding utf8
    if ($runText -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
        throw "battle qualification run evidence crossed low-sensitive boundary"
    }
}

# Invoke-DiagnosticScenario 将任一 tracked gate 收敛到单场景只读诊断入口。
function Invoke-DiagnosticScenario {
    param(
        [Parameter(Mandatory = $true)]$Run,
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)]$Cpp,
        [Parameter(Mandatory = $true)][string]$ScenarioId
    )
    $execution = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "fault-execution.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    if (@($execution.scenarios.scenarioId) -ccontains $ScenarioId) {
        return Invoke-Scenario `
            -Run $Run `
            -Environment $Environment `
            -Cpp $Cpp `
            -ScenarioId $ScenarioId
    }
    $workloads = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "workloads.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    $capacityPrefix = "capacity-"
    if ($ScenarioId.StartsWith(
        $capacityPrefix,
        [StringComparison]::Ordinal
    )) {
        $workloadId = $ScenarioId.Substring($capacityPrefix.Length)
        if (@($workloads.workloads.workloadId) -ccontains $workloadId) {
            return Invoke-Scenario `
                -Run $Run `
                -Environment $Environment `
                -Cpp $Cpp `
                -ScenarioId $ScenarioId `
                -WorkloadId $workloadId
        }
    }
    if ($ScenarioId -ceq "admission-capacity") {
        return Invoke-AdmissionCapacity -Run $Run -Environment $Environment
    }
    $policy = Get-Content `
        -LiteralPath (Join-Path $CorpusRoot "lifecycle-security.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    $securityPrefix = "security-"
    $securityCase = if ($ScenarioId.StartsWith(
        $securityPrefix,
        [StringComparison]::Ordinal
    )) {
        $ScenarioId.Substring($securityPrefix.Length)
    }
    else {
        $ScenarioId
    }
    if (@($policy.securityCases) -ccontains $securityCase) {
        return Invoke-Scenario `
            -Run $Run `
            -Environment $Environment `
            -Cpp $Cpp `
            -ScenarioId $securityCase `
            -SecurityMode
    }
    $lifecyclePrefix = "lifecycle-"
    $lifecycleCase = if ($ScenarioId.StartsWith(
        $lifecyclePrefix,
        [StringComparison]::Ordinal
    )) {
        $ScenarioId.Substring($lifecyclePrefix.Length)
    }
    else {
        $ScenarioId
    }
    if (@($policy.lifecycleCases) -ccontains $lifecycleCase) {
        $owner = [string]$policy.lifecycleOwners.PSObject.Properties[
            $lifecycleCase
        ].Value
        switch ($owner) {
            "public-protocol" {
                return Invoke-BusinessLifecycle `
                    -Run $Run `
                    -Environment $Environment `
                    -CaseId $lifecycleCase
            }
            "process-supervisor" {
                return Invoke-ProcessLifecycle `
                    -Run $Run `
                    -Environment $Environment `
                    -CaseId $lifecycleCase
            }
            "fault-gateway" {
                return Invoke-Scenario `
                    -Run $Run `
                    -Environment $Environment `
                    -Cpp $Cpp `
                    -ScenarioId $lifecycleCase `
                    -GatewayLifecycleMode
            }
            default {
                throw "battle qualification lifecycle owner is unsupported"
            }
        }
    }
    throw "battle qualification diagnostic scenario is not registered"
}

# Invoke-FaultRun 为 diagnose 或完整 12-scenario verify 建立一个独立 run owner。
function Invoke-FaultRun {
    param(
        [Parameter(Mandatory = $true)][bool]$DiagnosticOnly,
        [Parameter(Mandatory = $true)][string[]]$Scenarios
    )
    $run = New-RunDirectory
    $environment = $null
    $results = @()
    $identity = $null
    $cleanup = New-FailedCleanupEvidence
    try {
        Invoke-Build `
            -RunDirectory $run.Directory `
            -UseDiagnosticCache:$DiagnosticOnly
        $cpp = Get-CppIdentity
        $identity = Get-RunIdentity -RunDirectory $run.Directory -Cpp $cpp
        $environment = Start-BattleQualificationEnvironment `
            -RepositoryRoot $RepositoryRoot `
            -RunDirectory $run.Directory `
            -RunId $run.Id `
            -ServerBinary (Join-Path $run.Directory "server.exe") `
            -QualificationTool (Join-Path $run.Directory "qualificationtool.exe") `
            -Deadline $Deadline
        if ($DiagnosticOnly) {
            if ($Scenarios.Count -ne 1) {
                throw "battle qualification diagnose requires one scenario"
            }
            $results += Invoke-DiagnosticScenario `
                -Run $run `
                -Environment $environment `
                -Cpp $cpp `
                -ScenarioId $Scenarios[0]
        }
        else {
            for ($index = 0; $index -lt $Scenarios.Count; $index++) {
                $results += Invoke-Scenario `
                    -Run $run -Environment $environment -Cpp $cpp `
                    -ScenarioId $Scenarios[$index]
                if ($index -lt $Scenarios.Count - 1) {
                    Restart-BattleQualificationServer `
                        -Environment $environment `
                        -Deadline $Deadline
                }
            }
        }
        if (-not $DiagnosticOnly) {
            foreach ($workloadId in @(
                "solo-owner",
                "default-capacity",
                "qualified-capacity"
            )) {
                Restart-BattleQualificationServer `
                    -Environment $environment `
                    -Deadline $Deadline
                $results += Invoke-Scenario `
                    -Run $run -Environment $environment -Cpp $cpp `
                    -ScenarioId ("capacity-" + $workloadId) `
                    -WorkloadId $workloadId
            }
            Restart-BattleQualificationServer `
                -Environment $environment `
                -Deadline $Deadline
            $results += Invoke-AdmissionCapacity `
                -Run $run `
                -Environment $environment
            $results += Invoke-SecurityMatrix `
                -Run $run `
                -Environment $environment `
                -Cpp $cpp
            $results += Invoke-PublicLifecycleMatrix `
                -Run $run `
                -Environment $environment
            $results += Invoke-ProcessLifecycleMatrix `
                -Run $run `
                -Environment $environment
            $results += Invoke-GatewayLifecycleMatrix `
                -Run $run `
                -Environment $environment `
                -Cpp $cpp
            Invoke-RegressionGate
        }
        $cleanup = Stop-BattleQualificationEnvironment `
            -RepositoryRoot $RepositoryRoot `
            -Environment $environment
        $environment = $null
        Write-RunIndex `
            -Run $run `
            -Kind $(if ($DiagnosticOnly) { "diagnostic" } else { "verify" }) `
            -Identity $identity `
            -Results $results `
            -Cleanup $cleanup
        Write-Host "RunId=$($run.Id)"
    }
    finally {
        if ($environment) {
            try {
                $cleanup = Stop-BattleQualificationEnvironment `
                    -RepositoryRoot $RepositoryRoot `
                    -Environment $environment
            }
            catch {
                $cleanup = New-FailedCleanupEvidence
            }
        }
        if (-not (Test-Path -LiteralPath (Join-Path $run.Directory "run.json"))) {
            Write-RunIndex `
                -Run $run `
                -Kind $(if ($DiagnosticOnly) { "diagnostic" } else { "verify" }) `
                -Identity $identity `
                -Results $results `
                -Cleanup $cleanup
        }
    }
}

# Resolve-RunDirectory 只接受 local root 下显式 run identity，不自动选择“最新”证据。
function Resolve-RunDirectory {
    param([Parameter(Mandatory = $true)][string]$RunId)
    if ($RunId -notmatch '^bqrun_[0-9a-f]{32}$') {
        throw "battle qualification finalize run identity invalid"
    }
    return Join-Path $LocalRoot $RunId
}

# Invoke-Finalize 执行 identity、coverage、digest 与 cleanup completeness gate。
function Invoke-Finalize {
    $runADirectory = Resolve-RunDirectory -RunId $RunA
    $runBDirectory = Resolve-RunDirectory -RunId $RunB
    $soakDirectory = Resolve-RunDirectory -RunId $SoakRun
    $evidenceDirectory = Join-Path $RepositoryRoot `
        "shared\contracts\evidence\battle-network"
    [void](New-Item -ItemType Directory -Path $evidenceDirectory -Force)
    $reportPath = Join-Path $evidenceDirectory "qualification-report.json"
    $overlayPath = Join-Path $evidenceDirectory `
        "battle-network-profile-implementation-overlay.json"
    Invoke-Native -FilePath "powershell.exe" -Arguments @(
        "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $GoTool,
        "run", "./cmd/battlequalificationtool", "finalize",
        "-repository-root", $RepositoryRoot,
        "-run-a", $runADirectory,
        "-run-b", $runBDirectory,
        "-soak-run", $soakDirectory,
        "-report", $reportPath,
        "-overlay", $overlayPath
    ) -Category "finalize"
    if (-not (Test-Json `
        -LiteralPath $reportPath `
        -SchemaFile (Join-Path $CorpusRoot "report.schema.json") `
        -ErrorAction Stop)) {
        throw "battle qualification final report schema failed"
    }
    $reportText = Get-Content -LiteralPath $reportPath -Raw -Encoding utf8
    if ($reportText -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
        throw "battle qualification final report crossed low-sensitive boundary"
    }
    $report = $reportText | ConvertFrom-Json
    if (
        [string]$report.qualification -ceq
            "battle-network-qualified-windows-x64-controlled"
    ) {
        if (-not (Test-Json `
            -LiteralPath $overlayPath `
            -SchemaFile (Join-Path $CorpusRoot "profile-overlay.schema.json") `
            -ErrorAction Stop)) {
            throw "battle qualification profile overlay schema failed"
        }
        $overlayText = Get-Content -LiteralPath $overlayPath -Raw -Encoding utf8
        if ($overlayText -match '(?i)(btk1_|bts1_|playerId|remoteEndpoint|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
            throw "battle qualification profile overlay crossed low-sensitive boundary"
        }
    }
    elseif (Test-Path -LiteralPath $overlayPath) {
        throw "not-qualified report left a stale profile overlay"
    }
}

Invoke-EntryGate -AllowSourceDrift:($Action -in @("validate", "diagnose"))
switch ($Action) {
    "validate" {
        Invoke-RegressionGate -AllowSourceDrift
        Write-Stage "validation passed"
    }
    "diagnose" {
        Invoke-FaultRun -DiagnosticOnly $true -Scenarios @($Scenario)
    }
    "verify" {
        $execution = Get-Content `
            -LiteralPath (Join-Path $CorpusRoot "fault-execution.json") `
            -Raw -Encoding utf8 | ConvertFrom-Json
        Invoke-FaultRun -DiagnosticOnly $false -Scenarios @($execution.scenarios.scenarioId)
    }
    "soak" {
        Invoke-SoakRun
    }
    "finalize" {
        Invoke-Finalize
    }
}
