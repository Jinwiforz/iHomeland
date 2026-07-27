# 该回归入口在隔离进程中装载资格工具函数，并验证失败、deadline、证据与精确清理边界。
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ToolPath = Join-Path $PSScriptRoot "client-qualification.ps1"
$LocalToolPath = Join-Path $PSScriptRoot "client-qualification-local.ps1"
$ContractIdentityModule = Join-Path $PSScriptRoot "ClientContractIdentity.psm1"
$TestParent = Join-Path $RepositoryRoot ".local\client-qualification-tool-tests"
$TestRoot = Join-Path $TestParent ([guid]::NewGuid().ToString("N"))

# Assert-True为测试断言提供稳定、低敏失败文本。
function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}
# Assert-Throws要求目标路径显式失败，避免错误门禁被测试误判为成功。
function Assert-Throws {
    param([scriptblock]$Operation, [string]$Message)
    $failed = $false
    try { & $Operation }
    catch { $failed = $true }
    if (-not $failed) { throw $Message }
}

Import-Module $ContractIdentityModule -Force
$contractPathSpecs = @(Get-ClientContractPathSpecs)
foreach ($required in @(
        "shared/proto",
        "shared/contracts/registry",
        "shared/contracts/fixtures/client-qualification",
        "shared/contracts/fixtures/battle/wire",
        "shared/contracts/fixtures/simulation-control/runtime",
        "client/Packages",
        "client/ProjectSettings/ProjectVersion.txt"
    )) {
    Assert-True ($contractPathSpecs -contains $required) "client contract identity omitted a consumed input"
}
foreach ($excluded in @(
        "shared/contracts/fixtures/battle/qualification",
        "shared/contracts/fixtures/battle/network-profile",
        "shared/contracts/fixtures/battle/model",
        "shared/contracts/fixtures/simulation-control/manifest.json"
    )) {
    Assert-True ($contractPathSpecs -notcontains $excluded) "client contract identity included server qualification evidence"
}
$contractIdentity = Get-ClientContractDigest -RepositoryRoot $RepositoryRoot
Assert-True (
    $contractIdentity -cmatch '^[0-9a-f]{64}$'
) "client contract identity is not a canonical SHA-256"

# Import-ToolFunctions通过PowerShell AST装载指定函数，不执行资格入口主流程或创建第二套实现。
function Import-ToolFunctions {
    param([string[]]$Names)
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile(
        $ToolPath,
        [ref]$tokens,
        [ref]$errors)
    if ($errors.Count -ne 0) { throw "client qualification tool cannot be parsed" }

    foreach ($name in $Names) {
        $functionAst = $ast.Find(
            {
                param($node)
                $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
                    $node.Name -eq $name
            },
            $true)
        if ($null -eq $functionAst) { throw "client qualification function is missing" }
        $body = $functionAst.Body.Extent.Text
        $scriptBlock = [scriptblock]::Create($body.Substring(1, $body.Length - 2))
        Set-Item -Path "Function:script:$name" -Value $scriptBlock
    }
}

# New-TestEvidence创建只含公开digest和稳定scenario ID的闭合证据。
function New-TestEvidence {
    param([string]$Path, [string]$ContractDigest, [string]$DevelopmentDigest, [string]$ReleaseDigest)
    $evidence = [ordered]@{
        schemaVersion = 1
        qualificationVersion = "client-v1"
        contractDigest = $ContractDigest
        buildDigests = [ordered]@{
            development = $DevelopmentDigest
            release = $ReleaseDigest
        }
        records = @(
            [ordered]@{
                scenarioId = "root"
                outcome = "pass"
                evidenceType = "tool"
                completedAt = "2026-01-01T00:00:00Z"
            },
            [ordered]@{
                scenarioId = "dependent"
                outcome = "pass"
                evidenceType = "tool"
                completedAt = "2026-01-01T00:00:01Z"
            }
        )
    }
    [System.IO.File]::WriteAllText(
        $Path,
        (($evidence | ConvertTo-Json -Depth 8) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
}

$expectedParent = [System.IO.Path]::GetFullPath($TestParent).TrimEnd('\') + '\'
$resolvedTestRoot = [System.IO.Path]::GetFullPath($TestRoot)
if (-not $resolvedTestRoot.StartsWith($expectedParent, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "client qualification test root escaped local owner"
}
[System.IO.Directory]::CreateDirectory($TestRoot) | Out-Null

$unrelated = $null
try {
    Import-ToolFunctions @(
        "Assert-Remaining",
        "Assert-ClosedProperties",
        "Test-IsFullyQualifiedWindowsPath",
        "New-UnityCommandArguments",
        "Assert-UnityTestResults",
        "Read-DiagnosticRegistry",
        "Resolve-DiagnosticScenario",
        "Get-DiagnosticUnitySelectors",
        "Assert-DiagnosticUnitySelectorsPassed",
        "Write-WindowsPowerShellScript",
        "Write-DiagnosticArtifacts",
        "Invoke-OwnedProcess",
        "Read-Evidence",
        "Assert-MandatoryEvidenceCompleteness",
        "Stop-OwnedProcesses",
        "Invoke-PlayerStorageCleanup")

    $script:OwnedProcesses = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
    $script:StageResults = [System.Collections.Generic.List[object]]::new()
    $script:Deadline = [DateTime]::UtcNow.AddMinutes(2)
    $script:ProcessObservationSliceMilliseconds = 25
    $script:CleanupDeadlineSeconds = 5
    $script:CleanupFailure = ""
    $script:RunDirectory = $TestRoot
    $script:ClientRoot = Join-Path $RepositoryRoot "client"
    $script:DiagnosticRegistryPath = Join-Path $RepositoryRoot "shared\contracts\fixtures\client-qualification\diagnostic-registry.json"

    Assert-True (Test-IsFullyQualifiedWindowsPath "D:\Unity\Editor\Unity.exe") "drive absolute Unity path was rejected"
    Assert-True (Test-IsFullyQualifiedWindowsPath "D:/Unity/Editor/Unity.exe") "slash-normalized Unity path was rejected"
    Assert-True (Test-IsFullyQualifiedWindowsPath "\\server\share\Unity.exe") "UNC Unity path was rejected"
    Assert-True (-not (Test-IsFullyQualifiedWindowsPath "D:Unity\Editor\Unity.exe")) "drive-relative Unity path was accepted"
    Assert-True (-not (Test-IsFullyQualifiedWindowsPath "\Unity\Editor\Unity.exe")) "root-relative Unity path was accepted"
    Assert-True (-not (Test-IsFullyQualifiedWindowsPath "relative\Unity.exe")) "relative Unity path was accepted"

    $testArguments = New-UnityCommandArguments @("-runTests", "-testPlatform", "EditMode") "editmode"
    Assert-True (-not ($testArguments -contains "-quit")) "Unity Test Framework was preempted by the generic quit argument"
    Assert-True ($testArguments -contains "-runTests") "Unity test command lost the Test Framework owner"
    $buildArguments = New-UnityCommandArguments @("-executeMethod", "Build.Entry") "development-build"
    Assert-True ($buildArguments -contains "-quit") "synchronous Unity build command lost deterministic editor shutdown"

    $automaticRegistry = Get-Content -LiteralPath (
        Join-Path $RepositoryRoot "shared\contracts\fixtures\client-qualification\automatic-registry.json") `
        -Raw -Encoding utf8 | ConvertFrom-Json
    $diagnosticRegistry = Read-DiagnosticRegistry $automaticRegistry
    $ownerRecovery = Resolve-DiagnosticScenario $diagnosticRegistry "owner-gameplay-recovery"
    $ownerSelectors = @(Get-DiagnosticUnitySelectors $ownerRecovery $automaticRegistry "unity-editmode")
    Assert-True ($ownerSelectors -contains "IHomeland.Client.Tests.EditMode.WorldAdmissionCoordinatorTests") "owner recovery diagnostic lost admission fixture"
    Assert-True ($ownerSelectors -contains "IHomeland.Client.Tests.EditMode.ClientWorldServiceTests") "owner recovery diagnostic lost projection fixture"
    Assert-True ($ownerSelectors -contains "IHomeland.Client.Tests.EditMode.ClientPersonalWorldExperienceTests") "owner recovery diagnostic lost presentation fixture"
    Assert-Throws {
        Resolve-DiagnosticScenario $diagnosticRegistry "unknown-diagnostic"
    } "unknown diagnostic scenario was accepted"

    $diagnosticResult = Join-Path $TestRoot "diagnostic.xml"
    [System.IO.File]::WriteAllText(
        $diagnosticResult,
        '<test-run result="Passed" failed="0" skipped="0" inconclusive="0"><test-suite><test-case fullname="IHomeland.Client.Tests.EditMode.WorldAdmissionCoordinatorTests.OwnerRecovery" result="Passed" /></test-suite></test-run>',
        [System.Text.UTF8Encoding]::new($false))
    Assert-DiagnosticUnitySelectorsPassed $diagnosticResult @(
        "IHomeland.Client.Tests.EditMode.WorldAdmissionCoordinatorTests")
    Assert-Throws {
        Assert-DiagnosticUnitySelectorsPassed $diagnosticResult @(
            "IHomeland.Client.Tests.EditMode.ClientWorldServiceTests")
    } "missing diagnostic fixture was accepted"

    $windowsPowerShellScript = Join-Path $TestRoot "windows-powershell-utf8.ps1"
    Write-WindowsPowerShellScript $windowsPowerShellScript 'Write-Host "诊断启动"'
    $scriptBytes = [System.IO.File]::ReadAllBytes($windowsPowerShellScript)
    Assert-True (
        $scriptBytes.Length -ge 3 -and
        $scriptBytes[0] -eq 0xEF -and
        $scriptBytes[1] -eq 0xBB -and
        $scriptBytes[2] -eq 0xBF) "Windows PowerShell script lost its UTF-8 BOM"
    $parseTokens = $null
    $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile(
        $windowsPowerShellScript,
        [ref]$parseTokens,
        [ref]$parseErrors) | Out-Null
    Assert-True (@($parseErrors).Count -eq 0) "Windows PowerShell 5.1 could not parse the generated UTF-8 script"

    $diagnosticContract = [pscustomobject]@{
        id = "owner-gameplay-recovery"
        connectionGuide = [pscustomobject]@{ role = "owner"; channel = "gameplay"; remotePort = 8444; protectedRemotePort = 8080 }
        manualSteps = @(
            [pscustomobject]@{ id = "disconnect-owner-gameplay"; instruction = "只断开Owner gameplay连接。" })
    }
    Write-DiagnosticArtifacts $diagnosticContract ("d" * 64) ("e" * 64)
    $generatedLauncherPath = Join-Path $TestRoot "launch-two-players.ps1"
    $generatedLauncher = Get-Content -LiteralPath $generatedLauncherPath -Raw -Encoding UTF8
    Assert-True ($generatedLauncher.Contains('diagnostic-checklist.json')) "diagnostic launcher lost its checklist owner"
    Assert-True ($generatedLauncher.Contains('$checklist.manualSteps')) "diagnostic launcher no longer reads bound manual steps"
    Assert-True ($generatedLauncher.Contains('[step {0}/{1}]')) "diagnostic launcher no longer prints numbered instructions"
    Assert-True ($generatedLauncher.Contains('$ShowStepsOnly')) "diagnostic launcher cannot print steps without launching duplicate Players"
    Assert-True ($generatedLauncher.Contains('$ShowConnections')) "diagnostic launcher cannot display current channel endpoints"
    Assert-True ($generatedLauncher.Contains('player-logs')) "diagnostic launcher must isolate Player logs by role"
    Assert-True ($generatedLauncher.Contains('-logFile')) "diagnostic launcher must pass an explicit Player log path"
    Assert-True ($generatedLauncher.Contains('owner.log')) "diagnostic launcher is missing the owner log"
    Assert-True ($generatedLauncher.Contains('visitor.log')) "diagnostic launcher is missing the visitor log"
    Assert-True ($generatedLauncher.Contains('$LaunchRole')) "diagnostic launcher cannot restart exactly one profile"
    Assert-True ($generatedLauncher.Contains('[account-check]')) "diagnostic launcher no longer prevents profile/account role confusion"
    Assert-True ($generatedLauncher.Contains('profiles isolate storage but do not select accounts')) "diagnostic launcher falsely implies profiles select accounts"
    Assert-True ($generatedLauncher.Contains('Get-NetTCPConnection')) "diagnostic launcher no longer reads authoritative OS connections"
    Assert-True ($generatedLauncher.Contains('has no Established')) "diagnostic launcher treats a transient empty OS connection snapshot as a tool failure"
    Assert-True ($generatedLauncher.Contains('-ErrorAction SilentlyContinue')) "diagnostic launcher does not normalize an empty CIM query"
    Assert-True ($generatedLauncher.Contains('diagnostic-processes.json')) "diagnostic launcher no longer owns its launched process identities"
    Assert-True (-not $generatedLauncher.Contains('Get-CimInstance')) "diagnostic launcher still depends on unstable WMI process enumeration"
    $generatedTokens = $null
    $generatedErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile(
        $generatedLauncherPath,
        [ref]$generatedTokens,
        [ref]$generatedErrors) | Out-Null
    Assert-True (@($generatedErrors).Count -eq 0) "generated diagnostic launcher is not parseable by Windows PowerShell 5.1"

    $script:StageResults.Add([ordered]@{ id = "editmode"; outcome = "pass"; durationMs = 1 })
    Assert-Throws {
        Assert-UnityTestResults (Join-Path $TestRoot "missing-editmode.xml") "editmode"
    } "missing Unity result was accepted"
    Assert-True ($script:StageResults[0].outcome -eq "fail") "missing Unity result left a false passing stage"
    $script:StageResults.Clear()

    Invoke-OwnedProcess "cmd.exe" @("/d", "/c", "ver") 5000 "success"
    Assert-Throws {
        Invoke-OwnedProcess "cmd.exe" @("/d", "/c", "exit 7") 5000 "unity-test-failure"
    } "nonzero child exit was accepted"
    Assert-Throws {
        Invoke-OwnedProcess "ping.exe" @("-n", "30", "127.0.0.1") 200 "timeout"
    } "stage timeout was accepted"
    Assert-Throws {
        Invoke-OwnedProcess "cmd.exe" @("/d", "/c", "exit 9") 5000 "player-crash"
    } "player crash was accepted"

    $owned = Start-Process -FilePath "ping.exe" -ArgumentList @(
        "-n", "30", "127.0.0.1") -PassThru -WindowStyle Hidden
    $unrelated = Start-Process -FilePath "ping.exe" -ArgumentList @(
        "-n", "30", "127.0.0.1") -PassThru -WindowStyle Hidden
    $ownedID = $owned.Id
    $script:OwnedProcesses.Add($owned)
    Stop-OwnedProcesses
    $unrelated.Refresh()
    $ownedAfterCleanup = Get-Process -Id $ownedID -ErrorAction SilentlyContinue
    Assert-True ($null -eq $ownedAfterCleanup) "owned process survived finally cleanup"
    Assert-True (-not $unrelated.HasExited) "cleanup terminated an unrelated process"

    $contractDigest = "a" * 64
    $developmentDigest = "b" * 64
    $releaseDigest = "c" * 64
    $evidencePath = Join-Path $TestRoot "evidence.json"
    New-TestEvidence $evidencePath $contractDigest $developmentDigest $releaseDigest
    $manifest = [pscustomobject]@{
        scenarios = @(
            [pscustomobject]@{ id = "root"; preconditions = @() },
            [pscustomobject]@{ id = "dependent"; preconditions = @("root") }
        )
    }
    $accepted = Read-Evidence $evidencePath $manifest $contractDigest $developmentDigest $releaseDigest
    Assert-True (@($accepted.records).Count -eq 2) "valid evidence was rejected"
    $mandatoryManifest = [pscustomobject]@{
        scenarios = @(
            [pscustomobject]@{ id = "root"; mandatory = $true },
            [pscustomobject]@{ id = "dependent"; mandatory = $true }
        )
    }
    Assert-MandatoryEvidenceCompleteness $mandatoryManifest @($accepted.records)
    Assert-Throws {
        Assert-MandatoryEvidenceCompleteness $mandatoryManifest @($accepted.records | Where-Object { $_.scenarioId -ne "dependent" })
    } "missing mandatory evidence was accepted"
    Assert-Throws {
        Read-Evidence $evidencePath $manifest ("d" * 64) $developmentDigest $releaseDigest
    } "stale evidence was accepted"

    $outOfOrder = Get-Content -LiteralPath $evidencePath -Raw -Encoding utf8 | ConvertFrom-Json
    $outOfOrder.records[0].completedAt = "2026-01-01T00:00:02Z"
    [System.IO.File]::WriteAllText(
        $evidencePath,
        (($outOfOrder | ConvertTo-Json -Depth 8) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Assert-Throws {
        Read-Evidence $evidencePath $manifest $contractDigest $developmentDigest $releaseDigest
    } "out-of-order evidence was accepted"

    $fakeBuild = Join-Path $TestRoot "fake-build"
    [System.IO.Directory]::CreateDirectory($fakeBuild) | Out-Null
    $currentHostPath = (Get-Process -Id $PID).Path
    Copy-Item -LiteralPath $currentHostPath -Destination (Join-Path $fakeBuild "iHomeland.exe")
    $script:OwnedProcesses = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
    $script:CleanupFailure = ""
    Assert-Throws {
        Invoke-PlayerStorageCleanup $fakeBuild (Join-Path $TestRoot "storage") @("qualification-test")
    } "storage owner cleanup failure was accepted"
    Assert-True ($script:CleanupFailure -eq "secure-session-cleanup-failed") "cleanup failure was not recorded independently"

    $toolText = [System.IO.File]::ReadAllText($ToolPath)
    Assert-True ($toolText -notmatch 'IsPathFullyQualified') "qualification tool uses an API unavailable to Windows PowerShell 5.1"
    Assert-True ($toolText -match '(?s)finally\s*\{\s*Stop-OwnedProcesses') "Ctrl+C cannot reach the exact owned-process cleanup path"
    Assert-True ($toolText -match 'ValidateSet\("validate", "diagnose", "automatic"') "diagnose action is not a closed command contract"
    Assert-True ($toolText -match 'qualificationEvidence\s*=\s*\$false') "diagnostic output can be mistaken for qualification evidence"
    Assert-True (
        $toolText -match 'openspec\.cmd validate --all --strict --no-interactive') `
        "qualification governance no longer validates all current specs and changes"
    Assert-True (
        $toolText -notmatch 'openspec\.cmd validate qualify-client-v1') `
        "qualification governance still depends on an archived change name"
    Assert-True (
        $toolText -match
            'Invoke-OwnedProcess \$PowerShellHostPath .+client-qualification\.tests\.ps1') `
        "qualification tool regression is not owned by the current PowerShell 7 host"
    $cleanupFunction = [regex]::Match(
        $toolText,
        '(?s)function Invoke-PlayerStorageCleanup\s*\{.*?\n\}')
    Assert-True $cleanupFunction.Success "storage cleanup owner function is missing"
    Assert-True ($cleanupFunction.Value -match 'ihomelandQualificationMode.+cleanup') "storage cleanup does not route through Player owner"
    Assert-True ($cleanupFunction.Value -notmatch 'Remove-Item|Directory\]::Delete|File\]::Delete') "qualification tool directly deletes secure records"

    $localScriptBytes = [System.IO.File]::ReadAllBytes($LocalToolPath)
    Assert-True (
        $localScriptBytes.Length -ge 3 -and
        $localScriptBytes[0] -eq 0xEF -and
        $localScriptBytes[1] -eq 0xBB -and
        $localScriptBytes[2] -eq 0xBF) `
        "client local qualification script lost its Windows PowerShell 5.1 UTF-8 BOM"
    $localTokens = $null
    $localErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile(
        $LocalToolPath,
        [ref]$localTokens,
        [ref]$localErrors) | Out-Null
    Assert-True (@($localErrors).Count -eq 0) "client local qualification composition is not parseable"
    $localToolText = [System.IO.File]::ReadAllText($LocalToolPath)
    Assert-True (
        $localToolText -match 'ValidateSet\("soak", "operator"\)') `
        "client local qualification action contract is not closed"
    Assert-True (
        $localToolText -match
            'Join-Path \$RunDirectory "automatic-evidence\.json"') `
        "client local qualification is not bound to the qualification owner's automatic evidence"
    Assert-True (
        $localToolText.Contains(
            '("operator-attempts\" + [Guid]::NewGuid().ToString("N"))') -and
        $localToolText.Contains(
            '$root = Join-Path $AttemptRoot "product-fault"') -and
        $localToolText.Contains(
            '$root = Join-Path $AttemptRoot "server-restart"')) `
        "client local operator does not isolate coordination state per attempt"
    Assert-True (
        $localToolText -match 'Stop-Process\s+`\s*\r?\n\s*-Id \$playerProcess\.Id' -and
        $localToolText -match 'Stop-Process -Id \$ServerProcess\.Id') `
        "client local qualification cleanup is not bound to exact owned PIDs"
    Assert-True (
        $localToolText -match '(?s)-Action down\s+`\s*\r?\n\s*-RunId \$StorageRunId') `
        "client local qualification storage cleanup lost exact run ownership"
    Assert-True (
        $localToolText -match '(?s)foreach \(\$name in \$EnvironmentNames\).*PreviousEnvironment\[\$name\]') `
        "client local qualification does not restore inherited environment"
    Assert-True (
        $localToolText -match '"PSModulePath"' -and
        $localToolText -match
            '\$env:PSModulePath\s*=\s*\(@\(\$windowsPowerShellModuleRoot\)\s*\+\s*\$moduleEntries\)') `
        "client local qualification does not prioritize and restore Windows PowerShell modules"
    Assert-True (
        $localToolText -match '(?s)& powershell\.exe.+-EncodedCommand \$encodedGoBuildCommand' -and
        $localToolText -notmatch '(?m)^\s*& \$GoTool\b') `
        "client local qualification does not isolate the process-level Go CLI"
    Assert-True (
        $localToolText -match '(?s)& \$PowerShell7Path.+-File \$QualificationTool.+-Action soak' -and
        $localToolText -notmatch '(?m)^\s*& \$QualificationTool\b') `
        "client local qualification does not isolate the qualification CLI"
    Assert-True (
        $localToolText -match
            '@\(\$powerShell7ModuleRoot\)\s*\+\s*\$powerShell7ModuleEntries') `
        "client local qualification does not prioritize PowerShell 7 modules for the qualification CLI"
    Assert-True (
        $localToolText -match '\[Security\.Cryptography\.SHA256\]::Create\(\)' -and
        $localToolText -match '\[IO\.File\]::OpenRead\(\$Path\)' -and
        $localToolText -notmatch 'Get-FileHash') `
        "client local qualification hash still depends on ambient PowerShell modules"
    $localStartOperatorFunction = [regex]::Match(
        $localToolText,
        '(?s)function Start-OperatorPlayer\s*\{.*?\n\}')
    Assert-True $localStartOperatorFunction.Success "client local Player owner function is missing"
    Assert-True (
        $localStartOperatorFunction.Value -notmatch
            'IHOMELAND_QUALIFICATION_(USERNAME|PASSWORD)|Credential\.(Username|Password)') `
        "client local qualification leaked credentials into process arguments"
    foreach ($scenarioId in @(
        "two-player-product-flow",
        "two-player-channel-faults",
        "two-player-server-restart"
    )) {
        Assert-True (
            $localToolText.Contains('"' + $scenarioId + '"')) `
            "client local qualification lost mandatory operator evidence: $scenarioId"
    }

    Write-Host "[OK] Client qualification tool regression tests passed."
}
finally {
    if ($null -ne $unrelated) {
        try {
            $unrelated.Refresh()
            if (-not $unrelated.HasExited) {
                Stop-Process -Id $unrelated.Id -Force -ErrorAction Stop
                $unrelated.WaitForExit(5000) | Out-Null
            }
        }
        finally { $unrelated.Dispose() }
    }
    if (Test-Path -LiteralPath $resolvedTestRoot -PathType Container) {
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force
    }
}
