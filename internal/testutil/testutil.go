// Package testutil provides helper functions for generating test images
// and other test utilities. All images are generated programmatically
// to avoid binary files in the repository.
package testutil

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
)

// SolidColorImage creates an image filled with a single color.
func SolidColorImage(width, height int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// GradientImage creates an image with a horizontal gradient from black to white.
func GradientImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gray := uint8((x * 255) / width)
			img.Set(x, y, color.RGBA{gray, gray, gray, 255})
		}
	}
	return img
}

// CheckerboardImage creates a checkerboard pattern image.
func CheckerboardImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	blockSize := 8
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			isWhite := ((x/blockSize)+(y/blockSize))%2 == 0
			if isWhite {
				img.Set(x, y, color.White)
			} else {
				img.Set(x, y, color.Black)
			}
		}
	}
	return img
}

// RedGreenImage creates an image that is half red and half green.
func RedGreenImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x < width/2 {
				img.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				img.Set(x, y, color.RGBA{0, 255, 0, 255})
			}
		}
	}
	return img
}

// EncodeJPEG encodes an image to JPEG format and returns the bytes.
func EncodeJPEG(img image.Image) []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

// EncodeJPEGWithQuality encodes an image to JPEG with specified quality.
func EncodeJPEGWithQuality(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes()
}

// CorruptedJPEG returns invalid JPEG bytes for error testing.
func CorruptedJPEG() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00}
}

// EmptyBytes returns an empty byte slice for testing empty input.
func EmptyBytes() []byte {
	return []byte{}
}

// NotAnImage returns bytes that are clearly not an image.
func NotAnImage() []byte {
	return []byte("This is not an image file, just plain text.")
}

// CreateTempJPEG creates a temporary JPEG file and returns its path.
// The caller is responsible for cleaning up the file.
func CreateTempJPEG(img image.Image) (string, error) {
	tmpDir := os.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "test-*.jpg")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if err := jpeg.Encode(tmpFile, img, &jpeg.Options{Quality: 90}); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// CreateTempDir creates a temporary directory with test images.
// Returns the directory path and a cleanup function.
func CreateTempDir(images map[string]image.Image) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "imgsearch-test-*")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	for name, img := range images {
		path := filepath.Join(tmpDir, name)
		file, err := os.Create(path)
		if err != nil {
			cleanup()
			return "", nil, err
		}

		if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
			file.Close()
			cleanup()
			return "", nil, err
		}
		file.Close()
	}

	return tmpDir, cleanup, nil
}

// CreateTempDirWithSubdirs creates a temp directory with subdirectories containing images.
func CreateTempDirWithSubdirs() (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "imgsearch-test-*")
	if err != nil {
		return "", nil, err
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Create subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		cleanup()
		return "", nil, err
	}

	// Create images in root
	img1 := SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255})
	path1 := filepath.Join(tmpDir, "red.jpg")
	if err := saveJPEG(path1, img1); err != nil {
		cleanup()
		return "", nil, err
	}

	// Create images in subdirectory
	img2 := SolidColorImage(32, 32, color.RGBA{0, 255, 0, 255})
	path2 := filepath.Join(subDir, "green.jpg")
	if err := saveJPEG(path2, img2); err != nil {
		cleanup()
		return "", nil, err
	}

	// Create a non-image file
	txtPath := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtPath, []byte("not an image"), 0644); err != nil {
		cleanup()
		return "", nil, err
	}

	return tmpDir, cleanup, nil
}

func saveJPEG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return jpeg.Encode(file, img, &jpeg.Options{Quality: 90})
}

// CreateTempJPEGWithExif creates a temporary JPEG file.
// Note: This creates a standard JPEG without real EXIF metadata.
// Testing real EXIF extraction requires images with embedded EXIF data.
func CreateTempJPEGWithExif() (string, error) {
	// Create a simple small JPEG
	img := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.Gray{128})
		}
	}
	return CreateTempJPEG(img)
}
