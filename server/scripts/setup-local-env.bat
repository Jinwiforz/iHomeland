@echo off
setlocal EnableExtensions EnableDelayedExpansion
title iHomeland - Setup Local Environment

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
for %%I in ("%~dp0..\..") do set "REPO_ROOT=%%~fI"

set "LOCAL_ENV_PATH=%SERVER_ROOT%\.env.local"
set "MODE=auto"
set "EXIT_CODE=0"
set "PUSHED=0"

if not "%~1"=="" (
    if /I "%~1"=="--auto" set "MODE=auto"
    if /I "%~1"=="--docker" set "MODE=docker"
    if /I "%~1"=="--native" set "MODE=native"
    if /I "%~1"=="--help" goto :usage
    if /I "%~1"=="-h" goto :usage
)

if /I not "%MODE%"=="auto" if /I not "%MODE%"=="docker" if /I not "%MODE%"=="native" (
    call :fail "unsupported setup mode. Use --auto, --docker, or --native."
    goto :finish
)

echo.
echo ====================================
echo  iHomeland Local Environment Setup
echo ====================================
echo.
echo [INFO] Mode: %MODE%

pushd "%REPO_ROOT%"
set "PUSHED=1"

echo.
echo [1/4] Checking required local tools...
call :check_tool go "Go is required later to run and test the server." optional

if /I "%MODE%"=="auto" (
    call :can_connect 127.0.0.1 3306
    set "MYSQL_NATIVE=!CHECK_RESULT!"
    call :can_connect 127.0.0.1 6379
    set "REDIS_NATIVE=!CHECK_RESULT!"
    set "SELECTED_MODE=docker"
    if "!MYSQL_NATIVE!"=="1" if "!REDIS_NATIVE!"=="1" set "SELECTED_MODE=native"
) else (
    set "SELECTED_MODE=%MODE%"
)

echo [INFO] Selected mode: %SELECTED_MODE%

if /I "%SELECTED_MODE%"=="docker" (
    call :check_tool docker "Docker Desktop is required for Docker-managed MySQL and Redis." required
)

echo.
echo [2/4] Writing server\.env.local...
if /I "%SELECTED_MODE%"=="native" (
    call :write_env native 3306 6379 3306 6379
) else (
    call :write_env docker 3306 6379 3306 6379
)
if errorlevel 1 goto :finish

call "%SCRIPT_DIR%\load-local-env.bat" "%LOCAL_ENV_PATH%"
if errorlevel 1 (
    call :fail "failed to load server/.env.local."
    goto :finish
)

echo.
echo [3/4] Checking configured dependency endpoints...
if /I "%SELECTED_MODE%"=="native" (
    call :check_endpoint mysql 127.0.0.1 3306
    call :check_endpoint redis 127.0.0.1 6379
) else (
    call :check_port mysql IHOMELAND_MYSQL_PORT 3306
    call :check_port redis IHOMELAND_REDIS_PORT 6379
)

echo.
echo [4/4] Summary
if "%EXIT_CODE%"=="0" (
    echo [OK] Local environment setup passed.
    if /I "%SELECTED_MODE%"=="native" (
        echo [INFO] Using native MySQL/Redis. Start your local services before running the server.
        echo [INFO] Next: server\scripts\run.bat
    ) else (
        echo [INFO] Using Docker-managed MySQL/Redis.
        echo [INFO] Next: server\scripts\start-local-infra.bat
    )
) else (
    echo [FAIL] Local environment setup found issues.
    echo [INFO] Re-run with --docker, --native, or --auto after fixing the reported issue.
)
goto :finish

:usage
echo.
echo Usage:
echo   server\scripts\setup-local-env.bat [--auto^|--docker^|--native]
echo.
echo Modes:
echo   --auto    Use native MySQL/Redis if 127.0.0.1:3306 and 127.0.0.1:6379 are reachable; otherwise use Docker ports.
echo   --docker  Generate Docker-managed local config: MySQL 3306, Redis 6379.
echo   --native  Generate native-service local config: MySQL 3306, Redis 6379.
echo.
set "EXIT_CODE=0"
goto :finish

:finish
if "%PUSHED%"=="1" popd
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

:check_tool
where %~1 >nul 2>nul
if errorlevel 1 (
    if /I "%~3"=="required" (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[FAIL] %~1 was not found. %~2' -ForegroundColor Red"
        set "EXIT_CODE=1"
    ) else (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[WARN] %~1 was not found. %~2' -ForegroundColor Yellow"
    )
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[OK] %~1 is available.' -ForegroundColor Green"
)
exit /b 0

:can_connect
set "CHECK_RESULT=0"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$client = [System.Net.Sockets.TcpClient]::new(); try { $async = $client.BeginConnect('%~1', [int]'%~2', $null, $null); if (-not $async.AsyncWaitHandle.WaitOne(500, $false)) { exit 1 }; $client.EndConnect($async); exit 0 } catch { exit 1 } finally { $client.Close() }"
if not errorlevel 1 set "CHECK_RESULT=1"
exit /b 0

:write_env
set "ENV_MODE=%~1"
set "MYSQL_ADDR_PORT=%~2"
set "REDIS_ADDR_PORT=%~3"
set "MYSQL_DOCKER_PORT=%~4"
set "REDIS_DOCKER_PORT=%~5"
(
    echo IHOMELAND_CONFIG=config/local.yaml
    echo IHOMELAND_HTTP_ADDR=127.0.0.1:8080
    echo IHOMELAND_LOG_LEVEL=info
    echo IHOMELAND_PROTOCOL_VERSION=0
    echo IHOMELAND_RELEASE_PATH=../release.json
    echo IHOMELAND_SERVER_VERSION_PATH=version.json
    echo IHOMELAND_CLIENT_VERSION_PATH=../client/version.json
    echo.
    echo IHOMELAND_MYSQL_ADDR=127.0.0.1:%MYSQL_ADDR_PORT%
    echo IHOMELAND_MYSQL_DATABASE=ihomeland
    echo IHOMELAND_MYSQL_USER=ihomeland
    echo IHOMELAND_MYSQL_PASSWORD=ihomeland
    echo IHOMELAND_MYSQL_ROOT_PASSWORD=ihomeland_root
    echo IHOMELAND_MYSQL_PORT=%MYSQL_DOCKER_PORT%
    echo.
    echo IHOMELAND_REDIS_ADDR=127.0.0.1:%REDIS_ADDR_PORT%
    echo IHOMELAND_REDIS_PORT=%REDIS_DOCKER_PORT%
) > "%LOCAL_ENV_PATH%"
if errorlevel 1 (
    call :fail "failed to write server/.env.local."
    exit /b 1
)
echo [OK] Wrote server\.env.local for %ENV_MODE% mode.
exit /b 0

:check_endpoint
powershell -NoProfile -ExecutionPolicy Bypass -Command "$service = '%~1'; $hostName = '%~2'; $port = [int]'%~3'; $client = [System.Net.Sockets.TcpClient]::new(); try { $async = $client.BeginConnect($hostName, $port, $null, $null); if (-not $async.AsyncWaitHandle.WaitOne(1500, $false)) { throw 'timeout' }; $client.EndConnect($async); Write-Host ('[OK] ' + $service + ' is reachable at ' + $hostName + ':' + $port + '.') -ForegroundColor Green; exit 0 } catch { Write-Host ('[FAIL] ' + $service + ' is not reachable at ' + $hostName + ':' + $port + '.') -ForegroundColor Red; exit 1 } finally { $client.Close() }"
if errorlevel 1 set "EXIT_CODE=1"
exit /b 0

:check_port
set "SERVICE_NAME=%~1"
set "PORT_VALUE=!%~2!"
if "!PORT_VALUE!"=="" set "PORT_VALUE=%~3"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$service = '%SERVICE_NAME%'; $portText = '%PORT_VALUE%'; $port = 0; if (-not [int]::TryParse($portText, [ref]$port) -or $port -le 0 -or $port -gt 65535) { Write-Host ('[FAIL] ' + $service + ' port is invalid: ' + $portText) -ForegroundColor Red; exit 1 }; $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue); if ($listeners.Count -gt 0) { Write-Host ('[FAIL] ' + $service + ' port ' + $port + ' is already used by a local process.') -ForegroundColor Red; exit 1 }; $excludedHits = @(); foreach ($protocol in @('ipv4','ipv6')) { $lines = netsh interface $protocol show excludedportrange protocol=tcp 2>$null; foreach ($line in $lines) { if ($line -match '^\s*(\d+)\s+(\d+)') { $start = [int]$matches[1]; $end = [int]$matches[2]; if ($port -ge $start -and $port -le $end) { $excludedHits += ($protocol + ' ' + $start + '-' + $end) } } } }; if ($excludedHits.Count -gt 0) { Write-Host ('[FAIL] ' + $service + ' port ' + $port + ' is in Windows TCP excluded port range: ' + ($excludedHits -join ', ')) -ForegroundColor Red; Write-Host '       Use --docker with another project port, or adjust server/.env.local intentionally.' -ForegroundColor Yellow; exit 1 }; Write-Host ('[OK] ' + $service + ' port ' + $port + ' is available for Docker binding.') -ForegroundColor Green; exit 0"
if errorlevel 1 set "EXIT_CODE=1"
exit /b 0

:fail
echo.
powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[FAIL] %~1' -ForegroundColor Red"
set "EXIT_CODE=1"
exit /b 0
