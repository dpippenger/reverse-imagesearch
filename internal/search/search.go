package search

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"

	"imgsearch/internal/cache"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
)

// maxProgressUpdates bounds the number of progress-only results emitted
// while matches are buffered in TopN mode.
const maxProgressUpdates = 100

// Config holds search parameters
type Config struct {
	SearchDir string
	Threshold float64
	Workers   int
	TopN      int
	Cache     cache.Cache // Optional hash cache for faster repeated searches
}

// Result is sent for each match found
type Result struct {
	Match     imgutil.Match `json:"match"`
	Thumbnail string        `json:"thumbnail,omitempty"`
	Total     int           `json:"total"`
	Scanned   int           `json:"scanned"`
	Done      bool          `json:"done"`
	Error     string        `json:"error,omitempty"`
}

// Run performs the image search and calls the callback for each result.
// The callback may be called concurrently from worker goroutines and must
// be safe for concurrent use.
//
// When config.TopN == 0, every match at or above the threshold is streamed
// as it is found. When config.TopN > 0, matches are buffered and the N most
// similar (sorted by similarity, descending) are emitted after all workers
// finish; while buffering, throttled progress-only results (empty Match,
// Total and Scanned set) are emitted so callers can track progress.
// The context can be used to cancel the search early (e.g., when a client disconnects).
func Run(ctx context.Context, sourceData hash.Data, config Config, callback func(Result)) {
	// Find all images in directory
	images, err := imgutil.FindImages(config.SearchDir)
	if err != nil {
		callback(Result{Error: fmt.Sprintf("Error scanning directory: %v", err), Done: true})
		return
	}

	totalImages := len(images)
	if totalImages == 0 {
		callback(Result{Done: true, Total: 0, Scanned: 0})
		return
	}

	numWorkers := config.Workers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	// Emit a progress-only result roughly every progressEvery scans in
	// TopN mode, capping updates at maxProgressUpdates per search.
	progressEvery := totalImages / maxProgressUpdates
	if progressEvery < 1 {
		progressEvery = 1
	}

	var wg sync.WaitGroup
	imageChan := make(chan string, numWorkers*2)
	var mu sync.Mutex
	scanned := 0
	var buffered []imgutil.Match

	processImage := func(path string) {
		data := hashImage(path, config.Cache)

		mu.Lock()
		scanned++
		currentScanned := scanned
		mu.Unlock()

		if data.Error != nil {
			return
		}

		similarity := imgutil.ComputeSimilarity(sourceData, data)
		isMatch := similarity >= config.Threshold

		if config.TopN > 0 {
			// Buffer matches; emit throttled progress-only results so
			// callers can track progress while results are withheld.
			if isMatch {
				mu.Lock()
				buffered = append(buffered, imgutil.Match{Path: path, Similarity: similarity, Hash: data.PHash})
				mu.Unlock()
			}
			if currentScanned%progressEvery == 0 {
				callback(Result{Total: totalImages, Scanned: currentScanned})
			}
			return
		}

		if !isMatch {
			return
		}

		// Generate thumbnail
		thumb, _ := imgutil.GenerateThumbnail(path, 200)

		callback(Result{
			Match:     imgutil.Match{Path: path, Similarity: similarity, Hash: data.PHash},
			Thumbnail: thumb,
			Total:     totalImages,
			Scanned:   currentScanned,
		})
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range imageChan {
				// Check for cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}
				processImage(path)
			}
		}()
	}

	// Send work (respects cancellation)
sendLoop:
	for _, img := range images {
		select {
		case imageChan <- img:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(imageChan)

	// Wait for completion
	wg.Wait()

	mu.Lock()
	finalScanned := scanned
	mu.Unlock()

	if config.TopN > 0 {
		emitTopN(buffered, config.TopN, totalImages, finalScanned, callback)
	}

	callback(Result{Done: true, Total: totalImages, Scanned: finalScanned})
}

// emitTopN sorts buffered matches by similarity (descending) and emits the
// best n, generating thumbnails only for those emitted.
func emitTopN(matches []imgutil.Match, n, total, scanned int, callback func(Result)) {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})
	if len(matches) > n {
		matches = matches[:n]
	}
	for _, m := range matches {
		// Generate thumbnail
		thumb, _ := imgutil.GenerateThumbnail(m.Path, 200)
		callback(Result{
			Match:     m,
			Thumbnail: thumb,
			Total:     total,
			Scanned:   scanned,
		})
	}
}

// hashImage loads and hashes the image at path, consulting the cache when
// one is provided. The file is stat'ed once and its mtime reused for both
// the cache lookup and the cache write.
func hashImage(path string, c cache.Cache) hash.Data {
	if c == nil {
		return imgutil.LoadAndHash(path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return imgutil.LoadAndHash(path)
	}
	mtime := info.ModTime()

	if cached, hit := c.Get(path, mtime); hit {
		return *cached
	}

	data := imgutil.LoadAndHash(path)
	if data.Error == nil {
		if putErr := c.Put(path, mtime, &data); putErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: cache write failed for %s: %v\n", path, putErr)
		}
	}
	return data
}
