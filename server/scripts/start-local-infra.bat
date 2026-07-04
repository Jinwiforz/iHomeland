@echo off
setlocal EnableExtensions
title iHomeland - Start Local Infrastructure

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
set "LOCAL_ENV_PATH=%SERVER_ROOT%\.env.local"

echo.
echo ========================================
echo  iHomeland Local Infrastructure Startup
echo ========================================
echo.

pushd "%SERVER_ROOT%"

call "%SCRIPT_DIR%\load-local-env.bat" "%LOCAL_ENV_PATH%"
if errorlevel 1 (
    call :fail "failed to load local environment."
    goto :finish
)
if not defined IHOMELAND_MYSQL_PORT set "IHOMELAND_MYSQL_PORT=33306"
if not defined IHOMELAND_REDIS_PORT set "IHOMELAND_REDIS_PORT=36379"

where docker >nul 2>nul
if errorlevel 1 (
    call :fail "docker command is required but was not found. Start Docker Desktop and try again."
    goto :finish
)

if not exist "%SERVER_ROOT%\compose.yaml" (
    call :fail "compose.yaml was not found at %SERVER_ROOT%\compose.yaml."
    goto :finish
)

echo [1/3] Starting MySQL and Redis with docker compose...
echo [INFO] MySQL host port: %IHOMELAND_MYSQL_PORT%
echo [INFO] Redis host port: %IHOMELAND_REDIS_PORT%
docker compose -f "%SERVER_ROOT%\compose.yaml" up -d
if errorlevel 1 (
    call :fail "docker compose up failed."
    goto :finish
)

echo.
echo [2/3] Waiting for MySQL health check...
call :wait_healthy ihomeland-mysql mysql
if errorlevel 1 goto :finish

echo.
echo [3/3] Waiting for Redis health check...
call :wait_healthy ihomeland-redis redis
if errorlevel 1 goto :finish

echo.
echo [OK] Local infrastructure is healthy.
set "EXIT_CODE=0"
goto :finish

:wait_healthy
powershell -NoProfile -ExecutionPolicy Bypass -Command "$Container = '%~1'; $Service = '%~2'; for ($i = 0; $i -lt 60; $i++) { $health = docker inspect --format='{{.State.Health.Status}}' $Container 2>$null; if ($health -eq 'healthy') { Write-Host ('[OK] ' + $Service + ' is healthy.') -ForegroundColor Green; exit 0 }; if ($health) { Write-Host ('[WAIT] ' + $Service + ' is ' + $health + '...') -ForegroundColor Yellow }; Start-Sleep -Seconds 2 }; Write-Host ('[FAIL] ' + $Service + ' did not become healthy.') -ForegroundColor Red; exit 1"
if errorlevel 1 (
    set "EXIT_CODE=1"
    exit /b 1
)
exit /b 0

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
