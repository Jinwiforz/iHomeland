Set-StrictMode -Version Latest

# ClientContractPathSpecs 是 client-v1 实际消费的协议、fixture、包与引擎版本边界。
# 服务端资格报告和其可推导 binding 不进入该身份，避免 B0.3/B0.4/B0.6 证据形成循环依赖。
$script:ClientContractPathSpecs = @(
    "shared/proto"
    "shared/contracts/http"
    "shared/contracts/registry"
    "shared/contracts/fixtures/admission"
    "shared/contracts/fixtures/http"
    "shared/contracts/fixtures/realtime"
    "shared/contracts/fixtures/client-qualification"
    "shared/contracts/fixtures/battle/wire"
    "shared/contracts/fixtures/simulation-control/runtime"
    "client/Packages"
    "client/ProjectSettings/ProjectVersion.txt"
    "tools/client-qualification/ClientContractIdentity.psm1"
)

# Get-ClientContractPathSpecs 返回稳定副本，供门禁测试审计纳入与排除边界。
function Get-ClientContractPathSpecs {
    return @($script:ClientContractPathSpecs)
}

# Get-ClientContractDigest 对 tracked 与待提交输入执行唯一 path:hash 身份算法。
function Get-ClientContractDigest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RepositoryRoot
    )

    if (-not [System.IO.Path]::IsPathRooted($RepositoryRoot) -or
        -not (Test-Path -LiteralPath $RepositoryRoot -PathType Container)) {
        throw "client contract repository root is invalid"
    }
    $pathSpecs = Get-ClientContractPathSpecs
    $tracked = & git -C $RepositoryRoot ls-files -- @pathSpecs
    if ($LASTEXITCODE -ne 0) {
        throw "client contract tracked input cannot be enumerated"
    }
    $untracked = & git -C $RepositoryRoot ls-files --others --exclude-standard -- @pathSpecs
    if ($LASTEXITCODE -ne 0) {
        throw "client contract untracked input cannot be enumerated"
    }
    $paths = @($tracked) + @($untracked)
    if ($paths.Count -eq 0) {
        throw "client contract digest input is empty"
    }
    $builder = [System.Text.StringBuilder]::new()
    foreach ($relative in @($paths | Sort-Object -Unique)) {
        $full = Join-Path $RepositoryRoot $relative
        if (-not (Test-Path -LiteralPath $full -PathType Leaf)) {
            throw "client contract digest input is missing"
        }
        $hash = (
            Get-FileHash -LiteralPath $full -Algorithm SHA256
        ).Hash.ToLowerInvariant()
        [void]$builder.Append(
            $relative.Replace('\', '/')
        ).Append(':').Append($hash).Append("`n")
    }
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($builder.ToString())
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        return (
            [BitConverter]::ToString($sha.ComputeHash($bytes))
        ).Replace("-", "").ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

Export-ModuleMember -Function Get-ClientContractPathSpecs, Get-ClientContractDigest
