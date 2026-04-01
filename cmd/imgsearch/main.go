package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"imgsearch/internal/cache"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/search"
	"imgsearch/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
	webBind := flag.String("bind", "127.0.0.1", "Bind address for web UI (use 0.0.0.0 for network access)")
	cachePath := flag.String("cache-path", "", "Path to cache database file (enables hash caching)")
	noCache := flag.Bool("no-cache", false, "Disable hash caching even if cache-path is set")

	flag.Parse()

	// Initialize cache if requested
	var hashCache *cache.BoltCache
	if *cachePath != "" && !*noCache {
		var err error
		hashCache, err = cache.New(*cachePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to open cache: %v\n", err)
		} else {
			defer hashCache.Close()
		}
	}

	// Web mode
	if *webMode {
		server := web.NewWithOptions(*webPort, *webBind, "")
		if hashCache != nil {
			server.SetCache(hashCache)
			fmt.Printf("Hash caching enabled: %s\n", *cachePath)
		}
		return server.Start()
	}

	// CLI mode
	if *sourceFile == "" {
		fmt.Println("Usage: imgsearch -source <image> [-dir <directory>] [-threshold <0-100>]")
		fmt.Println("       imgsearch -web [-port <port>]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		return fmt.Errorf("source flag is required")
	}

	// Load source image
	fmt.Printf("Loading source image: %s\n", *sourceFile)
	sourceData := imgutil.LoadAndHash(*sourceFile)
	if sourceData.Error != nil {
		return fmt.Errorf("loading source image: %v", sourceData.Error)
	}

	if *verbose {
		fmt.Printf("Source hashes - pHash: %016x, aHash: %016x, dHash: %016x\n",
			sourceData.PHash, sourceData.AHash, sourceData.DHash)
	}

	fmt.Printf("Scanning directory: %s\n", *searchDir)

	// Resolve source path for exclusion from results
	absSource, err := filepath.Abs(*sourceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not resolve source path: %v\n", err)
	}

	config := search.Config{
		SearchDir: *searchDir,
		Threshold: *threshold,
		Workers:   *workers,
		TopN:      *topN,
	}
	if hashCache != nil {
		config.Cache = hashCache
	}

	resultCount := 0
	var resultMutex sync.Mutex
	var allMatches []imgutil.Match

	fmt.Println("\n=== Results (as found) ===")

	search.Run(context.Background(), sourceData, config, func(r search.Result) {
		if r.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", r.Error)
			return
		}
		if r.Done {
			return
		}
		if r.Match.Path == "" {
			return
		}

		// Exclude the source image from results
		if absMatch, err := filepath.Abs(r.Match.Path); err == nil && absMatch == absSource {
			return
		}

		resultMutex.Lock()
		resultCount++
		allMatches = append(allMatches, r.Match)
		count := resultCount
		resultMutex.Unlock()

		fmt.Printf("%d. [%.1f%%] %s\n", count, r.Match.Similarity, r.Match.Path)
		if *verbose {
			fmt.Printf("   pHash: %016x, Hamming distance: %d\n",
				r.Match.Hash, hash.HammingDistance(sourceData.PHash, r.Match.Hash))
		}
	})

	// Summary
	fmt.Printf("\n=== Total matches found: %d ===\n", resultCount)

	// Write sorted results to file if requested
	if *outputFile != "" {
		outFile, err := os.Create(*outputFile)
		if err != nil {
			return fmt.Errorf("creating output file: %v", err)
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

		fmt.Fprintln(outFile, "=== Similar Images (sorted by similarity) ===")
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

	return nil
}
