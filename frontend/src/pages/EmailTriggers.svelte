<script>
  import { api } from '../lib/api.js'

  let accounts = $state([])
  let triggers = $state([])
  let runs = $state([])
  let loading = $state(true)

  let showAccountForm = $state(false)
  let acctForm = $state({ address: '', host: '', port: 993, username: '', password: '', use_tls: true })
  let acctError = $state('')
  let acctResults = $state({})

  let editorOpen = $state(false)
  let editingId = $state(null)
  let triggerForm = $state({ account_id: '', name: '', prompt: '', enabled: true, filters: [{ field: 'sender', contains: '' }] })
  let triggerError = $state('')

  let testingId = $state(null)
  let sample = $state({ from: '', to: '', subject: '', body: '' })
  let testOut = $state(null)

  let expandedRun = $state(null)

  $effect(() => {
    loadAll()
    const id = setInterval(loadRuns, 5000)
    return () => clearInterval(id)
  })

  async function loadAll() {
    try {
      const [a, t, r] = await Promise.all([
        api.get('/api/email/accounts'),
        api.get('/api/triggers'),
        api.get('/api/triggers/runs'),
      ])
      accounts = a
      triggers = t
      runs = r
    } catch (e) {
      console.error('Failed to load email data', e)
    } finally {
      loading = false
    }
  }

  async function loadRuns() {
    try {
      runs = await api.get('/api/triggers/runs')
    } catch (e) {
      console.error('Failed to load runs', e)
    }
  }

  function accountName(id) {
    const a = accounts.find((a) => a.id === id)
    return a ? a.address : id
  }

  function openAccountForm() {
    showAccountForm = !showAccountForm
    acctError = ''
    if (showAccountForm) {
      acctForm = { address: '', host: '', port: 993, username: '', password: '', use_tls: true }
    }
  }

  async function saveAccount() {
    acctError = ''
    try {
      await api.post('/api/email/accounts', acctForm)
      showAccountForm = false
      await loadAll()
    } catch (e) {
      acctError = e.message
    }
  }

  async function testAccount(id) {
    acctResults = { ...acctResults, [id]: 'testing…' }
    try {
      const res = await api.post('/api/email/accounts/' + id + '/test', {})
      acctResults = { ...acctResults, [id]: res.ok ? 'OK' : 'Failed: ' + res.error }
    } catch (e) {
      acctResults = { ...acctResults, [id]: 'Failed: ' + e.message }
    }
  }

  async function deleteAccount(id) {
    if (!confirm('Delete this account and all of its triggers?')) return
    try {
      await api.del('/api/email/accounts/' + id)
      await loadAll()
    } catch (e) {
      console.error('Failed to delete account', e)
    }
  }

  function openNewTrigger() {
    editingId = null
    triggerForm = { account_id: accounts[0]?.id || '', name: '', prompt: '', enabled: true, filters: [{ field: 'sender', contains: '' }] }
    triggerError = ''
    testOut = null
    editorOpen = true
  }

  function editTrigger(t) {
    editingId = t.id
    triggerForm = {
      account_id: t.account_id,
      name: t.name,
      prompt: t.prompt,
      enabled: t.enabled,
      filters: (t.filters || []).map((f) => ({ ...f })),
    }
    if (triggerForm.filters.length === 0) triggerForm.filters = [{ field: 'sender', contains: '' }]
    triggerError = ''
    testOut = null
    editorOpen = true
  }

  function closeEditor() {
    editorOpen = false
  }

  function addFilter() {
    triggerForm.filters = [...triggerForm.filters, { field: 'sender', contains: '' }]
  }

  function removeFilter(i) {
    triggerForm.filters = triggerForm.filters.filter((_, idx) => idx !== i)
  }

  async function saveTrigger() {
    triggerError = ''
    const filters = triggerForm.filters
      .map((f) => ({ field: f.field, contains: f.contains }))
      .filter((f) => f.contains.trim() !== '')
    const body = { account_id: triggerForm.account_id, name: triggerForm.name, prompt: triggerForm.prompt, enabled: triggerForm.enabled, filters }
    try {
      if (editingId) {
        await api.put('/api/triggers/' + editingId, body)
      } else {
        await api.post('/api/triggers', body)
      }
      editorOpen = false
      await loadAll()
    } catch (e) {
      triggerError = e.message
    }
  }

  async function toggleEnabled(t) {
    try {
      await api.put('/api/triggers/' + t.id, { account_id: t.account_id, name: t.name, prompt: t.prompt, enabled: !t.enabled, filters: t.filters })
      await loadAll()
    } catch (e) {
      console.error('Failed to update trigger', e)
    }
  }

  async function deleteTrigger(id) {
    if (!confirm('Delete this trigger?')) return
    try {
      await api.del('/api/triggers/' + id)
      if (editingId === id) editorOpen = false
      await loadAll()
    } catch (e) {
      console.error('Failed to delete trigger', e)
    }
  }

  async function testTrigger(t) {
    testingId = t.id
    testOut = null
    try {
      const res = await api.post('/api/triggers/' + t.id + '/test', sample)
      testOut = { triggerId: t.id, ...res }
    } catch (e) {
      testOut = { triggerId: t.id, matched: false, error: e.message }
    }
  }

  function fmtTime(iso) {
    if (!iso) return ''
    const d = new Date(iso)
    if (isNaN(d.getTime())) return ''
    return d.toLocaleString()
  }

  function filtersSummary(t) {
    const fs = t.filters || []
    if (fs.length === 0) return 'any email'
    return fs.map((f) => `${f.field} contains "${f.contains}"`).join(' AND ')
  }
</script>

<div class="et-page">
  {#if loading}
    <div class="et-empty">Loading…</div>
  {:else}
    <header class="et-header">
      <div class="et-kicker">AUTOMATION / INBOUND MAIL</div>
      <h1>Email</h1>
      <p>Accounts, matching rules, and execution history.</p>
    </header>
    <section class="card">
      <div class="card-head">
        <h2>Email accounts</h2>
        <button class="btn" onclick={openAccountForm}>{showAccountForm ? 'Cancel' : '+ Add account'}</button>
      </div>

      {#if showAccountForm}
        <div class="panel">
          <div class="form-grid">
            <label>Address
              <input bind:value={acctForm.address} placeholder="inbox@example.com" />
            </label>
            <label>IMAP host
              <input bind:value={acctForm.host} placeholder="imap.example.com" />
            </label>
            <label>Port
              <input type="number" bind:value={acctForm.port} />
            </label>
            <label>Username
              <input bind:value={acctForm.username} placeholder="inbox@example.com" />
            </label>
            <label>Password / app password
              <input type="password" bind:value={acctForm.password} placeholder="••••••••" />
            </label>
            <label class="check">
              <input type="checkbox" bind:checked={acctForm.use_tls} />
              Use implicit TLS (SSL)
            </label>
          </div>
          {#if acctError}<div class="form-error">{acctError}</div>{/if}
          <button class="btn primary" onclick={saveAccount}>Save account</button>
        </div>
      {/if}

      {#if accounts.length === 0}
        <div class="et-empty">No accounts yet.</div>
      {:else}
        <div class="list">
          {#each accounts as a}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {a.address}
                  <span class="tag" class:off={!a.enabled}>{a.enabled ? 'enabled' : 'disabled'}</span>
                </div>
                <div class="row-sub">{a.host}:{a.port} · {a.username}</div>
                {#if acctResults[a.id]}
                  <div class="row-sub test-result">{acctResults[a.id]}</div>
                {/if}
              </div>
              <div class="row-actions">
                <button class="btn" onclick={() => testAccount(a.id)}>Test</button>
                <button class="btn danger" onclick={() => deleteAccount(a.id)}>Delete</button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Triggers</h2>
        <button class="btn" onclick={openNewTrigger} disabled={accounts.length === 0}>+ New trigger</button>
      </div>

      {#if editorOpen}
        <div class="panel">
          <div class="form-grid">
            <label>Account
              <select bind:value={triggerForm.account_id}>
                {#each accounts as a}
                  <option value={a.id}>{a.address}</option>
                {/each}
              </select>
            </label>
            <label>Name
              <input bind:value={triggerForm.name} placeholder="Invoice follow-up" />
            </label>
          </div>

          <div class="field">
            <div class="field-label">Filters <span class="hint">all must match</span></div>
            {#each triggerForm.filters as f, i}
              <div class="filter-row">
                <select bind:value={f.field}>
                  <option value="sender">sender</option>
                  <option value="recipient">recipient</option>
                  <option value="subject">subject</option>
                  <option value="body">body</option>
                </select>
                <span class="hint">contains</span>
                <input bind:value={f.contains} placeholder="text to match" />
                <button class="btn icon danger" onclick={() => removeFilter(i)} aria-label="Remove filter">✕</button>
              </div>
            {/each}
            <button class="btn" onclick={addFilter}>+ Add filter</button>
          </div>

          <div class="field">
            <div class="field-label">Agent prompt</div>
            <textarea bind:value={triggerForm.prompt} rows="4" placeholder="Summarize this email and extract the key action items."></textarea>
          </div>

          <label class="check">
            <input type="checkbox" bind:checked={triggerForm.enabled} />
            Enabled
          </label>

          {#if triggerError}<div class="form-error">{triggerError}</div>{/if}
          <div class="form-actions">
            <button class="btn primary" onclick={saveTrigger}>{editingId ? 'Save changes' : 'Create trigger'}</button>
            <button class="btn" onclick={closeEditor}>Cancel</button>
          </div>
        </div>
      {/if}

      {#if triggers.length === 0}
        <div class="et-empty">No triggers yet.</div>
      {:else}
        <div class="list">
          {#each triggers as t}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {t.name}
                  <span class="tag" class:off={!t.enabled}>{t.enabled ? 'enabled' : 'disabled'}</span>
                </div>
                <div class="row-sub">{accountName(t.account_id)} · {filtersSummary(t)}</div>
              </div>
              <div class="row-actions">
                <button class="btn" onclick={() => { testingId = t.id; testOut = null; sample = { from: '', to: '', subject: '', body: '' } }}>Test</button>
                <button class="btn" onclick={() => editTrigger(t)}>Edit</button>
                <button class="btn" onclick={() => toggleEnabled(t)}>{t.enabled ? 'Disable' : 'Enable'}</button>
                <button class="btn danger" onclick={() => deleteTrigger(t.id)}>Delete</button>
              </div>
            </div>

            {#if testingId === t.id}
              <div class="panel sub">
                <div class="form-grid">
                  <label>From
                    <input bind:value={sample.from} placeholder="sender@example.com" />
                  </label>
                  <label>To
                    <input bind:value={sample.to} placeholder="inbox@example.com" />
                  </label>
                  <label>Subject
                    <input bind:value={sample.subject} placeholder="Subject line" />
                  </label>
                  <label>Body
                    <textarea bind:value={sample.body} rows="3" placeholder="Message body…"></textarea>
                  </label>
                </div>
                {#if testOut && testOut.triggerId === t.id}
                  <div class="test-out">
                    {#if testOut.error}
                      <div class="form-error">{testOut.error}</div>
                    {:else}
                      <div class="row-sub">{testOut.matched ? '✓ Matches' : '✕ Does not match'}</div>
                      {#if testOut.matched_filters && testOut.matched_filters.length > 0}
                        <div class="row-sub">Matched: {testOut.matched_filters.join(' · ')}</div>
                      {/if}
                      {#if testOut.matched}
                        <div class="prompt-preview">
                          <div class="field-label">Prompt that would be sent</div>
                          <pre>{testOut.prompt}</pre>
                        </div>
                      {/if}
                    {/if}
                  </div>
                {/if}
                <button class="btn primary" onclick={() => testTrigger(t)}>Run test</button>
              </div>
            {/if}
          {/each}
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Runs</h2>
      </div>
      {#if runs.length === 0}
        <div class="et-empty">No runs yet.</div>
      {:else}
        <div class="list">
          {#each runs as run}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {run.trigger_name || run.trigger_id}
                  <span class="tag" class:ok={run.status === 'completed'} class:fail={run.status === 'failed'}>{run.status}</span>
                </div>
                <div class="row-sub">{fmtTime(run.created_at)} · {run.account_address || run.account_id}</div>
                <div class="row-sub">From: {run.email?.from} · Subject: {run.email?.subject}</div>
                <button class="btn" onclick={() => (expandedRun = expandedRun === run.id ? null : run.id)}>
                  {expandedRun === run.id ? 'Hide details' : 'Show details'}
                </button>
                {#if expandedRun === run.id}
                  <div class="run-detail">
                    {#if run.result}
                      <div class="field-label">Agent result</div>
                      <pre>{run.result}</pre>
                    {/if}
                    {#if run.error}
                      <div class="field-label">Error</div>
                      <pre class="err">{run.error}</pre>
                    {/if}
                    <div class="field-label">Prompt</div>
                    <pre>{run.prompt}</pre>
                  </div>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .et-page {
    flex: 1;
    overflow-y: auto;
    padding: 38px var(--page-pad) 64px;
    display: flex;
    flex-direction: column;
    gap: 0;
    background: var(--bg-base);
  }
  .et-header { max-width: var(--content-max); padding-bottom: 28px; }
  .et-kicker { font: 0.65rem var(--mono); letter-spacing: 0.1em; color: var(--text-faint); }
  .et-header h1 { font-size: 1.65rem; line-height: 1.2; font-weight: 500; letter-spacing: -0.02em; margin: 3px 0 5px; }
  .et-header p { color: var(--text-muted); margin: 0; font-size: 0.88rem; }
  .card {
    max-width: var(--content-max);
    border-top: 1px solid var(--border-strong);
    padding: 26px 0 38px;
  }
  .card-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 18px;
  }
  .card-head h2 {
    margin: 0;
    font: 700 0.68rem var(--mono);
    text-transform: uppercase;
    letter-spacing: 0.09em;
    color: var(--text-muted);
  }
  .et-empty {
    color: var(--text-muted);
    font-size: 0.85rem;
    padding: 8px 0;
  }
  .list {
    display: flex;
    flex-direction: column;
    border-top: var(--rule);
  }
  .row {
    border-bottom: var(--rule);
    padding: 16px 0;
    display: flex;
    align-items: flex-start;
    gap: 12px;
  }
  .row-main { flex: 1; min-width: 0; }
  .row-actions {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }
  .row-title {
    font-size: 0.88rem;
    font-weight: 600;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .row-sub {
    font: 0.72rem var(--mono);
    color: var(--text-muted);
    margin-top: 3px;
    word-break: break-word;
  }
  .tag {
    font: 600 0.64rem var(--mono);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-base);
    padding: 2px 0;
  }
  .tag.off { color: var(--text-faint); }
  .tag.ok { color: var(--text-base); }
  .tag.fail { color: var(--text-base); text-decoration: underline; text-decoration-thickness: 2px; }

  .panel {
    border: 1px solid var(--border-strong);
    border-radius: 2px;
    padding: 18px;
    margin-bottom: 20px;
    background: var(--bg-deep);
  }
  .panel.sub { margin-bottom: 0; }
  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 12px;
    margin-bottom: 12px;
  }
  .form-grid label {
    display: flex;
    flex-direction: column;
    gap: 5px;
    font: 0.7rem var(--mono);
    color: var(--text-muted);
  }
  .field { margin-bottom: 12px; }
  .field-label {
    font: 0.7rem var(--mono);
    color: var(--text-muted);
    margin-bottom: 6px;
  }
  .hint {
    font-size: 0.7rem;
    color: var(--text-muted);
    font-weight: 400;
  }
  input, select, textarea {
    background: var(--bg-base); border: 1px solid var(--border-strong); border-radius: 1px;
    color: var(--text-base);
    font-family: inherit;
    font-size: 0.85rem;
    padding: 7px 9px;
    width: 100%;
    outline: none;
  }
  input:focus, select:focus, textarea:focus {
    border-color: var(--accent);
  }
  textarea { resize: vertical; line-height: 1.5; }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.82rem;
    color: var(--text-base);
    margin-bottom: 12px;
  }
  .check input { width: auto; }
  .filter-row {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-bottom: 8px;
  }
  .filter-row select { width: 130px; flex-shrink: 0; }
  .filter-row input { flex: 1; }
  .filter-row .btn { flex-shrink: 0; }
  .form-error {
    color: var(--text-base); border-left: 2px solid var(--text-base); padding-left: 10px;
    font-size: 0.8rem;
    margin: 8px 0;
  }
  .form-actions {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .test-result { color: var(--text-base); }
  .test-out { margin-bottom: 12px; }
  .prompt-preview pre, .run-detail pre {
    background: var(--bg-deep);
    border: 1px solid var(--border);
    border-radius: 1px;
    padding: 12px;
    overflow-x: auto;
    font-size: 0.78rem;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .run-detail { margin-top: 10px; }
  .run-detail .err { color: var(--text-base); border-left: 2px solid var(--text-base); }
  .btn {
    background: transparent; border: 1px solid var(--border-strong); color: var(--accent); border-radius: 1px;
    padding: 6px 11px;
    font-family: inherit;
    font-size: 0.8rem;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s;
    white-space: nowrap;
  }
  .btn:hover { background: var(--accent-dim); border-color: var(--accent); }
  .btn:disabled { opacity: 0.5; pointer-events: none; }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: var(--bg-deep); }
  .btn.primary:hover { background: var(--accent-strong); }
  .btn.danger { color: var(--text-muted); }
  .btn.danger:hover { color: var(--accent); }
  .btn.icon { padding: 6px 9px; }
  @media (max-width: 700px) {
    .row { display: block; }
    .row-actions { margin-top: 14px; flex-wrap: wrap; }
    .filter-row { flex-wrap: wrap; }
    .filter-row select { width: 100%; }
  }
</style>
