import { test, expect } from '@playwright/test'

async function setup(page, initialMessages = []) {
  const conversation = {
    id: 'c1', title: 'Test conversation', messages: initialMessages,
    created_at: '2026-10-19T10:00:00Z', updated_at: '2026-10-19T10:00:00Z',
    total_messages: initialMessages.length, pending_message_ids: [],
  }
  const posts = []
  await page.addInitScript(() => {
    window.testStreams = []
    window.EventSource = class extends EventTarget {
      static CLOSED = 2
      constructor(url) {
        super()
        this.url = url
        this.readyState = 1
        window.testStreams.push(this)
      }
      close() { this.readyState = 2 }
    }
  })
  await page.route('**/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.pathname.endsWith('/messages') && request.method() === 'POST') {
      posts.push(request.postDataJSON())
      if (posts.length === 1) return route.fulfill({ status: 503, json: { error: 'Temporary outage' } })
      conversation.messages = [...initialMessages, { id: 3, role: 'user', channel: 'web', content: posts[1].content, source_id: posts[1].client_id }]
      conversation.pending_message_ids = [3]
      conversation.total_messages = conversation.messages.length
      return route.fulfill({ status: 202, json: { message_id: 3, queued: true } })
    }
    let value = {}
    if (url.pathname === '/api/conversations') value = [{ id: 'c1' }]
    else if (url.pathname === '/api/conversations/primary') value = { id: 'c1' }
    else if (url.pathname === '/api/conversations/c1') value = conversation
    else if (url.pathname === '/api/tasks' || url.pathname.includes('/accounts') || url.pathname.includes('/triggers')) value = []
    else if (url.pathname === '/api/identities') value = { identities: [] }
    await route.fulfill({ json: value })
  })
  await page.goto('/')
  await expect(page.getByText('Test conversation', { exact: true })).toBeVisible()
  return { conversation, posts }
}

test('named hub events update the visible conversation', async ({ page }) => {
  await setup(page)
  await page.evaluate(() => {
    const stream = window.testStreams.find(stream => stream.url === '/api/events')
    stream.dispatchEvent(new MessageEvent('event', { data: JSON.stringify({ type: 'message', conversation_id: 'c1', data: { id: 2, role: 'assistant', content: 'Live channel message' } }) }))
  })
  await expect(page.getByText('Live channel message', { exact: true })).toBeVisible()
})

test('retry preserves the message idempotency key and does not duplicate the bubble', async ({ page }) => {
  const { posts } = await setup(page)
  await page.getByRole('textbox', { name: 'Message to Eve' }).fill('Please keep this message')
  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByRole('status')).toContainText('Could not confirm delivery')
  await expect(page.getByRole('textbox', { name: 'Message to Eve' })).toHaveValue('Please keep this message')
  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByRole('status')).toContainText('Queued')
  expect(posts).toHaveLength(2)
  expect(posts[0].client_id).toBe(posts[1].client_id)
  await expect(page.locator('.bubble-user').filter({ hasText: 'Please keep this message' })).toHaveCount(1)
})

test('leaving chat cancels a pending global-event reconnect', async ({ page }) => {
  await setup(page)
  await page.clock.install()
  await page.evaluate(() => window.testStreams.find(stream => stream.url === '/api/events').onerror())
  await page.getByRole('button', { name: 'Email', exact: true }).click()
  await page.clock.fastForward(5000)
  const streams = await page.evaluate(() => window.testStreams.filter(stream => stream.url === '/api/events').length)
  expect(streams).toBe(1)
})

test('markdown is sanitized and plain CSS styles code blocks', async ({ page }) => {
  await setup(page, [{ id: 1, role: 'assistant', content: 'Safe text\n\n```js\nconst x = 1\n```\n<script>window.eveUnsafe = true</script>' }])
  await expect(page.locator('.bubble pre')).toBeVisible()
  expect(await page.evaluate(() => window.eveUnsafe)).toBeUndefined()
  const border = await page.locator('.bubble pre').evaluate(node => getComputedStyle(node).borderTopWidth)
  expect(border).toBe('1px')
})

test('identity dialog supports Escape and restores focus', async ({ page }) => {
  await setup(page)
  await page.getByRole('button', { name: 'Identities', exact: true }).click()
  const add = page.getByRole('button', { name: 'Add identity', exact: true })
  await add.click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(add).toBeFocused()
})

test('terminal task results remain available after archiving', async ({ page }) => {
  await setup(page)
  const task = { id: 'finished', agent_name: 'Archive example', status: 'completed', reported: true, message: 'Archive this result', result: 'Saved task result', updated_at: '2026-10-19T10:00:00Z' }
  let archived = false
  await page.route('**/api/tasks**', async route => {
    if (route.request().method() === 'POST') { archived = true; return route.fulfill({ status: 204 }) }
    const path = new URL(route.request().url()).pathname
    await route.fulfill({ json: path === '/api/tasks/archive' ? (archived ? [{ ...task, archived: true }] : []) : (archived ? [] : [task]) })
  })
  await page.getByRole('button', { name: 'Tasks', exact: true }).click()
  await expect(page.getByText('Saved task result', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Archive task', exact: true }).click()
  await expect(page.getByText('No background tasks yet.', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Archived tasks', exact: true }).click()
  await expect(page.getByText('Saved task result', { exact: true })).toBeVisible()
})
