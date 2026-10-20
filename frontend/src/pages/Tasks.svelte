<script>
  import { api } from '../lib/api.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'

  let tasks = $state([])
  let loading = $state(true)
  let error = $state('')
  let now = $state(Date.now())
  let replies = $state({})
  let pending = $state({})
  let showArchived = $state(false)
  let archiveOffset = $state(0)
  let loadVersion = 0
  let filter = $state('all')
  const labels = { running: 'In progress', needs_input: 'Needs your input', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled' }
  const tones = { running: 'accent', needs_input: 'warning', completed: 'success', failed: 'error' }
  let visibleTasks = $derived(filter === 'all' || showArchived ? tasks : tasks.filter(task => task.status === filter))

  $effect(() => {
    load()
    const iv = setInterval(() => {
      now = Date.now()
      load()
    }, 5000)
    return () => clearInterval(iv)
  })

  async function load() {
    const version = ++loadVersion
    try {
      const list = await api.get(showArchived ? '/api/tasks/archive?offset=' + archiveOffset : '/api/tasks')
      if (version !== loadVersion) return
      tasks = Array.isArray(list) ? list.slice().sort((a, b) => new Date(b.updated_at || b.created_at) - new Date(a.updated_at || a.created_at)) : []
      error = ''
    } catch (e) {
      if (version === loadVersion) error = e.message
    } finally {
      if (version === loadVersion) loading = false
    }
  }

  async function archiveTask(id) {
    if (pending[id]) return
    pending[id] = true
    try { await api.post('/api/tasks/' + id + '/archive', {}); await load() }
    catch (e) { error = e.message } finally { pending[id] = false }
  }

  async function replyToTask(id) {
    if (pending[id]) return
    const content = (replies[id] || '').trim()
    if (!content) return
    pending[id] = true
    try {
      await api.post('/api/tasks/' + id + '/reply', { content })
      replies[id] = ''
      load()
    } catch (e) {
      error = e.message
    } finally { pending[id] = false }
  }

  async function cancelTask(id) {
    if (pending[id]) return
    pending[id] = true
    try {
      await api.post('/api/tasks/' + id + '/cancel', {})
      load()
    } catch (e) {
      error = e.message
    } finally { pending[id] = false }
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

<main class="page" aria-labelledby="tasks-title"><div class="page-inner">
  <header class="page-heading">
    <div><p class="eyebrow">Workspace</p><h1 class="page-title" id="tasks-title">Tasks</h1><p class="page-description">See what Eve is working on, and step in when she needs you.</p></div>
    <span class="badge" data-tone={activeCount() ? 'accent' : undefined}>{activeCount()} active</span>
  </header>
  <div class="ts-toolbar">
    <div class="segmented" role="group" aria-label="Task view"><button aria-pressed={!showArchived} onclick={() => { if (showArchived) { showArchived = false; archiveOffset = 0; loading = true } }}>Current tasks</button><button aria-pressed={showArchived} onclick={() => { if (!showArchived) { showArchived = true; archiveOffset = 0; loading = true } }}><Icon name="archive" size={15} />Archived tasks</button></div>
    {#if !showArchived}<label class="ts-filter">Show<select class="input" aria-label="Filter tasks" bind:value={filter}><option value="all">All tasks</option><option value="needs_input">Needs your input</option><option value="running">In progress</option><option value="completed">Completed</option><option value="failed">Failed</option><option value="cancelled">Cancelled</option></select></label>{/if}
  </div>

  {#if loading}
    <EmptyState loading title="Loading tasks" />
  {:else if error}
    <div class="notice error" role="alert"><Icon name="warning" /><span>{error}</span></div><button class="button retry" onclick={load}>Try again</button>
  {:else if tasks.length === 0}
    <EmptyState icon="tasks" title={showArchived ? 'Your archive is clear' : 'No background tasks yet.'} description={showArchived ? 'Completed tasks you archive will stay here for later.' : 'Ask Eve to work on something in the background. You can track progress and reply to questions here.'} />
  {:else if visibleTasks.length === 0}
    <EmptyState icon="tasks" title="No tasks in this view" description="Try another filter to see the rest of your tasks."><button class="button" onclick={() => { filter = 'all' }}>Show all tasks</button></EmptyState>
  {:else}
    <div class="ts-list">
      {#each visibleTasks as t (t.id)}
        <article class="ts-item" class:needs-input={t.status === 'needs_input'}>
          <div class="ts-line">
            <span class="badge" data-tone={tones[t.status]}>{labels[t.status] || t.status}</span>
            <h2 class="ts-name">{t.agent_name || t.agent_id}</h2>
            <time class="ts-time" datetime={t.updated_at || t.created_at}>{fmtTime(t.updated_at || t.created_at)}</time>
            <div class="ts-actions">
            {#if activeStatuses.includes(t.status)}
              <button class="button danger small" disabled={pending[t.id]} onclick={() => cancelTask(t.id)}>Cancel task</button>
            {/if}
            {#if !showArchived && t.reported && !activeStatuses.includes(t.status)}<button class="button quiet small" disabled={pending[t.id]} onclick={() => archiveTask(t.id)}><Icon name="archive" size={15} />Archive task</button>{/if}
            </div>
          </div>
          <div class="ts-detail">
            <div class="ts-msg">{t.message}</div>
            {#if t.status === 'running' && t.dispatch_error}<div class="ts-question">Queued for dispatch retry: {t.dispatch_error}</div>{/if}

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
                  class="input"
                  aria-label={'Reply to ' + (t.agent_name || t.agent_id)}
                  placeholder="Reply to the task…"
                  bind:value={replies[t.id]}
                  onkeydown={(e) => { if (e.key === 'Enter' && !e.isComposing) replyToTask(t.id) }}
                />
                <button class="button primary" disabled={pending[t.id] || !replies[t.id]?.trim()} onclick={() => replyToTask(t.id)}>Reply</button>
              </div>
            {/if}
          </div>
        </article>
      {/each}
    </div>
  {/if}
  {#if showArchived && !loading}
    <div class="ts-pagination"><button class="button small" disabled={archiveOffset === 0} onclick={() => { archiveOffset = Math.max(0, archiveOffset - 100) }}>Previous archive page</button><button class="button small" disabled={tasks.length < 100} onclick={() => { archiveOffset += 100 }}>Next archive page</button></div>
  {/if}
</div></main>

<style>
  .ts-toolbar { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); margin-bottom: var(--space-6); flex-wrap: wrap; }
  .ts-filter { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); font-size: .8125rem; }
  .ts-filter select { width: auto; }
  .ts-list { border: var(--rule); border-radius: var(--radius); overflow: hidden; background: var(--bg-raised); }
  .ts-item { padding: var(--space-5) var(--space-6); overflow-wrap: anywhere; }
  .ts-item + .ts-item { border-top: var(--rule); }
  .ts-item.needs-input { border-left: 3px solid var(--warning); }
  .ts-line { display: flex; flex-wrap: wrap; gap: var(--space-3); align-items: center; margin-bottom: var(--space-3); }
  .ts-name { margin: 0; font-size: .875rem; font-weight: 600; }
  .ts-actions { display: flex; gap: var(--space-2); margin-left: auto; }
  .ts-time { font-size: .6875rem; color: var(--text-muted); }
  .ts-msg { font-size: .875rem; color: var(--text-base); white-space: pre-wrap; }
  .ts-question { background: var(--warning-bg); color: var(--warning); padding: var(--space-3) var(--space-4); margin-top: var(--space-4); border-radius: var(--radius-sm); font-size: .875rem; }
  .ts-result { font-size: .875rem; color: var(--text-muted); white-space: pre-wrap; margin-top: var(--space-3); }
  .ts-failed { color: var(--danger); }
  .ts-replies { margin-top: var(--space-4); padding-left: var(--space-4); border-left: var(--rule); }
  .ts-reply { font-size: .8125rem; color: var(--text-muted); }
  .ts-input-row { display: flex; gap: var(--space-2); margin-top: var(--space-4); max-width: 600px; }
  .ts-pagination { display: flex; flex-wrap: wrap; gap: var(--space-3); margin-top: var(--space-6); }
  .retry { margin-top: var(--space-4); }
  @media (max-width: 640px) { .ts-item { padding: var(--space-4); } .ts-actions { margin-left: 0; } .ts-filter { width: 100%; } .ts-filter select { flex: 1; } }
</style>
