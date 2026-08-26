<script>
  import { api } from '../lib/api.js'

  let identities = $state([])
  let loading = $state(true)
  let error = $state('')
  let editorOpen = $state(false)
  let editingName = $state('')
  let form = $state({ name: '', owner: false, channels: [{ type: 'email', address: '' }] })

  const CHANNEL_TYPES = ['web', 'email', 'matrix', 'sms', 'voice']

  $effect(() => {
    load()
    const iv = setInterval(load, 5000)
    return () => clearInterval(iv)
  })

  async function load() {
    try {
      const d = await api.get('/api/identities')
      identities = d.identities || []
      error = ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function openNew() {
    editingName = ''
    form = { name: '', owner: false, channels: [{ type: 'email', address: '' }] }
    editorOpen = true
  }

  function edit(id) {
    editingName = id.name
    form = {
      name: id.name,
      owner: !!id.owner,
      channels: (id.channels || []).map((c) => ({ type: c.type, address: c.address })),
    }
    if (form.channels.length === 0) form.channels = [{ type: 'email', address: '' }]
    editorOpen = true
  }

  function closeEditor() {
    editorOpen = false
  }

  function addChannel() {
    form.channels.push({ type: 'email', address: '' })
  }

  function removeChannel(i) {
    form.channels = form.channels.filter((_, idx) => idx !== i)
  }

  async function save() {
    const body = {
      name: form.name.trim(),
      owner: form.owner,
      channels: form.channels
        .map((c) => ({ type: c.type, address: c.address.trim() }))
        .filter((c) => c.address !== ''),
    }
    if (!body.name) return
    try {
      if (editingName) {
        await api.put(`/api/identities/${encodeURIComponent(editingName)}`, body)
      } else {
        await api.post('/api/identities', body)
      }
      editorOpen = false
      await load()
    } catch (e) {
      error = e.message
    }
  }

  async function remove(name) {
    if (!confirm(`Delete identity "${name}"?`)) return
    try {
      await api.del(`/api/identities/${encodeURIComponent(name)}`)
      await load()
    } catch (e) {
      error = e.message
    }
  }
</script>

<div class="id-page">
  <div class="id-head">
    <div><div class="id-kicker">SYSTEM / ADDRESS BOOK</div><h2 class="id-title">Identities</h2></div>
    <button class="id-btn id-btn-primary" onclick={openNew}>Add identity</button>
  </div>

  {#if loading}
    <div class="id-empty">Loading identities…</div>
  {:else if error}
    <div class="id-empty id-error">{error}</div>
  {:else if identities.length === 0}
    <div class="id-empty">No identities configured.</div>
  {:else}
    <div class="id-list">
      {#each identities as id}
        <div class="id-card">
          <div class="id-card-head">
            <span class="id-name">{id.name}</span>
            {#if id.owner}
              <span class="id-owner-badge">owner</span>
            {/if}
          </div>
          <div class="id-chans">
            {#if (id.channels || []).length === 0}
              <span class="id-muted">no channels</span>
            {:else}
              {#each id.channels as c}
                <span class="id-chan"><b>{c.type}</b><span>{c.address}</span></span>
              {/each}
            {/if}
          </div>
          <div class="id-actions">
            <button class="id-btn" onclick={() => edit(id)}>Edit</button>
            {#if !id.owner}
              <button class="id-btn id-btn-danger" onclick={() => remove(id.name)}>Delete</button>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if editorOpen}
  <div class="id-overlay" role="presentation" onclick={closeEditor}>
    <div class="id-modal" role="dialog" aria-modal="true" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <h3 class="id-modal-title">{editingName ? `Edit ${editingName}` : 'New identity'}</h3>
      <label class="id-field">
        <span>Name</span>
        <input
          type="text"
          bind:value={form.name}
          disabled={!!editingName}
          placeholder="e.g. alex, eve, teammate"
        />
      </label>
      <label class="id-field id-check">
        <input type="checkbox" bind:checked={form.owner} />
        <span>Owner identity (you)</span>
      </label>
      <div class="id-chans-label">Channels</div>
      {#each form.channels as c, i}
        <div class="id-chan-row">
          <select bind:value={c.type}>
            {#each CHANNEL_TYPES as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
          <input type="text" bind:value={c.address} placeholder="address / id" />
          <button
            class="id-btn id-btn-icon"
            onclick={() => removeChannel(i)}
            title="Remove channel"
          >
            ×
          </button>
        </div>
      {/each}
      <button class="id-btn" onclick={addChannel}>+ Add channel</button>
      <div class="id-modal-actions">
        <button class="id-btn" onclick={closeEditor}>Cancel</button>
        <button class="id-btn id-btn-primary" onclick={save}>Save</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .id-page { padding: 38px var(--page-pad) 64px; overflow-y: auto; height: 100%; width: 100%; }
  .id-head { display: flex; align-items: end; justify-content: space-between; padding-bottom: 24px; max-width: var(--content-max); }
  .id-kicker { color: var(--text-faint); font: 0.65rem var(--mono); letter-spacing: 0.1em; }
  .id-title {
    font-size: 1.65rem; font-weight: 500; letter-spacing: -0.02em; color: var(--text-base); margin: 3px 0 0;
  }
  .id-empty { color: var(--text-muted); font-size: 0.85rem; padding: 24px 0; border-top: var(--rule); max-width: var(--content-max); }
  .id-error { color: var(--text-base); }
  .id-list { max-width: var(--content-max); border-top: 1px solid var(--border-strong); }
  .id-card {
    border-bottom: var(--rule); padding: 18px 0; font-size: 0.83rem;
    display: grid; grid-template-columns: 180px minmax(0, 1fr) auto; gap: 24px; align-items: start;
  }
  .id-card-head { display: flex; flex-direction: column; align-items: flex-start; gap: 3px; }
  .id-name { font-weight: 650; color: var(--text-base); font-size: 0.94rem; }
  .id-owner-badge {
    font: 0.63rem var(--mono); text-transform: uppercase; letter-spacing: 0.08em; color: var(--text-muted);
  }
  .id-chans { display: flex; flex-direction: column; gap: 5px; }
  .id-chan {
    display: grid; grid-template-columns: 70px minmax(0, 1fr); gap: 10px; font: 0.72rem var(--mono); color: var(--text-base);
  }
  .id-chan b { color: var(--text-muted); font-weight: 400; text-transform: uppercase; }
  .id-chan span { overflow-wrap: anywhere; }
  .id-muted { color: var(--text-muted); font-size: 0.75rem; }
  .id-actions { display: flex; gap: 6px; }
  .id-btn {
    background: transparent; color: var(--accent); border: 1px solid var(--border-strong);
    border-radius: 1px; padding: 6px 12px; font-size: 0.78rem; cursor: pointer;
  }
  .id-btn:hover { border-color: var(--accent); background: var(--accent-dim); }
  .id-btn-primary { background: var(--accent); border-color: var(--accent); color: var(--bg-deep); }
  .id-btn-primary:hover { background: var(--accent-strong); }
  .id-btn-danger { color: var(--text-muted); }
  .id-btn-icon { padding: 0 8px; font-size: 0.9rem; line-height: 1; }
  .id-overlay {
    position: fixed; inset: 0; background: oklch(7% 0.01 55 / 0.78); display: flex;
    align-items: center; justify-content: center; z-index: 40;
  }
  .id-modal {
    background: var(--bg-raised); border: 1px solid var(--border-strong); border-radius: 2px;
    padding: 20px; width: min(480px, 92vw); max-height: 86vh; overflow-y: auto;
    display: flex; flex-direction: column; gap: 10px;
  }
  .id-modal-title { margin: 0 0 4px; font-size: 1rem; }
  .id-field { display: flex; flex-direction: column; gap: 4px; font-size: 0.8rem; color: var(--text-muted); }
  .id-field input[type='text'], .id-chan-row input[type='text'] {
    background: var(--bg-base, #141414); color: var(--text-base);
    border: 1px solid var(--border-strong); border-radius: 1px; padding: 7px 10px; font-size: 0.85rem;
  }
  .id-field input:disabled { opacity: 0.5; }
  .id-check { flex-direction: row; align-items: center; gap: 8px; }
  .id-chans-label { font-size: 0.8rem; color: var(--text-muted); margin-top: 4px; }
  .id-chan-row { display: flex; gap: 6px; align-items: center; }
  .id-chan-row select {
    background: var(--bg-base, #141414); color: var(--text-base);
    border: 1px solid var(--border-strong); border-radius: 1px; padding: 7px 8px; font-size: 0.85rem;
  }
  .id-chan-row input { flex: 1; }
  .id-modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
  @media (max-width: 680px) {
    .id-card { grid-template-columns: 1fr; gap: 14px; }
    .id-card-head { flex-direction: row; align-items: baseline; }
    .id-actions { justify-content: flex-start; }
  }
</style>
