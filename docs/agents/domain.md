# Domain docs

## Layout

This repo uses a single-context layout:

- CONTEXT.md at the repo root contains domain terminology.
- docs/adr/ contains architecture decision records.

## Before exploring

Read CONTEXT.md and ADRs relevant to the area being explored.

If these files are absent, proceed silently. Domain-modeling work
creates them when terminology or decisions are resolved.

## Use the glossary's vocabulary

Use terms defined in CONTEXT.md when naming domain concepts in issues,
proposals, hypotheses, and tests.

If a concept is missing, reconsider whether it belongs to the domain.
Record genuine vocabulary gaps for domain-modeling work.

## Flag ADR conflicts

Explicitly identify proposals that contradict an existing ADR,
including the ADR reference and the reason to reconsider it.
