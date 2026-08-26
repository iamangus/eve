<script>
  import Chat from './pages/Chat.svelte'
  import EmailTriggers from './pages/EmailTriggers.svelte'
  import Context from './pages/Context.svelte'
  import Channels from './pages/Channels.svelte'
  import Tasks from './pages/Tasks.svelte'
  import Identities from './pages/Identities.svelte'

  let tab = $state('chat')
</script>

<div class="app">
  <nav class="topnav">
    <div class="topnav-inner">
      <div class="topnav-brand">EVE<span> / personal system</span></div>
      <div class="topnav-links">
        <button class:active={tab === 'chat'} aria-current={tab === 'chat' ? 'page' : undefined} onclick={() => (tab = 'chat')}>Chat</button>
        <button class:active={tab === 'email'} aria-current={tab === 'email' ? 'page' : undefined} onclick={() => (tab = 'email')}>Email</button>
        <button class:active={tab === 'context'} aria-current={tab === 'context' ? 'page' : undefined} onclick={() => (tab = 'context')}>Context</button>
        <button class:active={tab === 'channels'} aria-current={tab === 'channels' ? 'page' : undefined} onclick={() => (tab = 'channels')}>Channels</button>
        <button class:active={tab === 'tasks'} aria-current={tab === 'tasks' ? 'page' : undefined} onclick={() => (tab = 'tasks')}>Tasks</button>
        <button class:active={tab === 'identities'} aria-current={tab === 'identities' ? 'page' : undefined} onclick={() => (tab = 'identities')}>Identities</button>
      </div>
    </div>
  </nav>
  <div class="app-shell">
    {#if tab === 'chat'}
      <Chat />
    {:else if tab === 'email'}
      <EmailTriggers />
    {:else if tab === 'context'}
      <Context />
    {:else if tab === 'channels'}
      <Channels />
    {:else if tab === 'tasks'}
      <Tasks />
    {:else if tab === 'identities'}
      <Identities />
    {:else}
      <Channels />
    {/if}
  </div>
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }
  .topnav {
    border-bottom: var(--rule);
    background: var(--bg-deep);
    flex-shrink: 0;
  }
  .topnav-inner {
    min-height: 58px;
    display: flex;
    align-items: stretch;
    gap: clamp(24px, 5vw, 72px);
    padding: 0 var(--page-pad);
    max-width: calc(var(--content-max) + 2 * var(--page-pad));
  }
  .topnav-brand {
    display: flex;
    align-items: center;
    flex-shrink: 0;
    font-family: var(--mono);
    font-weight: 700;
    font-size: 0.8rem;
    letter-spacing: 0.12em;
    color: var(--text-base);
  }
  .topnav-brand span { color: var(--text-faint); font-weight: 400; letter-spacing: 0; margin-left: 8px; text-transform: lowercase; }
  .topnav-links { display: flex; align-items: stretch; gap: 2px; overflow-x: auto; scrollbar-width: none; }
  .topnav-links::-webkit-scrollbar { display: none; }
  .topnav button {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-muted);
    font-family: var(--mono);
    font-size: 0.72rem;
    letter-spacing: 0.04em;
    padding: 2px 12px 0;
    cursor: pointer;
    white-space: nowrap;
    transition: color 0.12s, border-color 0.12s;
  }
  .topnav button:hover { color: var(--text-base); }
  .topnav button.active {
    border-bottom-color: var(--accent);
    color: var(--accent);
  }
  .app-shell {
    flex: 1;
    min-height: 0;
    height: auto;
  }
  @media (max-width: 720px) {
    .topnav-inner { min-height: auto; padding: 0; display: block; }
    .topnav-brand { height: 44px; padding: 0 var(--page-pad); border-bottom: var(--rule); }
    .topnav-links { height: 46px; padding-left: calc(var(--page-pad) - 10px); }
    .topnav button { padding-inline: 10px; }
  }
</style>
