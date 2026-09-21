package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const uploadPath = "./memobyte_storage"

type StorageNode struct {
	ID     int    `json:"id"`
	Host   string `json:"host"`
	Status string `json:"status"`
}

type ClusterConfig struct {
	Nodes []StorageNode `json:"cluster_nodes"`
}

// Loads cluster topology to determine where data shards live
func getClusterNodes() []StorageNode {
	file, err := os.Open("management/nodes.json")
	if err != nil {
		// Fallback to single local node if cluster map isn't found yet
		return []StorageNode{{ID: 0, Host: "localhost:8080", Status: "active"}}
	}
	defer file.Close()

	var config ClusterConfig
	json.NewDecoder(file).Decode(&config)
	return config.Nodes
}

// Deterministic hashing function (CRUSH-inspired placement logic)
func hashString(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form with a 32MB RAM buffer limit
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "File payload exceeds RAM buffer limit", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving file stream", http.StatusBadRequest)
		return
	}
	defer file.Close()

	nodes := getClusterNodes()
	chunkIndex := 0
	buffer := make([]byte, 1024*1024) // 1MB RAM sliding window chunker

	// Stream file entirely in-memory, slicing and routing chunks on the fly
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunkName := fmt.Sprintf("%s_chunk_%d", handler.Filename, chunkIndex)
			
			// Mathematically route chunk to a target cluster node
			targetNodeIndex := int(hashString(chunkName)) % len(nodes)
			targetNode := nodes[targetNodeIndex]

			if targetNode.Host == "localhost:8080" || targetNode.Host == "127.0.0.1:8080" {
				// Local storage pool write
				os.MkdirAll(uploadPath, os.ModePerm)
				os.WriteFile(filepath.Join(uploadPath, chunkName), buffer[:n], 0644)
			} else {
				// Forward chunk over network to remote cluster node via HTTP POST
				forwardURL := fmt.Sprintf("http://%s/api/receive-chunk?name=%s", targetNode.Host, chunkName)
				http.Post(forwardURL, "application/octet-stream", bytes.NewReader(buffer[:n]))
			}

			chunkIndex++
		}
		if readErr != nil {
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "success", "chunks_routed": %d, "filename": "%s"}`, chunkIndex, handler.Filename)
}

// Endpoint for receiving forwarded chunks from other cluster nodes
func receiveChunkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	chunkName := r.URL.Query().Get("name")
	if chunkName == "" {
		http.Error(w, "Missing chunk identifier", http.StatusBadRequest)
		return
	}

	os.MkdirAll(uploadPath, os.ModePerm)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read stream", http.StatusInternalServerError)
		return
	}

	os.WriteFile(filepath.Join(uploadPath, chunkName), data, 0644)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "chunk_acknowledged"}`)
}

func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll(uploadPath, os.ModePerm)
	var fileList []string
	
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

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Path[len("/files/"):]
	targetFile := filepath.Join(uploadPath, filePath)

	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		http.Error(w, "File not found in cluster pool", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, targetFile)
}

func main() {
	port := ":8080"

	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/receive-chunk", receiveChunkHandler)
	http.HandleFunc("/api/files", listFilesHandler)
	http.HandleFunc("/files/", fileDownloadHandler)
	
	fs := http.FileServer(http.Dir("./ui"))
	http.Handle("/", fs)

	fmt.Printf("[*] Memobyte Distributed Exabyte Node active on http://localhost%s\n", port)
	http.ListenAndServe(port, nil)
}
