# Vaultix web

Manager Web UI: Vite + React + TypeScript (`strict`, `noUncheckedIndexedAccess`),
Tailwind CSS + shadcn/ui components in `src/shared/ui`, `react-router-dom`,
`@tanstack/react-query`, `react-hook-form` + `zod`.

## Layout (TECH_SPEC section 1.2)

- `src/app/` — router, providers, app shell (sidebar + topbar), `/login` page.
- `src/shared/` — cross-feature code only (`ui/`, `lib/`, `hooks/`, `types/`).
- `src/features/<name>/` — one feature per PRD section 8 section, each owning
  `api.ts`, `hooks.ts`, `pages/`, `routes.tsx`, `types.ts`.

Rule: features import only from `shared/`, never from each other.

## Develop

```bash
npm ci
npm run dev      # Vite dev server, proxies /api to the Manager on :8080
npm run typecheck
npm run build    # emits web/dist/, embedded into the manager binary
```
