:: Copyright 2026 The Go Authors. All rights reserved.
:: Use of this source code is governed by a BSD-style
:: license that can be found in the LICENSE file.

:: go_cosmo_amd64_exec.bat runs a GOOS=cosmo test binary on an NT host.
:: cmd/go looks for it on %PATH% whenever GOOS is not the host GOOS.
::
:: The APE boots natively through its own PE header, but NT starts a
:: program by its extension, so the binary gets an .exe name first. A
:: hard link costs no disk: dist test links one APE per test package,
:: and a copy of each doubles that.
::
:: The argument list is NOT rebuilt token by token. cmd.exe's tokeniser
:: treats "=", "," and ";" as separators, so "%~1" turns
:: -test.short=true into two arguments, and the test binary reads "true"
:: as a positional. Everything after the first token is passed through
:: exactly as it arrived.

@echo off
setlocal
if "%~1"=="" (
    echo usage: %~n0 binary [args...] 1>&2
    exit /b 2
)
set "BIN=%~1"
set "EXT=%~x1"

set "EXE=%BIN%"
if /i not "%EXT%"==".exe" (
    set "EXE=%BIN%.exe"
    if not exist "%BIN%.exe" (
        mklink /h "%BIN%.exe" "%BIN%" >nul 2>&1 || copy /y "%BIN%" "%BIN%.exe" >nul || exit /b 1
    )
)

:: %* still carries the binary, so cut the first token off the front.
set "ARGS=%*"
call set "ARGS=%%ARGS:*%1=%%"

"%EXE%"%ARGS%
exit /b %ERRORLEVEL%
