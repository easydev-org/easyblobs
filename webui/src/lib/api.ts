// Typed client for the artifact admin API (/artifacts/system/*). All calls are
// same-origin and carry the operator's write-level token as a Bearer header.
export interface Pkg {
  format: string
  repository: string
  version: string
  size: number
  media_type?: string
  digest?: string
  source?: string
  hosted: boolean
  namespace?: string
  repo?: string
}
export interface Dl {
  id: number
  url: string
  format?: string
  written: number
  total: number
  tee: boolean
  elapsed_sec: number
  rate_bps: number
}
export interface FormatStat {
  format: string
  rows: number
  blobs: number
  bytes: number
  expired: number
}
export interface RepoMap {
  key: string
  url: string
}
export interface Target {
  id: string
  protocol: string
  kind: string
  base: string
  hosts: string[]
  auth: Record<string, unknown>
  builtin: boolean
}
export interface OciImage {
  owner: string
  name: string
  tags: string[]
}

export class Api {
  constructor(private token: () => string) {}
  private h(): HeadersInit {
    const t = this.token().trim()
    return t ? { Authorization: 'Bearer ' + t } : {}
  }
  async get<T>(path: string): Promise<T> {
    const r = await fetch(path, { headers: this.h() })
    if (!r.ok) throw new Error('HTTP ' + r.status)
    return (await r.json()) as T
  }
  async del(path: string): Promise<void> {
    const r = await fetch(path, { method: 'DELETE', headers: this.h() })
    if (!r.ok) throw new Error('HTTP ' + r.status)
  }
  stats = () => this.get<{ formats: FormatStat[] }>('/artifacts/system/stats')
  packages = () => this.get<{ packages: Pkg[] }>('/artifacts/system/packages')
  downloads = () => this.get<{ downloads: Dl[]; count: number }>('/artifacts/system/downloads')
  repos = () => this.get<{ formats: RepoMap[] }>('/artifacts/system/repos')
  targets = () => this.get<{ targets: Target[] }>('/artifacts/system/targets')
  upstreams = () => this.get<{ upstreams: Record<string, string> }>('/artifacts/system/upstreams')
  proxy = () => this.get<{ proxy: Record<string, string> }>('/artifacts/system/proxy')
  catalog = (owner?: string) =>
    this.get<{ images: OciImage[] }>('/v2/_catalog' + (owner ? '?owner=' + encodeURIComponent(owner) : ''))
  deleteRepo = (repo: string) =>
    this.del('/artifacts/system/packages?repo=' + encodeURIComponent(repo))
}
