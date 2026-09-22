// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "ArachneSDK",
    products: [
        .library(name: "ArachneSDK", targets: ["ArachneSDK"]),
    ],
    targets: [
        .systemLibrary(
            name: "CArachneSDK",
            path: "bindings/swift/Sources/CArachneSDK"
        ),
        .target(
            name: "ArachneSDK",
            dependencies: ["CArachneSDK"],
            path: "bindings/swift/Sources/ArachneSDK",
            linkerSettings: [.linkedLibrary("arachne_sdk")]
        ),
        .testTarget(
            name: "ArachneSDKTests",
            dependencies: ["ArachneSDK"],
            path: "bindings/swift/Tests/ArachneSDKTests"
        ),
    ]
)
