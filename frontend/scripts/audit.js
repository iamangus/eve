import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { assessAudit } from './audit-policy.js'

const result = spawnSync('npm', ['audit', '--json'], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 })
try {
  if (result.error || result.signal || ![0, 1].includes(result.status)) throw new Error('npm audit did not complete successfully')
  const report = JSON.parse(result.stdout)
  const lock = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url), 'utf8'))
  if (!assessAudit(report, lock)) {
    console.error(result.stdout)
    throw new Error('Audit failed: an unapproved advisory, production dependency, available fix, expired exception, or invalid report requires review')
  }
  console.log(report.metadata.vulnerabilities.total ? 'Audit passed with approved build-only exception GHSA-gcqx-w48m-w2h4, expiring 2026-11-28. No other advisories are allowed.' : 'Audit passed: no vulnerabilities.')
} catch (error) {
  console.error(error.message)
  if (result.stderr) console.error(result.stderr)
  process.exitCode = 1
}
