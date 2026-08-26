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
  <header class="ch-header">
    <div>
      <div class="ch-kicker">SYSTEM / DELIVERY</div>
      <h2 class="ch-title">Channels</h2>
    </div>
    <div class="ch-summary">{channels().length} registered channels · refresh interval 5 seconds</div>
  </header>
  {#if loading}
    <div class="ch-empty">Loading channels…</div>
  {:else if error}
    <div class="ch-empty ch-error">{error}</div>
  {:else if channels().length === 0}
    <div class="ch-empty">No channels registered.</div>
  {:else}
    <div class="ch-matrix" role="table" aria-label="Registered channels">
      <div class="ch-matrix-head" role="row">
        <span>Channel</span><span>Capabilities</span><span>Presence</span><span>Preference</span><span>Poller</span>
      </div>
      {#each channels() as c}
        {@const hs = healthState(c.id)}
        <div class="ch-card" role="row">
          <div class="ch-head" data-label="Channel">
            <span class="ch-name">{c.name}</span>
            <span class="ch-type">{c.type}</span>
            {#if c.default_recipient}<span class="ch-addr">{c.default_recipient}</span>{/if}
          </div>
          <div class="ch-badges" data-label="Capabilities">
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
          <div class="ch-presence" data-label="Presence">
            <b>{presentNow(c) ? 'PRESENT' : 'NOT PRESENT'}</b>
            <span>Last activity: {fmtTime(c.presence?.last_activity)}</span>
          </div>
          <div class="ch-pref" data-label="Preference">{c.preference}</div>
          <div class="ch-health" data-label="Poller">
            {#if c.type !== 'web'}
            {#if hs === 'error'}
              <b title="{health()[c.id].last_error}">ERROR</b><span>Checked {fmtTime(health()[c.id].last_check)}</span>
            {:else if hs === 'ok'}
              <b>HEALTHY</b><span>Checked {fmtTime(health()[c.id].last_check)}</span>
            {:else}
              <b>NOT REPORTING</b>
            {/if}
            {:else}<span>Not applicable</span>{/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .ch-page { padding: 38px var(--page-pad) 64px; overflow-y: auto; height: 100%; width: 100%; }
  .ch-header { max-width: var(--content-max); display: flex; justify-content: space-between; align-items: end; padding-bottom: 24px; }
  .ch-kicker, .ch-summary, .ch-matrix-head { font-family: var(--mono); }
  .ch-kicker { color: var(--text-faint); font-size: 0.65rem; letter-spacing: 0.1em; }
  .ch-title { font-size: 1.65rem; font-weight: 500; letter-spacing: -0.02em; color: var(--text-base); margin: 3px 0 0; }
  .ch-summary { color: var(--text-muted); font-size: 0.68rem; }
  .ch-empty { color: var(--text-muted); font-size: 0.88rem; padding: 24px 0; border-top: var(--rule); max-width: var(--content-max); }
  .ch-error { color: var(--text-base); border-top-color: var(--text-base); }
  .ch-matrix { max-width: var(--content-max); border-top: 1px solid var(--border-strong); }
  .ch-matrix-head, .ch-card { display: grid; grid-template-columns: 1.35fr 1.3fr 1.25fr 0.7fr 1.25fr; column-gap: 20px; }
  .ch-matrix-head { padding: 9px 0; color: var(--text-faint); font-size: 0.63rem; letter-spacing: 0.08em; text-transform: uppercase; border-bottom: var(--rule); }
  .ch-card { padding: 18px 0; border-bottom: var(--rule); font-size: 0.82rem; align-items: start; }
  .ch-head, .ch-presence, .ch-health { display: flex; flex-direction: column; min-width: 0; }
  .ch-name { font-weight: 650; color: var(--text-base); font-size: 0.92rem; }
  .ch-type, .ch-addr, .ch-presence span, .ch-health span, .ch-pref { font: 0.68rem/1.5 var(--mono); color: var(--text-muted); }
  .ch-type { text-transform: uppercase; letter-spacing: 0.08em; }
  .ch-addr { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 4px; }
  .ch-badges { display: flex; flex-wrap: wrap; gap: 4px; }
  .cap { font: 0.63rem var(--mono); padding: 2px 6px; color: var(--text-muted); border: var(--rule); text-transform: uppercase; }
  .cap-warn, .ch-presence b, .ch-health b { color: var(--text-base); }
  .ch-presence b, .ch-health b { font: 650 0.67rem var(--mono); letter-spacing: 0.04em; }
  .ch-pref { text-transform: uppercase; }
  [data-label]::before { display: none; }
  @media (max-width: 760px) {
    .ch-header { display: block; }
    .ch-summary { margin-top: 12px; }
    .ch-matrix-head { display: none; }
    .ch-card { display: grid; grid-template-columns: 1fr 1fr; gap: 18px 22px; }
    [data-label]::before { display: block; content: attr(data-label); font: 0.6rem var(--mono); letter-spacing: 0.08em; color: var(--text-faint); text-transform: uppercase; margin-bottom: 5px; }
  }
  @media (max-width: 440px) { .ch-card { grid-template-columns: 1fr; } }
</style>
