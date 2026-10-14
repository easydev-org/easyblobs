<script lang="ts">
  import { onMount } from 'svelte'
  import { CloudDownload, ArrowDownToLine, Gauge, Timer } from '@lucide/svelte'
  import type { Api, Dl } from '$lib/api'
  import { fmtBytes, fmtRate } from '$lib/format'

  let { api }: { api: Api } = $props()
  let items = $state<Dl[]>([])

  async function poll() {
    try {
      items = (await api.downloads()).downloads ?? []
    } catch {
      /* ignore */
    }
  }
  onMount(() => {
    poll()
    const t = setInterval(poll, 1500)
    return () => clearInterval(t)
  })
  const pct = (x: Dl) => (x.total > 0 ? Math.min(100, (100 * x.written) / x.total) : 0)
  const sum = $derived(items.reduce((a, x) => a + x.written, 0))
  const rate = $derived(items.reduce((a, x) => a + x.rate_bps, 0))
  const longest = $derived(items.length ? Math.max(...items.map((x) => x.elapsed_sec)).toFixed(1) + 's' : '—')
</script>

<div class="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
  {#each [['active', String(items.length), CloudDownload], ['in flight', fmtBytes(sum), ArrowDownToLine], ['throughput', fmtRate(rate), Gauge], ['longest', longest, Timer]] as [label, value, Icon] (label)}
    <div class="rounded-lg border border-border bg-card p-4">
      <div class="flex items-center gap-1.5 text-micro uppercase tracking-wider text-muted-foreground"><Icon class="size-3.5" /> {label}</div>
      <p class="mt-1.5 text-xl font-semibold tabular-nums">{value}</p>
    </div>
  {/each}
</div>

{#if items.length === 0}
  <div class="flex flex-col items-center gap-3 rounded-lg border border-dashed border-border py-20 text-muted-foreground">
    <CloudDownload class="size-10 opacity-50" />
    <p class="text-body">No active downloads</p>
    <p class="text-meta opacity-70">Upstream fetches (HF LFS, npm, maven, OCI layers…) appear here while they stream into the CAS.</p>
  </div>
{:else}
  <div class="overflow-hidden rounded-lg border border-border bg-card">
    <table class="w-full border-collapse">
      <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
        <th class="px-4 py-3 font-medium">source</th><th class="px-4 py-3 font-medium">progress</th>
        <th class="px-4 py-3 text-right font-medium">rate</th><th class="px-4 py-3 text-right font-medium">elapsed</th>
      </tr></thead>
      <tbody>
        {#each items as x (x.id)}
          <tr class="border-b border-border/60 last:border-0">
            <td class="max-w-[420px] px-4 py-3">
              <div class="flex items-center gap-2">
                <span class="shrink-0 rounded-sm px-1.5 py-0.5 text-micro font-medium {x.tee ? 'bg-accent/15 text-accent' : 'bg-secondary text-muted-foreground'}">{x.tee ? 'pull' : 'fetch'}</span>
                <span class="truncate font-mono text-meta" title={x.url}>{x.url}</span>
              </div>
            </td>
            <td class="px-4 py-3">
              <div class="flex items-center justify-between text-meta tabular-nums">
                <span>{fmtBytes(x.written)}{x.total > 0 ? ` / ${fmtBytes(x.total)}` : ''}</span>
                {#if x.total > 0}<span class="text-muted-foreground">{pct(x).toFixed(0)}%</span>{/if}
              </div>
              <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-secondary">
                {#if x.total > 0}<div class="h-full rounded-full bg-primary transition-[width] duration-500" style="width:{pct(x)}%"></div>
                {:else}<div class="h-full w-1/3 animate-pulse rounded-full bg-primary/60"></div>{/if}
              </div>
            </td>
            <td class="px-4 py-3 text-right tabular-nums">{fmtRate(x.rate_bps)}</td>
            <td class="px-4 py-3 text-right tabular-nums text-muted-foreground">{x.elapsed_sec.toFixed(1)}s</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
