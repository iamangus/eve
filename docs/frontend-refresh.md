# Eve frontend refresh

The refresh adapts the UX/UI Design System and Accessibility and Vitra Visual Theme skills to Eve's conversation-first workflow. It changes the frontend only; authentication and the server's conversation, delivery, and integration contracts are unchanged.

## Design

- Shared semantic colors, spacing, controls, status treatments, and reduced-motion behavior live in `frontend/src/app.css`.
- Inter Variable and JetBrains Mono Variable are bundled locally; no font service is required.
- Desktop sidebar and native mobile navigation dialog share the same destinations. Direct URLs and browser history work for each page.
- Light, dark, and system appearance choices persist locally. System mode follows operating-system changes.
- Chat keeps the composer available, separates speakers visually, offers draft-only conversation starters, and puts active tasks behind an explicit disclosure.
- Tasks retain cancellation, replies, results, and archival while adding status filtering.
- Email, context, channels, and identities use consistent headings, readable status labels, actionable empty states, and visible errors.
- Channel health distinguishes errors, stale checks, and missing health reports. Context estimates remain explicitly separate from live AgentFoundry context usage.

## Verification

`GOTOOLCHAIN=go1.26.9 make check` covers the production frontend build, Go race tests, frontend unit tests, Playwright, Go vet, npm audit, and govulncheck.

The browser suite contains the six existing chat/lifecycle regressions plus ten workspace checks. Fixtures intercept API requests and SSE locally; no production service is contacted. The new checks cover:

- Automated axe checks on all six populated screens in light and dark mode, plus open email and identity forms.
- Layouts at 280px, 320px, and 414px, including navigation dismissal and focus restoration.
- Appearance persistence, direct routes, browser history, draft starters, task filters, and server errors.

Playwright writes desktop light/dark and mobile screenshots into its ignored `frontend/test-results` directory. Screenshots were inspected for desktop chat, dark context, and narrow email layout. Automated accessibility checks are evidence for the tested fixtures, not a claim of complete WCAG compliance or live integration validation.
