// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "AgentMemoryMenuBar",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .executable(
            name: "agent-memory-menubar",
            targets: ["AgentMemoryMenuBar"]
        ),
    ],
    targets: [
        .executableTarget(
            name: "AgentMemoryMenuBar"
        ),
    ]
)
