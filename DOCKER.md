# Image Similarity Search - Docker

A fast, parallel image similarity search tool using perceptual hashing algorithms. Find visually similar images across large directories.

## Quick Start

```bash
docker pull dpippenger/reverse-imagesearch

# Run web UI
docker run -p 9183:9183 -v /path/to/images:/images dpippenger/reverse-imagesearch
```

Then open http://localhost:9183 and browse to `/images` to search.

## Features

- **Multiple hashing algorithms** for robust matching (pHash, aHash, dHash, color histogram)
- **Parallel processing** using all available CPU cores
- **Web UI** with drag-and-drop upload and real-time streaming results
- **CLI mode** for scripting and automation
- **Hash caching** for faster repeated searches

## Usage

### Web UI Mode (Default)

```bash
# Basic - mount your images directory
docker run -p 9183:9183 -v /path/to/images:/images dpippenger/reverse-imagesearch

# With persistent hash cache (recommended for large directories)
docker run -p 9183:9183 \
  -v /path/to/images:/images \
  -v imgsearch-cache:/app/cache \
  dpippenger/reverse-imagesearch -web -bind 0.0.0.0 -cache-path /app/cache/cache.db

# Custom port
docker run -p 8080:8080 -v /path/to/images:/images \
  dpippenger/reverse-imagesearch -web -bind 0.0.0.0 -port 8080
```

### CLI Mode

```bash
# Find similar images
docker run -v /path/to/images:/images dpippenger/reverse-imagesearch \
  -source /images/query.jpg -dir /images

# With custom threshold (default: 70%)
docker run -v /path/to/images:/images dpippenger/reverse-imagesearch \
  -source /images/query.jpg -dir /images -threshold 85

# Show only top 10 results
docker run -v /path/to/images:/images dpippenger/reverse-imagesearch \
  -source /images/query.jpg -dir /images -top 10
```

## Options

| Flag | Description | Default |
|------|-------------|---------|
| `-source` | Source image file to compare | (required for CLI) |
| `-dir` | Directory to search recursively | `.` |
| `-threshold` | Minimum similarity percentage (0-100) | `70` |
| `-top` | Show only top N results (0 = all) | `0` |
| `-workers` | Number of parallel workers (0 = auto) | `0` |
| `-web` | Start web UI instead of CLI | `true` (in container) |
| `-port` | Port for web UI | `9183` |
| `-bind` | Bind address for web UI | `0.0.0.0` (in container) |
| `-cache-path` | Path to BoltDB cache file | (none) |

## Docker Compose

```yaml
services:
  imgsearch:
    image: dpippenger/reverse-imagesearch
    ports:
      - "9183:9183"
    volumes:
      - /path/to/images:/images
      - imgsearch-cache:/app/cache
    command: ["-web", "-bind", "0.0.0.0", "-cache-path", "/app/cache/cache.db"]

volumes:
  imgsearch-cache:
```

## Supported Formats

- JPEG (.jpg, .jpeg)

## Source Code

GitHub: https://github.com/dpippenger/reverse-imagesearch
