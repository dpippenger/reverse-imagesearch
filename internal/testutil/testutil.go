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

// CreateTempJPEGWithRealExif creates a temporary JPEG file with embedded EXIF
// metadata that the goexif library can parse. Contains: Make, Model, Orientation,
// DateTime, FNumber, ExposureTime, ISO, and FocalLength tags.
func CreateTempJPEGWithRealExif() (string, error) {
	// First, create a normal JPEG in memory
	img := image.NewGray(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.Gray{128})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90}); err != nil {
		return "", err
	}
	jpegData := jpegBuf.Bytes()

	// Build EXIF APP1 segment with TIFF/IFD structure (big-endian)
	exifData := buildExifData()

	// Construct final JPEG: SOI + APP1 + rest of JPEG (skip SOI from encoded JPEG)
	var final bytes.Buffer
	final.Write([]byte{0xFF, 0xD8})         // SOI
	final.Write(buildAPP1Segment(exifData)) // APP1 with EXIF
	final.Write(jpegData[2:])               // rest of JPEG after SOI

	tmpFile, err := os.CreateTemp("", "exif-test-*.jpg")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.Write(final.Bytes()); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}
	return tmpFile.Name(), nil
}

// buildAPP1Segment wraps TIFF data in a JPEG APP1 segment.
func buildAPP1Segment(tiffData []byte) []byte {
	exifHeader := []byte("Exif\x00\x00")
	length := 2 + len(exifHeader) + len(tiffData) // 2 for length field itself
	var seg bytes.Buffer
	seg.Write([]byte{0xFF, 0xE1})                      // APP1 marker
	seg.Write([]byte{byte(length >> 8), byte(length)}) // length
	seg.Write(exifHeader)
	seg.Write(tiffData)
	return seg.Bytes()
}

// buildExifData constructs minimal TIFF/IFD data with common EXIF tags.
func buildExifData() []byte {
	var b bytes.Buffer

	// TIFF header (offset 0)
	b.Write([]byte("MM"))                   // big-endian
	b.Write([]byte{0x00, 0x2A})             // magic 42
	b.Write([]byte{0x00, 0x00, 0x00, 0x08}) // offset to IFD0

	// IFD0 at offset 8
	// 5 entries: Make, Model, Orientation, DateTime, ExifIFD pointer
	writeU16(&b, 5) // entry count

	// Calculate data area offset: 8 (header) + 2 (count) + 5*12 (entries) + 4 (next IFD) = 74
	dataOffset := uint32(74)

	// Entry 1: Make (0x010F, ASCII)
	makeStr := "TestCam\x00"
	writeIFDEntry(&b, 0x010F, 2, uint32(len(makeStr)), dataOffset)
	makeOffset := dataOffset
	dataOffset += uint32(len(makeStr))

	// Entry 2: Model (0x0110, ASCII)
	modelStr := "T100\x00"
	writeIFDEntry(&b, 0x0110, 2, uint32(len(modelStr)), dataOffset)
	dataOffset += uint32(len(modelStr))

	// Entry 3: Orientation (0x0112, SHORT, value=1 inline)
	writeIFDEntryInline(&b, 0x0112, 3, 1, []byte{0x00, 0x01, 0x00, 0x00})

	// Entry 4: DateTime (0x0132, ASCII)
	dtStr := "2024:01:15 10:30:00\x00"
	writeIFDEntry(&b, 0x0132, 2, uint32(len(dtStr)), dataOffset)
	dataOffset += uint32(len(dtStr))

	// Entry 5: ExifIFD pointer (0x8769, LONG)
	exifIFDOffset := dataOffset // will write Exif sub-IFD here after data
	writeIFDEntryInline(&b, 0x8769, 4, 1, []byte{
		byte(exifIFDOffset >> 24), byte(exifIFDOffset >> 16),
		byte(exifIFDOffset >> 8), byte(exifIFDOffset),
	})

	// Next IFD offset (0 = no more IFDs)
	writeU32(&b, 0)

	// Data area (starting at offset 74)
	b.Write([]byte(makeStr))
	b.Write([]byte(modelStr))
	b.Write([]byte(dtStr))

	// Exif Sub-IFD at exifIFDOffset
	// 4 entries: ExposureTime, FNumber, ISOSpeedRatings, FocalLength
	writeU16(&b, 4)

	// Calculate sub-IFD data offset
	subDataOffset := exifIFDOffset + 2 + 4*12 + 4 // count + entries + next

	// ExposureTime (0x829A, RATIONAL) = 1/125
	writeIFDEntry(&b, 0x829A, 5, 1, subDataOffset)
	subDataOffset += 8

	// FNumber (0x829D, RATIONAL) = 28/10
	writeIFDEntry(&b, 0x829D, 5, 1, subDataOffset)
	subDataOffset += 8

	// ISOSpeedRatings (0x8827, SHORT, value=400 inline)
	writeIFDEntryInline(&b, 0x8827, 3, 1, []byte{0x01, 0x90, 0x00, 0x00})

	// FocalLength (0x920A, RATIONAL) = 50/1
	writeIFDEntry(&b, 0x920A, 5, 1, subDataOffset)

	// Next IFD offset
	writeU32(&b, 0)

	// Rational data
	writeU32(&b, 1)   // ExposureTime numerator
	writeU32(&b, 125) // ExposureTime denominator
	writeU32(&b, 28)  // FNumber numerator
	writeU32(&b, 10)  // FNumber denominator
	writeU32(&b, 50)  // FocalLength numerator
	writeU32(&b, 1)   // FocalLength denominator

	_ = makeOffset // suppress unused warning
	return b.Bytes()
}

func writeU16(b *bytes.Buffer, v uint16) {
	b.Write([]byte{byte(v >> 8), byte(v)})
}

func writeU32(b *bytes.Buffer, v uint32) {
	b.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func writeIFDEntry(b *bytes.Buffer, tag, typ uint16, count, offset uint32) {
	writeU16(b, tag)
	writeU16(b, typ)
	writeU32(b, count)
	writeU32(b, offset)
}

func writeIFDEntryInline(b *bytes.Buffer, tag, typ uint16, count uint32, value []byte) {
	writeU16(b, tag)
	writeU16(b, typ)
	writeU32(b, count)
	b.Write(value)
}
