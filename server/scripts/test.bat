@echo off
setlocal

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
pushd "%SERVER_ROOT%"

set "GOCACHE=%SERVER_ROOT%\.gocache"
set "GOMODCACHE=%SERVER_ROOT%\.gomodcache"
set "GOSUMDB=off"

go test ./...
set "EXIT_CODE=%ERRORLEVEL%"
popd

if /I "%START_DIR%"=="%SCRIPT_DIR%" (
    echo.
    pause
)

exit /b %EXIT_CODE%
