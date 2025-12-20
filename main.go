package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nfnt/resize"
	"github.com/rwcarlsen/goexif/exif"
)

// ImageMatch represents a matching image with its similarity score
type ImageMatch struct {
	Path       string  `json:"path"`
	Similarity float64 `json:"similarity"`
	Hash       uint64  `json:"-"`
}

// PerceptualHash computes a perceptual hash (pHash) for an image.
// This hash is resistant to scaling and minor modifications.
func PerceptualHash(img image.Image) uint64 {
	// Step 1: Reduce size to 32x32 for DCT, then we'll use 8x8 for the hash
	smallImg := resize.Resize(32, 32, img, resize.Lanczos3)

	// Step 2: Convert to grayscale
	gray := make([][]float64, 32)
	for y := 0; y < 32; y++ {
		gray[y] = make([]float64, 32)
		for x := 0; x < 32; x++ {
			r, g, b, _ := smallImg.At(x, y).RGBA()
			// Convert to grayscale using luminosity method
			gray[y][x] = 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
		}
	}

	// Step 3: Compute DCT (Discrete Cosine Transform)
	dct := computeDCT(gray)

	// Step 4: Reduce the DCT - keep top-left 8x8 (excluding first element which is DC component)
	// These represent the lowest frequencies
	var dctValues []float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if x == 0 && y == 0 {
				continue // Skip DC component
			}
			dctValues = append(dctValues, dct[y][x])
		}
	}

	// Step 5: Calculate median
	sortedDCT := make([]float64, len(dctValues))
	copy(sortedDCT, dctValues)
	sort.Float64s(sortedDCT)
	median := sortedDCT[len(sortedDCT)/2]

	// Step 6: Compute hash - set bit to 1 if value > median
	var hash uint64
	bitIndex := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if x == 0 && y == 0 {
				continue
			}
			if dct[y][x] > median {
				hash |= 1 << bitIndex
			}
			bitIndex++
		}
	}

	return hash
}

// computeDCT computes the Discrete Cosine Transform of a 2D array
func computeDCT(input [][]float64) [][]float64 {
	size := len(input)
	output := make([][]float64, size)
	for i := range output {
		output[i] = make([]float64, size)
	}

	// Precompute cosine values
	cosines := make([][]float64, size)
	for i := range cosines {
		cosines[i] = make([]float64, size)
		for j := range cosines[i] {
			cosines[i][j] = math.Cos(math.Pi * float64(i) * (float64(j) + 0.5) / float64(size))
		}
	}

	for u := 0; u < size; u++ {
		for v := 0; v < size; v++ {
			var sum float64
			for x := 0; x < size; x++ {
				for y := 0; y < size; y++ {
					sum += input[y][x] * cosines[u][x] * cosines[v][y]
				}
			}
			// Normalization factors
			cu := 1.0
			cv := 1.0
			if u == 0 {
				cu = 1.0 / math.Sqrt(2)
			}
			if v == 0 {
				cv = 1.0 / math.Sqrt(2)
			}
			output[u][v] = sum * cu * cv * 2.0 / float64(size)
		}
	}

	return output
}

// AverageHash computes an average hash (aHash) for an image.
// Simpler but still effective for finding similar images.
func AverageHash(img image.Image) uint64 {
	// Resize to 8x8
	smallImg := resize.Resize(8, 8, img, resize.Lanczos3)

	// Convert to grayscale and compute average
	var total float64
	pixels := make([]float64, 64)
	idx := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			r, g, b, _ := smallImg.At(x, y).RGBA()
			gray := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
			pixels[idx] = gray
			total += gray
			idx++
		}
	}

	avg := total / 64.0

	// Generate hash
	var hash uint64
	for i, pixel := range pixels {
		if pixel > avg {
			hash |= 1 << i
		}
	}

	return hash
}

// DifferenceHash computes a difference hash (dHash) for an image.
// Compares adjacent pixels, very resistant to scaling.
func DifferenceHash(img image.Image) uint64 {
	// Resize to 9x8 (need extra column for horizontal gradient)
	smallImg := resize.Resize(9, 8, img, resize.Lanczos3)

	// Convert to grayscale
	gray := make([][]float64, 8)
	for y := 0; y < 8; y++ {
		gray[y] = make([]float64, 9)
		for x := 0; x < 9; x++ {
			r, g, b, _ := smallImg.At(x, y).RGBA()
			gray[y][x] = 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
		}
	}

	// Generate hash based on horizontal gradient
	var hash uint64
	bitIndex := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if gray[y][x] < gray[y][x+1] {
				hash |= 1 << bitIndex
			}
			bitIndex++
		}
	}

	return hash
}

// HammingDistance calculates the number of differing bits between two hashes
func HammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	count := 0
	for xor != 0 {
		count++
		xor &= xor - 1
	}
	return count
}

// HashSimilarity converts hamming distance to a similarity percentage (0-100)
func HashSimilarity(hash1, hash2 uint64, hashBits int) float64 {
	distance := HammingDistance(hash1, hash2)
	return 100.0 * (1.0 - float64(distance)/float64(hashBits))
}

// ColorHistogram computes a simple color histogram for additional comparison
type ColorHistogram struct {
	R, G, B [16]float64 // 16 bins per channel
}

// ComputeColorHistogram creates a normalized color histogram
func ComputeColorHistogram(img image.Image) ColorHistogram {
	var hist ColorHistogram
	bounds := img.Bounds()
	pixelCount := 0

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0 {
				continue // Skip transparent pixels
			}
			// Map to 16 bins (0-15)
			hist.R[r>>12]++
			hist.G[g>>12]++
			hist.B[b>>12]++
			pixelCount++
		}
	}

	// Normalize
	if pixelCount > 0 {
		for i := 0; i < 16; i++ {
			hist.R[i] /= float64(pixelCount)
			hist.G[i] /= float64(pixelCount)
			hist.B[i] /= float64(pixelCount)
		}
	}

	return hist
}

// HistogramSimilarity computes similarity between two histograms using correlation
func HistogramSimilarity(h1, h2 ColorHistogram) float64 {
	var similarity float64
	for i := 0; i < 16; i++ {
		similarity += math.Min(h1.R[i], h2.R[i])
		similarity += math.Min(h1.G[i], h2.G[i])
		similarity += math.Min(h1.B[i], h2.B[i])
	}
	// Normalize to 0-100
	return similarity * 100.0 / 3.0
}

// ImageData holds precomputed data for an image
type ImageData struct {
	Path      string
	PHash     uint64
	AHash     uint64
	DHash     uint64
	Histogram ColorHistogram
	Error     error
}

// LoadAndHashImage loads an image and computes all hashes
func LoadAndHashImage(path string) ImageData {
	data := ImageData{Path: path}

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

	data.PHash = PerceptualHash(img)
	data.AHash = AverageHash(img)
	data.DHash = DifferenceHash(img)
	data.Histogram = ComputeColorHistogram(img)

	return data
}

// LoadAndHashImageFromReader loads an image from a reader and computes all hashes
func LoadAndHashImageFromReader(r io.Reader) (ImageData, error) {
	data := ImageData{}

	img, _, err := image.Decode(r)
	if err != nil {
		return data, err
	}

	data.PHash = PerceptualHash(img)
	data.AHash = AverageHash(img)
	data.DHash = DifferenceHash(img)
	data.Histogram = ComputeColorHistogram(img)

	return data, nil
}

// ComputeSimilarity calculates overall similarity between two images
func ComputeSimilarity(source, target ImageData) float64 {
	// Weighted combination of different hash similarities
	pHashSim := HashSimilarity(source.PHash, target.PHash, 63) // 63 bits (64 - DC component)
	aHashSim := HashSimilarity(source.AHash, target.AHash, 64)
	dHashSim := HashSimilarity(source.DHash, target.DHash, 64)
	histSim := HistogramSimilarity(source.Histogram, target.Histogram)

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

// GrayscaleImage converts an image to grayscale (for display purposes)
func GrayscaleImage(img image.Image) *image.Gray {
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

// ExifData holds extracted EXIF metadata
type ExifData struct {
	Make         string `json:"make,omitempty"`
	Model        string `json:"model,omitempty"`
	DateTime     string `json:"dateTime,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	FileSize     int64  `json:"fileSize,omitempty"`
	Orientation  string `json:"orientation,omitempty"`
	FNumber      string `json:"fNumber,omitempty"`
	ExposureTime string `json:"exposureTime,omitempty"`
	ISO          string `json:"iso,omitempty"`
	FocalLength  string `json:"focalLength,omitempty"`
	LensModel    string `json:"lensModel,omitempty"`
	Software     string `json:"software,omitempty"`
	GPSLatitude  string `json:"gpsLatitude,omitempty"`
	GPSLongitude string `json:"gpsLongitude,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ExtractExifData reads EXIF metadata from an image file
func ExtractExifData(path string) ExifData {
	data := ExifData{}

	file, err := os.Open(path)
	if err != nil {
		data.Error = "Cannot open file"
		return data
	}
	defer file.Close()

	// Get file size
	if fileInfo, err := file.Stat(); err == nil {
		data.FileSize = fileInfo.Size()
	}

	// Get image dimensions
	img, _, err := image.DecodeConfig(file)
	if err == nil {
		data.Width = img.Width
		data.Height = img.Height
	}

	// Reset file position for EXIF reading
	file.Seek(0, 0)

	x, err := exif.Decode(file)
	if err != nil {
		data.Error = "No EXIF data"
		return data
	}

	// Helper to safely get string tag
	getString := func(tag exif.FieldName) string {
		t, err := x.Get(tag)
		if err != nil {
			return ""
		}
		s, err := t.StringVal()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}

	data.Make = getString(exif.Make)
	data.Model = getString(exif.Model)
	data.Software = getString(exif.Software)

	// DateTime
	if dt, err := x.DateTime(); err == nil {
		data.DateTime = dt.Format("2006-01-02 15:04:05")
	}

	// Orientation
	if orient, err := x.Get(exif.Orientation); err == nil {
		if v, err := orient.Int(0); err == nil {
			orientations := map[int]string{
				1: "Normal",
				2: "Flipped horizontal",
				3: "Rotated 180°",
				4: "Flipped vertical",
				5: "Rotated 90° CCW, flipped",
				6: "Rotated 90° CW",
				7: "Rotated 90° CW, flipped",
				8: "Rotated 90° CCW",
			}
			if name, ok := orientations[v]; ok {
				data.Orientation = name
			}
		}
	}

	// FNumber (aperture)
	if fn, err := x.Get(exif.FNumber); err == nil {
		if num, denom, err := fn.Rat2(0); err == nil && denom != 0 {
			data.FNumber = fmt.Sprintf("f/%.1f", float64(num)/float64(denom))
		}
	}

	// Exposure time
	if et, err := x.Get(exif.ExposureTime); err == nil {
		if num, denom, err := et.Rat2(0); err == nil && denom != 0 {
			if num < denom {
				data.ExposureTime = fmt.Sprintf("1/%d s", denom/num)
			} else {
				data.ExposureTime = fmt.Sprintf("%.1f s", float64(num)/float64(denom))
			}
		}
	}

	// ISO
	if iso, err := x.Get(exif.ISOSpeedRatings); err == nil {
		if v, err := iso.Int(0); err == nil {
			data.ISO = fmt.Sprintf("ISO %d", v)
		}
	}

	// Focal length
	if fl, err := x.Get(exif.FocalLength); err == nil {
		if num, denom, err := fl.Rat2(0); err == nil && denom != 0 {
			data.FocalLength = fmt.Sprintf("%.0f mm", float64(num)/float64(denom))
		}
	}

	// Lens model
	if lens, err := x.Get(exif.LensModel); err == nil {
		if s, err := lens.StringVal(); err == nil {
			data.LensModel = strings.TrimSpace(s)
		}
	}

	// GPS coordinates
	if lat, lon, err := x.LatLong(); err == nil {
		data.GPSLatitude = fmt.Sprintf("%.6f", lat)
		data.GPSLongitude = fmt.Sprintf("%.6f", lon)
	}

	return data
}

// SearchConfig holds search parameters
type SearchConfig struct {
	SearchDir  string
	Threshold  float64
	Workers    int
	TopN       int
	Verbose    bool
	OutputFile string
}

// SearchResult is sent for each match found
type SearchResult struct {
	Match     ImageMatch `json:"match"`
	Thumbnail string     `json:"thumbnail,omitempty"`
	Total     int        `json:"total"`
	Scanned   int        `json:"scanned"`
	Done      bool       `json:"done"`
	Error     string     `json:"error,omitempty"`
}

// RunSearch performs the image search and calls the callback for each result
func RunSearch(sourceData ImageData, config SearchConfig, callback func(SearchResult)) {
	// Find all images in directory
	images, err := FindImages(config.SearchDir)
	if err != nil {
		callback(SearchResult{Error: fmt.Sprintf("Error scanning directory: %v", err), Done: true})
		return
	}

	totalImages := len(images)
	if totalImages == 0 {
		callback(SearchResult{Done: true, Total: 0, Scanned: 0})
		return
	}

	numWorkers := config.Workers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	var wg sync.WaitGroup
	imageChan := make(chan string, len(images))
	var resultMutex sync.Mutex
	scanned := 0
	resultCount := 0

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range imageChan {
				data := LoadAndHashImage(path)

				resultMutex.Lock()
				scanned++
				currentScanned := scanned
				resultMutex.Unlock()

				if data.Error != nil {
					continue
				}

				similarity := ComputeSimilarity(sourceData, data)
				if similarity >= config.Threshold {
					resultMutex.Lock()
					resultCount++
					currentCount := resultCount
					resultMutex.Unlock()

					// Check if we should limit results
					if config.TopN > 0 && currentCount > config.TopN {
						continue
					}

					match := ImageMatch{
						Path:       path,
						Similarity: similarity,
						Hash:       data.PHash,
					}

					// Generate thumbnail
					thumb, _ := GenerateThumbnail(path, 200)

					callback(SearchResult{
						Match:     match,
						Thumbnail: thumb,
						Total:     totalImages,
						Scanned:   currentScanned,
					})
				}
			}
		}()
	}

	// Send work
	for _, img := range images {
		imageChan <- img
	}
	close(imageChan)

	// Wait for completion
	wg.Wait()

	callback(SearchResult{Done: true, Total: totalImages, Scanned: totalImages})
}

// ============== Web Server ==============

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Image Search</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            background: linear-gradient(135deg, #1a1a2e 0%, #16213e 50%, #0f3460 100%);
            min-height: 100vh;
            color: #e4e4e4;
        }

        .container {
            max-width: 1400px;
            margin: 0 auto;
            padding: 20px;
        }

        header {
            text-align: center;
            padding: 40px 0;
        }

        h1 {
            font-size: 2.5rem;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            margin-bottom: 10px;
        }

        .subtitle {
            color: #888;
            font-size: 1.1rem;
        }

        .config-panel {
            background: rgba(255, 255, 255, 0.05);
            backdrop-filter: blur(10px);
            border-radius: 16px;
            padding: 30px;
            margin-bottom: 30px;
            border: 1px solid rgba(255, 255, 255, 0.1);
        }

        .config-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 20px;
            margin-bottom: 25px;
        }

        .config-item {
            display: flex;
            flex-direction: column;
            gap: 8px;
        }

        .config-item label {
            font-size: 0.9rem;
            color: #aaa;
            font-weight: 500;
        }

        .config-item input[type="text"],
        .config-item input[type="number"] {
            padding: 12px 16px;
            border: 1px solid rgba(255, 255, 255, 0.15);
            border-radius: 8px;
            background: rgba(0, 0, 0, 0.3);
            color: #fff;
            font-size: 1rem;
            transition: all 0.3s ease;
        }

        .config-item input:focus {
            outline: none;
            border-color: #00d9ff;
            box-shadow: 0 0 0 3px rgba(0, 217, 255, 0.1);
        }

        .input-with-button {
            display: flex;
            gap: 8px;
        }

        .input-with-button input {
            flex: 1;
        }

        .btn-browse {
            padding: 12px 16px;
            border: 1px solid rgba(255, 255, 255, 0.15);
            border-radius: 8px;
            background: rgba(0, 217, 255, 0.2);
            color: #00d9ff;
            font-size: 1rem;
            cursor: pointer;
            transition: all 0.3s ease;
            white-space: nowrap;
        }

        .btn-browse:hover {
            background: rgba(0, 217, 255, 0.3);
            border-color: #00d9ff;
        }

        /* Modal styles */
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0, 0, 0, 0.7);
            backdrop-filter: blur(5px);
            z-index: 1000;
            align-items: center;
            justify-content: center;
        }

        .modal-overlay.active {
            display: flex;
        }

        .modal {
            background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
            border: 1px solid rgba(255, 255, 255, 0.1);
            border-radius: 16px;
            width: 90%;
            max-width: 600px;
            max-height: 80vh;
            display: flex;
            flex-direction: column;
            animation: modalSlideIn 0.3s ease;
        }

        @keyframes modalSlideIn {
            from {
                opacity: 0;
                transform: translateY(-20px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }

        .modal-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 20px;
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
        }

        .modal-header h3 {
            color: #fff;
            font-size: 1.2rem;
        }

        .modal-close {
            background: none;
            border: none;
            color: #888;
            font-size: 1.5rem;
            cursor: pointer;
            padding: 5px;
            line-height: 1;
        }

        .modal-close:hover {
            color: #fff;
        }

        .modal-path-bar {
            display: flex;
            align-items: center;
            gap: 8px;
            padding: 15px 20px;
            background: rgba(0, 0, 0, 0.2);
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
        }

        .modal-path-bar input {
            flex: 1;
            padding: 8px 12px;
            border: 1px solid rgba(255, 255, 255, 0.15);
            border-radius: 6px;
            background: rgba(0, 0, 0, 0.3);
            color: #fff;
            font-size: 0.9rem;
        }

        .modal-path-bar input:focus {
            outline: none;
            border-color: #00d9ff;
        }

        .btn-go {
            padding: 8px 16px;
            border: none;
            border-radius: 6px;
            background: rgba(0, 217, 255, 0.2);
            color: #00d9ff;
            cursor: pointer;
            font-size: 0.9rem;
        }

        .btn-go:hover {
            background: rgba(0, 217, 255, 0.3);
        }

        .modal-body {
            flex: 1;
            overflow-y: auto;
            padding: 10px;
            min-height: 300px;
        }

        .browser-list {
            list-style: none;
        }

        .browser-item {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 12px 15px;
            border-radius: 8px;
            cursor: pointer;
            transition: background 0.2s ease;
        }

        .browser-item:hover {
            background: rgba(255, 255, 255, 0.05);
        }

        .browser-item.selected {
            background: rgba(0, 217, 255, 0.15);
            border: 1px solid rgba(0, 217, 255, 0.3);
        }

        .browser-item-icon {
            font-size: 1.3rem;
        }

        .browser-item-name {
            flex: 1;
            color: #e4e4e4;
            font-size: 0.95rem;
        }

        .browser-item.parent-dir {
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
            margin-bottom: 5px;
        }

        .browser-item.parent-dir .browser-item-name {
            color: #888;
        }

        .modal-footer {
            display: flex;
            justify-content: flex-end;
            gap: 10px;
            padding: 15px 20px;
            border-top: 1px solid rgba(255, 255, 255, 0.1);
        }

        .browser-loading {
            text-align: center;
            padding: 40px;
            color: #888;
        }

        .browser-error {
            text-align: center;
            padding: 40px;
            color: #ff6b6b;
        }

        .upload-area {
            border: 2px dashed rgba(255, 255, 255, 0.2);
            border-radius: 12px;
            padding: 40px;
            text-align: center;
            cursor: pointer;
            transition: all 0.3s ease;
            background: rgba(0, 0, 0, 0.2);
        }

        .upload-area:hover {
            border-color: #00d9ff;
            background: rgba(0, 217, 255, 0.05);
        }

        .upload-area.dragover {
            border-color: #00ff88;
            background: rgba(0, 255, 136, 0.1);
        }

        .upload-icon {
            font-size: 3rem;
            margin-bottom: 15px;
        }

        .upload-text {
            font-size: 1.1rem;
            color: #aaa;
        }

        .upload-text span {
            color: #00d9ff;
            text-decoration: underline;
        }

        #fileInput {
            display: none;
        }

        .preview-container {
            display: none;
            align-items: center;
            gap: 20px;
            padding: 20px;
            background: rgba(0, 0, 0, 0.2);
            border-radius: 12px;
            margin-top: 20px;
        }

        .preview-container.active {
            display: flex;
        }

        #previewImage {
            max-width: 150px;
            max-height: 150px;
            border-radius: 8px;
            object-fit: cover;
        }

        .preview-info {
            flex: 1;
        }

        .preview-info h3 {
            color: #fff;
            margin-bottom: 5px;
        }

        .preview-info p {
            color: #888;
            font-size: 0.9rem;
        }

        .btn {
            padding: 14px 32px;
            border: none;
            border-radius: 8px;
            font-size: 1rem;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.3s ease;
            text-transform: uppercase;
            letter-spacing: 1px;
        }

        .btn-primary {
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            color: #1a1a2e;
        }

        .btn-primary:hover {
            transform: translateY(-2px);
            box-shadow: 0 10px 30px rgba(0, 217, 255, 0.3);
        }

        .btn-primary:disabled {
            opacity: 0.5;
            cursor: not-allowed;
            transform: none;
        }

        .btn-secondary {
            background: rgba(255, 255, 255, 0.1);
            color: #fff;
            border: 1px solid rgba(255, 255, 255, 0.2);
        }

        .btn-secondary:hover {
            background: rgba(255, 255, 255, 0.15);
        }

        .button-row {
            display: flex;
            gap: 15px;
            justify-content: center;
            margin-top: 25px;
        }

        .progress-section {
            display: none;
            margin-bottom: 30px;
        }

        .progress-section.active {
            display: block;
        }

        .progress-bar-container {
            background: rgba(0, 0, 0, 0.3);
            border-radius: 10px;
            height: 20px;
            overflow: hidden;
            margin-bottom: 10px;
        }

        .progress-bar {
            height: 100%;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            width: 0%;
            transition: width 0.3s ease;
        }

        .progress-text {
            text-align: center;
            color: #888;
            font-size: 0.9rem;
        }

        .results-section {
            display: none;
        }

        .results-section.active {
            display: block;
        }

        .results-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }

        .results-header h2 {
            font-size: 1.5rem;
            color: #fff;
        }

        .results-count {
            color: #00d9ff;
            font-weight: 600;
        }

        .results-grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
            gap: 20px;
        }

        .result-card {
            background: rgba(255, 255, 255, 0.05);
            border-radius: 12px;
            overflow: visible;
            transition: all 0.3s ease;
            border: 1px solid rgba(255, 255, 255, 0.1);
            animation: fadeIn 0.5s ease;
        }

        @keyframes fadeIn {
            from {
                opacity: 0;
                transform: translateY(20px);
            }
            to {
                opacity: 1;
                transform: translateY(0);
            }
        }

        .result-card:hover {
            transform: translateY(-5px);
            box-shadow: 0 10px 40px rgba(0, 0, 0, 0.3);
            border-color: rgba(0, 217, 255, 0.3);
        }

        .result-image {
            width: 100%;
            height: 180px;
            object-fit: cover;
            background: rgba(0, 0, 0, 0.3);
            border-radius: 12px 12px 0 0;
        }

        .result-info {
            padding: 15px;
        }

        .result-similarity {
            display: inline-block;
            padding: 4px 10px;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            color: #1a1a2e;
            border-radius: 20px;
            font-size: 0.85rem;
            font-weight: 600;
            margin-bottom: 8px;
        }

        .result-header {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 8px;
        }

        .info-btn, .download-btn {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            width: 20px;
            height: 20px;
            border-radius: 50%;
            background: rgba(255, 255, 255, 0.15);
            color: #aaa;
            font-size: 0.75rem;
            font-weight: 700;
            transition: all 0.2s ease;
            border: 1px solid rgba(255, 255, 255, 0.2);
        }

        .info-btn {
            font-style: italic;
            font-family: Georgia, serif;
            cursor: help;
        }

        .download-btn {
            cursor: pointer;
            text-decoration: none;
            font-size: 0.7rem;
        }

        .info-btn:hover {
            background: rgba(0, 217, 255, 0.3);
            color: #fff;
            border-color: #00d9ff;
        }

        .download-btn:hover {
            background: rgba(0, 255, 136, 0.3);
            color: #fff;
            border-color: #00ff88;
        }

        .exif-tooltip {
            display: none;
            position: absolute;
            bottom: 100%;
            right: 0;
            background: rgba(20, 20, 40, 0.95);
            border: 1px solid rgba(0, 217, 255, 0.3);
            border-radius: 8px;
            padding: 12px;
            min-width: 220px;
            max-width: 280px;
            z-index: 100;
            box-shadow: 0 4px 20px rgba(0, 0, 0, 0.4);
            margin-bottom: 8px;
        }

        .exif-tooltip::after {
            content: '';
            position: absolute;
            top: 100%;
            right: 8px;
            border: 8px solid transparent;
            border-top-color: rgba(0, 217, 255, 0.3);
        }

        .info-wrapper {
            position: relative;
            display: inline-block;
        }

        .info-wrapper:hover .exif-tooltip {
            display: block;
        }

        .exif-tooltip h4 {
            color: #00d9ff;
            font-size: 0.8rem;
            margin-bottom: 8px;
            padding-bottom: 6px;
            border-bottom: 1px solid rgba(255, 255, 255, 0.1);
        }

        .exif-row {
            display: flex;
            justify-content: space-between;
            font-size: 0.75rem;
            padding: 3px 0;
        }

        .exif-label {
            color: #888;
        }

        .exif-value {
            color: #e4e4e4;
            text-align: right;
            max-width: 150px;
            overflow: hidden;
            text-overflow: ellipsis;
            white-space: nowrap;
        }

        .exif-loading {
            color: #888;
            font-size: 0.75rem;
            text-align: center;
            padding: 10px;
        }

        .exif-error {
            color: #ff6b6b;
            font-size: 0.75rem;
            text-align: center;
            padding: 10px;
        }

        .result-path {
            color: #888;
            font-size: 0.8rem;
            word-break: break-all;
            line-height: 1.4;
        }

        .no-results {
            text-align: center;
            padding: 60px 20px;
            color: #888;
        }

        .no-results-icon {
            font-size: 4rem;
            margin-bottom: 20px;
        }

        .slider-container {
            display: flex;
            align-items: center;
            gap: 15px;
        }

        .slider-container input[type="range"] {
            flex: 1;
            -webkit-appearance: none;
            height: 8px;
            border-radius: 4px;
            background: rgba(255, 255, 255, 0.1);
            outline: none;
        }

        .slider-container input[type="range"]::-webkit-slider-thumb {
            -webkit-appearance: none;
            width: 20px;
            height: 20px;
            border-radius: 50%;
            background: linear-gradient(90deg, #00d9ff, #00ff88);
            cursor: pointer;
        }

        .slider-value {
            min-width: 50px;
            text-align: right;
            color: #00d9ff;
            font-weight: 600;
        }

        .status-message {
            text-align: center;
            padding: 20px;
            color: #888;
            font-style: italic;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Image Search</h1>
            <p class="subtitle">Find similar images using perceptual hashing</p>
        </header>

        <div class="config-panel">
            <div class="config-grid">
                <div class="config-item">
                    <label for="searchDir">Search Directory</label>
                    <div class="input-with-button">
                        <input type="text" id="searchDir" value="." placeholder="/path/to/search">
                        <button class="btn-browse" id="browseBtn">Browse</button>
                    </div>
                </div>
                <div class="config-item">
                    <label for="threshold">Similarity Threshold</label>
                    <div class="slider-container">
                        <input type="range" id="threshold" min="0" max="100" value="70">
                        <span class="slider-value" id="thresholdValue">70%</span>
                    </div>
                </div>
                <div class="config-item">
                    <label for="workers">Worker Threads (0 = auto)</label>
                    <input type="number" id="workers" value="0" min="0" max="64">
                </div>
                <div class="config-item">
                    <label for="topN">Max Results (0 = unlimited)</label>
                    <input type="number" id="topN" value="0" min="0">
                </div>
            </div>

            <div class="upload-area" id="uploadArea">
                <div class="upload-icon">📁</div>
                <p class="upload-text">Drag & drop an image here or <span>browse</span></p>
                <input type="file" id="fileInput" accept="image/*">
            </div>

            <div class="preview-container" id="previewContainer">
                <img id="previewImage" src="" alt="Preview">
                <div class="preview-info">
                    <h3 id="fileName">filename.jpg</h3>
                    <p id="fileSize">0 KB</p>
                </div>
                <button class="btn btn-secondary" id="clearBtn">Clear</button>
            </div>

            <div class="button-row">
                <button class="btn btn-primary" id="searchBtn" disabled>Start Search</button>
                <button class="btn btn-secondary" id="stopBtn" style="display: none;">Stop</button>
            </div>
        </div>

        <div class="progress-section" id="progressSection">
            <div class="progress-bar-container">
                <div class="progress-bar" id="progressBar"></div>
            </div>
            <p class="progress-text" id="progressText">Scanning images...</p>
        </div>

        <div class="results-section" id="resultsSection">
            <div class="results-header">
                <h2>Results</h2>
                <span class="results-count" id="resultsCount">0 matches found</span>
            </div>
            <div class="results-grid" id="resultsGrid"></div>
            <div class="no-results" id="noResults" style="display: none;">
                <div class="no-results-icon">🔍</div>
                <p>No similar images found above the threshold</p>
            </div>
        </div>
    </div>

    <!-- Directory Browser Modal -->
    <div class="modal-overlay" id="browserModal">
        <div class="modal">
            <div class="modal-header">
                <h3>Select Directory</h3>
                <button class="modal-close" id="modalClose">&times;</button>
            </div>
            <div class="modal-path-bar">
                <input type="text" id="modalPathInput" placeholder="/path/to/directory">
                <button class="btn-go" id="modalGoBtn">Go</button>
            </div>
            <div class="modal-body" id="browserBody">
                <div class="browser-loading">Loading...</div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-secondary" id="modalCancelBtn">Cancel</button>
                <button class="btn btn-primary" id="modalSelectBtn">Select</button>
            </div>
        </div>
    </div>

    <script>
        const uploadArea = document.getElementById('uploadArea');
        const fileInput = document.getElementById('fileInput');
        const previewContainer = document.getElementById('previewContainer');
        const previewImage = document.getElementById('previewImage');
        const fileName = document.getElementById('fileName');
        const fileSize = document.getElementById('fileSize');
        const clearBtn = document.getElementById('clearBtn');
        const searchBtn = document.getElementById('searchBtn');
        const stopBtn = document.getElementById('stopBtn');
        const progressSection = document.getElementById('progressSection');
        const progressBar = document.getElementById('progressBar');
        const progressText = document.getElementById('progressText');
        const resultsSection = document.getElementById('resultsSection');
        const resultsGrid = document.getElementById('resultsGrid');
        const resultsCount = document.getElementById('resultsCount');
        const noResults = document.getElementById('noResults');
        const threshold = document.getElementById('threshold');
        const thresholdValue = document.getElementById('thresholdValue');

        let selectedFile = null;
        let eventSource = null;

        // Threshold slider
        threshold.addEventListener('input', () => {
            thresholdValue.textContent = threshold.value + '%';
        });

        // File upload handling
        uploadArea.addEventListener('click', () => fileInput.click());

        uploadArea.addEventListener('dragover', (e) => {
            e.preventDefault();
            uploadArea.classList.add('dragover');
        });

        uploadArea.addEventListener('dragleave', () => {
            uploadArea.classList.remove('dragover');
        });

        uploadArea.addEventListener('drop', (e) => {
            e.preventDefault();
            uploadArea.classList.remove('dragover');
            const files = e.dataTransfer.files;
            if (files.length > 0) {
                handleFile(files[0]);
            }
        });

        fileInput.addEventListener('change', () => {
            if (fileInput.files.length > 0) {
                handleFile(fileInput.files[0]);
            }
        });

        function handleFile(file) {
            if (!file.type.startsWith('image/')) {
                alert('Please select an image file');
                return;
            }

            selectedFile = file;

            const reader = new FileReader();
            reader.onload = (e) => {
                previewImage.src = e.target.result;
                fileName.textContent = file.name;
                fileSize.textContent = formatBytes(file.size);
                previewContainer.classList.add('active');
                uploadArea.style.display = 'none';
                searchBtn.disabled = false;
            };
            reader.readAsDataURL(file);
        }

        function formatBytes(bytes) {
            if (bytes === 0) return '0 Bytes';
            const k = 1024;
            const sizes = ['Bytes', 'KB', 'MB', 'GB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
        }

        clearBtn.addEventListener('click', () => {
            selectedFile = null;
            fileInput.value = '';
            previewContainer.classList.remove('active');
            uploadArea.style.display = 'block';
            searchBtn.disabled = true;
        });

        searchBtn.addEventListener('click', startSearch);
        stopBtn.addEventListener('click', stopSearch);

        function startSearch() {
            if (!selectedFile) return;

            // Prepare form data
            const formData = new FormData();
            formData.append('image', selectedFile);
            formData.append('dir', document.getElementById('searchDir').value);
            formData.append('threshold', threshold.value);
            formData.append('workers', document.getElementById('workers').value);
            formData.append('topN', document.getElementById('topN').value);

            // Reset UI
            resultsGrid.innerHTML = '';
            noResults.style.display = 'none';
            progressSection.classList.add('active');
            resultsSection.classList.add('active');
            searchBtn.style.display = 'none';
            stopBtn.style.display = 'inline-block';
            progressBar.style.width = '0%';
            progressText.textContent = 'Starting search...';
            resultsCount.textContent = '0 matches found';

            let matchCount = 0;
            let searchStartTime = null;
            let lastScanned = 0;

            // Upload image and start search
            fetch('/api/search', {
                method: 'POST',
                body: formData
            })
            .then(response => response.json())
            .then(data => {
                if (data.error) {
                    progressText.textContent = 'Error: ' + data.error;
                    return;
                }

                // Connect to SSE stream
                eventSource = new EventSource('/api/results/' + data.searchId);

                eventSource.onmessage = (event) => {
                    const result = JSON.parse(event.data);

                    if (result.error) {
                        progressText.textContent = 'Error: ' + result.error;
                        eventSource.close();
                        return;
                    }

                    // Update progress
                    if (result.total > 0) {
                        // Initialize start time on first progress update
                        if (searchStartTime === null && result.scanned > 0) {
                            searchStartTime = Date.now();
                            lastScanned = result.scanned;
                        }

                        const percent = Math.round((result.scanned / result.total) * 100);
                        progressBar.style.width = percent + '%';

                        // Calculate ETA
                        let etaText = '';
                        if (searchStartTime !== null && result.scanned > lastScanned) {
                            const elapsedMs = Date.now() - searchStartTime;
                            const scannedSinceStart = result.scanned - lastScanned;
                            const imagesPerMs = scannedSinceStart / elapsedMs;
                            const remaining = result.total - result.scanned;

                            if (imagesPerMs > 0 && remaining > 0) {
                                const etaMs = remaining / imagesPerMs;
                                etaText = ' - ETA: ' + formatEta(etaMs);
                            }
                        }

                        progressText.textContent = 'Scanned ' + result.scanned + ' of ' + result.total + ' images' + etaText;
                    }

                    // Add result card if there's a match
                    if (result.match && result.match.path) {
                        matchCount++;
                        resultsCount.textContent = matchCount + ' match' + (matchCount !== 1 ? 'es' : '') + ' found';
                        addResultCard(result);
                    }

                    // Check if done
                    if (result.done) {
                        eventSource.close();
                        searchComplete(matchCount);
                    }
                };

                eventSource.onerror = () => {
                    eventSource.close();
                    searchComplete(matchCount);
                };
            })
            .catch(err => {
                progressText.textContent = 'Error: ' + err.message;
                searchComplete(0);
            });
        }

        function stopSearch() {
            if (eventSource) {
                eventSource.close();
            }
            searchComplete(parseInt(resultsCount.textContent) || 0);
        }

        function searchComplete(matchCount) {
            searchBtn.style.display = 'inline-block';
            stopBtn.style.display = 'none';
            progressText.textContent = 'Search complete';
            progressBar.style.width = '100%';

            if (matchCount === 0) {
                noResults.style.display = 'block';
            }
        }

        function addResultCard(result) {
            const card = document.createElement('div');
            card.className = 'result-card';

            const imgSrc = result.thumbnail
                ? 'data:image/jpeg;base64,' + result.thumbnail
                : '/api/thumbnail?path=' + encodeURIComponent(result.match.path);

            const infoId = 'exif-' + Date.now() + '-' + Math.random().toString(36).substr(2, 9);

            const downloadUrl = '/api/download?path=' + encodeURIComponent(result.match.path);

            card.innerHTML =
                '<img class="result-image" src="' + imgSrc + '" alt="Match" loading="lazy">' +
                '<div class="result-info">' +
                    '<div class="result-header">' +
                        '<span class="result-similarity">' + result.match.similarity.toFixed(1) + '%</span>' +
                        '<div class="info-wrapper">' +
                            '<span class="info-btn" data-path="' + escapeHtml(result.match.path) + '" data-tooltip-id="' + infoId + '">i</span>' +
                            '<div class="exif-tooltip" id="' + infoId + '">' +
                                '<div class="exif-loading">Loading...</div>' +
                            '</div>' +
                        '</div>' +
                        '<a class="download-btn" href="' + downloadUrl + '" title="Download original">\u2193</a>' +
                    '</div>' +
                    '<p class="result-path">' + escapeHtml(result.match.path) + '</p>' +
                '</div>';

            // Add hover event for EXIF loading
            const infoBtn = card.querySelector('.info-btn');
            let exifLoaded = false;

            infoBtn.addEventListener('mouseenter', function() {
                if (exifLoaded) return;
                exifLoaded = true;

                const path = this.dataset.path;
                const tooltipId = this.dataset.tooltipId;
                const tooltip = document.getElementById(tooltipId);

                fetch('/api/exif?path=' + encodeURIComponent(path))
                    .then(response => response.json())
                    .then(data => {
                        tooltip.innerHTML = renderExifTooltip(data);
                    })
                    .catch(err => {
                        tooltip.innerHTML = '<div class="exif-error">Failed to load EXIF data</div>';
                    });
            });

            resultsGrid.appendChild(card);
        }

        function renderExifTooltip(data) {
            if (data.error && !data.width && !data.fileSize) {
                return '<div class="exif-error">' + escapeHtml(data.error) + '</div>';
            }

            let html = '<h4>Image Info</h4>';
            const fields = [
                ['File Size', data.fileSize ? formatBytes(data.fileSize) : null],
                ['Dimensions', data.width && data.height ? data.width + ' x ' + data.height : null],
                ['Camera', data.make && data.model ? data.make + ' ' + data.model : (data.model || data.make)],
                ['Date', data.dateTime],
                ['Aperture', data.fNumber],
                ['Shutter', data.exposureTime],
                ['ISO', data.iso],
                ['Focal Length', data.focalLength],
                ['Lens', data.lensModel],
                ['Orientation', data.orientation],
                ['Software', data.software],
                ['GPS', data.gpsLatitude && data.gpsLongitude ? data.gpsLatitude + ', ' + data.gpsLongitude : null]
            ];

            let hasData = false;
            for (const [label, value] of fields) {
                if (value) {
                    html += '<div class="exif-row"><span class="exif-label">' + label + '</span><span class="exif-value">' + escapeHtml(value) + '</span></div>';
                    hasData = true;
                }
            }

            if (!hasData) {
                html += '<div class="exif-error">No metadata available</div>';
            }

            return html;
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        function formatEta(ms) {
            const seconds = Math.ceil(ms / 1000);
            if (seconds < 60) {
                return seconds + 's';
            }
            const minutes = Math.floor(seconds / 60);
            const remainingSeconds = seconds % 60;
            if (minutes < 60) {
                return minutes + 'm ' + remainingSeconds + 's';
            }
            const hours = Math.floor(minutes / 60);
            const remainingMinutes = minutes % 60;
            return hours + 'h ' + remainingMinutes + 'm';
        }

        // Directory Browser functionality
        const browseBtn = document.getElementById('browseBtn');
        const browserModal = document.getElementById('browserModal');
        const modalClose = document.getElementById('modalClose');
        const modalCancelBtn = document.getElementById('modalCancelBtn');
        const modalSelectBtn = document.getElementById('modalSelectBtn');
        const modalPathInput = document.getElementById('modalPathInput');
        const modalGoBtn = document.getElementById('modalGoBtn');
        const browserBody = document.getElementById('browserBody');
        const searchDirInput = document.getElementById('searchDir');

        let currentBrowsePath = '';
        let selectedPath = '';

        browseBtn.addEventListener('click', () => {
            openBrowser(searchDirInput.value || '');
        });

        modalClose.addEventListener('click', closeBrowser);
        modalCancelBtn.addEventListener('click', closeBrowser);

        browserModal.addEventListener('click', (e) => {
            if (e.target === browserModal) {
                closeBrowser();
            }
        });

        modalGoBtn.addEventListener('click', () => {
            const path = modalPathInput.value.trim();
            if (path) {
                loadDirectory(path);
            }
        });

        modalPathInput.addEventListener('keypress', (e) => {
            if (e.key === 'Enter') {
                const path = modalPathInput.value.trim();
                if (path) {
                    loadDirectory(path);
                }
            }
        });

        modalSelectBtn.addEventListener('click', () => {
            if (selectedPath) {
                searchDirInput.value = selectedPath;
                closeBrowser();
            }
        });

        function openBrowser(startPath) {
            browserModal.classList.add('active');
            selectedPath = '';
            loadDirectory(startPath);
        }

        function closeBrowser() {
            browserModal.classList.remove('active');
        }

        function loadDirectory(path) {
            browserBody.innerHTML = '<div class="browser-loading">Loading...</div>';

            const url = '/api/browse' + (path ? '?path=' + encodeURIComponent(path) : '');

            fetch(url)
                .then(response => response.json())
                .then(data => {
                    if (data.error) {
                        browserBody.innerHTML = '<div class="browser-error">' + escapeHtml(data.error) + '</div>';
                        return;
                    }

                    currentBrowsePath = data.path;
                    selectedPath = data.path;
                    modalPathInput.value = data.path;

                    renderDirectory(data);
                })
                .catch(err => {
                    browserBody.innerHTML = '<div class="browser-error">Failed to load directory</div>';
                });
        }

        function renderDirectory(data) {
            let html = '<ul class="browser-list">';

            // Add parent directory option if not at root
            if (data.parent) {
                html += '<li class="browser-item parent-dir" data-path="' + escapeHtml(data.parent) + '" data-isdir="true">';
                html += '<span class="browser-item-icon">📁</span>';
                html += '<span class="browser-item-name">..</span>';
                html += '</li>';
            }

            // Add entries
            for (const entry of data.entries) {
                const icon = entry.isDir ? '📁' : '📄';
                html += '<li class="browser-item' + (entry.isDir ? '' : ' file-item') + '" ';
                html += 'data-path="' + escapeHtml(entry.path) + '" ';
                html += 'data-isdir="' + entry.isDir + '">';
                html += '<span class="browser-item-icon">' + icon + '</span>';
                html += '<span class="browser-item-name">' + escapeHtml(entry.name) + '</span>';
                html += '</li>';
            }

            html += '</ul>';
            browserBody.innerHTML = html;

            // Add click handlers
            const items = browserBody.querySelectorAll('.browser-item');
            items.forEach(item => {
                item.addEventListener('click', () => {
                    const path = item.dataset.path;
                    const isDir = item.dataset.isdir === 'true';

                    if (isDir) {
                        // Navigate into directory
                        loadDirectory(path);
                    }
                });

                item.addEventListener('dblclick', () => {
                    const path = item.dataset.path;
                    const isDir = item.dataset.isdir === 'true';

                    if (isDir) {
                        // Double-click on directory selects it and closes
                        selectedPath = path;
                        searchDirInput.value = selectedPath;
                        closeBrowser();
                    }
                });
            });
        }

        // Keyboard navigation
        document.addEventListener('keydown', (e) => {
            if (browserModal.classList.contains('active')) {
                if (e.key === 'Escape') {
                    closeBrowser();
                }
            }
        });
    </script>
</body>
</html>`

// WebServer handles the web UI
type WebServer struct {
	port         int
	searches     map[string]chan SearchResult
	searchesMu   sync.RWMutex
	uploadedImgs map[string]ImageData
	uploadsMu    sync.RWMutex
}

// NewWebServer creates a new web server
func NewWebServer(port int) *WebServer {
	return &WebServer{
		port:         port,
		searches:     make(map[string]chan SearchResult),
		uploadedImgs: make(map[string]ImageData),
	}
}

// Start starts the web server
func (ws *WebServer) Start() error {
	http.HandleFunc("/", ws.handleIndex)
	http.HandleFunc("/api/search", ws.handleSearch)
	http.HandleFunc("/api/results/", ws.handleResults)
	http.HandleFunc("/api/thumbnail", ws.handleThumbnail)
	http.HandleFunc("/api/browse", ws.handleBrowse)
	http.HandleFunc("/api/exif", ws.handleExif)
	http.HandleFunc("/api/download", ws.handleDownload)

	addr := fmt.Sprintf(":%d", ws.port)
	fmt.Printf("Starting web server at http://localhost%s\n", addr)
	return http.ListenAndServe(addr, nil)
}

func (ws *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlTemplate))
}

func (ws *WebServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(32 << 20) // 32MB max
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "No image uploaded"})
		return
	}
	defer file.Close()

	// Hash the uploaded image
	sourceData, err := LoadAndHashImageFromReader(file)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to process image: " + err.Error()})
		return
	}

	// Parse config
	threshold := 70.0
	if t := r.FormValue("threshold"); t != "" {
		fmt.Sscanf(t, "%f", &threshold)
	}

	workers := 0
	if w := r.FormValue("workers"); w != "" {
		fmt.Sscanf(w, "%d", &workers)
	}

	topN := 0
	if n := r.FormValue("topN"); n != "" {
		fmt.Sscanf(n, "%d", &topN)
	}

	searchDir := r.FormValue("dir")
	if searchDir == "" {
		searchDir = "."
	}

	// Resolve to absolute path for consistency
	absSearchDir, err := filepath.Abs(searchDir)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid search directory: " + err.Error()})
		return
	}

	config := SearchConfig{
		SearchDir: absSearchDir,
		Threshold: threshold,
		Workers:   workers,
		TopN:      topN,
	}

	// Generate search ID
	searchID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Create result channel
	resultChan := make(chan SearchResult, 100)
	ws.searchesMu.Lock()
	ws.searches[searchID] = resultChan
	ws.searchesMu.Unlock()

	// Start search in background
	go func() {
		RunSearch(sourceData, config, func(result SearchResult) {
			resultChan <- result
			if result.Done {
				close(resultChan)
			}
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"searchId": searchID})
}

func (ws *WebServer) handleResults(w http.ResponseWriter, r *http.Request) {
	searchID := strings.TrimPrefix(r.URL.Path, "/api/results/")

	ws.searchesMu.RLock()
	resultChan, ok := ws.searches[searchID]
	ws.searchesMu.RUnlock()

	if !ok {
		http.Error(w, "Search not found", http.StatusNotFound)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	for result := range resultChan {
		data, _ := json.Marshal(result)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Clean up
	ws.searchesMu.Lock()
	delete(ws.searches, searchID)
	ws.searchesMu.Unlock()
}

func (ws *WebServer) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	thumb, err := GenerateThumbnail(path, 200)
	if err != nil {
		http.Error(w, "Failed to generate thumbnail", http.StatusInternalServerError)
		return
	}

	data, err := base64.StdEncoding.DecodeString(thumb)
	if err != nil {
		http.Error(w, "Failed to decode thumbnail", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(data)
}

// BrowseResponse represents a directory listing
type BrowseResponse struct {
	Path    string       `json:"path"`
	Parent  string       `json:"parent,omitempty"`
	Entries []BrowseEntry `json:"entries"`
	Error   string       `json:"error,omitempty"`
}

// BrowseEntry represents a file or directory entry
type BrowseEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Path  string `json:"path"`
}

func (ws *WebServer) handleBrowse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		// Start at home directory or current working directory
		home, err := os.UserHomeDir()
		if err != nil {
			cwd, _ := os.Getwd()
			path = cwd
		} else {
			path = home
		}
	}

	// Clean and resolve the path
	path = filepath.Clean(path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Invalid path"})
		return
	}

	// Check if path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Path not found"})
		return
	}
	if !info.IsDir() {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Not a directory"})
		return
	}

	// Read directory entries
	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Cannot read directory"})
		return
	}

	var entries []BrowseEntry
	for _, entry := range dirEntries {
		// Skip hidden files/directories (starting with .)
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		entryPath := filepath.Join(absPath, entry.Name())
		entries = append(entries, BrowseEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Path:  entryPath,
		})
	}

	// Sort: directories first, then alphabetically
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	// Get parent directory
	parent := filepath.Dir(absPath)
	if parent == absPath {
		parent = "" // Root directory has no parent
	}

	json.NewEncoder(w).Encode(BrowseResponse{
		Path:    absPath,
		Parent:  parent,
		Entries: entries,
	})
}

func (ws *WebServer) handleExif(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		json.NewEncoder(w).Encode(ExifData{Error: "Path required"})
		return
	}

	data := ExtractExifData(path)
	json.NewEncoder(w).Encode(data)
}

func (ws *WebServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	// Verify the file exists and is an image
	if !IsImageFile(path) {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Get file info for Content-Length and filename
	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, "Cannot read file info", http.StatusInternalServerError)
		return
	}

	// Set headers for download
	filename := filepath.Base(path)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Stream the file
	io.Copy(w, file)
}

// ============== Main ==============

func main() {
	// Command line flags
	sourceFile := flag.String("source", "", "Source image file to compare against")
	searchDir := flag.String("dir", ".", "Directory to search for similar images")
	threshold := flag.Float64("threshold", 70.0, "Minimum similarity threshold (0-100)")
	workers := flag.Int("workers", 0, "Number of parallel workers (0 = auto)")
	verbose := flag.Bool("verbose", false, "Show detailed output")
	topN := flag.Int("top", 0, "Show only top N results (0 = all above threshold)")
	outputFile := flag.String("output", "", "Optional file to write results to")
	webMode := flag.Bool("web", false, "Start web UI instead of CLI")
	webPort := flag.Int("port", 9183, "Port for web UI")

	flag.Parse()

	// Web mode
	if *webMode {
		server := NewWebServer(*webPort)
		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting web server: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// CLI mode
	if *sourceFile == "" {
		fmt.Println("Usage: imgsearch -source <image> [-dir <directory>] [-threshold <0-100>]")
		fmt.Println("       imgsearch -web [-port <port>]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Set number of workers
	numWorkers := *workers
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}

	// Load source image
	fmt.Printf("Loading source image: %s\n", *sourceFile)
	sourceData := LoadAndHashImage(*sourceFile)
	if sourceData.Error != nil {
		fmt.Fprintf(os.Stderr, "Error loading source image: %v\n", sourceData.Error)
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("Source hashes - pHash: %016x, aHash: %016x, dHash: %016x\n",
			sourceData.PHash, sourceData.AHash, sourceData.DHash)
	}

	// Find all images in directory
	fmt.Printf("Scanning directory: %s\n", *searchDir)
	images, err := FindImages(*searchDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning directory: %v\n", err)
		os.Exit(1)
	}

	// Remove source from search if present
	var searchImages []string
	absSource, _ := filepath.Abs(*sourceFile)
	for _, img := range images {
		absImg, _ := filepath.Abs(img)
		if absImg != absSource {
			searchImages = append(searchImages, img)
		}
	}

	fmt.Printf("Found %d images to compare\n", len(searchImages))

	if len(searchImages) == 0 {
		fmt.Println("No images found to compare.")
		return
	}

	// Helper function to output a result line to screen
	resultCount := 0
	var resultMutex sync.Mutex
	var allMatches []ImageMatch

	outputResult := func(match ImageMatch) {
		resultMutex.Lock()
		defer resultMutex.Unlock()
		resultCount++
		allMatches = append(allMatches, match)
		fmt.Printf("%d. [%.1f%%] %s\n", resultCount, match.Similarity, match.Path)
		if *verbose {
			fmt.Printf("   pHash: %016x, Hamming distance: %d\n",
				match.Hash, HammingDistance(sourceData.PHash, match.Hash))
		}
	}

	fmt.Println("\n=== Results (as found) ===\n")

	// Process images in parallel
	var wg sync.WaitGroup
	imageChan := make(chan string, len(searchImages))

	// Start workers - results are printed as they are found
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range imageChan {
				data := LoadAndHashImage(path)
				if data.Error != nil {
					if *verbose {
						fmt.Fprintf(os.Stderr, "Skipping %s: %v\n", path, data.Error)
					}
					continue
				}

				similarity := ComputeSimilarity(sourceData, data)
				if similarity >= *threshold {
					match := ImageMatch{
						Path:       path,
						Similarity: similarity,
						Hash:       data.PHash,
					}
					outputResult(match)
				}
			}
		}()
	}

	// Send work
	for _, img := range searchImages {
		imageChan <- img
	}
	close(imageChan)

	// Wait for completion
	wg.Wait()

	// Summary
	fmt.Printf("\n=== Total matches found: %d ===\n", resultCount)

	// Write sorted results to file if requested
	if *outputFile != "" {
		outFile, err := os.Create(*outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()

		// Sort by similarity (highest first)
		sort.Slice(allMatches, func(i, j int) bool {
			return allMatches[i].Similarity > allMatches[j].Similarity
		})

		// Limit results if requested
		outputMatches := allMatches
		if *topN > 0 && len(outputMatches) > *topN {
			outputMatches = outputMatches[:*topN]
		}

		fmt.Fprintln(outFile, "=== Similar Images (sorted by similarity) ===\n")
		for i, match := range outputMatches {
			fmt.Fprintf(outFile, "%d. [%.1f%%] %s\n", i+1, match.Similarity, match.Path)
			if *verbose {
				fmt.Fprintf(outFile, "   pHash: %016x, Hamming distance: %d\n",
					match.Hash, HammingDistance(sourceData.PHash, match.Hash))
			}
		}
		fmt.Fprintf(outFile, "\n=== Total matches: %d ===\n", len(outputMatches))
		fmt.Printf("Sorted results written to: %s\n", *outputFile)
	}
}
