# ImgSearch - Perceptual Image Search Tool

A fast, parallel image similarity search tool using perceptual hashing algorithms. Finds visually similar images across large directories.

## Build & Run

```bash
# Build
go build -o imgsearch .

# CLI mode
./imgsearch -source <image> -dir <search_directory> [-threshold 70] [-workers 0] [-top 10] [-output results.txt]

# Web UI mode
./imgsearch -web [-port 9183]
```

## Architecture

### Core Components

**Hashing Algorithms** (lines 31-195)
- `PerceptualHash()` - DCT-based perceptual hash (pHash), most reliable for scaled/modified images
- `AverageHash()` - Simple average-based hash (aHash), fast but less robust
- `DifferenceHash()` - Gradient-based hash (dHash), resistant to scaling
- `ComputeColorHistogram()` - 16-bin RGB histogram for color comparison

**Similarity Calculation** (lines 315-325)
- Weighted combination: 35% pHash + 25% dHash + 20% aHash + 20% histogram
- Returns 0-100% similarity score
- Threshold default: 70%

**Image Processing**
- `LoadAndHashImage()` - Load from file path and compute all hashes
- `LoadAndHashImageFromReader()` - Load from io.Reader (for uploads)
- `GenerateThumbnail()` - Create base64 JPEG thumbnails for web UI
- `ExtractExifData()` - Extract EXIF metadata from image files

### Hash Caching (`internal/cache`)

BoltDB-based persistent cache for computed image hashes. Eliminates redundant O(n⁴) DCT computations on repeated searches.

**Key Features:**
- Hashes keyed by `path\x00mtime_nanoseconds` (auto-invalidates on file modification)
- Thread-safe concurrent access
- Persistent across sessions in single `.db` file
- 10-100x speedup for repeated searches

**Interface:**
- `Get(path, mtime)` - Retrieve cached hash data
- `Put(path, mtime, data)` - Store hash data
- `Clear()` - Remove all cached entries
- `Stats()` - Return hit/miss counts, entry count, size
- `Scan(dir, callback)` - Pre-populate cache for directory (SSE progress)
- `Close()` - Close database connection

### Web Server (lines 506-1435)

**Endpoints:**
- `GET /` - Serves embedded HTML/CSS/JS UI
- `POST /api/search` - Upload image, returns searchId
- `GET /api/results/{searchId}` - SSE stream of results
- `GET /api/thumbnail?path=` - Generate thumbnail for a path
- `GET /api/browse?path=` - Browse filesystem directories (returns JSON with path, parent, entries)
- `GET /api/exif?path=` - Get EXIF metadata for an image (returns JSON with camera, date, dimensions, etc.)
- `GET /api/cache/stats` - Return cache statistics (entries, hits, misses, size)
- `GET/POST /api/cache/scan?path=` - Pre-populate cache for directory (SSE progress stream)
- `POST /api/cache/clear` - Clear all cached entries

**Streaming:**
- Uses Server-Sent Events (SSE) for real-time results
- Results sent as they're found by parallel workers
- Includes progress updates (scanned/total)

**Web UI Features:**
- Drag & drop or click to upload source image
- Directory browser for selecting search path
- Adjustable similarity threshold slider
- Real-time progress bar with ETA
- Result cards with thumbnails and similarity scores
- Info button (i) on each result shows EXIF metadata on hover (lazy-loaded)
- Settings tab with cache management (stats, scan, clear)

### CLI Mode (lines 1463-1609)

- Streams results to stdout as found (unsorted)
- Optional `-output` writes sorted results to file
- `-top N` limits output count

## Key Data Structures

```go
type ImageMatch struct {
    Path       string  // File path
    Similarity float64 // 0-100%
    Hash       uint64  // pHash value
}

type ImageData struct {
    Path      string
    PHash     uint64
    AHash     uint64
    DHash     uint64
    Histogram ColorHistogram
    Error     error
}

type SearchConfig struct {
    SearchDir  string
    Threshold  float64
    Workers    int
    TopN       int
    Verbose    bool
    OutputFile string
    Cache      cache.Cache // Optional hash cache
}

type CacheStats struct {
    Hits      int64 // Cache hit count
    Misses    int64 // Cache miss count
    Entries   int64 // Number of cached entries
    SizeBytes int64 // Cache file size in bytes
}

type ScanProgress struct {
    Scanned int    // Images scanned so far
    Total   int    // Total images to scan
    Cached  int    // New entries added to cache
    Done    bool   // Whether scan is complete
    Error   string // Error message if any
}

type BrowseResponse struct {
    Path    string        // Current directory absolute path
    Parent  string        // Parent directory path (empty if at root)
    Entries []BrowseEntry // Directory contents
    Error   string        // Error message if any
}

type BrowseEntry struct {
    Name  string // File/directory name
    IsDir bool   // True if directory
    Path  string // Full absolute path
}

type ExifData struct {
    Make         string // Camera manufacturer
    Model        string // Camera model
    DateTime     string // Date/time taken
    Width        int    // Image width in pixels
    Height       int    // Image height in pixels
    Orientation  string // Image orientation
    FNumber      string // Aperture (e.g., "f/2.8")
    ExposureTime string // Shutter speed (e.g., "1/125 s")
    ISO          string // ISO sensitivity
    FocalLength  string // Focal length (e.g., "50 mm")
    LensModel    string // Lens model
    Software     string // Software used
    GPSLatitude  string // GPS latitude
    GPSLongitude string // GPS longitude
    Error        string // Error message if EXIF extraction failed
}
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-source` | (required) | Source image to match against |
| `-dir` | `.` | Directory to search |
| `-threshold` | `70.0` | Minimum similarity % (0-100) |
| `-workers` | `0` (auto) | Parallel worker count |
| `-top` | `0` (all) | Limit results |
| `-output` | (none) | Write sorted results to file |
| `-verbose` | `false` | Show hash details |
| `-web` | `false` | Start web UI |
| `-port` | `9183` | Web UI port |
| `-bind` | `127.0.0.1` | Bind address (use `0.0.0.0` for network access) |
| `-cache-path` | (none) | Path to BoltDB cache file (enables hash caching) |
| `-no-cache` | `false` | Disable caching even if cache-path is set |

## Supported Formats

Currently only JPEG files (`.jpg`, `.jpeg`) are indexed. To add formats, modify `IsImageFile()` at line 328.

The image decoder supports JPEG, PNG, and GIF via blank imports, but `IsImageFile()` filters the search.

## Dependencies

- `github.com/nfnt/resize` - Image resizing with Lanczos3 interpolation
- `github.com/rwcarlsen/goexif/exif` - EXIF metadata extraction from JPEG images
- `go.etcd.io/bbolt` - Embedded key/value database for hash caching

## Performance Notes

- Worker count defaults to `runtime.NumCPU()`
- Channel buffer sized to total image count
- Thumbnails generated on-demand (200px max dimension)
- DCT computation is O(n^4) for 32x32 = 1M operations per image
- EXIF data is lazy-loaded on hover to minimize API calls
- Hash caching provides 10-100x speedup for repeated searches (eliminates DCT recomputation)

## Testing Requirements

All new code must include tests:
- Unit tests for all exported functions
- Table-driven tests preferred for multiple test cases
- Minimum 80% coverage for new packages
- Run `make test` before committing
- Run `make coverage` to generate coverage report

### Test Commands

```bash
go test ./...              # Run all tests
go test -v ./...           # Verbose output
go test -cover ./...       # Show coverage percentage
go test -race ./...        # Run with race detector
make test                  # Run all tests
make test-race             # Run tests with race detector
make coverage              # Generate HTML coverage report
```

### Test Image Generation

Test images are generated programmatically using `internal/testutil`:
- No binary test fixtures in repository
- Deterministic, reproducible tests
- Use `testutil.SolidColorImage()`, `testutil.GradientImage()`, etc.

### Current Coverage

| Package | Coverage | Notes |
|---------|----------|-------|
| internal/hash | 100% | All hash algorithms fully tested |
| internal/imgutil | 98% | Image processing fully tested |
| internal/search | 91% | Search engine with parallel workers |
| internal/web | 85% | Server startup (0%) excluded |
| internal/cache | 83% | BoltDB hash caching |
| internal/exif | 30% | Requires real EXIF images for full coverage |
| cmd/imgsearch | 0% | Main function (hard to unit test) |

Note: Individual package coverage is measured by running `go test -cover ./internal/<pkg>/...`. The exif package has lower coverage because EXIF field extraction requires images with embedded EXIF metadata, which cannot be easily generated programmatically.

## Security

### Implemented Security Features

- **Localhost Binding by Default**: Server binds to `127.0.0.1` by default; use `-bind 0.0.0.0` to allow network access.
- **Path Traversal Protection**: All file access endpoints validate paths are within allowed base directory (defaults to user's home directory). Use `NewWithBasePath()` to configure a custom base path.
- **Header Injection Prevention**: Filenames in Content-Disposition headers are sanitized to prevent HTTP header injection attacks.
- **Cryptographic Search IDs**: Search IDs use `crypto/rand` for unpredictable 128-bit identifiers.
- **Same-Origin SSE**: Removed wildcard CORS header from SSE endpoint; only same-origin requests allowed.

### Security Future Work

The following security improvements are recommended for production deployment:

| Priority | Issue | Description |
|----------|-------|-------------|
| High | HTTPS/TLS | Server runs plain HTTP; configure TLS or use reverse proxy |
| Medium | Rate limiting | Add per-IP request limits to prevent DoS attacks |
| Medium | Request timeouts | Configure `http.Server` timeouts to prevent slowloris |
| Medium | File signature validation | Validate JPEG magic bytes before processing uploads |
| Low | Cache-Control headers | Add appropriate caching headers for sensitive responses |

## Known Issues / Future Work

- Only indexes JPEG files (extend `IsImageFile()` for PNG/GIF/WebP)
- Web UI doesn't persist settings across page reloads
- No authentication on web UI (bind to localhost only in production)
- Large directories: consider adding progress during initial scan phase
