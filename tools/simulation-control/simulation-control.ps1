[CmdletBinding()]
param(
    [ValidateSet("validate", "test", "verify")]
    [string]$Action = "validate",
    [string]$FixtureRoot,
    [ValidateRange(3, 30)]
    [int]$FuzzTimeSeconds = 3,
    [ValidateRange(120, 900)]
    [int]$StorageTimeoutSeconds = 300,
    [string]$ServerQualificationReportPath = "",
    [string]$ClientQualificationReportPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Get-RepositoryRoot 返回脚本所在仓库根，避免依赖调用方当前目录。
function Get-RepositoryRoot {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

# Get-Sha256 返回文件内容的小写 SHA-256。
function Get-Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "simulation-control digest input is missing: $Path"
    }
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

# Get-TextSha256 返回 UTF-8 文本的小写 SHA-256，不引入换行或平台编码。
function Get-TextSha256 {
    param([Parameter(Mandatory = $true)][string]$Value)

    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = $algorithm.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($Value))
        return [string]::Concat(@($bytes | ForEach-Object { $_.ToString("x2") }))
    }
    finally {
        $algorithm.Dispose()
    }
}

# Read-JsonDocument 读取 UTF-8/LF JSON，并拒绝 BOM、CRLF 和顶层重复字段。
function Read-JsonDocument {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "simulation-control missing file: $Path"
    }
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        throw "simulation-control file has UTF-8 BOM: $Path"
    }
    $raw = [System.Text.Encoding]::UTF8.GetString($bytes)
    if ($raw.Contains("`r")) {
        throw "simulation-control file is not canonical LF: $Path"
    }
    Assert-NoTopLevelDuplicateProperty -Raw $raw -Path $Path
    try {
        return ($raw | ConvertFrom-Json)
    }
    catch {
        throw "simulation-control invalid JSON '$Path': $($_.Exception.Message)"
    }
}

# Assert-NoTopLevelDuplicateProperty 检查根 object 的成员名唯一性。
function Assert-NoTopLevelDuplicateProperty {
    param(
        [Parameter(Mandatory = $true)][string]$Raw,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $depth = 0
    $inString = $false
    $escaped = $false
    $token = New-Object System.Text.StringBuilder
    $candidate = $null
    $names = @{}
    for ($index = 0; $index -lt $Raw.Length; $index++) {
        $character = $Raw[$index]
        if ($inString) {
            if ($escaped) {
                $escaped = $false
                [void]$token.Append($character)
                continue
            }
            if ($character -eq "\") {
                $escaped = $true
                [void]$token.Append($character)
                continue
            }
            if ($character -eq '"') {
                $inString = $false
                if ($depth -eq 1) {
                    $candidate = $token.ToString()
                }
                $null = $token.Clear()
                continue
            }
            [void]$token.Append($character)
            continue
        }
        if ($character -eq '"') {
            $inString = $true
            $null = $token.Clear()
            continue
        }
        if ($character -eq "{") {
            $depth++
            $candidate = $null
            continue
        }
        if ($character -eq "}") {
            $depth--
            $candidate = $null
            continue
        }
        if ($depth -eq 1 -and $character -eq ":" -and $null -ne $candidate) {
            if ($names.ContainsKey($candidate)) {
                throw "simulation-control duplicate top-level property '$candidate': $Path"
            }
            $names[$candidate] = $true
            $candidate = $null
            continue
        }
        if (-not [char]::IsWhiteSpace($character) -and $character -ne ",") {
            $candidate = $null
        }
    }
}

# Assert-Properties 验证 closed object 的必需与允许字段集合。
function Assert-Properties {
    param(
        [Parameter(Mandatory = $true)]$Value,
        [Parameter(Mandatory = $true)][string[]]$Required,
        [Parameter(Mandatory = $true)][string[]]$Allowed,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $actual = @($Value.PSObject.Properties.Name)
    foreach ($name in $Required) {
        if ($actual -notcontains $name) {
            throw "$Context missing required property '$name'"
        }
    }
    foreach ($name in $actual) {
        if ($Allowed -notcontains $name) {
            throw "$Context contains unknown property '$name'"
        }
    }
}

# Assert-Identity 验证项目内部受控 identity，不接受空值或路径材料。
function Assert-Identity {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Context
    )

    if ($Value -notmatch "^[A-Za-z][A-Za-z0-9_-]{3,95}$") {
        throw "$Context is not a canonical identity"
    }
}

# Assert-Decimal 验证 uint64 使用的无前导零规范十进制字符串。
function Assert-Decimal {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Context
    )

    if ($Value -notmatch "^(0|[1-9][0-9]{0,19})$") {
        throw "$Context is not a canonical decimal"
    }
    $parsed = 0L
    if (-not [uint64]::TryParse($Value, [ref]$parsed)) {
        throw "$Context exceeds uint64"
    }
}

# Assert-Sha256 验证小写 SHA-256 文本。
function Assert-Sha256 {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Context
    )

    if ($Value -notmatch "^[0-9a-f]{64}$") {
        throw "$Context is not a lowercase SHA-256"
    }
}

# ConvertTo-CanonicalValue 按属性名递归排序，供 golden parity 使用。
function ConvertTo-CanonicalValue {
    param($Value)

    if ($null -eq $Value) {
        return $null
    }
    if ($Value -is [System.Array]) {
        $items = @()
        foreach ($item in $Value) {
            $items += ,(ConvertTo-CanonicalValue -Value $item)
        }
        return $items
    }
    if ($Value -is [System.Management.Automation.PSCustomObject]) {
        $ordered = [ordered]@{}
        foreach ($property in @($Value.PSObject.Properties.Name | Sort-Object)) {
            $ordered[$property] = ConvertTo-CanonicalValue -Value $Value.$property
        }
        return [pscustomobject]$ordered
    }
    return $Value
}

# Assert-ControlFrame 验证公共 frame envelope 与按 kind 封闭的 payload。
function Assert-ControlFrame {
    param(
        [Parameter(Mandatory = $true)]$Frame,
        [Parameter(Mandatory = $true)][string[]]$KnownKinds,
        [Parameter(Mandatory = $true)][string]$Context
    )

    $fields = @("schemaVersion", "sessionNonce", "sequence", "requestId", "kind", "payload")
    Assert-Properties -Value $Frame -Required $fields -Allowed $fields -Context $Context
    if ($Frame.schemaVersion -ne "simulation-control-v1") {
        throw "$Context has unsupported schemaVersion"
    }
    if ([string]$Frame.sessionNonce -notmatch "^[0-9a-f]{64}$") {
        throw "$Context has invalid sessionNonce"
    }
    Assert-Decimal -Value ([string]$Frame.sequence) -Context "$Context sequence"
    if ([string]$Frame.requestId -notmatch "^sctl_[A-Za-z0-9_-]{16,80}$") {
        throw "$Context has invalid requestId"
    }
    if ($KnownKinds -notcontains [string]$Frame.kind) {
        throw "$Context has unknown kind"
    }
    $payloadAllowed = @{
        "node.hello.challenge" = @("simulationNodeId", "runtimeNodeId", "expectedBuildIdentity", "expectedModelManifest", "expectedProfileManifest", "instanceCapacity", "actorCapacity")
        "node.hello.receipt" = @("simulationNodeId", "runtimeNodeId", "buildIdentity", "modelManifest", "profileManifest", "platformQualification", "instanceCapacity", "actorCapacity")
        "node.health.query" = @("simulationNodeId")
    }
    if ($payloadAllowed.ContainsKey([string]$Frame.kind)) {
        $allowed = $payloadAllowed[[string]$Frame.kind]
        Assert-Properties -Value $Frame.payload -Required $allowed -Allowed $allowed -Context "$Context payload"
    }
}

# Assert-ForbiddenScope 扫描 JSON source，拒绝后续网络与客户端能力提前进入。
function Assert-ForbiddenScope {
    param([Parameter(Mandatory = $true)][string]$Root)

    $patterns = @(
        '"grpc"',
        '"tcp"',
        '"udp"',
        '"asio"',
        '"kcp"',
        '"aead"',
        '"ticket"',
        '"cookie"',
        '"unity"',
        '"messageId"\s*:\s*[0-9]',
        '"port"\s*:'
    )
    foreach ($file in Get-ChildItem -LiteralPath $Root -Recurse -File -Filter "*.json") {
        $raw = [System.IO.File]::ReadAllText($file.FullName)
        foreach ($pattern in $patterns) {
            if ($raw -match $pattern) {
                throw "simulation-control forbidden scope '$pattern': $($file.FullName)"
            }
        }
    }
}

# Invoke-ControlValidation 验证 corpus 完整性、B0.3 binding 和 golden。
function Invoke-ControlValidation {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot
    )

    $manifestPath = Join-Path $Root "manifest.json"
    $manifest = Read-JsonDocument -Path $manifestPath
    $manifestFields = @("schemaVersion", "documentKind", "b03Binding", "files", "cases")
    Assert-Properties -Value $manifest -Required $manifestFields -Allowed $manifestFields -Context "manifest"
    if ($manifest.schemaVersion -ne "simulation-control-v1" -or $manifest.documentKind -ne "manifest") {
        throw "simulation-control manifest identity is invalid"
    }
    $bindingFields = @("qualificationConclusion", "releaseBuildIdentitySha256", "asanBuildIdentitySha256", "qualificationGateReceiptSha256", "modelManifestSha256", "profileManifestSha256", "maximumActors", "simulationTickNs")
    Assert-Properties -Value $manifest.b03Binding -Required $bindingFields -Allowed $bindingFields -Context "manifest b03Binding"
    foreach ($name in @("releaseBuildIdentitySha256", "asanBuildIdentitySha256", "qualificationGateReceiptSha256", "modelManifestSha256", "profileManifestSha256")) {
        Assert-Sha256 -Value ([string]$manifest.b03Binding.$name) -Context "manifest b03Binding $name"
    }
    if ($manifest.b03Binding.qualificationConclusion -ne "implementation-qualified-windows-x64" -or
        [int]$manifest.b03Binding.maximumActors -ne 8 -or
        [int64]$manifest.b03Binding.simulationTickNs -ne 50000000) {
        throw "simulation-control B0.3 qualification binding is invalid"
    }
    $qualificationPath = Join-Path $RepositoryRoot "simulation\reports\qualification.json"
    $qualification = Read-JsonDocument -Path $qualificationPath
    if (-not [bool]$qualification.qualified -or
        [string]$qualification.conclusion -ne [string]$manifest.b03Binding.qualificationConclusion -or
        [string]$qualification.build_identity_sha256 -ne [string]$manifest.b03Binding.releaseBuildIdentitySha256 -or
        [string]$qualification.asan_build_identity_sha256 -ne [string]$manifest.b03Binding.asanBuildIdentitySha256 -or
        [string]$qualification.gate_receipt_sha256 -ne [string]$manifest.b03Binding.qualificationGateReceiptSha256 -or
        [string]$qualification.model_manifest_sha256 -ne [string]$manifest.b03Binding.modelManifestSha256 -or
        [string]$qualification.profile_manifest_sha256 -ne [string]$manifest.b03Binding.profileManifestSha256 -or
        [int]$qualification.maximum_actors -ne [int]$manifest.b03Binding.maximumActors -or
        [int64]$qualification.simulation_tick_ns -ne [int64]$manifest.b03Binding.simulationTickNs) {
        throw "simulation-control B0.3 qualification evidence drifted"
    }
    $modelPath = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model\manifest.json"
    $profilePath = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\manifest.json"
    if ((Get-Sha256 -Path $modelPath) -ne $manifest.b03Binding.modelManifestSha256 -or
        (Get-Sha256 -Path $profilePath) -ne $manifest.b03Binding.profileManifestSha256) {
        throw "simulation-control B0.3 model/profile binding drifted"
    }

    $registeredPaths = @()
    foreach ($entry in @($manifest.files) + @($manifest.cases)) {
        Assert-Properties -Value $entry -Required @("path", "sha256") -Allowed @("path", "sha256", "documentKind", "caseId", "category") -Context "manifest entry"
        $relative = [string]$entry.path
        if ($relative -notmatch "^[a-z0-9][a-z0-9._/-]+\.json$" -or $relative.Contains("..")) {
            throw "simulation-control manifest path is invalid: $relative"
        }
        if ($registeredPaths -contains $relative) {
            throw "simulation-control manifest path is duplicated: $relative"
        }
        $registeredPaths += $relative
        Assert-Sha256 -Value ([string]$entry.sha256) -Context "manifest hash $relative"
        $fullPath = Join-Path $Root ($relative -replace "/", "\")
        if ((Get-Sha256 -Path $fullPath) -ne [string]$entry.sha256) {
            throw "simulation-control digest drift: $relative"
        }
        $null = Read-JsonDocument -Path $fullPath
    }
    $actualCases = @(Get-ChildItem -LiteralPath (Join-Path $Root "cases") -File -Filter "*.json" | ForEach-Object {
        "cases/" + $_.Name
    } | Sort-Object)
    $registeredCases = @($manifest.cases | ForEach-Object { [string]$_.path } | Sort-Object)
    if (($actualCases -join "|") -ne ($registeredCases -join "|")) {
        throw "simulation-control case registration is incomplete"
    }

    $inventory = Read-JsonDocument -Path (Join-Path $Root "message-inventory.json")
    $inventoryFields = @("schemaVersion", "documentKind", "frameMaxBytes", "pendingRequestLimit", "resultOutboxLimit", "messages")
    Assert-Properties -Value $inventory -Required $inventoryFields -Allowed $inventoryFields -Context "message inventory"
    if ($inventory.schemaVersion -ne "simulation-control-v1" -or
        $inventory.documentKind -ne "message-inventory" -or
        [int]$inventory.frameMaxBytes -ne 65536 -or
        [int]$inventory.pendingRequestLimit -ne 256 -or
        [int]$inventory.resultOutboxLimit -ne 256) {
        throw "simulation-control inventory limits are invalid"
    }
    $messageFields = @("kind", "direction", "maxPayloadBytes", "deadlineClass", "replayIdentity")
    $knownKinds = @()
    foreach ($message in $inventory.messages) {
        Assert-Properties -Value $message -Required $messageFields -Allowed $messageFields -Context "message inventory entry"
        if ($knownKinds -contains [string]$message.kind) {
            throw "simulation-control kind is duplicated: $($message.kind)"
        }
        if ($message.direction -notin @("go-to-cpp", "cpp-to-go") -or [int]$message.maxPayloadBytes -lt 1 -or [int]$message.maxPayloadBytes -gt 65536) {
            throw "simulation-control message policy is invalid: $($message.kind)"
        }
        $knownKinds += [string]$message.kind
    }
    $expectedKinds = @(
        "instance.drain", "instance.drained", "instance.ready", "instance.start",
        "instance.status.query", "instance.status.receipt", "instance.stop", "instance.stopped",
        "node.health.query", "node.health.receipt", "node.hello.challenge", "node.hello.receipt",
        "node.shutdown", "node.stopped", "result.ack", "result.proposal"
    )
    if ((@($knownKinds | Sort-Object) -join "|") -ne ($expectedKinds -join "|")) {
        throw "simulation-control inventory kinds are incomplete"
    }

    $hello = Read-JsonDocument -Path (Join-Path $Root "cases\hello.json")
    Assert-Properties -Value $hello -Required @("schemaVersion", "caseId", "category", "frames") -Allowed @("schemaVersion", "caseId", "category", "frames") -Context "hello case"
    $lastSequence = [uint64]0
    foreach ($frame in $hello.frames) {
        Assert-ControlFrame -Frame $frame -KnownKinds $knownKinds -Context "hello frame"
        $sequence = [uint64]$frame.sequence
        if ($sequence -ne ($lastSequence + 1)) {
            throw "simulation-control hello sequence is not contiguous"
        }
        $lastSequence = $sequence
    }

    $runtimeConfig = Read-JsonDocument -Path (Join-Path $Root "runtime\config\control-baseline-v1.json")
    Assert-Properties -Value $runtimeConfig -Required @("schemaVersion", "documentKind", "configId", "tickStepMilliseconds", "inputInboxEntries", "hardTickDebt") -Allowed @("schemaVersion", "documentKind", "configId", "tickStepMilliseconds", "inputInboxEntries", "hardTickDebt") -Context "runtime config"
    if ($runtimeConfig.schemaVersion -ne "simulation-control-runtime-v1" -or
        $runtimeConfig.documentKind -ne "simulation-config" -or
        $runtimeConfig.configId -ne "control-baseline-v1" -or
        [int]$runtimeConfig.tickStepMilliseconds -ne 50 -or
        [int]$runtimeConfig.inputInboxEntries -ne 256 -or
        [int]$runtimeConfig.hardTickDebt -ne 4) {
        throw "simulation-control runtime config drifted"
    }
    $navigation = Read-JsonDocument -Path (Join-Path $Root "runtime\navigation\control-baseline-v1.json")
    Assert-Properties -Value $navigation -Required @("schemaVersion", "documentKind", "navigationId", "adapter", "assetMode", "productAsset") -Allowed @("schemaVersion", "documentKind", "navigationId", "adapter", "assetMode", "productAsset") -Context "navigation binding"
    if ($navigation.schemaVersion -ne "simulation-control-runtime-v1" -or
        $navigation.documentKind -ne "navigation-binding" -or
        $navigation.navigationId -ne "control-baseline-v1" -or
        $navigation.adapter -ne "detour" -or
        $navigation.assetMode -ne "registered-smoke-fixture" -or
        [bool]$navigation.productAsset) {
        throw "simulation-control navigation binding drifted"
    }
    $physics = Read-JsonDocument -Path (Join-Path $Root "runtime\physics\control-baseline-v1.json")
    Assert-Properties -Value $physics -Required @("schemaVersion", "documentKind", "physicsId", "adapter", "sceneMode", "productScene") -Allowed @("schemaVersion", "documentKind", "physicsId", "adapter", "sceneMode", "productScene") -Context "physics binding"
    if ($physics.schemaVersion -ne "simulation-control-runtime-v1" -or
        $physics.documentKind -ne "physics-binding" -or
        $physics.physicsId -ne "control-baseline-v1" -or
        $physics.adapter -ne "jolt" -or
        $physics.sceneMode -ne "registered-smoke-fixture" -or
        [bool]$physics.productScene) {
        throw "simulation-control physics binding drifted"
    }
    $lifecycle = Read-JsonDocument -Path (Join-Path $Root "cases\lifecycle.json")
    Assert-Properties -Value $lifecycle -Required @("schemaVersion", "caseId", "category", "assignment", "mappingGeneration", "seed", "configIdentity", "navigationIdentity", "physicsIdentity", "actorCapacity", "expectations") -Allowed @("schemaVersion", "caseId", "category", "assignment", "mappingGeneration", "seed", "configIdentity", "navigationIdentity", "physicsIdentity", "actorCapacity", "expectations") -Context "lifecycle case"
    if ([string]$lifecycle.configIdentity -ne (Get-Sha256 -Path (Join-Path $Root "runtime\config\control-baseline-v1.json")) -or
        [string]$lifecycle.navigationIdentity -ne (Get-Sha256 -Path (Join-Path $Root "runtime\navigation\control-baseline-v1.json")) -or
        [string]$lifecycle.physicsIdentity -ne (Get-Sha256 -Path (Join-Path $Root "runtime\physics\control-baseline-v1.json")) -or
        [string]$lifecycle.mappingGeneration -notmatch "^[1-9][0-9]*$" -or
        [string]$lifecycle.seed -notmatch "^[1-9][0-9]*$" -or
        [int]$lifecycle.actorCapacity -ne 8 -or
        @($lifecycle.expectations) -notcontains "start-replay-compares-all-immutable-fields") {
        throw "simulation-control lifecycle binding drifted"
    }

    $result = Read-JsonDocument -Path (Join-Path $Root "cases\result.json")
    Assert-Properties -Value $result -Required @("schemaVersion", "caseId", "category", "proposal", "expectedDispositions") -Allowed @("schemaVersion", "caseId", "category", "proposal", "expectedDispositions") -Context "result case"
    $proposalFields = @("resultId", "resultKind", "assignmentFingerprint", "simulationInstanceId", "tickStart", "tickEnd", "payloadDigest", "evidenceDigest", "proposalFingerprint")
    Assert-Properties -Value $result.proposal -Required $proposalFields -Allowed $proposalFields -Context "result proposal"
    $proposalMaterial = [string]$result.proposal.resultId + "|" +
        [string]$result.proposal.resultKind + "|" +
        [string]$result.proposal.assignmentFingerprint + "|" +
        [string]$result.proposal.simulationInstanceId + "|" +
        [string]$result.proposal.tickStart + "|" +
        [string]$result.proposal.tickEnd + "|" +
        [string]$result.proposal.payloadDigest + "|" +
        [string]$result.proposal.evidenceDigest
    if ([string]$result.proposal.proposalFingerprint -cne (Get-TextSha256 -Value $proposalMaterial)) {
        throw "simulation-control result proposal fingerprint drifted"
    }

    $golden = Read-JsonDocument -Path (Join-Path $Root "canonical-golden.json")
    Assert-Properties -Value $golden -Required @("schemaVersion", "documentKind", "frames") -Allowed @("schemaVersion", "documentKind", "frames") -Context "canonical golden"
    foreach ($entry in $golden.frames) {
        Assert-Properties -Value $entry -Required @("goldenId", "canonicalJson", "payloadBytes", "lengthPrefixHex") -Allowed @("goldenId", "canonicalJson", "payloadBytes", "lengthPrefixHex") -Context "canonical golden entry"
        $frame = ([string]$entry.canonicalJson | ConvertFrom-Json)
        Assert-ControlFrame -Frame $frame -KnownKinds $knownKinds -Context "canonical golden frame"
        $canonical = ConvertTo-CanonicalValue -Value $frame | ConvertTo-Json -Depth 32 -Compress
        if ($canonical -cne [string]$entry.canonicalJson) {
            throw "simulation-control golden JSON is not canonical: $($entry.goldenId)"
        }
        $byteCount = [System.Text.Encoding]::UTF8.GetByteCount([string]$entry.canonicalJson)
        $expectedPrefix = "{0:x8}" -f $byteCount
        if ([int]$entry.payloadBytes -ne $byteCount -or [string]$entry.lengthPrefixHex -cne $expectedPrefix) {
            throw "simulation-control golden length prefix drifted: $($entry.goldenId)"
        }
    }
    Assert-ForbiddenScope -Root $Root
}

# Invoke-IsolatedTests 在临时 corpus 上证明各类漂移稳定失败且 validator 不改写 source。
function Invoke-IsolatedTests {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot
    )

    $mutations = @(
        @{
            Name = "unknown-field"
            Apply = {
                param($Root)
                $path = Join-Path $Root "manifest.json"
                $raw = [System.IO.File]::ReadAllText($path)
                [System.IO.File]::WriteAllText($path, $raw.Replace('"documentKind": "manifest",', '"documentKind": "manifest","unexpected":true,'), [System.Text.UTF8Encoding]::new($false))
            }
        },
        @{
            Name = "duplicate-field"
            Apply = {
                param($Root)
                $path = Join-Path $Root "manifest.json"
                $raw = [System.IO.File]::ReadAllText($path)
                [System.IO.File]::WriteAllText($path, $raw.Replace('"schemaVersion": "simulation-control-v1",', '"schemaVersion":"simulation-control-v1","schemaVersion": "simulation-control-v1",'), [System.Text.UTF8Encoding]::new($false))
            }
        },
        @{
            Name = "digest-drift"
            Apply = {
                param($Root)
                $path = Join-Path $Root "cases\result.json"
                [System.IO.File]::AppendAllText($path, " `n", [System.Text.UTF8Encoding]::new($false))
            }
        },
        @{
            Name = "missing-case"
            Apply = {
                param($Root)
                Remove-Item -LiteralPath (Join-Path $Root "cases\hello.json") -Force
            }
        },
        @{
            Name = "noncanonical-decimal"
            Apply = {
                param($Root)
                $path = Join-Path $Root "canonical-golden.json"
                $raw = [System.IO.File]::ReadAllText($path)
                [System.IO.File]::WriteAllText($path, $raw.Replace('\"sequence\":\"3\"', '\"sequence\":\"03\"'), [System.Text.UTF8Encoding]::new($false))
                $manifestPath = Join-Path $Root "manifest.json"
                $manifestRaw = [System.IO.File]::ReadAllText($manifestPath)
                $oldHash = [regex]::Match($manifestRaw, '"path": "canonical-golden.json",\s*"documentKind": "canonical-golden",\s*"sha256": "([0-9a-f]{64})"').Groups[1].Value
                $newHash = Get-Sha256 -Path $path
                [System.IO.File]::WriteAllText($manifestPath, $manifestRaw.Replace($oldHash, $newHash), [System.Text.UTF8Encoding]::new($false))
            }
        },
        @{
            Name = "oversize-policy"
            Apply = {
                param($Root)
                $path = Join-Path $Root "message-inventory.json"
                $raw = [System.IO.File]::ReadAllText($path)
                [System.IO.File]::WriteAllText($path, $raw.Replace('"frameMaxBytes": 65536', '"frameMaxBytes": 65537'), [System.Text.UTF8Encoding]::new($false))
                $manifestPath = Join-Path $Root "manifest.json"
                $manifestRaw = [System.IO.File]::ReadAllText($manifestPath)
                $oldHash = [regex]::Match($manifestRaw, '"path": "message-inventory.json",\s*"documentKind": "message-inventory",\s*"sha256": "([0-9a-f]{64})"').Groups[1].Value
                $newHash = Get-Sha256 -Path $path
                [System.IO.File]::WriteAllText($manifestPath, $manifestRaw.Replace($oldHash, $newHash), [System.Text.UTF8Encoding]::new($false))
            }
        },
        @{
            Name = "forbidden-scope"
            Apply = {
                param($Root)
                $path = Join-Path $Root "cases\negative.json"
                $raw = [System.IO.File]::ReadAllText($path)
                [System.IO.File]::WriteAllText($path, $raw.Replace('"category": "negative",', '"category": "negative","transport":"grpc",'), [System.Text.UTF8Encoding]::new($false))
                $manifestPath = Join-Path $Root "manifest.json"
                $manifestRaw = [System.IO.File]::ReadAllText($manifestPath)
                $oldHash = [regex]::Match($manifestRaw, '"path": "cases/negative.json",\s*"category": "negative",\s*"sha256": "([0-9a-f]{64})"').Groups[1].Value
                $newHash = Get-Sha256 -Path $path
                [System.IO.File]::WriteAllText($manifestPath, $manifestRaw.Replace($oldHash, $newHash), [System.Text.UTF8Encoding]::new($false))
            }
        }
    )
    foreach ($mutation in $mutations) {
        $temporary = Join-Path ([System.IO.Path]::GetTempPath()) ("ihomeland-simulation-control-" + [guid]::NewGuid().ToString("N"))
        try {
            Copy-Item -LiteralPath $SourceRoot -Destination $temporary -Recurse
            & $mutation.Apply $temporary
            $failed = $false
            try {
                Invoke-ControlValidation -Root $temporary -RepositoryRoot $RepositoryRoot
            }
            catch {
                $failed = $true
            }
            if (-not $failed) {
                throw "simulation-control mutation unexpectedly passed: $($mutation.Name)"
            }
        }
        finally {
            if (Test-Path -LiteralPath $temporary) {
                Remove-Item -LiteralPath $temporary -Recurse -Force
            }
        }
    }
    $before = Get-ChildItem -LiteralPath $SourceRoot -Recurse -File | Sort-Object FullName | ForEach-Object {
        "$($_.FullName)=$(Get-Sha256 -Path $_.FullName)"
    }
    Invoke-ControlValidation -Root $SourceRoot -RepositoryRoot $RepositoryRoot
    $after = Get-ChildItem -LiteralPath $SourceRoot -Recurse -File | Sort-Object FullName | ForEach-Object {
        "$($_.FullName)=$(Get-Sha256 -Path $_.FullName)"
    }
    if (($before -join "`n") -cne ($after -join "`n")) {
        throw "simulation-control validator modified source corpus"
    }
}

# Invoke-CheckedCommand 执行外部门禁并把非零退出统一转为 verify failure。
function Invoke-CheckedCommand {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Command,
        [Parameter(Mandatory = $true)][string]$Failure
    )

    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw $Failure
    }
}

# Get-CorpusSha256 对稳定相对路径和内容摘要再次哈希，供只读与报告绑定。
function Get-CorpusSha256 {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string[]]$Roots
    )

    $entries = New-Object System.Collections.Generic.List[string]
    foreach ($root in $Roots) {
        $fullRoot = Join-Path $RepositoryRoot $root
        if (-not (Test-Path -LiteralPath $fullRoot)) {
            throw "simulation-control corpus root is missing: $root"
        }
        foreach ($file in Get-ChildItem -LiteralPath $fullRoot -Recurse -File | Sort-Object FullName) {
            $relative = $file.FullName.Substring($RepositoryRoot.Length + 1).Replace("\", "/")
            $entries.Add($relative + "=" + (Get-Sha256 -Path $file.FullName))
        }
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes(($entries -join "`n"))
    $hasher = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString($hasher.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $hasher.Dispose()
    }
}

# Get-TrackedTreeSha256 与 client qualification 使用同一 tracked/untracked path:hash 规则。
function Get-TrackedTreeSha256 {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string[]]$PathSpecs
    )

    $tracked = & git -C $RepositoryRoot ls-files -- @PathSpecs
    if ($LASTEXITCODE -ne 0) { throw "simulation-control tracked evidence input cannot be enumerated" }
    $untracked = & git -C $RepositoryRoot ls-files --others --exclude-standard -- @PathSpecs
    if ($LASTEXITCODE -ne 0) { throw "simulation-control untracked evidence input cannot be enumerated" }
    $paths = @($tracked) + @($untracked)
    if (@($paths).Count -eq 0) { throw "simulation-control tracked evidence input is empty" }
    $builder = [System.Text.StringBuilder]::new()
    foreach ($relative in @($paths | Sort-Object -Unique)) {
        $full = Join-Path $RepositoryRoot $relative
        [void]$builder.Append($relative.Replace('\', '/')).Append(':').Append((Get-Sha256 -Path $full)).Append("`n")
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

# Get-DirectorySha256 绑定完整 Player build 目录的稳定相对路径与内容。
function Get-DirectorySha256 {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $root = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\') + '\'
    $builder = [System.Text.StringBuilder]::new()
    foreach ($file in @(Get-ChildItem -LiteralPath $Directory -File -Recurse | Sort-Object FullName)) {
        $relative = $file.FullName.Substring($root.Length).Replace('\', '/')
        [void]$builder.Append($relative).Append(':').Append((Get-Sha256 -Path $file.FullName)).Append("`n")
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

# Assert-V1QualificationEvidence 验证当前 server/client v1 最终报告和其绑定输入，而非接受历史绿色结论。
function Assert-V1QualificationEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$ServerReportPath,
        [Parameter(Mandatory = $true)][string]$ClientReportPath
    )

    if ([string]::IsNullOrWhiteSpace($ServerReportPath) -or [string]::IsNullOrWhiteSpace($ClientReportPath)) {
        throw "simulation-control verify requires current server and client qualification reports"
    }
    $serverRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot ".local\qualification")).TrimEnd('\') + '\'
    $clientRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot ".local\client-qualification")).TrimEnd('\') + '\'
    $serverPath = (Resolve-Path -LiteralPath $ServerReportPath).Path
    $clientPath = (Resolve-Path -LiteralPath $ClientReportPath).Path
    if (-not $serverPath.StartsWith($serverRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
        -not $clientPath.StartsWith($clientRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "simulation-control qualification report escaped its owned evidence root"
    }
    $server = Get-Content -LiteralPath $serverPath -Raw -Encoding utf8 | ConvertFrom-Json
    $freeze = Get-Content -LiteralPath (Join-Path $RepositoryRoot "shared\contracts\fixtures\qualification\freeze.json") -Raw -Encoding utf8 | ConvertFrom-Json
    if ([string]$server.qualificationVersion -ne "server-v1" -or
        -not [bool]$server.qualified -or [string]$server.cleanup -ne "pass" -or
        $null -ne $server.failure -or [string]$server.contractDigest -ne [string]$freeze.digest -or
        @($server.gates | Where-Object { $_.outcome -ne "pass" }).Count -ne 0 -or
        @($server.scenarios | Where-Object { [bool]$_.mandatory -and $_.outcome -ne "pass" }).Count -ne 0) {
        throw "simulation-control server v1 qualification evidence is incomplete or stale"
    }

    $client = Get-Content -LiteralPath $clientPath -Raw -Encoding utf8 | ConvertFrom-Json
    $clientRun = Split-Path -Parent $clientPath
    $development = Join-Path $clientRun "development"
    $release = Join-Path $clientRun "release"
    $currentContract = Get-TrackedTreeSha256 -RepositoryRoot $RepositoryRoot -PathSpecs @("shared/contracts", "client/Packages", "client/ProjectSettings/ProjectVersion.txt")
    if ([string]$client.qualificationVersion -ne "client-v1" -or
        -not [bool]$client.qualified -or [string]$client.cleanup -ne "pass" -or
        $null -ne $client.failure -or [string]$client.contractDigest -ne $currentContract -or
        [string]$client.buildDigests.development -ne (Get-DirectorySha256 -Directory $development) -or
        [string]$client.buildDigests.release -ne (Get-DirectorySha256 -Directory $release) -or
        @($client.stages | Where-Object { $_.outcome -ne "pass" }).Count -ne 0) {
        throw "simulation-control client v1 qualification evidence is incomplete or stale"
    }
    return [ordered]@{
        serverReportSha256 = Get-Sha256 -Path $serverPath
        serverContractSha256 = [string]$server.contractDigest
        clientReportSha256 = Get-Sha256 -Path $clientPath
        clientContractSha256 = [string]$client.contractDigest
        clientDevelopmentBuildSha256 = [string]$client.buildDigests.development
        clientReleaseBuildSha256 = [string]$client.buildDigests.release
    }
}

# Enable-GoRaceCompiler 使用已安装的 MSYS2 UCRT64 gcc，不静默关闭 CGO/race。
function Enable-GoRaceCompiler {
    $candidates = @(
        "C:\msys64\ucrt64\bin",
        "C:\msys64\mingw64\bin"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath (Join-Path $candidate "gcc.exe")) {
            $env:PATH = $candidate + ";" + $env:PATH
            $env:CC = "gcc"
            $env:CGO_ENABLED = "1"
            return
        }
    }
    throw "simulation-control race gate requires MSYS2 gcc"
}

# Invoke-GoVerification 聚合格式、unit/fuzz/race 与真实 child。
function Invoke-GoVerification {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][int]$FuzzSeconds
    )

    $serverRoot = Join-Path $RepositoryRoot "server"
    $goFiles = @(Get-ChildItem -LiteralPath $serverRoot -Recurse -File -Filter "*.go" |
        Where-Object { $_.FullName -notmatch "[\\/]internal[\\/]generated[\\/]" })
    $unformatted = @(& gofmt -l ($goFiles | ForEach-Object { $_.FullName }))
    if ($LASTEXITCODE -ne 0 -or $unformatted.Count -ne 0) {
        throw "simulation-control Go format gate failed"
    }
    $goTool = Join-Path $RepositoryRoot "tools\go\go.ps1"
    Push-Location $serverRoot
    try {
        Invoke-CheckedCommand -Failure "simulation-control Go unit gate failed" -Command {
            & $goTool test -count=1 ./...
        }
        Invoke-CheckedCommand -Failure "simulation-control Go fuzz gate failed" -Command {
            & $goTool test ./internal/simulationcontrol -run "^$" -fuzz "FuzzDecodeFrame" -fuzztime ($FuzzSeconds.ToString() + "s")
        }
        $previousPath = $env:PATH
        $previousCC = $env:CC
        $previousCGO = $env:CGO_ENABLED
        try {
            Enable-GoRaceCompiler
            Invoke-CheckedCommand -Failure "simulation-control Go race gate failed" -Command {
                & $goTool test -race -count=1 ./internal/simulationcontrol/... ./internal/placement ./internal/app
            }
        }
        finally {
            $env:PATH = $previousPath
            $env:CC = $previousCC
            $env:CGO_ENABLED = $previousCGO
        }
        $previousRealChild = $env:IHOMELAND_SIMULATION_REAL_CHILD
        try {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = "1"
            Invoke-CheckedCommand -Failure "simulation-control real child gate failed" -Command {
                & $goTool test -count=1 ./internal/simulationcontrol/process -run "^TestRealChild"
            }
        }
        finally {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = $previousRealChild
        }
        $buildRoot = Join-Path $RepositoryRoot ".local\simulation-control\go-build"
        New-Item -ItemType Directory -Path $buildRoot -Force | Out-Null
        $firstBinary = Join-Path $buildRoot "ihomeland-server-1.exe"
        $secondBinary = Join-Path $buildRoot "ihomeland-server-2.exe"
        Invoke-CheckedCommand -Failure "simulation-control first Go server build failed" -Command {
            & $goTool -GoArguments @("build", "-trimpath", "-buildvcs=false", "-o", $firstBinary, "./cmd/server")
        }
        Invoke-CheckedCommand -Failure "simulation-control second Go server build failed" -Command {
            & $goTool -GoArguments @("build", "-trimpath", "-buildvcs=false", "-o", $secondBinary, "./cmd/server")
        }
        if ((Get-Sha256 -Path $firstBinary) -ne (Get-Sha256 -Path $secondBinary)) {
            throw "simulation-control Go server build is not deterministic"
        }
    }
    finally {
        Pop-Location
    }
}

# Invoke-CppVerification 重建 B0.3 Release/ASan evidence，并构建 real-child Debug binary。
function Invoke-CppVerification {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $cppTool = Join-Path $RepositoryRoot "tools\cpp\cpp.ps1"
    Invoke-CheckedCommand -Failure "simulation-control C++ Release/ASan gate failed" -Command {
        & $cppTool verify -Preset windows-msvc-release
    }
    Invoke-CheckedCommand -Failure "simulation-control C++ Debug configure gate failed" -Command {
        & $cppTool configure -Preset windows-msvc-debug
    }
    Invoke-CheckedCommand -Failure "simulation-control C++ Debug build gate failed" -Command {
        & $cppTool build -Preset windows-msvc-debug
    }
    Invoke-CheckedCommand -Failure "simulation-control C++ hygiene gate failed" -Command {
        & $cppTool hygiene
    }
}

# Assert-DeliveryGovernance 验证 strict specs、diff、docs 与 generated/cache/secret 边界。
function Assert-DeliveryGovernance {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    Push-Location $RepositoryRoot
    try {
        Invoke-CheckedCommand -Failure "simulation-control OpenSpec strict gate failed" -Command {
            & openspec.cmd validate establish-go-simulation-control --strict
        }
        Invoke-CheckedCommand -Failure "simulation-control repository diff gate failed" -Command {
            & git diff --check
        }
        $trackedForbidden = @(& git ls-files -- "server/internal/generated" "client/Generated" ".local" "simulation/out")
        if ($LASTEXITCODE -ne 0 -or $trackedForbidden.Count -ne 0) {
            throw "simulation-control tracked generated/cache gate failed"
        }
        foreach ($path in @(
            "docs/architecture.md",
            "docs/gameplay-simulation-architecture.md",
            "docs/network-transport-architecture.md",
            "docs/file-structure.md",
            "docs/technology-versions.md",
            "docs/network-port-allocation.md",
            "docs/roadmap.md",
            "server/README.md",
            "simulation/README.md"
        )) {
            if (-not (Test-Path -LiteralPath (Join-Path $RepositoryRoot $path) -PathType Leaf)) {
                throw "simulation-control documentation gate is missing $path"
            }
        }
        $scope = @(& git grep -n -I -E 'BEGIN (RSA |EC )?PRIVATE KEY|wad1_[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9]{20,}' -- "server/config/simulation-control.example.yaml" "shared/contracts/fixtures/simulation-control")
        if ($LASTEXITCODE -eq 0 -and $scope.Count -ne 0) {
            throw "simulation-control high-confidence secret gate failed"
        }
        if ($LASTEXITCODE -ne 0 -and $LASTEXITCODE -ne 1) {
            throw "simulation-control secret scan failed"
        }
    }
    finally {
        Pop-Location
    }
}

# Write-QualificationReports 连续生成两个无时间、路径、identity 实例或 payload 的同源报告。
function Write-QualificationReports {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$SourceDigest,
        [Parameter(Mandatory = $true)]$V1Evidence
    )

    $reportRoot = Join-Path $RepositoryRoot ".local\simulation-control\qualification"
    New-Item -ItemType Directory -Path $reportRoot -Force | Out-Null
    $ciIdentityPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json"
    $asanIdentityPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-asan\ihomeland-build-identity.json"
    $receiptPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\qualification-gate-receipt.json"
    $goServerBinaryPath = Join-Path $RepositoryRoot ".local\simulation-control\go-build\ihomeland-server-1.exe"
    $body = [ordered]@{
        schemaVersion = 1
        qualificationVersion = "go-simulation-control-b0.4-v1"
        qualified = $true
        conclusion = "control-qualified-windows-x64"
        sourceCorpusSha256 = $SourceDigest
        controlManifestSha256 = Get-Sha256 -Path (Join-Path $RepositoryRoot "shared\contracts\fixtures\simulation-control\manifest.json")
        modelManifestSha256 = Get-Sha256 -Path (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\model\manifest.json")
        profileManifestSha256 = Get-Sha256 -Path (Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\network-profile\manifest.json")
        migrationSha256 = Get-Sha256 -Path (Join-Path $RepositoryRoot "server\internal\storage\mysql\migrations\000007_create_simulation_result_receipts.sql")
        goServerBinarySha256 = Get-Sha256 -Path $goServerBinaryPath
        ciBuildIdentitySha256 = Get-Sha256 -Path $ciIdentityPath
        asanBuildIdentitySha256 = Get-Sha256 -Path $asanIdentityPath
        qualificationReceiptSha256 = Get-Sha256 -Path $receiptPath
        serverQualificationReportSha256 = [string]$V1Evidence.serverReportSha256
        serverContractSha256 = [string]$V1Evidence.serverContractSha256
        clientQualificationReportSha256 = [string]$V1Evidence.clientReportSha256
        clientContractSha256 = [string]$V1Evidence.clientContractSha256
        clientDevelopmentBuildSha256 = [string]$V1Evidence.clientDevelopmentBuildSha256
        clientReleaseBuildSha256 = [string]$V1Evidence.clientReleaseBuildSha256
        gates = @(
            "schema-parity",
            "go-format-unit-fuzz-race-build",
            "mysql-integration",
            "cpp-release-asan-b03",
            "real-child-no-port",
            "server-v1-qualified",
            "client-v1-qualified",
            "scope-docs-governance"
        )
        excluded = @("linux", "udp-kcp", "battle-wire")
    }
    $canonical = ($body | ConvertTo-Json -Depth 8 -Compress) + "`n"
    $first = Join-Path $reportRoot "report-1.json"
    $second = Join-Path $reportRoot "report-2.json"
    [System.IO.File]::WriteAllText($first, $canonical, [System.Text.UTF8Encoding]::new($false))
    [System.IO.File]::WriteAllText($second, $canonical, [System.Text.UTF8Encoding]::new($false))
    if ((Get-Sha256 -Path $first) -ne (Get-Sha256 -Path $second)) {
        throw "simulation-control consecutive qualification reports drifted"
    }
    Write-Output "simulation-control qualification reports are stable and qualified"
}

$repositoryRoot = Get-RepositoryRoot
if ([string]::IsNullOrWhiteSpace($FixtureRoot)) {
    $FixtureRoot = Join-Path $repositoryRoot "shared\contracts\fixtures\simulation-control"
}
$resolvedFixtureRoot = (Resolve-Path -LiteralPath $FixtureRoot).Path

switch ($Action) {
    "validate" {
        Invoke-ControlValidation -Root $resolvedFixtureRoot -RepositoryRoot $repositoryRoot
        Write-Output "simulation-control validate passed"
    }
    "test" {
        Invoke-IsolatedTests -SourceRoot $resolvedFixtureRoot -RepositoryRoot $repositoryRoot
        Write-Output "simulation-control isolated tests passed"
    }
    "verify" {
        $sourceRoots = @(
            "shared\contracts\fixtures\simulation-control",
            "server\internal\simulationcontrol",
            "server\internal\storage\simulationresult",
            "simulation\include\ihomeland\sim\control",
            "simulation\src\control"
        )
        $sourceBefore = Get-CorpusSha256 -RepositoryRoot $repositoryRoot -Roots $sourceRoots
        Invoke-ControlValidation -Root $resolvedFixtureRoot -RepositoryRoot $repositoryRoot
        Invoke-IsolatedTests -SourceRoot $resolvedFixtureRoot -RepositoryRoot $repositoryRoot
        Invoke-CppVerification -RepositoryRoot $repositoryRoot
        Invoke-GoVerification -RepositoryRoot $repositoryRoot -FuzzSeconds $FuzzTimeSeconds
        $previousStorageRealChild = $env:IHOMELAND_SIMULATION_REAL_CHILD
        try {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = "1"
            Invoke-CheckedCommand -Failure "simulation-control storage integration gate failed" -Command {
                & (Join-Path $repositoryRoot "tools\storage\storage.ps1") -Action verify -TimeoutSeconds $StorageTimeoutSeconds
            }
        }
        finally {
            $env:IHOMELAND_SIMULATION_REAL_CHILD = $previousStorageRealChild
        }
        Assert-DeliveryGovernance -RepositoryRoot $repositoryRoot
        $v1Evidence = Assert-V1QualificationEvidence -RepositoryRoot $repositoryRoot -ServerReportPath $ServerQualificationReportPath -ClientReportPath $ClientQualificationReportPath
        $sourceAfter = Get-CorpusSha256 -RepositoryRoot $repositoryRoot -Roots $sourceRoots
        if ($sourceBefore -ne $sourceAfter) {
            throw "simulation-control verification modified source corpus"
        }
        Write-QualificationReports -RepositoryRoot $repositoryRoot -SourceDigest $sourceAfter -V1Evidence $v1Evidence
        Write-Output "simulation-control verify passed"
    }
}
