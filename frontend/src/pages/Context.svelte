<script>
  import { api } from '../lib/api.js'

  let status = $state(null)
  let loading = $state(true)
  let error = $state('')
  let compacting = $state(false)
  let now = $state(Date.now())

  $effect(() => {
    load()
    const iv = setInterval(() => {
      now = Date.now()
      load()
    }, 5000)
    return () => clearInterval(iv)
  })

  async function load() {
    try {
      status = await api.get('/api/context')
      error = ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function compactNow() {
    compacting = true
    try {
      await api.post('/api/context/compact', {})
      await load()
    } catch (e) {
      error = e.message
    } finally {
      compacting = false
    }
  }

  function pressureClass(p) {
    if (p >= 0.9) return 'warn'
    if (p >= 0.7) return 'mid'
    return 'ok'
  }

  function fmtTime(t) {
    if (!t || String(t).startsWith('0001')) return 'never'
    const d = Date.now() - new Date(t).getTime()
    if (d < 60000) return 'just now'
    if (d < 3600000) return Math.floor(d / 60000) + 'm ago'
    if (d < 86400000) return Math.floor(d / 3600000) + 'h ago'
    return new Date(t).toLocaleDateString()
  }

  function fmtDate(t) {
    return new Date(t).toLocaleString()
  }

  const catColors = {
    USER_PREFERENCES: 'pref',
    DECISIONS: 'decision',
    CONSTRAINTS: 'constraint',
    FACTS: 'fact',
    NAMING: 'naming',
  }

  const arr = (x) => (x || [])
</script>

<div class="ctx-page">
  {#if loading}
    <div class="ctx-empty">Loading context…</div>
  {:else if error}
    <div class="ctx-empty ctx-error">{error}</div>
  {:else if !status}
    <div class="ctx-empty">No context yet.</div>
  {:else}
    <div class="ctx-section">
      <div class="ctx-kicker">SYSTEM / WORKING MEMORY</div>
      <h2 class="ctx-page-title">Context</h2>
      <h3 class="ctx-title">Status</h3>
      {#if !status.enabled}
        <div class="ctx-banner">Context management is disabled (no historian agent configured).</div>
      {/if}
      <div class="status-grid">
        <div class="status-card">
          <div class="status-label">Context budget</div>
          <div class="budget-bar">
            <div class="budget-fill {pressureClass(status.pressure)}" style="width:{Math.min(status.pressure * 100, 100)}%"></div>
          </div>
          <div class="status-sub">
            {status.rendered_tokens.toLocaleString()} / {status.budget_tokens.toLocaleString()} tokens
            ({Math.round(status.pressure * 100)}%)
          </div>
        </div>
        <div class="status-card">
          <div class="status-label">Sources</div>
          <div class="src-row"><span>Summaries</span><b>{status.sources.compartments.toLocaleString()}</b></div>
          <div class="src-row"><span>Memory</span><b>{status.sources.memories.toLocaleString()}</b></div>
          <div class="src-row"><span>Raw tail</span><b>{status.sources.raw_tail.toLocaleString()}</b></div>
        </div>
        <div class="status-card">
          <div class="status-label">Historian</div>
          <div class="src-row">
            <span>Status</span>
            <b>{status.historian.running ? 'running…' : 'idle'}</b>
          </div>
          <div class="src-row"><span>Last run</span><b>{fmtTime(status.historian.last_run_at)}</b></div>
          <div class="src-row">
            <span>Unsummarized</span>
            <b>{status.historian.unsummarized_tokens.toLocaleString()} / {status.historian.trigger_threshold.toLocaleString()} tokens</b>
          </div>
          <div class="src-row"><span>Boundary msg</span><b>{status.historian.boundary_msg_id || '—'}</b></div>
          {#if status.historian.last_error}
            <div class="hist-error" title="{status.historian.last_error}">last error: {status.historian.last_error}</div>
          {/if}
        </div>
        <div class="status-card">
          <div class="status-label">Coverage</div>
          <div class="src-row"><span>Total messages</span><b>{status.coverage.total_messages}</b></div>
          <div class="src-row"><span>Summarized</span><b>{status.coverage.compartmentalized}</b></div>
          <div class="src-row"><span>Raw</span><b>{status.coverage.raw}</b></div>
        </div>
      </div>
      <div class="ctx-actions">
        <button class="compact-btn" onclick={compactNow} disabled={compacting || status.historian.running}>
          {compacting || status.historian.running ? 'Compacting…' : 'Compact now'}
        </button>
      </div>
    </div>

    <div class="ctx-section">
      <h2 class="ctx-title">Compartments <span class="count-badge">{arr(status.compartments).length}</span></h2>
      {#if arr(status.compartments).length === 0}
        <div class="ctx-empty">Nothing summarized yet. The historian runs automatically once the conversation grows past the trigger threshold.</div>
      {:else}
        <div class="comp-list">
          {#each arr(status.compartments) as c}
            <div class="comp-card">
              <div class="comp-head">
                <span class="tier-badge tier-{c.tier}">{c.tier}</span>
                <span class="comp-range">msgs {c.start_msg_id}–{c.end_msg_id}</span>
                <span class="comp-importance">imp {c.importance}</span>
                <span class="comp-date">{fmtDate(c.created_at)}</span>
              </div>
              <div class="comp-summary">{c.summary}</div>
              {#if arr(c.facts).length > 0}
                <div class="comp-facts">
                  {#each arr(c.facts) as f}
                    <span class="fact-chip cat-{catColors[f.category] || 'fact'}">{f.category}: {f.content}</span>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <div class="ctx-section">
      <h2 class="ctx-title">Memories <span class="count-badge">{arr(status.memories).length}</span></h2>
      {#if arr(status.memories).length === 0}
        <div class="ctx-empty">No memories captured yet. Durable facts from your conversation appear here.</div>
      {:else}
        <div class="mem-list">
          {#each arr(status.memories) as m}
            <div class="mem-row">
              <span class="cat-chip cat-{catColors[m.category] || 'fact'}">{m.category}</span>
              <span class="mem-content">{m.content}</span>
              <span class="mem-imp">imp {m.importance}</span>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .ctx-page { padding: 38px var(--page-pad) 64px; overflow-y: auto; height: 100%; width: 100%; }
  .ctx-section { max-width: var(--content-max); padding-bottom: 38px; margin-bottom: 38px; border-bottom: var(--rule); }
  .ctx-section:last-child { margin-bottom: 0; }
  .ctx-kicker { color: var(--text-faint); font: 0.65rem var(--mono); letter-spacing: 0.1em; }
  .ctx-page-title { font-size: 1.65rem; font-weight: 500; letter-spacing: -0.02em; margin: 3px 0 30px; }
  .ctx-title {
    font: 700 0.67rem var(--mono); text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-muted); margin: 0 0 12px; display: flex; align-items: center; gap: 8px;
  }
  .count-badge { color: var(--text-faint); font-weight: 400; }
  .ctx-empty { color: var(--text-muted); font-size: 0.85rem; padding: 8px 0; }
  .ctx-error { color: var(--text-base); }
  .ctx-banner {
    color: var(--text-base); border-left: 2px solid var(--text-base); padding: 8px 14px; font-size: 0.82rem; margin-bottom: 18px;
  }
  .status-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); border-top: 1px solid var(--border-strong); border-bottom: var(--rule); }
  .status-card {
    border-right: var(--rule); padding: 16px 18px 18px 0; margin-right: 18px; font-size: 0.82rem;
  }
  .status-card:last-child { border-right: 0; margin-right: 0; }
  .status-label { font: 600 0.65rem var(--mono); text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted); margin-bottom: 10px; }
  .status-sub { color: var(--text-muted); margin-top: 8px; font: 0.7rem var(--mono); }
  .budget-bar { height: 5px; background: var(--bg-deep); border: var(--rule); overflow: hidden; }
  .budget-fill { height: 100%; transition: width 0.3s; background: var(--text-base); }
  .budget-fill.mid { background: var(--text-muted); }
  .budget-fill.warn { background: var(--text-base); }
  .src-row { display: flex; justify-content: space-between; padding: 2px 0; color: var(--text-muted); }
  .src-row b { color: var(--text-base); font: 600 0.72rem var(--mono); text-align: right; }
  .hist-error { margin-top: 6px; color: var(--text-base); font: 0.7rem var(--mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ctx-actions { margin-top: 12px; }
  .compact-btn {
    background: var(--accent); color: var(--bg-deep); border: 1px solid var(--accent);
    border-radius: 1px; padding: 8px 16px; font-size: 0.83rem; cursor: pointer;
  }
  .compact-btn:hover { background: var(--accent-strong); border-color: var(--accent-strong); }
  .compact-btn:disabled { opacity: 0.5; cursor: default; }
  .comp-list { border-top: 1px solid var(--border-strong); }
  .comp-card { border-bottom: var(--rule); padding: 16px 0 18px; }
  .comp-head { display: flex; align-items: center; gap: 12px; margin-bottom: 8px; font: 0.68rem var(--mono); color: var(--text-muted); }
  .tier-badge {
    font-weight: 700; color: var(--text-base); text-transform: uppercase;
  }
  .tier-dropped { opacity: 0.5; text-decoration: line-through; }
  .comp-range { font-weight: 600; color: var(--text-base); }
  .comp-importance { margin-left: auto; }
  .comp-summary { font-size: 0.85rem; line-height: 1.55; color: var(--text-base); white-space: pre-wrap; }
  .comp-facts { margin-top: 10px; display: flex; flex-wrap: wrap; gap: 6px 14px; }
  .fact-chip, .cat-chip {
    font: 0.67rem var(--mono); color: var(--text-muted);
  }
  .mem-list { border-top: 1px solid var(--border-strong); }
  .mem-row { display: grid; grid-template-columns: 150px 1fr 70px; align-items: start; gap: 18px; font-size: 0.83rem; padding: 11px 0; border-bottom: var(--rule); }
  .mem-content { flex: 1; color: var(--text-base); }
  .mem-imp { color: var(--text-muted); font: 0.68rem var(--mono); white-space: nowrap; text-align: right; }
  @media (max-width: 860px) { .status-grid { grid-template-columns: 1fr 1fr; } .status-card:nth-child(2) { border-right: 0; } }
  @media (max-width: 560px) {
    .status-grid { grid-template-columns: 1fr; }
    .status-card { border-right: 0; border-bottom: var(--rule); margin-right: 0; padding-right: 0; }
    .status-card:last-child { border-bottom: 0; }
    .comp-head { align-items: flex-start; flex-wrap: wrap; }
    .comp-importance { margin-left: 0; }
    .comp-date { width: 100%; }
    .mem-row { grid-template-columns: 1fr; gap: 4px; }
    .mem-imp { text-align: left; }
  }
</style>
