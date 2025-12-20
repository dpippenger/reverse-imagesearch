package web

import (
	"embed"
	"encoding/base64"
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

	"imgsearch/internal/exif"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/search"
)

//go:embed template.html app.js
var content embed.FS

// Server handles the web UI
type Server struct {
	port       int
	searches   map[string]chan search.Result
	searchesMu sync.RWMutex
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

// New creates a new web server
func New(port int) *Server {
	return &Server{
		port:     port,
		searches: make(map[string]chan search.Result),
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

	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Starting web server at http://localhost%s\n", addr)
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

	// Resolve to absolute path for consistency
	absSearchDir, err := filepath.Abs(searchDir)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid search directory: " + err.Error()})
		return
	}

	config := search.Config{
		SearchDir: absSearchDir,
		Threshold: threshold,
		Workers:   workers,
		TopN:      topN,
	}

	// Generate search ID
	searchID := fmt.Sprintf("%d", time.Now().UnixNano())

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

	thumb, err := imgutil.GenerateThumbnail(path, 200)
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

func (s *Server) handleExif(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Query().Get("path")
	if path == "" {
		json.NewEncoder(w).Encode(exif.Data{Error: "Path required"})
		return
	}

	data := exif.Extract(path)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Path required", http.StatusBadRequest)
		return
	}

	// Verify the file exists and is an image
	if !imgutil.IsImageFile(path) {
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

// RunSearch is a helper to run searches (used by search package)
func RunSearch(sourceData hash.Data, config search.Config, callback func(search.Result)) {
	search.Run(sourceData, config, callback)
}
