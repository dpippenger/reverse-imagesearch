package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"imgsearch/internal/cache"
	"imgsearch/internal/exif"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/search"
)

//go:embed template.html app.js
var content embed.FS

// searchState tracks a running search and its cancellation.
type searchState struct {
	results      chan search.Result
	cancel       context.CancelFunc
	createdAt    time.Time
	lastActivity time.Time
	consuming    bool // true while a client is streaming results
}

// Server handles the web UI
type Server struct {
	port            int
	bindAddr        string // Bind address (default "127.0.0.1" for security)
	searches        map[string]*searchState
	searchesMu      sync.RWMutex
	allowedBasePath string      // Base path for file access (empty = user home)
	cache           cache.Cache // Optional hash cache
	done            chan struct{}
}

// validatePath checks if a path is within the allowed base directory and returns
// the cleaned absolute path. This prevents path traversal attacks by ensuring all
// file access stays within the configured base path.
// Returns the cleaned absolute path and true if valid, or empty string and false if not.
//
// Security: This function implements path traversal prevention by:
// 1. Cleaning the path to resolve ".." and other traversal attempts
// 2. Converting to absolute path to handle relative path tricks
// 3. Verifying the resolved path starts with the allowed base directory
// 4. Returning the validated absolute path for use in file operations
//
// Note: Static analyzers may flag callers as vulnerable because they can't trace
// the validation through this function. The returned path is safe to use.
func (s *Server) validatePath(requestedPath string) (string, bool) {
	// Clean and resolve to absolute path
	cleaned := filepath.Clean(requestedPath)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", false
	}

	// Get the allowed base path
	basePath := s.allowedBasePath
	if basePath == "" {
		// Default to user home directory
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		basePath = home
	}

	absBase, err := filepath.Abs(filepath.Clean(basePath))
	if err != nil {
		return "", false
	}

	// Ensure the path starts with the allowed base
	// Add trailing separator to prevent prefix attacks (e.g., /home/user vs /home/user2)
	if !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) && absPath != absBase {
		return "", false
	}

	return absPath, true
}

// sanitizeFilename removes potentially dangerous characters from a filename
// to prevent HTTP header injection attacks.
func sanitizeFilename(filename string) string {
	// Remove or replace characters that could cause header injection
	var result strings.Builder
	for _, r := range filename {
		switch r {
		case '"', '\\', '\r', '\n', '\x00':
			result.WriteRune('_')
		default:
			result.WriteRune(r)
		}
	}
	return result.String()
}

// generateSearchID creates a cryptographically random search ID.
// Panics if the OS entropy source is unavailable.
func generateSearchID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// BrowseResponse represents a directory listing
type BrowseResponse struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent,omitempty"`
	Entries []BrowseEntry `json:"entries"`
	Error   string        `json:"error,omitempty"`
}

// BrowseEntry represents a file or directory entry
type BrowseEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Path  string `json:"path"`
}

// New creates a new web server that binds to localhost only (secure default).
func New(port int) *Server {
	return &Server{
		port:     port,
		bindAddr: "127.0.0.1",
		searches: make(map[string]*searchState),
		done:     make(chan struct{}),
	}
}

// NewWithOptions creates a new web server with custom configuration.
// bindAddr: address to bind to ("127.0.0.1" for localhost, "0.0.0.0" for all interfaces)
// basePath: allowed base path for file access (empty = user home directory)
func NewWithOptions(port int, bindAddr, basePath string) *Server {
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	return &Server{
		port:            port,
		bindAddr:        bindAddr,
		searches:        make(map[string]*searchState),
		allowedBasePath: basePath,
		done:            make(chan struct{}),
	}
}

// NewWithBasePath creates a new web server with a custom allowed base path.
// This is useful for restricting file access to a specific directory.
// Binds to localhost only for security.
func NewWithBasePath(port int, basePath string) *Server {
	return &Server{
		port:            port,
		bindAddr:        "127.0.0.1",
		searches:        make(map[string]*searchState),
		allowedBasePath: basePath,
		done:            make(chan struct{}),
	}
}

// NewWithCache creates a new web server with cache support.
// cachePath: path to the BoltDB cache file (if empty, caching is disabled)
func NewWithCache(port int, bindAddr, basePath, cachePath string) (*Server, error) {
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}

	s := &Server{
		port:            port,
		bindAddr:        bindAddr,
		searches:        make(map[string]*searchState),
		allowedBasePath: basePath,
		done:            make(chan struct{}),
	}

	if cachePath != "" {
		c, err := cache.New(cachePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open cache: %w", err)
		}
		s.cache = c
	}

	return s, nil
}

// SetCache sets the cache for the server.
// This allows setting the cache after server creation.
func (s *Server) SetCache(c cache.Cache) {
	s.cache = c
}

// Close closes any resources held by the server and stops background goroutines.
func (s *Server) Close() error {
	select {
	case <-s.done:
		// Already closed
	default:
		close(s.done)
	}
	if s.cache != nil {
		return s.cache.Close()
	}
	return nil
}

// Start starts the web server
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/app.js", s.handleAppJS)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/results/", s.handleResults)
	mux.HandleFunc("/api/thumbnail", s.handleThumbnail)
	mux.HandleFunc("/api/browse", s.handleBrowse)
	mux.HandleFunc("/api/exif", s.handleExif)
	mux.HandleFunc("/api/download", s.handleDownload)
	mux.HandleFunc("/api/cache/stats", s.handleCacheStats)
	mux.HandleFunc("/api/cache/scan", s.handleCacheScan)
	mux.HandleFunc("/api/cache/clear", s.handleCacheClear)
	mux.HandleFunc("/api/cache/directories", s.handleCacheDirectories)

	// Start background cleanup of abandoned searches
	go s.cleanupAbandonedSearches()

	addr := fmt.Sprintf("%s:%d", s.bindAddr, s.port)
	if s.bindAddr == "0.0.0.0" {
		fmt.Printf("Starting web server at http://0.0.0.0:%d (accessible from network)\n", s.port)
		fmt.Println("WARNING: Server is accessible from the network. Ensure proper firewall rules.")
	} else {
		fmt.Printf("Starting web server at http://%s\n", addr)
	}
	return http.ListenAndServe(addr, mux)
}

// cleanupAbandonedSearches periodically removes searches that have been inactive.
// It skips searches that are actively being consumed by an SSE client.
// Stops when s.done is closed.
func (s *Server) cleanupAbandonedSearches() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.searchesMu.Lock()
			for id, state := range s.searches {
				if state.consuming {
					continue // Skip actively consumed searches
				}
				if time.Since(state.lastActivity) > 2*time.Minute {
					state.cancel()
					delete(s.searches, id)
					go func(ch chan search.Result) {
						for range ch {
						}
					}(state.results)
				}
			}
			s.searchesMu.Unlock()
		}
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := content.ReadFile("template.html")
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	data, err := content.ReadFile("app.js")
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Write(data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
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
	sourceData, err := imgutil.LoadAndHashFromReader(file)
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
	if wVal := r.FormValue("workers"); wVal != "" {
		fmt.Sscanf(wVal, "%d", &workers)
	}

	topN := 0
	if n := r.FormValue("topN"); n != "" {
		fmt.Sscanf(n, "%d", &topN)
	}

	searchDir := r.FormValue("dir")
	if searchDir == "" {
		searchDir = "."
	}

	// Validate and clean path
	validatedDir, ok := s.validatePath(searchDir)
	if !ok {
		json.NewEncoder(w).Encode(map[string]string{"error": "Access denied: path outside allowed directory"})
		return
	}
	// Apply filepath.Clean at point of use to satisfy static analysis (CodeQL)
	cleanSearchDir := filepath.Clean(validatedDir)

	config := search.Config{
		SearchDir: cleanSearchDir,
		Threshold: threshold,
		Workers:   workers,
		TopN:      topN,
		Cache:     s.cache,
	}

	// Generate cryptographically random search ID
	searchID := generateSearchID()

	// Create result channel and cancellation context
	resultChan := make(chan search.Result, 100)
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	s.searchesMu.Lock()
	s.searches[searchID] = &searchState{
		results:      resultChan,
		cancel:       cancel,
		createdAt:    now,
		lastActivity: now,
	}
	s.searchesMu.Unlock()

	// Start search in background — close channel after Run returns to
	// guarantee drain goroutines terminate even on context cancellation.
	go func() {
		defer close(resultChan)
		search.Run(ctx, sourceData, config, func(result search.Result) {
			select {
			case resultChan <- result:
			case <-ctx.Done():
			}
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"searchId": searchID})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	searchID := strings.TrimPrefix(r.URL.Path, "/api/results/")

	s.searchesMu.RLock()
	state, ok := s.searches[searchID]
	s.searchesMu.RUnlock()

	if !ok {
		http.Error(w, "Search not found", http.StatusNotFound)
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Note: CORS header removed for security - SSE only works same-origin

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Mark as actively consuming so cleanup goroutine skips this search
	s.searchesMu.Lock()
	state.consuming = true
	state.lastActivity = time.Now()
	s.searchesMu.Unlock()

	// Stream results, detecting client disconnect via request context
	clientCtx := r.Context()
	for {
		select {
		case result, chanOpen := <-state.results:
			if !chanOpen {
				goto cleanup
			}
			data, _ := json.Marshal(result)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			s.searchesMu.Lock()
			state.lastActivity = time.Now()
			s.searchesMu.Unlock()
		case <-clientCtx.Done():
			// Client disconnected — cancel the search and drain the channel
			state.cancel()
			go func() {
				for range state.results {
				}
			}()
			goto cleanup
		}
	}

cleanup:
	s.searchesMu.Lock()
	delete(s.searches, searchID)
	s.searchesMu.Unlock()
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	// Validate and get cleaned path
	validatedPath, ok := s.validatePath(path)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	// Apply filepath.Clean at point of use to satisfy static analysis (CodeQL)
	cleanPath := filepath.Clean(validatedPath)

	thumb, err := imgutil.GenerateThumbnail(cleanPath, 200)
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

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		// Start at allowed base directory or home directory
		if s.allowedBasePath != "" {
			path = s.allowedBasePath
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				cwd, _ := os.Getwd()
				path = cwd
			} else {
				path = home
			}
		}
	}

	// Validate and get cleaned path
	validatedPath, ok := s.validatePath(path)
	if !ok {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Access denied: path outside allowed directory"})
		return
	}
	// Apply filepath.Clean at point of use to satisfy static analysis (CodeQL)
	cleanPath := filepath.Clean(validatedPath)

	// Check if path exists and is a directory
	info, err := os.Stat(cleanPath)
	if err != nil {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Path not found"})
		return
	}
	if !info.IsDir() {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Not a directory"})
		return
	}

	// Read directory entries
	dirEntries, err := os.ReadDir(cleanPath)
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
		entryPath := filepath.Join(cleanPath, entry.Name())
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

	// Get parent directory, clamped to allowed base path
	parent := filepath.Dir(cleanPath)
	if parent == cleanPath {
		parent = "" // Root directory has no parent
	} else if _, ok := s.validatePath(parent); !ok {
		parent = "" // Parent is outside allowed base path
	}

	json.NewEncoder(w).Encode(BrowseResponse{
		Path:    cleanPath,
		Parent:  parent,
		Entries: entries,
	})
}

func (s *Server) handleExif(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		json.NewEncoder(w).Encode(exif.Data{Error: "Path required"})
		return
	}

	// Validate and get cleaned path
	validatedPath, ok := s.validatePath(path)
	if !ok {
		json.NewEncoder(w).Encode(exif.Data{Error: "Access denied"})
		return
	}
	// Apply filepath.Clean at point of use to satisfy static analysis (CodeQL)
	cleanPath := filepath.Clean(validatedPath)

	data := exif.Extract(cleanPath)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	// Validate and get cleaned path
	validatedPath, ok := s.validatePath(path)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	// Apply filepath.Clean at point of use to satisfy static analysis (CodeQL)
	cleanPath := filepath.Clean(validatedPath)

	// Verify the file exists and is an image
	if !imgutil.IsImageFile(cleanPath) {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	file, err := os.Open(cleanPath)
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

	// Set headers for download - sanitize filename to prevent header injection
	filename := sanitizeFilename(filepath.Base(cleanPath))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Stream the file
	io.Copy(w, file)
}

// CacheStatsResponse holds cache statistics for the API
type CacheStatsResponse struct {
	Enabled   bool    `json:"enabled"`
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	HitRate   float64 `json:"hitRate"`
	Entries   int64   `json:"entries"`
	SizeBytes int64   `json:"sizeBytes"`
	SizeMB    float64 `json:"sizeMB"`
}

func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.cache == nil {
		json.NewEncoder(w).Encode(CacheStatsResponse{Enabled: false})
		return
	}

	stats := s.cache.Stats()
	total := stats.Hits + stats.Misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(stats.Hits) / float64(total) * 100
	}

	json.NewEncoder(w).Encode(CacheStatsResponse{
		Enabled:   true,
		Hits:      stats.Hits,
		Misses:    stats.Misses,
		HitRate:   hitRate,
		Entries:   stats.Entries,
		SizeBytes: stats.SizeBytes,
		SizeMB:    float64(stats.SizeBytes) / (1024 * 1024),
	})
}

func (s *Server) handleCacheScan(w http.ResponseWriter, r *http.Request) {
	// Accept both GET and POST for SSE compatibility (EventSource uses GET)
	if r.Method != "POST" && r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.cache == nil {
		http.Error(w, "Cache not enabled", http.StatusBadRequest)
		return
	}

	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "Directory required", http.StatusBadRequest)
		return
	}

	// Validate path
	validatedDir, ok := s.validatePath(dir)
	if !ok {
		http.Error(w, "Access denied: path outside allowed directory", http.StatusForbidden)
		return
	}
	cleanDir := filepath.Clean(validatedDir)

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Run scan and stream progress
	err := s.cache.Scan(cleanDir, func(progress cache.ScanProgress) {
		data, _ := json.Marshal(progress)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	})

	if err != nil {
		errData, _ := json.Marshal(cache.ScanProgress{Error: err.Error(), Done: true})
		fmt.Fprintf(w, "data: %s\n\n", errData)
		flusher.Flush()
	}
}

func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.cache == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Cache not enabled",
		})
		return
	}

	if err := s.cache.Clear(); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// CacheDirectoriesResponse holds the list of cached directories
type CacheDirectoriesResponse struct {
	Enabled     bool                  `json:"enabled"`
	Directories []cache.DirectoryInfo `json:"directories"`
}

func (s *Server) handleCacheDirectories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.cache == nil {
		json.NewEncoder(w).Encode(CacheDirectoriesResponse{Enabled: false})
		return
	}

	dirs := s.cache.ListDirectories()
	json.NewEncoder(w).Encode(CacheDirectoriesResponse{
		Enabled:     true,
		Directories: dirs,
	})
}
