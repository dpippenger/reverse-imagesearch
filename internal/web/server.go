package web

import (
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

	"imgsearch/internal/exif"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/search"
)

//go:embed template.html app.js
var content embed.FS

// Server handles the web UI
type Server struct {
	port            int
	bindAddr        string // Bind address (default "127.0.0.1" for security)
	searches        map[string]chan search.Result
	searchesMu      sync.RWMutex
	allowedBasePath string // Base path for file access (empty = user home)
}

// validatePath checks if a path is within the allowed base directory and returns
// the cleaned absolute path. This prevents path traversal attacks by ensuring all
// file access stays within the configured base path.
// Returns the cleaned absolute path and true if valid, or empty string and false if not.
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

// isPathAllowed checks if a path is within the allowed base directory.
// Deprecated: Use validatePath instead which returns the cleaned path.
func (s *Server) isPathAllowed(requestedPath string) bool {
	_, ok := s.validatePath(requestedPath)
	return ok
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

// generateSearchID creates a cryptographically random search ID
func generateSearchID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to less random but still unique ID
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", b)))
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
		searches: make(map[string]chan search.Result),
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
		searches:        make(map[string]chan search.Result),
		allowedBasePath: basePath,
	}
}

// NewWithBasePath creates a new web server with a custom allowed base path.
// This is useful for restricting file access to a specific directory.
// Binds to localhost only for security.
func NewWithBasePath(port int, basePath string) *Server {
	return &Server{
		port:            port,
		bindAddr:        "127.0.0.1",
		searches:        make(map[string]chan search.Result),
		allowedBasePath: basePath,
	}
}

// Start starts the web server
func (s *Server) Start() error {
	http.HandleFunc("/", s.handleIndex)
	http.HandleFunc("/app.js", s.handleAppJS)
	http.HandleFunc("/api/search", s.handleSearch)
	http.HandleFunc("/api/results/", s.handleResults)
	http.HandleFunc("/api/thumbnail", s.handleThumbnail)
	http.HandleFunc("/api/browse", s.handleBrowse)
	http.HandleFunc("/api/exif", s.handleExif)
	http.HandleFunc("/api/download", s.handleDownload)

	addr := fmt.Sprintf("%s:%d", s.bindAddr, s.port)
	if s.bindAddr == "0.0.0.0" {
		fmt.Printf("Starting web server at http://0.0.0.0:%d (accessible from network)\n", s.port)
		fmt.Println("WARNING: Server is accessible from the network. Ensure proper firewall rules.")
	} else {
		fmt.Printf("Starting web server at http://%s\n", addr)
	}
	return http.ListenAndServe(addr, nil)
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
	}

	// Generate cryptographically random search ID
	searchID := generateSearchID()

	// Create result channel
	resultChan := make(chan search.Result, 100)
	s.searchesMu.Lock()
	s.searches[searchID] = resultChan
	s.searchesMu.Unlock()

	// Start search in background
	go func() {
		search.Run(sourceData, config, func(result search.Result) {
			resultChan <- result
			if result.Done {
				close(resultChan)
			}
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"searchId": searchID})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	searchID := strings.TrimPrefix(r.URL.Path, "/api/results/")

	s.searchesMu.RLock()
	resultChan, ok := s.searches[searchID]
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

	for result := range resultChan {
		data, _ := json.Marshal(result)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Clean up
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

	// Get parent directory
	parent := filepath.Dir(cleanPath)
	if parent == cleanPath {
		parent = "" // Root directory has no parent
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

// RunSearch is a helper to run searches (used by search package)
func RunSearch(sourceData hash.Data, config search.Config, callback func(search.Result)) {
	search.Run(sourceData, config, callback)
}
