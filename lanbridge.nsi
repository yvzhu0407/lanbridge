; lanbridge.nsi:Windows 安装包（NSIS）
;
; 用法（由 build-windows-setup.sh 调用，一般不需要手动跑）:
;   makensis -DVERSION=1.0.0 -DSRCDIR=dist -DOUTFILE=dist/LANBridge-1.0.0-windows-amd64-setup.exe lanbridge.nsi
;
; 安装后的行为：
;   - 主程序装到 Program Files\LANBridge\lanbridge-client.exe（管理员安装）
;   - 数据仍在 %ProgramData%\lanbridge\ 和用户目录，卸载时不动（避免误删玩家的配置）
;   - 卸载器会结束残留进程；TAP 驱动与防火墙规则不在卸载器里删，
;     因为它们可能被其他 ZeroTier 实例共用，误删会破坏别人的网络。

Unicode true

!include "MUI2.nsh"
!include "x64.nsh"

!ifndef VERSION
  !define VERSION "1.0.0"
!endif
!ifndef SRCDIR
  !define SRCDIR "dist"
!endif
!ifndef OUTFILE
  !define OUTFILE "dist/LANBridge-setup.exe"
!endif
; VIProductVersion 只接受纯数字 X.X.X.X，预发布后缀（1.0.0-test）会报错，
; 由构建脚本裁出前三段传入。
!ifndef VERNUM
  !define VERNUM "1.0.0"
!endif

!define APPNAME "LANBridge"
!define COMPANY "LANBridge"
!define EXENAME "lanbridge-client.exe"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\LANBridge"
!define ROOT "${__FILEDIR__}"

Name "桥联 LANBridge"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\LANBridge"
InstallDirRegKey HKLM "Software\LANBridge" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${VERNUM}.0"
VIAddVersionKey "ProductName" "桥联 LANBridge"
VIAddVersionKey "FileDescription" "LANBridge 局域网联机客户端"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MIT License"

; ---------------- 界面 ----------------
!define MUI_ABORTWARNING
!define MUI_ICON "${ROOT}\client\icon.ico"
!define MUI_UNICON "${ROOT}\client\icon.ico"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXENAME}"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 LANBridge"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; ---------------- 安装 ----------------
Section "主程序" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"

  ; 已安装版本正在运行则先结束，否则文件替换会失败
  nsExec::Exec 'taskkill /F /IM ${EXENAME}'
  Pop $0

  File "/oname=${EXENAME}" "${SRCDIR}\lanbridge-client.exe"
  File "/oname=LICENSE.txt" "${ROOT}\LICENSE"

  WriteRegStr HKLM "Software\LANBridge" "InstallDir" "$INSTDIR"

  ; 卸载信息（控制面板-程序和功能）
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "桥联 LANBridge"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${COMPANY}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\${EXENAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1

  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; 快捷方式：开始菜单 + 桌面
  CreateDirectory "$SMPROGRAMS\LANBridge"
  CreateShortcut "$SMPROGRAMS\LANBridge\桥联 LANBridge.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\${EXENAME}" 0
  CreateShortcut "$SMPROGRAMS\LANBridge\卸载 LANBridge.lnk" "$INSTDIR\uninstall.exe"
  CreateShortcut "$DESKTOP\桥联 LANBridge.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\${EXENAME}" 0
SectionEnd

Section "开机自启" SecAutostart
  ; 客户端需要常驻维持虚拟网卡，默认勾选；用户可取消
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "LANBridge" '"$INSTDIR\${EXENAME}"'
SectionEnd

; ---------------- 卸载 ----------------
Section "Uninstall"
  nsExec::Exec 'taskkill /F /IM ${EXENAME}'
  Pop $0

  Delete "$DESKTOP\桥联 LANBridge.lnk"
  Delete "$SMPROGRAMS\LANBridge\桥联 LANBridge.lnk"
  Delete "$SMPROGRAMS\LANBridge\卸载 LANBridge.lnk"
  RMDir "$SMPROGRAMS\LANBridge"

  DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "LANBridge"
  DeleteRegKey HKLM "${UNINSTKEY}"
  DeleteRegKey HKLM "Software\LANBridge"

  Delete "$INSTDIR\${EXENAME}"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"

  ; 保留 %ProgramData%\lanbridge\（内嵌 ZeroTier 与驱动文件）和用户数据目录：
  ; 重装时能直接复用已装的驱动，也不会丢房间配置。
SectionEnd
