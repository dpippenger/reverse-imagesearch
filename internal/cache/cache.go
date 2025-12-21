package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"

	bolt "go.etcd.io/bbolt"
)

var bucketName = []byte("hashes")

// Stats holds cache statistics
type Stats struct {
	Hits      int64 `json:"hits"`
	Misses    int64 `json:"misses"`
	Entries   int64 `json:"entries"`
	SizeBytes int64 `json:"sizeBytes"`
}

// ScanProgress reports progress during a cache scan operation
type ScanProgress struct {
	Scanned int    `json:"scanned"`
	Total   int    `json:"total"`
	Cached  int    `json:"cached"`
	Done    bool   `json:"done"`
	Error   string `json:"error,omitempty"`
}

// cacheEntry is the JSON-serialized form of cached hash data
type cacheEntry struct {
	PHash     uint64              `json:"phash"`
	AHash     uint64              `json:"ahash"`
	DHash     uint64              `json:"dhash"`
	Histogram hash.ColorHistogram `json:"histogram"`
}

// Cache defines the interface for hash caching
type Cache interface {
	Get(path string, mtime time.Time) (*hash.Data, bool)
	Put(path string, mtime time.Time, data *hash.Data) error
	Clear() error
	Stats() Stats
	Close() error
	Scan(dir string, callback func(ScanProgress)) error
}

// BoltCache implements Cache using BoltDB
type BoltCache struct {
	db     *bolt.DB
	hits   int64
	misses int64
}

// makeKey creates a cache key from path and modification time.
// Format: path\x00mtime_nanoseconds
func makeKey(path string, mtime time.Time) []byte {
	return []byte(fmt.Sprintf("%s\x00%d", path, mtime.UnixNano()))
}

// New creates a new BoltCache at the specified path.
// Creates the database file and parent directories if they don't exist.
func New(dbPath string) (*BoltCache, error) {
	// Create parent directory if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Open BoltDB with reasonable timeout
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open cache database: %w", err)
	}

	// Create bucket if it doesn't exist
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create cache bucket: %w", err)
	}

	return &BoltCache{db: db}, nil
}

// Get retrieves cached hash data for a file.
// Returns the cached data and true if found, nil and false otherwise.
// The mtime parameter ensures stale cache entries are not returned.
func (c *BoltCache) Get(path string, mtime time.Time) (*hash.Data, bool) {
	key := makeKey(path, mtime)
	var data *hash.Data

	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}

		v := b.Get(key)
		if v == nil {
			return nil
		}

		var entry cacheEntry
		if err := json.Unmarshal(v, &entry); err != nil {
			return nil // Treat corrupt entries as cache miss
		}

		data = &hash.Data{
			Path:      path,
			PHash:     entry.PHash,
			AHash:     entry.AHash,
			DHash:     entry.DHash,
			Histogram: entry.Histogram,
		}
		return nil
	})

	if err != nil || data == nil {
		atomic.AddInt64(&c.misses, 1)
		return nil, false
	}

	atomic.AddInt64(&c.hits, 1)
	return data, true
}

// Put stores hash data in the cache.
// Does not store entries with errors.
func (c *BoltCache) Put(path string, mtime time.Time, data *hash.Data) error {
	if data == nil || data.Error != nil {
		return nil // Don't cache errors
	}

	key := makeKey(path, mtime)
	entry := cacheEntry{
		PHash:     data.PHash,
		AHash:     data.AHash,
		DHash:     data.DHash,
		Histogram: data.Histogram,
	}

	value, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to serialize cache entry: %w", err)
	}

	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return fmt.Errorf("cache bucket not found")
		}
		return b.Put(key, value)
	})
}

// Clear removes all cached entries
func (c *BoltCache) Clear() error {
	return c.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketName); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err := tx.CreateBucket(bucketName)
		return err
	})
}

// Stats returns current cache statistics
func (c *BoltCache) Stats() Stats {
	var stats Stats
	stats.Hits = atomic.LoadInt64(&c.hits)
	stats.Misses = atomic.LoadInt64(&c.misses)

	c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		if b == nil {
			return nil
		}
		stats.Entries = int64(b.Stats().KeyN)
		return nil
	})

	// Get database file size
	if info, err := os.Stat(c.db.Path()); err == nil {
		stats.SizeBytes = info.Size()
	}

	return stats
}

// Close closes the cache database
func (c *BoltCache) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// Scan walks a directory and caches hashes for all images.
// The callback is called with progress updates.
func (c *BoltCache) Scan(dir string, callback func(ScanProgress)) error {
	// First, count total images
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible files
		}
		if !info.IsDir() && imgutil.IsImageFile(path) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		progress := ScanProgress{Error: err.Error(), Done: true}
		callback(progress)
		return err
	}

	total := len(paths)
	scanned := 0
	cached := 0

	// Report initial progress
	callback(ScanProgress{Scanned: 0, Total: total, Cached: 0, Done: false})

	// Process each image
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			scanned++
			continue
		}

		// Check if already cached with current mtime
		if _, ok := c.Get(path, info.ModTime()); ok {
			scanned++
			cached++
			callback(ScanProgress{Scanned: scanned, Total: total, Cached: cached, Done: false})
			continue
		}

		// Compute hash and cache it
		data := imgutil.LoadAndHash(path)
		if data.Error == nil {
			c.Put(path, info.ModTime(), &data)
			cached++
		}
		scanned++

		callback(ScanProgress{Scanned: scanned, Total: total, Cached: cached, Done: false})
	}

	callback(ScanProgress{Scanned: scanned, Total: total, Cached: cached, Done: true})
	return nil
}

// DefaultPath returns the default cache database path
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".imgsearch", "cache.db"), nil
}
