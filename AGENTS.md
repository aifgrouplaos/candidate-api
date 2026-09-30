## Agent skills

### Branching

For implementation work, create a `<short-task-name>` feature branch from the current base branch and commit changes there. Keep `main` free of direct implementation commits.

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
