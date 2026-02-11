# vX.Y.Z

## Quick Read
- **What changed:** 1-3 bullets with the highest-impact changes.
- **Who is affected:** operator/platform/app teams.
- **Action required:** `none` or exact action.

## Highlights
- Feature or behavior change 1.
- Feature or behavior change 2.
- Feature or behavior change 3.

## Operational Notes
- New env vars:
  - `VAR_NAME`
- New headers/telemetry:
  - `X-HEADER-NAME`
- Backward compatibility:
  - Defaults preserve prior behavior unless explicitly enabled.

## Validation
- Tests run:
  - `go test ./internal/...`
- Rollback:
  - Revert to previous tag and redeploy.

## Links
- Full changelog diff: `https://github.com/cassianwolfe/G-LM/compare/vX.Y.(Z-1)...vX.Y.Z`
- Changelog section: `CHANGELOG.md`
