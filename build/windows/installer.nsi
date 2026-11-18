; db-fly Windows installer (NSIS 3, per-machine).
;
; Installs to C:\Program Files\db-fly by default and requires administrator
; rights during setup. The installing user is granted Modify permission on the
; install directory so the silent Wails updater can replace db-fly-gui.exe
; without a UAC prompt on every launch. User data/config lives in ~/.db-fly
; and is intentionally NOT removed on uninstall.
;
; Expected cwd when compiled: build/windows (see build/installer.ps1).
; Override with: makensis /DAPP_VERSION=1.2.3 /DAPP_VERSION_NUM=1.2.3.0 installer.nsi

!include "MUI2.nsh"

!ifndef APP_VERSION
  !define APP_VERSION "dev"
!endif
!ifndef APP_VERSION_NUM
  !define APP_VERSION_NUM "0.0.0.0"
!endif

!define APP_NAME "db-fly"
!define APP_EXE "db-fly-gui.exe"
!define APP_REG_KEY "Software\db-fly"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\db-fly"

Name "${APP_NAME}"
OutFile "..\..\dist\db-fly-setup-windows-amd64.exe"
InstallDir "$PROGRAMFILES64\${APP_NAME}"
InstallDirRegKey HKLM "${APP_REG_KEY}" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma

Function .onInit
  ; Keep HKLM lookups out of the 32-bit registry redirect on 64-bit Windows.
  SetRegView 64
FunctionEnd

; Version resource shown in Explorer / Add or Remove Programs.
VIProductVersion "${APP_VERSION_NUM}"
VIAddVersionKey "ProductName" "db-fly"
VIAddVersionKey "ProductVersion" "${APP_VERSION}"
VIAddVersionKey "FileVersion" "${APP_VERSION_NUM}"
VIAddVersionKey "FileDescription" "db-fly Multi-Database Client"
VIAddVersionKey "CompanyName" "db-fly"
VIAddVersionKey "LegalCopyright" "db-fly"

; --- Interfaces -----------------------------------------------------------
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

; --- Install --------------------------------------------------------------
Section "Install" SEC_INSTALL
  SetOutPath "$INSTDIR"
  File "..\..\dist\${APP_EXE}"

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; Start menu + desktop shortcuts. Overwrite on reinstall so they stay valid.
  CreateDirectory "$SMPROGRAMS\${APP_NAME}"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk" "$INSTDIR\Uninstall.exe"
  CreateShortCut "$DESKTOP\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"

  ; Let the installing user update in place without a UAC prompt on every
  ; launch. Program Files stays per-machine; only this app folder is granted.
  ReadEnvStr $0 USERDOMAIN
  ReadEnvStr $1 USERNAME
  nsExec::ExecToLog 'icacls "$INSTDIR" /grant "$0\$1:(OI)(CI)M" /T'

  ; Register an install location and an entry in Add/Remove Programs.
  SetRegView 64
  WriteRegStr HKLM "${APP_REG_KEY}" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "db-fly"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
SectionEnd

; --- Uninstall ------------------------------------------------------------
Section "Uninstall"
  Delete "$INSTDIR\${APP_EXE}"
  Delete "$INSTDIR\Uninstall.exe"
  ; The install dir is dedicated to db-fly; only logs may appear there.
  ; ~/.db-fly (config, credentials, history) is deliberately left untouched.
  RMDir /r "$INSTDIR"

  Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
  Delete "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk"
  RMDir "$SMPROGRAMS\${APP_NAME}"
  Delete "$DESKTOP\${APP_NAME}.lnk"

  SetRegView 64
  DeleteRegKey HKLM "${APP_REG_KEY}"
  DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd
