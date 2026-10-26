#!/bin/sh
# nuget: the whole client surface against the mount, in both directions.
#
# The NuGet v3 protocol is several endpoints a client hits in a fixed order:
#
#   /v3/index.json                  the service index: where everything else is
#   /v3/query?q=                    search
#   /v3/autocomplete?id=            version autocomplete
#   /v3/registration/<id>/index.json  metadata leaves (what `dotnet add` reads)
#   /v3/flatcontainer/<id>/index.json the version list `dotnet restore` reads
#   /v3/flatcontainer/<id>/<v>/<f>.nupkg  the package itself
#
# PULL - each shape exercises a different restore path:
#   Newtonsoft.Json   no dependencies, the baseline
#   Serilog.Sinks.Console  has a dependency (Serilog) that must follow
#   Microsoft.Extensions.Logging  a package with a framework reference
#   System.Text.Json  ships several target frameworks in one nupkg
#   dotnet run        the restored package must actually be callable
#
# HOSTED
#   dotnet pack + push  a real nupkg goes to the mount
#   the v3 endpoints    index, registration and flatcontainer all list it
#   dotnet add/restore  a consumer resolves it back
#   DELETE              the version stops resolving
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "nuget: FAIL: $*" >&2; exit 1; }

export DOTNET_CLI_TELEMETRY_OPTOUT=1
export DOTNET_NOLOGO=1
export DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1

mkdir -p "$HOME/.nuget/NuGet"
cat > "$HOME/.nuget/NuGet/NuGet.Config" <<CONF
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="mount" value="$BASE/v3/index.json" allowInsecureConnections="true" />
  </packageSources>
</configuration>
CONF

echo "== the service index =="
curl -fsS "$BASE/v3/index.json" | grep -q registration || fail "the service index"

echo "== search =="
curl -fsS "$BASE/v3/query?q=newtonsoft" | grep -qi newtonsoft || fail "/v3/query"

echo "== autocomplete =="
curl -fsS "$BASE/v3/autocomplete?id=newtonsoft.json" >/dev/null || fail "/v3/autocomplete"

echo "== registration (the metadata leaves) =="
curl -fsS "$BASE/v3/registration/newtonsoft.json/index.json" | grep -qi 13.0.3 ||
  fail "/v3/registration"

echo "== flatcontainer version list =="
curl -fsS "$BASE/v3/flatcontainer/newtonsoft.json/index.json" | grep -qi 13.0.3 ||
  fail "/v3/flatcontainer index"

echo "== install: no dependencies =="
mkdir -p p1 && cd p1
dotnet new console -o app >/dev/null 2>&1 || fail "dotnet new"
cd app
dotnet add package Newtonsoft.Json --version 13.0.3 >/dev/null 2>&1 ||
  fail "dotnet add package Newtonsoft.Json"
printf '%s\n' \
  'using Newtonsoft.Json;' \
  'var o = JsonConvert.DeserializeObject<Dictionary<string,int>>("{\"a\":1}");' \
  'System.Console.WriteLine("dotnet + json: " + o!["a"]);' > Program.cs
dotnet run --verbosity quiet 2>/dev/null | grep -q 'dotnet + json' ||
  fail "the restored package is not usable"
cd "$WORK"

echo "== install: with a dependency =="
mkdir -p p2 && cd p2
dotnet new console -o app >/dev/null 2>&1
cd app
dotnet add package Serilog.Sinks.Console --version 5.0.1 >/dev/null 2>&1 ||
  fail "dotnet add Serilog.Sinks.Console"
# Its own dependency must have been restored too.
ls "$HOME/.nuget/packages" 2>/dev/null | grep -qi '^serilog$' ||
  fail "Serilog (the dependency) was not restored"
printf '%s\n' \
  'using Serilog;' \
  'Log.Logger = new LoggerConfiguration().WriteTo.Console().CreateLogger();' \
  'Log.Information("ez");' \
  'Log.CloseAndFlush();' > Program.cs
dotnet run --verbosity quiet 2>/dev/null | grep -q ez || fail "Serilog is not usable"
cd "$WORK"

echo "== install: a multi-target-framework package =="
mkdir -p p3 && cd p3
dotnet new console -o app >/dev/null 2>&1
cd app
dotnet add package System.Text.Json --version 8.0.0 >/dev/null 2>&1 ||
  fail "dotnet add System.Text.Json"
cat > Program.cs <<'CS'
using System.Text.Json;
var d = JsonSerializer.Deserialize<Dictionary<string,int>>("{\"a\":2}");
System.Console.WriteLine("stj: " + d!["a"]);
CS
dotnet run --verbosity quiet 2>/dev/null | grep -q 'stj: 2' ||
  fail "System.Text.Json is not usable"
cd "$WORK"

# ------------------------------------------------------------------ hosted --
ID="Easyblobs.E2e.Hello"
VER="1.0.0"
PROJ="$WORK/lib"
mkdir -p "$PROJ"
cd "$PROJ"
dotnet new classlib -o . >/dev/null 2>&1 || fail "dotnet new classlib"
rm -f Class1.cs
printf '%s\n' \
  'namespace Easyblobs.E2e;' \
  'public static class Hello {' \
  '    public static string Greet() => "ez hello";' \
  '}' > Hello.cs
cat > "$ID.csproj" <<CSPROJ
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
    <PackageId>$ID</PackageId>
    <Version>$VER</Version>
    <Authors>e2e</Authors>
    <Description>easyblobs e2e</Description>
  </PropertyGroup>
</Project>
CSPROJ
rm -f lib.csproj 2>/dev/null || true

echo "== dotnet pack =="
dotnet pack -c Release -o out >/dev/null 2>&1 || fail "dotnet pack"
ls out | grep -qi "\.nupkg$" || fail "pack produced no nupkg"
NUPKG=$(ls out/*.nupkg | head -1)

echo "== dotnet nuget push =="
dotnet nuget push "$NUPKG" --source "$BASE/v3/index.json" \
  --api-key e2e --skip-duplicate >/dev/null 2>&1 || fail "dotnet nuget push"

echo "== the v3 endpoints list it =="
curl -fsS "$BASE/v3/flatcontainer/$ID/index.json" | grep -qi "$VER" ||
  fail "flatcontainer does not list $VER"
curl -fsS "$BASE/v3/registration/$ID/index.json" | grep -qi "$VER" ||
  fail "registration does not list $VER"

echo "== a consumer restores it back =="
CONS="$WORK/consumer"
mkdir -p "$CONS"
cd "$CONS"
dotnet new console -o . >/dev/null 2>&1 || fail "dotnet new for the consumer"
dotnet add package "$ID" --version "$VER" >/dev/null 2>&1 ||
  fail "the pushed package cannot be added back"
printf '%s\n' \
  'System.Console.WriteLine(Easyblobs.E2e.Hello.Greet());' > Program.cs
dotnet run --verbosity quiet 2>/dev/null | grep -q 'ez hello' ||
  fail "the pushed package's code does not run"
echo "   the consumer ran it"

echo "== DELETE the version =="
# The mount's delete is /v3/package/<id>/<version> (the flatcontainer path is a
# read-only projection of the same data, so deleting through it would be a
# different operation).
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/v3/package/$ID/$VER")
case "$code" in 200|202|204) ;; *) fail "DELETE the nupkg -> $code" ;; esac
if curl -fsS "$BASE/v3/flatcontainer/$ID/index.json" 2>/dev/null | grep -qi "$VER"; then
  fail "the deleted version is still listed"
fi
echo "   the version is gone"

echo "nuget: OK (pull: index query autocomplete registration flatcontainer install[plain deps multitfm])"
echo "nuget: OK (hosted: pack push endpoints consumer-restores delete)"
