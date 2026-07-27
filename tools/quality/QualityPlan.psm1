Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Read-ClosedJson 只读取通过指定 closed schema 的 JSON，避免 plan 或 catalog 携带未登记字段。
function Read-ClosedJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$SchemaPath,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Label 不存在"
    }
    if (-not (Test-Json -LiteralPath $Path -SchemaFile $SchemaPath -ErrorAction Stop)) {
        throw "$Label schema 验证失败"
    }
    return Get-Content -LiteralPath $Path -Raw -Encoding utf8 | ConvertFrom-Json
}

# Get-QualityCatalog 验证中央 check catalog 的 schema、唯一 ID、唯一顺序和脚本实现闭包。
function Get-QualityCatalog {
    param(
        [Parameter(Mandatory = $true)][string]$CatalogPath,
        [Parameter(Mandatory = $true)][string]$SchemaPath,
        [Parameter(Mandatory = $true)][string[]]$SupportedCheckIds
    )

    $catalog = Read-ClosedJson -Path $CatalogPath -SchemaPath $SchemaPath -Label "quality catalog"
    $ids = @($catalog.checks | ForEach-Object { [string]$_.id })
    if (@($ids | Sort-Object -Unique).Count -ne $ids.Count) {
        throw "quality catalog 包含重复 check ID"
    }
    $orders = @($catalog.checks | ForEach-Object { [int]$_.order })
    if (@($orders | Sort-Object -Unique).Count -ne $orders.Count) {
        throw "quality catalog 包含重复执行顺序"
    }
    $unknown = @($ids | Where-Object { $SupportedCheckIds -cnotcontains $_ })
    $missing = @($SupportedCheckIds | Where-Object { $ids -cnotcontains $_ })
    if ($unknown.Count -gt 0 -or $missing.Count -gt 0) {
        throw "quality catalog 与受支持 check 实现不闭合"
    }
    return $catalog
}

# Get-QualityPlan 读取指定 OpenSpec change 的 plan，并拒绝任意命令文本和跨 change 冒名。
function Get-QualityPlan {
    param(
        [Parameter(Mandatory = $true)][string]$PlanPath,
        [Parameter(Mandatory = $true)][string]$SchemaPath,
        [Parameter(Mandatory = $true)][string]$ExpectedChange
    )

    $plan = Read-ClosedJson -Path $PlanPath -SchemaPath $SchemaPath -Label "change validation plan"
    if ([string]$plan.change -cne $ExpectedChange) {
        throw "validation plan change identity 不匹配"
    }
    $ids = @($plan.checks | ForEach-Object { [string]$_.id })
    if (@($ids | Sort-Object -Unique).Count -ne $ids.Count) {
        throw "validation plan 包含重复 check ID"
    }
    foreach ($entry in $plan.checks) {
        $reason = [string]$entry.reason
        if (
            $reason -match '(?i)(?:powershell|pwsh|cmd\.exe|\.ps1|[A-Z]:\\|\\\\|&&|\|\s)'
        ) {
            throw "validation plan reason 包含命令或本机路径文本"
        }
    }
    return $plan
}

# Resolve-QualityPlanChecks 把 plan check ID 解析成 catalog metadata，并按中央顺序稳定排序。
function Resolve-QualityPlanChecks {
    param(
        [Parameter(Mandatory = $true)]$Catalog,
        [Parameter(Mandatory = $true)]$Plan,
        [switch]$AllowFinalOnly
    )

    $catalogById = @{}
    foreach ($entry in $Catalog.checks) {
        $catalogById[[string]$entry.id] = $entry
    }
    $resolved = foreach ($requested in $Plan.checks) {
        $id = [string]$requested.id
        if (-not $catalogById.ContainsKey($id)) {
            throw "validation plan 引用了 unknown check ID"
        }
        $metadata = $catalogById[$id]
        if (-not $AllowFinalOnly -and [string]$metadata.class -ceq "final-only") {
            throw "change validation plan 不得引用 final-only check"
        }
        [pscustomobject][ordered]@{
            Id = $id
            Class = [string]$metadata.class
            Owner = [string]$metadata.owner
            Order = [int]$metadata.order
            Reason = [string]$requested.reason
        }
    }
    return @($resolved | Sort-Object Order, Id)
}

Export-ModuleMember -Function @(
    "Get-QualityCatalog",
    "Get-QualityPlan",
    "Resolve-QualityPlanChecks"
)
