// Smoke test for the generated Go binding (ADR A1/A4 step 8 slice).
// Run by scripts/uniffi-smoke.sh (cgo; CGO_LDFLAGS points at the cdylib).
package smoke_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/arachne-systems/arachne-sdk/generated/go/arachne_sdk"
)

func asAPI(t *testing.T, err error) *sdk.ApiError {
	t.Helper()
	var e *sdk.ApiError
	if !errors.As(err, &e) {
		t.Fatalf("not an *ApiError: %T %v", err, err)
	}
	return e
}

func TestSmoke(t *testing.T) {
	if v := sdk.ApiVersion(); v < 5 {
		t.Fatalf("api_version = %d", v)
	}

	// 1. Errors cross with their numeric code.
	short := []byte{1, 2, 3}
	_, err := sdk.ClientOpen(sdk.ClientConfig{Network: sdk.NetworkDirect, Secret: &short})
	if !errors.Is(err, sdk.ErrApiErrorInvalidInput) {
		t.Fatalf("short secret: want InvalidInput, got %v", err)
	}
	code := sdk.ApiErrorCode(asAPI(t, err))
	// uniffi-bindgen-go v0.7.1 reads enums with explicit discriminants by
	// variant index; patches/uniffi-bindgen-go-enum-discr.patch fixes it.
	if code.Number() != 100 || uint32(code) != 100 || code != sdk.ErrorCodeInvalidInput {
		t.Fatalf("short secret code: Number()=%d Go value=%d, want 100", code.Number(), uint32(code))
	}
	t.Logf("ok: short secret error code = %d (Go value %d)", code.Number(), uint32(code))

	secret := []byte(strings.Repeat("\x07", 32))
	_, err = sdk.ClientOpen(sdk.ClientConfig{Network: sdk.NetworkTor, Secret: &secret})
	if code := sdk.ApiErrorCode(asAPI(t, err)); code != sdk.ErrorCodeUnsupported || code.Number() != 103 {
		t.Fatalf("Tor code = %d (Go value %d), want 103", code.Number(), uint32(code))
	}
	t.Logf("ok: Tor error code = 103")

	_, err = sdk.ClientOpen(sdk.ClientConfig{Network: sdk.NetworkLan})
	if n := sdk.ApiErrorCode(asAPI(t, err)).Number(); n != 100 {
		t.Fatalf("LAN without secret code = %d, want 100 (%v)", n, err)
	}
	t.Logf("ok: core error code = 100 (%v)", err)

	// 2. open -> describe -> state.
	client, err := sdk.ClientOpen(sdk.ClientConfig{Network: sdk.NetworkDirect})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Destroy()
	endpoint, err := client.Describe()
	if err != nil || len(endpoint.EndpointId) != 64 {
		t.Fatalf("describe: %+v %v", endpoint, err)
	}
	t.Logf("ok: describe endpoint_id = %s", endpoint.EndpointId)
	state, err := client.State()
	if err != nil || state.Phase != sdk.WorkspacePhaseEmpty || state.Workspace != nil {
		t.Fatalf("state: %+v %v", state, err)
	}
	t.Logf("ok: state phase = Empty")

	// 3. NextEvent with a timeout returns nil after about the timeout.
	t0 := time.Now()
	ev, err := client.NextEvent(200)
	waited := time.Since(t0)
	if err != nil || ev != nil || waited < 150*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("NextEvent(200): %v %v after %v", ev, err, waited)
	}
	t.Logf("ok: NextEvent(200) returned nil after %d ms", waited.Milliseconds())

	// 4. Wake() releases a parked NextEvent.
	woke := make(chan *sdk.Event, 1)
	go func() { ev, _ := client.NextEvent(30_000); woke <- ev }()
	time.Sleep(200 * time.Millisecond)
	if err := client.Wake(); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-woke:
		if ev != nil {
			t.Fatalf("wake gave %v", *ev)
		}
		t.Logf("ok: Wake() released NextEvent")
	case <-time.After(2 * time.Second):
		t.Fatal("Wake() did not release NextEvent")
	}

	// 5. Close() from another goroutine releases a parked NextEvent.
	type result struct {
		ev      *sdk.Event
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		ev, err := client.NextEvent(30_000)
		done <- result{ev, err, time.Since(start)}
	}()
	time.Sleep(200 * time.Millisecond)
	closed := make(chan time.Duration)
	go func() {
		start := time.Now()
		if err := client.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		closed <- time.Since(start)
	}()
	closeTook := <-closed
	select {
	case r := <-done:
		if r.ev != nil || r.err != nil || r.elapsed >= 2*time.Second {
			t.Fatalf("after close: %v %v in %v", r.ev, r.err, r.elapsed)
		}
		t.Logf("ok: NextEvent returned nil after %d ms (close took %d ms)", r.elapsed.Milliseconds(), closeTook.Milliseconds())
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not return after Close()")
	}

	// 6. After close, calls fail with Closed (code 1).
	_, err = client.Describe()
	if !errors.Is(err, sdk.ErrApiErrorClosed) || sdk.ApiErrorCode(asAPI(t, err)).Number() != 1 {
		t.Fatalf("after close: %v", err)
	}
	t.Logf("ok: after close error code = 1")
	t.Logf("GO PASS")
}
