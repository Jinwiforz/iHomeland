@echo off
setlocal

for %%I in ("%~dp0..") do set "SERVER_ROOT=%%~fI"
pushd "%SERVER_ROOT%"

set "GOCACHE=%SERVER_ROOT%\.gocache"
set "GOMODCACHE=%SERVER_ROOT%\.gomodcache"
set "GOSUMDB=off"

go run ./cmd/server
set "EXIT_CODE=%ERRORLEVEL%"
popd
exit /b %EXIT_CODE%
