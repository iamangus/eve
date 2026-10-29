import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { installWorkspace } from './fixtures.js'

test('reply metrics expand without inventing provider timings', async ({ page }, testInfo) => {
  await installWorkspace(page)
  await page.goto('/')
  await page.getByText('Inference metrics', { exact: true }).click()
  await expect(page.getByText('All attempts, including retries', { exact: false })).toBeVisible()
  await page.locator('.call > summary').click()
  await expect(page.getByText('Prefill duration', { exact: true })).toBeVisible()
  await expect(page.locator('.call dt').filter({ hasText: 'Prefill duration' }).locator('..')).toContainText('Unavailable')
  await expect(page.locator('.call dt').filter({ hasText: 'Cached input tokens' }).locator('..')).toContainText('80')
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()
  expect(result.violations).toEqual([])
  await page.screenshot({ path: testInfo.outputPath('reply-metrics.png'), fullPage: true })
})

test('provider health identifies server-wide metrics', async ({ page }) => {
  await installWorkspace(page)
  await page.goto('/inference')
  await expect(page.getByText('Reporting', { exact: true })).toBeVisible()
  await expect(page.getByText('custom · server aggregate', { exact: true })).toBeVisible()
  await expect(page.getByText('0', { exact: true })).toBeVisible()
  await expect(page.getByText('This is not per-request prefill speed.', { exact: false })).toBeVisible()
})
