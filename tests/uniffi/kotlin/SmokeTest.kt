// Smoke test for the generated Kotlin binding (ADR A1/A4 step 7 slice).
// Run by scripts/uniffi-smoke.sh.
import org.arachne.core.api.*
import org.arachne.core.runtime.*
import java.time.Duration
import kotlin.concurrent.thread

fun check(ok: Boolean, what: String) {
    if (!ok) { println("FAIL: $what"); kotlin.system.exitProcess(1) }
    println("ok: $what")
}

fun main() {
    check(apiVersion() >= 6u, "api_version = ${apiVersion()}")

    val options = defaultPublicationOptions()
    check(options.recipients.isEmpty() && options.mode == PublicationMode.Critical,
        "Core publication defaults are group Critical")

    // 1. Errors cross with their numeric code.
    try {
        Client.open(defaultClientConfig(Network.DIRECT).copy(secret = byteArrayOf(1, 2, 3)))
        check(false, "short secret must throw")
    } catch (e: ApiException.InvalidInput) {
        check(apiErrorCode(e).value == 100u, "short secret error code = ${apiErrorCode(e).value} (${apiErrorCode(e)})")
        check(apiErrorCode(e).value == 100u, "ErrorCode.value discriminant = ${apiErrorCode(e).value}")
    }
    try {
        Client.open(defaultClientConfig(Network.TOR).copy(secret = ByteArray(32) { 7 }))
        check(false, "Tor must throw")
    } catch (e: ApiException.State) {
        check(apiErrorCode(e) == ErrorCode.UNSUPPORTED && apiErrorCode(e).value == 103u,
            "Tor error code = ${apiErrorCode(e).value}")
    }
    try {
        Client.open(defaultClientConfig(Network.LAN))
        check(false, "LAN without a secret must throw")
    } catch (e: ApiException) {
        check(apiErrorCode(e).value == 100u, "core error code = ${apiErrorCode(e).value} ($e)")
    }

    // 2. open -> describe -> state.
    val client = Client.open(defaultClientConfig(Network.DIRECT))
    val endpoint = client.endpoint()
    check(endpoint.endpointKey.length == 64, "describe endpoint_id = ${endpoint.endpointKey}")
    val state = client.workspaceState()
    check(state.phase == Phase.EMPTY && state.workspace == null, "state phase = ${state.phase}")

    // 3. next_event with a timeout returns null after about the timeout.
    val t0 = System.nanoTime()
    val none = client.nextEvent(Duration.ofMillis(200))
    val waited = (System.nanoTime() - t0) / 1_000_000
    check(none == null && waited in 150..5000, "next_event(200) returned null after $waited ms")

    // 4. wake() releases a parked next_event.
    var woke: Event? = Event.CLOSED
    val w = thread { woke = client.nextEvent(Duration.ofSeconds(30)) }
    Thread.sleep(200); client.wake(); w.join(2000)
    check(!w.isAlive && woke == null, "wake() released next_event")

    // 5. close() from another thread releases a parked next_event.
    var got: Event? = Event.CLOSED
    var elapsedMs = -1L
    val waiter = thread {
        val start = System.nanoTime()
        got = client.nextEvent(Duration.ofSeconds(30))
        elapsedMs = (System.nanoTime() - start) / 1_000_000
    }
    Thread.sleep(200)
    var closeMs = -1L
    thread {
        val start = System.nanoTime()
        client.shutdown() // Rust `close`, renamed for Kotlin (uniffi.toml)
        closeMs = (System.nanoTime() - start) / 1_000_000
    }.join()
    waiter.join(2000)
    check(!waiter.isAlive, "waiter returned after close() (close took $closeMs ms)")
    check(got == null && elapsedMs in 0..1999, "next_event returned null after $elapsedMs ms")

    // 6. After close, calls fail with Closed (code 1).
    try {
        client.endpoint()
        check(false, "call after close must throw")
    } catch (e: ApiException.Closed) {
        check(apiErrorCode(e).value == 1u, "after close error code = ${apiErrorCode(e).value}")
    }
    client.close() // AutoCloseable: frees the native handle
    println("KOTLIN PASS")

    runFlow()
}
