# Refactor: Simplification and Spec Adherence

## Goal

Structure-only refactoring to clean up dead code, fix stale documentation, and update the spec to cover all implemented features. No behavioral changes.

## Findings

### CLAUDE.md is stale

References a flat-file architecture that was refactored into packages:

| CLAUDE.md says | Actual code |
|---|---|
| `PerceptualHash()` | `hash.Perceptual()` |
| `AverageHash()` | `hash.Average()` |
| `DifferenceHash()` | `hash.Difference()` |
| `LoadAndHashImage()` | `imgutil.LoadAndHash()` |
| `LoadAndHashImageFromReader()` | `imgutil.LoadAndHashFromReader()` |
| `ExtractExifData()` | `exif.Extract()` |
| `ImageMatch` type | `imgutil.Match` |
| `ImageData` type | `hash.Data` |
| `SearchConfig` type | `search.Config` |
| "lines 31-195", "lines 506-1435" | These reference the old single-file layout |

Missing from CLAUDE.md:
- `GET /api/download?path=` endpoint
- `GET /api/cache/directories` endpoint
- `cache.DefaultPath()` function
- `cache.ListDirectories()` / `DirectoryInfo` type

### Spec gaps

`specs/perceptual-image-search/spec.md` is missing:
- **Req 6**: `GET /api/download?path=` endpoint is implemented but not in spec
- **Req 7**: `cache.DefaultPath()` returns `~/.imgsearch/cache.db` — not in spec
- **Req 7**: `cache.ListDirectories()` and `DirectoryInfo` type — not in spec
- **Req 7**: `cache.Cache` interface definition — not in spec

### web/server.go dead code and duplication

- `isPathAllowed()` (line 86-89): Deprecated wrapper around `validatePath()`, unused externally
- `RunSearch()` (line 562-564): Pass-through to `search.Run()`, adds no value
- Repeated `filepath.Clean(validatedPath)` after every `validatePath()` call — `validatePath()` already returns a cleaned path; the extra Clean is redundant (added for CodeQL but the comment explains this)

## Approach

Four parallel workstreams, then review:

1. **spec-updater**: Add missing requirements to spec.md
2. **claude-md-updater**: Rewrite CLAUDE.md to match actual package structure
3. **web-cleaner**: Remove dead code from server.go
4. **reviewer**: Verify no behavior changed, all tests pass

## Success criteria

- `go test ./...` passes with no failures
- `go vet ./...` clean
- No behavioral changes (all existing tests pass unchanged)
- CLAUDE.md references match actual function/type names
- spec.md covers all implemented features
- No dead code in server.go
