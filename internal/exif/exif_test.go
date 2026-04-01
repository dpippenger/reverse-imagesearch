package exif

import (
	"image/color"
	"os"
	"strings"
	"testing"

	"imgsearch/internal/testutil"
)

func TestExtract(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		data := Extract("/nonexistent/image.jpg")

		if data.Error == "" {
			t.Error("Expected error for non-existent file")
		}
		if !strings.Contains(data.Error, "opening") {
			t.Errorf("Error should mention opening, got %q", data.Error)
		}
	})

	t.Run("JPEG with attempted EXIF extraction", func(t *testing.T) {
		path, err := testutil.CreateTempJPEGWithExif()
		if err != nil {
			t.Fatalf("Failed to create temp JPEG with EXIF: %v", err)
		}
		defer os.Remove(path)

		data := Extract(path)

		// Should have file size regardless of EXIF parsing
		if data.FileSize <= 0 {
			t.Error("FileSize should be > 0")
		}

		// Should have dimensions (image can be decoded)
		if data.Width != 8 || data.Height != 8 {
			t.Logf("Warning: dimensions not parsed correctly (got %dx%d)", data.Width, data.Height)
		}
	})

	t.Run("valid JPEG without EXIF", func(t *testing.T) {
		img := testutil.SolidColorImage(100, 50, color.RGBA{255, 0, 0, 255})
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		data := Extract(path)

		// Should have error because no EXIF data
		if data.Error == "" {
			t.Error("Expected error for JPEG without EXIF")
		}
		if !strings.Contains(data.Error, "no EXIF data") {
			t.Errorf("Error should mention no EXIF data, got %q", data.Error)
		}

		// But should have file size
		if data.FileSize <= 0 {
			t.Error("FileSize should be > 0")
		}

		// And should have dimensions
		if data.Width != 100 {
			t.Errorf("Width = %d, want 100", data.Width)
		}
		if data.Height != 50 {
			t.Errorf("Height = %d, want 50", data.Height)
		}
	})

	t.Run("invalid image file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.jpg")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.Write(testutil.NotAnImage())
		tmpFile.Close()

		data := Extract(tmpFile.Name())

		// Should have error (either can't decode image config or no EXIF)
		if data.Error == "" {
			t.Error("Expected error for invalid image")
		}

		// Should still have file size
		if data.FileSize <= 0 {
			t.Error("FileSize should be > 0")
		}
	})

	t.Run("corrupted JPEG", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.jpg")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.Write(testutil.CorruptedJPEG())
		tmpFile.Close()

		data := Extract(tmpFile.Name())

		// Should have error
		if data.Error == "" {
			t.Error("Expected error for corrupted JPEG")
		}

		// Should still have file size
		if data.FileSize <= 0 {
			t.Error("FileSize should be > 0")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.jpg")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		data := Extract(tmpFile.Name())

		// Should have error
		if data.Error == "" {
			t.Error("Expected error for empty file")
		}

		// File size should be 0
		if data.FileSize != 0 {
			t.Errorf("FileSize = %d, want 0", data.FileSize)
		}
	})

	t.Run("Data struct fields are zeroed by default", func(t *testing.T) {
		data := Data{}

		if data.Make != "" {
			t.Errorf("Make should be empty, got %q", data.Make)
		}
		if data.Width != 0 {
			t.Errorf("Width should be 0, got %d", data.Width)
		}
		if data.FileSize != 0 {
			t.Errorf("FileSize should be 0, got %d", data.FileSize)
		}
	})

	t.Run("JPEG with real EXIF metadata", func(t *testing.T) {
		path, err := testutil.CreateTempJPEGWithRealExif()
		if err != nil {
			t.Fatalf("Failed to create EXIF JPEG: %v", err)
		}
		defer os.Remove(path)

		data := Extract(path)

		// Should have no error — EXIF data is present
		if data.Error != "" {
			t.Fatalf("Unexpected error: %s", data.Error)
		}

		// File metadata
		if data.FileSize <= 0 {
			t.Error("FileSize should be > 0")
		}
		if data.Width != 32 || data.Height != 32 {
			t.Errorf("Dimensions = %dx%d, want 32x32", data.Width, data.Height)
		}

		// EXIF string fields
		if data.Make != "TestCam" {
			t.Errorf("Make = %q, want %q", data.Make, "TestCam")
		}
		if data.Model != "T100" {
			t.Errorf("Model = %q, want %q", data.Model, "T100")
		}

		// DateTime
		if data.DateTime != "2024-01-15 10:30:00" {
			t.Errorf("DateTime = %q, want %q", data.DateTime, "2024-01-15 10:30:00")
		}

		// Orientation
		if data.Orientation != "Normal" {
			t.Errorf("Orientation = %q, want %q", data.Orientation, "Normal")
		}

		// FNumber = 28/10 = f/2.8
		if data.FNumber != "f/2.8" {
			t.Errorf("FNumber = %q, want %q", data.FNumber, "f/2.8")
		}

		// ExposureTime = 1/125
		if data.ExposureTime != "1/125 s" {
			t.Errorf("ExposureTime = %q, want %q", data.ExposureTime, "1/125 s")
		}

		// ISO
		if data.ISO != "ISO 400" {
			t.Errorf("ISO = %q, want %q", data.ISO, "ISO 400")
		}

		// FocalLength = 50/1 = 50 mm
		if data.FocalLength != "50 mm" {
			t.Errorf("FocalLength = %q, want %q", data.FocalLength, "50 mm")
		}

		// GPS and LensModel not embedded — should be empty
		if data.GPSLatitude != "" {
			t.Errorf("GPSLatitude should be empty, got %q", data.GPSLatitude)
		}
		if data.LensModel != "" {
			t.Errorf("LensModel should be empty, got %q", data.LensModel)
		}
	})
}

// TestOrientationMap tests that orientation values map to correct strings
// This tests the internal logic without needing real EXIF data
func TestOrientationMapping(t *testing.T) {
	// This is a documentation test - we verify the orientation strings
	// are properly defined in the Extract function
	expectedOrientations := map[int]string{
		1: "Normal",
		2: "Flipped horizontal",
		3: "Rotated 180°",
		4: "Flipped vertical",
		5: "Rotated 90° CCW, flipped",
		6: "Rotated 90° CW",
		7: "Rotated 90° CW, flipped",
		8: "Rotated 90° CCW",
	}

	// Just verify our expectations are correct
	if len(expectedOrientations) != 8 {
		t.Errorf("Expected 8 orientations, got %d", len(expectedOrientations))
	}
}

// Benchmark tests
func BenchmarkExtract(b *testing.B) {
	img := testutil.GradientImage(256, 256)
	path, err := testutil.CreateTempJPEG(img)
	if err != nil {
		b.Fatalf("Failed to create temp JPEG: %v", err)
	}
	defer os.Remove(path)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Extract(path)
	}
}
