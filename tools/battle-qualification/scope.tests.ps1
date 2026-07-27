[CmdletBinding()]
param(
    [string]$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Assert-NoMatches 将 inventory 违规统一收敛为不含本机路径的稳定失败。
function Assert-NoMatches {
    param(
        [Parameter(Mandatory = $true)][string[]]$Values,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][string]$Category
    )

    if (@($Values | Where-Object { $_ -match $Pattern }).Count -ne 0) {
        throw "battle qualification scope gate failed: $Category"
    }
}

$tracked = @(& git -C $RepositoryRoot ls-files --cached --others --exclude-standard)
if ($LASTEXITCODE -ne 0 -or $tracked.Count -eq 0) {
    throw "battle qualification scope inventory failed"
}

Assert-NoMatches -Values $tracked `
    -Pattern '^(?:\.local/|simulation/out/|client/(?:Library|Temp|Logs|UserSettings)/)' `
    -Category "tracked-cache-or-run-output"
Assert-NoMatches -Values $tracked `
    -Pattern '(?i)(?:^|/)(?:BattleNetworkClient|BattlePrediction|BattleReconciliation|BattleActorView|BattleHud)\.(?:cs|uxml|uss)$' `
    -Category "premature-unity-runtime"

$evidenceRoot = Join-Path $RepositoryRoot "shared\contracts\evidence\battle-network"
if (Test-Path -LiteralPath $evidenceRoot -PathType Container) {
    $evidenceFiles = @(Get-ChildItem -LiteralPath $evidenceRoot -File -Recurse)
    foreach ($file in $evidenceFiles) {
        if ($file.Length -gt 16MB) {
            throw "battle qualification scope gate failed: oversized-evidence"
        }
        $text = Get-Content -LiteralPath $file.FullName -Raw -Encoding utf8
        if ($text -match '(?i)(?:btk1_|bts1_|BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY|ticketSecret|trafficKey|cookieSecret|playerId|remoteEndpoint|payloadDump|[A-Z]:\\|(?:\d{1,3}\.){3}\d{1,3})') {
            throw "battle qualification scope gate failed: sensitive-evidence"
        }
    }
}

$qualificationSources = @(
    $tracked | Where-Object {
        $_ -match '^(?:server/(?:cmd/battlequalificationtool|internal/battlequalification)/|simulation/(?:apps/battle_protocol_client|include/ihomeland/qualification|src/qualification)/|tools/battle-qualification/)' -and
        $_ -cne 'tools/battle-qualification/scope.tests.ps1'
    }
)
foreach ($relativePath in $qualificationSources) {
    $path = Join-Path $RepositoryRoot $relativePath.Replace("/", "\")
    $text = Get-Content -LiteralPath $path -Raw -Encoding utf8
    if ($text -match '(?i)(?:service\s+locator|global\s+event\s+bus|disable\s+tls|insecureSkipVerify\s*:\s*true)') {
        throw "battle qualification scope gate failed: forbidden-shortcut"
    }
}

$protocolClientSource = Get-Content -LiteralPath (
    Join-Path $RepositoryRoot "simulation\apps\battle_protocol_client\main.cpp"
) -Raw -Encoding utf8
if ($protocolClientSource -match '(?m)\b(?:bind|listen)\s*\(') {
    throw "battle qualification scope gate failed: protocol-client-listener"
}
$commandSource = (
    Get-ChildItem -LiteralPath (
        Join-Path $RepositoryRoot "server\cmd\battlequalificationtool"
    ) -Filter "*.go" -File |
    Get-Content -Raw
) -join "`n"
if ($commandSource -match '\bnet\.Listen(?:TCP|UDP|Packet)?\s*\(') {
    throw "battle qualification scope gate failed: second-listener"
}

Write-Output "battle qualification scope/secret/cache tests passed"
