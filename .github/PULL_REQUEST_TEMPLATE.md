## Summary

What this PR changes and why. One short paragraph plus bullet points if needed. Reference the governing issue with `(refs #<number>)`.

## Measured

Concrete evidence for the claim: diff stat, file counts, timings — numbers with their sources, not adjectives.

## FS / Acceptance criteria

- [ ] Each acceptance criterion from the issue, as a checkbox
- [ ] Scope claims backed by grep/inspection evidence

## Known limits

What this PR deliberately does not do, and what remains open.

## Tests

The exact commands run and their results (required in every PR):

```
go build -trimpath -o ./bin-timesheet ./cmd/timesheet   # ok
go test -race -shuffle=on ./...                         # ok
go vet ./...                                            # ok
```

## Definition of Done

- [ ] Every item in FS / Acceptance criteria checked
- [ ] Build/test/vet green locally
- [ ] Untouched surfaces verified unchanged
