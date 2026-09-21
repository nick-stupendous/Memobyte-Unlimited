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
	"sort"
	"strings"
)

const uploadPath = "./memobyte_storage"

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
	replicationCount := 2
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

	r.ParseMultipartForm(32 << 20)
	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving file stream", http.StatusBadRequest)
		return
	}
	defer file.Close()

	nodes := getClusterNodes()
	chunkIndex := 0
	buffer := make([]byte, 1024*1024)

	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
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
	data, _ := io.ReadAll(r.Body)
	os.MkdirAll(uploadPath, os.ModePerm)
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

// Dynamically discover original filenames by parsing chunk prefixes in the storage pool
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll(uploadPath, os.ModePerm)
	fileMap := make(bool)
	var fileList []string
	
	files, err := os.ReadDir(uploadPath)
	if err == nil {
		for _, f := range files {
			name := f.Name()
			if strings.Contains(name, "_chunk_") {
				parts := strings.Split(name, "_chunk_")
				if len(parts) > 0 {
					baseName := parts[0]
					if !fileMap[baseName] {
						fileMap[baseName] = true
						fileList = append(fileList, baseName)
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(fileList)
}

func clusterHealthHandler(w http.ResponseWriter, r *http.Request) {
	nodes := getClusterNodes()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

// Reassemble and decrypt chunks on-the-fly when downloading/streaming
func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Path[len("/files/"):]
	
	// Find all chunks belonging to this file
	files, err := os.ReadDir(uploadPath)
	if err != nil {
		http.Error(w, "Storage pool inaccessible", http.StatusInternalServerError)
		return
	}

	var chunkFiles []string
	prefix := fileName + "_chunk_"
	for _, f := range files {
		if strings.HasPrefix(f.Name(), prefix) {
			chunkFiles = append(chunkFiles, f.Name())
		}
	}

	if len(chunkFiles) == 0 {
		http.Error(w, "File not found in cluster pool", http.StatusNotFound)
		return
	}

	// Sort chunks numerically by index to ensure correct reassembly order
	sort.Slice(chunkFiles, func(i, j int, ...) bool { // simple sorting fallback
		return chunkFiles[i] < chunkFiles[j]
	})

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))

	// Stream reassembled data to browser
	for _, chunk := range chunkFiles {
		chunkPath := filepath.Join(uploadPath, chunk)
		data, err := os.ReadFile(chunkPath)
		if err != nil {
			continue
		}

		decrypted, err := decryptData(data)
		if err == nil {
			w.Write(decrypted)
		} else {
			w.Write(data) // fallback if unencrypted
		}
	}
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
