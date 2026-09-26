"""Smoke test for the generated Python binding (ADR A1/A4 step 7 slice).

Run by scripts/uniffi-smoke.sh with generated/python on PYTHONPATH.
"""

from datetime import timedelta
import sys
import threading
import time

from arachne_generated import (
    ApiError,
    Client,
    default_client_config,
    ErrorCode,
    Event,
    Network,
    Phase,
    api_error_code,
    api_version,
)


def config(network, secret=None):
    value = default_client_config(network)
    value.secret = secret
    return value


def check(ok, what):
    if not ok:
        print(f"FAIL: {what}")
        sys.exit(1)
    print(f"ok: {what}")


check(api_version() >= 6, f"api_version = {api_version()}")

# 1. Errors cross with their numeric code.
try:
    Client.open(config(network=Network.DIRECT, secret=b"\x01\x02\x03"))
    check(False, "short secret must raise")
except ApiError.InvalidInput as e:
    check(api_error_code(e).value == 100, f"short secret error code = {api_error_code(e).value}")
    check(api_error_code(e).value == 100, f"ErrorCode.value discriminant = {api_error_code(e).value}")
try:
    Client.open(config(network=Network.TOR, secret=bytes([7]) * 32))
    check(False, "Tor must raise")
except ApiError.State as e:
    # The `code` field hides a `code()` method; use `api_error_code`.
    check(api_error_code(e) == ErrorCode.UNSUPPORTED and api_error_code(e).value == 103,
          f"Tor error code = {api_error_code(e).value}")
try:
    Client.open(config(network=Network.LAN))
    check(False, "LAN without a secret must raise")
except ApiError as e:
    check(api_error_code(e).value == 100, f"core error code = {api_error_code(e).value} ({e})")

# 2. open -> describe -> state.
client = Client.open(config(network=Network.DIRECT))
endpoint = client.endpoint()
check(len(endpoint.endpoint_key) == 64, f"describe endpoint_id = {endpoint.endpoint_key}")
state = client.workspace_state()
check(state.phase == Phase.EMPTY and state.workspace is None, f"state phase = {state.phase}")

# 3. next_event with a timeout returns None after about the timeout.
t0 = time.monotonic()
none = client.next_event(timedelta(milliseconds=200))
waited = int((time.monotonic() - t0) * 1000)
check(none is None and 150 <= waited < 5000, f"next_event(timedelta(milliseconds=200)) returned None after {waited} ms")

# 4. wake() releases a parked next_event.
box = {"ev": Event.CLOSED}
w = threading.Thread(target=lambda: box.update(ev=client.next_event(timedelta(seconds=30))))
w.start()
time.sleep(0.2)
client.wake()
w.join(2)
check(not w.is_alive() and box["ev"] is None, "wake() released next_event")

# 5. close() from another thread releases a parked next_event.
res = {"ev": Event.CLOSED, "ms": -1, "close_ms": -1}


def park():
    start = time.monotonic()
    res["ev"] = client.next_event(timedelta(seconds=30))
    res["ms"] = int((time.monotonic() - start) * 1000)


def shut():
    start = time.monotonic()
    client.close()
    res["close_ms"] = int((time.monotonic() - start) * 1000)


waiter = threading.Thread(target=park)
waiter.start()
time.sleep(0.2)
closer = threading.Thread(target=shut)
closer.start()
closer.join()
waiter.join(2)
check(not waiter.is_alive(), f"waiter returned after close() (close took {res['close_ms']} ms)")
check(res["ev"] is None and 0 <= res["ms"] < 2000, f"next_event returned None after {res['ms']} ms")

# 6. After close, calls fail with Closed (code 1).
try:
    client.endpoint()
    check(False, "call after close must raise")
except ApiError.Closed as e:
    check(api_error_code(e).value == 1, f"after close error code = {api_error_code(e).value}")
print("PYTHON PASS")
