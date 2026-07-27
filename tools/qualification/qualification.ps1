# 该入口聚合 Q0 全部分层门禁，并只按精确 run-id/PID 清理本次临时资源。
[CmdletBinding()]
param(
    # Action 选择完整资格运行或不创建业务 listener 的工具契约检查。
    [ValidateSet("verify", "contract", "blackbox")]
    [string]$Action = "verify",
    # TimeoutSeconds 是完整入口的总预算；cleanup 使用独立短预算。
    [ValidateRange(120, 3600)]
    [int]$TimeoutSeconds = 1200,
    # FuzzTimeSeconds 是每个显式 fuzz target 的固定非零预算。
    [ValidateRange(3, 30)]
    [int]$FuzzTimeSeconds = 3
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$QualificationRoot = Join-Path $RepositoryRoot ".local\qualification"
$GoTool = Join-Path $RepositoryRoot "tools\go\go.ps1"
$ProtoTool = Join-Path $RepositoryRoot "tools\proto\proto.ps1"
$StorageTool = Join-Path $RepositoryRoot "tools\storage\storage.ps1"
# QualificationSpec 是归档后持续约束 Q0 行为的主规格名称。
$QualificationSpec = "server-v1-qualification"
# QualificationTasksPath 锁定产生当前冻结基线的归档任务记录，避免长期入口依赖 active change。
$QualificationTasksPath = Join-Path $RepositoryRoot "openspec\changes\archive\2026-07-16-qualify-server-v1\tasks.md"
$RunId = [guid]::NewGuid().ToString("N")
$RunDirectory = Join-Path $QualificationRoot $RunId
$StartedAt = [DateTime]::UtcNow
$Deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
$CleanupTimeoutSeconds = 60
$ReportSchemaVersion = 1
$ServerProcess = $null
$ServerGeneration = 0
$StorageRunId = ""
$OperationFailure = $null
$CleanupFailure = $null
$ReportFailure = $null
$ReportPath = Join-Path $RunDirectory "report.json"
$LayeredEvidence = ""
$GateResults = @()
$ActiveGate = ""
$ActiveGateStartedAt = $null
$FailureCategory = ""
$ExpectedGateIDs = switch ($Action) {
    "contract" { @("contract", "cleanup") }
    "blackbox" { @("contract", "layered-evidence", "build", "environment", "black-box", "cleanup") }
    "verify" { @("contract", "unit", "fuzz", "race", "storage", "layered-evidence", "build", "environment", "black-box", "governance", "cleanup") }
}

# Write-Stage 输出稳定低敏阶段，不打印路径、端口、PID、credential 或 backend 文本。
function Write-Stage {
    param([string]$Stage, [string]$Message)
    Write-Host "[$Stage] $Message"
}

# Start-Gate 打开一个稳定门禁计时；门禁禁止嵌套，避免报告顺序与真实执行分叉。
function Start-Gate {
    param([string]$ID)
    if ($ActiveGate) { throw "Qualification gate nesting is invalid" }
    $script:ActiveGate = $ID
    $script:ActiveGateStartedAt = [DateTime]::UtcNow
}

# Complete-Gate 只记录已完整返回的门禁，不把子命令文本带入报告。
function Complete-Gate {
    if (-not $ActiveGate) { throw "Qualification gate completion is invalid" }
    $duration = [Math]::Max(0, [int64]([DateTime]::UtcNow - $ActiveGateStartedAt).TotalMilliseconds)
    $script:GateResults += [pscustomobject]@{ id = $ActiveGate; outcome = "pass"; durationMs = $duration }
    $script:ActiveGate = ""
    $script:ActiveGateStartedAt = $null
}

# Fail-ActiveGate 将首个失败归一为 gate ID，不保存原始异常或 backend 文本。
function Fail-ActiveGate {
    if (-not $ActiveGate) { return }
    $duration = [Math]::Max(0, [int64]([DateTime]::UtcNow - $ActiveGateStartedAt).TotalMilliseconds)
    $script:GateResults += [pscustomobject]@{ id = $ActiveGate; outcome = "fail"; durationMs = $duration }
    if (-not $FailureCategory) { $script:FailureCategory = $ActiveGate + "-failed" }
    $script:ActiveGate = ""
    $script:ActiveGateStartedAt = $null
}

# New-InitialReport 确保 contract 或环境阶段提前失败时仍留下 schema-versioned 低敏证据。
function New-InitialReport {
    $freeze = Get-Content -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\freeze.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $endpoint = Get-Content -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\endpoint-manifest.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $manifest = Get-Content -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\manifest.json") -Raw -Encoding utf8 | ConvertFrom-Json
    $initialScenarios = @($manifest.scenarios | ForEach-Object {
        [ordered]@{
            id = [string]$_.id
            phase = [string]$_.phase
            evidence = [string]$_.evidence
            execution = [string]$_.execution
            mandatory = [bool]$_.mandatory
            outcome = "skipped"
            durationMs = 0
        }
    })
    $body = [ordered]@{
        schemaVersion = $ReportSchemaVersion
        qualificationVersion = [string]$manifest.qualificationVersion
        runId = $RunId
        protocolVersion = [uint32]$endpoint.version.protocolVersion
        contractDigest = [string]$freeze.digest
        startedAt = $StartedAt.ToString("yyyy-MM-ddTHH:mm:ssZ")
        durationMs = 0
        qualified = $false
        gates = @()
        scenarios = $initialScenarios
        failure = $null
        cleanup = "pending"
    }
    [System.IO.File]::WriteAllText($ReportPath, (($body | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
}

# Assert-Remaining 在启动新阶段前检查全局预算，不允许无界子流程继续开始。
function Assert-Remaining {
    param([string]$Stage)
    if ([DateTime]::UtcNow -ge $Deadline) {
        throw "Qualification deadline elapsed before stage $Stage"
    }
}

# Assert-RunDirectory 验证所有递归创建和删除都限制在 ignored qualification root。
function Assert-RunDirectory {
    if ($RunId -notmatch '^[0-9a-f]{32}$') {
        throw "Qualification RunId is invalid"
    }
    $root = [System.IO.Path]::GetFullPath($QualificationRoot).TrimEnd('\') + '\'
    $resolved = [System.IO.Path]::GetFullPath($RunDirectory)
    if (-not $resolved.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Qualification run directory escaped local root"
    }
}

# Invoke-Go 通过项目锁定 SDK 执行 Go 命令并保留原始退出码。
function Invoke-Go {
    param([string[]]$Arguments)
    Assert-Remaining "go"
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $GoTool @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Go qualification gate failed"
    }
}

# Invoke-ProtoVerify 执行 clean generation、lint、fixture 与 tracked projection 边界检查。
function Invoke-ProtoVerify {
    Assert-Remaining "proto"
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $ProtoTool verify
    if ($LASTEXITCODE -ne 0) {
        throw "Protocol qualification gate failed"
    }
}

# Get-LoopbackPort 让 OS 选择当前空闲端口并立即释放给随后启动的精确 listener。
function Get-LoopbackPort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
    }
    finally {
        $listener.Stop()
    }
}

# Quote-YamlSingle 把绝对 Windows 路径安全写入单引号 YAML scalar。
function Quote-YamlSingle {
    param([string]$Value)
    return "'" + $Value.Replace("'", "''") + "'"
}

# ConvertTo-NativeArgument 按 Windows CommandLineToArgvW 规则转义单个 native 参数。
function ConvertTo-NativeArgument {
    param([AllowEmptyString()][string]$Value)
    if ($null -eq $Value) { $Value = "" }
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    $builder = [System.Text.StringBuilder]::new()
    [void]$builder.Append([char]34)
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') { $slashes++; continue }
        if ($character -eq [char]34) {
            [void]$builder.Append(('\' * (($slashes * 2) + 1)))
            [void]$builder.Append([char]34)
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) { [void]$builder.Append(('\' * $slashes)); $slashes = 0 }
        [void]$builder.Append($character)
    }
    if ($slashes -gt 0) { [void]$builder.Append(('\' * ($slashes * 2))) }
    [void]$builder.Append([char]34)
    return $builder.ToString()
}

# Invoke-StorageUp 创建并保留独立 MySQL/Redis，返回 owner 已登记的精确状态。
function Invoke-StorageUp {
    Assert-Remaining "storage-up"
    $output = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action up -TimeoutSeconds 300 2>&1
    $output | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) {
        throw "Storage setup failed"
    }
    $joined = $output -join "`n"
    if ($joined -notmatch 'RunId=([0-9a-f]{32})') {
        throw "Storage setup omitted RunId"
    }
    $script:StorageRunId = $Matches[1]
    $statusText = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action status -RunId $script:StorageRunId | Out-String
    if ($LASTEXITCODE -ne 0) {
        throw "Storage status failed"
    }
    return $statusText | ConvertFrom-Json
}

# Write-ServerConfig 只写非敏感配置和 file: secret reference，不复制 secret value。
function Write-ServerConfig {
    param([object]$StorageState, [int]$DiagnosticPort, [int]$PublicPort, [int]$GameplayPort)
    $storageDirectory = Join-Path (Join-Path $RepositoryRoot ".local\storage") $StorageRunId
    $certificate = Join-Path $RunDirectory "server-cert.pem"
    $privateKey = Join-Path $RunDirectory "server-key.pem"
    $admissionKey = Join-Path $RunDirectory "admission-key"
    $battleKey = Join-Path $RunDirectory "battle-derivation-key"
    $mysqlPassword = Join-Path $storageDirectory "mysql-password"
    $redisPassword = Join-Path $storageDirectory "redis-password"
    $simulationBinary = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-sim-server.exe"
    $simulationReceipt = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\qualification-gate-receipt.json"
    $simulationIdentity = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json"
    if (-not (Test-Path -LiteralPath $simulationBinary -PathType Leaf) -or
        -not (Test-Path -LiteralPath $simulationReceipt -PathType Leaf) -or
        -not (Test-Path -LiteralPath $simulationIdentity -PathType Leaf)) {
        throw "Qualified simulation artifacts are required before server v1 qualification"
    }
    $simulationBinaryDigest = (Get-FileHash -LiteralPath $simulationBinary -Algorithm SHA256).Hash.ToLowerInvariant()
    $simulationReceiptDigest = (Get-FileHash -LiteralPath $simulationReceipt -Algorithm SHA256).Hash.ToLowerInvariant()
    $simulationIdentityDocument = Get-Content -LiteralPath $simulationIdentity -Raw -Encoding UTF8 |
        ConvertFrom-Json
    $simulationBuildIdentity = [string]$simulationIdentityDocument.target_identity
    if ($simulationBuildIdentity -notmatch '^[0-9a-f]{64}$') {
        throw "Qualified simulation target identity is invalid"
    }
    $body = @"
environment: local
runtime:
  startupTimeout: 60s
  shutdownTimeout: 10s
logging:
  level: info
  format: json
diagnostic:
  address: 127.0.0.1:$DiagnosticPort
  readHeaderTimeout: 1s
  readTimeout: 2s
  writeTimeout: 2s
  idleTimeout: 5s
  maxHeaderBytes: 8192
publicApi:
  address: 127.0.0.1:$PublicPort
  tls:
    enabled: true
    certificateFile: $(Quote-YamlSingle $certificate)
    privateKeySecret: $(Quote-YamlSingle ("file:" + $privateKey))
  endpoints:
    wss:
      host: localhost
      port: $PublicPort
    tlsTcp:
      host: localhost
      port: $GameplayPort
  # 资格场景在单一 loopback IP 上顺序创建隔离 actor，避免生产注册预算污染场景边界。
  rates:
    getVersion: { requests: 10000, window: 1m, burst: 10000 }
    getBootstrapConfig: { requests: 10000, window: 1m, burst: 10000 }
    registerAccount: { requests: 10000, window: 1m, burst: 10000 }
    loginAccount: { requests: 10000, window: 1m, burst: 10000 }
    refreshSession: { requests: 10000, window: 1m, burst: 10000 }
    logoutSession: { requests: 10000, window: 1m, burst: 10000 }
    issueConnectionTicket: { requests: 10000, window: 1m, burst: 10000 }
    getWorldBootstrap: { requests: 10000, window: 1m, burst: 10000 }
    acceptVisitInvite: { requests: 10000, window: 1m, burst: 10000 }
    issueWorldAdmission: { requests: 10000, window: 1m, burst: 10000 }
    issueBattleTicket: { requests: 10000, window: 1m, burst: 10000 }
  worldRuntime:
    placementLeaseTtl: 5s
    maxInstances: 64
    deadlineEntries: 8192
  visitSession:
    capacity: 4
    sessionLifetime: 5m
    inviteLifetime: 30s
    reservationLifetime: 10s
    ownerGrace: 2s
    visitorReconnectGrace: 2s
    replayRetention: 1m
  worldAdmission:
    maximumLifetime: 15s
    replayRetention: 1m
    derivationKeySecret: $(Quote-YamlSingle ("file:" + $admissionKey))
  battleUdp:
    bindAddress: 127.0.0.1:58445
    advertised: { host: 127.0.0.1, port: 58445 }
    derivationKeySecret: $(Quote-YamlSingle ("file:" + $battleKey))
    wireIdentity: 3d0505f82dcfacec3b296e0a089ce58db2338a59b45e65c87ae6f1dddf47c2b1
  websocketControl:
    allowedHosts:
      - localhost:$PublicPort
    allowedOrigins: []
    preAuthRate: { requests: 10000, window: 1m, burst: 10000 }
    closeTimeout: 500ms
  gameplayTcp:
    address: 127.0.0.1:$GameplayPort
    preAuthRate: { requests: 10000, window: 1m, burst: 10000 }
    closeTimeout: 500ms
    shutdownTimeout: 3s
simulationControl:
  enabled: true
  binaryPath: $(Quote-YamlSingle $simulationBinary)
  binarySha256: $simulationBinaryDigest
  qualificationReceiptPath: $(Quote-YamlSingle $simulationReceipt)
  qualificationReceiptSha256: $simulationReceiptDigest
  buildIdentity: $simulationBuildIdentity
  modelManifest: 65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1
  profileManifest: c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424
  configIdentity: da4e34bb3c12a0f0e953fdf9e0c5cc5dc5bd3421a7ec54a8f0bd5fc42b84d381
  navigationIdentity: 3673d4c38f6a2f285eafd015f0d1b1169041553967a393a82f866d73e4305bbd
  physicsIdentity: ed46bed0ab9b95ced44719827fbc74074d56d9909eec90b062cce6b72b461b98
  instanceCapacity: 64
  actorCapacity: 8
  frameBytes: 65536
  pendingRequests: 256
  requestTimeout: 10s
  healthInterval: 5s
  healthTimeout: 1s
  drainTimeout: 3s
  shutdownTimeout: 5s
  stderrLineBytes: 1024
storage:
  mysql:
    address: 127.0.0.1:$($StorageState.mysqlPort)
    passwordSecret: $(Quote-YamlSingle ("file:" + $mysqlPassword))
    probe:
      interval: 500ms
      timeout: 200ms
      failureThreshold: 4
  redis:
    address: 127.0.0.1:$($StorageState.redisPort)
    passwordSecret: $(Quote-YamlSingle ("file:" + $redisPassword))
    probe:
      interval: 500ms
      timeout: 200ms
      failureThreshold: 4
"@
    [System.IO.File]::WriteAllText((Join-Path $RunDirectory "server.yaml"), $body, [System.Text.UTF8Encoding]::new($false))
}

# Start-Server 启动精确 binary、异步重定向 stdout/stderr 并记录唯一 PID owner。
function Start-Server {
    $script:ServerGeneration++
    $arguments = @("-config", (Join-Path $RunDirectory "server.yaml"))
    $process = Start-Process -FilePath (Join-Path $RunDirectory "server.exe") -ArgumentList $arguments -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $RunDirectory "server.$ServerGeneration.stdout.log") -RedirectStandardError (Join-Path $RunDirectory "server.$ServerGeneration.stderr.log")
    [System.IO.File]::WriteAllText((Join-Path $RunDirectory "server.pid"), [string]$process.Id, [System.Text.Encoding]::ASCII)
    $script:ServerProcess = $process
}

# Stop-ServerExact 终止本 run 记录的唯一 server PID，并等待重定向日志句柄释放。
function Stop-ServerExact {
    if (-not $ServerProcess -or $ServerProcess.HasExited) { return }
    Stop-Process -Id $ServerProcess.Id -Force -ErrorAction Stop
    if (-not $ServerProcess.WaitForExit(5000)) {
        throw "Qualification server did not exit within teardown budget"
    }
}

# Invoke-QualificationFault 执行 client checkpoint 请求的封闭 owner 动作。
function Invoke-QualificationFault {
    param([string]$Kind, [int]$PublicPort)
    switch ($Kind) {
        "server-restart" {
            if (-not $ServerProcess -or $ServerProcess.HasExited) { throw "Server restart source process is unavailable" }
            Stop-Process -Id $ServerProcess.Id -Force -ErrorAction Stop
            if (-not $ServerProcess.WaitForExit(5000)) { throw "Server restart source process did not exit" }
            Start-Server
            Wait-ServerReady $PublicPort
        }
        { $_ -in @("redis-flush", "redis-restart", "mysql-restart") } {
            # native child 的进度文本必须直接交给 host；若进入 success stream，PowerShell 会把它
            # 与函数的整数退出码组合成数组，调用方将无法可靠判断 qualificationtool 结果。
            & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action fault -RunId $StorageRunId -Fault $Kind -TimeoutSeconds 90 | Out-Host
            if ($LASTEXITCODE -ne 0) { throw "Storage fault owner failed" }
            # required probe 可能在 dependency restart 窗口内让进程安全退出；恢复后仍由本 run 的精确 binary replacement。
            if ($ServerProcess.HasExited) {
                Start-Server
                Wait-ServerReady $PublicPort
            }
        }
        default { throw "Qualification fault kind is unsupported" }
    }
}

# Write-FaultResponse 原子发布低敏 checkpoint 结果，不包含 backend 失败文本。
function Write-FaultResponse {
    param([uint64]$Sequence, [string]$Outcome)
    $path = Join-Path $RunDirectory "fault-response.json"
    $temporary = $path + ".tmp"
    $body = @{ schemaVersion = 1; sequence = $Sequence; outcome = $Outcome } | ConvertTo-Json -Compress
    [System.IO.File]::WriteAllText($temporary, $body + "`n", [System.Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temporary -Destination $path -Force
}

# Invoke-QualificationScenarios 并发排空 client 输出并处理当前 run 的 fault checkpoint。
function Invoke-QualificationScenarios {
    param([int]$PublicPort, [string]$Evidence)
    $arguments = @(
        "run", "-base-url", "https://localhost:$PublicPort", "-ca-file", (Join-Path $RunDirectory "server-cert.pem"),
        "-repository-root", $RepositoryRoot, "-manifest", (Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\manifest.json"),
        "-report", $ReportPath, "-control-directory", $RunDirectory, "-run-id", $RunId, "-scenarios", "all", "-evidence", $Evidence
    )
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = Join-Path $RunDirectory "qualificationtool.exe"
    $startInfo.Arguments = (($arguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    if (-not $process.Start()) { throw "Qualification client failed to start" }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $lastSequence = [uint64]0
    try {
        while (-not $process.HasExited) {
            if ([DateTime]::UtcNow -ge $Deadline) { throw "Qualification client exceeded global deadline" }
            $requestPath = Join-Path $RunDirectory "fault-request.json"
            if (Test-Path -LiteralPath $requestPath) {
                try {
                    $requestText = [System.IO.File]::ReadAllText($requestPath, [System.Text.Encoding]::UTF8)
                }
                catch [System.IO.IOException] {
                    # publisher 的 atomic rename/delete 与观察之间允许瞬时 TOCTOU，下一轮重新读取。
                    Start-Sleep -Milliseconds 25
                    continue
                }
                $request = $requestText | ConvertFrom-Json
                $sequence = [uint64]$request.sequence
                if ($request.schemaVersion -ne 1 -or $sequence -lt $lastSequence -or $request.kind -notin @("server-restart", "redis-flush", "redis-restart", "mysql-restart")) {
                    throw "Qualification fault checkpoint is invalid"
                }
                if ($sequence -eq $lastSequence) {
                    Start-Sleep -Milliseconds 50
                    continue
                }
                $lastSequence = $sequence
                try {
                    Invoke-QualificationFault ([string]$request.kind) $PublicPort
                    Write-FaultResponse $sequence "pass"
                }
                catch {
                    Write-FaultResponse $sequence "fail"
                }
            }
            Start-Sleep -Milliseconds 50
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        [System.IO.File]::WriteAllText((Join-Path $RunDirectory "client.stdout.log"), $stdout, [System.Text.UTF8Encoding]::new($false))
        [System.IO.File]::WriteAllText((Join-Path $RunDirectory "client.stderr.log"), $stderr, [System.Text.UTF8Encoding]::new($false))
        if ($stdout) { [Console]::Out.Write($stdout) }
        if ($stderr) { [Console]::Error.Write($stderr) }
        return $process.ExitCode
    }
    finally {
        if (-not $process.HasExited) {
            $process.Kill()
            $process.WaitForExit(5000)
        }
        $process.Dispose()
    }
}

# Read-DiagnosticGauges 读取公开 diagnostic metrics 中用于泄漏收敛的瞬时 gauge。
function Read-DiagnosticGauges {
    param([int]$DiagnosticPort)
    $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$DiagnosticPort/metrics" -TimeoutSec 5
    if ($response.StatusCode -ne 200 -or $response.RawContentLength -gt 1MB) {
        throw "Diagnostic metrics response is invalid"
    }
    $names = @(
        "ihomeland_server_websocket_control_connections",
        "ihomeland_server_tcp_gameplay_connections",
        "ihomeland_server_tcp_gameplay_in_flight"
    )
    $values = @{}
    foreach ($name in $names) { $values[$name] = [double]0 }
    foreach ($line in ([string]$response.Content -split "`n")) {
        if ($line -match '^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{[^}]*\})?\s+([-+]?[0-9.eE]+)\s*$' -and $values.ContainsKey($Matches[1])) {
            $value = [double]::Parse($Matches[2], [System.Globalization.CultureInfo]::InvariantCulture)
            if ([double]::IsNaN($value) -or [double]::IsInfinity($value) -or $value -lt 0) {
                throw "Diagnostic resource gauge is invalid"
            }
            $values[$Matches[1]] += $value
        }
    }
    return ,$values
}

# Wait-DiagnosticConvergence 等待所有瞬时 connection/in-flight gauge 回到场景前基线。
# PersonalWorld runtime 与 lease 属于已提交场景留下的合法当前事实，只在进程 shutdown gate 验证回收。
function Wait-DiagnosticConvergence {
    param([int]$DiagnosticPort, [hashtable]$Baseline)
    $convergenceDeadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $current = Read-DiagnosticGauges $DiagnosticPort
        $converged = $true
        foreach ($name in $Baseline.Keys) {
            if ($current[$name] -ne $Baseline[$name]) { $converged = $false; break }
        }
        if ($converged) { return }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $convergenceDeadline -and [DateTime]::UtcNow -lt $Deadline)
    throw "Diagnostic resource gauges did not converge"
}

# Assert-LowSensitivityArtifacts 扫描本轮 client/report/server 输出，拒绝已知 secret 泄漏；
# client/report 还禁止地址、PID、本机路径和业务 identity 字段，server 运维日志由既有日志规范治理。
function Assert-LowSensitivityArtifacts {
    $artifactPaths = @($ReportPath, (Join-Path $RunDirectory "client.stdout.log"), (Join-Path $RunDirectory "client.stderr.log")) + @(Get-ChildItem -LiteralPath $RunDirectory -Filter "server.*.log" | ForEach-Object { $_.FullName })
    $storageDirectory = Join-Path (Join-Path $RepositoryRoot ".local\storage") $StorageRunId
    $secretPaths = @((Join-Path $RunDirectory "admission-key"), (Join-Path $RunDirectory "battle-derivation-key"), (Join-Path $RunDirectory "server-key.pem"), (Join-Path $storageDirectory "mysql-password"), (Join-Path $storageDirectory "redis-password"))
    $secrets = @()
    foreach ($path in $secretPaths) {
        if (Test-Path -LiteralPath $path) {
            $value = [System.IO.File]::ReadAllText($path, [System.Text.Encoding]::UTF8).Trim()
            if ($value.Length -ge 8) { $secrets += $value }
        }
    }
    foreach ($path in $artifactPaths) {
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $content = [System.IO.File]::ReadAllText($path, [System.Text.Encoding]::UTF8)
        foreach ($secret in $secrets) {
            if ($content.IndexOf($secret, [System.StringComparison]::Ordinal) -ge 0) {
                throw "Qualification artifact leaked a run secret"
            }
        }
    }
    foreach ($path in @($ReportPath, (Join-Path $RunDirectory "client.stdout.log"), (Join-Path $RunDirectory "client.stderr.log"))) {
        $content = [System.IO.File]::ReadAllText($path, [System.Text.Encoding]::UTF8)
        # 稳定 scenario ID 可以包含 `credential-rejection` 等能力名；只拒绝可承载值的字段名。
        if ($content -match '(?i)([A-Z]:\\|127\.0\.0\.1|"(?:player|session|world|visit|account)[A-Za-z_]*"\s*:|"(?:pid|accessToken|refreshToken|credential|ticket|admission)"\s*:)') {
            throw "Qualification client or report artifact contains forbidden high-sensitivity context"
        }
    }
}

# Wait-ServerReady 通过独立公开 version/config probe 轮询 readiness，并监控 unexpected exit。
function Wait-ServerReady {
    param([int]$PublicPort)
    # 首次空库需要顺序执行 migration；readiness 预算必须覆盖 storage owner 的完整启动窗口。
    $probeDeadline = [DateTime]::UtcNow.AddSeconds(70)
    while ([DateTime]::UtcNow -lt $probeDeadline -and [DateTime]::UtcNow -lt $Deadline) {
        if ($ServerProcess.HasExited) {
            throw "Server exited before readiness"
        }
        # Windows PowerShell 5.1 会把 native stderr 包装成 ErrorRecord；未 ready 是轮询状态，
        # 只在这一条 probe 内按退出码处理，不能让全局 Stop 策略提前终止循环。
        $previousErrorAction = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            & (Join-Path $RunDirectory "qualificationtool.exe") probe -base-url "https://localhost:$PublicPort" -ca-file (Join-Path $RunDirectory "server-cert.pem") -timeout 2s 2>$null
            $probeExitCode = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $previousErrorAction
        }
        if ($probeExitCode -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 200
    }
    throw "Server readiness timed out"
}

# Invoke-FuzzMatrix 逐项执行显式 target；target 缺失或任何一次未运行都会失败。
function Invoke-FuzzMatrix {
    $targets = @(
        @("./internal/account", "FuzzUsernameValidation"), @("./internal/account", "FuzzDisplayNameNormalization"),
        @("./internal/fixtures", "FuzzGoldenPayloadRoundTrip"), @("./internal/contract", "FuzzWorldAdmissionOneOfIdentityBoundary"),
        @("./internal/placement", "FuzzPlacementIdentifiers"), @("./internal/placement", "FuzzAssignmentSnapshotHydration"),
        @("./internal/personalworld", "FuzzPersonalWorldIDValidation"), @("./internal/personalworld", "FuzzIdempotencyKeyValidation"),
        @("./internal/personalworld", "FuzzSnapshotHydration"), @("./internal/personalworld", "FuzzArchiveCommandFingerprint"),
        @("./internal/session", "FuzzParseTicketNonce"), @("./internal/session", "FuzzParseSecret"),
        @("./internal/visitsession", "FuzzVisitSessionIdentifiers"), @("./internal/worldadmission", "FuzzWorldAdmissionParsing"),
        @("./internal/storage/account", "FuzzParsePHC"), @("./internal/storage/placement", "FuzzCanonicalDecimalAndAssignmentHydration"),
        @("./internal/storage/redis", "FuzzBuildKey"), @("./internal/storage/session", "FuzzSessionCodecInputs"),
        @("./internal/storage/visitsession", "FuzzDecodeSnapshot"), @("./internal/storage/visitsession", "FuzzDecodeCommandResults"),
        @("./internal/transport/wscontrol", "FuzzCodecRejectsUnknownMessageID"), @("./internal/transport/wscontrol", "FuzzTicketCredential"),
        @("./internal/transport/tcpgameplay", "FuzzCodecDecode"), @("./internal/transport/tcpgameplay", "FuzzDecodePreface"),
        @("./internal/testclient", "FuzzLoadManifest"), @("./internal/testclient", "FuzzRealtimeEnvelope"), @("./internal/testclient", "FuzzGameplayPreface")
    )
    foreach ($target in $targets) {
        Invoke-Go @("test", $target[0], "-run=^$", "-fuzz=^$($target[1])$", "-fuzztime=$($FuzzTimeSeconds)s")
    }
}

# Enable-RaceToolchain 显式选择可用 Windows gcc 并启用 CGO，禁止 `-race` 静默跳过。
function Enable-RaceToolchain {
    $gcc = Get-Command gcc.exe -CommandType Application -ErrorAction SilentlyContinue
    if (-not $gcc) {
        foreach ($directory in @("C:\msys64\ucrt64\bin", "C:\msys64\mingw64\bin")) {
            $candidate = Join-Path $directory "gcc.exe"
            if (Test-Path -LiteralPath $candidate) {
                $env:PATH = $directory + ";" + $env:PATH
                $gcc = Get-Command gcc.exe -CommandType Application -ErrorAction SilentlyContinue
                break
            }
        }
    }
    if (-not $gcc) {
        throw "Race gate requires gcc; install MSYS2 UCRT64 or expose gcc.exe on PATH"
    }
    $env:CC = "gcc"
    $env:CGO_ENABLED = "1"
}

# Invoke-LayeredEvidenceMatrix 按共享证据目录运行无法由公开 wire 稳定制造的精细故障与资源边界证明。
# 目录同时受 Go 漂移测试约束；任一测试缺失、改名、未匹配或失败都阻止 scenario ID 进入报告。
function Invoke-LayeredEvidenceMatrix {
    Write-Stage "EVIDENCE" "running explicit domain, resource, shutdown and diagnostic evidence"
    $catalogPath = Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\evidence-manifest.json"
    $catalog = Get-Content -LiteralPath $catalogPath -Raw -Encoding utf8 | ConvertFrom-Json
    $scenarioIDs = @()
    foreach ($group in @($catalog.groups)) {
        $packages = @($group.packages | ForEach-Object { [string]$_.path })
        $tests = @($group.packages | ForEach-Object { @($_.tests) } | ForEach-Object { [string]$_ })
        if ($packages.Count -eq 0 -or $tests.Count -eq 0) {
            throw "Layered evidence group is empty"
        }
        $testPattern = ($tests | ForEach-Object { [regex]::Escape($_) }) -join "|"
        Invoke-Go (@("test", "-count=1") + $packages + @("-run=^($testPattern)$")) | Out-Host
        $scenarioIDs += @($group.scenarioIds | ForEach-Object { [string]$_ })
    }
    $script:LayeredEvidence = $scenarioIDs -join ","
}

# Invoke-GovernanceGate 在形成资格结论前校验 OpenSpec、任务完成度、diff 格式和生成物边界。
function Invoke-GovernanceGate {
    Write-Stage "GOVERNANCE" "validating OpenSpec and repository delivery rules"
    & openspec.cmd validate $QualificationSpec --strict
    if ($LASTEXITCODE -ne 0) { throw "Qualification OpenSpec validation failed" }
    & openspec.cmd validate --all --strict
    if ($LASTEXITCODE -ne 0) { throw "Repository OpenSpec validation failed" }
    & git diff --check
    if ($LASTEXITCODE -ne 0) { throw "Git diff validation failed" }
    $trackedGenerated = @(& git ls-files -- server/internal/generated client/Generated)
    if ($LASTEXITCODE -ne 0 -or $trackedGenerated.Count -ne 0) { throw "Generated projection boundary failed" }
    if (-not (Test-Path -LiteralPath $QualificationTasksPath -PathType Leaf)) { throw "Qualification task archive is missing" }
    if (Select-String -LiteralPath $QualificationTasksPath -Pattern '^- \[ \]' -Quiet) { throw "Qualification change has incomplete tasks" }
}

# Complete-Report 把 cleanup owner 的最终结果合并到机器报告，不添加本机细节。
function Complete-Report {
    param([bool]$OperationPassed, [bool]$CleanupPassed, [int64]$CleanupDurationMS)
    if (-not (Test-Path -LiteralPath $ReportPath)) {
        throw "Qualification report is missing"
    }
    $report = Get-Content -LiteralPath $ReportPath -Raw -Encoding utf8 | ConvertFrom-Json
    $report.cleanup = if ($CleanupPassed) { "pass" } else { "cleanup_failure" }
    $cleanupOutcome = if ($CleanupPassed) { "pass" } else { "fail" }
    $recordedGates = @{}
    foreach ($gate in @($GateResults)) {
        if ($recordedGates.ContainsKey($gate.id)) { throw "Qualification report contains a duplicate gate" }
        if ($ExpectedGateIDs -notcontains $gate.id) { throw "Qualification report contains an unexpected gate" }
        $recordedGates[$gate.id] = $gate
    }
    $recordedGates["cleanup"] = [pscustomobject]@{ id = "cleanup"; outcome = $cleanupOutcome; durationMs = [Math]::Max(0, $CleanupDurationMS) }
    $allGates = @($ExpectedGateIDs | ForEach-Object {
        if ($recordedGates.ContainsKey($_)) { $recordedGates[$_] }
        else { [pscustomobject]@{ id = $_; outcome = "skipped"; durationMs = 0 } }
    })
    $report | Add-Member -MemberType NoteProperty -Name gates -Value $allGates -Force
    $stableFailure = $FailureCategory
    if (-not $stableFailure -and -not $CleanupPassed) { $stableFailure = "cleanup-failed" }
    $report | Add-Member -MemberType NoteProperty -Name failure -Value $(if ($stableFailure) { $stableFailure } else { $null }) -Force
    $report.durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $StartedAt).TotalMilliseconds)
    $allPassed = @($report.scenarios | Where-Object { $_.mandatory -and $_.outcome -ne "pass" }).Count -eq 0
    $allGatesPassed = @($allGates | Where-Object { $_.outcome -ne "pass" }).Count -eq 0
    # `blackbox` 只用于缩短本地排障路径；只有完整 `verify` 跑完所有分层门禁才可形成资格结论。
    $report.qualified = ($Action -eq "verify") -and $OperationPassed -and $CleanupPassed -and $allPassed -and $allGatesPassed
    $json = $report | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($ReportPath, $json + "`n", [System.Text.UTF8Encoding]::new($false))
}

Assert-RunDirectory
New-Item -ItemType Directory -Path $RunDirectory -Force | Out-Null
New-InitialReport
try {
    Start-Gate "contract"
    Write-Stage "CONTRACT" "verifying protocol, fixtures and freeze inputs"
    Invoke-ProtoVerify
    Invoke-Go @("test", "-count=1", "./internal/testclient", "./cmd/qualificationtool")
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action contract
    if ($LASTEXITCODE -ne 0) { throw "Storage contract gate failed" }
    Complete-Gate
    if ($Action -eq "contract") {
        Write-Stage "OK" "qualification contract checks passed"
    }
    else {
    if ($Action -eq "verify") {
        Start-Gate "unit"
        Write-Stage "UNIT" "running complete Go test graph"
        Invoke-Go @("test", "-count=1", "./...")
        Complete-Gate
        Start-Gate "fuzz"
        Write-Stage "FUZZ" "running explicit non-zero fuzz matrix"
        Invoke-FuzzMatrix
        Complete-Gate
        Start-Gate "race"
        Write-Stage "RACE" "running concurrent owner packages"
        Enable-RaceToolchain
        Invoke-Go @("test", "-race", "-count=1", "./internal/account", "./internal/session", "./internal/placement", "./internal/visitsession", "./internal/app", "./internal/transport/wscontrol", "./internal/transport/tcpgameplay", "./internal/testclient")
        Complete-Gate
        Start-Gate "storage"
        Write-Stage "STORAGE" "running real MySQL/Redis integration matrix"
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action verify -TimeoutSeconds 300
        if ($LASTEXITCODE -ne 0) { throw "Storage verification gate failed" }
        Complete-Gate
    }

    Start-Gate "layered-evidence"
    Invoke-LayeredEvidenceMatrix
    Complete-Gate

    Start-Gate "build"
    Write-Stage "BUILD" "building exact server and qualification binaries"
    Invoke-Go @("build", ("-o=" + (Join-Path $RunDirectory "server.exe")), "./cmd/server")
    Invoke-Go @("build", ("-o=" + (Join-Path $RunDirectory "qualificationtool.exe")), "./cmd/qualificationtool")
    Complete-Gate
    Start-Gate "environment"
    & (Join-Path $RunDirectory "qualificationtool.exe") tls -directory $RunDirectory
    if ($LASTEXITCODE -ne 0) { throw "Temporary TLS generation failed" }
    $storageState = Invoke-StorageUp
    $diagnosticPort = Get-LoopbackPort
    $publicPort = Get-LoopbackPort
    $gameplayPort = Get-LoopbackPort
    if (@(@($diagnosticPort, $publicPort, $gameplayPort) | Select-Object -Unique).Count -ne 3) {
        throw "Dynamic listener ports collided"
    }
    Write-ServerConfig $storageState $diagnosticPort $publicPort $gameplayPort
    Start-Server
    Wait-ServerReady $publicPort
    $diagnosticBaseline = Read-DiagnosticGauges $diagnosticPort
    Complete-Gate

    Start-Gate "black-box"
    Write-Stage "BLACKBOX" "running manifest scenarios against independent server"
    # 三个真实 fault scenario 由 checkpoint driver 执行，不属于 layered evidence 字符串。
    $evidence = $LayeredEvidence
    $scenarioExitCode = Invoke-QualificationScenarios $publicPort $evidence
    if ($scenarioExitCode -ne 0) { throw "Black-box qualification scenarios failed" }
    if ($ServerProcess.HasExited) { throw "Server exited during qualification scenarios" }
    Wait-DiagnosticConvergence $diagnosticPort $diagnosticBaseline
    Stop-ServerExact
    Assert-LowSensitivityArtifacts
    Complete-Gate
    Write-Stage "OK" "qualification scenarios passed"
    if ($Action -eq "verify") {
        Start-Gate "governance"
        Invoke-GovernanceGate
        Complete-Gate
    }
    }
}
catch {
    Fail-ActiveGate
    $OperationFailure = $_
}
finally {
    $cleanupStartedAt = [DateTime]::UtcNow
    Write-Stage "CLEANUP" "removing exact qualification resources"
    if ($ServerProcess -and -not $ServerProcess.HasExited) {
        try {
            Stop-Process -Id $ServerProcess.Id -Force -ErrorAction Stop
            $null = $ServerProcess.WaitForExit(5000)
        }
        catch {
            $CleanupFailure = $_
        }
    }
    if ($StorageRunId) {
        try {
            & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $StorageTool -Action down -RunId $StorageRunId -TimeoutSeconds $CleanupTimeoutSeconds
            if ($LASTEXITCODE -ne 0) { throw "Storage cleanup failed" }
        }
        catch {
            $CleanupFailure = if ($CleanupFailure) { [System.Management.Automation.RuntimeException]::new("Multiple qualification cleanup operations failed") } else { $_ }
        }
    }
    $cleanupDurationMS = [int64]([DateTime]::UtcNow - $cleanupStartedAt).TotalMilliseconds
    try {
        Complete-Report ($null -eq $OperationFailure) ($null -eq $CleanupFailure) $cleanupDurationMS
    }
    catch {
        $ReportFailure = $_
    }
}
if ($OperationFailure) {
    if ($CleanupFailure -or $ReportFailure) {
        throw "Qualification failed; finalization also failed"
    }
    throw $OperationFailure.Exception
}
if ($CleanupFailure) {
    throw $CleanupFailure.Exception
}
if ($ReportFailure) {
    throw $ReportFailure.Exception
}
