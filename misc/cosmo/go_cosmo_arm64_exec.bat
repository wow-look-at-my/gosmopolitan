:: Copyright 2026 The Go Authors. All rights reserved.
:: Use of this source code is governed by a BSD-style
:: license that can be found in the LICENSE file.

:: go_cosmo_arm64_exec.bat runs a GOOS=cosmo test binary on an NT host.
:: cmd/go looks for it on %PATH% whenever GOOS is not the host GOOS.
::
:: The APE boots natively through its own PE header, but NT starts a
:: program by its extension, so the binary is copied to a .exe first.

@echo off
setlocal
if "%~1"=="" (
    echo usage: %~n0 binary [args...] 1>&2
    exit /b 2
)
set "BIN=%~1"
set "EXT=%~x1"
shift

set "EXE=%BIN%"
if /i not "%EXT%"==".exe" (
    set "EXE=%BIN%.exe"
    copy /y "%BIN%" "%BIN%.exe" >nul || exit /b 1
)

:: %* still carries the binary, so the argument list is rebuilt.
set "ARGS="
:args
if "%~1"=="" goto run
set ARGS=%ARGS% "%~1"
shift
goto args

:run
"%EXE%"%ARGS%
exit /b %ERRORLEVEL%
