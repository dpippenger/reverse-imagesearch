package search

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"

	"imgsearch/internal/cache"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
)

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

	var wg sync.WaitGroup
	imageChan := make(chan string, numWorkers*2)
	var resultMutex sync.Mutex
	scanned := 0
	resultCount := 0

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

				var data hash.Data

				// Try cache first
				if config.Cache != nil {
					if info, err := os.Stat(path); err == nil {
						if cached, ok := config.Cache.Get(path, info.ModTime()); ok {
							data = *cached
						}
					}
				}

				// Compute if not cached
				if data.Path == "" {
					data = imgutil.LoadAndHash(path)
					// Cache the result if we have a cache and no error
					if config.Cache != nil && data.Error == nil {
						if info, err := os.Stat(path); err == nil {
							if putErr := config.Cache.Put(path, info.ModTime(), &data); putErr != nil {
								fmt.Fprintf(os.Stderr, "Warning: cache write failed for %s: %v\n", path, putErr)
							}
						}
					}
				}

				resultMutex.Lock()
				scanned++
				currentScanned := scanned
				resultMutex.Unlock()

				if data.Error != nil {
					continue
				}

				similarity := imgutil.ComputeSimilarity(sourceData, data)
				if similarity >= config.Threshold {
					resultMutex.Lock()
					resultCount++
					currentCount := resultCount
					resultMutex.Unlock()

					// Check if we should limit results
					if config.TopN > 0 && currentCount > config.TopN {
						continue
					}

					match := imgutil.Match{
						Path:       path,
						Similarity: similarity,
						Hash:       data.PHash,
					}

					// Generate thumbnail
					thumb, _ := imgutil.GenerateThumbnail(path, 200)

					callback(Result{
						Match:     match,
						Thumbnail: thumb,
						Total:     totalImages,
						Scanned:   currentScanned,
					})
				}
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

	resultMutex.Lock()
	finalScanned := scanned
	resultMutex.Unlock()
	callback(Result{Done: true, Total: totalImages, Scanned: finalScanned})
}
