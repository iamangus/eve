// Stable client identities and idempotent reconciliation for the chat list.
//
// The rendered chat list is keyed by msg._clientId (see Chat.svelte), so a
// key must exist for every message, never change once assigned, and never
// collide — otherwise Svelte reuses a DOM row for a different message and a
// new send visually replaces prior chat entries.
//
// Identity sources, in order:
//   1. server id  -> "server:<id>"        (persisted message)
//   2. correlation id -> "correlation:<id>" alias, canonical "client:<n>"
//   3. monotonic client sequence -> "client:<n>" (optimistic/streamed)
//
// Snapshot aliases ("snapshot:<role|content|channel|created_at>") are only
// an index for id-less refresh batches and are never used to match a server
// message, so two distinct id-less messages with identical text keep
// separate identities.
export function createMessageState() {
  let sequence = 0

  function identity(message) {
    if (message._clientId) return message._clientId
    if (message.id !== undefined && message.id !== null) return 'server:' + message.id
    sequence += 1
    return 'client:' + sequence
  }

  function correlation(message) {
    return message.client_id || message.clientId || message.message_id || message.event_id || message.eventId || null
  }

  function messageKey(message) {
    return JSON.stringify([
      message.role || '',
      message.content || '',
      message.channel || '',
      message.created_at || '',
    ])
  }

  function normalize(message) {
    const aliases = new Set(message._aliases || [])
    const correlationId = correlation(message)
    if (correlationId) aliases.add('correlation:' + correlationId)
    if (message.id !== undefined && message.id !== null) aliases.add('server:' + message.id)
    // The snapshot alias is only used as a bucket for id-less snapshots. It is
    // never consulted when matching a server message, so equal text cannot
    // acquire the identity of a different persisted message.
    aliases.add('snapshot:' + messageKey(message))
    return { ...message, _clientId: identity(message), _aliases: [...aliases] }
  }

  function merge(existing, incoming) {
    const result = existing.map(normalize)
    const byIdentity = new Map()
    const index = (message) => {
      byIdentity.set(message._clientId, message)
      for (const alias of message._aliases || []) byIdentity.set(alias, message)
    }
    result.forEach(index)

    for (const raw of incoming || []) {
      const message = normalize(raw)
      let match = byIdentity.get(message._clientId)

      // A persisted snapshot can acquire its id after an optimistic/id-less
      // entry was already rendered. This transition must be unambiguous: an
      // incoming id message consumes the newest still-id-less candidate with
      // equal role/content and the same id can never be handed out twice
      // (_upgradedId marks the winner). Persisted ids may therefore arrive in
      // any order without swapping, dropping, or duplicating visible rows.
      if (!match && message.id !== undefined && message.id !== null) {
        for (let i = result.length - 1; i >= 0; i--) {
          const candidate = result[i]
          if (candidate._upgradedId || candidate.id !== undefined && candidate.id !== null) continue
          if (candidate.role !== message.role || candidate.content !== message.content) continue
          candidate._upgradedId = message.id
          match = candidate
          break
        }
      }

      if (match) {
        const clientId = match._clientId
        const aliases = new Set([
          ...(match._aliases || []),
          ...(message._aliases || []),
          message._clientId,
        ])
        // Keep every persisted id resolvable on later refreshes so the same
        // server message can never append a duplicate.
        if (match.id !== undefined && match.id !== null) aliases.add('server:' + match.id)
        if (message.id !== undefined && message.id !== null) aliases.add('server:' + message.id)
        const nextId = message.id !== undefined && message.id !== null ? message.id : match.id
        Object.assign(match, message, {
          _clientId: clientId,
          _aliases: [...aliases],
          _upgradedId: match._upgradedId,
          id: nextId,
        })
        index(match)
        continue
      }

      // Chronological insertion when the entry carries a parseable timestamp;
      // otherwise append (optimistic/streamed entries always belong at the
      // end until their persisted shape arrives).
      const time = message.created_at ? new Date(message.created_at).getTime() : NaN
      let insertAt = result.length
      if (Number.isFinite(time)) {
        const later = result.findIndex((item) => {
          const itemTime = item.created_at ? new Date(item.created_at).getTime() : NaN
          return Number.isFinite(itemTime) && itemTime > time
        })
        if (later >= 0) insertAt = later
      }
      result.splice(insertAt, 0, message)
      index(message)
    }
    return result
  }

  return { merge, normalize }
}

export const defaultMessageState = createMessageState()
export const mergeMessages = defaultMessageState.merge
export const normalizeMessage = defaultMessageState.normalize

// isCurrentHeal reports whether a heal response computed for (heal, epoch,
// send, convId) may still be applied to the visible conversation. Every
// generation must match exactly: any newer selection, run, or send invalidates
// the outstanding heal.
export function isCurrentHeal({ heal, currentHeal, epoch, currentEpoch, send, currentSend, convId, currentConvId }) {
  return heal === currentHeal && epoch === currentEpoch && send === currentSend && convId === currentConvId
}
