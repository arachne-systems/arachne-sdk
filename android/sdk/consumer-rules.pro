# R8 rules shipped in the AAR for apps that consume it.
# JNA finds native methods, Structure fields and callbacks by reflection.
-dontwarn java.awt.**
-keep class com.sun.jna.** { *; }
-keep class * implements com.sun.jna.** { *; }
-keep class * extends com.sun.jna.** { *; }
# The generated UniFFI binding: its JNA library interface (UniffiLib) and
# Structure subclasses are bound by name at run time.
-keep class org.arachne.sdk.generated.** { *; }
