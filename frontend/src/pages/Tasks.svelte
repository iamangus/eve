<script>
  import { api } from '../lib/api.js'

  let tasks = $state([])
  let loading = $state(true)
  let error = $state('')
  let now = $state(Date.now())
  let replies = $state({})

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
      const list = await api.get('/api/tasks')
      tasks = Array.isArray(list) ? list.slice().sort((a, b) => new Date(b.updated_at || b.created_at) - new Date(a.updated_at || a.created_at)) : []
      error = ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function replyToTask(id) {
    const content = (replies[id] || '').trim()
    if (!content) return
    try {
      await api.post('/api/tasks/' + id + '/reply', { content })
      replies[id] = ''
      load()
    } catch (e) {
      error = e.message
    }
  }

  async function cancelTask(id) {
    try {
      await api.post('/api/tasks/' + id + '/cancel', {})
      load()
    } catch (e) {
      error = e.message
    }
  }

  function fmtTime(t) {
    if (!t || String(t).startsWith('0001')) return 'never'
    const d = Date.now() - new Date(t).getTime()
    if (d < 60000) return 'just now'
    if (d < 3600000) return Math.floor(d / 60000) + 'm ago'
    if (d < 86400000) return Math.floor(d / 3600000) + 'h ago'
    return new Date(t).toLocaleString()
  }

  const activeStatuses = ['running', 'needs_input']
  const activeCount = () => tasks.filter(t => activeStatuses.includes(t.status)).length
</script>

<div class="ts-page">
  <header class="ts-head">
    <div><div class="ts-kicker">SYSTEM / WORK QUEUE</div><h2 class="ts-title">Tasks</h2></div>
    <div class="ts-count">{activeCount()} active · {tasks.length} total tasks</div>
  </header>

  {#if loading}
    <div class="ts-empty">Loading tasks…</div>
  {:else if error}
    <div class="ts-empty ts-error">{error}</div>
  {:else if tasks.length === 0}
    <div class="ts-empty">No background tasks yet.</div>
  {:else}
    <div class="ts-list">
      <div class="ts-list-head"><span>Status / agent</span><span>Detail</span><span>Updated</span></div>
      {#each tasks as t (t.id)}
        <div class="ts-item">
          <div class="ts-line">
            <span class="ts-status ts-{t.status}">{t.status.replaceAll('_', ' ')}</span>
            <span class="ts-name">{t.agent_name || t.agent_id}</span>
            {#if activeStatuses.includes(t.status)}
              <button class="ts-cancel" onclick={() => cancelTask(t.id)}>Cancel task</button>
            {/if}
          </div>
          <div class="ts-detail">
            <div class="ts-msg">{t.message}</div>

            {#if t.question}
              <div class="ts-question">Question: {t.question}</div>
            {/if}

            {#if t.status === 'completed' && t.result}
              <div class="ts-result">{t.result}</div>
            {/if}

            {#if t.status === 'failed'}
              <div class="ts-result ts-failed">{t.result || 'task failed'}</div>
            {/if}

            {#if t.replies && t.replies.length > 0}
              <div class="ts-replies">
                {#each t.replies as r (r.created_at)}
                  <div class="ts-reply">you: {r.content}</div>
                {/each}
              </div>
            {/if}

            {#if t.status === 'needs_input'}
              <div class="ts-input-row">
                <input
                  class="ts-input"
                  placeholder="Reply to the task…"
                  bind:value={replies[t.id]}
                  onkeydown={(e) => { if (e.key === 'Enter' && !e.isComposing) replyToTask(t.id) }}
                />
                <button class="ts-send" onclick={() => replyToTask(t.id)}>Reply</button>
              </div>
            {/if}
          </div>
          <time class="ts-time" datetime={t.updated_at || t.created_at}>{fmtTime(t.updated_at || t.created_at)}</time>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .ts-page { padding: 38px var(--page-pad) 64px; width: 100%; overflow-y: auto; }
  .ts-head { max-width: var(--content-max); display: flex; justify-content: space-between; align-items: end; padding-bottom: 24px; }
  .ts-kicker, .ts-count, .ts-list-head, .ts-status, .ts-time { font-family: var(--mono); }
  .ts-kicker { color: var(--text-faint); font-size: 0.65rem; letter-spacing: 0.1em; }
  .ts-title { font-size: 1.65rem; font-weight: 500; letter-spacing: -0.02em; margin: 3px 0 0; color: var(--text-base); }
  .ts-count { font-size: 0.68rem; color: var(--text-muted); }
  .ts-empty { color: var(--text-muted); padding: 24px 0; border-top: var(--rule); max-width: var(--content-max); font-size: 0.88rem; }
  .ts-error { color: var(--text-base); border-top-color: var(--text-base); }
  .ts-list { max-width: var(--content-max); border-top: 1px solid var(--border-strong); }
  .ts-list-head, .ts-item { display: grid; grid-template-columns: 210px minmax(0, 1fr) 110px; gap: 24px; }
  .ts-list-head { padding: 9px 0; border-bottom: var(--rule); color: var(--text-faint); font-size: 0.63rem; text-transform: uppercase; letter-spacing: 0.08em; }
  .ts-item { padding: 18px 0 20px; border-bottom: var(--rule); align-items: start; }
  .ts-line { display: flex; align-items: flex-start; flex-direction: column; gap: 4px; }
  .ts-status { font-size: 0.65rem; text-transform: uppercase; letter-spacing: 0.06em; color: var(--text-muted); font-weight: 700; }
  .ts-needs_input, .ts-failed { color: var(--text-base); }
  .ts-name { font-weight: 650; font-size: 0.9rem; color: var(--text-base); }
  .ts-time { font-size: 0.68rem; color: var(--text-muted); }
  .ts-cancel { background: none; border: 0; border-bottom: 1px solid transparent; color: var(--text-muted); cursor: pointer; font: 0.66rem var(--mono); padding: 4px 0 0; }
  .ts-cancel:hover { color: var(--accent); border-bottom-color: currentColor; }
  .ts-msg { font-size: 0.88rem; color: var(--text-base); margin-bottom: 4px; white-space: pre-wrap; }
  .ts-question { font-size: 0.85rem; color: var(--text-base); margin-top: 10px; padding-left: 12px; border-left: 2px solid var(--text-base); }
  .ts-result { font-size: 0.85rem; color: var(--text-muted); margin-top: 4px; white-space: pre-wrap; }
  .ts-replies { display: flex; flex-direction: column; gap: 2px; margin-top: 6px; }
  .ts-reply { font: 0.75rem var(--mono); color: var(--text-muted); }
  .ts-input-row { display: flex; gap: 8px; margin-top: 10px; }
  .ts-input { flex: 1; background: var(--bg-deep); border: 1px solid var(--border-strong); color: var(--text-base); border-radius: 1px; padding: 7px 10px; font-size: 0.85rem; }
  .ts-input:focus { border-color: var(--accent); }
  .ts-send { background: var(--accent); color: var(--bg-deep); border: none; border-radius: 1px; padding: 7px 14px; font-size: 0.85rem; cursor: pointer; }
  .ts-send:hover { background: var(--accent-strong); }
  @media (max-width: 720px) {
    .ts-head { display: block; }
    .ts-count { margin-top: 12px; }
    .ts-list-head { display: none; }
    .ts-item { grid-template-columns: 1fr; gap: 12px; }
    .ts-line { padding-right: 0; }
  }
</style>
