#requires -Version 7.0

[CmdletBinding()]
# 该回归只在隔离临时副本制造坏数据，证明 gameplay config validator 确定且 fail closed。
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ValidatorPath = Join-Path $PSScriptRoot "gameplay-config.ps1"
$SourceCorpusRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\gameplay-config"
$SourceProductionRoot = Join-Path $RepositoryRoot "shared\contracts\gameplay\battle\packages\personal-world-combat-v1"
$Utf8NoBom = [Text.UTF8Encoding]::new($false)
$TemporaryBase = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
$TestRoot = Join-Path ([IO.Path]::GetTempPath()) (
    "ihomeland-gameplay-config-tests-{0}" -f [guid]::NewGuid().ToString("N"))
$Passed = 0

# Get-TreeDigest 生成 source 树摘要，证明 validator 与回归不会改写 corpus。
function Get-TreeDigest {
    param([Parameter(Mandatory = $true)][string]$Root)

    $rootLength = [IO.Path]::GetFullPath($Root).TrimEnd('\').Length + 1
    $records = @(
        Get-ChildItem -LiteralPath $Root -Recurse -File |
        Sort-Object FullName |
        ForEach-Object {
            $relative = $_.FullName.Substring($rootLength).Replace('\', '/')
            "{0}:{1}" -f $relative, (
                Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        }
    )
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        return -join ($hasher.ComputeHash($Utf8NoBom.GetBytes(($records -join "`n"))) |
            ForEach-Object { $_.ToString("x2") })
    }
    finally {
        $hasher.Dispose()
    }
}

# Write-CanonicalJson 仅写入当前临时副本，并保持 UTF-8/LF/单一末尾换行。
function Write-CanonicalJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )

    $text = ($Value | ConvertTo-Json -Depth 100).Replace("`r`n", "`n").TrimEnd("`n") + "`n"
    [IO.File]::WriteAllText($Path, $text, $Utf8NoBom)
}

# Invoke-Validator 在独立 pwsh 进程捕获稳定退出码与低敏输出。
function Invoke-Validator {
    param([Parameter(Mandatory = $true)][string]$Root)

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = @(
            & (Get-Command pwsh.exe -ErrorAction Stop).Source `
                -NoLogo -NoProfile -File $ValidatorPath validate -CorpusRoot $Root 2>&1 |
            ForEach-Object { [string]$_ })
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = $output -join "`n"
    }
}

# Invoke-ProductionValidator 同时验证只读治理 corpus 与隔离 production package。
function Invoke-ProductionValidator {
    param([Parameter(Mandatory = $true)][string]$Root)

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = @(
            & (Get-Command pwsh.exe -ErrorAction Stop).Source `
                -NoLogo -NoProfile -File $ValidatorPath validate `
                -CorpusRoot $SourceCorpusRoot -ProductionRoot $Root 2>&1 |
            ForEach-Object { [string]$_ })
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = $output -join "`n"
    }
}

# New-IsolatedCorpusCopy 为每个 mutation 建立独立 source 副本。
function New-IsolatedCorpusCopy {
    param([Parameter(Mandatory = $true)][string]$Name)

    $caseRoot = Join-Path $TestRoot $Name
    $corpusRoot = Join-Path $caseRoot "gameplay-config"
    [IO.Directory]::CreateDirectory($caseRoot) | Out-Null
    Copy-Item -LiteralPath $SourceCorpusRoot -Destination $corpusRoot -Recurse
    return $corpusRoot
}

# New-IsolatedProductionCopy 为每个 production mutation 建立独立 package 副本。
function New-IsolatedProductionCopy {
    param([Parameter(Mandatory = $true)][string]$Name)

    $caseRoot = Join-Path $TestRoot $Name
    $productionRoot = Join-Path $caseRoot "personal-world-combat-v1"
    [IO.Directory]::CreateDirectory($caseRoot) | Out-Null
    Copy-Item -LiteralPath $SourceProductionRoot -Destination $productionRoot -Recurse
    return $productionRoot
}

# Update-ManifestDigest 让语义 mutation 越过 raw digest 门并抵达目标约束。
function Update-ManifestDigest {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $manifestPath = Join-Path $Root "manifest.json"
    $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json -Depth 100
    $matches = @($manifest.files | Where-Object { [string]$_.path -ceq $RelativePath })
    if ($matches.Count -ne 1) {
        throw "test setup failed: manifest file lookup is not unique"
    }
    $documentPath = Join-Path $Root $RelativePath.Replace('/', '\')
    $matches[0].file_sha256 = (
        Get-FileHash -LiteralPath $documentPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-CanonicalJson $manifestPath $manifest
}

# Get-JsonDocument 读取临时副本中的指定 JSON。
function Get-JsonDocument {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )
    return Get-Content -Raw -LiteralPath (
        Join-Path $Root $RelativePath.Replace('/', '\')) | ConvertFrom-Json -Depth 100
}

# Set-JsonDocument 写回临时 document 并同步 manifest digest。
function Set-JsonDocument {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)]$Value
    )

    Write-CanonicalJson (Join-Path $Root $RelativePath.Replace('/', '\')) $Value
    Update-ManifestDigest $Root $RelativePath
}

# Assert-Pass 验证成功路径并累计稳定断言数。
function Assert-Pass {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result
    )
    if ($Result.ExitCode -ne 0) {
        throw "test failed: $Name expected pass; $($Result.Output)"
    }
    $script:Passed++
}

# Assert-Fail 验证 mutation 由预期稳定 reason 拒绝。
function Assert-Fail {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Pattern
    )
    if ($Result.ExitCode -eq 0) {
        throw "test failed: $Name expected failure"
    }
    if ($Result.Output -notmatch $Pattern) {
        throw "test failed: $Name returned unexpected diagnostic; $($Result.Output)"
    }
    if ($Result.Output -match '(?i)[A-Z]:\\|\\\\|(?:password|token|ticket|proof|traffic|cookie)[_-]?(?:secret|key)') {
        throw "test failed: $Name diagnostic leaked a path or sensitive shape"
    }
    $script:Passed++
}

# Invoke-MutationTest 在断言后验证并回收精确临时目录。
function Invoke-MutationTest {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Mutation,
        [Parameter(Mandatory = $true)][string]$ExpectedPattern
    )

    $root = New-IsolatedCorpusCopy $Name
    try {
        & $Mutation $root
        Assert-Fail $Name (Invoke-Validator $root) $ExpectedPattern
    }
    finally {
        $caseRoot = [IO.Directory]::GetParent($root).FullName
        $expectedPrefix = [IO.Path]::GetFullPath($TestRoot).TrimEnd('\') + '\'
        if (-not ([IO.Path]::GetFullPath($caseRoot) + '\').StartsWith(
                $expectedPrefix, [StringComparison]::OrdinalIgnoreCase)) {
            throw "test cleanup escaped isolated root"
        }
        Remove-Item -LiteralPath $caseRoot -Recurse -Force
    }
}

# Invoke-ProductionMutationTest 在隔离 production package 上验证稳定失败原因。
function Invoke-ProductionMutationTest {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Mutation,
        [Parameter(Mandatory = $true)][string]$ExpectedPattern
    )

    $root = New-IsolatedProductionCopy $Name
    try {
        & $Mutation $root
        Assert-Fail $Name (Invoke-ProductionValidator $root) $ExpectedPattern
    }
    finally {
        $caseRoot = [IO.Directory]::GetParent($root).FullName
        $expectedPrefix = [IO.Path]::GetFullPath($TestRoot).TrimEnd('\') + '\'
        if (-not ([IO.Path]::GetFullPath($caseRoot) + '\').StartsWith(
                $expectedPrefix, [StringComparison]::OrdinalIgnoreCase)) {
            throw "test cleanup escaped isolated production root"
        }
        Remove-Item -LiteralPath $caseRoot -Recurse -Force
    }
}

if (-not (Test-Path -LiteralPath $ValidatorPath -PathType Leaf) -or
    -not (Test-Path -LiteralPath $SourceCorpusRoot -PathType Container) -or
    -not (Test-Path -LiteralPath $SourceProductionRoot -PathType Container)) {
    throw "gameplay config test prerequisites are missing"
}
$resolvedTestRoot = [IO.Path]::GetFullPath($TestRoot)
if (-not ($resolvedTestRoot + '\').StartsWith(
        $TemporaryBase, [StringComparison]::OrdinalIgnoreCase)) {
    throw "test root must stay below the operating system temporary directory"
}
[IO.Directory]::CreateDirectory($TestRoot) | Out-Null

try {
    $sourceDigestBefore = Get-TreeDigest $SourceCorpusRoot
    $productionDigestBefore = Get-TreeDigest $SourceProductionRoot
    $first = Invoke-Validator $SourceCorpusRoot
    $second = Invoke-Validator $SourceCorpusRoot
    Assert-Pass "baseline-first" $first
    Assert-Pass "baseline-second" $second
    if ($first.Output -cne $second.Output) {
        throw "test failed: consecutive validation output differs"
    }
    if ((Get-TreeDigest $SourceCorpusRoot) -cne $sourceDigestBefore) {
        throw "test failed: validator modified source corpus"
    }
    $Passed++

    $productionFirst = Invoke-ProductionValidator $SourceProductionRoot
    $productionSecond = Invoke-ProductionValidator $SourceProductionRoot
    Assert-Pass "production-baseline-first" $productionFirst
    Assert-Pass "production-baseline-second" $productionSecond
    if ($productionFirst.Output -cne $productionSecond.Output) {
        throw "test failed: consecutive production validation output differs"
    }
    if ((Get-TreeDigest $SourceProductionRoot) -cne $productionDigestBefore) {
        throw "test failed: validator modified production package"
    }
    $Passed++

    $validatorText = Get-Content -Raw -LiteralPath $ValidatorPath
    $forbiddenCapability = '(?i)\b(?:Invoke-WebRequest|Invoke-RestMethod|HttpClient|TcpClient|' +
        'UdpClient|Start-Process|Add-Type|New-Item|Copy-Item|Move-Item|' +
        'Remove-Item|Set-Content|Add-Content|Out-File|WriteAllBytes|WriteAllText)\b'
    if ($validatorText -match $forbiddenCapability -or
        $validatorText -match '(?m)\b[A-Za-z]:\\' -or
        $validatorText -match '(?i)(?:simulation[\\/]src|server[\\/]internal|client[\\/]Assets)') {
        throw "test failed: validator contains runtime import, side effect or absolute path"
    }
    $Passed++

    Invoke-MutationTest "orphan-json" {
        param($root)
        Copy-Item -LiteralPath (Join-Path $root "registries\required-coverage.json") `
            -Destination (Join-Path $root "registries\orphan.json")
    } '\[digest\]'

    Invoke-MutationTest "digest-drift" {
        param($root)
        $path = Join-Path $root "packages\governance-reference-v1\authority.json"
        [IO.File]::AppendAllText($path, " ", $Utf8NoBom)
    } '\[(?:schema|digest)\]'

    Invoke-MutationTest "unknown-field" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document | Add-Member -NotePropertyName "unity_guid" -NotePropertyValue "forbidden"
        Set-JsonDocument $root $relative $document
    } '\[schema\]'

    Invoke-MutationTest "missing-field" {
        param($root)
        $relative = "packages/governance-reference-v1/package.json"
        $document = Get-JsonDocument $root $relative
        $document.PSObject.Properties.Remove("bindings_path")
        Set-JsonDocument $root $relative $document
    } '\[schema\]'

    Invoke-MutationTest "format-drift" {
        param($root)
        $relative = "packages/governance-reference-v1/package.json"
        $document = Get-JsonDocument $root $relative
        $document.format_version = "gameplay-config-format-v2"
        Set-JsonDocument $root $relative $document
    } '\[schema\]'

    Invoke-MutationTest "model-drift" {
        param($root)
        $relative = "packages/governance-reference-v1/package.json"
        $document = Get-JsonDocument $root $relative
        $document.model_binding.manifest_sha256 = "0" * 64
        Set-JsonDocument $root $relative $document
    } '\[compatibility\]'

    Invoke-MutationTest "manifest-path-escape" {
        param($root)
        $manifestPath = Join-Path $root "manifest.json"
        $manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json -Depth 100
        $manifest.files[0].path = "../authority.json"
        Write-CanonicalJson $manifestPath $manifest
    } '\[schema\]'

    Invoke-MutationTest "cr-line-ending" {
        param($root)
        $path = Join-Path $root "registries\semantic-types.json"
        $text = Get-Content -Raw -LiteralPath $path
        [IO.File]::WriteAllText($path, $text.Replace("`n", "`r`n"), $Utf8NoBom)
        Update-ManifestDigest $root "registries/semantic-types.json"
    } '\[schema\]'

    Invoke-MutationTest "duplicate-semantic-id" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[1].id = $document.objects[0].id
        Set-JsonDocument $root $relative $document
    } '\[reference\]'

    Invoke-MutationTest "retired-semantic-id" {
        param($root)
        $relative = "registries/semantic-types.json"
        $document = Get-JsonDocument $root $relative
        $document.retired_ids = @("fixture/actor/player")
        Set-JsonDocument $root $relative $document
    } '\[reference\]'

    Invoke-MutationTest "cross-kind-reference" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[0].references[0].target_kind = "effect"
        Set-JsonDocument $root $relative $document
    } '\[reference\]'

    Invoke-MutationTest "dangling-reference" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[0].references[0].target_id = "fixture/weapon/missing"
        Set-JsonDocument $root $relative $document
    } '\[reference\]'

    Invoke-MutationTest "illegal-reference-direction" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[0].references[0].field = "effect-ref"
        $document.objects[0].references[0].target_id = "fixture/effect/sword-damage"
        $document.objects[0].references[0].target_kind = "effect"
        Set-JsonDocument $root $relative $document
    } '\[reference\]'

    Invoke-MutationTest "self-cycle" {
        param($root)
        $semanticRelative = "registries/semantic-types.json"
        $semantic = Get-JsonDocument $root $semanticRelative
        $semantic.allowed_references += [pscustomobject]@{
            from_kind = "cue"
            field = "cue-ref"
            to_kinds = @("cue")
        }
        Set-JsonDocument $root $semanticRelative $semantic
        $authorityRelative = "packages/governance-reference-v1/authority.json"
        $authority = Get-JsonDocument $root $authorityRelative
        $cue = @($authority.objects | Where-Object { $_.id -ceq "fixture/cue/player-state" })[0]
        $cue.references = @([pscustomobject]@{
            field = "cue-ref"
            target_id = "fixture/cue/player-state"
            target_kind = "cue"
        })
        Set-JsonDocument $root $authorityRelative $authority
    } '\[reference\]'

    Invoke-MutationTest "multi-node-cycle" {
        param($root)
        $semanticRelative = "registries/semantic-types.json"
        $semantic = Get-JsonDocument $root $semanticRelative
        $semantic.allowed_references += [pscustomobject]@{
            from_kind = "cue"
            field = "cue-ref"
            to_kinds = @("cue")
        }
        Set-JsonDocument $root $semanticRelative $semantic
        $authorityRelative = "packages/governance-reference-v1/authority.json"
        $authority = Get-JsonDocument $root $authorityRelative
        $cueA = @($authority.objects | Where-Object { $_.id -ceq "fixture/cue/player-state" })[0]
        $cueB = @($authority.objects | Where-Object { $_.id -ceq "fixture/cue/boss-state" })[0]
        $cueA.references = @([pscustomobject]@{ field = "cue-ref"; target_id = $cueB.id; target_kind = "cue" })
        $cueB.references = @([pscustomobject]@{ field = "cue-ref"; target_id = $cueA.id; target_kind = "cue" })
        Set-JsonDocument $root $authorityRelative $authority
    } '\[reference\]'

    Invoke-MutationTest "invalid-unit" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[0].numeric_values[0].unit = "health-points"
        Set-JsonDocument $root $relative $document
    } '\[range\]'

    Invoke-MutationTest "out-of-range" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $document.objects[0].numeric_values[0].value = "0"
        Set-JsonDocument $root $relative $document
    } '\[range\]'

    Invoke-MutationTest "effect-overflow" {
        param($root)
        $registryRelative = "registries/numeric-fields.json"
        $registry = Get-JsonDocument $root $registryRelative
        $rule = @($registry.entries | Where-Object { $_.kind -ceq "effect" -and $_.field -ceq "magnitude" })[0]
        $rule.maximum = [string][long]::MaxValue
        Set-JsonDocument $root $registryRelative $registry
        $authorityRelative = "packages/governance-reference-v1/authority.json"
        $authority = Get-JsonDocument $root $authorityRelative
        $effect = @($authority.objects | Where-Object { $_.id -ceq "fixture/effect/sword-damage" })[0]
        @($effect.numeric_values | Where-Object { $_.field -ceq "magnitude" })[0].value = [string][long]::MaxValue
        @($effect.numeric_values | Where-Object { $_.field -ceq "max-stacks" })[0].value = "2"
        Set-JsonDocument $root $authorityRelative $authority
    } '\[range\]'

    Invoke-MutationTest "boss-threshold-order" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $phase = @($document.objects | Where-Object { $_.id -ceq "fixture/boss-phase/desperate" })[0]
        @($phase.numeric_values | Where-Object { $_.field -ceq "health-threshold" })[0].value = "600000"
        Set-JsonDocument $root $relative $document
    } '\[range\]'

    Invoke-MutationTest "encounter-capacity" {
        param($root)
        $registryRelative = "registries/numeric-fields.json"
        $registry = Get-JsonDocument $root $registryRelative
        @($registry.entries | Where-Object { $_.kind -ceq "encounter" -and $_.field -ceq "visitor-count" })[0].maximum = "8"
        Set-JsonDocument $root $registryRelative $registry
        $authorityRelative = "packages/governance-reference-v1/authority.json"
        $authority = Get-JsonDocument $root $authorityRelative
        $encounter = @($authority.objects | Where-Object { $_.kind -ceq "encounter" })[0]
        @($encounter.numeric_values | Where-Object { $_.field -ceq "visitor-count" })[0].value = "8"
        Set-JsonDocument $root $authorityRelative $authority
    } '\[range\]'

    Invoke-MutationTest "hidden-default" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        $ability = @($document.objects | Where-Object { $_.id -ceq "fixture/ability/sword-primary" })[0]
        $ability.numeric_values = @($ability.numeric_values | Where-Object { $_.field -cne "cooldown" })
        Set-JsonDocument $root $relative $document
    } '\[range\]'

    Invoke-MutationTest "presentation-authority-field" {
        param($root)
        $relative = "packages/governance-reference-v1/presentation.json"
        $document = Get-JsonDocument $root $relative
        $document.mappings[0] | Add-Member -NotePropertyName "damage" -NotePropertyValue "1000"
        Set-JsonDocument $root $relative $document
    } '\[schema\]'

    Invoke-MutationTest "required-role-missing" {
        param($root)
        $relative = "packages/governance-reference-v1/authority.json"
        $document = Get-JsonDocument $root $relative
        @($document.objects | Where-Object { $_.role -ceq "boss" })[0].role = "none"
        Set-JsonDocument $root $relative $document
    } '\[coverage\]'

    Invoke-MutationTest "presentation-mapping-missing" {
        param($root)
        $relative = "packages/governance-reference-v1/presentation.json"
        $document = Get-JsonDocument $root $relative
        $document.mappings = @($document.mappings | Where-Object { $_.semantic_id -cne "fixture/actor/boss" })
        Set-JsonDocument $root $relative $document
    } '\[presentation-parity\]'

    Invoke-MutationTest "production-uses-fixture" {
        param($root)
        foreach ($relative in @(
            "packages/governance-reference-v1/package.json",
            "packages/governance-reference-v1/authority.json",
            "packages/governance-reference-v1/presentation.json",
            "packages/governance-reference-v1/bindings.json"
        )) {
            $document = Get-JsonDocument $root $relative
            $document.qualification_state = "production"
            Set-JsonDocument $root $relative $document
        }
        $relative = "packages/governance-reference-v1/wire-mapping.json"
        $document = Get-JsonDocument $root $relative
        $document.qualification_state = "production"
        Set-JsonDocument $root $relative $document
    } '\[(?:schema|compatibility)\]'

    Invoke-ProductionMutationTest "production-duplicate-numeric-id" {
        param($root)
        $path = Join-Path $root "wire-mapping.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $document.mappings[1].numeric_id = $document.mappings[0].numeric_id
        Write-CanonicalJson $path $document
    } '\[wire-mapping\]'

    Invoke-ProductionMutationTest "production-missing-projectile-mapping" {
        param($root)
        $path = Join-Path $root "wire-mapping.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $document.mappings = @($document.mappings | Where-Object { $_.kind -cne "projectile" })
        Write-CanonicalJson $path $document
    } '\[wire-mapping\]'

    Invoke-ProductionMutationTest "production-cross-kind-mapping" {
        param($root)
        $path = Join-Path $root "wire-mapping.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $mapping = @($document.mappings | Where-Object { $_.kind -ceq "weapon" })[0]
        $mapping.kind = "ability"
        Write-CanonicalJson $path $document
    } '\[(?:schema|wire-mapping)\]'

    Invoke-ProductionMutationTest "production-fixture-namespace" {
        param($root)
        $path = Join-Path $root "authority.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $document.objects[0].id = "fixture/actor/player"
        Write-CanonicalJson $path $document
    } '\[reference\]'

    Invoke-ProductionMutationTest "production-presentation-authority-field" {
        param($root)
        $path = Join-Path $root "presentation.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $document.mappings[0] | Add-Member -NotePropertyName "damage" -NotePropertyValue "1000"
        Write-CanonicalJson $path $document
    } '\[schema\]'

    Invoke-ProductionMutationTest "production-governance-classification" {
        param($root)
        foreach ($name in @("package.json", "authority.json", "presentation.json", "bindings.json", "wire-mapping.json")) {
            $path = Join-Path $root $name
            $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
            $document.qualification_state = "governance-only"
            Write-CanonicalJson $path $document
        }
    } '\[compatibility\]'

    Invoke-ProductionMutationTest "production-sensitive-logical-key" {
        param($root)
        $path = Join-Path $root "presentation.json"
        $document = Get-Content -Raw -LiteralPath $path | ConvertFrom-Json -Depth 100
        $document.resources[0].logical_key = "token-secret"
        Write-CanonicalJson $path $document
    } '\[security\]'

    Invoke-MutationTest "sensitive-logical-key" {
        param($root)
        $relative = "packages/governance-reference-v1/presentation.json"
        $document = Get-JsonDocument $root $relative
        $document.resources[0].logical_key = "token-secret"
        Set-JsonDocument $root $relative $document
    } '\[security\]'

    Invoke-MutationTest "binding-identities-merged" {
        param($root)
        $relative = "packages/governance-reference-v1/bindings.json"
        $document = Get-JsonDocument $root $relative
        $document.physics_identity = $document.navigation_identity
        Set-JsonDocument $root $relative $document
    } '\[binding\]'

    if ((Get-TreeDigest $SourceCorpusRoot) -cne $sourceDigestBefore) {
        throw "test failed: regression suite modified source corpus"
    }
    if ((Get-TreeDigest $SourceProductionRoot) -cne $productionDigestBefore) {
        throw "test failed: regression suite modified production package"
    }
}
finally {
    if (Test-Path -LiteralPath $TestRoot) {
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
}

Write-Output "GAMEPLAY_CONFIG_TESTS_PASS count=$Passed"
