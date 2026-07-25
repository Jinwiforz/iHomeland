Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Read-CatalogValue 以窄读取方式读取锁定目录，避免恢复工具自身依赖尚未恢复的 YAML runtime。
function Read-CatalogValue {
    param(
        [Parameter(Mandatory = $true)][string]$CatalogPath,
        [Parameter(Mandatory = $true)][string]$Section,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Property
    )

    $insideSection = $false
    $insideName = $false
    foreach ($line in (Get-Content -LiteralPath $CatalogPath -Encoding UTF8)) {
        if ($line -match "^$([regex]::Escape($Section)):\s*$") {
            $insideSection = $true
            $insideName = $false
            continue
        }
        if ($insideSection -and $line -match "^[^\s]") {
            break
        }
        if ($insideSection -and $line -match "^  $([regex]::Escape($Name)):\s*$") {
            $insideName = $true
            continue
        }
        if ($insideName -and $line -match "^    $([regex]::Escape($Property)):\s*`"([^`"]+)`"\s*$") {
            return $Matches[1]
        }
    }
    throw "versions.yaml 缺少 $Section.$Name.$Property"
}

# Assert-PathUnderRoot 防止清理、移动和发布操作逃逸到调用者指定的受控根之外。
function Assert-PathUnderRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $resolvedRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
    $resolvedPath = [System.IO.Path]::GetFullPath($Path)
    if (-not $resolvedPath.StartsWith($resolvedRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "路径逃逸受控根：$resolvedPath"
    }
}

# Test-UnsafeArchiveEntry 识别绝对路径、盘符和上级跳转，阻止归档覆盖 staging 根之外的文件。
function Test-UnsafeArchiveEntry {
    param([Parameter(Mandatory = $true)][string]$Entry)

    $normalized = $Entry.Replace('\', '/')
    if ([string]::IsNullOrWhiteSpace($normalized) -or $normalized.StartsWith('/') -or $normalized -match '^[A-Za-z]:') {
        return $true
    }
    foreach ($segment in $normalized.Split('/')) {
        if ($segment -eq '..') {
            return $true
        }
    }
    return $false
}

# Assert-ArchiveSafe 在解压前枚举完整成员列表；未知格式和任何越界成员都按不可信输入拒绝。
function Assert-ArchiveSafe {
    param([Parameter(Mandatory = $true)][string]$ArchivePath)

    if ($ArchivePath.EndsWith('.zip', [System.StringComparison]::OrdinalIgnoreCase)) {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
        try {
            foreach ($entry in $archive.Entries) {
                if (Test-UnsafeArchiveEntry $entry.FullName) {
                    throw "归档包含越界成员：$($entry.FullName)"
                }
            }
        }
        finally {
            $archive.Dispose()
        }
        return
    }

    if ($ArchivePath.EndsWith('.tar.gz', [System.StringComparison]::OrdinalIgnoreCase)) {
        $entries = @(& tar.exe -tzf $ArchivePath)
        if ($LASTEXITCODE -ne 0) {
            throw "无法枚举归档：$ArchivePath"
        }
        foreach ($entry in $entries) {
            if (Test-UnsafeArchiveEntry $entry) {
                throw "归档包含越界成员：$entry"
            }
        }
        return
    }

    throw "不支持的依赖归档格式：$ArchivePath"
}

# Get-CppDependencyCatalog 将版本目录映射为唯一下载名、顶层目录、许可证和恢复后完整性探针。
function Get-CppDependencyCatalog {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $catalogPath = Join-Path $RepositoryRoot "versions.yaml"
    $definitions = @(
        @{
            Section = "toolchains"; Name = "cmake"; Key = "cmake"
            Archive = "cmake-{0}-windows-x86_64.zip"; Top = "cmake-{0}-windows-x86_64"
            License = "doc/cmake/LICENSE.rst"; Probe = "bin/cmake.exe"; Kind = "tool"
        },
        @{
            Section = "libraries"; Name = "jolt_physics_cpp"; Key = "jolt"
            Archive = "JoltPhysics-v{0}.tar.gz"; Top = "JoltPhysics-{0}"
            License = "LICENSE"; Probe = "Build/CMakeLists.txt"; Kind = "source"
        },
        @{
            Section = "libraries"; Name = "recast_navigation_cpp"; Key = "recast"
            Archive = "recastnavigation-v{0}.tar.gz"; Top = "recastnavigation-{0}"
            License = "License.txt"; Probe = "CMakeLists.txt"; Kind = "source"
        },
        @{
            Section = "libraries"; Name = "nlohmann_json_cpp"; Key = "json"
            Archive = "nlohmann-json-v{0}.tar.gz"; Top = "json-{0}"
            License = "LICENSE.MIT"; Probe = "include/nlohmann/json.hpp"; Kind = "source"
        },
        @{
            Section = "libraries"; Name = "asio_cpp"; Key = "asio"
            Archive = "asio-{0}.zip"; Top = "asio-{0}"
            License = "LICENSE_1_0.txt"; Probe = "include/asio.hpp"; Kind = "source"
            RequiresRollback = $true
        },
        @{
            Section = "libraries"; Name = "kcp_cpp"; Key = "kcp"
            Archive = "kcp-{0}.tar.gz"; Top = "kcp-{0}"
            License = "LICENSE"; Probe = "ikcp.c"; Kind = "source"
            RequiresRollback = $true
        },
        @{
            Section = "libraries"; Name = "libsodium_cpp"; Key = "libsodium"
            Archive = "libsodium-{0}.tar.gz"; Top = "libsodium-{0}"
            License = "LICENSE"; Probe = "src/libsodium/include/sodium.h"; Kind = "source"
            RequiresRollback = $true
        },
        @{
            Section = "libraries"; Name = "abseil_cpp"; Key = "abseil"
            Archive = "abseil-cpp-{0}.tar.gz"; Top = "abseil-cpp-{0}"
            License = "LICENSE"; Probe = "CMakeLists.txt"; Kind = "source"
            RequiresRollback = $true
        },
        @{
            Section = "libraries"; Name = "protobuf_cpp"; Key = "protobuf"
            Archive = "protobuf-{0}.zip"; Top = "protobuf-{0}"
            License = "LICENSE"; Probe = "src/google/protobuf/message_lite.h"; Kind = "source"
            RequiresRollback = $true
        }
    )

    foreach ($definition in $definitions) {
        $version = Read-CatalogValue $catalogPath $definition.Section $definition.Name "version"
        $sourceCommit = ""
        if ($definition.Kind -eq "source") {
            $sourceCommit = Read-CatalogValue $catalogPath $definition.Section $definition.Name "source_commit"
        }
        $rollback = ""
        if ($definition.ContainsKey("RequiresRollback") -and $definition.RequiresRollback) {
            $rollback = Read-CatalogValue $catalogPath $definition.Section $definition.Name "rollback"
        }
        [pscustomobject]@{
            Key = $definition.Key
            Version = $version
            Url = Read-CatalogValue $catalogPath $definition.Section $definition.Name "package_url"
            Sha256 = (Read-CatalogValue $catalogPath $definition.Section $definition.Name "package_sha256").ToLowerInvariant()
            SourceCommit = $sourceCommit
            LicenseIdentity = Read-CatalogValue $catalogPath $definition.Section $definition.Name "license"
            Rollback = $rollback
            ArchiveName = ($definition.Archive -f $version)
            TopDirectory = ($definition.Top -f $version)
            LicensePath = $definition.License
            ProbePath = $definition.Probe
            Kind = $definition.Kind
        }
    }
}

# Test-RestoredDependency 只有 manifest、许可证、探针和所有锁定身份同时匹配时才允许复用缓存。
function Test-RestoredDependency {
    param(
        [Parameter(Mandatory = $true)]$Dependency,
        [Parameter(Mandatory = $true)][string]$Destination
    )

    $manifestPath = Join-Path $Destination ".restore.json"
    if (-not (Test-Path -LiteralPath $manifestPath) -or
        -not (Test-Path -LiteralPath (Join-Path $Destination $Dependency.LicensePath)) -or
        -not (Test-Path -LiteralPath (Join-Path $Destination $Dependency.ProbePath))) {
        return $false
    }
    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
        return $manifest.version -eq $Dependency.Version -and
            $manifest.archive_sha256 -eq $Dependency.Sha256 -and
            $manifest.source_commit -eq $Dependency.SourceCommit -and
            $manifest.license -eq $Dependency.LicenseIdentity -and
            ([string]::IsNullOrEmpty($Dependency.Rollback) -or $manifest.rollback -eq $Dependency.Rollback)
    }
    catch {
        return $false
    }
}

# Invoke-DefaultDownload 将单个官方归档写入指定 partial 路径；调用方负责校验后再发布。
function Invoke-DefaultDownload {
    param([string]$Url, [string]$OutFile)

    Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
}

# Restore-CppDependency 通过 partial 下载、预解压校验和 staging 发布保证失败不留下可复用半成品。
function Restore-CppDependency {
    param(
        [Parameter(Mandatory = $true)]$Dependency,
        [Parameter(Mandatory = $true)][string]$CppRoot,
        [scriptblock]$Downloader = ${function:Invoke-DefaultDownload}
    )

    $downloadRoot = Join-Path $CppRoot "downloads"
    $destination = if ($Dependency.Kind -eq "tool") {
        Join-Path $CppRoot ("cmake\" + $Dependency.Version)
    }
    else {
        Join-Path $CppRoot ("sources\" + $Dependency.Key + "\" + $Dependency.Version)
    }
    $archivePath = Join-Path $downloadRoot $Dependency.ArchiveName
    $partialPath = "$archivePath.partial-$([guid]::NewGuid().ToString('N'))"
    $stagingRoot = Join-Path $CppRoot (".extract-" + [guid]::NewGuid().ToString("N"))
    Assert-PathUnderRoot $destination $CppRoot
    Assert-PathUnderRoot $archivePath $CppRoot
    Assert-PathUnderRoot $partialPath $CppRoot
    Assert-PathUnderRoot $stagingRoot $CppRoot
    New-Item -ItemType Directory -Force -Path $downloadRoot | Out-Null

    if (Test-RestoredDependency $Dependency $destination) {
        return $destination
    }

    try {
        $archiveValid = $false
        if (Test-Path -LiteralPath $archivePath) {
            $archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
            $archiveValid = $archiveHash -eq $Dependency.Sha256
        }
        if (-not $archiveValid) {
            & $Downloader $Dependency.Url $partialPath
            if (-not (Test-Path -LiteralPath $partialPath)) {
                throw "下载器未产生归档：$($Dependency.Key)"
            }
            $partialHash = (Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($partialHash -ne $Dependency.Sha256) {
                throw "$($Dependency.Key) checksum 不匹配：期望 $($Dependency.Sha256)，实际 $partialHash"
            }
            Move-Item -LiteralPath $partialPath -Destination $archivePath -Force
        }

        Assert-ArchiveSafe $archivePath
        New-Item -ItemType Directory -Force -Path $stagingRoot | Out-Null
        if ($archivePath.EndsWith('.zip', [System.StringComparison]::OrdinalIgnoreCase)) {
            # Windows PowerShell 5.1 Expand-Archive 会在 Asio 官方 archive 的 Unix mode
            # metadata 上触发错误清理；bsdtar 已在预枚举后使用同一受控 staging root。
            & tar.exe -xf $archivePath -C $stagingRoot
        }
        else {
            & tar.exe -xzf $archivePath -C $stagingRoot
        }
        if ($LASTEXITCODE -ne 0) {
            throw "解压失败：$archivePath"
        }

        $expanded = Join-Path $stagingRoot $Dependency.TopDirectory
        if (-not (Test-Path -LiteralPath (Join-Path $expanded $Dependency.LicensePath)) -or
            -not (Test-Path -LiteralPath (Join-Path $expanded $Dependency.ProbePath))) {
            throw "$($Dependency.Key) source identity 或许可证不符合锁定目录"
        }
        $manifest = [ordered]@{
            schema_version = 1
            dependency = $Dependency.Key
            version = $Dependency.Version
            archive_sha256 = $Dependency.Sha256
            source_commit = $Dependency.SourceCommit
            license = $Dependency.LicenseIdentity
            rollback = $Dependency.Rollback
        }
        $manifest | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $expanded ".restore.json") -Encoding UTF8

        if (Test-Path -LiteralPath $destination) {
            Remove-Item -LiteralPath $destination -Recurse -Force
        }
        New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
        Move-Item -LiteralPath $expanded -Destination $destination
        if (-not (Test-RestoredDependency $Dependency $destination)) {
            throw "$($Dependency.Key) 发布后完整性检查失败"
        }
        return $destination
    }
    finally {
        if (Test-Path -LiteralPath $partialPath) {
            Remove-Item -LiteralPath $partialPath -Force
        }
        if (Test-Path -LiteralPath $stagingRoot) {
            Remove-Item -LiteralPath $stagingRoot -Recurse -Force
        }
    }
}

# Restore-CppDependencies 按锁定目录恢复全部 C++ 工具与 source，不读取系统级同名包。
function Restore-CppDependencies {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [scriptblock]$Downloader = ${function:Invoke-DefaultDownload}
    )

    $cppRoot = Join-Path $RepositoryRoot ".local\cpp"
    New-Item -ItemType Directory -Force -Path $cppRoot | Out-Null
    $result = [ordered]@{}
    foreach ($dependency in (Get-CppDependencyCatalog $RepositoryRoot)) {
        $result[$dependency.Key] = Restore-CppDependency $dependency $cppRoot $Downloader
    }
    return $result
}

# Get-VerifiedMicrosoftInstaller 下载并校验固定 Microsoft installer，失败时不发布 partial 文件。
function Get-VerifiedMicrosoftInstaller {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$CatalogName,
        [Parameter(Mandatory = $true)][string]$UrlProperty,
        [Parameter(Mandatory = $true)][string]$HashProperty,
        [Parameter(Mandatory = $true)][string]$FileName
    )

    $catalogPath = Join-Path $RepositoryRoot "versions.yaml"
    $url = Read-CatalogValue $catalogPath "toolchains" $CatalogName $UrlProperty
    $expectedHash = (Read-CatalogValue $catalogPath "toolchains" $CatalogName $HashProperty).ToLowerInvariant()
    $downloadRoot = Join-Path $RepositoryRoot ".local\cpp\downloads"
    $installerPath = Join-Path $downloadRoot $FileName
    $partialPath = "$installerPath.partial-$([guid]::NewGuid().ToString('N'))"
    Assert-PathUnderRoot $installerPath (Join-Path $RepositoryRoot ".local")
    New-Item -ItemType Directory -Force -Path $downloadRoot | Out-Null

    try {
        $validHash = (Test-Path -LiteralPath $installerPath) -and
            ((Get-FileHash -LiteralPath $installerPath -Algorithm SHA256).Hash.ToLowerInvariant() -eq $expectedHash)
        if (-not $validHash) {
            Invoke-WebRequest -Uri $url -OutFile $partialPath -UseBasicParsing
            $actualHash = (Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actualHash -ne $expectedHash) {
                throw "$CatalogName installer checksum 不匹配：期望 $expectedHash，实际 $actualHash"
            }
            Move-Item -LiteralPath $partialPath -Destination $installerPath -Force
        }

        $signature = Get-AuthenticodeSignature -LiteralPath $installerPath
        if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid -or
            $null -eq $signature.SignerCertificate -or
            $signature.SignerCertificate.Subject -notlike "CN=Microsoft Corporation,*") {
            throw "$CatalogName installer 缺少有效 Microsoft Authenticode 签名"
        }
        return $installerPath
    }
    finally {
        if (Test-Path -LiteralPath $partialPath) {
            Remove-Item -LiteralPath $partialPath -Force
        }
    }
}

# Test-ExactWindowsSdkInstalled 通过主产品登记与固定 include/lib/bin 探针验证 servicing identity。
function Test-ExactWindowsSdkInstalled {
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$DirectoryVersion
    )

    $displayName = "Windows Software Development Kit - Windows $Version"
    $products = @(Get-ChildItem "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall" -ErrorAction SilentlyContinue |
        Get-ItemProperty |
        Where-Object {
            $_.PSObject.Properties.Name -contains "DisplayName" -and $_.DisplayName -eq $displayName
        })
    $sdkRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10"
    $probes = @(
        (Join-Path $sdkRoot "Include\$DirectoryVersion\um\Windows.h"),
        (Join-Path $sdkRoot "Lib\$DirectoryVersion\um\x64\Kernel32.Lib"),
        (Join-Path $sdkRoot "Lib\$DirectoryVersion\ucrt\x64\ucrt.Lib"),
        (Join-Path $sdkRoot "bin\$DirectoryVersion\x64\rc.exe")
    )
    return $products.Count -eq 1 -and @($probes | Where-Object { -not (Test-Path -LiteralPath $_) }).Count -eq 0
}

# Wait-ExactWindowsSdkInstalled 等待 VS installer 启动的 SDK 子安装器完成发布，避免并发启动兜底安装器。
function Wait-ExactWindowsSdkInstalled {
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$DirectoryVersion,
        [int]$TimeoutSeconds = 120
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if (Test-ExactWindowsSdkInstalled $Version $DirectoryVersion) {
            return $true
        }
        Start-Sleep -Seconds 2
    } while ([DateTime]::UtcNow -lt $deadline)
    return $false
}

# Invoke-ElevatedInstaller 执行受信 installer；非提升终端只在此边界触发一次 Windows UAC。
function Invoke-ElevatedInstaller {
    param(
        [Parameter(Mandatory = $true)][string]$InstallerPath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$DisplayName
    )

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    $startArguments = @{
        FilePath = $InstallerPath
        ArgumentList = $Arguments
        Wait = $true
        PassThru = $true
    }
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        $startArguments.Verb = "RunAs"
    }
    $process = Start-Process @startArguments
    if ($process.ExitCode -notin @(0, 3010)) {
        throw "$DisplayName 安装失败，退出码 $($process.ExitCode)"
    }
    return $process.ExitCode -eq 3010
}

# Test-MsvcUiLanguageInstalled 验证 compiler 存在指定 UI resource，避免本地化诊断被 UTF-8 构建日志错误解码。
function Test-MsvcUiLanguageInstalled {
    param(
        [Parameter(Mandatory = $true)]$Toolchain,
        [Parameter(Mandatory = $true)][string]$LanguageLcid
    )

    $compilerDirectory = Split-Path -Parent $Toolchain.ClPath
    return Test-Path -LiteralPath (Join-Path $compilerDirectory "$LanguageLcid\clui.dll") -PathType Leaf
}

# Invoke-WithScopedProcessEnvironment 在回调期间覆盖少量进程变量，并在成功或异常后恢复调用方环境。
function Invoke-WithScopedProcessEnvironment {
    param(
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Variables,
        [Parameter(Mandatory = $true)][scriptblock]$Action
    )

    $previous = @{}
    try {
        foreach ($entry in $Variables.GetEnumerator()) {
            $name = [string]$entry.Key
            $previous[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
            [Environment]::SetEnvironmentVariable($name, [string]$entry.Value, "Process")
        }
        & $Action
    }
    finally {
        foreach ($entry in $previous.GetEnumerator()) {
            [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
        }
    }
}

# Assert-CMakeMsvcEnglishShowIncludesPrefix 验证 CMake 记录 ASCII include 前缀，防止 NMake 构建泄漏乱码依赖行。
function Assert-CMakeMsvcEnglishShowIncludesPrefix {
    param([Parameter(Mandatory = $true)][string]$BuildRoot)

    $compilerMetadata = @(
        Get-ChildItem -LiteralPath (Join-Path $BuildRoot "CMakeFiles") -Recurse -Filter "CMakeCXXCompiler.cmake" -File
    )
    if ($compilerMetadata.Count -ne 1) {
        throw "CMake C++ compiler metadata 数量异常：$($compilerMetadata.Count)"
    }
    $metadataText = Get-Content -LiteralPath $compilerMetadata[0].FullName -Raw -Encoding UTF8
    $prefixMatch = [regex]::Match(
        $metadataText,
        'set\(CMAKE_CXX_CL_SHOWINCLUDES_PREFIX[ \t]+"([^"]*)"\)')
    if (-not $prefixMatch.Success -or
        -not $prefixMatch.Groups[1].Value.StartsWith("Note: including file: ", [StringComparison]::Ordinal)) {
        throw "MSVC /showIncludes 未使用英文 ASCII 前缀；请运行 cpp.ps1 bootstrap 恢复 en-US Build Tools language pack"
    }
}

# Install-CppPrerequisites 自动检测并安装锁定 Build Tools 与 Windows SDK，不使用 evergreen URL。
function Install-CppPrerequisites {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $catalogPath = Join-Path $RepositoryRoot "versions.yaml"
    $vsVersion = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "version"
    $vsInstallRelative = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "install_root"
    $vsProductLanguage = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "product_language"
    $vsProductLanguageLcid = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "product_language_lcid"
    $vsInstallRoot = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot $vsInstallRelative))
    $msvcVersion = Read-CatalogValue $catalogPath "toolchains" "msvc" "version"
    $msvcComponent = Read-CatalogValue $catalogPath "toolchains" "msvc" "component_id"
    $sdkVersion = Read-CatalogValue $catalogPath "toolchains" "windows_sdk" "version"
    $sdkDirectoryVersion = Read-CatalogValue $catalogPath "toolchains" "windows_sdk" "directory_version"
    $sdkComponent = Read-CatalogValue $catalogPath "toolchains" "windows_sdk" "component_id"
    Assert-PathUnderRoot $vsInstallRoot (Join-Path $RepositoryRoot ".local")

    $restartRequired = $false
    # Build Tools 与 SDK 使用独立探针，避免其中一项缺失时误判另一项已经就绪。
    $expectedClPath = Join-Path $vsInstallRoot ("VC\Tools\MSVC\" + $msvcVersion + "\bin\Hostx64\x64\cl.exe")
    $vsReady = Test-Path -LiteralPath $expectedClPath
    $uiLanguageReady = $vsReady -and (Test-MsvcUiLanguageInstalled ([pscustomobject]@{ ClPath = $expectedClPath }) $vsProductLanguageLcid)
    $sdkReady = Test-ExactWindowsSdkInstalled $sdkVersion $sdkDirectoryVersion
    if (-not $vsReady -or -not $uiLanguageReady -or -not $sdkReady) {
        $vsInstaller = Get-VerifiedMicrosoftInstaller $RepositoryRoot "visual_studio_build_tools" "bootstrapper_url" "bootstrapper_sha256" "vs_BuildTools-$vsVersion.exe"
        $restartRequired = (Invoke-ElevatedInstaller $vsInstaller @(
                "--quiet", "--wait", "--norestart", "--nocache",
                "--installPath", $vsInstallRoot,
                "--addProductLang", $vsProductLanguage,
                "--add", $msvcComponent,
                "--add", "Microsoft.VisualStudio.Component.VC.ASAN",
                "--add", $sdkComponent
            ) "Visual Studio Build Tools $vsVersion") -or $restartRequired
    }

    # 固定 VS channel 组件是首选；独立、验签的 SDK installer 只在组件未发布完整探针时兜底。
    if (-not (Wait-ExactWindowsSdkInstalled $sdkVersion $sdkDirectoryVersion)) {
        $sdkInstaller = Get-VerifiedMicrosoftInstaller $RepositoryRoot "windows_sdk" "installer_url" "installer_sha256" "winsdksetup-$sdkVersion.exe"
        $restartRequired = (Invoke-ElevatedInstaller $sdkInstaller @(
                "/features", "OptionId.DesktopCPPx64", "OptionId.DesktopCPPx86", "OptionId.SigningTools",
                "/quiet", "/norestart"
            ) "Windows SDK $sdkVersion") -or $restartRequired
    }

    $toolchain = Get-ExactCppToolchain $RepositoryRoot
    if (-not (Test-MsvcUiLanguageInstalled $toolchain $vsProductLanguageLcid)) {
        throw "Visual Studio Build Tools 缺少 $vsProductLanguage language pack"
    }
    if (-not (Test-ExactWindowsSdkInstalled $sdkVersion $sdkDirectoryVersion)) {
        throw "Windows SDK $sdkVersion 安装后 evidence 不完整"
    }
    return [pscustomobject]@{ Toolchain = $toolchain; RestartRequired = $restartRequired }
}

# Assert-ExactToolchainMetadata 校验发现结果的每个锁定身份，不允许回退到 PATH 中的默认 compiler。
function Assert-ExactToolchainMetadata {
    param(
        [Parameter(Mandatory = $true)]$Actual,
        [Parameter(Mandatory = $true)]$Expected
    )

    foreach ($property in @("VisualStudioVersion", "VisualStudioBuild", "MsvcVersion", "WindowsSdkVersion")) {
        if ([string]$Actual.$property -ne [string]$Expected.$property) {
            throw "C++ toolchain 不匹配：$property 期望 $($Expected.$property)，实际 $($Actual.$property)"
        }
    }
    if (-not (Test-Path -LiteralPath $Actual.ClPath)) {
        throw "锁定 cl.exe 不存在：$($Actual.ClPath)"
    }
    if (-not (Test-Path -LiteralPath $Actual.WindowsSdkRoot)) {
        throw "锁定 Windows SDK 不存在：$($Actual.WindowsSdkRoot)"
    }
}

# Get-ExactCppToolchain 仅通过 vswhere 和锁定目录定位 compiler，不接受 PATH 或较旧实例作为兜底。
function Get-ExactCppToolchain {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $catalogPath = Join-Path $RepositoryRoot "versions.yaml"
    $expected = [pscustomobject]@{
        VisualStudioVersion = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "version"
        VisualStudioBuild = Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "build"
        MsvcVersion = Read-CatalogValue $catalogPath "toolchains" "msvc" "version"
        WindowsSdkVersion = Read-CatalogValue $catalogPath "toolchains" "windows_sdk" "version"
        WindowsSdkDirectoryVersion = Read-CatalogValue $catalogPath "toolchains" "windows_sdk" "directory_version"
        MsvcComponent = Read-CatalogValue $catalogPath "toolchains" "msvc" "component_id"
        InstallationPath = [System.IO.Path]::GetFullPath((Join-Path $RepositoryRoot (Read-CatalogValue $catalogPath "toolchains" "visual_studio_build_tools" "install_root")))
    }
    $vswhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
    if (-not (Test-Path -LiteralPath $vswhere)) {
        throw "未找到 vswhere；需要安装锁定的 Visual Studio Build Tools $($expected.VisualStudioVersion)"
    }
    $instanceJson = (& $vswhere -all -products * -version "[18.0,19.0)" -requires $expected.MsvcComponent -format json -utf8)
    $instances = @($instanceJson | ConvertFrom-Json | Where-Object {
            [System.IO.Path]::GetFullPath([string]$_.installationPath) -eq $expected.InstallationPath
        })
    if ($instances.Count -ne 1) {
        throw "未找到 Visual Studio 2026 与 MSVC $($expected.MsvcVersion)；不会回退到系统默认 compiler"
    }
    $instance = $instances[0]
    $instancePath = [string]$instance.installationPath

    $clPath = Join-Path $instancePath ("VC\Tools\MSVC\" + $expected.MsvcVersion + "\bin\Hostx64\x64\cl.exe")
    $sdkBase = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10"
    $sdkRoot = Join-Path $sdkBase ("Include\" + $expected.WindowsSdkDirectoryVersion)
    $actualBuild = ([string]$instance.installationVersion -split '\.', 3)[2]
    $sdkInstalled = Test-ExactWindowsSdkInstalled $expected.WindowsSdkVersion $expected.WindowsSdkDirectoryVersion
    $actual = [pscustomobject]@{
        VisualStudioVersion = [string]$instance.catalog.productDisplayVersion
        VisualStudioBuild = $actualBuild
        MsvcVersion = $expected.MsvcVersion
        WindowsSdkVersion = if ($sdkInstalled) { $expected.WindowsSdkVersion } else { "missing" }
        ClPath = $clPath
        WindowsSdkRoot = $sdkRoot
        InstallationPath = $instancePath
    }
    Assert-ExactToolchainMetadata $actual $expected
    return $actual
}

# Invoke-MsvcEvidence 预处理最小探针，以编译器宏而非目录名证明实际使用的 MSVC 身份。
function Invoke-MsvcEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)]$Toolchain
    )

    $catalogPath = Join-Path $RepositoryRoot "versions.yaml"
    $expectedMscVer = Read-CatalogValue $catalogPath "toolchains" "msvc" "msc_ver"
    $temporaryRoot = Join-Path $RepositoryRoot (".tmp\cpp-compiler-evidence-" + [guid]::NewGuid().ToString("N"))
    Assert-PathUnderRoot $temporaryRoot (Join-Path $RepositoryRoot ".tmp")
    New-Item -ItemType Directory -Force -Path $temporaryRoot | Out-Null
    $source = Join-Path $temporaryRoot "compiler_evidence.cpp"
    try {
        @'
#define IH_STRINGIZE_INNER(value) #value
#define IH_STRINGIZE(value) IH_STRINGIZE_INNER(value)
IHOMELAND_MSC_VER=IH_STRINGIZE(_MSC_VER)
IHOMELAND_MSC_FULL_VER=IH_STRINGIZE(_MSC_FULL_VER)
'@ | Set-Content -LiteralPath $source -Encoding ASCII
        $preprocessed = @(& $Toolchain.ClPath /nologo /EP $source)
        if ($LASTEXITCODE -ne 0) {
            throw "锁定 MSVC compiler 宏探针失败"
        }
        $mscVerLine = $preprocessed | Where-Object { $_ -match '^IHOMELAND_MSC_VER="?\d+"?$' } | Select-Object -First 1
        $mscFullVerLine = $preprocessed | Where-Object { $_ -match '^IHOMELAND_MSC_FULL_VER="?\d+"?$' } | Select-Object -First 1
        if ($null -eq $mscVerLine -or $null -eq $mscFullVerLine) {
            throw "锁定 MSVC compiler 未产生可解析的 macro evidence"
        }
        $mscVer = [regex]::Match($mscVerLine, '\d+').Value
        $mscFullVer = [regex]::Match($mscFullVerLine, '\d+').Value
        if ($mscVer -ne $expectedMscVer) {
            throw "_MSC_VER 不匹配：期望 $expectedMscVer，实际 $mscVer"
        }
        return [pscustomobject]@{ msc_ver = $mscVer; msc_full_ver = $mscFullVer }
    }
    finally {
        if (Test-Path -LiteralPath $temporaryRoot) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
}

# Assert-CppRepositoryHygiene 证明本机 source/build/report/preset 不可入库，并扫描 C++ 变更中的高置信 secret。
function Assert-CppRepositoryHygiene {
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $ignoredSamples = @(
        ".local/cpp/downloads/sample.zip",
        ".local/cpp/sources/sample/source.cpp",
        "simulation/out/sample.obj",
        "simulation/build/CMakeCache.txt",
        "simulation/CMakeUserPresets.json",
        "simulation/.vs/state.json",
        "simulation/compile_commands.json",
        "simulation/reports/benchmark.json"
    )
    foreach ($sample in $ignoredSamples) {
        & git -C $RepositoryRoot check-ignore -q -- $sample
        if ($LASTEXITCODE -ne 0) {
            throw "C++ 本机产物未被 .gitignore 覆盖：$sample"
        }
    }

    $prohibitedTracked = @(& git -C $RepositoryRoot ls-files -- ".local/cpp" "simulation/out" "simulation/build" "simulation/CMakeUserPresets.json" "simulation/.vs" "simulation/compile_commands.json" "simulation/reports")
    if ($LASTEXITCODE -ne 0) {
        throw "无法检查 C++ tracked cache"
    }
    if ($prohibitedTracked.Count -ne 0) {
        throw "C++ 本机产物已被 Git 跟踪：$($prohibitedTracked -join ', ')"
    }

    $candidateFiles = @(& git -C $RepositoryRoot ls-files -- "simulation" "tools/cpp")
    $candidateFiles += @(& git -C $RepositoryRoot ls-files --others --exclude-standard -- "simulation" "tools/cpp")
    $secretPattern = '(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|(?:password|client_secret|access_token|private_key)\s*[:=]\s*["'']?[A-Za-z0-9+/=_-]{8,}'
    foreach ($relativePath in ($candidateFiles | Sort-Object -Unique)) {
        $absolutePath = Join-Path $RepositoryRoot $relativePath
        if ((Test-Path -LiteralPath $absolutePath -PathType Leaf) -and
            (Get-Content -LiteralPath $absolutePath -Raw -Encoding UTF8) -match $secretPattern) {
            throw "C++ source 或工具包含疑似 secret：$relativePath"
        }
    }
}

Export-ModuleMember -Function Read-CatalogValue, Assert-PathUnderRoot, Test-UnsafeArchiveEntry, Assert-ArchiveSafe, Get-CppDependencyCatalog, Test-RestoredDependency, Restore-CppDependency, Restore-CppDependencies, Get-VerifiedMicrosoftInstaller, Test-ExactWindowsSdkInstalled, Wait-ExactWindowsSdkInstalled, Invoke-ElevatedInstaller, Test-MsvcUiLanguageInstalled, Invoke-WithScopedProcessEnvironment, Assert-CMakeMsvcEnglishShowIncludesPrefix, Install-CppPrerequisites, Assert-ExactToolchainMetadata, Get-ExactCppToolchain, Invoke-MsvcEvidence, Assert-CppRepositoryHygiene
