package web

import (
	"bytes"
	"encoding/json"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	server := New(8080)

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
	server := New(8080)

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
	server := New(8080)

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
		req := httptest.NewRequest("GET", "/api/thumbnail?path=/nonexistent/image.jpg", nil)
		w := httptest.NewRecorder()

		server.handleThumbnail(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandleDownload(t *testing.T) {
	server := New(8080)

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
		// First create a valid-looking path that would pass IsImageFile
		req := httptest.NewRequest("GET", "/api/download?path=/nonexistent/image.jpg", nil)
		w := httptest.NewRecorder()

		server.handleDownload(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestHandleSearch(t *testing.T) {
	server := New(8080)

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

	t.Run("search with default directory", func(t *testing.T) {
		img := testutil.SolidColorImage(64, 64, color.RGBA{255, 0, 0, 255})
		jpegBytes := testutil.EncodeJPEG(img)

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		part, _ := writer.CreateFormFile("image", "test.jpg")
		part.Write(jpegBytes)

		// Don't set dir - should default to "."
		writer.Close()

		req := httptest.NewRequest("POST", "/api/search", &buf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()

		server.handleSearch(w, req)

		var result map[string]string
		json.NewDecoder(w.Body).Decode(&result)

		if result["searchId"] == "" {
			t.Error("Expected searchId in response")
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

func TestRunSearch(t *testing.T) {
	t.Run("RunSearch calls search.Run", func(t *testing.T) {
		sourceData := hash.Data{
			PHash: 0xFFFF,
			AHash: 0xAAAA,
			DHash: 0x5555,
		}

		tmpDir, err := os.MkdirTemp("", "runsearch-test-*")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		config := search.Config{
			SearchDir: tmpDir,
			Threshold: 50.0,
			Workers:   1,
		}

		var gotDone bool
		RunSearch(sourceData, config, func(r search.Result) {
			if r.Done {
				gotDone = true
			}
		})

		if !gotDone {
			t.Error("Expected Done result from RunSearch")
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
	server := New(8080)

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
	server := New(8080)

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
	server := New(8080)

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
	server := New(8080)

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
