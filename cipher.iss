; Cipher — instalador (Inno Setup 6). Compilar: ISCC cipher.iss  ->  dist\Cipher-Setup-x.y.z.exe
#define AppName    "Cipher"
#define AppVer     "1.0.0"
#define AppExe     "cipher.exe"
#define AppPub     "Agustin Yarrus"
#define AppUrl     "https://github.com/agustinyarrus/cipher"
#define Exts       ".go;.c;.h;.cpp;.cc;.cxx;.hpp;.cs;.java;.class;.kt;.kts;.scala;.groovy;.py;.pyw;.rb;.rs;.swift;.js;.mjs;.cjs;.jsx;.ts;.tsx;.json;.jsonc;.html;.htm;.css;.scss;.sass;.less;.php;.lua;.pl;.pm;.sh;.bash;.zsh;.fish;.ps1;.psm1;.bat;.cmd;.sql;.r;.dart;.m;.mm;.vb;.fs;.f90;.jl;.hs;.ml;.clj;.cljs;.ex;.exs;.erl;.vue;.svelte;.astro;.toml;.yaml;.yml;.xml;.ini;.cfg;.conf;.rst;.asm;.s;.proto;.graphql;.tf"

[Setup]
AppId={{7E3C1F4A-9D52-4B86-A1C7-2F5E0C9A6D31}
AppName={#AppName}
AppVersion={#AppVer}
AppPublisher={#AppPub}
AppPublisherURL={#AppUrl}
AppSupportURL={#AppUrl}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#AppExe}
LicenseFile=LICENSE
OutputDir=dist
OutputBaseFilename={#AppName}-Setup-{#AppVer}
SetupIconFile=cipher.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
MinVersion=10.0
AppReadmeFile={app}\README.md

[Languages]
Name: "es"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "openwith";    Description: "Registrar {#AppName} en el menu ""Abrir con"" para archivos de codigo (no cambia tus apps por defecto)"; GroupDescription: "Integracion con Windows:"

[Files]
Source: "{#AppExe}";   DestDir: "{app}"; Flags: ignoreversion
Source: "cipher.ico";  DestDir: "{app}"; Flags: ignoreversion
Source: "README.md";   DestDir: "{app}"; Flags: ignoreversion isreadme
Source: "LICENSE";     DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}";              Filename: "{app}\{#AppExe}"
Name: "{group}\Desinstalar {#AppName}";  Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}";        Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Registry]
; La aplicacion: como se ve Cipher en el menu "Abrir con"
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "{#AppName}"; Flags: uninsdeletekey; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}\DefaultIcon"; ValueType: string; ValueData: "{app}\cipher.ico"; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Applications\{#AppExe}\shell\open\command"; ValueType: string; ValueData: """{app}\{#AppExe}"" ""%1"""; Tasks: openwith
; ProgID propio (opcion de "Abrir con"; no se fuerza como default de ninguna extension)
Root: HKA; Subkey: "Software\Classes\Cipher.Source"; ValueType: string; ValueData: "Codigo fuente"; Flags: uninsdeletekey; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Cipher.Source"; ValueType: string; ValueName: "FriendlyTypeName"; ValueData: "Codigo fuente"; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Cipher.Source\DefaultIcon"; ValueType: string; ValueData: "{app}\cipher.ico"; Tasks: openwith
Root: HKA; Subkey: "Software\Classes\Cipher.Source\shell\open\command"; ValueType: string; ValueData: """{app}\{#AppExe}"" ""%1"""; Tasks: openwith

[Run]
Filename: "{app}\{#AppExe}"; Description: "Abrir {#AppName} ahora"; Flags: nowait postinstall skipifsilent

[Code]
procedure AddSupportedTypes(exeName, csv: String);
var rk: Integer; stKey, owpKey, ext: String; p: Integer;
begin
  if IsAdminInstallMode then rk := HKLM else rk := HKCU;
  stKey := 'Software\Classes\Applications\' + exeName + '\SupportedTypes';
  csv := csv + ';';
  repeat
    p := Pos(';', csv);
    ext := Trim(Copy(csv, 1, p-1));
    Delete(csv, 1, p);
    if ext <> '' then begin
      RegWriteStringValue(rk, stKey, ext, '');
      // ofrecer Cipher.Source como "Abrir con" para esta extension (NO pisa el default)
      owpKey := 'Software\Classes\' + ext + '\OpenWithProgids';
      RegWriteStringValue(rk, owpKey, 'Cipher.Source', '');
    end;
  until Length(csv) = 0;
end;

procedure DelSupportedTypes(csv: String);
var rk: Integer; owpKey, ext: String; p: Integer;
begin
  if IsAdminInstallMode then rk := HKLM else rk := HKCU;
  csv := csv + ';';
  repeat
    p := Pos(';', csv);
    ext := Trim(Copy(csv, 1, p-1));
    Delete(csv, 1, p);
    if ext <> '' then begin
      owpKey := 'Software\Classes\' + ext + '\OpenWithProgids';
      RegDeleteValue(rk, owpKey, 'Cipher.Source');
    end;
  until Length(csv) = 0;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('openwith') then
    AddSupportedTypes('{#AppExe}', '{#Exts}');
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    DelSupportedTypes('{#Exts}');
end;
