[CmdletBinding()]
param(
    # StorageRunId选择已经启动且允许本工具保留数据的唯一MySQL/Redis运行。
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-f0-9]{32}$')]
    [string]$StorageRunId,

    # PlayerPath必须指向包含server-restart-recovery mode的最新Development Player。
    [Parameter(Mandatory = $true)]
    [string]$PlayerPath,

    # ServerPath默认使用当前client-v1验收服务端，可显式替换为同契约构建。
    [string]$ServerPath = '',

    # 成功后默认保留replacement server，便于继续人工或其他自动诊断。
    [switch]$StopReplacementServer
)

$ErrorActionPreference = 'Stop'
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$RunId = [Guid]::NewGuid().ToString('N')
$RunDirectory = Join-Path $RepositoryRoot ".local\client-recovery-diagnostics\$RunId"
$ProfileRoot = Join-Path $RunDirectory 'player-storage'
$SignalPath = Join-Path $RunDirectory 'replacement-ready.signal'
$PlayerLog = Join-Path $RunDirectory 'player.log'
$StatePath = Join-Path $RepositoryRoot ".local\storage\$StorageRunId\state.json"
$ServerConfig = Join-Path $RepositoryRoot 'server\config\local.yaml'
if ([string]::IsNullOrWhiteSpace($ServerPath)) {
    $ServerPath = Join-Path $RepositoryRoot '.local\server-build\qualify-client-v1-authoritative-visit-revision\server.exe'
}

foreach ($requiredFile in @($StatePath, $PlayerPath, $ServerPath, $ServerConfig)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "required recovery diagnostic file is missing: $requiredFile"
    }
}
if ([string]::IsNullOrWhiteSpace($env:IHOMELAND_QUALIFICATION_USERNAME) -or
    [string]::IsNullOrEmpty($env:IHOMELAND_QUALIFICATION_PASSWORD)) {
    throw 'qualification credentials are missing from the current process environment'
}

$PlayerPath = (Resolve-Path -LiteralPath $PlayerPath).Path
$ServerPath = (Resolve-Path -LiteralPath $ServerPath).Path
$ServerConfig = (Resolve-Path -LiteralPath $ServerConfig).Path
$StorageState = Get-Content -LiteralPath $StatePath -Raw -Encoding UTF8 | ConvertFrom-Json
[System.IO.Directory]::CreateDirectory($ProfileRoot) | Out-Null
$OwnedPlayer = $null
$OwnedServer = $null
$Succeeded = $false

# Wait-PlayerMarker只观察Development Player提交的稳定低敏事实，不参与产品状态推进。
function Wait-PlayerMarker {
    param(
        [string]$Marker,
        [int]$DeadlineSeconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($DeadlineSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if (Test-Path -LiteralPath $PlayerLog -PathType Leaf) {
            if (Select-String -LiteralPath $PlayerLog -SimpleMatch $Marker -Quiet) {
                return
            }
            $failure = Select-String -LiteralPath $PlayerLog -SimpleMatch '[IHOMELAND_QUALIFICATION] outcome=fail' |
                Select-Object -Last 1
            if ($null -ne $failure) {
                throw "Development Player rejected recovery: $($failure.Line)"
            }
        }
        if ($null -ne $OwnedPlayer) {
            $current = Get-Process -Id $OwnedPlayer.Id -ErrorAction SilentlyContinue
            if ($null -eq $current) {
                throw "Development Player exited before marker: $Marker"
            }
        }
        Start-Sleep -Milliseconds 250
    }
    throw "Development Player marker deadline elapsed: $Marker"
}

# Wait-OwnedProcessExit以有界OS事实替代Process.WaitForExit的句柄等待。
function Wait-OwnedProcessExit {
    param(
        [int]$ProcessId,
        [int]$DeadlineSeconds)
    $deadline = [DateTime]::UtcNow.AddSeconds($DeadlineSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($null -eq (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
            return
        }
        Start-Sleep -Milliseconds 250
    }
    throw "owned process exit deadline elapsed: PID=$ProcessId"
}

# Start-RecoveryServer用同一StorageRunId启动新进程，并验证三个listener都归该PID所有。
function Start-RecoveryServer {
    param([string]$Stage)
    $env:IHOMELAND_MYSQL_ADDRESS = "127.0.0.1:$($StorageState.mysqlPort)"
    $env:IHOMELAND_REDIS_ADDRESS = "127.0.0.1:$($StorageState.redisPort)"
    $env:IHOMELAND_MYSQL_PASSWORD = [IO.File]::ReadAllText(
        (Resolve-Path -LiteralPath (Join-Path $RepositoryRoot ".local\storage\$StorageRunId\mysql-password"))).Trim()
    $env:IHOMELAND_REDIS_PASSWORD = [IO.File]::ReadAllText(
        (Resolve-Path -LiteralPath (Join-Path $RepositoryRoot ".local\storage\$StorageRunId\redis-password"))).Trim()
    $env:IHOMELAND_WORLD_ADMISSION_KEY = 'replace-with-local-random-material-at-least-32-bytes'
    $process = Start-Process -FilePath $ServerPath -ArgumentList @('--config', $ServerConfig) `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $RunDirectory "$Stage.server.stdout.log") `
        -RedirectStandardError (Join-Path $RunDirectory "$Stage.server.stderr.log") `
        -PassThru
    Write-Host "[$Stage] server PID=$($process.Id)"
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($process.HasExited) {
            throw "$Stage server exited during startup"
        }
        $ports = @(Get-NetTCPConnection -OwningProcess $process.Id -State Listen -ErrorAction SilentlyContinue |
            Where-Object { $_.LocalPort -in @(8080, 8081, 8444) } |
            Select-Object -ExpandProperty LocalPort -Unique)
        if ($ports.Count -eq 3) {
            Write-Host "[$Stage] listeners=8080,8081,8444"
            return $process
        }
        Start-Sleep -Milliseconds 250
    }
    throw "$Stage server listener deadline elapsed"
}

try {
    # 只停止精确持有目标端口且可执行文件匹配的旧服务端，不枚举或终止无关进程。
    $listenerOwners = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
        Where-Object { $_.LocalPort -in @(8080, 8081, 8444) } |
        Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($listenerOwner in $listenerOwners) {
        $candidate = Get-Process -Id $listenerOwner -ErrorAction Stop
        if ($candidate.ProcessName -ne 'server' -or $candidate.Path -ne $ServerPath) {
            throw "target port is owned by an unexpected process: PID=$listenerOwner"
        }
        Stop-Process -Id $listenerOwner -Force
        Wait-OwnedProcessExit $listenerOwner 10
    }

    $OwnedServer = Start-RecoveryServer 'initial'
    $OwnedPlayer = Start-Process -FilePath $PlayerPath -ArgumentList @(
        '-batchmode',
        '-nographics',
        '-logFile', $PlayerLog,
        '-ihomelandQualificationMode', 'server-restart-recovery',
        '-ihomelandQualificationRecoverySignal', $SignalPath,
        '-ihomelandQualificationStorageRoot', $ProfileRoot) -PassThru
    $env:IHOMELAND_QUALIFICATION_USERNAME = $null
    $env:IHOMELAND_QUALIFICATION_PASSWORD = $null
    Write-Host "[player] PID=$($OwnedPlayer.Id)"

    Wait-PlayerMarker 'state=ready-for-server-stop scenario=server-restart-recovery' 90
    Stop-Process -Id $OwnedServer.Id -Force
    Wait-OwnedProcessExit $OwnedServer.Id 10
    $OwnedServer = $null
    Wait-PlayerMarker 'state=connection-lost scenario=server-restart-recovery' 90

    $OwnedServer = Start-RecoveryServer 'replacement'
    [System.IO.File]::WriteAllBytes($SignalPath, [byte[]]@())
    Wait-PlayerMarker 'outcome=pass scenario=server-restart-recovery' 90
    Wait-OwnedProcessExit $OwnedPlayer.Id 20
    $OwnedPlayer = $null
    $Succeeded = $true
    Write-Host "[pass] real Development Player server-restart recovery; run-id=$RunId"
    Write-Host "[artifacts] $RunDirectory"
    Write-Host "[replacement-server] PID=$($OwnedServer.Id)"
}
finally {
    $env:IHOMELAND_QUALIFICATION_USERNAME = $null
    $env:IHOMELAND_QUALIFICATION_PASSWORD = $null
    $env:IHOMELAND_MYSQL_PASSWORD = $null
    $env:IHOMELAND_REDIS_PASSWORD = $null
    $env:IHOMELAND_WORLD_ADMISSION_KEY = $null
    if ($null -ne $OwnedPlayer -and
        $null -ne (Get-Process -Id $OwnedPlayer.Id -ErrorAction SilentlyContinue)) {
        Stop-Process -Id $OwnedPlayer.Id -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $OwnedServer -and
        ((-not $Succeeded) -or $StopReplacementServer.IsPresent) -and
        $null -ne (Get-Process -Id $OwnedServer.Id -ErrorAction SilentlyContinue)) {
        Stop-Process -Id $OwnedServer.Id -Force -ErrorAction SilentlyContinue
    }
}
