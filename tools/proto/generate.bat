@echo off
setlocal

for /F "tokens=1 delims=#" %%E in ('"prompt #$E# & echo on & for %%B in (1) do rem"') do set "ESC=%%E"
set "C_RESET=%ESC%[0m"
set "C_INFO=%ESC%[36m"
set "C_OK=%ESC%[32m"
set "C_WARN=%ESC%[33m"
set "C_ERR=%ESC%[31m"

for %%I in ("%CD%") do set "START_DIR=%%~fI"
for %%I in ("%~dp0.") do set "SCRIPT_DIR=%%~fI"
for %%I in ("%~dp0..\..") do set "PROJECT_ROOT=%%~fI"

set "SERVER_ROOT=%PROJECT_ROOT%\server"
set "PROTO_ROOT=%PROJECT_ROOT%\shared\proto"
set "LOCAL_PROTOC=%PROJECT_ROOT%\.tools\protoc\bin\protoc.exe"
set "LOCAL_PROTOC_GEN_GO=%PROJECT_ROOT%\.tools\go\bin\protoc-gen-go.exe"
set "LOCAL_PROTOC_INCLUDE=%PROJECT_ROOT%\.tools\protoc\include"
set "PROTO_FILE=shared\proto\realtime\v1\envelope.proto"
set "GO_OUT=server\internal\protocol\pb"
set "GO_FILE=%GO_OUT%\realtime\v1\envelope.pb.go"
set "CS_OUT=client\Assets\App\Scripts\Protocol\Pb\Realtime\V1"
set "CS_FILE=%CS_OUT%\Envelope.cs"

echo %C_INFO%[proto] project root:%C_RESET% %PROJECT_ROOT%
echo %C_INFO%[proto] proto root:  %C_RESET% %PROTO_ROOT%
echo %C_INFO%[proto] Go output:   %C_RESET% %PROJECT_ROOT%\%GO_OUT%
echo %C_INFO%[proto] C# output:   %C_RESET% %PROJECT_ROOT%\%CS_OUT%
echo.

pushd "%PROJECT_ROOT%" || goto fail_pushd

set "NEED_PROTO_TOOLS="
if not exist "%LOCAL_PROTOC%" set "NEED_PROTO_TOOLS=1"
if not exist "%LOCAL_PROTOC_GEN_GO%" set "NEED_PROTO_TOOLS=1"

if defined NEED_PROTO_TOOLS (
    echo %C_WARN%[proto] project-local protocol tools are incomplete, preparing them now.%C_RESET%
    call "%PROJECT_ROOT%\tools\proto\setup.bat"
    if errorlevel 1 (
        set "EXIT_CODE=1"
        echo %C_ERR%[proto] ERROR: failed to prepare project-local protocol tools.%C_RESET%
        goto finish
    )
)

set "PROTOC=protoc"
if exist "%LOCAL_PROTOC%" (
    set "PROTOC=%LOCAL_PROTOC%"
    echo %C_INFO%[proto] using local protoc:%C_RESET% %LOCAL_PROTOC%
) else (
    echo %C_WARN%[proto] local protoc not found, trying PATH.%C_RESET%
)

echo %C_INFO%[proto] checking protoc...%C_RESET%
"%PROTOC%" --version
if errorlevel 1 (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: protoc is required.%C_RESET%
    echo %C_ERR%[proto] Run tools\proto\setup.bat to install project-local protocol tools.%C_RESET%
    goto finish
)

echo %C_INFO%[proto] checking protoc-gen-go...%C_RESET%
set "PROTOC_GEN_GO_DIR="
if exist "%LOCAL_PROTOC_GEN_GO%" (
    for %%I in ("%LOCAL_PROTOC_GEN_GO%") do set "PROTOC_GEN_GO_DIR=%%~dpI"
    echo %C_INFO%[proto] using local protoc-gen-go:%C_RESET% %LOCAL_PROTOC_GEN_GO%
)
if defined PROTOC_GEN_GO_DIR set "PATH=%PROTOC_GEN_GO_DIR%;%PATH%"
where protoc-gen-go >nul 2>nul
if errorlevel 1 (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: protoc-gen-go is required.%C_RESET%
    echo %C_ERR%[proto] Run tools\proto\setup.bat to install project-local protocol tools.%C_RESET%
    goto finish
)
where protoc-gen-go

if not exist "%PROTO_FILE%" (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: proto file not found:%C_RESET% %PROTO_FILE%
    goto finish
)

if not exist "%GO_OUT%" (
    echo %C_INFO%[proto] creating output dir:%C_RESET% %GO_OUT%
    mkdir "%GO_OUT%"
    if errorlevel 1 (
        set "EXIT_CODE=1"
        echo %C_ERR%[proto] ERROR: failed to create output dir:%C_RESET% %GO_OUT%
        goto finish
    )
)

if not exist "%CS_OUT%" (
    echo %C_INFO%[proto] creating output dir:%C_RESET% %CS_OUT%
    mkdir "%CS_OUT%"
    if errorlevel 1 (
        set "EXIT_CODE=1"
        echo %C_ERR%[proto] ERROR: failed to create output dir:%C_RESET% %CS_OUT%
        goto finish
    )
)

echo.
echo %C_INFO%[proto] generating Go code...%C_RESET%
"%PROTOC%" ^
  --proto_path=shared\proto ^
  --proto_path="%LOCAL_PROTOC_INCLUDE%" ^
  --go_out=%GO_OUT% ^
  --go_opt=paths=source_relative ^
  %PROTO_FILE%

if errorlevel 1 (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: protoc generation failed.%C_RESET%
    goto finish
)

if not exist "%GO_FILE%" (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: expected output not found:%C_RESET% %GO_FILE%
    goto finish
)

echo.
echo %C_INFO%[proto] generating Unity C# code...%C_RESET%
"%PROTOC%" ^
  --proto_path=shared\proto ^
  --proto_path="%LOCAL_PROTOC_INCLUDE%" ^
  --csharp_out=%CS_OUT% ^
  %PROTO_FILE%

if errorlevel 1 (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: Unity C# protoc generation failed.%C_RESET%
    goto finish
)

if not exist "%CS_FILE%" (
    set "EXIT_CODE=1"
    echo %C_ERR%[proto] ERROR: expected output not found:%C_RESET% %CS_FILE%
    goto finish
)

set "EXIT_CODE=0"
echo %C_OK%[proto] generated:%C_RESET% %GO_FILE%
echo %C_OK%[proto] generated:%C_RESET% %CS_FILE%
echo %C_OK%[proto] done.%C_RESET%

:finish
popd
echo.
if "%EXIT_CODE%"=="0" (
    echo %C_OK%[proto] result: success%C_RESET%
) else (
    echo %C_ERR%[proto] result: failed with code %EXIT_CODE%%C_RESET%
)
if /I "%START_DIR%"=="%SCRIPT_DIR%" pause
exit /b %EXIT_CODE%

:fail_pushd
echo %C_ERR%[proto] ERROR: failed to enter project root:%C_RESET% %PROJECT_ROOT%
if /I "%START_DIR%"=="%SCRIPT_DIR%" pause
exit /b 1
