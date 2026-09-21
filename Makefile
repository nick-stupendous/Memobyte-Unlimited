# Memobyte-Unlimited Master Makefile

all: build-core run-network

build-core:
	@echo "[*] Compiling C++ Exabyte Core Engine..."
	g++ core/engine.cpp -o core/engine_app
	@echo "[✓] C++ Engine compiled successfully."

run-network:
	@echo "[*] Starting Go Network Transport Layer..."
	go run network/transfer.go &
	@echo "[✓] Network node active."

clean:
	@echo "[*] Cleaning local binaries and storage..."
	rm -f core/engine_app
	rm -rf memobyte_storage
	rm -f memobyte_metadata.db
	@echo "[✓] Clean complete."
