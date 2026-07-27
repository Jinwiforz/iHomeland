#requires -Version 7.0

[CmdletBinding()]
# 该测试只使用小型假 binary 验证 receipt、漂移拒绝和 hard-link run 隔离。
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$ModulePath = Join-Path $PSScriptRoot "internal\QualificationBuildCache.psm1"
Import-Module $ModulePath -Force

$temporaryRoot = Join-Path $RepositoryRoot (
    ".tmp\qualification-build-cache-tests-" + [guid]::NewGuid().ToString("N")
)
$cacheRoot = Join-Path $temporaryRoot "cache"
$cacheDirectory = Join-Path $cacheRoot ("a" * 64)
$runDirectory = Join-Path $temporaryRoot "run"
$binaryNames = @(
    "server.exe",
    "qualificationtool.exe",
    "battlequalificationtool.exe"
)
[void](New-Item -ItemType Directory -Path $cacheDirectory -Force)
[void](New-Item -ItemType Directory -Path $runDirectory -Force)
try {
    Assert-QualificationBuildCachePath -Path $cacheDirectory -CacheRoot $cacheRoot
    foreach ($name in $binaryNames) {
        [IO.File]::WriteAllText(
            (Join-Path $cacheDirectory $name),
            "fake-$name",
            [Text.UTF8Encoding]::new($false)
        )
    }
    $key = "a" * 64
    $source = "b" * 64
    Write-QualificationBuildCacheReceipt `
        -CacheDirectory $cacheDirectory `
        -CacheKey $key `
        -SourceSha256 $source `
        -BinaryNames $binaryNames
    if (-not (Test-QualificationBuildCacheReceipt `
        -CacheDirectory $cacheDirectory `
        -CacheKey $key `
        -SourceSha256 $source `
        -BinaryNames $binaryNames)) {
        throw "valid qualification build cache receipt was rejected"
    }
    New-QualificationBuildCacheLinks `
        -CacheDirectory $cacheDirectory `
        -RunDirectory $runDirectory `
        -BinaryNames $binaryNames
    foreach ($name in $binaryNames) {
        $runPath = Join-Path $runDirectory $name
        if (-not (Test-Path -LiteralPath $runPath -PathType Leaf)) {
            throw "qualification build cache hard link missing"
        }
        if (
            (Get-FileHash -LiteralPath $runPath -Algorithm SHA256).Hash -cne
            (Get-FileHash -LiteralPath (Join-Path $cacheDirectory $name) -Algorithm SHA256).Hash
        ) {
            throw "qualification build cache hard link digest drifted"
        }
    }
    [IO.File]::AppendAllText(
        (Join-Path $cacheDirectory "server.exe"),
        "-drift",
        [Text.UTF8Encoding]::new($false)
    )
    if (Test-QualificationBuildCacheReceipt `
        -CacheDirectory $cacheDirectory `
        -CacheKey $key `
        -SourceSha256 $source `
        -BinaryNames $binaryNames) {
        throw "drifted qualification build cache was accepted"
    }
}
finally {
    if (Test-Path -LiteralPath $temporaryRoot) {
        Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
    }
}

Write-Output "QUALIFICATION_BUILD_CACHE_PASS"
