async function request(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    window.location.href = '/login'
    return
  }
  if (!res.ok) {
    let msg = res.statusText
    try { const e = await res.json(); if (e?.error) msg = e.error } catch {}
    throw new Error(msg)
  }
  if (res.status === 204) return null
  const ct = res.headers.get('Content-Type') || ''
  if (!ct.includes('application/json')) return null
  return res.json()
}

export const api = {
  get: (p) => request('GET', p),
  post: (p, body) => request('POST', p, body),
  del: (p) => request('DELETE', p),
}