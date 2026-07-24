[CmdletBinding()]
# 该入口统一锁定 C++ 工具链、依赖恢复、构建与资格验证，避免开发机和 CI 使用不同隐式环境。
param(
    # Command 只允许受支持的非交互动作，未知动作在任何文件变更前失败。
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("bootstrap", "restore", "toolchain", "hygiene", "configure", "build", "test", "verify", "clean-evidence")]
    [string]$Command,

    # Preset 选择 tracked Windows 配置，不接受本机自定义参数覆盖锁定 identity。
    [ValidateSet("windows-msvc-debug", "windows-msvc-release", "windows-msvc-asan", "windows-msvc-ci")]
    [string]$Preset = "windows-msvc-debug"
)

$ErrorActionPreference = "Stop"
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
# RepositoryRoot 是版本目录、缓存、simulation source 与报告的唯一可信路径基准。
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
# CppToolModule 承载可单独回归的下载、归档安全和 exact toolchain 原语。
$CppToolModule = Join-Path $PSScriptRoot "CppTool.psm1"
Import-Module $CppToolModule -Force
Set-Location $RepositoryRoot

# Write-CppStatus 输出简短、稳定的阶段状态，便于本地终端和 CI 共用日志。
function Write-CppStatus {
    param([string]$Label, [string]$Message, [System.ConsoleColor]$Color)

    Write-Host "[$Label]" -NoNewline -ForegroundColor $Color
    Write-Host " $Message"
}

# Invoke-CppRestore 只恢复被 versions.yaml 锁定且可校验的工具与第三方 source。
function Invoke-CppRestore {
    $restored = Restore-CppDependencies $RepositoryRoot
    foreach ($entry in $restored.GetEnumerator()) {
        Write-CppStatus "READY" "$($entry.Key): $($entry.Value)" Green
    }
}

# Invoke-CppToolchainEvidence 拒绝系统默认 compiler，并展示实际 compiler macro 证据。
function Invoke-CppToolchainEvidence {
    $toolchain = Get-ExactCppToolchain $RepositoryRoot
    $evidence = Invoke-MsvcEvidence $RepositoryRoot $toolchain
    [ordered]@{
        visual_studio = $toolchain.VisualStudioVersion
        visual_studio_build = $toolchain.VisualStudioBuild
        msvc = $toolchain.MsvcVersion
        windows_sdk = $toolchain.WindowsSdkVersion
        msc_ver = $evidence.msc_ver
        msc_full_ver = $evidence.msc_full_ver
        cl = $toolchain.ClPath
    } | ConvertTo-Json
}

# Get-CppReadyEnvironment 验证本地依赖与 exact toolchain；缺失时只提示 bootstrap，不在构建阶段联网。
function Get-CppReadyEnvironment {
    $restored = [ordered]@{}
    foreach ($dependency in (Get-CppDependencyCatalog $RepositoryRoot)) {
        $destination = if ($dependency.Kind -eq "tool") {
            Join-Path $RepositoryRoot (".local\cpp\cmake\" + $dependency.Version)
        }
        else {
            Join-Path $RepositoryRoot (".local\cpp\sources\" + $dependency.Key + "\" + $dependency.Version)
        }
        if (-not (Test-RestoredDependency $dependency $destination)) {
            throw "$($dependency.Key) 本地缓存缺失或漂移；请先运行 cpp.ps1 bootstrap"
        }
        $restored[$dependency.Key] = $destination
    }
    return [pscustomobject]@{
        Toolchain = Get-ExactCppToolchain $RepositoryRoot
        CMake = Join-Path $restored.cmake "bin\cmake.exe"
    }
}

# Invoke-WithMsvcEnvironment 仅在子动作期间加载锁定 VsDevCmd 环境，并验证其未选择默认 14.51。
function Invoke-WithMsvcEnvironment {
    param(
        [Parameter(Mandatory = $true)]$Environment,
        [Parameter(Mandatory = $true)][scriptblock]$Callback
    )

    $vsDevCmd = Join-Path $Environment.Toolchain.InstallationPath "Common7\Tools\VsDevCmd.bat"
    if (-not (Test-Path -LiteralPath $vsDevCmd)) {
        throw "锁定 VsDevCmd.bat 不存在：$vsDevCmd"
    }
    $sdkDirectoryVersion = Read-CatalogValue (Join-Path $RepositoryRoot "versions.yaml") "toolchains" "windows_sdk" "directory_version"
    $productLanguage = Read-CatalogValue (Join-Path $RepositoryRoot "versions.yaml") "toolchains" "visual_studio_build_tools" "product_language"
    $productLanguageLcid = Read-CatalogValue (Join-Path $RepositoryRoot "versions.yaml") "toolchains" "visual_studio_build_tools" "product_language_lcid"
    $commandLine = "`"$vsDevCmd`" -no_logo -arch=x64 -host_arch=x64 -vcvars_ver=14.50 -winsdk=$sdkDirectoryVersion && set"
    $environmentLines = @(& cmd.exe /d /s /c $commandLine)
    if ($LASTEXITCODE -ne 0) {
        throw "锁定 VsDevCmd 环境初始化失败"
    }

    $previous = @{}
    try {
        foreach ($line in $environmentLines) {
            $separator = $line.IndexOf("=")
            if ($separator -le 0) {
                continue
            }
            $name = $line.Substring(0, $separator)
            $value = $line.Substring($separator + 1)
            $previous[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
            [Environment]::SetEnvironmentVariable($name, $value, "Process")
        }
        if ($env:VCToolsVersion.TrimEnd('\') -ne $Environment.Toolchain.MsvcVersion -or
            $env:WindowsSDKVersion.TrimEnd('\') -ne $sdkDirectoryVersion) {
            throw "VsDevCmd 选择了非锁定 compiler 或 Windows SDK"
        }
        # MSVC 本地化 resource 使用系统代码页输出；固定英文 UI 后日志仅含 ASCII，
        # 避免 CMake /showIncludes 前缀与项目 UTF-8 终端之间发生不可逆乱码。
        Invoke-WithScopedProcessEnvironment ([ordered]@{
                VSLANG = $productLanguageLcid
                PreferredUILang = $productLanguage
            }) $Callback
    }
    finally {
        foreach ($entry in $previous.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
        }
    }
}

# Get-CppSourceDigest 对 tracked 与待提交的 C++ 输入按相对路径和内容计算稳定摘要，不包含仓库绝对路径。
function Get-CppSourceDigest {
    $relativeFiles = @(& git -C $RepositoryRoot ls-files --cached --others --exclude-standard -- versions.yaml simulation tools/cpp)
    if ($LASTEXITCODE -ne 0) {
        throw "无法枚举 C++ build identity 输入"
    }
    $rows = foreach ($relativePath in ($relativeFiles | Sort-Object -Unique)) {
        $absolutePath = Join-Path $RepositoryRoot $relativePath
        if (Test-Path -LiteralPath $absolutePath -PathType Leaf) {
            $normalizedPath = $relativePath.Replace("\", "/")
            $fileHash = (Get-FileHash -LiteralPath $absolutePath -Algorithm SHA256).Hash.ToLowerInvariant()
            "$normalizedPath=$fileHash"
        }
    }
    $payload = ([string]::Join("`n", $rows) + "`n")
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([System.BitConverter]::ToString(
            $sha256.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($payload))
        ) -replace "-", "").ToLowerInvariant()
    }
    finally {
        $sha256.Dispose()
    }
}

# Write-CppBuildIdentity 把低敏 CMake manifest 与 source/config 摘要绑定为可重复 target identity。
function Write-CppBuildIdentity {
    param([Parameter(Mandatory = $true)][string]$SelectedPreset)

    $buildRoot = Join-Path $RepositoryRoot "simulation\out\build\$SelectedPreset"
    $manifestPath = Join-Path $buildRoot "ihomeland-build-manifest.json"
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "CMake build manifest 不存在：$manifestPath"
    }
    $manifestText = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8
    if ($manifestText -match [regex]::Escape($RepositoryRoot) -or
        $manifestText -match '(?i)[A-Z]:\\Users\\|password|secret|token') {
        throw "CMake build manifest 包含绝对用户路径或敏感字段"
    }
    $manifest = $manifestText | ConvertFrom-Json
    $expectedBuildType = switch ($SelectedPreset) {
        "windows-msvc-debug" { "Debug" }
        "windows-msvc-release" { "Release" }
        "windows-msvc-ci" { "Release" }
        "windows-msvc-asan" { "RelWithDebInfo" }
        default { throw "未知 C++ preset：$SelectedPreset" }
    }
    $expectedAsan = $SelectedPreset -eq "windows-msvc-asan"
    $expectedCompilePolicy = "/W4;/WX;/permissive-;/Zc:__cplusplus;/Zc:preprocessor;/EHsc;/utf-8"
    if ($manifest.cxx_compiler_id -ne "MSVC" -or
        $manifest.msvc_toolset -ne "14.50.35717" -or
        $manifest.compiler_ui_language -ne "en-US" -or
        $manifest.compiler_ui_lcid -ne "1033" -or
        $manifest.windows_sdk_package -ne "10.0.26100.8876" -or
        $manifest.cxx_standard -ne 20 -or
        $manifest.msvc_runtime -ne "dynamic" -or
        $manifest.compile_policy -ne $expectedCompilePolicy -or
        $manifest.build_type -ne $expectedBuildType -or
        [bool]$manifest.asan -ne $expectedAsan -or
        -not $manifest.warnings_as_errors -or
        -not $manifest.dependencies_disconnected -or
        $manifest.dependencies.jolt -ne "5.5.0" -or
        $manifest.dependencies.recast_detour -ne "1.6.0" -or
        $manifest.dependencies.nlohmann_json -ne "3.12.0") {
        throw "CMake build manifest 未绑定锁定 compiler、SDK、preset、编译策略或依赖 identity"
    }

    $sourceDigest = Get-CppSourceDigest
    $canonicalManifest = $manifest | ConvertTo-Json -Depth 10 -Compress
    $identityPayload = "preset=$SelectedPreset`nsource_sha256=$sourceDigest`nmanifest=$canonicalManifest`n"
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $targetIdentity = ([System.BitConverter]::ToString(
            $sha256.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($identityPayload))
        ) -replace "-", "").ToLowerInvariant()
    }
    finally {
        $sha256.Dispose()
    }

    $identity = [ordered]@{
        schema_version = 1
        preset = $SelectedPreset
        source_sha256 = $sourceDigest
        target_identity = $targetIdentity
        manifest = $manifest
    }
    $identityPath = Join-Path $buildRoot "ihomeland-build-identity.json"
    $identityJson = $identity | ConvertTo-Json -Depth 10
    [System.IO.File]::WriteAllText($identityPath, $identityJson + "`n", [System.Text.UTF8Encoding]::new($false))
    return [pscustomobject]$identity
}

# Remove-CppBuildTree 只删除单个已验证 preset 的 ignored build tree，用于 clean-build evidence。
function Remove-CppBuildTree {
    param([Parameter(Mandatory = $true)][string]$SelectedPreset)

    $buildRoot = Join-Path $RepositoryRoot "simulation\out\build\$SelectedPreset"
    if (Test-Path -LiteralPath $buildRoot) {
        Assert-PathUnderRoot $buildRoot (Join-Path $RepositoryRoot "simulation\out\build")
        Remove-Item -LiteralPath $buildRoot -Recurse -Force
    }
}

# Assert-BattleCorpora 调用既有 source-of-truth validators，C++ consumer 不复制 corpus 治理规则。
function Assert-BattleCorpora {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (
        Join-Path $RepositoryRoot "tools\battle-model\battle-model.ps1") validate
    if ($LASTEXITCODE -ne 0) {
        throw "battle model validator 失败"
    }
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (
        Join-Path $RepositoryRoot "tools\battle-network-profile\battle-network-profile.ps1") validate
    if ($LASTEXITCODE -ne 0) {
        throw "battle network profile validator 失败"
    }
}

# Invoke-CMakeAction 在 simulation 根执行 tracked preset，并把非零退出码转换为终止错误。
function Invoke-CMakeAction {
    param(
        [Parameter(Mandatory = $true)][ValidateSet("configure", "build", "test")][string]$Action,
        [Parameter(Mandatory = $true)][string]$SelectedPreset
    )

    if ($Action -eq "configure") {
        Assert-BattleCorpora
    }
    $environment = Get-CppReadyEnvironment
    $simulationRoot = Join-Path $RepositoryRoot "simulation"
    Invoke-WithMsvcEnvironment $environment {
        $offlineVariables = @("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY")
        $offlinePrevious = @{}
        foreach ($name in $offlineVariables) {
            $offlinePrevious[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
        }
        $env:HTTP_PROXY = "http://127.0.0.1:9"
        $env:HTTPS_PROXY = "http://127.0.0.1:9"
        $env:ALL_PROXY = "http://127.0.0.1:9"
        $env:NO_PROXY = "localhost,127.0.0.1"
        Push-Location $simulationRoot
        try {
            switch ($Action) {
                "configure" {
                    # 首次 configure 只生成已锁定 toolchain manifest；真实 target identity
                    # 在 source+manifest 摘要完成后立即通过同一 build tree 二次 configure 嵌入。
                    $bootstrapIdentity = "0" * 64
                    & $environment.CMake --preset $SelectedPreset --fresh `
                        "-DIHOMELAND_CONTROL_BUILD_IDENTITY=$bootstrapIdentity"
                }
                "build" { & $environment.CMake --build --preset $SelectedPreset }
                "test" { & (Join-Path (Split-Path $environment.CMake -Parent) "ctest.exe") --preset $SelectedPreset }
            }
            if ($LASTEXITCODE -ne 0) {
                throw "CMake $Action ($SelectedPreset) 失败，退出码 $LASTEXITCODE"
            }
            if ($Action -eq "configure") {
                $buildRoot = Join-Path $simulationRoot "out\build\$SelectedPreset"
                Assert-CMakeMsvcEnglishShowIncludesPrefix $buildRoot
                $identity = Write-CppBuildIdentity $SelectedPreset
                & $environment.CMake -S $simulationRoot -B $buildRoot `
                    "-DIHOMELAND_CONTROL_BUILD_IDENTITY=$($identity.target_identity)"
                if ($LASTEXITCODE -ne 0) {
                    throw "CMake control identity configure ($SelectedPreset) 失败，退出码 $LASTEXITCODE"
                }
            }
        }
        finally {
            Pop-Location
            foreach ($entry in $offlinePrevious.GetEnumerator()) {
                [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
            }
        }
    }
}

# Invoke-CppVerify 先允许 bootstrap 恢复缺失环境，再在禁网策略下执行 clean CI/ASan 构建与稳定身份校验。
function Invoke-CppVerify {
    Invoke-CppRestore
    $prerequisites = Install-CppPrerequisites $RepositoryRoot
    Invoke-MsvcEvidence $RepositoryRoot $prerequisites.Toolchain | Out-Null
    Assert-CppRepositoryHygiene $RepositoryRoot

    Remove-CppBuildTree "windows-msvc-ci"
    Invoke-CMakeAction "configure" "windows-msvc-ci"
    $firstIdentity = (Get-Content -LiteralPath (Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json") -Raw -Encoding UTF8 | ConvertFrom-Json).target_identity
    Invoke-CMakeAction "build" "windows-msvc-ci"
    Invoke-CMakeAction "test" "windows-msvc-ci"
    Invoke-CMakeAction "configure" "windows-msvc-ci"
    $secondIdentity = (Get-Content -LiteralPath (Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json") -Raw -Encoding UTF8 | ConvertFrom-Json).target_identity
    if ($firstIdentity -ne $secondIdentity) {
        throw "相同 source/config 重建产生不同 target identity"
    }

    Remove-CppBuildTree "windows-msvc-asan"
    Invoke-CMakeAction "configure" "windows-msvc-asan"
    Invoke-CMakeAction "build" "windows-msvc-asan"
    Invoke-CMakeAction "test" "windows-msvc-asan"
    $ciIdentityPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-build-identity.json"
    $asanIdentityPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-asan\ihomeland-build-identity.json"
    $ciIdentity = Get-Content -LiteralPath $ciIdentityPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $asanIdentity = Get-Content -LiteralPath $asanIdentityPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($ciIdentity.source_sha256 -ne $asanIdentity.source_sha256) {
        throw "CI 与 ASan build identity 未绑定同一 source"
    }
    # receipt 只在两套 CTest 成功后写入，并由 Release qualification binary 重新验证。
    $gateReceipt = [ordered]@{
        schema_version = 1
        source_sha256 = $ciIdentity.source_sha256
        ci_target_identity = $ciIdentity.target_identity
        asan_target_identity = $asanIdentity.target_identity
        ci_ctest_passed = $true
        asan_ctest_passed = $true
    }
    $gateReceiptPath = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\qualification-gate-receipt.json"
    [System.IO.File]::WriteAllText(
        $gateReceiptPath,
        ($gateReceipt | ConvertTo-Json -Compress) + "`n",
        [System.Text.UTF8Encoding]::new($false))
    # qualification 必须在 ASan suite 通过后由同一 source identity 的固定 Release binary 生成，
    # 避免 sanitizer instrumentation 污染 reference benchmark。
    $qualificationTool = Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-ci\ihomeland-sim-qualification.exe"
    $environment = Get-CppReadyEnvironment
    Invoke-WithMsvcEnvironment $environment {
        & $qualificationTool --emit-qualified
        if ($LASTEXITCODE -ne 0) {
            throw "B0.3 qualification report 生成失败"
        }
    }
    Write-CppStatus "PASS" "clean offline CI、ASan 与稳定 target identity 验证通过：$firstIdentity" Green
}

switch ($Command) {
    "bootstrap" {
        Invoke-CppRestore
        $prerequisites = Install-CppPrerequisites $RepositoryRoot
        $evidence = Invoke-MsvcEvidence $RepositoryRoot $prerequisites.Toolchain
        Write-CppStatus "READY" "MSVC $($prerequisites.Toolchain.MsvcVersion), _MSC_VER=$($evidence.msc_ver), Windows SDK $($prerequisites.Toolchain.WindowsSdkVersion)" Green
        if ($prerequisites.RestartRequired) {
            Write-CppStatus "NOTICE" "安装器请求重启；重启后重新运行 bootstrap。" Yellow
        }
    }
    "restore" {
        Invoke-CppRestore
    }
    "toolchain" {
        Invoke-CppToolchainEvidence
    }
    "hygiene" {
        Assert-CppRepositoryHygiene $RepositoryRoot
        Write-CppStatus "PASS" "C++ cache、构建产物与高置信 secret 检查通过。" Green
    }
    "configure" {
        Invoke-CMakeAction "configure" $Preset
        Write-CppStatus "PASS" "CMake configure: $Preset" Green
    }
    "build" {
        Invoke-CMakeAction "build" $Preset
        Write-CppStatus "PASS" "CMake build: $Preset" Green
    }
    "test" {
        Invoke-CMakeAction "test" $Preset
        Write-CppStatus "PASS" "CTest: $Preset" Green
    }
    "verify" {
        Invoke-CppVerify
    }
    "clean-evidence" {
        $evidenceRoot = Join-Path $RepositoryRoot "simulation\reports"
        if (Test-Path -LiteralPath $evidenceRoot) {
            Assert-PathUnderRoot $evidenceRoot (Join-Path $RepositoryRoot "simulation")
            Remove-Item -LiteralPath $evidenceRoot -Recurse -Force
        }
        Write-CppStatus "OK" "本地 C++ evidence 已清理。" Green
    }
    default {
        throw "$Command 尚未进入实现阶段"
    }
}
