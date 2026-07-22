; Cipher — instalador (Inno Setup 6). Compilar: ISCC cipher.iss  ->  dist\Cipher-Setup-x.y.z.exe
#define AppName    "Cipher"
#define AppVer     "1.0.0"
#define AppExe     "cipher.exe"
#define AppPub     "Agustin Yarrus"
#define AppUrl     "https://github.com/agustinyarrus/cipher"
#define Exts       ".1p;.3pm;.6pl;.6pm;.abap;.abnf;.ada;.adb;.ads;.agda;.ahk;.ahkl;.al;.als;.apl;.applescript;.aql;.arexx;.art;.as;.asm;.atl;.au3;.automount;.aux;.avsc;.awk;.b;.bal;.bas;.bash;.batch;.bb;.bf;.bib;.bicep;.bnf;.bqn;.build;.bzl;.c;.c++;.c3;.c3i;.c3t;.capnp;.cc;.cdf;.ceylon;.cf;.cfg;.cginc;.chai;.chpl;.cjs;.cl;.class;.clj;.cls;.cmake;.cob;.coffee;.container;.containerfile;.core;.cp;.cpp;.cpy;.cql;.cr;.cs;.csh;.csproj;.css;.csv;.cts;.cue;.cxx;.d;.dal;.dart;.dax;.decls;.def;.desktop;.device;.di;.diff;.dnssd;.docker;.dockerfile;.dpk;.dpr;.dtd;.dts;.dtsi;.duby;.dyl;.dylan;.dzn;.ebnf;.ebuild;.ecl;.eclass;.edn;.eex;.el;.elm;.env;.epf;.eps;.epsf;.epsi;.erb;.erf;.erl;.es;.escript;.ex;.exheres-0;.exlib;.exs;.f;.f03;.f90;.f95;.factor;.feature;.fennel;.fish;.frag;.frt;.fs;.fsi;.fsproj;.fth;.fun;.fx;.fxh;.fzn;.gd;.gemfile;.gemspec;.geo;.gitattributes;.gitignore;.gleam;.gmi;.gmni;.go;.gotmpl;.gradle;.graphql;.graphqls;.groovy;.h;.h++;.ha;.handlebars;.hbs;.hc;.hcl;.hh;.hlb;.hlsl;.hlsli;.hpp;.hrl;.hs;.htm;.html;.hx;.hxsl;.hxx;.hy;.i;.idc;.idr;.ijs;.image;.in;.inc;.inf;.ini;.ino;.intr;.io;.ipf;.janet;.java;.jbuilder;.jdn;.jl;.js;.jsm;.json;.json5;.jsonata;.jsonc;.jsonl;.jsonnet;.jsx;.jungle;.jy;.kak;.kdl;.kid;.ksh;.kt;.kts;.kube;.lean;.libsonnet;.link;.lisp;.ll;.load;.lock;.log;.lox;.lpk;.lpr;.ltl;.lua;.luau;.ly;.m;.ma;.mak;.man;.mao;.markdown;.markless;.mbt;.mc;.mcad;.mcfunction;.md;.mess;.metal;.mhtml;.mi;.mjs;.mk;.mkd;.ml;.mli;.mlir;.mll;.mly;.mo;.mod;.mojo;.moon;.mount;.mt;.mts;.mx;.myt;.mzn;.nasm;.nb;.nbp;.netdev;.network;.nim;.nimrod;.nix;.nqp;.ns2;.ns7;.nsa;.nsc;.nsg;.nsh;.nsi;.nsl;.nsm;.nsn;.nsp;.nss;.nu;.odin;.org;.p;.p6;.p6l;.p6m;.pas;.patch;.path;.pbtxt;.pc;.pfa;.php;.phtml;.pig;.pl;.pl6;.plc;.plot;.plt;.pm;.pm6;.pml;.po;.pod;.pony;.pot;.pov;.pp;.pq;.pr;.prm;.pro;.proc;.prolog;.prom;.promela;.promql;.properties;.proto;.prql;.ps;.ps1;.psd1;.psl;.psm1;.pxd;.pxi;.py;.pyi;.pyw;.pyx;.qbs;.qml;.qrc;.r;.rake;.raku;.rakudoc;.rakumod;.rakutest;.rb;.rbw;.rbx;.re;.react;.reg;.rego;.rei;.rest;.rex;.rexx;.rform;.rh;.rhtml;.ring;.rkt;.rktd;.rktl;.rpgle;.rq;.rs;.rss;.rst;.run;.rvt;.rx;.s;.sage;.sas;.sass;.sc;.scad;.scala;.scd;.scdoc;.sce;.sci;.scm;.scope;.scss;.sed;.service;.sh;.sieve;.sig;.siv;.slice;.sls;.smali;.sml;.snbt;.snobol;.socket;.sol;.sp;.spade;.sparql;.spec;.spt;.sql;.sqlrpgle;.ss;.st;.star;.stas;.styl;.sv;.svelte;.svg;.svh;.swap;.swift;.t;.t42;.tac;.tal;.tape;.target;.tasm;.tcl;.tcsh;.td;.templ;.tex;.text;.textpb;.textproto;.tf;.thrift;.timer;.tmpl;.toc;.toml;.tpl;.tpp;.trig;.ts;.tst;.tsx;.ttl;.tu;.turing;.tv;.twig;.txt;.txtpb;.typ;.uc;.ucad;.v;.vala;.vapi;.vb;.vcxproj;.vert;.vhd;.vhdl;.vim;.volume;.vsh;.vtt;.vue;.vv;.w;.wast;.wat;.wdte;.wgsl;.whiley;.wl;.wlua;.wsdl;.xhtml;.xml;.xsd;.xsl;.xslt;.yaml;.yang;.yml;.z;.z80;.zed;.zig;.zon;.zone;.zsh;.zshrc"

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
