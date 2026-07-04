@echo off
setlocal EnableExtensions EnableDelayedExpansion
title iHomeland - Verify Local Environment

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..\..") do set "REPO_ROOT=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
set "LOCAL_ENV_PATH=%SERVER_ROOT%\.env.local"

echo.
echo =====================================
echo  iHomeland Local Environment Verify
echo =====================================
echo.

pushd "%REPO_ROOT%"

call "%SCRIPT_DIR%\load-local-env.bat" "%LOCAL_ENV_PATH%"
if errorlevel 1 (
    call :fail "failed to load local environment."
    goto :finish
)

set "INFRA_FAILED=0"

if "%IHOMELAND_MYSQL_ADDR%"=="" (
    set "MYSQL_ADDR=127.0.0.1:33306"
) else (
    set "MYSQL_ADDR=%IHOMELAND_MYSQL_ADDR%"
)

if "%IHOMELAND_REDIS_ADDR%"=="" (
    set "REDIS_ADDR=127.0.0.1:36379"
) else (
    set "REDIS_ADDR=%IHOMELAND_REDIS_ADDR%"
)

echo [1/3] Checking configured MySQL and Redis TCP addresses...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$service = 'mysql'; $addr = '!MYSQL_ADDR!'; $index = $addr.LastIndexOf(':'); if ($index -le 0 -or $index -eq ($addr.Length - 1)) { Write-Host ('[FAIL] ' + $service + ' address is invalid: ' + $addr) -ForegroundColor Red; exit 1 }; $hostName = $addr.Substring(0, $index); $portText = $addr.Substring($index + 1); $port = 0; if (-not [int]::TryParse($portText, [ref]$port)) { Write-Host ('[FAIL] ' + $service + ' port is invalid: ' + $addr) -ForegroundColor Red; exit 1 }; $client = [System.Net.Sockets.TcpClient]::new(); try { $async = $client.BeginConnect($hostName, $port, $null, $null); if (-not $async.AsyncWaitHandle.WaitOne(3000, $false)) { throw 'timeout' }; $client.EndConnect($async); Write-Host ('[OK] ' + $service + ' is reachable at ' + $addr + '.') -ForegroundColor Green; exit 0 } catch { Write-Host ('[FAIL] ' + $service + ' is not reachable at ' + $addr + '.') -ForegroundColor Red; exit 1 } finally { $client.Close() }"
if errorlevel 1 set "INFRA_FAILED=1"

powershell -NoProfile -ExecutionPolicy Bypass -Command "$service = 'redis'; $addr = '!REDIS_ADDR!'; $index = $addr.LastIndexOf(':'); if ($index -le 0 -or $index -eq ($addr.Length - 1)) { Write-Host ('[FAIL] ' + $service + ' address is invalid: ' + $addr) -ForegroundColor Red; exit 1 }; $hostName = $addr.Substring(0, $index); $portText = $addr.Substring($index + 1); $port = 0; if (-not [int]::TryParse($portText, [ref]$port)) { Write-Host ('[FAIL] ' + $service + ' port is invalid: ' + $addr) -ForegroundColor Red; exit 1 }; $client = [System.Net.Sockets.TcpClient]::new(); try { $async = $client.BeginConnect($hostName, $port, $null, $null); if (-not $async.AsyncWaitHandle.WaitOne(3000, $false)) { throw 'timeout' }; $client.EndConnect($async); Write-Host ('[OK] ' + $service + ' is reachable at ' + $addr + '.') -ForegroundColor Green; exit 0 } catch { Write-Host ('[FAIL] ' + $service + ' is not reachable at ' + $addr + '.') -ForegroundColor Red; exit 1 } finally { $client.Close() }"
if errorlevel 1 set "INFRA_FAILED=1"

if "%IHOMELAND_HTTP_ADDR%"=="" (
    set "HTTP_ADDR=127.0.0.1:8080"
) else (
    set "HTTP_ADDR=%IHOMELAND_HTTP_ADDR%"
)

echo.
echo [2/3] Checking server HTTP endpoints at http://%HTTP_ADDR% ...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "$base = 'http://%HTTP_ADDR%';" ^
    "$ErrorActionPreference = 'Stop';" ^
    "try {" ^
    "  Write-Host '[CHECK] /healthz' -ForegroundColor Cyan;" ^
    "  $health = Invoke-RestMethod -Uri ($base + '/healthz') -TimeoutSec 5;" ^
    "  if ($health.status -ne 'ok') { throw 'healthz status is not ok' }" ^
    "  Write-Host '[OK] /healthz' -ForegroundColor Green;" ^
    "  Write-Host '[CHECK] /readyz' -ForegroundColor Cyan;" ^
    "  try { $ready = Invoke-WebRequest -Uri ($base + '/readyz') -TimeoutSec 5; Write-Host '[OK] /readyz' -ForegroundColor Green } catch { if ($_.ErrorDetails.Message) { Write-Host $_.ErrorDetails.Message -ForegroundColor Yellow }; if ($_.Exception.Response) { $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream()); Write-Host $reader.ReadToEnd() -ForegroundColor Yellow }; throw 'readyz is not ready' }" ^
    "  if ($ready.StatusCode -ne 200) { throw 'readyz did not return 200' }" ^
    "  Write-Host '[CHECK] /version' -ForegroundColor Cyan;" ^
    "  $version = Invoke-RestMethod -Uri ($base + '/version') -TimeoutSec 5;" ^
    "  if ($null -eq $version.release.protocol) { throw 'version response misses release.protocol' }" ^
    "  Write-Host '[OK] /version' -ForegroundColor Green;" ^
    "} catch {" ^
    "  Write-Host ('[FAIL] server HTTP check failed: ' + $_.Exception.Message) -ForegroundColor Red;" ^
    "  exit 1" ^
    "}"
set "EXIT_CODE=%ERRORLEVEL%"
if "%INFRA_FAILED%"=="1" if "%EXIT_CODE%"=="0" set "EXIT_CODE=1"

echo.
echo [3/3] Summary
if "%EXIT_CODE%"=="0" (
    echo [OK] Local service verification passed.
    echo [INFO] This verifies dependency reachability and HTTP health only.
    echo [INFO] Run server\scripts\test.bat for storage boundary and business recovery tests.
) else (
    echo [FAIL] Local service verification failed.
)
goto :finish

:fail
echo.
powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[FAIL] %~1' -ForegroundColor Red"
set "EXIT_CODE=1"
exit /b 0

:finish
if not defined EXIT_CODE set "EXIT_CODE=1"
popd
echo.
if "%EXIT_CODE%"=="0" (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host 'Done.' -ForegroundColor Green"
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host 'Stopped with errors.' -ForegroundColor Red"
)
if /I "%START_DIR%"=="%SCRIPT_DIR%" (
    echo.
    pause
)
exit /b %EXIT_CODE%
