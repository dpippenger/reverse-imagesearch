package main

import (
	"flag"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"imgsearch/internal/testutil"
)

// resetFlags resets the global flag state for test isolation.
func resetFlags() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
}

func TestRunCLISearch(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Create temp dir with test images
	tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanup()

	// Create source image
	sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
	sourcePath, err := testutil.CreateTempJPEG(sourceImg)
	if err != nil {
		t.Fatalf("Failed to create source JPEG: %v", err)
	}
	defer os.Remove(sourcePath)

	t.Run("basic search completes without error", func(t *testing.T) {
		resetFlags()
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-threshold", "0"}
		err := run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}
	})

	t.Run("search with output file", func(t *testing.T) {
		resetFlags()
		outputPath := filepath.Join(t.TempDir(), "results.txt")
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-threshold", "0", "-output", outputPath}
		err := run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}

		// Verify output file was created
		data, err := os.ReadFile(outputPath)
		if err != nil {
			t.Fatalf("Failed to read output file: %v", err)
		}
		if !strings.Contains(string(data), "Similar Images") {
			t.Error("Output file should contain results header")
		}
	})

	t.Run("search with verbose flag", func(t *testing.T) {
		resetFlags()
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-verbose"}
		err := run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}
	})

	t.Run("search with top-N limit", func(t *testing.T) {
		resetFlags()
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-top", "1", "-threshold", "0"}
		err := run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}
	})

	t.Run("search with cache", func(t *testing.T) {
		resetFlags()
		cachePath := filepath.Join(t.TempDir(), "cache.db")
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-cache-path", cachePath}
		err := run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}

		// Verify cache file was created
		if _, err := os.Stat(cachePath); os.IsNotExist(err) {
			t.Error("Cache file should have been created")
		}
	})
}

func TestRunErrors(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	t.Run("non-existent source image", func(t *testing.T) {
		resetFlags()
		os.Args = []string{"imgsearch", "-source", "/nonexistent/image.jpg", "-dir", "."}
		err := run()
		if err == nil {
			t.Error("Expected error for non-existent source")
		}
		if !strings.Contains(err.Error(), "loading source image") {
			t.Errorf("Error should mention loading source, got: %v", err)
		}
	})

	t.Run("invalid cache path warning does not error", func(t *testing.T) {
		resetFlags()
		// Create a valid source image first
		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, err := testutil.CreateTempJPEG(sourceImg)
		if err != nil {
			t.Fatalf("Failed to create source: %v", err)
		}
		defer os.Remove(sourcePath)

		tmpDir := t.TempDir()
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-cache-path", "/dev/null/bad/cache.db"}
		// Should not error — bad cache path just prints warning
		err = run()
		if err != nil {
			t.Errorf("run() should succeed even with bad cache path: %v", err)
		}
	})

	t.Run("no-cache flag disables caching", func(t *testing.T) {
		resetFlags()
		sourceImg := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		sourcePath, err := testutil.CreateTempJPEG(sourceImg)
		if err != nil {
			t.Fatalf("Failed to create source: %v", err)
		}
		defer os.Remove(sourcePath)

		tmpDir := t.TempDir()
		cachePath := filepath.Join(tmpDir, "cache.db")
		os.Args = []string{"imgsearch", "-source", sourcePath, "-dir", tmpDir, "-cache-path", cachePath, "-no-cache"}
		err = run()
		if err != nil {
			t.Errorf("run() returned error: %v", err)
		}

		// Cache file should NOT exist
		if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
			t.Error("Cache file should not be created with -no-cache")
		}
	})
}
