# 该入口只读检查客户端程序集、owner、依赖和Unity序列化边界；report阶段不阻断迁移。
[CmdletBinding()]
param(
    # Action区分迁移期只读报告与最终硬门；硬门将在模块迁移完成后启用全部规则。
    [ValidateSet("report", "verify")]
    [string]$Action = "report",
    # ReportPath可选写入ignored目录，未提供时只向stdout输出JSON。
    [string]$ReportPath = ""
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ClientRoot = Join-Path $RepositoryRoot "client"
$AssetsRoot = Join-Path $ClientRoot "Assets"
$ScriptsRoot = Join-Path $AssetsRoot "App\Scripts"
$TestsRoot = Join-Path $AssetsRoot "App\Tests"
$RegistryPath = Join-Path $ClientRoot "Architecture\owner-registry.json"

# Assert-ClosedProperties拒绝registry和asmdef中未声明字段造成的静默语义漂移。
function Assert-ClosedProperties {
    param([object]$Value, [string[]]$Allowed, [string]$Context)
    $actual = @($Value.PSObject.Properties.Name)
    $unknown = @($actual | Where-Object { $_ -notin $Allowed })
    $missing = @($Allowed | Where-Object { $_ -notin $actual })
    if ($unknown.Count -gt 0 -or $missing.Count -gt 0) {
        throw "$Context shape is invalid"
    }
}

# Get-RelativePath把审计结果限制为仓库相对路径，避免报告泄漏本机目录。
function Get-RelativePath {
    param([string]$Path)
    $root = [System.IO.Path]::GetFullPath($RepositoryRoot).TrimEnd('\') + '\'
    $full = [System.IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "architecture input escaped repository root"
    }
    return $full.Substring($root.Length).Replace('\', '/')
}

# Get-ShortTypeName取得完整C#类型名最后一段，用于声明存在性检查。
function Get-ShortTypeName {
    param([string]$TypeName)
    return ($TypeName -split '\.')[-1]
}

# Test-TypeExists只检查稳定类型声明，不推断业务正确性或可变状态。
function Test-TypeExists {
    param([string]$TypeName, [object[]]$SourceDocuments)
    $shortName = [regex]::Escape((Get-ShortTypeName $TypeName))
    $pattern = "\b(class|struct|interface|enum)\s+$shortName\b"
    return @($SourceDocuments | Where-Object { $_.Text -match $pattern }).Count -gt 0
}

# Read-OwnerRegistry验证唯一owner、状态种类和登记字段，并返回逐owner差异。
function Read-OwnerRegistry {
    param([object[]]$SourceDocuments, [object[]]$TestDocuments)
    if (-not (Test-Path -LiteralPath $RegistryPath -PathType Leaf)) {
        throw "client owner registry is missing"
    }
    $registry = Get-Content -LiteralPath $RegistryPath -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-ClosedProperties $registry @("schemaVersion", "owners") "owner registry"
    if ($registry.schemaVersion -ne 1 -or @($registry.owners).Count -eq 0) {
        throw "owner registry version or owners are invalid"
    }

    $ownerIDs = @{}
    $stateKinds = @{}
    $results = [System.Collections.Generic.List[object]]::new()
    foreach ($owner in @($registry.owners)) {
        Assert-ClosedProperties $owner @(
            "id",
            "stateKinds",
            "ownerType",
            "facadeType",
            "snapshotType",
            "module",
            "source",
            "commands",
            "collaborators",
            "tests") "owner entry"
        $differences = [System.Collections.Generic.List[string]]::new()
        $id = [string]$owner.id
        if ($id -notmatch '^[a-z][a-z0-9-]*$' -or $ownerIDs.ContainsKey($id)) {
            $differences.Add("owner-id-invalid-or-duplicate")
        }
        else {
            $ownerIDs[$id] = $true
        }
        if ($owner.module -notin @("Foundation", "Application", "Infrastructure", "Presentation", "Runtime")) {
            $differences.Add("module-invalid")
        }
        foreach ($stateKind in @($owner.stateKinds)) {
            $state = [string]$stateKind
            if ($state -notmatch '^[a-z][a-z0-9-]*$' -or $stateKinds.ContainsKey($state)) {
                $differences.Add("state-kind-invalid-or-duplicate:$state")
            }
            else {
                $stateKinds[$state] = $id
            }
        }

        $sourcePath = Join-Path $RepositoryRoot ([string]$owner.source)
        $sourceText = ""
        if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
            $differences.Add("owner-source-missing")
        }
        else {
            $sourceText = [System.IO.File]::ReadAllText($sourcePath)
            $ownerPattern = "\bclass\s+$([regex]::Escape((Get-ShortTypeName $owner.ownerType)))\b"
            if ($sourceText -notmatch $ownerPattern) {
                $differences.Add("owner-type-not-declared-in-source")
            }
        }
        if (-not (Test-TypeExists $owner.facadeType $SourceDocuments)) {
            $differences.Add("facade-type-missing")
        }
        if (-not (Test-TypeExists $owner.snapshotType $SourceDocuments)) {
            $differences.Add("snapshot-type-missing")
        }
        foreach ($command in @($owner.commands)) {
            $commandName = [string]$command
            if ($commandName -notmatch '^[A-Z][A-Za-z0-9]*$' -or
                $sourceText -notmatch ("\b" + [regex]::Escape($commandName) + "\s*\(")) {
                $differences.Add("command-missing:$commandName")
            }
        }
        foreach ($collaborator in @($owner.collaborators)) {
            if ([string]$collaborator -notmatch '^[a-z][a-z0-9-]*$') {
                $differences.Add("collaborator-invalid:$collaborator")
            }
        }
        foreach ($test in @($owner.tests)) {
            if (-not (Test-TypeExists ([string]$test) $TestDocuments)) {
                $differences.Add("test-entry-missing:$test")
            }
        }
        $results.Add([ordered]@{
            id = $id
            module = [string]$owner.module
            ownerType = [string]$owner.ownerType
            stateKinds = @($owner.stateKinds)
            status = if ($differences.Count -eq 0) { "matched" } else { "different" }
            differences = @($differences)
        })
    }
    return [ordered]@{
        registry = $registry
        owners = @($results)
        stateOwnerCount = $stateKinds.Count
    }
}

# Read-AssemblyGraph读取手写asmdef及其显式引用，不读取或提交generated程序集。
function Read-AssemblyGraph {
    $assemblies = [System.Collections.Generic.List[object]]::new()
    foreach ($file in @(Get-ChildItem -LiteralPath $AssetsRoot -Filter "*.asmdef" -File -Recurse |
            Where-Object { $_.FullName -notmatch '[\\/]Generated[\\/]' } |
            Sort-Object FullName)) {
        $asmdef = Get-Content -LiteralPath $file.FullName -Raw -Encoding UTF8 | ConvertFrom-Json
        $assemblies.Add([ordered]@{
            name = [string]$asmdef.name
            path = Get-RelativePath $file.FullName
            references = @($asmdef.references)
            noEngineReferences = [bool]$asmdef.noEngineReferences
        })
    }
    return @($assemblies)
}

# Find-ForbiddenImports报告当前跨目标层namespace；report阶段只记录，不判断迁移完成。
function Find-ForbiddenImports {
    param([object[]]$SourceDocuments)
    $rules = @(
        [ordered]@{ path = "/Application/"; pattern = '^using IHomeland\.Client\.Infrastructure'; id = "application-to-infrastructure" },
        [ordered]@{ path = "/Application/"; pattern = '^using (IHomeland\.Protocol|Google\.Protobuf)'; id = "application-to-protocol" },
        [ordered]@{ path = "/Presentation/"; pattern = '^using IHomeland\.Client\.Infrastructure'; id = "presentation-to-infrastructure" },
        [ordered]@{ path = "/Presentation/"; pattern = '^using IHomeland\.Client\.Scenes'; id = "presentation-to-runtime-scene" },
        [ordered]@{ path = "/Foundation/"; pattern = '^using IHomeland\.Client\.Infrastructure'; id = "foundation-to-infrastructure" }
    )
    $findings = [System.Collections.Generic.List[object]]::new()
    foreach ($document in $SourceDocuments) {
        foreach ($rule in $rules) {
            if ($document.Path -notlike "*$($rule.path)*") {
                continue
            }
            foreach ($line in @($document.Text -split "`r?`n")) {
                if ($line.Trim() -match $rule.pattern) {
                    $findings.Add([ordered]@{
                        rule = $rule.id
                        path = $document.RelativePath
                        import = $line.Trim().TrimEnd(';').Substring(6)
                    })
                }
            }
        }
    }
    return @($findings)
}

# Find-CompositionResultReferences列出顶层装配结果向feature、Host或资格代码的扩散。
function Find-CompositionResultReferences {
    param([object[]]$SourceDocuments)
    $references = [System.Collections.Generic.List[object]]::new()
    foreach ($document in $SourceDocuments) {
        if ($document.RelativePath.EndsWith("/AppComposition.cs")) {
            continue
        }
        $count = [regex]::Matches($document.Text, '\bAppCompositionResult\b').Count
        if ($count -gt 0) {
            $references.Add([ordered]@{
                path = $document.RelativePath
                references = $count
            })
        }
    }
    return @($references)
}

# Read-SerializedScriptReferences把Scene/Prefab/asset中的script GUID映射回脚本路径，仅做完整性盘点。
function Read-SerializedScriptReferences {
    $guidOwners = @{}
    foreach ($meta in @(Get-ChildItem -LiteralPath $AssetsRoot -Filter "*.cs.meta" -File -Recurse)) {
        $text = [System.IO.File]::ReadAllText($meta.FullName)
        $match = [regex]::Match($text, '(?m)^guid:\s*([0-9a-f]{32})\s*$')
        if ($match.Success) {
            $guidOwners[$match.Groups[1].Value] = Get-RelativePath $meta.FullName.Substring(0, $meta.FullName.Length - 5)
        }
    }
    $references = [System.Collections.Generic.List[object]]::new()
    $serializedAppRoot = Join-Path $AssetsRoot "App"
    foreach ($asset in @(Get-ChildItem -LiteralPath $serializedAppRoot -File -Recurse |
            Where-Object { $_.Extension -in @(".unity", ".prefab", ".asset") } |
            Sort-Object FullName)) {
        $text = [System.IO.File]::ReadAllText($asset.FullName)
        foreach ($match in [regex]::Matches($text, 'guid:\s*([0-9a-f]{32})')) {
            $guid = $match.Groups[1].Value
            if ($guidOwners.ContainsKey($guid)) {
                $references.Add([ordered]@{
                    asset = Get-RelativePath $asset.FullName
                    script = $guidOwners[$guid]
                })
            }
        }
    }
    return @($references | Sort-Object asset, script -Unique)
}

# New-SourceDocuments一次读取生产或测试C#，供后续稳定声明与import检查复用。
function Read-SerializationIntegrity {
    $scriptGuidOwners = @{}
    $duplicateGuids = [System.Collections.Generic.List[string]]::new()
    $missingMetas = [System.Collections.Generic.List[string]]::new()
    foreach ($source in @(Get-ChildItem -LiteralPath $ScriptsRoot -Filter "*.cs" -File -Recurse)) {
        $metaPath = $source.FullName + ".meta"
        if (-not (Test-Path -LiteralPath $metaPath -PathType Leaf)) {
            $missingMetas.Add((Get-RelativePath $source.FullName))
            continue
        }

        $metaText = [System.IO.File]::ReadAllText($metaPath)
        $guidMatch = [regex]::Match($metaText, '(?m)^guid:\s*([0-9a-f]{32})\s*$')
        if (-not $guidMatch.Success) {
            $missingMetas.Add((Get-RelativePath $metaPath))
            continue
        }

        $guid = $guidMatch.Groups[1].Value
        if ($scriptGuidOwners.ContainsKey($guid)) {
            $duplicateGuids.Add($guid)
        }
        else {
            $scriptGuidOwners[$guid] = Get-RelativePath $source.FullName
        }
    }

    $missingScripts = [System.Collections.Generic.List[object]]::new()
    $serializedAppRoot = Join-Path $AssetsRoot "App"
    foreach ($asset in @(Get-ChildItem -LiteralPath $serializedAppRoot -File -Recurse |
            Where-Object { $_.Extension -in @(".unity", ".prefab", ".asset") } |
            Sort-Object FullName)) {
        $text = [System.IO.File]::ReadAllText($asset.FullName)
        foreach ($match in [regex]::Matches(
                $text,
                'm_Script:\s*\{fileID:\s*-?\d+,\s*guid:\s*([0-9a-f]{32}),\s*type:\s*3\}')) {
            $guid = $match.Groups[1].Value
            $inspectionLength = [Math]::Min(300, $text.Length - $match.Index)
            $componentHeader = $text.Substring($match.Index, $inspectionLength)
            $typeMatch = [regex]::Match(
                $componentHeader,
                '(?m)^  m_EditorClassIdentifier:\s*(\S.*)?$')
            if ($typeMatch.Success -and
                -not [string]::IsNullOrWhiteSpace($typeMatch.Groups[1].Value)) {
                continue
            }
            if (-not $scriptGuidOwners.ContainsKey($guid)) {
                $missingScripts.Add([ordered]@{
                    asset = Get-RelativePath $asset.FullName
                    guid = $guid
                })
            }
        }
    }

    return [ordered]@{
        missingMetas = @($missingMetas | Sort-Object -Unique)
        duplicateScriptGuids = @($duplicateGuids | Sort-Object -Unique)
        missingSerializedScripts = @($missingScripts | Sort-Object asset, guid -Unique)
    }
}

function Test-ProductionAssemblyGraph {
    param([object[]]$Assemblies)
    $violations = [System.Collections.Generic.List[string]]::new()
    $expected = [ordered]@{
        "IHomeland.Client.Foundation" = @()
        "IHomeland.Client.Application" = @("IHomeland.Client.Foundation")
        "IHomeland.Client.Infrastructure" = @(
            "IHomeland.Client.Foundation",
            "IHomeland.Client.Application",
            "IHomeland.Client.Protocol.Generated")
        "IHomeland.Client.Presentation" = @(
            "IHomeland.Client.Foundation",
            "IHomeland.Client.Application")
        "IHomeland.Client.Runtime" = @(
            "IHomeland.Client.Foundation",
            "IHomeland.Client.Application",
            "IHomeland.Client.Infrastructure",
            "IHomeland.Client.Presentation",
            "IHomeland.Client.Protocol.Generated",
            "Unity.Cinemachine",
            "Unity.InputSystem",
            "Unity.TextMeshPro")
    }

    foreach ($entry in $expected.GetEnumerator()) {
        $assembly = @($Assemblies | Where-Object { $_.name -eq $entry.Key })
        if ($assembly.Count -ne 1) {
            $violations.Add("assembly-missing-or-duplicate:$($entry.Key)")
            continue
        }

        $actual = @($assembly[0].references | Sort-Object)
        $wanted = @($entry.Value | Sort-Object)
        if (($actual -join "`n") -ne ($wanted -join "`n")) {
            $violations.Add("assembly-references-differ:$($entry.Key)")
        }
    }

    foreach ($name in @(
            "IHomeland.Client.Foundation",
            "IHomeland.Client.Application",
            "IHomeland.Client.Presentation")) {
        $assembly = @($Assemblies | Where-Object { $_.name -eq $name })
        if ($assembly.Count -eq 1 -and -not $assembly[0].noEngineReferences) {
            $violations.Add("no-engine-references-disabled:$name")
        }
    }

    return @($violations)
}

function Read-ModularityInventory {
    param([object[]]$SourceDocuments)
    $flows = [System.Collections.Generic.List[string]]::new()
    $ports = [System.Collections.Generic.List[string]]::new()
    $adapters = [System.Collections.Generic.List[string]]::new()
    $complexity = [System.Collections.Generic.List[object]]::new()
    foreach ($document in $SourceDocuments) {
        foreach ($match in [regex]::Matches(
                $document.Text,
                '\bclass\s+([A-Z][A-Za-z0-9]*Flow)\b')) {
            $flows.Add($match.Groups[1].Value)
        }
        foreach ($match in [regex]::Matches(
                $document.Text,
                '\binterface\s+(I[A-Z][A-Za-z0-9]*Port)\b')) {
            $ports.Add($match.Groups[1].Value)
        }
        foreach ($match in [regex]::Matches(
                $document.Text,
                '\bclass\s+([A-Z][A-Za-z0-9]*Adapter)\b')) {
            $adapters.Add($match.Groups[1].Value)
        }

        $lineCount = @($document.Text -split "`r?`n").Count
        if ($lineCount -ge 500) {
            $complexity.Add([ordered]@{
                path = $document.RelativePath
                lines = $lineCount
                severity = if ($lineCount -ge 1200) { "review" } else { "observe" }
            })
        }
    }

    return [ordered]@{
        flows = @($flows | Sort-Object -Unique)
        ports = @($ports | Sort-Object -Unique)
        adapters = @($adapters | Sort-Object -Unique)
        complexityHints = @($complexity | Sort-Object lines -Descending)
    }
}

function New-SourceDocuments {
    param([string]$Root)
    $documents = [System.Collections.Generic.List[object]]::new()
    foreach ($file in @(Get-ChildItem -LiteralPath $Root -Filter "*.cs" -File -Recurse |
            Where-Object { $_.FullName -notmatch '[\\/]Generated[\\/]' } |
            Sort-Object FullName)) {
        $documents.Add([pscustomobject]@{
            Path = $file.FullName.Replace('\', '/')
            RelativePath = Get-RelativePath $file.FullName
            Text = [System.IO.File]::ReadAllText($file.FullName)
        })
    }
    return @($documents)
}

$sourceDocuments = @(New-SourceDocuments $ScriptsRoot)
$testDocuments = @(New-SourceDocuments $TestsRoot)
$ownerAudit = Read-OwnerRegistry $sourceDocuments $testDocuments
$ownerDifferences = @($ownerAudit.owners | Where-Object { $_.status -ne "matched" })
$assemblies = @(Read-AssemblyGraph)
$forbiddenImports = @(Find-ForbiddenImports $sourceDocuments)
$serializationIntegrity = Read-SerializationIntegrity
$assemblyViolations = @(Test-ProductionAssemblyGraph $assemblies)
$legacyPaths = @(
    "client/Assets/App/Scripts/Core/Lifetime",
    "client/Assets/App/Scripts/Application/Control/ClientControlChannel.cs",
    "client/Assets/App/Scripts/Application/Gameplay/ClientGameplayChannel.cs"
)
$legacyPathViolations = @($legacyPaths | Where-Object {
        Test-Path -LiteralPath (Join-Path $RepositoryRoot $_)
    })
$hardGateViolations = @(
    @($ownerDifferences | ForEach-Object { "owner-registry-difference:$($_.id)" })
    $assemblyViolations
    @($forbiddenImports | ForEach-Object { "forbidden-import:$($_.rule):$($_.path)" })
    @($serializationIntegrity.missingMetas | ForEach-Object { "missing-meta:$_" })
    @($serializationIntegrity.duplicateScriptGuids | ForEach-Object { "duplicate-script-guid:$_" })
    @($serializationIntegrity.missingSerializedScripts | ForEach-Object {
            "missing-serialized-script:$($_.asset):$($_.guid)"
        })
    @($legacyPathViolations | ForEach-Object { "legacy-path:$_" })
)
$report = [ordered]@{
    schemaVersion = 1
    mode = if ($Action -eq "verify") { "hard-gate" } else { "report-only" }
    hardGateEnabled = $Action -eq "verify"
    hardGateViolations = @($hardGateViolations)
    registry = [ordered]@{
        path = Get-RelativePath $RegistryPath
        ownerCount = @($ownerAudit.owners).Count
        stateOwnerCount = $ownerAudit.stateOwnerCount
        differences = @($ownerDifferences)
        owners = @($ownerAudit.owners)
    }
    assemblies = $assemblies
    forbiddenImports = $forbiddenImports
    compositionResultReferences = @(Find-CompositionResultReferences $sourceDocuments)
    serializedScriptReferences = @(Read-SerializedScriptReferences)
    serializationIntegrity = $serializationIntegrity
    modularity = Read-ModularityInventory $sourceDocuments
}

$json = ($report | ConvertTo-Json -Depth 12) + "`n"
if (-not [string]::IsNullOrWhiteSpace($ReportPath)) {
    $fullReportPath = [System.IO.Path]::GetFullPath($ReportPath)
    $reportDirectory = [System.IO.Path]::GetDirectoryName($fullReportPath)
    [System.IO.Directory]::CreateDirectory($reportDirectory) | Out-Null
    [System.IO.File]::WriteAllText(
        $fullReportPath,
        $json,
        [System.Text.UTF8Encoding]::new($false))
}
$json

if ($Action -eq "verify" -and $hardGateViolations.Count -gt 0) {
    throw "client architecture hard gate failed: $($hardGateViolations -join ', ')"
}
