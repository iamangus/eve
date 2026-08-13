<script>
  import { api } from '../lib/api.js'

  let data = $state({ channels: [], health: {} })
  let loading = $state(true)
  let error = $state('')
  let now = $state(Date.now())
  let es = $state(null)

  $effect(() => {
    load()
    const iv = setInterval(() => {
      now = Date.now()
      load()
    }, 5000)
    const src = new EventSource('/api/events')
    src.onmessage = (ev) => {
      try {
        const e = JSON.parse(ev.data)
        if (e.type === 'channels') load()
      } catch {}
    }
    es = src
    return () => {
      clearInterval(iv)
      src.close()
    }
  })

  async function load() {
    try {
      data = await api.get('/api/channels')
      error = ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  const channels = () => data.channels || []
  const health = () => data.health || {}

  function fmtTime(t) {
    if (!t || String(t).startsWith('0001')) return 'never'
    const d = Date.now() - new Date(t).getTime()
    if (d < 60000) return 'just now'
    if (d < 3600000) return Math.floor(d / 60000) + 'm ago'
    if (d < 86400000) return Math.floor(d / 3600000) + 'h ago'
    return new Date(t).toLocaleString()
  }

  function presentNow(c) {
    return c.presence?.connected
  }

  function healthState(id) {
    const h = health()[id]
    if (!h) return null
    return h.last_error ? 'error' : 'ok'
  }
</script>

<div class="ch-page">
  <h2 class="ch-title">Channels</h2>
  {#if loading}
    <div class="ch-empty">Loading channels…</div>
  {:else if error}
    <div class="ch-empty ch-error">{error}</div>
  {:else if channels().length === 0}
    <div class="ch-empty">No channels registered.</div>
  {:else}
    <div class="ch-grid">
      {#each channels() as c}
        <div class="ch-card">
          <div class="ch-head">
            <span class="ch-name">{c.name}</span>
            <span class="ch-type">{c.type}</span>
          </div>
          <div class="ch-badges">
            {#if c.input}
              <span class="cap">in</span>
            {/if}
            {#if c.output}
              <span class="cap">out</span>
            {/if}
            {#if c.streams}
              <span class="cap">streams</span>
            {/if}
            {#if c.rich_text}
              <span class="cap">rich text</span>
            {/if}
            {#if !c.reachable}
              <span class="cap cap-warn">unreachable</span>
            {/if}
          </div>
          <div class="ch-row">
            <span>Presence</span>
            <b class="{presentNow(c) ? 'ok-text' : 'muted-text'}">
              {presentNow(c) ? 'present' : 'not present'}
            </b>
          </div>
          <div class="ch-row">
            <span>Last activity</span>
            <b>{fmtTime(c.presence?.last_activity)}</b>
          </div>
          <div class="ch-row">
            <span>Preference</span>
            <b>{c.preference}</b>
          </div>
          {#if c.default_recipient}
            <div class="ch-row">
              <span>Default recipient</span>
              <b class="ch-addr">{c.default_recipient}</b>
            </div>
          {/if}
          {#if c.type !== 'web'}
            {@const hs = healthState(c.id)}
            {#if hs === 'error'}
              <div class="ch-health ch-health-error" title="{health()[c.id].last_error}">
                last check {fmtTime(health()[c.id].last_check)} · error
              </div>
            {:else if hs === 'ok'}
              <div class="ch-health ch-health-ok">
                poller healthy · checked {fmtTime(health()[c.id].last_check)}
              </div>
            {:else}
              <div class="ch-health">
                poller not reporting
              </div>
            {/if}
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .ch-page { padding: 24px; overflow-y: auto; height: 100%; max-width: 960px; margin: 0 auto; }
  .ch-title {
    font-size: 0.75rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-muted); margin: 0 0 16px;
  }
  .ch-empty { color: var(--text-muted); font-size: 0.85rem; padding: 8px 0; }
  .ch-error { color: oklch(70% 0.2 20); }
  .ch-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 10px; }
  .ch-card {
    background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px;
    padding: 14px; font-size: 0.83rem; display: flex; flex-direction: column; gap: 6px;
  }
  .ch-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 2px; }
  .ch-name { font-weight: 700; color: var(--text-base); font-size: 0.9rem; }
  .ch-type {
    font-size: 0.68rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted);
    border: 1px solid var(--border); border-radius: 10px; padding: 1px 8px;
  }
  .ch-badges { display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 4px; }
  .cap {
    font-size: 0.66rem; border-radius: 6px; padding: 1px 7px;
    background: oklch(30% 0.06 292.7); color: oklch(80% 0.18 292.0); border: 1px solid oklch(59.1% 0.249 292.7 / 0.35);
  }
  .cap-warn { background: oklch(30% 0.08 25); color: oklch(80% 0.18 25); border-color: oklch(59.1% 0.249 25 / 0.35); }
  .ch-row { display: flex; justify-content: space-between; gap: 8px; color: var(--text-muted); }
  .ch-row b { color: var(--text-base); font-weight: 600; text-align: right; }
  .ok-text { color: oklch(65% 0.18 145); }
  .muted-text { color: var(--text-muted); font-weight: 500; }
  .ch-addr { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 60%; }
  .ch-health { margin-top: 4px; font-size: 0.72rem; color: var(--text-muted); }
  .ch-health-ok { color: oklch(65% 0.18 145); }
  .ch-health-error { color: oklch(70% 0.2 20); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
