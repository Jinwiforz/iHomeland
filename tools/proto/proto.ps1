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
# TemporaryCSharpDirectory 隔离不进入 Unity 工程的 C# 生成验证结果。
$TemporaryCSharpDirectory = Join-Path $TemporaryDirectory "csharp"
# GeneratedGoDirectory 是服务端编译所需但不进入 Git 的 Go Protobuf code 根目录。
$GeneratedGoDirectory = Join-Path $RepositoryRoot "server\internal\generated\proto"
# GoLauncher 确保 contracttool 与测试始终使用项目 SDK，而不是当前终端的系统 Go。
$GoLauncher = Join-Path $RepositoryRoot "tools\go\go.ps1"
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

# Remove-CSharpTemporaryOutput 只清理 C# 临时子目录，不触碰其他工具可能使用的 .tmp 内容。
function Remove-CSharpTemporaryOutput {
    Assert-UnderRepository $TemporaryCSharpDirectory
    if (Test-Path -LiteralPath $TemporaryCSharpDirectory) {
        Remove-Item -LiteralPath $TemporaryCSharpDirectory -Recurse -Force
    }
    if ((Test-Path -LiteralPath $TemporaryDirectory) -and ((Get-ChildItem -LiteralPath $TemporaryDirectory -Force | Measure-Object).Count -eq 0)) {
        Remove-Item -LiteralPath $TemporaryDirectory
    }
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

# Invoke-CSharpGeneration 由 Buf 调用项目内 protoc，在临时目录验证 C# template。
# 输出数量、文件名与 namespace 必须和 Proto 源一致；无论成功或失败都会清理临时 code。
function Invoke-CSharpGeneration {
    param([string]$Buf)

    Write-LogLabel "RUN" "Validating Unity C# Protobuf generation..." Cyan
    Remove-CSharpTemporaryOutput
    Install-Protoc | Out-Null
    try {
        & $Buf generate --template (Join-Path $RepositoryRoot "tools\proto\buf.gen.csharp.yaml")
        if ($LASTEXITCODE -ne 0) { throw "C# protocol generation failed" }
        $generatedFiles = @(Get-ChildItem -LiteralPath $TemporaryCSharpDirectory -Recurse -Filter *.cs)
        $protoFiles = @(Get-ChildItem -LiteralPath (Join-Path $RepositoryRoot "shared\proto") -Recurse -Filter *.proto)
        if ($generatedFiles.Count -ne $protoFiles.Count) {
            throw "C# generation produced $($generatedFiles.Count) files for $($protoFiles.Count) Proto sources"
        }
        $expectedNames = @($protoFiles | ForEach-Object {
            [char]::ToUpperInvariant($_.BaseName[0]) + $_.BaseName.Substring(1) + ".cs"
        } | Sort-Object)
        $actualNames = @($generatedFiles | ForEach-Object { $_.Name } | Sort-Object)
        if (Compare-Object -ReferenceObject $expectedNames -DifferenceObject $actualNames) {
            throw "C# generated file names do not match Proto source names"
        }
        foreach ($file in $generatedFiles) {
            $source = Get-Content -LiteralPath $file.FullName -Raw -Encoding utf8
            if ($source -notmatch '(?m)^namespace IHomeland\.Protocol\.(Common|Account|Session|Control|World|Visit)\.V1\s*\{?\s*$') {
                throw "C# generated namespace is outside the protocol root: $($file.FullName)"
            }
        }
        Write-LogLabel "OK" "$($generatedFiles.Count) C# files and namespaces validated; temporary output will be removed." Green
    }
    finally {
        Remove-CSharpTemporaryOutput
    }
}

# Invoke-Generation 统一执行服务端 Go generation 与客户端 C# generation 资格验证。
function Invoke-Generation {
    param([string]$Buf)

    Invoke-GoGeneration $Buf
    Invoke-CSharpGeneration $Buf
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
        Write-Step 1 4 "Preparing Buf CLI..."
        Install-Buf | Out-Null
        Write-Step 2 4 "Preparing the official OpenAPI schema..."
        Install-OpenAPISchema | Out-Null
        Write-Step 3 4 "Preparing the C# protocol compiler..."
        Install-Protoc | Out-Null
        Write-Step 4 4 "Checking the project Go SDK..."
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
            Write-Step 1 2 "Generating server Go protocol code..."
            Invoke-GoGeneration $buf
            Write-Step 2 2 "Validating client C# protocol generation..."
            Invoke-CSharpGeneration $buf
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
            Write-Step 1 8 "Checking Proto formatting and lint rules..."
            $schema = Install-OpenAPISchema
            & $buf format --diff --exit-code
            if ($LASTEXITCODE -ne 0) { throw "Buf format verification failed" }
            & $buf lint
            if ($LASTEXITCODE -ne 0) { throw "Buf lint failed" }
            Write-LogLabel "OK" "Proto formatting and lint passed." Green

            Write-Step 2 8 "Checking backward compatibility..."
            Invoke-BreakingCheck $buf

            Write-Step 3 8 "Rebuilding Go code and validating C# generation..."
            Invoke-Generation $buf

            Write-Step 4 8 "Validating registry, HTTP contracts, and OpenAPI schema..."
            Invoke-Go run ./cmd/contracttool -root $RepositoryRoot -openapi-schema $schema validate
            Write-LogLabel "OK" "Protocol and HTTP contracts passed." Green

            Write-Step 5 8 "Checking that rebuildable outputs are not tracked..."
            Assert-NoTrackedGeneratedArtifacts
            Write-LogLabel "OK" "No forbidden generated artifacts are tracked." Green

            Write-Step 6 8 "Checking compatibility fixtures against current contracts..."
            Invoke-Go run ./cmd/contracttool -root $RepositoryRoot verify-fixtures
            Write-LogLabel "OK" "Protocol fixtures passed." Green

            Write-Step 7 8 "Checking deterministic Go protocol generation..."
            $before = Get-ArtifactHashes
            Invoke-GoGeneration $buf
            $after = Get-ArtifactHashes
            Assert-HashesEqual $before $after
            Write-LogLabel "OK" "$($after.Count) generated files are deterministic." Green

            Write-Step 8 8 "Running server Go tests..."
            Invoke-Go test ./...
            if (Test-Path -LiteralPath $TemporaryCSharpDirectory) { throw "C# temporary output was not removed" }
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
