# 该入口为 client-v1 soak/operator 组合隔离 storage、当前 Go server 与一次性测试账号。
[CmdletBinding()]
param(
    # Action 选择五分钟 soak 或双 Player operator 矩阵。
    [ValidateSet("soak", "operator")]
    [string]$Action = "soak",

    # RunId 必须来自已成功完成 automatic 的 client-v1 资格运行。
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[0-9a-f]{32}$')]
    [string]$RunId,

    # UnityEditorPath 必须匹配 ProjectVersion 锁定版本。
    [Parameter(Mandatory = $true)]
    [string]$UnityEditorPath,

    # TimeoutSeconds 覆盖 server build、环境启动与既有五分钟 soak。
    [ValidateRange(600, 1800)]
    [int]$TimeoutSeconds = 1200
)

$ErrorActionPreference = "Stop"
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$RunDirectory = Join-Path $RepositoryRoot ".local\client-qualification\$RunId"
$StorageTool = Join-Path $RepositoryRoot "tools\storage\storage.ps1"
$GoTool = Join-Path $RepositoryRoot "tools\go\go.ps1"
$QualificationTool = Join-Path $PSScriptRoot "client-qualification.ps1"
$PowerShell7Path = ""
$ServerConfig = Join-Path $RepositoryRoot "server\config\local.yaml"
$ServerRuntimeConfig = Join-Path $RunDirectory "local-qualification-server.yaml"
$ServerBinary = Join-Path $RunDirectory "server.exe"
$PublicApiPort = 8080
$DiagnosticPort = 8081
$GameplayPort = 8444
$BattleUdpPort = 58445
$ServerReadyTimeout = [TimeSpan]::FromSeconds(60)
$ServerStopTimeout = [TimeSpan]::FromSeconds(30)
$OperatorTimeout = [TimeSpan]::FromMinutes(10)
$OperatorPollInterval = [TimeSpan]::FromMilliseconds(250)
$CredentialBytes = 24
# 24 随机 bytes 编码为 32 个 ASCII Base64 bytes，满足 battle 精确长度契约。
$DerivationKeySourceBytes = 24
$SimulationInstanceCapacity = 64
$SimulationActorCapacity = 8
$SimulationFrameBytes = 65536
$SimulationPendingRequests = 256
$SimulationRequestTimeout = "10s"
$SimulationHealthInterval = "5s"
$SimulationHealthTimeout = "1s"
$SimulationDrainTimeout = "3s"
$SimulationShutdownTimeout = "5s"
$SimulationStandardErrorLineBytes = 1024
$SimulationQualificationSampleInterval = "1s"
$StorageRunId = ""
$ServerProcess = $null
$OwnedPlayerProcesses =
    [Collections.Generic.List[Diagnostics.Process]]::new()
$PrimaryFailure = $null
$CleanupFailures = [Collections.Generic.List[string]]::new()
$EnvironmentNames = @(
    "IHOMELAND_MYSQL_ADDRESS",
    "IHOMELAND_REDIS_ADDRESS",
    "IHOMELAND_MYSQL_PASSWORD",
    "IHOMELAND_REDIS_PASSWORD",
    "IHOMELAND_WORLD_ADMISSION_KEY",
    "IHOMELAND_BATTLE_DERIVATION_KEY",
    "IHOMELAND_QUALIFICATION_USERNAME",
    "IHOMELAND_QUALIFICATION_PASSWORD",
    "PSModulePath"
)
$PreviousEnvironment = @{}
foreach ($name in $EnvironmentNames) {
    $PreviousEnvironment[$name] =
        [Environment]::GetEnvironmentVariable($name, "Process")
}
$windowsPowerShellModuleRoot =
    Join-Path $env:WINDIR "System32\WindowsPowerShell\v1.0\Modules"
$moduleEntries = @($env:PSModulePath -split ';' | Where-Object {
        -not [string]::IsNullOrWhiteSpace($_) -and
        -not [string]::Equals(
            $_.TrimEnd('\'),
            $windowsPowerShellModuleRoot.TrimEnd('\'),
            [StringComparison]::OrdinalIgnoreCase)
    })
# Windows PowerShell 5.1 必须优先加载自身系统 module，不能误载父级 PowerShell 7 同名 module。
$env:PSModulePath = (@($windowsPowerShellModuleRoot) + $moduleEntries) -join ';'
$PowerShell7Path = @(
    Get-Command pwsh.exe -CommandType Application -ErrorAction Stop
)[0].Source
if (-not [IO.Path]::IsPathRooted($PowerShell7Path) -or
    -not (Test-Path -LiteralPath $PowerShell7Path -PathType Leaf)) {
    throw "client-v1 qualification PowerShell 7 host is missing"
}

# Quote-YamlSingle 把 run-local absolute path 编码为 YAML single quoted scalar。
function Quote-YamlSingle {
    param([Parameter(Mandatory = $true)][string]$Value)

    return "'" + $Value.Replace("'", "''") + "'"
}

# Get-LowerSha256 返回文件原始 bytes 的 lowercase SHA-256。
function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    $algorithm = [Security.Cryptography.SHA256]::Create()
    $stream = $null
    try {
        $stream = [IO.File]::OpenRead($Path)
        return [BitConverter]::ToString(
            $algorithm.ComputeHash($stream)
        ).Replace("-", "").ToLowerInvariant()
    }
    finally {
        if ($null -ne $stream) {
            $stream.Dispose()
        }
        $algorithm.Dispose()
    }
}

# New-RandomBase64 返回由系统 CSPRNG 生成的指定长度 secret。
function New-RandomBase64 {
    param([Parameter(Mandatory = $true)][int]$ByteCount)

    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $bytes = [byte[]]::new($ByteCount)
        $generator.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    }
    finally {
        $generator.Dispose()
    }
}

# Write-RunLocalServerConfig 绑定当前 B0.3 证据并启用唯一 simulation child。
function Write-RunLocalServerConfig {
    $simulationRoot =
        Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci"
    $simulationBinary =
        Join-Path $simulationRoot "ihomeland-sim-server.exe"
    $simulationReceipt =
        Join-Path $simulationRoot "qualification-gate-receipt.json"
    $simulationIdentityPath =
        Join-Path $simulationRoot "ihomeland-build-identity.json"
    $modelManifest =
        Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model\manifest.json"
    $profileManifest =
        Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\manifest.json"
    $controlConfig =
        Join-Path $RepositoryRoot "shared\contracts\fixtures\simulation-control\runtime\config\control-baseline-v1.json"
    $controlNavigation =
        Join-Path $RepositoryRoot "shared\contracts\fixtures\simulation-control\runtime\navigation\control-baseline-v1.json"
    $controlPhysics =
        Join-Path $RepositoryRoot "shared\contracts\fixtures\simulation-control\runtime\physics\control-baseline-v1.json"
    foreach ($path in @(
        $simulationBinary,
        $simulationReceipt,
        $simulationIdentityPath,
        $modelManifest,
        $profileManifest,
        $controlConfig,
        $controlNavigation,
        $controlPhysics
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "client-v1 local soak simulation prerequisite is missing"
        }
    }
    $source = Get-Content -LiteralPath $ServerConfig -Raw -Encoding utf8
    if ($source -match '(?m)^simulationControl:\s*$') {
        throw "client-v1 local config already owns simulationControl"
    }
    $identity =
        Get-Content -LiteralPath $simulationIdentityPath -Raw -Encoding utf8 |
            ConvertFrom-Json
    if ([string]$identity.target_identity -notmatch '^[0-9a-f]{64}$') {
        throw "client-v1 local soak simulation identity is invalid"
    }
    $control = @"
simulationControl:
  enabled: true
  binaryPath: $(Quote-YamlSingle $simulationBinary)
  binarySha256: $(Get-LowerSha256 $simulationBinary)
  qualificationReceiptPath: $(Quote-YamlSingle $simulationReceipt)
  qualificationReceiptSha256: $(Get-LowerSha256 $simulationReceipt)
  buildIdentity: $([string]$identity.target_identity)
  modelManifest: $(Get-LowerSha256 $modelManifest)
  profileManifest: $(Get-LowerSha256 $profileManifest)
  configIdentity: $(Get-LowerSha256 $controlConfig)
  navigationIdentity: $(Get-LowerSha256 $controlNavigation)
  physicsIdentity: $(Get-LowerSha256 $controlPhysics)
  instanceCapacity: $SimulationInstanceCapacity
  actorCapacity: $SimulationActorCapacity
  frameBytes: $SimulationFrameBytes
  pendingRequests: $SimulationPendingRequests
  requestTimeout: $SimulationRequestTimeout
  healthInterval: $SimulationHealthInterval
  healthTimeout: $SimulationHealthTimeout
  drainTimeout: $SimulationDrainTimeout
  shutdownTimeout: $SimulationShutdownTimeout
  stderrLineBytes: $SimulationStandardErrorLineBytes
  qualificationMode: true
  qualificationRunId: bqrun_$RunId
  qualificationSampleInterval: $SimulationQualificationSampleInterval
"@
    [IO.File]::WriteAllText(
        $ServerRuntimeConfig,
        $source.TrimEnd() + "`n" + $control,
        [Text.UTF8Encoding]::new($false)
    )
}

# Assert-ListenerPortsAvailable 拒绝复用操作者或其他资格运行持有的固定端口。
function Assert-ListenerPortsAvailable {
    $tcpConflicts = @(
        Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
            Where-Object {
                [int]$_.LocalPort -in @(
                    $PublicApiPort,
                    $DiagnosticPort,
                    $GameplayPort
                )
            }
    )
    $udpConflicts = @(
        Get-NetUDPEndpoint -ErrorAction SilentlyContinue |
            Where-Object { [int]$_.LocalPort -eq $BattleUdpPort }
    )
    if ($tcpConflicts.Count -ne 0 -or $udpConflicts.Count -ne 0) {
        throw "client-v1 local soak fixed listener port is already owned"
    }
}

# Wait-ServerReady 只通过公开 version endpoint 判断 Composition Root 已就绪。
function Wait-ServerReady {
    $deadline = [DateTime]::UtcNow.Add($ServerReadyTimeout)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($ServerProcess.HasExited) {
            throw "client-v1 local soak server exited during startup"
        }
        try {
            $response = Invoke-WebRequest `
                -Uri "http://127.0.0.1:$PublicApiPort/v1/version" `
                -TimeoutSec 2 `
                -UseBasicParsing
            if ($response.StatusCode -eq 200) {
                return
            }
        }
        catch {
            # Composition Root 启动期间连接拒绝是可观察的临时状态。
        }
        Start-Sleep -Milliseconds 250
    }
    throw "client-v1 local soak server readiness deadline elapsed"
}

# Start-LocalServer 启动当前 Go binary，并按 incarnation 隔离日志。
function Start-LocalServer {
    param([Parameter(Mandatory = $true)][string]$Incarnation)

    $script:ServerProcess = Start-Process `
        -FilePath $ServerBinary `
        -ArgumentList @("--config", $ServerRuntimeConfig) `
        -WorkingDirectory $RepositoryRoot `
        -WindowStyle Hidden `
        -RedirectStandardOutput (
            Join-Path $RunDirectory "local-$Incarnation-server.stdout.log"
        ) `
        -RedirectStandardError (
            Join-Path $RunDirectory "local-$Incarnation-server.stderr.log"
        ) `
        -PassThru
    Wait-ServerReady
}

# Stop-LocalServer 只终止当前 helper 记录的精确 server PID。
function Stop-LocalServer {
    if ($null -eq $ServerProcess -or $ServerProcess.HasExited) {
        return
    }
    Stop-Process -Id $ServerProcess.Id -Force -ErrorAction Stop
    if (-not $ServerProcess.WaitForExit(
        [int]$ServerStopTimeout.TotalMilliseconds
    )) {
        throw "client-v1 local server stop deadline elapsed"
    }
}

# Register-QualificationAccount 通过公开 API 创建本 run 唯一账号并返回内存 credential。
function Register-QualificationAccount {
    $username =
        "qual" + [Guid]::NewGuid().ToString("N").Substring(0, 20)
    $password =
        New-RandomBase64 -ByteCount $CredentialBytes
    $body = @{
        username = $username
        password = $password
        displayName = "资格测试玩家"
    } | ConvertTo-Json -Compress
    try {
        $response = Invoke-WebRequest `
            -Uri "http://127.0.0.1:$PublicApiPort/v1/auth/register" `
            -Method Post `
            -ContentType "application/json" `
            -Body $body `
            -TimeoutSec 15 `
            -UseBasicParsing
        if ($response.StatusCode -ne 201) {
            throw "client-v1 local account registration was rejected"
        }
        return [pscustomobject]@{
            Username = $username
            Password = $password
        }
    }
    finally {
        $body = $null
        $response = $null
    }
}

# Set-QualificationCredential 只在启动目标 Player 前设置继承环境。
function Set-QualificationCredential {
    param([Parameter(Mandatory = $true)]$Credential)

    $env:IHOMELAND_QUALIFICATION_USERNAME =
        [string]$Credential.Username
    $env:IHOMELAND_QUALIFICATION_PASSWORD =
        [string]$Credential.Password
}

# Clear-QualificationCredential 避免后续 child 继承不属于自己的账号。
function Clear-QualificationCredential {
    $env:IHOMELAND_QUALIFICATION_USERNAME = $null
    $env:IHOMELAND_QUALIFICATION_PASSWORD = $null
}

# Get-DevelopmentPlayer 返回 automatic 生成的唯一 Development Player。
function Get-DevelopmentPlayer {
    $players = @(
        Get-ChildItem `
            -LiteralPath (Join-Path $RunDirectory "development") `
            -Filter "iHomeland.exe" `
            -File `
            -Recurse
    )
    if ($players.Count -ne 1) {
        throw "client-v1 local operator Development Player is missing or ambiguous"
    }
    return $players[0].FullName
}

# Start-OperatorPlayer 启动一个隔离 role/profile 的 headless Development Player。
function Start-OperatorPlayer {
    param(
        [Parameter(Mandatory = $true)][string]$PlayerPath,
        [Parameter(Mandatory = $true)][string]$Role,
        [Parameter(Mandatory = $true)][string]$Scenario,
        [Parameter(Mandatory = $true)][string]$CoordinationRoot,
        [Parameter(Mandatory = $true)][string]$LogSuffix,
        $Credential
    )

    if ($null -ne $Credential) {
        Set-QualificationCredential -Credential $Credential
    }
    else {
        Clear-QualificationCredential
    }
    try {
        $process = Start-Process `
            -FilePath $PlayerPath `
            -ArgumentList @(
                "-batchmode",
                "-nographics",
                "-logFile",
                (Join-Path $RunDirectory "operator-$LogSuffix.player.log"),
                "-ihomelandDataProfile",
                "qualification-$Role",
                "-ihomelandQualificationStorageRoot",
                (Join-Path $RunDirectory "manual-player-storage"),
                "-ihomelandQualificationMode",
                "two-player-operator",
                "-ihomelandQualificationRole",
                $Role,
                "-ihomelandQualificationScenario",
                $Scenario,
                "-ihomelandQualificationCoordinationRoot",
                $CoordinationRoot
            ) `
            -WorkingDirectory $RunDirectory `
            -WindowStyle Hidden `
            -PassThru
        $OwnedPlayerProcesses.Add($process)
        return $process
    }
    finally {
        Clear-QualificationCredential
    }
}

# Stop-OperatorPlayer 只终止等待显式 replacement 的精确 Player PID。
function Stop-OperatorPlayer {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process)

    if ($Process.HasExited) {
        throw "client-v1 operator Player exited before replacement"
    }
    Stop-Process -Id $Process.Id -Force -ErrorAction Stop
    if (-not $Process.WaitForExit(
        [int]$ServerStopTimeout.TotalMilliseconds
    )) {
        throw "client-v1 operator Player replacement deadline elapsed"
    }
}

# Wait-OperatorSignals 等待闭合 signal 集，并在任一 Player 提前退出时失败。
function Wait-OperatorSignals {
    param(
        [Parameter(Mandatory = $true)][string]$CoordinationRoot,
        [Parameter(Mandatory = $true)][string[]]$Names,
        [Parameter(Mandatory = $true)][Diagnostics.Process[]]$Processes
    )

    $deadline = [DateTime]::UtcNow.Add($OperatorTimeout)
    while ([DateTime]::UtcNow -lt $deadline) {
        $missing = @(
            $Names | Where-Object {
                -not (
                    Test-Path `
                        -LiteralPath (Join-Path $CoordinationRoot "$_.signal") `
                        -PathType Leaf
                )
            }
        )
        if ($missing.Count -eq 0) {
            return
        }
        foreach ($process in $Processes) {
            if ($process.HasExited) {
                throw "client-v1 operator Player exited before signal"
            }
        }
        Start-Sleep -Milliseconds (
            [int]$OperatorPollInterval.TotalMilliseconds
        )
    }
    throw "client-v1 operator signal deadline elapsed"
}

# Wait-OperatorPlayers 等待全部 Player 正常退出并验证稳定 pass marker。
function Wait-OperatorPlayers {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process[]]$Processes,
        [Parameter(Mandatory = $true)][string[]]$LogSuffixes
    )

    $deadline = [DateTime]::UtcNow.Add($OperatorTimeout)
    while ([DateTime]::UtcNow -lt $deadline -and
        @($Processes | Where-Object { -not $_.HasExited }).Count -ne 0) {
        Start-Sleep -Milliseconds (
            [int]$OperatorPollInterval.TotalMilliseconds
        )
    }
    for ($index = 0; $index -lt $Processes.Count; $index++) {
        $process = $Processes[$index]
        if (-not $process.HasExited -or $process.ExitCode -ne 0) {
            throw "client-v1 operator Player did not exit successfully"
        }
        $logPath =
            Join-Path $RunDirectory "operator-$($LogSuffixes[$index]).player.log"
        if (-not (
            Select-String `
                -LiteralPath $logPath `
                -SimpleMatch `
                "[IHOMELAND_QUALIFICATION] outcome=pass scenario=two-player-operator" `
                -Quiet
        )) {
            throw "client-v1 operator Player pass marker is missing"
        }
    }
}

# Write-OperatorSignal 只写闭合低敏协调事实。
function Write-OperatorSignal {
    param(
        [Parameter(Mandatory = $true)][string]$CoordinationRoot,
        [Parameter(Mandatory = $true)][string]$Name
    )

    [IO.File]::WriteAllText(
        (Join-Path $CoordinationRoot "$Name.signal"),
        "pass",
        [Text.Encoding]::ASCII
    )
}

# Invoke-ProductFaultOperator 驱动产品流、分通道故障和两个 Player replacement。
function Invoke-ProductFaultOperator {
    param(
        [Parameter(Mandatory = $true)][string]$PlayerPath,
        [Parameter(Mandatory = $true)]$OwnerCredential,
        [Parameter(Mandatory = $true)]$VisitorCredential,
        [Parameter(Mandatory = $true)][string]$AttemptRoot
    )

    $attemptId = Split-Path -Leaf $AttemptRoot
    $root = Join-Path $AttemptRoot "product-fault"
    [IO.Directory]::CreateDirectory($root) | Out-Null
    $owner = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "owner" `
        -Scenario "product-fault" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-product-owner-initial" `
        -Credential $OwnerCredential
    $visitor = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "visitor" `
        -Scenario "product-fault" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-product-visitor-initial" `
        -Credential $VisitorCredential
    Wait-OperatorSignals `
        -CoordinationRoot $root `
        -Names @("ready-owner-restart", "ready-visitor-restart") `
        -Processes @($owner, $visitor)
    Stop-OperatorPlayer -Process $owner
    Stop-OperatorPlayer -Process $visitor
    Write-OperatorSignal -CoordinationRoot $root -Name "resume-owner"
    Write-OperatorSignal -CoordinationRoot $root -Name "resume-visitor"
    $owner = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "owner" `
        -Scenario "product-fault" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-product-owner-resumed"
    $visitor = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "visitor" `
        -Scenario "product-fault" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-product-visitor-resumed"
    Wait-OperatorPlayers `
        -Processes @($owner, $visitor) `
        -LogSuffixes @(
            "$attemptId-product-owner-resumed",
            "$attemptId-product-visitor-resumed"
        )
    if (-not (
        Test-Path `
            -LiteralPath (Join-Path $root "product-fault-pass.signal") `
            -PathType Leaf
    )) {
        throw "client-v1 product operator completion signal is missing"
    }
}

# Invoke-PlayerProfileCleanup 让 secure store owner 清理两个 operator profile。
function Invoke-PlayerProfileCleanup {
    param(
        [Parameter(Mandatory = $true)][string]$PlayerPath,
        [Parameter(Mandatory = $true)][string]$AttemptId
    )

    foreach ($role in @("owner", "visitor")) {
        $logPath =
            Join-Path $RunDirectory "operator-$AttemptId-pre-restart-cleanup-$role.player.log"
        $process = Start-Process `
            -FilePath $PlayerPath `
            -ArgumentList @(
                "-batchmode",
                "-nographics",
                "-logFile",
                $logPath,
                "-ihomelandDataProfile",
                "qualification-$role",
                "-ihomelandQualificationStorageRoot",
                (Join-Path $RunDirectory "manual-player-storage"),
                "-ihomelandQualificationMode",
                "cleanup"
            ) `
            -WorkingDirectory $RunDirectory `
            -WindowStyle Hidden `
            -PassThru
        $OwnedPlayerProcesses.Add($process)
        if (-not $process.WaitForExit(
            [int]$OperatorTimeout.TotalMilliseconds
        ) -or $process.ExitCode -ne 0 -or
            -not (
                Select-String `
                    -LiteralPath $logPath `
                    -SimpleMatch `
                    "[IHOMELAND_QUALIFICATION] outcome=pass scenario=storage-cleanup" `
                    -Quiet
            )) {
            throw "client-v1 operator profile cleanup failed"
        }
    }
}

# Invoke-ServerRestartOperator 驱动真实 server replacement 与双 Player 权威收敛。
function Invoke-ServerRestartOperator {
    param(
        [Parameter(Mandatory = $true)][string]$PlayerPath,
        [Parameter(Mandatory = $true)]$OwnerCredential,
        [Parameter(Mandatory = $true)]$VisitorCredential,
        [Parameter(Mandatory = $true)][string]$AttemptRoot
    )

    $attemptId = Split-Path -Leaf $AttemptRoot
    Invoke-PlayerProfileCleanup `
        -PlayerPath $PlayerPath `
        -AttemptId $attemptId
    $root = Join-Path $AttemptRoot "server-restart"
    [IO.Directory]::CreateDirectory($root) | Out-Null
    $owner = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "owner" `
        -Scenario "server-restart" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-restart-owner" `
        -Credential $OwnerCredential
    $visitor = Start-OperatorPlayer `
        -PlayerPath $PlayerPath `
        -Role "visitor" `
        -Scenario "server-restart" `
        -CoordinationRoot $root `
        -LogSuffix "$attemptId-restart-visitor" `
        -Credential $VisitorCredential
    Wait-OperatorSignals `
        -CoordinationRoot $root `
        -Names @("ready-server-stop") `
        -Processes @($owner, $visitor)
    Stop-LocalServer
    Write-OperatorSignal -CoordinationRoot $root -Name "server-offline"
    Wait-OperatorSignals `
        -CoordinationRoot $root `
        -Names @("owner-offline-pass", "visitor-offline-pass") `
        -Processes @($owner, $visitor)
    Start-LocalServer -Incarnation "operator-replacement"
    Write-OperatorSignal -CoordinationRoot $root -Name "server-online"
    Wait-OperatorPlayers `
        -Processes @($owner, $visitor) `
        -LogSuffixes @(
            "$attemptId-restart-owner",
            "$attemptId-restart-visitor"
        )
    if (-not (
        Test-Path `
            -LiteralPath (Join-Path $root "server-restart-pass.signal") `
            -PathType Leaf
    )) {
        throw "client-v1 server restart operator completion signal is missing"
    }
}

# Add-OperatorEvidence 只在三个真实 operator 场景全部通过后原子替换正式 automatic evidence。
function Add-OperatorEvidence {
    $evidencePath = Join-Path $RunDirectory "automatic-evidence.json"
    $evidence =
        Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8 |
            ConvertFrom-Json
    $scenarioIds = @(
        "two-player-product-flow",
        "two-player-channel-faults",
        "two-player-server-restart"
    )
    if (@(
        $evidence.records |
            Where-Object { $_.scenarioId -in $scenarioIds }
    ).Count -ne 0) {
        throw "client-v1 operator evidence already exists"
    }
    $records = [Collections.Generic.List[object]]::new()
    foreach ($record in @($evidence.records)) {
        $records.Add($record)
    }
    foreach ($scenarioId in $scenarioIds) {
        $records.Add([ordered]@{
            scenarioId = $scenarioId
            outcome = "pass"
            evidenceType = "operator"
            completedAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
        })
    }
    $body = [ordered]@{
        schemaVersion = [int]$evidence.schemaVersion
        qualificationVersion = [string]$evidence.qualificationVersion
        contractDigest = [string]$evidence.contractDigest
        buildDigests = [ordered]@{
            development = [string]$evidence.buildDigests.development
            release = [string]$evidence.buildDigests.release
        }
        records = @($records)
    }
    $temporaryPath = $evidencePath + ".operator"
    [IO.File]::WriteAllText(
        $temporaryPath,
        ($body | ConvertTo-Json -Depth 8) + "`n",
        [Text.UTF8Encoding]::new($false)
    )
    Move-Item `
        -LiteralPath $temporaryPath `
        -Destination $evidencePath `
        -Force
}

if (-not (Test-Path -LiteralPath $RunDirectory -PathType Container)) {
    throw "client-v1 qualification run directory is missing"
}
if (-not (Test-Path -LiteralPath $ServerConfig -PathType Leaf)) {
    throw "client-v1 local server config is missing"
}

try {
    Assert-ListenerPortsAvailable
    $storageOutput = @(
        & powershell.exe `
            -NoProfile `
            -ExecutionPolicy Bypass `
            -File $StorageTool `
            -Action up `
            -TimeoutSeconds 300 2>&1
    )
    if ($LASTEXITCODE -ne 0 -or
        ($storageOutput -join "`n") -notmatch
            'RunId=([0-9a-f]{32})') {
        throw "client-v1 local soak storage startup failed"
    }
    $StorageRunId = $Matches[1]
    $storageState = (
        & powershell.exe `
            -NoProfile `
            -ExecutionPolicy Bypass `
            -File $StorageTool `
            -Action status `
            -RunId $StorageRunId |
            Out-String
    ) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) {
        throw "client-v1 local soak storage status failed"
    }

    Push-Location (Join-Path $RepositoryRoot "server")
    try {
        # GoTool 是以 exit 返回 CLI 状态的进程入口，必须隔离运行，
        # 否则会提前展开当前资格进程并破坏后续 cmdlet module autoload。
        $goBuildArguments = @(
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-o",
            $ServerBinary,
            "./cmd/server"
        )
        $quotedGoBuildArguments = @(
            $goBuildArguments |
                ForEach-Object { "'" + $_.Replace("'", "''") + "'" }
        ) -join ","
        $goBuildCommand =
            '$ProgressPreference = ''SilentlyContinue''; ' +
            '$env:IHOMELAND_GO_NESTED = ''1''; ' +
            "& '" + $GoTool.Replace("'", "''") +
            "' -GoArguments @(" + $quotedGoBuildArguments + ")"
        $encodedGoBuildCommand = [Convert]::ToBase64String(
            [Text.Encoding]::Unicode.GetBytes($goBuildCommand))
        & powershell.exe `
            -NoProfile `
            -NonInteractive `
            -ExecutionPolicy Bypass `
            -OutputFormat Text `
            -EncodedCommand $encodedGoBuildCommand
        if ($LASTEXITCODE -ne 0) {
            throw "client-v1 local server build failed"
        }
    }
    finally {
        Pop-Location
    }

    $storageDirectory =
        Join-Path $RepositoryRoot ".local\storage\$StorageRunId"
    $env:IHOMELAND_MYSQL_ADDRESS =
        "127.0.0.1:$([int]$storageState.mysqlPort)"
    $env:IHOMELAND_REDIS_ADDRESS =
        "127.0.0.1:$([int]$storageState.redisPort)"
    $env:IHOMELAND_MYSQL_PASSWORD =
        [IO.File]::ReadAllText(
            (Join-Path $storageDirectory "mysql-password")
        ).Trim()
    $env:IHOMELAND_REDIS_PASSWORD =
        [IO.File]::ReadAllText(
            (Join-Path $storageDirectory "redis-password")
        ).Trim()
    $env:IHOMELAND_WORLD_ADMISSION_KEY =
        New-RandomBase64 -ByteCount $DerivationKeySourceBytes
    $env:IHOMELAND_BATTLE_DERIVATION_KEY =
        New-RandomBase64 -ByteCount $DerivationKeySourceBytes

    Write-RunLocalServerConfig
    Start-LocalServer -Incarnation $Action
    if ($Action -eq "soak") {
        $credential = Register-QualificationAccount
        Set-QualificationCredential -Credential $credential
        try {
            # QualificationTool 是完整 CLI，独立进程隔离其 module 与 cleanup owner。
            $localModulePath = $env:PSModulePath
            $powerShell7ModuleRoot =
                Join-Path (Split-Path -Parent $PowerShell7Path) "Modules"
            $powerShell7ModuleEntries = @(
                $localModulePath -split ';' | Where-Object {
                    -not [string]::IsNullOrWhiteSpace($_) -and
                    -not [string]::Equals(
                        $_.TrimEnd('\'),
                        $powerShell7ModuleRoot.TrimEnd('\'),
                        [StringComparison]::OrdinalIgnoreCase)
                })
            try {
                $env:PSModulePath =
                    (@($powerShell7ModuleRoot) + $powerShell7ModuleEntries) -join ';'
                & $PowerShell7Path `
                    -NoProfile `
                    -File $QualificationTool `
                    -Action soak `
                    -UnityEditorPath $UnityEditorPath `
                    -RunId $RunId `
                    -TimeoutSeconds $TimeoutSeconds
            }
            finally {
                $env:PSModulePath = $localModulePath
            }
            if ($LASTEXITCODE -ne 0) {
                throw "client-v1 player soak qualification failed"
            }
        }
        finally {
            Clear-QualificationCredential
            $credential = $null
        }
    }
    else {
        $ownerCredential = Register-QualificationAccount
        $visitorCredential = Register-QualificationAccount
        $playerPath = Get-DevelopmentPlayer
        $operatorAttemptRoot = Join-Path `
            $RunDirectory `
            ("operator-attempts\" + [Guid]::NewGuid().ToString("N"))
        [IO.Directory]::CreateDirectory($operatorAttemptRoot) | Out-Null
        Invoke-ProductFaultOperator `
            -PlayerPath $playerPath `
            -OwnerCredential $ownerCredential `
            -VisitorCredential $visitorCredential `
            -AttemptRoot $operatorAttemptRoot
        Invoke-ServerRestartOperator `
            -PlayerPath $playerPath `
            -OwnerCredential $ownerCredential `
            -VisitorCredential $visitorCredential `
            -AttemptRoot $operatorAttemptRoot
        Add-OperatorEvidence
        $ownerCredential = $null
        $visitorCredential = $null
    }
}
catch {
    $PrimaryFailure = $_
}
finally {
    foreach ($name in $EnvironmentNames) {
        [Environment]::SetEnvironmentVariable(
            $name,
            $PreviousEnvironment[$name],
            "Process"
        )
    }
    foreach ($playerProcess in $OwnedPlayerProcesses) {
        if (-not $playerProcess.HasExited) {
            try {
                Stop-Process `
                    -Id $playerProcess.Id `
                    -Force `
                    -ErrorAction Stop
                if (-not $playerProcess.WaitForExit(
                    [int]$ServerStopTimeout.TotalMilliseconds
                )) {
                    $CleanupFailures.Add("player-timeout")
                }
            }
            catch {
                $CleanupFailures.Add("player")
            }
        }
    }
    if ($null -ne $ServerProcess -and
        -not $ServerProcess.HasExited) {
        try {
            Stop-Process -Id $ServerProcess.Id -Force -ErrorAction Stop
            if (-not $ServerProcess.WaitForExit(
                [int]$ServerStopTimeout.TotalMilliseconds
            )) {
                $CleanupFailures.Add("server-timeout")
            }
        }
        catch {
            $CleanupFailures.Add("server")
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($StorageRunId)) {
        try {
            & powershell.exe `
                -NoProfile `
                -ExecutionPolicy Bypass `
                -File $StorageTool `
                -Action down `
                -RunId $StorageRunId `
                -TimeoutSeconds 300 *> $null
            if ($LASTEXITCODE -ne 0) {
                $CleanupFailures.Add("storage")
            }
        }
        catch {
            $CleanupFailures.Add("storage")
        }
    }
}

if ($null -ne $PrimaryFailure) {
    if ($CleanupFailures.Count -ne 0) {
        throw "client-v1 local soak failed; cleanup=$($CleanupFailures -join ',')"
    }
    throw $PrimaryFailure
}
if ($CleanupFailures.Count -ne 0) {
    throw "client-v1 local soak cleanup failed: $($CleanupFailures -join ',')"
}
Write-Output "CLIENT_QUALIFICATION_LOCAL_$($Action.ToUpperInvariant())_PASS run-id=$RunId"
