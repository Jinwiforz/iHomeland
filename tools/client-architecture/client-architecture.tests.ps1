# 该回归验证架构报告的闭合shape、owner唯一性和迁移期report-only语义。
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ToolPath = Join-Path $PSScriptRoot "client-architecture.ps1"
$TestRoot = Join-Path $RepositoryRoot ".local\client-architecture-tests"
$ReportPath = Join-Path $TestRoot "report.json"

# Assert-True为架构工具回归提供稳定失败。
function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) {
        throw $Message
    }
}

[System.IO.Directory]::CreateDirectory($TestRoot) | Out-Null
try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $ToolPath `
        -Action report `
        -ReportPath $ReportPath | Out-Null
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $ReportPath -PathType Leaf)) {
        throw "client architecture report was not produced"
    }
    $report = Get-Content -LiteralPath $ReportPath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-True ($report.schemaVersion -eq 1) "architecture report schema version changed"
    Assert-True ($report.mode -eq "report-only") "migration report unexpectedly became a hard gate"
    Assert-True (-not $report.hardGateEnabled) "migration report claims hard gate is enabled"
    Assert-True (@($report.registry.owners).Count -eq 16) "owner registry lost a declared owner"
    Assert-True (@($report.registry.differences).Count -eq 0) "owner registry differs from current source"
    $allStates = @($report.registry.owners | ForEach-Object { @($_.stateKinds) })
    Assert-True (
        @($allStates | Select-Object -Unique).Count -eq $allStates.Count) `
        "state kind has more than one owner"
    Assert-True (@($report.assemblies).Count -ge 4) "asmdef inventory is incomplete"
    $foundationAssemblies = @($report.assemblies | Where-Object {
            $_.name -eq "IHomeland.Client.Foundation"
        })
    Assert-True ($foundationAssemblies.Count -eq 1) "Foundation assembly is missing or duplicated"
    Assert-True ($foundationAssemblies[0].noEngineReferences) `
        "Foundation assembly no longer rejects UnityEngine references"
    Assert-True (@($foundationAssemblies[0].references).Count -eq 0) `
        "Foundation assembly acquired a production assembly dependency"
    $foundationText = @(
        Get-ChildItem -LiteralPath (
            Join-Path $RepositoryRoot "client\Assets\App\Scripts\Foundation") `
            -Filter "*.cs" -File -Recurse |
        ForEach-Object { [System.IO.File]::ReadAllText($_.FullName) }) -join "`n"
    Assert-True (
        $foundationText -notmatch 'using\s+(UnityEngine|IHomeland\.Protocol|Google\.Protobuf|IHomeland\.Client\.(Application|Infrastructure|Presentation|Scenes))') `
        "Foundation source imports Engine, Protocol, or a downstream module"
    Assert-True (@($report.compositionResultReferences).Count -gt 0) `
        "composition result reference inventory unexpectedly became empty"
    Assert-True (@($report.serializedScriptReferences).Count -gt 0) `
        "Unity serialized script inventory unexpectedly became empty"
    Assert-True (@($report.hardGateViolations).Count -eq 0) `
        "report mode found a hard-gate violation"

    $verifyReportPath = Join-Path $TestRoot "verify.json"
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $ToolPath `
        -Action verify `
        -ReportPath $verifyReportPath | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "client architecture hard gate failed"
    }
    $verify = Get-Content -LiteralPath $verifyReportPath -Raw -Encoding UTF8 |
        ConvertFrom-Json
    Assert-True ($verify.mode -eq "hard-gate") "verify mode did not enable hard gate"
    Assert-True ($verify.hardGateEnabled) "verify report did not claim hard gate"
    Assert-True (@($verify.hardGateViolations).Count -eq 0) `
        "verify report contains hard-gate violations"
    Assert-True (@($verify.serializationIntegrity.missingSerializedScripts).Count -eq 0) `
        "serialized assets reference a missing project script"
    Assert-True (@($verify.modularity.flows).Count -gt 0) `
        "modularity report contains no flows"
    Assert-True (@($verify.modularity.adapters).Count -gt 0) `
        "modularity report contains no adapters"
    Write-Host "[OK] Client architecture report and hard-gate tests passed."
}
finally {
    if (Test-Path -LiteralPath $TestRoot -PathType Container) {
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
}
