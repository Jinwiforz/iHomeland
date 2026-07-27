[CmdletBinding()]
param(
    # AllowSourceDrift 只供 tooling validate；mutation 仍验证 corpus、coverage 与预算边界。
    [switch]$AllowSourceDrift
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$qualificationRoot = Join-Path $repositoryRoot "shared\contracts\fixtures\battle\qualification"
$modulePath = Join-Path $PSScriptRoot "internal\QualificationCorpus.psm1"
Import-Module $modulePath -Force

# Assert-ExpectedFailure 证明 mutation 被 validator 拒绝，且错误必须命中稳定类别。
function Assert-ExpectedFailure {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Action,
        [Parameter(Mandatory = $true)][string]$ExpectedFragment
    )

    try {
        & $Action
    }
    catch {
        if ($_.Exception.Message -notlike "*$ExpectedFragment*") {
            throw "failure regression 返回了错误类别：$($_.Exception.Message)"
        }
        return
    }
    throw "failure regression 未拒绝 mutation：$ExpectedFragment"
}

# Set-JsonDocument 只修改临时 corpus，并以 UTF-8 no-BOM 写入稳定 JSON。
function Set-JsonDocument {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Document
    )

    $json = $Document | ConvertTo-Json -Depth 100
    [IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
}

# Update-ManifestDigest 让 mutation 越过文件摘要门，验证更深层语义 gate。
function Update-ManifestDigest {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $manifestPath = Join-Path $Root "manifest.json"
    $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding utf8 | ConvertFrom-Json
    $entry = @($manifest.files | Where-Object path -ceq $RelativePath)
    if ($entry.Count -ne 1) {
        throw "临时 manifest 缺少唯一文件：$RelativePath"
    }
    $entry[0].sha256 = Get-QualificationSha256 -Path (Join-Path $Root $RelativePath)
    Set-JsonDocument -Path $manifestPath -Document $manifest
}

# Reset-QualificationCopy 重建隔离副本，保证每个 mutation 只表达一个失败意图。
function Reset-QualificationCopy {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$DestinationParent,
        [Parameter(Mandatory = $true)][string]$Destination
    )

    if (Test-Path -LiteralPath $Destination) {
        Remove-Item -LiteralPath $Destination -Recurse -Force
    }
    Copy-Item -LiteralPath $Source -Destination $DestinationParent -Recurse
}

# Assert-JsonMutationRejected 修改单个 tracked document，并验证越过摘要后的语义 gate。
function Assert-JsonMutationRejected {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][string]$TemporaryRoot,
        [Parameter(Mandatory = $true)][string]$CopyRoot,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][scriptblock]$Mutation,
        [Parameter(Mandatory = $true)][string]$ExpectedFragment,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot
    )

    Write-Host "[B0.6 regression] $RelativePath -> $ExpectedFragment"
    Reset-QualificationCopy `
        -Source $SourceRoot `
        -DestinationParent $TemporaryRoot `
        -Destination $CopyRoot
    $path = Join-Path $CopyRoot $RelativePath
    $document = Get-Content -LiteralPath $path -Raw -Encoding utf8 | ConvertFrom-Json
    & $Mutation $document
    Set-JsonDocument -Path $path -Document $document
    Update-ManifestDigest -Root $CopyRoot -RelativePath $RelativePath
    Assert-ExpectedFailure -ExpectedFragment $ExpectedFragment -Action {
        Invoke-BattleQualificationCorpusValidation `
            -QualificationRoot $CopyRoot `
            -RepositoryRoot $RepositoryRoot `
            -AllowSourceDrift:$AllowSourceDrift
    }
}

$before = Get-QualificationTreeDigest -QualificationRoot $qualificationRoot
$first = Invoke-BattleQualificationCorpusValidation `
    -QualificationRoot $qualificationRoot `
    -RepositoryRoot $repositoryRoot `
    -AllowSourceDrift:$AllowSourceDrift
$second = Invoke-BattleQualificationCorpusValidation `
    -QualificationRoot $qualificationRoot `
    -RepositoryRoot $repositoryRoot `
    -AllowSourceDrift:$AllowSourceDrift
if ($first -cne $second -or $first -cne $before) {
    throw "qualification corpus 连续校验摘要不一致"
}

$temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("ihomeland-battle-qualification-" + [Guid]::NewGuid().ToString("N"))
if (-not ([IO.Path]::GetFullPath($temporaryRoot)).StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase)) {
    throw "failure regression 临时目录逃逸"
}
New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
try {
    Copy-Item -LiteralPath $qualificationRoot -Destination $temporaryRoot -Recurse
    $copyRoot = Join-Path $temporaryRoot "qualification"

    $bindingPath = Join-Path $copyRoot "upstream-binding.json"
    $binding = Get-Content -LiteralPath $bindingPath -Raw -Encoding utf8 | ConvertFrom-Json
    $binding.sources[0].sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
    Set-JsonDocument -Path $bindingPath -Document $binding
    Update-ManifestDigest -Root $copyRoot -RelativePath "upstream-binding.json"
    Assert-ExpectedFailure -ExpectedFragment "upstream source 摘要漂移" -Action {
        Invoke-BattleQualificationCorpusValidation -QualificationRoot $copyRoot -RepositoryRoot $repositoryRoot
    }

    Remove-Item -LiteralPath $copyRoot -Recurse -Force
    Copy-Item -LiteralPath $qualificationRoot -Destination $temporaryRoot -Recurse
    $executionPath = Join-Path $copyRoot "fault-execution.json"
    $execution = Get-Content -LiteralPath $executionPath -Raw -Encoding utf8 | ConvertFrom-Json
    $execution.scenarios = @($execution.scenarios | Select-Object -Skip 1)
    Set-JsonDocument -Path $executionPath -Document $execution
    Update-ManifestDigest -Root $copyRoot -RelativePath "fault-execution.json"
    Assert-ExpectedFailure -ExpectedFragment "closed schema" -Action {
        Invoke-BattleQualificationCorpusValidation `
            -QualificationRoot $copyRoot `
            -RepositoryRoot $repositoryRoot `
            -AllowSourceDrift:$AllowSourceDrift
    }

    Remove-Item -LiteralPath $copyRoot -Recurse -Force
    Copy-Item -LiteralPath $qualificationRoot -Destination $temporaryRoot -Recurse
    $metricsPath = Join-Path $copyRoot "metrics.json"
    $metrics = Get-Content -LiteralPath $metricsPath -Raw -Encoding utf8 | ConvertFrom-Json
    $metrics.metrics[0].maximum = 32768
    Set-JsonDocument -Path $metricsPath -Document $metrics
    Update-ManifestDigest -Root $copyRoot -RelativePath "metrics.json"
    Assert-ExpectedFailure -ExpectedFragment "metric budget" -Action {
        Invoke-BattleQualificationCorpusValidation `
            -QualificationRoot $copyRoot `
            -RepositoryRoot $repositoryRoot `
            -AllowSourceDrift:$AllowSourceDrift
    }

    Remove-Item -LiteralPath $copyRoot -Recurse -Force
    Copy-Item -LiteralPath $qualificationRoot -Destination $temporaryRoot -Recurse
    $workloadPath = Join-Path $copyRoot "workloads.json"
    $workloads = Get-Content -LiteralPath $workloadPath -Raw -Encoding utf8 | ConvertFrom-Json
    $workloads.workloads[0].phases[0] = "C:\local-only"
    Set-JsonDocument -Path $workloadPath -Document $workloads
    Update-ManifestDigest -Root $copyRoot -RelativePath "workloads.json"
    Assert-ExpectedFailure -ExpectedFragment "closed schema" -Action {
        Invoke-BattleQualificationCorpusValidation `
            -QualificationRoot $copyRoot `
            -RepositoryRoot $repositoryRoot `
            -AllowSourceDrift:$AllowSourceDrift
    }

    $mutations = @(
        @{
            Path = "fault-execution.json"; Error = "fault seed"
            Apply = { param($document) $document.seed++ }
        },
        @{
            Path = "fault-execution.json"; Error = "closed schema"
            Apply = { param($document) $document.requiredDirections = @("uplink", "downlink") }
        },
        @{
            Path = "fault-execution.json"; Error = "fault phase"
            Apply = {
                param($document)
                $document.scenarios[1].sourcePhases = @(
                    $document.scenarios[1].sourcePhases | Select-Object -Skip 1
                )
            }
        },
        @{
            Path = "fault-execution.json"; Error = "fault impairment"
            Apply = {
                param($document)
                $document.scenarios[0].impairments = @(
                    $document.scenarios[0].impairments | Select-Object -Skip 1
                )
            }
        },
        @{
            Path = "fault-execution.json"; Error = "执行边界漂移"
            Apply = { param($document) $document.scenarios[0].sourceDurationTicks-- }
        },
        @{
            Path = "fault-execution.json"; Error = "closed schema"
            Apply = { param($document) $document.executionPolicy.measurementMilliseconds-- }
        },
        @{
            Path = "fault-execution.json"; Error = "closed schema"
            Apply = { param($document) $document.gatewayPolicy.maximumDatagramBytes-- }
        },
        @{
            Path = "metrics.json"; Error = "metric budget"
            Apply = { param($document) $document.metrics[0].maximum++ }
        },
        @{
            Path = "metrics.json"; Error = "复现容差"
            Apply = { param($document) $document.metrics[0].reproducibilityToleranceBasisPoints++ }
        },
        @{
            Path = "metrics.json"; Error = "metric source"
            Apply = { param($document) $document.metrics[3].source = "client" }
        },
        @{
            Path = "metrics.json"; Error = "method"
            Apply = { param($document) $document.metrics[3].method = "maximum-client-snapshot-receipt-gap" }
        },
        @{
            Path = "lifecycle-security.json"; Error = "closed schema"
            Apply = {
                param($document)
                $document.securityCases = @($document.securityCases | Select-Object -Skip 1)
            }
        },
        @{
            Path = "workloads.json"; Error = "actor 边界"
            Apply = { param($document) $document.workloads[3].battleActorCount = 5 }
        },
        @{
            Path = "workloads.json"; Error = "message cadence"
            Apply = { param($document) $document.messageCadence[0].lane = "kcp" }
        }
    )
    foreach ($mutation in $mutations) {
        Assert-JsonMutationRejected `
            -SourceRoot $qualificationRoot `
            -TemporaryRoot $temporaryRoot `
            -CopyRoot $copyRoot `
            -RelativePath $mutation.Path `
            -Mutation $mutation.Apply `
            -ExpectedFragment $mutation.Error `
            -RepositoryRoot $repositoryRoot
    }
}
finally {
    if (Test-Path -LiteralPath $temporaryRoot) {
        $resolvedTemporary = [IO.Path]::GetFullPath($temporaryRoot)
        if (-not $resolvedTemporary.StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase)) {
            throw "failure regression 拒绝清理非临时目录"
        }
        Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force
    }
}

if ((Get-QualificationTreeDigest -QualificationRoot $qualificationRoot) -cne $before) {
    throw "failure regression 改写了 source corpus"
}
Write-Output "BATTLE_QUALIFICATION_CORPUS_TEST_PASS digest=$before"
