import { test } from 'node:test'
import assert from 'node:assert/strict'
import { metric, percent, summarize } from './inference.js'

test('missing is different from measured zero', () => {
  assert.equal(metric(null), 'Unavailable')
  assert.equal(metric(0), '0')
  assert.equal(percent(0), '0%')
})

test('cache ratio uses weighted tokens across all attempts', () => {
  const result = summarize([{ metrics: { input_tokens: 100, output_tokens: 10, cached_input_tokens: 50 } }, { metrics: { input_tokens: 900, output_tokens: 20, cached_input_tokens: 0 } }])
  assert.deepEqual(result, { input: 1000, output: 30, cache: .05 })
  assert.equal(summarize([{ metrics: { input_tokens: 100 } }]).output, null)
  assert.equal(summarize([]).input, null)
})
