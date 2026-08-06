# 该回归验证 source manifest 的闭合 shape 与关键进入条件会 fail closed。
[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ToolPath = Join-Path $PSScriptRoot "client-battle-runtime.ps1"
$ScopeToolPath = Join-Path $RepositoryRoot (
    "tools\battle-qualification\scope.tests.ps1")
$SourceManifestPath = Join-Path $RepositoryRoot (
    "shared\contracts\fixtures\client-battle-runtime\source-manifest.json")
$TestRoot = Join-Path $RepositoryRoot ".local\client-battle-runtime-tests"

# Assert-True 为入口回归提供稳定失败。
function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw $Message
    }
}

# Invoke-ExpectedFailure 确认 mutation 被入口拒绝且不会被普通 warning 吞掉。
function Invoke-ExpectedFailure {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ManifestPath,
        [Parameter(Mandatory = $true)]
        [string]$ExpectedFragment
    )

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & powershell.exe -NoProfile -ExecutionPolicy Bypass `
            -File $ToolPath `
            -Action validate `
            -ManifestPath $ManifestPath 2>&1 | Out-String
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    if ($exitCode -eq 0 -or
        $output -notmatch [regex]::Escape($ExpectedFragment)) {
        throw "entry mutation was not rejected: $ExpectedFragment`n$output"
    }
}

# Invoke-ScopeExpectedFailure 确认 B0.6 范围门只接受闭合的 active B0.7 授权。
function Invoke-ScopeExpectedFailure {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ManifestPath
    )

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & pwsh.exe -NoProfile -File $ScopeToolPath `
            -RepositoryRoot $RepositoryRoot `
            -RuntimeManifestPath $ManifestPath 2>&1 | Out-String
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
    if ($exitCode -eq 0 -or
        $output -notmatch "premature-unity-runtime") {
        throw "scope authorization mutation was not rejected`n$output"
    }
}

[System.IO.Directory]::CreateDirectory($TestRoot) | Out-Null
try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $ToolPath `
        -Action validate | Out-Null
    Assert-True ($LASTEXITCODE -eq 0) "current client battle runtime entry failed"

    $toolSource = Get-Content -LiteralPath $ToolPath -Raw -Encoding UTF8
    $localHarnessPath = Join-Path $RepositoryRoot (
        "tools\client-qualification\client-qualification-local.ps1")
    $localHarnessSource = Get-Content `
        -LiteralPath $localHarnessPath `
        -Raw `
        -Encoding UTF8
    foreach ($token in @(
            '"player-targeted"',
            'Assert-PackagedNativePlugin',
            'Assert-ReleaseDiagnosticSurfaceRemoved',
            'ConvertTo-BinarySearchTexts',
            'AllowEmptyCollection()',
            'Assert-NoTrackedGeneratedArtifacts',
            '-Action battle',
            'client-battle-runtime-player',
            'PersonalWorldSceneContextPlayModeTests')) {
        Assert-True (
            $toolSource.Contains($token)
        ) "Player targeted gate is missing: $token"
    }
    foreach ($token in @(
            '[ValidateSet("soak", "operator", "battle")]',
            'Set-IsolatedListenerPorts',
            'Resolve-SimulationChild',
            'ParentProcessId = $ParentProcessId',
            '-ihomelandQualificationHttpBaseUri',
            'Invoke-ClientBattleRuntimeOperator',
            'client-battle-runtime-pass.signal',
            'Assert-NoRunSecretArtifacts',
            'Assert-LocalCleanup')) {
        Assert-True (
            $localHarnessSource.Contains($token)
        ) "real Player harness is missing: $token"
    }
    Assert-True (
        -not $localHarnessSource.Contains("taskkill") -and
        -not $localHarnessSource.Contains("Get-Process -Name")
    ) "real Player harness contains a broad process cleanup"

    $tokens = $null
    $parseErrors = $null
    $toolAst = [System.Management.Automation.Language.Parser]::ParseFile(
        $ToolPath,
        [ref]$tokens,
        [ref]$parseErrors)
    Assert-True ($parseErrors.Count -eq 0) (
        "client battle runtime tool cannot be parsed")
    $binarySearchFunction = @(
        $toolAst.FindAll(
            {
                param($node)
                $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
                $node.Name -ceq "ConvertTo-BinarySearchTexts"
            },
            $true)
    )
    Assert-True ($binarySearchFunction.Count -eq 1) (
        "binary search function must have one owner")
    . ([scriptblock]::Create($binarySearchFunction[0].Extent.Text))

    $qualificationMarker = "client-battle-runtime"
    $unicodeMarker = [Text.Encoding]::Unicode.GetBytes($qualificationMarker)
    $oddAlignedBytes = [byte[]]::new($unicodeMarker.Length + 1)
    $oddAlignedBytes[0] = 0x7f
    [Array]::Copy(
        $unicodeMarker,
        0,
        $oddAlignedBytes,
        1,
        $unicodeMarker.Length)
    $oddAlignedTexts = ConvertTo-BinarySearchTexts $oddAlignedBytes
    Assert-True (
        @($oddAlignedTexts | Where-Object {
                $_.IndexOf(
                    $qualificationMarker,
                    [StringComparison]::Ordinal) -ge 0
            }).Count -ne 0
    ) "odd-aligned UTF-16 qualification marker was missed"

    $source = Get-Content -LiteralPath $SourceManifestPath -Raw -Encoding UTF8 |
        ConvertFrom-Json

    $oldProfile = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    $oldProfile.entryPolicy.profileVersion = "battle-network-profile-v1"
    $oldProfilePath = Join-Path $TestRoot "old-profile.json"
    [System.IO.File]::WriteAllText(
        $oldProfilePath,
        (($oldProfile | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $oldProfilePath "old or mismatched battle network profile"
    Invoke-ScopeExpectedFailure $oldProfilePath
    Invoke-ScopeExpectedFailure (Join-Path $TestRoot "missing-manifest.json")

    $staleExpiry = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    @($staleExpiry.entryPolicy.routes | Where-Object {
            [int]$_.messageId -in @(3006, 3007)
        }) | ForEach-Object { $_.expiryMs = 500 }
    $staleExpiryPath = Join-Path $TestRoot "stale-expiry.json"
    [System.IO.File]::WriteAllText(
        $staleExpiryPath,
        (($staleExpiry | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $staleExpiryPath "battle route policy drifted: 3006"

    $wrongGapExpiry = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    $wrongGapExpiry.entryPolicy.inputGapExpirySimulationTicks = 5
    $wrongGapExpiryPath = Join-Path $TestRoot "wrong-gap-expiry.json"
    [System.IO.File]::WriteAllText(
        $wrongGapExpiryPath,
        (($wrongGapExpiry | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $wrongGapExpiryPath (
        "battle profile parameter drifted: input-gap-expiry-ticks")

    $missingAck = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    $missingAck.entryPolicy.snapshotAcknowledgement.requiredMessages = @(
        "BattleFullSnapshot")
    $missingAckPath = Join-Path $TestRoot "missing-ack.json"
    [System.IO.File]::WriteAllText(
        $missingAckPath,
        (($missingAck | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $missingAckPath (
        "snapshot acknowledgement messages are not closed")

    $wrongLane = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    @($wrongLane.entryPolicy.routes | Where-Object {
            [int]$_.messageId -eq 3006
        })[0].lane = "RAW"
    $wrongLanePath = Join-Path $TestRoot "wrong-lane.json"
    [System.IO.File]::WriteAllText(
        $wrongLanePath,
        (($wrongLane | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $wrongLanePath "battle route policy drifted: 3006"

    $notReady = $source | ConvertTo-Json -Depth 12 | ConvertFrom-Json
    $notReady.entryPolicy.developmentReadiness.qualityCheckId =
        "final-product-qualification"
    $notReadyPath = Join-Path $TestRoot "not-ready.json"
    [System.IO.File]::WriteAllText(
        $notReadyPath,
        (($notReady | ConvertTo-Json -Depth 12) + "`n"),
        [System.Text.UTF8Encoding]::new($false))
    Invoke-ExpectedFailure $notReadyPath "development readiness identity drifted"

    Write-Host "[OK] Client battle runtime entry regression passed."
}
finally {
    if (Test-Path -LiteralPath $TestRoot -PathType Container) {
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
}
