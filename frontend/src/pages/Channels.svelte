<script>
  import { api } from '../lib/api.js'
  import { HUB_EVENT, parseHubEvent } from '../lib/events.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'
  import { scrollRegion } from '../lib/scrollRegion.js'

  let data = $state({ channels: [], health: {} })
  let loading = $state(true)
  let error = $state('')
  let now = $state(Date.now())
  let loadVersion = 0

  $effect(() => {
    load()
    const timer = setInterval(() => { now = Date.now(); load() }, 5000)
    const source = new EventSource('/api/events')
    source.addEventListener(HUB_EVENT, event => {
      const update = parseHubEvent(event.data)
      if (update?.type === 'channels' || update?.type === 'resync') load()
    })
    return () => { loadVersion++; clearInterval(timer); source.close() }
  })

  async function load() {
    const version = ++loadVersion
    try {
      const result = await api.get('/api/channels')
      if (version !== loadVersion) return
      data = result
      error = ''
    } catch (e) { if (version === loadVersion) error = e.message }
    finally { if (version === loadVersion) loading = false }
  }

  function fmtTime(value) {
    if (!value || String(value).startsWith('0001')) return 'not yet'
    const elapsed = now - new Date(value).getTime()
    if (elapsed < 60000) return 'just now'
    if (elapsed < 3600000) return Math.floor(elapsed / 60000) + 'm ago'
    if (elapsed < 86400000) return Math.floor(elapsed / 3600000) + 'h ago'
    return new Date(value).toLocaleString()
  }

  function healthState(id) {
    const health = data.health?.[id]
    if (!health?.last_check || String(health.last_check).startsWith('0001')) return 'Not checked yet'
    if (health.last_error) return 'Needs attention'
    if (now - new Date(health.last_check).getTime() > Math.max(120000, (health.expected_interval_seconds || 60) * 2500)) return 'Check overdue'
    return 'Healthy'
  }
</script>

<main class="page" use:scrollRegion aria-labelledby="channels-title"><div class="page-inner">
  <header class="page-heading">
    <div><p class="eyebrow">Manage</p><h1 class="page-title" id="channels-title">Channels</h1><p class="page-description">How Eve hears from you and delivers a reply.</p></div>
    <span class="refresh-label"><Icon name="refresh" size={14} />Updates every 5 seconds</span>
  </header>
  {#if loading}
    <EmptyState loading title="Checking your channels" />
  {:else}
    {#if error}<div class="notice error" role="alert"><Icon name="warning" /><span>{error}</span><button class="button small" onclick={load}>Try again</button></div>{/if}
    <section aria-labelledby="connected-channels">
      <div class="section-header"><h2 class="section-title" id="connected-channels">Your channels <span class="badge">{data.channels?.length || 0}</span></h2></div>
      {#if !data.channels?.length}
        <EmptyState icon="channels" title="No channels connected" description="Connect a channel in your server configuration to receive and send messages here." />
      {:else}
        <div class="channel-list">
          {#each data.channels as channel}
            {@const state = healthState(channel.id)}
            <article class="channel-row">
              <div class="channel-icon"><Icon name={channel.type === 'web' ? 'chat' : channel.type} size={21} /></div>
              <div class="channel-detail"><h3>{channel.name}</h3><p>{channel.default_recipient || (channel.type === 'web' ? 'Your conversation in this browser' : 'Destination resolved when sending')}</p><div class="capabilities"><span>{channel.type}</span>{#if channel.input}<span>Receives messages</span>{/if}{#if channel.output}<span>Sends replies</span>{/if}{#if channel.streams}<span>Live responses</span>{/if}{#if channel.rich_text}<span>Rich text</span>{/if}{#if channel.preference}<span>Preference: {channel.preference}</span>{/if}</div></div>
              <div class="channel-state">
                {#if channel.type === 'web'}
                  <span class="badge" data-tone={channel.presence?.connected ? 'success' : undefined}>{channel.presence?.connected ? 'You’re here' : 'Not active'}</span>
                  <span class="checked">Last activity {fmtTime(channel.presence?.last_activity)}</span>
                {:else}
                  <span class="badge" data-tone={state === 'Healthy' ? 'success' : state === 'Needs attention' ? 'error' : 'warning'}>{state}</span>
                  <span class="checked">Checked {fmtTime(data.health?.[channel.id]?.last_check)}</span>
                  {#if channel.presence}<span class="checked">{channel.presence.connected ? 'Present' : 'Not present'} · Last activity {fmtTime(channel.presence.last_activity)}</span>{/if}
                {/if}
                {#if !channel.reachable && channel.type !== 'web'}<span class="unreachable">Not reachable</span>{/if}
              </div>
              {#if data.health?.[channel.id]?.last_error}<div class="channel-error" role="status">{data.health[channel.id].last_error}</div>{/if}
            </article>
          {/each}
        </div>
      {/if}
    </section>
    {#if Object.keys(data.queues || {}).length}
      <section class="queue-section" aria-labelledby="delivery-queues"><div class="section-header"><div><h2 class="section-title" id="delivery-queues">Delivery queues</h2><p class="section-description">Accepted messages stay here until they can be handled.</p></div></div>
        <div class="queue-grid">
          {#each Object.entries(data.queues) as [name, queue]}
            <div class="queue"><h3>{name === 'outbound' ? 'Outgoing messages' : 'Matrix inbox'}</h3><dl><div><dt>Waiting</dt><dd>{queue.pending}</dd></div><div><dt>In progress</dt><dd>{queue.leased}</dd></div><div class:has-error={queue.failed > 0}><dt>Failed</dt><dd>{queue.failed}</dd></div></dl>{#if queue.pending || queue.leased}<p>Oldest message queued {fmtTime(queue.oldest_pending_at)}</p>{:else}<p>No messages waiting.</p>{/if}</div>
          {/each}
        </div>
      </section>
    {/if}
    {#each Object.entries(data.health || {}) as [name, poller]}
      {#if !data.channels?.some(channel => channel.id === name)}<div class="poller-status"><Icon name="refresh" size={16} /><div><strong>{name} background check</strong><span>Last checked {fmtTime(poller.last_check)}</span>{#if poller.last_error}<p class="unreachable">{poller.last_error}</p>{/if}</div></div>{/if}
    {/each}
  {/if}
</div></main>

<style>
  .refresh-label { display: inline-flex; align-items: center; gap: var(--space-2); color: var(--text-muted); font-size: .75rem; }
  .channel-list { border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .channel-row { display: grid; grid-template-columns: 42px minmax(0, 1fr) auto; column-gap: var(--space-4); row-gap: var(--space-3); align-items: start; padding: var(--space-6); }
  .channel-row + .channel-row { border-top: var(--rule); }
  .channel-icon { display: grid; place-items: center; width: 42px; height: 42px; border: var(--rule); border-radius: var(--radius); color: var(--text-muted); }
  .channel-detail h3, .queue h3 { font-size: .875rem; font-weight: 600; margin: 0; }
  .channel-detail p { margin: var(--space-1) 0 var(--space-3); font-size: .8125rem; color: var(--text-muted); overflow-wrap: anywhere; }
  .capabilities { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-4); font-size: .6875rem; color: var(--text-muted); }
  .capabilities span { border-bottom: 1px dotted var(--border-strong); }
  .channel-state { display: flex; flex-direction: column; align-items: flex-end; gap: var(--space-2); }
  .checked { color: var(--text-muted); font-size: .6875rem; }
  .unreachable { color: var(--danger); font-size: .75rem; }
  .channel-error { grid-column: 2 / -1; background: var(--danger-bg); color: var(--danger); padding: var(--space-3); border-radius: var(--radius-sm); font-size: .8125rem; overflow-wrap: anywhere; }
  .queue-section { margin-top: var(--space-10); }
  .section-description { margin: var(--space-1) 0 0; font-size: .8125rem; color: var(--text-muted); }
  .queue-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .queue { padding: var(--space-5) var(--space-6); }
  .queue + .queue { border-left: var(--rule); }
  dl { display: flex; gap: var(--space-6); margin: var(--space-5) 0 var(--space-3); }
  dt { color: var(--text-muted); font-size: .6875rem; }
  dd { margin: var(--space-1) 0 0; font-size: 1.25rem; font-weight: 550; font-variant-numeric: tabular-nums; }
  .has-error dd { color: var(--danger); }
  .queue > p { color: var(--text-muted); font-size: .75rem; margin: 0; }
  .poller-status { display: flex; gap: var(--space-3); padding-top: var(--space-6); color: var(--text-muted); }
  .poller-status strong { font-size: .8125rem; font-weight: 500; display: block; text-transform: capitalize; }
  .poller-status span { font-size: .75rem; }
  .notice { margin-bottom: var(--space-6); }
  @media (max-width: 640px) { .channel-row { grid-template-columns: 36px minmax(0, 1fr); padding: var(--space-4); column-gap: var(--space-3); } .channel-icon { width: 36px; height: 36px; } .channel-state { grid-column: 2; align-items: flex-start; } .queue-grid { grid-template-columns: 1fr; } .queue + .queue { border-left: 0; border-top: var(--rule); } }
</style>
