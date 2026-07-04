@echo off
setlocal EnableExtensions
title iHomeland - Run Server

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
set "LOCAL_ENV_PATH=%SERVER_ROOT%\.env.local"

echo.
echo ===========================
echo  iHomeland Server Startup
echo ===========================
echo.

pushd "%SERVER_ROOT%"

call "%SCRIPT_DIR%\load-local-env.bat" "%LOCAL_ENV_PATH%"
if errorlevel 1 (
    call :fail "failed to load local environment."
    goto :finish
)

where go >nul 2>nul
if errorlevel 1 (
    call :fail "go command is required but was not found."
    goto :finish
)

set "GOCACHE=%SERVER_ROOT%\.gocache"
set "GOMODCACHE=%SERVER_ROOT%\.gomodcache"
set "GOSUMDB=off"

echo [INFO] Server root: %SERVER_ROOT%
echo [INFO] HTTP default: 127.0.0.1:8080
if defined IHOMELAND_HTTP_ADDR echo [INFO] HTTP configured: %IHOMELAND_HTTP_ADDR%
if defined IHOMELAND_MYSQL_ADDR echo [INFO] MySQL configured: %IHOMELAND_MYSQL_ADDR%
if defined IHOMELAND_REDIS_ADDR echo [INFO] Redis configured: %IHOMELAND_REDIS_ADDR%
echo [INFO] Press Ctrl+C to stop the server.
echo.

go run ./cmd/server
set "EXIT_CODE=%ERRORLEVEL%"
if not "%EXIT_CODE%"=="0" (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[FAIL] Server exited with error code %EXIT_CODE%.' -ForegroundColor Red"
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[OK] Server stopped.' -ForegroundColor Green"
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
if /I "%START_DIR%"=="%SCRIPT_DIR%" (
    echo.
    pause
)
exit /b %EXIT_CODE%
