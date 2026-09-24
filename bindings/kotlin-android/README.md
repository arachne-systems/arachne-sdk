# Kotlin for Android

The Android library reuses the Kotlin/JVM client and packages a release Rust
SDK for arm64 and x86_64. It uses JNA's Android AAR and targets Android 8.0/API
26 or newer. The Rust shared libraries are linked for 16 KB page-size devices.
The supported ABIs are `arm64-v8a` and `x86_64`; restrict an app's ABI filters
to those targets.

Install the Android SDK/NDK, set `ANDROID_HOME` and `ANDROID_NDK_HOME`, and add
the Rust Android targets before building the AAR:

```sh
rustup toolchain install 1.98.0 --profile minimal
rustup target add aarch64-linux-android x86_64-linux-android --toolchain 1.98.0
cargo +1.98.0 install cargo-ndk --version 4.1.2 --locked
cd bindings/kotlin-android
../kotlin/gradlew :sdk:assembleRelease
```

The Gradle task builds `libarachne_sdk.so` for both ABIs and places the generated
libraries in the AAR. To consume a local AAR, add it and its runtime dependencies
to the app:

```kotlin
dependencies {
    implementation(files("libs/sdk-release.aar"))
    implementation("net.java.dev.jna:jna:5.17.0@aar")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.20.0")
}
```

The `:sdk` project declares those dependencies when included as a Gradle
subproject. The library client is blocking; call it from a worker thread.

For example, set `ndk { abiFilters += setOf("arm64-v8a", "x86_64") }` in the
app's `defaultConfig`.

Run the Android runtime smoke test on an x86_64 emulator:

```sh
../kotlin/gradlew :smoke:connectedDebugAndroidTest
```
