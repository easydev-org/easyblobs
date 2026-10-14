<script lang="ts">
  import { onMount } from 'svelte'
  import { Boxes, HardDrive, CloudDownload, Package, ArrowDownToLine } from '@lucide/svelte'
  import type { Api, FormatStat } from '$lib/api'
  import { fmtBytes } from '$lib/format'

  let { api }: { api: Api } = $props()
  let stats = $state<FormatStat[]>([])
  let active = $state(0)
  let err = $state('')

  async function load() {
    try {
      stats = (await api.stats()).formats ?? []
      active = (await api.downloads()).count ?? 0
    } catch (e) {
      err = String(e)
    }
  }
  onMount(load)

  const totalRows = $derived(stats.reduce((a, s) => a + s.rows, 0))
  const totalBytes = $derived(stats.reduce((a, s) => a + s.bytes, 0))
  const maxBytes = $derived(Math.max(1, ...stats.map((s) => s.bytes)))
</script>

{#if err}<p class="mb-3 text-meta text-destructive">{err}</p>{/if}
<div class="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
  {#each [['packages', totalRows.toLocaleString(), Package], ['storage', fmtBytes(totalBytes), HardDrive], ['formats', String(stats.length), Boxes], ['downloading', String(active), CloudDownload]] as [label, value, Icon] (label)}
    <div class="rounded-lg border border-border bg-card p-4">
      <div class="flex items-center gap-1.5 text-micro uppercase tracking-wider text-muted-foreground"><Icon class="size-3.5" /> {label}</div>
      <p class="mt-1.5 text-xl font-semibold tabular-nums">{value}</p>
    </div>
  {/each}
</div>

<h3 class="mb-2 text-micro uppercase tracking-wider text-muted-foreground">by format</h3>
<div class="overflow-hidden rounded-lg border border-border bg-card">
  <table class="w-full border-collapse">
    <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
      <th class="px-4 py-3 font-medium">format</th>
      <th class="px-4 py-3 text-right font-medium">packages</th>
      <th class="px-4 py-3 text-right font-medium">blobs</th>
      <th class="px-4 py-3 text-right font-medium">size</th>
      <th class="px-4 py-3 font-medium">share</th>
    </tr></thead>
    <tbody>
      {#each stats.sort((a, b) => b.bytes - a.bytes) as s (s.format)}
        <tr class="border-b border-border/60 last:border-0 hover:bg-secondary">
          <td class="px-4 py-2.5 text-meta font-medium">{s.format}</td>
          <td class="px-4 py-2.5 text-right text-meta tabular-nums">{s.rows.toLocaleString()}</td>
          <td class="px-4 py-2.5 text-right text-meta tabular-nums text-muted-foreground">{s.blobs.toLocaleString()}</td>
          <td class="px-4 py-2.5 text-right text-meta tabular-nums">{fmtBytes(s.bytes)}</td>
          <td class="px-4 py-2.5">
            <div class="h-1.5 w-40 overflow-hidden rounded-full bg-secondary"><div class="h-full rounded-full bg-primary" style="width:{(100 * s.bytes) / maxBytes}%"></div></div>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
