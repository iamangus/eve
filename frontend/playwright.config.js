import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  use: { baseURL: process.env.EVE_TEST_URL || 'http://127.0.0.1:15173', trace: 'retain-on-failure' },
  webServer: process.env.EVE_TEST_URL ? undefined : {
    command: 'npm run dev -- --host 127.0.0.1 --port 15173',
    url: 'http://127.0.0.1:15173',
    reuseExistingServer: !process.env.CI,
  },
})
