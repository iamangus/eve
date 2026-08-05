<script>
  import { api } from '../lib/api.js'
  import { marked } from 'marked'
  marked.setOptions({ gfm: true, breaks: true })

  let currentConv = $state(null)
  let messages = $state([])
  let newMessage = $state('')
  let loading = $state(true)
  let messageListEl = $state(null)

  let streamBubbles = $state([])
  let streamingRaw = ''
  let streamingStatus = $state('')
  let activeRunId = $state('')
  let eventSource = $state(null)

  $effect(() => {
    loadPrimary()
    return () => {
      eventSource?.close()
    }
  })

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
        navigate('/?conv=' + conv.id)
      }
      await selectConversation(conv)
    } catch (e) {
      console.error('Failed to load conversation', e)
    } finally {
      loading = false
    }
  }

  async function selectConversation(conv) {
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
      if (list.length > 0) {
        const full = await api.get('/api/conversations/' + list[0].id)
        currentConv = full
      }
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
      </div>

      <div class="chat-body" bind:this={messageListEl}>
        {#each messages as msg, i (msg.id || i)}
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
    <div class="empty-state">
      <p class="empty-title">No chat selected</p>
      <p class="empty-sub">Start a new chat from the sidebar.</p>
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
  .empty-title { font-size: 1.05rem; font-weight: 600; color: var(--text-base); margin: 14px 0 4px; }
  .empty-sub { font-size: 0.85rem; margin: 0; }
</style>
