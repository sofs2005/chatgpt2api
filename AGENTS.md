# Repository Guidelines

## Coding Principles

**No compatibility layers**
Write for the current API version only. Do not add fallbacks, shims, feature flags, or multi-path handling unless explicitly asked. Prefer deleting old code over guarding it.

## Project Structure & Module Organization

This repository is a Go backend with a Vite/React admin UI. The backend entry point is `cmd/chatgpt2api/main.go`; implementation packages live under `internal/` (`httpapi`, `service`, `protocol`, `backend`, `storage`, `config`, and helpers). Frontend source is in `web/src/`, with pages under `web/src/app/`, shared UI in `web/src/components/`, API helpers in `web/src/lib/`, and stores in `web/src/store/`. Screenshots are in `assets/`. ChatGPT web reverse-engineering notes live in `jshook/docs/`, with validation scripts and sanitized response samples under `jshook/`.

## Build, Test, and Development Commands

- `cd web && npm run build` generates the embedded admin UI assets under `internal/web/dist`.
- `go test ./...` runs all backend tests after the frontend assets exist.
- `go build -tags=embed -ldflags "-X chatgpt2api/internal/version.Version=1.0.0" -o chatgpt2api ./cmd/chatgpt2api` builds the service binary with embedded admin UI assets.
- `CHATGPT2API_ADMIN_PASSWORD=change_me_please ./chatgpt2api` runs the backend locally after build.
- `docker compose up -d` starts the default containerized deployment using `.env`.
- `docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build` rebuilds the image from local source.
- `cd web && npm run dev` starts the frontend dev server.
- `cd web && npm run build` type-checks and builds the frontend.
- `cd web && npm run lint` runs ESLint.

## Coding Style & Naming Conventions

Use `gofmt` for Go code and keep package names short, lowercase, and domain-oriented. Place tests beside the code they exercise as `*_test.go`. Frontend code uses TypeScript, React 19, Vite, ESLint, Tailwind CSS, and shadcn-style components. Prefer kebab-case filenames for React components (`image-composer.tsx`) and PascalCase exports. Reuse helpers from `web/src/lib/` and primitives from `web/src/components/ui/` before adding abstractions.

Admin async creation-task routes use `/api/creation-tasks` as the resource root. Submit task-type-specific work through explicit child resources: `image-generations`, `image-edits`, and `chat-completions`. Do not introduce image-named task aliases or chat routes under image-named resources.

## Design Guidelines

For frontend UI and visual design work, consult `DESIGN.md` for the project design system and apply those rules unless the user explicitly requests a different direction.

## jshook Reverse-Engineering Notes

Use `jshook/README.md` as the index for ChatGPT web protocol research. Keep endpoint inventories, content-type mappings, request-flow notes, internal codename mappings, and authenticated API schema notes in `jshook/docs/*.md` rather than duplicating them elsewhere.

Use jshook MCP (Chrome CDP + JS Hook + Network Interception) when fresh browser evidence is needed for ChatGPT web behavior, especially request construction, SSE event shape, frontend function mapping, Statsig gates, PoW/sentinel requirements, and image-generation flows. Treat upstream ChatGPT behavior as time-sensitive: verify with a fresh capture or script run before changing backend protocol code based on these notes.

Keep validation scripts in `jshook/scripts/` and raw or reduced response artifacts in `jshook/responses/`. Do not commit live OAuth tokens, cookies, account identifiers, proxy credentials, private prompts, or reusable CDN/download URLs; redact or regenerate fixtures before saving them. jshook scripts may require authenticated local state and external network access, so they are not part of the default `go test ./...` or frontend build verification unless a task explicitly targets those flows.

## Testing Guidelines

Backend coverage is Go test based; add focused unit tests in the relevant `internal/**` package when changing service, protocol, config, or route behavior. Keep test names descriptive, for example `TestRegisterFlow...` or `TestCreationTask...`. Frontend changes should pass `npm run build` and `npm run lint`; add UI tests only if a framework is introduced later.

## Commit & Pull Request Guidelines

Recent history uses Conventional Commit-style subjects such as `feat: ...`, `chore: ...`, `feat(httpapi): ...`, and breaking markers like `feat!:`. Keep subjects concise and scoped to intent. Pull requests should include a summary, verification (`go test ./...`, `npm run build`, screenshots for UI changes), linked issues when applicable, and notes for config or deployment changes.

## Security & Configuration Tips

Do not commit real secrets. Start from `.env.example`, set `CHATGPT2API_ADMIN_PASSWORD`, and keep account tokens, proxy credentials, and database URLs local or in deployment secrets. Public deployments should add external access control.
