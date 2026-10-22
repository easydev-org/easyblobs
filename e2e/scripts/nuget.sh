#!/bin/sh
# NuGet: point the package source at the artifact service index.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
mkdir -p $HOME/.nuget/NuGet
cat > $HOME/.nuget/NuGet/NuGet.Config <<X
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="easy-platform" value="$EASYBLOBS_BASE/v3/index.json" allowInsecureConnections="true" />
  </packageSources>
</configuration>
X
cd /tmp
rm -rf w
dotnet new console -o w >/dev/null
cd w
dotnet add package Newtonsoft.Json --version 13.0.3 >/dev/null
dotnet restore
# Compile/run assertion: use the restored package for real.
cat > Program.cs <<'X'
using Newtonsoft.Json;
var o = JsonConvert.DeserializeObject<Dictionary<string,int>>("{\"a\":1}");
System.Console.WriteLine("dotnet + json: " + o["a"]);
X
dotnet run --verbosity quiet
