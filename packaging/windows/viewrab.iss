; Inno Setup script for the Viewrab installer. It packs the unpacked portable
; zip, dist\viewrab-<version>-windows-<arch>, and writes the installer next to
; it, wherever iscc runs (packaging\windows\installers.ps1 runs it):
;
;   iscc /DVersion=1.2.0 /DArch=amd64 packaging\windows\viewrab.iss
;
; It installs for the current user without asking for administrator rights
; (or for everyone, if the user picks that), adds a Start menu entry, and
; registers Viewrab as a program that can open PDFs. Windows does not let a
; program make itself the default one; the last page offers to open the
; settings page where the user can choose it.

#if !Defined(Version) || !Defined(Arch)
  #error Pass the version and architecture: /DVersion=1.2.0 /DArch=amd64
#endif
#define Dist SourcePath + "..\..\dist"
#define Source Dist + "\viewrab-" + Version + "-windows-" + Arch
; The numeric version Windows shows, as make winres stamped it into the .exe.
#define NumericVersion GetVersionNumbersString(Source + "\viewrab.exe")

#define Name "Viewrab"
#define Exe "viewrab.exe"
#define ProgID "Viewrab.PDF"
#define Website "https://github.com/voidrab/viewrab"

[Setup]
; AppId names the installation; it must stay the same for every release so
; that an update replaces the installed copy.
AppId={{38B69CD5-09CE-41AB-AE13-25C7570D3D99}
AppName={#Name}
AppVersion={#Version}
AppVerName={#Name} {#Version}
AppPublisher=The Viewrab authors
AppPublisherURL={#Website}
AppSupportURL={#Website}/issues
AppUpdatesURL={#Website}/releases
AppCopyright=© 2026 The Viewrab authors
VersionInfoVersion={#NumericVersion}
VersionInfoProductName={#Name}
VersionInfoDescription={#Name} setup

DefaultDirName={autopf}\{#Name}
DefaultGroupName={#Name}
DisableProgramGroupPage=yes
DisableDirPage=auto
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
MinVersion=10.0.17134
ChangesAssociations=yes
CloseApplications=yes

SetupIconFile={#SourcePath}..\..\build\windows\appicon.ico
UninstallDisplayIcon={app}\{#Exe}
UninstallDisplayName={#Name}
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
OutputDir={#Dist}
OutputBaseFilename=viewrab-{#Version}-windows-{#Arch}-setup

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
; The program and its licences: everything the portable zip holds.
Source: "{#Source}\*"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#Name}"; Filename: "{app}\{#Exe}"
Name: "{autodesktop}\{#Name}"; Filename: "{app}\{#Exe}"; Tasks: desktopicon

[Registry]
; The ProgID PDFs open through, listed under "Open with" for .pdf.
Root: HKA; Subkey: "Software\Classes\{#ProgID}"; ValueType: string; ValueName: ""; ValueData: "PDF document"; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\{#ProgID}\DefaultIcon"; ValueType: string; ValueName: ""; ValueData: """{app}\{#Exe}"",0"
Root: HKA; Subkey: "Software\Classes\{#ProgID}\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#Exe}"" ""%1"""
Root: HKA; Subkey: "Software\Classes\.pdf\OpenWithProgids"; ValueType: string; ValueName: "{#ProgID}"; ValueData: ""; Flags: uninsdeletevalue
; The program itself, for "Open with" and the Run dialog.
Root: HKA; Subkey: "Software\Classes\Applications\{#Exe}"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "{#Name}"; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\Applications\{#Exe}\SupportedTypes"; ValueType: string; ValueName: ".pdf"; ValueData: ""
Root: HKA; Subkey: "Software\Microsoft\Windows\CurrentVersion\App Paths\{#Exe}"; ValueType: string; ValueName: ""; ValueData: "{app}\{#Exe}"; Flags: uninsdeletekey
; Its capabilities, which list it in Settings > Apps > Default apps.
Root: HKA; Subkey: "Software\{#Name}"; Flags: uninsdeletekeyifempty
Root: HKA; Subkey: "Software\{#Name}\Capabilities"; ValueType: string; ValueName: "ApplicationName"; ValueData: "{#Name}"; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\{#Name}\Capabilities"; ValueType: string; ValueName: "ApplicationDescription"; ValueData: "A fast, small and simple PDF viewer with PDF/A validation"
Root: HKA; Subkey: "Software\{#Name}\Capabilities\FileAssociations"; ValueType: string; ValueName: ".pdf"; ValueData: "{#ProgID}"
Root: HKA; Subkey: "Software\RegisteredApplications"; ValueType: string; ValueName: "{#Name}"; ValueData: "Software\{#Name}\Capabilities"; Flags: uninsdeletevalue

[Run]
Filename: "{app}\{#Exe}"; Description: "{cm:LaunchProgram,{#Name}}"; Flags: nowait postinstall skipifsilent
Filename: "ms-settings:defaultapps?registeredApp{code:Scope}={#Name}"; Description: "Choose Viewrab as the default PDF viewer"; Flags: shellexec nowait postinstall skipifsilent unchecked

[Code]
// Scope names who the installation is for, as the Default apps settings
// page expects it: User or Machine.
function Scope(Param: String): String;
begin
  if IsAdminInstallMode then
    Result := 'Machine'
  else
    Result := 'User';
end;
