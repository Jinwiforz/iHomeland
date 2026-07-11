[CmdletBinding()]
# 该入口为仓库提供唯一 Go SDK 解析路径，避免系统 PATH 和 Go 自动工具链改变构建结果。
# 首次运行允许下载已锁定 SDK；之后所有参数原样转发给项目 SDK。
param(
    # GoArguments 保留调用方传入的完整 Go CLI 参数，不在 wrapper 中重解释子命令语义。
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$GoArguments
)

$ErrorActionPreference = "Stop"
# Windows PowerShell 5.1 默认使用系统代码页写控制台；固定 UTF-8，保证终端和 CI 接收一致字节流。
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
# RepositoryRoot 是版本目录、工具缓存和 server module 的共同解析基准。
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
# GoLocalRoot 限定 SDK 下载与清理只能发生在已被 Git 忽略的项目本地环境目录。
$GoLocalRoot = Join-Path $RepositoryRoot ".local\go"
# Go module 与 build cache 位于源码树外的项目本地目录，避免 IDE 把依赖模块识别为服务端源码。
$env:GOMODCACHE = Join-Path $RepositoryRoot ".local\cache\go\mod"
$env:GOCACHE = Join-Path $RepositoryRoot ".local\cache\go\build"
# local 禁止 go 命令根据 go.mod 静默下载另一个工具链，精确版本由 wrapper 自己保证。
$env:GOTOOLCHAIN = "local"
# 协议工具通过独立 PowerShell 进程调用该入口时，由上层负责展示阶段信息，避免嵌套日志重复。
$IsNestedInvocation = $env:IHOMELAND_GO_NESTED -eq "1"

# Write-LogLabel 只为状态标签着色，正文保持终端默认颜色，避免长路径和工具输出形成大面积色块。
function Write-LogLabel {
    param(
        [string]$Label,
        [string]$Message,
        [System.ConsoleColor]$Color
    )

    Write-Host "[$Label]" -NoNewline -ForegroundColor $Color
    Write-Host " $Message"
}

# Write-GoTestLine 识别 Go 原生测试摘要，仅突出会随执行结果变化的状态和缓存标记。
# 未匹配的诊断保持原样，确保编译错误、panic 和测试日志不会因美化而丢失上下文。
function Write-GoTestLine {
    param([AllowNull()][object]$Line)

    $text = [string]$Line
    if ($text -match '^(ok|\?|FAIL)\s+(.+?)(\s+\(cached\)|\s+\[no test files\])?$') {
        $statusColor = switch ($Matches[1]) {
            "ok" { [System.ConsoleColor]::Green }
            "?" { [System.ConsoleColor]::DarkYellow }
            default { [System.ConsoleColor]::Red }
        }
        Write-Host $Matches[1] -NoNewline -ForegroundColor $statusColor
        Write-Host ("    " + $Matches[2]) -NoNewline
        if ($Matches[3]) {
            Write-Host $Matches[3] -ForegroundColor DarkGray
        }
        else {
            Write-Host ""
        }
        return
    }
    if ($text -match '^--- FAIL:') {
        Write-Host $text -ForegroundColor Red
        return
    }
    Write-Host $text
}

# Read-GoCatalogValue 在不依赖 Go 或其他 YAML runtime 的情况下读取锁定的 Windows SDK 元数据。
# Property 必须是 languages.go 下的直接标量键；缺失时抛错，不能回退到系统默认值。
# 返回值保持 versions.yaml 中的字符串表达，供版本路径和 SHA-256 校验共同使用。
function Read-GoCatalogValue {
    param([string]$Property)

    $lines = Get-Content -LiteralPath (Join-Path $RepositoryRoot "versions.yaml") -Encoding utf8
    $propertyPattern = '^    {0}:\s*"([^"]+)"' -f [regex]::Escape($Property)
    $insideLanguages = $false
    $insideGo = $false
    foreach ($line in $lines) {
        if ($line -match '^languages:\s*$') {
            $insideLanguages = $true
            continue
        }
        if ($insideLanguages -and $line -match '^[^\s]') {
            break
        }
        if ($insideLanguages -and $line -match '^  go:\s*$') {
            $insideGo = $true
            continue
        }
        if ($insideGo -and $line -match $propertyPattern) {
            return $Matches[1]
        }
    }
    throw "Go catalog property languages.go.$Property was not found in versions.yaml"
}

# Assert-UnderGoLocal 防止清理或安装路径逃逸出已忽略的项目本地环境目录。
# Path 会先规范化为绝对路径再比较；校验失败必须在任何递归删除或移动发生前终止。
function Assert-UnderGoLocal {
    param([string]$Path)

    $root = [System.IO.Path]::GetFullPath($GoLocalRoot).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Go SDK path escaped project local directory: $candidate"
    }
}

# Install-ProjectGo 在 SDK 缺失时下载并校验 versions.yaml 指定的精确版本。
# 已存在 SDK 只有在 go version 精确匹配 Windows/amd64 锁定值时才会复用。
# 下载采用临时解压目录并在 finally 中清理，失败不会把半安装目录当作可用 SDK。
function Install-ProjectGo {
    $version = Read-GoCatalogValue "version"
    $expectedHash = (Read-GoCatalogValue "windows_amd64_sha256").ToUpperInvariant()
    $sdkRoot = Join-Path $GoLocalRoot $version
    $goBinary = Join-Path $sdkRoot "bin\go.exe"
    if (Test-Path -LiteralPath $goBinary) {
        $actualVersion = & $goBinary version
        if ($LASTEXITCODE -eq 0 -and $actualVersion -match "go$([regex]::Escape($version))\s+windows/amd64$") {
            return $goBinary
        }
    }

    Assert-UnderGoLocal $sdkRoot
    New-Item -ItemType Directory -Force -Path $GoLocalRoot | Out-Null
    if (Test-Path -LiteralPath $sdkRoot) {
        Remove-Item -LiteralPath $sdkRoot -Recurse -Force
    }

    $archive = Join-Path $GoLocalRoot "go$version.windows-amd64.zip"
    $extractionRoot = Join-Path $GoLocalRoot (".extract-" + [guid]::NewGuid().ToString("N"))
    Assert-UnderGoLocal $archive
    Assert-UnderGoLocal $extractionRoot
    try {
        Write-LogLabel "SETUP" "Installing project Go SDK $version..." Cyan
        Invoke-WebRequest -Uri "https://go.dev/dl/go$version.windows-amd64.zip" -OutFile $archive
        $actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToUpperInvariant()
        if ($actualHash -ne $expectedHash) {
            throw "Go SDK checksum mismatch: expected $expectedHash, got $actualHash"
        }
        Expand-Archive -LiteralPath $archive -DestinationPath $extractionRoot -Force
        Move-Item -LiteralPath (Join-Path $extractionRoot "go") -Destination $sdkRoot
    }
    finally {
        if (Test-Path -LiteralPath $archive) {
            Remove-Item -LiteralPath $archive -Force
        }
        if (Test-Path -LiteralPath $extractionRoot) {
            Remove-Item -LiteralPath $extractionRoot -Recurse -Force
        }
    }
    if (-not (Test-Path -LiteralPath $goBinary)) {
        throw "Project Go SDK installation did not produce $goBinary"
    }
    Write-LogLabel "OK" "Project Go SDK $version installed." Green
    return $goBinary
}

# 主执行块统一记录命令、耗时与退出状态；嵌套调用只保留 Go 原生输出供协议阶段解释。
try {
    # GoBinary 是本次命令唯一允许执行的 go.exe 绝对路径，不写回用户 PATH 或 go env。
    $GoBinary = Install-ProjectGo
    $versionOutput = (& $GoBinary version)
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to read project Go SDK version"
    }

    if (-not $IsNestedInvocation) {
        Write-Host ""
        Write-LogLabel "INFO" "SDK: $versionOutput" Cyan
        Write-LogLabel "INFO" "Command: go $($GoArguments -join ' ')" Cyan
        Write-LogLabel "INFO" "Module: $RepositoryRoot\server" Cyan
        Write-Host ""
    }

    # 直接运行项目 SDK，不要求系统安装 Go，也不依赖 PATH 条目。
    $startedAt = [System.Diagnostics.Stopwatch]::StartNew()
    Push-Location (Join-Path $RepositoryRoot "server")
    try {
        if ($GoArguments.Count -gt 0 -and $GoArguments[0] -eq "test") {
            & $GoBinary @GoArguments 2>&1 | ForEach-Object { Write-GoTestLine $_ }
        }
        else {
            & $GoBinary @GoArguments
        }
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
        $startedAt.Stop()
    }

    if (-not $IsNestedInvocation) {
        Write-Host ""
        if ($exitCode -eq 0) {
            Write-LogLabel "OK" ("Go command completed in {0:N2}s." -f $startedAt.Elapsed.TotalSeconds) Green
        }
        else {
            Write-LogLabel "FAIL" "Go command failed with exit code $exitCode." Red
        }
    }
    exit $exitCode
}
catch {
    Write-LogLabel "FAIL" $_.Exception.Message Red
    exit 1
}
