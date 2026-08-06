# 该入口是唯一可以聚合客户端v1资格证据的owner；所有运行资源限制在精确run-id目录。
[CmdletBinding()]
param(
    # Action区分无副作用校验、定向诊断、自动门禁、人工准备与最终聚合。
    [ValidateSet("validate", "diagnose", "automatic", "soak", "prepare-manual", "finalize")]
    [string]$Action = "validate",
    # Scenario只为diagnose选择闭合registry中的稳定开发场景。
    [string]$Scenario = "",
    # UnityEditorPath必须指向ProjectVersion锁定版本的Unity.exe。
    [string]$UnityEditorPath = "",
    # RunId用于继续本入口创建的同类运行；diagnose与正式资格目录彼此隔离。
    [ValidatePattern('^[0-9a-f]{32}$')]
    [string]$RunId = "",
    # EvidencePath只在finalize时接受schema-versioned证据文件。
    [string]$EvidencePath = "",
    # TimeoutSeconds限制本次入口的全局运行预算；cleanup另有独立预算。
    [ValidateRange(120, 7200)]
    [int]$TimeoutSeconds = 3600
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ClientRoot = Join-Path $RepositoryRoot "client"
$ContractRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\client-qualification"
$ManifestPath = Join-Path $ContractRoot "manifest.json"
$RegistryPath = Join-Path $ContractRoot "automatic-registry.json"
$DiagnosticRegistryPath = Join-Path $ContractRoot "diagnostic-registry.json"
$ClientContractIdentityModule = Join-Path $PSScriptRoot "ClientContractIdentity.psm1"
Import-Module $ClientContractIdentityModule -Force
$QualificationRoot = if ($Action -eq "diagnose") {
    Join-Path $RepositoryRoot ".local\client-diagnostics"
}
else {
    Join-Path $RepositoryRoot ".local\client-qualification"
}
$StartedAt = [DateTime]::UtcNow
$Deadline = $StartedAt.AddSeconds($TimeoutSeconds)
# Cleanup预算独立于主deadline，保证首个失败不会阻止精确资源回收。
$CleanupDeadlineSeconds = 30
# Process等待切片只用于工具deadline观察，不参与产品状态判定。
$ProcessObservationSliceMilliseconds = 200
# Protocol clean generation与parity共享的工具阶段预算。
$ProtocolStageBudgetMilliseconds = 120000
# 全量单一Unity测试平台阶段预算。
$UnityTestStageBudgetMilliseconds = 600000
# 单个Windows Player profile的构建预算。
$UnityBuildStageBudgetMilliseconds = 900000
# Player smoke必须观察到完整App Scope进入Running，不能仅以进程仍存活判定成功。
$PlayerSmokeObservationSliceMilliseconds = 250
$PowerShellHostPath = (Get-Process -Id $PID).Path
$OwnedProcesses = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$StageResults = [System.Collections.Generic.List[object]]::new()
$PrimaryFailure = ""
$CleanupFailure = ""

if ($Action -eq "diagnose") {
    if ([string]::IsNullOrWhiteSpace($Scenario) -or
        $Scenario -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$') {
        throw "diagnostic scenario is required and must use stable id grammar"
    }
}
elseif (-not [string]::IsNullOrWhiteSpace($Scenario)) {
    throw "Scenario is only valid for diagnose"
}

if (-not $RunId) { $RunId = [guid]::NewGuid().ToString("N") }
$RunDirectory = Join-Path $QualificationRoot $RunId
$ReportPath = Join-Path $RunDirectory "report.json"
$AutomaticEvidencePath = Join-Path $RunDirectory "automatic-evidence.json"

# Write-Stage只输出稳定阶段，不输出路径、PID、endpoint或原始异常。
function Write-Stage {
    param([string]$ID, [string]$Message)
    Write-Host "[$ID] $Message"
}

# Assert-RunDirectory阻止后续创建或清理逃逸ignored资格根目录。
function Assert-RunDirectory {
    $root = [System.IO.Path]::GetFullPath($QualificationRoot).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($RunDirectory)
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "client qualification run directory escaped local root"
    }
}

# Assert-Remaining在每个外部阶段前执行全局deadline检查。
function Assert-Remaining {
    if ([DateTime]::UtcNow -ge $Deadline) {
        throw "client qualification global deadline elapsed"
    }
}

# Assert-ClosedProperties拒绝JSON对象的未知字段。
function Assert-ClosedProperties {
    param([object]$Value, [string[]]$Allowed, [string]$Context)
    $unknown = @($Value.PSObject.Properties.Name | Where-Object { $_ -notin $Allowed })
    if ($unknown.Count -gt 0) { throw "$Context contains unknown fields" }
}

# Read-QualificationManifest执行闭合字段、grammar、唯一性与依赖引用校验。
function Read-QualificationManifest {
    $manifest = Get-Content -LiteralPath $ManifestPath -Raw -Encoding utf8 | ConvertFrom-Json
    Assert-ClosedProperties $manifest @("schemaVersion", "qualificationVersion", "scenarios") "manifest"
    if ($manifest.schemaVersion -ne 1 -or $manifest.qualificationVersion -ne "client-v1") {
        throw "client qualification manifest identity is invalid"
    }

    $ids = @{}
    $groups = @("contract", "session", "control", "gameplay", "ui-scene", "build", "soak", "product", "fault", "governance")
    $executions = @("tool", "editmode", "playmode", "player-automatic", "player-manual")
    $profiles = @("source", "development", "release")
    $owners = @("qualification-tool", "unity-editmode", "unity-playmode", "player-runner", "operator")
    foreach ($scenario in @($manifest.scenarios)) {
        Assert-ClosedProperties $scenario @("id", "group", "mandatory", "execution", "preconditions", "budgetMs", "expectedOutcome", "buildProfiles", "evidenceOwner") "scenario"
        if ([string]$scenario.id -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$' -or $ids.ContainsKey([string]$scenario.id)) {
            throw "client qualification scenario id is invalid or duplicate"
        }
        $ids[[string]$scenario.id] = $true
        if ($scenario.group -notin $groups -or $scenario.execution -notin $executions -or
            $scenario.evidenceOwner -notin $owners -or $scenario.expectedOutcome -ne "pass" -or
            -not [bool]$scenario.mandatory -or [int64]$scenario.budgetMs -lt 1000 -or
            [int64]$scenario.budgetMs -gt 1800000) {
            throw "client qualification scenario contract is invalid"
        }
        $scenarioProfiles = @($scenario.buildProfiles)
        if ($scenarioProfiles.Count -eq 0 -or @($scenarioProfiles | Where-Object { $_ -notin $profiles }).Count -gt 0 -or
            @($scenarioProfiles | Select-Object -Unique).Count -ne $scenarioProfiles.Count) {
            throw "client qualification build profile contract is invalid"
        }
    }

    foreach ($scenario in @($manifest.scenarios)) {
        foreach ($precondition in @($scenario.preconditions)) {
            if (-not $ids.ContainsKey([string]$precondition) -or $precondition -eq $scenario.id) {
                throw "client qualification precondition is unknown or recursive"
            }
        }
    }
    return $manifest
}

# Assert-RegistryCompleteness双向拒绝隐藏runner和缺少runner的自动mandatory场景。
function Assert-RegistryCompleteness {
    param([object]$Manifest)
    $registry = Get-Content -LiteralPath $RegistryPath -Raw -Encoding utf8 | ConvertFrom-Json
    Assert-ClosedProperties $registry @("schemaVersion", "entries") "automatic registry"
    if ($registry.schemaVersion -ne 1) { throw "automatic registry schema is invalid" }
    $registered = @{}
    $runners = @("protocol-and-static", "unity-editmode", "unity-playmode", "unity-build", "release-scan", "player-smoke", "player-soak", "redaction", "governance")
    foreach ($entry in @($registry.entries)) {
        Assert-ClosedProperties $entry @("scenarioId", "runner", "selectors") "automatic registry entry"
        if ($registered.ContainsKey([string]$entry.scenarioId)) { throw "automatic registry contains duplicate scenario" }
        $selectors = @($entry.selectors)
        if ($entry.runner -notin $runners -or $selectors.Count -eq 0 -or
            @($selectors | Where-Object { -not $_ -or $_ -notmatch '^[A-Za-z0-9_.:/-]+$' }).Count -gt 0 -or
            @($selectors | Select-Object -Unique).Count -ne $selectors.Count) {
            throw "automatic registry runner contract is invalid"
        }
        $registered[[string]$entry.scenarioId] = [string]$entry.runner
    }
    $automatic = @($Manifest.scenarios | Where-Object { $_.execution -ne "player-manual" })
    foreach ($scenario in $automatic) {
        if (-not $registered.ContainsKey([string]$scenario.id)) { throw "automatic mandatory scenario has no runner" }
    }
    foreach ($id in @($registered.Keys)) {
        if (@($automatic | Where-Object { $_.id -eq $id }).Count -ne 1) { throw "automatic registry contains hidden runner" }
    }
    return $registry
}

# Read-DiagnosticRegistry验证开发诊断场景只引用现有Unity自动测试owner，不能发明平行selector。
function Read-DiagnosticRegistry {
    param([object]$AutomaticRegistry)
    $registry = Get-Content -LiteralPath $DiagnosticRegistryPath -Raw -Encoding utf8 | ConvertFrom-Json
    Assert-ClosedProperties $registry @("schemaVersion", "scenarios") "diagnostic registry"
    if ($registry.schemaVersion -ne 1) { throw "diagnostic registry schema is invalid" }

    $automaticByID = @{}
    foreach ($entry in @($AutomaticRegistry.entries)) {
        $automaticByID[[string]$entry.scenarioId] = $entry
    }

    $ids = @{}
    foreach ($scenario in @($registry.scenarios)) {
        Assert-ClosedProperties $scenario @("id", "automaticScenarios", "connectionGuide", "manualSteps") "diagnostic scenario"
        $id = [string]$scenario.id
        $automaticScenarios = @($scenario.automaticScenarios)
        $manualSteps = @($scenario.manualSteps)
        if ($id -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$' -or $ids.ContainsKey($id) -or
            $automaticScenarios.Count -eq 0 -or $manualSteps.Count -eq 0 -or
            @($automaticScenarios | Select-Object -Unique).Count -ne $automaticScenarios.Count -or
            @($automaticScenarios | Where-Object { -not $_ -or $_ -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$' }).Count -gt 0) {
            throw "diagnostic scenario contract is invalid"
        }
        $stepIDs = @{}
        foreach ($manualStep in $manualSteps) {
            Assert-ClosedProperties $manualStep @("id", "instruction") "diagnostic manual step"
            $stepID = [string]$manualStep.id
            $instruction = [string]$manualStep.instruction
            if ($stepID -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$' -or $stepIDs.ContainsKey($stepID) -or
                [string]::IsNullOrWhiteSpace($instruction) -or $instruction.Length -gt 160 -or
                $instruction.Contains("`r") -or $instruction.Contains("`n")) {
                throw "diagnostic manual step contract is invalid"
            }
            $stepIDs[$stepID] = $true
        }
        if ($null -ne $scenario.connectionGuide) {
            $guide = $scenario.connectionGuide
            Assert-ClosedProperties $guide @("role", "channel", "remotePort", "protectedRemotePort") "diagnostic connection guide"
            $expectedRemotePort = if ($guide.channel -eq "control") { 8080 } else { 8444 }
            $expectedProtectedPort = if ($guide.channel -eq "control") { 8444 } else { 8080 }
            if ($guide.role -notin @("owner", "visitor") -or
                $guide.channel -notin @("control", "gameplay") -or
                [int]$guide.remotePort -ne $expectedRemotePort -or
                [int]$guide.protectedRemotePort -ne $expectedProtectedPort) {
                throw "diagnostic connection guide is invalid"
            }
        }
        $ids[$id] = $true
        foreach ($automaticID in $automaticScenarios) {
            $automaticKey = [string]$automaticID
            if (-not $automaticByID.ContainsKey($automaticKey) -or
                $automaticByID[$automaticKey].runner -notin @("unity-editmode", "unity-playmode")) {
                throw "diagnostic scenario references unsupported automatic owner"
            }
        }
    }
    return $registry
}

# Resolve-DiagnosticScenario按精确稳定ID选择单个开发诊断，不允许模糊匹配或默认全量运行。
function Resolve-DiagnosticScenario {
    param([object]$Registry, [string]$ScenarioID)
    $matches = @($Registry.scenarios | Where-Object { $_.id -eq $ScenarioID })
    if ($matches.Count -ne 1) { throw "diagnostic scenario is unknown" }
    return $matches[0]
}

# Get-DiagnosticUnitySelectors从automatic registry汇总指定runner的唯一fixture selector。
function Get-DiagnosticUnitySelectors {
    param([object]$ScenarioContract, [object]$AutomaticRegistry, [string]$Runner)
    $selectors = @()
    foreach ($automaticID in @($ScenarioContract.automaticScenarios)) {
        $entry = @($AutomaticRegistry.entries | Where-Object {
                $_.scenarioId -eq $automaticID -and $_.runner -eq $Runner
            })
        if ($entry.Count -eq 1) { $selectors += @($entry[0].selectors) }
    }
    return @($selectors | Sort-Object -Unique)
}

# Get-BuildDigest绑定完整Player目录，不依赖文件枚举返回顺序。
function Get-BuildDigest {
    param([string]$Directory)
    $root = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\') + '\'
    $builder = [System.Text.StringBuilder]::new()
    foreach ($file in @(Get-ChildItem -LiteralPath $Directory -File -Recurse | Sort-Object FullName)) {
        $relative = $file.FullName.Substring($root.Length).Replace('\', '/')
        $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        [void]$builder.Append($relative).Append(':').Append($hash).Append("`n")
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant() }
    finally { $sha.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
}

# Invoke-OwnedProcess只等待和终止本入口启动的精确Process对象。
function Invoke-OwnedProcess {
    param([string]$FilePath, [string[]]$Arguments, [int]$BudgetMs, [string]$Stage)
    Assert-Remaining
    $stageStarted = [DateTime]::UtcNow
    $safeStage = $Stage -replace '[^a-z0-9-]', '-'
    $stdout = Join-Path $RunDirectory "$safeStage.stdout.log"
    $stderr = Join-Path $RunDirectory "$safeStage.stderr.log"
    $previousModulePath = $env:PSModulePath
    try {
        if ([System.IO.Path]::GetFileName($FilePath) -ieq "powershell.exe") {
            # Windows PowerShell子进程必须优先解析自身系统模块，不能误载同机PowerShell 7同名模块。
            $windowsModuleRoot = Join-Path $env:WINDIR "System32\WindowsPowerShell\v1.0\Modules"
            $moduleEntries = @($previousModulePath -split ';' | Where-Object {
                -not [string]::IsNullOrWhiteSpace($_) -and
                -not [string]::Equals($_.TrimEnd('\'), $windowsModuleRoot.TrimEnd('\'), [System.StringComparison]::OrdinalIgnoreCase)
            })
            $env:PSModulePath = (@($windowsModuleRoot) + $moduleEntries) -join ';'
        }
        $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    }
    finally {
        $env:PSModulePath = $previousModulePath
    }
    # Windows PowerShell 5.1只有在子进程存活时取得Handle，稍后才能可靠读取ExitCode。
    $null = $process.Handle
    $OwnedProcesses.Add($process)
    $stageDeadline = [DateTime]::UtcNow.AddMilliseconds($BudgetMs)
    try {
        while (-not $process.WaitForExit($ProcessObservationSliceMilliseconds)) {
            if ([DateTime]::UtcNow -ge $stageDeadline -or [DateTime]::UtcNow -ge $Deadline) {
                try { Stop-Process -Id $process.Id -Force -ErrorAction Stop }
                catch { $script:CleanupFailure = "owned-process-cleanup-failed" }
                throw "$Stage deadline elapsed"
            }
        }
        # Windows PowerShell 5.1在重定向stdout/stderr时需要无参WaitForExit完成异步流收尾，之后ExitCode才可靠。
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw "$Stage failed" }
        $StageResults.Add([ordered]@{ id = $safeStage; outcome = "pass"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $stageStarted).TotalMilliseconds) })
    }
    catch {
        $StageResults.Add([ordered]@{ id = $safeStage; outcome = "fail"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $stageStarted).TotalMilliseconds) })
        throw
    }
}

# Test-IsFullyQualifiedWindowsPath在Windows PowerShell 5.1可用的API上验证drive或UNC绝对路径。
function Test-IsFullyQualifiedWindowsPath {
    param([AllowNull()][string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return $false }

    $normalized = $Path.Replace('/', '\')
    try { $root = [System.IO.Path]::GetPathRoot($normalized) }
    catch { return $false }

    if ($root -match '^[a-zA-Z]:\\$') { return $true }
    return $root -match '^\\\\[^\\]+\\[^\\]+\\?$'
}

# Assert-LockedUnity验证命令行Editor与ProjectVersion完全一致。
function Assert-LockedUnity {
    if (-not (Test-IsFullyQualifiedWindowsPath $UnityEditorPath) -or
        -not (Test-Path -LiteralPath $UnityEditorPath -PathType Leaf)) {
        throw "UnityEditorPath must be an existing absolute Unity.exe"
    }
    $versionLine = Get-Content -LiteralPath (Join-Path $ClientRoot "ProjectSettings\ProjectVersion.txt") -Encoding utf8 | Select-Object -First 1
    $version = ($versionLine -split ':', 2)[1].Trim()
    $normalized = [System.IO.Path]::GetFullPath($UnityEditorPath).Replace('/', '\')
    if ($normalized -notmatch [regex]::Escape("\$version\")) { throw "UnityEditorPath does not match ProjectVersion" }
}

# New-UnityCommandArguments区分Test Framework与同步Editor method的退出所有权。
function New-UnityCommandArguments {
    param([string[]]$Arguments, [string]$Stage)
    $common = @("-batchmode", "-nographics", "-projectPath", $ClientRoot, "-logFile", (Join-Path $RunDirectory "$Stage.unity.log"))
    # Test Framework在下一次Editor update启动测试并在完成后自行退出；提前传入-quit会在首轮导入后跳过测试。
    if (-not ($Arguments -contains "-runTests")) { $common += "-quit" }
    return @($common + $Arguments)
}

# Invoke-Unity运行无交互Editor阶段并把原始日志限制在ignored run目录。
function Invoke-Unity {
    param([string[]]$Arguments, [int]$BudgetMs, [string]$Stage)
    Invoke-OwnedProcess $UnityEditorPath (New-UnityCommandArguments $Arguments $Stage) $BudgetMs $Stage
}

# Invoke-PlayerSmoke启动真实Player并等待低敏App Scope运行标记，随后只关闭当前精确Process。
function Invoke-PlayerSmoke {
    param([string]$BuildRoot, [string]$Stage, [int]$BudgetMs, [string[]]$AdditionalArguments)
    Assert-Remaining
    $executables = @(Get-ChildItem -LiteralPath $BuildRoot -Filter "iHomeland.exe" -File -Recurse)
    if ($executables.Count -ne 1) { throw "$Stage player executable is missing or ambiguous" }

    $safeStage = $Stage -replace '[^a-z0-9-]', '-'
    $stageStarted = [DateTime]::UtcNow
    $playerLog = Join-Path $RunDirectory "$safeStage.player.log"
    $stdout = Join-Path $RunDirectory "$safeStage.stdout.log"
    $stderr = Join-Path $RunDirectory "$safeStage.stderr.log"
    $arguments = @("-batchmode", "-nographics", "-logFile", $playerLog) + @($AdditionalArguments)
    $process = Start-Process -FilePath $executables[0].FullName -ArgumentList $arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    # 固定原生handle，保证Player在观察循环中退出后仍可读取稳定进程状态。
    $null = $process.Handle
    $OwnedProcesses.Add($process)
    $stageDeadline = [DateTime]::UtcNow.AddMilliseconds($BudgetMs)
    try {
        $runningObserved = $false
        while (-not $runningObserved) {
            if ($process.HasExited) { throw "$Stage player exited before App Scope became ready" }
            if ([DateTime]::UtcNow -ge $stageDeadline -or [DateTime]::UtcNow -ge $Deadline) {
                throw "$Stage deadline elapsed"
            }
            if (Test-Path -LiteralPath $playerLog -PathType Leaf) {
                $runningObserved = Select-String -LiteralPath $playerLog -SimpleMatch "[IHOMELAND_APP] state=running" -Quiet
            }
            if (-not $runningObserved) {
                Start-Sleep -Milliseconds $PlayerSmokeObservationSliceMilliseconds
            }
        }

        $process.Refresh()
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction Stop
        }
        if (-not $process.WaitForExit($CleanupDeadlineSeconds * 1000)) {
            throw "$Stage player did not exit after owned stop"
        }
        $StageResults.Add([ordered]@{ id = $safeStage; outcome = "pass"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $stageStarted).TotalMilliseconds) })
    }
    catch {
        $StageResults.Add([ordered]@{ id = $safeStage; outcome = "fail"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $stageStarted).TotalMilliseconds) })
        throw
    }
}

# Assert-ReleaseSmokeProfileClean避免Release smoke轮换操作者现有default refresh lineage。
function Assert-ReleaseSmokeProfileClean {
    $defaultRoot = Join-Path ([Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)) "AppData\LocalLow\Jinwiforz\iHomeland\secure-session\default"
    foreach ($name in @("session.v1.bin", "session.v1.tmp", "session.v1.retired")) {
        if (Test-Path -LiteralPath (Join-Path $defaultRoot $name) -PathType Leaf) {
            throw "release smoke requires a clean Windows user profile"
        }
    }
}

# Invoke-PlayerSoak运行Development-only真实产品graph；credential只从继承环境进入Player内存。
function Invoke-PlayerSoak {
    param([string]$BuildRoot, [string]$StorageRoot)
    if ([string]::IsNullOrWhiteSpace($env:IHOMELAND_QUALIFICATION_USERNAME) -or
        [string]::IsNullOrEmpty($env:IHOMELAND_QUALIFICATION_PASSWORD)) {
        throw "player soak credentials are missing"
    }

    $executables = @(Get-ChildItem -LiteralPath $BuildRoot -Filter "iHomeland.exe" -File -Recurse)
    if ($executables.Count -ne 1) { throw "development soak player executable is missing or ambiguous" }
    [System.IO.Directory]::CreateDirectory($StorageRoot) | Out-Null
    $playerLog = Join-Path $RunDirectory "five-minute-recovery-soak.player.log"
    Invoke-OwnedProcess $executables[0].FullName @(
        "-batchmode",
        "-nographics",
        "-logFile", $playerLog,
        "-ihomelandDataProfile", "qualification-soak",
        "-ihomelandQualificationStorageRoot", $StorageRoot,
        "-ihomelandQualificationMode", "soak"
    ) 420000 "five-minute-recovery-soak"
    if (-not (Test-Path -LiteralPath $playerLog -PathType Leaf) -or
        -not (Select-String -LiteralPath $playerLog -SimpleMatch "[IHOMELAND_QUALIFICATION] outcome=pass scenario=five-minute-recovery-soak" -Quiet)) {
        throw "development soak result marker is missing"
    }
}

# Invoke-PlayerStorageCleanup只让Development Player中的secure store owner删除精确profile record。
function Invoke-PlayerStorageCleanup {
    param([string]$BuildRoot, [string]$StorageRoot, [string[]]$Profiles)
    $executables = @(Get-ChildItem -LiteralPath $BuildRoot -Filter "iHomeland.exe" -File -Recurse)
    if ($executables.Count -ne 1) { throw "storage cleanup player executable is missing or ambiguous" }
    try {
        foreach ($profile in @($Profiles)) {
            $log = Join-Path $RunDirectory "storage-cleanup-$profile.player.log"
            Invoke-OwnedProcess $executables[0].FullName @(
                "-batchmode",
                "-nographics",
                "-logFile", $log,
                "-ihomelandDataProfile", $profile,
                "-ihomelandQualificationStorageRoot", $StorageRoot,
                "-ihomelandQualificationMode", "cleanup"
            ) 90000 "storage-cleanup-$profile"
            if (-not (Test-Path -LiteralPath $log -PathType Leaf) -or
                -not (Select-String -LiteralPath $log -SimpleMatch "[IHOMELAND_QUALIFICATION] outcome=pass scenario=storage-cleanup" -Quiet)) {
                throw "storage cleanup result marker is missing"
            }
        }

        foreach ($profile in @($Profiles)) {
            $profileRoot = Join-Path $StorageRoot (Join-Path "secure-session" $profile)
            foreach ($name in @("session.v1.bin", "session.v1.tmp", "session.v1.retired")) {
                if (Test-Path -LiteralPath (Join-Path $profileRoot $name) -PathType Leaf) {
                    throw "secure session cleanup left an owned record"
                }
            }
        }
    }
    catch {
        $script:CleanupFailure = "secure-session-cleanup-failed"
        throw
    }
}

# Assert-PlayerLogRedaction拒绝资格Player日志泄漏当前账号、密码或admission/ticket header形态。
function Assert-PlayerLogRedaction {
    param([string[]]$SensitiveValues)
    $stageStarted = [DateTime]::UtcNow
    foreach ($log in @(Get-ChildItem -LiteralPath $RunDirectory -Filter "*.player.log" -File)) {
        $contents = [System.IO.File]::ReadAllText($log.FullName)
        foreach ($sensitive in @($SensitiveValues)) {
            if (-not [string]::IsNullOrEmpty($sensitive) -and $contents.Contains($sensitive)) {
                throw "player log redaction failed"
            }
        }
        if ($contents -match 'wad1_[A-Za-z0-9_-]{16,}' -or
            $contents -match '(?i)Authorization\s*:\s*(Bearer|Ticket)\s+') {
            throw "player log credential pattern detected"
        }
    }
    $StageResults.Add([ordered]@{ id = "credential-redaction"; outcome = "pass"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $stageStarted).TotalMilliseconds) })
}

# Invoke-DocumentationGovernance验证OpenSpec、diff whitespace、必需文档和禁止tracked输出。
function Invoke-DocumentationGovernance {
    Invoke-OwnedProcess $PowerShellHostPath @("-NoProfile", "-File", (Join-Path $RepositoryRoot "tools\client-qualification\client-qualification.tests.ps1")) 120000 "qualification-tool-tests"
    Invoke-OwnedProcess "cmd.exe" @(
        "/d",
        "/c",
        "openspec.cmd validate --all --strict --no-interactive"
    ) 120000 "openspec-strict"
    Invoke-OwnedProcess "git.exe" @("-C", $RepositoryRoot, "diff", "--check") 60000 "git-diff-check"
    Invoke-OwnedProcess "git.exe" @("-C", $RepositoryRoot, "diff", "--cached", "--check") 60000 "git-cached-diff-check"
    $requiredDocuments = @(
        "docs/client-v1-qualification.md",
        "docs/client-architecture.md",
        "docs/client-integration.md",
        "docs/client-ui-architecture.md",
        "docs/file-structure.md",
        "docs/workflow.md",
        "docs/technology-versions.md",
        "client/README.md"
    )
    foreach ($relative in $requiredDocuments) {
        if (-not (Test-Path -LiteralPath (Join-Path $RepositoryRoot $relative) -PathType Leaf)) {
            throw "qualification governance document is missing"
        }
    }
    $forbiddenTracked = @(& git -C $RepositoryRoot ls-files -- "client/Library" "client/Temp" "client/UserSettings" "client/Assets/App/Generated" ".local")
    if ($LASTEXITCODE -ne 0 -or $forbiddenTracked.Count -ne 0) {
        throw "qualification governance found tracked generated or local output"
    }
}

# Assert-UnityTestResults拒绝missing、failed、inconclusive与skipped测试结果，并修正进程零退出后的阶段结论。
function Assert-UnityTestResults {
    param([string]$Path, [string]$Stage)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        $stageResult = @($StageResults | Where-Object { $_.id -eq $Stage } | Select-Object -Last 1)
        if ($stageResult.Count -eq 1) { $stageResult[0].outcome = "fail" }
        throw "$Stage Unity Test Framework did not produce a result"
    }
    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding utf8
    $run = $document.SelectSingleNode("/test-run")
    if ($null -eq $run -or $run.result -ne "Passed" -or [int]$run.failed -ne 0 -or
        [int]$run.skipped -ne 0 -or [int]$run.inconclusive -ne 0) {
        $stageResult = @($StageResults | Where-Object { $_.id -eq $Stage } | Select-Object -Last 1)
        if ($stageResult.Count -eq 1) { $stageResult[0].outcome = "fail" }
        throw "$Stage Unity test result is not completely passed"
    }
}

# Assert-UnityScenarioMappings证明每个Unity场景登记的fixture真实存在且全部通过。
function Assert-UnityScenarioMappings {
    param([string]$Path, [object]$Registry, [string]$Runner)
    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding utf8
    $cases = @($document.SelectNodes("//test-case"))
    foreach ($entry in @($Registry.entries | Where-Object { $_.runner -eq $Runner })) {
        foreach ($selector in @($entry.selectors)) {
            $prefix = [string]$selector + "."
            $matches = @($cases | Where-Object { ([string]$_.fullname).StartsWith($prefix, [System.StringComparison]::Ordinal) })
            if ($matches.Count -eq 0 -or @($matches | Where-Object { $_.result -ne "Passed" }).Count -gt 0) {
                throw "Unity scenario selector is missing or not passed"
            }
        }
    }
}

# Assert-DiagnosticUnitySelectorsPassed验证定向结果确实包含每个登记fixture且全部通过。
function Assert-DiagnosticUnitySelectorsPassed {
    param([string]$Path, [string[]]$Selectors)
    [xml]$document = Get-Content -LiteralPath $Path -Raw -Encoding utf8
    $cases = @($document.SelectNodes("//test-case"))
    foreach ($selector in @($Selectors)) {
        $prefix = [string]$selector + "."
        $matches = @($cases | Where-Object {
                ([string]$_.fullname).StartsWith($prefix, [System.StringComparison]::Ordinal)
            })
        if ($matches.Count -eq 0 -or @($matches | Where-Object { $_.result -ne "Passed" }).Count -gt 0) {
            throw "diagnostic Unity selector is missing or not passed"
        }
    }
}

# New-EvidenceRecord创建不含机器或业务identity的稳定证据。
function New-EvidenceRecord {
    param([string]$ScenarioID, [string]$Type)
    return [ordered]@{ scenarioId = $ScenarioID; outcome = "pass"; evidenceType = $Type; completedAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ") }
}

# Get-ToolVersions只提取公开版本号，不记录可执行文件路径或机器identity。
function Get-ToolVersions {
    $unityLine = Get-Content -LiteralPath (Join-Path $ClientRoot "ProjectSettings\ProjectVersion.txt") -Encoding utf8 | Select-Object -First 1
    $unity = ($unityLine -split ':', 2)[1].Trim()
    $dotnet = (& dotnet --version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "qualification dotnet version cannot be resolved" }
    $goOutput = (& go version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "qualification go version cannot be resolved" }
    $gitOutput = (& git --version).Trim()
    if ($LASTEXITCODE -ne 0) { throw "qualification git version cannot be resolved" }
    $goMatch = [regex]::Match($goOutput, 'go([0-9]+\.[0-9]+(?:\.[0-9]+)?)')
    $gitMatch = [regex]::Match($gitOutput, '([0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9]+)?)')
    if (-not $goMatch.Success -or -not $gitMatch.Success) {
        throw "qualification tool version cannot be resolved"
    }
    return [ordered]@{
        unity = $unity
        dotnet = $dotnet
        go = $goMatch.Groups[1].Value
        git = $gitMatch.Groups[1].Value
        powershell = $PSVersionTable.PSVersion.ToString()
    }
}

# Write-Report始终输出低敏结果；原始工具日志不被嵌入。
function Write-Report {
    param([bool]$Qualified, [string]$ContractDigest, [string]$DevelopmentDigest, [string]$ReleaseDigest, [object[]]$Records)
    $body = [pscustomobject][ordered]@{
        schemaVersion = 1
        qualificationVersion = "client-v1"
        startedAt = $StartedAt.ToString("yyyy-MM-ddTHH:mm:ssZ")
        durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $StartedAt).TotalMilliseconds)
        contractDigest = $ContractDigest
        buildDigests = [ordered]@{ development = $DevelopmentDigest; release = $ReleaseDigest }
        toolVersions = Get-ToolVersions
        qualified = $Qualified
        stages = @($StageResults)
        records = @($Records)
        failure = $(if ($PrimaryFailure) { $PrimaryFailure } else { $null })
        cleanup = $(if ($CleanupFailure) { "fail" } else { "pass" })
    }
    Assert-ClosedProperties $body @("schemaVersion", "qualificationVersion", "startedAt", "durationMs", "contractDigest", "buildDigests", "toolVersions", "qualified", "stages", "records", "failure", "cleanup") "report"
    [System.IO.File]::WriteAllText($ReportPath, (($body | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
}

# Read-Evidence严格拒绝未知字段、duplicate、unknown、skipped和digest漂移。
function Read-Evidence {
    param([string]$Path, [object]$Manifest, [string]$ContractDigest, [string]$DevelopmentDigest, [string]$ReleaseDigest)
    if (-not $Path -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw "qualification evidence file is missing" }
    $evidence = Get-Content -LiteralPath $Path -Raw -Encoding utf8 | ConvertFrom-Json
    Assert-ClosedProperties $evidence @("schemaVersion", "qualificationVersion", "contractDigest", "buildDigests", "records") "evidence"
    Assert-ClosedProperties $evidence.buildDigests @("development", "release") "evidence build digests"
    if ($evidence.schemaVersion -ne 1 -or $evidence.qualificationVersion -ne "client-v1" -or
        $evidence.contractDigest -ne $ContractDigest -or $evidence.buildDigests.development -ne $DevelopmentDigest -or
        $evidence.buildDigests.release -ne $ReleaseDigest) { throw "qualification evidence is stale" }
    $scenarioByID = @{}; foreach ($scenario in @($Manifest.scenarios)) { $scenarioByID[[string]$scenario.id] = $scenario }
    $seen = @{}
    $recordByID = @{}
    $completedAtByID = @{}
    foreach ($record in @($evidence.records)) {
        Assert-ClosedProperties $record @("scenarioId", "outcome", "evidenceType", "completedAt") "evidence record"
        if (-not $scenarioByID.ContainsKey([string]$record.scenarioId) -or $seen.ContainsKey([string]$record.scenarioId) -or
            $record.outcome -notin @("pass", "fail") -or $record.evidenceType -notin @("tool", "unity-test", "player-automatic", "operator")) {
            throw "qualification evidence record is invalid"
        }
        $parsed = [DateTime]::MinValue
        if (-not [DateTime]::TryParse([string]$record.completedAt, [ref]$parsed)) { throw "qualification evidence timestamp is invalid" }
        $scenarioID = [string]$record.scenarioId
        $seen[$scenarioID] = $true
        $recordByID[$scenarioID] = $record
        $completedAtByID[$scenarioID] = $parsed.ToUniversalTime()
    }
    foreach ($scenarioID in @($recordByID.Keys)) {
        foreach ($precondition in @($scenarioByID[$scenarioID].preconditions)) {
            $preconditionID = [string]$precondition
            if (-not $recordByID.ContainsKey($preconditionID) -or
                $recordByID[$preconditionID].outcome -ne "pass" -or
                $completedAtByID[$preconditionID] -gt $completedAtByID[$scenarioID]) {
                throw "qualification evidence precondition is missing, failed, or out of order"
            }
        }
    }
    return $evidence
}

# Assert-MandatoryEvidenceCompleteness在独立作用域聚合mandatory记录，避免顶层Scenario参数污染遍历变量。
function Assert-MandatoryEvidenceCompleteness {
    param([object]$Manifest, [object[]]$Records)
    $recordByID = @{}
    foreach ($record in @($Records)) {
        $recordByID[[string]$record.scenarioId] = $record
    }

    foreach ($mandatoryScenario in @($Manifest.scenarios | Where-Object { $_.mandatory })) {
        $scenarioID = [string]$mandatoryScenario.id
        if (-not $recordByID.ContainsKey($scenarioID)) {
            throw "mandatory qualification evidence is missing: $scenarioID"
        }

        $mandatoryRecord = $recordByID[$scenarioID]
        if ([string]$mandatoryRecord.outcome -ne "pass") {
            throw "mandatory qualification evidence failed: $scenarioID"
        }
    }
}

# Stop-OwnedProcesses在独立cleanup预算内只终止仍存活的精确对象。
function Stop-OwnedProcesses {
    $cleanupDeadline = [DateTime]::UtcNow.AddSeconds($CleanupDeadlineSeconds)
    foreach ($process in $OwnedProcesses) {
        try {
            if (-not $process.HasExited) {
                Stop-Process -Id $process.Id -Force -ErrorAction Stop
                $remaining = [Math]::Max(1, [int]($cleanupDeadline - [DateTime]::UtcNow).TotalMilliseconds)
                if (-not $process.WaitForExit($remaining)) { throw "owned process did not exit" }
            }
        }
        catch { $script:CleanupFailure = "owned-process-cleanup-failed" }
        finally { $process.Dispose() }
    }
}

# Write-DiagnosticArtifacts生成非证据清单与隔离双Player启动器，不接入finalize输入。
# Write-WindowsPowerShellScript以带BOM UTF-8写入由Windows PowerShell 5.1执行的脚本。
function Write-WindowsPowerShellScript {
    param(
        [string]$Path,
        [string]$Content)
    [System.IO.File]::WriteAllText(
        $Path,
        $Content,
        [System.Text.UTF8Encoding]::new($true))
}

function Write-DiagnosticArtifacts {
    param(
        [object]$ScenarioContract,
        [string]$ContractDigest,
        [string]$DevelopmentDigest)
    $launcherPath = Join-Path $RunDirectory "launch-two-players.ps1"
    $launcher = @'
# 该启动器只运行当前diagnose构建，并为两个Player选择彼此隔离的Development profile。
[CmdletBinding()]
param(
    [switch]$ShowStepsOnly,
    [switch]$ShowConnections,
    [ValidateSet("owner", "visitor")]
    [string]$LaunchRole)

$ErrorActionPreference = "Stop"
$selectedModes = @($ShowStepsOnly.IsPresent, $ShowConnections.IsPresent, -not [string]::IsNullOrEmpty($LaunchRole)) |
    Where-Object { $_ }
if ($selectedModes.Count -gt 1) {
    throw "ShowStepsOnly, ShowConnections and LaunchRole cannot be combined"
}
$executable = Join-Path $PSScriptRoot "development\iHomeland.exe"
$storage = Join-Path $PSScriptRoot "player-storage"
$playerLogs = Join-Path $PSScriptRoot "player-logs"
$checklistPath = Join-Path $PSScriptRoot "diagnostic-checklist.json"
$processStatePath = Join-Path $PSScriptRoot "diagnostic-processes.json"
if (-not (Test-Path -LiteralPath $checklistPath -PathType Leaf)) {
    throw "diagnostic checklist is missing"
}
$checklist = Get-Content -LiteralPath $checklistPath -Raw -Encoding UTF8 | ConvertFrom-Json

# Get-DiagnosticRoleProcess只接受本启动器亲自记录且仍匹配启动时间的PID。
function Get-DiagnosticRoleProcess {
    param([string]$Role)
    if (-not (Test-Path -LiteralPath $processStatePath -PathType Leaf)) {
        throw "diagnostic process state is missing; launch Players through this script before querying connections"
    }
    $state = Get-Content -LiteralPath $processStatePath -Raw -Encoding UTF8 | ConvertFrom-Json
    $matches = @($state.processes | Where-Object { $_.role -eq $Role })
    if ($state.schemaVersion -ne 1 -or $matches.Count -ne 1) { throw "diagnostic process state is invalid" }
    $record = $matches[0]
    $process = Get-Process -Id ([int]$record.processId) -ErrorAction SilentlyContinue
    if ($null -eq $process -or $process.ProcessName -ne "iHomeland" -or
        $process.StartTime.ToUniversalTime().ToString("o") -ne [string]$record.startTimeUtc) {
        throw "diagnostic $Role process state is stale; relaunch the requested role before querying connections"
    }
    return [pscustomobject]@{ ProcessId = $process.Id }
}

# Write-DiagnosticProcessState只保留由本启动器创建且仍可验证的角色进程。
function Write-DiagnosticProcessState {
    param([object[]]$Records)
    $state = [ordered]@{ schemaVersion = 1; processes = @($Records) }
    [System.IO.File]::WriteAllText(
        $processStatePath,
        (($state | ConvertTo-Json -Depth 4) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
}

# Show-DiagnosticConnections从操作系统当前连接表给出唯一故障目标，不轮询或推测客户端状态。
function Show-DiagnosticConnections {
    param([object]$Guide)
    if ($null -eq $Guide) { throw "this diagnostic scenario has no TCP fault target" }
    $process = Get-DiagnosticRoleProcess ([string]$Guide.role)
    $processState = Get-Content -LiteralPath $processStatePath -Raw -Encoding UTF8 | ConvertFrom-Json
    $processRecord = @($processState.processes | Where-Object { $_.role -eq [string]$Guide.role })[0]
    if (-not [string]::IsNullOrWhiteSpace([string]$processRecord.logFile)) {
        Write-Host ("[log] role={0} path={1}" -f $Guide.role, $processRecord.logFile)
    }
    $ports = @([int]$Guide.remotePort, [int]$Guide.protectedRemotePort)
    $connections = @(Get-NetTCPConnection -OwningProcess ([int]$process.ProcessId) -State Established -ErrorAction SilentlyContinue |
        Where-Object { $_.RemotePort -in $ports } |
        Sort-Object RemotePort, LocalPort)
    foreach ($connection in $connections) {
        $label = if ([int]$connection.RemotePort -eq [int]$Guide.remotePort) { "TARGET" } else { "DO-NOT-CLOSE" }
        Write-Host ("[{0}] role={1} PID={2} LocalPort={3} RemotePort={4} State={5}" -f
            $label, $Guide.role, $process.ProcessId, $connection.LocalPort, $connection.RemotePort, $connection.State)
    }
    $targets = @($connections | Where-Object { [int]$_.RemotePort -eq [int]$Guide.remotePort })
    if ($targets.Count -eq 0) {
        Write-Host ("[status] role={0} PID={1} has no Established RemotePort={2} target in this OS snapshot; wait for the current recovery to settle, then run -ShowConnections again." -f
            $Guide.role, $process.ProcessId, $Guide.remotePort) -ForegroundColor Yellow
        return
    }
    if ($targets.Count -ne 1) {
        throw "expected exactly one target connection; port 8080 also carries transient HTTP, so finish the current request and query again instead of guessing"
    }
    $target = $targets[0]
    Write-Host ("[action] In TCPView close PID={0}, LocalPort={1}, RemotePort={2}; do not close RemotePort={3}." -f
        $process.ProcessId, $target.LocalPort, $target.RemotePort, $Guide.protectedRemotePort) -ForegroundColor Yellow
}

if ($ShowConnections) {
    Show-DiagnosticConnections $checklist.connectionGuide
}
elseif (-not [string]::IsNullOrEmpty($LaunchRole)) {
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "diagnostic Development Player is missing"
    }
    [System.IO.Directory]::CreateDirectory($storage) | Out-Null
    [System.IO.Directory]::CreateDirectory($playerLogs) | Out-Null
    $quotedStorage = '"' + $storage + '"'
    $roleLog = Join-Path $playerLogs "$LaunchRole.log"
    $quotedRoleLog = '"' + $roleLog + '"'
    $roleProcess = Start-Process -FilePath $executable -ArgumentList (
        "-ihomelandDataProfile diagnostic-$LaunchRole -ihomelandQualificationStorageRoot $quotedStorage -logFile $quotedRoleLog") -PassThru
    $records = @()
    if (Test-Path -LiteralPath $processStatePath -PathType Leaf) {
        $previousState = Get-Content -LiteralPath $processStatePath -Raw -Encoding UTF8 | ConvertFrom-Json
        foreach ($record in @($previousState.processes | Where-Object { $_.role -ne $LaunchRole })) {
            $existing = Get-Process -Id ([int]$record.processId) -ErrorAction SilentlyContinue
            if ($null -ne $existing -and $existing.ProcessName -eq "iHomeland" -and
                $existing.StartTime.ToUniversalTime().ToString("o") -eq [string]$record.startTimeUtc) {
                $records += $record
            }
        }
    }
    $records += [ordered]@{ role = $LaunchRole; processId = $roleProcess.Id; startTimeUtc = $roleProcess.StartTime.ToUniversalTime().ToString("o"); logFile = $roleLog }
    Write-DiagnosticProcessState $records
    Write-Host ("[diagnose] {0} PID={1}" -f $LaunchRole, $roleProcess.Id)
    Write-Host ("[log] role={0} path={1}" -f $LaunchRole, $roleLog)
    Write-Host ("[account-check] profile=diagnostic-{0} only isolates storage; authenticate the matching {0} test account." -f $LaunchRole) -ForegroundColor Yellow
}
elseif (-not $ShowStepsOnly) {
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "diagnostic Development Player is missing"
    }
    [System.IO.Directory]::CreateDirectory($storage) | Out-Null
    [System.IO.Directory]::CreateDirectory($playerLogs) | Out-Null
    $quotedStorage = '"' + $storage + '"'
    $ownerLog = Join-Path $playerLogs "owner.log"
    $visitorLog = Join-Path $playerLogs "visitor.log"
    $quotedOwnerLog = '"' + $ownerLog + '"'
    $quotedVisitorLog = '"' + $visitorLog + '"'
    $ownerProcess = Start-Process -FilePath $executable -ArgumentList (
        "-ihomelandDataProfile diagnostic-owner -ihomelandQualificationStorageRoot $quotedStorage -logFile $quotedOwnerLog") -PassThru
    $visitorProcess = Start-Process -FilePath $executable -ArgumentList (
        "-ihomelandDataProfile diagnostic-visitor -ihomelandQualificationStorageRoot $quotedStorage -logFile $quotedVisitorLog") -PassThru
    Write-DiagnosticProcessState @(
        [ordered]@{ role = "owner"; processId = $ownerProcess.Id; startTimeUtc = $ownerProcess.StartTime.ToUniversalTime().ToString("o"); logFile = $ownerLog },
        [ordered]@{ role = "visitor"; processId = $visitorProcess.Id; startTimeUtc = $visitorProcess.StartTime.ToUniversalTime().ToString("o"); logFile = $visitorLog })
    Write-Host ("[diagnose] owner PID={0}; visitor PID={1}" -f $ownerProcess.Id, $visitorProcess.Id)
    Write-Host ("[log] owner={0}" -f $ownerLog)
    Write-Host ("[log] visitor={0}" -f $visitorLog)
    Write-Host "[account-check] profiles isolate storage but do not select accounts; authenticate different Owner and Visitor accounts, then verify their OwnWorld IDs differ." -ForegroundColor Yellow
    Write-Host ("[next] After establishing the product flow, run: powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"{0}`" -ShowConnections" -f $PSCommandPath)
}
$manualSteps = @($checklist.manualSteps)
for ($stepIndex = 0; $stepIndex -lt $manualSteps.Count; $stepIndex++) {
    $manualStep = $manualSteps[$stepIndex]
    Write-Host ("[step {0}/{1}] {2}" -f ($stepIndex + 1), $manualSteps.Count, $manualStep.instruction)
}
'@
    Write-WindowsPowerShellScript `
        -Path $launcherPath `
        -Content ($launcher + "`n")

    $checklist = [ordered]@{
        schemaVersion = 1
        diagnosticVersion = "client-v1-diagnostic"
        qualificationEvidence = $false
        scenarioId = [string]$ScenarioContract.id
        contractDigest = $ContractDigest
        developmentBuildDigest = $DevelopmentDigest
        buildArtifact = "development/iHomeland.exe"
        launcherArtifact = "launch-two-players.ps1"
        profiles = @("diagnostic-owner", "diagnostic-visitor")
        automatedStages = @($StageResults)
        connectionGuide = $ScenarioContract.connectionGuide
        manualSteps = @($ScenarioContract.manualSteps)
    }
    [System.IO.File]::WriteAllText(
        (Join-Path $RunDirectory "diagnostic-checklist.json"),
        (($checklist | ConvertTo-Json -Depth 8) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
}

Assert-RunDirectory
[System.IO.Directory]::CreateDirectory($RunDirectory) | Out-Null
Write-Stage "run" "run-id=$RunId"
$manifest = $null
$contractDigest = ""
$developmentDigest = ""
$releaseDigest = ""
$records = @()
try {
    $contractStageStarted = [DateTime]::UtcNow
    Write-Stage "contract" "校验client-v1资格manifest与runner完整性"
    $manifest = Read-QualificationManifest
    $registry = Assert-RegistryCompleteness $manifest
    $diagnosticRegistry = Read-DiagnosticRegistry $registry
    $contractDigest = Get-ClientContractDigest -RepositoryRoot $RepositoryRoot
    $StageResults.Add([ordered]@{ id = "contract"; outcome = "pass"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $contractStageStarted).TotalMilliseconds) })

    if ($Action -eq "validate") { return }

    if ($Action -eq "diagnose") {
        Assert-LockedUnity
        $scenarioContract = Resolve-DiagnosticScenario $diagnosticRegistry $Scenario
        $editModeSelectors = @(Get-DiagnosticUnitySelectors $scenarioContract $registry "unity-editmode")
        $playModeSelectors = @(Get-DiagnosticUnitySelectors $scenarioContract $registry "unity-playmode")
        if ($editModeSelectors.Count -gt 0) {
            Write-Stage "diagnostic-editmode" "执行场景映射的定向EditMode fixtures"
            $editModeResults = Join-Path $RunDirectory "diagnostic-editmode.xml"
            Invoke-Unity @(
                "-runTests",
                "-testPlatform", "EditMode",
                "-testFilter", ($editModeSelectors -join ";"),
                "-testResults", $editModeResults) $UnityTestStageBudgetMilliseconds "diagnostic-editmode"
            Assert-UnityTestResults $editModeResults "diagnostic-editmode"
            Assert-DiagnosticUnitySelectorsPassed $editModeResults $editModeSelectors
        }
        if ($playModeSelectors.Count -gt 0) {
            Write-Stage "diagnostic-playmode" "执行场景映射的定向PlayMode fixtures"
            $playModeResults = Join-Path $RunDirectory "diagnostic-playmode.xml"
            Invoke-Unity @(
                "-runTests",
                "-testPlatform", "PlayMode",
                "-testFilter", ($playModeSelectors -join ";"),
                "-testResults", $playModeResults) $UnityTestStageBudgetMilliseconds "diagnostic-playmode"
            Assert-UnityTestResults $playModeResults "diagnostic-playmode"
            Assert-DiagnosticUnitySelectorsPassed $playModeResults $playModeSelectors
        }

        Write-Stage "diagnostic-build" "构建单个Windows Development Player"
        $developmentRoot = Join-Path $RunDirectory "development"
        Invoke-Unity @(
            "-executeMethod", "IHomeland.Client.AppShell.Editor.ClientDevelopmentBuild.BuildWindowsDevelopment",
            "-ihomelandBuildOutput", $developmentRoot) $UnityBuildStageBudgetMilliseconds "diagnostic-build"
        $developmentDigest = Get-BuildDigest $developmentRoot
        Write-DiagnosticArtifacts $scenarioContract $contractDigest $developmentDigest
        Write-Stage "diagnose" "定向自动检查已通过；清单与启动器不构成client-v1资格证据"
        foreach ($manualStep in @($scenarioContract.manualSteps)) {
            Write-Stage "manual-step" ("{0}：{1}" -f $manualStep.id, $manualStep.instruction)
        }
        return
    }

    if ($Action -eq "automatic") {
        Assert-LockedUnity
        Write-Stage "protocol" "执行clean protocol generation与parity"
        Invoke-OwnedProcess "powershell.exe" @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", (Join-Path $RepositoryRoot "tools\proto\proto.ps1"), "verify") $ProtocolStageBudgetMilliseconds "protocol"
        $records += New-EvidenceRecord "contract-clean-generation" "tool"
        $editModeResults = Join-Path $RunDirectory "editmode.xml"
        Invoke-Unity @("-runTests", "-testPlatform", "EditMode", "-testResults", $editModeResults) $UnityTestStageBudgetMilliseconds "editmode"
        Assert-UnityTestResults $editModeResults "editmode"
        Assert-UnityScenarioMappings $editModeResults $registry "unity-editmode"
        foreach ($entry in @($registry.entries | Where-Object { $_.runner -eq "unity-editmode" })) { $records += New-EvidenceRecord $entry.scenarioId "unity-test" }
        $playModeResults = Join-Path $RunDirectory "playmode.xml"
        Invoke-Unity @("-runTests", "-testPlatform", "PlayMode", "-testResults", $playModeResults) $UnityTestStageBudgetMilliseconds "playmode"
        Assert-UnityTestResults $playModeResults "playmode"
        Assert-UnityScenarioMappings $playModeResults $registry "unity-playmode"
        foreach ($entry in @($registry.entries | Where-Object { $_.runner -eq "unity-playmode" })) { $records += New-EvidenceRecord $entry.scenarioId "unity-test" }
        $developmentRoot = Join-Path $RunDirectory "development"
        $releaseRoot = Join-Path $RunDirectory "release"
        Invoke-Unity @("-executeMethod", "IHomeland.Client.AppShell.Editor.ClientDevelopmentBuild.BuildWindowsDevelopment", "-ihomelandBuildOutput", $developmentRoot) $UnityBuildStageBudgetMilliseconds "development-build"
        $records += New-EvidenceRecord "windows-development-build" "tool"
        Invoke-Unity @("-executeMethod", "IHomeland.Client.AppShell.Editor.ClientDevelopmentBuild.BuildWindowsRelease", "-ihomelandBuildOutput", $releaseRoot) $UnityBuildStageBudgetMilliseconds "release-build"
        $records += New-EvidenceRecord "windows-release-build" "tool"
        $developmentDigest = Get-BuildDigest $developmentRoot
        $releaseDigest = Get-BuildDigest $releaseRoot
        $releaseScanStarted = [DateTime]::UtcNow
        $forbidden = @("-ihomelandDataProfile", "-ihomelandQualificationStorageRoot", "IHOMELAND_QUALIFICATION", "recovery.owner")
        foreach ($binary in @(Get-ChildItem -LiteralPath $releaseRoot -File -Recurse | Where-Object { $_.Extension -in @(".exe", ".dll") })) {
            $releaseBytes = [System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes($binary.FullName))
            foreach ($pattern in $forbidden) { if ($releaseBytes.Contains($pattern)) { throw "release surface scan failed" } }
        }
        $StageResults.Add([ordered]@{ id = "release-scan"; outcome = "pass"; durationMs = [Math]::Max(0, [int64]([DateTime]::UtcNow - $releaseScanStarted).TotalMilliseconds) })
        $records += New-EvidenceRecord "release-surface-scan" "tool"
        $qualificationStorageRoot = Join-Path $RunDirectory "player-storage"
        [System.IO.Directory]::CreateDirectory($qualificationStorageRoot) | Out-Null
        Invoke-PlayerSmoke $developmentRoot "development-player-smoke" 90000 @("-ihomelandDataProfile", "qualification-smoke", "-ihomelandQualificationStorageRoot", $qualificationStorageRoot)
        $records += New-EvidenceRecord "development-player-smoke" "player-automatic"
        Assert-ReleaseSmokeProfileClean
        Invoke-PlayerSmoke $releaseRoot "release-player-smoke" 90000 @()
        $records += New-EvidenceRecord "release-player-smoke" "player-automatic"
        Invoke-DocumentationGovernance
        $evidence = [ordered]@{ schemaVersion = 1; qualificationVersion = "client-v1"; contractDigest = $contractDigest; buildDigests = [ordered]@{ development = $developmentDigest; release = $releaseDigest }; records = $records }
        [System.IO.File]::WriteAllText($AutomaticEvidencePath, (($evidence | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
        Write-Report $false $contractDigest $developmentDigest $releaseDigest $records
        Write-Stage "automatic" "自动证据已生成；五分钟soak与人工矩阵尚未完成，结论保持not-qualified"
        return
    }

    if ($Action -eq "soak") {
        $developmentRoot = Join-Path $RunDirectory "development"
        $releaseRoot = Join-Path $RunDirectory "release"
        if (-not (Test-Path -LiteralPath $developmentRoot -PathType Container) -or
            -not (Test-Path -LiteralPath $releaseRoot -PathType Container) -or
            -not (Test-Path -LiteralPath $AutomaticEvidencePath -PathType Leaf)) {
            throw "qualification automatic inputs are missing"
        }
        $developmentDigest = Get-BuildDigest $developmentRoot
        $releaseDigest = Get-BuildDigest $releaseRoot
        $automatic = Read-Evidence $AutomaticEvidencePath $manifest $contractDigest $developmentDigest $releaseDigest
        $records = @($automatic.records)
        if (@($records | Where-Object { $_.scenarioId -eq "five-minute-recovery-soak" }).Count -ne 0) {
            throw "player soak evidence already exists"
        }
        $soakUsername = [string]$env:IHOMELAND_QUALIFICATION_USERNAME
        $soakPassword = [string]$env:IHOMELAND_QUALIFICATION_PASSWORD
        Invoke-PlayerSoak $developmentRoot (Join-Path $RunDirectory "player-storage")
        $records += New-EvidenceRecord "five-minute-recovery-soak" "player-automatic"
        Assert-PlayerLogRedaction @($soakUsername, $soakPassword)
        $soakUsername = $null
        $soakPassword = $null
        $records += New-EvidenceRecord "credential-redaction" "tool"
        Invoke-DocumentationGovernance
        $records += New-EvidenceRecord "documentation-governance" "tool"
        Invoke-PlayerStorageCleanup $developmentRoot (Join-Path $RunDirectory "player-storage") @("qualification-soak")
        $evidence = [ordered]@{ schemaVersion = 1; qualificationVersion = "client-v1"; contractDigest = $contractDigest; buildDigests = [ordered]@{ development = $developmentDigest; release = $releaseDigest }; records = $records }
        [System.IO.File]::WriteAllText($AutomaticEvidencePath, (($evidence | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
        Write-Report $false $contractDigest $developmentDigest $releaseDigest $records
        Write-Stage "soak" "五分钟真实Player恢复soak已通过；人工双Player矩阵尚未完成"
        return
    }

    if ($Action -eq "prepare-manual") {
        $developmentRoot = Join-Path $RunDirectory "development"
        $releaseRoot = Join-Path $RunDirectory "release"
        if (-not (Test-Path -LiteralPath $developmentRoot -PathType Container) -or
            -not (Test-Path -LiteralPath $releaseRoot -PathType Container)) {
            throw "qualification build inputs are missing"
        }
        $developmentDigest = Get-BuildDigest $developmentRoot
        $releaseDigest = Get-BuildDigest $releaseRoot
        $automatic = Read-Evidence $AutomaticEvidencePath $manifest $contractDigest $developmentDigest $releaseDigest
        if (@($automatic.records | Where-Object { $_.scenarioId -eq "five-minute-recovery-soak" -and $_.outcome -eq "pass" }).Count -ne 1) {
            throw "player soak evidence must pass before manual preparation"
        }
        $manualStorage = Join-Path $RunDirectory "manual-player-storage"
        [System.IO.Directory]::CreateDirectory($manualStorage) | Out-Null
        $checklist = [ordered]@{
            schemaVersion = 1
            qualificationVersion = "client-v1"
            contractDigest = $contractDigest
            buildDigests = [ordered]@{ development = $developmentDigest; release = $releaseDigest }
            profiles = @(
                [ordered]@{ role = "owner"; dataProfile = "qualification-owner"; buildArtifact = "development/iHomeland.exe"; storageArtifact = "manual-player-storage" },
                [ordered]@{ role = "visitor"; dataProfile = "qualification-visitor"; buildArtifact = "development/iHomeland.exe"; storageArtifact = "manual-player-storage" }
            )
            scenarios = @(
                [ordered]@{ scenarioId = "two-player-product-flow"; steps = @("clean-login-own-world", "owner-open-create-invite", "visitor-accept-join", "visitor-leave", "owner-kick", "owner-close", "reinvite", "owner-visitor-permissions") },
                [ordered]@{ scenarioId = "two-player-channel-faults"; steps = @("control-only-disconnect", "owner-gameplay-disconnect", "visitor-grace-reconnect", "owner-player-restart", "visitor-player-restart", "session-invalidation") },
                [ordered]@{ scenarioId = "two-player-server-restart"; steps = @("stop-server-during-visit", "bounded-retry-while-offline", "restart-preserved-storage", "authoritative-own-world-convergence", "reinvite-after-restart") }
            )
        }
        $checklistPath = Join-Path $RunDirectory "manual-checklist.json"
        [System.IO.File]::WriteAllText($checklistPath, (($checklist | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
        $template = [ordered]@{ schemaVersion = 1; qualificationVersion = "client-v1"; contractDigest = $contractDigest; buildDigests = [ordered]@{ development = $developmentDigest; release = $releaseDigest }; records = @($automatic.records) }
        $templatePath = Join-Path $RunDirectory "evidence.json"
        [System.IO.File]::WriteAllText($templatePath, (($template | ConvertTo-Json -Depth 8) + "`n"), [System.Text.UTF8Encoding]::new($false))
        Write-Stage "manual" "已生成绑定当前contract/build的低敏清单与evidence模板；完成Player场景后再finalize"
        return
    }

    $developmentRoot = Join-Path $RunDirectory "development"
    $releaseRoot = Join-Path $RunDirectory "release"
    if (-not (Test-Path -LiteralPath $developmentRoot -PathType Container) -or
        -not (Test-Path -LiteralPath $releaseRoot -PathType Container)) {
        throw "qualification build inputs are missing"
    }
    $developmentDigest = Get-BuildDigest $developmentRoot
    $releaseDigest = Get-BuildDigest $releaseRoot
    $evidence = Read-Evidence $EvidencePath $manifest $contractDigest $developmentDigest $releaseDigest
    $records = @($evidence.records)
    Assert-MandatoryEvidenceCompleteness $manifest $records
    $StageResults.Add([ordered]@{ id = "completeness"; outcome = "pass"; durationMs = 0 })
    Invoke-PlayerStorageCleanup $developmentRoot (Join-Path $RunDirectory "manual-player-storage") @("qualification-owner", "qualification-visitor")
    Write-Report $true $contractDigest $developmentDigest $releaseDigest $records
    Write-Stage "qualified" "client-v1全部mandatory证据属于同一冻结输入"
}
catch {
    if (-not $PrimaryFailure) { $PrimaryFailure = "client-qualification-stage-failed" }
    if (@($StageResults | Where-Object { $_.outcome -eq "fail" }).Count -eq 0) {
        $StageResults.Add([ordered]@{ id = "active"; outcome = "fail"; durationMs = 0 })
    }
    if ($Action -ne "diagnose" -and $contractDigest) {
        Write-Report $false $contractDigest $developmentDigest $releaseDigest $records
    }
    throw
}
finally {
    Stop-OwnedProcesses
    if ($Action -ne "diagnose" -and $CleanupFailure -and $contractDigest) {
        Write-Report $false $contractDigest $developmentDigest $releaseDigest $records
    }
}
