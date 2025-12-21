package cache

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"imgsearch/internal/hash"
	"imgsearch/internal/testutil"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() string
		wantErr bool
	}{
		{
			name: "creates cache in new directory",
			setup: func() string {
				dir := t.TempDir()
				return filepath.Join(dir, "subdir", "cache.db")
			},
			wantErr: false,
		},
		{
			name: "creates cache in existing directory",
			setup: func() string {
				dir := t.TempDir()
				return filepath.Join(dir, "cache.db")
			},
			wantErr: false,
		},
		{
			name: "fails for invalid path",
			setup: func() string {
				return "/dev/null/invalid/cache.db"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := tt.setup()
			cache, err := New(dbPath)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer cache.Close()

			// Verify database file was created
			if _, err := os.Stat(dbPath); os.IsNotExist(err) {
				t.Error("database file was not created")
			}
		})
	}
}

func TestGetPut(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	testPath := "/path/to/image.jpg"
	testMtime := time.Now()
	testData := &hash.Data{
		Path:  testPath,
		PHash: 12345,
		AHash: 67890,
		DHash: 11111,
		Histogram: hash.ColorHistogram{
			R: [16]float64{0.1, 0.2, 0.3},
			G: [16]float64{0.4, 0.5, 0.6},
			B: [16]float64{0.7, 0.8, 0.9},
		},
	}

	// Test cache miss
	data, ok := cache.Get(testPath, testMtime)
	if ok || data != nil {
		t.Error("expected cache miss for new key")
	}

	// Test put and get
	if err := cache.Put(testPath, testMtime, testData); err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	data, ok = cache.Get(testPath, testMtime)
	if !ok || data == nil {
		t.Fatal("expected cache hit after put")
	}

	// Verify data integrity
	if data.PHash != testData.PHash {
		t.Errorf("PHash mismatch: got %d, want %d", data.PHash, testData.PHash)
	}
	if data.AHash != testData.AHash {
		t.Errorf("AHash mismatch: got %d, want %d", data.AHash, testData.AHash)
	}
	if data.DHash != testData.DHash {
		t.Errorf("DHash mismatch: got %d, want %d", data.DHash, testData.DHash)
	}
	if data.Histogram.R[0] != testData.Histogram.R[0] {
		t.Errorf("Histogram R mismatch: got %f, want %f", data.Histogram.R[0], testData.Histogram.R[0])
	}
}

func TestMtimeInvalidation(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	testPath := "/path/to/image.jpg"
	oldMtime := time.Now()
	newMtime := oldMtime.Add(1 * time.Hour)
	testData := &hash.Data{
		Path:  testPath,
		PHash: 12345,
	}

	// Put with old mtime
	if err := cache.Put(testPath, oldMtime, testData); err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	// Get with old mtime should hit
	_, ok := cache.Get(testPath, oldMtime)
	if !ok {
		t.Error("expected cache hit with original mtime")
	}

	// Get with new mtime should miss
	_, ok = cache.Get(testPath, newMtime)
	if ok {
		t.Error("expected cache miss with different mtime")
	}
}

func TestPutSkipsErrors(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	testPath := "/path/to/broken.jpg"
	testMtime := time.Now()

	// Data with error should not be cached
	dataWithError := &hash.Data{
		Path:  testPath,
		Error: os.ErrNotExist,
	}

	if err := cache.Put(testPath, testMtime, dataWithError); err != nil {
		t.Fatalf("put with error should not fail: %v", err)
	}

	// Should be a cache miss
	_, ok := cache.Get(testPath, testMtime)
	if ok {
		t.Error("expected cache miss for data with error")
	}

	// Nil data should also not cause error
	if err := cache.Put(testPath, testMtime, nil); err != nil {
		t.Fatalf("put with nil should not fail: %v", err)
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Add some entries
	mtime := time.Now()
	for i := 0; i < 5; i++ {
		data := &hash.Data{Path: "/path/to/image.jpg", PHash: uint64(i)}
		cache.Put("/path/"+string(rune('a'+i))+".jpg", mtime, data)
	}

	// Verify entries exist
	stats := cache.Stats()
	if stats.Entries != 5 {
		t.Errorf("expected 5 entries, got %d", stats.Entries)
	}

	// Clear cache
	if err := cache.Clear(); err != nil {
		t.Fatalf("failed to clear: %v", err)
	}

	// Verify cache is empty
	stats = cache.Stats()
	if stats.Entries != 0 {
		t.Errorf("expected 0 entries after clear, got %d", stats.Entries)
	}
}

func TestStats(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Initial stats
	stats := cache.Stats()
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("expected 0 hits/misses initially, got %d/%d", stats.Hits, stats.Misses)
	}

	mtime := time.Now()
	testData := &hash.Data{Path: "/test.jpg", PHash: 123}

	// Generate some hits and misses
	cache.Get("/nonexistent", mtime)  // miss
	cache.Get("/nonexistent2", mtime) // miss
	cache.Put("/test.jpg", mtime, testData)
	cache.Get("/test.jpg", mtime) // hit
	cache.Get("/test.jpg", mtime) // hit
	cache.Get("/test.jpg", mtime) // hit

	stats = cache.Stats()
	if stats.Misses != 2 {
		t.Errorf("expected 2 misses, got %d", stats.Misses)
	}
	if stats.Hits != 3 {
		t.Errorf("expected 3 hits, got %d", stats.Hits)
	}
	if stats.Entries != 1 {
		t.Errorf("expected 1 entry, got %d", stats.Entries)
	}
	if stats.SizeBytes <= 0 {
		t.Error("expected positive database size")
	}
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	mtime := time.Now()
	var wg sync.WaitGroup

	// Concurrent puts
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			data := &hash.Data{Path: "/test.jpg", PHash: uint64(idx)}
			cache.Put("/image"+string(rune('a'+idx%26))+".jpg", mtime, data)
		}(i)
	}

	// Concurrent gets
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cache.Get("/image"+string(rune('a'+idx%26))+".jpg", mtime)
		}(i)
	}

	wg.Wait()

	// Just verify no panics occurred and we have some entries
	stats := cache.Stats()
	if stats.Entries == 0 {
		t.Error("expected some entries after concurrent access")
	}
}

func TestScan(t *testing.T) {
	// Create temp directory with test images
	images := map[string]image.Image{
		"red.jpg":   testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255}),
		"green.jpg": testutil.SolidColorImage(32, 32, color.RGBA{0, 255, 0, 255}),
		"blue.jpg":  testutil.SolidColorImage(32, 32, color.RGBA{0, 0, 255, 255}),
	}
	imgDir, cleanup, err := testutil.CreateTempDir(images)
	if err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	defer cleanup()

	// Create cache
	cacheDir := t.TempDir()
	cache, err := New(filepath.Join(cacheDir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Track progress
	var progressUpdates []ScanProgress
	callback := func(p ScanProgress) {
		progressUpdates = append(progressUpdates, p)
	}

	// Run scan
	if err := cache.Scan(imgDir, callback); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// Verify final progress
	if len(progressUpdates) == 0 {
		t.Fatal("expected progress updates")
	}

	final := progressUpdates[len(progressUpdates)-1]
	if !final.Done {
		t.Error("expected final progress to have Done=true")
	}
	if final.Total != 3 {
		t.Errorf("expected 3 total images, got %d", final.Total)
	}
	if final.Scanned != 3 {
		t.Errorf("expected 3 scanned images, got %d", final.Scanned)
	}
	if final.Cached != 3 {
		t.Errorf("expected 3 cached images, got %d", final.Cached)
	}

	// Verify cache has entries
	stats := cache.Stats()
	if stats.Entries != 3 {
		t.Errorf("expected 3 cache entries, got %d", stats.Entries)
	}

	// Run scan again - should all be cache hits
	progressUpdates = nil
	if err := cache.Scan(imgDir, callback); err != nil {
		t.Fatalf("second scan failed: %v", err)
	}

	// Cached count should still be 3 (re-used from cache)
	final = progressUpdates[len(progressUpdates)-1]
	if final.Cached != 3 {
		t.Errorf("expected 3 cached on re-scan, got %d", final.Cached)
	}
}

func TestScanWithSubdirs(t *testing.T) {
	// Create temp directory with subdirectories
	imgDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
	if err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	defer cleanup()

	// Create cache
	cacheDir := t.TempDir()
	cache, err := New(filepath.Join(cacheDir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	var final ScanProgress
	if err := cache.Scan(imgDir, func(p ScanProgress) {
		final = p
	}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// Should find 2 images (red.jpg in root, green.jpg in subdir)
	// Note: non-image files are skipped
	if final.Total != 2 {
		t.Errorf("expected 2 images across directories, got %d", final.Total)
	}
}

func TestScanEmptyDirectory(t *testing.T) {
	// Create empty temp directory
	imgDir := t.TempDir()

	// Create cache
	cacheDir := t.TempDir()
	cache, err := New(filepath.Join(cacheDir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	var final ScanProgress
	if err := cache.Scan(imgDir, func(p ScanProgress) {
		final = p
	}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if final.Total != 0 {
		t.Errorf("expected 0 images in empty directory, got %d", final.Total)
	}
	if !final.Done {
		t.Error("expected Done=true for empty directory scan")
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("failed to get default path: %v", err)
	}

	if path == "" {
		t.Error("expected non-empty default path")
	}

	if !filepath.IsAbs(path) {
		t.Error("expected absolute path")
	}

	if filepath.Base(path) != "cache.db" {
		t.Errorf("expected cache.db, got %s", filepath.Base(path))
	}

	if !contains(path, ".imgsearch") {
		t.Errorf("expected path to contain .imgsearch, got %s", path)
	}
}

func TestMakeKey(t *testing.T) {
	path := "/path/to/image.jpg"
	mtime1 := time.Unix(1000, 0)
	mtime2 := time.Unix(1000, 1) // 1 nanosecond later

	key1 := makeKey(path, mtime1)
	key2 := makeKey(path, mtime2)

	// Keys should be different for different mtimes
	if string(key1) == string(key2) {
		t.Error("keys should differ for different modification times")
	}

	// Same path and mtime should produce same key
	key1Again := makeKey(path, mtime1)
	if string(key1) != string(key1Again) {
		t.Error("same inputs should produce same key")
	}
}

func TestCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	cache, err := New(filepath.Join(dir, "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// First close should succeed
	if err := cache.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	// Second close should also not panic (though may return error)
	cache.Close()
}

func TestListDirectories(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	cache, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Initially should be empty
	dirs := cache.ListDirectories()
	if len(dirs) != 0 {
		t.Errorf("expected empty directories, got %d", len(dirs))
	}

	// Add entries from different directories
	mtime := time.Now()
	testData := []struct {
		path string
		data *hash.Data
	}{
		{"/photos/vacation/img1.jpg", &hash.Data{Path: "/photos/vacation/img1.jpg", PHash: 1}},
		{"/photos/vacation/img2.jpg", &hash.Data{Path: "/photos/vacation/img2.jpg", PHash: 2}},
		{"/photos/family/img3.jpg", &hash.Data{Path: "/photos/family/img3.jpg", PHash: 3}},
		{"/documents/scan.jpg", &hash.Data{Path: "/documents/scan.jpg", PHash: 4}},
	}

	for _, td := range testData {
		if err := cache.Put(td.path, mtime, td.data); err != nil {
			t.Fatalf("failed to put: %v", err)
		}
	}

	// List directories
	dirs = cache.ListDirectories()

	// Should have 3 directories
	if len(dirs) != 3 {
		t.Errorf("expected 3 directories, got %d", len(dirs))
	}

	// Check counts
	dirMap := make(map[string]int)
	for _, d := range dirs {
		dirMap[d.Path] = d.Count
	}

	if dirMap["/photos/vacation"] != 2 {
		t.Errorf("expected /photos/vacation to have 2 images, got %d", dirMap["/photos/vacation"])
	}
	if dirMap["/photos/family"] != 1 {
		t.Errorf("expected /photos/family to have 1 image, got %d", dirMap["/photos/family"])
	}
	if dirMap["/documents"] != 1 {
		t.Errorf("expected /documents to have 1 image, got %d", dirMap["/documents"])
	}

	// Verify sorted order
	for i := 1; i < len(dirs); i++ {
		if dirs[i-1].Path > dirs[i].Path {
			t.Errorf("directories not sorted: %s > %s", dirs[i-1].Path, dirs[i].Path)
		}
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

// TestIntegrationCachePersistence tests that cache persists across sessions
func TestIntegrationCachePersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persistent.db")
	mtime := time.Now()

	// Session 1: Create cache and add data
	cache1, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	testData := &hash.Data{
		Path:  "/test/image.jpg",
		PHash: 0xDEADBEEF,
		AHash: 0xCAFEBABE,
		DHash: 0x12345678,
	}

	if err := cache1.Put("/test/image.jpg", mtime, testData); err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	stats1 := cache1.Stats()
	if stats1.Entries != 1 {
		t.Errorf("expected 1 entry, got %d", stats1.Entries)
	}

	// Close session 1
	if err := cache1.Close(); err != nil {
		t.Fatalf("failed to close cache: %v", err)
	}

	// Session 2: Reopen cache and verify data persisted
	cache2, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen cache: %v", err)
	}
	defer cache2.Close()

	// Verify entry count persisted
	stats2 := cache2.Stats()
	if stats2.Entries != 1 {
		t.Errorf("expected 1 entry after reopen, got %d", stats2.Entries)
	}

	// Verify data is retrievable
	data, ok := cache2.Get("/test/image.jpg", mtime)
	if !ok {
		t.Fatal("expected to find cached entry after reopen")
	}

	if data.PHash != testData.PHash {
		t.Errorf("PHash mismatch after reopen: got %x, want %x", data.PHash, testData.PHash)
	}
	if data.AHash != testData.AHash {
		t.Errorf("AHash mismatch after reopen: got %x, want %x", data.AHash, testData.AHash)
	}
	if data.DHash != testData.DHash {
		t.Errorf("DHash mismatch after reopen: got %x, want %x", data.DHash, testData.DHash)
	}
}

// TestIntegrationScanThenSearch tests scanning images then verifying cache hits
func TestIntegrationScanThenSearch(t *testing.T) {
	// Create test images
	images := map[string]image.Image{
		"red.jpg":    testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255}),
		"green.jpg":  testutil.SolidColorImage(64, 64, color.RGBA{0, 255, 0, 255}),
		"blue.jpg":   testutil.SolidColorImage(64, 64, color.RGBA{0, 0, 255, 255}),
		"yellow.jpg": testutil.SolidColorImage(64, 64, color.RGBA{255, 255, 0, 255}),
	}
	imgDir, cleanup, err := testutil.CreateTempDir(images)
	if err != nil {
		t.Fatalf("failed to create test images: %v", err)
	}
	defer cleanup()

	// Create cache
	cache, err := New(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Scan directory to populate cache
	var scanProgress ScanProgress
	if err := cache.Scan(imgDir, func(p ScanProgress) {
		scanProgress = p
	}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if scanProgress.Total != 4 {
		t.Errorf("expected 4 images scanned, got %d", scanProgress.Total)
	}
	if scanProgress.Cached != 4 {
		t.Errorf("expected 4 images cached, got %d", scanProgress.Cached)
	}

	// Verify cache stats
	stats := cache.Stats()
	if stats.Entries != 4 {
		t.Errorf("expected 4 cache entries, got %d", stats.Entries)
	}

	// Now simulate searching - get each image by its path and mtime
	files, _ := filepath.Glob(filepath.Join(imgDir, "*.jpg"))
	hitsBefore := stats.Hits

	for _, file := range files {
		info, _ := os.Stat(file)
		data, ok := cache.Get(file, info.ModTime())
		if !ok {
			t.Errorf("expected cache hit for %s", file)
		}
		if data.PHash == 0 {
			t.Errorf("expected non-zero PHash for %s", file)
		}
	}

	// Verify we got cache hits
	statsAfter := cache.Stats()
	hitsAfter := statsAfter.Hits
	if hitsAfter-hitsBefore != 4 {
		t.Errorf("expected 4 cache hits, got %d", hitsAfter-hitsBefore)
	}
}

// TestIntegrationCacheInvalidationOnModify tests that modified files miss cache
func TestIntegrationCacheInvalidationOnModify(t *testing.T) {
	// Create a test image
	images := map[string]image.Image{
		"test.jpg": testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255}),
	}
	imgDir, cleanup, err := testutil.CreateTempDir(images)
	if err != nil {
		t.Fatalf("failed to create test images: %v", err)
	}
	defer cleanup()

	cache, err := New(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	imgPath := filepath.Join(imgDir, "test.jpg")

	// Get original mtime and cache the image
	info1, _ := os.Stat(imgPath)
	mtime1 := info1.ModTime()

	testData := &hash.Data{Path: imgPath, PHash: 12345}
	cache.Put(imgPath, mtime1, testData)

	// Verify cache hit with original mtime
	_, ok := cache.Get(imgPath, mtime1)
	if !ok {
		t.Error("expected cache hit with original mtime")
	}

	// Wait a bit and "modify" the file by updating mtime
	time.Sleep(10 * time.Millisecond)
	newTime := time.Now()
	os.Chtimes(imgPath, newTime, newTime)

	// Get new mtime
	info2, _ := os.Stat(imgPath)
	mtime2 := info2.ModTime()

	// Verify cache miss with new mtime
	_, ok = cache.Get(imgPath, mtime2)
	if ok {
		t.Error("expected cache miss after file modification")
	}

	// Verify stats show the miss
	stats := cache.Stats()
	if stats.Misses < 1 {
		t.Error("expected at least 1 cache miss")
	}
}

// TestIntegrationListDirectoriesAfterScan tests ListDirectories after scanning
func TestIntegrationListDirectoriesAfterScan(t *testing.T) {
	// Create directory structure with subdirectories
	imgDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
	if err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	defer cleanup()

	cache, err := New(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Scan directory
	if err := cache.Scan(imgDir, func(p ScanProgress) {}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// List directories
	dirs := cache.ListDirectories()

	// Should have entries from different directories
	if len(dirs) < 1 {
		t.Error("expected at least 1 directory after scan")
	}

	// Verify all directories have positive counts
	for _, dir := range dirs {
		if dir.Count <= 0 {
			t.Errorf("directory %s has invalid count: %d", dir.Path, dir.Count)
		}
	}
}

// TestIntegrationClearAndRescan tests clearing cache then rescanning
func TestIntegrationClearAndRescan(t *testing.T) {
	images := map[string]image.Image{
		"img1.jpg": testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255}),
		"img2.jpg": testutil.SolidColorImage(32, 32, color.RGBA{0, 255, 0, 255}),
	}
	imgDir, cleanup, err := testutil.CreateTempDir(images)
	if err != nil {
		t.Fatalf("failed to create test images: %v", err)
	}
	defer cleanup()

	cache, err := New(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// First scan
	cache.Scan(imgDir, func(p ScanProgress) {})
	stats1 := cache.Stats()
	if stats1.Entries != 2 {
		t.Errorf("expected 2 entries after first scan, got %d", stats1.Entries)
	}

	// Clear cache
	if err := cache.Clear(); err != nil {
		t.Fatalf("failed to clear cache: %v", err)
	}

	stats2 := cache.Stats()
	if stats2.Entries != 0 {
		t.Errorf("expected 0 entries after clear, got %d", stats2.Entries)
	}

	// Rescan
	var rescanProgress ScanProgress
	cache.Scan(imgDir, func(p ScanProgress) {
		rescanProgress = p
	})

	// Should have cached new entries (not reused from cache)
	if rescanProgress.Cached != 2 {
		t.Errorf("expected 2 newly cached after rescan, got %d", rescanProgress.Cached)
	}

	stats3 := cache.Stats()
	if stats3.Entries != 2 {
		t.Errorf("expected 2 entries after rescan, got %d", stats3.Entries)
	}
}

// TestIntegrationConcurrentScanAndGet tests concurrent scanning and getting
func TestIntegrationConcurrentScanAndGet(t *testing.T) {
	images := map[string]image.Image{
		"a.jpg": testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255}),
		"b.jpg": testutil.SolidColorImage(32, 32, color.RGBA{0, 255, 0, 255}),
		"c.jpg": testutil.SolidColorImage(32, 32, color.RGBA{0, 0, 255, 255}),
	}
	imgDir, cleanup, err := testutil.CreateTempDir(images)
	if err != nil {
		t.Fatalf("failed to create test images: %v", err)
	}
	defer cleanup()

	cache, err := New(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	var wg sync.WaitGroup

	// Start scan in background
	wg.Add(1)
	go func() {
		defer wg.Done()
		cache.Scan(imgDir, func(p ScanProgress) {})
	}()

	// Concurrent Gets while scanning
	mtime := time.Now()
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Get(filepath.Join(imgDir, "a.jpg"), mtime)
			cache.Get(filepath.Join(imgDir, "b.jpg"), mtime)
			cache.Get(filepath.Join(imgDir, "c.jpg"), mtime)
		}()
	}

	wg.Wait()

	// Should have completed without panic
	stats := cache.Stats()
	if stats.Entries != 3 {
		t.Errorf("expected 3 entries, got %d", stats.Entries)
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
