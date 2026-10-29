import { test } from 'node:test'
import assert from 'node:assert/strict'
import { assessAudit } from '../../scripts/audit-policy.js'

const now = Date.parse('2026-10-29T00:00:00Z')
function fixture() {
  return {
    report: { auditReportVersion: 2, metadata: { vulnerabilities: { total: 1 } }, vulnerabilities: { deepmerge: { name: 'deepmerge', severity: 'low', fixAvailable: false, nodes: ['node_modules/deepmerge'], via: [{ dependency: 'deepmerge', severity: 'low', url: 'https://github.com/advisories/GHSA-gcqx-w48m-w2h4' }] } } },
    lock: { packages: { 'node_modules/deepmerge': { dev: true } } },
  }
}

test('audit permits only the approved build-only advisory before expiry', () => {
  const { report, lock } = fixture()
  assert.equal(assessAudit(report, lock, now), true)
  assert.equal(assessAudit(report, lock, Date.parse('2026-11-28T00:00:00Z')), false)
  assert.equal(assessAudit({ auditReportVersion: 2, metadata: { vulnerabilities: { total: 0 } }, vulnerabilities: {} }, lock, now), true)
})

test('audit rejects new advisories, production exposure and available fixes', () => {
  for (const mutate of [
    (r, l) => { l.packages['node_modules/deepmerge'].dev = false },
    r => { r.vulnerabilities.deepmerge.via[0].url = 'https://github.com/advisories/another' },
    r => { r.vulnerabilities.deepmerge.severity = 'high' },
    r => { r.vulnerabilities.deepmerge.fixAvailable = true },
    r => { r.vulnerabilities.deepmerge.via.push('unknown') },
    r => { r.metadata.vulnerabilities.total = 2 },
    r => { r.error = { message: 'registry unavailable' } },
  ]) {
    const { report, lock } = fixture()
    mutate(report, lock)
    assert.equal(assessAudit(report, lock, now), false)
  }
  assert.equal(assessAudit({}, {}, now), false)
})
