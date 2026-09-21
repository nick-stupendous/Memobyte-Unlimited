package main

import (
	"fmt"
	"net/http"
	"os"
)

func handleRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Memobyte-Unlimited Local Storage Node Active.")
}

func main() {
	port := ":8080"
	
	// Expose the local storage folder via a local web server endpoint
	fs := http.FileServer(http.Dir("./memobyte_storage"))
	http.Handle("/storage/", http.StripPrefix("/storage/", fs))
	http.HandleFunc("/", handleRoot)

	fmt.Printf("[*] Go Network Node running locally on http://localhost%s\n", port)
	fmt.Println("[*] Serving local directory: ./memobyte_storage")
	
	err := http.ListenAndServe(port, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Server failed to start: %v\n", err)
	}
}
