export function scrollRegion(element) {
  const update = () => { element.tabIndex = element.scrollHeight > element.clientHeight || element.scrollWidth > element.clientWidth ? 0 : -1 }
  const observer = new ResizeObserver(update)
  observer.observe(element)
  if (element.firstElementChild) observer.observe(element.firstElementChild)
  update()
  return { destroy() { observer.disconnect() } }
}
