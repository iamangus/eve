import { test } from 'node:test'
import assert from 'node:assert/strict'
import { api } from './api.js'

test('API surfaces unauthorized responses without a nonexistent login redirect', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response('', { status: 401 })
    await assert.rejects(api.get('/api/tasks'), /Access denied/)
    globalThis.fetch = async () => new Response(null, { status: 204 })
    assert.equal(await api.del('/api/conversations/c1'), null)
    globalThis.fetch = async () => Response.json({ error: 'disk full' }, { status: 500 })
    await assert.rejects(api.post('/api/conversations', {}), /disk full/)
  } finally { globalThis.fetch = original }
})
