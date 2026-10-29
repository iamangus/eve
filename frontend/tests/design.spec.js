import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { installWorkspace } from './fixtures.js'

const screens = ['chat', 'tasks', 'inference', 'email', 'context', 'channels', 'identities']

for (const theme of ['light', 'dark']) {
  test(`${theme} workspace screens have accessible structure and contrast`, async ({ page }, testInfo) => {
    await installWorkspace(page)
    await page.goto('/')
    await page.getByRole('button', { name: `${theme[0].toUpperCase() + theme.slice(1)} theme`, exact: true }).click()
    for (const screen of screens) {
      if (screen !== 'chat') await page.getByRole('button', { name: screen[0].toUpperCase() + screen.slice(1), exact: true }).click()
      await expect(page.locator('main h1')).toBeVisible()
      await page.evaluate(() => document.fonts.ready)
      const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()
      expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([])
      await page.screenshot({ path: testInfo.outputPath(`${screen}-${theme}.png`), fullPage: true })
    }
  })
}

for (const width of [280, 320, 414]) {
  test(`navigation and all screens fit a ${width}px viewport`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 })
    await installWorkspace(page)
    await page.goto('/')
    for (const screen of screens) {
      if (screen !== 'chat') {
        await page.getByRole('button', { name: 'Open navigation', exact: true }).click()
        await page.getByRole('dialog', { name: 'Workspace navigation' }).getByRole('button', { name: screen[0].toUpperCase() + screen.slice(1), exact: true }).click()
      }
      await expect(page.locator('main h1')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      const visibleOverflow = await page.locator('main').evaluate(element => element.scrollWidth > element.clientWidth + 1)
      expect(visibleOverflow, `${screen} overflows at ${width}px`).toBe(false)
      await page.screenshot({ path: testInfo.outputPath(`${screen}-${width}.png`), fullPage: true })
    }
    await page.getByRole('button', { name: 'Open navigation', exact: true }).click()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).not.toBeVisible()
    await expect(page.getByRole('button', { name: 'Open navigation', exact: true })).toBeFocused()
  })
}

test('appearance persists, direct routes and browser history work', async ({ page }) => {
  await installWorkspace(page)
  await page.goto('/tasks')
  await expect(page.getByRole('heading', { name: 'Tasks', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Dark theme', exact: true }).click()
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.getByRole('button', { name: 'Channels', exact: true }).click()
  await expect(page).toHaveURL(/\/channels$/)
  await page.goBack()
  await expect(page.getByRole('heading', { name: 'Tasks', exact: true })).toBeVisible()
})

test('empty chat starters fill a draft without sending it', async ({ page }) => {
  await installWorkspace(page, { empty: true })
  await page.goto('/')
  await page.getByRole('button', { name: 'Plan my day', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Message to Eve' })).toHaveValue('Help me make a plan for today.')
  await expect(page.getByRole('textbox', { name: 'Message to Eve' })).toBeFocused()
  await expect(page.locator('.bubble-user')).toHaveCount(0)
})

test('task filter reveals questions that need a reply', async ({ page }) => {
  await installWorkspace(page)
  await page.goto('/tasks')
  await page.getByLabel('Filter tasks').selectOption('needs_input')
  await expect(page.getByRole('heading', { name: 'Project proposal' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Reading notes' })).toHaveCount(0)
  await expect(page.getByRole('textbox', { name: 'Reply to Project proposal' })).toBeVisible()
})

test('email and identity forms have labels, errors, and keyboard focus', async ({ page }) => {
  await installWorkspace(page)
  await page.goto('/email')
  await page.getByRole('button', { name: 'Add account', exact: true }).click()
  await expect(page.getByLabel('IMAP host', { exact: true })).toBeVisible()
  let result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([])
  await page.getByRole('button', { name: 'New rule', exact: true }).click()
  await expect(page.getByLabel('Instructions for Eve')).toBeVisible()
  result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([])
  await page.getByRole('button', { name: 'Identities', exact: true }).click()
  await page.getByRole('button', { name: 'Add identity', exact: true }).click()
  await page.getByRole('button', { name: 'Save identity', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Give this identity a name')
  result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([])
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: 'Add identity', exact: true })).toBeFocused()
})

test('server failure is visible and offers a retry', async ({ page }) => {
  await installWorkspace(page, { errors: ['/api/tasks'] })
  await page.goto('/tasks')
  await expect(page.getByRole('alert')).toContainText('temporarily unavailable')
  await expect(page.getByRole('button', { name: 'Try again', exact: true })).toBeVisible()
})
