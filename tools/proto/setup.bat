@echo off
setlocal EnableExtensions

for /F "tokens=1 delims=#" %%E in ('"prompt #$E# & echo on & for %%B in (1) do rem"') do set "ESC=%%E"
set "C_RESET=%ESC%[0m"
set "C_INFO=%ESC%[36m"
set "C_OK=%ESC%[32m"
set "C_WARN=%ESC%[33m"
set "C_ERR=%ESC%[31m"

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..\..") do set "PROJECT_ROOT=%%~fI"

set "PROTOC_VERSION=27.3"
if not "%~1"=="" set "PROTOC_VERSION=%~1"

set "TOOLS_ROOT=%PROJECT_ROOT%\.tools"
set "PROTOC_ROOT=%TOOLS_ROOT%\protoc"
set "PROTOC_EXE=%PROTOC_ROOT%\bin\protoc.exe"
set "PROTOC_GEN_GO_VERSION=v1.34.1"
set "GOBIN=%TOOLS_ROOT%\go\bin"
set "PROTOC_GEN_GO_EXE=%GOBIN%\protoc-gen-go.exe"

if exist "%ProgramFiles%\Go\bin\go.exe" set "PATH=%ProgramFiles%\Go\bin;%PATH%"
if exist "%USERPROFILE%\go\bin\go.exe" set "PATH=%USERPROFILE%\go\bin;%PATH%"

echo %C_INFO%[proto-tools] project root:%C_RESET% %PROJECT_ROOT%
echo %C_INFO%[proto-tools] protoc:     %C_RESET% %PROTOC_EXE%
echo %C_INFO%[proto-tools] protoc-gen-go:%C_RESET% %PROTOC_GEN_GO_EXE%
echo.

where powershell >nul 2>nul
if errorlevel 1 (
    echo %C_ERR%[proto-tools] ERROR: powershell is required.%C_RESET%
    exit /b 1
)

if exist "%PROTOC_EXE%" (
    echo %C_OK%[proto-tools] local protoc already exists.%C_RESET%
) else (
    echo %C_INFO%[proto-tools] installing protoc %PROTOC_VERSION% into .tools...%C_RESET%
    powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference = 'Stop'; $version = '%PROTOC_VERSION%'; $tools = '%TOOLS_ROOT%'; $target = '%PROTOC_ROOT%'; $downloads = Join-Path $tools 'downloads'; $zip = Join-Path $downloads ('protoc-' + $version + '-win64.zip'); $url = 'https://github.com/protocolbuffers/protobuf/releases/download/v' + $version + '/protoc-' + $version + '-win64.zip'; New-Item -ItemType Directory -Force -Path $downloads | Out-Null; if (-not (Test-Path $zip)) { Write-Host ('[proto-tools] downloading ' + $url); Invoke-WebRequest -Uri $url -OutFile $zip }; $tmp = Join-Path $tools ('protoc-' + $version + '-tmp'); if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }; New-Item -ItemType Directory -Force -Path $tmp | Out-Null; Expand-Archive -Path $zip -DestinationPath $tmp -Force; if (Test-Path $target) { Remove-Item -Recurse -Force $target }; New-Item -ItemType Directory -Force -Path $target | Out-Null; Move-Item -Path (Join-Path $tmp 'bin') -Destination (Join-Path $target 'bin'); Move-Item -Path (Join-Path $tmp 'include') -Destination (Join-Path $target 'include'); Remove-Item -Recurse -Force $tmp"
    if errorlevel 1 (
        echo %C_ERR%[proto-tools] ERROR: failed to install protoc.%C_RESET%
        exit /b 1
    )
)

if not exist "%PROTOC_EXE%" (
    echo %C_ERR%[proto-tools] ERROR: protoc was not found after install:%C_RESET% %PROTOC_EXE%
    exit /b 1
)

"%PROTOC_EXE%" --version
if errorlevel 1 (
    echo %C_ERR%[proto-tools] ERROR: local protoc is not executable.%C_RESET%
    exit /b 1
)

where go >nul 2>nul
if errorlevel 1 (
    echo %C_ERR%[proto-tools] ERROR: go command is required to install protoc-gen-go.%C_RESET%
    exit /b 1
)

if exist "%PROTOC_GEN_GO_EXE%" (
    echo %C_OK%[proto-tools] local protoc-gen-go already exists.%C_RESET%
) else (
    echo %C_INFO%[proto-tools] installing protoc-gen-go %PROTOC_GEN_GO_VERSION% into .tools...%C_RESET%
    if not exist "%GOBIN%" mkdir "%GOBIN%" >nul 2>nul
    go install google.golang.org/protobuf/cmd/protoc-gen-go@%PROTOC_GEN_GO_VERSION%
    if errorlevel 1 (
        echo %C_ERR%[proto-tools] ERROR: failed to install protoc-gen-go.%C_RESET%
        exit /b 1
    )
)

if not exist "%PROTOC_GEN_GO_EXE%" (
    echo %C_ERR%[proto-tools] ERROR: protoc-gen-go was not found after install:%C_RESET% %PROTOC_GEN_GO_EXE%
    exit /b 1
)

"%PROTOC_GEN_GO_EXE%" --version
if errorlevel 1 (
    echo %C_ERR%[proto-tools] ERROR: local protoc-gen-go is not executable.%C_RESET%
    exit /b 1
)

echo.
echo %C_OK%[proto-tools] local protocol tools are ready.%C_RESET%
if /I "%START_DIR%"=="%SCRIPT_DIR%" pause
exit /b 0
