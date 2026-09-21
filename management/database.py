import sqlite3
import os

DB_NAME = "memobyte_metadata.db"

def initialize_database():
    """Initializes the local SQLite database for tracking file metadata."""
    conn = sqlite3.connect(DB_NAME)
    cursor = conn.cursor()
    
    # Table to store files and their assigned cryptographic chunk hashes
    cursor.execute('''
        CREATE TABLE IF NOT EXISTS file_records (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            file_name TEXT NOT NULL,
            chunk_hash TEXT NOT NULL,
            chunk_index INTEGER NOT NULL
        )
    ''')
    
    conn.commit()
    conn.close()
    print("[✓] Database initialized: file_records table ready.")

def register_file_chunk(file_name, chunk_hash, chunk_index):
    """Saves a record of a file's chunk into the local database."""
    conn = sqlite3.connect(DB_NAME)
    cursor = conn.cursor()
    
    cursor.execute('''
        INSERT INTO file_records (file_name, chunk_hash, chunk_index)
        VALUES (?, ?, ?)
    ''', (file_name, chunk_hash, chunk_index))
    
    conn.commit()
    conn.close()

if __name__ == "__main__":
    initialize_database()
