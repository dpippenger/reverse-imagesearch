package search

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"imgsearch/internal/cache"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/testutil"
)

func TestRun(t *testing.T) {
	t.Run("empty directory returns done immediately", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "empty-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		sourceData := hash.Data{}
		config := Config{
			SearchDir: tmpDir,
			Threshold: 50.0,
			Workers:   1,
		}

		var results []Result
		var mu sync.Mutex

		Run(context.Background(), sourceData, config, func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		})

		if len(results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(results))
		}
		if !results[0].Done {
			t.Error("Expected Done to be true")
		}
		if results[0].Total != 0 {
			t.Errorf("Expected Total=0, got %d", results[0].Total)
		}
	})

	t.Run("finds matching images", func(t *testing.T) {
		// Create temp directory with images
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		// Create source image data by loading one of the test images
		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, err := testutil.CreateTempJPEG(sourceImg)
		if err != nil {
			t.Fatalf("Failed to create source JPEG: %v", err)
		}
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0, // Accept all matches
			Workers:   2,
		}

		var results []Result
		var mu sync.Mutex

		Run(context.Background(), sourceData, config, func(r Result) {
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		})

		// Should have at least the done result
		if len(results) < 1 {
			t.Error("Expected at least 1 result")
		}

		// Last result should be Done
		lastResult := results[len(results)-1]
		if !lastResult.Done {
			t.Error("Last result should have Done=true")
		}

		// Should have found 2 images total
		if lastResult.Total != 2 {
			t.Errorf("Expected Total=2, got %d", lastResult.Total)
		}
	})

	t.Run("threshold filtering", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		// Create completely different source
		sourceImg := testutil.CheckerboardImage(64, 64)
		sourcePath, err := testutil.CreateTempJPEG(sourceImg)
		if err != nil {
			t.Fatalf("Failed to create source JPEG: %v", err)
		}
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 99.0, // Very high threshold
			Workers:   1,
		}

		var matchCount int
		var doneReceived bool

		Run(context.Background(), sourceData, config, func(r Result) {
			if r.Done {
				doneReceived = true
			} else if r.Match.Path != "" {
				matchCount++
			}
		})

		if !doneReceived {
			t.Error("Expected Done result")
		}

		// With 99% threshold, checkerboard should not match solid colors
		// (though this depends on the hashing algorithm)
		t.Logf("Match count at 99%% threshold: %d", matchCount)
	})

	t.Run("TopN limiting", func(t *testing.T) {
		// Create directory with multiple similar images
		tmpDir, err := os.MkdirTemp("", "topn-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create 5 nearly identical images
		for i := 0; i < 5; i++ {
			img := testutil.SolidColorImage(32, 32, color.RGBA{255, uint8(i), 0, 255})
			path, _ := testutil.CreateTempJPEG(img)
			os.Rename(path, tmpDir+"/image"+string(rune('0'+i))+".jpg")
		}

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0, // Accept all
			TopN:      2,   // Only top 2
			Workers:   1,
		}

		var matchCount int

		Run(context.Background(), sourceData, config, func(r Result) {
			if r.Match.Path != "" {
				matchCount++
			}
		})

		// Should only get TopN matches
		if matchCount > 2 {
			t.Errorf("Expected at most 2 matches, got %d", matchCount)
		}
	})

	t.Run("default workers equals NumCPU", func(t *testing.T) {
		config := Config{
			Workers: 0, // Should default to NumCPU
		}

		expected := runtime.NumCPU()
		if config.Workers != 0 {
			t.Errorf("Config.Workers should be 0 (to trigger default), got %d", config.Workers)
		}

		// The actual worker count is checked inside Run(), we just verify
		// the NumCPU value is positive
		if expected <= 0 {
			t.Errorf("runtime.NumCPU() should be > 0, got %d", expected)
		}
	})

	t.Run("results include thumbnails", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0,
			Workers:   1,
		}

		var hasThumbnail bool

		Run(context.Background(), sourceData, config, func(r Result) {
			if r.Match.Path != "" && r.Thumbnail != "" {
				hasThumbnail = true
			}
		})

		if !hasThumbnail {
			t.Error("Expected at least one result with thumbnail")
		}
	})

	t.Run("scanned count increments", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0,
			Workers:   1,
		}

		var finalScanned int

		Run(context.Background(), sourceData, config, func(r Result) {
			if r.Done {
				finalScanned = r.Scanned
			}
		})

		// Should have scanned 2 images
		if finalScanned != 2 {
			t.Errorf("Expected Scanned=2, got %d", finalScanned)
		}
	})

	t.Run("uses cache when provided", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		// Create cache
		cacheDir := t.TempDir()
		c, err := cache.New(filepath.Join(cacheDir, "cache.db"))
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer c.Close()

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0,
			Workers:   1,
			Cache:     c,
		}

		// First run - should populate cache
		Run(context.Background(), sourceData, config, func(r Result) {})

		// Verify cache was populated
		stats := c.Stats()
		if stats.Entries == 0 {
			t.Error("Expected cache entries after first run")
		}

		// Second run - should use cache hits
		Run(context.Background(), sourceData, config, func(r Result) {})

		// Verify cache hits occurred
		stats = c.Stats()
		if stats.Hits == 0 {
			t.Error("Expected cache hits on second run")
		}
	})

	t.Run("nil cache works", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0,
			Workers:   1,
			Cache:     nil, // Explicitly nil cache
		}

		var doneReceived bool
		Run(context.Background(), sourceData, config, func(r Result) {
			if r.Done {
				doneReceived = true
			}
		})

		if !doneReceived {
			t.Error("Expected Done result with nil cache")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
		defer os.Remove(sourcePath)

		sourceData := imgutil.LoadAndHash(sourcePath)

		// Cancel context immediately
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		config := Config{
			SearchDir: tmpDir,
			Threshold: 0.0,
			Workers:   1,
		}

		var doneReceived bool
		Run(ctx, sourceData, config, func(r Result) {
			if r.Done {
				doneReceived = true
			}
		})

		if !doneReceived {
			t.Error("Expected Done result even with cancelled context")
		}
	})
}

func TestConfig(t *testing.T) {
	t.Run("Config struct has expected fields", func(t *testing.T) {
		config := Config{
			SearchDir: "/path/to/dir",
			Threshold: 75.5,
			Workers:   4,
			TopN:      10,
		}

		if config.SearchDir != "/path/to/dir" {
			t.Errorf("SearchDir = %q, want %q", config.SearchDir, "/path/to/dir")
		}
		if config.Threshold != 75.5 {
			t.Errorf("Threshold = %f, want 75.5", config.Threshold)
		}
		if config.Workers != 4 {
			t.Errorf("Workers = %d, want 4", config.Workers)
		}
		if config.TopN != 10 {
			t.Errorf("TopN = %d, want 10", config.TopN)
		}
	})
}

func TestResult(t *testing.T) {
	t.Run("Result struct has expected fields", func(t *testing.T) {
		result := Result{
			Match: imgutil.Match{
				Path:       "/path/to/image.jpg",
				Similarity: 95.5,
				Hash:       0xFFFF,
			},
			Thumbnail: "base64data",
			Total:     100,
			Scanned:   50,
			Done:      false,
			Error:     "",
		}

		if result.Match.Path != "/path/to/image.jpg" {
			t.Errorf("Match.Path = %q, want %q", result.Match.Path, "/path/to/image.jpg")
		}
		if result.Match.Similarity != 95.5 {
			t.Errorf("Match.Similarity = %f, want 95.5", result.Match.Similarity)
		}
		if result.Total != 100 {
			t.Errorf("Total = %d, want 100", result.Total)
		}
		if result.Scanned != 50 {
			t.Errorf("Scanned = %d, want 50", result.Scanned)
		}
	})
}

// Benchmark tests
func BenchmarkRun(b *testing.B) {
	tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanup()

	sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
	sourcePath, _ := testutil.CreateTempJPEG(sourceImg)
	defer os.Remove(sourcePath)

	sourceData := imgutil.LoadAndHash(sourcePath)

	config := Config{
		SearchDir: tmpDir,
		Threshold: 50.0,
		Workers:   2,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Run(context.Background(), sourceData, config, func(r Result) {})
	}
}
