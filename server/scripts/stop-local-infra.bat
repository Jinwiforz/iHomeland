@echo off
setlocal EnableExtensions
title iHomeland - Stop Local Infrastructure

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"

echo.
echo =======================================
echo  iHomeland Local Infrastructure Stop
echo =======================================
echo.

pushd "%SERVER_ROOT%"

where docker >nul 2>nul
if errorlevel 1 (
    call :fail "docker command is required but was not found. Start Docker Desktop and try again."
    goto :finish
)

if not exist "%SERVER_ROOT%\compose.yaml" (
    call :fail "compose.yaml was not found at %SERVER_ROOT%\compose.yaml."
    goto :finish
)

echo [1/1] Stopping MySQL and Redis containers. Data volumes are kept.
docker compose -f "%SERVER_ROOT%\compose.yaml" down
if errorlevel 1 (
    call :fail "docker compose down failed."
    goto :finish
)

echo.
echo [OK] Local infrastructure stopped.
set "EXIT_CODE=0"
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
