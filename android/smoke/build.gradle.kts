// A consumer app for the SDK AAR. It takes the AAR from the local Maven repo
// (so JNA comes from the POM, as for a real app) and is minified with R8 in
// both build types, so the instrumented test runs the shrunk binding.
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
        testProguardFiles("test-proguard-rules.pro")
        ndk { abiFilters += setOf("arm64-v8a", "x86_64") }
    }

    buildTypes {
        all {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin.compilerOptions.jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)

dependencies {
    implementation("org.arachne:arachne-sdk-android:0.1.0-SNAPSHOT")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.test.ext:junit:1.2.1")
}

tasks.named("preBuild") { dependsOn(":sdk:publishReleasePublicationToLocalRepository") }
