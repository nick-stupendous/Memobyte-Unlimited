package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const uploadPath = "./memobyte_storage"

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form data from the web dashboard
	r.ParseMultipartForm(10 << 30) // 10GB max limit per request
	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create target storage path
	os.MkdirAll(uploadPath, os.ModePerm)
	dstPath := filepath.Join(uploadPath, handler.Filename)
	
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Error saving file on node", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Error writing file bytes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "success", "filename": "%s", "share_link": "http://localhost:8080/files/%s"}`, handler.Filename, handler.Filename)
}

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	// Stream file back dynamically for cloud playback/download
	fileName := r.URL.Path[len("/files/"):]
	targetFile := filepath.Join(uploadPath, fileName)

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		http.Error(w, "File not found in cluster", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, targetFile)
}

func main() {
	port := ":8080"

	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/files/", fileDownloadHandler)
	
	// Serve static frontend UI folder
	fs := http.FileServer(http.Dir("./ui"))
	http.Handle("/", fs)

	fmt.Printf("[*] Memobyte Cloud PaaS Engine active at http://localhost%s\n", port)
	http.ListenAndServe(port, nil)
}
