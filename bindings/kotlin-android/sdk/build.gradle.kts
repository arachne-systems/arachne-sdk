plugins {
    id("com.android.library")
    kotlin("android")
}

android {
    namespace = "org.arachne.sdk"
    compileSdk = 35

    defaultConfig {
        minSdk = 26
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets["main"].java.srcDir("../../kotlin/src/main/kotlin")
    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("generated/jniLibs"))
}

kotlin.compilerOptions.jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)

dependencies {
    implementation("net.java.dev.jna:jna:5.17.0@aar")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.20.0")
}

val repositoryRoot = rootProject.projectDir.resolve("../..").canonicalFile
val nativeLibraries = layout.buildDirectory.dir("generated/jniLibs")
val buildNative by tasks.registering(Exec::class) {
    workingDir(repositoryRoot)
    inputs.file(repositoryRoot.resolve("Cargo.toml"))
    inputs.file(repositoryRoot.resolve("Cargo.lock"))
    inputs.files(fileTree(repositoryRoot.resolve(".cargo")))
    inputs.files(fileTree(repositoryRoot.resolve("crates/arachne-sdk")))
    inputs.files(fileTree(repositoryRoot.resolve("core")) { exclude("target/**") })
    inputs.property("releaseStrip", "symbols")
    outputs.dir(nativeLibraries)
    doFirst {
        project.delete(nativeLibraries.get().asFile)
        nativeLibraries.get().asFile.mkdirs()
    }
    environment("CARGO_PROFILE_RELEASE_STRIP", "symbols")
    commandLine("cargo", "+1.98.0", "ndk", "-t", "arm64-v8a", "-t", "x86_64", "-P", "26",
        "-o", nativeLibraries.get().asFile.absolutePath, "build", "--locked", "--release", "-p", "arachne-sdk")
}

tasks.named("preBuild") { dependsOn(buildNative) }
