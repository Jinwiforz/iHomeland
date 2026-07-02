@echo off
setlocal EnableExtensions
title iHomeland - Server Tests

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"

echo.
echo =======================
echo  iHomeland Go Tests
echo =======================
echo.

pushd "%SERVER_ROOT%"

where go >nul 2>nul
if errorlevel 1 (
    call :fail "go command is required but was not found."
    goto :finish
)

set "GOCACHE=%SERVER_ROOT%\.gocache"
set "GOMODCACHE=%SERVER_ROOT%\.gomodcache"
set "GOSUMDB=off"

echo [1/1] Running go test ./...
go test ./...
set "EXIT_CODE=%ERRORLEVEL%"

if "%EXIT_CODE%"=="0" (
    echo.
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[OK] All Go tests passed.' -ForegroundColor Green"
) else (
    echo.
    powershell -NoProfile -ExecutionPolicy Bypass -Command "Write-Host '[FAIL] Go tests failed with error code %EXIT_CODE%.' -ForegroundColor Red"
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
