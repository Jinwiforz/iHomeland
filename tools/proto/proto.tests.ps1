[CmdletBinding()]
# 该脚本用 PowerShell 自带能力回归协议工具的路径、缓存、下载失败和包结构门禁，不依赖 Pester。
param(
    [Parameter(Mandatory = $true)][string]$RepositoryRoot,
    [Parameter(Mandatory = $true)][string]$ValidPackagePath,
    [Parameter(Mandatory = $true)][string]$ExpectedSHA256,
    [Parameter(Mandatory = $true)][string]$DLLPath,
    [Parameter(Mandatory = $true)][string]$AssemblyName,
    [Parameter(Mandatory = $true)][string]$AssemblyVersion,
    [Parameter(Mandatory = $true)][string]$PublicKeyToken
)

$ErrorActionPreference = "Stop"
Import-Module (Join-Path $PSScriptRoot "ProtocolTool.psm1") -Force
$repositoryPath = [System.IO.Path]::GetFullPath($RepositoryRoot)
if (-not (Test-Path -LiteralPath (Join-Path $repositoryPath "versions.yaml") -PathType Leaf)) {
    throw "Protocol tool tests require the iHomeland repository root"
}
$testRoot = Join-Path $RepositoryRoot ".tmp\proto-tool-tests"
$localRoot = Join-Path $testRoot ".local"
Assert-PathUnderRoot -Path $testRoot -Root $repositoryPath -Purpose "Protocol tool test root"

# Assert-Throws 确认失败分支既被拒绝又保留调用方要求的诊断关键词。
function Assert-Throws {
    param([scriptblock]$Action, [string]$ExpectedMessage)

    try {
        & $Action
    }
    catch {
        if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
            throw "Expected error containing '$ExpectedMessage', got '$($_.Exception.Message)'"
        }
        return
    }
    throw "Expected action to fail with '$ExpectedMessage'"
}

# New-PackageCopyDownloader 返回不访问网络的下载器，确保恢复测试只消费已经校验的官方样本。
function New-PackageCopyDownloader {
    param([string]$Source)

    return {
        param([string]$Uri, [string]$Destination)
        Copy-Item -LiteralPath $Source -Destination $Destination -Force
    }.GetNewClosure()
}

try {
    if (Test-Path -LiteralPath $testRoot) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $localRoot | Out-Null

    Assert-Throws -ExpectedMessage "escaped" -Action {
        Assert-PathUnderRoot -Path (Split-Path $RepositoryRoot -Parent) -Root $RepositoryRoot -Purpose "test"
    }

    $cache = Join-Path $localRoot "valid"
    $downloader = New-PackageCopyDownloader -Source $ValidPackagePath
    $restored = Restore-VerifiedNuGetAssembly -PackageURL "https://example.invalid/locked.nupkg" -ExpectedSHA256 $ExpectedSHA256 -CacheDirectory $cache -LocalEnvironmentRoot $localRoot -DLLPath $DLLPath -AssemblyName $AssemblyName -AssemblyVersion $AssemblyVersion -PublicKeyToken $PublicKeyToken -DownloadFile $downloader
    Assert-AssemblyIdentity -Path $restored -ExpectedName $AssemblyName -ExpectedVersion $AssemblyVersion -ExpectedPublicKeyToken $PublicKeyToken

    Assert-Throws -ExpectedMessage "escaped" -Action {
        Restore-VerifiedNuGetAssembly -PackageURL "https://example.invalid/path-escape.nupkg" -ExpectedSHA256 $ExpectedSHA256 -CacheDirectory (Join-Path $localRoot "path-escape") -LocalEnvironmentRoot $localRoot -DLLPath "../outside.dll" -AssemblyName $AssemblyName -AssemblyVersion $AssemblyVersion -PublicKeyToken $PublicKeyToken -DownloadFile $downloader
    }

    [System.IO.File]::WriteAllText((Join-Path $cache "package.nupkg"), "corrupted")
    $restored = Restore-VerifiedNuGetAssembly -PackageURL "https://example.invalid/locked.nupkg" -ExpectedSHA256 $ExpectedSHA256 -CacheDirectory $cache -LocalEnvironmentRoot $localRoot -DLLPath $DLLPath -AssemblyName $AssemblyName -AssemblyVersion $AssemblyVersion -PublicKeyToken $PublicKeyToken -DownloadFile $downloader
    Get-VerifiedPackageHash -Path (Join-Path $cache "package.nupkg") -ExpectedSHA256 $ExpectedSHA256 | Out-Null

    Assert-Throws -ExpectedMessage "Download locked NuGet package failed" -Action {
        Restore-VerifiedNuGetAssembly -PackageURL "https://example.invalid/failure.nupkg" -ExpectedSHA256 $ExpectedSHA256 -CacheDirectory (Join-Path $localRoot "download-failure") -LocalEnvironmentRoot $localRoot -DLLPath $DLLPath -AssemblyName $AssemblyName -AssemblyVersion $AssemblyVersion -PublicKeyToken $PublicKeyToken -DownloadFile { throw "simulated network failure" }
    }

    $invalidSource = Join-Path $testRoot "invalid-source"
    $invalidPackage = Join-Path $testRoot "invalid.nupkg"
    New-Item -ItemType Directory -Force -Path $invalidSource | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $invalidSource "placeholder.txt"), "missing managed DLL")
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::CreateFromDirectory($invalidSource, $invalidPackage)
    $invalidHash = (Get-FileHash -LiteralPath $invalidPackage -Algorithm SHA256).Hash.ToLowerInvariant()
    $invalidDownloader = New-PackageCopyDownloader -Source $invalidPackage
    Assert-Throws -ExpectedMessage "Managed assembly does not exist" -Action {
        Restore-VerifiedNuGetAssembly -PackageURL "https://example.invalid/invalid.nupkg" -ExpectedSHA256 $invalidHash -CacheDirectory (Join-Path $localRoot "invalid-structure") -LocalEnvironmentRoot $localRoot -DLLPath $DLLPath -AssemblyName $AssemblyName -AssemblyVersion $AssemblyVersion -PublicKeyToken $PublicKeyToken -DownloadFile $invalidDownloader
    }

    $publishRoot = Join-Path $testRoot "publish"
    $stage = Join-Path $publishRoot "stage"
    $target = Join-Path $publishRoot "target"
    New-Item -ItemType Directory -Force -Path $stage, $target | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $stage "new.txt"), "new output")
    [System.IO.File]::WriteAllText((Join-Path $target "old.txt"), "old output")
    Publish-GeneratedDirectory -Stage $stage -Target $target -BackupRoot $publishRoot -RepositoryRoot $RepositoryRoot
    if (-not (Test-Path -LiteralPath (Join-Path $target "new.txt") -PathType Leaf) -or
        (Test-Path -LiteralPath (Join-Path $target "old.txt"))) {
        throw "Generated publication did not replace the previous output"
    }
    Assert-Throws -ExpectedMessage "must not overlap" -Action {
        Publish-GeneratedDirectory -Stage $target -Target (Join-Path $target "nested") -BackupRoot $publishRoot -RepositoryRoot $RepositoryRoot
    }

    Write-Host "[OK] Protocol PowerShell tool tests passed."
}
finally {
    if (Test-Path -LiteralPath $testRoot) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
