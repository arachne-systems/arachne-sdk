// Smoke test for the generated Swift binding (ADR A1/A4 step 7 slice).
// Imports the generated SwiftPM modules in scripts/uniffi-smoke.sh.
import Foundation
import ArachneSDK

func clientConfig(_ network: Network, _ secret: Data? = nil) -> ClientConfig {
    var config = defaultClientConfig(network: network)
    config.secret = secret
    return config
}

func check(_ ok: Bool, _ what: String) {
    if !ok { print("FAIL: \(what)"); exit(1) }
    print("ok: \(what)")
}

final class Box<T>: @unchecked Sendable { var value: T; init(_ v: T) { value = v } }

check(apiVersion() >= 6, "api_version = \(apiVersion())")

let options = defaultPublicationOptions()
check(options.recipients.isEmpty && options.mode == .critical,
      "Core publication defaults are group Critical")

// 1. Errors cross with their numeric code.
do {
    _ = try Client.open(config: clientConfig(.direct, Data([1, 2, 3])))
    check(false, "short secret must throw")
} catch let e as ApiError {
    guard case .InvalidInput = e else { check(false, "wrong case \(e)"); exit(1) }
    check(apiErrorCode(error: e).rawValue == 100, "short secret error code = \(apiErrorCode(error: e).rawValue)")
    check(apiErrorCode(error: e).rawValue == 100, "ErrorCode.rawValue discriminant = \(apiErrorCode(error: e).rawValue)")
}
do {
    _ = try Client.open(config: clientConfig(.tor, Data(repeating: 7, count: 32)))
    check(false, "Tor must throw")
} catch let e as ApiError {
    check(apiErrorCode(error: e) == .unsupported && apiErrorCode(error: e).rawValue == 103,
          "Tor error code = \(apiErrorCode(error: e).rawValue)")
}
do {
    _ = try Client.open(config: clientConfig(.lan))
    check(false, "LAN without a secret must throw")
} catch let e as ApiError {
    check(apiErrorCode(error: e).rawValue == 100, "core error code = \(apiErrorCode(error: e).rawValue) (\(e))")
}

// 2. open -> describe -> state.
let client = try! Client.open(config: clientConfig(.direct))
let endpoint = try! client.endpoint()
check(endpoint.endpointKey.count == 64, "describe endpoint_id = \(endpoint.endpointKey)")
let state = try! client.workspaceState()
check(state.phase == .empty && state.workspace == nil, "state phase = \(state.phase)")

// 3. next_event with a timeout returns nil after about the timeout.
let t0 = Date()
let none = try! client.nextEvent(timeout: 0.2)
let waited = Int(Date().timeIntervalSince(t0) * 1000)
check(none == nil && waited >= 150 && waited < 5000, "next_event(200) returned nil after \(waited) ms")

// 4. wake() releases a parked next_event.
let woke = Box<Event?>(.closed)
let wakeDone = DispatchSemaphore(value: 0)
Thread.detachNewThread { woke.value = try! client.nextEvent(timeout: 30); wakeDone.signal() }
Thread.sleep(forTimeInterval: 0.2)
try! client.wake()
check(wakeDone.wait(timeout: .now() + 2) == .success && woke.value == nil, "wake() released next_event")

// 5. close() from another thread releases a parked next_event.
let got = Box<Event?>(.closed)
let elapsedMs = Box<Int>(-1)
let done = DispatchSemaphore(value: 0)
Thread.detachNewThread {
    let start = Date()
    got.value = try! client.nextEvent(timeout: 30)
    elapsedMs.value = Int(Date().timeIntervalSince(start) * 1000)
    done.signal()
}
Thread.sleep(forTimeInterval: 0.2)
let closeMs = Box<Int>(-1)
let closed = DispatchSemaphore(value: 0)
Thread.detachNewThread {
    let start = Date()
    try! client.close()
    closeMs.value = Int(Date().timeIntervalSince(start) * 1000)
    closed.signal()
}
closed.wait()
check(done.wait(timeout: .now() + 2) == .success, "waiter returned after close() (close took \(closeMs.value) ms)")
check(got.value == nil && elapsedMs.value >= 0 && elapsedMs.value < 2000, "next_event returned nil after \(elapsedMs.value) ms")

// 6. After close, calls fail with Closed (code 1).
do {
    _ = try client.endpoint()
    check(false, "call after close must throw")
} catch let e as ApiError {
    guard case .Closed = e else { check(false, "wrong case \(e)"); exit(1) }
    check(apiErrorCode(error: e).rawValue == 1, "after close error code = \(apiErrorCode(error: e).rawValue)")
}
print("SWIFT PASS")

do { try runFlow() } catch { check(false, "flow threw \(error)") }
