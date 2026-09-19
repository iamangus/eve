<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { mergeMessages, isCurrentHeal } from '../lib/chatMessageState.js'

  marked.setOptions({ gfm: true, breaks: true })

  let currentConv = $state(null)
  let messages = $state([])
  let newMessage = $state('')
  let loading = $state(true)
  let sending = $state(false)
  let notice = $state('')
  let messageListEl = $state(null)
  let inputEl = $state(null)

  let stream = $state({ runId: '', status: '', raw: '', html: '' })
  let eventSource = null
  let globalEvents = null
  let heartbeatTimer = null
  let tasksTimer = null
  let tasks = $state([])
  let taskReplies = $state({})
  let tasksOpen = $state(false)

  // Generations guard every asynchronous callback against stale application:
  // selectionSeq advances whenever a conversation is (re)selected,
  // conversationEpoch advances whenever the active conversation resets,
  // sendSeq advances per send, healSeq per heal, metaSeq per meta refresh,
  // and activeRun names the run the visible stream belongs to.
  let selectionSeq = 0
  let conversationEpoch = 0
  let sendSeq = 0
  let healSeq = 0
  let metaSeq = 0
  let activeRun = ''

  // appendMessage merges one entry into the visible list through the
  // reconciliation helper so prior entries keep their identity and can never
  // be dropped or visually replaced by a keyed-list reuse.
  function appendMessage(message) {
    messages = mergeMessages(messages, [message])
  }

  onMount(() => {
    loadPrimary()
    openGlobalEvents()
    startHeartbeat()
    loadTasks()
    tasksTimer = setInterval(loadTasks, 5000)
    window.addEventListener('popstate', handlePopState)
    return () => {
      eventSource?.close()
      eventSource = null
      globalEvents?.close()
      globalEvents = null
      if (heartbeatTimer) clearInterval(heartbeatTimer)
      heartbeatTimer = null
      if (tasksTimer) clearInterval(tasksTimer)
      tasksTimer = null
      window.removeEventListener('popstate', handlePopState)
    }
  })

  // openGlobalEvents subscribes to the IO hub: proactive messages Eve sends
  // outside a turn (via send_message) arrive here and are appended live.
  function openGlobalEvents() {
    const es = new EventSource('/api/events')
    globalEvents = es
    es.addEventListener('message', (e) => {
      let ev
      try {
        ev = JSON.parse(e.data)
      } catch {
        return
      }
      if (ev.type !== 'message' || !ev.conv_id || !currentConv) return
      if (ev.conv_id !== currentConv.id) return
      const msg = ev.data
      if (msg === null || msg === undefined) return
      // A delayed proactive event from an older run must not land after a
      // newer run has taken over the conversation; skip it when the hub
      // identifies its run and that run is no longer active. The server also
      // broadcasts the same entry on a meta refresh, so reconciliation (not a
      // local array check) handles deduplication.
      const evRun = ev.run_id || ev.runId || ''
      if (evRun && activeRun && evRun !== activeRun) return
      appendMessage(msg)
      requestAnimationFrame(() => scrollDown())
    })
    es.onerror = () => {
      es.close()
      globalEvents = null
      setTimeout(openGlobalEvents, 3000)
    }
  }

  // startHeartbeat marks the web channel present so the router knows the user
  // is at the computer when deciding where to deliver proactive messages.
  function startHeartbeat() {
    const beat = () => {
      fetch('/api/presence', { method: 'POST' }).catch(() => {})
    }
    beat()
    heartbeatTimer = setInterval(beat, 30000)
  }

  async function loadTasks() {
    try {
      const list = await api.get('/api/tasks')
      tasks = Array.isArray(list) ? list.filter(t => t.status === 'running' || t.status === 'needs_input') : []
      if (tasks.length > 0) tasksOpen = true
    } catch {
      // tasks endpoint unavailable; ignore
    }
  }

  async function replyToTask(id) {
    const content = (taskReplies[id] || '').trim()
    if (!content) return
    await api.post('/api/tasks/' + id + '/reply', { content })
    taskReplies[id] = ''
    loadTasks()
  }

  async function cancelTask(id) {
    await api.post('/api/tasks/' + id + '/cancel', {})
    loadTasks()
  }

  function sanitize(html) {
    return DOMPurify.sanitize(html)
  }

  function renderMarkdown(raw) {
    return sanitize(marked.parse(raw || ''))
  }

  async function loadPrimary() {
    // Claim the selection for the initial load; a navigation that starts a
    // newer selection in the meantime must win, so every continuation is
    // re-checked against the live generation.
    const loadSelection = ++selectionSeq
    try {
      const convs = await api.get('/api/conversations')
      if (loadSelection !== selectionSeq) return

      const url = new URL(window.location.href)
      const convId = url.searchParams.get('conv')
      let conv = null
      if (convId) {
        conv = convs.find(s => s.id === convId)
      }
      if (!conv && convs.length > 0) {
        conv = convs[0]
      }
      if (!conv) {
        conv = await api.post('/api/conversations', {})
        pushUrl('/?conv=' + conv.id)
      }
      if (loadSelection !== selectionSeq) return
      await selectConversation(conv, loadSelection)
    } catch (e) {
      console.error('Failed to load conversation', e)
    } finally {
      loading = false
    }
  }

  async function handlePopState() {
    const convId = new URL(window.location.href).searchParams.get('conv')
    if (convId && convId !== currentConv?.id) {
      await selectConversationById(convId)
    }
  }

  // beginSelection claims the newest selection generation and invalidates
  // every outstanding conversation-scoped asynchronous operation (pending
  // sends, heals, and metadata refreshes) so none of them can apply after the
  // new selection has started.
  function beginSelection() {
    selectionSeq += 1
    conversationEpoch += 1
    sendSeq += 1
    healSeq += 1
    metaSeq += 1
  }

  async function selectConversationById(id) {
    beginSelection()
    const selection = selectionSeq
    try {
      const full = await api.get('/api/conversations/' + id)
      if (selection === selectionSeq) applyConversation(full)
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }

  async function selectConversation(conv, expectedSelection = null) {
    // When expectedSelection is provided (initial load), the caller has
    // already claimed the generation; only continue while it is still the
    // newest one so a later navigation cannot be superseded.
    if (expectedSelection === null) {
      beginSelection()
    } else if (expectedSelection !== selectionSeq) {
      return
    }
    const selection = selectionSeq
    try {
      const full = await api.get('/api/conversations/' + conv.id)
      if (selection === selectionSeq) applyConversation(full)
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }

  function applyConversation(full) {
    // Reset all conversation-scoped async state: close any live stream, drop
    // stale generations, and rebuild the visible list from the authoritative
    // server snapshot.
    conversationEpoch += 1
    sendSeq += 1
    healSeq += 1
    metaSeq += 1
    eventSource?.close()
    eventSource = null
    activeRun = ''
    currentConv = full
    messages = mergeMessages([], full.messages || [])
    sending = false
    stream = { runId: '', status: '', raw: '', html: '' }
    pushUrl('/?conv=' + full.id)
    requestAnimationFrame(() => scrollDown())
    if (full.active_run_id) {
      startStream(full.active_run_id, full.id)
    }
  }

  async function sendMessage() {
    if (sending || !newMessage.trim() || !currentConv) return
    const convId = currentConv.id
    // The send is scoped to this conversation's selection epoch and its own
    // send generation; any selection, newer send, or conversation reset makes
    // the in-flight POST stale so it can never start or mutate a newer run.
    const send = ++sendSeq
    const sendEpoch = conversationEpoch
    const content = newMessage
    newMessage = ''
    if (inputEl) {
      inputEl.value = ''
      inputEl.style.height = 'auto'
    }
    sending = true
    notice = ''
    appendMessage({ role: 'user', content })
    requestAnimationFrame(() => scrollDown())

    try {
      const result = await api.post('/api/conversations/' + convId + '/messages', { content })
      if (send === sendSeq && sendEpoch === conversationEpoch && currentConv?.id === convId) {
        startStream(result.run_id, convId, send)
      }
    } catch (e) {
      if (send !== sendSeq || sendEpoch !== conversationEpoch || currentConv?.id !== convId) return
      sending = false
      console.error('Failed to send message', e)
      appendMessage({ role: 'assistant', content: '⚠️ Failed to send message. (' + e.message + ')' })
      scrollDown()
    }
  }

  function startStream(runId, convId = currentConv?.id, sendGeneration = sendSeq) {
    if (!runId || !convId) return
    // A stream may only start while its send generation is still current and
    // the visible conversation still matches; stale sends and streams left
    // over from a previous selection are rejected outright.
    if (sendGeneration !== sendSeq || currentConv?.id !== convId) return
    eventSource?.close()
    activeRun = runId

    const es = new EventSource('/runs/' + runId + '/events')
    eventSource = es
    stream = { runId, status: 'Thinking', raw: '', html: '' }

    // current() is the single ownership check for every stream callback: the
    // run must still be the active one, this EventSource instance must still
    // be the attached one, and the visible conversation must still match.
    const current = () => activeRun === runId && eventSource === es && currentConv?.id === convId
    const clear = () => {
      if (!current()) return false
      eventSource = null
      activeRun = ''
      stream = { runId: '', status: '', raw: '', html: '' }
      return true
    }

    es.addEventListener('token', (e) => {
      if (!current()) return
      stream.status = ''
      stream.raw += e.data
      stream.html = renderMarkdown(stream.raw)
      scrollDown()
    })

    es.addEventListener('status', (e) => {
      if (current()) stream.status = e.data
    })

    es.addEventListener('done', (e) => {
      if (!current()) return
      es.close()
      if (e.data) appendMessage({ role: 'assistant', content: e.data })
      clear()
      sending = false
      scrollDown()
      refreshConversationMeta(convId)
    })

    es.addEventListener('error', (e) => {
      if (!e.data || !current()) return
      es.close()
      if (e.data) appendMessage({ role: 'assistant', content: e.data })
      clear()
      sending = false
      scrollDown()
      refreshConversationMeta(convId)
    })

    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED || !current()) return
      es.close()
      const partial = stream.raw
      if (clear() && partial && partial.trim()) {
        appendMessage({ role: 'assistant', content: partial })
        scrollDown()
      }
      sending = false
      healConversation(convId, runId)
    }
  }

  async function healConversation(convId = currentConv?.id, runId = '') {
    if (!convId) return
    // The heal is scoped to its own generation plus the conversation's
    // selection epoch, send generation, and conversation id. Any newer
    // selection, run, or send invalidates it — including a newer run that
    // has already completed by the time this heal response returns — so a
    // stale heal can never overwrite newer conversation state.
    const heal = ++healSeq
    const healEpoch = conversationEpoch
    const healSend = sendSeq
    const deadline = Date.now() + 20000
    const currentHeal = () => isCurrentHeal({
      heal,
      currentHeal: healSeq,
      epoch: healEpoch,
      currentEpoch: conversationEpoch,
      send: healSend,
      currentSend: sendSeq,
      convId,
      currentConvId: currentConv?.id,
    }) && (activeRun === '' || activeRun === runId)
    while (Date.now() < deadline) {
      await sleep(500)
      if (!currentHeal()) return
      let full
      try {
        full = await api.get('/api/conversations/' + convId)
      } catch {
        continue
      }
      if (!currentHeal()) return
      currentConv = full
      if (full.active_run_id) continue
      // Merge rather than replace: locally visible optimistic/streamed
      // entries survive and the persisted snapshot deduplicates by identity.
      messages = mergeMessages(messages, full.messages || [])
      requestAnimationFrame(() => scrollDown())
      return
    }
    if (currentHeal()) {
      notice = 'The connection to the agent was lost. The response may still be processing — try refreshing.'
    }
  }

  function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms))
  }

  async function refreshConversationMeta(convId = currentConv?.id) {
    if (!convId) return
    // Metadata refreshes are conversation- and generation-scoped: a refresh
    // started before a newer selection, run, or send can never overwrite the
    // newer state when its response finally arrives.
    const meta = ++metaSeq
    const selection = selectionSeq
    const epoch = conversationEpoch
    const send = sendSeq
    try {
      const full = await api.get('/api/conversations/' + convId)
      if (meta !== metaSeq || selection !== selectionSeq || epoch !== conversationEpoch ||
          send !== sendSeq || currentConv?.id !== convId) {
        return
      }
      currentConv = { ...currentConv, ...full }
      messages = mergeMessages(messages, full.messages || [])
    } catch {}
  }

  function summarizedBoundary() {
    return currentConv?.summarized_up_to || 0
  }

  function gapBetween(prev, next) {
    if (!prev || !next) return ''
    const d = new Date(next.created_at).getTime() - new Date(prev.created_at).getTime()
    if (d < 5 * 60 * 1000) return ''
    const mins = Math.floor(d / 60000)
    const h = Math.floor(mins / 60)
    const m = mins % 60
    const days = Math.floor(h / 24)
    if (days > 0) return '+' + days + 'd ' + (h % 24) + 'h'
    if (h > 0) return '+' + h + 'h' + (m > 0 ? ' ' + m + 'm' : '')
    return '+' + m + 'm'
  }

  function channelLabel(ch) {
    if (!ch || ch === 'web') return ''
    const names = { matrix: 'Matrix', email: 'Email', sms: 'SMS', voice: 'Voice' }
    return names[ch] || ch
  }

  function messageTime(t) {
    if (!t) return ''
    return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  function scrollDown() {
    if (!messageListEl) return
    messageListEl.scrollTop = messageListEl.scrollHeight
  }

  function pushUrl(href) {
    if (window.location.pathname + window.location.search === href) {
      window.history.replaceState({}, '', href)
    } else {
      window.history.pushState({}, '', href)
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault()
      sendMessage()
    }
  }
</script>

<main class="chat">
  {#if loading}
    <div class="empty-state">
      <p class="empty-title">Loading…</p>
    </div>
  {:else if currentConv}
    <div class="chat-layout">
      <div class="chat-head">
        <div>
          <span class="chat-kicker">CONVERSATION</span>
          <span class="chat-head-name">{currentConv.title}</span>
        </div>
        <span class="chat-head-badge">{messages.length} {messages.length === 1 ? 'message' : 'messages'}</span>
        {#if summarizedBoundary() > 0}
          <span class="chat-head-badge summary-badge">summarized</span>
        {/if}
        {#if tasks.length > 0}
          <button class="tasks-toggle" onclick={() => { tasksOpen = !tasksOpen }}>
            View {tasks.length} active {tasks.length === 1 ? 'task' : 'tasks'}
          </button>
        {/if}
      </div>

      {#if tasksOpen && tasks.length > 0}
        <div class="tasks-panel">
          {#each tasks as t (t.id)}
            <div class="task-item">
              <div class="task-line">
                <span class="task-status task-{t.status}">{t.status.replaceAll('_', ' ')}</span>
                <span class="task-name">{t.agent_name}</span>
                <button class="task-x" onclick={() => cancelTask(t.id)}>Cancel task</button>
              </div>
              <div class="task-msg">{t.message}</div>
              {#if t.status === 'needs_input' && t.question}
                <div class="task-q">→ {t.question}</div>
                <div class="task-reply">
                  <input bind:value={taskReplies[t.id]} placeholder="Reply…" onkeydown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); replyToTask(t.id) } }} />
                  <button onclick={() => replyToTask(t.id)}>Reply</button>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      <div class="chat-body" bind:this={messageListEl}>
        {#each messages as msg, i (msg._clientId)}
          {#if i > 0 && gapBetween(messages[i - 1], msg)}
            <div class="time-gap">{gapBetween(messages[i - 1], msg)}</div>
          {/if}
          {#if msg.id > summarizedBoundary() && i > 0 && messages[i - 1].id <= summarizedBoundary()}
            <div class="summ-divider">↑ earlier conversation summarized</div>
          {/if}
          <div class="msg-row" class:msg-right={msg.role === 'user'} class:msg-left={msg.role !== 'user'}>
            <div class="msg-meta">
              <span>{msg.role === 'user' ? 'YOU' : 'EVE'}</span>
              {#if msg.created_at}<time datetime={msg.created_at}>{messageTime(msg.created_at)}</time>{/if}
              {#if msg.channel && msg.channel !== 'web'}<span>{channelLabel(msg.channel)}</span>{/if}
            </div>
            <div class="bubble" class:bubble-user={msg.role === 'user'} class:bubble-bot={msg.role !== 'user'}>
              {#if msg.role === 'user'}
                {msg.content}
              {:else}
                {@html renderMarkdown(msg.content)}
              {/if}
            </div>
          </div>
        {/each}

        {#if stream.raw || stream.status}
          <div class="msg-row msg-left">
            <div class="msg-meta"><span>EVE</span><span>LIVE</span></div>
            <div class="bubble bubble-bot">
              {#if stream.html}
                {@html stream.html}
              {:else}
                <div class="thinking-bubble">
                  <span class="thinking-label">{stream.status || 'Thinking'}</span>
                  <div class="thinking-dot"></div>
                  <div class="thinking-dot" style="animation-delay:0.2s"></div>
                  <div class="thinking-dot" style="animation-delay:0.4s"></div>
                </div>
              {/if}
            </div>
          </div>
        {/if}

        <div class="scroll-anchor"></div>
      </div>

      <div class="chat-foot">
        {#if notice}
          <div class="chat-notice">{notice}</div>
        {/if}
        <div class="input-wrap">
          <textarea
            bind:this={inputEl}
            bind:value={newMessage}
            onkeydown={handleKeydown}
            rows="1"
            placeholder="Write a message to Eve"
            oninput={(e) => { e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 140) + 'px'; }}
          ></textarea>
          <button onclick={sendMessage} class="send-btn" aria-label="Send message" disabled={sending || !currentConv}>
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 12 3.269 3.125A59.769 59.769 0 0 1 21.485 12 59.768 59.768 0 0 1 3.27 20.875L5.999 12Zm0 0h7.5" />
            </svg>
          </button>
        </div>
      </div>
    </div>
  {:else}
    <div class="empty-state">
      <p class="empty-title">No chat selected</p>
      <p class="empty-sub">A conversation is created automatically the first time you send a message.</p>
    </div>
  {/if}
</main>

<style>
  .chat { display: flex; flex-direction: column; height: 100%; overflow: hidden; flex: 1; }
  .chat-layout { display: flex; flex-direction: column; height: 100%; }
  .chat-head {
    padding: 18px var(--page-pad); border-bottom: var(--rule);
    display: flex; align-items: center; gap: 18px; max-width: 100%;
    background: var(--bg-base); flex-shrink: 0;
  }
  .chat-head > div { flex: 1; min-width: 0; display: flex; align-items: baseline; gap: 14px; }
  .chat-kicker { font-family: var(--mono); color: var(--text-faint); font-size: 0.65rem; letter-spacing: 0.1em; }
  .chat-head-name { font-size: 1rem; font-weight: 600; color: var(--text-base); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chat-head-badge {
    font: 0.68rem var(--mono); color: var(--text-muted); white-space: nowrap;
  }
  .summary-badge { border-left: var(--rule); padding-left: 14px; }
  .chat-body {
    flex: 1; overflow-y: auto; padding: 12px var(--page-pad) 40px;
    display: flex; flex-direction: column; max-width: calc(var(--content-max) + 2 * var(--page-pad));
  }
  .msg-row { display: grid; grid-template-columns: 92px minmax(0, var(--measure)); border-bottom: var(--rule); padding: 24px 0; width: 100%; }
  .msg-left, .msg-right { align-self: stretch; }
  .msg-meta { display: flex; flex-direction: column; gap: 3px; padding-top: 2px; font: 0.65rem/1.35 var(--mono); letter-spacing: 0.08em; color: var(--text-faint); }
  .msg-meta span:first-child { color: var(--text-muted); font-weight: 700; }
  .bubble {
    padding: 0; border-radius: 0; font-size: 0.94rem; line-height: 1.65;
    word-wrap: break-word; overflow-wrap: anywhere;
  }
  .bubble-user { color: var(--text-base); }
  .bubble-bot { color: oklch(87% 0.01 65); }
  .time-gap {
    font: 0.65rem var(--mono); color: var(--text-faint); padding: 12px 0 0 92px;
  }
  .summ-divider {
    font: 0.64rem var(--mono); color: var(--text-muted); border-bottom: 1px dashed var(--border-strong);
    padding: 18px 0 8px 92px; text-transform: uppercase; letter-spacing: 0.08em;
  }
  .chat-foot {
    padding: 14px var(--page-pad) 18px; border-top: var(--rule); background: var(--bg-deep); flex-shrink: 0;
  }
  .chat-notice {
    max-width: calc(var(--measure) + 92px); font: 0.72rem var(--mono); color: var(--text-base);
    border-left: 2px solid var(--text-base); padding: 6px 12px; margin-bottom: 10px;
  }
  .input-wrap {
    display: flex; gap: 10px; align-items: flex-end; max-width: calc(var(--measure) + 92px);
    background: var(--bg-raised); border: 1px solid var(--border-strong);
    border-radius: 2px; padding: 9px 9px 9px 15px;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .input-wrap:focus-within {
    border-color: var(--accent); box-shadow: 0 0 0 1px var(--accent);
  }
  .input-wrap textarea {
    flex: 1; resize: none; background: transparent; border: none; outline: none;
    color: var(--text-base); font-family: inherit; font-size: 0.92rem;
    line-height: 1.6; max-height: 140px; overflow-y: auto;
  }
  .input-wrap textarea::placeholder { color: var(--text-muted); }
  .send-btn {
    flex-shrink: 0; width: 38px; height: 38px; border-radius: 1px;
    background: var(--accent); border: none; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    transition: background 0.15s, transform 0.1s; color: var(--bg-deep);
  }
  .send-btn:hover { background: var(--accent-strong); }
  .send-btn:active { transform: scale(0.93); }
  .send-btn:disabled { opacity: 0.5; pointer-events: none; }
  .send-btn svg { width: 16px; height: 16px; }

  .thinking-bubble {
    display: flex; align-items: baseline; gap: 4px;
  }
  .thinking-label { font-size: 0.9rem; color: var(--text-muted); }
  .thinking-dot {
    width: 5px; height: 5px; border-radius: 50%; background: var(--text-muted);
    animation: thinkanim 1.4s infinite both;
  }
  @keyframes thinkanim {
    0%, 80%, 100% { opacity: 0.2; transform: scale(0.85); }
    40%           { opacity: 1;   transform: scale(1.15); }
  }

  .scroll-anchor { overflow-anchor: auto; height: 1px; }

  .empty-state { flex: 1; padding: 64px var(--page-pad); color: var(--text-muted); }
  .empty-title { font-size: 1.05rem; font-weight: 600; color: var(--text-base); margin: 14px 0 4px; }
  .empty-sub { font-size: 0.85rem; margin: 0; }

  .tasks-toggle {
    font: 0.68rem var(--mono); cursor: pointer; background: transparent; color: var(--accent);
    border: 0; border-bottom: 1px solid currentColor; padding: 2px 0; flex-shrink: 0;
  }
  .tasks-toggle:hover { color: var(--text-base); }

  .tasks-panel {
    flex-shrink: 0; max-height: 40%; overflow-y: auto;
    border-bottom: 1px solid var(--border);
    background: var(--bg-base); padding: 0 var(--page-pad);
    display: flex; flex-direction: column;
  }
  .task-item {
    border-bottom: var(--rule); padding: 12px 0; max-width: calc(var(--measure) + 92px);
  }
  .task-line { display: flex; align-items: center; gap: 8px; }
  .task-status {
    font-size: 0.62rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em;
    color: var(--text-muted);
  }
  .task-needs_input { color: var(--text-base); }
  .task-name { font-size: 0.82rem; font-weight: 600; color: var(--text-base); flex: 1; }
  .task-x {
    border: none; background: transparent; color: var(--text-muted);
    cursor: pointer; font: 0.68rem var(--mono); padding: 2px 0; border-bottom: 1px solid transparent;
  }
  .task-x:hover { color: var(--accent); border-bottom-color: currentColor; }
  .task-msg { font-size: 0.78rem; color: var(--text-muted); margin: 2px 0 0; }
  .task-q { font-size: 0.8rem; color: var(--text-base); margin: 6px 0 0; }
  .task-reply { display: flex; gap: 6px; margin-top: 6px; }
  .task-reply input {
    flex: 1; background: var(--bg-deep); border: 1px solid var(--border-strong);
    border-radius: 1px; padding: 5px 10px; color: var(--text-base);
    font-size: 0.8rem; outline: none;
  }
  .task-reply input:focus { border-color: var(--accent); }
  .task-reply button {
    border: none; background: var(--accent); color: var(--bg-deep);
    border-radius: 1px; padding: 5px 12px; font-size: 0.78rem; cursor: pointer;
  }
  @media (max-width: 640px) {
    .chat-head { align-items: flex-start; flex-wrap: wrap; }
    .chat-head > div { width: 100%; flex-basis: 100%; display: block; }
    .chat-kicker { display: block; margin-bottom: 3px; }
    .msg-row { grid-template-columns: 1fr; gap: 8px; padding: 20px 0; }
    .msg-meta { flex-direction: row; gap: 10px; }
    .time-gap, .summ-divider { padding-left: 0; }
    .summary-badge { padding-left: 10px; }
  }
</style>
