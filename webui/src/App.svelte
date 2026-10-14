<script lang="ts">
  // easyblobs operator console. Pages: Overview / Packages / Images /
  // Repositories / Downloads / Config. Same-origin; the operator pastes a
  // write-level token (localStorage) that only the data endpoints validate.
  import { Package, LayoutDashboard, Boxes, Layers, GitBranch, HardDriveDownload, Settings } from '@lucide/svelte'
  import Button from '$lib/components/ui/button.svelte'
  import Input from '$lib/components/ui/input.svelte'
  import { Api } from '$lib/api'
  import Overview from '$lib/pages/Overview.svelte'
  import Packages from '$lib/pages/Packages.svelte'
  import Images from '$lib/pages/Images.svelte'
  import Repositories from '$lib/pages/Repositories.svelte'
  import Downloads from '$lib/pages/Downloads.svelte'
  import Config from '$lib/pages/Config.svelte'

  const KEY = 'easyblobsToken'
  let token = $state(localStorage.getItem(KEY) ?? '')
  let page = $state<'overview' | 'packages' | 'images' | 'repositories' | 'downloads' | 'config'>('overview')
  const api = new Api(() => token)

  function save() {
    localStorage.setItem(KEY, token.trim())
    // Force a remount of the current page so it reloads with the new token.
    const p = page
    page = 'overview'
    queueMicrotask(() => (page = p))
  }
  const nav = [
    ['overview', LayoutDashboard],
    ['packages', Boxes],
    ['images', Layers],
    ['repositories', GitBranch],
    ['downloads', HardDriveDownload],
    ['config', Settings],
  ] as const
</script>

<div class="flex h-dvh flex-col bg-background text-foreground">
  <header class="flex h-12 shrink-0 items-center gap-3 border-b border-border bg-background/80 px-5 backdrop-blur">
    <div class="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground"><Package class="size-4" /></div>
    <div class="leading-tight"><h1 class="text-body font-semibold">easyblobs</h1><p class="text-micro text-muted-foreground">registry console</p></div>
    <div class="ml-auto flex items-center gap-2">
      <Input type="password" bind:value={token} placeholder="write token (Bearer)" class="w-72" />
      <Button onclick={save}>Connect</Button>
    </div>
  </header>

  <nav class="flex shrink-0 gap-1 border-b border-border px-3">
    {#each nav as [id, Icon] (id)}
      <button onclick={() => (page = id)}
        class="flex items-center gap-1.5 border-b-2 px-3 py-2 text-meta transition {page === id ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}">
        <Icon class="size-3.5" />{id}
      </button>
    {/each}
  </nav>

  <main class="min-h-0 flex-1 overflow-auto px-5 py-5">
    {#key page + token}
      {#if page === 'overview'}<Overview {api} />
      {:else if page === 'packages'}<Packages {api} />
      {:else if page === 'images'}<Images {api} />
      {:else if page === 'repositories'}<Repositories {api} />
      {:else if page === 'downloads'}<Downloads {api} />
      {:else}<Config {api} />{/if}
    {/key}
  </main>
</div>
