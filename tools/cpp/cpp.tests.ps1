[CmdletBinding()]
# 该脚本在隔离临时目录中回归下载、归档与工具链拒绝语义，不访问外部网络。
param()

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
# RepositoryRoot 用于定位待测模块；所有测试产物必须保留在已忽略的 .tmp 下。
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
# TestRoot 每次运行均唯一，避免并行测试互相删除文件。
$TestRoot = Join-Path $RepositoryRoot (".tmp\cpp-tool-tests-" + [guid]::NewGuid().ToString("N"))
Import-Module (Join-Path $PSScriptRoot "CppTool.psm1") -Force

# Assert-True 为无第三方测试框架的 bootstrap 阶段提供最小断言。
function Assert-True {
    param([bool]$Condition, [string]$Message)

    if (-not $Condition) {
        throw "断言失败：$Message"
    }
}

# Assert-Throws 验证安全边界明确失败，并可选校验稳定错误片段。
function Assert-Throws {
    param([scriptblock]$Action, [string]$ExpectedMessage)

    try {
        & $Action
    }
    catch {
        if (-not [string]::IsNullOrEmpty($ExpectedMessage) -and $_.Exception.Message -notlike "*$ExpectedMessage*") {
            throw "错误不匹配：期望包含 '$ExpectedMessage'，实际 '$($_.Exception.Message)'"
        }
        return
    }
    throw "期望动作失败，但实际成功"
}

# New-TestZip 创建带固定 source identity 的最小依赖归档。
function New-TestZip {
    param([string]$Path, [string]$TopDirectory, [switch]$Unsafe)

    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::Create)
    $archive = [System.IO.Compression.ZipArchive]::new($stream, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        $entries = if ($Unsafe) {
            @{ "../escape.txt" = "bad" }
        }
        else {
            @{
                "$TopDirectory/LICENSE" = "test license"
                "$TopDirectory/bin/tool.exe" = "test tool"
            }
        }
        foreach ($name in $entries.Keys) {
            $entry = $archive.CreateEntry($name)
            $writer = [System.IO.StreamWriter]::new($entry.Open())
            try {
                $writer.Write($entries[$name])
            }
            finally {
                $writer.Dispose()
            }
        }
    }
    finally {
        $archive.Dispose()
        $stream.Dispose()
    }
}

try {
    New-Item -ItemType Directory -Force -Path $TestRoot | Out-Null
    $safeArchive = Join-Path $TestRoot "safe.zip"
    $unsafeArchive = Join-Path $TestRoot "unsafe.zip"
    New-TestZip $safeArchive "sample-1.0"
    New-TestZip $unsafeArchive "sample-1.0" -Unsafe
    Assert-ArchiveSafe $safeArchive
    Assert-Throws { Assert-ArchiveSafe $unsafeArchive } "越界成员"

    $catalog = @(Get-CppDependencyCatalog $RepositoryRoot)
    Assert-True ($catalog.Count -eq 9) "C++ dependency catalog 必须完整包含 tool、simulation 与 battle transport source"
    foreach ($requiredKey in @("asio", "kcp", "libsodium", "abseil", "protobuf")) {
        $locked = @($catalog | Where-Object { $_.Key -eq $requiredKey })
        Assert-True ($locked.Count -eq 1) "$requiredKey 必须且只能登记一次"
        Assert-True (-not [string]::IsNullOrWhiteSpace($locked[0].Rollback)) "$requiredKey 必须登记回滚规则"
    }
    Assert-True (($catalog | Where-Object { $_.Key -eq "asio" }).TopDirectory -eq "asio-1.38.2") "Asio standalone archive 顶层目录必须绑定精确版本"

    $dependency = [pscustomobject]@{
        Key = "sample"; Version = "1.0"; Url = "test://sample"
        Sha256 = (Get-FileHash -LiteralPath $safeArchive -Algorithm SHA256).Hash.ToLowerInvariant()
        SourceCommit = "0123456789abcdef"; LicenseIdentity = "Test"
        Rollback = "test rollback"
        ArchiveName = "sample-1.0.zip"; TopDirectory = "sample-1.0"
        LicensePath = "LICENSE"; ProbePath = "bin/tool.exe"; Kind = "tool"
    }
    $cppRoot = Join-Path $TestRoot "cpp"
    $copyDownloader = {
        param($Url, $OutFile)
        Copy-Item -LiteralPath $safeArchive -Destination $OutFile
    }
    $destination = Restore-CppDependency $dependency $cppRoot $copyDownloader
    Assert-True (Test-RestoredDependency $dependency $destination) "首次恢复必须产生可复用完整缓存"
    $manifestTimestamp = (Get-Item -LiteralPath (Join-Path $destination ".restore.json")).LastWriteTimeUtc
    $reusedDestination = Restore-CppDependency $dependency $cppRoot $copyDownloader
    Assert-True ($reusedDestination -eq $destination) "身份一致时必须复用同一路径"
    Assert-True ((Get-Item -LiteralPath (Join-Path $destination ".restore.json")).LastWriteTimeUtc -eq $manifestTimestamp) "复用不得重写 manifest"

    $badDependency = $dependency.PSObject.Copy()
    $badDependency.Sha256 = ("0" * 64)
    $badRoot = Join-Path $TestRoot "bad-checksum"
    Assert-Throws { Restore-CppDependency $badDependency $badRoot $copyDownloader } "checksum 不匹配"
    Assert-True (@(Get-ChildItem -LiteralPath (Join-Path $badRoot "downloads") -Filter "*.partial-*" -ErrorAction SilentlyContinue).Count -eq 0) "checksum 失败必须清理 partial"

    $partialRoot = Join-Path $TestRoot "partial"
    $failingDownloader = {
        param($Url, $OutFile)
        [System.IO.File]::WriteAllText($OutFile, "partial")
        throw "模拟下载中断"
    }
    Assert-Throws { Restore-CppDependency $dependency $partialRoot $failingDownloader } "模拟下载中断"
    Assert-True (@(Get-ChildItem -LiteralPath (Join-Path $partialRoot "downloads") -Filter "*.partial-*" -ErrorAction SilentlyContinue).Count -eq 0) "下载中断必须清理 partial"

    $unsafeDependency = $dependency.PSObject.Copy()
    $unsafeDependency.Sha256 = (Get-FileHash -LiteralPath $unsafeArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    $unsafeRoot = Join-Path $TestRoot "unsafe"
    $unsafeDownloader = {
        param($Url, $OutFile)
        Copy-Item -LiteralPath $unsafeArchive -Destination $OutFile
    }
    Assert-Throws { Restore-CppDependency $unsafeDependency $unsafeRoot $unsafeDownloader } "越界成员"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $TestRoot "escape.txt"))) "归档越界文件不得落盘"

    $fakeRoot = Join-Path $TestRoot "toolchain"
    $fakeCl = Join-Path $fakeRoot "cl.exe"
    $fakeSdk = Join-Path $fakeRoot "sdk"
    New-Item -ItemType Directory -Force -Path $fakeSdk | Out-Null
    New-Item -ItemType File -Force -Path $fakeCl | Out-Null
    $expected = [pscustomobject]@{
        VisualStudioVersion = "18.8.1"; VisualStudioBuild = "12021.73"
        MsvcVersion = "14.50.35717"; WindowsSdkVersion = "10.0.26100.8876"
    }
    $actual = [pscustomobject]@{
        VisualStudioVersion = "18.8.1"; VisualStudioBuild = "12021.73"
        MsvcVersion = "14.50.35717"; WindowsSdkVersion = "10.0.26100.8876"
        ClPath = $fakeCl; WindowsSdkRoot = $fakeSdk
    }
    Assert-ExactToolchainMetadata $actual $expected
    $actual.VisualStudioVersion = "17.7.6"
    Assert-Throws { Assert-ExactToolchainMetadata $actual $expected } "VisualStudioVersion"

    $uiCompilerRoot = Join-Path $TestRoot "ui-toolchain"
    $uiCompiler = Join-Path $uiCompilerRoot "cl.exe"
    New-Item -ItemType File -Force -Path $uiCompiler | Out-Null
    Assert-True (-not (Test-MsvcUiLanguageInstalled ([pscustomobject]@{ ClPath = $uiCompiler }) "1033")) "缺失 en-US compiler resource 必须被识别"
    New-Item -ItemType File -Force -Path (Join-Path $uiCompilerRoot "1033\clui.dll") | Out-Null
    Assert-True (Test-MsvcUiLanguageInstalled ([pscustomobject]@{ ClPath = $uiCompiler }) "1033") "存在 clui.dll 时语言包探针必须通过"

    $originalVsLang = [Environment]::GetEnvironmentVariable("VSLANG", "Process")
    $originalPreferredUiLang = [Environment]::GetEnvironmentVariable("PreferredUILang", "Process")
    try {
        [Environment]::SetEnvironmentVariable("VSLANG", "2052", "Process")
        [Environment]::SetEnvironmentVariable("PreferredUILang", "zh-CN", "Process")
        Assert-Throws {
            Invoke-WithScopedProcessEnvironment ([ordered]@{ VSLANG = "1033"; PreferredUILang = "en-US" }) {
                Assert-True ($env:VSLANG -eq "1033") "回调必须看到固定 LCID"
                Assert-True ($env:PreferredUILang -eq "en-US") "回调必须看到固定 UI locale"
                throw "模拟回调失败"
            }
        } "模拟回调失败"
        Assert-True ($env:VSLANG -eq "2052") "异常后必须恢复调用方 VSLANG"
        Assert-True ($env:PreferredUILang -eq "zh-CN") "异常后必须恢复调用方 UI locale"
    }
    finally {
        [Environment]::SetEnvironmentVariable("VSLANG", $originalVsLang, "Process")
        [Environment]::SetEnvironmentVariable("PreferredUILang", $originalPreferredUiLang, "Process")
    }

    $cmakeMetadataRoot = Join-Path $TestRoot "cmake-build\CMakeFiles\4.4.0"
    New-Item -ItemType Directory -Force -Path $cmakeMetadataRoot | Out-Null
    $cmakeMetadataPath = Join-Path $cmakeMetadataRoot "CMakeCXXCompiler.cmake"
    [System.IO.File]::WriteAllText(
        $cmakeMetadataPath,
        "set(CMAKE_CXX_CL_SHOWINCLUDES_PREFIX `"Note: including file:  `")`n",
        [System.Text.UTF8Encoding]::new($false))
    Assert-CMakeMsvcEnglishShowIncludesPrefix (Join-Path $TestRoot "cmake-build")
    [System.IO.File]::WriteAllText(
        $cmakeMetadataPath,
        "set(CMAKE_CXX_CL_SHOWINCLUDES_PREFIX `"娉ㄦ剰: 鍖呭惈鏂囦欢:  `")`n",
        [System.Text.UTF8Encoding]::new($false))
    Assert-Throws {
        Assert-CMakeMsvcEnglishShowIncludesPrefix (Join-Path $TestRoot "cmake-build")
    } "未使用英文 ASCII 前缀"

    Assert-CppRepositoryHygiene $RepositoryRoot
    Write-Host "[PASS] C++ bootstrap security and exact-toolchain tests"
}
finally {
    if (Test-Path -LiteralPath $TestRoot) {
        Assert-PathUnderRoot $TestRoot (Join-Path $RepositoryRoot ".tmp")
        # ZipArchive 在 Windows PowerShell 5.1 中可能延迟释放底层句柄；回收后再执行受控清理。
        [GC]::Collect()
        [GC]::WaitForPendingFinalizers()
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
}
