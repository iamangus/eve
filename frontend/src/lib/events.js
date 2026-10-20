export const HUB_EVENT = 'event'

export function parseHubEvent(data) {
  try {
    const event = JSON.parse(data)
    if (!event || typeof event.type !== 'string') return null
    if (event.type === 'message' && (typeof event.conversation_id !== 'string' || !event.data?.id)) return null
    return event
  } catch {
    return null
  }
}
