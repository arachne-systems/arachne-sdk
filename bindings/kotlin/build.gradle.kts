plugins {
    kotlin("jvm") version "2.2.20"
}

repositories { mavenCentral() }

dependencies {
    implementation("net.java.dev.jna:jna:5.17.0")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.20.0")
    testImplementation(kotlin("test-junit5"))
    testRuntimeOnly("org.junit.platform:junit-platform-launcher")
}

kotlin { jvmToolchain(21) }

tasks.test { useJUnitPlatform() }
