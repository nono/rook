# Rook: show the CodeRabbit comments that are not addressed

Status: record of the design on 2026-11-25. It is not updated. The README is the spec of the current behavior.

## Intent

When CodeRabbit leaves comments on one of my PRs, I must address them. The card shows the CodeRabbit logo and the count of the comments that are not addressed, so I see the work to do.

## Goals

- Count, on each of my PRs (drafts included), the unresolved review threads that `coderabbitai` started.
- Show the CodeRabbit logo and the count in the footer of the card, after the Claude logo (variant B of the mockups). The tooltip says "N unresolved CodeRabbit comments".
- No new config.

## Non-goals

- A change of group. The logo only marks the card.
- The logo on PRs that I did not author.
- The CodeRabbit nitpicks in the review body: they are not threads and cannot be resolved.
- More than 10 threads on a PR: only the last 10 count. The README documents this limit.

## Anti-goals

- A slower page: no new API call.
- A page load that uses much of the GitHub rate limit (5,000 points per hour, shared with `gh`).

## Approaches considered

1. **Review threads in the existing GraphQL query (chosen).** Each PR adds `reviewThreads(last: 10) { nodes { isResolved comments(first: 1) { nodes { author { login } } } } }`. No new call. GitHub computes the cost from the `first`/`last` arguments: 100 PRs × N threads × 1 comment gives about 4 + N points per page (dry runs: 3 before the change, 104 with `first: 100`, 14 with `last: 10`). `last` keeps the newest threads, so new feedback shows.
2. **One REST call per PR.** Rejected: slower, and more code for no gain.
3. **A search alias for my PRs only, in the same request** (30 PRs × 100 threads, 33 points per page). Rejected: more code, for an exact count that 10 threads give in most cases.

## Decisions

- "Not addressed" means "unresolved". A thread I replied to but did not resolve counts: CodeRabbit often replies to my reply, so the author of the last comment does not tell if a thread is addressed.
- An outdated thread counts while it is unresolved.
- The author of a thread is the author of its first comment. CodeRabbit's GraphQL login is `coderabbitai`.

## Design

- `github.PR` gets the count of unresolved threads started by `coderabbitai`.
- `board.Card` gets `CodeRabbit int`: that count on my PRs, 0 on the others.
- `page.html` shows the logo (Simple Icons, `#FF570A`, 14px) and the count when `CodeRabbit > 0`.
- The `## Groups` section of the README describes the rule, next to the Claude logo paragraph.
- Tests: the GraphQL parser (testdata JSON), `board.Build` (mine or not, resolved or not, other authors), the handler (logo and count shown or hidden).
