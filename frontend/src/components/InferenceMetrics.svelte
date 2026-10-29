<script>
  import { api } from '../lib/api.js'
  import { metric, percent, summarize, callMetrics } from '../lib/inference.js'

  let { conversationID, messageID } = $props()
  let attempts = $state([])
  let loaded = $state(false)
  let loading = $state(false)
  let error = $state('')
  let cursor = $state('')
  let totals = $derived(summarize(attempts))

  async function load(more = false) {
    if (loading) return
    loading = true
    error = ''
    try {
      const page = await api.get(`/api/conversations/${encodeURIComponent(conversationID)}/messages/${messageID}/inference${more ? '?cursor=' + encodeURIComponent(cursor) : ''}`)
      attempts = more ? [...attempts, ...page.attempts] : page.attempts
      cursor = page.next_cursor || ''
      loaded = true
    } catch (e) { error = e.message }
    finally { loading = false }
  }
</script>

<details class="inference" ontoggle={event => { if (event.currentTarget.open && !loaded) load() }}>
  <summary>Inference metrics{#if loaded && attempts.length} · {attempts.length}{cursor ? '+' : ''} calls · {metric(totals.input)} in · {metric(totals.output)} out{/if}</summary>
  {#if loading}<p role="status">Loading metrics…</p>{/if}
  {#if error}<p role="alert">{error} <button class="button small" onclick={() => load()}>Retry</button></p>{/if}
  {#if loaded}
    {#if !attempts.length}<p>No recorded metrics for this reply. Metrics are collected only after instrumentation is enabled.</p>
    {:else}
      <p>{cursor ? 'Partial totals' : 'All attempts, including retries'} · Cache hit ratio: {percent(totals.cache)}</p>
      <p>Timings start at AgentFoundry’s upstream dispatch, not message submission. Generation rate is observed from streamed chunks, not exact GPU decode speed. Unreported measurements remain unavailable.</p>
      {#each attempts as attempt (attempt.id)}
        <details class="call">
          <summary>{attempt.model} · {attempt.status}{attempt.backup ? ' · backup' : ''} · {metric(attempt.metrics?.duration_ms, ' ms')}</summary>
          <p>Provider {attempt.provider_id} · activity attempt {attempt.activity_attempt} · HTTP attempt {attempt.http_attempt}</p>
          <dl>{#each callMetrics(attempt.metrics || {}) as [label, value]}<div><dt>{label}</dt><dd>{value}</dd></div>{/each}</dl>
          {#if attempt.metrics?.provider_timing_source}<p>Provider timings: {attempt.metrics.provider_timing_source}</p>{/if}
        </details>
      {/each}
      {#if cursor}<button class="button small" disabled={loading} onclick={() => load(true)}>Load more calls</button>{/if}
    {/if}
  {/if}
</details>

<style>
  .inference { margin-top: var(--space-3); color: var(--text-muted); font-size: .75rem; }
  summary { cursor: pointer; padding-block: var(--space-2); overflow-wrap: anywhere; }
  p { line-height: 1.6; margin-block: var(--space-2); }
  .call { background: var(--bg-raised); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3); margin-block: var(--space-2); }
  dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: var(--space-3); }
  dd { margin: var(--space-1) 0 0; color: var(--text-base); font-variant-numeric: tabular-nums; }
</style>
