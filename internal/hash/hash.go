package hash

import (
	"image"
	"math"
	"sort"

	"github.com/nfnt/resize"
)

// ColorHistogram computes a simple color histogram for additional comparison
type ColorHistogram struct {
	R, G, B [16]float64 // 16 bins per channel
}

// Data holds precomputed hash data for an image
type Data struct {
	Path      string
	PHash     uint64
	AHash     uint64
	DHash     uint64
	Histogram ColorHistogram
	Error     error
}

// Perceptual computes a perceptual hash (pHash) for an image.
// This hash is resistant to scaling and minor modifications.
func Perceptual(img image.Image) uint64 {
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

// Average computes an average hash (aHash) for an image.
// Simpler but still effective for finding similar images.
func Average(img image.Image) uint64 {
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

// Difference computes a difference hash (dHash) for an image.
// Compares adjacent pixels, very resistant to scaling.
func Difference(img image.Image) uint64 {
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

// Similarity converts hamming distance to a similarity percentage (0-100)
func Similarity(hash1, hash2 uint64, hashBits int) float64 {
	distance := HammingDistance(hash1, hash2)
	return 100.0 * (1.0 - float64(distance)/float64(hashBits))
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
