package imgutil

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"imgsearch/internal/hash"
	"imgsearch/internal/testutil"
)

func TestIsImageFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"lowercase jpg", "image.jpg", true},
		{"uppercase JPG", "image.JPG", true},
		{"lowercase jpeg", "image.jpeg", true},
		{"uppercase JPEG", "image.JPEG", true},
		{"mixed case Jpg", "image.Jpg", true},
		{"png file", "image.png", false},
		{"gif file", "image.gif", false},
		{"webp file", "image.webp", false},
		{"no extension", "image", false},
		{"empty string", "", false},
		{"double extension", "image.jpg.txt", false},
		{"hidden file jpg", ".hidden.jpg", true},
		{"path with directory", "/path/to/image.jpg", true},
		{"relative path", "../images/photo.jpeg", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsImageFile(tt.path)
			if result != tt.expected {
				t.Errorf("IsImageFile(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestComputeSimilarity(t *testing.T) {
	t.Run("identical data returns 100", func(t *testing.T) {
		data := hash.Data{
			PHash:     0xFFFFFFFFFFFFFFFF,
			AHash:     0xFFFFFFFFFFFFFFFF,
			DHash:     0xFFFFFFFFFFFFFFFF,
			Histogram: hash.ColorHistogram{},
		}
		// Set histogram to have values
		data.Histogram.R[0] = 1.0
		data.Histogram.G[0] = 1.0
		data.Histogram.B[0] = 1.0

		similarity := ComputeSimilarity(data, data)
		if similarity != 100.0 {
			t.Errorf("Identical data similarity = %f, want 100.0", similarity)
		}
	})

	t.Run("completely different data returns low similarity", func(t *testing.T) {
		source := hash.Data{
			PHash: 0x0000000000000000,
			AHash: 0x0000000000000000,
			DHash: 0x0000000000000000,
		}
		source.Histogram.R[0] = 1.0
		source.Histogram.G[0] = 1.0
		source.Histogram.B[0] = 1.0

		target := hash.Data{
			PHash: 0xFFFFFFFFFFFFFFFF,
			AHash: 0xFFFFFFFFFFFFFFFF,
			DHash: 0xFFFFFFFFFFFFFFFF,
		}
		target.Histogram.R[15] = 1.0
		target.Histogram.G[15] = 1.0
		target.Histogram.B[15] = 1.0

		similarity := ComputeSimilarity(source, target)
		// Should be very low (close to 0)
		if similarity > 10 {
			t.Errorf("Different data similarity = %f, want < 10", similarity)
		}
	})

	t.Run("weight distribution is correct", func(t *testing.T) {
		// Create data where only pHash matches (35% weight)
		source := hash.Data{
			PHash: 0xFFFFFFFFFFFFFFFF,
			AHash: 0x0000000000000000,
			DHash: 0x0000000000000000,
		}
		target := hash.Data{
			PHash: 0xFFFFFFFFFFFFFFFF,
			AHash: 0xFFFFFFFFFFFFFFFF,
			DHash: 0xFFFFFFFFFFFFFFFF,
		}

		similarity := ComputeSimilarity(source, target)
		// pHash: 100% * 0.35 = 35
		// aHash: 0% * 0.20 = 0
		// dHash: 0% * 0.25 = 0
		// histogram: 0% * 0.20 = 0
		// Total should be around 35
		if similarity < 30 || similarity > 40 {
			t.Errorf("Weight distribution incorrect: similarity = %f, expected ~35", similarity)
		}
	})
}

func TestGrayscale(t *testing.T) {
	t.Run("output type is image.Gray", func(t *testing.T) {
		img := testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
		gray := Grayscale(img)

		if _, ok := interface{}(gray).(*image.Gray); !ok {
			t.Error("Grayscale did not return *image.Gray")
		}
	})

	t.Run("bounds are preserved", func(t *testing.T) {
		img := testutil.SolidColorImage(100, 50, color.White)
		gray := Grayscale(img)

		if gray.Bounds() != img.Bounds() {
			t.Errorf("Bounds mismatch: got %v, want %v", gray.Bounds(), img.Bounds())
		}
	})

	t.Run("white stays white", func(t *testing.T) {
		img := testutil.SolidColorImage(10, 10, color.White)
		gray := Grayscale(img)

		pixel := gray.GrayAt(5, 5)
		if pixel.Y != 255 {
			t.Errorf("White pixel grayscale = %d, want 255", pixel.Y)
		}
	})

	t.Run("black stays black", func(t *testing.T) {
		img := testutil.SolidColorImage(10, 10, color.Black)
		gray := Grayscale(img)

		pixel := gray.GrayAt(5, 5)
		if pixel.Y != 0 {
			t.Errorf("Black pixel grayscale = %d, want 0", pixel.Y)
		}
	})

	t.Run("red converts to expected luminosity", func(t *testing.T) {
		img := testutil.SolidColorImage(10, 10, color.RGBA{255, 0, 0, 255})
		gray := Grayscale(img)

		pixel := gray.GrayAt(5, 5)
		expected := uint8(76) // 0.299 * 255 = ~76
		if pixel.Y < expected-2 || pixel.Y > expected+2 {
			t.Errorf("Red pixel grayscale = %d, want ~%d", pixel.Y, expected)
		}
	})
}

func TestLoadAndHash(t *testing.T) {
	t.Run("valid JPEG file", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		data := LoadAndHash(path)

		if data.Error != nil {
			t.Errorf("LoadAndHash returned error: %v", data.Error)
		}
		if data.Path != path {
			t.Errorf("Path = %q, want %q", data.Path, path)
		}
		// Verify hashes were computed (non-zero for non-trivial images)
		if data.PHash == 0 && data.AHash == 0 && data.DHash == 0 {
			t.Error("All hashes are zero")
		}
	})

	t.Run("non-existent file", func(t *testing.T) {
		data := LoadAndHash("/nonexistent/path/to/image.jpg")

		if data.Error == nil {
			t.Error("Expected error for non-existent file")
		}
	})

	t.Run("invalid image file", func(t *testing.T) {
		// Create a temp file with non-image content
		tmpFile, err := os.CreateTemp("", "test-*.jpg")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.Write(testutil.NotAnImage())
		tmpFile.Close()

		data := LoadAndHash(tmpFile.Name())

		if data.Error == nil {
			t.Error("Expected error for invalid image file")
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

		data := LoadAndHash(tmpFile.Name())

		if data.Error == nil {
			t.Error("Expected error for corrupted JPEG")
		}
	})
}

func TestLoadAndHashFromReader(t *testing.T) {
	t.Run("valid JPEG reader", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{0, 255, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)
		reader := bytes.NewReader(jpegBytes)

		data, err := LoadAndHashFromReader(reader)

		if err != nil {
			t.Errorf("LoadAndHashFromReader returned error: %v", err)
		}
		// Verify hashes were computed
		if data.PHash == 0 && data.AHash == 0 && data.DHash == 0 {
			t.Error("All hashes are zero")
		}
	})

	t.Run("empty reader", func(t *testing.T) {
		reader := bytes.NewReader(testutil.EmptyBytes())

		_, err := LoadAndHashFromReader(reader)

		if err == nil {
			t.Error("Expected error for empty reader")
		}
	})

	t.Run("invalid image data", func(t *testing.T) {
		reader := bytes.NewReader(testutil.NotAnImage())

		_, err := LoadAndHashFromReader(reader)

		if err == nil {
			t.Error("Expected error for invalid image data")
		}
	})

	t.Run("corrupted JPEG data", func(t *testing.T) {
		reader := bytes.NewReader(testutil.CorruptedJPEG())

		_, err := LoadAndHashFromReader(reader)

		if err == nil {
			t.Error("Expected error for corrupted JPEG data")
		}
	})
}

func TestFindImages(t *testing.T) {
	t.Run("finds images recursively", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		images, err := FindImages(tmpDir)

		if err != nil {
			t.Errorf("FindImages returned error: %v", err)
		}
		// Should find red.jpg and subdir/green.jpg
		if len(images) != 2 {
			t.Errorf("Found %d images, want 2", len(images))
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "empty-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		images, err := FindImages(tmpDir)

		if err != nil {
			t.Errorf("FindImages returned error: %v", err)
		}
		if len(images) != 0 {
			t.Errorf("Found %d images in empty dir, want 0", len(images))
		}
	})

	t.Run("filters non-image files", func(t *testing.T) {
		tmpDir, cleanup, err := testutil.CreateTempDirWithSubdirs()
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer cleanup()

		images, err := FindImages(tmpDir)

		if err != nil {
			t.Errorf("FindImages returned error: %v", err)
		}

		// Verify no .txt files in results
		for _, img := range images {
			if filepath.Ext(img) == ".txt" {
				t.Errorf("Non-image file included: %s", img)
			}
		}
	})

	t.Run("non-existent directory returns empty list", func(t *testing.T) {
		// FindImages logs warnings but continues - returns empty list, no error
		images, err := FindImages("/nonexistent/directory/path")

		// The function logs a warning but doesn't return an error
		// This is by design - it continues walking even on access errors
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if len(images) != 0 {
			t.Errorf("Expected empty list, got %d images", len(images))
		}
	})
}

func TestGenerateThumbnail(t *testing.T) {
	t.Run("generates valid base64", func(t *testing.T) {
		img := testutil.SolidColorImage(200, 200, color.RGBA{0, 0, 255, 255})
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		thumb, err := GenerateThumbnail(path, 100)

		if err != nil {
			t.Errorf("GenerateThumbnail returned error: %v", err)
		}

		// Verify it's valid base64
		decoded, err := base64.StdEncoding.DecodeString(thumb)
		if err != nil {
			t.Errorf("Invalid base64: %v", err)
		}

		// Verify it's a valid JPEG
		_, _, err = image.Decode(bytes.NewReader(decoded))
		if err != nil {
			t.Errorf("Decoded data is not valid image: %v", err)
		}
	})

	t.Run("respects max size", func(t *testing.T) {
		img := testutil.SolidColorImage(500, 500, color.White)
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		thumb, err := GenerateThumbnail(path, 50)
		if err != nil {
			t.Fatalf("GenerateThumbnail returned error: %v", err)
		}

		decoded, _ := base64.StdEncoding.DecodeString(thumb)
		thumbImg, _, _ := image.Decode(bytes.NewReader(decoded))

		bounds := thumbImg.Bounds()
		if bounds.Dx() > 50 || bounds.Dy() > 50 {
			t.Errorf("Thumbnail too large: %dx%d, max 50x50", bounds.Dx(), bounds.Dy())
		}
	})

	t.Run("preserves aspect ratio", func(t *testing.T) {
		// Create a 200x100 image (2:1 ratio)
		img := testutil.SolidColorImage(200, 100, color.White)
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		thumb, err := GenerateThumbnail(path, 100)
		if err != nil {
			t.Fatalf("GenerateThumbnail returned error: %v", err)
		}

		decoded, _ := base64.StdEncoding.DecodeString(thumb)
		thumbImg, _, _ := image.Decode(bytes.NewReader(decoded))

		bounds := thumbImg.Bounds()
		ratio := float64(bounds.Dx()) / float64(bounds.Dy())
		// Should maintain ~2:1 ratio
		if ratio < 1.8 || ratio > 2.2 {
			t.Errorf("Aspect ratio not preserved: %dx%d = %.2f, want ~2.0", bounds.Dx(), bounds.Dy(), ratio)
		}
	})

	t.Run("non-existent file", func(t *testing.T) {
		_, err := GenerateThumbnail("/nonexistent/image.jpg", 100)

		if err == nil {
			t.Error("Expected error for non-existent file")
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

		_, err = GenerateThumbnail(tmpFile.Name(), 100)

		if err == nil {
			t.Error("Expected error for invalid image")
		}
	})
}

// Benchmark tests
func BenchmarkLoadAndHash(b *testing.B) {
	img := testutil.GradientImage(256, 256)
	path, err := testutil.CreateTempJPEG(img)
	if err != nil {
		b.Fatalf("Failed to create temp JPEG: %v", err)
	}
	defer os.Remove(path)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LoadAndHash(path)
	}
}

func BenchmarkComputeSimilarity(b *testing.B) {
	data := hash.Data{
		PHash: 0xAAAAAAAAAAAAAAAA,
		AHash: 0x5555555555555555,
		DHash: 0xFFFF0000FFFF0000,
	}
	data.Histogram.R[0] = 0.5
	data.Histogram.G[8] = 0.5
	data.Histogram.B[15] = 0.5

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeSimilarity(data, data)
	}
}

func BenchmarkGenerateThumbnail(b *testing.B) {
	img := testutil.GradientImage(500, 500)
	path, err := testutil.CreateTempJPEG(img)
	if err != nil {
		b.Fatalf("Failed to create temp JPEG: %v", err)
	}
	defer os.Remove(path)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GenerateThumbnail(path, 100)
	}
}
