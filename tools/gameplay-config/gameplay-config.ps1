#requires -Version 7.0

[CmdletBinding()]
# 该入口只验证 gameplay config source contract；它不加载或执行任何 gameplay runtime。
param(
    # Action 当前只允许确定只读的 validate。
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("validate")]
    [string]$Action,

    # CorpusRoot 允许隔离失败回归传入临时副本；默认值始终指向仓库 source corpus。
    [string]$CorpusRoot = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$DefaultCorpusRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\gameplay-config"
$ResolvedCorpusRoot = if ($CorpusRoot) {
    (Resolve-Path -LiteralPath $CorpusRoot).Path
} else {
    (Resolve-Path -LiteralPath $DefaultCorpusRoot).Path
}
$Utf8Strict = [Text.UTF8Encoding]::new($false, $true)
$Utf8NoBom = [Text.UTF8Encoding]::new($false)
$Int64Minimum = [System.Numerics.BigInteger]::Parse([string][long]::MinValue)
$Int64Maximum = [System.Numerics.BigInteger]::Parse([string][long]::MaxValue)

# Fail-GameplayConfig 统一产生稳定 reason code，避免异常回显本机路径或完整配置。
function Fail-GameplayConfig {
    param(
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][string]$Message
    )
    throw "gameplay config validation failed [$Code]: $Message"
}

# Get-CorpusRelativePath 把所有诊断限制为 corpus-relative path，并拒绝目录逃逸。
function Get-CorpusRelativePath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $root = [IO.Path]::GetFullPath($ResolvedCorpusRoot).TrimEnd('\') + '\'
    $full = [IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
        Fail-GameplayConfig "path" "path escaped corpus root"
    }
    return $full.Substring($root.Length).Replace('\', '/')
}

# Resolve-CorpusPath 只接受 manifest 登记的规范相对路径。
function Resolve-CorpusPath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)

    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath.Contains('\') -or
        $RelativePath.Split('/') -contains '..') {
        Fail-GameplayConfig "path" "manifest path is not canonical"
    }
    $full = [IO.Path]::GetFullPath((Join-Path $ResolvedCorpusRoot $RelativePath))
    $null = Get-CorpusRelativePath $full
    return $full
}

# Read-CanonicalText 验证 UTF-8 无 BOM、LF 与单一末尾换行后返回文本。
function Read-CanonicalText {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail-GameplayConfig "schema" "required document is missing"
    }
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -ge 3 -and
        $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $Path) contains UTF-8 BOM"
    }
    if ($bytes -contains 13) {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $Path) contains CR line endings"
    }
    try {
        $text = $Utf8Strict.GetString($bytes)
    }
    catch {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $Path) is not strict UTF-8"
    }
    if (-not $text.EndsWith("`n") -or $text.EndsWith("`n`n")) {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $Path) must end with one LF"
    }
    return $text
}

# Read-JsonDocument 读取 canonical JSON，并收敛 parser 的不稳定错误文本。
function Read-JsonDocument {
    param([Parameter(Mandatory = $true)][string]$Path)

    $text = Read-CanonicalText $Path
    try {
        return $text | ConvertFrom-Json -Depth 100
    }
    catch {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $Path) is not valid JSON"
    }
}

# Get-Sha256Bytes 返回原始 bytes 的 lowercase SHA-256。
function Get-Sha256Bytes {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)

    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        return -join ($hasher.ComputeHash($Bytes) | ForEach-Object { $_.ToString("x2") })
    }
    finally {
        $hasher.Dispose()
    }
}

# Get-Sha256File 返回文件原始 bytes 的 lowercase SHA-256。
function Get-Sha256File {
    param([Parameter(Mandatory = $true)][string]$Path)
    return Get-Sha256Bytes ([IO.File]::ReadAllBytes($Path))
}

# Assert-JsonSchema 使用本地 closed schema 验证 document，不允许远程 schema 或 parser 诊断泄漏。
function Assert-JsonSchema {
    param(
        [Parameter(Mandatory = $true)][string]$DocumentPath,
        [Parameter(Mandatory = $true)][string]$SchemaPath
    )

    $json = Read-CanonicalText $DocumentPath
    $null = Read-CanonicalText $SchemaPath
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "SilentlyContinue"
    try {
        $valid = Test-Json -Json $json -SchemaFile $SchemaPath 2>$null
    }
    catch {
        $valid = $false
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    if (-not $valid) {
        Fail-GameplayConfig "schema" "$(Get-CorpusRelativePath $DocumentPath) violates closed schema"
    }
}

# Assert-SortedUnique 要求 contract array 使用 ordinal 稳定顺序且无重复值。
function Assert-SortedUnique {
    param(
        [Parameter(Mandatory = $true)][string[]]$Values,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $sorted = @($Values | Sort-Object -CaseSensitive -Unique)
    if ($sorted.Count -ne $Values.Count) {
        Fail-GameplayConfig "schema" "$Context contains duplicate values"
    }
    for ($index = 0; $index -lt $Values.Count; $index++) {
        if ($Values[$index] -cne $sorted[$index]) {
            Fail-GameplayConfig "schema" "$Context is not ordinal sorted"
        }
    }
}

# Assert-StringSetEqual 比较两个无序字符串集合并拒绝遗漏或额外项。
function Assert-StringSetEqual {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Actual,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Expected,
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $actualSorted = @($Actual | Sort-Object -CaseSensitive -Unique)
    $expectedSorted = @($Expected | Sort-Object -CaseSensitive -Unique)
    if ($actualSorted.Count -ne $Actual.Count -or
        $expectedSorted.Count -ne $Expected.Count -or
        $actualSorted.Count -ne $expectedSorted.Count) {
        Fail-GameplayConfig $Code "$Context differs"
    }
    for ($index = 0; $index -lt $actualSorted.Count; $index++) {
        if ($actualSorted[$index] -cne $expectedSorted[$index]) {
            Fail-GameplayConfig $Code "$Context differs"
        }
    }
}

# ConvertTo-CheckedInteger 解析规范十进制整数并限制为 signed 64-bit。
function ConvertTo-CheckedInteger {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $result = [System.Numerics.BigInteger]::Zero
    if (-not [System.Numerics.BigInteger]::TryParse(
            $Value,
            [Globalization.NumberStyles]::AllowLeadingSign,
            [Globalization.CultureInfo]::InvariantCulture,
            [ref]$result)) {
        Fail-GameplayConfig "range" "$Context is not a canonical integer"
    }
    if ($result -lt $Int64Minimum -or $result -gt $Int64Maximum) {
        Fail-GameplayConfig "range" "$Context exceeds checked-int64"
    }
    return $result
}

# Get-NumericValue 返回对象内唯一 numeric field 的 checked value。
function Get-NumericValue {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string]$Field
    )

    $matches = @($Object.numeric_values | Where-Object { [string]$_.field -ceq $Field })
    if ($matches.Count -ne 1) {
        Fail-GameplayConfig "range" "numeric field $Field is not unique"
    }
    return ConvertTo-CheckedInteger ([string]$matches[0].value) "$($Object.id).$Field"
}

# Test-ReferenceGraphAcyclic 使用稳定 Kahn traversal 拒绝任何引用 cycle。
function Test-ReferenceGraphAcyclic {
    param(
        [Parameter(Mandatory = $true)][hashtable]$ObjectsById
    )

    $indegree = @{}
    $edges = @{}
    foreach ($id in @($ObjectsById.Keys)) {
        $indegree[$id] = 0
        $edges[$id] = [Collections.Generic.List[string]]::new()
    }
    foreach ($id in @($ObjectsById.Keys)) {
        foreach ($reference in @($ObjectsById[$id].references)) {
            $target = [string]$reference.target_id
            $edges[$id].Add($target)
            $indegree[$target] = [int]$indegree[$target] + 1
        }
    }
    $ready = [Collections.Generic.SortedSet[string]]::new([StringComparer]::Ordinal)
    foreach ($id in @($indegree.Keys)) {
        if ([int]$indegree[$id] -eq 0) {
            [void]$ready.Add($id)
        }
    }
    $visited = 0
    while ($ready.Count -gt 0) {
        $current = $ready.Min
        [void]$ready.Remove($current)
        $visited++
        foreach ($target in @($edges[$current])) {
            $indegree[$target] = [int]$indegree[$target] - 1
            if ([int]$indegree[$target] -eq 0) {
                [void]$ready.Add($target)
            }
        }
    }
    if ($visited -ne $ObjectsById.Count) {
        Fail-GameplayConfig "reference" "authority reference graph contains a cycle"
    }
}

# Get-PackageIdentity 按 manifest 约定重算 package 的 ConfigIdentity。
function Get-PackageIdentity {
    param(
        [Parameter(Mandatory = $true)]$Manifest,
        [Parameter(Mandatory = $true)]$PackageEntry,
        [Parameter(Mandatory = $true)][hashtable]$FileEntries
    )

    $lines = [Text.StringBuilder]::new()
    [void]$lines.Append([string]$Manifest.identity_domain).Append("`n")
    $paths = @($PackageEntry.identity_files | ForEach-Object { [string]$_ })
    Assert-SortedUnique $paths "package identity_files"
    foreach ($path in $paths) {
        if (-not $FileEntries.ContainsKey($path)) {
            Fail-GameplayConfig "digest" "package identity references an unregistered file"
        }
        $entry = $FileEntries[$path]
        [void]$lines.Append($path).Append([char]0)
        [void]$lines.Append([string]$entry.document_kind).Append([char]0)
        [void]$lines.Append([string]$entry.file_sha256).Append("`n")
    }
    return Get-Sha256Bytes ($Utf8NoBom.GetBytes($lines.ToString()))
}

# Assert-NoSensitiveData 拒绝凭据、个人身份、本机路径与 Unity runtime 资产路径形态。
function Assert-NoSensitiveData {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $forbidden = '(?i)(?:[A-Z]:\\|\\\\|player[_-]?id|account[_-]?id|' +
        '(?:password|token|ticket|proof|traffic|cookie)[_-]?(?:secret|key)|' +
        'Library/PackageCache)'
    if ($Text -match $forbidden) {
        Fail-GameplayConfig "security" "$Context contains forbidden or sensitive data"
    }
}

# Invoke-GameplayConfigValidation 执行完整纯数据治理门并返回稳定 package identity。
function Invoke-GameplayConfigValidation {
    $manifestPath = Join-Path $ResolvedCorpusRoot "manifest.json"
    $manifestSchemaPath = Join-Path $ResolvedCorpusRoot "schema\manifest.schema.json"
    Assert-JsonSchema $manifestPath $manifestSchemaPath
    $manifest = Read-JsonDocument $manifestPath

    $manifestPaths = @($manifest.files | ForEach-Object { [string]$_.path })
    Assert-SortedUnique $manifestPaths "manifest files"
    $actualPaths = @(
        Get-ChildItem -LiteralPath $ResolvedCorpusRoot -Recurse -File -Filter *.json |
        ForEach-Object { Get-CorpusRelativePath $_.FullName } |
        Where-Object { $_ -cne "manifest.json" } |
        Sort-Object -CaseSensitive
    )
    Assert-StringSetEqual $manifestPaths $actualPaths "digest" "manifest file inventory"

    $fileEntries = @{}
    foreach ($entry in @($manifest.files)) {
        $relative = [string]$entry.path
        $path = Resolve-CorpusPath $relative
        $actualDigest = Get-Sha256File $path
        if ($actualDigest -cne [string]$entry.file_sha256) {
            Fail-GameplayConfig "digest" "$relative digest differs"
        }
        $fileEntries[$relative] = $entry
        $document = Read-JsonDocument $path
        if ($relative.StartsWith("schema/", [StringComparison]::Ordinal)) {
            if (-not ($document.PSObject.Properties.Name -ccontains '$schema') -or
                [string]$document.'$schema' -cne "https://json-schema.org/draft/2020-12/schema") {
                Fail-GameplayConfig "schema" "$relative is not a supported local schema"
            }
        }
        else {
            if ([string]$document.document_kind -cne [string]$entry.document_kind) {
                Fail-GameplayConfig "schema" "$relative document kind differs"
            }
            Assert-NoSensitiveData (Read-CanonicalText $path) $relative
        }
    }

    $schemaByKind = @{
        "gameplay-package" = "schema/package.schema.json"
        "authority-catalog" = "schema/authority.schema.json"
        "presentation-catalog" = "schema/presentation.schema.json"
        "external-bindings" = "schema/bindings.schema.json"
        "semantic-registry" = "schema/typed-reference.schema.json"
        "numeric-registry" = "schema/numeric-range.schema.json"
        "coverage-registry" = "schema/coverage.schema.json"
    }
    foreach ($entry in @($manifest.files)) {
        $kind = [string]$entry.document_kind
        if ($schemaByKind.ContainsKey($kind)) {
            Assert-JsonSchema `
                (Resolve-CorpusPath ([string]$entry.path)) `
                (Resolve-CorpusPath ([string]$schemaByKind[$kind]))
        }
    }

    $semantic = Read-JsonDocument (Join-Path $ResolvedCorpusRoot "registries\semantic-types.json")
    $numeric = Read-JsonDocument (Join-Path $ResolvedCorpusRoot "registries\numeric-fields.json")
    $coverage = Read-JsonDocument (Join-Path $ResolvedCorpusRoot "registries\required-coverage.json")

    $namespaceKinds = @($semantic.namespaces | ForEach-Object { [string]$_.kind })
    if (@($namespaceKinds | Sort-Object -Unique).Count -ne $namespaceKinds.Count) {
        Fail-GameplayConfig "reference" "semantic namespace kind is duplicated"
    }
    foreach ($namespace in @($semantic.namespaces)) {
        if ([string]$namespace.prefix -cne (([string]$namespace.kind) + "/")) {
            Fail-GameplayConfig "reference" "semantic namespace prefix differs from kind"
        }
    }

    $allowedReferences = @{}
    foreach ($rule in @($semantic.allowed_references)) {
        $key = "{0}|{1}" -f [string]$rule.from_kind, [string]$rule.field
        if ($allowedReferences.ContainsKey($key)) {
            Fail-GameplayConfig "reference" "allowed reference rule is duplicated"
        }
        $allowedReferences[$key] = @($rule.to_kinds | ForEach-Object { [string]$_ })
    }

    $numericRules = @{}
    foreach ($rule in @($numeric.entries)) {
        $key = "{0}|{1}" -f [string]$rule.kind, [string]$rule.field
        if ($numericRules.ContainsKey($key)) {
            Fail-GameplayConfig "range" "numeric rule is duplicated"
        }
        $minimum = ConvertTo-CheckedInteger ([string]$rule.minimum) "$key minimum"
        $maximum = ConvertTo-CheckedInteger ([string]$rule.maximum) "$key maximum"
        if ($minimum -gt $maximum) {
            Fail-GameplayConfig "range" "$key range is reversed"
        }
        $numericRules[$key] = $rule
    }

    $coverageRoles = @($coverage.required_roles | ForEach-Object { [string]$_.role })
    if (@($coverageRoles | Sort-Object -Unique).Count -ne $coverageRoles.Count) {
        Fail-GameplayConfig "coverage" "required role is duplicated"
    }

    if (@($manifest.packages).Count -ne 1) {
        Fail-GameplayConfig "schema" "v1 corpus must register exactly one reference package"
    }
    $packageEntry = @($manifest.packages)[0]
    $packagePath = Resolve-CorpusPath ([string]$packageEntry.package_path)
    $package = Read-JsonDocument $packagePath
    if ([string]$package.package_id -cne [string]$packageEntry.package_id) {
        Fail-GameplayConfig "compatibility" "package identity differs from manifest"
    }
    Assert-StringSetEqual `
        @($package.required_roles | ForEach-Object { [string]$_ }) `
        $coverageRoles `
        "coverage" `
        "package required roles"

    $modelDigest = Get-Sha256File (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model\manifest.json")
    $profileDigest = Get-Sha256File (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\manifest.json")
    if ([string]$package.model_binding.version -cne "battle-model-v1" -or
        [string]$package.model_binding.manifest_sha256 -cne $modelDigest -or
        [string]$package.profile_binding.version -cne "battle-network-profile-v2" -or
        [string]$package.profile_binding.manifest_sha256 -cne $profileDigest) {
        Fail-GameplayConfig "compatibility" "model or profile binding differs"
    }

    $packageRoot = [IO.Directory]::GetParent($packagePath).FullName
    $authorityPath = Join-Path $packageRoot ([string]$package.authority_path)
    $presentationPath = Join-Path $packageRoot ([string]$package.presentation_path)
    $bindingsPath = Join-Path $packageRoot ([string]$package.bindings_path)
    $null = Get-CorpusRelativePath $authorityPath
    $null = Get-CorpusRelativePath $presentationPath
    $null = Get-CorpusRelativePath $bindingsPath
    $authority = Read-JsonDocument $authorityPath
    $presentation = Read-JsonDocument $presentationPath
    $bindings = Read-JsonDocument $bindingsPath
    foreach ($document in @($authority, $presentation, $bindings)) {
        if ([string]$document.package_id -cne [string]$package.package_id -or
            [string]$document.qualification_state -cne [string]$package.qualification_state) {
            Fail-GameplayConfig "compatibility" "package document identity differs"
        }
    }

    $objectsById = @{}
    foreach ($object in @($authority.objects)) {
        $id = [string]$object.id
        $kind = [string]$object.kind
        if ($objectsById.ContainsKey($id)) {
            Fail-GameplayConfig "reference" "authority semantic ID is duplicated"
        }
        $segments = $id.Split('/')
        if ($segments.Count -ne 3 -or $segments[1] -cne $kind -or
            $namespaceKinds -cnotcontains $kind) {
            Fail-GameplayConfig "reference" "authority semantic ID kind differs"
        }
        if (@($semantic.retired_ids) -ccontains $id) {
            Fail-GameplayConfig "reference" "authority semantic ID is retired"
        }
        $objectsById[$id] = $object
    }

    foreach ($object in @($authority.objects)) {
        $referenceKeys = @()
        foreach ($reference in @($object.references)) {
            $referenceKey = "{0}|{1}" -f [string]$reference.field, [string]$reference.target_id
            $referenceKeys += $referenceKey
            $ruleKey = "{0}|{1}" -f [string]$object.kind, [string]$reference.field
            if (-not $allowedReferences.ContainsKey($ruleKey) -or
                $allowedReferences[$ruleKey] -cnotcontains [string]$reference.target_kind) {
                Fail-GameplayConfig "reference" "authority reference direction is not registered"
            }
            $targetId = [string]$reference.target_id
            if (-not $objectsById.ContainsKey($targetId) -or
                [string]$objectsById[$targetId].kind -cne [string]$reference.target_kind) {
                Fail-GameplayConfig "reference" "authority reference target differs"
            }
        }
        if (@($referenceKeys | Sort-Object -Unique).Count -ne $referenceKeys.Count) {
            Fail-GameplayConfig "reference" "authority reference is duplicated"
        }

        $actualNumeric = @($object.numeric_values | ForEach-Object { [string]$_.field })
        if (@($actualNumeric | Sort-Object -Unique).Count -ne $actualNumeric.Count) {
            Fail-GameplayConfig "range" "numeric field is duplicated"
        }
        $expectedNumeric = @(
            $numeric.entries |
            Where-Object { [string]$_.kind -ceq [string]$object.kind } |
            ForEach-Object { [string]$_.field }
        )
        Assert-StringSetEqual $actualNumeric $expectedNumeric "range" "$($object.id) numeric field set"
        foreach ($value in @($object.numeric_values)) {
            $numericKey = "{0}|{1}" -f [string]$object.kind, [string]$value.field
            if (-not $numericRules.ContainsKey($numericKey)) {
                Fail-GameplayConfig "range" "numeric field is not registered"
            }
            $rule = $numericRules[$numericKey]
            if ([string]$value.unit -cne [string]$rule.unit) {
                Fail-GameplayConfig "range" "numeric unit differs"
            }
            $checked = ConvertTo-CheckedInteger ([string]$value.value) "$($object.id).$($value.field)"
            $minimum = ConvertTo-CheckedInteger ([string]$rule.minimum) "$numericKey minimum"
            $maximum = ConvertTo-CheckedInteger ([string]$rule.maximum) "$numericKey maximum"
            if ($checked -lt $minimum -or $checked -gt $maximum) {
                Fail-GameplayConfig "range" "$($object.id).$($value.field) is outside registered range"
            }
        }
    }
    Test-ReferenceGraphAcyclic $objectsById

    foreach ($effect in @($authority.objects | Where-Object { [string]$_.kind -ceq "effect" })) {
        $product = (Get-NumericValue $effect "magnitude") * (Get-NumericValue $effect "max-stacks")
        if ($product -lt $Int64Minimum -or $product -gt $Int64Maximum) {
            Fail-GameplayConfig "range" "effect checked intermediate overflows"
        }
    }
    foreach ($projectile in @($authority.objects | Where-Object { [string]$_.kind -ceq "projectile" })) {
        $travelProduct = (Get-NumericValue $projectile "speed") *
            (Get-NumericValue $projectile "lifetime") * 50
        if ($travelProduct -lt $Int64Minimum -or $travelProduct -gt $Int64Maximum) {
            Fail-GameplayConfig "range" "projectile checked intermediate overflows"
        }
    }

    $bossPhases = @($authority.objects | Where-Object { [string]$_.kind -ceq "boss-phase" })
    $orderedPhases = @($bossPhases | Sort-Object { Get-NumericValue $_ "phase-order" })
    $previousThreshold = $null
    for ($index = 0; $index -lt $orderedPhases.Count; $index++) {
        $order = Get-NumericValue $orderedPhases[$index] "phase-order"
        $threshold = Get-NumericValue $orderedPhases[$index] "health-threshold"
        if ($order -ne ($index + 1) -or
            ($null -ne $previousThreshold -and $threshold -ge $previousThreshold)) {
            Fail-GameplayConfig "range" "Boss phase order or threshold is not strictly monotonic"
        }
        $previousThreshold = $threshold
    }

    $encounters = @($authority.objects | Where-Object { [string]$_.kind -ceq "encounter" })
    foreach ($encounter in $encounters) {
        $ownerCount = Get-NumericValue $encounter "owner-count"
        $visitorCount = Get-NumericValue $encounter "visitor-count"
        $bossCount = Get-NumericValue $encounter "boss-count"
        if ($ownerCount -ne 1 -or $bossCount -ne 1 -or ($ownerCount + $visitorCount) -gt 8) {
            Fail-GameplayConfig "range" "encounter player or Boss capacity is invalid"
        }
    }

    $resourceIds = @($presentation.resources | ForEach-Object { [string]$_.id })
    if (@($resourceIds | Sort-Object -Unique).Count -ne $resourceIds.Count) {
        Fail-GameplayConfig "presentation-parity" "presentation resource ID is duplicated"
    }
    $mappingIds = @($presentation.mappings | ForEach-Object { [string]$_.semantic_id })
    if (@($mappingIds | Sort-Object -Unique).Count -ne $mappingIds.Count) {
        Fail-GameplayConfig "presentation-parity" "presentation semantic mapping is duplicated"
    }
    foreach ($mapping in @($presentation.mappings)) {
        $semanticId = [string]$mapping.semantic_id
        if (-not $objectsById.ContainsKey($semanticId) -or
            [string]$objectsById[$semanticId].kind -cne [string]$mapping.semantic_kind) {
            Fail-GameplayConfig "presentation-parity" "presentation semantic target differs"
        }
        foreach ($resourceRef in @($mapping.resource_refs)) {
            if ($resourceIds -cnotcontains [string]$resourceRef) {
                Fail-GameplayConfig "presentation-parity" "presentation resource reference differs"
            }
        }
    }
    $parityIds = @(
        $authority.objects |
        Where-Object { [string]$_.kind -in @("actor", "ability", "effect", "cue") } |
        ForEach-Object { [string]$_.id }
    )
    Assert-StringSetEqual $mappingIds $parityIds "presentation-parity" "authority presentation mapping"

    foreach ($required in @($coverage.required_roles)) {
        $matches = @($authority.objects | Where-Object { [string]$_.role -ceq [string]$required.role })
        if ($matches.Count -ne 1 -or
            [string]$matches[0].kind -cne [string]$required.authority_kind) {
            Fail-GameplayConfig "coverage" "required authority role differs"
        }
        if ([bool]$required.presentation_required -and
            $mappingIds -cnotcontains [string]$matches[0].id) {
            Fail-GameplayConfig "coverage" "required presentation role differs"
        }
    }

    if (-not $objectsById.ContainsKey([string]$bindings.collision_layer_ref) -or
        [string]$objectsById[[string]$bindings.collision_layer_ref].kind -cne "collision-layer" -or
        -not $objectsById.ContainsKey([string]$bindings.navigation_policy_ref) -or
        [string]$objectsById[[string]$bindings.navigation_policy_ref].kind -cne "navigation-policy") {
        Fail-GameplayConfig "binding" "map collision or navigation binding differs"
    }

    if ([string]$package.qualification_state -ceq "governance-only") {
        if (@($objectsById.Keys | Where-Object { -not $_.StartsWith("fixture/", [StringComparison]::Ordinal) }).Count -gt 0 -or
            @($resourceIds | Where-Object { -not $_.StartsWith("fixture/", [StringComparison]::Ordinal) }).Count -gt 0 -or
            -not ([string]$bindings.map_id).StartsWith("fixture/", [StringComparison]::Ordinal)) {
            Fail-GameplayConfig "compatibility" "governance-only package escaped fixture namespace"
        }
    }
    elseif (@($objectsById.Keys | Where-Object { $_.StartsWith("fixture/", [StringComparison]::Ordinal) }).Count -gt 0 -or
        @($resourceIds | Where-Object { $_.StartsWith("fixture/", [StringComparison]::Ordinal) }).Count -gt 0 -or
        ([string]$bindings.map_id).StartsWith("fixture/", [StringComparison]::Ordinal)) {
        Fail-GameplayConfig "compatibility" "production package uses fixture namespace"
    }

    $packageIdentity = Get-PackageIdentity $manifest $packageEntry $fileEntries
    $bindingDigests = @(
        $packageIdentity,
        [string]$bindings.navigation_identity,
        [string]$bindings.physics_identity
    )
    if (@($bindingDigests | Sort-Object -Unique).Count -ne 3) {
        Fail-GameplayConfig "binding" "config navigation and physics identities are not distinct"
    }
    return [pscustomobject][ordered]@{
        PackageId = [string]$package.package_id
        ConfigIdentity = $packageIdentity
        QualificationState = [string]$package.qualification_state
    }
}

try {
    switch ($Action) {
        "validate" {
            $result = Invoke-GameplayConfigValidation
            Write-Output (
                "GAMEPLAY_CONFIG_VALID package={0} config_identity={1} state={2}" -f
                $result.PackageId,
                $result.ConfigIdentity,
                $result.QualificationState
            )
        }
    }
    exit 0
}
catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
