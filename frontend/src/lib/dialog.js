export function dialog(node, close) {
  const previous = document.activeElement
  const controls = () => [...node.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]')]
  const timer = setTimeout(() => (controls()[0] || node).focus(), 0)
  const keydown = (event) => {
    if (event.key === 'Escape') { event.preventDefault(); close(); return }
    if (event.key !== 'Tab') return
    const items = controls()
    if (!items.length) { event.preventDefault(); node.focus(); return }
    const first = items[0], last = items[items.length - 1]
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
  }
  node.addEventListener('keydown', keydown)
  return { destroy() { clearTimeout(timer); node.removeEventListener('keydown', keydown); if (previous?.isConnected) previous.focus() } }
}
