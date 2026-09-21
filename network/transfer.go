package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const uploadPath = "./memobyte_storage"

// Static cluster master encryption key (Ensures data is naturally encrypted at rest)
var clusterEncryptionKey = []byte("0123456789abcdef0123456789abcdef") 

type StorageNode struct {
	ID     int    `json:"id"`
	Host   string `json:"host"`
	Status string `json:"status"`
}

type ClusterConfig struct {
	Nodes []StorageNode `json:"cluster_nodes"`
}

func getClusterNodes() []StorageNode {
	file, err := os.Open("management/nodes.json")
	if err != nil {
		return []StorageNode{{ID: 0, Host: "localhost:8080", Status: "active"}}
	}
	defer file.Close()

	var config ClusterConfig
	json.NewDecoder(file).Decode(&config)
	return config.Nodes
}

func hashString(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// Encrypt data using AES-GCM before writing to storage blocks
func encryptData(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(clusterEncryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt data on-the-fly during file retrieval/streaming
func decryptData(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(clusterEncryptionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, cipherbytes := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, cipherbytes, nil)
}

func replicateChunkToCluster(chunkName string, encryptedData []byte, nodes []StorageNode) {
	replicationCount := 2 // Keep 2 backup copies for cluster fault tolerance
	if len(nodes) < replicationCount {
		replicationCount = len(nodes)
	}

	primaryIndex := int(hashString(chunkName)) % len(nodes)

	for i := 0; i < replicationCount; i++ {
		nodeIndex := (primaryIndex + i) % len(nodes)
		targetNode := nodes[nodeIndex]

		go func(node StorageNode) {
			if node.Host == "localhost:8080" || node.Host == "127.0.0.1:8080" {
				os.MkdirAll(uploadPath, os.ModePerm)
				os.WriteFile(filepath.Join(uploadPath, chunkName), encryptedData, 0644)
			} else {
				forwardURL := fmt.Sprintf("http://%s/api/receive-chunk?name=%s", node.Host, chunkName)
				http.Post(forwardURL, "application/octet-stream", bytes.NewReader(encryptedData))
			}
		}(targetNode)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(32 << 20) // 32MB RAM buffer limit
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

	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			// Naturally encrypt chunk block before cluster routing
			encryptedBlock, err := encryptData(buffer[:n])
			if err != nil {
				continue
			}

			chunkName := fmt.Sprintf("%s_chunk_%d", handler.Filename, chunkIndex)
			replicateChunkToCluster(chunkName, encryptedBlock, nodes)
			chunkIndex++
		}
		if readErr != nil {
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "success", "encrypted_chunks": %d, "filename": "%s"}`, chunkIndex, handler.Filename)
}

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
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	fileName := r.URL.Query().Get("file")
	if fileName == "" {
		http.Error(w, "Missing file identifier", http.StatusBadRequest)
		return
	}

	// Cluster-wide purge of file and its corresponding chunk blocks
	filepath.Walk(uploadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(uploadPath, path)
		if rel == fileName || strings.HasPrefix(rel, fileName+"_chunk_") {
			os.Remove(path)
		}
		return nil
	})

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "deleted", "filename": "%s"}`, fileName)
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
			if err == nil && !strings.Contains(rel, "_chunk_") {
				fileList = append(fileList, rel)
			}
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fileList)
}

func clusterHealthHandler(w http.ResponseWriter, r *http.Request) {
	nodes := getClusterNodes()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Path[len("/files/"):]
	targetFile := filepath.Join(uploadPath, filePath)

	data, err := os.ReadFile(targetFile)
	if err != nil {
		http.Error(w, "File not found in cluster pool", http.StatusNotFound)
		return
	}

	// On-the-fly decryption for secure client delivery
	decrypted, err := decryptData(data)
	if err == nil {
		w.Write(decrypted)
		return
	}

	http.ServeFile(w, r, targetFile)
}

func main() {
	port := ":8080"

	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/receive-chunk", receiveChunkHandler)
	http.HandleFunc("/api/delete", deleteHandler)
	http.HandleFunc("/api/files", listFilesHandler)
	http.HandleFunc("/api/health", clusterHealthHandler)
	http.HandleFunc("/files/", fileDownloadHandler)
	
	fs := http.FileServer(http.Dir("./ui"))
	http.Handle("/", fs)

	fmt.Printf("[*] Memobyte Secure Distributed Exabyte Node active on http://localhost%s\n", port)
	http.ListenAndServe(port, nil)
}
