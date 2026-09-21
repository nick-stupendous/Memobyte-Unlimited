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

type ChunkInfo struct {
	ChunkName string `json:"chunk_name"`
	NodeID    int    `json:"node_id"`
	NodeHost  string `json:"node_host"`
	SizeBytes int64  `json:"size_bytes"`
}

type ObjectInspector struct {
	Filename    string      `json:"filename"`
	TotalChunks int         `json:"total_chunks"`
	Encrypted   bool        `json:"encrypted"`
	Chunks      []ChunkInfo `json:"chunks"`
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

func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	os.MkdirAll(uploadPath, os.ModePerm)
	fileMap := make(map[string]bool)
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

func inspectHandler(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("file")
	if fileName == "" {
		http.Error(w, "Missing file identifier", http.StatusBadRequest)
		return
	}

	nodes := getClusterNodes()
	files, err := os.ReadDir(uploadPath)
	if err != nil {
		http.Error(w, "Storage pool error", http.StatusInternalServerError)
		return
	}

	var chunkInfos []ChunkInfo
	prefix := fileName + "_chunk_"

	for _, f := range files {
		if strings.HasPrefix(f.Name(), prefix) {
			info, _ := f.Info()
			chunkName := f.Name()
			
			primaryIndex := int(hashString(chunkName)) % len(nodes)
			targetNode := nodes[primaryIndex]

			chunkInfos = append(chunkInfos, ChunkInfo{
				ChunkName: chunkName,
				NodeID:    targetNode.ID,
				NodeHost:  targetNode.Host,
				SizeBytes: info.Size(),
			})
		}
	}

	inspectorData := ObjectInspector{
		Filename:    fileName,
		TotalChunks: len(chunkInfos),
		Encrypted:   true,
		Chunks:      chunkInfos,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inspectorData)
}

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Path[len("/files/"):]
	
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

	// Numerical sorting fix so it handles infinite chunks cleanly
	sort.Slice(chunkFiles, func(i, j int) bool {
		var id1, id2 int
		partsI := strings.Split(chunkFiles[i], "_chunk_")
		partsJ := strings.Split(chunkFiles[j], "_chunk_")
		if len(partsI) > 1 {
			fmt.Sscanf(partsI[1], "%d", &id1)
		}
		if len(partsJ) > 1 {
			fmt.Sscanf(partsJ[1], "%d", &id2)
		}
		return id1 < id2
	})

	var fullFileBytes []byte
	for _, chunk := range chunkFiles {
		chunkPath := filepath.Join(uploadPath, chunk)
		data, err := os.ReadFile(chunkPath)
		if err != nil {
			continue
		}

		decrypted, err := decryptData(data)
		if err == nil {
			fullFileBytes = append(fullFileBytes, decrypted...)
		} else {
			fullFileBytes = append(fullFileBytes, data...)
		}
	}

	fileSize := int64(len(fullFileBytes))

	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".flac":
		w.Header().Set("Content-Type", "audio/flac")
	case ".mp3":
		w.Header().Set("Content-Type", "audio/mpeg")
	case ".wav":
		w.Header().Set("Content-Type", "audio/wav")
	case ".ogg":
		w.Header().Set("Content-Type", "audio/ogg")
	case ".mp4":
		w.Header().Set("Content-Type", "video/mp4")
	case ".webm":
		w.Header().Set("Content-Type", "video/webm")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Header().Set("Accept-Ranges", "bytes")

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		w.Write(fullFileBytes)
		return
	}

	var start, end int64
	_, err = fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
	if err != nil {
		_, err = fmt.Sscanf(rangeHeader, "bytes=%d-", &start)
		if err != nil {
			http.Error(w, "Invalid range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end = fileSize - 1
	}

	if start >= fileSize {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		http.Error(w, "Range out of bounds", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if end >= fileSize {
		end = fileSize - 1
	}

	chunkLength := (end - start) + 1
	w.Header().Set("Content-Length", fmt.Sprintf("%d", chunkLength))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	w.WriteHeader(http.StatusPartialContent)

	w.Write(fullFileBytes[start : end+1])
}

func main() {
	port := "0.0.0.0:8080"

	http.HandleFunc("/api/upload", uploadHandler)
	http.HandleFunc("/api/receive-chunk", receiveChunkHandler)
	http.HandleFunc("/api/delete", deleteHandler)
	http.HandleFunc("/api/files", listFilesHandler)
	http.HandleFunc("/api/health", clusterHealthHandler)
	http.HandleFunc("/api/inspect", inspectHandler)
	http.HandleFunc("/files/", fileDownloadHandler)
	
	fs := http.FileServer(http.Dir("./ui"))
	http.Handle("/", fs)

	fmt.Printf("[*] Memobyte Secure Distributed Exabyte Node active on http://0.0.0.0:8080 (e.g http://your-own-ip:8080)\n")
	http.ListenAndServe(port, nil)
}
