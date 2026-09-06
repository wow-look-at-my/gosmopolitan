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

:: dist test tests the HOST port, and every go command it starts has to agree.
:: env.bat carries the fork's default TARGET in GOOS, which is cosmo, so the
:: host values replace it. Otherwise every test binary is an APE, and NT starts
:: a program by its extension rather than by reading a shell header.
set GOOS=%GOHOSTOS%
set GOARCH=%GOHOSTARCH%

set GOPATH=c:\nonexist-gopath
..\bin\go tool dist test --rebuild %* || exit /b 1
