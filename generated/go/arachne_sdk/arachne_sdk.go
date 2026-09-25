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
			return C.uniffi_arachne_sdk_checksum_func_is_suspended()
		})
		if checksum != 46233 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_func_is_suspended: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_func_resume()
		})
		if checksum != 24835 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_func_resume: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_func_suspend()
		})
		if checksum != 29681 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_func_suspend: UniFFI API checksum mismatch")
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
			return C.uniffi_arachne_sdk_checksum_method_client_acknowledge_admission_approval()
		})
		if checksum != 60484 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_acknowledge_admission_approval: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_add_address_hint()
		})
		if checksum != 41844 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_add_address_hint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_admission_approvals()
		})
		if checksum != 61891 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_admission_approvals: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_admission()
		})
		if checksum != 23402 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_invitation()
		})
		if checksum != 59068 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_join()
		})
		if checksum != 4589 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_protected_publication()
		})
		if checksum != 31539 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_protected_publication: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_protected_reception()
		})
		if checksum != 34784 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_protected_reception: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_adopt_recovery()
		})
		if checksum != 17776 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_adopt_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_begin_join()
		})
		if checksum != 26002 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_begin_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_cancel_recovery_range()
		})
		if checksum != 58830 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_cancel_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_create_workspace()
		})
		if checksum != 41138 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_create_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_fetch_invitation_checkpoint()
		})
		if checksum != 19639 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_fetch_invitation_checkpoint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_fetch_recovery_range()
		})
		if checksum != 27062 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_fetch_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_inspect_invitation()
		})
		if checksum != 23746 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_inspect_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_install_member_policy()
		})
		if checksum != 32558 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_install_member_policy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_install_workspace_policy()
		})
		if checksum != 60122 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_install_workspace_policy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_invitation_controls()
		})
		if checksum != 32555 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_invitation_controls: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_member_roster()
		})
		if checksum != 12780 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_member_roster: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_metrics()
		})
		if checksum != 43460 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_metrics: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_network_change()
		})
		if checksum != 62595 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_network_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_control()
		})
		if checksum != 2049 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_control: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_interest()
		})
		if checksum != 24627 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_interest: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_pending_object()
		})
		if checksum != 11998 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_pending_object: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_presence()
		})
		if checksum != 7412 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_presence: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_protected()
		})
		if checksum != 59576 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_protected: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_poll_recovery_range()
		})
		if checksum != 19239 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_poll_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_retained_admission()
		})
		if checksum != 45248 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_retained_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_send_admission_reply()
		})
		if checksum != 40754 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_send_admission_reply: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_set_deadline()
		})
		if checksum != 32066 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_set_deadline: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_set_interest()
		})
		if checksum != 63561 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_set_interest: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_admission()
		})
		if checksum != 20793 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_invitation()
		})
		if checksum != 59519 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_invitation_approval()
		})
		if checksum != 54891 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_invitation_approval: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_invitation_decline()
		})
		if checksum != 18391 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_invitation_decline: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_join()
		})
		if checksum != 52062 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_object_acknowledgement()
		})
		if checksum != 39524 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_object_acknowledgement: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_object_rejection()
		})
		if checksum != 8552 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_object_rejection: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_protected_publication()
		})
		if checksum != 7575 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_protected_publication: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_stage_recovery_range()
		})
		if checksum != 14092 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_stage_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_client_use_service_profile()
		})
		if checksum != 2127 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_client_use_service_profile: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_admissioncandidate_is_used()
		})
		if checksum != 29219 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_admissioncandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_admissioncandidate_workspace()
		})
		if checksum != 31048 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_admissioncandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_invitationcandidate_is_used()
		})
		if checksum != 47978 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_invitationcandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_invitationcandidate_workspace()
		})
		if checksum != 41200 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_invitationcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_joincandidate_is_used()
		})
		if checksum != 40736 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_joincandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_joincandidate_workspace()
		})
		if checksum != 14471 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_joincandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_publicationcandidate_is_used()
		})
		if checksum != 5993 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_publicationcandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_publicationcandidate_workspace()
		})
		if checksum != 40684 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_publicationcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_receptioncandidate_is_used()
		})
		if checksum != 22203 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_receptioncandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_receptioncandidate_workspace()
		})
		if checksum != 52165 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_receptioncandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_recoverycandidate_durable()
		})
		if checksum != 45684 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_recoverycandidate_durable: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_recoverycandidate_is_used()
		})
		if checksum != 42481 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_recoverycandidate_is_used: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_recoverycandidate_publication_count()
		})
		if checksum != 42585 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_recoverycandidate_publication_count: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_sdk_checksum_method_recoverycandidate_workspace()
		})
		if checksum != 39319 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_sdk: uniffi_arachne_sdk_checksum_method_recoverycandidate_workspace: UniFFI API checksum mismatch")
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

// A staged admission, approval or decline. Adopt it with `adopt_admission`.
type AdmissionCandidateInterface interface {
	// It was adopted (or an adopt was tried).
	IsUsed() bool
	// The workspace this candidate changes.
	Workspace() WorkspaceId
}

// A staged admission, approval or decline. Adopt it with `adopt_admission`.
type AdmissionCandidate struct {
	ffiObject FfiObject
}

// It was adopted (or an adopt was tried).
func (_self *AdmissionCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*AdmissionCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_admissioncandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

// The workspace this candidate changes.
func (_self *AdmissionCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*AdmissionCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_admissioncandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *AdmissionCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAdmissionCandidate struct{}

var FfiConverterAdmissionCandidateINSTANCE = FfiConverterAdmissionCandidate{}

func (c FfiConverterAdmissionCandidate) Lift(handle C.uint64_t) *AdmissionCandidate {
	result := &AdmissionCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_admissioncandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_admissioncandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*AdmissionCandidate).Destroy)
	return result
}

func (c FfiConverterAdmissionCandidate) Read(reader io.Reader) *AdmissionCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterAdmissionCandidate) Lower(value *AdmissionCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*AdmissionCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterAdmissionCandidate) Write(writer io.Writer, value *AdmissionCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalAdmissionCandidate(handle uint64) *AdmissionCandidate {
	return FfiConverterAdmissionCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalAdmissionCandidate(value *AdmissionCandidate) uint64 {
	return uint64(FfiConverterAdmissionCandidateINSTANCE.Lower(value))
}

type FfiDestroyerAdmissionCandidate struct{}

func (_ FfiDestroyerAdmissionCandidate) Destroy(value *AdmissionCandidate) {
	value.Destroy()
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
	// Mark a pending approval as seen.
	AcknowledgeAdmissionApproval(attemptId AttemptId) error
	// Tell the transport where `peer` can be reached (`host:port`).
	AddAddressHint(peer EndpointId, address string) error
	// One page of requests that wait for an administrator (`limit` 1-64).
	AdmissionApprovals(after *AttemptId, limit *uint32) (AdmissionApprovalPage, error)
	AdoptAdmission(candidate *AdmissionCandidate) (WorkspaceInfo, error)
	// Adopt a staged invitation and get its bearer link.
	AdoptInvitation(candidate *InvitationCandidate) (InvitationInfo, error)
	AdoptJoin(candidate *JoinCandidate) (WorkspaceInfo, error)
	// Adopt and send a staged publication.
	AdoptProtectedPublication(candidate *PublicationCandidate) (DeliveryReport, error)
	// Adopt a reception, acknowledgement or rejection.
	AdoptProtectedReception(candidate *ReceptionCandidate) error
	// Adopt a staged range. Recovered objects wait in the inbox.
	AdoptRecovery(candidate *RecoveryCandidate) (RecoveryAdoption, error)
	// Start joining. Send `admission_request` of the result to a member.
	BeginJoin(invitation []byte, checkpoint []byte, displayName string, peers []EndpointId) (JoinRequest, error)
	CancelRecoveryRange() error
	// Create a workspace with this client as its first administrator.
	CreateWorkspace(displayName string, workspaceName *string) (WorkspaceInfo, error)
	// Fetch the current checkpoint of a compact invitation from up to three members.
	FetchInvitationCheckpoint(invitation []byte, peers []EndpointId) (InvitationCheckpoint, error)
	// Ask a peer for a range of an author's objects. `epoch`: an earlier
	// author epoch still in the receive window (`None`: current).
	FetchRecoveryRange(request RecoveryRangeRequest, epoch *uint64) (RecoveryRangeStatus, error)
	// Check an invitation link against its checkpoint without joining.
	InspectInvitation(invitation []byte, checkpoint []byte) (InvitationDetails, error)
	// Route only `topics` between all members at `revision`.
	InstallMemberPolicy(revision uint64, topics []string) error
	// Route every topic between all members at `revision` (epoch + 1).
	InstallWorkspacePolicy(revision uint64) error
	// The registered invitation links (read only).
	InvitationControls() ([]InvitationControl, error)
	MemberRoster() (MemberRoster, error)
	// Local counters for diagnostics. Do not export them as telemetry.
	Metrics() (WorkspaceMetrics, error)
	// Rebind sockets after the device network changed.
	NetworkChange() error
	// Serve one queued peer-control exchange. `true`: one was served.
	PollControl() (bool, error)
	// The settled result of `set_interest`, or `None` while it is pending.
	PollInterest() (*InterestObservation, error)
	// The next object that the application has not acknowledged or rejected.
	PollPendingObject() (*ReceivedPublication, error)
	// One presence round with the members. `announce` marks a restart.
	PollPresence(announce bool) (PresenceRound, error)
	// Stage one incoming protected publication. The plaintext stays hidden
	// until the candidate is adopted; then read it with `poll_pending_object`.
	PollProtected() (**ReceptionCandidate, error)
	// The fetch result once it changed, or `None`.
	PollRecoveryRange() (*RecoveryRangeStatus, error)
	// The retained answer for an adopted admission, to send to the joiner.
	RetainedAdmission(authenticatedEndpoint EndpointId, request []byte) (AdmissionReply, error)
	// Answer the held exchange after its transition is durable.
	// `false`: the requester expired.
	SendAdmissionReply() (bool, error)
	// Give each later blocking op this deadline (`None`: no deadline). At
	// the deadline the op fails with `DeadlineExceeded`.
	SetDeadline(deadlineMs *uint64)
	SetInterest(workspace WorkspaceId, revision uint64, topic string, subscribed bool) error
	// Stage the admission of a joiner. `authenticated_endpoint` is the
	// endpoint the request came from.
	StageAdmission(authenticatedEndpoint EndpointId, request []byte) (*AdmissionCandidate, error)
	// Register an invitation link. `expires_at` is Unix seconds; 0 never expires.
	StageInvitation(expiresAt uint64, kind InvitationKind) (*InvitationCandidate, error)
	// Approve (bind) a personal invitation for one join request.
	StageInvitationApproval(request []byte, attemptId *AttemptId) (*AdmissionCandidate, error)
	// Decline a personal invitation request.
	StageInvitationDecline(request []byte, attemptId *AttemptId) (*AdmissionCandidate, error)
	// Stage the join from the member's welcome and admission commits.
	StageJoin(welcome []byte, commits []JoinAdmissionStep) (*JoinCandidate, error)
	// Stage the application's acceptance of a pending object.
	StageObjectAcknowledgement(object ReceivedPublication) (*ReceptionCandidate, error)
	// Stage a permanent rejection of a pending object; it never comes back.
	StageObjectRejection(object ReceivedPublication) (*ReceptionCandidate, error)
	// Stage an encrypted publication for the workspace members.
	StageProtectedPublication(workspace WorkspaceId, revision uint64, topic string, id RecordId, payload []byte, current *PublicationCurrent) (*PublicationCandidate, error)
	// Stage a ready range. `retain_until` is Unix seconds; 0 keeps no copy
	// for third-party recovery.
	StageRecoveryRange(retainUntil uint64) (RecoveryStage, error)
	// Mark this client's workspace profile as a service (no extra rights).
	UseServiceProfile() error
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

// Mark a pending approval as seen.
func (_self *Client) AcknowledgeAdmissionApproval(attemptId AttemptId) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_acknowledge_admission_approval(
			_pointer, FfiConverterTypeAttemptIdINSTANCE.Lower(attemptId), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Tell the transport where `peer` can be reached (`host:port`).
func (_self *Client) AddAddressHint(peer EndpointId, address string) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_add_address_hint(
			_pointer, FfiConverterTypeEndpointIdINSTANCE.Lower(peer), FfiConverterStringINSTANCE.Lower(address), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// One page of requests that wait for an administrator (`limit` 1-64).
func (_self *Client) AdmissionApprovals(after *AttemptId, limit *uint32) (AdmissionApprovalPage, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_admission_approvals(
				_pointer, FfiConverterOptionalTypeAttemptIdINSTANCE.Lower(after), FfiConverterOptionalUint32INSTANCE.Lower(limit), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue AdmissionApprovalPage
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionApprovalPageINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) AdoptAdmission(candidate *AdmissionCandidate) (WorkspaceInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_adopt_admission(
				_pointer, FfiConverterAdmissionCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

// Adopt a staged invitation and get its bearer link.
func (_self *Client) AdoptInvitation(candidate *InvitationCandidate) (InvitationInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_adopt_invitation(
				_pointer, FfiConverterInvitationCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue InvitationInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) AdoptJoin(candidate *JoinCandidate) (WorkspaceInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_adopt_join(
				_pointer, FfiConverterJoinCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

// Adopt and send a staged publication.
func (_self *Client) AdoptProtectedPublication(candidate *PublicationCandidate) (DeliveryReport, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_adopt_protected_publication(
				_pointer, FfiConverterPublicationCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue DeliveryReport
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDeliveryReportINSTANCE.Lift(_uniffiRV), nil
	}
}

// Adopt a reception, acknowledgement or rejection.
func (_self *Client) AdoptProtectedReception(candidate *ReceptionCandidate) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_adopt_protected_reception(
			_pointer, FfiConverterReceptionCandidateINSTANCE.Lower(candidate), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Adopt a staged range. Recovered objects wait in the inbox.
func (_self *Client) AdoptRecovery(candidate *RecoveryCandidate) (RecoveryAdoption, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_adopt_recovery(
				_pointer, FfiConverterRecoveryCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryAdoption
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryAdoptionINSTANCE.Lift(_uniffiRV), nil
	}
}

// Start joining. Send `admission_request` of the result to a member.
func (_self *Client) BeginJoin(invitation []byte, checkpoint []byte, displayName string, peers []EndpointId) (JoinRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_begin_join(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterBytesINSTANCE.Lower(checkpoint), FfiConverterStringINSTANCE.Lower(displayName), FfiConverterSequenceTypeEndpointIdINSTANCE.Lower(peers), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue JoinRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) CancelRecoveryRange() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_cancel_recovery_range(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Create a workspace with this client as its first administrator.
func (_self *Client) CreateWorkspace(displayName string, workspaceName *string) (WorkspaceInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_create_workspace(
				_pointer, FfiConverterStringINSTANCE.Lower(displayName), FfiConverterOptionalStringINSTANCE.Lower(workspaceName), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

// Fetch the current checkpoint of a compact invitation from up to three members.
func (_self *Client) FetchInvitationCheckpoint(invitation []byte, peers []EndpointId) (InvitationCheckpoint, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_fetch_invitation_checkpoint(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterSequenceTypeEndpointIdINSTANCE.Lower(peers), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue InvitationCheckpoint
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationCheckpointINSTANCE.Lift(_uniffiRV), nil
	}
}

// Ask a peer for a range of an author's objects. `epoch`: an earlier
// author epoch still in the receive window (`None`: current).
func (_self *Client) FetchRecoveryRange(request RecoveryRangeRequest, epoch *uint64) (RecoveryRangeStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_fetch_recovery_range(
				_pointer, FfiConverterRecoveryRangeRequestINSTANCE.Lower(request), FfiConverterOptionalUint64INSTANCE.Lower(epoch), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryRangeStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryRangeStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// Check an invitation link against its checkpoint without joining.
func (_self *Client) InspectInvitation(invitation []byte, checkpoint []byte) (InvitationDetails, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_inspect_invitation(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterBytesINSTANCE.Lower(checkpoint), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue InvitationDetails
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationDetailsINSTANCE.Lift(_uniffiRV), nil
	}
}

// Route only `topics` between all members at `revision`.
func (_self *Client) InstallMemberPolicy(revision uint64, topics []string) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_install_member_policy(
			_pointer, FfiConverterUint64INSTANCE.Lower(revision), FfiConverterSequenceStringINSTANCE.Lower(topics), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Route every topic between all members at `revision` (epoch + 1).
func (_self *Client) InstallWorkspacePolicy(revision uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_install_workspace_policy(
			_pointer, FfiConverterUint64INSTANCE.Lower(revision), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// The registered invitation links (read only).
func (_self *Client) InvitationControls() ([]InvitationControl, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_invitation_controls(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue []InvitationControl
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSequenceInvitationControlINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) MemberRoster() (MemberRoster, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_member_roster(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue MemberRoster
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterMemberRosterINSTANCE.Lift(_uniffiRV), nil
	}
}

// Local counters for diagnostics. Do not export them as telemetry.
func (_self *Client) Metrics() (WorkspaceMetrics, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_metrics(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceMetrics
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceMetricsINSTANCE.Lift(_uniffiRV), nil
	}
}

// Rebind sockets after the device network changed.
func (_self *Client) NetworkChange() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_network_change(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Serve one queued peer-control exchange. `true`: one was served.
func (_self *Client) PollControl() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_client_poll_control(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// The settled result of `set_interest`, or `None` while it is pending.
func (_self *Client) PollInterest() (*InterestObservation, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_poll_interest(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *InterestObservation
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalInterestObservationINSTANCE.Lift(_uniffiRV), nil
	}
}

// The next object that the application has not acknowledged or rejected.
func (_self *Client) PollPendingObject() (*ReceivedPublication, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_poll_pending_object(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ReceivedPublication
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalReceivedPublicationINSTANCE.Lift(_uniffiRV), nil
	}
}

// One presence round with the members. `announce` marks a restart.
func (_self *Client) PollPresence(announce bool) (PresenceRound, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_poll_presence(
				_pointer, FfiConverterBoolINSTANCE.Lower(announce), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue PresenceRound
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPresenceRoundINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage one incoming protected publication. The plaintext stays hidden
// until the candidate is adopted; then read it with `poll_pending_object`.
func (_self *Client) PollProtected() (**ReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_poll_protected(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue **ReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// The fetch result once it changed, or `None`.
func (_self *Client) PollRecoveryRange() (*RecoveryRangeStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_poll_recovery_range(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RecoveryRangeStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalRecoveryRangeStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// The retained answer for an adopted admission, to send to the joiner.
func (_self *Client) RetainedAdmission(authenticatedEndpoint EndpointId, request []byte) (AdmissionReply, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_retained_admission(
				_pointer, FfiConverterTypeEndpointIdINSTANCE.Lower(authenticatedEndpoint), FfiConverterBytesINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue AdmissionReply
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionReplyINSTANCE.Lift(_uniffiRV), nil
	}
}

// Answer the held exchange after its transition is durable.
// `false`: the requester expired.
func (_self *Client) SendAdmissionReply() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_client_send_admission_reply(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Give each later blocking op this deadline (`None`: no deadline). At
// the deadline the op fails with `DeadlineExceeded`.
func (_self *Client) SetDeadline(deadlineMs *uint64) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_set_deadline(
			_pointer, FfiConverterOptionalUint64INSTANCE.Lower(deadlineMs), _uniffiStatus)
		return false
	})
}

func (_self *Client) SetInterest(workspace WorkspaceId, revision uint64, topic string, subscribed bool) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_set_interest(
			_pointer, FfiConverterTypeWorkspaceIdINSTANCE.Lower(workspace), FfiConverterUint64INSTANCE.Lower(revision), FfiConverterStringINSTANCE.Lower(topic), FfiConverterBoolINSTANCE.Lower(subscribed), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Stage the admission of a joiner. `authenticated_endpoint` is the
// endpoint the request came from.
func (_self *Client) StageAdmission(authenticatedEndpoint EndpointId, request []byte) (*AdmissionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_admission(
			_pointer, FfiConverterTypeEndpointIdINSTANCE.Lower(authenticatedEndpoint), FfiConverterBytesINSTANCE.Lower(request), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *AdmissionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Register an invitation link. `expires_at` is Unix seconds; 0 never expires.
func (_self *Client) StageInvitation(expiresAt uint64, kind InvitationKind) (*InvitationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_invitation(
			_pointer, FfiConverterUint64INSTANCE.Lower(expiresAt), FfiConverterInvitationKindINSTANCE.Lower(kind), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *InvitationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Approve (bind) a personal invitation for one join request.
func (_self *Client) StageInvitationApproval(request []byte, attemptId *AttemptId) (*AdmissionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_invitation_approval(
			_pointer, FfiConverterBytesINSTANCE.Lower(request), FfiConverterOptionalTypeAttemptIdINSTANCE.Lower(attemptId), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *AdmissionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Decline a personal invitation request.
func (_self *Client) StageInvitationDecline(request []byte, attemptId *AttemptId) (*AdmissionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_invitation_decline(
			_pointer, FfiConverterBytesINSTANCE.Lower(request), FfiConverterOptionalTypeAttemptIdINSTANCE.Lower(attemptId), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *AdmissionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage the join from the member's welcome and admission commits.
func (_self *Client) StageJoin(welcome []byte, commits []JoinAdmissionStep) (*JoinCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_join(
			_pointer, FfiConverterBytesINSTANCE.Lower(welcome), FfiConverterSequenceJoinAdmissionStepINSTANCE.Lower(commits), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *JoinCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage the application's acceptance of a pending object.
func (_self *Client) StageObjectAcknowledgement(object ReceivedPublication) (*ReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_object_acknowledgement(
			_pointer, FfiConverterReceivedPublicationINSTANCE.Lower(object), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage a permanent rejection of a pending object; it never comes back.
func (_self *Client) StageObjectRejection(object ReceivedPublication) (*ReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_object_rejection(
			_pointer, FfiConverterReceivedPublicationINSTANCE.Lower(object), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage an encrypted publication for the workspace members.
func (_self *Client) StageProtectedPublication(workspace WorkspaceId, revision uint64, topic string, id RecordId, payload []byte, current *PublicationCurrent) (*PublicationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_client_stage_protected_publication(
			_pointer, FfiConverterTypeWorkspaceIdINSTANCE.Lower(workspace), FfiConverterUint64INSTANCE.Lower(revision), FfiConverterStringINSTANCE.Lower(topic), FfiConverterTypeRecordIdINSTANCE.Lower(id), FfiConverterBytesINSTANCE.Lower(payload), FfiConverterOptionalPublicationCurrentINSTANCE.Lower(current), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *PublicationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPublicationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage a ready range. `retain_until` is Unix seconds; 0 keeps no copy
// for third-party recovery.
func (_self *Client) StageRecoveryRange(retainUntil uint64) (RecoveryStage, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_client_stage_recovery_range(
				_pointer, FfiConverterUint64INSTANCE.Lower(retainUntil), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryStage
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryStageINSTANCE.Lift(_uniffiRV), nil
	}
}

// Mark this client's workspace profile as a service (no extra rights).
func (_self *Client) UseServiceProfile() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_method_client_use_service_profile(
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

// A staged invitation link. Adopt it with `adopt_invitation`.
type InvitationCandidateInterface interface {
	// It was adopted (or an adopt was tried).
	IsUsed() bool
	// The workspace this candidate changes.
	Workspace() WorkspaceId
}

// A staged invitation link. Adopt it with `adopt_invitation`.
type InvitationCandidate struct {
	ffiObject FfiObject
}

// It was adopted (or an adopt was tried).
func (_self *InvitationCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*InvitationCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_invitationcandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

// The workspace this candidate changes.
func (_self *InvitationCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*InvitationCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_invitationcandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *InvitationCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterInvitationCandidate struct{}

var FfiConverterInvitationCandidateINSTANCE = FfiConverterInvitationCandidate{}

func (c FfiConverterInvitationCandidate) Lift(handle C.uint64_t) *InvitationCandidate {
	result := &InvitationCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_invitationcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_invitationcandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*InvitationCandidate).Destroy)
	return result
}

func (c FfiConverterInvitationCandidate) Read(reader io.Reader) *InvitationCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterInvitationCandidate) Lower(value *InvitationCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*InvitationCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterInvitationCandidate) Write(writer io.Writer, value *InvitationCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalInvitationCandidate(handle uint64) *InvitationCandidate {
	return FfiConverterInvitationCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalInvitationCandidate(value *InvitationCandidate) uint64 {
	return uint64(FfiConverterInvitationCandidateINSTANCE.Lower(value))
}

type FfiDestroyerInvitationCandidate struct{}

func (_ FfiDestroyerInvitationCandidate) Destroy(value *InvitationCandidate) {
	value.Destroy()
}

// A staged join. Adopt it with `adopt_join`.
type JoinCandidateInterface interface {
	// It was adopted (or an adopt was tried).
	IsUsed() bool
	// The workspace this candidate changes.
	Workspace() WorkspaceId
}

// A staged join. Adopt it with `adopt_join`.
type JoinCandidate struct {
	ffiObject FfiObject
}

// It was adopted (or an adopt was tried).
func (_self *JoinCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*JoinCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_joincandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

// The workspace this candidate changes.
func (_self *JoinCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*JoinCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_joincandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *JoinCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterJoinCandidate struct{}

var FfiConverterJoinCandidateINSTANCE = FfiConverterJoinCandidate{}

func (c FfiConverterJoinCandidate) Lift(handle C.uint64_t) *JoinCandidate {
	result := &JoinCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_joincandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_joincandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*JoinCandidate).Destroy)
	return result
}

func (c FfiConverterJoinCandidate) Read(reader io.Reader) *JoinCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterJoinCandidate) Lower(value *JoinCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*JoinCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterJoinCandidate) Write(writer io.Writer, value *JoinCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalJoinCandidate(handle uint64) *JoinCandidate {
	return FfiConverterJoinCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalJoinCandidate(value *JoinCandidate) uint64 {
	return uint64(FfiConverterJoinCandidateINSTANCE.Lower(value))
}

type FfiDestroyerJoinCandidate struct{}

func (_ FfiDestroyerJoinCandidate) Destroy(value *JoinCandidate) {
	value.Destroy()
}

// A staged protected publication. Adopt it with `adopt_protected_publication`.
type PublicationCandidateInterface interface {
	// It was adopted (or an adopt was tried).
	IsUsed() bool
	// The workspace this candidate changes.
	Workspace() WorkspaceId
}

// A staged protected publication. Adopt it with `adopt_protected_publication`.
type PublicationCandidate struct {
	ffiObject FfiObject
}

// It was adopted (or an adopt was tried).
func (_self *PublicationCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*PublicationCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_publicationcandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

// The workspace this candidate changes.
func (_self *PublicationCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*PublicationCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_publicationcandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *PublicationCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPublicationCandidate struct{}

var FfiConverterPublicationCandidateINSTANCE = FfiConverterPublicationCandidate{}

func (c FfiConverterPublicationCandidate) Lift(handle C.uint64_t) *PublicationCandidate {
	result := &PublicationCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_publicationcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_publicationcandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*PublicationCandidate).Destroy)
	return result
}

func (c FfiConverterPublicationCandidate) Read(reader io.Reader) *PublicationCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterPublicationCandidate) Lower(value *PublicationCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*PublicationCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterPublicationCandidate) Write(writer io.Writer, value *PublicationCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalPublicationCandidate(handle uint64) *PublicationCandidate {
	return FfiConverterPublicationCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalPublicationCandidate(value *PublicationCandidate) uint64 {
	return uint64(FfiConverterPublicationCandidateINSTANCE.Lower(value))
}

type FfiDestroyerPublicationCandidate struct{}

func (_ FfiDestroyerPublicationCandidate) Destroy(value *PublicationCandidate) {
	value.Destroy()
}

// A staged inbox change: a reception from `poll_protected`, or an
// acknowledgement or rejection. Adopt it with `adopt_protected_reception`.
type ReceptionCandidateInterface interface {
	// It was adopted (or an adopt was tried).
	IsUsed() bool
	// The workspace this candidate changes.
	Workspace() WorkspaceId
}

// A staged inbox change: a reception from `poll_protected`, or an
// acknowledgement or rejection. Adopt it with `adopt_protected_reception`.
type ReceptionCandidate struct {
	ffiObject FfiObject
}

// It was adopted (or an adopt was tried).
func (_self *ReceptionCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*ReceptionCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_receptioncandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

// The workspace this candidate changes.
func (_self *ReceptionCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*ReceptionCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_receptioncandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *ReceptionCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterReceptionCandidate struct{}

var FfiConverterReceptionCandidateINSTANCE = FfiConverterReceptionCandidate{}

func (c FfiConverterReceptionCandidate) Lift(handle C.uint64_t) *ReceptionCandidate {
	result := &ReceptionCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_receptioncandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_receptioncandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*ReceptionCandidate).Destroy)
	return result
}

func (c FfiConverterReceptionCandidate) Read(reader io.Reader) *ReceptionCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterReceptionCandidate) Lower(value *ReceptionCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*ReceptionCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterReceptionCandidate) Write(writer io.Writer, value *ReceptionCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalReceptionCandidate(handle uint64) *ReceptionCandidate {
	return FfiConverterReceptionCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalReceptionCandidate(value *ReceptionCandidate) uint64 {
	return uint64(FfiConverterReceptionCandidateINSTANCE.Lower(value))
}

type FfiDestroyerReceptionCandidate struct{}

func (_ FfiDestroyerReceptionCandidate) Destroy(value *ReceptionCandidate) {
	value.Destroy()
}

// A staged recovery range. Adopt it with `adopt_recovery`.
type RecoveryCandidateInterface interface {
	Durable() bool
	IsUsed() bool
	PublicationCount() uint64
	Workspace() WorkspaceId
}

// A staged recovery range. Adopt it with `adopt_recovery`.
type RecoveryCandidate struct {
	ffiObject FfiObject
}

func (_self *RecoveryCandidate) Durable() bool {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_recoverycandidate_durable(
			_pointer, _uniffiStatus)
	}))
}

func (_self *RecoveryCandidate) IsUsed() bool {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_method_recoverycandidate_is_used(
			_pointer, _uniffiStatus)
	}))
}

func (_self *RecoveryCandidate) PublicationCount() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_sdk_fn_method_recoverycandidate_publication_count(
			_pointer, _uniffiStatus)
	}))
}

func (_self *RecoveryCandidate) Workspace() WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterTypeWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_sdk_fn_method_recoverycandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *RecoveryCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterRecoveryCandidate struct{}

var FfiConverterRecoveryCandidateINSTANCE = FfiConverterRecoveryCandidate{}

func (c FfiConverterRecoveryCandidate) Lift(handle C.uint64_t) *RecoveryCandidate {
	result := &RecoveryCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_sdk_fn_clone_recoverycandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_sdk_fn_free_recoverycandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*RecoveryCandidate).Destroy)
	return result
}

func (c FfiConverterRecoveryCandidate) Read(reader io.Reader) *RecoveryCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterRecoveryCandidate) Lower(value *RecoveryCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*RecoveryCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterRecoveryCandidate) Write(writer io.Writer, value *RecoveryCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalRecoveryCandidate(handle uint64) *RecoveryCandidate {
	return FfiConverterRecoveryCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalRecoveryCandidate(value *RecoveryCandidate) uint64 {
	return uint64(FfiConverterRecoveryCandidateINSTANCE.Lower(value))
}

type FfiDestroyerRecoveryCandidate struct{}

func (_ FfiDestroyerRecoveryCandidate) Destroy(value *RecoveryCandidate) {
	value.Destroy()
}

// One admission request that waits for an administrator.
type AdmissionApproval struct {
	AttemptId    AttemptId
	Endpoint     EndpointId
	Request      []byte
	DisplayName  *string
	Automatic    bool
	Delivered    bool
	Acknowledged bool
}

func (r *AdmissionApproval) Destroy() {
	FfiDestroyerTypeAttemptId{}.Destroy(r.AttemptId)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerBytes{}.Destroy(r.Request)
	FfiDestroyerOptionalString{}.Destroy(r.DisplayName)
	FfiDestroyerBool{}.Destroy(r.Automatic)
	FfiDestroyerBool{}.Destroy(r.Delivered)
	FfiDestroyerBool{}.Destroy(r.Acknowledged)
}

type FfiConverterAdmissionApproval struct{}

var FfiConverterAdmissionApprovalINSTANCE = FfiConverterAdmissionApproval{}

func (c FfiConverterAdmissionApproval) Lift(rb RustBufferI) AdmissionApproval {
	return LiftFromRustBuffer[AdmissionApproval](c, rb)
}

func (c FfiConverterAdmissionApproval) Read(reader io.Reader) AdmissionApproval {
	return AdmissionApproval{
		FfiConverterTypeAttemptIdINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionApproval) Lower(value AdmissionApproval) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionApproval](c, value)
}

func (c FfiConverterAdmissionApproval) LowerExternal(value AdmissionApproval) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionApproval](c, value))
}

func (c FfiConverterAdmissionApproval) Write(writer io.Writer, value AdmissionApproval) {
	FfiConverterTypeAttemptIdINSTANCE.Write(writer, value.AttemptId)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterBytesINSTANCE.Write(writer, value.Request)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.DisplayName)
	FfiConverterBoolINSTANCE.Write(writer, value.Automatic)
	FfiConverterBoolINSTANCE.Write(writer, value.Delivered)
	FfiConverterBoolINSTANCE.Write(writer, value.Acknowledged)
}

type FfiDestroyerAdmissionApproval struct{}

func (_ FfiDestroyerAdmissionApproval) Destroy(value AdmissionApproval) {
	value.Destroy()
}

// One page of pending approvals. Pass `next_after` for the next page.
type AdmissionApprovalPage struct {
	Approvals []AdmissionApproval
	Complete  bool
	NextAfter *AttemptId
}

func (r *AdmissionApprovalPage) Destroy() {
	FfiDestroyerSequenceAdmissionApproval{}.Destroy(r.Approvals)
	FfiDestroyerBool{}.Destroy(r.Complete)
	FfiDestroyerOptionalTypeAttemptId{}.Destroy(r.NextAfter)
}

type FfiConverterAdmissionApprovalPage struct{}

var FfiConverterAdmissionApprovalPageINSTANCE = FfiConverterAdmissionApprovalPage{}

func (c FfiConverterAdmissionApprovalPage) Lift(rb RustBufferI) AdmissionApprovalPage {
	return LiftFromRustBuffer[AdmissionApprovalPage](c, rb)
}

func (c FfiConverterAdmissionApprovalPage) Read(reader io.Reader) AdmissionApprovalPage {
	return AdmissionApprovalPage{
		FfiConverterSequenceAdmissionApprovalINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalTypeAttemptIdINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionApprovalPage) Lower(value AdmissionApprovalPage) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionApprovalPage](c, value)
}

func (c FfiConverterAdmissionApprovalPage) LowerExternal(value AdmissionApprovalPage) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionApprovalPage](c, value))
}

func (c FfiConverterAdmissionApprovalPage) Write(writer io.Writer, value AdmissionApprovalPage) {
	FfiConverterSequenceAdmissionApprovalINSTANCE.Write(writer, value.Approvals)
	FfiConverterBoolINSTANCE.Write(writer, value.Complete)
	FfiConverterOptionalTypeAttemptIdINSTANCE.Write(writer, value.NextAfter)
}

type FfiDestroyerAdmissionApprovalPage struct{}

func (_ FfiDestroyerAdmissionApprovalPage) Destroy(value AdmissionApprovalPage) {
	value.Destroy()
}

type AdmissionAuthorization struct {
	InvitationKey       Key32
	GrantSignature      []byte
	RedemptionSignature []byte
}

func (r *AdmissionAuthorization) Destroy() {
	FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
	FfiDestroyerBytes{}.Destroy(r.GrantSignature)
	FfiDestroyerBytes{}.Destroy(r.RedemptionSignature)
}

type FfiConverterAdmissionAuthorization struct{}

var FfiConverterAdmissionAuthorizationINSTANCE = FfiConverterAdmissionAuthorization{}

func (c FfiConverterAdmissionAuthorization) Lift(rb RustBufferI) AdmissionAuthorization {
	return LiftFromRustBuffer[AdmissionAuthorization](c, rb)
}

func (c FfiConverterAdmissionAuthorization) Read(reader io.Reader) AdmissionAuthorization {
	return AdmissionAuthorization{
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionAuthorization) Lower(value AdmissionAuthorization) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionAuthorization](c, value)
}

func (c FfiConverterAdmissionAuthorization) LowerExternal(value AdmissionAuthorization) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionAuthorization](c, value))
}

func (c FfiConverterAdmissionAuthorization) Write(writer io.Writer, value AdmissionAuthorization) {
	FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
	FfiConverterBytesINSTANCE.Write(writer, value.GrantSignature)
	FfiConverterBytesINSTANCE.Write(writer, value.RedemptionSignature)
}

type FfiDestroyerAdmissionAuthorization struct{}

func (_ FfiDestroyerAdmissionAuthorization) Destroy(value AdmissionAuthorization) {
	value.Destroy()
}

// The member's answer to a join request: pass `welcome` and a step made of
// `commit` and `authorization` to `stage_join`.
type AdmissionReply struct {
	Workspace     WorkspaceId
	Epoch         uint64
	Commit        []byte
	Welcome       []byte
	Authorization AdmissionAuthorization
}

func (r *AdmissionReply) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerBytes{}.Destroy(r.Commit)
	FfiDestroyerBytes{}.Destroy(r.Welcome)
	FfiDestroyerAdmissionAuthorization{}.Destroy(r.Authorization)
}

type FfiConverterAdmissionReply struct{}

var FfiConverterAdmissionReplyINSTANCE = FfiConverterAdmissionReply{}

func (c FfiConverterAdmissionReply) Lift(rb RustBufferI) AdmissionReply {
	return LiftFromRustBuffer[AdmissionReply](c, rb)
}

func (c FfiConverterAdmissionReply) Read(reader io.Reader) AdmissionReply {
	return AdmissionReply{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterAdmissionAuthorizationINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionReply) Lower(value AdmissionReply) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionReply](c, value)
}

func (c FfiConverterAdmissionReply) LowerExternal(value AdmissionReply) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionReply](c, value))
}

func (c FfiConverterAdmissionReply) Write(writer io.Writer, value AdmissionReply) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterBytesINSTANCE.Write(writer, value.Commit)
	FfiConverterBytesINSTANCE.Write(writer, value.Welcome)
	FfiConverterAdmissionAuthorizationINSTANCE.Write(writer, value.Authorization)
}

type FfiDestroyerAdmissionReply struct{}

func (_ FfiDestroyerAdmissionReply) Destroy(value AdmissionReply) {
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

type ConnectionCapacityMetrics struct {
	Evicted uint64
	Refused uint64
}

func (r *ConnectionCapacityMetrics) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Evicted)
	FfiDestroyerUint64{}.Destroy(r.Refused)
}

type FfiConverterConnectionCapacityMetrics struct{}

var FfiConverterConnectionCapacityMetricsINSTANCE = FfiConverterConnectionCapacityMetrics{}

func (c FfiConverterConnectionCapacityMetrics) Lift(rb RustBufferI) ConnectionCapacityMetrics {
	return LiftFromRustBuffer[ConnectionCapacityMetrics](c, rb)
}

func (c FfiConverterConnectionCapacityMetrics) Read(reader io.Reader) ConnectionCapacityMetrics {
	return ConnectionCapacityMetrics{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterConnectionCapacityMetrics) Lower(value ConnectionCapacityMetrics) C.RustBuffer {
	return LowerIntoRustBuffer[ConnectionCapacityMetrics](c, value)
}

func (c FfiConverterConnectionCapacityMetrics) LowerExternal(value ConnectionCapacityMetrics) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ConnectionCapacityMetrics](c, value))
}

func (c FfiConverterConnectionCapacityMetrics) Write(writer io.Writer, value ConnectionCapacityMetrics) {
	FfiConverterUint64INSTANCE.Write(writer, value.Evicted)
	FfiConverterUint64INSTANCE.Write(writer, value.Refused)
}

type FfiDestroyerConnectionCapacityMetrics struct{}

func (_ FfiDestroyerConnectionCapacityMetrics) Destroy(value ConnectionCapacityMetrics) {
	value.Destroy()
}

type ControlTimingMetrics struct {
	Inquiry     DurationSummary
	HostWait    DurationSummary
	HostService DurationSummary
}

func (r *ControlTimingMetrics) Destroy() {
	FfiDestroyerDurationSummary{}.Destroy(r.Inquiry)
	FfiDestroyerDurationSummary{}.Destroy(r.HostWait)
	FfiDestroyerDurationSummary{}.Destroy(r.HostService)
}

type FfiConverterControlTimingMetrics struct{}

var FfiConverterControlTimingMetricsINSTANCE = FfiConverterControlTimingMetrics{}

func (c FfiConverterControlTimingMetrics) Lift(rb RustBufferI) ControlTimingMetrics {
	return LiftFromRustBuffer[ControlTimingMetrics](c, rb)
}

func (c FfiConverterControlTimingMetrics) Read(reader io.Reader) ControlTimingMetrics {
	return ControlTimingMetrics{
		FfiConverterDurationSummaryINSTANCE.Read(reader),
		FfiConverterDurationSummaryINSTANCE.Read(reader),
		FfiConverterDurationSummaryINSTANCE.Read(reader),
	}
}

func (c FfiConverterControlTimingMetrics) Lower(value ControlTimingMetrics) C.RustBuffer {
	return LowerIntoRustBuffer[ControlTimingMetrics](c, value)
}

func (c FfiConverterControlTimingMetrics) LowerExternal(value ControlTimingMetrics) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ControlTimingMetrics](c, value))
}

func (c FfiConverterControlTimingMetrics) Write(writer io.Writer, value ControlTimingMetrics) {
	FfiConverterDurationSummaryINSTANCE.Write(writer, value.Inquiry)
	FfiConverterDurationSummaryINSTANCE.Write(writer, value.HostWait)
	FfiConverterDurationSummaryINSTANCE.Write(writer, value.HostService)
}

type FfiDestroyerControlTimingMetrics struct{}

func (_ FfiDestroyerControlTimingMetrics) Destroy(value ControlTimingMetrics) {
	value.Destroy()
}

type DeliveryFailure struct {
	Peer  EndpointId
	Error string
}

func (r *DeliveryFailure) Destroy() {
	FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerString{}.Destroy(r.Error)
}

type FfiConverterDeliveryFailure struct{}

var FfiConverterDeliveryFailureINSTANCE = FfiConverterDeliveryFailure{}

func (c FfiConverterDeliveryFailure) Lift(rb RustBufferI) DeliveryFailure {
	return LiftFromRustBuffer[DeliveryFailure](c, rb)
}

func (c FfiConverterDeliveryFailure) Read(reader io.Reader) DeliveryFailure {
	return DeliveryFailure{
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterDeliveryFailure) Lower(value DeliveryFailure) C.RustBuffer {
	return LowerIntoRustBuffer[DeliveryFailure](c, value)
}

func (c FfiConverterDeliveryFailure) LowerExternal(value DeliveryFailure) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DeliveryFailure](c, value))
}

func (c FfiConverterDeliveryFailure) Write(writer io.Writer, value DeliveryFailure) {
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterStringINSTANCE.Write(writer, value.Error)
}

type FfiDestroyerDeliveryFailure struct{}

func (_ FfiDestroyerDeliveryFailure) Destroy(value DeliveryFailure) {
	value.Destroy()
}

// Where a publication or interest change went.
type DeliveryReport struct {
	Admitted []EndpointId
	Queued   bool
	Failed   []DeliveryFailure
}

func (r *DeliveryReport) Destroy() {
	FfiDestroyerSequenceTypeEndpointId{}.Destroy(r.Admitted)
	FfiDestroyerBool{}.Destroy(r.Queued)
	FfiDestroyerSequenceDeliveryFailure{}.Destroy(r.Failed)
}

type FfiConverterDeliveryReport struct{}

var FfiConverterDeliveryReportINSTANCE = FfiConverterDeliveryReport{}

func (c FfiConverterDeliveryReport) Lift(rb RustBufferI) DeliveryReport {
	return LiftFromRustBuffer[DeliveryReport](c, rb)
}

func (c FfiConverterDeliveryReport) Read(reader io.Reader) DeliveryReport {
	return DeliveryReport{
		FfiConverterSequenceTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterSequenceDeliveryFailureINSTANCE.Read(reader),
	}
}

func (c FfiConverterDeliveryReport) Lower(value DeliveryReport) C.RustBuffer {
	return LowerIntoRustBuffer[DeliveryReport](c, value)
}

func (c FfiConverterDeliveryReport) LowerExternal(value DeliveryReport) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DeliveryReport](c, value))
}

func (c FfiConverterDeliveryReport) Write(writer io.Writer, value DeliveryReport) {
	FfiConverterSequenceTypeEndpointIdINSTANCE.Write(writer, value.Admitted)
	FfiConverterBoolINSTANCE.Write(writer, value.Queued)
	FfiConverterSequenceDeliveryFailureINSTANCE.Write(writer, value.Failed)
}

type FfiDestroyerDeliveryReport struct{}

func (_ FfiDestroyerDeliveryReport) Destroy(value DeliveryReport) {
	value.Destroy()
}

type DurationSummary struct {
	Count   uint64
	TotalUs uint64
	MaxUs   uint64
}

func (r *DurationSummary) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Count)
	FfiDestroyerUint64{}.Destroy(r.TotalUs)
	FfiDestroyerUint64{}.Destroy(r.MaxUs)
}

type FfiConverterDurationSummary struct{}

var FfiConverterDurationSummaryINSTANCE = FfiConverterDurationSummary{}

func (c FfiConverterDurationSummary) Lift(rb RustBufferI) DurationSummary {
	return LiftFromRustBuffer[DurationSummary](c, rb)
}

func (c FfiConverterDurationSummary) Read(reader io.Reader) DurationSummary {
	return DurationSummary{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterDurationSummary) Lower(value DurationSummary) C.RustBuffer {
	return LowerIntoRustBuffer[DurationSummary](c, value)
}

func (c FfiConverterDurationSummary) LowerExternal(value DurationSummary) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DurationSummary](c, value))
}

func (c FfiConverterDurationSummary) Write(writer io.Writer, value DurationSummary) {
	FfiConverterUint64INSTANCE.Write(writer, value.Count)
	FfiConverterUint64INSTANCE.Write(writer, value.TotalUs)
	FfiConverterUint64INSTANCE.Write(writer, value.MaxUs)
}

type FfiDestroyerDurationSummary struct{}

func (_ FfiDestroyerDurationSummary) Destroy(value DurationSummary) {
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

// The settled result of `set_interest`.
type InterestObservation struct {
	Workspace  WorkspaceId
	Revision   uint64
	Topic      string
	Subscribed bool
	Admission  DeliveryReport
}

func (r *InterestObservation) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerString{}.Destroy(r.Topic)
	FfiDestroyerBool{}.Destroy(r.Subscribed)
	FfiDestroyerDeliveryReport{}.Destroy(r.Admission)
}

type FfiConverterInterestObservation struct{}

var FfiConverterInterestObservationINSTANCE = FfiConverterInterestObservation{}

func (c FfiConverterInterestObservation) Lift(rb RustBufferI) InterestObservation {
	return LiftFromRustBuffer[InterestObservation](c, rb)
}

func (c FfiConverterInterestObservation) Read(reader io.Reader) InterestObservation {
	return InterestObservation{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterDeliveryReportINSTANCE.Read(reader),
	}
}

func (c FfiConverterInterestObservation) Lower(value InterestObservation) C.RustBuffer {
	return LowerIntoRustBuffer[InterestObservation](c, value)
}

func (c FfiConverterInterestObservation) LowerExternal(value InterestObservation) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InterestObservation](c, value))
}

func (c FfiConverterInterestObservation) Write(writer io.Writer, value InterestObservation) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	FfiConverterBoolINSTANCE.Write(writer, value.Subscribed)
	FfiConverterDeliveryReportINSTANCE.Write(writer, value.Admission)
}

type FfiDestroyerInterestObservation struct{}

func (_ FfiDestroyerInterestObservation) Destroy(value InterestObservation) {
	value.Destroy()
}

// A verified invitation checkpoint and the member that served it.
type InvitationCheckpoint struct {
	Workspace  WorkspaceId
	Checkpoint []byte
	Peer       EndpointId
}

func (r *InvitationCheckpoint) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerBytes{}.Destroy(r.Checkpoint)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
}

type FfiConverterInvitationCheckpoint struct{}

var FfiConverterInvitationCheckpointINSTANCE = FfiConverterInvitationCheckpoint{}

func (c FfiConverterInvitationCheckpoint) Lift(rb RustBufferI) InvitationCheckpoint {
	return LiftFromRustBuffer[InvitationCheckpoint](c, rb)
}

func (c FfiConverterInvitationCheckpoint) Read(reader io.Reader) InvitationCheckpoint {
	return InvitationCheckpoint{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
	}
}

func (c FfiConverterInvitationCheckpoint) Lower(value InvitationCheckpoint) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationCheckpoint](c, value)
}

func (c FfiConverterInvitationCheckpoint) LowerExternal(value InvitationCheckpoint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationCheckpoint](c, value))
}

func (c FfiConverterInvitationCheckpoint) Write(writer io.Writer, value InvitationCheckpoint) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterBytesINSTANCE.Write(writer, value.Checkpoint)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
}

type FfiDestroyerInvitationCheckpoint struct{}

func (_ FfiDestroyerInvitationCheckpoint) Destroy(value InvitationCheckpoint) {
	value.Destroy()
}

// One registered invitation link (read only; changing it is management).
type InvitationControl struct {
	Number        uint64
	Key           Key32
	ExpiresAt     uint64
	Enabled       bool
	Personal      bool
	Automatic     bool
	RequestAccess bool
	Approved      bool
}

func (r *InvitationControl) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Number)
	FfiDestroyerTypeKey32{}.Destroy(r.Key)
	FfiDestroyerUint64{}.Destroy(r.ExpiresAt)
	FfiDestroyerBool{}.Destroy(r.Enabled)
	FfiDestroyerBool{}.Destroy(r.Personal)
	FfiDestroyerBool{}.Destroy(r.Automatic)
	FfiDestroyerBool{}.Destroy(r.RequestAccess)
	FfiDestroyerBool{}.Destroy(r.Approved)
}

type FfiConverterInvitationControl struct{}

var FfiConverterInvitationControlINSTANCE = FfiConverterInvitationControl{}

func (c FfiConverterInvitationControl) Lift(rb RustBufferI) InvitationControl {
	return LiftFromRustBuffer[InvitationControl](c, rb)
}

func (c FfiConverterInvitationControl) Read(reader io.Reader) InvitationControl {
	return InvitationControl{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterInvitationControl) Lower(value InvitationControl) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationControl](c, value)
}

func (c FfiConverterInvitationControl) LowerExternal(value InvitationControl) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationControl](c, value))
}

func (c FfiConverterInvitationControl) Write(writer io.Writer, value InvitationControl) {
	FfiConverterUint64INSTANCE.Write(writer, value.Number)
	FfiConverterTypeKey32INSTANCE.Write(writer, value.Key)
	FfiConverterUint64INSTANCE.Write(writer, value.ExpiresAt)
	FfiConverterBoolINSTANCE.Write(writer, value.Enabled)
	FfiConverterBoolINSTANCE.Write(writer, value.Personal)
	FfiConverterBoolINSTANCE.Write(writer, value.Automatic)
	FfiConverterBoolINSTANCE.Write(writer, value.RequestAccess)
	FfiConverterBoolINSTANCE.Write(writer, value.Approved)
}

type FfiDestroyerInvitationControl struct{}

func (_ FfiDestroyerInvitationControl) Destroy(value InvitationControl) {
	value.Destroy()
}

// What an invitation link grants, checked against its checkpoint.
type InvitationDetails struct {
	Workspace     WorkspaceId
	InvitationKey Key32
	WorkspaceName *string
	Epoch         uint64
	Personal      bool
	Automatic     bool
	ExpiresAt     uint64
}

func (r *InvitationDetails) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerBool{}.Destroy(r.Personal)
	FfiDestroyerBool{}.Destroy(r.Automatic)
	FfiDestroyerUint64{}.Destroy(r.ExpiresAt)
}

type FfiConverterInvitationDetails struct{}

var FfiConverterInvitationDetailsINSTANCE = FfiConverterInvitationDetails{}

func (c FfiConverterInvitationDetails) Lift(rb RustBufferI) InvitationDetails {
	return LiftFromRustBuffer[InvitationDetails](c, rb)
}

func (c FfiConverterInvitationDetails) Read(reader io.Reader) InvitationDetails {
	return InvitationDetails{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterInvitationDetails) Lower(value InvitationDetails) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationDetails](c, value)
}

func (c FfiConverterInvitationDetails) LowerExternal(value InvitationDetails) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationDetails](c, value))
}

func (c FfiConverterInvitationDetails) Write(writer io.Writer, value InvitationDetails) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterBoolINSTANCE.Write(writer, value.Personal)
	FfiConverterBoolINSTANCE.Write(writer, value.Automatic)
	FfiConverterUint64INSTANCE.Write(writer, value.ExpiresAt)
}

type FfiDestroyerInvitationDetails struct{}

func (_ FfiDestroyerInvitationDetails) Destroy(value InvitationDetails) {
	value.Destroy()
}

// An adopted invitation link. `invitation` and `checkpoint` are secret
// bearer material: give them only to the invited person.
type InvitationInfo struct {
	Workspace      WorkspaceId
	WorkspaceName  *string
	Invitation     []byte
	InvitationKey  Key32
	Checkpoint     []byte
	Peer           EndpointId
	BootstrapPeers []EndpointId
	Address        string
	Routes         []RouteHint
}

func (r *InvitationInfo) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerBytes{}.Destroy(r.Invitation)
	FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
	FfiDestroyerBytes{}.Destroy(r.Checkpoint)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerSequenceTypeEndpointId{}.Destroy(r.BootstrapPeers)
	FfiDestroyerString{}.Destroy(r.Address)
	FfiDestroyerSequenceRouteHint{}.Destroy(r.Routes)
}

type FfiConverterInvitationInfo struct{}

var FfiConverterInvitationInfoINSTANCE = FfiConverterInvitationInfo{}

func (c FfiConverterInvitationInfo) Lift(rb RustBufferI) InvitationInfo {
	return LiftFromRustBuffer[InvitationInfo](c, rb)
}

func (c FfiConverterInvitationInfo) Read(reader io.Reader) InvitationInfo {
	return InvitationInfo{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterSequenceTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterSequenceRouteHintINSTANCE.Read(reader),
	}
}

func (c FfiConverterInvitationInfo) Lower(value InvitationInfo) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationInfo](c, value)
}

func (c FfiConverterInvitationInfo) LowerExternal(value InvitationInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationInfo](c, value))
}

func (c FfiConverterInvitationInfo) Write(writer io.Writer, value InvitationInfo) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterBytesINSTANCE.Write(writer, value.Invitation)
	FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
	FfiConverterBytesINSTANCE.Write(writer, value.Checkpoint)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterSequenceTypeEndpointIdINSTANCE.Write(writer, value.BootstrapPeers)
	FfiConverterStringINSTANCE.Write(writer, value.Address)
	FfiConverterSequenceRouteHintINSTANCE.Write(writer, value.Routes)
}

type FfiDestroyerInvitationInfo struct{}

func (_ FfiDestroyerInvitationInfo) Destroy(value InvitationInfo) {
	value.Destroy()
}

// One admission commit for `stage_join`.
type JoinAdmissionStep struct {
	Commit        []byte
	Authorization AdmissionAuthorization
}

func (r *JoinAdmissionStep) Destroy() {
	FfiDestroyerBytes{}.Destroy(r.Commit)
	FfiDestroyerAdmissionAuthorization{}.Destroy(r.Authorization)
}

type FfiConverterJoinAdmissionStep struct{}

var FfiConverterJoinAdmissionStepINSTANCE = FfiConverterJoinAdmissionStep{}

func (c FfiConverterJoinAdmissionStep) Lift(rb RustBufferI) JoinAdmissionStep {
	return LiftFromRustBuffer[JoinAdmissionStep](c, rb)
}

func (c FfiConverterJoinAdmissionStep) Read(reader io.Reader) JoinAdmissionStep {
	return JoinAdmissionStep{
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterAdmissionAuthorizationINSTANCE.Read(reader),
	}
}

func (c FfiConverterJoinAdmissionStep) Lower(value JoinAdmissionStep) C.RustBuffer {
	return LowerIntoRustBuffer[JoinAdmissionStep](c, value)
}

func (c FfiConverterJoinAdmissionStep) LowerExternal(value JoinAdmissionStep) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[JoinAdmissionStep](c, value))
}

func (c FfiConverterJoinAdmissionStep) Write(writer io.Writer, value JoinAdmissionStep) {
	FfiConverterBytesINSTANCE.Write(writer, value.Commit)
	FfiConverterAdmissionAuthorizationINSTANCE.Write(writer, value.Authorization)
}

type FfiDestroyerJoinAdmissionStep struct{}

func (_ FfiDestroyerJoinAdmissionStep) Destroy(value JoinAdmissionStep) {
	value.Destroy()
}

// A started join. Send `admission_request` to a member.
type JoinRequest struct {
	Workspace        WorkspaceId
	Member           MemberId
	Endpoint         EndpointId
	AdmissionRequest []byte
}

func (r *JoinRequest) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerBytes{}.Destroy(r.AdmissionRequest)
}

type FfiConverterJoinRequest struct{}

var FfiConverterJoinRequestINSTANCE = FfiConverterJoinRequest{}

func (c FfiConverterJoinRequest) Lift(rb RustBufferI) JoinRequest {
	return LiftFromRustBuffer[JoinRequest](c, rb)
}

func (c FfiConverterJoinRequest) Read(reader io.Reader) JoinRequest {
	return JoinRequest{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterJoinRequest) Lower(value JoinRequest) C.RustBuffer {
	return LowerIntoRustBuffer[JoinRequest](c, value)
}

func (c FfiConverterJoinRequest) LowerExternal(value JoinRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[JoinRequest](c, value))
}

func (c FfiConverterJoinRequest) Write(writer io.Writer, value JoinRequest) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterBytesINSTANCE.Write(writer, value.AdmissionRequest)
}

type FfiDestroyerJoinRequest struct{}

func (_ FfiDestroyerJoinRequest) Destroy(value JoinRequest) {
	value.Destroy()
}

// One member of the workspace.
type MemberInfo struct {
	Id                 MemberId
	Endpoint           EndpointId
	Administrator      bool
	SelfMember         bool
	DisplayName        *string
	Kind               MemberKind
	Presence           Presence
	LastContactAgeMs   *uint64
	PresenceFreshForMs *uint64
}

func (r *MemberInfo) Destroy() {
	FfiDestroyerTypeMemberId{}.Destroy(r.Id)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerBool{}.Destroy(r.Administrator)
	FfiDestroyerBool{}.Destroy(r.SelfMember)
	FfiDestroyerOptionalString{}.Destroy(r.DisplayName)
	FfiDestroyerMemberKind{}.Destroy(r.Kind)
	FfiDestroyerPresence{}.Destroy(r.Presence)
	FfiDestroyerOptionalUint64{}.Destroy(r.LastContactAgeMs)
	FfiDestroyerOptionalUint64{}.Destroy(r.PresenceFreshForMs)
}

type FfiConverterMemberInfo struct{}

var FfiConverterMemberInfoINSTANCE = FfiConverterMemberInfo{}

func (c FfiConverterMemberInfo) Lift(rb RustBufferI) MemberInfo {
	return LiftFromRustBuffer[MemberInfo](c, rb)
}

func (c FfiConverterMemberInfo) Read(reader io.Reader) MemberInfo {
	return MemberInfo{
		FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterMemberKindINSTANCE.Read(reader),
		FfiConverterPresenceINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterMemberInfo) Lower(value MemberInfo) C.RustBuffer {
	return LowerIntoRustBuffer[MemberInfo](c, value)
}

func (c FfiConverterMemberInfo) LowerExternal(value MemberInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MemberInfo](c, value))
}

func (c FfiConverterMemberInfo) Write(writer io.Writer, value MemberInfo) {
	FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Id)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterBoolINSTANCE.Write(writer, value.Administrator)
	FfiConverterBoolINSTANCE.Write(writer, value.SelfMember)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.DisplayName)
	FfiConverterMemberKindINSTANCE.Write(writer, value.Kind)
	FfiConverterPresenceINSTANCE.Write(writer, value.Presence)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.LastContactAgeMs)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.PresenceFreshForMs)
}

type FfiDestroyerMemberInfo struct{}

func (_ FfiDestroyerMemberInfo) Destroy(value MemberInfo) {
	value.Destroy()
}

// The workspace members.
type MemberRoster struct {
	Workspace             WorkspaceId
	WorkspaceName         *string
	WorkspaceNameRevision uint64
	WorkspaceNameHead     Key32
	Epoch                 uint64
	Members               []MemberInfo
	ProfileCount          uint64
	ProfilesRetained      bool
}

func (r *MemberRoster) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerUint64{}.Destroy(r.WorkspaceNameRevision)
	FfiDestroyerTypeKey32{}.Destroy(r.WorkspaceNameHead)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerSequenceMemberInfo{}.Destroy(r.Members)
	FfiDestroyerUint64{}.Destroy(r.ProfileCount)
	FfiDestroyerBool{}.Destroy(r.ProfilesRetained)
}

type FfiConverterMemberRoster struct{}

var FfiConverterMemberRosterINSTANCE = FfiConverterMemberRoster{}

func (c FfiConverterMemberRoster) Lift(rb RustBufferI) MemberRoster {
	return LiftFromRustBuffer[MemberRoster](c, rb)
}

func (c FfiConverterMemberRoster) Read(reader io.Reader) MemberRoster {
	return MemberRoster{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterSequenceMemberInfoINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterMemberRoster) Lower(value MemberRoster) C.RustBuffer {
	return LowerIntoRustBuffer[MemberRoster](c, value)
}

func (c FfiConverterMemberRoster) LowerExternal(value MemberRoster) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MemberRoster](c, value))
}

func (c FfiConverterMemberRoster) Write(writer io.Writer, value MemberRoster) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterUint64INSTANCE.Write(writer, value.WorkspaceNameRevision)
	FfiConverterTypeKey32INSTANCE.Write(writer, value.WorkspaceNameHead)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterSequenceMemberInfoINSTANCE.Write(writer, value.Members)
	FfiConverterUint64INSTANCE.Write(writer, value.ProfileCount)
	FfiConverterBoolINSTANCE.Write(writer, value.ProfilesRetained)
}

type FfiDestroyerMemberRoster struct{}

func (_ FfiDestroyerMemberRoster) Destroy(value MemberRoster) {
	value.Destroy()
}

type MembershipGossipMetrics struct {
	Sent        uint64
	NoOverlay   uint64
	Failed      uint64
	Received    uint64
	Staged      uint64
	Rejected    uint64
	RangePulled uint64
	RangeFailed uint64
}

func (r *MembershipGossipMetrics) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Sent)
	FfiDestroyerUint64{}.Destroy(r.NoOverlay)
	FfiDestroyerUint64{}.Destroy(r.Failed)
	FfiDestroyerUint64{}.Destroy(r.Received)
	FfiDestroyerUint64{}.Destroy(r.Staged)
	FfiDestroyerUint64{}.Destroy(r.Rejected)
	FfiDestroyerUint64{}.Destroy(r.RangePulled)
	FfiDestroyerUint64{}.Destroy(r.RangeFailed)
}

type FfiConverterMembershipGossipMetrics struct{}

var FfiConverterMembershipGossipMetricsINSTANCE = FfiConverterMembershipGossipMetrics{}

func (c FfiConverterMembershipGossipMetrics) Lift(rb RustBufferI) MembershipGossipMetrics {
	return LiftFromRustBuffer[MembershipGossipMetrics](c, rb)
}

func (c FfiConverterMembershipGossipMetrics) Read(reader io.Reader) MembershipGossipMetrics {
	return MembershipGossipMetrics{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterMembershipGossipMetrics) Lower(value MembershipGossipMetrics) C.RustBuffer {
	return LowerIntoRustBuffer[MembershipGossipMetrics](c, value)
}

func (c FfiConverterMembershipGossipMetrics) LowerExternal(value MembershipGossipMetrics) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MembershipGossipMetrics](c, value))
}

func (c FfiConverterMembershipGossipMetrics) Write(writer io.Writer, value MembershipGossipMetrics) {
	FfiConverterUint64INSTANCE.Write(writer, value.Sent)
	FfiConverterUint64INSTANCE.Write(writer, value.NoOverlay)
	FfiConverterUint64INSTANCE.Write(writer, value.Failed)
	FfiConverterUint64INSTANCE.Write(writer, value.Received)
	FfiConverterUint64INSTANCE.Write(writer, value.Staged)
	FfiConverterUint64INSTANCE.Write(writer, value.Rejected)
	FfiConverterUint64INSTANCE.Write(writer, value.RangePulled)
	FfiConverterUint64INSTANCE.Write(writer, value.RangeFailed)
}

type FfiDestroyerMembershipGossipMetrics struct{}

func (_ FfiDestroyerMembershipGossipMetrics) Destroy(value MembershipGossipMetrics) {
	value.Destroy()
}

type PeerRoute struct {
	Member MemberId
	Route  RouteKind
	RttMs  uint64
}

func (r *PeerRoute) Destroy() {
	FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	FfiDestroyerRouteKind{}.Destroy(r.Route)
	FfiDestroyerUint64{}.Destroy(r.RttMs)
}

type FfiConverterPeerRoute struct{}

var FfiConverterPeerRouteINSTANCE = FfiConverterPeerRoute{}

func (c FfiConverterPeerRoute) Lift(rb RustBufferI) PeerRoute {
	return LiftFromRustBuffer[PeerRoute](c, rb)
}

func (c FfiConverterPeerRoute) Read(reader io.Reader) PeerRoute {
	return PeerRoute{
		FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterRouteKindINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterPeerRoute) Lower(value PeerRoute) C.RustBuffer {
	return LowerIntoRustBuffer[PeerRoute](c, value)
}

func (c FfiConverterPeerRoute) LowerExternal(value PeerRoute) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PeerRoute](c, value))
}

func (c FfiConverterPeerRoute) Write(writer io.Writer, value PeerRoute) {
	FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	FfiConverterRouteKindINSTANCE.Write(writer, value.Route)
	FfiConverterUint64INSTANCE.Write(writer, value.RttMs)
}

type FfiDestroyerPeerRoute struct{}

func (_ FfiDestroyerPeerRoute) Destroy(value PeerRoute) {
	value.Destroy()
}

// The outcome of one presence round.
type PresenceRound struct {
	SyncPeer       *EndpointId
	ResponseErrors uint32
	ResponseError  *string
}

func (r *PresenceRound) Destroy() {
	FfiDestroyerOptionalTypeEndpointId{}.Destroy(r.SyncPeer)
	FfiDestroyerUint32{}.Destroy(r.ResponseErrors)
	FfiDestroyerOptionalString{}.Destroy(r.ResponseError)
}

type FfiConverterPresenceRound struct{}

var FfiConverterPresenceRoundINSTANCE = FfiConverterPresenceRound{}

func (c FfiConverterPresenceRound) Lift(rb RustBufferI) PresenceRound {
	return LiftFromRustBuffer[PresenceRound](c, rb)
}

func (c FfiConverterPresenceRound) Read(reader io.Reader) PresenceRound {
	return PresenceRound{
		FfiConverterOptionalTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterPresenceRound) Lower(value PresenceRound) C.RustBuffer {
	return LowerIntoRustBuffer[PresenceRound](c, value)
}

func (c FfiConverterPresenceRound) LowerExternal(value PresenceRound) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PresenceRound](c, value))
}

func (c FfiConverterPresenceRound) Write(writer io.Writer, value PresenceRound) {
	FfiConverterOptionalTypeEndpointIdINSTANCE.Write(writer, value.SyncPeer)
	FfiConverterUint32INSTANCE.Write(writer, value.ResponseErrors)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.ResponseError)
}

type FfiDestroyerPresenceRound struct{}

func (_ FfiDestroyerPresenceRound) Destroy(value PresenceRound) {
	value.Destroy()
}

// Current-value (latest-value) metadata of a protected publication.
type PublicationCurrent struct {
	Selector       Key32
	ReplacementKey Key32
	// Unix seconds (UTC), by the author's clock.
	ExpiresAt uint64
	Tombstone bool
}

func (r *PublicationCurrent) Destroy() {
	FfiDestroyerTypeKey32{}.Destroy(r.Selector)
	FfiDestroyerTypeKey32{}.Destroy(r.ReplacementKey)
	FfiDestroyerUint64{}.Destroy(r.ExpiresAt)
	FfiDestroyerBool{}.Destroy(r.Tombstone)
}

type FfiConverterPublicationCurrent struct{}

var FfiConverterPublicationCurrentINSTANCE = FfiConverterPublicationCurrent{}

func (c FfiConverterPublicationCurrent) Lift(rb RustBufferI) PublicationCurrent {
	return LiftFromRustBuffer[PublicationCurrent](c, rb)
}

func (c FfiConverterPublicationCurrent) Read(reader io.Reader) PublicationCurrent {
	return PublicationCurrent{
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterPublicationCurrent) Lower(value PublicationCurrent) C.RustBuffer {
	return LowerIntoRustBuffer[PublicationCurrent](c, value)
}

func (c FfiConverterPublicationCurrent) LowerExternal(value PublicationCurrent) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PublicationCurrent](c, value))
}

func (c FfiConverterPublicationCurrent) Write(writer io.Writer, value PublicationCurrent) {
	FfiConverterTypeKey32INSTANCE.Write(writer, value.Selector)
	FfiConverterTypeKey32INSTANCE.Write(writer, value.ReplacementKey)
	FfiConverterUint64INSTANCE.Write(writer, value.ExpiresAt)
	FfiConverterBoolINSTANCE.Write(writer, value.Tombstone)
}

type FfiDestroyerPublicationCurrent struct{}

func (_ FfiDestroyerPublicationCurrent) Destroy(value PublicationCurrent) {
	value.Destroy()
}

// An authenticated object in the durable inbox. It stays pending until an
// acknowledgement or rejection is adopted (at-least-once delivery).
type ReceivedPublication struct {
	Workspace  WorkspaceId
	Revision   uint64
	Member     MemberId
	Endpoint   EndpointId
	Topic      string
	Id         RecordId
	Sequence   *uint64
	Payload    []byte
	Recipients []MemberId
	// Author sender counter; identifies the object for acknowledgement.
	Counter uint64
	Current *PublicationCurrent
}

func (r *ReceivedPublication) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerString{}.Destroy(r.Topic)
	FfiDestroyerTypeRecordId{}.Destroy(r.Id)
	FfiDestroyerOptionalUint64{}.Destroy(r.Sequence)
	FfiDestroyerBytes{}.Destroy(r.Payload)
	FfiDestroyerSequenceTypeMemberId{}.Destroy(r.Recipients)
	FfiDestroyerUint64{}.Destroy(r.Counter)
	FfiDestroyerOptionalPublicationCurrent{}.Destroy(r.Current)
}

type FfiConverterReceivedPublication struct{}

var FfiConverterReceivedPublicationINSTANCE = FfiConverterReceivedPublication{}

func (c FfiConverterReceivedPublication) Lift(rb RustBufferI) ReceivedPublication {
	return LiftFromRustBuffer[ReceivedPublication](c, rb)
}

func (c FfiConverterReceivedPublication) Read(reader io.Reader) ReceivedPublication {
	return ReceivedPublication{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterTypeRecordIdINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterSequenceTypeMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterOptionalPublicationCurrentINSTANCE.Read(reader),
	}
}

func (c FfiConverterReceivedPublication) Lower(value ReceivedPublication) C.RustBuffer {
	return LowerIntoRustBuffer[ReceivedPublication](c, value)
}

func (c FfiConverterReceivedPublication) LowerExternal(value ReceivedPublication) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ReceivedPublication](c, value))
}

func (c FfiConverterReceivedPublication) Write(writer io.Writer, value ReceivedPublication) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	FfiConverterTypeRecordIdINSTANCE.Write(writer, value.Id)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Sequence)
	FfiConverterBytesINSTANCE.Write(writer, value.Payload)
	FfiConverterSequenceTypeMemberIdINSTANCE.Write(writer, value.Recipients)
	FfiConverterUint64INSTANCE.Write(writer, value.Counter)
	FfiConverterOptionalPublicationCurrentINSTANCE.Write(writer, value.Current)
}

type FfiDestroyerReceivedPublication struct{}

func (_ FfiDestroyerReceivedPublication) Destroy(value ReceivedPublication) {
	value.Destroy()
}

// An adopted recovery. `missing_publications` counts objects the source
// no longer had (a direct miss).
type RecoveryAdoption struct {
	Workspace             WorkspaceId
	Epoch                 uint64
	MemberCount           uint64
	Durable               bool
	RecoveredPublications uint64
	MissingPublications   uint64
}

func (r *RecoveryAdoption) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerUint64{}.Destroy(r.MemberCount)
	FfiDestroyerBool{}.Destroy(r.Durable)
	FfiDestroyerUint64{}.Destroy(r.RecoveredPublications)
	FfiDestroyerUint64{}.Destroy(r.MissingPublications)
}

type FfiConverterRecoveryAdoption struct{}

var FfiConverterRecoveryAdoptionINSTANCE = FfiConverterRecoveryAdoption{}

func (c FfiConverterRecoveryAdoption) Lift(rb RustBufferI) RecoveryAdoption {
	return LiftFromRustBuffer[RecoveryAdoption](c, rb)
}

func (c FfiConverterRecoveryAdoption) Read(reader io.Reader) RecoveryAdoption {
	return RecoveryAdoption{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterRecoveryAdoption) Lower(value RecoveryAdoption) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryAdoption](c, value)
}

func (c FfiConverterRecoveryAdoption) LowerExternal(value RecoveryAdoption) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryAdoption](c, value))
}

func (c FfiConverterRecoveryAdoption) Write(writer io.Writer, value RecoveryAdoption) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterUint64INSTANCE.Write(writer, value.MemberCount)
	FfiConverterBoolINSTANCE.Write(writer, value.Durable)
	FfiConverterUint64INSTANCE.Write(writer, value.RecoveredPublications)
	FfiConverterUint64INSTANCE.Write(writer, value.MissingPublications)
}

type FfiDestroyerRecoveryAdoption struct{}

func (_ FfiDestroyerRecoveryAdoption) Destroy(value RecoveryAdoption) {
	value.Destroy()
}

// A fetched range, ready to stage.
type RecoveryRangeReady struct {
	Workspace       WorkspaceId
	Author          MemberId
	Peer            EndpointId
	Epoch           uint64
	Revision        uint64
	After           uint64
	Through         uint64
	PacketCount     uint64
	RetainedBytes   uint64
	AutomaticSource bool
	Attempted       *uint64
}

func (r *RecoveryRangeReady) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerTypeMemberId{}.Destroy(r.Author)
	FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerUint64{}.Destroy(r.After)
	FfiDestroyerUint64{}.Destroy(r.Through)
	FfiDestroyerUint64{}.Destroy(r.PacketCount)
	FfiDestroyerUint64{}.Destroy(r.RetainedBytes)
	FfiDestroyerBool{}.Destroy(r.AutomaticSource)
	FfiDestroyerOptionalUint64{}.Destroy(r.Attempted)
}

type FfiConverterRecoveryRangeReady struct{}

var FfiConverterRecoveryRangeReadyINSTANCE = FfiConverterRecoveryRangeReady{}

func (c FfiConverterRecoveryRangeReady) Lift(rb RustBufferI) RecoveryRangeReady {
	return LiftFromRustBuffer[RecoveryRangeReady](c, rb)
}

func (c FfiConverterRecoveryRangeReady) Read(reader io.Reader) RecoveryRangeReady {
	return RecoveryRangeReady{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterRecoveryRangeReady) Lower(value RecoveryRangeReady) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryRangeReady](c, value)
}

func (c FfiConverterRecoveryRangeReady) LowerExternal(value RecoveryRangeReady) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryRangeReady](c, value))
}

func (c FfiConverterRecoveryRangeReady) Write(writer io.Writer, value RecoveryRangeReady) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Author)
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterUint64INSTANCE.Write(writer, value.After)
	FfiConverterUint64INSTANCE.Write(writer, value.Through)
	FfiConverterUint64INSTANCE.Write(writer, value.PacketCount)
	FfiConverterUint64INSTANCE.Write(writer, value.RetainedBytes)
	FfiConverterBoolINSTANCE.Write(writer, value.AutomaticSource)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Attempted)
}

type FfiDestroyerRecoveryRangeReady struct{}

func (_ FfiDestroyerRecoveryRangeReady) Destroy(value RecoveryRangeReady) {
	value.Destroy()
}

// Which range of an author's objects to fetch from a peer.
type RecoveryRangeRequest struct {
	Peer     *EndpointId
	Author   *MemberId
	Revision uint64
	Topics   []string
	After    *uint64
	Through  *uint64
}

func (r *RecoveryRangeRequest) Destroy() {
	FfiDestroyerOptionalTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerOptionalTypeMemberId{}.Destroy(r.Author)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerSequenceString{}.Destroy(r.Topics)
	FfiDestroyerOptionalUint64{}.Destroy(r.After)
	FfiDestroyerOptionalUint64{}.Destroy(r.Through)
}

type FfiConverterRecoveryRangeRequest struct{}

var FfiConverterRecoveryRangeRequestINSTANCE = FfiConverterRecoveryRangeRequest{}

func (c FfiConverterRecoveryRangeRequest) Lift(rb RustBufferI) RecoveryRangeRequest {
	return LiftFromRustBuffer[RecoveryRangeRequest](c, rb)
}

func (c FfiConverterRecoveryRangeRequest) Read(reader io.Reader) RecoveryRangeRequest {
	return RecoveryRangeRequest{
		FfiConverterOptionalTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalTypeMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterSequenceStringINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterRecoveryRangeRequest) Lower(value RecoveryRangeRequest) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryRangeRequest](c, value)
}

func (c FfiConverterRecoveryRangeRequest) LowerExternal(value RecoveryRangeRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryRangeRequest](c, value))
}

func (c FfiConverterRecoveryRangeRequest) Write(writer io.Writer, value RecoveryRangeRequest) {
	FfiConverterOptionalTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterOptionalTypeMemberIdINSTANCE.Write(writer, value.Author)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Topics)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.After)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Through)
}

type FfiDestroyerRecoveryRangeRequest struct{}

func (_ FfiDestroyerRecoveryRangeRequest) Destroy(value RecoveryRangeRequest) {
	value.Destroy()
}

type RouteHint struct {
	Peer    EndpointId
	Address string
}

func (r *RouteHint) Destroy() {
	FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerString{}.Destroy(r.Address)
}

type FfiConverterRouteHint struct{}

var FfiConverterRouteHintINSTANCE = FfiConverterRouteHint{}

func (c FfiConverterRouteHint) Lift(rb RustBufferI) RouteHint {
	return LiftFromRustBuffer[RouteHint](c, rb)
}

func (c FfiConverterRouteHint) Read(reader io.Reader) RouteHint {
	return RouteHint{
		FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterRouteHint) Lower(value RouteHint) C.RustBuffer {
	return LowerIntoRustBuffer[RouteHint](c, value)
}

func (c FfiConverterRouteHint) LowerExternal(value RouteHint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RouteHint](c, value))
}

func (c FfiConverterRouteHint) Write(writer io.Writer, value RouteHint) {
	FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterStringINSTANCE.Write(writer, value.Address)
}

type FfiDestroyerRouteHint struct{}

func (_ FfiDestroyerRouteHint) Destroy(value RouteHint) {
	value.Destroy()
}

// A workspace this client created or joined.
type WorkspaceInfo struct {
	Workspace     WorkspaceId
	WorkspaceName *string
	Epoch         uint64
	MemberCount   uint64
	Durable       bool
	Phase         WorkspacePhase
	Reason        *string
}

func (r *WorkspaceInfo) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerUint64{}.Destroy(r.MemberCount)
	FfiDestroyerBool{}.Destroy(r.Durable)
	FfiDestroyerWorkspacePhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
}

type FfiConverterWorkspaceInfo struct{}

var FfiConverterWorkspaceInfoINSTANCE = FfiConverterWorkspaceInfo{}

func (c FfiConverterWorkspaceInfo) Lift(rb RustBufferI) WorkspaceInfo {
	return LiftFromRustBuffer[WorkspaceInfo](c, rb)
}

func (c FfiConverterWorkspaceInfo) Read(reader io.Reader) WorkspaceInfo {
	return WorkspaceInfo{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterWorkspacePhaseINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterWorkspaceInfo) Lower(value WorkspaceInfo) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspaceInfo](c, value)
}

func (c FfiConverterWorkspaceInfo) LowerExternal(value WorkspaceInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspaceInfo](c, value))
}

func (c FfiConverterWorkspaceInfo) Write(writer io.Writer, value WorkspaceInfo) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterUint64INSTANCE.Write(writer, value.MemberCount)
	FfiConverterBoolINSTANCE.Write(writer, value.Durable)
	FfiConverterWorkspacePhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
}

type FfiDestroyerWorkspaceInfo struct{}

func (_ FfiDestroyerWorkspaceInfo) Destroy(value WorkspaceInfo) {
	value.Destroy()
}

// A local, read-only snapshot of workspace counters (diagnostics only).
type WorkspaceMetrics struct {
	Workspace           WorkspaceId
	Phase               WorkspacePhase
	Reason              *string
	ReceivedBytes       uint64
	SentBytes           uint64
	ReceiveQueue        uint64
	AdmissionQueue      uint64
	AdmissionQueueBytes uint64
	AdmissionWaiters    uint64
	AdmissionInFlight   uint64
	ApprovalPending     uint64
	PendingObjects      uint64
	RepairJobs          uint64
	GossipNeighbors     uint64
	ControlTiming       ControlTimingMetrics
	MembershipGossip    MembershipGossipMetrics
	ConnectionCapacity  ConnectionCapacityMetrics
	Paths               []PeerRoute
	PathsLimited        bool
}

func (r *WorkspaceMetrics) Destroy() {
	FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerWorkspacePhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
	FfiDestroyerUint64{}.Destroy(r.ReceivedBytes)
	FfiDestroyerUint64{}.Destroy(r.SentBytes)
	FfiDestroyerUint64{}.Destroy(r.ReceiveQueue)
	FfiDestroyerUint64{}.Destroy(r.AdmissionQueue)
	FfiDestroyerUint64{}.Destroy(r.AdmissionQueueBytes)
	FfiDestroyerUint64{}.Destroy(r.AdmissionWaiters)
	FfiDestroyerUint64{}.Destroy(r.AdmissionInFlight)
	FfiDestroyerUint64{}.Destroy(r.ApprovalPending)
	FfiDestroyerUint64{}.Destroy(r.PendingObjects)
	FfiDestroyerUint64{}.Destroy(r.RepairJobs)
	FfiDestroyerUint64{}.Destroy(r.GossipNeighbors)
	FfiDestroyerControlTimingMetrics{}.Destroy(r.ControlTiming)
	FfiDestroyerMembershipGossipMetrics{}.Destroy(r.MembershipGossip)
	FfiDestroyerConnectionCapacityMetrics{}.Destroy(r.ConnectionCapacity)
	FfiDestroyerSequencePeerRoute{}.Destroy(r.Paths)
	FfiDestroyerBool{}.Destroy(r.PathsLimited)
}

type FfiConverterWorkspaceMetrics struct{}

var FfiConverterWorkspaceMetricsINSTANCE = FfiConverterWorkspaceMetrics{}

func (c FfiConverterWorkspaceMetrics) Lift(rb RustBufferI) WorkspaceMetrics {
	return LiftFromRustBuffer[WorkspaceMetrics](c, rb)
}

func (c FfiConverterWorkspaceMetrics) Read(reader io.Reader) WorkspaceMetrics {
	return WorkspaceMetrics{
		FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterWorkspacePhaseINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterControlTimingMetricsINSTANCE.Read(reader),
		FfiConverterMembershipGossipMetricsINSTANCE.Read(reader),
		FfiConverterConnectionCapacityMetricsINSTANCE.Read(reader),
		FfiConverterSequencePeerRouteINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterWorkspaceMetrics) Lower(value WorkspaceMetrics) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspaceMetrics](c, value)
}

func (c FfiConverterWorkspaceMetrics) LowerExternal(value WorkspaceMetrics) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspaceMetrics](c, value))
}

func (c FfiConverterWorkspaceMetrics) Write(writer io.Writer, value WorkspaceMetrics) {
	FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterWorkspacePhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
	FfiConverterUint64INSTANCE.Write(writer, value.ReceivedBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.SentBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.ReceiveQueue)
	FfiConverterUint64INSTANCE.Write(writer, value.AdmissionQueue)
	FfiConverterUint64INSTANCE.Write(writer, value.AdmissionQueueBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.AdmissionWaiters)
	FfiConverterUint64INSTANCE.Write(writer, value.AdmissionInFlight)
	FfiConverterUint64INSTANCE.Write(writer, value.ApprovalPending)
	FfiConverterUint64INSTANCE.Write(writer, value.PendingObjects)
	FfiConverterUint64INSTANCE.Write(writer, value.RepairJobs)
	FfiConverterUint64INSTANCE.Write(writer, value.GossipNeighbors)
	FfiConverterControlTimingMetricsINSTANCE.Write(writer, value.ControlTiming)
	FfiConverterMembershipGossipMetricsINSTANCE.Write(writer, value.MembershipGossip)
	FfiConverterConnectionCapacityMetricsINSTANCE.Write(writer, value.ConnectionCapacity)
	FfiConverterSequencePeerRouteINSTANCE.Write(writer, value.Paths)
	FfiConverterBoolINSTANCE.Write(writer, value.PathsLimited)
}

type FfiDestroyerWorkspaceMetrics struct{}

func (_ FfiDestroyerWorkspaceMetrics) Destroy(value WorkspaceMetrics) {
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

// The kind of invitation link to register.
type InvitationKind uint

const (
	// Anyone with the link may join until it expires or is disabled.
	InvitationKindReusable InvitationKind = 1
	// One person; an administrator approves the first join request.
	InvitationKindPersonal InvitationKind = 2
	// One person; the first join request is approved automatically.
	InvitationKindPersonalAutomatic InvitationKind = 3
	// One person asks for access; an administrator approves or declines.
	InvitationKindRequestAccess InvitationKind = 4
)

type FfiConverterInvitationKind struct{}

var FfiConverterInvitationKindINSTANCE = FfiConverterInvitationKind{}

func (c FfiConverterInvitationKind) Lift(rb RustBufferI) InvitationKind {
	return LiftFromRustBuffer[InvitationKind](c, rb)
}

func (c FfiConverterInvitationKind) Lower(value InvitationKind) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationKind](c, value)
}

func (c FfiConverterInvitationKind) LowerExternal(value InvitationKind) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationKind](c, value))
}
func (FfiConverterInvitationKind) Read(reader io.Reader) InvitationKind {
	id := readInt32(reader)
	return InvitationKind(id)
}

func (FfiConverterInvitationKind) Write(writer io.Writer, value InvitationKind) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerInvitationKind struct{}

func (_ FfiDestroyerInvitationKind) Destroy(value InvitationKind) {
}

type MemberKind uint

const (
	MemberKindPerson  MemberKind = 1
	MemberKindService MemberKind = 2
)

type FfiConverterMemberKind struct{}

var FfiConverterMemberKindINSTANCE = FfiConverterMemberKind{}

func (c FfiConverterMemberKind) Lift(rb RustBufferI) MemberKind {
	return LiftFromRustBuffer[MemberKind](c, rb)
}

func (c FfiConverterMemberKind) Lower(value MemberKind) C.RustBuffer {
	return LowerIntoRustBuffer[MemberKind](c, value)
}

func (c FfiConverterMemberKind) LowerExternal(value MemberKind) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MemberKind](c, value))
}
func (FfiConverterMemberKind) Read(reader io.Reader) MemberKind {
	id := readInt32(reader)
	return MemberKind(id)
}

func (FfiConverterMemberKind) Write(writer io.Writer, value MemberKind) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerMemberKind struct{}

func (_ FfiDestroyerMemberKind) Destroy(value MemberKind) {
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

type Presence uint

const (
	PresenceSelfMember Presence = 1
	PresenceUnknown    Presence = 2
	PresenceReachable  Presence = 3
	PresenceStale      Presence = 4
)

type FfiConverterPresence struct{}

var FfiConverterPresenceINSTANCE = FfiConverterPresence{}

func (c FfiConverterPresence) Lift(rb RustBufferI) Presence {
	return LiftFromRustBuffer[Presence](c, rb)
}

func (c FfiConverterPresence) Lower(value Presence) C.RustBuffer {
	return LowerIntoRustBuffer[Presence](c, value)
}

func (c FfiConverterPresence) LowerExternal(value Presence) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Presence](c, value))
}
func (FfiConverterPresence) Read(reader io.Reader) Presence {
	id := readInt32(reader)
	return Presence(id)
}

func (FfiConverterPresence) Write(writer io.Writer, value Presence) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerPresence struct{}

func (_ FfiDestroyerPresence) Destroy(value Presence) {
}

// The state of a recovery range fetch.
type RecoveryRangeStatus interface {
	Destroy()
}
type RecoveryRangeStatusPending struct {
	CandidateCount  uint64
	AutomaticSource bool
}

func (e RecoveryRangeStatusPending) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.CandidateCount)
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
}

type RecoveryRangeStatusReady struct {
	Range RecoveryRangeReady
}

func (e RecoveryRangeStatusReady) Destroy() {
	FfiDestroyerRecoveryRangeReady{}.Destroy(e.Range)
}

type RecoveryRangeStatusSourceWaiting struct {
	AutomaticSource bool
}

func (e RecoveryRangeStatusSourceWaiting) Destroy() {
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
}

type RecoveryRangeStatusSourceUnavailable struct {
	Attempted       uint64
	Reason          string
	AutomaticSource bool
}

func (e RecoveryRangeStatusSourceUnavailable) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Attempted)
	FfiDestroyerString{}.Destroy(e.Reason)
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
}

type RecoveryRangeStatusRejected struct {
	Reason string
}

func (e RecoveryRangeStatusRejected) Destroy() {
	FfiDestroyerString{}.Destroy(e.Reason)
}

type RecoveryRangeStatusCancelled struct {
}

func (e RecoveryRangeStatusCancelled) Destroy() {
}

type FfiConverterRecoveryRangeStatus struct{}

var FfiConverterRecoveryRangeStatusINSTANCE = FfiConverterRecoveryRangeStatus{}

func (c FfiConverterRecoveryRangeStatus) Lift(rb RustBufferI) RecoveryRangeStatus {
	return LiftFromRustBuffer[RecoveryRangeStatus](c, rb)
}

func (c FfiConverterRecoveryRangeStatus) Lower(value RecoveryRangeStatus) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryRangeStatus](c, value)
}

func (c FfiConverterRecoveryRangeStatus) LowerExternal(value RecoveryRangeStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryRangeStatus](c, value))
}
func (FfiConverterRecoveryRangeStatus) Read(reader io.Reader) RecoveryRangeStatus {
	id := readInt32(reader)
	switch id {
	case 1:
		return RecoveryRangeStatusPending{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterBoolINSTANCE.Read(reader),
		}
	case 2:
		return RecoveryRangeStatusReady{
			FfiConverterRecoveryRangeReadyINSTANCE.Read(reader),
		}
	case 3:
		return RecoveryRangeStatusSourceWaiting{
			FfiConverterBoolINSTANCE.Read(reader),
		}
	case 4:
		return RecoveryRangeStatusSourceUnavailable{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterBoolINSTANCE.Read(reader),
		}
	case 5:
		return RecoveryRangeStatusRejected{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 6:
		return RecoveryRangeStatusCancelled{}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRecoveryRangeStatus.Read()", id))
	}
}

func (FfiConverterRecoveryRangeStatus) Write(writer io.Writer, value RecoveryRangeStatus) {
	switch variant_value := value.(type) {
	case RecoveryRangeStatusPending:
		writeInt32(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.CandidateCount)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
	case RecoveryRangeStatusReady:
		writeInt32(writer, 2)
		FfiConverterRecoveryRangeReadyINSTANCE.Write(writer, variant_value.Range)
	case RecoveryRangeStatusSourceWaiting:
		writeInt32(writer, 3)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
	case RecoveryRangeStatusSourceUnavailable:
		writeInt32(writer, 4)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Attempted)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Reason)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
	case RecoveryRangeStatusRejected:
		writeInt32(writer, 5)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Reason)
	case RecoveryRangeStatusCancelled:
		writeInt32(writer, 6)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRecoveryRangeStatus.Write", value))
	}
}

type FfiDestroyerRecoveryRangeStatus struct{}

func (_ FfiDestroyerRecoveryRangeStatus) Destroy(value RecoveryRangeStatus) {
	value.Destroy()
}

// The result of `stage_recovery_range`.
type RecoveryStage interface {
	Destroy()
}
type RecoveryStageCandidate struct {
	Candidate *RecoveryCandidate
}

func (e RecoveryStageCandidate) Destroy() {
	FfiDestroyerRecoveryCandidate{}.Destroy(e.Candidate)
}

type RecoveryStageAlreadyCovered struct {
}

func (e RecoveryStageAlreadyCovered) Destroy() {
}

type RecoveryStageNoNewObjects struct {
}

func (e RecoveryStageNoNewObjects) Destroy() {
}

// Nothing fits the pending bounds until the application acknowledges
// or rejects pending objects. Drain the inbox, then ask again.
type RecoveryStageAwaitingApplication struct {
}

func (e RecoveryStageAwaitingApplication) Destroy() {
}

type FfiConverterRecoveryStage struct{}

var FfiConverterRecoveryStageINSTANCE = FfiConverterRecoveryStage{}

func (c FfiConverterRecoveryStage) Lift(rb RustBufferI) RecoveryStage {
	return LiftFromRustBuffer[RecoveryStage](c, rb)
}

func (c FfiConverterRecoveryStage) Lower(value RecoveryStage) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryStage](c, value)
}

func (c FfiConverterRecoveryStage) LowerExternal(value RecoveryStage) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryStage](c, value))
}
func (FfiConverterRecoveryStage) Read(reader io.Reader) RecoveryStage {
	id := readInt32(reader)
	switch id {
	case 1:
		return RecoveryStageCandidate{
			FfiConverterRecoveryCandidateINSTANCE.Read(reader),
		}
	case 2:
		return RecoveryStageAlreadyCovered{}
	case 3:
		return RecoveryStageNoNewObjects{}
	case 4:
		return RecoveryStageAwaitingApplication{}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRecoveryStage.Read()", id))
	}
}

func (FfiConverterRecoveryStage) Write(writer io.Writer, value RecoveryStage) {
	switch variant_value := value.(type) {
	case RecoveryStageCandidate:
		writeInt32(writer, 1)
		FfiConverterRecoveryCandidateINSTANCE.Write(writer, variant_value.Candidate)
	case RecoveryStageAlreadyCovered:
		writeInt32(writer, 2)
	case RecoveryStageNoNewObjects:
		writeInt32(writer, 3)
	case RecoveryStageAwaitingApplication:
		writeInt32(writer, 4)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRecoveryStage.Write", value))
	}
}

type FfiDestroyerRecoveryStage struct{}

func (_ FfiDestroyerRecoveryStage) Destroy(value RecoveryStage) {
	value.Destroy()
}

type RouteKind interface {
	Destroy()
}
type RouteKindDirect struct {
}

func (e RouteKindDirect) Destroy() {
}

type RouteKindRelay struct {
}

func (e RouteKindRelay) Destroy() {
}

type RouteKindTor struct {
}

func (e RouteKindTor) Destroy() {
}

type RouteKindCustom struct {
	Name string
}

func (e RouteKindCustom) Destroy() {
	FfiDestroyerString{}.Destroy(e.Name)
}

type FfiConverterRouteKind struct{}

var FfiConverterRouteKindINSTANCE = FfiConverterRouteKind{}

func (c FfiConverterRouteKind) Lift(rb RustBufferI) RouteKind {
	return LiftFromRustBuffer[RouteKind](c, rb)
}

func (c FfiConverterRouteKind) Lower(value RouteKind) C.RustBuffer {
	return LowerIntoRustBuffer[RouteKind](c, value)
}

func (c FfiConverterRouteKind) LowerExternal(value RouteKind) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RouteKind](c, value))
}
func (FfiConverterRouteKind) Read(reader io.Reader) RouteKind {
	id := readInt32(reader)
	switch id {
	case 1:
		return RouteKindDirect{}
	case 2:
		return RouteKindRelay{}
	case 3:
		return RouteKindTor{}
	case 4:
		return RouteKindCustom{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRouteKind.Read()", id))
	}
}

func (FfiConverterRouteKind) Write(writer io.Writer, value RouteKind) {
	switch variant_value := value.(type) {
	case RouteKindDirect:
		writeInt32(writer, 1)
	case RouteKindRelay:
		writeInt32(writer, 2)
	case RouteKindTor:
		writeInt32(writer, 3)
	case RouteKindCustom:
		writeInt32(writer, 4)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Name)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRouteKind.Write", value))
	}
}

type FfiDestroyerRouteKind struct{}

func (_ FfiDestroyerRouteKind) Destroy(value RouteKind) {
	value.Destroy()
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

type FfiConverterOptionalUint32 struct{}

var FfiConverterOptionalUint32INSTANCE = FfiConverterOptionalUint32{}

func (c FfiConverterOptionalUint32) Lift(rb RustBufferI) *uint32 {
	return LiftFromRustBuffer[*uint32](c, rb)
}

func (_ FfiConverterOptionalUint32) Read(reader io.Reader) *uint32 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint32INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint32) Lower(value *uint32) C.RustBuffer {
	return LowerIntoRustBuffer[*uint32](c, value)
}

func (c FfiConverterOptionalUint32) LowerExternal(value *uint32) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*uint32](c, value))
}

func (_ FfiConverterOptionalUint32) Write(writer io.Writer, value *uint32) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint32INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint32 struct{}

func (_ FfiDestroyerOptionalUint32) Destroy(value *uint32) {
	if value != nil {
		FfiDestroyerUint32{}.Destroy(*value)
	}
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

type FfiConverterOptionalReceptionCandidate struct{}

var FfiConverterOptionalReceptionCandidateINSTANCE = FfiConverterOptionalReceptionCandidate{}

func (c FfiConverterOptionalReceptionCandidate) Lift(rb RustBufferI) **ReceptionCandidate {
	return LiftFromRustBuffer[**ReceptionCandidate](c, rb)
}

func (_ FfiConverterOptionalReceptionCandidate) Read(reader io.Reader) **ReceptionCandidate {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterReceptionCandidateINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalReceptionCandidate) Lower(value **ReceptionCandidate) C.RustBuffer {
	return LowerIntoRustBuffer[**ReceptionCandidate](c, value)
}

func (c FfiConverterOptionalReceptionCandidate) LowerExternal(value **ReceptionCandidate) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**ReceptionCandidate](c, value))
}

func (_ FfiConverterOptionalReceptionCandidate) Write(writer io.Writer, value **ReceptionCandidate) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterReceptionCandidateINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalReceptionCandidate struct{}

func (_ FfiDestroyerOptionalReceptionCandidate) Destroy(value **ReceptionCandidate) {
	if value != nil {
		FfiDestroyerReceptionCandidate{}.Destroy(*value)
	}
}

type FfiConverterOptionalInterestObservation struct{}

var FfiConverterOptionalInterestObservationINSTANCE = FfiConverterOptionalInterestObservation{}

func (c FfiConverterOptionalInterestObservation) Lift(rb RustBufferI) *InterestObservation {
	return LiftFromRustBuffer[*InterestObservation](c, rb)
}

func (_ FfiConverterOptionalInterestObservation) Read(reader io.Reader) *InterestObservation {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterInterestObservationINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalInterestObservation) Lower(value *InterestObservation) C.RustBuffer {
	return LowerIntoRustBuffer[*InterestObservation](c, value)
}

func (c FfiConverterOptionalInterestObservation) LowerExternal(value *InterestObservation) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*InterestObservation](c, value))
}

func (_ FfiConverterOptionalInterestObservation) Write(writer io.Writer, value *InterestObservation) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterInterestObservationINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalInterestObservation struct{}

func (_ FfiDestroyerOptionalInterestObservation) Destroy(value *InterestObservation) {
	if value != nil {
		FfiDestroyerInterestObservation{}.Destroy(*value)
	}
}

type FfiConverterOptionalPublicationCurrent struct{}

var FfiConverterOptionalPublicationCurrentINSTANCE = FfiConverterOptionalPublicationCurrent{}

func (c FfiConverterOptionalPublicationCurrent) Lift(rb RustBufferI) *PublicationCurrent {
	return LiftFromRustBuffer[*PublicationCurrent](c, rb)
}

func (_ FfiConverterOptionalPublicationCurrent) Read(reader io.Reader) *PublicationCurrent {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterPublicationCurrentINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalPublicationCurrent) Lower(value *PublicationCurrent) C.RustBuffer {
	return LowerIntoRustBuffer[*PublicationCurrent](c, value)
}

func (c FfiConverterOptionalPublicationCurrent) LowerExternal(value *PublicationCurrent) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*PublicationCurrent](c, value))
}

func (_ FfiConverterOptionalPublicationCurrent) Write(writer io.Writer, value *PublicationCurrent) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterPublicationCurrentINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalPublicationCurrent struct{}

func (_ FfiDestroyerOptionalPublicationCurrent) Destroy(value *PublicationCurrent) {
	if value != nil {
		FfiDestroyerPublicationCurrent{}.Destroy(*value)
	}
}

type FfiConverterOptionalReceivedPublication struct{}

var FfiConverterOptionalReceivedPublicationINSTANCE = FfiConverterOptionalReceivedPublication{}

func (c FfiConverterOptionalReceivedPublication) Lift(rb RustBufferI) *ReceivedPublication {
	return LiftFromRustBuffer[*ReceivedPublication](c, rb)
}

func (_ FfiConverterOptionalReceivedPublication) Read(reader io.Reader) *ReceivedPublication {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterReceivedPublicationINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalReceivedPublication) Lower(value *ReceivedPublication) C.RustBuffer {
	return LowerIntoRustBuffer[*ReceivedPublication](c, value)
}

func (c FfiConverterOptionalReceivedPublication) LowerExternal(value *ReceivedPublication) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*ReceivedPublication](c, value))
}

func (_ FfiConverterOptionalReceivedPublication) Write(writer io.Writer, value *ReceivedPublication) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterReceivedPublicationINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalReceivedPublication struct{}

func (_ FfiDestroyerOptionalReceivedPublication) Destroy(value *ReceivedPublication) {
	if value != nil {
		FfiDestroyerReceivedPublication{}.Destroy(*value)
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

type FfiConverterOptionalRecoveryRangeStatus struct{}

var FfiConverterOptionalRecoveryRangeStatusINSTANCE = FfiConverterOptionalRecoveryRangeStatus{}

func (c FfiConverterOptionalRecoveryRangeStatus) Lift(rb RustBufferI) *RecoveryRangeStatus {
	return LiftFromRustBuffer[*RecoveryRangeStatus](c, rb)
}

func (_ FfiConverterOptionalRecoveryRangeStatus) Read(reader io.Reader) *RecoveryRangeStatus {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterRecoveryRangeStatusINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalRecoveryRangeStatus) Lower(value *RecoveryRangeStatus) C.RustBuffer {
	return LowerIntoRustBuffer[*RecoveryRangeStatus](c, value)
}

func (c FfiConverterOptionalRecoveryRangeStatus) LowerExternal(value *RecoveryRangeStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*RecoveryRangeStatus](c, value))
}

func (_ FfiConverterOptionalRecoveryRangeStatus) Write(writer io.Writer, value *RecoveryRangeStatus) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterRecoveryRangeStatusINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalRecoveryRangeStatus struct{}

func (_ FfiDestroyerOptionalRecoveryRangeStatus) Destroy(value *RecoveryRangeStatus) {
	if value != nil {
		FfiDestroyerRecoveryRangeStatus{}.Destroy(*value)
	}
}

type FfiConverterOptionalTypeAttemptId struct{}

var FfiConverterOptionalTypeAttemptIdINSTANCE = FfiConverterOptionalTypeAttemptId{}

func (c FfiConverterOptionalTypeAttemptId) Lift(rb RustBufferI) *AttemptId {
	return LiftFromRustBuffer[*AttemptId](c, rb)
}

func (_ FfiConverterOptionalTypeAttemptId) Read(reader io.Reader) *AttemptId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTypeAttemptIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTypeAttemptId) Lower(value *AttemptId) C.RustBuffer {
	return LowerIntoRustBuffer[*AttemptId](c, value)
}

func (c FfiConverterOptionalTypeAttemptId) LowerExternal(value *AttemptId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*AttemptId](c, value))
}

func (_ FfiConverterOptionalTypeAttemptId) Write(writer io.Writer, value *AttemptId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTypeAttemptIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTypeAttemptId struct{}

func (_ FfiDestroyerOptionalTypeAttemptId) Destroy(value *AttemptId) {
	if value != nil {
		FfiDestroyerTypeAttemptId{}.Destroy(*value)
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

type FfiConverterOptionalTypeMemberId struct{}

var FfiConverterOptionalTypeMemberIdINSTANCE = FfiConverterOptionalTypeMemberId{}

func (c FfiConverterOptionalTypeMemberId) Lift(rb RustBufferI) *MemberId {
	return LiftFromRustBuffer[*MemberId](c, rb)
}

func (_ FfiConverterOptionalTypeMemberId) Read(reader io.Reader) *MemberId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTypeMemberIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTypeMemberId) Lower(value *MemberId) C.RustBuffer {
	return LowerIntoRustBuffer[*MemberId](c, value)
}

func (c FfiConverterOptionalTypeMemberId) LowerExternal(value *MemberId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*MemberId](c, value))
}

func (_ FfiConverterOptionalTypeMemberId) Write(writer io.Writer, value *MemberId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTypeMemberIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTypeMemberId struct{}

func (_ FfiDestroyerOptionalTypeMemberId) Destroy(value *MemberId) {
	if value != nil {
		FfiDestroyerTypeMemberId{}.Destroy(*value)
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

type FfiConverterSequenceString struct{}

var FfiConverterSequenceStringINSTANCE = FfiConverterSequenceString{}

func (c FfiConverterSequenceString) Lift(rb RustBufferI) []string {
	return LiftFromRustBuffer[[]string](c, rb)
}

func (c FfiConverterSequenceString) Read(reader io.Reader) []string {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]string, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterStringINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceString) Lower(value []string) C.RustBuffer {
	return LowerIntoRustBuffer[[]string](c, value)
}

func (c FfiConverterSequenceString) LowerExternal(value []string) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]string](c, value))
}

func (c FfiConverterSequenceString) Write(writer io.Writer, value []string) {
	if len(value) > math.MaxInt32 {
		panic("[]string is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterStringINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceString struct{}

func (FfiDestroyerSequenceString) Destroy(sequence []string) {
	for _, value := range sequence {
		FfiDestroyerString{}.Destroy(value)
	}
}

type FfiConverterSequenceAdmissionApproval struct{}

var FfiConverterSequenceAdmissionApprovalINSTANCE = FfiConverterSequenceAdmissionApproval{}

func (c FfiConverterSequenceAdmissionApproval) Lift(rb RustBufferI) []AdmissionApproval {
	return LiftFromRustBuffer[[]AdmissionApproval](c, rb)
}

func (c FfiConverterSequenceAdmissionApproval) Read(reader io.Reader) []AdmissionApproval {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]AdmissionApproval, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterAdmissionApprovalINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceAdmissionApproval) Lower(value []AdmissionApproval) C.RustBuffer {
	return LowerIntoRustBuffer[[]AdmissionApproval](c, value)
}

func (c FfiConverterSequenceAdmissionApproval) LowerExternal(value []AdmissionApproval) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]AdmissionApproval](c, value))
}

func (c FfiConverterSequenceAdmissionApproval) Write(writer io.Writer, value []AdmissionApproval) {
	if len(value) > math.MaxInt32 {
		panic("[]AdmissionApproval is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterAdmissionApprovalINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceAdmissionApproval struct{}

func (FfiDestroyerSequenceAdmissionApproval) Destroy(sequence []AdmissionApproval) {
	for _, value := range sequence {
		FfiDestroyerAdmissionApproval{}.Destroy(value)
	}
}

type FfiConverterSequenceDeliveryFailure struct{}

var FfiConverterSequenceDeliveryFailureINSTANCE = FfiConverterSequenceDeliveryFailure{}

func (c FfiConverterSequenceDeliveryFailure) Lift(rb RustBufferI) []DeliveryFailure {
	return LiftFromRustBuffer[[]DeliveryFailure](c, rb)
}

func (c FfiConverterSequenceDeliveryFailure) Read(reader io.Reader) []DeliveryFailure {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]DeliveryFailure, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterDeliveryFailureINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceDeliveryFailure) Lower(value []DeliveryFailure) C.RustBuffer {
	return LowerIntoRustBuffer[[]DeliveryFailure](c, value)
}

func (c FfiConverterSequenceDeliveryFailure) LowerExternal(value []DeliveryFailure) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]DeliveryFailure](c, value))
}

func (c FfiConverterSequenceDeliveryFailure) Write(writer io.Writer, value []DeliveryFailure) {
	if len(value) > math.MaxInt32 {
		panic("[]DeliveryFailure is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterDeliveryFailureINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceDeliveryFailure struct{}

func (FfiDestroyerSequenceDeliveryFailure) Destroy(sequence []DeliveryFailure) {
	for _, value := range sequence {
		FfiDestroyerDeliveryFailure{}.Destroy(value)
	}
}

type FfiConverterSequenceInvitationControl struct{}

var FfiConverterSequenceInvitationControlINSTANCE = FfiConverterSequenceInvitationControl{}

func (c FfiConverterSequenceInvitationControl) Lift(rb RustBufferI) []InvitationControl {
	return LiftFromRustBuffer[[]InvitationControl](c, rb)
}

func (c FfiConverterSequenceInvitationControl) Read(reader io.Reader) []InvitationControl {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]InvitationControl, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterInvitationControlINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceInvitationControl) Lower(value []InvitationControl) C.RustBuffer {
	return LowerIntoRustBuffer[[]InvitationControl](c, value)
}

func (c FfiConverterSequenceInvitationControl) LowerExternal(value []InvitationControl) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]InvitationControl](c, value))
}

func (c FfiConverterSequenceInvitationControl) Write(writer io.Writer, value []InvitationControl) {
	if len(value) > math.MaxInt32 {
		panic("[]InvitationControl is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterInvitationControlINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceInvitationControl struct{}

func (FfiDestroyerSequenceInvitationControl) Destroy(sequence []InvitationControl) {
	for _, value := range sequence {
		FfiDestroyerInvitationControl{}.Destroy(value)
	}
}

type FfiConverterSequenceJoinAdmissionStep struct{}

var FfiConverterSequenceJoinAdmissionStepINSTANCE = FfiConverterSequenceJoinAdmissionStep{}

func (c FfiConverterSequenceJoinAdmissionStep) Lift(rb RustBufferI) []JoinAdmissionStep {
	return LiftFromRustBuffer[[]JoinAdmissionStep](c, rb)
}

func (c FfiConverterSequenceJoinAdmissionStep) Read(reader io.Reader) []JoinAdmissionStep {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]JoinAdmissionStep, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterJoinAdmissionStepINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceJoinAdmissionStep) Lower(value []JoinAdmissionStep) C.RustBuffer {
	return LowerIntoRustBuffer[[]JoinAdmissionStep](c, value)
}

func (c FfiConverterSequenceJoinAdmissionStep) LowerExternal(value []JoinAdmissionStep) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]JoinAdmissionStep](c, value))
}

func (c FfiConverterSequenceJoinAdmissionStep) Write(writer io.Writer, value []JoinAdmissionStep) {
	if len(value) > math.MaxInt32 {
		panic("[]JoinAdmissionStep is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterJoinAdmissionStepINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceJoinAdmissionStep struct{}

func (FfiDestroyerSequenceJoinAdmissionStep) Destroy(sequence []JoinAdmissionStep) {
	for _, value := range sequence {
		FfiDestroyerJoinAdmissionStep{}.Destroy(value)
	}
}

type FfiConverterSequenceMemberInfo struct{}

var FfiConverterSequenceMemberInfoINSTANCE = FfiConverterSequenceMemberInfo{}

func (c FfiConverterSequenceMemberInfo) Lift(rb RustBufferI) []MemberInfo {
	return LiftFromRustBuffer[[]MemberInfo](c, rb)
}

func (c FfiConverterSequenceMemberInfo) Read(reader io.Reader) []MemberInfo {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]MemberInfo, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterMemberInfoINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceMemberInfo) Lower(value []MemberInfo) C.RustBuffer {
	return LowerIntoRustBuffer[[]MemberInfo](c, value)
}

func (c FfiConverterSequenceMemberInfo) LowerExternal(value []MemberInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]MemberInfo](c, value))
}

func (c FfiConverterSequenceMemberInfo) Write(writer io.Writer, value []MemberInfo) {
	if len(value) > math.MaxInt32 {
		panic("[]MemberInfo is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterMemberInfoINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceMemberInfo struct{}

func (FfiDestroyerSequenceMemberInfo) Destroy(sequence []MemberInfo) {
	for _, value := range sequence {
		FfiDestroyerMemberInfo{}.Destroy(value)
	}
}

type FfiConverterSequencePeerRoute struct{}

var FfiConverterSequencePeerRouteINSTANCE = FfiConverterSequencePeerRoute{}

func (c FfiConverterSequencePeerRoute) Lift(rb RustBufferI) []PeerRoute {
	return LiftFromRustBuffer[[]PeerRoute](c, rb)
}

func (c FfiConverterSequencePeerRoute) Read(reader io.Reader) []PeerRoute {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]PeerRoute, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterPeerRouteINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequencePeerRoute) Lower(value []PeerRoute) C.RustBuffer {
	return LowerIntoRustBuffer[[]PeerRoute](c, value)
}

func (c FfiConverterSequencePeerRoute) LowerExternal(value []PeerRoute) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]PeerRoute](c, value))
}

func (c FfiConverterSequencePeerRoute) Write(writer io.Writer, value []PeerRoute) {
	if len(value) > math.MaxInt32 {
		panic("[]PeerRoute is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterPeerRouteINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequencePeerRoute struct{}

func (FfiDestroyerSequencePeerRoute) Destroy(sequence []PeerRoute) {
	for _, value := range sequence {
		FfiDestroyerPeerRoute{}.Destroy(value)
	}
}

type FfiConverterSequenceRouteHint struct{}

var FfiConverterSequenceRouteHintINSTANCE = FfiConverterSequenceRouteHint{}

func (c FfiConverterSequenceRouteHint) Lift(rb RustBufferI) []RouteHint {
	return LiftFromRustBuffer[[]RouteHint](c, rb)
}

func (c FfiConverterSequenceRouteHint) Read(reader io.Reader) []RouteHint {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]RouteHint, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterRouteHintINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceRouteHint) Lower(value []RouteHint) C.RustBuffer {
	return LowerIntoRustBuffer[[]RouteHint](c, value)
}

func (c FfiConverterSequenceRouteHint) LowerExternal(value []RouteHint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]RouteHint](c, value))
}

func (c FfiConverterSequenceRouteHint) Write(writer io.Writer, value []RouteHint) {
	if len(value) > math.MaxInt32 {
		panic("[]RouteHint is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterRouteHintINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceRouteHint struct{}

func (FfiDestroyerSequenceRouteHint) Destroy(sequence []RouteHint) {
	for _, value := range sequence {
		FfiDestroyerRouteHint{}.Destroy(value)
	}
}

type FfiConverterSequenceTypeEndpointId struct{}

var FfiConverterSequenceTypeEndpointIdINSTANCE = FfiConverterSequenceTypeEndpointId{}

func (c FfiConverterSequenceTypeEndpointId) Lift(rb RustBufferI) []EndpointId {
	return LiftFromRustBuffer[[]EndpointId](c, rb)
}

func (c FfiConverterSequenceTypeEndpointId) Read(reader io.Reader) []EndpointId {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]EndpointId, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterTypeEndpointIdINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceTypeEndpointId) Lower(value []EndpointId) C.RustBuffer {
	return LowerIntoRustBuffer[[]EndpointId](c, value)
}

func (c FfiConverterSequenceTypeEndpointId) LowerExternal(value []EndpointId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]EndpointId](c, value))
}

func (c FfiConverterSequenceTypeEndpointId) Write(writer io.Writer, value []EndpointId) {
	if len(value) > math.MaxInt32 {
		panic("[]EndpointId is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterTypeEndpointIdINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceTypeEndpointId struct{}

func (FfiDestroyerSequenceTypeEndpointId) Destroy(sequence []EndpointId) {
	for _, value := range sequence {
		FfiDestroyerTypeEndpointId{}.Destroy(value)
	}
}

type FfiConverterSequenceTypeMemberId struct{}

var FfiConverterSequenceTypeMemberIdINSTANCE = FfiConverterSequenceTypeMemberId{}

func (c FfiConverterSequenceTypeMemberId) Lift(rb RustBufferI) []MemberId {
	return LiftFromRustBuffer[[]MemberId](c, rb)
}

func (c FfiConverterSequenceTypeMemberId) Read(reader io.Reader) []MemberId {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]MemberId, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterTypeMemberIdINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceTypeMemberId) Lower(value []MemberId) C.RustBuffer {
	return LowerIntoRustBuffer[[]MemberId](c, value)
}

func (c FfiConverterSequenceTypeMemberId) LowerExternal(value []MemberId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]MemberId](c, value))
}

func (c FfiConverterSequenceTypeMemberId) Write(writer io.Writer, value []MemberId) {
	if len(value) > math.MaxInt32 {
		panic("[]MemberId is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterTypeMemberIdINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceTypeMemberId struct{}

func (FfiDestroyerSequenceTypeMemberId) Destroy(sequence []MemberId) {
	for _, value := range sequence {
		FfiDestroyerTypeMemberId{}.Destroy(value)
	}
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type AttemptId = string
type FfiConverterTypeAttemptId = FfiConverterString
type FfiDestroyerTypeAttemptId = FfiDestroyerString

var FfiConverterTypeAttemptIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypeAttemptId(value ExternalCRustBuffer) AttemptId {
	return FfiConverterTypeAttemptIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeAttemptId(value AttemptId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeAttemptIdINSTANCE.Lower(value))
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
type Key32 = string
type FfiConverterTypeKey32 = FfiConverterString
type FfiDestroyerTypeKey32 = FfiDestroyerString

var FfiConverterTypeKey32INSTANCE = FfiConverterString{}

func LiftFromExternalTypeKey32(value ExternalCRustBuffer) Key32 {
	return FfiConverterTypeKey32INSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeKey32(value Key32) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeKey32INSTANCE.Lower(value))
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type MemberId = string
type FfiConverterTypeMemberId = FfiConverterString
type FfiDestroyerTypeMemberId = FfiDestroyerString

var FfiConverterTypeMemberIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypeMemberId(value ExternalCRustBuffer) MemberId {
	return FfiConverterTypeMemberIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeMemberId(value MemberId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeMemberIdINSTANCE.Lower(value))
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type RecordId = string
type FfiConverterTypeRecordId = FfiConverterString
type FfiDestroyerTypeRecordId = FfiDestroyerString

var FfiConverterTypeRecordIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypeRecordId(value ExternalCRustBuffer) RecordId {
	return FfiConverterTypeRecordIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeRecordId(value RecordId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeRecordIdINSTANCE.Lower(value))
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

// The default context is suspended.
func IsSuspended() (bool, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_sdk_fn_func_is_suspended(_uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Restart what `suspend` stopped and rebind sockets.
func Resume() error {
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_func_resume(_uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Stop background work of every client in the default context (for an app
// in the background). Clients stay open and ops still run.
func Suspend() error {
	_, _uniffiErr := rustCallWithError[*ApiError](FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_sdk_fn_func_suspend(_uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}
