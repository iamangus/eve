<script>
  import Chat from './pages/Chat.svelte'
  import EmailTriggers from './pages/EmailTriggers.svelte'
  import Context from './pages/Context.svelte'
  import Channels from './pages/Channels.svelte'

  let tab = $state('chat')
</script>

<div class="app">
  <nav class="topnav">
    <div class="topnav-brand">eve</div>
    <button class:active={tab === 'chat'} onclick={() => (tab = 'chat')}>Chat</button>
    <button class:active={tab === 'email'} onclick={() => (tab = 'email')}>Email Triggers</button>
    <button class:active={tab === 'context'} onclick={() => (tab = 'context')}>Context</button>
    <button class:active={tab === 'channels'} onclick={() => (tab = 'channels')}>Channels</button>
  </nav>
  <div class="app-shell">
    {#if tab === 'chat'}
      <Chat />
    {:else if tab === 'email'}
      <EmailTriggers />
    {:else if tab === 'context'}
      <Context />
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
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px 16px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-sidebar);
    flex-shrink: 0;
  }
  .topnav-brand {
    font-weight: 700;
    font-size: 0.9rem;
    letter-spacing: 0.02em;
    margin-right: 12px;
    color: var(--text-base);
  }
  .topnav button {
    background: none;
    border: none;
    color: var(--text-muted);
    font-family: inherit;
    font-size: 0.85rem;
    padding: 6px 12px;
    border-radius: 7px;
    cursor: pointer;
    transition: background 0.12s, color 0.12s;
  }
  .topnav button:hover { background: var(--bg-card); color: var(--text-base); }
  .topnav button.active {
    background: var(--purple-dim);
    color: var(--text-base);
  }
  .app-shell {
    flex: 1;
    min-height: 0;
    height: auto;
  }
</style>
