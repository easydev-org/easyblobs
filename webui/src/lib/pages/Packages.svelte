<script lang="ts">
  import { onMount } from 'svelte'
  import { Boxes, Search, Copy, Check, Trash2 } from '@lucide/svelte'
  import Button from '$lib/components/ui/button.svelte'
  import Badge from '$lib/components/ui/badge.svelte'
  import type { Api, Pkg } from '$lib/api'
  import { fmtBytes, installCmd, registryHint } from '$lib/format'

  let { api }: { api: Api } = $props()
  let packages = $state<Pkg[]>([])
  let q = $state('')
  let fmtFilter = $state('')
  let origin = $state<'all' | 'hosted' | 'cached'>('all')
  let copied = $state('')
  let err = $state('')

  async function load() {
    try {
      packages = (await api.packages()).packages ?? []
    } catch (e) {
      err = String(e)
    }
  }
  onMount(load)

  const formats = $derived([...new Set(packages.map((p) => p.format))].sort())
  const shown = $derived(
    packages.filter(
      (p) =>
        (!fmtFilter || p.format === fmtFilter) &&
        (origin === 'all' || (origin === 'hosted') === p.hosted) &&
        (q === '' ||
          (p.repository + ' ' + p.version + ' ' + (p.namespace ?? '') + ' ' + (p.repo ?? ''))
            .toLowerCase()
            .includes(q.toLowerCase())),
    ),
  )
  async function copy(text: string, key: string) {
    try {
      await navigator.clipboard.writeText(text)
      copied = key
      setTimeout(() => (copied = ''), 1200)
    } catch {}
  }
  async function del(p: Pkg) {
    if (!confirm(`Delete all packages of ${p.format}/${p.repository}?`)) return
    try {
      await api.deleteRepo(p.repository)
      await load()
    } catch (e) {
      err = String(e)
    }
  }
</script>

{#if err}<p class="mb-3 text-meta text-destructive">{err}</p>{/if}
<div class="mb-4 flex flex-wrap items-center gap-2">
  <div class="flex h-8 items-center gap-2 rounded-md border border-input bg-secondary px-3">
    <Search class="size-3.5 text-muted-foreground" />
    <input bind:value={q} placeholder="Filter packages" class="w-56 bg-transparent text-meta outline-none placeholder:text-muted-foreground" />
  </div>
  {#each ['all', 'hosted', 'cached'] as o (o)}
    <button onclick={() => (origin = o as typeof origin)}
      class="rounded-md border px-2 py-1 text-micro {origin === o ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}">{o}</button>
  {/each}
  <span class="mx-1 text-muted-foreground">·</span>
  <button onclick={() => (fmtFilter = '')}
    class="rounded-md border px-2 py-1 text-micro {fmtFilter === '' ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}">all</button>
  {#each formats as f (f)}
    <button onclick={() => (fmtFilter = f)}
      class="rounded-md border px-2 py-1 text-micro {fmtFilter === f ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}">{f}</button>
  {/each}
  <span class="ml-auto text-micro text-muted-foreground">{shown.length} shown · {packages.filter((p) => p.hosted).length} hosted</span>
</div>

{#if packages.length === 0}
  <div class="flex flex-col items-center gap-3 rounded-lg border border-dashed border-border py-20 text-muted-foreground">
    <Boxes class="size-10 opacity-50" /><p class="text-body">No packages</p>
    <p class="text-meta opacity-70">Everything artifact has cached (pull-through) or hosted (published) will be listed here, across all protocols.</p>
  </div>
{:else}
  <div class="overflow-hidden rounded-lg border border-border bg-card">
    <table class="w-full border-collapse">
      <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
        <th class="px-4 py-3 font-medium">package</th><th class="px-4 py-3 font-medium">version</th>
        <th class="px-4 py-3 font-medium">origin</th><th class="px-4 py-3 text-right font-medium">size</th>
        <th class="px-4 py-3 font-medium">install</th><th class="px-4 py-3"></th>
      </tr></thead>
      <tbody>
        {#each shown as p (p.format + '/' + p.repository + '@' + p.version)}
          <tr class="border-b border-border/60 align-top last:border-0">
            <td class="px-4 py-3"><div class="flex items-center gap-2"><Badge variant="muted">{p.format}</Badge><span class="font-mono text-meta">{p.repository}</span></div></td>
            <td class="px-4 py-3 text-meta text-muted-foreground">{p.version}</td>
            <td class="px-4 py-3">{#if p.hosted}<Badge variant="success">hosted</Badge>{:else}<Badge variant="accent">cached</Badge>{/if}</td>
            <td class="px-4 py-3 text-right text-meta tabular-nums text-muted-foreground">{fmtBytes(p.size)}</td>
            <td class="px-4 py-3">
              <div class="flex items-center gap-2">
                <code class="max-w-[380px] truncate rounded-sm bg-secondary px-2 py-1 font-mono text-micro" title="{registryHint(p)}
{installCmd(p)}">{installCmd(p)}</code>
                <button onclick={() => copy(installCmd(p), p.format + p.repository + p.version)} class="text-muted-foreground hover:text-foreground" title="Copy install command">
                  {#if copied === p.format + p.repository + p.version}<Check class="size-3.5 text-success" />{:else}<Copy class="size-3.5" />{/if}
                </button>
              </div>
            </td>
            <td class="px-4 py-3">
              <Button variant="ghost" size="sm" onclick={() => del(p)} title="Delete this repository's packages"><Trash2 class="size-3.5" /></Button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
