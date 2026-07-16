[CmdletBinding()]
# 该入口创建相互隔离的 MySQL/Redis integration runtime，并只按 run-id ownership 清理资源。
param(
    # Action 选择完整验收、保留环境、显式清理、状态查询或纯契约检查。
    [ValidateSet("verify", "up", "down", "status", "contract", "fault")]
    [string]$Action = "verify",
    # RunId 只用于 down/status；格式固定为 32 位小写十六进制 GUID。
    [string]$RunId = "",
    # Fault 只在 Action=fault 时选择当前 run 的封闭资格故障动作。
    [ValidateSet("", "redis-flush", "redis-restart", "mysql-restart")]
    [string]$Fault = "",
    # TimeoutSeconds 是 setup/tests 或显式 down 共享的总预算；verify cleanup 另有最多 60 秒预算。
    [ValidateRange(30, 1800)]
    [int]$TimeoutSeconds = 300
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$StorageRoot = Join-Path $RepositoryRoot ".local\storage"
$OwnershipLabel = "com.ihomeland.storage.run"
# Cleanup 使用独立短预算，使主阶段耗尽 deadline 后仍能尝试回收，同时不会再次长期阻塞。
$CleanupTimeoutCeilingSeconds = 60

# Write-Stage 输出稳定阶段名，不打印 command line、password 或临时配置内容。
function Write-Stage {
    param([string]$Stage, [string]$Message)
    Write-Host "[$Stage] $Message"
}

# Read-ImageLocks 从 versions.yaml 的 infrastructure 直接标量读取锁定 image 与 linux/amd64 digest。
function Read-ImageLocks {
    $lines = Get-Content -LiteralPath (Join-Path $RepositoryRoot "versions.yaml") -Encoding utf8
    $insideInfrastructure = $false
    $component = ""
    $result = @{}
    foreach ($line in $lines) {
        if ($line -match '^infrastructure:\s*$') {
            $insideInfrastructure = $true
            continue
        }
        if ($insideInfrastructure -and $line -match '^[^\s]') {
            break
        }
        if ($insideInfrastructure -and $line -match '^  (mysql|redis):\s*$') {
            $component = $Matches[1]
            $result[$component] = @{}
            continue
        }
        if ($component -and $line -match '^    (image|linux_amd64_digest):\s*"([^"]+)"\s*$') {
            $result[$component][$Matches[1]] = $Matches[2]
        }
    }
    foreach ($name in @("mysql", "redis")) {
        if (-not $result.ContainsKey($name)) {
            throw "infrastructure.$name image lock is missing"
        }
        $image = $result[$name]["image"]
        $digest = $result[$name]["linux_amd64_digest"]
        if (-not $image -or $image -match ':latest$' -or $image -notmatch '^[a-z0-9./_-]+:[0-9][a-zA-Z0-9._-]*$') {
            throw "infrastructure.$name.image must use an explicit non-latest tag"
        }
        if (-not $digest -or $digest -notmatch '^sha256:[0-9a-f]{64}$') {
            throw "infrastructure.$name.linux_amd64_digest must be a locked SHA-256"
        }
    }
    return $result
}

# Convert-ToDigestReference 保留 registry/repository 并用目标平台 digest 替换 tag。
function Convert-ToDigestReference {
    param([string]$Image, [string]$Digest)
    $lastSlash = $Image.LastIndexOf('/')
    $lastColon = $Image.LastIndexOf(':')
    if ($lastColon -le $lastSlash) {
        throw "Image tag is missing"
    }
    return $Image.Substring(0, $lastColon) + "@" + $Digest
}

# New-Secret 使用系统 CSPRNG 创建不含 shell 分隔符的高熵临时凭据。
function New-Secret {
    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return ([Convert]::ToBase64String($bytes)).TrimEnd('=').Replace('+', 'A').Replace('/', 'B')
}

# Assert-RunId 防止用户输入进入路径或 Docker resource name 注入。
function Assert-RunId {
    param([string]$Value)
    if ($Value -notmatch '^[0-9a-f]{32}$') {
        throw "RunId must be a 32-character lowercase hexadecimal GUID"
    }
}

# Get-RunDirectory 返回经规范化且确定属于 .local/storage 的 run directory。
function Get-RunDirectory {
    param([string]$Value)
    Assert-RunId $Value
    $root = [System.IO.Path]::GetFullPath($StorageRoot).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath((Join-Path $StorageRoot $Value))
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Storage run directory escaped project local root"
    }
    return $candidate
}

# ConvertTo-NativeArgument 按 Windows CommandLineToArgvW 规则转义单个参数，保留空值、空格、引号和尾随反斜杠。
function ConvertTo-NativeArgument {
    param([AllowEmptyString()][string]$Value)
    if ($null -eq $Value) {
        $Value = ""
    }
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') {
        return $Value
    }
    $builder = [System.Text.StringBuilder]::new()
    [void]$builder.Append([char]34)
    $backslashCount = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $backslashCount++
            continue
        }
        if ($character -eq [char]34) {
            [void]$builder.Append(('\' * (($backslashCount * 2) + 1)))
            [void]$builder.Append([char]34)
            $backslashCount = 0
            continue
        }
        if ($backslashCount -gt 0) {
            [void]$builder.Append(('\' * $backslashCount))
            $backslashCount = 0
        }
        [void]$builder.Append($character)
    }
    if ($backslashCount -gt 0) {
        [void]$builder.Append(('\' * ($backslashCount * 2)))
    }
    [void]$builder.Append([char]34)
    return $builder.ToString()
}

# Invoke-NativeProcess 在 deadline 内执行单个 native process，并在 timeout、取消或异常退出路径终止仍存活的子进程。
# 返回的 stdout/stderr 只在调用边界消费；错误文本只包含稳定 operation，不回显可能携带 secret 的参数。
function Invoke-NativeProcess {
    param(
        [string]$Executable,
        [string[]]$Arguments,
        [DateTime]$Deadline,
        [string]$Operation
    )
    $remainingMilliseconds = [Math]::Ceiling(($Deadline - [DateTime]::UtcNow).TotalMilliseconds)
    if ($remainingMilliseconds -le 0) {
        throw "Native process deadline elapsed before start (operation=$Operation)"
    }
    $timeoutMilliseconds = [int][Math]::Min([int]::MaxValue, $remainingMilliseconds)
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Executable
    $startInfo.Arguments = (($Arguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    $started = $false
    $processFailure = $null
    $terminationFailure = ""
    $result = $null
    try {
        if (-not $process.Start()) {
            throw "Native process failed to start (operation=$Operation)"
        }
        $started = $true
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timedOut = -not $process.WaitForExit($timeoutMilliseconds)
        if ($timedOut) {
            try {
                $process.Kill()
            }
            catch {
                if (-not $process.HasExited) {
                    throw "Native process exceeded deadline and could not be terminated (operation=$Operation)"
                }
            }
            if (-not $process.WaitForExit(5000)) {
                throw "Native process exceeded deadline and did not terminate (operation=$Operation)"
            }
        }
        # 无参数 WaitForExit 确保 redirected streams 在读取结果前完成异步 drain。
        $process.WaitForExit()
        $standardOutput = $stdoutTask.GetAwaiter().GetResult()
        $standardError = $stderrTask.GetAwaiter().GetResult()
        if ($timedOut) {
            throw "Native process exceeded deadline (operation=$Operation)"
        }
        $result = [pscustomobject]@{
            ExitCode = $process.ExitCode
            StandardOutput = $standardOutput
            StandardError = $standardError
        }
    }
    catch {
        $processFailure = $_
    }
    finally {
        # Ctrl+C 或上层异常也必须回收 native child；终止失败会与主失败合并，不能静默遗留 orphan。
        if ($started -and -not $process.HasExited) {
            try {
                $process.Kill()
            }
            catch {
                $terminationFailure = "native child termination failed (operation=$Operation)"
            }
            if (-not $terminationFailure) {
                try {
                    if (-not $process.WaitForExit(5000)) {
                        $terminationFailure = "native child did not terminate (operation=$Operation)"
                    }
                }
                catch {
                    $terminationFailure = "native child termination wait failed (operation=$Operation)"
                }
            }
        }
        $process.Dispose()
    }
    if ($processFailure) {
        if ($terminationFailure) {
            throw "$($processFailure.Exception.Message); $terminationFailure"
        }
        throw $processFailure.Exception
    }
    if ($terminationFailure) {
        throw $terminationFailure
    }
    return $result
}

# Invoke-Docker 执行受 deadline 约束的 Docker CLI，并在失败时终止当前阶段，不把 secret 作为日志字符串输出。
function Invoke-Docker {
    param([string[]]$Arguments, [DateTime]$Deadline, [switch]$Capture)
    if ($Arguments.Count -eq 0) {
        throw "Docker operation is missing"
    }
    if ([DateTime]::UtcNow -ge $Deadline) {
        throw "Native process deadline elapsed before start (operation=$($Arguments[0]))"
    }
    $docker = (Get-Command docker.exe -CommandType Application -ErrorAction Stop).Source
    $result = Invoke-NativeProcess -Executable $docker -Arguments $Arguments -Deadline $Deadline -Operation $Arguments[0]
    if ($Capture) {
        if ($result.ExitCode -ne 0) {
            throw "Docker command failed"
        }
        return $result.StandardOutput.Trim()
    }
    if ($result.StandardError) {
        [Console]::Error.Write($result.StandardError)
    }
    if ($result.ExitCode -ne 0) {
        $imageReferences = @($Arguments | Where-Object { $_ -match '^[a-z0-9./_-]+@sha256:[0-9a-f]{64}$' }) -join ","
        throw "Docker command failed (operation=$($Arguments[0]), lockedImages=$imageReferences)"
    }
}

# Assert-ResourceOwnership 查询 Docker label，任何缺失或不匹配都拒绝删除。
function Assert-ResourceOwnership {
    param([ValidateSet("container", "network", "volume")][string]$Kind, [string]$Name, [string]$ExpectedRunId, [DateTime]$Deadline)
    $format = '{{json .Config.Labels}}'
    if ($Kind -eq "network" -or $Kind -eq "volume") {
        $format = '{{json .Labels}}'
    }
    $labels = (Invoke-Docker @("inspect", "--type", $Kind, "--format", $format, $Name) -Deadline $Deadline -Capture) | ConvertFrom-Json
    $property = $labels.PSObject.Properties[$OwnershipLabel]
    $actual = if ($property) { [string]$property.Value } else { "" }
    if ($actual -ne $ExpectedRunId) {
        throw "Refusing to remove $Kind without matching run ownership"
    }
}

# Wait-Healthy 等待单个 container 进入 healthy，并受全局 deadline 限制。
function Wait-Healthy {
    param([string]$Container, [DateTime]$Deadline)
    while ([DateTime]::UtcNow -lt $Deadline) {
        $state = Invoke-Docker @("inspect", "--type", "container", "--format", '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}', $Container) -Deadline $Deadline -Capture
        if ($state -eq "healthy") {
            return
        }
        if ($state -eq "exited" -or $state -eq "dead") {
            throw "Storage container exited before becoming healthy"
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Storage container health wait exceeded global timeout"
}

# Get-PublishedPort 读取 Docker 分配的 loopback host port，并拒绝非 loopback 映射。
function Get-PublishedPort {
    param([string]$Container, [int]$ContainerPort, [DateTime]$Deadline)
    $mapping = Invoke-Docker @("port", $Container, "$ContainerPort/tcp") -Deadline $Deadline -Capture
    $line = ($mapping -split "`n")[0].Trim()
    if ($line -notmatch '^(127\.0\.0\.1|\[::1\]):([0-9]+)$') {
        throw "Storage port was not published on loopback"
    }
    return [int]$Matches[2]
}

# Get-RandomLoopbackPort 让操作系统选择当前空闲 ephemeral port；Docker 随后固定绑定该端口，使 container restart 不改变 endpoint。
function Get-RandomLoopbackPort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
    }
    finally {
        $listener.Stop()
    }
}

# Save-State 只保存资源名、端口和 image identity；secret material 始终留在独立 ignored 文件。
function Save-State {
    param([string]$Directory, [hashtable]$State)
    $State | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $Directory "state.json") -Encoding utf8
}

# Load-State 从已验证 run directory 读取本次资源 manifest。
function Load-State {
    param([string]$Value)
    $directory = Get-RunDirectory $Value
    $path = Join-Path $directory "state.json"
    if (-not (Test-Path -LiteralPath $path)) {
        throw "Storage state manifest does not exist"
    }
    return Get-Content -LiteralPath $path -Raw -Encoding utf8 | ConvertFrom-Json
}

# Start-Storage 创建唯一 network/volume/containers，并将所有 host port 绑定 loopback 随机端口。
function Start-Storage {
    param([string]$Value, [DateTime]$Deadline)
    $directory = Get-RunDirectory $Value
    New-Item -ItemType Directory -Path $directory -Force | Out-Null
    $locks = Read-ImageLocks
    $prefix = "ih-storage-$Value"
    $network = "$prefix-net"
    $mysqlVolume = "$prefix-mysql-data"
    $mysqlContainer = "$prefix-mysql"
    $redisContainer = "$prefix-redis"
    $mysqlPort = Get-RandomLoopbackPort
    $redisPort = Get-RandomLoopbackPort
    if ($mysqlPort -eq $redisPort) {
        $redisPort = Get-RandomLoopbackPort
    }
    $provisionalState = @{
        runId = $Value; network = $network; mysqlVolume = $mysqlVolume; mysqlContainer = $mysqlContainer; redisContainer = $redisContainer
        mysqlPort = $mysqlPort; redisPort = $redisPort; mysqlImage = "pending"; redisImage = "pending"
    }
    Save-State $directory $provisionalState
    $mysqlPassword = New-Secret
    $mysqlRootPassword = New-Secret
    $redisPassword = New-Secret
    [System.IO.File]::WriteAllText((Join-Path $directory "mysql-password"), $mysqlPassword, [System.Text.UTF8Encoding]::new($false))
    [System.IO.File]::WriteAllText((Join-Path $directory "mysql-root-password"), $mysqlRootPassword, [System.Text.UTF8Encoding]::new($false))
    [System.IO.File]::WriteAllText((Join-Path $directory "redis-password"), $redisPassword, [System.Text.UTF8Encoding]::new($false))
    [System.IO.File]::WriteAllText((Join-Path $directory "redis.conf"), "bind 0.0.0.0`nprotected-mode yes`nappendonly yes`nrequirepass $redisPassword`n", [System.Text.UTF8Encoding]::new($false))

    $label = "$OwnershipLabel=$Value"
    Invoke-Docker @("network", "create", "--label", $label, $network) -Deadline $Deadline
    Invoke-Docker @("volume", "create", "--label", $label, $mysqlVolume) -Deadline $Deadline
    $mysqlImage = Convert-ToDigestReference $locks.mysql.image $locks.mysql.linux_amd64_digest
    $redisImage = Convert-ToDigestReference $locks.redis.image $locks.redis.linux_amd64_digest
    $mysqlPasswordPath = (Join-Path $directory "mysql-password")
    $mysqlRootPasswordPath = (Join-Path $directory "mysql-root-password")
    $redisConfigPath = (Join-Path $directory "redis.conf")
    Invoke-Docker @("run", "-d", "--platform", "linux/amd64", "--name", $mysqlContainer, "--network", $network, "--label", $label,
        "--mount", "type=volume,source=$mysqlVolume,target=/var/lib/mysql",
        "--mount", "type=bind,source=$mysqlPasswordPath,target=/run/secrets/mysql-password,readonly",
        "--mount", "type=bind,source=$mysqlRootPasswordPath,target=/run/secrets/mysql-root-password,readonly",
        "-e", "MYSQL_DATABASE=ihomeland", "-e", "MYSQL_USER=ihomeland", "-e", "MYSQL_PASSWORD_FILE=/run/secrets/mysql-password", "-e", "MYSQL_ROOT_PASSWORD_FILE=/run/secrets/mysql-root-password",
        "-p", "127.0.0.1:${mysqlPort}:3306", "--health-cmd", "mysqladmin ping -h 127.0.0.1 -uroot --password=`$(cat /run/secrets/mysql-root-password)", "--health-interval", "2s", "--health-timeout", "2s", "--health-retries", "60",
        $mysqlImage, "--default-time-zone=+00:00", "--sql-mode=STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION") -Deadline $Deadline
    Invoke-Docker @("run", "-d", "--platform", "linux/amd64", "--name", $redisContainer, "--network", $network, "--label", $label,
        "--mount", "type=bind,source=$redisConfigPath,target=/usr/local/etc/redis/redis.conf,readonly", "-p", "127.0.0.1:${redisPort}:6379",
        "--health-cmd", 'redis-cli -a $(awk ''/requirepass/{print $2}'' /usr/local/etc/redis/redis.conf) ping', "--health-interval", "2s", "--health-timeout", "2s", "--health-retries", "30",
        $redisImage, "redis-server", "/usr/local/etc/redis/redis.conf") -Deadline $Deadline
    Wait-Healthy $mysqlContainer $Deadline
    Wait-Healthy $redisContainer $Deadline
    $state = @{
        runId = $Value; network = $network; mysqlVolume = $mysqlVolume; mysqlContainer = $mysqlContainer; redisContainer = $redisContainer
        mysqlPort = Get-PublishedPort $mysqlContainer 3306 $Deadline; redisPort = Get-PublishedPort $redisContainer 6379 $Deadline
        mysqlImage = $mysqlImage; redisImage = $redisImage
    }
    Save-State $directory $state
    return $state
}

# Stop-Storage 按 container -> volume -> network 顺序清理，并在每次删除前重新验证 ownership label。
function Stop-Storage {
    param([string]$Value, [DateTime]$Deadline, [switch]$AllowMissing)
    $directory = Get-RunDirectory $Value
    if (-not (Test-Path -LiteralPath (Join-Path $directory "state.json"))) {
        if ($AllowMissing) {
            if (Test-Path -LiteralPath $directory) {
                Remove-Item -LiteralPath $directory -Recurse -Force
            }
            return
        }
        throw "Storage state manifest does not exist"
    }
    $state = Load-State $Value
    foreach ($container in @($state.redisContainer, $state.mysqlContainer)) {
        $existingNames = Invoke-Docker @("container", "ls", "-a", "--filter", "name=^/$container$", "--format", '{{.Names}}') -Deadline $Deadline -Capture
        if (($existingNames -split "`n") -contains $container) {
            Assert-ResourceOwnership "container" $container $Value $Deadline
            Invoke-Docker @("container", "rm", "-f", $container) -Deadline $Deadline
        }
    }
    $existingVolumes = Invoke-Docker @("volume", "ls", "--filter", "name=^$($state.mysqlVolume)$", "--format", '{{.Name}}') -Deadline $Deadline -Capture
    if (($existingVolumes -split "`n") -contains $state.mysqlVolume) {
        Assert-ResourceOwnership "volume" $state.mysqlVolume $Value $Deadline
        Invoke-Docker @("volume", "rm", $state.mysqlVolume) -Deadline $Deadline
    }
    $existingNetworks = Invoke-Docker @("network", "ls", "--filter", "name=^$($state.network)$", "--format", '{{.Name}}') -Deadline $Deadline -Capture
    if (($existingNetworks -split "`n") -contains $state.network) {
        Assert-ResourceOwnership "network" $state.network $Value $Deadline
        Invoke-Docker @("network", "rm", $state.network) -Deadline $Deadline
    }
    $root = [System.IO.Path]::GetFullPath($StorageRoot).TrimEnd('\') + '\'
    $resolved = [System.IO.Path]::GetFullPath($directory)
    if (-not $resolved.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove storage directory outside local root"
    }
    Remove-Item -LiteralPath $resolved -Recurse -Force
}

# Invoke-StorageFault 只操作 state manifest 登记且 ownership label 匹配的当前 run container。
function Invoke-StorageFault {
    param([string]$Value, [string]$Kind, [DateTime]$Deadline)
    $state = Load-State $Value
    switch ($Kind) {
        "redis-flush" {
            Assert-ResourceOwnership "container" $state.redisContainer $Value $Deadline
            # password 只在 container 内从受控 config 读取，不进入宿主 command line 或输出。
            Invoke-Docker @("exec", $state.redisContainer, "sh", "-c", 'redis-cli --no-auth-warning -a "$(awk ''/requirepass/{print $2}'' /usr/local/etc/redis/redis.conf)" FLUSHDB >/dev/null') -Deadline $Deadline
        }
        "redis-restart" {
            Assert-ResourceOwnership "container" $state.redisContainer $Value $Deadline
            Invoke-Docker @("container", "restart", $state.redisContainer) -Deadline $Deadline
            Wait-Healthy $state.redisContainer $Deadline
        }
        "mysql-restart" {
            Assert-ResourceOwnership "container" $state.mysqlContainer $Value $Deadline
            Invoke-Docker @("container", "restart", $state.mysqlContainer) -Deadline $Deadline
            Wait-Healthy $state.mysqlContainer $Deadline
        }
        default {
            throw "Storage fault action is invalid"
        }
    }
}

# Invoke-IntegrationTests 通过项目 Go wrapper 运行显式 storage_integration build tag。
function Invoke-IntegrationTests {
    param([object]$State, [string]$Directory, [DateTime]$Deadline)
    $env:IHOMELAND_STORAGE_INTEGRATION = "1"
    $env:IHOMELAND_TEST_MYSQL_ADDRESS = "127.0.0.1:$($State.mysqlPort)"
    $env:IHOMELAND_TEST_MYSQL_PASSWORD_FILE = Join-Path $Directory "mysql-password"
    $env:IHOMELAND_TEST_MYSQL_ROOT_PASSWORD_FILE = Join-Path $Directory "mysql-root-password"
    $env:IHOMELAND_TEST_MYSQL_CONTAINER = $State.mysqlContainer
    $env:IHOMELAND_TEST_REDIS_ADDRESS = "127.0.0.1:$($State.redisPort)"
    $env:IHOMELAND_TEST_REDIS_PASSWORD_FILE = Join-Path $Directory "redis-password"
    $env:IHOMELAND_TEST_REDIS_CONTAINER = $State.redisContainer
    $remainingSeconds = [Math]::Floor(($Deadline - [DateTime]::UtcNow).TotalSeconds)
    if ($remainingSeconds -lt 1) {
        throw "Storage verification exceeded global timeout before tests"
    }
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $RepositoryRoot "tools\go\go.ps1") test -tags=storage_integration -count=1 -p=1 "-timeout=$($remainingSeconds)s" ./cmd/server ./internal/app ./internal/storage/...
    if ($LASTEXITCODE -ne 0) {
        throw "Storage integration tests failed"
    }
}

# Invoke-ContractTests 验证版本锁、路径逃逸、run-id 和重复 cleanup 的纯逻辑边界。
function Invoke-ContractTests {
    $locks = Read-ImageLocks
    foreach ($name in @("mysql", "redis")) {
        $reference = Convert-ToDigestReference $locks[$name].image $locks[$name].linux_amd64_digest
        if ($reference -notmatch '@sha256:[0-9a-f]{64}$') {
            throw "Digest reference contract failed"
        }
    }
    $valid = [guid]::NewGuid().ToString("N")
    $directory = Get-RunDirectory $valid
    if (-not $directory.StartsWith([System.IO.Path]::GetFullPath($StorageRoot), [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Run directory contract failed"
    }
    try { Assert-RunId "..\escape"; throw "Invalid RunId was accepted" } catch { if ($_.Exception.Message -eq "Invalid RunId was accepted") { throw } }
    $runIds = 1..100 | ForEach-Object { [guid]::NewGuid().ToString("N") }
    if (($runIds | Select-Object -Unique).Count -ne 100) {
        throw "Concurrent run-id uniqueness contract failed"
    }
    $cleanupRunId = [guid]::NewGuid().ToString("N")
    New-Item -ItemType Directory -Path (Get-RunDirectory $cleanupRunId) -Force | Out-Null
    Stop-Storage $cleanupRunId ([DateTime]::UtcNow.AddSeconds(5)) -AllowMissing
    Stop-Storage $cleanupRunId ([DateTime]::UtcNow.AddSeconds(5)) -AllowMissing
    # 过期 deadline 模拟 daemon 无法参与 ownership cleanup；state 必须保留给恢复后的显式 down。
    $recoveryRunId = [guid]::NewGuid().ToString("N")
    $recoveryDirectory = Get-RunDirectory $recoveryRunId
    New-Item -ItemType Directory -Path $recoveryDirectory -Force | Out-Null
    Save-State $recoveryDirectory @{
        runId = $recoveryRunId
        network = "ih-storage-$recoveryRunId-net"
        mysqlVolume = "ih-storage-$recoveryRunId-mysql-data"
        mysqlContainer = "ih-storage-$recoveryRunId-mysql"
        redisContainer = "ih-storage-$recoveryRunId-redis"
        mysqlPort = 1
        redisPort = 1
        mysqlImage = "pending"
        redisImage = "pending"
    }
    try {
        $cleanupTimedOut = $false
        try {
            Stop-Storage $recoveryRunId ([DateTime]::UtcNow.AddSeconds(-1)) -AllowMissing
        }
        catch {
            if ($_.Exception.Message -ne "Native process deadline elapsed before start (operation=container)") {
                throw
            }
            $cleanupTimedOut = $true
        }
        if (-not $cleanupTimedOut -or -not (Test-Path -LiteralPath (Join-Path $recoveryDirectory "state.json"))) {
            throw "Timed out cleanup did not preserve recovery state"
        }
    }
    finally {
        Remove-Item -LiteralPath $recoveryDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
    New-Item -ItemType Directory -Path $StorageRoot -Force | Out-Null
    $timeoutPIDPath = Join-Path $StorageRoot ("contract timeout " + [guid]::NewGuid().ToString("N") + ".pid")
    $previousTimeoutPIDPath = $env:IHOMELAND_STORAGE_TIMEOUT_PID_PATH
    try {
        $env:IHOMELAND_STORAGE_TIMEOUT_PID_PATH = $timeoutPIDPath
        $timeoutCommand = '[System.IO.File]::WriteAllText($env:IHOMELAND_STORAGE_TIMEOUT_PID_PATH, [string]$PID); Start-Sleep -Seconds 30'
        $timedOut = $false
        try {
            Invoke-NativeProcess -Executable (Join-Path $PSHOME "powershell.exe") -Arguments @("-NoProfile", "-Command", $timeoutCommand) -Deadline ([DateTime]::UtcNow.AddSeconds(2)) -Operation "contract-timeout" | Out-Null
        }
        catch {
            if ($_.Exception.Message -ne "Native process exceeded deadline (operation=contract-timeout)") {
                throw
            }
            $timedOut = $true
        }
        if (-not $timedOut -or -not (Test-Path -LiteralPath $timeoutPIDPath)) {
            throw "Native process timeout contract failed"
        }
        $terminatedPID = [int](Get-Content -LiteralPath $timeoutPIDPath -Raw -Encoding ascii)
        if (Get-Process -Id $terminatedPID -ErrorAction SilentlyContinue) {
            throw "Timed out native process was not terminated"
        }
    }
    finally {
        $env:IHOMELAND_STORAGE_TIMEOUT_PID_PATH = $previousTimeoutPIDPath
        Remove-Item -LiteralPath $timeoutPIDPath -Force -ErrorAction SilentlyContinue
    }
    Write-Stage "OK" "storage contract checks passed"
}

if ($Action -eq "contract") {
    Invoke-ContractTests
    exit 0
}
if ($Action -eq "status") {
    $state = Load-State $RunId
    $state | ConvertTo-Json -Depth 4
    exit 0
}
if ($Action -eq "down") {
    Stop-Storage $RunId ([DateTime]::UtcNow.AddSeconds($TimeoutSeconds))
    Write-Stage "OK" "storage resources removed for run $RunId"
    exit 0
}
if ($Action -eq "fault") {
    Assert-RunId $RunId
    if (-not $Fault) {
        throw "Storage fault action is required"
    }
    Invoke-StorageFault $RunId $Fault ([DateTime]::UtcNow.AddSeconds($TimeoutSeconds))
    Write-Stage "OK" "storage fault completed for run $RunId"
    exit 0
}

$activeRunId = [guid]::NewGuid().ToString("N")
$deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
$runDirectory = Get-RunDirectory $activeRunId
$keepRequested = $Action -eq "up"
$keep = $false
$operationError = $null
$cleanupError = $null
try {
    Write-Stage "SETUP" "creating isolated storage run $activeRunId"
    $state = Start-Storage $activeRunId $deadline
    if ($keepRequested) {
        $keep = $true
        Write-Stage "OK" "storage ready; RunId=$activeRunId MySQL=127.0.0.1:$($state.mysqlPort) Redis=127.0.0.1:$($state.redisPort)"
    }
    else {
        Invoke-IntegrationTests $state $runDirectory $deadline
        Write-Stage "OK" "storage verification passed"
    }
}
catch {
    # 先保存主阶段失败，避免 finally 中的 cleanup failure 覆盖原始验收结论。
    $operationError = $_
}
finally {
    if (-not $keep) {
        Write-Stage "CLEANUP" "removing isolated storage run $activeRunId"
        try {
            $cleanupDeadline = [DateTime]::UtcNow.AddSeconds([Math]::Min($TimeoutSeconds, $CleanupTimeoutCeilingSeconds))
            Stop-Storage $activeRunId $cleanupDeadline -AllowMissing
        }
        catch {
            $cleanupError = $_
        }
    }
}
if ($operationError) {
    if ($cleanupError) {
        throw "Storage operation failed: $($operationError.Exception.Message); cleanup also failed: $($cleanupError.Exception.Message)"
    }
    throw $operationError.Exception
}
if ($cleanupError) {
    throw $cleanupError.Exception
}
