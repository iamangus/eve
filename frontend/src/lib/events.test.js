import { test } from 'node:test'
import assert from 'node:assert/strict'
import { HUB_EVENT, parseHubEvent } from './events.js'

test('hub contract uses named events and conversation_id', () => {
  assert.equal(HUB_EVENT, 'event')
  assert.equal(parseHubEvent('{"type":"message","conversation_id":"c1","data":{"id":2}}').conversation_id, 'c1')
  assert.equal(parseHubEvent('{"type":"message","conv_id":"c1","data":{"id":2}}'), null)
  assert.equal(parseHubEvent('broken'), null)
  assert.equal(parseHubEvent('{"type":"channels"}').type, 'channels')
})
