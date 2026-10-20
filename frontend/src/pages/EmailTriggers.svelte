<script>
  import { api } from '../lib/api.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'

  let accounts = $state([])
  let triggers = $state([])
  let runs = $state([])
  let loading = $state(true)
  let pageError = $state('')
  let savingTrigger = $state(false)
  let pending = $state({})

  let showAccountForm = $state(false)
  let editingAccountID = $state('')
  let savingAccount = $state(false)
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
      pageError = ''
    } catch (e) {
      console.error('Failed to load email data', e)
      pageError = e.message
    } finally {
      loading = false
    }
  }

  async function loadRuns() {
    try {
      runs = await api.get('/api/triggers/runs')
    } catch (e) {
      console.error('Failed to load runs', e)
      pageError = 'Run history could not be refreshed. ' + e.message
    }
  }

  function accountName(id) {
    const a = accounts.find((a) => a.id === id)
    return a ? a.address : id
  }

  function openAccountForm() {
    editingAccountID = ''
    showAccountForm = !showAccountForm
    acctError = ''
    if (showAccountForm) {
      acctForm = { address: '', host: '', port: 993, username: '', password: '', use_tls: true, enabled: true, trusted_authserv_id: '' }
    }
  }

  async function saveAccount() {
    if (savingAccount) return
    savingAccount = true
    acctError = ''
    try {
      if (editingAccountID) await api.put('/api/email/accounts/' + editingAccountID, acctForm)
      else await api.post('/api/email/accounts', acctForm)
      showAccountForm = false
      await loadAll()
    } catch (e) {
      acctError = e.message
    } finally { savingAccount = false }
  }

  function editAccount(a) {
    editingAccountID = a.id
    acctForm = { address: a.address, host: a.host, port: a.port, username: a.username, password: '', use_tls: a.use_tls, enabled: a.enabled, trusted_authserv_id: a.trusted_authserv_id || '' }
    acctError = ''
    showAccountForm = true
  }

  async function testAccount(id) {
    if (pending[id]) return
    pending[id] = true
    acctResults = { ...acctResults, [id]: 'testing…' }
    try {
      const res = await api.post('/api/email/accounts/' + id + '/test', {})
      acctResults = { ...acctResults, [id]: res.ok ? 'OK' : 'Failed: ' + res.error }
    } catch (e) {
      acctResults = { ...acctResults, [id]: 'Failed: ' + e.message }
    } finally { pending[id] = false }
  }

  async function deleteAccount(id) {
    if (!confirm('Delete this account and all of its triggers?')) return
    try {
      await api.del('/api/email/accounts/' + id)
      await loadAll()
    } catch (e) {
      console.error('Failed to delete account', e)
      pageError = e.message
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
    if (savingTrigger) return
    savingTrigger = true
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
    } finally { savingTrigger = false }
  }

  async function toggleEnabled(t) {
    try {
      await api.put('/api/triggers/' + t.id, { account_id: t.account_id, name: t.name, prompt: t.prompt, enabled: !t.enabled, filters: t.filters })
      await loadAll()
    } catch (e) {
      console.error('Failed to update trigger', e)
      pageError = e.message
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
      pageError = e.message
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

<main class="page" aria-labelledby="email-title"><div class="page-inner">
  <header class="page-heading"><div><p class="eyebrow">Manage</p><h1 class="page-title" id="email-title">Email</h1><p class="page-description">Turn incoming mail into useful work, with rules you control.</p></div></header>
  {#if loading}
    <EmptyState loading title="Loading your email setup" />
  {:else}
    {#if pageError}<div class="notice error" role="alert"><Icon name="warning" /><span>{pageError}</span><button class="button small" onclick={loadAll}>Try again</button></div>{/if}
    <section class="card">
      {#if acctError && !showAccountForm}<div class="form-error" role="alert">{acctError}</div>{/if}
      <div class="card-head">
        <div><h2>Email accounts <span class="badge">{accounts.length}</span></h2><p class="section-description">Mailboxes Eve checks for new messages.</p></div>
        <button class="button small" onclick={openAccountForm}>{#if !showAccountForm}<Icon name="plus" size={16} />{/if}{showAccountForm ? 'Cancel' : 'Add account'}</button>
      </div>

      {#if showAccountForm}
        <div class="panel">
          <h3>{editingAccountID ? 'Edit email account' : 'Connect an email account'}</h3>
          <div class="form-grid">
            <label>Address
              <input type="email" bind:value={acctForm.address} placeholder="inbox@example.com" />
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
              <input type="password" autocomplete="new-password" bind:value={acctForm.password} placeholder={editingAccountID ? 'Leave blank to keep the existing password' : 'App password'} />
            </label>
            <label class="check">
              <input type="checkbox" bind:checked={acctForm.use_tls} />
              Use implicit TLS (SSL)
            </label>
            <label>Trusted authentication server ID
              <input bind:value={acctForm.trusted_authserv_id} placeholder="Optional: your mail gateway's authserv-id" />
            </label>
            <label class="check"><input type="checkbox" bind:checked={acctForm.enabled} />Enabled</label>
          </div>
          {#if acctError}<div class="form-error">{acctError}</div>{/if}
          <p class="hint">Owner commands also need a trusted mail gateway that verifies sender provenance. Leave the authentication server ID empty if this mailbox is only for automation.</p>
          <button class="button primary" onclick={saveAccount} disabled={savingAccount}>{savingAccount ? 'Saving…' : 'Save account'}</button>
        </div>
      {/if}

      {#if accounts.length === 0}
        <EmptyState icon="email" title="Your inbox, with a little help" description="Connect an email account so Eve can check incoming messages and run the rules you choose." />
      {:else}
        <div class="list">
          {#each accounts as a}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {a.address}
                  <span class="badge" data-tone={a.enabled ? 'success' : undefined}>{a.enabled ? 'Enabled' : 'Disabled'}</span>
                </div>
                <div class="row-sub">{a.host}:{a.port} · {a.username}</div>
                {#if acctResults[a.id]}
                  <div class="row-sub test-result">{acctResults[a.id]}</div>
                {/if}
              </div>
              <div class="row-actions">
                <button class="button small" onclick={() => editAccount(a)}>Edit</button>
                <button class="button small" disabled={pending[a.id]} onclick={() => testAccount(a.id)}>{pending[a.id] ? 'Testing…' : 'Test connection'}</button>
                <button class="button danger small" onclick={() => deleteAccount(a.id)}>Delete</button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <div><h2>Automation rules <span class="badge">{triggers.length}</span></h2><p class="section-description">When a message matches, tell Eve what to do.</p></div>
        <button class="button small" onclick={openNewTrigger} disabled={accounts.length === 0}><Icon name="plus" size={16} />New rule</button>
      </div>

      {#if editorOpen}
        <div class="panel">
          <h3>{editingId ? 'Edit automation rule' : 'New automation rule'}</h3>
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
                <select aria-label={'Filter field ' + (i + 1)} bind:value={f.field}>
                  <option value="sender">sender</option>
                  <option value="recipient">recipient</option>
                  <option value="subject">subject</option>
                  <option value="body">body</option>
                </select>
                <span class="hint">contains</span>
                <input aria-label={'Filter text ' + (i + 1)} bind:value={f.contains} placeholder="Text to match" />
                <button class="button danger small" onclick={() => removeFilter(i)} aria-label="Remove filter"><Icon name="close" size={16} /></button>
              </div>
            {/each}
            <button class="button small" onclick={addFilter}><Icon name="plus" size={16} />Add filter</button>
          </div>

          <div class="field">
            <label class="field-label" for="rule-prompt">Instructions for Eve</label>
            <textarea id="rule-prompt" bind:value={triggerForm.prompt} rows="4" placeholder="Summarize this email and extract the key action items."></textarea>
          </div>

          <label class="check">
            <input type="checkbox" bind:checked={triggerForm.enabled} />
            Enabled
          </label>

          {#if triggerError}<div class="form-error">{triggerError}</div>{/if}
          <div class="form-actions">
            <button class="button primary" disabled={savingTrigger} onclick={saveTrigger}>{savingTrigger ? 'Saving…' : editingId ? 'Save changes' : 'Create rule'}</button>
            <button class="button" onclick={closeEditor}>Cancel</button>
          </div>
        </div>
      {/if}

      {#if triggers.length === 0}
        <EmptyState icon="sparkle" title="Make the routine a little easier" description={accounts.length ? 'Create a rule to summarize mail, collect action items, or follow up on messages that matter.' : 'Connect an account first, then choose which messages should start an automation.'} />
      {:else}
        <div class="list">
          {#each triggers as t}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {t.name}
                  <span class="badge" data-tone={t.enabled ? 'success' : undefined}>{t.enabled ? 'Enabled' : 'Disabled'}</span>
                </div>
                <div class="row-sub">{accountName(t.account_id)} · {filtersSummary(t)}</div>
              </div>
              <div class="row-actions">
                <button class="button small" onclick={() => { testingId = t.id; testOut = null; sample = { from: '', to: '', subject: '', body: '' } }}>Test rule</button>
                <button class="button small" onclick={() => editTrigger(t)}>Edit</button>
                <button class="button small" onclick={() => toggleEnabled(t)}>{t.enabled ? 'Disable' : 'Enable'}</button>
                <button class="button danger small" onclick={() => deleteTrigger(t.id)}>Delete</button>
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
                <div class="form-actions"><button class="button primary" onclick={() => testTrigger(t)}>Run test</button><button class="button" onclick={() => { testingId = null }}>Close test</button></div>
              </div>
            {/if}
          {/each}
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <div><h2>Recent runs <span class="badge">{runs.length}</span></h2><p class="section-description">The work your rules have started.</p></div>
      </div>
      {#if runs.length === 0}
        <EmptyState icon="clock" title="No rules have run yet" description="When an incoming message matches a rule, its progress and result will appear here." />
      {:else}
        <div class="list">
          {#each runs as run}
            <div class="row">
              <div class="row-main">
                <div class="row-title">
                  {run.trigger_name || run.trigger_id}
                  <span class="badge" data-tone={run.status === 'completed' ? 'success' : run.status === 'failed' ? 'error' : 'accent'}>{run.status}</span>
                </div>
                <div class="row-sub">{fmtTime(run.created_at)} · {run.account_address || run.account_id}</div>
                <div class="row-sub">From: {run.email?.from} · Subject: {run.email?.subject}</div>
                <button class="button quiet small" aria-expanded={expandedRun === run.id} onclick={() => (expandedRun = expandedRun === run.id ? null : run.id)}>
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
</div></main>

<style>
  .card { border-top: var(--rule); padding: var(--space-6) 0 var(--space-10); }
  .card-head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); margin-bottom: var(--space-5); }
  .card-head h2 { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-2); margin: 0; font-size: 1rem; font-weight: 600; letter-spacing: -.015em; }
  .section-description { color: var(--text-muted); font-size: .8125rem; margin: var(--space-1) 0 0; }
  .list { border: var(--rule); border-radius: var(--radius); background: var(--bg-raised); }
  .row { display: flex; align-items: flex-start; gap: var(--space-4); padding: var(--space-5); }
  .row + .row { border-top: var(--rule); }
  .row-main { flex: 1; min-width: 0; }
  .row-actions { display: flex; gap: var(--space-2); min-width: 0; max-width: 100%; flex-wrap: wrap; }
  .row-title { font-size: .875rem; font-weight: 600; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); overflow-wrap: anywhere; }
  .row-sub { font-size: .75rem; color: var(--text-muted); margin-top: var(--space-2); overflow-wrap: anywhere; }
  .panel { background: var(--bg-deep); padding: var(--space-6); border: var(--rule); border-radius: var(--radius); margin-bottom: var(--space-6); }
  .panel h3 { font-size: 1rem; font-weight: 550; margin: 0 0 var(--space-5); }
  .panel.sub { margin: 0 var(--space-4) var(--space-4); }
  .form-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-4); margin-bottom: var(--space-5); }
  .form-grid label { display: flex; flex-direction: column; gap: var(--space-2); font-size: .8125rem; font-weight: 500; }
  .field { margin-bottom: var(--space-5); }
  .field-label { display: block; color: var(--text-base); font-size: .8125rem; font-weight: 500; margin-bottom: var(--space-2); }
  .hint { color: var(--text-muted); font-size: .75rem; font-weight: 400; }
  input:not([type='checkbox']), select, textarea { width: 100%; min-height: 40px; padding: var(--space-2) var(--space-3); border: 1px solid var(--border-strong); background: var(--bg-raised); border-radius: var(--radius-sm); color: var(--text-base); font-size: .875rem; font-weight: 400; }
  input:focus, select:focus, textarea:focus { border-color: var(--accent); }
  textarea { resize: vertical; line-height: 1.6; }
  .check, .form-grid label.check { display: flex; flex-direction: row; align-items: center; gap: var(--space-2); min-height: 40px; font-size: .8125rem; }
  .filter-row { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-3); }
  .filter-row select { width: 130px; flex-shrink: 0; }
  .filter-row input { flex: 1; }
  .filter-row .button { flex-shrink: 0; }
  .form-error { color: var(--danger); background: var(--danger-bg); padding: var(--space-3); border-radius: var(--radius-sm); font-size: .8125rem; margin-block: var(--space-3); }
  .form-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-4); }
  .test-result { color: var(--accent-text); }
  .test-out { margin-bottom: var(--space-4); }
  .prompt-preview pre, .run-detail pre { background: var(--bg-deep); border: var(--rule); border-radius: var(--radius-sm); padding: var(--space-4); max-width: 100%; overflow-x: auto; font-family: var(--mono); font-size: .75rem; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
  .run-detail { margin-top: var(--space-4); }
  .run-detail .err { color: var(--danger); background: var(--danger-bg); }
  .row-main > .button { margin-top: var(--space-3); }
  .notice { margin-bottom: var(--space-6); }
  @media (max-width: 1000px) { .row { flex-wrap: wrap; } .row-main { flex-basis: 100%; } }
  @media (max-width: 640px) { .card-head { flex-wrap: wrap; } .form-grid { grid-template-columns: 1fr; } .panel { padding: var(--space-4); } .row { padding: var(--space-4); } .filter-row { flex-wrap: wrap; } .filter-row select { width: 100%; } .filter-row .hint { display: none; } .panel.sub { margin-inline: var(--space-2); } }
</style>
