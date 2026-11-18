; db-fly Windows installer (NSIS 3, per-user).
;
; Installs to %LOCALAPPDATA%\Programs\db-fly so the Wails updater can replace
; db-fly-gui.exe without administrator rights. User data/config lives in
; ~/.db-fly and is intentionally NOT removed on uninstall.
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
InstallDir "$LOCALAPPDATA\Programs\${APP_NAME}"
InstallDirRegKey HKCU "${APP_REG_KEY}" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma

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

  ; Register an install location and an entry in Add/Remove Programs.
  WriteRegStr HKCU "${APP_REG_KEY}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "db-fly"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
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

  DeleteRegKey HKCU "${APP_REG_KEY}"
  DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
