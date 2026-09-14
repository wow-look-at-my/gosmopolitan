@echo off
rem Copyright 2026 The Go Authors. All rights reserved.
rem Use of this source code is governed by a BSD-style
rem license that can be found in the LICENSE file.
rem
rem Move every org submodule onto the head of the branch it follows. See
rem submodulebranch.bash for why: the branch this repository has checked out
rem wins when the submodule has one, and the branch .gitmodules names for it is
rem used otherwise. git submodule update --init --remote takes the branch to
rem follow from submodule.<name>.branch, so that is what this sets.

setlocal
cd /d "%~dp0.."
if not exist .gitmodules exit /b 0

set "here="
for /f "delims=" %%i in ('git rev-parse --abbrev-ref HEAD 2^>nul') do set "here=%%i"
if "%here%"=="HEAD" set "here="

for /f "tokens=1,2" %%a in ('git config -f .gitmodules --get-regexp submodule') do call :one "%%a" "%%b"
exit /b 0

:one
setlocal
set "key=%~1"
if not "%key:~-5%"==".path" exit /b 0
set "name=%key:~10,-5%"
set "path=%~2"

set "url="
for /f "delims=" %%u in ('git config -f .gitmodules "submodule.%name%.url"') do set "url=%%u"
echo %url% | findstr /c:"github.com/wow-look-at-my/" >nul || exit /b 0

set "branch="
for /f "delims=" %%b in ('git config -f .gitmodules "submodule.%name%.branch" 2^>nul') do set "branch=%%b"
if defined here (
	git ls-remote --exit-code --heads "%url%" "refs/heads/%here%" >nul 2>nul
	if not errorlevel 1 set "branch=%here%"
)
if not defined branch exit /b 0

git config "submodule.%name%.branch" "%branch%"
git submodule update --init --remote -- "%path%" 2>nul
if errorlevel 1 (
	echo submodulebranch: %path% stays where it is: cannot reach %url%
) else (
	echo submodulebranch: %path% at %branch%
)
exit /b 0
