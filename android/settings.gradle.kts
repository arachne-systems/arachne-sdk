pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        // The SDK AAR published by `:sdk:publishReleasePublicationToLocalRepository`.
        maven { url = uri(layout.rootDirectory.dir("build/repo")) }
    }
}

rootProject.name = "arachne-android"
include(":sdk", ":smoke")
