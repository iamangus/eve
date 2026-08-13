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

  function fmtAddress(c) {
    return `${c.type} · ${c.address}`
  }
</script>

<div class="id-page">
  <div class="id-head">
    <h2 class="id-title">Identities</h2>
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
                <span class="id-chan">{fmtAddress(c)}</span>
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
    <div class="id-modal" role="dialog" onclick={(e) => e.stopPropagation()}>
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
  .id-page { padding: 24px; overflow-y: auto; height: 100%; max-width: 960px; margin: 0 auto; }
  .id-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
  .id-title {
    font-size: 0.75rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-muted); margin: 0;
  }
  .id-empty { color: var(--text-muted); font-size: 0.85rem; padding: 8px 0; }
  .id-error { color: oklch(70% 0.2 20); }
  .id-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 10px; }
  .id-card {
    background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px;
    padding: 14px; font-size: 0.83rem; display: flex; flex-direction: column; gap: 8px;
  }
  .id-card-head { display: flex; align-items: center; gap: 8px; }
  .id-name { font-weight: 700; color: var(--text-base); font-size: 0.9rem; }
  .id-owner-badge {
    font-size: 0.66rem; text-transform: uppercase; letter-spacing: 0.08em;
    color: oklch(80% 0.18 292.0); background: oklch(30% 0.06 292.7);
    border: 1px solid oklch(59.1% 0.249 292.7 / 0.35); border-radius: 10px; padding: 1px 8px;
  }
  .id-chans { display: flex; flex-wrap: wrap; gap: 4px; }
  .id-chan {
    font-size: 0.72rem; border-radius: 6px; padding: 1px 7px;
    background: oklch(28% 0.03 0); color: var(--text-base); border: 1px solid var(--border);
  }
  .id-muted { color: var(--text-muted); font-size: 0.75rem; }
  .id-actions { display: flex; gap: 6px; margin-top: 2px; }
  .id-btn {
    background: var(--bg-card); color: var(--text-base); border: 1px solid var(--border);
    border-radius: 8px; padding: 5px 12px; font-size: 0.78rem; cursor: pointer;
  }
  .id-btn:hover { border-color: var(--accent, oklch(70% 0.2 250)); }
  .id-btn-primary { background: oklch(45% 0.2 292.7); border-color: oklch(45% 0.2 292.7); color: white; }
  .id-btn-danger { color: oklch(70% 0.2 20); border-color: oklch(70% 0.2 20 / 0.4); }
  .id-btn-icon { padding: 0 8px; font-size: 0.9rem; line-height: 1; }
  .id-overlay {
    position: fixed; inset: 0; background: rgb(0 0 0 / 0.55); display: flex;
    align-items: center; justify-content: center; z-index: 40;
  }
  .id-modal {
    background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
    padding: 20px; width: min(480px, 92vw); max-height: 86vh; overflow-y: auto;
    display: flex; flex-direction: column; gap: 10px;
  }
  .id-modal-title { margin: 0 0 4px; font-size: 1rem; }
  .id-field { display: flex; flex-direction: column; gap: 4px; font-size: 0.8rem; color: var(--text-muted); }
  .id-field input[type='text'], .id-chan-row input[type='text'] {
    background: var(--bg-base, #141414); color: var(--text-base);
    border: 1px solid var(--border); border-radius: 8px; padding: 7px 10px; font-size: 0.85rem;
  }
  .id-field input:disabled { opacity: 0.5; }
  .id-check { flex-direction: row; align-items: center; gap: 8px; }
  .id-chans-label { font-size: 0.8rem; color: var(--text-muted); margin-top: 4px; }
  .id-chan-row { display: flex; gap: 6px; align-items: center; }
  .id-chan-row select {
    background: var(--bg-base, #141414); color: var(--text-base);
    border: 1px solid var(--border); border-radius: 8px; padding: 7px 8px; font-size: 0.85rem;
  }
  .id-chan-row input { flex: 1; }
  .id-modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
</style>
