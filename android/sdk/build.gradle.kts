// Android library for the generated Kotlin binding (ADR A1/A4 step 7).
// Sources: generated/kotlin (scripts/generate-bindings.sh). Native code:
// libarachne_sdk.so for arm64-v8a and x86_64, 16 KB aligned
// (scripts/build-android-libs.sh).
plugins {
    id("com.android.library")
    kotlin("android")
    `maven-publish`
}

android {
    namespace = "org.arachne.sdk.generated"
    compileSdk = 35

    defaultConfig {
        minSdk = 26
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets["main"].java.srcDir("../../generated/kotlin")
    sourceSets["main"].jniLibs.srcDir(layout.buildDirectory.dir("jniLibs"))

    publishing { singleVariant("release") }
}

kotlin.compilerOptions.jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)

dependencies {
    // `api`: the generated classes expose JNA types, and consumers need it
    // at run time. The published POM carries it; the AAR does not embed it
    // (a second libjnidispatch in an ATAK process is the ADR step 9 risk).
    api("net.java.dev.jna:jna:5.17.0@aar")
}

val repositoryRoot = rootProject.projectDir.resolve("..").canonicalFile
val nativeLibraries = layout.buildDirectory.dir("jniLibs")
val buildNative by tasks.registering(Exec::class) {
    workingDir(repositoryRoot)
    inputs.file(repositoryRoot.resolve("Cargo.toml"))
    inputs.file(repositoryRoot.resolve("Cargo.lock"))
    inputs.files(fileTree(repositoryRoot.resolve(".cargo")))
    inputs.files(fileTree(repositoryRoot.resolve("crates/arachne-sdk")))
    inputs.files(fileTree(repositoryRoot.resolve("core")) { exclude("target/**") })
    outputs.dir(nativeLibraries)
    environment("RUSTUP_TOOLCHAIN", "1.98.0")
    commandLine(repositoryRoot.resolve("scripts/build-android-libs.sh").path,
        nativeLibraries.get().asFile.absolutePath)
}
tasks.named("preBuild") { dependsOn(buildNative) }

publishing {
    publications {
        register<MavenPublication>("release") {
            groupId = "org.arachne"
            artifactId = "arachne-sdk-android"
            version = "0.1.0-SNAPSHOT"
            afterEvaluate { from(components["release"]) }
        }
    }
    repositories {
        maven {
            name = "local"
            url = uri(rootProject.layout.projectDirectory.dir("build/repo"))
        }
    }
}
