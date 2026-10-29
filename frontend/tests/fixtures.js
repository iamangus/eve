export async function installWorkspace(page, { empty = false, errors = [] } = {}) {
  const now = new Date().toISOString()
  const conversation = {
    id: 'design-chat', title: 'A little structure for the week', total_messages: empty ? 0 : 3, pending_message_ids: [],
    messages: empty ? [] : [
      { id: 1, role: 'user', sender: 'owner', channel: 'web', content: 'I have a busy week ahead. Can you help me make it feel a little more manageable?', created_at: now },
      { id: 2, role: 'assistant', content: 'Absolutely. Let’s make room for what matters, rather than fill every hour.\n\n### Three things to focus on\n\n1. **Protect your mornings.** Set aside one uninterrupted block for your most important work.\n2. **Make the small things smaller.** Put errands and admin into a single afternoon batch.\n3. **Leave a little breathing room.** Keep a few gaps for whatever comes up.\n\nI can work on a first draft of your plan in the background. What is the one thing you’d feel good about finishing this week?', created_at: now },
      { id: 3, role: 'user', sender: 'owner', channel: 'web', content: 'Finishing the project proposal. Let’s start there.', created_at: now },
    ],
  }
  const tasks = empty ? [] : [
    { id: 'proposal', conversation_id: conversation.id, agent_name: 'Project proposal', status: 'needs_input', message: 'Put together a first draft of the project proposal.', question: 'Who will be reading the proposal, and what do you want them to take away?', updated_at: now },
    { id: 'week', conversation_id: conversation.id, agent_name: 'Weekly planning', status: 'running', message: 'Bring together the priorities and small tasks for this week.', updated_at: now },
    { id: 'reading', conversation_id: conversation.id, agent_name: 'Reading notes', status: 'completed', reported: true, message: 'Summarize the articles saved for later.', result: 'Your reading notes are ready. Three themes stood out: clearer priorities, fewer meetings, and space for focused work.', updated_at: now },
  ]
  const accounts = empty ? [] : [{ id: 'inbox', address: 'hello@example.com', host: 'imap.example.com', port: 993, username: 'hello@example.com', use_tls: true, enabled: true, trusted_authserv_id: '' }]
  const identities = [{ name: 'owner', owner: true, channels: [{ type: 'web', address: 'owner' }, { type: 'email', address: 'hello@example.com' }, { type: 'matrix', address: '@you:example.com' }] }, ...(!empty ? [{ name: 'Project team', channels: [{ type: 'email', address: 'team@example.com' }] }] : [])]
  const channels = [
    { id: 'web', type: 'web', name: 'Web chat', input: true, output: true, streams: true, reachable: true, presence: { connected: true, last_activity: now } },
    { id: 'email', type: 'email', name: 'Email', input: true, output: true, reachable: true, default_recipient: 'hello@example.com' },
    { id: 'matrix', type: 'matrix', name: 'Matrix', input: true, output: true, reachable: true, default_recipient: '!your-room:example.com' },
  ]
  const context = {
    enabled: true, budget_tokens: 64000, rendered_tokens: 18450, pressure: .288, bootstrap_preview_available: true, bootstrap_preview_messages: 42, bootstrap_preview_tokens: 18970,
    sources: { compartments: 7200, memories: 1250, raw_tail: 10000 },
    historian: { running: false, last_run_at: now, unsummarized_tokens: 10000, trigger_threshold: 32000, boundary_msg_id: 180 },
    coverage: { total_messages: 224, compartmentalized: 180, raw: 44 },
    memories: empty ? [] : [
      { category: 'USER_PREFERENCES', content: 'Keep mornings open for focused work and batch small tasks in the afternoon.', importance: 85 },
      { category: 'DECISIONS', content: 'The project proposal is the main priority this week.', importance: 90 },
      { category: 'FACTS', content: 'Weekly planning works best with a little space for things that come up.', importance: 70 },
    ],
    compartments: empty ? [] : [{ id: 'summary', start_msg_id: 1, end_msg_id: 180, created_at: now, tier: 'p2', importance: 80, summary: 'Planning conversations have focused on protecting time for important work and simplifying day-to-day administration.', facts: [] }],
  }
  await page.addInitScript(() => {
    window.workspaceStreams = []
    window.EventSource = class extends EventTarget {
      constructor(url) { super(); this.url = url; this.readyState = 1; window.workspaceStreams.push(this) }
      close() { this.readyState = 2 }
    }
  })
  await page.route('**/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (errors.some(path => url.pathname === path)) return route.fulfill({ status: 503, json: { error: 'The server is temporarily unavailable. Your saved data has not changed.' } })
    let result = {}
    if (url.pathname === '/api/conversations') result = [{ id: conversation.id, title: conversation.title }]
    else if (url.pathname === '/api/conversations/primary') result = { id: conversation.id }
    else if (url.pathname === '/api/conversations/' + conversation.id) result = conversation
    else if (url.pathname === '/api/tasks') result = tasks
    else if (url.pathname === '/api/tasks/archive') result = []
    else if (url.pathname === '/api/email/accounts') result = accounts
    else if (url.pathname === '/api/triggers') result = empty ? [] : [{ id: 'digest', account_id: 'inbox', name: 'A clearer weekly roundup', enabled: true, prompt: 'Summarize the important points and next steps.', filters: [{ field: 'subject', contains: 'Weekly roundup' }] }]
    else if (url.pathname === '/api/triggers/runs') result = empty ? [] : [{ id: 'run', trigger_id: 'digest', trigger_name: 'A clearer weekly roundup', status: 'completed', created_at: now, account_address: 'hello@example.com', email: { from: 'team@example.com', subject: 'Weekly roundup' }, result: 'Three priorities and two follow-ups, ready for your next planning session.', prompt: 'Summarize the important points.' }]
    else if (url.pathname === '/api/identities') result = { identities }
    else if (url.pathname === '/api/channels') result = { channels: empty ? channels.slice(0, 1) : channels, health: { email: { last_check: now }, matrix: { last_check: now } }, queues: { outbound: { pending: 0, leased: 0, failed: 0 }, matrix_inbound: { pending: 0, leased: 0, failed: 0 } } }
    else if (url.pathname === '/api/context') result = context
    else if (url.pathname === '/api/inference/providers') result = url.searchParams.has('provider_id') ? { id: 'local', name: 'Local inference', provider_type: 'custom', scope: 'server_aggregate', available: true, queue_depth: 0, queue_mean_ms: 125, ttft_mean_ms: 340, cache_hit_ratio: .8, prefill_tokens_per_second: 1200, output_tokens_per_second: 42, rate_window_seconds: 10, sampled_at: now } : { providers: [{ id: 'local', name: 'Local inference', provider_type: 'custom' }] }
    else if (url.pathname.endsWith('/inference')) result = { attempts: [{ id: 'call', model: 'local-model', provider_id: 'local', status: 'completed', activity_attempt: 1, http_attempt: 1, metrics: { input_tokens: 100, output_tokens: 10, cached_input_tokens: 80, cache_hit_ratio: .8, time_to_first_token_ms: 100, observed_output_tokens_per_second: 42, duration_ms: 350 } }] }
    await route.fulfill({ json: result })
  })
  return { conversation, tasks }
}
