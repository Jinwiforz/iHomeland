#requires -Version 7.0

[CmdletBinding()]
# 该回归只验证 quality plan/catalog/入口边界，不运行任何 production build 或资格矩阵。
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ModulePath = Join-Path $PSScriptRoot "QualityPlan.psm1"
$CatalogPath = Join-Path $PSScriptRoot "catalog.json"
$CatalogSchemaPath = Join-Path $PSScriptRoot "catalog.schema.json"
$PlanSchemaPath = Join-Path $PSScriptRoot "validation-plan.schema.json"
Import-Module $ModulePath -Force

$supported = @(
    "quality-contract",
    "battle-profile-validate",
    "battle-wire-validate",
    "simulation-control-validate",
    "battle-qualification-validate",
    "client-battle-runtime-validate",
    "proto-verify",
    "cpp-handshake-targeted",
    "go-handshake-targeted",
    "cpp-resync-targeted",
    "go-resync-targeted",
    "cpp-acknowledgement-targeted",
    "go-acknowledgement-targeted",
    "battle-handshake-diagnose",
    "battle-resync-diagnose",
    "battle-acknowledgement-diagnose",
    "battle-qualification-representative",
    "client-battle-runtime-targeted",
    "client-battle-runtime-unity",
    "openspec-change-strict",
    "final-product-qualification"
)

# Assert-Throws 要求回归动作稳定失败，避免异常 plan 被误解释为有效。
function Assert-Throws {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Action,
        [Parameter(Mandatory = $true)][string]$Label
    )
    $failed = $false
    try {
        & $Action
    }
    catch {
        $failed = $true
    }
    if (-not $failed) {
        throw "$Label 未稳定失败"
    }
}

# Invoke-QualityProcess 在独立进程验证公共入口退出码，避免测试作用域吞掉 exit。
function Invoke-QualityProcess {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $output = @(& (Get-Command pwsh.exe -ErrorAction Stop).Source `
        -NoLogo -NoProfile -File (Join-Path $PSScriptRoot "quality.ps1") @Arguments 2>&1)
    return [pscustomobject]@{
        ExitCode = $LASTEXITCODE
        Output = @($output | ForEach-Object { [string]$_ })
    }
}

$catalog = Get-QualityCatalog `
    -CatalogPath $CatalogPath `
    -SchemaPath $CatalogSchemaPath `
    -SupportedCheckIds $supported

$entryText = Get-Content `
    -LiteralPath (Join-Path $PSScriptRoot "quality.ps1") `
    -Raw -Encoding utf8
if ($entryText -notmatch '"capacity-default-capacity"' -or
    $entryText -match '"capacity-default-coop"') {
    throw "五人代表容量诊断未绑定 registered capacity workload"
}

$temporaryRoot = Join-Path $RepositoryRoot (
    ".tmp\quality-tests-" + [guid]::NewGuid().ToString("N")
)
$dryRunName = "quality-dry-run-" + [guid]::NewGuid().ToString("N")
$dryRunRoot = Join-Path $RepositoryRoot ("openspec\changes\" + $dryRunName)
$ownerFailureName = "quality-owner-failure-" + [guid]::NewGuid().ToString("N")
$ownerFailureRoot = Join-Path $RepositoryRoot ("openspec\changes\" + $ownerFailureName)
[void](New-Item -ItemType Directory -Path $temporaryRoot -Force)
try {
    $validPlanPath = Join-Path $temporaryRoot "valid.json"
    [IO.File]::WriteAllText(
        $validPlanPath,
        (@{
            schemaVersion = 1
            change = "sample-change"
            checks = @(
                @{
                    id = "battle-wire-validate"
                    reason = "wire contract 发生变化，需要验证 closed corpus"
                },
                @{
                    id = "openspec-change-strict"
                    reason = "change artifacts 必须通过 strict validation"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    $validPlan = Get-QualityPlan `
        -PlanPath $validPlanPath `
        -SchemaPath $PlanSchemaPath `
        -ExpectedChange "sample-change"
    $resolved = Resolve-QualityPlanChecks -Catalog $catalog -Plan $validPlan
    if ($resolved.Count -ne 2 -or $resolved[0].Id -cne "battle-wire-validate") {
        throw "valid plan 未按中央顺序解析"
    }

    $finalPlanPath = Join-Path $temporaryRoot "final.json"
    [IO.File]::WriteAllText(
        $finalPlanPath,
        (@{
            schemaVersion = 1
            change = "sample-change"
            checks = @(
                @{
                    id = "final-product-qualification"
                    reason = "尝试把最终资格注入普通 change"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    $finalPlan = Get-QualityPlan `
        -PlanPath $finalPlanPath `
        -SchemaPath $PlanSchemaPath `
        -ExpectedChange "sample-change"
    Assert-Throws {
        Resolve-QualityPlanChecks -Catalog $catalog -Plan $finalPlan
    } "final-only plan"

    $unknownPlanPath = Join-Path $temporaryRoot "unknown.json"
    [IO.File]::WriteAllText(
        $unknownPlanPath,
        (@{
            schemaVersion = 1
            change = "sample-change"
            checks = @(
                @{
                    id = "unknown-owner-command"
                    reason = "未知 owner 不得执行"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    $unknownPlan = Get-QualityPlan `
        -PlanPath $unknownPlanPath `
        -SchemaPath $PlanSchemaPath `
        -ExpectedChange "sample-change"
    Assert-Throws {
        Resolve-QualityPlanChecks -Catalog $catalog -Plan $unknownPlan
    } "unknown check"

    $commandPlanPath = Join-Path $temporaryRoot "command.json"
    [IO.File]::WriteAllText(
        $commandPlanPath,
        (@{
            schemaVersion = 1
            change = "sample-change"
            checks = @(
                @{
                    id = "battle-wire-validate"
                    reason = "运行 pwsh evil.ps1"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    Assert-Throws {
        Get-QualityPlan `
            -PlanPath $commandPlanPath `
            -SchemaPath $PlanSchemaPath `
            -ExpectedChange "sample-change"
    } "command text"

    [void](New-Item -ItemType Directory -Path $dryRunRoot -Force)
    [IO.File]::WriteAllText(
        (Join-Path $dryRunRoot "validation.json"),
        (@{
            schemaVersion = 1
            change = $dryRunName
            checks = @(
                @{
                    id = "battle-wire-validate"
                    reason = "dry-run 只预览登记 owner，不执行实际 corpus validator"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    $dryRun = Invoke-QualityProcess -Arguments @(
        "check-change",
        "-Change", $dryRunName,
        "-DryRun"
    )
    if ($dryRun.ExitCode -ne 0 -or -not ($dryRun.Output -match '\[DRY-RUN\]')) {
        throw "check-change dry-run 未稳定通过"
    }
    $joinedDryRun = $dryRun.Output -join "`n"
    if ($joinedDryRun -match '(?i)[A-Z]:\\|btk_[A-Za-z0-9_-]+|bts_[A-Za-z0-9_-]+|(?:proof|traffic|cookie)[-_ ]?key') {
        throw "impact/dry-run 输出包含路径或敏感凭据形态"
    }

    $unknownChange = Invoke-QualityProcess -Arguments @(
        "impact",
        "-Change", "missing-quality-change"
    )
    if ($unknownChange.ExitCode -eq 0) {
        throw "unknown change 未稳定失败"
    }

    [void](New-Item -ItemType Directory -Path $ownerFailureRoot -Force)
    [IO.File]::WriteAllText(
        (Join-Path $ownerFailureRoot "validation.json"),
        (@{
            schemaVersion = 1
            change = $ownerFailureName
            checks = @(
                @{
                    id = "openspec-change-strict"
                    reason = "不存在 artifacts 时 owner 必须把失败传播给统一入口"
                }
            )
        } | ConvertTo-Json -Depth 8),
        [Text.UTF8Encoding]::new($false)
    )
    $ownerFailure = Invoke-QualityProcess -Arguments @(
        "check-change",
        "-Change", $ownerFailureName
    )
    if ($ownerFailure.ExitCode -eq 0) {
        throw "owner failure 未传播"
    }

    $head = (& git -C $RepositoryRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "测试无法解析 current HEAD"
    }
    # temporaryRoot/ownerFailureRoot 保证 worktree 为 dirty，入口必须在任何 build 前拒绝。
    $dirtyCandidate = Invoke-QualityProcess -Arguments @(
        "qualify",
        "-Candidate", $head
    )
    if ($dirtyCandidate.ExitCode -eq 0 -or
        -not (($dirtyCandidate.Output -join "`n") -match 'clean worktree')) {
        throw "dirty candidate 未在 build 前稳定拒绝"
    }
}
finally {
    if (Test-Path -LiteralPath $dryRunRoot) {
        Remove-Item -LiteralPath $dryRunRoot -Recurse -Force
    }
    if (Test-Path -LiteralPath $ownerFailureRoot) {
        Remove-Item -LiteralPath $ownerFailureRoot -Recurse -Force
    }
    if (Test-Path -LiteralPath $temporaryRoot) {
        Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
    }
}

Write-Output "QUALITY_CONTRACT_PASS"
