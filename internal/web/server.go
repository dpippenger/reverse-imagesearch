package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// sseResult is the SSE payload sent to the browser: a search result plus
// a base64-encoded JPEG thumbnail for matches.
type sseResult struct {
	search.Result
	Thumbnail string `json:"thumbnail,omitempty"`
}

// searchState tracks a running search and its cancellation.
type searchState struct {
	results      chan sseResult
	cancel       context.CancelFunc
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

// effectiveBasePath returns the base directory that file access is restricted
// to: the configured allowedBasePath, or the user's home directory.
func (s *Server) effectiveBasePath() (string, error) {
	if s.allowedBasePath != "" {
		return s.allowedBasePath, nil
	}
	return os.UserHomeDir()
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

	basePath, err := s.effectiveBasePath()
	if err != nil {
		return "", false
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

// cleanValidatedPath validates requestedPath via validatePath and applies
// filepath.Clean to the result so static analysis (CodeQL) sees sanitization
// on the value handlers use. Returns the cleaned path and whether it is allowed.
func (s *Server) cleanValidatedPath(requestedPath string) (string, bool) {
	validated, ok := s.validatePath(requestedPath)
	if !ok {
		return "", false
	}
	return filepath.Clean(validated), true
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

// sseSetup sets the response headers for Server-Sent Events and returns the
// flusher. If streaming is not supported it writes an error response and
// returns false.
func sseSetup(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return nil, false
	}
	return flusher, true
}

// sendSSE marshals v and writes it as a single SSE data event, flushing it
// to the client immediately.
func sendSSE(w http.ResponseWriter, flusher http.Flusher, v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
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
				timeout := 2 * time.Minute
				if state.consuming {
					timeout = 10 * time.Minute // Longer timeout for active SSE clients
				}
				if time.Since(state.lastActivity) > timeout {
					state.cancel()
					delete(s.searches, id)
					go func(ch chan sseResult) {
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

	// Parse config, keeping defaults on parse failure
	threshold := 70.0
	if t := r.FormValue("threshold"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = v
		}
	}

	workers := 0
	if wVal := r.FormValue("workers"); wVal != "" {
		if v, err := strconv.Atoi(wVal); err == nil {
			workers = v
		}
	}

	topN := 0
	if n := r.FormValue("topN"); n != "" {
		if v, err := strconv.Atoi(n); err == nil {
			topN = v
		}
	}

	searchDir := r.FormValue("dir")
	if searchDir == "" {
		searchDir = "."
	}

	cleanSearchDir, ok := s.cleanValidatedPath(searchDir)
	if !ok {
		json.NewEncoder(w).Encode(map[string]string{"error": "Access denied: path outside allowed directory"})
		return
	}

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
	resultChan := make(chan sseResult, 100)
	ctx, cancel := context.WithCancel(context.Background())

	s.searchesMu.Lock()
	s.searches[searchID] = &searchState{
		results:      resultChan,
		cancel:       cancel,
		lastActivity: time.Now(),
	}
	s.searchesMu.Unlock()

	// Start search in background — close channel after Run returns to
	// guarantee drain goroutines terminate even on context cancellation.
	go func() {
		defer close(resultChan)
		search.Run(ctx, sourceData, config, func(result search.Result) {
			payload := sseResult{Result: result}
			if result.Match.Path != "" {
				// Thumbnail errors are ignored; the browser falls back
				// to /api/thumbnail when the field is empty.
				if thumb, err := imgutil.GenerateThumbnail(result.Match.Path, 200); err == nil {
					payload.Thumbnail = base64.StdEncoding.EncodeToString(thumb)
				}
			}
			select {
			case resultChan <- payload:
			case <-ctx.Done():
			}
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"searchId": searchID})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	searchID := strings.TrimPrefix(r.URL.Path, "/api/results/")

	// Atomically look up and mark as consuming to prevent TOCTOU race with cleanup
	s.searchesMu.Lock()
	state, ok := s.searches[searchID]
	if ok {
		state.consuming = true
		state.lastActivity = time.Now()
	}
	s.searchesMu.Unlock()

	if !ok {
		http.Error(w, "Search not found", http.StatusNotFound)
		return
	}

	flusher, ok := sseSetup(w)
	if !ok {
		return
	}

	// Remove the search from the map when streaming ends
	defer func() {
		s.searchesMu.Lock()
		delete(s.searches, searchID)
		s.searchesMu.Unlock()
	}()

	// Stream results, detecting client disconnect via request context
	clientCtx := r.Context()
	lastUpdate := time.Now()
	for {
		select {
		case result, chanOpen := <-state.results:
			if !chanOpen {
				return
			}
			sendSSE(w, flusher, result)
			// Throttle lastActivity updates — cleanup checks every 30s
			if time.Since(lastUpdate) > 10*time.Second {
				s.searchesMu.Lock()
				state.lastActivity = time.Now()
				s.searchesMu.Unlock()
				lastUpdate = time.Now()
			}
		case <-clientCtx.Done():
			// Client disconnected — cancel the search and drain the channel
			state.cancel()
			go func() {
				for range state.results {
				}
			}()
			return
		}
	}
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	cleanPath, ok := s.cleanValidatedPath(path)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	thumb, err := imgutil.GenerateThumbnail(cleanPath, 200)
	if err != nil {
		http.Error(w, "Failed to generate thumbnail", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(thumb)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		// Start at the effective base directory; if the lookup fails,
		// validatePath below rejects the request anyway.
		path, _ = s.effectiveBasePath()
	}

	cleanPath, ok := s.cleanValidatedPath(path)
	if !ok {
		json.NewEncoder(w).Encode(BrowseResponse{Error: "Access denied: path outside allowed directory"})
		return
	}

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

	cleanPath, ok := s.cleanValidatedPath(path)
	if !ok {
		json.NewEncoder(w).Encode(exif.Data{Error: "Access denied"})
		return
	}

	data := exif.Extract(cleanPath)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	cleanPath, ok := s.cleanValidatedPath(path)
	if !ok {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

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

	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(w, "Cannot read file info", http.StatusInternalServerError)
		return
	}

	// Set headers for download - sanitize filename to prevent header injection
	filename := sanitizeFilename(filepath.Base(cleanPath))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	// ServeContent sets Content-Length and handles range requests
	http.ServeContent(w, r, "", fileInfo.ModTime(), file)
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

	cleanDir, ok := s.cleanValidatedPath(dir)
	if !ok {
		http.Error(w, "Access denied: path outside allowed directory", http.StatusForbidden)
		return
	}

	flusher, ok := sseSetup(w)
	if !ok {
		return
	}

	// Run scan and stream progress
	err := s.cache.Scan(cleanDir, func(progress cache.ScanProgress) {
		sendSSE(w, flusher, progress)
	})
	if err != nil {
		sendSSE(w, flusher, cache.ScanProgress{Error: err.Error(), Done: true})
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
