import type { Pkg } from './api'

export function fmtBytes(b: number): string {
  if (b < 1024) return b + ' B'
  const u = ['KB', 'MB', 'GB', 'TB']
  let i = -1
  do {
    b /= 1024
    i++
  } while (b >= 1024 && i < u.length - 1)
  return b.toFixed(1) + ' ' + u[i]
}
export const fmtRate = (b: number) => (b > 0 ? fmtBytes(b) + '/s' : '—')
export const fmtWhen = (ms?: number) => (ms ? new Date(ms).toLocaleString() : '')

/** A consumer's install command (the client's registry must point at artifact). */
export function installCmd(p: Pkg): string {
  const base = location.origin + '/artifacts'
  switch (p.format) {
    case 'npm': return `npm install ${p.repository}@${p.version}`
    case 'pypi': return `pip install ${p.repository}==${p.version}`
    case 'go': return `GOPROXY=${base}/go GOSUMDB=off go get ${p.repository}@${p.version}`
    case 'cargo': return `cargo add ${p.repository}@${p.version}`
    case 'maven': return `mvn dependency:get -Dartifact=${p.repository.replace('/', ':')}:${p.version}`
    case 'nuget': return `dotnet add package ${p.repository} --version ${p.version}`
    case 'rubygems': return `gem install ${p.repository} -v ${p.version}`
    case 'composer': return `composer require ${p.repository}:${p.version}`
    case 'helm': return `helm pull ${p.repository} --version ${p.version}`
    case 'oci': return `docker pull ${location.host}/${p.repository}:${p.version}`
    case 'debian': return `apt-get install ${p.repository}=${p.version}`
    case 'apk': return `apk add ${p.repository}=${p.version}`
    case 'rpm': return `dnf install ${p.repository}-${p.version}`
    case 'huggingface': return `HF_ENDPOINT=${base}/huggingface huggingface-cli download ${p.repository}`
    case 'generic': return `curl -O ${base}/generic/${p.repository}/${p.version}`
    default: return `${base}/${p.format}/${p.repository} (${p.version})`
  }
}
export function registryHint(p: Pkg): string {
  const base = location.origin + '/artifacts'
  switch (p.format) {
    case 'npm': return `# .npmrc → registry=${base}/npm/`
    case 'pypi': return `# pip --index-url ${base}/pypi/simple/`
    case 'go': return `# GOPROXY=${base}/go`
    case 'cargo': return `# sparse+${base}/cargo/index/`
    case 'maven': return `# ~/.m2 mirror → ${base}/maven/`
    case 'oci': return `# registry host: ${location.host}`
    case 'helm': return `# helm repo add artifact ${base}/helm`
    default: return `# ${base}/${p.format}/`
  }
}
