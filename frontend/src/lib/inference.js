export const metric = (value, unit = '') => value == null || !Number.isFinite(value) ? 'Unavailable' : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}${unit}`
export const percent = value => value == null ? 'Unavailable' : metric(value * 100, '%')

export function summarize(attempts) {
  const sum = key => attempts.length && attempts.every(a => a.metrics?.[key] != null) ? attempts.reduce((n, a) => n + a.metrics[key], 0) : null
  const input = sum('input_tokens')
  const cached = sum('cached_input_tokens')
  return { input, output: sum('output_tokens'), cache: input > 0 && cached != null ? cached / input : null }
}

export function callMetrics(m) {
  return [
    ['Input tokens', metric(m.input_tokens)], ['Output tokens', metric(m.output_tokens)],
    ['Cached input tokens', metric(m.cached_input_tokens)], ['Cache hit ratio', percent(m.cache_hit_ratio)],
    ['Time to first token', metric(m.time_to_first_token_ms, ' ms')], ['First visible text', metric(m.time_to_first_visible_token_ms, ' ms')],
    ['Observed generation', metric(m.observed_output_tokens_per_second, ' tok/s')], ['Total duration', metric(m.duration_ms, ' ms')],
    ['Time to start prefill', metric(m.time_to_start_prefill_ms, ' ms')], ['Prefill duration', metric(m.prefill_ms, ' ms')],
    ['Prefill throughput', metric(m.prefill_tokens_per_second, ' tok/s')], ['Reasoning tokens', metric(m.reasoning_tokens)],
  ]
}
