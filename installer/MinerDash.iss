#ifndef MyAppVersion
  #define MyAppVersion "1.0.2"
#endif
#ifndef SourceDir
  #define SourceDir "..\build\windows-installer"
#endif
#ifndef OutputDir
  #define OutputDir "..\build"
#endif

[Setup]
AppId={{71391369-2104-4C80-A395-6B6E9FB96635}
AppName=Miner Dash
AppVersion={#MyAppVersion}
AppVerName=Miner Dash {#MyAppVersion}
AppPublisher=BitsPleaseYT
AppPublisherURL=https://github.com/OBitsPlease/Miner-Dash
AppSupportURL=https://github.com/OBitsPlease/Miner-Dash/issues
AppUpdatesURL=https://github.com/OBitsPlease/Miner-Dash/releases
DefaultDirName={autopf}\MinerDash
DisableDirPage=yes
DisableProgramGroupPage=yes
OutputDir={#OutputDir}
OutputBaseFilename=MinerDash-Setup-{#MyAppVersion}
SetupIconFile={#SourceDir}\assets\branding\miningdash.ico
UninstallDisplayIcon={app}\miningdash.ico
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
Compression=lzma2/fast
SolidCompression=yes
WizardStyle=modern
CloseApplications=no
RestartIfNeededByRun=no
VersionInfoVersion={#MyAppVersion}
VersionInfoCompany=BitsPleaseYT
VersionInfoDescription=Miner Dash Windows Installer
VersionInfoProductName=Miner Dash
VersionInfoProductVersion={#MyAppVersion}

[Files]
Source: "{#SourceDir}\*"; DestDir: "{tmp}\MinerDashPayload"; Flags: recursesubdirs createallsubdirs deleteafterinstall
Source: "{#SourceDir}\scripts\uninstall-controller.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\assets\branding\miningdash.ico"; DestDir: "{app}"; Flags: ignoreversion

[Run]
Filename: "https://localhost:8443"; Description: "Open Miner Dash"; Flags: shellexec postinstall skipifsilent nowait

[UninstallRun]
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\uninstall-controller.ps1"""; Flags: runhidden waituntilterminated; RunOnceId: "RemoveMinerDashController"

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
  PowerShellPath: String;
  ScriptPath: String;
begin
  if CurStep <> ssPostInstall then
    Exit;

  WizardForm.StatusLabel.Caption := 'Installing and starting the Miner Dash controller...';
  PowerShellPath := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  ScriptPath := ExpandConstant('{tmp}\MinerDashPayload\scripts\install-controller-wizard.ps1');
  if not Exec(
    PowerShellPath,
    '-NoProfile -ExecutionPolicy Bypass -File "' + ScriptPath + '"',
    '',
    SW_SHOWNORMAL,
    ewWaitUntilTerminated,
    ResultCode
  ) then
    RaiseException('Windows could not start the Miner Dash installation script.');

  if ResultCode <> 0 then
    RaiseException('Miner Dash installation failed. Review the setup message for details.');
end;
