<script lang="ts">
  import { onMount } from 'svelte'
  import { Boxes, Copy, Check } from '@lucide/svelte'
  import Badge from '$lib/components/ui/badge.svelte'
  import type { Api, OciImage } from '$lib/api'

  let { api }: { api: Api } = $props()
  let images = $state<OciImage[]>([])
  let q = $state('')
  let copied = $state('')
  let err = $state('')
  onMount(async () => {
    try {
      images = (await api.catalog()).images ?? []
    } catch (e) {
      err = String(e)
    }
  })
  const shown = $derived(images.filter((i) => (i.owner + '/' + i.name).toLowerCase().includes(q.toLowerCase())))
  async function copy(text: string, key: string) {
    try {
      await navigator.clipboard.writeText(text)
      copied = key
      setTimeout(() => (copied = ''), 1200)
    } catch {}
  }
</script>

{#if err}<p class="mb-3 text-meta text-destructive">{err}</p>{/if}
<div class="mb-4 flex items-center gap-2">
  <input bind:value={q} placeholder="Filter images (owner/name)" class="h-8 w-72 rounded-md border border-input bg-secondary px-3 text-meta outline-none placeholder:text-muted-foreground" />
  <span class="text-micro text-muted-foreground">{shown.length} images</span>
</div>

{#if images.length === 0}
  <div class="flex flex-col items-center gap-3 rounded-lg border border-dashed border-border py-20 text-muted-foreground">
    <Boxes class="size-10 opacity-50" /><p class="text-body">No images</p>
    <p class="text-meta opacity-70">OCI images pushed to this registry (by owner) appear here.</p>
  </div>
{:else}
  <div class="overflow-hidden rounded-lg border border-border bg-card">
    <table class="w-full border-collapse">
      <thead><tr class="border-b border-border text-left text-micro uppercase tracking-wider text-muted-foreground">
        <th class="px-4 py-3 font-medium">image</th><th class="px-4 py-3 font-medium">tags</th><th class="px-4 py-3 font-medium">pull</th>
      </tr></thead>
      <tbody>
        {#each shown as img (img.owner + '/' + img.name)}
          <tr class="border-b border-border/60 align-top last:border-0">
            <td class="px-4 py-3 font-mono text-meta"><span class="text-muted-foreground">{img.owner}/</span>{img.name}</td>
            <td class="px-4 py-3">
              <div class="flex flex-wrap gap-1">
                {#each img.tags as t (t)}<Badge variant="muted">{t}</Badge>{/each}
              </div>
            </td>
            <td class="px-4 py-3">
              {#if img.tags.length}
                <div class="flex items-center gap-2">
                  <code class="rounded-sm bg-secondary px-2 py-1 font-mono text-micro">docker pull {location.host}/{img.owner}/{img.name}:{img.tags[0]}</code>
                  <button onclick={() => copy(`docker pull ${location.host}/${img.owner}/${img.name}:${img.tags[0]}`, img.owner + img.name)} class="text-muted-foreground hover:text-foreground">
                    {#if copied === img.owner + img.name}<Check class="size-3.5 text-success" />{:else}<Copy class="size-3.5" />{/if}
                  </button>
                </div>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
