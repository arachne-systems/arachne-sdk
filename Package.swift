// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ArachneSDK",
    products: [
        .library(name: "ArachneSDK", targets: ["ArachneSDK"]),
    ],
    targets: [
        .systemLibrary(
            name: "ArachneApiFFI",
            path: "generated/swift/ArachneApiFFI"
        ),
        .systemLibrary(
            name: "ArachneRuntimeFFI",
            path: "generated/swift/ArachneRuntimeFFI"
        ),
        .target(
            name: "ArachneSDK",
            dependencies: ["ArachneApiFFI", "ArachneRuntimeFFI"],
            path: "generated/swift",
            exclude: ["ArachneApiFFI", "ArachneRuntimeFFI"],
            sources: ["ArachneApi/ArachneApi.swift", "ArachneRuntime/ArachneRuntime.swift"],
            linkerSettings: [.linkedLibrary("arachne_sdk")]
        ),
        .executableTarget(
            name: "ArachneSmoke",
            dependencies: ["ArachneSDK"],
            path: "tests/uniffi/swift"
        ),
    ]
)
