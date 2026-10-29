const advisory = 'https://github.com/advisories/GHSA-gcqx-w48m-w2h4'
const expires = Date.parse('2026-11-28T00:00:00Z')
const packages = new Set(['deepmerge', '@sveltejs/vite-plugin-svelte', '@sveltejs/vite-plugin-svelte-inspector'])

export function assessAudit(report, lock, now = Date.now()) {
  const findings = report?.vulnerabilities
  if (report?.error || report?.auditReportVersion !== 2 || !findings || typeof findings !== 'object' || !report?.metadata?.vulnerabilities) return false
  const entries = Object.entries(findings)
  if (!entries.length) return report.metadata.vulnerabilities.total === 0
  if (now >= expires || findings.deepmerge?.fixAvailable !== false) return false
  if (report.metadata.vulnerabilities.total !== entries.length) return false
  let sources = 0
  for (const [name, finding] of entries) {
    if (!packages.has(name) || finding.name !== name || finding.severity !== 'low' || !finding.nodes?.length || !finding.via?.length) return false
    if (!finding.nodes.every(node => lock?.packages?.[node]?.dev === true)) return false
    for (const via of finding.via) {
      if (typeof via === 'string') {
        if (!packages.has(via) || !findings[via]) return false
      } else {
        if (via?.url !== advisory || via?.dependency !== 'deepmerge' || via?.severity !== 'low') return false
        sources++
      }
    }
  }
  return sources === 1
}
