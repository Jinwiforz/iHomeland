# 该回归入口只在隔离临时副本中制造坏数据，验证 battle model 门禁保持 fail closed。
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ValidatorPath = Join-Path $PSScriptRoot "battle-model.ps1"
$SourceModelRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model"
$Utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$TemporaryBase = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd('\') + '\'
$TestRoot = Join-Path ([System.IO.Path]::GetTempPath()) (
    "ihomeland-battle-model-tests-{0}" -f [guid]::NewGuid().ToString("N"))
$Passed = 0

# Get-TreeDigest 生成只读源树摘要，用于证明连续校验没有修改 corpus。
function Get-TreeDigest {
    param([string]$Root)
    $records = @(
        Get-ChildItem -LiteralPath $Root -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            $relativeRootLength =
                [System.IO.Path]::GetFullPath($Root).TrimEnd('\').Length + 1
            $relative = $_.FullName.Substring($relativeRootLength).Replace('\', '/')
            "{0}:{1}" -f $relative, (
                Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        })
    $bytes = $Utf8NoBom.GetBytes(($records -join "`n"))
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $digest = [System.BitConverter]::ToString($sha.ComputeHash($bytes))
        return $digest.Replace("-", "").ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

# Write-CanonicalJson 仅写入当前测试的临时副本，并保持 corpus 所要求的编码与换行。
function Write-CanonicalJson {
    param([string]$Path, [object]$Value)
    $text = ($Value | ConvertTo-Json -Depth 100).Replace("`r`n", "`n").TrimEnd("`n") + "`n"
    [System.IO.File]::WriteAllText($Path, $text, $Utf8NoBom)
}

# Invoke-Validator 捕获独立进程的退出码与低敏输出。
function Invoke-Validator {
    param([string]$ModelRoot)
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = @(
            & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $ValidatorPath `
                -Action validate -ModelRoot $ModelRoot 2>&1 |
            ForEach-Object { $_.ToString() })
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = ($output -join "`n")
    }
}

# New-IsolatedModelCopy 为一个变异创建全新的副本，避免失败用例彼此污染。
function New-IsolatedModelCopy {
    param([string]$Name)
    $caseRoot = Join-Path $TestRoot $Name
    $modelRoot = Join-Path $caseRoot "model"
    [System.IO.Directory]::CreateDirectory($caseRoot) | Out-Null
    Copy-Item -LiteralPath $SourceModelRoot -Destination $modelRoot -Recurse
    return $modelRoot
}

# Update-ManifestCaseDigest 让语义变异越过文件摘要门，抵达目标约束。
function Update-ManifestCaseDigest {
    param([string]$ModelRoot, [string]$RelativeCasePath)
    $manifestPath = Join-Path $ModelRoot "manifest.json"
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    $entry = @($manifest.cases | Where-Object { $_.path -eq $RelativeCasePath })
    if ($entry.Count -ne 1) {
        throw "test setup failed: manifest case lookup is not unique"
    }
    $casePath = Join-Path $ModelRoot $RelativeCasePath.Replace('/', '\')
    $entry[0].file_sha256 = (
        Get-FileHash -LiteralPath $casePath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-CanonicalJson $manifestPath $manifest
}

# Assert-Pass 验证成功路径，并保持失败信息不包含 corpus 内容。
function Assert-Pass {
    param([string]$Name, [object]$Result)
    if ($Result.ExitCode -ne 0) {
        throw "test failed: $Name expected pass; $($Result.Output)"
    }
    $script:Passed++
}

# Assert-Fail 验证坏数据被稳定拒绝。
function Assert-Fail {
    param([string]$Name, [object]$Result, [string]$Pattern)
    if ($Result.ExitCode -eq 0) {
        throw "test failed: $Name expected failure"
    }
    if ($Result.Output -notmatch $Pattern) {
        throw "test failed: $Name returned an unexpected diagnostic; $($Result.Output)"
    }
    $script:Passed++
}

# Invoke-MutationTest 对每个坏数据使用独立副本，并在断言后立即回收。
function Invoke-MutationTest {
    param(
        [string]$Name,
        [scriptblock]$Mutation,
        [string]$ExpectedPattern = "battle model validation failed"
    )
    $modelRoot = New-IsolatedModelCopy $Name
    try {
        & $Mutation $modelRoot
        Assert-Fail $Name (Invoke-Validator $modelRoot) $ExpectedPattern
    } finally {
        $caseRoot = [System.IO.Path]::GetFullPath(
            [System.IO.Directory]::GetParent($modelRoot).FullName)
        if (-not ($caseRoot + '\').StartsWith(
                [System.IO.Path]::GetFullPath($TestRoot).TrimEnd('\') + '\',
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "test cleanup escaped the isolated test root"
        }
        Remove-Item -LiteralPath $caseRoot -Recurse -Force
    }
}

if (-not (Test-Path -LiteralPath $ValidatorPath -PathType Leaf) -or
    -not (Test-Path -LiteralPath $SourceModelRoot -PathType Container)) {
    throw "battle model test prerequisites are missing"
}

$resolvedTestRoot = [System.IO.Path]::GetFullPath($TestRoot)
if (-not ($resolvedTestRoot + '\').StartsWith(
        $TemporaryBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "test root must stay below the operating system temporary directory"
}
[System.IO.Directory]::CreateDirectory($TestRoot) | Out-Null

try {
    $sourceDigestBefore = Get-TreeDigest $SourceModelRoot
    $first = Invoke-Validator $SourceModelRoot
    $second = Invoke-Validator $SourceModelRoot
    Assert-Pass "baseline-first" $first
    Assert-Pass "baseline-second" $second
    if ($first.Output -cne $second.Output) {
        throw "test failed: consecutive validation output differs"
    }
    if ((Get-TreeDigest $SourceModelRoot) -cne $sourceDigestBefore) {
        throw "test failed: validator modified the source corpus"
    }
    $Passed++

    $validatorText = Get-Content -Raw -LiteralPath $ValidatorPath
    $forbiddenValidatorCapability =
        '(?i)\b(?:Invoke-WebRequest|Invoke-RestMethod|HttpClient|TcpClient|UdpClient|' +
        'docker|Start-Process|Add-Type|New-Item|Copy-Item|Move-Item|Remove-Item|' +
        'Set-Content|Add-Content|Out-File|WriteAllBytes|WriteAllText)\b'
    if ($validatorText -match $forbiddenValidatorCapability -or
        $validatorText -match '(?m)\b[A-Za-z]:\\') {
        throw "test failed: validator contains a forbidden side-effect or absolute path"
    }
    $Passed++

    Invoke-MutationTest "orphan-case" {
        param($root)
        $source = Join-Path $root "cases\ai\deterministic-target-and-boss.json"
        $orphan = Join-Path $root "cases\ai\orphan-case.json"
        Copy-Item -LiteralPath $source -Destination $orphan
    } "case path inventory differs"

    Invoke-MutationTest "duplicate-id" {
        param($root)
        $path = Join-Path $root "manifest.json"
        $manifest = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $manifest.cases[1].case_id = $manifest.cases[0].case_id
        Write-CanonicalJson $path $manifest
    } "manifest cases"

    Invoke-MutationTest "unknown-field" {
        param($root)
        $relative = "cases/movement/kinematic-jump-and-collision.json"
        $path = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $case | Add-Member -NotePropertyName "unexpected_field" -NotePropertyValue $true
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $root $relative
    } "unknown field"

    Invoke-MutationTest "invalid-tick" {
        param($root)
        $relative = "cases/tick-input/tick-mapping-and-generation.json"
        $path = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $case.commands[0].target_tick = -1
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $root $relative
    } "minimum"

    Invoke-MutationTest "invalid-unit" {
        param($root)
        $relative = "cases/movement/kinematic-jump-and-collision.json"
        $path = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $case.units.distance = "meter"
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $root $relative
    } "schema const"

    Invoke-MutationTest "canonical-digest-drift" {
        param($root)
        $relative = "cases/combat/weapon-ability-and-sword.json"
        $path = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $case.expected.canonical_output = $case.expected.canonical_output + ":drift"
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $root $relative
    } "canonical digest differs"

    Invoke-MutationTest "missing-requirement-coverage" {
        param($root)
        $relative = "cases/movement/kinematic-jump-and-collision.json"
        $casePath = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $casePath | ConvertFrom-Json
        $case.requirements = @($case.requirements | Where-Object { $_ -ne "movement" })
        Write-CanonicalJson $casePath $case

        $manifestPath = Join-Path $root "manifest.json"
        $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
        foreach ($entry in @($manifest.cases)) {
            $entry.requirements = @($entry.requirements | Where-Object { $_ -ne "movement" })
            if ($entry.path -eq $relative) {
                $entry.file_sha256 = (
                    Get-FileHash -LiteralPath $casePath -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        }
        Write-CanonicalJson $manifestPath $manifest
    } "required requirement coverage is missing"

    Invoke-MutationTest "forbidden-wire-field" {
        param($root)
        $relative = "cases/history/bounded-lag-compensation.json"
        $path = Join-Path $root $relative.Replace('/', '\')
        $case = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json
        $case | Add-Member -NotePropertyName "wire" -NotePropertyValue "tcp"
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $root $relative
    } "contains forbidden data"

    if ((Get-TreeDigest $SourceModelRoot) -cne $sourceDigestBefore) {
        throw "test failed: regression suite modified the source corpus"
    }
    Write-Output ("[OK] Battle model regression tests passed: {0} checks." -f $Passed)
} finally {
    if (Test-Path -LiteralPath $TestRoot) {
        $cleanupRoot = [System.IO.Path]::GetFullPath($TestRoot)
        if (($cleanupRoot + '\').StartsWith(
                $TemporaryBase, [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $cleanupRoot -Recurse -Force
        }
    }
}
