Set-StrictMode -Version Latest

# Assert-PathUnderRoot 保护递归清理、缓存替换和生成发布只能发生在显式 owner 根目录内。
# Root 本身不是合法候选，避免调用方把整个 .local、.tmp 或仓库根传给删除操作。
function Assert-PathUnderRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Purpose
    )

    $rootPath = [System.IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "$Purpose path escaped its owner root: $candidate"
    }
}

# Get-AssemblyIdentity 读取受管 DLL 的强名称身份而不执行其中代码。
# 返回对象字段用于同时验证短名称、四段版本和 public key token，不能只按文件名信任依赖。
function Get-AssemblyIdentity {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Managed assembly does not exist: $Path"
    }
    try {
        $assemblyName = [System.Reflection.AssemblyName]::GetAssemblyName($Path)
    }
    catch {
        throw "Read managed assembly identity failed for ${Path}: $($_.Exception.Message)"
    }
    $token = ($assemblyName.GetPublicKeyToken() | ForEach-Object { $_.ToString("x2") }) -join ""
    return [pscustomobject]@{
        Name = $assemblyName.Name
        Version = $assemblyName.Version.ToString()
        PublicKeyToken = $token
    }
}

# Assert-AssemblyIdentity 拒绝同名但版本或签名不同的 DLL，防止 Unity 隐式消费本机其他依赖。
function Assert-AssemblyIdentity {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedName,
        [Parameter(Mandatory = $true)][string]$ExpectedVersion,
        [Parameter(Mandatory = $true)][string]$ExpectedPublicKeyToken
    )

    $actual = Get-AssemblyIdentity -Path $Path
    if ($actual.Name -ne $ExpectedName -or $actual.Version -ne $ExpectedVersion -or $actual.PublicKeyToken -ne $ExpectedPublicKeyToken) {
        throw "Managed assembly identity mismatch for ${Path}: expected $ExpectedName, Version=$ExpectedVersion, PublicKeyToken=$ExpectedPublicKeyToken; got $($actual.Name), Version=$($actual.Version), PublicKeyToken=$($actual.PublicKeyToken)"
    }
}

# Get-VerifiedPackageHash 验证 nupkg 完整摘要并返回标准化小写值，供缓存与测试使用同一规则。
function Get-VerifiedPackageHash {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedSHA256
    )

    if ($ExpectedSHA256 -notmatch '^[0-9a-f]{64}$') {
        throw "Expected package SHA-256 must be 64 lowercase hexadecimal characters"
    }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "NuGet package does not exist: $Path"
    }
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $ExpectedSHA256) {
        throw "NuGet package checksum mismatch for ${Path}: expected $ExpectedSHA256, got $actual"
    }
    return $actual
}

# Restore-VerifiedNuGetAssembly 从锁定包恢复单个 Unity 编译依赖，并隔离所有临时写入。
# DownloadFile 仅用于无网络单测注入；正常调用不传时固定使用 Invoke-WebRequest。
function Restore-VerifiedNuGetAssembly {
    param(
        [Parameter(Mandatory = $true)][string]$PackageURL,
        [Parameter(Mandatory = $true)][string]$ExpectedSHA256,
        [Parameter(Mandatory = $true)][string]$CacheDirectory,
        [Parameter(Mandatory = $true)][string]$LocalEnvironmentRoot,
        [Parameter(Mandatory = $true)][string]$DLLPath,
        [Parameter(Mandatory = $true)][string]$AssemblyName,
        [Parameter(Mandatory = $true)][string]$AssemblyVersion,
        [Parameter(Mandatory = $true)][string]$PublicKeyToken,
        [scriptblock]$DownloadFile
    )

    Assert-PathUnderRoot -Path $CacheDirectory -Root $LocalEnvironmentRoot -Purpose "NuGet cache"
    New-Item -ItemType Directory -Force -Path $CacheDirectory | Out-Null
    $package = Join-Path $CacheDirectory "package.nupkg"
    $expanded = Join-Path $CacheDirectory "expanded"
    Assert-PathUnderRoot -Path $package -Root $LocalEnvironmentRoot -Purpose "NuGet package"
    Assert-PathUnderRoot -Path $expanded -Root $LocalEnvironmentRoot -Purpose "NuGet expansion"

    $packageValid = $false
    if (Test-Path -LiteralPath $package -PathType Leaf) {
        try {
            Get-VerifiedPackageHash -Path $package -ExpectedSHA256 $ExpectedSHA256 | Out-Null
            $packageValid = $true
        }
        catch {
            Remove-Item -LiteralPath $package -Force
        }
    }
    if (-not $packageValid) {
        $download = Join-Path $CacheDirectory (".download-" + [guid]::NewGuid().ToString("N") + ".tmp")
        Assert-PathUnderRoot -Path $download -Root $LocalEnvironmentRoot -Purpose "NuGet download"
        try {
            try {
                if ($null -eq $DownloadFile) {
                    Invoke-WebRequest -Uri $PackageURL -OutFile $download
                }
                else {
                    & $DownloadFile $PackageURL $download
                }
            }
            catch {
                throw "Download locked NuGet package failed from ${PackageURL}: $($_.Exception.Message)"
            }
            Get-VerifiedPackageHash -Path $download -ExpectedSHA256 $ExpectedSHA256 | Out-Null
            Move-Item -LiteralPath $download -Destination $package -Force
        }
        finally {
            if (Test-Path -LiteralPath $download) {
                Remove-Item -LiteralPath $download -Force
            }
        }
    }

    $cachedDLL = Join-Path $expanded ($DLLPath -replace '/', '\')
    Assert-PathUnderRoot -Path $cachedDLL -Root $expanded -Purpose "NuGet cached DLL"
    if (Test-Path -LiteralPath $cachedDLL -PathType Leaf) {
        try {
            Assert-AssemblyIdentity -Path $cachedDLL -ExpectedName $AssemblyName -ExpectedVersion $AssemblyVersion -ExpectedPublicKeyToken $PublicKeyToken
            return $cachedDLL
        }
        catch {
            Remove-Item -LiteralPath $expanded -Recurse -Force
        }
    }
    elseif (Test-Path -LiteralPath $expanded) {
        Remove-Item -LiteralPath $expanded -Recurse -Force
    }

    $temporaryExpansion = Join-Path $CacheDirectory (".expand-" + [guid]::NewGuid().ToString("N"))
    Assert-PathUnderRoot -Path $temporaryExpansion -Root $LocalEnvironmentRoot -Purpose "NuGet temporary expansion"
    try {
        New-Item -ItemType Directory -Force -Path $temporaryExpansion | Out-Null
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [System.IO.Compression.ZipFile]::ExtractToDirectory($package, $temporaryExpansion)
        $temporaryDLL = Join-Path $temporaryExpansion ($DLLPath -replace '/', '\')
        Assert-PathUnderRoot -Path $temporaryDLL -Root $temporaryExpansion -Purpose "NuGet extracted DLL"
        Assert-AssemblyIdentity -Path $temporaryDLL -ExpectedName $AssemblyName -ExpectedVersion $AssemblyVersion -ExpectedPublicKeyToken $PublicKeyToken
        Move-Item -LiteralPath $temporaryExpansion -Destination $expanded
    }
    catch {
        throw "Restore locked NuGet assembly failed: $($_.Exception.Message)"
    }
    finally {
        if (Test-Path -LiteralPath $temporaryExpansion) {
            Remove-Item -LiteralPath $temporaryExpansion -Recurse -Force
        }
    }
    return (Join-Path $expanded ($DLLPath -replace '/', '\'))
}

# Get-DirectoryManifest 为受管目录建立相对路径到 SHA-256 的有序快照。
# 相对路径统一为正斜杠，使不同 Windows 调用位置不会改变确定性比较键。
function Get-DirectoryManifest {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string[]]$ExcludedExtensions = @()
    )

    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        throw "Managed directory does not exist: $Root"
    }
    $rootPath = [System.IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $manifest = [ordered]@{}
    Get-ChildItem -LiteralPath $Root -Recurse -File | Where-Object {
        $ExcludedExtensions -notcontains $_.Extension
    } | Sort-Object FullName | ForEach-Object {
        $relative = $_.FullName.Substring($rootPath.Length).Replace('\', '/')
        $manifest[$relative] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    if ($manifest.Count -eq 0) {
        throw "Managed directory is empty: $Root"
    }
    return $manifest
}

# Assert-DirectoryManifestsEqual 定位重复生成时首个路径集合或内容摘要差异。
function Assert-DirectoryManifestsEqual {
    param(
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Before,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$After
    )

    if ($Before.Count -ne $After.Count) {
        throw "Generated artifact count changed from $($Before.Count) to $($After.Count)"
    }
    foreach ($path in $Before.Keys) {
        if (-not $After.Contains($path) -or $Before[$path] -ne $After[$path]) {
            throw "Generated artifact is not deterministic: $path"
        }
    }
}

# Publish-GeneratedDirectory 用独立临时 backup 保护最后一次完整输出，发布失败时恢复旧目录。
# Stage 与 Target 必须互不包含；BackupRoot 不能属于两者，既避免 Unity 导入，也避免 staging cleanup 误删恢复副本。
function Publish-GeneratedDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Stage,
        [Parameter(Mandatory = $true)][string]$Target,
        [Parameter(Mandatory = $true)][string]$BackupRoot,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot
    )

    Assert-PathUnderRoot -Path $Stage -Root $RepositoryRoot -Purpose "Generated stage"
    Assert-PathUnderRoot -Path $Target -Root $RepositoryRoot -Purpose "Generated target"
    Assert-PathUnderRoot -Path $BackupRoot -Root $RepositoryRoot -Purpose "Generated backup root"
    if (-not (Test-Path -LiteralPath $Stage -PathType Container)) {
        throw "Generated stage does not exist: $Stage"
    }
    $stagePath = [System.IO.Path]::GetFullPath($Stage).TrimEnd('\')
    $targetPath = [System.IO.Path]::GetFullPath($Target).TrimEnd('\')
    $stagePrefix = $stagePath + '\'
    $targetPrefix = $targetPath + '\'
    if ($stagePath.Equals($targetPath, [System.StringComparison]::OrdinalIgnoreCase) -or
        $stagePath.StartsWith($targetPrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
        $targetPath.StartsWith($stagePrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Generated stage and target must not overlap"
    }
    $parent = Split-Path $Target -Parent
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    New-Item -ItemType Directory -Force -Path $BackupRoot | Out-Null
    $backup = Join-Path $BackupRoot ((Split-Path $Target -Leaf) + ".backup-" + [guid]::NewGuid().ToString("N"))
    Assert-PathUnderRoot -Path $backup -Root $RepositoryRoot -Purpose "Generated backup"
    $backupPath = [System.IO.Path]::GetFullPath($backup).TrimEnd('\')
    if ($backupPath.StartsWith($stagePrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
        $backupPath.StartsWith($targetPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Generated backup must not be inside stage or target"
    }
    $hadTarget = Test-Path -LiteralPath $Target
    try {
        if ($hadTarget) {
            Move-Item -LiteralPath $Target -Destination $backup
        }
        Move-Item -LiteralPath $Stage -Destination $Target
    }
    catch {
        $publishError = $_.Exception.Message
        if ((-not (Test-Path -LiteralPath $Target)) -and (Test-Path -LiteralPath $backup)) {
            try {
                Move-Item -LiteralPath $backup -Destination $Target
            }
            catch {
                throw "Publish generated directory failed: $publishError; restore previous output failed: $($_.Exception.Message)"
            }
        }
        throw "Publish generated directory failed: $publishError"
    }
    if (Test-Path -LiteralPath $backup) {
        Remove-Item -LiteralPath $backup -Recurse -Force
    }
}

Export-ModuleMember -Function Assert-PathUnderRoot, Assert-AssemblyIdentity, Get-VerifiedPackageHash, Restore-VerifiedNuGetAssembly, Get-DirectoryManifest, Assert-DirectoryManifestsEqual, Publish-GeneratedDirectory
