# 该回归入口只在隔离临时副本制造坏数据，验证 battle network profile 保持只读和 fail closed。
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ValidatorPath = Join-Path $PSScriptRoot "battle-network-profile.ps1"
$SourceProfileRoot = Join-Path $RepositoryRoot `
    "shared\contracts\fixtures\battle\network-profile"
$SourceModelRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model"
$Utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$TemporaryBase = [System.IO.Path]::GetFullPath(
    [System.IO.Path]::GetTempPath()).TrimEnd('\') + '\'
$TestRoot = Join-Path ([System.IO.Path]::GetTempPath()) (
    "ihomeland-battle-network-profile-tests-{0}" -f
    [guid]::NewGuid().ToString("N"))
$Passed = 0

# Get-TreeDigest 生成稳定树摘要，用于证明 validator/simulator 没有修改 source corpus。
function Get-TreeDigest {
    param([string[]]$Roots)
    $records = @()
    foreach ($root in @($Roots | Sort-Object)) {
        $rootPath = [System.IO.Path]::GetFullPath($root).TrimEnd('\')
        $records += @(
            Get-ChildItem -LiteralPath $rootPath -Recurse -File |
            Sort-Object FullName |
            ForEach-Object {
                $relative = $_.FullName.Substring(
                    $rootPath.Length + 1).Replace('\', '/')
                "{0}|{1}:{2}" -f (
                    [System.IO.Path]::GetFileName($rootPath)),
                    $relative, (
                    Get-FileHash -LiteralPath $_.FullName `
                        -Algorithm SHA256).Hash.ToLowerInvariant()
            })
    }
    $bytes = $Utf8NoBom.GetBytes(($records -join "`n"))
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return -join ($sha.ComputeHash($bytes) | ForEach-Object {
                $_.ToString("x2")
            })
    } finally {
        $sha.Dispose()
    }
}

# Write-CanonicalJson 只写测试临时副本，并保持 UTF-8 无 BOM、LF 与单一末尾换行。
function Write-CanonicalJson {
    param([string]$Path, [object]$Value)
    $text = ($Value | ConvertTo-Json -Depth 100).Replace(
        "`r`n", "`n").Replace("`r", "`n").TrimEnd("`n") + "`n"
    [System.IO.File]::WriteAllText($Path, $text, $Utf8NoBom)
}

# Write-CanonicalText 只在测试副本规范化文本，用于制造不改变 JSON 语义的摘要漂移。
function Write-CanonicalText {
    param([string]$Path, [string]$Text)
    $canonical = $Text.Replace("`r`n", "`n").Replace(
        "`r", "`n").TrimEnd("`n") + "`n"
    [System.IO.File]::WriteAllText($Path, $canonical, $Utf8NoBom)
}

# Invoke-Tool 在独立 Windows PowerShell 进程捕获退出码与低敏输出。
function Invoke-Tool {
    param(
        [ValidateSet("validate", "simulate")]
        [string]$Action,
        [string]$ProfileRoot,
        [string]$ModelRoot
    )
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = @(
            & powershell.exe -NoProfile -ExecutionPolicy Bypass `
                -File $ValidatorPath -Action $Action `
                -ProfileRoot $ProfileRoot -ModelRoot $ModelRoot 2>&1 |
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

# New-IsolatedCorpusCopy 为每个 mutation 同时复制 profile 与 model，避免用例相互污染。
function New-IsolatedCorpusCopy {
    param([string]$Name)
    $caseRoot = Join-Path $TestRoot $Name
    $profileRoot = Join-Path $caseRoot "network-profile"
    $modelRoot = Join-Path $caseRoot "model"
    [System.IO.Directory]::CreateDirectory($caseRoot) | Out-Null
    Copy-Item -LiteralPath $SourceProfileRoot -Destination $profileRoot -Recurse
    Copy-Item -LiteralPath $SourceModelRoot -Destination $modelRoot -Recurse
    return [pscustomobject]@{
        CaseRoot = $caseRoot
        ProfileRoot = $profileRoot
        ModelRoot = $modelRoot
    }
}

# Update-ManifestFileDigest 让 source mutation 越过文件摘要门并抵达目标语义约束。
function Update-ManifestFileDigest {
    param([string]$ProfileRoot, [string]$RelativePath)
    $manifestPath = Join-Path $ProfileRoot "manifest.json"
    $manifest = Get-Content -Raw -Encoding UTF8 `
        -LiteralPath $manifestPath | ConvertFrom-Json
    $entry = @($manifest.files | Where-Object { $_.path -ceq $RelativePath })
    if ($entry.Count -ne 1) {
        throw "test setup failed: manifest file lookup is not unique"
    }
    $path = Join-Path $ProfileRoot $RelativePath.Replace('/', '\')
    $entry[0].file_sha256 = (
        Get-FileHash -LiteralPath $path `
            -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-CanonicalJson $manifestPath $manifest
}

# Update-ManifestCaseDigest 同步测试 case 的摘要和可选 projection。
function Update-ManifestCaseDigest {
    param(
        [string]$ProfileRoot,
        [string]$RelativePath,
        [object]$CaseDocument
    )
    $manifestPath = Join-Path $ProfileRoot "manifest.json"
    $manifest = Get-Content -Raw -Encoding UTF8 `
        -LiteralPath $manifestPath | ConvertFrom-Json
    $entry = @($manifest.cases | Where-Object { $_.path -ceq $RelativePath })
    if ($entry.Count -ne 1) {
        throw "test setup failed: manifest case lookup is not unique"
    }
    $path = Join-Path $ProfileRoot $RelativePath.Replace('/', '\')
    $entry[0].file_sha256 = (
        Get-FileHash -LiteralPath $path `
            -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($null -ne $CaseDocument) {
        $entry[0].requirements = @($CaseDocument.requirements)
        $entry[0].workloads = @($CaseDocument.workloads)
        $entry[0].phases = @($CaseDocument.phases)
    }
    Write-CanonicalJson $manifestPath $manifest
}

# Assert-Pass 验证成功路径并保留原始输出用于确定性比较。
function Assert-Pass {
    param([string]$Name, [object]$Result)
    if ($Result.ExitCode -ne 0) {
        throw "test failed: $Name expected pass; $($Result.Output)"
    }
    $script:Passed++
}

# Assert-Fail 验证 mutation 被稳定前缀与目标 pattern 拒绝。
function Assert-Fail {
    param(
        [string]$Name,
        [object]$Result,
        [string]$Pattern = "validation failed"
    )
    if ($Result.ExitCode -eq 0) {
        throw "test failed: $Name expected failure"
    }
    if ($Result.Output -notmatch $Pattern) {
        throw "test failed: $Name returned an unexpected diagnostic; $($Result.Output)"
    }
    $script:Passed++
}

# Invoke-MutationTest 为一个坏数据创建、验证并安全回收独立副本。
function Invoke-MutationTest {
    param(
        [string]$Name,
        [scriptblock]$Mutation,
        [string]$ExpectedPattern = "battle network profile validation failed"
    )
    $copy = New-IsolatedCorpusCopy $Name
    try {
        & $Mutation $copy
        Assert-Fail $Name (
            Invoke-Tool "validate" $copy.ProfileRoot $copy.ModelRoot
        ) $ExpectedPattern
    } finally {
        $caseRoot = [System.IO.Path]::GetFullPath($copy.CaseRoot)
        if (-not ($caseRoot + '\').StartsWith(
                [System.IO.Path]::GetFullPath($TestRoot).TrimEnd('\') + '\',
                [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "test cleanup escaped the isolated test root"
        }
        Remove-Item -LiteralPath $caseRoot -Recurse -Force
    }
}

if (-not (Test-Path -LiteralPath $ValidatorPath -PathType Leaf) -or
    -not (Test-Path -LiteralPath $SourceProfileRoot -PathType Container) -or
    -not (Test-Path -LiteralPath $SourceModelRoot -PathType Container)) {
    throw "battle network profile test prerequisites are missing"
}

$resolvedTestRoot = [System.IO.Path]::GetFullPath($TestRoot)
if (-not ($resolvedTestRoot + '\').StartsWith(
        $TemporaryBase, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "test root must stay below the operating system temporary directory"
}
[System.IO.Directory]::CreateDirectory($TestRoot) | Out-Null

try {
    $sourceDigestBefore = Get-TreeDigest @(
        $SourceModelRoot, $SourceProfileRoot)
    $baseline = Invoke-Tool "validate" $SourceProfileRoot $SourceModelRoot
    Assert-Pass "baseline-validation" $baseline
    $simulationFirst = Invoke-Tool "simulate" $SourceProfileRoot $SourceModelRoot
    $simulationSecond = Invoke-Tool "simulate" $SourceProfileRoot $SourceModelRoot
    Assert-Pass "simulation-first" $simulationFirst
    Assert-Pass "simulation-second" $simulationSecond
    if ($simulationFirst.Output -cne $simulationSecond.Output) {
        throw "test failed: consecutive simulation output differs"
    }
    if ((Get-TreeDigest @($SourceModelRoot, $SourceProfileRoot)) -cne
        $sourceDigestBefore) {
        throw "test failed: validator or simulator modified source corpus"
    }
    $Passed++

    $validatorText = Get-Content -Raw -Encoding UTF8 -LiteralPath $ValidatorPath
    $forbiddenValidatorCapability =
        '(?i)\b(?:Invoke-WebRequest|Invoke-RestMethod|HttpClient|TcpClient|' +
        'UdpClient|Socket|docker|Start-Process|New-Item|Copy-Item|Move-Item|' +
        'Remove-Item|Set-Content|Add-Content|Out-File|WriteAllBytes|' +
        'WriteAllText)\b'
    if ($validatorText -match $forbiddenValidatorCapability) {
        throw "test failed: validator contains a forbidden side effect"
    }
    $Passed++

    Invoke-MutationTest "model-assumptions-drift" {
        param($copy)
        $path = Join-Path $copy.ModelRoot "assumptions.json"
        $text = Get-Content -Raw -Encoding UTF8 -LiteralPath $path
        Write-CanonicalText $path ($text.Replace(
                "`n  `"format_version`"", "`n   `"format_version`""))
    } "model source digest differs"

    Invoke-MutationTest "model-case-digest-drift" {
        param($copy)
        $path = Join-Path $copy.ModelRoot `
            "cases\movement\kinematic-jump-and-collision.json"
        $text = Get-Content -Raw -Encoding UTF8 -LiteralPath $path
        Write-CanonicalText $path ($text.Replace(
                "`n  `"format_version`"", "`n   `"format_version`""))
    } "battle model validation failed"

    Invoke-MutationTest "orphan-profile-case" {
        param($copy)
        $source = Join-Path $copy.ProfileRoot `
            "cases\baseline\baseline-recovery.json"
        $target = Join-Path $copy.ProfileRoot `
            "cases\baseline\orphan-profile-case.json"
        Copy-Item -LiteralPath $source -Destination $target
    } "case path inventory differs"

    Invoke-MutationTest "duplicate-case-id" {
        param($copy)
        $path = Join-Path $copy.ProfileRoot "manifest.json"
        $manifest = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $manifest.cases[1].case_id = $manifest.cases[0].case_id
        Write-CanonicalJson $path $manifest
    } "case IDs are not unique"

    Invoke-MutationTest "unknown-profile-field" {
        param($copy)
        $path = Join-Path $copy.ProfileRoot "profile.json"
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile | Add-Member -NotePropertyName "unexpected_field" `
            -NotePropertyValue $true
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot "profile.json"
    } "unknown field"

    Invoke-MutationTest "invalid-parameter-unit" {
        param($copy)
        $path = Join-Path $copy.ProfileRoot "profile.json"
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile.parameters[0].unit = "widgets"
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot "profile.json"
    } "parameter metadata differs"

    Invoke-MutationTest "parameter-overflow" {
        param($copy)
        $path = Join-Path $copy.ProfileRoot "profile.json"
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile.parameters[0].value = [decimal]9007199254740992
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot "profile.json"
    } "integer range"

    Invoke-MutationTest "missing-requirement-coverage" {
        param($copy)
        $relative = "cases/baseline/baseline-recovery.json"
        $path = Join-Path $copy.ProfileRoot $relative.Replace('/', '\')
        $case = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $case.requirements = @($case.requirements | Where-Object {
                $_ -ne "snapshot"
            })
        Write-CanonicalJson $path $case
        Update-ManifestCaseDigest $copy.ProfileRoot $relative $case
    } "requirement coverage differs"

    Invoke-MutationTest "stale-qualification-report" {
        param($copy)
        $relative = "reports/qualification.json"
        $path = Join-Path $copy.ProfileRoot $relative.Replace('/', '\')
        $report = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $report.result_digest = "0" * 64
        Write-CanonicalJson $path $report
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "report differs"

    Invoke-MutationTest "snapshot-on-kcp" {
        param($copy)
        $relative = "message-inventory.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $inventory = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $snapshot = @($inventory.messages | Where-Object {
                $_.kind -eq "battle.snapshot.delta"
            })[0]
        $snapshot.lane = "kcp"
        Write-CanonicalJson $path $inventory
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "snapshot must not use KCP"

    Invoke-MutationTest "duplicate-message-lane" {
        param($copy)
        $relative = "message-inventory.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $inventory = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $duplicate = $inventory.messages[0] |
            ConvertTo-Json -Depth 20 | ConvertFrom-Json
        $duplicate.lane = "raw"
        $inventory.messages = @($inventory.messages) + @($duplicate)
        Write-CanonicalJson $path $inventory
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "message inventory kinds"

    Invoke-MutationTest "payload-over-mtu" {
        param($copy)
        $relative = "message-inventory.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $inventory = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $inventory.messages[0].max_logical_payload_bytes = 2000
        Write-CanonicalJson $path $inventory
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "payload exceeds lane budget"

    Invoke-MutationTest "implicit-fragmentation" {
        param($copy)
        $relative = "profile.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile.mtu_budget.fragmentation_policy = "allow-ip-fragments"
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "MTU budget arithmetic differs"

    Invoke-MutationTest "reliable-deliver-after-expiry" {
        param($copy)
        $relative = "message-inventory.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $inventory = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $reliable = @($inventory.messages | Where-Object {
                $_.lane -eq "kcp"
            })[0]
        $reliable.recovery_policy = "deliver-after-expiry"
        Write-CanonicalJson $path $inventory
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "reliable expiry policy differs"

    Invoke-MutationTest "kcp-parity-drift" {
        param($copy)
        $relative = "profile.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile.kcp_profile.adapter_parity_status = "qualified"
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "KCP profile budget differs"

    Invoke-MutationTest "unbounded-kcp-queue" {
        param($copy)
        $relative = "profile.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $profile.kcp_profile.queue_limit_messages = 0
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "KCP profile budget differs"

    Invoke-MutationTest "synthetic-cpu-qualified" {
        param($copy)
        $relative = "profile.json"
        $path = Join-Path $copy.ProfileRoot $relative
        $profile = Get-Content -Raw -Encoding UTF8 `
            -LiteralPath $path | ConvertFrom-Json
        $parameter = @($profile.parameters | Where-Object {
                $_.id -eq "cpu-per-tick-measurement"
            })[0]
        $parameter.value = 1
        $parameter.classification = "profile_qualified"
        Write-CanonicalJson $path $profile
        Update-ManifestFileDigest $copy.ProfileRoot $relative
    } "implementation evidence is misclassified"

    foreach ($forbidden in @(
            @{ Name = "raw-ticket"; Field = "ticket"; Value = "secret" },
            @{ Name = "real-player-data"; Field = "player_id"; Value = "player-1" },
            @{ Name = "numeric-message-id"; Field = "numeric_message_id"; Value = 9001 },
            @{ Name = "udp-port"; Field = "udp_port"; Value = 31000 },
            @{ Name = "generated-wire"; Field = "generated_wire"; Value = "bytes" },
            @{ Name = "absolute-path"; Field = "artifact_path"; Value = "C:\temp\evidence" }
        )) {
        $test = $forbidden
        Invoke-MutationTest ("forbidden-" + $test.Name) {
            param($copy)
            $relative = "profile.json"
            $path = Join-Path $copy.ProfileRoot $relative
            $profile = Get-Content -Raw -Encoding UTF8 `
                -LiteralPath $path | ConvertFrom-Json
            $profile | Add-Member -NotePropertyName $test.Field `
                -NotePropertyValue $test.Value
            Write-CanonicalJson $path $profile
            Update-ManifestFileDigest $copy.ProfileRoot $relative
        } "contains forbidden data"
    }

    if ((Get-TreeDigest @($SourceModelRoot, $SourceProfileRoot)) -cne
        $sourceDigestBefore) {
        throw "test failed: regression suite modified source corpus"
    }
    Write-Output (
        "[OK] Battle network profile regression tests passed: {0} checks." -f
        $Passed)
} finally {
    if (Test-Path -LiteralPath $TestRoot) {
        $cleanupRoot = [System.IO.Path]::GetFullPath($TestRoot)
        if (($cleanupRoot + '\').StartsWith(
                $TemporaryBase, [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $cleanupRoot -Recurse -Force
        }
    }
}
