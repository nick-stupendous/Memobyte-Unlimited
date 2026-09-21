import os
import subprocess
import sys

def print_banner():
    print("==========================================")
    print("   MEMOBYTE-UNLIMITED (Local Node Engine) ")
    print("==========================================")

def local_upload(file_path):
    if not os.path.exists(file_path):
        print(f"[!] Error: File '{file_path}' not found on your local system.")
        return

    print(f"[*] Starting local processing for: {file_path}")

    # 1. Ensure C++ engine is compiled
    if not os.path.exists("core/engine_app"):
        print("[*] Compiling C++ core engine...")
        compile_status = subprocess.run(["g++", "core/engine.cpp", "-o", "core/engine_app"])
        if compile_status.returncode != 0:
            print("[!] C++ compilation failed.")
            return

    # 2. Run the C++ Engine to chunk the file
    print("[*] Running C++ Chunking Engine...")
    subprocess.run(["./core/engine_app", file_path])

    # 3. Initialize/Update Local Database
    print("[*] Registering chunks into local SQLite database...")
    from management.database import initialize_database, register_file_chunk
    initialize_database()
    
    file_name = os.path.basename(file_path)
    storage_dir = "memobyte_storage"
    
    if os.path.exists(storage_dir):
        chunks = os.listdir(storage_dir)
        for index, chunk in enumerate(chunks):
            register_file_chunk(file_name, chunk, index)
        print(f"[✓] Successfully registered {len(chunks)} local chunks in the database.")

    print("\n[✓] Local upload complete! Your files are safely chunked and indexed locally.")

if __name__ == "__main__":
    print_banner()
    if len(sys.argv) < 2:
        print("Usage:")
        print("  python main.py upload <path_to_file>")
        sys.exit(1)

    command = sys.argv[1]
    if command == "upload" and len(sys.argv) > 2:
        local_upload(sys.argv[2])
    else:
        print("[!] Unknown command. Use: python main.py upload <file>")
