#requires -Version 7.0

[CmdletBinding()]
# 该入口只编排 PersonalWorld production combat 的定向开发验收，不产生最终资格结论。
param(
    # Action 区分只读契约检查与显式真实环境运行。
    [ValidateSet("validate", "targeted")]
    [string]$Action = "validate",

    # TimeoutSeconds 是真实双 Player runner 的总 deadline，不修改产品 deadline。
    [ValidateRange(900, 14400)]
    [int]$TimeoutSeconds = 7200,

    # UnityEditorPath 只允许传给现有 Unity owner；为空时由其解析锁定环境变量。
    [string]$UnityEditorPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$PackageRoot = Join-Path $RepositoryRoot (
    "shared\contracts\gameplay\battle\packages\personal-world-combat-v1")
$GameplayTool = Join-Path $RepositoryRoot "tools\gameplay-config\gameplay-config.ps1"
$CppTool = Join-Path $RepositoryRoot "tools\cpp\cpp.ps1"
$GoTool = Join-Path $RepositoryRoot "tools\go\go.ps1"
$ProtoTool = Join-Path $RepositoryRoot "tools\proto\proto.ps1"
$ClientTool = Join-Path $RepositoryRoot (
    "tools\client-battle-runtime\client-battle-runtime.ps1")

# Invoke-OwnedStage 在独立 PowerShell 进程中调用登记 owner，并传播其退出码。
function Invoke-OwnedStage {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Label
    )

    Write-Host "[COMBAT] $Label"
    & (Get-Command pwsh.exe -ErrorAction Stop).Source `
        -NoLogo -NoProfile -File $Path @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Label 失败"
    }
}

# Get-CanonicalIdentity 读取 closed manifest 的公开 lowercase SHA-256 identity。
function Get-CanonicalIdentity {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$Property,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $value = [string]$Document.$Property
    if ($value -cnotmatch '^[0-9a-f]{64}$') {
        throw "$Label identity 非 canonical SHA-256"
    }
    return $value
}

if (-not (Test-Path -LiteralPath $PackageRoot -PathType Container)) {
    throw "PersonalWorld production combat package 缺失"
}
$manifestPath = Join-Path $PackageRoot "package.json"
$mappingPath = Join-Path $PackageRoot "wire-mapping.json"
foreach ($path in @($manifestPath, $mappingPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "PersonalWorld production combat closed document 缺失"
    }
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 |
    ConvertFrom-Json
$mapping = Get-Content -LiteralPath $mappingPath -Raw -Encoding UTF8 |
    ConvertFrom-Json
if ([string]$manifest.qualification_state -cne "production" -or
    [string]$manifest.package_id -cne "personal-world-combat-v1" -or
    [string]$mapping.qualification_state -cne "production" -or
    [string]$mapping.package_id -cne "personal-world-combat-v1") {
    throw "PersonalWorld combat package identity 或 classification 漂移"
}
$configIdentity =
    "d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b"
$wireIdentity = Get-CanonicalIdentity `
    -Document $manifest.wire_binding `
    -Property "manifest_sha256" `
    -Label "Wire"

Invoke-OwnedStage `
    -Path $GameplayTool `
    -Arguments @("validate", "-ProductionRoot", $PackageRoot) `
    -Label "production package validate"
if ($Action -eq "validate") {
    Write-Host (
        "[PASS] PersonalWorld combat contract validated: " +
        "config=$configIdentity wire=$wireIdentity")
    return
}

Invoke-OwnedStage -Path $ProtoTool -Arguments @("verify") -Label "proto parity"
Invoke-OwnedStage `
    -Path $CppTool `
    -Arguments @("configure", "-Preset", "windows-msvc-ci") `
    -Label "C++ configure"
Invoke-OwnedStage `
    -Path $CppTool `
    -Arguments @("build", "-Preset", "windows-msvc-ci") `
    -Label "C++ build"
$cppRegex = (
    '^battle\.(qualification\.protocol-client|transport\.udp-listener|' +
    'session\.ingress-replication|runtime\.metrics)$|' +
    '^simulation\.(config\.gameplay-package|arena\.personal-world|' +
    'gameplay\.production-encounter|control\.node)$')
Invoke-OwnedStage `
    -Path $CppTool `
    -Arguments @(
        "test", "-Preset", "windows-msvc-ci", "-TestRegex", $cppRegex) `
    -Label "C++ combat and real socket tests"

Push-Location (Join-Path $RepositoryRoot "server")
try {
    Invoke-OwnedStage `
        -Path $GoTool `
        -Arguments @(
            "test", "-count=1",
            "./internal/gameplaypackage/...",
            "./internal/simulationcontrol/...",
            "./internal/battlequalification/...") `
        -Label "Go selector and supervised runtime tests"
}
finally {
    Pop-Location
}

$unityArguments = @("-Action", "unity-tests")
$playerArguments = @(
    "-Action", "player-targeted",
    "-NativePreset", "windows-msvc-ci",
    "-PlayerTargetedTimeoutSeconds", [string]([Math]::Min($TimeoutSeconds, 2400)))
if (-not [string]::IsNullOrWhiteSpace($UnityEditorPath)) {
    $unityArguments += @("-UnityEditorPath", $UnityEditorPath)
    $playerArguments += @("-UnityEditorPath", $UnityEditorPath)
}
Invoke-OwnedStage `
    -Path $ClientTool `
    -Arguments $unityArguments `
    -Label "Unity combat EditMode and PlayMode"
Invoke-OwnedStage `
    -Path $ClientTool `
    -Arguments $playerArguments `
    -Label "real Go/C++/storage/two-Player scenarios"

$runID = [guid]::NewGuid().ToString("N")
$evidenceRoot = Join-Path $RepositoryRoot (
    ".local\personal-world-combat\" + $runID)
[IO.Directory]::CreateDirectory($evidenceRoot) | Out-Null
$evidence = [ordered]@{
    schemaVersion = 1
    checkId = "personal-world-combat-targeted"
    runId = $runID
    configIdentity = $configIdentity
    wireIdentity = $wireIdentity
    scenarios = @(
        "single-player-production-encounter",
        "owner-visitor-shared-authority",
        "battle-successor-and-safe-return")
    lowSensitivity = $true
    settlementConclusion = $false
    completedAt = [DateTime]::UtcNow.ToString("o")
}
[IO.File]::WriteAllText(
    (Join-Path $evidenceRoot "evidence.json"),
    (($evidence | ConvertTo-Json -Depth 8) + "`n"),
    [Text.UTF8Encoding]::new($false))
Write-Host (
    "[PASS] PersonalWorld combat targeted development evidence passed: " +
    "run-id=$runID")
