<script lang="ts">
  import { onMount } from 'svelte'
  import { GitBranch } from '@lucide/svelte'
  import type { Api, RepoMap } from '$lib/api'

  let { api }: { api: Api } = $props()
  let repos = $state<RepoMap[]>([])
  let err = $state('')
  onMount(async () => {
    try {
      repos = (await api.repos()).formats ?? []
    } catch (e) {
      err = String(e)
    }
  })
</script>

{#if err}<p class="mb-3 text-meta text-destructive">{err}</p>{/if}
<p class="mb-3 text-meta text-muted-foreground">
  Upstream repository mapping (key → URL). Packages are pulled through from these;
  self-published packages are <em>hosted</em> (see Packages).
</p>
<div class="overflow-hidden rounded-lg border border-border bg-card">
  <table class="w-full border-collapse">
    <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
      <th class="px-4 py-3 font-medium">key</th><th class="px-4 py-3 font-medium">upstream url</th>
    </tr></thead>
    <tbody>
      {#each repos as r (r.key)}
        <tr class="border-b border-border/60 last:border-0 hover:bg-secondary">
          <td class="px-4 py-2.5"><span class="flex items-center gap-2 font-mono text-meta"><GitBranch class="size-3.5 text-muted-foreground" />{r.key}</span></td>
          <td class="px-4 py-2.5 font-mono text-micro text-muted-foreground">{r.url}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
