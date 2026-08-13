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
  <h2 class="ts-title">Tasks
    {#if activeCount() > 0}
      <span class="ts-count">{activeCount()} active</span>
    {/if}
  </h2>

  {#if loading}
    <div class="ts-empty">Loading tasks…</div>
  {:else if error}
    <div class="ts-empty ts-error">{error}</div>
  {:else if tasks.length === 0}
    <div class="ts-empty">No background tasks yet.</div>
  {:else}
    <div class="ts-list">
      {#each tasks as t (t.id)}
        <div class="ts-item">
          <div class="ts-line">
            <span class="ts-status ts-{t.status}">{t.status}</span>
            <span class="ts-name">{t.agent_name || t.agent_id}</span>
            <span class="ts-time">{fmtTime(t.updated_at || t.created_at)}</span>
            {#if activeStatuses.includes(t.status)}
              <button class="ts-cancel" onclick={() => cancelTask(t.id)} aria-label="Cancel">✕</button>
            {/if}
          </div>
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
      {/each}
    </div>
  {/if}
</div>

<style>
  .ts-page { padding: 20px 24px; max-width: 860px; }
  .ts-title { font-size: 1.1rem; font-weight: 600; margin: 0 0 16px; color: var(--text-base); display: flex; align-items: center; gap: 10px; }
  .ts-count { font-size: 0.75rem; color: var(--text-muted); background: var(--bg-card); padding: 2px 8px; border-radius: 999px; }
  .ts-empty { color: var(--text-muted); padding: 40px 0; text-align: center; font-size: 0.9rem; }
  .ts-error { color: #e5484d; }
  .ts-list { display: flex; flex-direction: column; gap: 10px; }
  .ts-item { background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px; padding: 12px 14px; }
  .ts-line { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
  .ts-status { font-size: 0.68rem; text-transform: uppercase; letter-spacing: 0.04em; padding: 2px 7px; border-radius: 999px; font-weight: 600; }
  .ts-running { background: rgba(61, 122, 249, 0.16); color: #5b8bff; }
  .ts-needs_input { background: rgba(255, 179, 71, 0.16); color: #ffb347; }
  .ts-completed { background: rgba(62, 184, 92, 0.16); color: #3eb85c; }
  .ts-failed { background: rgba(229, 72, 77, 0.16); color: #e5484d; }
  .ts-cancelled { background: var(--bg-sidebar); color: var(--text-muted); }
  .ts-name { font-weight: 600; font-size: 0.9rem; color: var(--text-base); }
  .ts-time { margin-left: auto; font-size: 0.75rem; color: var(--text-muted); }
  .ts-cancel { background: none; border: none; color: var(--text-muted); cursor: pointer; font-size: 0.85rem; padding: 2px 6px; border-radius: 6px; }
  .ts-cancel:hover { background: rgba(229, 72, 77, 0.12); color: #e5484d; }
  .ts-msg { font-size: 0.88rem; color: var(--text-base); margin-bottom: 4px; white-space: pre-wrap; }
  .ts-question { font-size: 0.85rem; color: #ffb347; margin-top: 4px; }
  .ts-result { font-size: 0.85rem; color: var(--text-muted); margin-top: 4px; white-space: pre-wrap; }
  .ts-failed { color: #e5484d; }
  .ts-replies { display: flex; flex-direction: column; gap: 2px; margin-top: 6px; }
  .ts-reply { font-size: 0.8rem; color: var(--text-muted); }
  .ts-input-row { display: flex; gap: 8px; margin-top: 10px; }
  .ts-input { flex: 1; background: var(--bg-sidebar); border: 1px solid var(--border); color: var(--text-base); border-radius: 8px; padding: 7px 10px; font-size: 0.85rem; font-family: inherit; }
  .ts-input:focus { outline: none; border-color: var(--purple-dim); }
  .ts-send { background: var(--purple-dim); color: var(--text-base); border: none; border-radius: 8px; padding: 7px 14px; font-size: 0.85rem; cursor: pointer; }
  .ts-send:hover { filter: brightness(1.1); }
</style>
