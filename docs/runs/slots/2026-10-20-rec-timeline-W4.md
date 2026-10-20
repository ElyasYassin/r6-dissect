# W4 slot log: object graph and millisecond clock

Branch `feat/rt02-graph-msclock`, based on `track/rec-timeline`. Inline simplify and verify passes were done over the diff by the worker, not by nested agents.

## Gate evidence

```
$ go vet ./... && go build ./... && go test ./... -count=1
?   	github.com/redraskal/r6-dissect	[no test files]
ok  	github.com/redraskal/r6-dissect/dissect	9.204s
ok  	github.com/redraskal/r6-dissect/dissect/test	6.046s
?   	github.com/redraskal/r6-dissect/dissect/ubi	[no test files]
ok  	github.com/redraskal/r6-dissect/dissect/wire	16.767s
?   	github.com/redraskal/r6-dissect/exports	[no test files]
$ git status --short dissect/test
(empty = goldens unchanged)
$ go test ./dissect -run TestMsClock_Fixture -v
```

## Mutation proof

Raw output: the mutant runs were captured under `/tmp/claude-1001/w4-mutants.txt` and `/tmp/claude-1001/w4-mutants-f.txt`.

| Mutant | Result |
|---|---|
| Ancestor without the step cap | red (test run hits the 30 s timeout on the cycle case) |
| Add records null-target refs | red, TestGraph |
| Phase jump threshold 100000 | red, TestMsClock_Phases |
| PostPlant BaseMs 0 | red, TestMsClock_Phases |
| observe accepts the clock field on any object | red, TestMsClock_IgnoresOtherObjects |
| bare append at a covered site | red, TestAddUpdate_OffsetsStamped |

## Candidates

### C-W4-1 plan test series contradicts the plan's jump rule
- Observed: the plan's TestMsClock_Phases feeds 179880 then 45000 and expects a PostPlant phase, but 45000 is below the previous sample, so the rising-jump rule yields no phase. Running the plan series printed only Prep and Action phases.
- Also the plan's `at(25)` expects the sample at tick 29. The test here uses a rise from 12000 to 45000 (BaseMs 167917) and `at(30)`.
- Acceptance: the plan text is corrected, or the phase rule is extended to detect drops.
- Verified-against: 37c3992

### C-W4-2 plant with more than 45 s left is invisible to the ms clock
- Observed: a drop from 100000 to 45000 stays one phase; pinned by `TestMsClock_PlantAboveCountdownIsNotDetected`. The fixture plants with 12625 ms left, so it rises.
- Acceptance: Task 3 uses the legacy plant event to split the phase, or a drop rule is added.
- Verified-against: 37c3992

## Observations

Mutating the append sites below to a bare append kept the offset test green where the result is 0, because no valid fixture reaches that site. The number is the count of failing lines.

```
=== feedback.go:152	0
=== feedback.go:213	105
=== feedback.go:249	31
=== player.go:225	18
=== player.go:263	15
=== defuse.go:228	0
=== defuse.go:270	0
=== defuse.go:366	7
=== defuse.go:415	6
=== defuse.go:450	4
=== time.go:297	0
```
