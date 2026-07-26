<script>
  import { api } from '../lib/api.js'
  import { marked } from 'marked'
  marked.setOptions({ gfm: true, breaks: true })

  let conversations = $state([])
  let currentConv = $state(null)
  let messages = $state([])
  let newMessage = $state('')
  let loading = $state(true)
  let sidebarOpen = $state(true)
  let messageListEl = $state(null)

  let streamBubbles = $state([])
  let streamingRaw = ''
  let streamingStatus = $state('')
  let activeRunId = $state('')
  let eventSource = $state(null)

  $effect(() => {
    loadConversations()
    return () => {
      eventSource?.close()
    }
  })

  async function loadConversations() {
    try {
      conversations = await api.get('/api/conversations')

      const url = new URL(window.location.href)
      const convId = url.searchParams.get('conv')
      if (convId) {
        const c = conversations.find(s => s.id === convId)
        if (c) await selectConversation(c)
      }
    } catch (e) {
      console.error('Failed to load conversations', e)
    } finally {
      loading = false
    }
  }

  async function selectConversation(conv) {
    currentConv = conv
    navigate('/?conv=' + conv.id)
    try {
      const full = await api.get('/api/conversations/' + conv.id)
      currentConv = full
      messages = (full.messages || []).map(msg => {
        if (msg.role === 'assistant') {
          return { ...msg, content: marked.parse(msg.content) }
        }
        return msg
      })
      requestAnimationFrame(() => scrollDown())
      if (full.active_run_id) {
        startStream(full.active_run_id)
      }
    } catch (e) {
      console.error('Failed to load conversation', e)
    }
  }

  async function newConversation() {
    try {
      const conv = await api.post('/api/conversations', {})
      conversations = [conv, ...conversations]
      await selectConversation(conv)
    } catch (e) {
      console.error('Failed to create conversation', e)
    }
  }

  async function deleteConversation(conv) {
    if (!confirm('Delete this conversation?')) return
    try {
      await api.del('/api/conversations/' + conv.id)
      conversations = conversations.filter(c => c.id !== conv.id)
      if (currentConv?.id === conv.id) {
        currentConv = null
        messages = []
        navigate('/?')
      }
    } catch (e) {
      console.error('Failed to delete conversation', e)
    }
  }

  async function sendMessage() {
    if (!newMessage.trim() || !currentConv) return
    const content = newMessage
    newMessage = ''
    messages = [...messages, { role: 'user', content }]
    requestAnimationFrame(() => scrollDown())

    try {
      const result = await api.post('/api/conversations/' + currentConv.id + '/messages', { content })
      activeRunId = result.run_id
      startStream(result.run_id)
    } catch (e) {
      console.error('Failed to send message', e)
      messages = [...messages, { role: 'assistant', content: marked.parse('⚠️ Failed to send message. (' + e.message + ')') }]
    }
  }

  function startStream(runId) {
    eventSource?.close()
    activeRunId = runId
    streamingStatus = 'Thinking'
    streamBubbles = []
    streamingRaw = ''

    const es = new EventSource('/runs/' + runId + '/events')
    eventSource = es

    es.addEventListener('response_start', () => {
      if (streamBubbles.length > 0) {
        for (const bubble of streamBubbles) {
          if (bubble && bubble.trim()) {
            messages = [...messages, { role: 'assistant', content: bubble }]
          }
        }
      }
      streamingStatus = ''
      streamingRaw = ''
      streamBubbles = ['']
    })

    es.addEventListener('token', (e) => {
      streamingStatus = ''
      streamingRaw += e.data
      if (streamBubbles.length === 0) {
        streamBubbles = ['']
      }
      try {
        const html = marked.parse(streamingRaw)
        streamBubbles[streamBubbles.length - 1] = html
      } catch (err) {
        streamBubbles[streamBubbles.length - 1] = streamingRaw
      }
      scrollDown()
    })

    es.addEventListener('status', (e) => {
      streamingStatus = e.data
    })

    es.addEventListener('done', (e) => {
      es.close()
      eventSource = null
      finalizeStream(e.data)
      refreshConversationMeta()
    })

    es.addEventListener('error', (e) => {
      if (es.readyState === EventSource.CLOSED) return
      es.close()
      eventSource = null
      if (e.data) {
        finalizeStream(e.data)
      } else {
        streamingStatus = ''
        streamBubbles = []
        streamingRaw = ''
      }
      activeRunId = ''
      refreshConversationMeta()
    })

    es.onerror = () => {
      es.close()
      eventSource = null
      activeRunId = ''
    }
  }

  function finalizeStream(mdText) {
    streamingStatus = ''
    if (streamBubbles.length > 0) {
      streamBubbles[streamBubbles.length - 1] = marked.parse(mdText || '')
      for (let i = 0; i < streamBubbles.length; i++) {
        messages = [...messages, { role: 'assistant', content: streamBubbles[i] }]
      }
    }
    streamBubbles = []
    streamingRaw = ''
    activeRunId = ''
    requestAnimationFrame(() => scrollDown())
  }

  async function refreshConversationMeta() {
    try {
      const list = await api.get('/api/conversations')
      conversations = list
    } catch {}
  }

  function scrollDown() {
    if (!messageListEl) return
    messageListEl.scrollTop = messageListEl.scrollHeight
  }

  function navigate(href) {
    if (window.history.length) {
      window.history.replaceState({}, '', href)
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }
</script>

<aside class="sidebar" class:collapsed={!sidebarOpen}>
  <div class="sidebar-inner">
    <div class="sidebar-section">
      <button class="new-chat-btn" onclick={newConversation}>
        <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor" style="width:16px;height:16px;">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
        </svg>
        New chat
      </button>
    </div>
    <div class="sidebar-section" style="padding-top:0;">
      <div class="sb-section-label">Conversations</div>
    </div>
    <div class="session-list">
      {#if loading}
        <div class="session-empty">Loading…</div>
      {:else if conversations.length === 0}
        <div class="session-empty">No chats yet</div>
      {:else}
        {#each conversations as c}
          <button
            class="session-row"
            class:active={currentConv?.id === c.id}
            onclick={() => selectConversation(c)}
          >
            <span class="session-name">{c.title}</span>
            <span class="session-preview">{c.message_count} messages</span>
            <span class="session-del" onclick={(e) => { e.stopPropagation(); deleteConversation(c) }} title="Delete">✕</span>
          </button>
        {/each}
      {/if}
    </div>
  </div>
</aside>

<main class="chat">
  {#if currentConv}
    <div class="chat-layout">
      <div class="chat-head">
        <button class="sidebar-toggle-btn" onclick={() => sidebarOpen = !sidebarOpen} aria-label="Toggle sidebar">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/>
          </svg>
        </button>
        <span class="chat-head-name">{currentConv.title}</span>
        <span class="chat-head-badge">{messages.length} messages</span>
      </div>

      <div class="chat-body" bind:this={messageListEl}>
        {#each messages as msg}
          <div class="msg-row" class:msg-right={msg.role === 'user'} class:msg-left={msg.role !== 'user'}>
            <div class="bubble" class:bubble-user={msg.role === 'user'} class:bubble-bot={msg.role !== 'user'}>
              {#if msg.role === 'user'}
                {msg.content}
              {:else}
                {@html msg.content}
              {/if}
            </div>
          </div>
        {/each}

        {#each streamBubbles as content}
          <div class="msg-row msg-left">
            <div class="bubble bubble-bot">
              {@html content || ''}
            </div>
          </div>
        {/each}

        {#if streamingStatus}
          <div class="msg-row msg-left">
            <div class="thinking-bubble">
              <span class="thinking-label">{streamingStatus}</span>
              <div class="thinking-dot"></div>
              <div class="thinking-dot" style="animation-delay:0.2s"></div>
              <div class="thinking-dot" style="animation-delay:0.4s"></div>
            </div>
          </div>
        {/if}

        <div class="scroll-anchor"></div>
      </div>

      <div class="chat-foot">
        <div class="input-wrap">
          <textarea
            value={newMessage}
            onkeydown={handleKeydown}
            rows="1"
            placeholder="Message…"
            oninput={(e) => { newMessage = e.target.value; e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 140) + 'px'; }}
          ></textarea>
          <button onclick={sendMessage} class="send-btn" aria-label="Send" disabled={!currentConv}>
            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 12 3.269 3.125A59.769 59.769 0 0 1 21.485 12 59.768 59.768 0 0 1 3.27 20.875L5.999 12Zm0 0h7.5" />
            </svg>
          </button>
        </div>
      </div>
    </div>
  {:else}
    <div class="chat-layout">
      <div class="chat-head">
        <button class="sidebar-toggle-btn" onclick={() => sidebarOpen = !sidebarOpen} aria-label="Toggle sidebar">
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/>
          </svg>
        </button>
      </div>
      <div class="empty-state">
        <div class="empty-icon">
          <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
          </svg>
        </div>
        <p class="empty-title">No chat selected</p>
        <p class="empty-sub">Start a new chat from the sidebar.</p>
      </div>
    </div>
  {/if}
</main>

<style>
  .sidebar {
    background: var(--bg-sidebar);
    border-right: 1px solid var(--border);
    width: 260px;
    min-width: 260px;
    overflow: hidden;
    transition: width 0.2s ease, min-width 0.2s ease;
  }
  .sidebar.collapsed {
    width: 0; min-width: 0; border-right: none;
  }
  .sidebar-inner {
    display: flex; flex-direction: column; height: 100%;
  }
  .sidebar-section {
    padding: 16px 12px 10px;
  }
  .sb-section-label {
    font-size: 0.65rem; font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.08em; color: #52525b; margin-bottom: 6px; padding: 0 4px;
  }
  .new-chat-btn {
    width: 100%; display: flex; align-items: center; justify-content: center; gap: 6px;
    padding: 9px 12px; border-radius: 9px; border: 1px solid var(--border);
    background: var(--purple-dim); color: var(--text-base); cursor: pointer;
    font-family: inherit; font-size: 0.85rem; font-weight: 500;
    transition: background 0.12s, border-color 0.12s;
  }
  .new-chat-btn:hover { background: oklch(34% 0.14 292.7); border-color: oklch(59.1% 0.249 292.7 / 0.4); }
  .session-list {
    flex: 1; overflow-y: auto; padding: 4px 12px;
  }
  .session-empty {
    padding: 16px 4px; color: var(--text-muted); font-size: 0.82rem;
  }
  .session-row {
    display: flex; flex-direction: column; position: relative;
    padding: 8px 10px; border-radius: 8px;
    cursor: pointer; border: none; background: none; color: inherit;
    width: 100%; text-align: left; font-family: inherit;
    transition: background 0.12s, border-color 0.12s;
    border: 1px solid transparent; margin: 2px 0;
  }
  .session-row:hover { background: var(--bg-card); color: var(--text-base); }
  .session-row:hover .session-del { opacity: 1; }
  .session-row.active {
    background: var(--purple-dim);
    border-color: oklch(59.1% 0.249 292.7 / 0.25);
    color: var(--text-base);
  }
  .session-name { font-size: 0.82rem; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding-right: 16px; }
  .session-preview { font-size: 0.72rem; color: var(--text-muted); margin-top: 2px; }
  .session-del {
    position: absolute; top: 50%; right: 6px; transform: translateY(-50%);
    font-size: 0.7rem; color: var(--text-muted); opacity: 0;
    cursor: pointer; padding: 2px 4px; border-radius: 4px; transition: opacity 0.1s, background 0.1s;
  }
  .session-del:hover { background: oklch(20% 0.04 0); color: oklch(70% 0.2 20); }

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

  .chat-foot {
    padding: 16px 24px; border-top: 1px solid var(--border);
    background: var(--bg-base); flex-shrink: 0;
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

  .sidebar-toggle-btn {
    flex-shrink: 0; width: 30px; height: 30px; border-radius: 7px;
    border: 1px solid var(--border); background: var(--bg-card); color: var(--text-muted);
    cursor: pointer; display: flex; align-items: center; justify-content: center;
    transition: background 0.12s, color 0.12s;
  }
  .sidebar-toggle-btn:hover { background: var(--purple-dim); color: var(--text-base); }
  .sidebar-toggle-btn svg { width: 14px; height: 14px; pointer-events: none; }

  .thinking-bubble {
    background: var(--bg-card); border: 1px solid var(--border); border-bottom-left-radius: 4px;
    padding: 14px 18px; border-radius: 14px;
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
  .empty-icon svg { width: 56px; height: 56px; }
  .empty-title { font-size: 1.05rem; font-weight: 600; color: var(--text-base); margin: 14px 0 4px; }
  .empty-sub { font-size: 0.85rem; margin: 0; }
</style>