<script>
  import { api } from '../lib/api.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'

  let status = $state(null)
  let loading = $state(true)
  let error = $state('')
  let compacting = $state(false)
  let now = $state(Date.now())
  let version = 0

  $effect(() => {
    load()
    const timer = setInterval(() => { now = Date.now(); load() }, 5000)
    return () => { version++; clearInterval(timer) }
  })

  async function load() {
    const request = ++version
    try {
      const result = await api.get('/api/context')
      if (request !== version) return
      status = result
      error = ''
    } catch (e) { if (request === version) error = e.message }
    finally { if (request === version) loading = false }
  }

  async function compactNow() {
    if (compacting) return
    compacting = true
    try { await api.post('/api/context/compact', {}); await load() }
    catch (e) { error = e.message }
    finally { compacting = false }
  }

  const number = value => Number(value || 0).toLocaleString()
  const category = value => String(value || 'Fact').replaceAll('_', ' ').toLowerCase()
  function fmtTime(value) {
    if (!value || String(value).startsWith('0001')) return 'Not yet'
    const elapsed = now - new Date(value).getTime()
    if (elapsed < 60000) return 'Just now'
    if (elapsed < 3600000) return Math.floor(elapsed / 60000) + 'm ago'
    if (elapsed < 86400000) return Math.floor(elapsed / 3600000) + 'h ago'
    return new Date(value).toLocaleDateString()
  }
</script>

<main class="page" aria-labelledby="context-title"><div class="page-inner">
  <header class="page-heading"><div><p class="eyebrow">Manage</p><h1 class="page-title" id="context-title">Context</h1><p class="page-description">The memories and conversation history Eve carries forward.</p></div><button class="button" onclick={compactNow} disabled={!status?.enabled || compacting || status?.historian?.running}><Icon name="context" size={16} />{compacting || status?.historian?.running ? 'Summarizing…' : 'Summarize history'}</button></header>
  {#if loading}<EmptyState loading title="Gathering context" />
  {:else}
    {#if error}<div class="notice error" role="alert"><Icon name="warning" /><span>{error}</span><button class="button small" onclick={load}>Try again</button></div>{/if}
    {#if status}
      <div class="context-overview">
        <section class="budget-panel" aria-labelledby="budget-title">
          <div class="section-header"><h2 class="section-title" id="budget-title">History budget</h2><span class="badge" data-tone={(status.pressure || 0) > .9 ? 'warning' : 'accent'}>{Math.round((status.pressure || 0) * 100)}% estimated</span></div>
          <div class="budget-value">{number(status.rendered_tokens)}<span> / {number(status.budget_tokens)} tokens</span></div>
          <progress max={Math.max(1, status.budget_tokens || 0)} value={Math.min(status.rendered_tokens || 0, status.budget_tokens || 0)} aria-label="Estimated local history budget"></progress>
          <p class="budget-description">Local history prepared for a new or replacement run—not the live context of an existing run.</p>
          <dl class="source-list"><div><dt><span class="source-dot summaries"></span>Summaries</dt><dd>{number(status.sources?.compartments)}</dd></div><div><dt><span class="source-dot memories"></span>Memories</dt><dd>{number(status.sources?.memories)}</dd></div><div><dt><span class="source-dot history"></span>Recent messages</dt><dd>{number(status.sources?.raw_tail)}</dd></div></dl>
        </section>
        <section class="historian-panel" aria-labelledby="historian-title"><div class="section-header"><h2 class="section-title" id="historian-title">History summaries</h2><span class="badge" data-tone={status.historian?.running ? 'accent' : undefined}>{!status.enabled ? 'Not configured' : status.historian?.running ? 'Working' : 'Ready'}</span></div><p>Eve keeps recent messages intact and condenses older history when a historian is configured.</p><dl><div><dt>Last summary</dt><dd>{fmtTime(status.historian?.last_run_at)}</dd></div><div><dt>Messages retained</dt><dd>{number(status.coverage?.total_messages)}</dd></div><div><dt>Already summarized</dt><dd>{number(status.coverage?.compartmentalized)}</dd></div></dl>{#if status.historian?.last_error}<div class="notice error" role="status">{status.historian.last_error}</div>{/if}</section>
      </div>
      <details class="context-explainer"><summary><Icon name="info" size={16} />What these numbers mean</summary><div><p>The estimate above describes the local renderer before final message and byte limits. Existing persistent runs keep their own context; these are not live AgentFoundry token counts.</p>{#if status.bootstrap_preview_available}<p>After adding task context and applying safety limits, the next-run preview contains <strong>{number(status.bootstrap_preview_messages)} messages</strong> and approximately <strong>{number(status.bootstrap_preview_tokens)} tokens</strong>.</p>{/if}{#if !status.enabled}<p>Automatic summarization is off. Configure HISTORIAN_AGENT_ID on the server to enable it.</p>{/if}</div></details>
      <section class="memory-section" aria-labelledby="memories-title"><div class="section-header"><h2 class="section-title" id="memories-title">Durable memories <span class="badge">{status.memories?.length || 0}</span></h2><span class="section-note">Useful details, kept for later</span></div>
        {#if !status.memories?.length}<EmptyState icon="context" title="Memories start with a conversation" description="Preferences, decisions, and useful facts captured from your conversations will appear here." />
        {:else}<div class="memory-list">{#each status.memories as memory}<article class="memory-row"><span class="badge memory-category">{category(memory.category)}</span><p>{memory.content}</p><span class="importance" title="Importance assigned during memory capture">Importance {memory.importance}</span></article>{/each}</div>{/if}
      </section>
      <section class="summary-section" aria-labelledby="summaries-title"><div class="section-header"><h2 class="section-title" id="summaries-title">Conversation summaries <span class="badge">{status.compartments?.length || 0}</span></h2></div>
        {#if !status.compartments?.length}<EmptyState icon="archive" title="Nothing summarized yet" description="As your conversation grows, older messages can become concise summaries without losing the recent thread." />
        {:else}<div class="summary-list">{#each status.compartments as compartment}<details class="summary-row"><summary><Icon name="chevron" size={16} /><span>Messages {compartment.start_msg_id}–{compartment.end_msg_id}</span><span class="badge">{compartment.tier === 'dropped' ? 'Not included' : compartment.tier.toUpperCase()}</span><time datetime={compartment.created_at}>{fmtTime(compartment.created_at)}</time></summary><div class="summary-body"><p>{compartment.summary}</p>{#if compartment.facts?.length}<ul>{#each compartment.facts as fact}<li><strong>{category(fact.category)}:</strong> {fact.content}</li>{/each}</ul>{/if}</div></details>{/each}</div>{/if}
      </section>
    {:else}<EmptyState icon="context" title="No context available" description="Open a conversation with Eve to get started." />{/if}
  {/if}
</div></main>

<style>
  .context-overview { display: grid; grid-template-columns: 1.2fr 1fr; border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .budget-panel, .historian-panel { padding: var(--space-6); min-width: 0; }
  .historian-panel { border-left: var(--rule); }
  .budget-value { font-size: 1.75rem; font-weight: 550; letter-spacing: -.035em; line-height: 1.3; font-variant-numeric: tabular-nums; }
  .budget-value > span { font-size: .75rem; font-weight: 400; color: var(--text-muted); letter-spacing: 0; }
  progress { display: block; width: 100%; height: 6px; border: 0; border-radius: var(--radius-sm); accent-color: var(--accent); background: var(--bg-deep); overflow: hidden; margin-block: var(--space-4); }
  progress::-webkit-progress-bar { background: var(--bg-deep); }
  progress::-webkit-progress-value { background: var(--accent); border-radius: var(--radius-sm); }
  progress::-moz-progress-bar { background: var(--accent); }
  .budget-description, .historian-panel > p { font-size: .8125rem; color: var(--text-muted); margin: 0 0 var(--space-4); }
  dl { margin: 0; }
  dl > div { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3); font-size: .8125rem; padding-block: var(--space-2); }
  dt { color: var(--text-muted); }
  dd { margin: 0; font-variant-numeric: tabular-nums; font-weight: 500; }
  .source-list dt { display: flex; align-items: center; gap: var(--space-2); }
  .source-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--accent); }
  .source-dot.memories { background: var(--success); }
  .source-dot.history { background: var(--text-muted); }
  .context-explainer { margin-top: var(--space-4); color: var(--text-muted); font-size: .8125rem; }
  .context-explainer summary { display: flex; align-items: center; gap: var(--space-2); cursor: pointer; min-height: 40px; width: fit-content; }
  .context-explainer > div { padding: var(--space-1) var(--space-4); border-left: var(--rule); margin-left: var(--space-2); max-width: 80ch; }
  .context-explainer p { margin: 0 0 var(--space-3); }
  .memory-section, .summary-section { margin-top: var(--space-10); }
  .section-note { font-size: .75rem; color: var(--text-muted); }
  .memory-list, .summary-list { border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .memory-row { display: grid; grid-template-columns: 150px minmax(0, 1fr) auto; align-items: start; gap: var(--space-4); padding: var(--space-4) var(--space-5); }
  .memory-row + .memory-row, .summary-row + .summary-row { border-top: var(--rule); }
  .memory-category { text-transform: capitalize; }
  .memory-row p { margin: 0; font-size: .875rem; }
  .importance { color: var(--text-muted); font-size: .6875rem; }
  .summary-row > summary { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); font-size: .8125rem; font-weight: 500; cursor: pointer; }
  .summary-row time { margin-left: auto; color: var(--text-muted); font-size: .75rem; font-weight: 400; }
  .summary-body { padding: 0 var(--space-5) var(--space-5); font-size: .875rem; overflow-wrap: anywhere; }
  .summary-body p { margin-top: 0; white-space: pre-wrap; }
  .summary-body ul { padding-left: var(--space-5); color: var(--text-muted); }
  .notice { margin-bottom: var(--space-5); }
  @media (max-width: 900px) { .context-overview { grid-template-columns: 1fr; } .historian-panel { border-left: 0; border-top: var(--rule); } .memory-row { grid-template-columns: 1fr; gap: var(--space-2); } }
  @media (max-width: 640px) { .budget-panel, .historian-panel { padding: var(--space-4); } .summary-row time { margin-left: 0; } .budget-value > span { display: block; margin-top: var(--space-1); } }
</style>
