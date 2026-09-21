#include <iostream>
#include <string>
#include <functional>

// Simulating an Exabyte Cluster's Node Pool (e.g., thousands of storage servers)
const int TOTAL_CLUSTER_NODES = 1024; 

// The CRUSH-inspired deterministic placement function
// Instead of looking up a database, math decides precisely where the object lives.
int calculateTargetNode(const std::string& objectId) {
    std::hash<std::string> hasher;
    size_t hashValue = hasher(objectId);
    
    // Modulo mapping across thousands of decentralized nodes
    int targetNode = hashValue % TOTAL_CLUSTER_NODES;
    return targetNode;
}

int main() {
    std::string fileObjectId = "memobyte_file_98214375_chunk1";
    
    int assignedNode = calculateTargetNode(fileObjectId);
    
    std::cout << "[*] Object ID: " << fileObjectId << "\n";
    std::cout << "[✓] Exabyte Cluster Routing: Automatically assigned to Node ID -> [" << assignedNode << "]\n";
    std::cout << "    (No central database lookup required. Scaling to exabytes achieved via hash distribution.)\n";
    
    return 0;
}
