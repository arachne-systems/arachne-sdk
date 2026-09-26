// Smoke test for the generated Go binding (ADR A1/A4 step 8 slice).
// Run by scripts/uniffi-smoke.sh (cgo; CGO_LDFLAGS points at the cdylib).
package smoke_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	api "github.com/arachne-systems/arachne-sdk/generated/go/arachne_api"
	sdk "github.com/arachne-systems/arachne-sdk/generated/go/arachne_runtime"
)

func asAPI(t *testing.T, err error) *api.ApiError {
	t.Helper()
	var e *api.ApiError
	if !errors.As(err, &e) {
		t.Fatalf("not an *ApiError: %T %v", err, err)
	}
	return e
}

func clientConfig(network api.Network, secret *[]byte) sdk.ClientConfig {
	config := sdk.DefaultClientConfig(network)
	config.Secret = secret
	return config
}

func TestSmoke(t *testing.T) {
	if v := api.ApiVersion(); v < 6 {
		t.Fatalf("api_version = %d", v)
	}

	// 1. Errors cross with their numeric code.
	short := []byte{1, 2, 3}
	_, err := sdk.ClientOpen(clientConfig(api.NetworkDirect, &short))
	if !errors.Is(err, api.ErrApiErrorInvalidInput) {
		t.Fatalf("short secret: want InvalidInput, got %v", err)
	}
	code := api.ApiErrorCode(asAPI(t, err))
	// uniffi-bindgen-go v0.7.1 reads enums with explicit discriminants by
	// variant index; patches/uniffi-bindgen-go-enum-discr.patch fixes it.
	if uint32(code) != 100 || code != api.ErrorCodeInvalidInput {
		t.Fatalf("short secret code: numeric code=%d Go value=%d, want 100", code, uint32(code))
	}
	t.Logf("ok: short secret error code = %d (Go value %d)", code, uint32(code))

	secret := []byte(strings.Repeat("\x07", 32))
	_, err = sdk.ClientOpen(clientConfig(api.NetworkTor, &secret))
	if code := api.ApiErrorCode(asAPI(t, err)); code != api.ErrorCodeUnsupported || uint32(code) != 103 {
		t.Fatalf("Tor code = %d (Go value %d), want 103", code, uint32(code))
	}
	t.Logf("ok: Tor error code = 103")

	_, err = sdk.ClientOpen(clientConfig(api.NetworkLan, nil))
	if n := api.ApiErrorCode(asAPI(t, err)); n != 100 {
		t.Fatalf("LAN without secret code = %d, want 100 (%v)", n, err)
	}
	t.Logf("ok: core error code = 100 (%v)", err)

	// 2. open -> describe -> state.
	client, err := sdk.ClientOpen(clientConfig(api.NetworkDirect, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Destroy()
	endpoint, err := client.Endpoint()
	if err != nil || len(endpoint.EndpointKey) != 64 {
		t.Fatalf("describe: %+v %v", endpoint, err)
	}
	t.Logf("ok: describe endpoint_id = %s", endpoint.EndpointKey)
	state, err := client.WorkspaceState()
	if err != nil || state.Phase != sdk.PhaseEmpty || state.Workspace != nil {
		t.Fatalf("state: %+v %v", state, err)
	}
	t.Logf("ok: state phase = Empty")

	// 3. NextEvent with a timeout returns nil after about the timeout.
	t0 := time.Now()
	shortWait := 200 * time.Millisecond
	parkWait := 30 * time.Second
	ev, err := client.NextEvent(&shortWait)
	waited := time.Since(t0)
	if err != nil || ev != nil || waited < 150*time.Millisecond || waited > 5*time.Second {
		t.Fatalf("NextEvent(200): %v %v after %v", ev, err, waited)
	}
	t.Logf("ok: NextEvent(200) returned nil after %d ms", waited.Milliseconds())

	// 4. Wake() releases a parked NextEvent.
	woke := make(chan *api.Event, 1)
	go func() { ev, _ := client.NextEvent(&parkWait); woke <- ev }()
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
		ev      *api.Event
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		ev, err := client.NextEvent(&parkWait)
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
	_, err = client.Endpoint()
	if !errors.Is(err, api.ErrApiErrorClosed) || api.ApiErrorCode(asAPI(t, err)) != 1 {
		t.Fatalf("after close: %v", err)
	}
	t.Logf("ok: after close error code = 1")
	t.Logf("GO PASS")
}
