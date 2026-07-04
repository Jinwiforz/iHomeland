@echo off
if "%~1"=="" (
    for %%I in ("%~dp0..\.env.local") do set "LOCAL_ENV_PATH=%%~fI"
) else (
    for %%I in ("%~1") do set "LOCAL_ENV_PATH=%%~fI"
)

if not exist "%LOCAL_ENV_PATH%" (
    echo [INFO] Local env file not found: %LOCAL_ENV_PATH%
    echo [INFO] Run server\scripts\setup-local-env.bat to create one.
    exit /b 0
)

echo [INFO] Loading local env: %LOCAL_ENV_PATH%
for /f "usebackq eol=# tokens=1,* delims==" %%A in ("%LOCAL_ENV_PATH%") do (
    if not "%%A"=="" if not defined %%A set "%%A=%%B"
)
exit /b 0
