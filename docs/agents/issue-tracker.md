# Issue tracker: GitHub

Issues and specs live in aifgrouplaos/candidate-api:
https://github.com/aifgrouplaos/candidate-api

Use the gh CLI. Pass --repo aifgrouplaos/candidate-api explicitly,
since this checkout currently has no Git remote.

## Conventions

- Create: gh issue create --repo aifgrouplaos/candidate-api --title "..." --body-file <file>
- Read: gh issue view <number> --repo aifgrouplaos/candidate-api --comments
- List: gh issue list --repo aifgrouplaos/candidate-api --state open --json number,title,body,labels,comments
- Comment: gh issue comment <number> --repo aifgrouplaos/candidate-api --body-file <file>
- Label: gh issue edit <number> --repo aifgrouplaos/candidate-api --add-label "..." or --remove-label "..."
- Close: gh issue close <number> --repo aifgrouplaos/candidate-api

For multiline bodies, write the exact text to a temporary file and use
--body-file. Apply appropriate state and label filters when listing.

## Pull requests as a triage surface

PRs as a request surface: no.

## Skill operations

When a skill says "publish to the issue tracker", create a GitHub issue.
When it says "fetch the relevant ticket", read the issue and its comments.

## Wayfinding operations

- Map: one issue labelled wayfinder:map, containing Notes,
  Decisions-so-far, and Fog.
- Children: linked GitHub sub-issues labelled wayfinder:<type>,
  where type is research, prototype, grilling, or task.
  If sub-issues are unavailable, use a task list in the map and
  a "Part of #<map>" line in each child.
- Blocking: use native GitHub issue dependencies. If unavailable,
  record "Blocked by: #<number>, ..." in the child body.
  A ticket is unblocked when all blockers are closed.
- Frontier: choose the first open, unblocked, unassigned child
  in map order.
- Claim: assign the ticket to the driving developer before starting work.
- Resolve: comment with the answer, close the child, then append
  a summary and link to the map's Decisions-so-far.
