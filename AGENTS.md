## Agent skills

### Branching

For implementation work, create a `<short-task-name>` feature branch from the current base branch and commit changes there. Keep `main` free of direct implementation commits.

### Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <summary>`.

- Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- Scope is optional and names the module, e.g. `auth`, `ratelimit`, `config`.
- Summary is imperative, lowercase, no trailing period, under 72 characters.
- Mark breaking changes with `!`, e.g. `feat(auth)!: require tenant claim`.

Examples: `feat(auth): add refresh token rotation`, `fix(ratelimit): use last X-Forwarded-For IP`, `docs: document trusted proxy setup`.

### Code structure

Follow the `bkgo` skill for all backend structure, for maintainability and readability.

- Create, add, or remove modules and layers with the `bkgo` CLI, not by hand.
- Each `internal/<module>` keeps only the bkgo files: `domain.go`, `usecase.go`, `handler.go`, `repository.go`, and their tests. Put module-specific HTTP middleware, such as route rate-limit policies, in `handler.go`.
- Keep layer rules: entities and ports in `domain.go`, business rules in `usecase.go`, HTTP only in `handler.go`, persistence only in `repository.go`. Only `cmd/api/main.go` builds adapters.
- Put cross-module code in `pkg/` (e.g. `pkg/ratelimit`, `pkg/httpresponse`). `pkg/` never imports `internal/`; pass module values in as functions or ports instead.
- Keep the project's `pkg/httpresponse` envelope instead of bkgo's template response helpers.

### Refactoring

Follow the `refactor` skill when changing existing code, for maintainability and readability.

- Preserve behavior: never mix a refactor with a feature or behavior change.
- Make small steps, run `go fmt ./...`, `go vet ./...`, and `go test ./...` after each, and commit at safe states.
- Add a test before refactoring code that lacks one.
- Remove dead code and duplication instead of working around them.

### Issue tracker

Track issues and specs in GitHub Issues. Read `docs/agents/issue-tracker.md`
before issue operations.

### Triage labels

Use the five default triage labels. Read `docs/agents/triage-labels.md`
when assigning triage state.

### Domain docs

Use a single-context layout. Read `docs/agents/domain.md`
before exploring the codebase.

### API security

For every API change, follow the OWASP security baseline and authentication rules in `API_SPEC.md` sections 2–3. Review authorization, input bounds, rate limits, secrets, and failure behavior before merging.
