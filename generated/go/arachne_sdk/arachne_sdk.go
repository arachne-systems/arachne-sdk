package arachne_sdk

// #include <arachne_sdk.h>
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync/atomic"
	"unsafe"
)

// This is needed, because as of go 1.24
// type RustBuffer C.RustBuffer cannot have methods,
// RustBuffer is treated as non-local type
type GoRustBuffer struct {
	inner C.RustBuffer
}

type RustBufferI interface {
	AsReader() *bytes.Reader
	Free()
	ToGoBytes() []byte
	Data() unsafe.Pointer
	Len() uint64
	Capacity() uint64
}

// C.RustBuffer fields exposed as an interface so they can be accessed in different Go packages.
// See https://github.com/golang/go/issues/13467
type ExternalCRustBuffer interface {
	Data() unsafe.Pointer
	Len() uint64
	Capacity() uint64
}

func RustBufferFromC(b C.RustBuffer) ExternalCRustBuffer {
	return GoRustBuffer{
		inner: b,
	}
}

func CFromRustBuffer(b ExternalCRustBuffer) C.RustBuffer {
	return C.RustBuffer{
		capacity: C.uint64_t(b.Capacity()),
		len:      C.uint64_t(b.Len()),
		data:     (*C.uchar)(b.Data()),
	}
}

func RustBufferFromExternal(b ExternalCRustBuffer) GoRustBuffer {
	return GoRustBuffer{
		inner: C.RustBuffer{
			capacity: C.uint64_t(b.Capacity()),
			len:      C.uint64_t(b.Len()),
			data:     (*C.uchar)(b.Data()),
		},
	}
}

func (cb GoRustBuffer) Capacity() uint64 {
	return uint64(cb.inner.capacity)
}

func (cb GoRustBuffer) Len() uint64 {
	return uint64(cb.inner.len)
}

func (cb GoRustBuffer) Data() unsafe.Pointer {
	return unsafe.Pointer(cb.inner.data)
}

func (cb GoRustBuffer) AsReader() *bytes.Reader {
	b := unsafe.Slice((*byte)(cb.inner.data), C.uint64_t(cb.inner.len))
	return bytes.NewReader(b)
}

func (cb GoRustBuffer) Free() {
	rustCall(func(status *C.RustCallStatus) bool {
		C.ffi_arachne_sdk_rustbuffer_free(cb.inner, status)
		return false
	})
}

func (cb GoRustBuffer) ToGoBytes() []byte {
	return C.GoBytes(unsafe.Pointer(cb.inner.data), C.int(cb.inner.len))
}

func stringToRustBuffer(str string) C.RustBuffer {
	return bytesToRustBuffer([]byte(str))
}

func bytesToRustBuffer(b []byte) C.RustBuffer {
	if len(b) == 0 {
		return C.RustBuffer{}
	}
	// We can pass the pointer along here, as it is pinned
	// for the duration of this call
	foreign := C.ForeignBytes{
		len:  C.int(len(b)),
		data: (*C.uchar)(unsafe.Pointer(&b[0])),
	}

	return rustCall(func(status *C.RustCallStatus) C.RustBuffer {
		return C.ffi_arachne_sdk_rustbuffer_from_bytes(foreign, status)
	})
}

type BufLifter[GoType any] interface {
	Lift(value RustBufferI) GoType
}

type BufLowerer[GoType any] interface {
	Lower(value GoType) C.RustBuffer
}

type BufReader[GoType any] interface {
	Read(reader io.Reader) GoType
}

type BufWriter[GoType any] interface {
	Write(writer io.Writer, value GoType)
}

func LowerIntoRustBuffer[GoType any](bufWriter BufWriter[GoType], value GoType) C.RustBuffer {
	// This might be not the most efficient way but it does not require knowing allocation size
	// beforehand
	var buffer bytes.Buffer
	bufWriter.Write(&buffer, value)

	bytes, err := io.ReadAll(&buffer)
	if err != nil {
		panic(fmt.Errorf("reading written data: %w", err))
	}
	return bytesToRustBuffer(bytes)
}

func LiftFromRustBuffer[GoType any](bufReader BufReader[GoType], rbuf RustBufferI) GoType {
	defer rbuf.Free()
	reader := rbuf.AsReader()
	item := bufReader.Read(reader)
	if reader.Len() > 0 {
		// TODO: Remove this
		leftover, _ := io.ReadAll(reader)
		panic(fmt.Errorf("Junk remaining in buffer after lifting: %s", string(leftover)))
	}
	return item
}

func rustCallWithError[E any, U any](converter BufReader[E], callback func(*C.RustCallStatus) U) (U, E) {
	var status C.RustCallStatus
	returnValue := callback(&status)
	err := checkCallStatus(converter, status)
	return returnValue, err
}

func checkCallStatus[E any](converter BufReader[E], status C.RustCallStatus) E {
	switch status.code {
	case 0:
		var zero E
		return zero
	case 1:
		return LiftFromRustBuffer(converter, GoRustBuffer{inner: status.errorBuf})
	case 2:
		// when the rust code sees a panic, it tries to construct a rustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{inner: status.errorBuf})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		panic(fmt.Errorf("unknown status code: %d", status.code))
	}
}

func checkCallStatusUnknown(status C.RustCallStatus) error {
	switch status.code {
	case 0:
		return nil
	case 1:
		panic(fmt.Errorf("function not returning an error returned an error"))
	case 2:
		// when the rust code sees a panic, it tries to construct a C.RustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{
				inner: status.errorBuf,
			})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		return fmt.Errorf("unknown status code: %d", status.code)
	}
}

func rustCall[U any](callback func(*C.RustCallStatus) U) U {
	returnValue, err := rustCallWithError[error](nil, callback)
	if err != nil {
		panic(err)
	}
	return returnValue
}

type NativeError interface {
	AsError() error
}

func writeInt8(writer io.Writer, value int8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint8(writer io.Writer, value uint8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt16(writer io.Writer, value int16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint16(writer io.Writer, value uint16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt32(writer io.Writer, value int32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint32(writer io.Writer, value uint32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt64(writer io.Writer, value int64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint64(writer io.Writer, value uint64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat32(writer io.Writer, value float32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat64(writer io.Writer, value float64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func readInt8(reader io.Reader) int8 {
	var result int8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint8(reader io.Reader) uint8 {
	var result uint8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt16(reader io.Reader) int16 {
	var result int16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint16(reader io.Reader) uint16 {
	var result uint16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt32(reader io.Reader) int32 {
	var result int32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint32(reader io.Reader) uint32 {
	var result uint32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt64(reader io.Reader) int64 {
	var result int64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint64(reader io.Reader) uint64 {
	var result uint64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat32(reader io.Reader) float32 {
	var result float32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat64(reader io.Reader) float64 {
	var result float64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func init() {

	uniffiCheckChecksums()
}

func uniffiCheckChecksums() {
	// Get the bindings contract version from our ComponentInterface
	bindingsContractVersion := 30
	// Get the scaffolding contract version by calling the into the dylib
	scaffoldingContractVersion := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.ffi_arachne_sdk_uniffi_contract_version()
	})
	if bindingsContractVersion != int(scaffoldingContractVersion) {
		// If this happens try cleaning and rebuilding your project
		panic("arachne_sdk: UniFFI contract version mismatch")
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_func_api_error_code()
		})
		if checksum != 46887 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_func_api_error_code: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_func_api_version()
		})
		if checksum != 5616 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_func_api_version: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_close()
		})
		if checksum != 1634 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_close: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_describe()
		})
		if checksum != 64790 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_describe: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_next_event()
		})
		if checksum != 920 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_next_event: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_state()
		})
		if checksum != 29322 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_state: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_wait_for_work()
		})
		if checksum != 53847 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_wait_for_work: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_wake()
		})
		if checksum != 58561 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_wake: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_constructor_client_open()
		})
		if checksum != 13811 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_constructor_client_open: UniFFI API checksum mismatch")
		}
	}
}

type FfiConverterUint32 struct{}

var FfiConverterUint32INSTANCE = FfiConverterUint32{}

func (FfiConverterUint32) Lower(value uint32) C.uint32_t {
	return C.uint32_t(value)
}

func (FfiConverterUint32) Write(writer io.Writer, value uint32) {
	writeUint32(writer, value)
}

func (FfiConverterUint32) Lift(value C.uint32_t) uint32 {
	return uint32(value)
}

func (FfiConverterUint32) Read(reader io.Reader) uint32 {
	return readUint32(reader)
}

type FfiDestroyerUint32 struct{}

func (FfiDestroyerUint32) Destroy(_ uint32) {}

type FfiConverterUint64 struct{}

var FfiConverterUint64INSTANCE = FfiConverterUint64{}

func (FfiConverterUint64) Lower(value uint64) C.uint64_t {
	return C.uint64_t(value)
}

func (FfiConverterUint64) Write(writer io.Writer, value uint64) {
	writeUint64(writer, value)
}

func (FfiConverterUint64) Lift(value C.uint64_t) uint64 {
	return uint64(value)
}

func (FfiConverterUint64) Read(reader io.Reader) uint64 {
	return readUint64(reader)
}

type FfiDestroyerUint64 struct{}

func (FfiDestroyerUint64) Destroy(_ uint64) {}

type FfiConverterBool struct{}

var FfiConverterBoolINSTANCE = FfiConverterBool{}

func (FfiConverterBool) Lower(value bool) C.int8_t {
	if value {
		return C.int8_t(1)
	}
	return C.int8_t(0)
}

func (FfiConverterBool) Write(writer io.Writer, value bool) {
	if value {
		writeInt8(writer, 1)
	} else {
		writeInt8(writer, 0)
	}
}

func (FfiConverterBool) Lift(value C.int8_t) bool {
	return value != 0
}

func (FfiConverterBool) Read(reader io.Reader) bool {
	return readInt8(reader) != 0
}

type FfiDestroyerBool struct{}

func (FfiDestroyerBool) Destroy(_ bool) {}

type FfiConverterString struct{}

var FfiConverterStringINSTANCE = FfiConverterString{}

func (FfiConverterString) Lift(rb RustBufferI) string {
	defer rb.Free()
	reader := rb.AsReader()
	b, err := io.ReadAll(reader)
	if err != nil {
		panic(fmt.Errorf("reading reader: %w", err))
	}
	return string(b)
}

func (FfiConverterString) Read(reader io.Reader) string {
	length := readInt32(reader)
	buffer := make([]byte, length)
	read_length, err := reader.Read(buffer)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if read_length != int(length) {
		panic(fmt.Errorf("bad read length when reading string, expected %d, read %d", length, read_length))
	}
	return string(buffer)
}

func (FfiConverterString) Lower(value string) C.RustBuffer {
	return stringToRustBuffer(value)
}

func (c FfiConverterString) LowerExternal(value string) ExternalCRustBuffer {
	return RustBufferFromC(stringToRustBuffer(value))
}

func (FfiConverterString) Write(writer io.Writer, value string) {
	if len(value) > math.MaxInt32 {
		panic("String is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	write_length, err := io.WriteString(writer, value)
	if err != nil {
		panic(err)
	}
	if write_length != len(value) {
		panic(fmt.Errorf("bad write length when writing string, expected %d, written %d", len(value), write_length))
	}
}

type FfiDestroyerString struct{}

func (FfiDestroyerString) Destroy(_ string) {}

type FfiConverterBytes struct{}

var FfiConverterBytesINSTANCE = FfiConverterBytes{}

func (c FfiConverterBytes) Lower(value []byte) C.RustBuffer {
	return LowerIntoRustBuffer[[]byte](c, value)
}

func (c FfiConverterBytes) LowerExternal(value []byte) ExternalCRustBuffer {
	return RustBufferFromC(c.Lower(value))
}

func (c FfiConverterBytes) Write(writer io.Writer, value []byte) {
	if len(value) > math.MaxInt32 {
		panic("[]byte is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	write_length, err := writer.Write(value)
	if err != nil {
		panic(err)
	}
	if write_length != len(value) {
		panic(fmt.Errorf("bad write length when writing []byte, expected %d, written %d", len(value), write_length))
	}
}

func (c FfiConverterBytes) Lift(rb RustBufferI) []byte {
	return LiftFromRustBuffer[[]byte](c, rb)
}

func (c FfiConverterBytes) Read(reader io.Reader) []byte {
	length := readInt32(reader)
	buffer := make([]byte, length)
	read_length, err := reader.Read(buffer)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if read_length != int(length) {
		panic(fmt.Errorf("bad read length when reading []byte, expected %d, read %d", length, read_length))
	}
	return buffer
}

type FfiDestroyerBytes struct{}

func (FfiDestroyerBytes) Destroy(_ []byte) {}

// Below is an implementation of synchronization requirements outlined in the link.
// https://github.com/mozilla/uniffi-rs/blob/0dc031132d9493ca812c3af6e7dd60ad2ea95bf0/uniffi_bindgen/src/bindings/kotlin/templates/ObjectRuntime.kt#L31

type FfiObject struct {
	handle        C.uint64_t
	callCounter   atomic.Int64
	cloneFunction func(C.uint64_t, *C.RustCallStatus) C.uint64_t
	freeFunction  func(C.uint64_t, *C.RustCallStatus)
	destroyed     atomic.Bool
}

func newFfiObject(
	handle C.uint64_t,
	cloneFunction func(C.uint64_t, *C.RustCallStatus) C.uint64_t,
	freeFunction func(C.uint64_t, *C.RustCallStatus),
) FfiObject {
	return FfiObject{
		handle:        handle,
		cloneFunction: cloneFunction,
		freeFunction:  freeFunction,
	}
}

func (ffiObject *FfiObject) incrementPointer(debugName string) C.uint64_t {
	for {
		counter := ffiObject.callCounter.Load()
		if counter <= -1 {
			panic(fmt.Errorf("%v object has already been destroyed", debugName))
		}
		if counter == math.MaxInt64 {
			panic(fmt.Errorf("%v object call counter would overflow", debugName))
		}
		if ffiObject.callCounter.CompareAndSwap(counter, counter+1) {
			break
		}
	}

	return rustCall(func(status *C.RustCallStatus) C.uint64_t {
		return ffiObject.cloneFunction(ffiObject.handle, status)
	})
}

func (ffiObject *FfiObject) decrementPointer() {
	if ffiObject.callCounter.Add(-1) == -1 {
		ffiObject.freeRustArcPtr()
	}
}

func (ffiObject *FfiObject) destroy() {
	if ffiObject.destroyed.CompareAndSwap(false, true) {
		if ffiObject.callCounter.Add(-1) == -1 {
			ffiObject.freeRustArcPtr()
		}
	}
}

func (ffiObject *FfiObject) freeRustArcPtr() {
	if ffiObject.handle == 0 {
		return
	}
	rustCall(func(status *C.RustCallStatus) int32 {
		ffiObject.freeFunction(ffiObject.handle, status)
		return 0
	})
}

// One Arachne session. All methods block; call them from a worker thread.
// `close`, `wake`, `wait_for_work` and `next_event` may run on any thread
// while another thread waits: no lock is held while a call waits.
type ClientInterface interface {
	// Close the session. Idempotent, from any thread. It releases every
	// waiter; later calls fail with `Closed`. Kotlin names it `shutdown`.
	Close() error
	// The bound endpoint.
	Describe() (EndpointInfo, error)
	// The next event, waiting up to `timeout_ms`. `None`: the timeout
	// passed, `wake` was called, or the client closed while it waited.
	// After `close` it fails with `Closed`.
	NextEvent(timeoutMs uint64) (*Event, error)
	// The workspace state.
	State() (WorkspaceState, error)
	// Wait up to `timeout_ms` for work. `true`: drain the queues, then call
	// again. `false`: the timeout passed, `wake` was called, or the client
	// closed.
	WaitForWork(timeoutMs uint64) (bool, error)
	// Release one waiter (`next_event` or `wait_for_work`) without work.
	Wake() error
}

// One Arachne session. All methods block; call them from a worker thread.
// `close`, `wake`, `wait_for_work` and `next_event` may run on any thread
// while another thread waits: no lock is held while a call waits.
type Client struct {
	ffiObject FfiObject
}

// Open a client in the process default context.
func ClientOpen(config ClientConfig) (*Client, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_constructor_client_open(FfiConverterClientConfigINSTANCE.Lower(config), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Client
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterClientINSTANCE.Lift(_uniffiRV), nil
	}
}

// Close the session. Idempotent, from any thread. It releases every
// waiter; later calls fail with `Closed`. Kotlin names it `shutdown`.
func (_self *Client) Close() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_close(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// The bound endpoint.
func (_self *Client) Describe() (EndpointInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_describe(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue EndpointInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterEndpointInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

// The next event, waiting up to `timeout_ms`. `None`: the timeout
// passed, `wake` was called, or the client closed while it waited.
// After `close` it fails with `Closed`.
func (_self *Client) NextEvent(timeoutMs uint64) (*Event, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_next_event(
				_pointer, FfiConverterUint64INSTANCE.Lower(timeoutMs), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Event
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalEventINSTANCE.Lift(_uniffiRV), nil
	}
}

// The workspace state.
func (_self *Client) State() (WorkspaceState, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_state(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceState
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceStateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Wait up to `timeout_ms` for work. `true`: drain the queues, then call
// again. `false`: the timeout passed, `wake` was called, or the client
// closed.
func (_self *Client) WaitForWork(timeoutMs uint64) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_client_wait_for_work(
			_pointer, FfiConverterUint64INSTANCE.Lower(timeoutMs), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Release one waiter (`next_event` or `wait_for_work`) without work.
func (_self *Client) Wake() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_wake(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}
func (object *Client) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterClient struct{}

var FfiConverterClientINSTANCE = FfiConverterClient{}

func (c FfiConverterClient) Lift(handle C.uint64_t) *Client {
	result := &Client{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_client(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_client(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Client).Destroy)
	return result
}

func (c FfiConverterClient) Read(reader io.Reader) *Client {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterClient) Lower(value *Client) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Client")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterClient) Write(writer io.Writer, value *Client) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalClient(handle uint64) *Client {
	return FfiConverterClientINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalClient(value *Client) uint64 {
	return uint64(FfiConverterClientINSTANCE.Lower(value))
}

type FfiDestroyerClient struct{}

func (_ FfiDestroyerClient) Destroy(value *Client) {
	value.Destroy()
}

// How to open a client.
type ClientConfig struct {
	Network Network
	// A 32-byte endpoint secret. Only `Direct` accepts none (an ephemeral
	// endpoint).
	Secret *[]byte
	// Per-op deadline in milliseconds for blocking ops and for the bind.
	DeadlineMs *uint64
}

func (r *ClientConfig) Destroy() {
	FfiDestroyerNetwork{}.Destroy(r.Network)
	FfiDestroyerOptionalBytes{}.Destroy(r.Secret)
	FfiDestroyerOptionalUint64{}.Destroy(r.DeadlineMs)
}

type FfiConverterClientConfig struct{}

var FfiConverterClientConfigINSTANCE = FfiConverterClientConfig{}

func (c FfiConverterClientConfig) Lift(rb RustBufferI) ClientConfig {
	return LiftFromRustBuffer[ClientConfig](c, rb)
}

func (c FfiConverterClientConfig) Read(reader io.Reader) ClientConfig {
	return ClientConfig{
		FfiConverterNetworkINSTANCE.Read(reader),
		FfiConverterOptionalBytesINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterClientConfig) Lower(value ClientConfig) C.RustBuffer {
	return LowerIntoRustBuffer[ClientConfig](c, value)
}

func (c FfiConverterClientConfig) LowerExternal(value ClientConfig) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ClientConfig](c, value))
}

func (c FfiConverterClientConfig) Write(writer io.Writer, value ClientConfig) {
	FfiConverterNetworkINSTANCE.Write(writer, value.Network)
	FfiConverterOptionalBytesINSTANCE.Write(writer, value.Secret)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.DeadlineMs)
}

type FfiDestroyerClientConfig struct{}

func (_ FfiDestroyerClientConfig) Destroy(value ClientConfig) {
	value.Destroy()
}

// The bound endpoint of a client.
type EndpointInfo struct {
	EndpointId     EndpointId
	BoundAddress   string
	WorkspaceReady bool
}

func (r *EndpointInfo) Destroy() {
	FfiDestroyerTypeEndpointId{}.Destroy(r.EndpointId)
	FfiDestroyerString{}.Destroy(r.BoundAddress)
	FfiDestroyerBool{}.Destroy(r.WorkspaceReady)
}

type FfiConverterEndpointInfo struct{}

var FfiConverterEndpointInfoINSTANCE = FfiConverterEndpointInfo{}

func (c FfiConverterEndpointInfo) Lift(rb RustBufferI) EndpointInfo {
	return LiftFromRustBuffer[EndpointInfo](c, rb)
}

func (c FfiConverterEndpointInfo) Read(reader io.Reader) EndpointInfo {
	return EndpointInfo{
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterEndpointInfo) Lower(value EndpointInfo) C.RustBuffer {
	return LowerIntoRustBuffer[EndpointInfo](c, value)
}

func (c FfiConverterEndpointInfo) LowerExternal(value EndpointInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[EndpointInfo](c, value))
}

func (c FfiConverterEndpointInfo) Write(writer io.Writer, value EndpointInfo) {
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.EndpointId)
	FfiConverterStringINSTANCE.Write(writer, value.BoundAddress)
	FfiConverterBoolINSTANCE.Write(writer, value.WorkspaceReady)
}

type FfiDestroyerEndpointInfo struct{}

func (_ FfiDestroyerEndpointInfo) Destroy(value EndpointInfo) {
	value.Destroy()
}

// The workspace state of a client.
type WorkspaceState struct {
	EndpointId     EndpointId
	Workspace      *WorkspaceId
	WorkspaceReady bool
	Durable        bool
	Phase          WorkspacePhase
	Reason         *string
}

func (r *WorkspaceState) Destroy() {
	FfiDestroyerTypeEndpointId{}.Destroy(r.EndpointId)
	FfiDestroyerOptionalTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerBool{}.Destroy(r.WorkspaceReady)
	FfiDestroyerBool{}.Destroy(r.Durable)
	FfiDestroyerWorkspacePhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
}

type FfiConverterWorkspaceState struct{}

var FfiConverterWorkspaceStateINSTANCE = FfiConverterWorkspaceState{}

func (c FfiConverterWorkspaceState) Lift(rb RustBufferI) WorkspaceState {
	return LiftFromRustBuffer[WorkspaceState](c, rb)
}

func (c FfiConverterWorkspaceState) Read(reader io.Reader) WorkspaceState {
	return WorkspaceState{
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterWorkspacePhaseINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterWorkspaceState) Lower(value WorkspaceState) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspaceState](c, value)
}

func (c FfiConverterWorkspaceState) LowerExternal(value WorkspaceState) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspaceState](c, value))
}

func (c FfiConverterWorkspaceState) Write(writer io.Writer, value WorkspaceState) {
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.EndpointId)
	FfiConverterOptionalTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterBoolINSTANCE.Write(writer, value.WorkspaceReady)
	FfiConverterBoolINSTANCE.Write(writer, value.Durable)
	FfiConverterWorkspacePhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
}

type FfiDestroyerWorkspaceState struct{}

func (_ FfiDestroyerWorkspaceState) Destroy(value WorkspaceState) {
	value.Destroy()
}

// The error of every SDK call. Programs read `api_error_code(error)`; the
// text is for people and never holds secrets.
type ApiError struct {
	err error
}

// Convenience method to turn *ApiError into error
// Avoiding treating nil pointer as non nil error interface
func (err *ApiError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err ApiError) Error() string {
	return fmt.Sprintf("ApiError: %s", err.err.Error())
}

func (err ApiError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrApiErrorClosed = fmt.Errorf("ApiErrorClosed")
var ErrApiErrorCancelled = fmt.Errorf("ApiErrorCancelled")
var ErrApiErrorDeadlineExceeded = fmt.Errorf("ApiErrorDeadlineExceeded")
var ErrApiErrorInvalidInput = fmt.Errorf("ApiErrorInvalidInput")
var ErrApiErrorInvalidId = fmt.Errorf("ApiErrorInvalidId")
var ErrApiErrorCapacityExceeded = fmt.Errorf("ApiErrorCapacityExceeded")
var ErrApiErrorLimitReached = fmt.Errorf("ApiErrorLimitReached")
var ErrApiErrorStorage = fmt.Errorf("ApiErrorStorage")
var ErrApiErrorTransport = fmt.Errorf("ApiErrorTransport")
var ErrApiErrorAuthorization = fmt.Errorf("ApiErrorAuthorization")
var ErrApiErrorState = fmt.Errorf("ApiErrorState")
var ErrApiErrorInternal = fmt.Errorf("ApiErrorInternal")

// Variant structs
type ApiErrorClosed struct {
}

func NewApiErrorClosed() *ApiError {
	return &ApiError{err: &ApiErrorClosed{}}
}

func (e ApiErrorClosed) destroy() {
}

func (err ApiErrorClosed) Error() string {
	return fmt.Sprint("Closed")
}

func (self ApiErrorClosed) Is(target error) bool {
	return target == ErrApiErrorClosed
}

type ApiErrorCancelled struct {
}

func NewApiErrorCancelled() *ApiError {
	return &ApiError{err: &ApiErrorCancelled{}}
}

func (e ApiErrorCancelled) destroy() {
}

func (err ApiErrorCancelled) Error() string {
	return fmt.Sprint("Cancelled")
}

func (self ApiErrorCancelled) Is(target error) bool {
	return target == ErrApiErrorCancelled
}

type ApiErrorDeadlineExceeded struct {
}

func NewApiErrorDeadlineExceeded() *ApiError {
	return &ApiError{err: &ApiErrorDeadlineExceeded{}}
}

func (e ApiErrorDeadlineExceeded) destroy() {
}

func (err ApiErrorDeadlineExceeded) Error() string {
	return fmt.Sprint("DeadlineExceeded")
}

func (self ApiErrorDeadlineExceeded) Is(target error) bool {
	return target == ErrApiErrorDeadlineExceeded
}

type ApiErrorInvalidInput struct {
	Field  string
	Reason string
}

func NewApiErrorInvalidInput(
	field string,
	reason string,
) *ApiError {
	return &ApiError{err: &ApiErrorInvalidInput{
		Field:  field,
		Reason: reason}}
}

func (e ApiErrorInvalidInput) destroy() {
	FfiDestroyerString{}.Destroy(e.Field)
	FfiDestroyerString{}.Destroy(e.Reason)
}

func (err ApiErrorInvalidInput) Error() string {
	return fmt.Sprint("InvalidInput",
		": ",

		"Field=",
		err.Field,
		", ",
		"Reason=",
		err.Reason,
	)
}

func (self ApiErrorInvalidInput) Is(target error) bool {
	return target == ErrApiErrorInvalidInput
}

type ApiErrorInvalidId struct {
	Kind   string
	Reason string
}

func NewApiErrorInvalidId(
	kind string,
	reason string,
) *ApiError {
	return &ApiError{err: &ApiErrorInvalidId{
		Kind:   kind,
		Reason: reason}}
}

func (e ApiErrorInvalidId) destroy() {
	FfiDestroyerString{}.Destroy(e.Kind)
	FfiDestroyerString{}.Destroy(e.Reason)
}

func (err ApiErrorInvalidId) Error() string {
	return fmt.Sprint("InvalidId",
		": ",

		"Kind=",
		err.Kind,
		", ",
		"Reason=",
		err.Reason,
	)
}

func (self ApiErrorInvalidId) Is(target error) bool {
	return target == ErrApiErrorInvalidId
}

type ApiErrorCapacityExceeded struct {
	Resource string
	Limit    uint64
	Detail   string
}

func NewApiErrorCapacityExceeded(
	resource string,
	limit uint64,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorCapacityExceeded{
		Resource: resource,
		Limit:    limit,
		Detail:   detail}}
}

func (e ApiErrorCapacityExceeded) destroy() {
	FfiDestroyerString{}.Destroy(e.Resource)
	FfiDestroyerUint64{}.Destroy(e.Limit)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorCapacityExceeded) Error() string {
	return fmt.Sprint("CapacityExceeded",
		": ",

		"Resource=",
		err.Resource,
		", ",
		"Limit=",
		err.Limit,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorCapacityExceeded) Is(target error) bool {
	return target == ErrApiErrorCapacityExceeded
}

type ApiErrorLimitReached struct {
	Resource string
	Limit    uint64
	Detail   string
}

func NewApiErrorLimitReached(
	resource string,
	limit uint64,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorLimitReached{
		Resource: resource,
		Limit:    limit,
		Detail:   detail}}
}

func (e ApiErrorLimitReached) destroy() {
	FfiDestroyerString{}.Destroy(e.Resource)
	FfiDestroyerUint64{}.Destroy(e.Limit)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorLimitReached) Error() string {
	return fmt.Sprint("LimitReached",
		": ",

		"Resource=",
		err.Resource,
		", ",
		"Limit=",
		err.Limit,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorLimitReached) Is(target error) bool {
	return target == ErrApiErrorLimitReached
}

type ApiErrorStorage struct {
	Code   ErrorCode
	Detail string
}

func NewApiErrorStorage(
	code ErrorCode,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorStorage{
		Code:   code,
		Detail: detail}}
}

func (e ApiErrorStorage) destroy() {
	FfiDestroyerErrorCode{}.Destroy(e.Code)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorStorage) Error() string {
	return fmt.Sprint("Storage",
		": ",

		"Code=",
		err.Code,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorStorage) Is(target error) bool {
	return target == ErrApiErrorStorage
}

type ApiErrorTransport struct {
	Code   ErrorCode
	Peer   *EndpointId
	Detail string
}

func NewApiErrorTransport(
	code ErrorCode,
	peer *EndpointId,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorTransport{
		Code:   code,
		Peer:   peer,
		Detail: detail}}
}

func (e ApiErrorTransport) destroy() {
	FfiDestroyerErrorCode{}.Destroy(e.Code)
	FfiDestroyerOptionalTypeEndpointId{}.Destroy(e.Peer)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorTransport) Error() string {
	return fmt.Sprint("Transport",
		": ",

		"Code=",
		err.Code,
		", ",
		"Peer=",
		err.Peer,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorTransport) Is(target error) bool {
	return target == ErrApiErrorTransport
}

type ApiErrorAuthorization struct {
	Code   ErrorCode
	Detail string
}

func NewApiErrorAuthorization(
	code ErrorCode,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorAuthorization{
		Code:   code,
		Detail: detail}}
}

func (e ApiErrorAuthorization) destroy() {
	FfiDestroyerErrorCode{}.Destroy(e.Code)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorAuthorization) Error() string {
	return fmt.Sprint("Authorization",
		": ",

		"Code=",
		err.Code,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorAuthorization) Is(target error) bool {
	return target == ErrApiErrorAuthorization
}

type ApiErrorState struct {
	Code   ErrorCode
	Detail string
}

func NewApiErrorState(
	code ErrorCode,
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorState{
		Code:   code,
		Detail: detail}}
}

func (e ApiErrorState) destroy() {
	FfiDestroyerErrorCode{}.Destroy(e.Code)
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorState) Error() string {
	return fmt.Sprint("State",
		": ",

		"Code=",
		err.Code,
		", ",
		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorState) Is(target error) bool {
	return target == ErrApiErrorState
}

type ApiErrorInternal struct {
	Detail string
}

func NewApiErrorInternal(
	detail string,
) *ApiError {
	return &ApiError{err: &ApiErrorInternal{
		Detail: detail}}
}

func (e ApiErrorInternal) destroy() {
	FfiDestroyerString{}.Destroy(e.Detail)
}

func (err ApiErrorInternal) Error() string {
	return fmt.Sprint("Internal",
		": ",

		"Detail=",
		err.Detail,
	)
}

func (self ApiErrorInternal) Is(target error) bool {
	return target == ErrApiErrorInternal
}

type FfiConverterApiError struct{}

var FfiConverterApiErrorINSTANCE = FfiConverterApiError{}

func (c FfiConverterApiError) Lift(eb RustBufferI) *ApiError {
	return LiftFromRustBuffer[*ApiError](c, eb)
}

func (c FfiConverterApiError) Lower(value *ApiError) C.RustBuffer {
	return LowerIntoRustBuffer[*ApiError](c, value)
}

func (c FfiConverterApiError) LowerExternal(value *ApiError) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*ApiError](c, value))
}

func (c FfiConverterApiError) Read(reader io.Reader) *ApiError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &ApiError{&ApiErrorClosed{}}
	case 2:
		return &ApiError{&ApiErrorCancelled{}}
	case 3:
		return &ApiError{&ApiErrorDeadlineExceeded{}}
	case 4:
		return &ApiError{&ApiErrorInvalidInput{
			Field:  FfiConverterStringINSTANCE.Read(reader),
			Reason: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 5:
		return &ApiError{&ApiErrorInvalidId{
			Kind:   FfiConverterStringINSTANCE.Read(reader),
			Reason: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 6:
		return &ApiError{&ApiErrorCapacityExceeded{
			Resource: FfiConverterStringINSTANCE.Read(reader),
			Limit:    FfiConverterUint64INSTANCE.Read(reader),
			Detail:   FfiConverterStringINSTANCE.Read(reader),
		}}
	case 7:
		return &ApiError{&ApiErrorLimitReached{
			Resource: FfiConverterStringINSTANCE.Read(reader),
			Limit:    FfiConverterUint64INSTANCE.Read(reader),
			Detail:   FfiConverterStringINSTANCE.Read(reader),
		}}
	case 8:
		return &ApiError{&ApiErrorStorage{
			Code:   FfiConverterErrorCodeINSTANCE.Read(reader),
			Detail: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 9:
		return &ApiError{&ApiErrorTransport{
			Code:   FfiConverterErrorCodeINSTANCE.Read(reader),
			Peer:   FfiConverterOptionalTypeEndpointIdINSTANCE.Read(reader),
			Detail: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 10:
		return &ApiError{&ApiErrorAuthorization{
			Code:   FfiConverterErrorCodeINSTANCE.Read(reader),
			Detail: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 11:
		return &ApiError{&ApiErrorState{
			Code:   FfiConverterErrorCodeINSTANCE.Read(reader),
			Detail: FfiConverterStringINSTANCE.Read(reader),
		}}
	case 12:
		return &ApiError{&ApiErrorInternal{
			Detail: FfiConverterStringINSTANCE.Read(reader),
		}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterApiError.Read()", errorID))
	}
}

func (c FfiConverterApiError) Write(writer io.Writer, value *ApiError) {
	switch variantValue := value.err.(type) {
	case *ApiErrorClosed:
		writeInt32(writer, 1)
	case *ApiErrorCancelled:
		writeInt32(writer, 2)
	case *ApiErrorDeadlineExceeded:
		writeInt32(writer, 3)
	case *ApiErrorInvalidInput:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Field)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Reason)
	case *ApiErrorInvalidId:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Kind)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Reason)
	case *ApiErrorCapacityExceeded:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Resource)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Limit)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorLimitReached:
		writeInt32(writer, 7)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Resource)
		FfiConverterUint64INSTANCE.Write(writer, variantValue.Limit)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorStorage:
		writeInt32(writer, 8)
		FfiConverterErrorCodeINSTANCE.Write(writer, variantValue.Code)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorTransport:
		writeInt32(writer, 9)
		FfiConverterErrorCodeINSTANCE.Write(writer, variantValue.Code)
		FfiConverterOptionalTypeEndpointIdINSTANCE.Write(writer, variantValue.Peer)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorAuthorization:
		writeInt32(writer, 10)
		FfiConverterErrorCodeINSTANCE.Write(writer, variantValue.Code)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorState:
		writeInt32(writer, 11)
		FfiConverterErrorCodeINSTANCE.Write(writer, variantValue.Code)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	case *ApiErrorInternal:
		writeInt32(writer, 12)
		FfiConverterStringINSTANCE.Write(writer, variantValue.Detail)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterApiError.Write", value))
	}
}

type FfiDestroyerApiError struct{}

func (_ FfiDestroyerApiError) Destroy(value *ApiError) {
	switch variantValue := value.err.(type) {
	case ApiErrorClosed:
		variantValue.destroy()
	case ApiErrorCancelled:
		variantValue.destroy()
	case ApiErrorDeadlineExceeded:
		variantValue.destroy()
	case ApiErrorInvalidInput:
		variantValue.destroy()
	case ApiErrorInvalidId:
		variantValue.destroy()
	case ApiErrorCapacityExceeded:
		variantValue.destroy()
	case ApiErrorLimitReached:
		variantValue.destroy()
	case ApiErrorStorage:
		variantValue.destroy()
	case ApiErrorTransport:
		variantValue.destroy()
	case ApiErrorAuthorization:
		variantValue.destroy()
	case ApiErrorState:
		variantValue.destroy()
	case ApiErrorInternal:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerApiError.Destroy", value))
	}
}

// A stable numeric error code. The discriminant is the number; see
// `arachne_api::ErrorCode` for the ranges and rules.
type ErrorCode uint32

const (
	ErrorCodeClosed            ErrorCode = 1
	ErrorCodeCancelled         ErrorCode = 2
	ErrorCodeDeadlineExceeded  ErrorCode = 3
	ErrorCodeInvalidInput      ErrorCode = 100
	ErrorCodeInvalidId         ErrorCode = 101
	ErrorCodeWrongState        ErrorCode = 102
	ErrorCodeUnsupported       ErrorCode = 103
	ErrorCodeCapacityExceeded  ErrorCode = 200
	ErrorCodeLimitReached      ErrorCode = 201
	ErrorCodeStorageFailed     ErrorCode = 300
	ErrorCodeStorageCorrupt    ErrorCode = 301
	ErrorCodeCandidateStale    ErrorCode = 302
	ErrorCodePeerUnreachable   ErrorCode = 400
	ErrorCodeTimeout           ErrorCode = 401
	ErrorCodeTransportFailed   ErrorCode = 402
	ErrorCodeNotAuthorized     ErrorCode = 500
	ErrorCodeInvitationInvalid ErrorCode = 501
	ErrorCodeInvitationExpired ErrorCode = 502
	ErrorCodeNotMember         ErrorCode = 503
	ErrorCodeEpochMismatch     ErrorCode = 600
	ErrorCodePolicyMismatch    ErrorCode = 601
	ErrorCodeInternal          ErrorCode = 900
)

// The stable number of this code.
func (_self ErrorCode) Number() uint32 {
	_selfBuf := FfiConverterErrorCodeINSTANCE.Lower(_self)

	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_arachne_sdk_fn_method_errorcode_number(
			_selfBuf, _uniffiStatus)
	}))

}

type FfiConverterErrorCode struct{}

var FfiConverterErrorCodeINSTANCE = FfiConverterErrorCode{}

func (c FfiConverterErrorCode) Lift(rb RustBufferI) ErrorCode {
	return LiftFromRustBuffer[ErrorCode](c, rb)
}

func (c FfiConverterErrorCode) Lower(value ErrorCode) C.RustBuffer {
	return LowerIntoRustBuffer[ErrorCode](c, value)
}

func (c FfiConverterErrorCode) LowerExternal(value ErrorCode) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ErrorCode](c, value))
}

// The wire value is the 1-based variant index, not the discriminant.
func (FfiConverterErrorCode) Read(reader io.Reader) ErrorCode {
	id := readInt32(reader)
	switch id {
	case 1:
		return ErrorCodeClosed
	case 2:
		return ErrorCodeCancelled
	case 3:
		return ErrorCodeDeadlineExceeded
	case 4:
		return ErrorCodeInvalidInput
	case 5:
		return ErrorCodeInvalidId
	case 6:
		return ErrorCodeWrongState
	case 7:
		return ErrorCodeUnsupported
	case 8:
		return ErrorCodeCapacityExceeded
	case 9:
		return ErrorCodeLimitReached
	case 10:
		return ErrorCodeStorageFailed
	case 11:
		return ErrorCodeStorageCorrupt
	case 12:
		return ErrorCodeCandidateStale
	case 13:
		return ErrorCodePeerUnreachable
	case 14:
		return ErrorCodeTimeout
	case 15:
		return ErrorCodeTransportFailed
	case 16:
		return ErrorCodeNotAuthorized
	case 17:
		return ErrorCodeInvitationInvalid
	case 18:
		return ErrorCodeInvitationExpired
	case 19:
		return ErrorCodeNotMember
	case 20:
		return ErrorCodeEpochMismatch
	case 21:
		return ErrorCodePolicyMismatch
	case 22:
		return ErrorCodeInternal
	default:
		panic(fmt.Sprintf("invalid enum value, %v, in FfiConverterErrorCode.Read()", id))
	}
}

func (FfiConverterErrorCode) Write(writer io.Writer, value ErrorCode) {
	switch value {
	case ErrorCodeClosed:
		writeInt32(writer, 1)
	case ErrorCodeCancelled:
		writeInt32(writer, 2)
	case ErrorCodeDeadlineExceeded:
		writeInt32(writer, 3)
	case ErrorCodeInvalidInput:
		writeInt32(writer, 4)
	case ErrorCodeInvalidId:
		writeInt32(writer, 5)
	case ErrorCodeWrongState:
		writeInt32(writer, 6)
	case ErrorCodeUnsupported:
		writeInt32(writer, 7)
	case ErrorCodeCapacityExceeded:
		writeInt32(writer, 8)
	case ErrorCodeLimitReached:
		writeInt32(writer, 9)
	case ErrorCodeStorageFailed:
		writeInt32(writer, 10)
	case ErrorCodeStorageCorrupt:
		writeInt32(writer, 11)
	case ErrorCodeCandidateStale:
		writeInt32(writer, 12)
	case ErrorCodePeerUnreachable:
		writeInt32(writer, 13)
	case ErrorCodeTimeout:
		writeInt32(writer, 14)
	case ErrorCodeTransportFailed:
		writeInt32(writer, 15)
	case ErrorCodeNotAuthorized:
		writeInt32(writer, 16)
	case ErrorCodeInvitationInvalid:
		writeInt32(writer, 17)
	case ErrorCodeInvitationExpired:
		writeInt32(writer, 18)
	case ErrorCodeNotMember:
		writeInt32(writer, 19)
	case ErrorCodeEpochMismatch:
		writeInt32(writer, 20)
	case ErrorCodePolicyMismatch:
		writeInt32(writer, 21)
	case ErrorCodeInternal:
		writeInt32(writer, 22)
	default:
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterErrorCode.Write", value))
	}
}

type FfiDestroyerErrorCode struct{}

func (_ FfiDestroyerErrorCode) Destroy(value ErrorCode) {
}

// One event from `Client.next_event`. It names the queue or job that has
// work; the host then drains that queue. Keep a default branch: new
// variants can come in a later `api_version`.
type Event uint

const (
	EventAdmissionRequest    Event = 1
	EventMembershipChanged   Event = 2
	EventProtectedReceived   Event = 3
	EventRecoveryReady       Event = 4
	EventCurrentViewReady    Event = 5
	EventInterestChanged     Event = 6
	EventPresence            Event = 7
	EventNearbyInvitation    Event = 8
	EventClosed              Event = 9
	EventControl             Event = 10
	EventPublicationReceived Event = 11
)

type FfiConverterEvent struct{}

var FfiConverterEventINSTANCE = FfiConverterEvent{}

func (c FfiConverterEvent) Lift(rb RustBufferI) Event {
	return LiftFromRustBuffer[Event](c, rb)
}

func (c FfiConverterEvent) Lower(value Event) C.RustBuffer {
	return LowerIntoRustBuffer[Event](c, value)
}

func (c FfiConverterEvent) LowerExternal(value Event) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Event](c, value))
}
func (FfiConverterEvent) Read(reader io.Reader) Event {
	id := readInt32(reader)
	return Event(id)
}

func (FfiConverterEvent) Write(writer io.Writer, value Event) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerEvent struct{}

func (_ FfiDestroyerEvent) Destroy(value Event) {
}

// A network mode. `Tor` always exists, so the bindings are the same for
// every build; without Tor in the build, `Client.open` gives `Unsupported`.
type Network uint

const (
	NetworkDirect    Network = 1
	NetworkLan       Network = 2
	NetworkNearby    Network = 3
	NetworkWan       Network = 4
	NetworkRelayOnly Network = 5
	NetworkWanOnly   Network = 6
	NetworkTor       Network = 7
)

type FfiConverterNetwork struct{}

var FfiConverterNetworkINSTANCE = FfiConverterNetwork{}

func (c FfiConverterNetwork) Lift(rb RustBufferI) Network {
	return LiftFromRustBuffer[Network](c, rb)
}

func (c FfiConverterNetwork) Lower(value Network) C.RustBuffer {
	return LowerIntoRustBuffer[Network](c, value)
}

func (c FfiConverterNetwork) LowerExternal(value Network) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Network](c, value))
}
func (FfiConverterNetwork) Read(reader io.Reader) Network {
	id := readInt32(reader)
	return Network(id)
}

func (FfiConverterNetwork) Write(writer io.Writer, value Network) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerNetwork struct{}

func (_ FfiDestroyerNetwork) Destroy(value Network) {
}

// Core's workspace phase. It is exhaustive in core, so UniFFI exports it
// as a remote type with no mirror.
type WorkspacePhase uint

const (
	WorkspacePhaseEmpty         WorkspacePhase = 1
	WorkspacePhaseCreating      WorkspacePhase = 2
	WorkspacePhaseJoining       WorkspacePhase = 3
	WorkspacePhaseSynchronizing WorkspacePhase = 4
	WorkspacePhaseActive        WorkspacePhase = 5
	WorkspacePhaseRecovering    WorkspacePhase = 6
	WorkspacePhaseLeaving       WorkspacePhase = 7
	WorkspacePhaseResetting     WorkspacePhase = 8
	WorkspacePhaseRemoved       WorkspacePhase = 9
	WorkspacePhaseFailed        WorkspacePhase = 10
)

type FfiConverterWorkspacePhase struct{}

var FfiConverterWorkspacePhaseINSTANCE = FfiConverterWorkspacePhase{}

func (c FfiConverterWorkspacePhase) Lift(rb RustBufferI) WorkspacePhase {
	return LiftFromRustBuffer[WorkspacePhase](c, rb)
}

func (c FfiConverterWorkspacePhase) Lower(value WorkspacePhase) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspacePhase](c, value)
}

func (c FfiConverterWorkspacePhase) LowerExternal(value WorkspacePhase) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspacePhase](c, value))
}
func (FfiConverterWorkspacePhase) Read(reader io.Reader) WorkspacePhase {
	id := readInt32(reader)
	return WorkspacePhase(id)
}

func (FfiConverterWorkspacePhase) Write(writer io.Writer, value WorkspacePhase) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerWorkspacePhase struct{}

func (_ FfiDestroyerWorkspacePhase) Destroy(value WorkspacePhase) {
}

type FfiConverterOptionalUint64 struct{}

var FfiConverterOptionalUint64INSTANCE = FfiConverterOptionalUint64{}

func (c FfiConverterOptionalUint64) Lift(rb RustBufferI) *uint64 {
	return LiftFromRustBuffer[*uint64](c, rb)
}

func (_ FfiConverterOptionalUint64) Read(reader io.Reader) *uint64 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint64INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint64) Lower(value *uint64) C.RustBuffer {
	return LowerIntoRustBuffer[*uint64](c, value)
}

func (c FfiConverterOptionalUint64) LowerExternal(value *uint64) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*uint64](c, value))
}

func (_ FfiConverterOptionalUint64) Write(writer io.Writer, value *uint64) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint64 struct{}

func (_ FfiDestroyerOptionalUint64) Destroy(value *uint64) {
	if value != nil {
		FfiDestroyerUint64{}.Destroy(*value)
	}
}

type FfiConverterOptionalString struct{}

var FfiConverterOptionalStringINSTANCE = FfiConverterOptionalString{}

func (c FfiConverterOptionalString) Lift(rb RustBufferI) *string {
	return LiftFromRustBuffer[*string](c, rb)
}

func (_ FfiConverterOptionalString) Read(reader io.Reader) *string {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterStringINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalString) Lower(value *string) C.RustBuffer {
	return LowerIntoRustBuffer[*string](c, value)
}

func (c FfiConverterOptionalString) LowerExternal(value *string) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*string](c, value))
}

func (_ FfiConverterOptionalString) Write(writer io.Writer, value *string) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalString struct{}

func (_ FfiDestroyerOptionalString) Destroy(value *string) {
	if value != nil {
		FfiDestroyerString{}.Destroy(*value)
	}
}

type FfiConverterOptionalBytes struct{}

var FfiConverterOptionalBytesINSTANCE = FfiConverterOptionalBytes{}

func (c FfiConverterOptionalBytes) Lift(rb RustBufferI) *[]byte {
	return LiftFromRustBuffer[*[]byte](c, rb)
}

func (_ FfiConverterOptionalBytes) Read(reader io.Reader) *[]byte {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterBytesINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalBytes) Lower(value *[]byte) C.RustBuffer {
	return LowerIntoRustBuffer[*[]byte](c, value)
}

func (c FfiConverterOptionalBytes) LowerExternal(value *[]byte) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*[]byte](c, value))
}

func (_ FfiConverterOptionalBytes) Write(writer io.Writer, value *[]byte) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterBytesINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalBytes struct{}

func (_ FfiDestroyerOptionalBytes) Destroy(value *[]byte) {
	if value != nil {
		FfiDestroyerBytes{}.Destroy(*value)
	}
}

type FfiConverterOptionalEvent struct{}

var FfiConverterOptionalEventINSTANCE = FfiConverterOptionalEvent{}

func (c FfiConverterOptionalEvent) Lift(rb RustBufferI) *Event {
	return LiftFromRustBuffer[*Event](c, rb)
}

func (_ FfiConverterOptionalEvent) Read(reader io.Reader) *Event {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterEventINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalEvent) Lower(value *Event) C.RustBuffer {
	return LowerIntoRustBuffer[*Event](c, value)
}

func (c FfiConverterOptionalEvent) LowerExternal(value *Event) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*Event](c, value))
}

func (_ FfiConverterOptionalEvent) Write(writer io.Writer, value *Event) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterEventINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalEvent struct{}

func (_ FfiDestroyerOptionalEvent) Destroy(value *Event) {
	if value != nil {
		FfiDestroyerEvent{}.Destroy(*value)
	}
}

type FfiConverterOptionalTypeEndpointId struct{}

var FfiConverterOptionalTypeEndpointIdINSTANCE = FfiConverterOptionalTypeEndpointId{}

func (c FfiConverterOptionalTypeEndpointId) Lift(rb RustBufferI) *EndpointId {
	return LiftFromRustBuffer[*EndpointId](c, rb)
}

func (_ FfiConverterOptionalTypeEndpointId) Read(reader io.Reader) *EndpointId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTypeEndpointIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTypeEndpointId) Lower(value *EndpointId) C.RustBuffer {
	return LowerIntoRustBuffer[*EndpointId](c, value)
}

func (c FfiConverterOptionalTypeEndpointId) LowerExternal(value *EndpointId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*EndpointId](c, value))
}

func (_ FfiConverterOptionalTypeEndpointId) Write(writer io.Writer, value *EndpointId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTypeEndpointIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTypeEndpointId struct{}

func (_ FfiDestroyerOptionalTypeEndpointId) Destroy(value *EndpointId) {
	if value != nil {
		FfiDestroyerTypeEndpointId{}.Destroy(*value)
	}
}

type FfiConverterOptionalTypeWorkspaceId struct{}

var FfiConverterOptionalTypeWorkspaceIdINSTANCE = FfiConverterOptionalTypeWorkspaceId{}

func (c FfiConverterOptionalTypeWorkspaceId) Lift(rb RustBufferI) *WorkspaceId {
	return LiftFromRustBuffer[*WorkspaceId](c, rb)
}

func (_ FfiConverterOptionalTypeWorkspaceId) Read(reader io.Reader) *WorkspaceId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTypeWorkspaceIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTypeWorkspaceId) Lower(value *WorkspaceId) C.RustBuffer {
	return LowerIntoRustBuffer[*WorkspaceId](c, value)
}

func (c FfiConverterOptionalTypeWorkspaceId) LowerExternal(value *WorkspaceId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*WorkspaceId](c, value))
}

func (_ FfiConverterOptionalTypeWorkspaceId) Write(writer io.Writer, value *WorkspaceId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTypeWorkspaceId struct{}

func (_ FfiDestroyerOptionalTypeWorkspaceId) Destroy(value *WorkspaceId) {
	if value != nil {
		FfiDestroyerTypeWorkspaceId{}.Destroy(*value)
	}
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type EndpointId = string
type FfiConverterTypeEndpointId = FfiConverterString
type FfiDestroyerTypeEndpointId = FfiDestroyerString

var FfiConverterTypeEndpointIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypeEndpointId(value ExternalCRustBuffer) EndpointId {
	return FfiConverterTypeEndpointIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeEndpointId(value EndpointId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeEndpointIdINSTANCE.Lower(value))
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type WorkspaceId = string
type FfiConverterTypeWorkspaceId = FfiConverterString
type FfiDestroyerTypeWorkspaceId = FfiDestroyerString

var FfiConverterTypeWorkspaceIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypeWorkspaceId(value ExternalCRustBuffer) WorkspaceId {
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeWorkspaceId(value WorkspaceId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeWorkspaceIdINSTANCE.Lower(value))
}

// The stable code of `error`. A free function, so every language has it.
func ApiErrorCode(error *ApiError) ErrorCode {
	return FfiConverterErrorCodeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_func_api_error_code(FfiConverterApiErrorINSTANCE.Lower(error), _uniffiStatus),
		}
	}))
}

// The contract version (`arachne_api::API_VERSION`) this library was built with.
func ApiVersion() uint32 {
	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_arachne_sdk_fn_func_api_version(_uniffiStatus)
	}))
}
