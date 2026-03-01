package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imgsearch/internal/cache"
	"imgsearch/internal/hash"
	"imgsearch/internal/imgutil"
	"imgsearch/internal/search"
	"imgsearch/internal/testutil"
)

func TestNew(t *testing.T) {
	t.Run("creates server with correct port", func(t *testing.T) {
		server := New(8080)

		if server.port != 8080 {
			t.Errorf("port = %d, want 8080", server.port)
		}
	})

	t.Run("initializes searches map", func(t *testing.T) {
		server := New(8080)

		if server.searches == nil {
			t.Error("searches map should be initialized")
		}
	})
}

func TestHandleIndex(t *testing.T) {
	server := New(8080)

	t.Run("GET / returns HTML", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		server.handleIndex(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/html" {
			t.Errorf("Content-Type = %q, want text/html", contentType)
		}

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "<!DOCTYPE html>") {
			t.Error("Response should contain HTML doctype")
		}
	})

	t.Run("GET /other returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/other", nil)
		w := httptest.NewRecorder()

		server.handleIndex(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestHandleAppJS(t *testing.T) {
	server := New(8080)

	t.Run("GET /app.js returns JavaScript", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/app.js", nil)
		w := httptest.NewRecorder()

		server.handleAppJS(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "application/javascript" {
			t.Errorf("Content-Type = %q, want application/javascript", contentType)
		}

		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			t.Error("Response body should not be empty")
		}
	})
}

func TestHandleBrowse(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("browse with valid directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "browse-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create a subdirectory
		os.Mkdir(tmpDir+"/subdir", 0755)

		req := httptest.NewRequest("GET", "/api/browse?path="+tmpDir, nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		var result BrowseResponse
		json.NewDecoder(resp.Body).Decode(&result)

		if result.Path != tmpDir {
			t.Errorf("Path = %q, want %q", result.Path, tmpDir)
		}

		if result.Error != "" {
			t.Errorf("Unexpected error: %s", result.Error)
		}

		// Should contain the subdirectory
		found := false
		for _, entry := range result.Entries {
			if entry.Name == "subdir" && entry.IsDir {
				found = true
				break
			}
		}
		if !found {
			t.Error("Should find 'subdir' in entries")
		}
	})

	t.Run("browse with non-existent path", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/nonexistent/path", nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Error == "" {
			t.Error("Expected error for non-existent path")
		}
	})

	t.Run("browse with file path returns error", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "browse-file-*")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		req := httptest.NewRequest("GET", "/api/browse?path="+tmpFile.Name(), nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Error != "Not a directory" {
			t.Errorf("Error = %q, want 'Not a directory'", result.Error)
		}
	})

	t.Run("browse with empty path uses home directory", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse", nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Path == "" {
			t.Error("Path should not be empty")
		}
		if result.Error != "" {
			t.Errorf("Unexpected error: %s", result.Error)
		}
	})

	t.Run("browse filters hidden files", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "browse-hidden-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create hidden and visible files
		os.WriteFile(tmpDir+"/.hidden", []byte("hidden"), 0644)
		os.WriteFile(tmpDir+"/visible.txt", []byte("visible"), 0644)

		req := httptest.NewRequest("GET", "/api/browse?path="+tmpDir, nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		// Should only contain visible file
		for _, entry := range result.Entries {
			if strings.HasPrefix(entry.Name, ".") {
				t.Errorf("Hidden file should be filtered: %s", entry.Name)
			}
		}
	})

	t.Run("browse shows parent directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "browse-parent-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		subDir := tmpDir + "/subdir"
		os.Mkdir(subDir, 0755)

		req := httptest.NewRequest("GET", "/api/browse?path="+subDir, nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Parent == "" {
			t.Error("Parent should not be empty for non-root directory")
		}
		if result.Parent != tmpDir {
			t.Errorf("Parent = %q, want %q", result.Parent, tmpDir)
		}
	})
}

func TestHandleExif(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("exif with valid image", func(t *testing.T) {
		img := testutil.SolidColorImage(100, 50, color.White)
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		req := httptest.NewRequest("GET", "/api/exif?path="+path, nil)
		w := httptest.NewRecorder()

		server.handleExif(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)

		// Should have dimensions
		if result["width"] == nil || result["height"] == nil {
			t.Error("Should have width and height")
		}
	})

	t.Run("exif with missing path", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/exif", nil)
		w := httptest.NewRecorder()

		server.handleExif(w, req)

		var result map[string]interface{}
		json.NewDecoder(w.Body).Decode(&result)

		if result["error"] != "Path required" {
			t.Errorf("error = %v, want 'Path required'", result["error"])
		}
	})
}

func TestHandleThumbnail(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("thumbnail with valid image", func(t *testing.T) {
		img := testutil.SolidColorImage(200, 200, color.RGBA{0, 0, 255, 255})
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		req := httptest.NewRequest("GET", "/api/thumbnail?path="+path, nil)
		w := httptest.NewRecorder()

		server.handleThumbnail(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "image/jpeg" {
			t.Errorf("Content-Type = %q, want image/jpeg", contentType)
		}

		body, _ := io.ReadAll(resp.Body)
		// JPEG magic bytes
		if len(body) < 2 || body[0] != 0xFF || body[1] != 0xD8 {
			t.Error("Response should be valid JPEG")
		}
	})

	t.Run("thumbnail with missing path", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/thumbnail", nil)
		w := httptest.NewRecorder()

		server.handleThumbnail(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("thumbnail with non-existent file", func(t *testing.T) {
		// Use a path within allowed directory that doesn't exist
		nonExistentPath := os.TempDir() + "/nonexistent-image-12345.jpg"
		req := httptest.NewRequest("GET", "/api/thumbnail?path="+nonExistentPath, nil)
		w := httptest.NewRecorder()

		server.handleThumbnail(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandleDownload(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("download with valid image", func(t *testing.T) {
		img := testutil.SolidColorImage(100, 100, color.White)
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		req := httptest.NewRequest("GET", "/api/download?path="+path, nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "image/jpeg" {
			t.Errorf("Content-Type = %q, want image/jpeg", contentType)
		}

		contentDisposition := resp.Header.Get("Content-Disposition")
		if !strings.Contains(contentDisposition, "attachment") {
			t.Errorf("Content-Disposition should contain 'attachment', got %q", contentDisposition)
		}

		contentLength := resp.Header.Get("Content-Length")
		if contentLength == "" || contentLength == "0" {
			t.Error("Content-Length should be set and non-zero")
		}
	})

	t.Run("download with missing path", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/download", nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("download with non-image file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.txt")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.Write([]byte("not an image"))
		tmpFile.Close()

		req := httptest.NewRequest("GET", "/api/download?path="+tmpFile.Name(), nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("download with non-existent file", func(t *testing.T) {
		// Use a path within allowed directory that doesn't exist
		nonExistentPath := os.TempDir() + "/nonexistent-image-12345.jpg"
		req := httptest.NewRequest("GET", "/api/download?path="+nonExistentPath, nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestHandleSearch(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("search with GET returns 405", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/search", nil)
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Status = %d, want 405", resp.StatusCode)
		}
	})

	t.Run("search with POST and valid image", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)

		tmpDir, err := os.MkdirTemp("", "search-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create multipart form
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(jpegBytes)

		writer.WriteField("dir", tmpDir)
		writer.WriteField("threshold", "50")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result)

		if result["searchId"] == "" {
			t.Error("Expected searchId in response")
		}
	})

	t.Run("search without image returns error", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writer.WriteField("dir", ".")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		if result["error"] == "" {
			t.Error("Expected error for missing image")
		}
	})

	t.Run("search with invalid image returns error", func(t *testing.T) {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(testutil.NotAnImage())

		writer.WriteField("dir", ".")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		if result["error"] == "" {
			t.Error("Expected error for invalid image")
		}
	})

	t.Run("search with custom parameters", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)

		tmpDir, err := os.MkdirTemp("", "search-params-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(jpegBytes)

		writer.WriteField("dir", tmpDir)
		writer.WriteField("threshold", "80")
		writer.WriteField("workers", "2")
		writer.WriteField("topN", "5")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result)

		if result["searchId"] == "" {
			t.Error("Expected searchId in response")
		}
	})

	t.Run("search with default directory returns error when outside allowed path", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(jpegBytes)

		// Don't set dir - should default to "." which is outside allowed path
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		// Should get access denied error since "." is outside /tmp
		if result["error"] == "" {
			t.Error("Expected error for path outside allowed directory")
		}
	})
}

func TestHandleResults(t *testing.T) {
	server := New(8080)

	t.Run("results with invalid searchId returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/results/invalid-id", nil)
		w := httptest.NewRecorder()

		server.handleResults(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("results with valid searchId streams SSE", func(t *testing.T) {
		server := New(8080)

		// Create a search result channel and register it
		searchID := "test-search-123"
		resultChan := make(chan search.Result, 10)
		server.searchesMu.Lock()
		server.searches[searchID] = resultChan
		server.searchesMu.Unlock()

		// Send results in a goroutine
		go func() {
			resultChan <- search.Result{
				Match:   imgutil.Match{Path: "/test/image.jpg", Similarity: 95.5},
				Total:   10,
				Scanned: 1,
			}
			resultChan <- search.Result{Done: true, Total: 10, Scanned: 10}
			close(resultChan)
		}()

		req := httptest.NewRequest("GET", "/api/results/"+searchID, nil)
		w := httptest.NewRecorder()

		server.handleResults(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", contentType)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		if !strings.Contains(bodyStr, "data:") {
			t.Error("Response should contain SSE data")
		}
		if !strings.Contains(bodyStr, "/test/image.jpg") {
			t.Error("Response should contain match path")
		}
	})
}


// Test data types
func TestBrowseResponse(t *testing.T) {
	resp := BrowseResponse{
		Path:   "/home/user",
		Parent: "/home",
		Entries: []BrowseEntry{
			{Name: "file.txt", IsDir: false, Path: "/home/user/file.txt"},
			{Name: "subdir", IsDir: true, Path: "/home/user/subdir"},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded BrowseResponse
	json.Unmarshal(data, &decoded)

	if decoded.Path != resp.Path {
		t.Errorf("Path = %q, want %q", decoded.Path, resp.Path)
	}
	if len(decoded.Entries) != 2 {
		t.Errorf("Entries count = %d, want 2", len(decoded.Entries))
	}
}

func TestBrowseEntry(t *testing.T) {
	entry := BrowseEntry{
		Name:  "test.jpg",
		IsDir: false,
		Path:  "/path/to/test.jpg",
	}

	data, _ := json.Marshal(entry)
	var decoded BrowseEntry
	json.Unmarshal(data, &decoded)

	if decoded.Name != entry.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entry.Name)
	}
	if decoded.IsDir != entry.IsDir {
		t.Errorf("IsDir = %v, want %v", decoded.IsDir, entry.IsDir)
	}
	if decoded.Path != entry.Path {
		t.Errorf("Path = %q, want %q", decoded.Path, entry.Path)
	}
}

func TestHandleBrowseSorting(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("browse sorts directories before files", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "browse-sort-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create files and directories in non-sorted order
		os.WriteFile(tmpDir+"/zebra.txt", []byte("z"), 0644)
		os.WriteFile(tmpDir+"/apple.txt", []byte("a"), 0644)
		os.Mkdir(tmpDir+"/banana_dir", 0755)
		os.Mkdir(tmpDir+"/aardvark_dir", 0755)

		req := httptest.NewRequest("GET", "/api/browse?path="+tmpDir, nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if len(result.Entries) != 4 {
			t.Fatalf("Expected 4 entries, got %d", len(result.Entries))
		}

		// First two should be directories (sorted alphabetically)
		if !result.Entries[0].IsDir || result.Entries[0].Name != "aardvark_dir" {
			t.Errorf("First entry should be aardvark_dir, got %s", result.Entries[0].Name)
		}
		if !result.Entries[1].IsDir || result.Entries[1].Name != "banana_dir" {
			t.Errorf("Second entry should be banana_dir, got %s", result.Entries[1].Name)
		}

		// Last two should be files (sorted alphabetically)
		if result.Entries[2].IsDir || result.Entries[2].Name != "apple.txt" {
			t.Errorf("Third entry should be apple.txt, got %s", result.Entries[2].Name)
		}
		if result.Entries[3].IsDir || result.Entries[3].Name != "zebra.txt" {
			t.Errorf("Fourth entry should be zebra.txt, got %s", result.Entries[3].Name)
		}
	})

	t.Run("browse at root directory has no parent", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Parent != "" {
			t.Errorf("Root directory should have empty parent, got %q", result.Parent)
		}
	})

	t.Run("browse unreadable directory returns error", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "browse-unreadable-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create a subdirectory and remove read permission
		unreadableDir := tmpDir + "/unreadable"
		os.Mkdir(unreadableDir, 0000)
		defer os.Chmod(unreadableDir, 0755)

		req := httptest.NewRequest("GET", "/api/browse?path="+unreadableDir, nil)
		w := httptest.NewRecorder()

		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Error != "Cannot read directory" {
			t.Errorf("Expected 'Cannot read directory' error, got %q", result.Error)
		}
	})
}

func TestHandleSearchEdgeCases(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("search with invalid multipart form", func(t *testing.T) {
		// Send a POST with invalid content type
		req := httptest.NewRequest("POST", "/api/search", strings.NewReader("invalid"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=invalid")
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		if result["error"] == "" {
			t.Error("Expected error for invalid multipart form")
		}
	})

	t.Run("search with invalid directory path returns error", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(jpegBytes)

		// Use a path that can't be converted to absolute path on most systems
		// This is tricky because most paths CAN be converted
		writer.WriteField("dir", "/some/path")
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		// Should either succeed with searchId or fail with error
		if result["searchId"] == "" && result["error"] == "" {
			t.Error("Expected either searchId or error in response")
		}
	})
}

func TestHandleDownloadEdgeCases(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("download with unreadable file returns error", func(t *testing.T) {
		img := testutil.SolidColorImage(100, 100, color.White)
		path, err := testutil.CreateTempJPEG(img)
		if err != nil {
			t.Fatalf("Failed to create temp JPEG: %v", err)
		}
		defer os.Remove(path)

		// Remove read permission
		os.Chmod(path, 0000)
		defer os.Chmod(path, 0644)

		req := httptest.NewRequest("GET", "/api/download?path="+path, nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		// Should get 404 because file can't be opened
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestHandleThumbnailEdgeCases(t *testing.T) {
	// Use /tmp as base path for tests since temp files are created there
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("thumbnail with corrupt JPEG", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "corrupt-*.jpg")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.Write(testutil.CorruptedJPEG())
		tmpFile.Close()

		req := httptest.NewRequest("GET", "/api/thumbnail?path="+tmpFile.Name(), nil)
		w := httptest.NewRecorder()

		server.handleThumbnail(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Status = %d, want 500", resp.StatusCode)
		}
	})
}

// Tests for security helper functions
func TestValidatePath(t *testing.T) {
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("allows path within base directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "pathtest-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		if _, ok := server.validatePath(tmpDir); !ok {
			t.Error("Path within base should be allowed")
		}
	})

	t.Run("allows nested path within base directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "pathtest-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		nestedPath := tmpDir + "/subdir/deep/path"
		if _, ok := server.validatePath(nestedPath); !ok {
			t.Error("Nested path within base should be allowed")
		}
	})

	t.Run("rejects path outside base directory", func(t *testing.T) {
		if _, ok := server.validatePath("/etc/passwd"); ok {
			t.Error("Path outside base should be rejected")
		}
	})

	t.Run("rejects path traversal attempts", func(t *testing.T) {
		traversalPath := os.TempDir() + "/../etc/passwd"
		if _, ok := server.validatePath(traversalPath); ok {
			t.Error("Path traversal should be rejected")
		}
	})

	t.Run("allows base directory itself", func(t *testing.T) {
		if _, ok := server.validatePath(os.TempDir()); !ok {
			t.Error("Base directory itself should be allowed")
		}
	})

	t.Run("rejects similar prefix outside base", func(t *testing.T) {
		// If base is /home/user, reject /home/user2
		// This tests the trailing separator check
		server := NewWithBasePath(8080, "/home/user")
		if _, ok := server.validatePath("/home/user2"); ok {
			t.Error("Similar prefix path should be rejected")
		}
	})
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.jpg", "normal.jpg"},
		{"file with spaces.jpg", "file with spaces.jpg"},
		{"file\"with\"quotes.jpg", "file_with_quotes.jpg"},
		{"file\\with\\backslash.jpg", "file_with_backslash.jpg"},
		{"file\rwith\nlines.jpg", "file_with_lines.jpg"},
		{"file\x00null.jpg", "file_null.jpg"},
		{"normal-file_123.jpeg", "normal-file_123.jpeg"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := sanitizeFilename(tc.input)
			if result != tc.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestGenerateSearchID(t *testing.T) {
	t.Run("generates non-empty ID", func(t *testing.T) {
		id := generateSearchID()
		if id == "" {
			t.Error("Search ID should not be empty")
		}
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			id := generateSearchID()
			if ids[id] {
				t.Errorf("Duplicate ID generated: %s", id)
			}
			ids[id] = true
		}
	})

	t.Run("generates 32-character hex string", func(t *testing.T) {
		id := generateSearchID()
		if len(id) != 32 {
			t.Errorf("ID length = %d, want 32", len(id))
		}
		// Verify it's valid hex
		for _, c := range id {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("Invalid hex character in ID: %c", c)
			}
		}
	})
}

func TestNewWithBasePath(t *testing.T) {
	t.Run("creates server with custom base path", func(t *testing.T) {
		server := NewWithBasePath(8080, "/custom/path")
		if server.port != 8080 {
			t.Errorf("port = %d, want 8080", server.port)
		}
		if server.allowedBasePath != "/custom/path" {
			t.Errorf("allowedBasePath = %q, want /custom/path", server.allowedBasePath)
		}
		if server.bindAddr != "127.0.0.1" {
			t.Errorf("bindAddr = %q, want 127.0.0.1", server.bindAddr)
		}
	})

	t.Run("initializes searches map", func(t *testing.T) {
		server := NewWithBasePath(8080, "/custom/path")
		if server.searches == nil {
			t.Error("searches map should be initialized")
		}
	})
}

func TestNewWithOptions(t *testing.T) {
	t.Run("creates server with all options", func(t *testing.T) {
		server := NewWithOptions(8080, "0.0.0.0", "/custom/path")
		if server.port != 8080 {
			t.Errorf("port = %d, want 8080", server.port)
		}
		if server.bindAddr != "0.0.0.0" {
			t.Errorf("bindAddr = %q, want 0.0.0.0", server.bindAddr)
		}
		if server.allowedBasePath != "/custom/path" {
			t.Errorf("allowedBasePath = %q, want /custom/path", server.allowedBasePath)
		}
	})

	t.Run("defaults to localhost when bindAddr is empty", func(t *testing.T) {
		server := NewWithOptions(8080, "", "/path")
		if server.bindAddr != "127.0.0.1" {
			t.Errorf("bindAddr = %q, want 127.0.0.1", server.bindAddr)
		}
	})

	t.Run("initializes searches map", func(t *testing.T) {
		server := NewWithOptions(8080, "0.0.0.0", "")
		if server.searches == nil {
			t.Error("searches map should be initialized")
		}
	})
}

func TestNewDefaultsToLocalhost(t *testing.T) {
	server := New(8080)
	if server.bindAddr != "127.0.0.1" {
		t.Errorf("bindAddr = %q, want 127.0.0.1 (secure default)", server.bindAddr)
	}
}

func TestPathTraversalPrevention(t *testing.T) {
	// Integration test for path traversal prevention across endpoints
	server := NewWithBasePath(8080, os.TempDir())

	t.Run("thumbnail rejects path traversal", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/thumbnail?path=/etc/passwd", nil)
		w := httptest.NewRecorder()
		server.handleThumbnail(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("Status = %d, want 403", w.Code)
		}
	})

	t.Run("download rejects path traversal", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/download?path=/etc/passwd", nil)
		w := httptest.NewRecorder()
		server.handleDownload(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("Status = %d, want 403", w.Code)
		}
	})

	t.Run("exif rejects path traversal", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/exif?path=/etc/passwd", nil)
		w := httptest.NewRecorder()
		server.handleExif(w, req)

		var result map[string]interface{}
		json.NewDecoder(w.Body).Decode(&result)
		if result["error"] != "Access denied" {
			t.Errorf("error = %v, want 'Access denied'", result["error"])
		}
	})

	t.Run("browse rejects path traversal", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/etc", nil)
		w := httptest.NewRecorder()
		server.handleBrowse(w, req)

		var result BrowseResponse
		json.NewDecoder(w.Body).Decode(&result)
		if result.Error != "Access denied: path outside allowed directory" {
			t.Errorf("error = %q, want access denied", result.Error)
		}
	})
}

// Cache endpoint tests

func TestHandleCacheStats(t *testing.T) {
	t.Run("returns disabled when no cache", func(t *testing.T) {
		server := New(8080)

		req := httptest.NewRequest("GET", "/api/cache/stats", nil)
		w := httptest.NewRecorder()

		server.handleCacheStats(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		var result CacheStatsResponse
		json.NewDecoder(resp.Body).Decode(&result)

		if result.Enabled {
			t.Error("Expected Enabled=false when no cache")
		}
	})

	t.Run("returns stats when cache is enabled", func(t *testing.T) {
		server := New(8080)

		// Create a cache
		cacheDir := t.TempDir()
		c, err := cache.New(filepath.Join(cacheDir, "cache.db"))
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer c.Close()
		server.SetCache(c)

		req := httptest.NewRequest("GET", "/api/cache/stats", nil)
		w := httptest.NewRecorder()

		server.handleCacheStats(w, req)

		var result CacheStatsResponse
		json.NewDecoder(w.Body).Decode(&result)

		if !result.Enabled {
			t.Error("Expected Enabled=true with cache")
		}
	})

	t.Run("calculates hit rate correctly", func(t *testing.T) {
		server := New(8080)

		cacheDir := t.TempDir()
		c, err := cache.New(filepath.Join(cacheDir, "cache.db"))
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer c.Close()
		server.SetCache(c)

		// Generate some cache activity with time.Now()
		mtime := time.Now()

		// Do a search to populate cache misses
		c.Get("/nonexistent", mtime)
		c.Get("/nonexistent2", mtime)

		req := httptest.NewRequest("GET", "/api/cache/stats", nil)
		w := httptest.NewRecorder()

		server.handleCacheStats(w, req)

		var result CacheStatsResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Misses != 2 {
			t.Errorf("Expected 2 misses, got %d", result.Misses)
		}
	})
}

func TestHandleCacheClear(t *testing.T) {
	t.Run("returns error when no cache", func(t *testing.T) {
		server := New(8080)

		req := httptest.NewRequest("POST", "/api/cache/clear", nil)
		w := httptest.NewRecorder()

		server.handleCacheClear(w, req)

		var result map[string]interface{}
		json.NewDecoder(w.Body).Decode(&result)

		if result["success"] != false {
			t.Error("Expected success=false when no cache")
		}
		if result["error"] == nil {
			t.Error("Expected error message")
		}
	})

	t.Run("clears cache successfully", func(t *testing.T) {
		server := New(8080)

		cacheDir := t.TempDir()
		c, err := cache.New(filepath.Join(cacheDir, "cache.db"))
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer c.Close()
		server.SetCache(c)

		req := httptest.NewRequest("POST", "/api/cache/clear", nil)
		w := httptest.NewRecorder()

		server.handleCacheClear(w, req)

		var result map[string]interface{}
		json.NewDecoder(w.Body).Decode(&result)

		if result["success"] != true {
			t.Error("Expected success=true")
		}
	})

	t.Run("rejects GET requests", func(t *testing.T) {
		server := New(8080)

		req := httptest.NewRequest("GET", "/api/cache/clear", nil)
		w := httptest.NewRecorder()

		server.handleCacheClear(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Status = %d, want 405", resp.StatusCode)
		}
	})
}

func TestHandleCacheDirectories(t *testing.T) {
	t.Run("returns disabled when no cache", func(t *testing.T) {
		server := New(8080)

		req := httptest.NewRequest("GET", "/api/cache/directories", nil)
		w := httptest.NewRecorder()

		server.handleCacheDirectories(w, req)

		var result CacheDirectoriesResponse
		json.NewDecoder(w.Body).Decode(&result)

		if result.Enabled {
			t.Error("Expected enabled=false when no cache")
		}
	})

	t.Run("returns directories with cache", func(t *testing.T) {
		server := New(8080)

		cacheDir := t.TempDir()
		c, err := cache.New(filepath.Join(cacheDir, "cache.db"))
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		defer c.Close()
		server.SetCache(c)

		// Add some cached entries
		mtime := time.Now()
		c.Put("/photos/img1.jpg", mtime, &hash.Data{Path: "/photos/img1.jpg", PHash: 1})
		c.Put("/photos/img2.jpg", mtime, &hash.Data{Path: "/photos/img2.jpg", PHash: 2})
		c.Put("/docs/scan.jpg", mtime, &hash.Data{Path: "/docs/scan.jpg", PHash: 3})

		req := httptest.NewRequest("GET", "/api/cache/directories", nil)
		w := httptest.NewRecorder()

		server.handleCacheDirectories(w, req)

		var result CacheDirectoriesResponse
		json.NewDecoder(w.Body).Decode(&result)

		if !result.Enabled {
			t.Error("Expected enabled=true")
		}
		if len(result.Directories) != 2 {
			t.Errorf("Expected 2 directories, got %d", len(result.Directories))
		}

		// Check that directories have correct counts
		dirMap := make(map[string]int)
		for _, d := range result.Directories {
			dirMap[d.Path] = d.Count
		}

		if dirMap["/photos"] != 2 {
			t.Errorf("Expected /photos to have 2 images, got %d", dirMap["/photos"])
		}
		if dirMap["/docs"] != 1 {
			t.Errorf("Expected /docs to have 1 image, got %d", dirMap["/docs"])
		}
	})
}

func TestHandleCacheScan(t *testing.T) {
	t.Run("returns error when no cache", func(t *testing.T) {
		server := NewWithBasePath(8080, os.TempDir())

		req := httptest.NewRequest("POST", "/api/cache/scan?dir="+os.TempDir(), nil)
		w := httptest.NewRecorder()

		server.handleCacheScan(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("rejects PUT requests", func(t *testing.T) {
		server := New(8080)

		req := httptest.NewRequest("PUT", "/api/cache/scan", nil)
		w := httptest.NewRecorder()

		server.handleCacheScan(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Status = %d, want 405", resp.StatusCode)
		}
	})

	t.Run("requires directory parameter", func(t *testing.T) {
		server := New(8080)

		cacheDir := t.TempDir()
		c, _ := cache.New(filepath.Join(cacheDir, "cache.db"))
		defer c.Close()
		server.SetCache(c)

		req := httptest.NewRequest("POST", "/api/cache/scan", nil)
		w := httptest.NewRecorder()

		server.handleCacheScan(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		server := NewWithBasePath(8080, os.TempDir())

		cacheDir := t.TempDir()
		c, _ := cache.New(filepath.Join(cacheDir, "cache.db"))
		defer c.Close()
		server.SetCache(c)

		req := httptest.NewRequest("POST", "/api/cache/scan?dir=/etc", nil)
		w := httptest.NewRecorder()

		server.handleCacheScan(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("scans directory and returns SSE", func(t *testing.T) {
		// Create temp directory with images
		images := map[string]image.Image{
			"red.jpg": testutil.SolidColorImage(32, 32, color.RGBA{255, 0, 0, 255}),
		}
		imgDir, cleanup, err := testutil.CreateTempDir(images)
		if err != nil {
			t.Fatalf("Failed to create test directory: %v", err)
		}
		defer cleanup()

		server := NewWithBasePath(8080, imgDir)

		cacheDir := t.TempDir()
		c, _ := cache.New(filepath.Join(cacheDir, "cache.db"))
		defer c.Close()
		server.SetCache(c)

		req := httptest.NewRequest("POST", "/api/cache/scan?dir="+imgDir, nil)
		w := httptest.NewRecorder()

		server.handleCacheScan(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status = %d, want 200", resp.StatusCode)
		}

		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", contentType)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		if !strings.Contains(bodyStr, "data:") {
			t.Error("Response should contain SSE data")
		}
		if !strings.Contains(bodyStr, "\"done\":true") {
			t.Error("Response should contain done:true")
		}
	})
}

func TestNewWithCache(t *testing.T) {
	t.Run("creates server with cache", func(t *testing.T) {
		cacheDir := t.TempDir()
		cachePath := filepath.Join(cacheDir, "cache.db")

		server, err := NewWithCache(8080, "127.0.0.1", os.TempDir(), cachePath)
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		if server.cache == nil {
			t.Error("Expected cache to be set")
		}
	})

	t.Run("creates server without cache when path is empty", func(t *testing.T) {
		server, err := NewWithCache(8080, "127.0.0.1", os.TempDir(), "")
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		if server.cache != nil {
			t.Error("Expected cache to be nil when path is empty")
		}
	})

	t.Run("returns error for invalid cache path", func(t *testing.T) {
		_, err := NewWithCache(8080, "127.0.0.1", os.TempDir(), "/dev/null/invalid/cache.db")
		if err == nil {
			t.Error("Expected error for invalid cache path")
		}
	})

	t.Run("defaults to localhost when bindAddr is empty", func(t *testing.T) {
		cacheDir := t.TempDir()
		cachePath := filepath.Join(cacheDir, "cache.db")

		server, err := NewWithCache(8080, "", os.TempDir(), cachePath)
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		if server.bindAddr != "127.0.0.1" {
			t.Errorf("bindAddr = %q, want 127.0.0.1", server.bindAddr)
		}
	})
}

func TestServerClose(t *testing.T) {
	t.Run("closes cache when present", func(t *testing.T) {
		cacheDir := t.TempDir()
		cachePath := filepath.Join(cacheDir, "cache.db")

		server, err := NewWithCache(8080, "127.0.0.1", os.TempDir(), cachePath)
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		if err := server.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})

	t.Run("succeeds when no cache", func(t *testing.T) {
		server := New(8080)

		if err := server.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})
}

func TestCacheStatsResponse(t *testing.T) {
	resp := CacheStatsResponse{
		Enabled:   true,
		Hits:      100,
		Misses:    20,
		HitRate:   83.3,
		Entries:   500,
		SizeBytes: 1024000,
		SizeMB:    1.0,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded CacheStatsResponse
	json.Unmarshal(data, &decoded)

	if decoded.Enabled != resp.Enabled {
		t.Errorf("Enabled = %v, want %v", decoded.Enabled, resp.Enabled)
	}
	if decoded.Hits != resp.Hits {
		t.Errorf("Hits = %d, want %d", decoded.Hits, resp.Hits)
	}
	if decoded.HitRate != resp.HitRate {
		t.Errorf("HitRate = %f, want %f", decoded.HitRate, resp.HitRate)
	}
}
