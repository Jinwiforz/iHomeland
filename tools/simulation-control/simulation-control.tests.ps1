[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 本入口只调用 owner 脚本的隔离失败回归，避免维护第二套 validator。
& (Join-Path $PSScriptRoot "simulation-control.ps1") -Action test
