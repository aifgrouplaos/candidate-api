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
