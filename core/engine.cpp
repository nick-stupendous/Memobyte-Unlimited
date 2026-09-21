#include <iostream>
#include <fstream>
#include <vector>
#include <string>
#include <sys/stat.h>

const size_t CHUNK_SIZE = 1024 * 1024; // 1 MB per chunk

void createDirectory(const std::string& dirName) {
    mkdir(dirName.c_str(), 0777);
}

int main(int argc, char* argv[]) {
    if (argc < 2) {
        std::cout << "[!] Usage: ./engine <path_to_file>\n";
        return 1;
    }

    std::string filePath = argv[1];
    std::ifstream file(filePath, std::ios::binary);

    if (!file.is_open()) {
        std::cout << "[!] Error: Could not open file " << filePath << "\n";
        return 1;
    }

    createDirectory("memobyte_storage");
    std::cout << "[*] Processing file via C++ Core Engine...\n";

    std::vector<char> buffer(CHUNK_SIZE);
    int chunkIndex = 0;

    while (file.read(buffer.data(), CHUNK_SIZE) || file.gcount() > 0) {
        std::streamsize bytesRead = file.gcount();
        
        // Output chunk file name
        std::string chunkName = "memobyte_storage/chunk_" + std::to_string(chunkIndex);
        std::ofstream outFile(chunkName, std::ios::binary);
        
        outFile.write(buffer.data(), bytesRead);
        outFile.close();

        std::cout << "    [+] Written " << chunkName << " (" << bytesRead << " bytes)\n";
        chunkIndex++;
    }

    file.close();
    std::cout << "[✓] File successfully split into " << chunkIndex << " raw chunks.\n";
    return 0;
}
