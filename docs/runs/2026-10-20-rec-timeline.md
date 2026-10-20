# Run log — replay timeline M1–M2 (wave-ship loop)

**Lane:** `track/rec-timeline` (cut from `origin/fork/integration` @ `76ae9d9` on 2026-10-20).
**Repo:** `https://github.com/prestonwillis10/r6-dissect` (the fork is canonical; never upstream).
**Spec:** `docs/superpowers/specs/2026-10-19-rec-timeline-framework-design.md` (§5.1, §5.2, §5.4, §6, §7 M1–M2).
**Findings:** `docs/research/2026-10-19-rec-format-findings.md` (byte grammar).
**Plans:** `docs/superpowers/plans/2026-10-19-rec-timeline-01-wire-decoders.md`, `docs/superpowers/plans/2026-10-19-rec-timeline-02-timeline-output.md`.
**Mode:** autonomous (slot PRs auto-merge on MERGE; the landing PR `track/rec-timeline` → `fork/integration` is gated on the operator, as the R6Stats phase-1 lane's landing was).
**Orchestrator worktree:** `/home/prest/r6-dissect-worktrees/orchestrator`.
**Toolchain:** Go at `~/.local/go/bin` (1.27.2 locally; `go.mod` floor 1.26.0); `gh` authed as prestonwillis10; preflight script borrowed from the R6Stats track (`/home/prest/R6Stats-worktrees/orchestrator/scripts/pr-preflight.sh`, run with `GH_REPO=prestonwillis10/r6-dissect`).

## Gate zero (premises verified before building)

| Claim | Status | Evidence |
|---|---|---|
| `origin/fork/integration` head is buildable and the suite is green | CONFIRMED | `git rev-parse HEAD` → `76ae9d9`; `go vet ./... && go test ./...` → `ok dissect (cached)`, `ok dissect/test 1.690s` |
| Eleven `r.MatchFeedback = append` sites at the lines plan 02 names | CONFIRMED | `grep -n 'MatchFeedback = append' dissect/*.go` → feedback.go 139/200/236, defuse.go 228/270/363/412/447, player.go 225/263, time.go 295; `stats.go:75` is a local slice in `stats.go`, not a site |
| Reader anchors plan 02 relies on exist | CONFIRMED | `r.b = nil` at `dissect/reader.go:294` inside `Read` (239); `Write` at 410; `inventory.players` `dissect/loadout.go:94`; `defuserPawnToSlot` `dissect/reader.go:32`; `pendingDefuserPlantIdx` `dissect/reader.go:42`; `lastAliveDefuserPlayer` `dissect/defuse.go:370`; `readScoreboardScoreY10S4` `dissect/scoreboard.go:237`; `trackPlayerObject` `dissect/loadout.go:237`; `readHandle` `dissect/loadout.go:124`; `applyLoadouts` `dissect/loadout.go:265` |
| `writeRound` already takes an `*os.File` (plan 02 T4 note) | DISPROVED (detail) | `main.go:160` is `func writeRound(in io.Reader, out io.Writer)`; T4 changes the signature anyway, so W5 also changes the parameter type and the call site in `main` |
| The three Y11S3 rounds exist at the stated sizes | CONFIRMED | `ls -la` → R01 10 059 296 B, 19-08-03 R12 6 928 486 B, 21-07-35 R04 8 710 584 B |
| Committing ~26 MB of fixtures is in keeping with the repo | CONFIRMED | `du -sh dissect/test/data/replays/valid` → 99M; `Y8S1/ranked_1.rec` is 14 MB |
| CI runs on PRs against any base, on Go 1.26/1.27 × ubuntu/macos/windows, plus the c-shared build | CONFIRMED | `.github/workflows/build-test.yaml` (`on: pull_request`, matrix, `go build -buildmode=c-shared … exports/exports.go`) |
| No competing PR touches this work | CONFIRMED | `gh pr list --state open` → only #1 (`fork/integration` → `main`) |
| `docs/` does not exist in the fork | CONFIRMED | `ls docs` → no such directory; the track branch creates it |
| Spec/plans are committed somewhere PRs can read them | STALE → fixed | they were uncommitted on R6Stats `main`; copied onto this track branch. The R6Stats copies stay uncommitted because landing PR #14 there pins `main` at `ba167c7` |

## Decomposition (Stage 1)

Plan 01 (M1) and plan 02 (M2) form a chain, but several tasks are file-disjoint, so the lane runs four parallel groups. Every wave is one PR based on `track/rec-timeline`; implementers are Sonnet, reviewers Opus (`rev-<PR#>`). No slot is blast-radius gated (parser code, no data, no deploy). The landing PR to `fork/integration` is gated on the operator.

| Wave | Branch | Plan tasks | Owns (files) | PR | Verdict | Merged |
|---|---|---|---|---|---|---|
| W1 | `feat/rt01-fixtures-walker` | 01: T1–T2 (+ `dissect/wire/helpers_test.go`) | `dissect/test/data/replays/valid/Y11S3/*`, `dissect/wire/{ids,container}.go`, `container_test.go`, `helpers_test.go`, `dissect/reader.go` (`Data()`) | | | |
| W2a | `feat/rt01-tlv` | 01: T3 | `dissect/wire/tlv.go`, `tlv_test.go` | | | |
| W2b | `feat/rt01-movement` | 01: T4 | `dissect/wire/movement.go`, `movement_test.go` | | | |
| W3 | `feat/rt01-dissect-lab` | 01: T5–T6 | `cmd/dissect-lab/*`, `README.md` (Research CLI section), `.gitignore`, `docs/wire-coverage.md` | | | |
| W4 | `feat/rt02-graph-msclock` | 02: T1–T2 | `dissect/wire/graph.go`, `graph_test.go`, `dissect/wire/ids.go` (`HandleOf`), `dissect/msclock.go`, `msclock_test.go`, `dissect/{feedback,defuse,player,time,scoreboard}.go` | | | |
| W5 | `feat/rt02-timeline-sidecar` | 02: T3–T4 | `dissect/timeline.go`, `dissect/reader.go`, `dissect/match.go`, `dissect/test/timeline_test.go`, `main.go`, `main_test.go`, `README.md` (Timeline section) | | | |
| W6 | `feat/rt02-lab-join-graph` | 02: T5 | `cmd/dissect-lab/*`, `README.md` (Research CLI section) | | | |

Order: W1 → {W2a ∥ W2b} → {W3 ∥ W4} → W5 → W6. W2a/W2b share only `helpers_test.go`, which W1 creates with every helper both need (listed in W1's dispatch); neither may edit it. W3/W4 are file-disjoint (W3: `cmd/`, README, `.gitignore`, `docs/`; W4: `dissect/`, `dissect/wire/graph.go`, `ids.go`).

Orchestrator-owned cross-cutting items: the run log (committed at wave boundaries only; slots write `docs/runs/slots/2026-10-20-rec-timeline-<wave>.md`); re-validation of every wave after the first against the current track before merge (Stage 5); the README has three independent sections touched by W3, W5, W6 in sequence; the landing PR's `## Candidates` consolidation.

## Worktree ledger

- `worktree: /home/prest/r6-dissect-worktrees/orchestrator branch=track/rec-timeline for=orchestrator`

## Event log

- 2026-10-20 14:34Z gate zero verified (table above); track branch cut from origin/fork/integration @ 76ae9d9; docs copied onto the track.

## Findings (one line per reviewer finding, newest first)

## Candidates

## Observations
