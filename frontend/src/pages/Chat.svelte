<script>
  import { onMount } from 'svelte'
  import { api } from '../lib/api.js'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { HUB_EVENT, parseHubEvent } from '../lib/events.js'
  import Icon from '../components/Icon.svelte'
  import EmptyState from '../components/EmptyState.svelte'
  import { scrollRegion } from '../lib/scrollRegion.js'

  marked.setOptions({ gfm: true, breaks: true })

  let currentConv = $state(null)
  let messages = $state([])
  let newMessage = $state('')
  let loading = $state(true)
  let sending = $state(false)
  let posting = $state(false)
  let notice = $state('')
  let messageListEl = $state(null)
  let inputEl = $state(null)

  let stream = $state({ runId: '', status: '', raw: '', html: '' })
  let eventSource = null
  let globalEvents = null
  let heartbeatTimer = null
  let tasksTimer = null
  let queueTimer = null
  let reconnectTimer = null
  let destroyed = false
  let generation = 0
  let markdownFrame = null
  let taskActions = $state({})
  let pendingMessageID = null
  let retryMessage = null
  let tasks = $state([])
  let taskReplies = $state({})
  let tasksOpen = $state(false)
  let hasOlder = $state(false)
  let loadingOlder = $state(false)
  let loadingConversation = $state(false)

  onMount(() => {
    destroyed = false
    loadPrimary()
    openGlobalEvents()
    startHeartbeat()
    loadTasks()
    tasksTimer = setInterval(loadTasks, 5000)
    window.addEventListener('popstate', handlePopState)
    return () => {
      destroyed = true
      generation++
      clearTimeout(reconnectTimer)
      if (markdownFrame !== null) cancelAnimationFrame(markdownFrame)
      eventSource?.close()
      eventSource = null
      globalEvents?.close()
      globalEvents = null
      if (heartbeatTimer) clearInterval(heartbeatTimer)
      heartbeatTimer = null
      if (tasksTimer) clearInterval(tasksTimer)
      if (queueTimer) clearInterval(queueTimer)
      tasksTimer = null
      window.removeEventListener('popstate', handlePopState)
    }
  })

  // openGlobalEvents subscribes to the IO hub: proactive messages Eve sends
  // outside a turn (via send_message) arrive here and are appended live.
  function openGlobalEvents() {
    if (destroyed) return
    const es = new EventSource('/api/events')
    globalEvents = es
    es.onopen = () => syncConversation()
    es.addEventListener(HUB_EVENT, (e) => {
      if (destroyed || globalEvents !== es) return
      const ev = parseHubEvent(e.data)
      if (!ev) return
      if (ev.type === 'resync') { syncConversation(); return }
      if (ev.type !== 'message' || !ev.conversation_id || !currentConv) return
      if (ev.conversation_id !== currentConv.id) return
      const msg = ev.data
      if (!msg || !msg.id) return
      if (messages.some((m) => m.id === msg.id)) return
      const last = messages[messages.length - 1]
      if (last && !last.id && last.role === msg.role && last.content === msg.content) {
        messages = [...messages.slice(0, -1), msg]
        return
      }
      messages = [...messages, msg]
      requestAnimationFrame(() => scrollDown())
    })
    es.onerror = () => {
      es.close()
      globalEvents = null
      if (!destroyed) reconnectTimer = setTimeout(openGlobalEvents, 3000)
    }
  }

  async function syncConversation() {
    const id = currentConv?.id
    const requestGeneration = generation
    if (!id) return
    try {
      const full = await api.get('/api/conversations/' + id)
      if (destroyed || currentConv?.id !== id || requestGeneration !== generation) return
      mergeServerMessages(full)
      currentConv = full
      updateDeliveryNotice(full)
    } catch (e) { if (!destroyed) notice = e.message }
  }

  function startHeartbeat() {
    const beat = () => {
      if (currentConv?.active_run_id) refreshConversationMeta()
    }
    beat()
    heartbeatTimer = setInterval(beat, 30000)
  }

  async function loadTasks() {
    try {
      const list = await api.get('/api/tasks')
      tasks = Array.isArray(list) ? list.filter(t => t.status === 'running' || t.status === 'needs_input') : []
    } catch {
      // tasks endpoint unavailable; ignore
    }
  }

  async function replyToTask(id) {
    if (taskActions[id]) return
    const content = (taskReplies[id] || '').trim()
    if (!content) return
    taskActions[id] = true
    try {
      await api.post('/api/tasks/' + id + '/reply', { content })
      taskReplies[id] = ''
      loadTasks()
    } catch (e) { notice = e.message } finally { taskActions[id] = false }
  }

  async function cancelTask(id) {
    if (taskActions[id]) return
    taskActions[id] = true
    try {
      await api.post('/api/tasks/' + id + '/cancel', {})
      loadTasks()
    } catch (e) { notice = e.message } finally { taskActions[id] = false }
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
      if (!conv) conv = await api.get('/api/conversations/primary')
      if (!conv) {
        conv = await api.post('/api/conversations', { primary: true })
        pushUrl('/?conv=' + conv.id)
      }
      await selectConversation(conv)
    } catch (e) {
      console.error('Failed to load conversation', e)
      notice = e.message
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
    const requestGeneration = ++generation
    loadingConversation = true
    try {
      const full = await api.get('/api/conversations/' + id)
      if (destroyed || requestGeneration !== generation) return
      applyConversation(full)
    } catch (e) {
      console.error('Failed to load conversation', e)
      if (!destroyed && requestGeneration === generation) notice = e.message
    } finally { if (requestGeneration === generation) loadingConversation = false }
  }

  async function selectConversation(conv) {
    return selectConversationById(conv.id)
  }

  function applyConversation(full) {
    const sameConversation = currentConv?.id === full.id
    if (currentConv?.id && currentConv.id !== full.id) {
      if (queueTimer) clearInterval(queueTimer)
      queueTimer = null
      pendingMessageID = null
      sending = false
      eventSource?.close()
      eventSource = null
      newMessage = ''
      retryMessage = null
      notice = ''
    }
    currentConv = full
    if (sameConversation) mergeServerMessages(full)
    else { messages = full.messages || []; hasOlder = !!full.has_more }
    pushUrl('/?conv=' + full.id)
    requestAnimationFrame(() => scrollDown(!sameConversation))
    if (full.active_run_id && full.active_submitted) {
      startStream(full.active_run_id)
    }
    if (!queueTimer && pendingMessageID === null) {
      const queuedWeb = (full.pending_message_ids || []).map(id => messages.find(m => m.id === id && m.channel === 'web')).find(Boolean)
      const waitingWeb = messages.find(m => m.id === full.active_message_id && m.channel === 'web' && !full.active_submitted)
      const pending = queuedWeb || waitingWeb
      if (pending) {
        pendingMessageID = pending.id
        sending = true
        notice = 'Queued — Eve will reply after the current turn.'
        waitForQueuedTurn(full.id, pending.id)
      }
    }
    updateDeliveryNotice(full)
  }

  function mergeServerMessages(full) {
    const durable = full.messages || []
    const firstID = durable[0]?.id
    const older = firstID ? messages.filter(m => m.id && m.id < firstID) : []
    const optimistic = messages.filter(m => !m.id && m.source_id && !durable.some(d => d.source_id === m.source_id))
    messages = [...older, ...durable, ...optimistic]
    if (!older.length) hasOlder = !!full.has_more
  }

  async function loadOlder() {
    const convID = currentConv?.id
    const first = messages.find(m => m.id)
    if (!convID || !first || loadingOlder) return
    loadingOlder = true
    const previousHeight = messageListEl?.scrollHeight || 0
    const previousTop = messageListEl?.scrollTop || 0
    try {
      const page = await api.get('/api/conversations/' + convID + '?before_id=' + first.id)
      if (destroyed || currentConv?.id !== convID) return
      const known = new Set(messages.filter(m => m.id).map(m => m.id))
      messages = [...(page.messages || []).filter(m => !known.has(m.id)), ...messages]
      hasOlder = !!page.has_more
      requestAnimationFrame(() => { if (messageListEl) messageListEl.scrollTop = previousTop + messageListEl.scrollHeight - previousHeight })
    } catch (e) { notice = e.message } finally { loadingOlder = false }
  }

  async function sendMessage() {
    if (posting || loadingConversation || !newMessage.trim() || !currentConv) return
    const sendGeneration = ++generation
    const content = newMessage
    const convID = currentConv.id
    const clientID = retryMessage?.content === content ? retryMessage.id : (globalThis.crypto?.randomUUID?.() || String(Date.now()) + '-' + Math.random())
    retryMessage = null
    newMessage = ''
    if (inputEl) {
      inputEl.value = ''
      inputEl.style.height = 'auto'
    }
    posting = true
    notice = ''
    if (!messages.some(m => m.source_id === clientID)) {
      messages = [...messages, { role: 'user', content, source_id: clientID }]
    }
    requestAnimationFrame(() => scrollDown())

    try {
      const result = await api.post('/api/conversations/' + convID + '/messages', { content, client_id: clientID })
      if (destroyed || currentConv?.id !== convID || sendGeneration !== generation) return
      generation++
      messages = messages.map(m => m.source_id === clientID ? { ...m, id: result.message_id } : m)
      if (result.queued) {
        pendingMessageID = result.message_id
        sending = true
        notice = 'Queued — Eve will reply after the current turn.'
        waitForQueuedTurn(currentConv.id, result.message_id)
      } else if (result.steered && eventSource && stream.runId === result.run_id) {
        notice = 'Eve will fold this into her current reply.'
      } else {
        startStream(result.run_id)
      }
    } catch (e) {
      console.error('Failed to send message', e)
      if (destroyed || currentConv?.id !== convID || sendGeneration !== generation) return
      retryMessage = { content, id: clientID }
      newMessage = content
      notice = 'Could not confirm delivery. Retry to check the same message.'
      scrollDown()
    } finally {
      posting = false
    }
  }

  function waitForQueuedTurn(convID, messageID) {
    if (queueTimer) clearInterval(queueTimer)
    const check = async () => {
      try {
        const conv = await api.get('/api/conversations/' + convID)
        if (destroyed || currentConv?.id !== convID) return
        if ((conv.steered_message_ids || []).includes(messageID)) {
          clearInterval(queueTimer)
          queueTimer = null
          pendingMessageID = null
          notice = 'Eve will fold this into her current reply.'
          if (currentConv?.id === convID && conv.active_run_id && (!eventSource || stream.runId !== conv.active_run_id)) {
            startStream(conv.active_run_id)
          }
          return
        }
        if (conv.active_message_id === messageID) {
          if (conv.active_run_id && conv.active_submitted) {
            clearInterval(queueTimer)
            queueTimer = null
            pendingMessageID = null
            notice = ''
            if (currentConv?.id === convID) startStream(conv.active_run_id)
          }
          return
        }
        if (!(conv.pending_message_ids || []).includes(messageID)) {
          clearInterval(queueTimer)
          queueTimer = null
          pendingMessageID = null
          sending = false
          notice = ''
          if (currentConv?.id === convID) applyConversation(conv)
        }
      } catch (e) {
        console.error('Failed to check queued turn', e)
      }
    }
    queueTimer = setInterval(check, 2000)
    check()
  }

  function startStream(runId) {
    if (destroyed || !runId) return
    if (markdownFrame !== null) { cancelAnimationFrame(markdownFrame); markdownFrame = null }
    eventSource?.close()
    eventSource = null
    stream = { runId, status: 'Thinking', raw: '', html: '' }

    const es = new EventSource('/runs/' + runId + '/events')
    eventSource = es

    es.addEventListener('token', (e) => {
      if (eventSource !== es || destroyed) return
      stream.status = ''
      stream.raw += e.data
      if (markdownFrame === null) markdownFrame = requestAnimationFrame(() => {
        markdownFrame = null
        if (eventSource !== es || destroyed) return
        stream.html = renderMarkdown(stream.raw)
        scrollDown()
      })
    })

    es.addEventListener('reset', () => {
      if (eventSource !== es || destroyed) return
      stream.raw = ''
      stream.html = ''
      stream.status = 'Revising answer'
    })

    es.addEventListener('status', (e) => {
      if (eventSource !== es || destroyed) return
      stream.status = e.data
    })

    es.addEventListener('done', (e) => {
      if (eventSource !== es || destroyed) return
      es.close()
      eventSource = null
      if (e.data) {
        messages = [...messages, { role: 'assistant', content: e.data }]
      }
      stream = { runId: '', status: '', raw: '', html: '' }
      if (notice && !notice.startsWith('Reply delivery is retrying.')) notice = ''
      sending = pendingMessageID !== null
      scrollDown()
      healConversation()
    })

    es.addEventListener('error', (e) => {
      if (eventSource !== es || destroyed) return
      if (!e.data) return
      es.close()
      eventSource = null
      stream = { runId: '', status: '', raw: '', html: '' }
      messages = [...messages, { role: 'assistant', content: e.data }]
      scrollDown()
      sending = pendingMessageID !== null
      healConversation()
    })

    es.onerror = () => {
      if (eventSource !== es || destroyed) return
      if (es.readyState === EventSource.CLOSED) return
      es.close()
      eventSource = null
      const partial = stream.raw
      stream = { runId: '', status: '', raw: '', html: '' }
      if (partial && partial.trim()) {
        messages = [...messages, { role: 'assistant', content: partial }]
        scrollDown()
      }
      sending = pendingMessageID !== null
      healConversation()
    }
  }

  async function healConversation() {
    const convId = currentConv?.id
    if (!convId) return
    const requestGeneration = generation
    const deadline = Date.now() + 120000
    while (!destroyed && currentConv?.id === convId && requestGeneration === generation && Date.now() < deadline) {
      await sleep(500)
      let full
      try {
        full = await api.get('/api/conversations/' + convId)
      } catch {
        continue
      }
      if (destroyed || currentConv?.id !== convId || requestGeneration !== generation) return
      currentConv = full
      updateDeliveryNotice(full)
      if (full.delivery_failures) return
      if (full.active_run_id) continue
      if (full.messages && full.messages.length > 0) {
        mergeServerMessages(full)
        requestAnimationFrame(() => scrollDown())
      }
      return
    }
    if (!destroyed && currentConv?.id === convId) {
      notice = 'Reconnecting to the current turn…'
      if (currentConv.active_run_id) startStream(currentConv.active_run_id)
    }
  }

  function sleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms))
  }

  async function refreshConversationMeta() {
    if (!currentConv) return
    const convID = currentConv.id
    const requestGeneration = generation
    try {
      const full = await api.get('/api/conversations/' + convID)
      if (destroyed || currentConv?.id !== convID || requestGeneration !== generation) return
      currentConv = full
      if (!full.active_run_id && !posting && pendingMessageID === null) mergeServerMessages(full)
      updateDeliveryNotice(full)
    } catch {}
  }

  function updateDeliveryNotice(full) {
    if (full.delivery_failures > 0) {
      notice = 'Reply delivery is retrying. ' + (full.delivery_error || 'Queued turns will wait until the channel recovers.')
    } else if (notice.startsWith('Reply delivery is retrying.')) {
      notice = ''
    }
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

  function scrollDown(force = false) {
    if (!messageListEl) return
    if (!force && messageListEl.scrollHeight - messageListEl.scrollTop - messageListEl.clientHeight > 180) return
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

  function startDraft(text) {
    newMessage = text
    inputEl?.focus()
  }
</script>

<main class="chat">
  {#if loading}
    <EmptyState loading title="Opening your conversation" description="Bringing your messages together." />
  {:else if currentConv}
    <div class="chat-layout">
      <div class="chat-head">
        <div>
          <p class="eyebrow">Your conversation</p>
          <h1 class="chat-head-name">{currentConv.title}</h1>
          <span class="chat-head-description">A place to think, plan, and get things done.</span>
        </div>
        <div class="chat-actions">
        <a class="button quiet small" href={'/api/conversations/' + currentConv.id + '/export'} download><Icon name="download" size={16} /><span>Export</span></a>
        <span class="chat-head-badge">{Math.max(messages.length, currentConv.total_messages || 0)} messages</span>
        {#if summarizedBoundary() > 0}
          <span class="badge">Earlier messages summarized</span>
        {/if}
        {#if tasks.length > 0}
          <button class="button small" aria-expanded={tasksOpen} aria-controls="chat-tasks" onclick={() => { tasksOpen = !tasksOpen }}>
            <Icon name="tasks" size={16} />{tasks.length} active {tasks.length === 1 ? 'task' : 'tasks'}
          </button>
        {/if}
        </div>
      </div>

      {#if tasksOpen && tasks.length > 0}
        <div class="tasks-panel" id="chat-tasks">
          {#each tasks as t (t.id)}
            <div class="task-item">
              <div class="task-line">
                <span class="badge" data-tone={t.status === 'needs_input' ? 'warning' : 'accent'}>{t.status === 'needs_input' ? 'Needs your input' : 'In progress'}</span>
                <span class="task-name">{t.agent_name}</span>
                <button class="button danger small" disabled={taskActions[t.id]} onclick={() => cancelTask(t.id)}>Cancel task</button>
              </div>
              <div class="task-msg">{t.message}</div>
              {#if t.status === 'needs_input' && t.question}
                <div class="task-q">→ {t.question}</div>
                <div class="task-reply">
                  <input class="input" bind:value={taskReplies[t.id]} aria-label="Reply to background task" placeholder="Your reply…" onkeydown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); replyToTask(t.id) } }} />
                  <button class="button primary" disabled={taskActions[t.id] || !taskReplies[t.id]?.trim()} onclick={() => replyToTask(t.id)}>Reply</button>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      <div class="chat-body" role="region" aria-label="Conversation messages" use:scrollRegion bind:this={messageListEl}>
        <div class="conversation">
        {#if hasOlder}<div class="history-control"><button class="button quiet small" onclick={loadOlder} disabled={loadingOlder}><Icon name="clock" size={16} />{loadingOlder ? 'Loading…' : 'Load earlier messages'}</button></div>{/if}
        {#if messages.length === 0 && !stream.raw && !stream.status}
          <div class="welcome">
            <span class="welcome-mark"><Icon name="sparkle" size={32} /></span>
            <p class="eyebrow">A little clarity, a little momentum</p>
            <h2>What’s on your mind?</h2>
            <p>Bring an idea, a question, or something on your list.<br />We can work through it together.</p>
            <div class="starters" aria-label="Conversation starters">
              {#each [{ icon: 'tasks', label: 'Plan my day', draft: 'Help me make a plan for today.' }, { icon: 'sparkle', label: 'Think it through', draft: 'I’d like to think something through with you. ' }, { icon: 'email', label: 'Find the right words', draft: 'Help me draft a message. ' }] as starter}
                <button onclick={() => startDraft(starter.draft)}><Icon name={starter.icon} size={18} /><span>{starter.label}</span><Icon name="chevron" size={14} /></button>
              {/each}
            </div>
          </div>
        {/if}
        {#each messages as msg, i (msg.id != null ? 'id-' + msg.id : msg.source_id ? 'source-' + msg.source_id : 'pending-' + i)}
          {#if i > 0 && gapBetween(messages[i - 1], msg)}
            <div class="time-gap">{gapBetween(messages[i - 1], msg)}</div>
          {/if}
          {#if msg.id > summarizedBoundary() && i > 0 && messages[i - 1].id <= summarizedBoundary()}
            <div class="summ-divider">↑ earlier conversation summarized</div>
          {/if}
          <div class="msg-row" class:msg-right={msg.role === 'user'} class:msg-left={msg.role !== 'user'}>
            <div class="msg-avatar" aria-hidden="true">{#if msg.role === 'user'}Y{:else}<Icon name="sparkle" size={18} />{/if}</div>
            <div class="message-content">
            <div class="msg-meta">
              <span>{msg.role === 'user' ? (msg.sender && msg.sender !== 'owner' ? msg.sender : 'You') : 'Eve'}</span>
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
          </div>
        {/each}

        {#if stream.raw || stream.status}
          <div class="msg-row msg-left">
            <div class="msg-avatar" aria-hidden="true"><Icon name="sparkle" size={18} /></div>
            <div class="message-content">
            <div class="msg-meta"><span>Eve</span><span class="live-label">Responding</span></div>
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
          </div>
        {/if}

        <div class="scroll-anchor"></div>
        </div>
      </div>

      <div class="chat-foot">
        {#if notice}
          <div class="chat-notice notice" role="status"><Icon name="info" size={17} /><span>{notice}</span></div>
        {/if}
        <div class="input-wrap">
          <textarea
            bind:this={inputEl}
            bind:value={newMessage}
            onkeydown={handleKeydown}
            rows="1"
            placeholder={stream.runId ? 'Add a thought while Eve is working…' : 'Message Eve…'}
            aria-label="Message to Eve"
            oninput={(e) => { e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 140) + 'px'; }}
          ></textarea>
          <button onclick={sendMessage} class="send-btn" aria-label="Send message" disabled={posting || loadingConversation || !newMessage.trim() || !currentConv}>
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 12 3.269 3.125A59.769 59.769 0 0 1 21.485 12 59.768 59.768 0 0 1 3.27 20.875L5.999 12Zm0 0h7.5" />
            </svg>
          </button>
        </div>
        <div class="composer-hint"><span>{stream.runId ? 'You can add a follow-up without waiting.' : 'One thought at a time. Eve is here to help.'}</span><span class="keyboard-hint"><kbd>Enter</kbd> to send · <kbd>Shift + Enter</kbd> for a new line</span></div>
      </div>
    </div>
  {:else}
    <EmptyState icon="chat" title="Let’s get your conversation open" description={notice || 'Your conversation is not available right now. Try again to reconnect.'}><button class="button primary" onclick={() => { loading = true; loadPrimary() }}>Try again</button></EmptyState>
  {/if}
</main>

<style>
  .chat { display: flex; flex-direction: column; height: 100%; overflow: hidden; flex: 1; min-width: 0; }
  .chat-layout { display: flex; flex-direction: column; height: 100%; min-height: 0; }
  .chat-head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-5); padding: var(--space-6) var(--page-pad); border-bottom: var(--rule); flex-shrink: 0; }
  .chat-head > div:first-child { min-width: 0; }
  .chat-head .eyebrow { margin-bottom: var(--space-1); }
  .chat-head-name { font-size: 1.125rem; font-weight: 600; letter-spacing: -.025em; line-height: 1.4; margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .chat-head-description { display: block; font-size: .75rem; color: var(--text-muted); margin-top: var(--space-1); }
  .chat-actions { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; flex-shrink: 0; }
  .chat-head-badge { font-size: .75rem; color: var(--text-muted); }
  .chat-body { flex: 1; min-height: 0; overflow-y: auto; padding: var(--space-6) var(--page-pad) var(--space-8); display: flex; flex-direction: column; }
  .conversation { width: min(100%, var(--measure)); margin-inline: auto; display: flex; flex-direction: column; flex: 1; }
  .history-control { text-align: center; }
  .msg-row { display: flex; gap: var(--space-3); padding: var(--space-5) 0; width: 100%; }
  .msg-avatar { flex-shrink: 0; width: 32px; height: 32px; display: grid; place-items: center; color: var(--accent-text); background: var(--accent-dim); border-radius: var(--radius); font-size: .6875rem; font-weight: 600; }
  .message-content { flex: 1; min-width: 0; padding-top: var(--space-1); }
  .msg-right { flex-direction: row-reverse; padding-left: var(--space-8); }
  .msg-right .msg-avatar { background: var(--bg-hover); color: var(--text-muted); }
  .msg-right .msg-meta { justify-content: flex-end; }
  .msg-meta { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); font-size: .6875rem; color: var(--text-muted); margin-bottom: var(--space-2); }
  .msg-meta > span:first-child { color: var(--text-base); font-size: .75rem; font-weight: 600; }
  .bubble { font-size: .9375rem; line-height: 1.8; overflow-wrap: anywhere; color: var(--text-base); }
  .bubble-user { background: var(--user-bubble); padding: var(--space-3) var(--space-4); border: 1px solid var(--accent-dim); border-radius: var(--radius-lg) var(--radius-sm) var(--radius-lg) var(--radius-lg); width: fit-content; margin-left: auto; white-space: pre-wrap; }
  .time-gap, .summ-divider { text-align: center; font-size: .6875rem; color: var(--text-muted); margin: var(--space-4) 0; }
  .summ-divider { padding-top: var(--space-3); border-top: 1px dashed var(--border); }
  .chat-foot { flex-shrink: 0; padding: var(--space-3) var(--page-pad) var(--space-5); background: var(--bg-base); }
  .chat-notice { width: min(100%, var(--measure)); margin: 0 auto var(--space-3); }
  .input-wrap { display: flex; align-items: flex-end; gap: var(--space-3); width: min(100%, var(--measure)); margin-inline: auto; padding: var(--space-3); border: 1px solid var(--border-strong); border-radius: var(--radius-lg); background: var(--bg-raised); box-shadow: var(--composer-shadow); transition: border-color var(--duration); }
  .input-wrap:focus-within { border-color: var(--accent); }
  .input-wrap textarea { flex: 1; padding: var(--space-2) var(--space-1); min-height: 42px; resize: none; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text-base); font-size: .9375rem; line-height: 1.6; max-height: 140px; overflow-y: auto; }
  .input-wrap textarea::placeholder { color: var(--text-muted); }
  .send-btn { flex-shrink: 0; width: 42px; height: 42px; border-radius: var(--radius); background: var(--accent); color: var(--on-accent); border: 0; display: grid; place-items: center; transition: background var(--duration); }
  .send-btn:hover:not(:disabled) { background: var(--accent-strong); }
  .send-btn svg { width: 18px; height: 18px; }
  .composer-hint { display: flex; justify-content: space-between; gap: var(--space-3); width: min(100%, var(--measure)); margin: var(--space-2) auto 0; font-size: .625rem; color: var(--text-muted); }
  kbd { font-family: inherit; }
  .thinking-bubble { display: flex; align-items: center; gap: var(--space-1); }
  .thinking-label { color: var(--text-muted); margin-right: var(--space-2); font-size: .875rem; }
  .thinking-dot { width: 4px; height: 4px; border-radius: 50%; background: var(--accent); animation: thinkanim 1.4s infinite both; }
  .live-label { color: var(--accent-text); }
  @keyframes thinkanim { 0%, 80%, 100% { opacity: .3; } 40% { opacity: 1; } }
  .scroll-anchor { overflow-anchor: auto; height: 1px; }
  .welcome { display: flex; flex: 1; flex-direction: column; justify-content: center; align-items: center; text-align: center; padding: var(--space-8) 0 var(--space-12); }
  .welcome-mark { display: grid; place-items: center; width: 64px; height: 64px; background: var(--accent-dim); color: var(--accent-text); border-radius: var(--radius-lg); margin-bottom: var(--space-6); }
  .welcome h2 { font-size: clamp(1.5rem, 3vw, 2rem); font-weight: 550; line-height: 1.3; letter-spacing: -.04em; margin: var(--space-1) 0 var(--space-3); }
  .welcome > p:not(.eyebrow) { color: var(--text-muted); font-size: .875rem; line-height: 1.8; margin: 0; }
  .welcome .eyebrow { font-size: .625rem; }
  .starters { display: flex; flex-wrap: wrap; justify-content: center; gap: var(--space-2); margin-top: var(--space-8); }
  .starters button { display: flex; align-items: center; gap: var(--space-2); min-height: 44px; padding: var(--space-3); background: var(--bg-raised); color: var(--text-muted); border: var(--rule); border-radius: var(--radius); font-size: .75rem; transition: background var(--duration), border-color var(--duration); }
  .starters button:hover { background: var(--accent-dim); border-color: var(--accent); color: var(--accent-text); }
  .tasks-panel { flex-shrink: 0; max-height: 40%; overflow-y: auto; border-bottom: var(--rule); background: var(--bg-raised); padding: var(--space-3) var(--page-pad); }
  .task-item { padding: var(--space-3) 0; width: min(100%, var(--measure)); margin: auto; }
  .task-item + .task-item { border-top: var(--rule); }
  .task-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); }
  .task-name { font-size: .875rem; font-weight: 600; flex: 1; }
  .task-msg { font-size: .8125rem; color: var(--text-muted); margin-top: var(--space-2); overflow-wrap: anywhere; }
  .task-q { font-size: .875rem; margin-top: var(--space-3); }
  .task-reply { display: flex; gap: var(--space-2); margin-top: var(--space-3); }
  @media (max-width: 1000px) { .chat-head-description { display: none; } .chat-head-badge { display: none; } .keyboard-hint { display: none; } }
  @media (max-width: 640px) {
    .chat-head { padding: var(--space-4) var(--page-pad); gap: var(--space-3); align-items: flex-start; }
    .chat-head .eyebrow { display: none; }
    .chat-head-name { font-size: .9375rem; white-space: normal; }
    .chat-actions { gap: var(--space-1); justify-content: flex-end; max-width: 50%; }
    .chat-actions .badge { display: none; }
    .chat-body { padding-top: var(--space-3); }
    .chat-foot { padding-bottom: max(var(--space-3), env(safe-area-inset-bottom)); }
    .msg-row { gap: var(--space-2); padding-block: var(--space-4); }
    .msg-avatar { width: 28px; height: 28px; }
    .msg-right { padding-left: var(--space-3); }
    .bubble { font-size: .875rem; }
    .welcome { padding-block: var(--space-6); }
    .welcome > p:not(.eyebrow) br { display: none; }
    .starters { flex-direction: column; width: min(100%, 270px); margin-top: var(--space-6); }
    .starters button { justify-content: flex-start; }
    .starters button > span { flex: 1; text-align: left; }
    .composer-hint { font-size: .625rem; }
  }
</style>
