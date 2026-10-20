<script>
  import { api } from '../lib/api.js'
  import { dialog } from '../lib/dialog.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'

  let identities = $state([])
  let loading = $state(true)
  let error = $state('')
  let saving = $state(false)
  let formError = $state('')
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
    formError = ''
    editingName = ''
    form = { name: '', owner: false, channels: [{ type: 'email', address: '' }] }
    editorOpen = true
  }

  function edit(id) {
    formError = ''
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
    if (saving) return
    const body = {
      name: form.name.trim(),
      owner: form.owner,
      channels: form.channels
        .map((c) => ({ type: c.type, address: c.address.trim() }))
        .filter((c) => c.address !== ''),
    }
    formError = ''
    if (!body.name) { formError = 'Give this identity a name before saving.'; return }
    saving = true
    try {
      if (editingName) {
        await api.put(`/api/identities/${encodeURIComponent(editingName)}`, body)
      } else {
        await api.post('/api/identities', body)
      }
      editorOpen = false
      await load()
    } catch (e) {
      formError = e.message
    } finally { saving = false }
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

<main class="page" aria-labelledby="identities-title"><div class="page-inner">
  <header class="page-heading">
    <div><p class="eyebrow">Manage</p><h1 class="page-title" id="identities-title">Identities</h1><p class="page-description">Help Eve recognize you across your connected channels.</p></div>
    <button class="button primary" onclick={openNew}><Icon name="plus" size={16} />Add identity</button>
  </header>

  {#if loading}
    <EmptyState loading title="Loading identities" />
  {:else if error}
    <div class="notice error" role="alert">{error}</div><button class="button retry" onclick={load}>Try again</button>
  {:else if identities.length === 0}
    <EmptyState icon="identities" title="Give familiar faces a name" description="Add an identity and its channel addresses so Eve can recognize who is messaging her."><button class="button" onclick={openNew}>Create your first identity</button></EmptyState>
  {:else}
    <div class="id-list">
      {#each identities as id}
        <div class="id-card">
          <div class="id-avatar" aria-hidden="true">{id.name.slice(0, 1).toUpperCase()}</div>
          <div class="id-details">
          <div class="id-card-head">
            <h2 class="id-name">{id.name}</h2>
            {#if id.owner}
              <span class="badge" data-tone="accent">Owner · you</span>
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
          </div>
          <div class="id-actions">
            <button class="button small" onclick={() => edit(id)}>Edit</button>
            {#if !id.owner}
              <button class="button danger small" onclick={() => remove(id.name)}>Delete</button>
            {/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div></main>

{#if editorOpen}
  <div class="id-overlay" role="presentation" onclick={closeEditor}>
    <div class="id-modal" use:dialog={closeEditor} role="dialog" aria-modal="true" aria-labelledby="identity-dialog-title" tabindex="-1" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
      <div class="section-header"><h2 id="identity-dialog-title" class="id-modal-title">{editingName ? `Edit ${editingName}` : 'New identity'}</h2><button class="icon-button" aria-label="Close identity editor" onclick={closeEditor}><Icon name="close" size={18} /></button></div>
      <p class="editor-description">Link a name to the addresses Eve should recognize.</p>
      <label class="id-field">
        <span>Name</span>
        <input class="input"
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
          <select class="input" aria-label={'Channel type ' + (i + 1)} bind:value={c.type}>
            {#each CHANNEL_TYPES as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
          <input class="input" type="text" aria-label={'Channel address ' + (i + 1)} bind:value={c.address} placeholder="Address or user ID" />
          <button
            class="button danger small"
            onclick={() => removeChannel(i)}
            title="Remove channel"
          >
            ×
          </button>
        </div>
      {/each}
      <button class="button add-channel" onclick={addChannel}><Icon name="plus" size={16} />Add channel</button>
      {#if formError}<div class="notice error" role="alert">{formError}</div>{/if}
      <div class="id-modal-actions">
        <button class="button" onclick={closeEditor}>Cancel</button>
        <button class="button primary" disabled={saving} onclick={save}>{saving ? 'Saving…' : 'Save identity'}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .id-list { border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .id-card { padding: var(--space-6); display: grid; grid-template-columns: 40px minmax(0, 1fr) auto; gap: var(--space-4); align-items: start; }
  .id-card + .id-card { border-top: var(--rule); }
  .id-avatar { display: grid; place-items: center; width: 40px; height: 40px; border-radius: var(--radius); color: var(--accent-text); background: var(--accent-dim); font-size: .875rem; font-weight: 600; }
  .id-card-head { display: flex; flex-wrap: wrap; gap: var(--space-3); align-items: center; margin-bottom: var(--space-3); }
  .id-name { margin: 0; font-weight: 600; font-size: .9375rem; }
  .id-chans { display: flex; flex-direction: column; gap: var(--space-2); }
  .id-chan { display: grid; grid-template-columns: 60px minmax(0, 1fr); gap: var(--space-3); font-size: .8125rem; }
  .id-chan b { color: var(--text-muted); font-weight: 450; text-transform: capitalize; }
  .id-chan span { overflow-wrap: anywhere; }
  .id-muted { color: var(--text-muted); font-size: .8125rem; }
  .id-actions { display: flex; gap: var(--space-2); }
  .id-overlay { position: fixed; inset: 0; background: var(--overlay); display: flex; align-items: center; justify-content: center; z-index: 40; padding: var(--space-4); }
  .id-modal { background: var(--bg-raised); border: var(--rule); border-radius: var(--radius-lg); padding: var(--space-6); width: min(480px, 100%); max-height: 90dvh; overflow-y: auto; display: flex; flex-direction: column; gap: var(--space-4); box-shadow: var(--shadow); }
  .id-modal .section-header { margin-bottom: 0; }
  .id-modal-title { margin: 0; font-size: 1.125rem; font-weight: 600; }
  .editor-description { color: var(--text-muted); font-size: .8125rem; margin: calc(-1 * var(--space-3)) 0 0; }
  .id-field { display: flex; flex-direction: column; gap: var(--space-2); font-size: .875rem; font-weight: 500; }
  .id-check { flex-direction: row; align-items: center; gap: var(--space-2); min-height: 40px; }
  .id-chans-label { font-size: .875rem; font-weight: 500; }
  .id-chan-row { display: grid; grid-template-columns: 100px minmax(0, 1fr) 40px; gap: var(--space-2); align-items: center; }
  .id-chan-row .button { padding-inline: var(--space-2); }
  .id-modal-actions { display: flex; justify-content: flex-end; gap: var(--space-2); margin-top: var(--space-2); padding-top: var(--space-4); border-top: var(--rule); }
  .add-channel { align-self: flex-start; }
  .retry { margin-top: var(--space-4); }
  @media (max-width: 640px) { .id-card { padding: var(--space-4); grid-template-columns: 32px minmax(0, 1fr); gap: var(--space-3); } .id-avatar { width: 32px; height: 32px; } .id-actions { grid-column: 2; } .id-modal { padding: var(--space-4); } .id-chan-row { grid-template-columns: minmax(0, 1fr) 40px; } .id-chan-row select { grid-column: 1 / -1; } }
</style>
