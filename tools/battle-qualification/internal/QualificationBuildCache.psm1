Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Assert-QualificationBuildCachePath 验证递归清理和移动只能发生在专用 ignored cache 根内。
function Assert-QualificationBuildCachePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$CacheRoot
    )

    $root = [IO.Path]::GetFullPath($CacheRoot).TrimEnd('\') + '\'
    $candidate = [IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
        throw "qualification build cache path escaped cache root"
    }
}

# Write-QualificationBuildCacheReceipt 只在全部 binary 存在后写入 identity 与内容摘要。
function Write-QualificationBuildCacheReceipt {
    param(
        [Parameter(Mandatory = $true)][string]$CacheDirectory,
        [Parameter(Mandatory = $true)][string]$CacheKey,
        [Parameter(Mandatory = $true)][string]$SourceSha256,
        [Parameter(Mandatory = $true)][string[]]$BinaryNames
    )

    $binaries = [ordered]@{}
    foreach ($name in @($BinaryNames | Sort-Object)) {
        if ($name -notmatch '^[a-z][a-z0-9-]*\.exe$') {
            throw "qualification build cache binary name invalid"
        }
        $path = Join-Path $CacheDirectory $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "qualification build cache binary missing"
        }
        $binaries[$name] = (
            Get-FileHash -LiteralPath $path -Algorithm SHA256
        ).Hash.ToLowerInvariant()
    }
    $receipt = [ordered]@{
        schemaVersion = 1
        cacheKey = $CacheKey
        sourceSha256 = $SourceSha256
        binaries = $binaries
    }
    [IO.File]::WriteAllText(
        (Join-Path $CacheDirectory "receipt.json"),
        ($receipt | ConvertTo-Json -Depth 6) + "`n",
        [Text.UTF8Encoding]::new($false)
    )
}

# Test-QualificationBuildCacheReceipt 验证 cache key、source 与全部 binary digest 完全匹配。
function Test-QualificationBuildCacheReceipt {
    param(
        [Parameter(Mandatory = $true)][string]$CacheDirectory,
        [Parameter(Mandatory = $true)][string]$CacheKey,
        [Parameter(Mandatory = $true)][string]$SourceSha256,
        [Parameter(Mandatory = $true)][string[]]$BinaryNames
    )

    $receiptPath = Join-Path $CacheDirectory "receipt.json"
    if (-not (Test-Path -LiteralPath $receiptPath -PathType Leaf)) {
        return $false
    }
    try {
        $receipt = Get-Content -LiteralPath $receiptPath -Raw -Encoding utf8 |
            ConvertFrom-Json
        if (
            [int]$receipt.schemaVersion -ne 1 -or
            [string]$receipt.cacheKey -cne $CacheKey -or
            [string]$receipt.sourceSha256 -cne $SourceSha256
        ) {
            return $false
        }
        $receiptNames = @($receipt.binaries.PSObject.Properties.Name | Sort-Object)
        $expectedNames = @($BinaryNames | Sort-Object)
        if (($receiptNames -join "`n") -cne ($expectedNames -join "`n")) {
            return $false
        }
        foreach ($name in $expectedNames) {
            $path = Join-Path $CacheDirectory $name
            if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
                return $false
            }
            $expectedHash = [string]$receipt.binaries.$name
            $actualHash = (
                Get-FileHash -LiteralPath $path -Algorithm SHA256
            ).Hash.ToLowerInvariant()
            if ($actualHash -cne $expectedHash) {
                return $false
            }
        }
        return $true
    }
    catch {
        return $false
    }
}

# New-QualificationBuildCacheLinks 为新 run 建立 binary hard links，不复制 credential 或 evidence。
function New-QualificationBuildCacheLinks {
    param(
        [Parameter(Mandatory = $true)][string]$CacheDirectory,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string[]]$BinaryNames
    )

    foreach ($name in $BinaryNames) {
        $source = Join-Path $CacheDirectory $name
        $destination = Join-Path $RunDirectory $name
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "qualification build cache binary missing before materialization"
        }
        if (Test-Path -LiteralPath $destination) {
            throw "qualification run binary already exists"
        }
        [void](New-Item -ItemType HardLink -Path $destination -Target $source)
    }
}

Export-ModuleMember -Function @(
    "Assert-QualificationBuildCachePath",
    "Write-QualificationBuildCacheReceipt",
    "Test-QualificationBuildCacheReceipt",
    "New-QualificationBuildCacheLinks"
)
