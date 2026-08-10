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
      <h2 class="ctx-title">Status</h2>
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
            <b>{status.historian.unsummarized_tokens.toLocaleString()} / {status.historian.trigger_threshold.toLocaleString()}</b>
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
  .ctx-page { padding: 24px; overflow-y: auto; height: 100%; max-width: 960px; margin: 0 auto; }
  .ctx-section { margin-bottom: 28px; }
  .ctx-title {
    font-size: 0.75rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-muted); margin: 0 0 10px; display: flex; align-items: center; gap: 8px;
  }
  .count-badge {
    background: var(--purple-dim); color: oklch(75% 0.2 292.0);
    border-radius: 20px; padding: 1px 8px; font-size: 0.7rem;
  }
  .ctx-empty { color: var(--text-muted); font-size: 0.85rem; padding: 8px 0; }
  .ctx-error { color: oklch(70% 0.2 20); }
  .ctx-banner {
    background: oklch(30% 0.06 292.7); border: 1px solid oklch(59.1% 0.249 292.7 / 0.4);
    color: oklch(82% 0.15 292.0); border-radius: 8px; padding: 10px 14px; font-size: 0.82rem; margin-bottom: 12px;
  }
  .status-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 10px; }
  .status-card {
    background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px;
    padding: 12px 14px; font-size: 0.82rem;
  }
  .status-label { font-size: 0.68rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted); margin-bottom: 8px; }
  .status-sub { color: var(--text-muted); margin-top: 6px; font-size: 0.78rem; }
  .budget-bar { height: 8px; border-radius: 4px; background: var(--bg-base); border: 1px solid var(--border); overflow: hidden; }
  .budget-fill { height: 100%; border-radius: 4px; transition: width 0.3s; }
  .budget-fill.ok { background: oklch(65% 0.18 145); }
  .budget-fill.mid { background: oklch(75% 0.15 80); }
  .budget-fill.warn { background: oklch(65% 0.2 25); }
  .src-row { display: flex; justify-content: space-between; padding: 2px 0; color: var(--text-muted); }
  .src-row b { color: var(--text-base); font-weight: 600; }
  .hist-error { margin-top: 6px; color: oklch(70% 0.2 20); font-size: 0.75rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ctx-actions { margin-top: 12px; }
  .compact-btn {
    background: var(--purple-dim); color: var(--text-base); border: 1px solid oklch(59.1% 0.249 292.7 / 0.4);
    border-radius: 8px; padding: 8px 16px; font-family: inherit; font-size: 0.83rem; cursor: pointer;
  }
  .compact-btn:hover { border-color: oklch(59.1% 0.249 292.7 / 0.7); }
  .compact-btn:disabled { opacity: 0.5; cursor: default; }
  .comp-list { display: flex; flex-direction: column; gap: 8px; }
  .comp-card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px; padding: 12px 14px; }
  .comp-head { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; font-size: 0.72rem; color: var(--text-muted); }
  .tier-badge {
    font-weight: 700; padding: 1px 7px; border-radius: 10px; font-size: 0.7rem;
    background: oklch(30% 0.06 292.7); color: oklch(80% 0.18 292.0); border: 1px solid oklch(59.1% 0.249 292.7 / 0.35);
  }
  .tier-dropped { opacity: 0.5; text-decoration: line-through; }
  .comp-range { font-weight: 600; color: var(--text-base); }
  .comp-importance { margin-left: auto; }
  .comp-summary { font-size: 0.85rem; line-height: 1.55; color: var(--text-base); white-space: pre-wrap; }
  .comp-facts { margin-top: 8px; display: flex; flex-wrap: wrap; gap: 6px; }
  .fact-chip, .cat-chip {
    font-size: 0.7rem; border-radius: 6px; padding: 2px 8px; border: 1px solid var(--border); color: var(--text-muted);
  }
  .cat-pref { background: oklch(30% 0.08 292.7); color: oklch(80% 0.2 292.0); }
  .cat-decision { background: oklch(30% 0.08 25); color: oklch(80% 0.18 25); }
  .cat-constraint { background: oklch(30% 0.08 220); color: oklch(80% 0.18 220); }
  .cat-fact { background: oklch(30% 0.05 145); color: oklch(78% 0.15 145); }
  .cat-naming { background: oklch(30% 0.08 60); color: oklch(80% 0.18 60); }
  .mem-list { display: flex; flex-direction: column; gap: 6px; }
  .mem-row { display: flex; align-items: center; gap: 10px; font-size: 0.83rem; padding: 7px 10px; background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px; }
  .mem-content { flex: 1; color: var(--text-base); }
  .mem-imp { color: var(--text-muted); font-size: 0.72rem; white-space: nowrap; }
</style>
