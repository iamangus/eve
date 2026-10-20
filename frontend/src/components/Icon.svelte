<script>
  let { name, size = 20 } = $props()
  const paths = {
    chat: 'M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z',
    tasks: 'm4 6 1.5 1.5L8 4M11 6h9M4 12l1.5 1.5L8 10M11 12h9M4 18l1.5 1.5L8 16M11 18h9',
    email: 'M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1Zm-1 1 9 7 9-7',
    context: 'M9 18H7a4 4 0 0 1-3.5-6 4 4 0 0 1 1.3-6A4 4 0 0 1 12 4v16a3 3 0 0 1-5 1m8-3h2a4 4 0 0 0 3.5-6 4 4 0 0 0-1.3-6A4 4 0 0 0 12 4v16a3 3 0 0 0 5 1M8 10a4 4 0 0 1 4 4m4-4a4 4 0 0 0-4 4',
    channels: 'M12 3v6m0 6v6M3 12h6m6 0h6M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6M12 1a2 2 0 1 0 0 4 2 2 0 0 0 0-4M1 12a2 2 0 1 0 4 0 2 2 0 0 0-4 0m20-2a2 2 0 1 0 0 4 2 2 0 0 0 0-4m-9 9a2 2 0 1 0 0 4 2 2 0 0 0 0-4',
    identities: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m7-18a4 4 0 1 0 0 8 4 4 0 0 0 0-8m7 1a4 4 0 0 1 0 8m6 9v-2a4 4 0 0 0-3-3.9',
    sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5',
    moon: 'M20.9 13A9 9 0 0 1 11 3.1 9 9 0 1 0 20.9 13Z',
    system: 'M3 4h18v13H3V4Zm5 17h8m-4-4v4',
    menu: 'M4 6h16M4 12h16M4 18h16',
    close: 'm6 6 12 12M6 18 18 6',
    arrow: 'M12 19V5m-6 6 6-6 6 6',
    download: 'M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5',
    plus: 'M12 5v14M5 12h14',
    chevron: 'm9 5 7 7-7 7',
    check: 'm5 12 4 4L19 6',
    archive: 'M3 4h18v4H3V4Zm2 4v13h14V8m-9 5h4',
    clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18m0 4v5l3 2',
    refresh: 'M20 7v5h-5M4 17v-5h5m-4-4a8 8 0 0 1 13-4l2 3M4 17l2 3a8 8 0 0 0 13-4',
    info: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18m0 8v6m0-10h.01',
    sparkle: 'm12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3Z',
    warning: 'm12 3 10 18H2L12 3Zm0 5v6m0 3h.01',
  }
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d={paths[name] || paths.sparkle} /></svg>
