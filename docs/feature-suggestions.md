# Feature Suggestions

Captured from full codebase review (2026-04-01). Prioritized by impact and effort.

## High Impact, Low Effort

| # | Feature | Effort | Notes |
|---|---------|--------|-------|
| 1 | **Enable PNG/GIF/WebP indexing** | ~15 min | Decoders already registered via blank imports. Just add extensions to `IsImageFile()`. Instantly indexes 50%+ more images. |
| 2 | **JSON output for CLI** | ~30 min | Add `-format json` flag. Enables scripting and pipeline integration. |
| 3 | **Quiet mode for CLI** | ~30 min | Add `-quiet` flag to suppress progress, only emit matches. Better for scripting. |
| 4 | **Config file support** | ~2 hrs | Load defaults from `~/.imgsearch/config.json`. Saves retyping for repeated searches. |

## High Impact, Medium Effort

| # | Feature | Effort | Notes |
|---|---------|--------|-------|
| 5 | **Deduplication mode** | ~4-6 hrs | Find all near-duplicates *within* a directory (not against a source). Major use case for photo library cleanup. New `search.FindDuplicates(dir, threshold)` API. |
| 6 | **Cache eviction / size limits** | ~2-3 hrs | Cache currently grows unbounded. Add configurable max size with FIFO eviction. |
| 7 | **Result filtering in web UI** | ~2-3 hrs | Filter loaded results by similarity range, filename pattern, date. No new search needed. |
| 8 | **Side-by-side comparison view** | ~3-4 hrs | Modal showing source + result with similarity score breakdown. Better for manual verification. |
| 9 | **Reverse search from URL** | ~1-2 hrs | `--source-url https://...` for CLI, URL input field in web UI. |
| 10 | **Batch search** | ~3-4 hrs | Accept list of source images. `--batch sources.txt` for CLI. |

## Medium Impact

| # | Feature | Notes |
|---|---------|-------|
| 11 | **EXIF orientation correction** | Rotate images before hashing to improve matching accuracy for rotated photos |
| 12 | **Search history in web UI** | Save recent searches with quick re-run |
| 13 | **CSV/TSV export** | `path,similarity,phash,file_size` for spreadsheet workflows |
| 14 | **Mobile-responsive web UI** | Current layout is desktop-focused |
| 15 | **Copy-to-clipboard for paths** | Small UX win in web UI |
