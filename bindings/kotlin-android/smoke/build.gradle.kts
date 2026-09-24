plugins {
    id("com.android.application")
    kotlin("android")
}

android {
    namespace = "org.arachne.sdk.smoke"
    compileSdk = 35

    defaultConfig {
        applicationId = "org.arachne.sdk.smoke"
        minSdk = 26
        targetSdk = 35
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        ndk { abiFilters += setOf("arm64-v8a", "x86_64") }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin.compilerOptions.jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)

val sdkAar = rootProject.project(":sdk").layout.buildDirectory.file("outputs/aar/sdk-release.aar")

dependencies {
    implementation(files(sdkAar))
    implementation("net.java.dev.jna:jna:5.17.0@aar")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.20.0")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}

tasks.named("preBuild") { dependsOn(":sdk:assembleRelease") }
