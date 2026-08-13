<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

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
      if (!msg || !msg.id) return
      if (messages.some((m) => m.id === msg.id)) return
      messages = [...messages, msg]
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
    try {
      const convs = await api.get('/api/conversations')

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
      await selectConversation(conv)
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

  async function selectConversationById(id) {
    try {
      const full = await api.get('/api/conversations/' + id)
      applyConversation(full)
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }

  async function selectConversation(conv) {
    try {
      const full = await api.get('/api/conversations/' + conv.id)
      applyConversation(full)
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }

  function applyConversation(full) {
    currentConv = full
    messages = full.messages || []
    pushUrl('/?conv=' + full.id)
    requestAnimationFrame(() => scrollDown())
    if (full.active_run_id) {
      startStream(full.active_run_id)
    }
  }

  async function sendMessage() {
    if (sending || !newMessage.trim() || !currentConv) return
    const content = newMessage
    newMessage = ''
    if (inputEl) inputEl.style.height = 'auto'
    sending = true
    notice = ''
    messages = [...messages, { role: 'user', content }]
    requestAnimationFrame(() => scrollDown())

    try {
      const result = await api.post('/api/conversations/' + currentConv.id + '/messages', { content })
      startStream(result.run_id)
    } catch (e) {
      sending = false
      console.error('Failed to send message', e)
      messages = [...messages, { role: 'assistant', content: '⚠️ Failed to send message. (' + e.message + ')' }]
      scrollDown()
    }
  }

  function startStream(runId) {
    eventSource?.close()
    eventSource = null
    stream = { runId, status: 'Thinking', raw: '', html: '' }

    const es = new EventSource('/runs/' + runId + '/events')
    eventSource = es

    es.addEventListener('token', (e) => {
      stream.status = ''
      stream.raw += e.data
      stream.html = renderMarkdown(stream.raw)
      scrollDown()
    })

    es.addEventListener('status', (e) => {
      stream.status = e.data
    })

    es.addEventListener('done', (e) => {
      es.close()
      eventSource = null
      if (e.data) {
        messages = [...messages, { role: 'assistant', content: e.data }]
      }
      stream = { runId: '', status: '', raw: '', html: '' }
      sending = false
      scrollDown()
      refreshConversationMeta()
    })

    es.addEventListener('error', (e) => {
      if (!e.data) return
      es.close()
      eventSource = null
      stream = { runId: '', status: '', raw: '', html: '' }
      messages = [...messages, { role: 'assistant', content: e.data }]
      scrollDown()
      sending = false
      refreshConversationMeta()
    })

    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) return
      es.close()
      eventSource = null
      const partial = stream.raw
      stream = { runId: '', status: '', raw: '', html: '' }
      if (partial && partial.trim()) {
        messages = [...messages, { role: 'assistant', content: partial }]
        scrollDown()
      }
      sending = false
      healConversation()
    }
  }

  async function healConversation() {
    const convId = currentConv?.id
    if (!convId) return
    const deadline = Date.now() + 20000
    while (Date.now() < deadline) {
      await sleep(500)
      let full
      try {
        full = await api.get('/api/conversations/' + convId)
      } catch {
        continue
      }
      currentConv = full
      if (full.active_run_id) continue
      if (full.messages && full.messages.length > 0) {
        messages = full.messages
        requestAnimationFrame(() => scrollDown())
      }
      return
    }
    notice = 'The connection to the agent was lost. The response may still be processing — try refreshing.'
  }

  function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms))
  }

  async function refreshConversationMeta() {
    if (!currentConv) return
    try {
      const full = await api.get('/api/conversations/' + currentConv.id)
      currentConv = full
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
        <span class="chat-head-name">{currentConv.title}</span>
        <span class="chat-head-badge">{messages.length} messages</span>
        {#if summarizedBoundary() > 0}
          <span class="chat-head-badge summary-badge">summarized</span>
        {/if}
        {#if tasks.length > 0}
          <button class="tasks-toggle" onclick={() => { tasksOpen = !tasksOpen }}>
            tasks ({tasks.length})
          </button>
        {/if}
      </div>

      {#if tasksOpen && tasks.length > 0}
        <div class="tasks-panel">
          {#each tasks as t (t.id)}
            <div class="task-item">
              <div class="task-line">
                <span class="task-status task-{t.status}">{t.status}</span>
                <span class="task-name">{t.agent_name}</span>
                <button class="task-x" onclick={() => cancelTask(t.id)} aria-label="Cancel">✕</button>
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
        {#each messages as msg, i (msg.id ?? i)}
          {#if i > 0 && gapBetween(messages[i - 1], msg)}
            <div class="time-gap">{gapBetween(messages[i - 1], msg)}</div>
          {/if}
          {#if msg.id > summarizedBoundary() && i > 0 && messages[i - 1].id <= summarizedBoundary()}
            <div class="summ-divider">↑ earlier conversation summarized</div>
          {/if}
          <div class="msg-row" class:msg-right={msg.role === 'user'} class:msg-left={msg.role !== 'user'}>
            <div class="bubble" class:bubble-user={msg.role === 'user'} class:bubble-bot={msg.role !== 'user'}>
              {#if msg.role === 'user'}
                {msg.content}
              {:else}
                {@html renderMarkdown(msg.content)}
              {/if}
            </div>
            {#if msg.role !== 'user' && msg.channel && msg.channel !== 'web'}
              <span class="chan-badge">{msg.channel}</span>
            {/if}
          </div>
        {/each}

        {#if stream.raw || stream.status}
          <div class="msg-row msg-left">
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
            placeholder="Message…"
            oninput={(e) => { e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 140) + 'px'; }}
          ></textarea>
          <button onclick={sendMessage} class="send-btn" aria-label="Send" disabled={sending || !currentConv}>
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
    padding: 14px 24px; border-bottom: 1px solid var(--border);
    display: flex; align-items: center; gap: 12px;
    background: var(--bg-base); flex-shrink: 0;
  }
  .chat-head-name { font-size: 0.95rem; font-weight: 600; color: var(--text-base); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chat-head-badge {
    font-size: 0.7rem; font-weight: 500;
    background: var(--purple-dim); color: oklch(75% 0.2 292.0);
    border: 1px solid oklch(59.1% 0.249 292.7 / 0.3);
    padding: 2px 8px; border-radius: 20px;
  }
  .summary-badge { color: oklch(80% 0.18 145); border-color: oklch(65% 0.18 145 / 0.3); }

  .chat-body {
    flex: 1; overflow-y: auto; padding: 28px 24px;
    display: flex; flex-direction: column; gap: 4px;
  }

  .msg-row { display: flex; max-width: 78%; margin-bottom: 6px; }
  .msg-left { align-self: flex-start; }
  .msg-right { align-self: flex-end; justify-content: flex-end; }
  .bubble {
    padding: 12px 16px; border-radius: 14px; font-size: 0.9rem; line-height: 1.6;
    word-wrap: break-word; overflow-wrap: anywhere;
  }
  .bubble-user {
    background: var(--purple-solid); color: #fff; border-bottom-right-radius: 4px;
  }
  .bubble-bot {
    background: var(--bg-card); border: 1px solid var(--border); border-bottom-left-radius: 4px;
  }
  .chan-badge {
    align-self: flex-start; margin-left: 8px; font-size: 0.62rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.06em;
    color: var(--text-muted); background: var(--bg-card);
    border: 1px solid var(--border); border-radius: 8px; padding: 2px 7px;
  }

  .time-gap {
    align-self: center; font-size: 0.68rem; color: var(--text-muted);
    background: var(--bg-card); border: 1px solid var(--border);
    padding: 1px 10px; border-radius: 10px; margin: 8px 0 4px;
  }
  .summ-divider {
    align-self: stretch; text-align: center; font-size: 0.68rem; color: var(--text-muted);
    border-top: 1px dashed var(--border); margin: 14px 0 10px; padding-top: 8px;
    text-transform: uppercase; letter-spacing: 0.08em;
  }

  .chat-foot {
    padding: 16px 24px; border-top: 1px solid var(--border);
    background: var(--bg-base); flex-shrink: 0;
  }
  .chat-notice {
    font-size: 0.75rem; color: oklch(75% 0.12 45);
    background: oklch(50% 0.12 45 / 0.12);
    border: 1px solid oklch(60% 0.14 45 / 0.35);
    border-radius: 8px; padding: 6px 12px; margin-bottom: 10px;
  }
  .input-wrap {
    display: flex; gap: 10px; align-items: flex-end;
    background: var(--bg-card); border: 1px solid var(--border);
    border-radius: 12px; padding: 10px 10px 10px 16px;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .input-wrap:focus-within {
    border-color: oklch(59.1% 0.249 292.7 / 0.6);
    box-shadow: 0 0 0 3px oklch(59.1% 0.249 292.7 / 0.1);
  }
  .input-wrap textarea {
    flex: 1; resize: none; background: transparent; border: none; outline: none;
    color: var(--text-base); font-family: inherit; font-size: 0.9rem;
    line-height: 1.6; max-height: 140px; overflow-y: auto;
  }
  .input-wrap textarea::placeholder { color: var(--text-muted); }
  .send-btn {
    flex-shrink: 0; width: 36px; height: 36px; border-radius: 8px;
    background: var(--purple-solid); border: none; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    transition: opacity 0.15s, transform 0.1s; color: #fff;
  }
  .send-btn:hover { opacity: 0.85; }
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

  .empty-state {
    flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: center;
    color: var(--text-muted);
  }
  .empty-title { font-size: 1.05rem; font-weight: 600; color: var(--text-base); margin: 14px 0 4px; }
  .empty-sub { font-size: 0.85rem; margin: 0; }

  .tasks-toggle {
    font-size: 0.7rem; font-weight: 600; cursor: pointer;
    background: var(--bg-card); color: oklch(75% 0.2 292.0);
    border: 1px solid oklch(59.1% 0.249 292.7 / 0.3);
    padding: 3px 10px; border-radius: 20px; flex-shrink: 0;
  }
  .tasks-toggle:hover { background: var(--purple-dim); }

  .tasks-panel {
    flex-shrink: 0; max-height: 40%; overflow-y: auto;
    border-bottom: 1px solid var(--border);
    background: var(--bg-base); padding: 10px 24px;
    display: flex; flex-direction: column; gap: 8px;
  }
  .task-item {
    background: var(--bg-card); border: 1px solid var(--border);
    border-radius: 10px; padding: 8px 12px;
  }
  .task-line { display: flex; align-items: center; gap: 8px; }
  .task-status {
    font-size: 0.62rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em;
    padding: 1px 7px; border-radius: 8px;
  }
  .task-running { background: oklch(55% 0.2 250 / 0.15); color: oklch(75% 0.2 250); }
  .task-needs_input { background: oklch(55% 0.18 80 / 0.15); color: oklch(75% 0.18 80); }
  .task-name { font-size: 0.82rem; font-weight: 600; color: var(--text-base); flex: 1; }
  .task-x {
    border: none; background: transparent; color: var(--text-muted);
    cursor: pointer; font-size: 0.8rem; padding: 2px 4px; border-radius: 6px;
  }
  .task-x:hover { color: oklch(70% 0.2 25); }
  .task-msg { font-size: 0.78rem; color: var(--text-muted); margin: 2px 0 0; }
  .task-q { font-size: 0.8rem; color: oklch(80% 0.18 80); margin: 6px 0 0; }
  .task-reply { display: flex; gap: 6px; margin-top: 6px; }
  .task-reply input {
    flex: 1; background: var(--bg-base); border: 1px solid var(--border);
    border-radius: 8px; padding: 5px 10px; color: var(--text-base);
    font-size: 0.8rem; outline: none;
  }
  .task-reply input:focus { border-color: oklch(59.1% 0.249 292.7 / 0.6); }
  .task-reply button {
    border: none; background: var(--purple-solid); color: #fff;
    border-radius: 8px; padding: 5px 12px; font-size: 0.78rem; cursor: pointer;
  }
</style>
