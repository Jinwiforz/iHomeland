Set-StrictMode -Version Latest

# Get-IdentityFileSha256 使用显式文件流计算摘要，不依赖宿主 cmdlet 的格式或 locale。
function Get-IdentityFileSha256 {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $stream = [System.IO.File]::Open(
        $Path,
        [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::Read)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString(
                $sha.ComputeHash($stream)
            )).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        $stream.Dispose()
    }
}

# Get-CppSourceIdentityDigest 对规范相对路径执行 ordinal 去重和排序，跨 PowerShell 生成唯一 C++ source identity。
function Get-CppSourceIdentityDigest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RepositoryRoot
    )

    if (-not [System.IO.Path]::IsPathRooted($RepositoryRoot) -or
        -not (Test-Path -LiteralPath $RepositoryRoot -PathType Container)) {
        throw "C++ source identity repository root is invalid"
    }
    $tracked = @(& git -C $RepositoryRoot ls-files --cached --others --exclude-standard -- versions.yaml simulation tools/cpp)
    if ($LASTEXITCODE -ne 0) {
        throw "无法枚举 C++ source identity 输入"
    }
    $unique = [System.Collections.Generic.HashSet[string]]::new(
        [System.StringComparer]::Ordinal)
    foreach ($relativePath in $tracked) {
        [void]$unique.Add($relativePath.Replace('\', '/'))
    }
    $ordered = [string[]]@($unique)
    [System.Array]::Sort($ordered, [System.StringComparer]::Ordinal)
    $builder = [System.Text.StringBuilder]::new()
    foreach ($relativePath in $ordered) {
        $absolutePath = Join-Path $RepositoryRoot $relativePath
        if (Test-Path -LiteralPath $absolutePath -PathType Leaf) {
            [void]$builder.Append($relativePath).Append('=').Append(
                (Get-IdentityFileSha256 -Path $absolutePath)
            ).Append("`n")
        }
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString(
                $sha.ComputeHash($bytes)
            )).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

Export-ModuleMember -Function Get-CppSourceIdentityDigest
