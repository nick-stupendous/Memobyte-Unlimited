package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const uploadPath = "./memobyte_storage"

type NewDocRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll(uploadPath, os.ModePerm)
	var fileList []string

	// Recursively walk through directories to display folders and files like OneDrive
	filepath.Walk(uploadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(uploadPath, path)
			if err == nil {
				fileList = append(fileList, rel)
			}
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fileList)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(10 << 30)
	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Preserves subfolder paths if a folder was uploaded
	targetPath := filepath.Join(uploadPath, handler.Filename)
	parentDir := filepath.Dir(targetPath)
	
	os.MkdirAll(parentDir, os.ModePerm)

	dst, err := os.Create(targetPath)
	if err != nil {
		http.Error(w, "Error saving file structure", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Error writing bytes", http.StatusInternalServerError)
		return
	}

	// Trigger C++ Engine chunker on the saved asset
	cmd := exec.Command("./core/engine_app", targetPath)
	cmd.Run()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "success"}`)
}

func createDocHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req NewDocRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.Filename == "" {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	targetPath := filepath.Join(uploadPath, req.Filename)
	os.MkdirAll(filepath.Dir(targetPath), os.ModePerm)

	err = os.WriteFile(targetPath, []byte(req.Content), 0644)
	if err != nil {
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		return
	}

	// Handoff to C++ engine
	cmd := exec.Command("./core/engine_app", targetPath)
	cmd.Run()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "success"}`)
}

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Path[len("/files/"):]
	targetFile := filepath.Join(uploadPath, filePath)

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, targetFile)
}

func main() {
	port := ":8080"

	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/create-file", createDocHandler)
	http.HandleFunc("/api/files", listFilesHandler)
	http.HandleFunc("/files/", fileDownloadHandler)
	
	fs := http.FileServer(http.Dir("./ui"))
	http.Handle("/", fs)

	fmt.Printf("[*] Memobyte FOSS Storage Node active on http://localhost%s\n", port)
	http.ListenAndServe(port, nil)
}
