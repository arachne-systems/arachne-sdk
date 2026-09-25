# The smoke app adds no rules for the SDK or JNA: everything the binding
# needs must come from the AAR's consumer-rules.pro.
#
# Test harness only: the instrumentation APK resolves the Kotlin stdlib
# from the app under test (AGP dedups it), so the app must keep it.
-keep class kotlin.** { *; }
