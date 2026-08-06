Set-StrictMode -Version Latest

# Quote-YamlSingle 对 run-local absolute path 使用 YAML single quoted scalar。
function Quote-YamlSingle {
    param([Parameter(Mandatory = $true)][string]$Value)
    return "'" + $Value.Replace("'", "''") + "'"
}

# Get-FreeTcpPort 让 OS 选择 loopback TCP port；仅在随后立即启动的隔离环境内使用。
function Get-FreeTcpPort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([Net.IPEndPoint]$listener.LocalEndpoint).Port
    }
    finally {
        $listener.Stop()
    }
}

# Get-FreeUdpPort 让 OS 选择 loopback UDP port；backend/frontend 各自独占一个 port。
function Get-FreeUdpPort {
    $client = [Net.Sockets.UdpClient]::new(
        [Net.IPEndPoint]::new([Net.IPAddress]::Loopback, 0)
    )
    try {
        return ([Net.IPEndPoint]$client.Client.LocalEndPoint).Port
    }
    finally {
        $client.Dispose()
    }
}

# Get-LowerSha256 返回 canonical lowercase file digest。
function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Write-ServerConfig 生成只属于本 run 的受控资格 Composition Root。
function Write-ServerConfig {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$RunId,
        [Parameter(Mandatory = $true)][string]$StorageRunId,
        [Parameter(Mandatory = $true)]$StorageState,
        [Parameter(Mandatory = $true)][int]$DiagnosticPort,
        [Parameter(Mandatory = $true)][int]$PublicPort,
        [Parameter(Mandatory = $true)][int]$GameplayPort,
        [Parameter(Mandatory = $true)][int]$BackendPort,
        [Parameter(Mandatory = $true)][int]$FrontendPort
    )

    $storageDirectory = Join-Path (Join-Path $RepositoryRoot ".local\storage") $StorageRunId
    $certificate = Join-Path $RunDirectory "server-cert.pem"
    $privateKey = Join-Path $RunDirectory "server-key.pem"
    $admissionKey = Join-Path $RunDirectory "admission-key"
    $battleKey = Join-Path $RunDirectory "battle-derivation-key"
    $mysqlPassword = Join-Path $storageDirectory "mysql-password"
    $redisPassword = Join-Path $storageDirectory "redis-password"
    $simulationRoot = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci"
    $simulationBinary = Join-Path $simulationRoot "ihomeland-sim-server.exe"
    $simulationReceipt = Join-Path $simulationRoot "qualification-gate-receipt.json"
    $simulationIdentityPath = Join-Path $simulationRoot "ihomeland-build-identity.json"
    foreach ($path in @(
        $certificate, $privateKey, $admissionKey, $battleKey,
        $mysqlPassword, $redisPassword, $simulationBinary,
        $simulationReceipt, $simulationIdentityPath
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "battle qualification environment 缺少已验证 prerequisite"
        }
    }
    $simulationIdentity = Get-Content -LiteralPath $simulationIdentityPath -Raw -Encoding utf8 |
        ConvertFrom-Json
    $buildIdentity = [string]$simulationIdentity.target_identity
    if ($buildIdentity -notmatch '^[0-9a-f]{64}$') {
        throw "battle qualification C++ identity 无效"
    }
    $simulationBinaryDigest = Get-LowerSha256 -Path $simulationBinary
    $simulationReceiptDigest = Get-LowerSha256 -Path $simulationReceipt
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
    wss: { host: localhost, port: $PublicPort }
    tlsTcp: { host: localhost, port: $GameplayPort }
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
    capacity: 32
    sessionLifetime: 35m
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
    bindAddress: 127.0.0.1:$BackendPort
    advertised: { host: 127.0.0.1, port: $FrontendPort }
    derivationKeySecret: $(Quote-YamlSingle ("file:" + $battleKey))
    wireIdentity: 9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432
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
  buildIdentity: $buildIdentity
  modelManifest: 65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1
  profileManifest: c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424
  configIdentity: d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b
  navigationIdentity: 14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f
  physicsIdentity: 64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e
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
  qualificationMode: true
  qualificationRunId: $RunId
  qualificationSampleInterval: 1s
gameplayPackage:
  rootPath: $(Quote-YamlSingle (Join-Path $RepositoryRoot "shared\contracts\gameplay\battle\packages\personal-world-combat-v1"))
  packageId: personal-world-combat-v1
  configIdentity: d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b
  navigationIdentity: 14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f
  physicsIdentity: 64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e
  wireIdentity: 9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432
storage:
  mysql:
    address: 127.0.0.1:$($StorageState.mysqlPort)
    passwordSecret: $(Quote-YamlSingle ("file:" + $mysqlPassword))
    probe: { interval: 500ms, timeout: 200ms, failureThreshold: 4 }
  redis:
    address: 127.0.0.1:$($StorageState.redisPort)
    passwordSecret: $(Quote-YamlSingle ("file:" + $redisPassword))
    probe: { interval: 500ms, timeout: 200ms, failureThreshold: 4 }
"@
    [IO.File]::WriteAllText(
        (Join-Path $RunDirectory "server.yaml"),
        $body,
        [Text.UTF8Encoding]::new($false)
    )
}

# Wait-ServerReady 仅通过公开 TLS version/config 探测 Composition Root。
function Wait-ServerReady {
    param(
        [Parameter(Mandatory = $true)][string]$QualificationTool,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][int]$PublicPort,
        [Parameter(Mandatory = $true)][Diagnostics.Process]$ServerProcess,
        [Parameter(Mandatory = $true)][DateTime]$Deadline
    )
    while ([DateTime]::UtcNow -lt $Deadline) {
        if ($ServerProcess.HasExited) {
            $stderrPath = Join-Path $RunDirectory "server.stderr.log"
            $diagnostic = if (Test-Path -LiteralPath $stderrPath) {
                (Get-Content -LiteralPath $stderrPath -Tail 1) -join ""
            }
            else {
                "stderr unavailable"
            }
            throw "battle qualification server exited before readiness: $diagnostic"
        }
        & $QualificationTool probe `
            -base-url "https://localhost:$PublicPort" `
            -ca-file (Join-Path $RunDirectory "server-cert.pem") `
            -timeout 2s *> $null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 200
    }
    throw "battle qualification server readiness deadline exceeded"
}

# Start-BattleQualificationEnvironment 启动 storage、真实 Go server 与 exact C++ child。
function Start-BattleQualificationEnvironment {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$RunId,
        [Parameter(Mandatory = $true)][string]$ServerBinary,
        [Parameter(Mandatory = $true)][string]$QualificationTool,
        [Parameter(Mandatory = $true)][DateTime]$Deadline
    )
    if ($RunId -notmatch '^bqrun_[0-9a-f]{32}$') {
        throw "battle qualification run identity 无效"
    }
    $storageTool = Join-Path $RepositoryRoot "tools\storage\storage.ps1"
    $storageOutput = & powershell.exe -NoProfile -ExecutionPolicy Bypass `
        -File $storageTool -Action up -TimeoutSeconds 300 2>&1
    if ($LASTEXITCODE -ne 0 -or ($storageOutput -join "`n") -notmatch 'RunId=([0-9a-f]{32})') {
        throw "battle qualification storage startup failed"
    }
    $storageRunId = $Matches[1]
    $process = $null
    try {
        $storageStateText = & powershell.exe -NoProfile -ExecutionPolicy Bypass `
            -File $storageTool -Action status -RunId $storageRunId | Out-String
        if ($LASTEXITCODE -ne 0) {
            throw "battle qualification storage status failed"
        }
        $storageState = $storageStateText | ConvertFrom-Json
        & $QualificationTool tls -directory $RunDirectory
        if ($LASTEXITCODE -ne 0) {
            throw "battle qualification TLS generation failed"
        }
        $ports = [ordered]@{
            Diagnostic = Get-FreeTcpPort
            Public = Get-FreeTcpPort
            Gameplay = Get-FreeTcpPort
            Backend = Get-FreeUdpPort
            Frontend = Get-FreeUdpPort
        }
        if (@($ports.Values | Select-Object -Unique).Count -ne $ports.Count) {
            throw "battle qualification port allocation collided"
        }
        Write-ServerConfig `
            -RepositoryRoot $RepositoryRoot `
            -RunDirectory $RunDirectory `
            -RunId $RunId `
            -StorageRunId $storageRunId `
            -StorageState $storageState `
            -DiagnosticPort $ports.Diagnostic `
            -PublicPort $ports.Public `
            -GameplayPort $ports.Gameplay `
            -BackendPort $ports.Backend `
            -FrontendPort $ports.Frontend
        $stdoutPath = Join-Path $RunDirectory "server.stdout.log"
        $stderrPath = Join-Path $RunDirectory "server.stderr.log"
        $process = Start-Process -FilePath $ServerBinary `
            -ArgumentList @("-config", (Join-Path $RunDirectory "server.yaml")) `
            -PassThru -WindowStyle Hidden `
            -RedirectStandardOutput $stdoutPath `
            -RedirectStandardError $stderrPath
        [IO.File]::WriteAllText(
            (Join-Path $RunDirectory "server.pid"),
            [string]$process.Id,
            [Text.Encoding]::ASCII
        )
        Wait-ServerReady `
            -QualificationTool $QualificationTool `
            -RunDirectory $RunDirectory `
            -PublicPort $ports.Public `
            -ServerProcess $process `
            -Deadline $Deadline
        return [pscustomobject]@{
            Process = $process
            StorageRunId = $storageRunId
            RunDirectory = $RunDirectory
            ServerBinary = $ServerBinary
            QualificationTool = $QualificationTool
            PublicPort = $ports.Public
            DiagnosticPort = $ports.Diagnostic
            GameplayPort = $ports.Gameplay
            BackendPort = $ports.Backend
            FrontendPort = $ports.Frontend
            StandardOutputPath = $stdoutPath
            StandardErrorPath = $stderrPath
        }
    }
    catch {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            [void]$process.WaitForExit(5000)
        }
        & powershell.exe -NoProfile -ExecutionPolicy Bypass `
            -File $storageTool -Action down -RunId $storageRunId -TimeoutSeconds 120 *> $null
        throw
    }
}

# Restart-BattleQualificationServer 替换 exact Go/C++ process incarnation 并保留本 run storage。
function Restart-BattleQualificationServer {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)][DateTime]$Deadline
    )
    if ($Environment.Process -and -not $Environment.Process.HasExited) {
        Stop-Process -Id $Environment.Process.Id -Force -ErrorAction Stop
        if (-not $Environment.Process.WaitForExit(10000)) {
            throw "battle qualification predecessor server cleanup timeout"
        }
    }
    $restartIdentity = [guid]::NewGuid().ToString("N")
    $stdoutPath = Join-Path $Environment.RunDirectory `
        ("server.restart." + $restartIdentity + ".stdout.log")
    $stderrPath = Join-Path $Environment.RunDirectory `
        ("server.restart." + $restartIdentity + ".stderr.log")
    $process = Start-Process -FilePath $Environment.ServerBinary `
        -ArgumentList @("-config", (Join-Path $Environment.RunDirectory "server.yaml")) `
        -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $stdoutPath `
        -RedirectStandardError $stderrPath
    $Environment.Process = $process
    $Environment.StandardOutputPath = $stdoutPath
    $Environment.StandardErrorPath = $stderrPath
    [IO.File]::WriteAllText(
        (Join-Path $Environment.RunDirectory "server.pid"),
        [string]$process.Id,
        [Text.Encoding]::ASCII
    )
    Wait-ServerReady `
        -QualificationTool $Environment.QualificationTool `
        -RunDirectory $Environment.RunDirectory `
        -PublicPort $Environment.PublicPort `
        -ServerProcess $process `
        -Deadline $Deadline
}

# Stop-BattleQualificationEnvironment 只清理返回对象记录的 exact process 与 storage run。
function Stop-BattleQualificationEnvironment {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)]$Environment
    )
    $failures = [Collections.Generic.List[string]]::new()
    if ($Environment.Process -and -not $Environment.Process.HasExited) {
        try {
            Stop-Process -Id $Environment.Process.Id -Force -ErrorAction Stop
            if (-not $Environment.Process.WaitForExit(10000)) {
                $failures.Add("process-timeout")
            }
        }
        catch {
            $failures.Add("process")
        }
    }
    if ($Environment.StorageRunId) {
        & powershell.exe -NoProfile -ExecutionPolicy Bypass `
            -File (Join-Path $RepositoryRoot "tools\storage\storage.ps1") `
            -Action down -RunId $Environment.StorageRunId -TimeoutSeconds 120 *> $null
        if ($LASTEXITCODE -ne 0) {
            $failures.Add("storage")
        }
    }
    if ($failures.Count -ne 0) {
        throw "battle qualification cleanup failed: $($failures -join ',')"
    }
    foreach ($credentialName in @(
        "admission-key",
        "battle-derivation-key",
        "server-key.pem"
    )) {
        $credentialPath = Join-Path $Environment.RunDirectory $credentialName
        if (Test-Path -LiteralPath $credentialPath -PathType Leaf) {
            Remove-Item -LiteralPath $credentialPath -Force
        }
    }
    return Get-BattleQualificationCleanupEvidence -Environment $Environment
}

# Get-BattleQualificationCleanupEvidence 只检查本 run 明确拥有的 process、ports 与 credential。
function Get-BattleQualificationCleanupEvidence {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Environment
    )
    $remainingProcesses = if (
        $Environment.Process -and -not $Environment.Process.HasExited
    ) { 1 } else { 0 }
    $tcpPorts = @(
        [int]$Environment.PublicPort,
        [int]$Environment.DiagnosticPort,
        [int]$Environment.GameplayPort
    )
    $udpPorts = @(
        [int]$Environment.BackendPort,
        [int]$Environment.FrontendPort
    )
    $remainingListeners = @(
        Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
            Where-Object { $tcpPorts -contains [int]$_.LocalPort }
        Get-NetUDPEndpoint -ErrorAction SilentlyContinue |
            Where-Object { $udpPorts -contains [int]$_.LocalPort }
    ).Count
    $remainingCredentials = @(
        "admission-key",
        "battle-derivation-key",
        "server-key.pem"
    ) | Where-Object {
        Test-Path -LiteralPath (Join-Path $Environment.RunDirectory $_) -PathType Leaf
    }
    $disposition = if (
        $remainingProcesses -eq 0 -and
        $remainingListeners -eq 0 -and
        @($remainingCredentials).Count -eq 0
    ) { "passed" } else { "failed" }
    return [pscustomobject][ordered]@{
        disposition = $disposition
        remainingProcesses = $remainingProcesses
        remainingListeners = $remainingListeners
        remainingContainers = 0
        reusableCredentials = @($remainingCredentials).Count
    }
}

Export-ModuleMember -Function @(
    "Start-BattleQualificationEnvironment",
    "Restart-BattleQualificationServer",
    "Stop-BattleQualificationEnvironment",
    "Get-BattleQualificationCleanupEvidence"
)
