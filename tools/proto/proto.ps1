[CmdletBinding()]
# 该入口统一协议格式化、校验、生成和 fixture 生命周期，供本地开发与 CI 使用同一行为。
param(
    # Command 只允许已定义的非交互动作，未知拼写在执行任何生成前由 PowerShell 拒绝。
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("bootstrap", "format", "lint", "generate", "fixtures", "verify")]
    [string]$Command
)

$ErrorActionPreference = "Stop"
# Windows PowerShell 5.1 默认使用系统代码页写控制台；固定 UTF-8，保证终端和 CI 接收一致字节流。
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
# RepositoryRoot 是所有模板、契约源、生成物与项目工具路径的可信解析基准。
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
# LocalEnvironmentDirectory 只保存可重建且不进入 Git 的锁定 SDK、编译器、生成器与官方 schema。
$LocalEnvironmentDirectory = Join-Path $RepositoryRoot ".local"
# TemporaryDirectory 只承载资格验证期间的临时输出，命令结束前必须为空。
$TemporaryDirectory = Join-Path $RepositoryRoot ".tmp"
# CSharpStagingDirectory 隔离尚未通过结构与依赖校验的 C# 协议候选产物。
$CSharpStagingDirectory = Join-Path $TemporaryDirectory "client-protocol-stage"
# CSharpStagingProtocolDirectory 是校验成功后整体发布的目录，不允许混入其他临时文件。
$CSharpStagingProtocolDirectory = Join-Path $CSharpStagingDirectory "Protocol"
# CSharpStagingSourcesDirectory 必须与 buf.gen.csharp.yaml 的 out 保持一致。
$CSharpStagingSourcesDirectory = Join-Path $CSharpStagingProtocolDirectory "Sources"
# GeneratedClientProtocolDirectory 是 Unity 编译前必须由统一入口恢复的被忽略协议根。
$GeneratedClientProtocolDirectory = Join-Path $RepositoryRoot "client\Assets\App\Generated\Protocol"
# GeneratedGoDirectory 是服务端编译所需但不进入 Git 的 Go Protobuf code 根目录。
$GeneratedGoDirectory = Join-Path $RepositoryRoot "server\internal\generated\proto"
# CppStagingDirectory 隔离尚未完成结构校验的 C++ Protobuf candidate。
$CppStagingDirectory = Join-Path $TemporaryDirectory "simulation-protocol-stage"
# GeneratedCppDirectory 是 CMake configure 前必须存在、但不进入 Git 的 C++ 协议根。
$GeneratedCppDirectory = Join-Path $RepositoryRoot "simulation\out\generated\proto"
# GoLauncher 确保 contracttool 与测试始终使用项目 SDK，而不是当前终端的系统 Go。
$GoLauncher = Join-Path $RepositoryRoot "tools\go\go.ps1"
# ProtocolToolModule 承载可独立回归的路径、依赖和安全发布原语。
$ProtocolToolModule = Join-Path $PSScriptRoot "ProtocolTool.psm1"
Import-Module $ProtocolToolModule -Force
# 所有 Buf template 的相对路径都以仓库根为准，调用者当前目录不得改变生成结果。
Set-Location $RepositoryRoot

# Write-LogLabel 只为短状态标签着色，正文和工具原生输出保留默认颜色以控制视觉噪声。
function Write-LogLabel {
    param(
        [string]$Label,
        [string]$Message,
        [System.ConsoleColor]$Color
    )

    Write-Host "[$Label]" -NoNewline -ForegroundColor $Color
    Write-Host " $Message"
}

# Write-Step 为复合命令标记当前阶段；序号反映执行进度，文字说明该阶段验证的边界。
function Write-Step {
    param([int]$Current, [int]$Total, [string]$Message)

    Write-Host ""
    Write-Host "[$Current/$Total]" -NoNewline -ForegroundColor Cyan
    Write-Host " $Message"
}

# Write-CommandContext 展示命令与仓库根，使并行日志不依赖调用方界面也能独立辨认。
function Write-CommandContext {
    Write-Host ""
    Write-LogLabel "INFO" "Command: proto $Command" Cyan
    Write-LogLabel "INFO" "Repository: $RepositoryRoot" Cyan
}

# Buf 在 Windows 上执行格式比较时可能调用 Git 提供的 diff 工具。
$GitCommand = Get-Command git -ErrorAction SilentlyContinue
if ($null -ne $GitCommand) {
    $GitRoot = Split-Path (Split-Path $GitCommand.Source -Parent) -Parent
    $GitUtilities = Join-Path $GitRoot "usr\bin"
    if (Test-Path -LiteralPath $GitUtilities) {
        $env:PATH = "$GitUtilities;$env:PATH"
    }
}

# Read-VersionValue 在不为 bootstrap 引入第二个 YAML runtime 的前提下读取版本目录条目。
# Section、Name 与 Property 必须定位到带引号的二级节点属性；缺失值直接终止 bootstrap。
# 该窄读取器只用于工具尚未可用的阶段，完整目录结构仍由 Go contract validator 校验。
function Read-VersionValue {
    param([string]$Section, [string]$Name, [string]$Property)

    $lines = Get-Content -LiteralPath (Join-Path $RepositoryRoot "versions.yaml") -Encoding utf8
    $insideSection = $false
    $insideName = $false
    foreach ($line in $lines) {
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
        if ($insideName -and $line -match "^    $([regex]::Escape($Property)):\s*`"([^`"]+)`"") {
            return $Matches[1]
        }
    }
    throw "Version entry $Section.$Name.$Property was not found in versions.yaml"
}

# Read-Version 是调用最频繁的 version 属性快捷入口，避免每个安装器重复属性名。
function Read-Version {
    param([string]$Section, [string]$Name)

    return Read-VersionValue $Section $Name "version"
}

# Install-Buf 将可执行文件保留在 Git 之外，同时保证版本目录指定的版本可重复安装。
# 已存在文件只有在 --version 精确匹配时复用；旧版散落路径仅用于一次性迁移后立即清理。
# 返回值是本次命令唯一使用的 buf.exe 绝对路径，不修改用户级安装。
function Install-Buf {
    $version = Read-Version "toolchains" "buf_cli"
    $expectedHash = Read-VersionValue "toolchains" "buf_cli" "windows_amd64_sha256"
    $bufRoot = Join-Path $LocalEnvironmentDirectory ("buf\" + $version)
    $binary = Join-Path $bufRoot "buf.exe"
    $legacyBinary = Join-Path $LocalEnvironmentDirectory "buf.exe"
    $legacyExpanded = Join-Path $LocalEnvironmentDirectory "buf-dist"
    Assert-UnderLocalEnvironment $bufRoot
    New-Item -ItemType Directory -Force -Path $bufRoot | Out-Null
    if (-not (Test-Path -LiteralPath $binary) -and (Test-Path -LiteralPath $legacyBinary) -and ((& $legacyBinary --version) -eq $version)) {
        Move-Item -LiteralPath $legacyBinary -Destination $binary
    }
    if (Test-Path -LiteralPath $legacyExpanded) {
        $localRoot = [System.IO.Path]::GetFullPath($LocalEnvironmentDirectory).TrimEnd('\') + '\'
        $legacyPath = [System.IO.Path]::GetFullPath($legacyExpanded)
        if (-not $legacyPath.StartsWith($localRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw "Buf cleanup path escaped project local directory: $legacyPath"
        }
        Remove-Item -LiteralPath $legacyPath -Recurse -Force
    }
    if ((Test-Path -LiteralPath $binary) -and ((& $binary --version) -eq $version)) {
        Write-LogLabel "READY" "Buf $version" Green
        return $binary
    }
    $archive = Join-Path $bufRoot "buf.zip"
    $expanded = Join-Path $bufRoot (".extract-" + [guid]::NewGuid().ToString("N"))
    Assert-UnderLocalEnvironment $archive
    Assert-UnderLocalEnvironment $expanded
    try {
        Write-LogLabel "SETUP" "Installing Buf $version..." Cyan
        Invoke-WebRequest -Uri "https://github.com/bufbuild/buf/releases/download/v$version/buf-Windows-x86_64.zip" -OutFile $archive
        $actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
            throw "Buf archive checksum mismatch: expected $expectedHash, got $actualHash"
        }
        Expand-Archive -LiteralPath $archive -DestinationPath $expanded -Force
        Copy-Item -LiteralPath (Join-Path $expanded "buf\bin\buf.exe") -Destination $binary -Force
    }
    finally {
        if (Test-Path -LiteralPath $archive) {
            Remove-Item -LiteralPath $archive -Force
        }
        if (Test-Path -LiteralPath $expanded) {
            Remove-Item -LiteralPath $expanded -Recurse -Force
        }
    }
    if (-not (Test-Path -LiteralPath $binary) -or ((& $binary --version) -ne $version)) {
        throw "Buf installation did not produce version $version"
    }
    Write-LogLabel "OK" "Buf $version installed." Green
    return $binary
}

# Install-OpenAPISchema 缓存版本目录指定的官方 schema，用于完整 OpenAPI 验证。
# schema 按规范版本隔离，避免升级后错误复用旧结构；缓存缺失时才访问官方地址。
function Install-OpenAPISchema {
    $version = Read-Version "protocols" "openapi"
    $expectedHash = Read-VersionValue "protocols" "openapi" "schema_sha256"
    $minor = ($version -split '\.')[0..1] -join '.'
    $openAPIRoot = Join-Path $LocalEnvironmentDirectory ("openapi\" + $version)
    $schema = Join-Path $openAPIRoot "schema.json"
    $legacySchema = Join-Path $LocalEnvironmentDirectory "openapi-$version-schema.json"
    Assert-UnderLocalEnvironment $openAPIRoot
    Assert-UnderLocalEnvironment $schema
    New-Item -ItemType Directory -Force -Path $openAPIRoot | Out-Null
    if (-not (Test-Path -LiteralPath $schema) -and (Test-Path -LiteralPath $legacySchema)) {
        Move-Item -LiteralPath $legacySchema -Destination $schema
    }
    if (Test-Path -LiteralPath $schema) {
        $actualHash = (Get-FileHash -LiteralPath $schema -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
            Remove-Item -LiteralPath $schema -Force
        }
    }
    if (-not (Test-Path -LiteralPath $schema)) {
        Write-LogLabel "SETUP" "Caching the official OpenAPI $version schema..." Cyan
        Invoke-WebRequest -Uri "https://spec.openapis.org/oas/$minor/schema/2025-09-17" -OutFile $schema
        $actualHash = (Get-FileHash -LiteralPath $schema -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
            Remove-Item -LiteralPath $schema -Force
            throw "OpenAPI schema checksum mismatch: expected $expectedHash, got $actualHash"
        }
        Write-LogLabel "OK" "OpenAPI $version schema cached." Green
    }
    else {
        Write-LogLabel "READY" "OpenAPI $version schema" Green
    }
    return $schema
}

# Invoke-Go 委托项目 SDK 包装入口，确保协议命令不依赖或使用系统 Go。
# Arguments 原样传递；任何非零退出码都会转换为终止错误，防止流水线继续消费半成品。
function Invoke-Go {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)

    $previousNestedFlag = $env:IHOMELAND_GO_NESTED
    try {
        $env:IHOMELAND_GO_NESTED = "1"
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $GoLauncher @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "go $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        if ($null -eq $previousNestedFlag) {
            Remove-Item Env:IHOMELAND_GO_NESTED -ErrorAction SilentlyContinue
        }
        else {
            $env:IHOMELAND_GO_NESTED = $previousNestedFlag
        }
    }
}

# Install-GoGenerator 使用项目 Go SDK 安装版本目录锁定的 protoc-gen-go，Go generation 不经过 protoc。
# 可执行文件按 generator/version 隔离在 .local，并在版本精确匹配时复用。
function Install-GoGenerator {
    $version = Read-Version "toolchains" "protobuf_go_generator"
    $root = Join-Path $LocalEnvironmentDirectory ("protoc-gen-go\" + $version)
    $binary = Join-Path $root "protoc-gen-go.exe"
    if (Test-Path -LiteralPath $binary) {
        $reportedVersion = [string](& $binary --version)
        $versionPattern = '^protoc-gen-go(?:\.exe)? v{0}$' -f [regex]::Escape($version)
        if ($LASTEXITCODE -eq 0 -and $reportedVersion -match $versionPattern) {
            Write-LogLabel "READY" "protoc-gen-go $version" Green
            return $binary
        }
    }
    Assert-UnderLocalEnvironment $root
    New-Item -ItemType Directory -Force -Path $root | Out-Null
    if (Test-Path -LiteralPath $binary) {
        Remove-Item -LiteralPath $binary -Force
    }
    $previousGoBin = $env:GOBIN
    try {
        Write-LogLabel "SETUP" "Installing protoc-gen-go $version..." Cyan
        $env:GOBIN = $root
        Invoke-Go install "google.golang.org/protobuf/cmd/protoc-gen-go@v$version"
    }
    finally {
        if ($null -eq $previousGoBin) {
            Remove-Item Env:GOBIN -ErrorAction SilentlyContinue
        }
        else {
            $env:GOBIN = $previousGoBin
        }
    }
    if (-not (Test-Path -LiteralPath $binary)) {
        throw "protoc-gen-go installation did not produce $binary"
    }
    Write-LogLabel "OK" "protoc-gen-go $version installed." Green
    return $binary
}

# Install-Protoc 下载并校验版本目录锁定的官方 Windows compiler，供 Buf 内置 C# generator 使用。
# 完整发行目录按版本保留 bin 与 include；下载、校验和替换均限制在项目 .local/protoc 内。
function Install-Protoc {
    $version = Read-Version "toolchains" "protoc"
    $expectedHash = Read-VersionValue "toolchains" "protoc" "windows_amd64_sha256"
    $parent = Join-Path $LocalEnvironmentDirectory "protoc"
    $root = Join-Path $parent $version
    $binary = Join-Path $root "bin\protoc.exe"
    if ((Test-Path -LiteralPath $binary) -and ((& $binary --version) -eq "libprotoc $version")) {
        Write-LogLabel "READY" "protoc $version" Green
        return $binary
    }

    Assert-UnderLocalEnvironment $parent
    Assert-UnderLocalEnvironment $root
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $archive = Join-Path $parent ("protoc-" + $version + "-win64.zip")
    $expanded = Join-Path $parent (".extract-" + [guid]::NewGuid().ToString("N"))
    try {
        Write-LogLabel "SETUP" "Installing protoc $version..." Cyan
        Invoke-WebRequest -Uri "https://github.com/protocolbuffers/protobuf/releases/download/v$version/protoc-$version-win64.zip" -OutFile $archive
        $actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
            throw "protoc archive checksum mismatch: expected $expectedHash, got $actualHash"
        }
        Expand-Archive -LiteralPath $archive -DestinationPath $expanded -Force
        $expandedBinary = Join-Path $expanded "bin\protoc.exe"
        if (-not (Test-Path -LiteralPath $expandedBinary) -or ((& $expandedBinary --version) -ne "libprotoc $version")) {
            throw "protoc archive did not contain the locked compiler version $version"
        }
        if (Test-Path -LiteralPath $root) {
            Remove-Item -LiteralPath $root -Recurse -Force
        }
        Move-Item -LiteralPath $expanded -Destination $root
    }
    finally {
        if (Test-Path -LiteralPath $archive) {
            Remove-Item -LiteralPath $archive -Force
        }
        if (Test-Path -LiteralPath $expanded) {
            Remove-Item -LiteralPath $expanded -Recurse -Force
        }
    }
    Write-LogLabel "OK" "protoc $version installed." Green
    return $binary
}

# Assert-UnderLocalEnvironment 防止工具安装、替换或清理路径逃逸出已忽略的 .local 目录。
function Assert-UnderLocalEnvironment {
    param([string]$Path)

    $root = [System.IO.Path]::GetFullPath($LocalEnvironmentDirectory).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Tool path escaped project local environment: $candidate"
    }
}

# Assert-UnderRepository 防止生成清理路径逃逸出仓库，所有递归删除都必须先通过该检查。
function Assert-UnderRepository {
    param([string]$Path)

    $root = [System.IO.Path]::GetFullPath($RepositoryRoot).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Generated path escaped repository: $candidate"
    }
}

# Remove-GeneratedDirectory 清理完整生成根，避免 schema 删除后遗留旧类型继续通过编译。
function Remove-GeneratedDirectory {
    param([string]$Path)

    Assert-UnderRepository $Path
    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Recurse -Force
    }
}

# Remove-CSharpStagingOutput 只清理尚未发布的 C# staging，不触碰 Unity 中最后一次完整生成结果。
function Remove-CSharpStagingOutput {
    Assert-UnderRepository $CSharpStagingDirectory
    if (Test-Path -LiteralPath $CSharpStagingDirectory) {
        Remove-Item -LiteralPath $CSharpStagingDirectory -Recurse -Force
    }
    if ((Test-Path -LiteralPath $TemporaryDirectory) -and ((Get-ChildItem -LiteralPath $TemporaryDirectory -Force | Measure-Object).Count -eq 0)) {
        Remove-Item -LiteralPath $TemporaryDirectory
    }
}

# Remove-CppStagingOutput 只清理 C++ candidate，不触碰最后一次已验证生成结果。
function Remove-CppStagingOutput {
    Assert-UnderRepository $CppStagingDirectory
    if (Test-Path -LiteralPath $CppStagingDirectory) {
        Remove-Item -LiteralPath $CppStagingDirectory -Recurse -Force
    }
    if ((Test-Path -LiteralPath $TemporaryDirectory) -and ((Get-ChildItem -LiteralPath $TemporaryDirectory -Force | Measure-Object).Count -eq 0)) {
        Remove-Item -LiteralPath $TemporaryDirectory
    }
}

# Get-GoogleProtobufRuntimeSettings 从唯一版本目录构造恢复和校验所需的完整只读配置。
function Get-GoogleProtobufRuntimeSettings {
    $version = Read-Version "libraries" "google_protobuf_csharp"
    $cacheDirectory = Join-Path $LocalEnvironmentDirectory ("nuget\google.protobuf\" + $version)
    $targetFramework = Read-VersionValue "libraries" "google_protobuf_csharp" "target_framework"
    $dllPath = Read-VersionValue "libraries" "google_protobuf_csharp" "dll_path"
    if ($dllPath -ne "lib/$targetFramework/Google.Protobuf.dll") {
        throw "Google.Protobuf dll_path does not match target_framework in versions.yaml"
    }
    return [pscustomobject]@{
        Version = $version
        PackageURL = Read-VersionValue "libraries" "google_protobuf_csharp" "package_url"
        PackageSHA256 = Read-VersionValue "libraries" "google_protobuf_csharp" "package_sha256"
        TargetFramework = $targetFramework
        DLLPath = $dllPath
        AssemblyName = Read-VersionValue "libraries" "google_protobuf_csharp" "assembly_name"
        AssemblyVersion = Read-VersionValue "libraries" "google_protobuf_csharp" "assembly_version"
        PublicKeyToken = Read-VersionValue "libraries" "google_protobuf_csharp" "public_key_token"
        CacheDirectory = $cacheDirectory
        PackagePath = Join-Path $cacheDirectory "package.nupkg"
    }
}

# Install-GoogleProtobufRuntime 恢复并验证 Unity 生成程序集唯一允许引用的官方 C# runtime。
function Install-GoogleProtobufRuntime {
    $settings = Get-GoogleProtobufRuntimeSettings
    Write-LogLabel "SETUP" "Preparing Google.Protobuf $($settings.Version)..." Cyan
    $dll = Restore-VerifiedNuGetAssembly `
        -PackageURL $settings.PackageURL `
        -ExpectedSHA256 $settings.PackageSHA256 `
        -CacheDirectory $settings.CacheDirectory `
        -LocalEnvironmentRoot $LocalEnvironmentDirectory `
        -DLLPath $settings.DLLPath `
        -AssemblyName $settings.AssemblyName `
        -AssemblyVersion $settings.AssemblyVersion `
        -PublicKeyToken $settings.PublicKeyToken
    Write-LogLabel "READY" "Google.Protobuf $($settings.Version)" Green
    return $dll
}

# Write-GeneratedProtocolAssemblyDefinition 生成稳定 asmdef，使手写程序集不依赖 Assembly-CSharp。
# overrideReferences 固定 Google.Protobuf 来源，noEngineReferences 保持协议模型与 UnityEngine 解耦。
function Write-GeneratedProtocolAssemblyDefinition {
    $path = Join-Path $CSharpStagingProtocolDirectory "IHomeland.Client.Protocol.Generated.asmdef"
    $contents = @'
{
  "name": "IHomeland.Client.Protocol.Generated",
  "rootNamespace": "IHomeland.Protocol",
  "references": [],
  "includePlatforms": [],
  "excludePlatforms": [],
  "allowUnsafeCode": false,
  "overrideReferences": true,
  "precompiledReferences": [
    "Google.Protobuf.dll"
  ],
  "autoReferenced": false,
  "defineConstraints": [],
  "versionDefines": [],
  "noEngineReferences": true
}
'@
    [System.IO.File]::WriteAllText($path, $contents.TrimStart() + "`n", [System.Text.UTF8Encoding]::new($false))
}

# Assert-CSharpProtocolStage 通过生成头中的 source 和 Proto 声明验证一一对应关系。
# 校验不写死 package 或文件命名算法，使后续合法 schema 仍只受 csharp_namespace 根边界约束。
function Assert-CSharpProtocolStage {
    $generatedFiles = @(Get-ChildItem -LiteralPath $CSharpStagingSourcesDirectory -Recurse -Filter *.cs)
    $protoRoot = Join-Path $RepositoryRoot "shared\proto"
    $protoRootPath = [System.IO.Path]::GetFullPath($protoRoot).TrimEnd('\') + '\'
    $protoFiles = @(Get-ChildItem -LiteralPath $protoRoot -Recurse -Filter *.proto)
    if ($generatedFiles.Count -ne $protoFiles.Count) {
        throw "C# generation produced $($generatedFiles.Count) files for $($protoFiles.Count) Proto sources"
    }

    $protoBySource = [System.Collections.Generic.Dictionary[string, System.IO.FileInfo]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($protoFile in $protoFiles) {
        $relativeSource = $protoFile.FullName.Substring($protoRootPath.Length).Replace('\', '/')
        if ($protoBySource.ContainsKey($relativeSource)) {
            throw "Proto source path is duplicated: $relativeSource"
        }
        $protoBySource.Add($relativeSource, $protoFile)
    }

    $seenSources = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($file in $generatedFiles) {
        $generatedSource = Get-Content -LiteralPath $file.FullName -Raw -Encoding utf8
        $sourceMatch = [regex]::Match($generatedSource, '(?m)^//\s+source:\s+(?<path>[^\r\n]+)\r?$')
        if (-not $sourceMatch.Success) {
            throw "C# generated file does not declare its Proto source: $($file.FullName)"
        }
        $declaredSource = $sourceMatch.Groups['path'].Value.Trim().Replace('\', '/')
        if (-not $protoBySource.ContainsKey($declaredSource)) {
            throw "C# generated file declares an unknown Proto source: $declaredSource"
        }
        if (-not $seenSources.Add($declaredSource)) {
            throw "C# generation produced duplicate output for Proto source: $declaredSource"
        }

        $protoSource = Get-Content -LiteralPath $protoBySource[$declaredSource].FullName -Raw -Encoding utf8
        $optionMatch = [regex]::Match(
            $protoSource,
            '(?m)^\s*option\s+csharp_namespace\s*=\s*"(?<namespace>[^"]+)"\s*;\s*$')
        if (-not $optionMatch.Success) {
            throw "Proto source does not declare csharp_namespace: $declaredSource"
        }
        $expectedNamespace = $optionMatch.Groups['namespace'].Value
        if (-not $expectedNamespace.StartsWith('IHomeland.Protocol.', [System.StringComparison]::Ordinal)) {
            throw "Proto csharp_namespace is outside IHomeland.Protocol: $declaredSource"
        }
        $namespaceMatches = [regex]::Matches(
            $generatedSource,
            '(?m)^namespace\s+(?<namespace>[A-Za-z_][A-Za-z0-9_.]*)\s*\{?\s*$')
        if ($namespaceMatches.Count -ne 1 -or
            $namespaceMatches[0].Groups['namespace'].Value -ne $expectedNamespace) {
            throw "C# generated namespace does not match $declaredSource"
        }
    }

    foreach ($relativeSource in $protoBySource.Keys) {
        if (-not $seenSources.Contains($relativeSource)) {
            throw "C# generation omitted Proto source: $relativeSource"
        }
    }

    $sourcesRootPath = [System.IO.Path]::GetFullPath($CSharpStagingSourcesDirectory).TrimEnd('\') + '\'
    $expectedPaths = @($generatedFiles | ForEach-Object {
        "Sources/" + $_.FullName.Substring($sourcesRootPath.Length).Replace('\', '/')
    })
    $expectedPaths += "IHomeland.Client.Protocol.Generated.asmdef"
    $expectedPaths += "Runtime/Google.Protobuf.dll"
    $rootPath = [System.IO.Path]::GetFullPath($CSharpStagingProtocolDirectory).TrimEnd('\') + '\'
    $actualPaths = @(Get-ChildItem -LiteralPath $CSharpStagingProtocolDirectory -Recurse -File | ForEach-Object {
        $_.FullName.Substring($rootPath.Length).Replace('\', '/')
    } | Sort-Object)
    if (Compare-Object -ReferenceObject @($expectedPaths | Sort-Object) -DifferenceObject $actualPaths) {
        throw "C# protocol stage contains missing or unowned files"
    }
    return $generatedFiles.Count
}

# Assert-CppProtocolStage 要求每个 Proto source 精确产生一组 .pb.cc/.pb.h，禁止残留插件副产物。
function Assert-CppProtocolStage {
    $protoRoot = Join-Path $RepositoryRoot "shared\proto"
    $protoRootPath = [System.IO.Path]::GetFullPath($protoRoot).TrimEnd('\') + '\'
    $stageRootPath = [System.IO.Path]::GetFullPath($CppStagingDirectory).TrimEnd('\') + '\'
    $expected = @()
    foreach ($protoFile in Get-ChildItem -LiteralPath $protoRoot -Recurse -Filter *.proto) {
        $relative = $protoFile.FullName.Substring($protoRootPath.Length).Replace('\', '/')
        $stem = $relative.Substring(0, $relative.Length - ".proto".Length)
        $expected += "$stem.pb.cc"
        $expected += "$stem.pb.h"
    }
    $actual = @(Get-ChildItem -LiteralPath $CppStagingDirectory -Recurse -File | ForEach-Object {
        $_.FullName.Substring($stageRootPath.Length).Replace('\', '/')
    })
    if (Compare-Object -ReferenceObject @($expected | Sort-Object) -DifferenceObject @($actual | Sort-Object)) {
        throw "C++ protocol stage contains missing or unowned files"
    }
    foreach ($path in $actual) {
        $text = Get-Content -LiteralPath (Join-Path $CppStagingDirectory $path) -Raw -Encoding UTF8
        if ($text -notmatch '(?m)^// Generated by the protocol buffer compiler\.  DO NOT EDIT!$') {
            throw "C++ protocol artifact 缺少 generated ownership header：$path"
        }
    }
    return $actual.Count
}

# Invoke-GoGeneration 从已提交 schema 重建被忽略的 Go code。
# 每次执行先清空生成根，确保删除或移动 schema 后不会残留幽灵类型。
function Invoke-GoGeneration {
    param([string]$Buf)

    Write-LogLabel "RUN" "Generating server Go Protobuf code..." Cyan
    Remove-GeneratedDirectory $GeneratedGoDirectory
    $generator = Install-GoGenerator
    $previousPath = $env:PATH
    try {
        $env:PATH = "$(Split-Path $generator -Parent);$previousPath"
        & $Buf generate --template (Join-Path $RepositoryRoot "tools\proto\buf.gen.go.yaml")
        if ($LASTEXITCODE -ne 0) { throw "Go protocol generation failed" }
    }
    finally {
        $env:PATH = $previousPath
    }
    $goCount = (Get-ChildItem -LiteralPath $GeneratedGoDirectory -Recurse -Filter *.go | Measure-Object).Count
    if ($goCount -eq 0) {
        throw "Go protocol generation produced no files"
    }
    Invoke-Go fmt ./...
    Write-LogLabel "OK" "$goCount Go files generated and formatted." Green
}

# Invoke-CSharpGeneration 由 Buf 调用项目内 protoc，在 staging 完整验证后发布 Unity 协议目录。
# 失败只清理 staging，不能破坏最后一次完整产物；目标目录中的 .meta 不属于工具事实且不会被复制。
function Invoke-CSharpGeneration {
    param([string]$Buf)

    Write-LogLabel "RUN" "Generating Unity C# Protobuf protocol..." Cyan
    Remove-CSharpStagingOutput
    Install-Protoc | Out-Null
    try {
        & $Buf generate --template (Join-Path $RepositoryRoot "tools\proto\buf.gen.csharp.yaml")
        if ($LASTEXITCODE -ne 0) { throw "C# protocol generation failed" }
        $runtimeDirectory = Join-Path $CSharpStagingProtocolDirectory "Runtime"
        New-Item -ItemType Directory -Force -Path $runtimeDirectory | Out-Null
        $runtime = Install-GoogleProtobufRuntime
        Copy-Item -LiteralPath $runtime -Destination (Join-Path $runtimeDirectory "Google.Protobuf.dll") -Force
        Write-GeneratedProtocolAssemblyDefinition
        $generatedCount = Assert-CSharpProtocolStage
        Publish-GeneratedDirectory -Stage $CSharpStagingProtocolDirectory -Target $GeneratedClientProtocolDirectory -BackupRoot $TemporaryDirectory -RepositoryRoot $RepositoryRoot
        Write-LogLabel "OK" "$generatedCount C# files, asmdef, and Google.Protobuf runtime published." Green
    }
    finally {
        Remove-CSharpStagingOutput
    }
}

# Invoke-CppGeneration 由同一 Buf/protoc identity 生成并原子发布 C++ Protobuf lite source。
function Invoke-CppGeneration {
    param([string]$Buf)

    Write-LogLabel "RUN" "Generating simulation C++ Protobuf lite code..." Cyan
    Remove-CppStagingOutput
    Install-Protoc | Out-Null
    try {
        & $Buf generate --template (Join-Path $RepositoryRoot "tools\proto\buf.gen.cpp.yaml")
        if ($LASTEXITCODE -ne 0) { throw "C++ protocol generation failed" }
        $generatedCount = Assert-CppProtocolStage
        Publish-GeneratedDirectory -Stage $CppStagingDirectory -Target $GeneratedCppDirectory -BackupRoot $TemporaryDirectory -RepositoryRoot $RepositoryRoot
        Write-LogLabel "OK" "$generatedCount C++ Protobuf lite files published." Green
    }
    finally {
        Remove-CppStagingOutput
    }
}

# Invoke-Generation 统一重建服务端 Go、客户端 Unity C# 与 Simulation C++ 协议产物。
function Invoke-Generation {
    param([string]$Buf)

    Invoke-GoGeneration $Buf
    Invoke-CSharpGeneration $Buf
    Invoke-CppGeneration $Buf
}

# Resolve-BreakingAgainst 选择协议兼容基线；CI 必须显式传入目标，本地默认使用当前 HEAD。
# 首次建立协议且 HEAD 尚无 shared/proto 时返回空值，因为此时不存在可比较的旧契约。
function Resolve-BreakingAgainst {
    if (-not [string]::IsNullOrWhiteSpace($env:IHOMELAND_BREAKING_AGAINST)) {
        return $env:IHOMELAND_BREAKING_AGAINST
    }
    if (-not [string]::IsNullOrWhiteSpace($env:CI)) {
        throw "CI must set IHOMELAND_BREAKING_AGAINST to the pull request base Git input"
    }
    $head = (& git -C $RepositoryRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($head)) {
        throw "Unable to resolve Git HEAD for breaking validation"
    }
    $baselinePath = [string](& git -C $RepositoryRoot ls-tree -d --name-only $head -- shared/proto)
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to inspect Git HEAD for a schema baseline"
    }
    if ([string]::IsNullOrWhiteSpace($baselinePath)) {
        return $null
    }
    return ".git#commit=$head"
}

# Invoke-BreakingCheck 直接比较 Git 中的 Proto 源，避免提交可重复生成的 descriptor binary。
function Invoke-BreakingCheck {
    param([string]$Buf)

    $against = Resolve-BreakingAgainst
    if ([string]::IsNullOrWhiteSpace($against)) {
        Write-LogLabel "SKIP" "Buf breaking check: no committed schema baseline exists yet." Yellow
        return
    }
    Push-Location $RepositoryRoot
    try {
        & $Buf breaking --against $against
        if ($LASTEXITCODE -ne 0) { throw "Buf breaking validation failed against $against" }
        Write-LogLabel "OK" "Protocol compatibility passed against $against." Green
    }
    finally {
        Pop-Location
    }
}

# Assert-NoTrackedGeneratedArtifacts 阻止 force-add 绕过 .gitignore 并污染共享历史。
# fixtures、registry 和 lock/checksum 不在禁止列表，因为它们承担兼容性或依赖基线职责。
function Assert-NoTrackedGeneratedArtifacts {
    $tracked = @(& git -C $RepositoryRoot ls-files -- `
        "server/internal/generated" `
        "client/Assets/App/Generated" `
        "client/Assets/App/Generated.meta" `
        "simulation/out/generated/proto" `
        "simulation/generated" `
        "shared/contracts/descriptor.bin" `
        "shared/contracts/registry/projection.json")
    if ($LASTEXITCODE -ne 0) { throw "Unable to inspect tracked generated artifacts" }
    if ($tracked.Count -ne 0) {
        throw "Generated artifacts must not be tracked: $($tracked -join ', ')"
    }
}

# Get-ArtifactHashes 对被忽略的 Go 生成代码创建摘要快照，不要求产物进入 Git。
# 返回的有序映射以绝对路径为键，使前后两次生成可以同时发现内容和文件集合漂移。
function Get-ArtifactHashes {
    $hashes = [ordered]@{}
    if (Test-Path -LiteralPath $GeneratedGoDirectory) {
        Get-ChildItem -LiteralPath $GeneratedGoDirectory -Recurse -File | Sort-Object FullName | ForEach-Object {
            $hashes[$_.FullName] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
        }
    }
    if ($hashes.Count -eq 0) { throw "Generated Go artifact set is empty" }
    return $hashes
}

# Invoke-ProtocolToolTests 在隔离 PowerShell 进程中回归缓存损坏、下载失败、结构漂移和路径逃逸。
function Invoke-ProtocolToolTests {
    $settings = Get-GoogleProtobufRuntimeSettings
    Install-GoogleProtobufRuntime | Out-Null
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "proto.tests.ps1") `
        -RepositoryRoot $RepositoryRoot `
        -ValidPackagePath $settings.PackagePath `
        -ExpectedSHA256 $settings.PackageSHA256 `
        -DLLPath $settings.DLLPath `
        -AssemblyName $settings.AssemblyName `
        -AssemblyVersion $settings.AssemblyVersion `
        -PublicKeyToken $settings.PublicKeyToken
    if ($LASTEXITCODE -ne 0) {
        throw "Protocol PowerShell tool tests failed with exit code $LASTEXITCODE"
    }
}

# Assert-HashesEqual 定位相同输入重复运行时首个发生变化的生成产物。
# Before 与 After 必须来自同一仓库路径；数量或任一 SHA-256 不同都表示生成不确定。
function Assert-HashesEqual {
    param([System.Collections.IDictionary]$Before, [System.Collections.IDictionary]$After)

    if ($Before.Count -ne $After.Count) {
        throw "Generated artifact count changed from $($Before.Count) to $($After.Count)"
    }
    foreach ($key in $Before.Keys) {
        if (-not $After.Contains($key) -or $Before[$key] -ne $After[$key]) {
            throw "Generated artifact is not deterministic: $key"
        }
    }
}

# Invoke-ProtocolCommand 编排面向开发者的六个稳定动作，并为复合动作提供可定位的阶段日志。
# 底层函数仍负责具体失败条件；该层只定义顺序、进度和成功摘要，不吞掉工具原生诊断。
function Invoke-ProtocolCommand {
    Write-CommandContext

    if ($Command -eq "bootstrap") {
        Write-Step 1 5 "Preparing Buf CLI..."
        Install-Buf | Out-Null
        Write-Step 2 5 "Preparing the official OpenAPI schema..."
        Install-OpenAPISchema | Out-Null
        Write-Step 3 5 "Preparing the C# protocol compiler..."
        Install-Protoc | Out-Null
        Write-Step 4 5 "Preparing the C# Protobuf runtime..."
        Install-GoogleProtobufRuntime | Out-Null
        Write-Step 5 5 "Checking the project Go SDK..."
        Invoke-Go version
        Write-Host ""
        Write-LogLabel "OK" "Protocol development environment is ready." Green
        return
    }

    # buf 是本次进程锁定的唯一 Buf CLI，后续分支不得重新解析 PATH。
    $buf = Install-Buf

    switch ($Command) {
        "format" {
            Write-Step 1 1 "Formatting Proto sources..."
            & $buf format -w
            if ($LASTEXITCODE -ne 0) { throw "Buf format failed" }
            Write-LogLabel "OK" "Proto formatting completed." Green
        }
        "lint" {
            Write-Step 1 1 "Checking Proto style and structure..."
            & $buf lint
            if ($LASTEXITCODE -ne 0) { throw "Buf lint failed" }
            Write-LogLabel "OK" "Proto lint passed." Green
        }
        "generate" {
            Write-Step 1 3 "Generating server Go protocol code..."
            Invoke-GoGeneration $buf
            Write-Step 2 3 "Generating client C# protocol assembly..."
            Invoke-CSharpGeneration $buf
            Write-Step 3 3 "Generating simulation C++ Protobuf lite code..."
            Invoke-CppGeneration $buf
            Write-Host ""
            Write-LogLabel "OK" "Protocol code generation completed." Green
        }
        "fixtures" {
            Write-Step 1 2 "Generating server Go protocol code..."
            Invoke-GoGeneration $buf
            Write-Step 2 2 "Updating protocol compatibility fixtures..."
            Invoke-Go run ./cmd/contracttool -root $RepositoryRoot fixtures
            Write-LogLabel "OK" "Protocol fixtures updated." Green
        }
        "verify" {
            Write-Step 1 9 "Checking Proto formatting and lint rules..."
            $schema = Install-OpenAPISchema
            & $buf format --diff --exit-code
            if ($LASTEXITCODE -ne 0) { throw "Buf format verification failed" }
            & $buf lint
            if ($LASTEXITCODE -ne 0) { throw "Buf lint failed" }
            Write-LogLabel "OK" "Proto formatting and lint passed." Green

            Write-Step 2 9 "Checking backward compatibility..."
            Invoke-BreakingCheck $buf

            Write-Step 3 9 "Rebuilding Go, Unity C#, and Simulation C++ protocol code..."
            Invoke-Generation $buf

            Write-Step 4 9 "Validating registry, HTTP contracts, and OpenAPI schema..."
            Invoke-Go run ./cmd/contracttool -root $RepositoryRoot -openapi-schema $schema validate
            & (Join-Path $RepositoryRoot "tools\secure-battle-transport\secure-battle-transport.ps1") validate-corpus
            if ($LASTEXITCODE -ne 0) { throw "Secure battle corpus and architecture validation failed" }
            Write-LogLabel "OK" "Protocol and HTTP contracts passed." Green

            Write-Step 5 9 "Checking that rebuildable outputs are not tracked..."
            Assert-NoTrackedGeneratedArtifacts
            Write-LogLabel "OK" "No forbidden generated artifacts are tracked." Green

            Write-Step 6 9 "Checking compatibility fixtures against current contracts..."
            Invoke-Go run ./cmd/contracttool -root $RepositoryRoot verify-fixtures
            Write-LogLabel "OK" "Protocol fixtures passed." Green

            Write-Step 7 9 "Checking deterministic Go, C#, and C++ protocol generation..."
            $goBefore = Get-ArtifactHashes
            $csharpBefore = Get-DirectoryManifest -Root $GeneratedClientProtocolDirectory -ExcludedExtensions @(".meta")
            $cppBefore = Get-DirectoryManifest -Root $GeneratedCppDirectory
            Invoke-Generation $buf
            $goAfter = Get-ArtifactHashes
            $csharpAfter = Get-DirectoryManifest -Root $GeneratedClientProtocolDirectory -ExcludedExtensions @(".meta")
            $cppAfter = Get-DirectoryManifest -Root $GeneratedCppDirectory
            Assert-HashesEqual $goBefore $goAfter
            Assert-DirectoryManifestsEqual -Before $csharpBefore -After $csharpAfter
            Assert-DirectoryManifestsEqual -Before $cppBefore -After $cppAfter
            Write-LogLabel "OK" "$($goAfter.Count) Go, $($csharpAfter.Count) C#, and $($cppAfter.Count) C++ protocol artifacts are deterministic." Green

            Write-Step 8 9 "Running protocol tool regression tests..."
            Invoke-ProtocolToolTests

            Write-Step 9 9 "Running server Go tests..."
            Invoke-Go test ./...
            if (Test-Path -LiteralPath $CSharpStagingDirectory) { throw "C# staging output was not removed" }
            if (Test-Path -LiteralPath $CppStagingDirectory) { throw "C++ staging output was not removed" }
            Write-Host ""
            Write-LogLabel "OK" "Protocol contract verification passed." Green
        }
    }
}

# 顶层错误边界将 PowerShell 异常转换成稳定的红色失败摘要，同时保留非零退出码供 IDE 与 CI 判断。
try {
    Invoke-ProtocolCommand
    exit 0
}
catch {
    Write-Host ""
    Write-LogLabel "FAIL" $_.Exception.Message Red
    exit 1
}
