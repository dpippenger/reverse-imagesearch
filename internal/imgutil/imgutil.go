package imgutil

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nfnt/resize"

	"imgsearch/internal/hash"
)

// Match represents a matching image with its similarity score
type Match struct {
	Path       string  `json:"path"`
	Similarity float64 `json:"similarity"`
	Hash       uint64  `json:"-"`
}

// LoadAndHash loads an image and computes all hashes
func LoadAndHash(path string) hash.Data {
	data := hash.Data{Path: path}

	file, err := os.Open(path)
	if err != nil {
		data.Error = err
		return data
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		data.Error = err
		return data
	}

	data.PHash = hash.Perceptual(img)
	data.AHash = hash.Average(img)
	data.DHash = hash.Difference(img)
	data.Histogram = hash.ComputeColorHistogram(img)

	return data
}

// LoadAndHashFromReader loads an image from a reader and computes all hashes
func LoadAndHashFromReader(r io.Reader) (hash.Data, error) {
	data := hash.Data{}

	img, _, err := image.Decode(r)
	if err != nil {
		return data, err
	}

	data.PHash = hash.Perceptual(img)
	data.AHash = hash.Average(img)
	data.DHash = hash.Difference(img)
	data.Histogram = hash.ComputeColorHistogram(img)

	return data, nil
}

// ComputeSimilarity calculates overall similarity between two images
func ComputeSimilarity(source, target hash.Data) float64 {
	// Weighted combination of different hash similarities
	pHashSim := hash.Similarity(source.PHash, target.PHash, 63) // 63 bits (64 - DC component)
	aHashSim := hash.Similarity(source.AHash, target.AHash, 64)
	dHashSim := hash.Similarity(source.DHash, target.DHash, 64)
	histSim := hash.HistogramSimilarity(source.Histogram, target.Histogram)

	// Weighted average - pHash and dHash are most reliable for similar images
	return 0.35*pHashSim + 0.25*dHashSim + 0.20*aHashSim + 0.20*histSim
}

// IsImageFile checks if a file is a supported image format
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return true
	}
	return false
}

// FindImages recursively finds all image files in a directory
func FindImages(root string) ([]string, error) {
	var images []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Log the error but continue walking
			fmt.Fprintf(os.Stderr, "Warning: cannot access %s: %v\n", path, err)
			return nil
		}
		if !d.IsDir() && IsImageFile(path) {
			images = append(images, path)
		}
		return nil
	})

	return images, err
}

// Grayscale converts an image to grayscale (for display purposes)
func Grayscale(img image.Image) *image.Gray {
	bounds := img.Bounds()
	gray := image.NewGray(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			lum := uint8(0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8))
			gray.SetGray(x, y, color.Gray{Y: lum})
		}
	}

	return gray
}

// GenerateThumbnail creates a base64 encoded JPEG thumbnail
func GenerateThumbnail(path string, maxSize uint) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return "", err
	}

	// Resize maintaining aspect ratio
	thumb := resize.Thumbnail(maxSize, maxSize, img, resize.Lanczos3)

	// Encode to JPEG
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 80})
	if err != nil {
		return "", err
	}

	// Return base64 encoded
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
