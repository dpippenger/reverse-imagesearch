# Image Similarity Search

A fast, parallel image similarity search tool using perceptual hashing algorithms. Find visually similar images across large directories, regardless of resolution, file size, or minor modifications.

## Table of Contents

- [Features](#features)
- [How It Works](#how-it-works)
- [Installation](#installation)
  - [From Source](#from-source)
  - [Docker](#docker)
- [Usage](#usage)
  - [Web UI Mode](#web-ui-mode)
  - [CLI Mode](#cli-mode)
- [Options](#options)
- [Supported Formats](#supported-formats)
- [Similarity Scores](#similarity-scores)
- [Example Output](#example-output)
- [Web UI Screenshot](#web-ui-screenshot)
- [Hash Caching](#hash-caching)
- [Performance](#performance)
- [Dependencies](#dependencies)

## Features

- **Multiple hashing algorithms** for robust matching (pHash, aHash, dHash, color histogram)
- **Parallel processing** using all available CPU cores
- **Web UI** with drag-and-drop upload and real-time streaming results
- **CLI mode** for scripting and automation
- **Streaming output** - see results as they're found
- **File export** - save sorted results to a file
- **Hash caching** - BoltDB-based persistent cache for faster repeated searches

## How It Works

The program uses multiple algorithms to compare images:

1. **Perceptual Hash (pHash)** - Uses DCT (Discrete Cosine Transform) to create a hash based on image frequencies. Very robust against scaling and compression.

2. **Average Hash (aHash)** - Compares pixels to the average brightness. Simple but effective.

3. **Difference Hash (dHash)** - Compares adjacent pixels. Resistant to scaling and aspect ratio changes.

4. **Color Histogram** - Compares color distribution across 16 bins per channel.

The final similarity score is a weighted combination: 35% pHash + 25% dHash + 20% aHash + 20% histogram.

## Installation

### From Source

```bash
go mod tidy
go build -o imgsearch .
```

### Docker

Pre-built images are available on [DockerHub](https://hub.docker.com/r/dpippenger/reverse-imagesearch):

```bash
docker pull dpippenger/reverse-imagesearch
docker run -p 9183:9183 -v /path/to/images:/images dpippenger/reverse-imagesearch
```

See [DOCKER.md](DOCKER.md) for full Docker documentation including CLI mode, caching, and Docker Compose examples.

## Usage

### Web UI Mode

Start the web server:

```bash
# Default - binds to localhost only (secure)
./imgsearch -web

# Custom port
./imgsearch -web -port 8080

# With hash caching enabled (recommended for large directories)
./imgsearch -web -cache-path ~/.imgsearch/cache.db

# Allow network access (bind to all interfaces)
./imgsearch -web -bind 0.0.0.0
```

Then open `http://localhost:9183` in your browser.

**Security Note:** By default, the web server binds to `127.0.0.1` (localhost only) for security. This means it's only accessible from the same machine. To allow access from other computers on your network, use `-bind 0.0.0.0`. Only do this on trusted networks, as there is no authentication.

The web UI provides:
- Drag-and-drop image upload
- **Filesystem browser** for selecting search directory (click "Browse" button)
- Similarity threshold slider
- Worker thread control
- Max results limit
- Real-time progress bar
- Thumbnail grid of matches

### CLI Mode

```bash
# Basic usage - find images similar to source.jpg in current directory
./imgsearch -source photo.jpg

# Search in a specific directory
./imgsearch -source photo.jpg -dir /path/to/photos

# Set a higher similarity threshold (default is 70%)
./imgsearch -source photo.jpg -dir ~/Pictures -threshold 85

# Show only top 10 results
./imgsearch -source photo.jpg -dir ~/Pictures -top 10

# Save sorted results to a file (screen shows results as found, file is sorted)
./imgsearch -source photo.jpg -dir ~/Pictures -output results.txt

# Verbose output with hash details
./imgsearch -source photo.jpg -dir ~/Pictures -verbose

# Specify number of parallel workers
./imgsearch -source photo.jpg -dir ~/Pictures -workers 8

# Enable hash caching for faster repeated searches
./imgsearch -source photo.jpg -dir ~/Pictures -cache-path ~/.imgsearch/cache.db
```

## Options

| Flag | Description | Default |
|------|-------------|---------|
| `-source` | Source image file to compare | (required for CLI) |
| `-dir` | Directory to search recursively | `.` |
| `-threshold` | Minimum similarity percentage (0-100) | `70` |
| `-top` | Show only top N results (0 = all) | `0` |
| `-workers` | Number of parallel workers (0 = auto) | `0` |
| `-verbose` | Show detailed hash information | `false` |
| `-output` | Write sorted results to file | (none) |
| `-web` | Start web UI instead of CLI | `false` |
| `-port` | Port for web UI | `9183` |
| `-bind` | Bind address for web UI | `127.0.0.1` |
| `-cache-path` | Path to BoltDB cache file (enables caching) | (none) |
| `-no-cache` | Disable caching even if cache-path is set | `false` |

## Supported Formats

- JPEG (.jpg, .jpeg)

Note: The image decoder supports PNG and GIF, but the directory scanner currently only indexes JPEG files.

## Similarity Scores

- **95-100%**: Nearly identical images (different compression/size)
- **85-95%**: Very similar (minor edits, crops, filters)
- **70-85%**: Similar content (same subject, different angle)
- **50-70%**: Some similarity (similar colors/composition)
- **Below 50%**: Likely unrelated images

## Example Output

### CLI Output (streaming, as found)

```
Loading source image: vacation.jpg
Scanning directory: /home/user/Photos
Found 1523 images to compare

=== Results (as found) ===

1. [98.2%] /home/user/Photos/2023/vacation_backup.jpg
2. [87.3%] /home/user/Photos/2023/vacation2.jpg
3. [94.7%] /home/user/Photos/edited/vacation_cropped.jpg
4. [76.1%] /home/user/Photos/beach/sunset.jpg
5. [71.4%] /home/user/Photos/travel/similar_view.jpg

=== Total matches found: 5 ===
```

### File Output (sorted by similarity)

```
=== Similar Images (sorted by similarity) ===

1. [98.2%] /home/user/Photos/2023/vacation_backup.jpg
2. [94.7%] /home/user/Photos/edited/vacation_cropped.jpg
3. [87.3%] /home/user/Photos/2023/vacation2.jpg
4. [76.1%] /home/user/Photos/beach/sunset.jpg
5. [71.4%] /home/user/Photos/travel/similar_view.jpg

=== Total matches: 5 ===
```

## Web UI Screenshot

The web interface features:
- Dark theme with gradient background
- Drag-and-drop image upload area
- Filesystem browser modal for directory selection
- Real-time progress bar during search
- Thumbnail grid showing matches with similarity percentages
- Configurable parameters via sliders and input fields

## Hash Caching

For large image collections, hash caching can significantly speed up repeated searches. The first scan computes and stores image hashes in a BoltDB database. Subsequent searches retrieve hashes from the cache instead of recomputing them.

### How It Works

- Hashes are keyed by file path and modification time
- If a file is modified, its hash is automatically recomputed
- Cache persists across sessions in a single `.db` file
- The DCT-based perceptual hash is the most expensive operation (O(n^4) for each image)

### Web UI Settings

When caching is enabled, the web UI includes a **Settings** tab with:
- **Cache Statistics**: View entries, hit rate, and cache size
- **Scan to Cache**: Pre-populate the cache for a directory
- **Clear Cache**: Remove all cached entries

### Recommended Usage

```bash
# Enable caching for web UI (recommended)
./imgsearch -web -cache-path ~/.imgsearch/cache.db

# Enable caching for CLI searches
./imgsearch -source photo.jpg -dir ~/Pictures -cache-path ~/.imgsearch/cache.db
```

## Performance

- Automatically uses all CPU cores by default
- Processes images in parallel with configurable worker count
- Generates thumbnails on-demand for web UI
- Handles large directories (tested with 10,000+ images)
- Hash caching provides 10-100x speedup for repeated searches

## Dependencies

- [github.com/nfnt/resize](https://github.com/nfnt/resize) - High-quality image resizing
- [go.etcd.io/bbolt](https://github.com/etcd-io/bbolt) - Embedded key/value database for hash caching
