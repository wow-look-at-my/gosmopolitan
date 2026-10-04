:: Copyright 2012 The Go Authors. All rights reserved.
:: Use of this source code is governed by a BSD-style
:: license that can be found in the LICENSE file.

@echo off

if not exist ..\bin\go.exe (
    echo Must run run.bat from Go src directory after installing cmd/go.
    exit /b 1
)

setlocal

set GOENV=off
..\bin\go tool dist env > env.bat || exit /b 1
call .\env.bat
del env.bat

:: Test binaries are APEs. One boots natively through its own PE header, but
:: NT starts a program by its extension, so cmd/go runs a cross-GOOS test
:: binary through go_%GOOS%_%GOARCH%_exec.bat. misc\cosmo goes on PATH.
set PATH=%CD%\..\misc\cosmo;%PATH%

set GOOS=%GOHOSTOS%
set GOARCH=%GOHOSTARCH%

:: An empty GOPATH of its own keeps the suite off the user's packages. Every
:: test shares its module cache, so a module downloads once per run.
for %%G in ("%CD%\..") do set "GOPATH=%%~fG\pkg\gopath"
if not exist "%GOPATH%" mkdir "%GOPATH%" || exit /b 1
..\bin\go tool dist test %* || exit /b 1
