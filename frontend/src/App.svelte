<script>
  import { onMount, tick } from 'svelte'
  import { api } from './lib/api.js'
  import Icon from './components/Icon.svelte'
  import Chat from './pages/Chat.svelte'
  import EmailTriggers from './pages/EmailTriggers.svelte'
  import Context from './pages/Context.svelte'
  import Channels from './pages/Channels.svelte'
  import Tasks from './pages/Tasks.svelte'
  import Identities from './pages/Identities.svelte'
  import Inference from './pages/Inference.svelte'

  const sections = [
    { label: 'Workspace', pages: [{ id: 'chat', label: 'Chat' }, { id: 'tasks', label: 'Tasks' }, { id: 'inference', label: 'Inference' }] },
    { label: 'Manage', pages: [{ id: 'email', label: 'Email' }, { id: 'context', label: 'Context' }, { id: 'channels', label: 'Channels' }, { id: 'identities', label: 'Identities' }] },
  ]
  const pageNames = Object.fromEntries(sections.flatMap(section => section.pages.map(page => [page.id, page.label])))
  const pageFromURL = () => {
    const path = window.location.pathname.split('/')[1]
    return pageNames[path] ? path : 'chat'
  }
  let tab = $state(pageFromURL())
  let theme = $state('system')
  let prefersDark = $state(false)
  let drawer
  let drawerOpen = $state(false)
  let mainContent

  $effect(() => {
    document.documentElement.dataset.theme = theme === 'system' ? (prefersDark ? 'dark' : 'light') : theme
  })

  async function navigate(id) {
    const href = id === 'chat' ? '/' : '/' + id
    if (tab !== id) {
      tab = id
      window.history.pushState({}, '', href)
    }
    drawer?.close()
    await tick()
    mainContent?.focus({ preventScroll: true })
  }

  function setTheme(value) {
    theme = value
    try { localStorage.setItem('eve-theme', value) } catch {}
  }

  onMount(() => {
    try {
      const stored = localStorage.getItem('eve-theme')
      if (['system', 'light', 'dark'].includes(stored)) theme = stored
    } catch {}
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    prefersDark = media.matches
    const appearance = () => { prefersDark = media.matches }
    const popstate = () => { tab = pageFromURL(); drawer?.close() }
    const resize = () => { if (window.innerWidth > 760) drawer?.close() }
    const beat = () => { if (document.visibilityState === 'visible') api.post('/api/presence').catch(() => {}) }
    beat()
    const timer = setInterval(beat, 30000)
    media.addEventListener('change', appearance)
    window.addEventListener('popstate', popstate)
    window.addEventListener('resize', resize)
    document.addEventListener('visibilitychange', beat)
    return () => {
      clearInterval(timer)
      media.removeEventListener('change', appearance)
      window.removeEventListener('popstate', popstate)
      window.removeEventListener('resize', resize)
      document.removeEventListener('visibilitychange', beat)
    }
  })
</script>

{#snippet brand()}
  <div class="brand"><span class="brand-mark"><Icon name="sparkle" size={22} /></span><span>Eve<span class="brand-subtitle">Your personal assistant</span></span></div>
{/snippet}

{#snippet navigation()}
  <nav aria-label="Main navigation">
    {#each sections as section}
      <div class="nav-group"><div class="nav-label">{section.label}</div>
        {#each section.pages as page}
          <button class="nav-item" class:active={tab === page.id} aria-current={tab === page.id ? 'page' : undefined} onclick={() => navigate(page.id)}><Icon name={page.id} size={19} /><span>{page.label}</span>{#if tab === page.id}<span class="nav-indicator" aria-hidden="true"></span>{/if}</button>
        {/each}
      </div>
    {/each}
  </nav>
  <div class="sidebar-footer">
    <div class="appearance-label">Appearance</div>
    <div class="segmented theme-control" role="group" aria-label="Appearance">
      {#each [{ id: 'light', icon: 'sun', label: 'Light theme' }, { id: 'dark', icon: 'moon', label: 'Dark theme' }, { id: 'system', icon: 'system', label: 'System theme' }] as option}
        <button aria-label={option.label} title={option.label} aria-pressed={theme === option.id} onclick={() => setTheme(option.id)}><Icon name={option.icon} size={16} /></button>
      {/each}
    </div>
    <div class="workspace-label"><span class="workspace-avatar">Y</span><div><strong>Personal workspace</strong><span>Just you and Eve</span></div></div>
  </div>
{/snippet}

<a class="skip-link" href="#main-content">Skip to content</a>
<div class="app">
  <aside class="sidebar" aria-label="Workspace sidebar">{@render brand()}{@render navigation()}</aside>
  <div class="workspace">
    <header class="mobile-header">
      <button class="icon-button" aria-label="Open navigation" aria-expanded={drawerOpen} aria-controls="mobile-navigation" onclick={() => { drawer.showModal(); drawerOpen = true }}><Icon name="menu" /></button>
      <span class="mobile-brand">Eve <span>/ {pageNames[tab]}</span></span>
      <span class="mobile-mark" aria-hidden="true"><Icon name="sparkle" size={18} /></span>
    </header>
    <div class="app-shell" id="main-content" tabindex="-1" bind:this={mainContent}>
      {#if tab === 'chat'}<Chat />
      {:else if tab === 'tasks'}<Tasks />
      {:else if tab === 'email'}<EmailTriggers />
      {:else if tab === 'context'}<Context />
      {:else if tab === 'channels'}<Channels />
      {:else if tab === 'identities'}<Identities />
      {:else if tab === 'inference'}<Inference />{/if}
    </div>
  </div>
</div>

<dialog class="mobile-drawer" id="mobile-navigation" aria-label="Workspace navigation" bind:this={drawer} onclose={() => { drawerOpen = false }} onclick={(event) => { if (event.target === drawer) drawer.close() }}>
  <div class="drawer-inner"><div class="drawer-head">{@render brand()}<button class="icon-button" aria-label="Close navigation" onclick={() => drawer.close()}><Icon name="close" /></button></div>{@render navigation()}</div>
</dialog>

<style>
  .app { display: flex; height: 100dvh; overflow: hidden; }
  .sidebar { width: var(--sidebar-width); flex-shrink: 0; display: flex; flex-direction: column; background: var(--bg-sidebar); border-right: var(--rule); padding: var(--space-6) var(--space-3) var(--space-4); }
  .brand { display: flex; gap: var(--space-3); align-items: center; padding: var(--space-2) var(--space-3) var(--space-8); font-size: 1.35rem; font-weight: 650; letter-spacing: -.04em; }
  .brand-mark { display: grid; place-items: center; color: var(--accent); background: var(--accent-dim); width: 38px; height: 38px; border-radius: var(--radius); }
  .brand-subtitle { display: block; font-size: .6875rem; font-weight: 450; letter-spacing: 0; color: var(--text-muted); margin-top: var(--space-1); }
  .nav-group + .nav-group { margin-top: var(--space-6); }
  .nav-label { margin: 0 var(--space-3) var(--space-2); color: var(--text-muted); font-size: .6875rem; font-weight: 550; }
  .nav-item { display: flex; align-items: center; gap: var(--space-3); width: 100%; min-height: 44px; text-align: left; padding: var(--space-2) var(--space-3); margin-block: var(--space-1); border: 1px solid transparent; background: transparent; color: var(--text-muted); border-radius: var(--radius-sm); font-size: .875rem; font-weight: 500; transition: background var(--duration), color var(--duration); }
  .nav-item:hover { background: var(--bg-hover); color: var(--text-base); }
  .nav-item.active { background: var(--accent-dim); color: var(--accent-text); font-weight: 600; }
  .nav-indicator { width: 5px; height: 5px; border-radius: 50%; background: currentColor; margin-left: auto; }
  .sidebar-footer { margin-top: auto; padding: var(--space-8) var(--space-3) 0; }
  .appearance-label { color: var(--text-muted); font-size: .6875rem; margin-bottom: var(--space-2); }
  .theme-control { display: flex; }
  .theme-control button { flex: 1; }
  .workspace-label { margin-top: var(--space-5); padding-top: var(--space-5); border-top: var(--rule); display: flex; align-items: center; gap: var(--space-3); }
  .workspace-avatar { width: 30px; height: 30px; border: var(--rule); border-radius: var(--radius-sm); background: var(--bg-raised); display: grid; place-items: center; font-size: .6875rem; font-weight: 600; }
  .workspace-label strong { display: block; font-size: .6875rem; font-weight: 550; }
  .workspace-label div > span { display: block; font-size: .625rem; color: var(--text-muted); }
  .workspace { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  .app-shell { display: flex; flex: 1; min-height: 0; overflow: hidden; outline: none; }
  .mobile-header { display: none; }
  .mobile-drawer { border: 0; border-right: var(--rule); color: var(--text-base); background: var(--bg-sidebar); padding: 0; margin: 0; height: 100dvh; max-height: 100dvh; width: min(300px, calc(100vw - 24px)); max-width: none; }
  .mobile-drawer::backdrop { background: var(--overlay); }
  .drawer-inner { display: flex; flex-direction: column; min-height: 100%; padding: var(--space-4) var(--space-3); }
  .drawer-head { display: flex; align-items: flex-start; }
  .drawer-head .brand { flex: 1; }
  .drawer-head .icon-button { flex-shrink: 0; }
  .skip-link { position: fixed; top: var(--space-3); left: var(--space-3); z-index: 100; padding: var(--space-3) var(--space-4); background: var(--accent); color: var(--on-accent); border-radius: var(--radius-sm); transform: translateY(-180%); }
  .skip-link:focus { transform: translateY(0); }
  @media (max-width: 760px) {
    .sidebar { display: none; }
    .mobile-header { display: flex; align-items: center; gap: var(--space-3); min-height: 60px; border-bottom: var(--rule); padding: var(--space-2) var(--space-3); background: var(--bg-sidebar); }
    .mobile-brand { font-size: 1rem; font-weight: 650; letter-spacing: -.02em; }
    .mobile-brand > span { font-size: .8125rem; font-weight: 400; color: var(--text-muted); }
    .mobile-mark { margin-left: auto; padding-right: var(--space-2); color: var(--accent); }
  }
</style>
