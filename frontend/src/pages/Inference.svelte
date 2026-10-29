<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { metric, percent } from '../lib/inference.js'
  let providers = $state([])
  let loading = $state(true)
  let refreshing = $state(false)
  let error = $state('')
  let alive = true

  async function refresh() {
    if (refreshing) return
    refreshing = true
    try {
      const list = await api.get('/api/inference/providers')
      const results = await Promise.all(list.providers.map(async p => {
        try { return await api.get('/api/inference/providers?provider_id=' + encodeURIComponent(p.id)) }
        catch { return { ...p, message: 'Metrics unavailable.' } }
      }))
      if (alive) { providers = results; error = '' }
    } catch (e) { if (alive) error = e.message }
    finally { if (alive) { loading = false; refreshing = false } }
  }
  onMount(() => {
    refresh()
    const timer = setInterval(refresh, 10000)
    return () => { alive = false; clearInterval(timer) }
  })
</script>

<main class="page" aria-labelledby="inference-title"><div class="page-inner">
  <header class="page-heading"><div><p class="eyebrow">Workspace</p><h1 class="page-title" id="inference-title">Inference</h1><p class="page-description">Provider health and server-wide throughput, separate from reply measurements.</p></div><button class="button" disabled={refreshing} onclick={refresh}>{refreshing ? 'Refreshing…' : 'Refresh'}</button></header>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if loading}<p role="status">Loading providers…</p>{:else if !providers.length && !error}<p>No accessible providers.</p>{/if}
  <div class="providers">
    {#each providers as p (p.id)}
      <section class="provider" aria-label={p.name}>
        <h2>{p.name} <span class="badge">{p.available ? 'Reporting' : 'Unavailable'}</span></h2>
        <p>{p.provider_type} · server aggregate</p>
        {#if p.available}
          <dl>
            {#each [['Queue depth', metric(p.queue_depth)], ['Mean queue wait', metric(p.queue_mean_ms, ' ms')], ['Mean first token', metric(p.ttft_mean_ms, ' ms')], ['Cache hit ratio', percent(p.cache_hit_ratio)], ['Prefill compute throughput', metric(p.prefill_tokens_per_second, ' tok/s')], ['Decode throughput', metric(p.output_tokens_per_second, ' tok/s')]] as [label, value]}
              <div><dt>{label}</dt><dd>{value}</dd></div>
            {/each}
          </dl>
          <p>Queue and first-token means, and cache ratio, cover server lifetime. Throughput is server-wide over {metric(p.rate_window_seconds, ' s')}; the first sample needs a second refresh. This is not per-request prefill speed.</p>
          <p>Sampled {new Date(p.sampled_at).toLocaleTimeString()}</p>
        {:else}<p>{p.message}</p>{/if}
      </section>
    {/each}
  </div>
</div></main>

<style>
  .providers { display: grid; gap: var(--space-5); }
  .provider { padding: var(--space-5); background: var(--bg-raised); border-radius: var(--radius); }
  h2 { font-size: 1rem; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); }
  p, dt { color: var(--text-muted); font-size: .8125rem; line-height: 1.6; }
  dl { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: var(--space-5); margin-block: var(--space-5); }
  dd { margin: var(--space-1) 0 0; font-size: 1.25rem; font-variant-numeric: tabular-nums; }
</style>
