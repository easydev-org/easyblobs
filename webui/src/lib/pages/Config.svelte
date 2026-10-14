<script lang="ts">
  import { onMount } from 'svelte'
  import BadgeCmp from '$lib/components/ui/badge.svelte'
  import type { Api, Target } from '$lib/api'

  let { api }: { api: Api } = $props()
  let targets = $state<Target[]>([])
  let upstreams = $state<Record<string, string>>({})
  let proxy = $state<Record<string, string>>({})
  let tab = $state<'targets' | 'upstreams' | 'proxy'>('targets')
  let err = $state('')

  onMount(async () => {
    try {
      targets = (await api.targets()).targets ?? []
      upstreams = (await api.upstreams()).upstreams ?? {}
      proxy = (await api.proxy()).proxy ?? {}
    } catch (e) {
      err = String(e)
    }
  })
</script>

{#if err}<p class="mb-3 text-meta text-destructive">{err}</p>{/if}
<nav class="mb-4 flex gap-1">
  {#each ['targets', 'upstreams', 'proxy'] as t (t)}
    <button onclick={() => (tab = t as typeof tab)}
      class="rounded-md border px-3 py-1 text-meta {tab === t ? 'border-primary text-foreground' : 'border-border text-muted-foreground hover:text-foreground'}">{t}</button>
  {/each}
</nav>

{#if tab === 'targets'}
  <div class="overflow-hidden rounded-lg border border-border bg-card">
    <table class="w-full border-collapse">
      <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
        <th class="px-4 py-3 font-medium">id</th><th class="px-4 py-3 font-medium">protocol</th><th class="px-4 py-3 font-medium">kind</th>
        <th class="px-4 py-3 font-medium">base</th><th class="px-4 py-3 font-medium">hosts</th><th class="px-4 py-3 font-medium">builtin</th>
      </tr></thead>
      <tbody>
        {#each targets as t (t.id)}
          <tr class="border-b border-border/60 align-top last:border-0">
            <td class="px-4 py-2.5 font-mono text-meta">{t.id}</td>
            <td class="px-4 py-2.5 text-meta">{t.protocol}</td>
            <td class="px-4 py-2.5 text-meta text-muted-foreground">{t.kind}</td>
            <td class="px-4 py-2.5 font-mono text-micro text-muted-foreground">{t.base}</td>
            <td class="px-4 py-2.5 text-micro text-muted-foreground">{t.hosts.join(', ')}</td>
            <td class="px-4 py-2.5">{#if t.builtin}<BadgeCmp variant="muted">builtin</BadgeCmp>{:else}<BadgeCmp variant="accent">custom</BadgeCmp>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else if tab === 'upstreams'}
  <div class="overflow-hidden rounded-lg border border-border bg-card">
    <table class="w-full border-collapse">
      <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
        <th class="px-4 py-3 font-medium">key</th><th class="px-4 py-3 font-medium">url</th>
      </tr></thead>
      <tbody>
        {#each Object.entries(upstreams) as [k, v] (k)}
          <tr class="border-b border-border/60 last:border-0 hover:bg-secondary">
            <td class="px-4 py-2.5 font-mono text-meta">{k}</td>
            <td class="px-4 py-2.5 font-mono text-micro text-muted-foreground">{v}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  {#if Object.keys(proxy).length === 0}
    <p class="text-meta text-muted-foreground">No per-key proxy overrides.</p>
  {:else}
    <div class="overflow-hidden rounded-lg border border-border bg-card">
      <table class="w-full border-collapse">
        <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
          <th class="px-4 py-3 font-medium">key</th><th class="px-4 py-3 font-medium">proxy</th>
        </tr></thead>
        <tbody>
          {#each Object.entries(proxy) as [k, v] (k)}
            <tr class="border-b border-border/60 last:border-0"><td class="px-4 py-2.5 font-mono text-meta">{k}</td><td class="px-4 py-2.5 font-mono text-micro text-muted-foreground">{v}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
{/if}
