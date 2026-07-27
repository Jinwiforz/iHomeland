#requires -Version 7.0

[CmdletBinding()]
# 该入口是使用者和代理唯一需要理解的项目质量编排入口；底层脚本继续拥有实际验证逻辑。
param(
    # Action 明确区分只读影响面、change 检查、单场景诊断和用户授权的最终资格。
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("impact", "check-change", "diagnose", "qualify")]
    [string]$Action,

    # Change 选择包含 validation.json 的 OpenSpec change；impact/check-change 必填。
    [string]$Change = "",

    # Scenario 只供 diagnose 使用，并由 battle qualification corpus 校验。
    [string]$Scenario = "",

    # Candidate 只供 qualify 使用，必须是 current clean HEAD 的完整或可解析 commit。
    [string]$Candidate = "",

    # DryRun 只允许 impact/check-change 展示执行计划，不调用任何 owner。
    [switch]$DryRun,

    # TimeoutSeconds 传递给单次 battle diagnose 或每个最终 B0.6 action。
    [ValidateRange(60, 14400)]
    [int]$TimeoutSeconds = 7200
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$CatalogPath = Join-Path $PSScriptRoot "catalog.json"
$CatalogSchemaPath = Join-Path $PSScriptRoot "catalog.schema.json"
$PlanSchemaPath = Join-Path $PSScriptRoot "validation-plan.schema.json"
$PlanModulePath = Join-Path $PSScriptRoot "QualityPlan.psm1"
Import-Module $PlanModulePath -Force

$SupportedCheckIds = @(
    "quality-contract",
    "battle-profile-validate",
    "battle-wire-validate",
    "simulation-control-validate",
    "battle-qualification-validate",
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
    "openspec-change-strict",
    "final-product-qualification"
)

# Write-QualityStage 输出不含 endpoint、credential 或本机路径的稳定阶段信息。
function Write-QualityStage {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)][string]$Message
    )
    Write-Host "[$Label] $Message"
}

# Invoke-CheckedOwner 调用已有 owner 并把非零退出统一转换为当前 check failure。
function Invoke-CheckedOwner {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$CaptureOutput
    )

    if ($CaptureOutput) {
        $output = @(& $FilePath @Arguments)
        $exitCode = $LASTEXITCODE
        foreach ($line in $output) {
            Write-Host $line
        }
        if ($exitCode -ne 0) {
            throw "$Label 失败"
        }
        return $output
    }
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Label 失败"
    }
}

# Invoke-ProjectPowerShell 通过同一 pwsh host 调用仓库脚本，避免复制其实现。
function Invoke-ProjectPowerShell {
    param(
        [Parameter(Mandatory = $true)][string]$ScriptPath,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$CaptureOutput
    )

    $pwsh = (Get-Command pwsh.exe -ErrorAction Stop).Source
    $ownerArguments = @("-NoLogo", "-NoProfile", "-File", $ScriptPath) + $Arguments
    return Invoke-CheckedOwner `
        -FilePath $pwsh `
        -Arguments $ownerArguments `
        -Label $Label `
        -CaptureOutput:$CaptureOutput
}

# Get-ChangeChecks 解析并验证 change plan，任何 final-only 或任意命令注入都会在执行前失败。
function Get-ChangeChecks {
    param([Parameter(Mandatory = $true)][string]$ChangeName)

    if ($ChangeName -notmatch '^[a-z0-9]+(?:-[a-z0-9]+)*$') {
        throw "Change 名称格式非法"
    }
    $changeRoot = Join-Path $RepositoryRoot ("openspec\changes\" + $ChangeName)
    if (-not (Test-Path -LiteralPath $changeRoot -PathType Container)) {
        throw "OpenSpec change 不存在"
    }
    $catalog = Get-QualityCatalog `
        -CatalogPath $CatalogPath `
        -SchemaPath $CatalogSchemaPath `
        -SupportedCheckIds $SupportedCheckIds
    $plan = Get-QualityPlan `
        -PlanPath (Join-Path $changeRoot "validation.json") `
        -SchemaPath $PlanSchemaPath `
        -ExpectedChange $ChangeName
    return Resolve-QualityPlanChecks -Catalog $catalog -Plan $plan
}

# Show-Impact 以稳定顺序展示 owner、成本分类和选择原因，不运行任何验证。
function Show-Impact {
    param([Parameter(Mandatory = $true)]$Checks)

    Write-QualityStage "IMPACT" "change=$Change checks=$(@($Checks).Count)"
    foreach ($check in $Checks) {
        Write-Host ("- {0} [{1}] owner={2}: {3}" -f
            $check.Id, $check.Class, $check.Owner, $check.Reason)
    }
}

# Invoke-CppTargeted 只增量构建 CI preset，并用登记 regex 缩小 CTest 集合。
function Invoke-CppTargeted {
    param(
        [Parameter(Mandatory = $true)][string]$Regex,
        [Parameter(Mandatory = $true)][string]$Label
    )
    $cppTool = Join-Path $RepositoryRoot "tools\cpp\cpp.ps1"
    Invoke-ProjectPowerShell `
        -ScriptPath $cppTool `
        -Arguments @("build", "-Preset", "windows-msvc-ci") `
        -Label "$Label build"
    Invoke-ProjectPowerShell `
        -ScriptPath $cppTool `
        -Arguments @("test", "-Preset", "windows-msvc-ci", "-TestRegex", $Regex) `
        -Label "$Label tests"
}

# Invoke-GoTargeted 使用项目锁定 SDK 执行显式 package 集合，不扩张到全仓 race/fuzz。
function Invoke-GoTargeted {
    param(
        [Parameter(Mandatory = $true)][string[]]$Packages,
        [Parameter(Mandatory = $true)][string]$Label
    )
    Invoke-ProjectPowerShell `
        -ScriptPath (Join-Path $RepositoryRoot "tools\go\go.ps1") `
        -Arguments (@("test", "-count=1") + $Packages) `
        -Label $Label
}

# Invoke-BattleDiagnostics 逐场景调用现有 diagnose owner；每次保持独立 run 与 cleanup。
function Invoke-BattleDiagnostics {
    param(
        [Parameter(Mandatory = $true)][string[]]$Scenarios,
        [Parameter(Mandatory = $true)][string]$Label
    )

    foreach ($scenario in $Scenarios) {
        Invoke-ProjectPowerShell `
            -ScriptPath (Join-Path $RepositoryRoot "tools\battle-qualification\battle-qualification.ps1") `
            -Arguments @(
                "diagnose",
                "-Scenario", $scenario,
                "-TimeoutSeconds", [string]$TimeoutSeconds
            ) `
            -Label "$Label scenario=$scenario"
    }
}

# Invoke-ChangeCheck 把 closed check ID 映射到现有 owner；没有默认分支可执行任意命令。
function Invoke-ChangeCheck {
    param([Parameter(Mandatory = $true)]$Check)

    Write-QualityStage "CHECK" ("{0} owner={1}" -f $Check.Id, $Check.Owner)
    switch ([string]$Check.Id) {
        "quality-contract" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $PSScriptRoot "quality.tests.ps1") `
                -Arguments @() `
                -Label "quality contract"
        }
        "battle-profile-validate" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\battle-network-profile\battle-network-profile.ps1") `
                -Arguments @("validate") `
                -Label "battle profile validation"
        }
        "battle-wire-validate" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\secure-battle-transport\secure-battle-transport.ps1") `
                -Arguments @("validate-corpus") `
                -Label "battle wire validation"
        }
        "simulation-control-validate" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\simulation-control\simulation-control.ps1") `
                -Arguments @("validate") `
                -Label "simulation control validation"
        }
        "battle-qualification-validate" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\battle-qualification\battle-qualification.ps1") `
                -Arguments @("validate") `
                -Label "battle qualification validation"
        }
        "proto-verify" {
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\proto\proto.ps1") `
                -Arguments @("verify") `
                -Label "proto verification"
        }
        "cpp-handshake-targeted" {
            Invoke-CppTargeted `
                -Regex '^battle\.(qualification\.protocol-client|crypto\.|handshake\.|transport\.udp-listener|transport\.endpoint-rebind|session\.)' `
                -Label "handshake C++"
        }
        "go-handshake-targeted" {
            Invoke-GoTargeted `
                -Packages @(
                    "./internal/battleticket/...",
                    "./internal/battleentry/...",
                    "./internal/battleticketcontrol/...",
                    "./internal/simulationcontrol/process/..."
                ) `
                -Label "handshake Go tests"
        }
        "cpp-resync-targeted" {
            Invoke-CppTargeted `
                -Regex '^battle\.(transport\.kcp-adapter|session\.ingress-replication)$|^simulation\.config\.profile$' `
                -Label "resync C++"
        }
        "go-resync-targeted" {
            Invoke-GoTargeted `
                -Packages @("./internal/battlequalification/...", "./internal/simulationcontrol/...") `
                -Label "resync Go tests"
        }
        "cpp-acknowledgement-targeted" {
            Invoke-CppTargeted `
                -Regex '^battle\.(qualification\.protocol-client|protocol\.lite-round-trip|session\.ingress-replication)$|^simulation\.instance\.lifecycle$' `
                -Label "acknowledgement C++"
        }
        "go-acknowledgement-targeted" {
            Invoke-GoTargeted `
                -Packages @("./internal/battlequalification/...", "./internal/contract/...") `
                -Label "acknowledgement Go tests"
        }
        "battle-handshake-diagnose" {
            Invoke-BattleDiagnostics `
                -Scenarios @(
                    "real-clean-default",
                    "security-proof-forgery",
                    "lifecycle-valid-endpoint-rebind"
                ) `
                -Label "handshake representative diagnostics"
        }
        "battle-resync-diagnose" {
            Invoke-BattleDiagnostics `
                -Scenarios @(
                    "real-baseline-gap",
                    "real-kcp-retransmit",
                    "real-raw-loss-burst"
                ) `
                -Label "resync representative diagnostics"
        }
        "battle-acknowledgement-diagnose" {
            Invoke-BattleDiagnostics `
                -Scenarios @(
                    "real-clean-default",
                    "real-reorder-duplicate",
                    "lifecycle-visitor-reconnect"
                ) `
                -Label "acknowledgement representative diagnostics"
        }
        "battle-qualification-representative" {
            Invoke-BattleDiagnostics `
                -Scenarios @(
                    "real-baseline-gap",
                    "capacity-default-capacity",
                    "security-proof-forgery",
                    "lifecycle-valid-endpoint-rebind"
                ) `
                -Label "battle qualification representative diagnostics"
        }
        "openspec-change-strict" {
            Invoke-CheckedOwner `
                -FilePath (Get-Command openspec -ErrorAction Stop).Source `
                -Arguments @("validate", $Change, "--strict") `
                -Label "OpenSpec change strict"
        }
        "final-product-qualification" {
            throw "final-only check 只能由显式 qualify 动作执行"
        }
        default {
            throw "check implementation 未登记"
        }
    }
}

# Assert-FrozenCandidate 要求显式 candidate 等于 current HEAD 且工作区完全干净。
function Assert-FrozenCandidate {
    param([Parameter(Mandatory = $true)][string]$RequestedCandidate)

    if (-not $RequestedCandidate) {
        throw "qualify 必须显式传入 Candidate"
    }
    $head = (& git -C $RepositoryRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "无法解析 current HEAD"
    }
    $resolved = (& git -C $RepositoryRoot rev-parse $RequestedCandidate).Trim()
    if ($LASTEXITCODE -ne 0 -or $resolved -cne $head) {
        throw "Candidate 必须等于 current HEAD"
    }
    $status = @(& git -C $RepositoryRoot status --porcelain=v1 --untracked-files=all)
    if ($LASTEXITCODE -ne 0) {
        throw "无法检查 candidate worktree"
    }
    if ($status.Count -ne 0) {
        throw "最终资格要求 clean worktree"
    }
    return $head
}

# Get-RunId 从 battle qualification 的稳定输出读取唯一 RunId。
function Get-RunId {
    param(
        [Parameter(Mandatory = $true)][object[]]$Output,
        [Parameter(Mandatory = $true)][string]$Label
    )
    $matches = @($Output | ForEach-Object {
        if ([string]$_ -match '^RunId=(bqrun_[0-9a-f]{32})$') {
            $Matches[1]
        }
    })
    if ($matches.Count -ne 1) {
        throw "$Label 未返回唯一 RunId"
    }
    return [string]$matches[0]
}

# Invoke-FinalQualification 编排既有严格 owner；本函数不复制其测试、矩阵或报告判断。
function Invoke-FinalQualification {
    param([Parameter(Mandatory = $true)][string]$FrozenCandidate)

    Write-QualityStage "QUALIFY" "candidate=$FrozenCandidate"
    Invoke-ProjectPowerShell `
        -ScriptPath (Join-Path $RepositoryRoot "tools\battle-network-profile\battle-network-profile.ps1") `
        -Arguments @("validate") `
        -Label "final profile validation"
    Invoke-ProjectPowerShell `
        -ScriptPath (Join-Path $RepositoryRoot "tools\cpp\cpp.ps1") `
        -Arguments @("verify", "-Preset", "windows-msvc-ci") `
        -Label "final C++ qualification"
    Invoke-ProjectPowerShell `
        -ScriptPath (Join-Path $RepositoryRoot "tools\simulation-control\simulation-control.ps1") `
        -Arguments @("verify", "-ReuseQualifiedCppEvidence") `
        -Label "final simulation control qualification"
    Invoke-ProjectPowerShell `
        -ScriptPath (Join-Path $RepositoryRoot "tools\secure-battle-transport\secure-battle-transport.ps1") `
        -Arguments @("finalize") `
        -Label "final secure transport qualification"

    $battleTool = Join-Path $RepositoryRoot "tools\battle-qualification\battle-qualification.ps1"
    Invoke-ProjectPowerShell `
        -ScriptPath $battleTool `
        -Arguments @("validate") `
        -Label "final battle qualification validation"
    $runAOutput = Invoke-ProjectPowerShell `
        -ScriptPath $battleTool `
        -Arguments @("verify", "-TimeoutSeconds", [string]$TimeoutSeconds) `
        -Label "battle verify A" `
        -CaptureOutput
    $runA = Get-RunId -Output $runAOutput -Label "battle verify A"
    $runBOutput = Invoke-ProjectPowerShell `
        -ScriptPath $battleTool `
        -Arguments @("verify", "-TimeoutSeconds", [string]$TimeoutSeconds) `
        -Label "battle verify B" `
        -CaptureOutput
    $runB = Get-RunId -Output $runBOutput -Label "battle verify B"
    $soakOutput = Invoke-ProjectPowerShell `
        -ScriptPath $battleTool `
        -Arguments @("soak", "-TimeoutSeconds", [string]$TimeoutSeconds) `
        -Label "battle soak" `
        -CaptureOutput
    $soakRun = Get-RunId -Output $soakOutput -Label "battle soak"
    Invoke-ProjectPowerShell `
        -ScriptPath $battleTool `
        -Arguments @(
            "finalize",
            "-RunA", $runA,
            "-RunB", $runB,
            "-SoakRun", $soakRun
        ) `
        -Label "battle finalize"
}

try {
    switch ($Action) {
        "impact" {
            if (-not $Change) {
                throw "impact 必须传入 Change"
            }
            $checks = Get-ChangeChecks -ChangeName $Change
            Show-Impact -Checks $checks
        }
        "check-change" {
            if (-not $Change) {
                throw "check-change 必须传入 Change"
            }
            $checks = Get-ChangeChecks -ChangeName $Change
            Show-Impact -Checks $checks
            if ($DryRun) {
                Write-QualityStage "DRY-RUN" "未执行任何 owner check"
                break
            }
            foreach ($check in $checks) {
                Invoke-ChangeCheck -Check $check
            }
            Write-QualityStage "PASS" "change=$Change"
        }
        "diagnose" {
            if ($DryRun) {
                throw "diagnose 不支持 DryRun；使用 impact 预览 change checks"
            }
            if (-not $Scenario) {
                throw "diagnose 必须传入 Scenario"
            }
            Invoke-ProjectPowerShell `
                -ScriptPath (Join-Path $RepositoryRoot "tools\battle-qualification\battle-qualification.ps1") `
                -Arguments @(
                    "diagnose",
                    "-Scenario", $Scenario,
                    "-TimeoutSeconds", [string]$TimeoutSeconds
                ) `
                -Label "battle diagnostic"
        }
        "qualify" {
            if ($DryRun) {
                throw "qualify 不支持 DryRun；最终资格必须针对真实冻结 candidate"
            }
            $frozenCandidate = Assert-FrozenCandidate -RequestedCandidate $Candidate
            Invoke-FinalQualification -FrozenCandidate $frozenCandidate
        }
    }
    exit 0
}
catch {
    Write-Error $_.Exception.Message
    exit 1
}
