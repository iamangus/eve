// Focused regression coverage for the chat message lifecycle fix in
// frontend/src/pages/Chat.svelte and frontend/src/lib/chatMessageState.js.
//
// The rendered chat list is keyed by msg._clientId. These tests assert that
// every visible message keeps a stable, collision-free identity while the
// optimistic send, streaming completion, error/heal, and server refresh paths
// merge state — so no prior entry can be dropped, duplicated, or visually
// replaced, and rendered keys stay unique and stable.
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizeMessage,
  mergeMessages,
  isCurrentHeal,
} from '../src/lib/chatMessageState.js'

const keys = (messages) => messages.map((message) => message._clientId)
const uniqueKeys = (messages) => new Set(keys(messages)).size

test('prior history survives optimistic send, streaming done, error/heal, and refresh exactly once', () => {
  const prior = mergeMessages([], [
    { id: 1, created_at: '2026-01-01T10:00:00Z', role: 'user', content: 'first question' },
    { id: 2, created_at: '2026-01-01T10:00:05Z', role: 'assistant', content: 'first answer' },
  ])

  // 1. Optimistic send appends the user entry without touching prior entries.
  const afterSend = mergeMessages(prior, [{ role: 'user', content: 'second question' }])
  const keysAfterSend = keys(afterSend)
  assert.deepEqual(keysAfterSend.slice(0, 2), keys(prior))
  assert.equal(afterSend.length, 3)

  // 2. Streaming done appends the persisted assistant reply.
  const afterDone = mergeMessages(afterSend, [
    { id: 3, created_at: '2026-01-01T10:01:00Z', role: 'user', content: 'second question' },
    { id: 4, created_at: '2026-01-01T10:01:10Z', role: 'assistant', content: 'second answer' },
  ])
  assert.deepEqual(keys(afterDone).slice(0, 3), keysAfterSend)
  assert.equal(afterDone.length, 4)
  assert.equal(uniqueKeys(afterDone), 4)

  // 3. Failure/heal replays the persisted snapshot of the same run.
  const afterHeal = mergeMessages(afterDone, [
    { id: 1, created_at: '2026-01-01T10:00:00Z', role: 'user', content: 'first question' },
    { id: 2, created_at: '2026-01-01T10:00:05Z', role: 'assistant', content: 'first answer' },
    { id: 3, created_at: '2026-01-01T10:01:00Z', role: 'user', content: 'second question' },
    { id: 4, created_at: '2026-01-01T10:01:10Z', role: 'assistant', content: 'second answer' },
  ])
  assert.deepEqual(keys(afterHeal), keys(afterDone))
  assert.equal(afterHeal.length, 4)

  // 4. Refresh of conversation metadata is idempotent.
  const refreshed = mergeMessages(afterHeal, [
    { id: 1, created_at: '2026-01-01T10:00:00Z', role: 'user', content: 'first question' },
    { id: 2, created_at: '2026-01-01T10:00:05Z', role: 'assistant', content: 'first answer' },
    { id: 3, created_at: '2026-01-01T10:01:00Z', role: 'user', content: 'second question' },
    { id: 4, created_at: '2026-01-01T10:01:10Z', role: 'assistant', content: 'second answer' },
  ])
  assert.deepEqual(keys(refreshed), keys(afterDone))
  assert.equal(refreshed.length, 4)
})

test('rendered keys are unique and stable across repeated server refreshes', () => {
  const snapshot = [
    { id: 7, created_at: '2026-02-01T09:00:00Z', role: 'user', content: 'hello' },
    { id: 8, created_at: '2026-02-01T09:00:10Z', role: 'assistant', content: 'hi there' },
  ]
  const loaded = mergeMessages([], snapshot)
  const baseKeys = keys(loaded)

  const refreshed = mergeMessages(mergeMessages(loaded, snapshot), snapshot)
  assert.deepEqual(keys(refreshed), baseKeys)
  assert.equal(refreshed.length, 2)
  assert.equal(uniqueKeys(refreshed), 2)
})

test('optimistic entries and id-less server entries keep stable identities across merges', () => {
  const base = mergeMessages([], [
    { role: 'user', content: 'pending one' },
    { role: 'user', content: 'pending two' },
  ])
  const baseKeys = keys(base)
  assert.equal(uniqueKeys(base), 2)

  // Reconciling with the persisted versions must not renumber the entries.
  const reconciled = mergeMessages(base, [
    { id: 21, role: 'user', content: 'pending one' },
    { id: 22, role: 'user', content: 'pending two' },
  ])
  const reconciledKeys = keys(reconciled)
  assert.deepEqual(reconciledKeys, baseKeys)
  assert.equal(reconciled.length, 2)
  assert.equal(uniqueKeys(reconciled), 2)

  // The persisted ids must remain matchable on the next refresh so the same
  // server message cannot append a duplicate.
  const refreshed = mergeMessages(reconciled, [
    { id: 21, role: 'user', content: 'pending one' },
    { id: 22, role: 'user', content: 'pending two' },
  ])
  assert.deepEqual(keys(refreshed), baseKeys)
  assert.equal(refreshed.length, 2)
})

test('distinct id-less messages with equal role, content, and timestamp keep separate keys', () => {
  const snapshot = { role: 'user', content: 'same text', created_at: '2026-02-02T08:00:00Z' }
  const first = mergeMessages([], [snapshot])
  const second = mergeMessages(first, [snapshot])

  assert.equal(first.length, 1)
  assert.equal(second.length, 2)
  const [firstKey, secondKey] = keys(second)
  assert.equal(firstKey, keys(first)[0])
  assert.notEqual(firstKey, secondKey)
  assert.equal(uniqueKeys(second), 2)
})

test('equal-content server ids delivered out of order cannot swap visible identities', () => {
  const optimistic = mergeMessages([], [
    { role: 'user', content: 'duplicate' },
    { role: 'user', content: 'duplicate' },
  ])
  const [firstKey, secondKey] = keys(optimistic)

  const persisted = mergeMessages(optimistic, [
    { id: 31, role: 'user', content: 'duplicate' },
    { id: 32, role: 'user', content: 'duplicate' },
  ])
  assert.deepEqual(keys(persisted), [firstKey, secondKey])
  assert.equal(persisted.length, 2)
  assert.equal(uniqueKeys(persisted), 2)

  // Each persisted id resolves to a distinct visible entry, so a later refresh
  // cannot merge one server message into the wrong row.
  const byId = new Map(persisted.map((message) => [message.id, message]))
  assert.notEqual(byId.get(31)._clientId, byId.get(32)._clientId)
  const refreshed = mergeMessages(persisted, [{ id: 31, role: 'user', content: 'duplicate' }])
  assert.deepEqual(keys(refreshed), [firstKey, secondKey])
  assert.equal(refreshed.length, 2)
})

test('heal result merges a late id-less snapshot without replacing prior entries', () => {
  const live = mergeMessages([], [
    { id: 41, created_at: '2026-03-01T12:00:00Z', role: 'user', content: 'question' },
    { role: 'assistant', content: 'streamed partial' },
  ])
  const liveKeys = keys(live)

  const healed = mergeMessages(live, [
    { id: 42, created_at: '2026-03-01T12:00:20Z', role: 'assistant', content: 'streamed partial' },
    { id: 43, created_at: '2026-03-01T12:00:21Z', role: 'assistant', content: 'final answer' },
  ])
  assert.deepEqual(keys(healed).slice(0, 2), liveKeys)
  assert.equal(healed.length, 3)
  assert.equal(uniqueKeys(healed), 3)
})

test('normalizeMessage assigns server and client identities without array positions', () => {
  const withId = normalizeMessage({ id: 5, role: 'user', content: 'persisted' })
  assert.equal(withId._clientId, 'server:5')

  const idless = normalizeMessage({ role: 'user', content: 'optimistic' })
  assert.match(idless._clientId, /^client:/)

  const correlated = normalizeMessage({ role: 'assistant', content: 'reply', client_id: 'run-9' })
  assert.ok(correlated._aliases.includes('correlation:run-9'))
})

test('isCurrentHeal rejects stale heal, epoch, and send generations', () => {
  assert.equal(isCurrentHeal({ heal: 1, currentHeal: 1, epoch: 2, currentEpoch: 2, send: 3, currentSend: 3, convId: 'c1', currentConvId: 'c1' }), true)
  assert.equal(isCurrentHeal({ heal: 1, currentHeal: 2, epoch: 2, currentEpoch: 2, send: 3, currentSend: 3, convId: 'c1', currentConvId: 'c1' }), false)
  assert.equal(isCurrentHeal({ heal: 1, currentHeal: 1, epoch: 1, currentEpoch: 2, send: 3, currentSend: 3, convId: 'c1', currentConvId: 'c1' }), false)
  assert.equal(isCurrentHeal({ heal: 1, currentHeal: 1, epoch: 2, currentEpoch: 2, send: 2, currentSend: 3, convId: 'c1', currentConvId: 'c1' }), false)
  assert.equal(isCurrentHeal({ heal: 1, currentHeal: 1, epoch: 2, currentEpoch: 2, send: 3, currentSend: 3, convId: 'c1', currentConvId: 'c2' }), false)
})
