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
echo Server root: %SERVER_ROOT%
if not "%~1"=="" echo Extra args : %*
echo.

pushd "%SERVER_ROOT%" >nul 2>nul
if errorlevel 1 (
    call :fail "failed to enter server root: %SERVER_ROOT%"
    goto :finish
)

if not exist "go.mod" (
    call :fail "go.mod was not found under server root."
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

if not exist "%GOCACHE%" mkdir "%GOCACHE%" >nul 2>nul
if errorlevel 1 (
    call :fail "failed to create Go cache directory: %GOCACHE%"
    goto :finish
)
if not exist "%GOMODCACHE%" mkdir "%GOMODCACHE%" >nul 2>nul
if errorlevel 1 (
    call :fail "failed to create Go module cache directory: %GOMODCACHE%"
    goto :finish
)

for /f "usebackq delims=" %%V in (`go version`) do set "GO_VERSION=%%V"
echo Go version : %GO_VERSION%
echo Go cache   : %GOCACHE%
echo Mod cache  : %GOMODCACHE%
echo.

echo [1/1] Running go test ./... %*
powershell -NoProfile -ExecutionPolicy Bypass -Command "$lines = & go test ./... %* 2>&1; $code = $LASTEXITCODE; foreach ($line in $lines) { $text = [string]$line; if ($text -match '^(ok|\?|FAIL)\s+(.+?)(\s+\(cached\)|\s+\[no test files\])?$') { if ($matches[1] -eq 'ok') { Write-Host $matches[1] -NoNewline -ForegroundColor Green } elseif ($matches[1] -eq '?') { Write-Host $matches[1] -NoNewline -ForegroundColor DarkYellow } else { Write-Host $matches[1] -NoNewline -ForegroundColor Red }; Write-Host ('    ' + $matches[2]) -NoNewline; if ($matches[3]) { Write-Host $matches[3] -ForegroundColor DarkGray } else { Write-Host '' } } elseif ($text -match '^--- FAIL:') { Write-Host $text -ForegroundColor Red } else { Write-Host $text } }; exit $code"
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
popd >nul 2>nul
if /I "%START_DIR%"=="%SCRIPT_DIR%" (
    echo.
    pause
)
exit /b %EXIT_CODE%
