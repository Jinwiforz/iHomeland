[CmdletBinding()]
param(
    [string]$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path,
    [string]$ClientPath = (Join-Path $RepositoryRoot "simulation\out\build\windows-msvc-debug\ihomeland-battle-protocol-client.exe")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:ContractRoot = Join-Path $RepositoryRoot "shared\contracts\fixtures\battle\protocol-client"
$script:MaximumFrameBytes = 65536
$script:HeaderBytes = 28

# Get-LowerSha256 统一 manifest 内容摘要格式。
function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Assert-ClosedSchema 递归拒绝未显式关闭的 object schema。
function Assert-ClosedSchema {
    param([Parameter(Mandatory = $true)]$Node)

    if ($null -eq $Node) {
        return
    }
    if ($Node -is [PSCustomObject]) {
        $properties = @($Node.PSObject.Properties)
        $type = $properties | Where-Object Name -CEQ "type"
        if ($type -and [string]$type.Value -CEQ "object") {
            $closed = $properties | Where-Object Name -CEQ "additionalProperties"
            if (-not $closed -or [bool]$closed.Value) {
                throw "protocol client schema 存在未关闭 object"
            }
        }
        foreach ($property in $properties) {
            Assert-ClosedSchema -Node $property.Value
        }
        return
    }
    if ($Node -is [Collections.IEnumerable] -and $Node -isnot [string]) {
        foreach ($item in $Node) {
            Assert-ClosedSchema -Node $item
        }
    }
}

# Write-U32BE 向 byte array 写入 network-order uint32。
function Write-U32BE {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$Offset,
        [Parameter(Mandatory = $true)][uint32]$Value
    )
    $Bytes[$Offset] = [byte](($Value -shr 24) -band 0xff)
    $Bytes[$Offset + 1] = [byte](($Value -shr 16) -band 0xff)
    $Bytes[$Offset + 2] = [byte](($Value -shr 8) -band 0xff)
    $Bytes[$Offset + 3] = [byte]($Value -band 0xff)
}

# Write-U64BE 向 byte array 写入 network-order uint64。
function Write-U64BE {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$Offset,
        [Parameter(Mandatory = $true)][uint64]$Value
    )
    for ($index = 0; $index -lt 8; $index++) {
        $shift = (7 - $index) * 8
        $Bytes[$Offset + $index] = [byte](($Value -shr $shift) -band 0xff)
    }
}

# Read-U32BE 从 byte array 读取 network-order uint32。
function Read-U32BE {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$Offset
    )
    return [uint32](
        ([uint32]$Bytes[$Offset] -shl 24) -bor
        ([uint32]$Bytes[$Offset + 1] -shl 16) -bor
        ([uint32]$Bytes[$Offset + 2] -shl 8) -bor
        [uint32]$Bytes[$Offset + 3])
}

# Read-U64BE 从 byte array 读取 network-order uint64。
function Read-U64BE {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$Offset
    )
    [uint64]$value = 0
    for ($index = 0; $index -lt 8; $index++) {
        $value = ($value -shl 8) -bor [uint64]$Bytes[$Offset + $index]
    }
    return $value
}

# New-Frame 构造 byte-exact stdin frame。
function New-Frame {
    param(
        [Parameter(Mandatory = $true)][byte]$Kind,
        [Parameter(Mandatory = $true)][uint64]$Sequence,
        [Parameter(Mandatory = $true)][uint64]$DeadlineUnixMs,
        [byte[]]$Payload = @()
    )
    $body = [byte[]]::new($script:HeaderBytes + $Payload.Length)
    [Text.Encoding]::ASCII.GetBytes("IHBQ").CopyTo($body, 0)
    $body[4] = 1
    $body[5] = $Kind
    Write-U64BE -Bytes $body -Offset 8 -Value $Sequence
    Write-U64BE -Bytes $body -Offset 16 -Value $DeadlineUnixMs
    Write-U32BE -Bytes $body -Offset 24 -Value $Payload.Length
    $Payload.CopyTo($body, $script:HeaderBytes)
    $frame = [byte[]]::new(4 + $body.Length)
    Write-U32BE -Bytes $frame -Offset 0 -Value $body.Length
    $body.CopyTo($frame, 4)
    return $frame
}

# Invoke-Client 执行一个隔离 child，完整捕获 binary stdout 与 stderr。
function Invoke-Client {
    param([Parameter(Mandatory = $true)][byte[]]$InputBytes)

    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = (Resolve-Path -LiteralPath $ClientPath).Path
    $start.ArgumentList.Add("--stdio")
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    if (-not $process.Start()) {
        throw "protocol client 无法启动"
    }
    $stdout = [IO.MemoryStream]::new()
    $stdoutTask = $process.StandardOutput.BaseStream.CopyToAsync($stdout)
    $stderrTask = $process.StandardError.ReadToEndAsync()
    try {
        $process.StandardInput.BaseStream.Write($InputBytes, 0, $InputBytes.Length)
        $process.StandardInput.BaseStream.Flush()
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(5000)) {
            $process.Kill($true)
            throw "protocol client stdio deadline exceeded"
        }
        [void]$stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        return ,([PSCustomObject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdout.ToArray()
            Stderr = $stderr
        })
    }
    finally {
        $stdout.Dispose()
        $process.Dispose()
    }
}

# Assert-Contract 验证 closed schema、manifest、语义与 implementation scope。
function Assert-Contract {
    $schemaPath = Join-Path $script:ContractRoot "schema.json"
    $contractPath = Join-Path $script:ContractRoot "contract.json"
    $manifestPath = Join-Path $script:ContractRoot "manifest.json"
    if (-not (Test-Json -LiteralPath $contractPath -SchemaFile $schemaPath) -or
        -not (Test-Json -LiteralPath $manifestPath -SchemaFile $schemaPath)) {
        throw "protocol client corpus 不符合 closed schema"
    }
    $schema = Get-Content -LiteralPath $schemaPath -Raw -Encoding utf8 | ConvertFrom-Json
    Assert-ClosedSchema -Node $schema
    $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding utf8 | ConvertFrom-Json
    $actualFiles = Get-ChildItem -LiteralPath $script:ContractRoot -File |
        ForEach-Object Name |
        Sort-Object
    $expectedFiles = @("manifest.json") + @($manifest.files.path) | Sort-Object
    if (($actualFiles -join "`n") -CNE ($expectedFiles -join "`n")) {
        throw "protocol client corpus file registry 漂移"
    }
    foreach ($entry in $manifest.files) {
        $path = Join-Path $script:ContractRoot $entry.path
        if ((Get-LowerSha256 -Path $path) -CNE [string]$entry.sha256) {
            throw "protocol client corpus hash 漂移：$($entry.path)"
        }
    }
    $contract = Get-Content -LiteralPath $contractPath -Raw -Encoding utf8 | ConvertFrom-Json
    if ($contract.framing.maximumBodyBytes -ne $script:MaximumFrameBytes -or
        $contract.framing.headerBytes -ne $script:HeaderBytes -or
        @($contract.kinds.value | Select-Object -Unique).Count -ne 12 -or
        [string]$contract.payloads.sessionStart.credentialIngress -CNE "inherited-stdin-once" -or
        [string]$contract.outputPolicy.malformedInputPolicy -CNE "exit-two-without-echo") {
        throw "protocol client framing/secret contract 漂移"
    }
    $clientSources = Get-ChildItem -LiteralPath (Join-Path $RepositoryRoot "simulation\src\qualification") -Filter "battle_protocol_*.cpp" -File
    $sourceText = ($clientSources | Get-Content -Raw) -join "`n"
    if ($sourceText -match 'ihomeland/sim/(?:transport/(?:authenticated_handshake|secure_datagram|battle_session|authenticated_multiplexer)|simulation/simulation_instance)\.hpp') {
        throw "protocol client 引用了 production adapter"
    }
}

# Assert-Stdio 验证正常 framing、sequence、低敏输出与 secret failure。
function Assert-Stdio {
    $describe = New-Frame -Kind 1 -Sequence 1 -DeadlineUnixMs 2000000000000
    $shutdown = New-Frame -Kind 2 -Sequence 2 -DeadlineUnixMs 2000000000000
    $input = [byte[]]::new($describe.Length + $shutdown.Length)
    $describe.CopyTo($input, 0)
    $shutdown.CopyTo($input, $describe.Length)
    $result = Invoke-Client -InputBytes $input
    if ($result.ExitCode -ne 0 -or $result.Stderr.Length -ne 0) {
        throw "protocol client 正常 stdio session 失败"
    }
    $offset = 0
    $expectedKinds = @(129, 130)
    $expectedSequences = @(1, 2)
    for ($index = 0; $index -lt 2; $index++) {
        $bodyBytes = Read-U32BE -Bytes $result.Stdout -Offset $offset
        if ($bodyBytes -lt $script:HeaderBytes -or
            $offset + 4 + $bodyBytes -gt $result.Stdout.Length) {
            throw "protocol client receipt length 漂移"
        }
        $bodyOffset = $offset + 4
        if ([Text.Encoding]::ASCII.GetString($result.Stdout, $bodyOffset, 4) -CNE "IHBQ" -or
            $result.Stdout[$bodyOffset + 5] -ne $expectedKinds[$index] -or
            (Read-U64BE -Bytes $result.Stdout -Offset ($bodyOffset + 8)) -ne $expectedSequences[$index] -or
            (Read-U64BE -Bytes $result.Stdout -Offset ($bodyOffset + 16)) -ne 0) {
            throw "protocol client receipt header 漂移"
        }
        $offset += 4 + $bodyBytes
    }
    if ($offset -ne $result.Stdout.Length) {
        throw "protocol client stdout 包含未登记 bytes"
    }

    $secret = [Text.Encoding]::ASCII.GetBytes("proof-secret-must-not-echo")
    $secretResult = Invoke-Client -InputBytes (New-Frame -Kind 3 -Sequence 1 -DeadlineUnixMs 2000000000000 -Payload $secret)
    if ($secretResult.ExitCode -ne 2 -or
        $secretResult.Stdout.Length -ne 0 -or
        $secretResult.Stderr.Contains("proof-secret-must-not-echo", [StringComparison]::Ordinal)) {
        throw "protocol client malformed secret redaction 失败"
    }

    $invalidSession = [byte[]]::new(76)
    $invalidResult = Invoke-Client -InputBytes (
        New-Frame -Kind 3 -Sequence 1 -DeadlineUnixMs 2000000000000 -Payload $invalidSession
    )
    $invalidBodyBytes = if ($invalidResult.Stdout.Length -ge 4) {
        Read-U32BE -Bytes $invalidResult.Stdout -Offset 0
    }
    else {
        0
    }
    if ($invalidResult.ExitCode -ne 0 -or
        $invalidResult.Stderr.Length -ne 0 -or
        $invalidBodyBytes -ne ($script:HeaderBytes + 3) -or
        $invalidResult.Stdout.Length -ne (4 + $invalidBodyBytes) -or
        $invalidResult.Stdout[9] -ne 131 -or
        (Read-U64BE -Bytes $invalidResult.Stdout -Offset 12) -ne 1 -or
        $invalidResult.Stdout[32] -ne 0 -or
        $invalidResult.Stdout[33] -ne 2 -or
        $invalidResult.Stdout[34] -ne 1) {
        $invalidHex = [BitConverter]::ToString(
            $invalidResult.Stdout
        )
        throw "protocol client session-start typed failure receipt 失败：exit=$($invalidResult.ExitCode) stderr=$($invalidResult.Stderr.Length) body=$invalidBodyBytes stdout=$invalidHex"
    }

    $oversized = [byte[]]::new(4)
    Write-U32BE -Bytes $oversized -Offset 0 -Value ($script:MaximumFrameBytes + 1)
    $oversizedResult = Invoke-Client -InputBytes $oversized
    if ($oversizedResult.ExitCode -ne 2 -or
        $oversizedResult.Stdout.Length -ne 0) {
        throw "protocol client 64 KiB ceiling 未 fail closed"
    }
}

Assert-Contract
Assert-Stdio
Write-Output "battle protocol client contract/stdin tests passed"
