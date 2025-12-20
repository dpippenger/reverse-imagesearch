package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/web"
)

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
		server := web.New(*webPort)
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
	sourceData := imgutil.LoadAndHash(*sourceFile)
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
	images, err := imgutil.FindImages(*searchDir)
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
	var allMatches []imgutil.Match

	outputResult := func(match imgutil.Match) {
		resultMutex.Lock()
		defer resultMutex.Unlock()
		resultCount++
		allMatches = append(allMatches, match)
		fmt.Printf("%d. [%.1f%%] %s\n", resultCount, match.Similarity, match.Path)
		if *verbose {
			fmt.Printf("   pHash: %016x, Hamming distance: %d\n",
				match.Hash, hash.HammingDistance(sourceData.PHash, match.Hash))
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
				data := imgutil.LoadAndHash(path)
				if data.Error != nil {
					if *verbose {
						fmt.Fprintf(os.Stderr, "Skipping %s: %v\n", path, data.Error)
					}
					continue
				}

				similarity := imgutil.ComputeSimilarity(sourceData, data)
				if similarity >= *threshold {
					match := imgutil.Match{
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
					match.Hash, hash.HammingDistance(sourceData.PHash, match.Hash))
			}
		}
		fmt.Fprintf(outFile, "\n=== Total matches: %d ===\n", len(outputMatches))
		fmt.Printf("Sorted results written to: %s\n", *outputFile)
	}
}
