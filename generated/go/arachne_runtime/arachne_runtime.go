package arachne_runtime

// #include <arachne_runtime.h>
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/arachne-systems/arachne-sdk/generated/go/arachne_api"
	"io"
	"math"
	"runtime"
	"sync/atomic"
	"time"
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
		C.ffi_arachne_runtime_rustbuffer_free(cb.inner, status)
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
		return C.ffi_arachne_runtime_rustbuffer_from_bytes(foreign, status)
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
		return C.ffi_arachne_runtime_uniffi_contract_version()
	})
	if bindingsContractVersion != int(scaffoldingContractVersion) {
		// If this happens try cleaning and rebuilding your project
		panic("arachne_runtime: UniFFI contract version mismatch")
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_func_default_client_config()
		})
		if checksum != 17430 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_func_default_client_config: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_func_default_transport_options()
		})
		if checksum != 37452 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_func_default_transport_options: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_acknowledge_admission_approval()
		})
		if checksum != 20280 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_acknowledge_admission_approval: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_add_address_hint()
		})
		if checksum != 46600 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_add_address_hint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_admission_approvals()
		})
		if checksum != 42088 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_admission_approvals: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_admission()
		})
		if checksum != 21945 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_invitation()
		})
		if checksum != 13574 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_join()
		})
		if checksum != 21194 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_protected_publication()
		})
		if checksum != 45011 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_protected_publication: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_protected_reception()
		})
		if checksum != 17887 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_protected_reception: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_recovery()
		})
		if checksum != 4181 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_removal()
		})
		if checksum != 63319 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_removal: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_advertise_nearby_workspace()
		})
		if checksum != 64321 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_advertise_nearby_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_begin_join()
		})
		if checksum != 40021 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_begin_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_begin_join_with_peers()
		})
		if checksum != 23777 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_begin_join_with_peers: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_cancel()
		})
		if checksum != 34505 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_cancel: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_cancel_recovery_range()
		})
		if checksum != 54886 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_cancel_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_capabilities()
		})
		if checksum != 38290 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_capabilities: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_close()
		})
		if checksum != 42027 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_close: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_connectivity()
		})
		if checksum != 14558 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_connectivity: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_create_workspace()
		})
		if checksum != 8798 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_create_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_discard_workspace_candidate()
		})
		if checksum != 41388 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_discard_workspace_candidate: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_enable_moq_delivery()
		})
		if checksum != 39751 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_enable_moq_delivery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_endpoint()
		})
		if checksum != 14091 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_endpoint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_invitation_checkpoint()
		})
		if checksum != 43931 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_invitation_checkpoint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_membership_update()
		})
		if checksum != 36804 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_membership_update: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_recovery_range()
		})
		if checksum != 34137 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_recovery_range_at()
		})
		if checksum != 21605 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_recovery_range_at: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_inspect_invitation()
		})
		if checksum != 47596 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_inspect_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_install_member_policy()
		})
		if checksum != 3881 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_install_member_policy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_install_workspace_policy()
		})
		if checksum != 1208 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_install_workspace_policy: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_invitation_controls()
		})
		if checksum != 7025 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_invitation_controls: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_leave_via_peer()
		})
		if checksum != 41362 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_leave_via_peer: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_member_roster()
		})
		if checksum != 51378 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_member_roster: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_members_without_self_update()
		})
		if checksum != 2628 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_members_without_self_update: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_metrics()
		})
		if checksum != 62812 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_metrics: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_moq_metrics()
		})
		if checksum != 20309 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_moq_metrics: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_nearby_endpoints()
		})
		if checksum != 22566 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_nearby_endpoints: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_nearby_workspaces()
		})
		if checksum != 19177 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_nearby_workspaces: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_network_change()
		})
		if checksum != 12052 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_network_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_next_event()
		})
		if checksum != 2823 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_next_event: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_control()
		})
		if checksum != 23691 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_control: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_interest()
		})
		if checksum != 47882 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_interest: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_pending_object()
		})
		if checksum != 30047 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_pending_object: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_presence()
		})
		if checksum != 29494 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_presence: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_protected()
		})
		if checksum != 49497 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_protected: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_recovery_range()
		})
		if checksum != 24515 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_record_freshness()
		})
		if checksum != 22189 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_record_freshness: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_reset_workspace()
		})
		if checksum != 54966 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_reset_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_restore_workspace()
		})
		if checksum != 26548 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_restore_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_retained_admission()
		})
		if checksum != 43165 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_retained_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_send_admission_reply()
		})
		if checksum != 2154 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_send_admission_reply: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_send_nearby_invitation()
		})
		if checksum != 46406 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_send_nearby_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_set_deadline()
		})
		if checksum != 27724 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_set_deadline: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_set_interest()
		})
		if checksum != 50095 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_set_interest: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_set_nearby_identity()
		})
		if checksum != 35004 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_set_nearby_identity: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_admission()
		})
		if checksum != 53850 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_invitation()
		})
		if checksum != 5893 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_invitation: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_invitation_approval()
		})
		if checksum != 6445 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_invitation_approval: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_invitation_decline()
		})
		if checksum != 50998 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_invitation_decline: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_invitation_of()
		})
		if checksum != 17268 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_invitation_of: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_join()
		})
		if checksum != 7864 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_management()
		})
		if checksum != 13475 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_management: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_object_acknowledgement()
		})
		if checksum != 5159 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_object_acknowledgement: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_object_rejection()
		})
		if checksum != 29346 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_object_rejection: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_protected_publication()
		})
		if checksum != 46410 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_protected_publication: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_protected_publication_with_current()
		})
		if checksum != 5183 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_protected_publication_with_current: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_recovery_range()
		})
		if checksum != 31108 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_recovery_range: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_solo_leave()
		})
		if checksum != 5601 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_solo_leave: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_workspace_name()
		})
		if checksum != 20832 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_workspace_name: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_use_service_profile()
		})
		if checksum != 59786 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_use_service_profile: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_wait_for_work()
		})
		if checksum != 8108 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_wait_for_work: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_wake()
		})
		if checksum != 40121 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_wake: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_withdraw_nearby_workspace()
		})
		if checksum != 37888 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_withdraw_nearby_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_workspace_state()
		})
		if checksum != 55907 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_workspace_state: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_drive_join()
		})
		if checksum != 9046 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_drive_join: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_drive_workspace()
		})
		if checksum != 10656 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_drive_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_membership_update()
		})
		if checksum != 59672 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_membership_update: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_request_admission()
		})
		if checksum != 10737 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_request_admission: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_join_grant()
		})
		if checksum != 24990 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_join_grant: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_membership_update()
		})
		if checksum != 50772 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_membership_update: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_adopt_current_view()
		})
		if checksum != 61357 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_adopt_current_view: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_cancel_current_view()
		})
		if checksum != 64607 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_cancel_current_view: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_cancel_direct_recovery()
		})
		if checksum != 58694 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_cancel_direct_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_discover_recovery_cutoff()
		})
		if checksum != 53155 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_discover_recovery_cutoff: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_current_view()
		})
		if checksum != 3586 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_current_view: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_fetch_direct_recovery()
		})
		if checksum != 45737 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_fetch_direct_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_next_direct_gap()
		})
		if checksum != 11861 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_next_direct_gap: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_current_view()
		})
		if checksum != 32398 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_current_view: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_direct_recovery()
		})
		if checksum != 14141 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_direct_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_poll_recovery_cutoff()
		})
		if checksum != 2590 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_poll_recovery_cutoff: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_current_view()
		})
		if checksum != 3813 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_current_view: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_direct_miss()
		})
		if checksum != 14461 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_direct_miss: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_stage_direct_recovery()
		})
		if checksum != 5072 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_stage_direct_recovery: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_client_resource()
		})
		if checksum != 27068 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_client_resource: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_invitationcandidate_discard()
		})
		if checksum != 59179 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_invitationcandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_invitationcandidate_workspace()
		})
		if checksum != 20876 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_invitationcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_joincandidate_discard()
		})
		if checksum != 62150 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_joincandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_joincandidate_workspace()
		})
		if checksum != 51963 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_joincandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_protectedreceptioncandidate_discard()
		})
		if checksum != 58737 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_protectedreceptioncandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_protectedreceptioncandidate_workspace()
		})
		if checksum != 53892 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_protectedreceptioncandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_publicationcandidate_discard()
		})
		if checksum != 11039 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_publicationcandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_publicationcandidate_workspace()
		})
		if checksum != 57492 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_publicationcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_recoverycandidate_discard()
		})
		if checksum != 55203 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_recoverycandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_recoverycandidate_publication_count()
		})
		if checksum != 15392 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_recoverycandidate_publication_count: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_recoverycandidate_workspace()
		})
		if checksum != 62518 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_recoverycandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_removalcandidate_discard()
		})
		if checksum != 6517 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_removalcandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_removalcandidate_workspace()
		})
		if checksum != 51185 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_removalcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_workspacecandidate_discard()
		})
		if checksum != 51426 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_workspacecandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_workspacecandidate_workspace()
		})
		if checksum != 51382 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_workspacecandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_admissiongrant_epoch()
		})
		if checksum != 22764 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_admissiongrant_epoch: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_admissiongrant_history_steps()
		})
		if checksum != 50512 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_admissiongrant_history_steps: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_admissiongrant_workspace()
		})
		if checksum != 58067 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_admissiongrant_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_memberupdate_epoch()
		})
		if checksum != 17971 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_memberupdate_epoch: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_memberupdate_fingerprint()
		})
		if checksum != 27048 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_memberupdate_fingerprint: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_memberupdate_peer()
		})
		if checksum != 12697 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_memberupdate_peer: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_memberupdate_state()
		})
		if checksum != 58004 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_memberupdate_state: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_memberupdate_workspace()
		})
		if checksum != 46288 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_memberupdate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_currentviewcandidate_discard()
		})
		if checksum != 56731 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_currentviewcandidate_discard: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_currentviewcandidate_workspace()
		})
		if checksum != 45789 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_currentviewcandidate_workspace: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_is_suspended()
		})
		if checksum != 19112 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_is_suspended: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_limits()
		})
		if checksum != 58248 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_limits: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_open()
		})
		if checksum != 4380 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_open: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_power()
		})
		if checksum != 48174 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_power: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_resume()
		})
		if checksum != 56622 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_resume: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_method_context_suspend()
		})
		if checksum != 52380 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_method_context_suspend: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_constructor_client_open()
		})
		if checksum != 63381 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_constructor_client_open: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_constructor_client_open_in()
		})
		if checksum != 53951 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_constructor_client_open_in: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_constructor_context_default_shared()
		})
		if checksum != 64196 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_constructor_context_default_shared: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_constructor_context_owned()
		})
		if checksum != 37338 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_constructor_context_owned: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_runtime_checksum_constructor_storageconfig_open_sqlite()
		})
		if checksum != 50004 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_runtime: uniffi_arachne_runtime_checksum_constructor_storageconfig_open_sqlite: UniFFI API checksum mismatch")
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

// FfiConverterDuration converts between uniffi duration and Go duration.
type FfiConverterDuration struct{}

var FfiConverterDurationINSTANCE = FfiConverterDuration{}

func (c FfiConverterDuration) Lift(rb RustBufferI) time.Duration {
	return LiftFromRustBuffer[time.Duration](c, rb)
}

func (c FfiConverterDuration) Read(reader io.Reader) time.Duration {
	sec := readUint64(reader)
	nsec := readUint32(reader)
	return time.Duration(sec*1_000_000_000 + uint64(nsec))
}

func (c FfiConverterDuration) Lower(value time.Duration) C.RustBuffer {
	return LowerIntoRustBuffer[time.Duration](c, value)
}

func (c FfiConverterDuration) LowerExternal(value time.Duration) ExternalCRustBuffer {
	return RustBufferFromC(c.Lower(value))
}

func (c FfiConverterDuration) Write(writer io.Writer, value time.Duration) {
	if value.Nanoseconds() < 0 {
		// Rust does not support negative durations:
		// https://www.reddit.com/r/rust/comments/ljl55u/why_rusts_duration_not_supporting_negative_values/
		// This panic is very bad, because it depends on user input, and in Go user input related
		// error are supposed to be returned as errors, and not cause panics. However, with the
		// current architecture, its not possible to return an error from here, so panic is used as
		// the only other option to signal an error.
		panic("negative duration is not allowed")
	}

	writeUint64(writer, uint64(value)/1_000_000_000)
	writeUint32(writer, uint32(uint64(value)%1_000_000_000))
}

type FfiDestroyerDuration struct{}

func (FfiDestroyerDuration) Destroy(_ time.Duration) {}

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

// A peer's admission history. Stage it on the client that requested it.
// Membership authorization is verified by the native stage operation.
type AdmissionGrantInterface interface {
	Epoch() uint64
	HistorySteps() uint64
	Workspace() arachne_api.WorkspaceId
}

// A peer's admission history. Stage it on the client that requested it.
// Membership authorization is verified by the native stage operation.
type AdmissionGrant struct {
	ffiObject FfiObject
}

func (_self *AdmissionGrant) Epoch() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*AdmissionGrant")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_admissiongrant_epoch(
			_pointer, _uniffiStatus)
	}))
}

func (_self *AdmissionGrant) HistorySteps() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*AdmissionGrant")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_admissiongrant_history_steps(
			_pointer, _uniffiStatus)
	}))
}

func (_self *AdmissionGrant) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*AdmissionGrant")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_admissiongrant_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *AdmissionGrant) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAdmissionGrant struct{}

var FfiConverterAdmissionGrantINSTANCE = FfiConverterAdmissionGrant{}

func (c FfiConverterAdmissionGrant) Lift(handle C.uint64_t) *AdmissionGrant {
	result := &AdmissionGrant{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_admissiongrant(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_admissiongrant(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*AdmissionGrant).Destroy)
	return result
}

func (c FfiConverterAdmissionGrant) Read(reader io.Reader) *AdmissionGrant {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterAdmissionGrant) Lower(value *AdmissionGrant) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*AdmissionGrant")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterAdmissionGrant) Write(writer io.Writer, value *AdmissionGrant) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalAdmissionGrant(handle uint64) *AdmissionGrant {
	return FfiConverterAdmissionGrantINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalAdmissionGrant(value *AdmissionGrant) uint64 {
	return uint64(FfiConverterAdmissionGrantINSTANCE.Lower(value))
}

type FfiDestroyerAdmissionGrant struct{}

func (_ FfiDestroyerAdmissionGrant) Destroy(value *AdmissionGrant) {
	value.Destroy()
}

// Typed adopter seam over the portable runtime. The JSON dispatcher remains
// private to adapters; consumers use typed lifecycle operations here.
//
// A `Client` is `Send + Sync`. `close`, `wake`, `wait_for_work` and
// `next_event` may run on any thread while another thread waits.
type ClientInterface interface {
	// Mark a pending approval as seen by the administrator's UI.
	AcknowledgeAdmissionApproval(attemptId arachne_api.AttemptId) error
	AddAddressHint(peer arachne_api.EndpointId, address string) error
	// One page of admission requests that wait for an administrator,
	// after `after` (an attempt ID), at most `limit` (1 to 64, default 64).
	AdmissionApprovals(after *arachne_api.AttemptId, limit *uint64) (AdmissionApprovalPage, error)
	// Adopt an admission, administrator action or name change. Core saves
	// it, reads it back, then adopts it.
	//
	// Only its own kind compiles:
	// ```compile_fail
	// # fn wrong(client: &arachne_runtime::Client, c: &arachne_runtime::InvitationCandidate) {
	// client.adopt_admission(c);
	// # }
	// ```
	AdoptAdmission(candidate *WorkspaceCandidate) (WorkspaceInfo, error)
	// Adopt a staged invitation registration and return its bearer link.
	AdoptInvitation(candidate *InvitationCandidate) (InvitationInfo, error)
	AdoptJoin(candidate *JoinCandidate) (WorkspaceInfo, error)
	AdoptProtectedPublication(candidate *PublicationCandidate) (DeliveryReport, error)
	// Adopt a staged inbox candidate: a reception from `poll_protected`, or an
	// acknowledgement or rejection. A received object then waits in the
	// durable inbox; read it with `poll_pending_object`.
	AdoptProtectedReception(candidate *ProtectedReceptionCandidate) error
	AdoptRecovery(candidate *RecoveryCandidate) (RecoveryAdoption, error)
	// Adopt a saved removal of this member. The session ends: later calls
	// give `Closed`.
	AdoptRemoval(candidate *RemovalCandidate) (RemovedMembership, error)
	// Advertise one workspace's invitation to nearby devices. Returns
	// whether this device now advertises anything.
	AdvertiseNearbyWorkspace(workspace *arachne_api.WorkspaceId, mode NearbyMode, invitation []byte, workspaceName *string) (bool, error)
	BeginJoin(invitation []byte, checkpoint []byte, displayName string) (JoinRequest, error)
	// Begin joining with known peers for the admission exchange.
	BeginJoinWithPeers(invitation []byte, checkpoint []byte, displayName string, peers []arachne_api.EndpointId) (JoinRequest, error)
	// Interrupt the blocking op in flight (its outbound control
	// exchanges). Not sticky: the latch clears when that op ends, so the
	// next op runs normally.
	Cancel() error
	CancelRecoveryRange() error
	// Features and limits of this client's context.
	Capabilities() (arachne_api.Capabilities, error)
	// Close the session. Idempotent, and callable from any thread: it
	// interrupts the op in flight, releases every waiter, and later calls
	// fail with `Closed`. Bounded by the close drain deadline.
	Close() error
	Connectivity() (ConnectivityReport, error)
	CreateWorkspace(displayName string, workspaceName *string) (WorkspaceInfo, error)
	// Drop the staged candidate. Returns whether one was staged.
	DiscardWorkspaceCandidate() (bool, error)
	// Opt an authenticated endpoint and topic into protected MoQ delivery.
	EnableMoqDelivery(workspace arachne_api.WorkspaceId, revision uint64, peerEndpoint arachne_api.EndpointId, topic string) error
	Endpoint() (EndpointInfo, error)
	// Fetch and verify the current checkpoint of a compact invitation from
	// one of up to three members.
	FetchInvitationCheckpoint(invitation []byte, peers []arachne_api.EndpointId) (InvitationCheckpoint, error)
	// Ask `peer` for the membership step after this node's epoch. Read the
	// answer with `poll_membership_update`.
	FetchMembershipUpdate(peer arachne_api.EndpointId, replacePending bool) error
	FetchRecoveryRange(request RecoveryRangeRequest) (RecoveryRangeStatus, error)
	// `fetch_recovery_range` for an earlier author epoch that is still in
	// the receive window (A3f). `None` is the current epoch.
	FetchRecoveryRangeAt(request RecoveryRangeRequest, epoch *uint64) (RecoveryRangeStatus, error)
	InspectInvitation(invitation []byte, checkpoint []byte) (InvitationDetails, error)
	// Route only `topics` between all members at `revision`.
	InstallMemberPolicy(revision uint64, topics []string) error
	// Route every topic between all members at `revision` (epoch + 1).
	InstallWorkspacePolicy(revision uint64) error
	// The registered invitation links, numbered for people.
	InvitationControls() ([]InvitationControl, error)
	// Leave through another member, who commits the departure. Adopt the
	// staged removal with `adopt_removal`; that ends the session.
	LeaveViaPeer(peer arachne_api.EndpointId) (*RemovalCandidate, error)
	MemberRoster() (MemberRoster, error)
	// Members whose leaf still comes from their KeyPackage: they never
	// self-updated. Each adds about 82 bytes to every management commit
	// (B3c), so a host can predict commit size and nudge those members.
	MembersWithoutSelfUpdate() (uint64, error)
	Metrics() (WorkspaceMetrics, error)
	MoqMetrics() (StreamMetrics, error)
	// Nearby endpoints on the local network and the names they announce.
	NearbyEndpoints() ([]NearbyEndpoint, error)
	// Workspaces that nearby devices advertise.
	NearbyWorkspaces() (NearbyScan, error)
	NetworkChange() error
	// The next event of any queue, up to `timeout` (`None`: no timeout).
	// `Ok(None)`: the timeout passed, `wake` was called, or the client
	// closed while it waited. Queue events repeat until the host drains
	// the queue with its poll call; a ready job reports once. After
	// `close`, it fails with `Closed`.
	NextEvent(timeout *time.Duration) (*arachne_api.Event, error)
	// Service one queued peer-control exchange and report whether one was served.
	PollControl() (bool, error)
	PollInterest() (*InterestObservation, error)
	// The next authenticated object that the application has not yet
	// acknowledged or rejected. It stays pending (also after a restart) until
	// an acknowledgement or rejection is adopted: delivery is at least once.
	PollPendingObject() (*ReceivedProtectedPublication, error)
	// One presence round with the workspace's members: send this node's
	// head to each and read their answers. `announce` marks a restart.
	PollPresence(announce bool) (PresenceRound, error)
	// Stage one protected incoming publication without exposing its plaintext.
	// Adopt it with `adopt_protected_reception`; core saves it first.
	PollProtected() (**ProtectedReceptionCandidate, error)
	PollRecoveryRange() (*RecoveryRangeStatus, error)
	// Anchor after the latest record commit. Without a monotonic anchor
	// store, read it after every call and save it outside the database.
	RecordFreshness() (FreshnessAnchor, error)
	// Forget all workspace state. Returns whether anything changed.
	ResetWorkspace() (bool, error)
	// Restore the workspace stored for `workspace`: an active workspace, a
	// pending join, or this member's removal (the session then ends). With
	// `expected`, the store must match that saved anchor exactly.
	RestoreWorkspace(workspace arachne_api.WorkspaceId, expected *FreshnessAnchor) (RestoredWorkspace, error)
	RetainedAdmission(authenticatedEndpoint arachne_api.EndpointId, request []byte) (AdmissionReply, error)
	// Answer the held admission, leave or offer exchange after its
	// transition is durable. `false`: the requester expired; its result
	// stays retained for a retry.
	SendAdmissionReply() (bool, error)
	// Hand an invitation to one nearby device.
	SendNearbyInvitation(peer arachne_api.EndpointId, invitation []byte) error
	// Give each later blocking op this deadline. At the deadline the op
	// fails with `DeadlineExceeded` and the session stays usable.
	SetDeadline(deadline *time.Duration)
	SetInterest(workspace arachne_api.WorkspaceId, revision uint64, topic string, subscribed bool) error
	// The name this device answers to nearby identity asks.
	SetNearbyIdentity(name string) error
	StageAdmission(authenticatedEndpoint arachne_api.EndpointId, request []byte) (*WorkspaceCandidate, error)
	// Register a reusable invitation link in shared policy. The link is
	// released only by `adopt_invitation`, after the candidate is saved.
	StageInvitation(expiresAt uint64) (*InvitationCandidate, error)
	// Approve (bind) a personal invitation for one join request. Adopt the
	// candidate with `adopt_admission`.
	StageInvitationApproval(request []byte, attemptId *arachne_api.AttemptId) (*WorkspaceCandidate, error)
	// Decline a personal invitation request. Adopt the candidate with
	// `adopt_admission`.
	StageInvitationDecline(request []byte, attemptId *arachne_api.AttemptId) (*WorkspaceCandidate, error)
	// Register an invitation link of `kind`. Adopt it with
	// `adopt_invitation` after the candidate is saved.
	StageInvitationOf(expiresAt uint64, kind InvitationKind) (*InvitationCandidate, error)
	StageJoin(welcome []byte, commits []JoinAdmissionStep) (*JoinCandidate, error)
	// Stage an administrator action. Adopt it with `adopt_admission`.
	StageManagement(action MemberAction) (*WorkspaceCandidate, error)
	// Stage the application's durable acceptance of a pending object. Save
	// then adopt it with `adopt_protected_reception`.
	StageObjectAcknowledgement(object ReceivedProtectedPublication) (*ProtectedReceptionCandidate, error)
	// Stage a permanent application rejection of a pending object. Its
	// identity stays recorded, so it is never delivered again.
	StageObjectRejection(object ReceivedProtectedPublication) (*ProtectedReceptionCandidate, error)
	StageProtectedPublication(workspace arachne_api.WorkspaceId, revision uint64, topic string, id arachne_api.RecordId, payload []byte) (*PublicationCandidate, error)
	StageProtectedPublicationWithCurrent(workspace arachne_api.WorkspaceId, revision uint64, topic string, id arachne_api.RecordId, payload []byte, current *PublicationCurrent) (*PublicationCandidate, error)
	// `retain_until` is Unix seconds (UTC) by this node's clock; 0 keeps no
	// copy for third-party recovery.
	StageRecoveryRange(retainUntil uint64) (RecoveryStage, error)
	// The last member leaves alone. Adopt with `adopt_removal`.
	StageSoloLeave() (*RemovalCandidate, error)
	// Rename the workspace. Adopt the candidate with `adopt_admission`.
	StageWorkspaceName(workspaceName string) (*WorkspaceCandidate, error)
	// Mark this session's signed workspace profile as a service.
	// This grants no membership or publication rights.
	UseServiceProfile() error
	// Park until the session may have work, up to `timeout` (`None`: no
	// timeout). Holds no client or session lock. `Ok(true)`: drain the
	// queues (or call `next_event`), then call again. `Ok(false)`: the
	// timeout passed, `wake` was called, or the client closed.
	WaitForWork(timeout *time.Duration) (bool, error)
	// Release one waiter (`wait_for_work` or `next_event`) without work,
	// for example at host shutdown.
	Wake() error
	// Stop advertising `workspace`, or every workspace for `None`.
	WithdrawNearbyWorkspace(workspace *arachne_api.WorkspaceId) (bool, error)
	WorkspaceState() (WorkspaceState, error)
	// Advance a stored join. A pushed admission can return a candidate; adopt
	// it through `adopt_join`. All other committed results are already durable.
	DriveJoin() (JoinProgress, error)
	// Serve one queued operation and advance native durable lifecycle work.
	// Presence remains part of the result from this same drive operation.
	DriveWorkspace() (WorkspaceProgress, error)
	PollMembershipUpdate() (**MemberUpdate, error)
	RequestAdmission(peer arachne_api.EndpointId) (AdmissionResponse, error)
	// Stage the exact received history, including management and self-update steps.
	StageJoinGrant(grant *AdmissionGrant) (*JoinCandidate, error)
	StageMembershipUpdate(update *MemberUpdate) (MembershipCandidate, error)
	AdoptCurrentView(candidate *CurrentViewCandidate) (CurrentViewAdoption, error)
	CancelCurrentView() error
	CancelDirectRecovery() error
	DiscoverRecoveryCutoff(request RecoveryCutoffRequest) (RecoveryCutoffStatus, error)
	// Fetch the current catalog view from the author or an authorized holder.
	// This uses the signed selector, replacement keys and expiry metadata.
	FetchCurrentView(request CurrentViewRequest) (CurrentViewStatus, error)
	FetchDirectRecovery(request DirectRecoveryRequest) (DirectRecoveryStatus, error)
	// The next direct stream gap. No progress is accepted by this read.
	NextDirectGap() (*DirectRecoveryRequest, error)
	PollCurrentView() (*CurrentViewStatus, error)
	PollDirectRecovery() (*DirectRecoveryStatus, error)
	PollRecoveryCutoff() (*RecoveryCutoffStatus, error)
	StageCurrentView() (*CurrentViewCandidate, error)
	// Record an authenticated miss only after the recovery sources are exhausted.
	StageDirectMiss() (*RecoveryCandidate, error)
	StageDirectRecovery() (RecoveryStage, error)
	// Start, poll, cancel or revoke a resource transfer. Member authorization
	// and absolute-path checks run in the existing native resource service.
	// `root` is the blob cache directory. The host must select immutable source
	// files and check the content audience before it creates a grant.
	Resource(request ResourceRequest) (ResourceStatus, error)
}

// Typed adopter seam over the portable runtime. The JSON dispatcher remains
// private to adapters; consumers use typed lifecycle operations here.
//
// A `Client` is `Send + Sync`. `close`, `wake`, `wait_for_work` and
// `next_event` may run on any thread while another thread waits.
type Client struct {
	ffiObject FfiObject
}

// Open a client in the process default context
// ([`Context::default_shared`](crate::Context::default_shared)).
func ClientOpen(config ClientConfig) (*Client, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_constructor_client_open(FfiConverterClientConfigINSTANCE.Lower(config), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Client
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterClientINSTANCE.Lift(_uniffiRV), nil
	}
}

// Open a client in `context`. Storage and other per-client setup
// attach here, after the session is registered.
func ClientOpenIn(context *Context, config ClientConfig) (*Client, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_constructor_client_open_in(FfiConverterContextINSTANCE.Lower(context), FfiConverterClientConfigINSTANCE.Lower(config), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Client
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterClientINSTANCE.Lift(_uniffiRV), nil
	}
}

// Mark a pending approval as seen by the administrator's UI.
func (_self *Client) AcknowledgeAdmissionApproval(attemptId arachne_api.AttemptId) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_acknowledge_admission_approval(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeAttemptId(attemptId)), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) AddAddressHint(peer arachne_api.EndpointId, address string) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_add_address_hint(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peer)), FfiConverterStringINSTANCE.Lower(address), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// One page of admission requests that wait for an administrator,
// after `after` (an attempt ID), at most `limit` (1 to 64, default 64).
func (_self *Client) AdmissionApprovals(after *arachne_api.AttemptId, limit *uint64) (AdmissionApprovalPage, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_admission_approvals(
				_pointer, FfiConverterOptionalAttemptIdINSTANCE.Lower(after), FfiConverterOptionalUint64INSTANCE.Lower(limit), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue AdmissionApprovalPage
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionApprovalPageINSTANCE.Lift(_uniffiRV), nil
	}
}

// Adopt an admission, administrator action or name change. Core saves
// it, reads it back, then adopts it.
//
// Only its own kind compiles:
// ```compile_fail
// # fn wrong(client: &arachne_runtime::Client, c: &arachne_runtime::InvitationCandidate) {
// client.adopt_admission(c);
// # }
// ```
func (_self *Client) AdoptAdmission(candidate *WorkspaceCandidate) (WorkspaceInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_admission(
				_pointer, FfiConverterWorkspaceCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceInfo
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceInfoINSTANCE.Lift(_uniffiRV), nil
	}
}

// Adopt a staged invitation registration and return its bearer link.
func (_self *Client) AdoptInvitation(candidate *InvitationCandidate) (InvitationInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_invitation(
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
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_join(
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

func (_self *Client) AdoptProtectedPublication(candidate *PublicationCandidate) (DeliveryReport, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_protected_publication(
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

// Adopt a staged inbox candidate: a reception from `poll_protected`, or an
// acknowledgement or rejection. A received object then waits in the
// durable inbox; read it with `poll_pending_object`.
func (_self *Client) AdoptProtectedReception(candidate *ProtectedReceptionCandidate) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_adopt_protected_reception(
			_pointer, FfiConverterProtectedReceptionCandidateINSTANCE.Lower(candidate), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) AdoptRecovery(candidate *RecoveryCandidate) (RecoveryAdoption, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_recovery(
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

// Adopt a saved removal of this member. The session ends: later calls
// give `Closed`.
func (_self *Client) AdoptRemoval(candidate *RemovalCandidate) (RemovedMembership, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_removal(
				_pointer, FfiConverterRemovalCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RemovedMembership
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRemovedMembershipINSTANCE.Lift(_uniffiRV), nil
	}
}

// Advertise one workspace's invitation to nearby devices. Returns
// whether this device now advertises anything.
func (_self *Client) AdvertiseNearbyWorkspace(workspace *arachne_api.WorkspaceId, mode NearbyMode, invitation []byte, workspaceName *string) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_advertise_nearby_workspace(
			_pointer, FfiConverterOptionalWorkspaceIdINSTANCE.Lower(workspace), FfiConverterNearbyModeINSTANCE.Lower(mode), FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterOptionalStringINSTANCE.Lower(workspaceName), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) BeginJoin(invitation []byte, checkpoint []byte, displayName string) (JoinRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_begin_join(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterBytesINSTANCE.Lower(checkpoint), FfiConverterStringINSTANCE.Lower(displayName), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue JoinRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

// Begin joining with known peers for the admission exchange.
func (_self *Client) BeginJoinWithPeers(invitation []byte, checkpoint []byte, displayName string, peers []arachne_api.EndpointId) (JoinRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_begin_join_with_peers(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterBytesINSTANCE.Lower(checkpoint), FfiConverterStringINSTANCE.Lower(displayName), FfiConverterSequenceEndpointIdINSTANCE.Lower(peers), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue JoinRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

// Interrupt the blocking op in flight (its outbound control
// exchanges). Not sticky: the latch clears when that op ends, so the
// next op runs normally.
func (_self *Client) Cancel() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_cancel(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) CancelRecoveryRange() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_cancel_recovery_range(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Features and limits of this client's context.
func (_self *Client) Capabilities() (arachne_api.Capabilities, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_capabilities(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue arachne_api.Capabilities
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return arachne_api.FfiConverterCapabilitiesINSTANCE.Lift(_uniffiRV), nil
	}
}

// Close the session. Idempotent, and callable from any thread: it
// interrupts the op in flight, releases every waiter, and later calls
// fail with `Closed`. Bounded by the close drain deadline.
func (_self *Client) Close() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_close(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) Connectivity() (ConnectivityReport, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_connectivity(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue ConnectivityReport
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterConnectivityReportINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) CreateWorkspace(displayName string, workspaceName *string) (WorkspaceInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_create_workspace(
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

// Drop the staged candidate. Returns whether one was staged.
func (_self *Client) DiscardWorkspaceCandidate() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_discard_workspace_candidate(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Opt an authenticated endpoint and topic into protected MoQ delivery.
func (_self *Client) EnableMoqDelivery(workspace arachne_api.WorkspaceId, revision uint64, peerEndpoint arachne_api.EndpointId, topic string) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_enable_moq_delivery(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeWorkspaceId(workspace)), FfiConverterUint64INSTANCE.Lower(revision),
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peerEndpoint)), FfiConverterStringINSTANCE.Lower(topic), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) Endpoint() (EndpointInfo, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_endpoint(
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

// Fetch and verify the current checkpoint of a compact invitation from
// one of up to three members.
func (_self *Client) FetchInvitationCheckpoint(invitation []byte, peers []arachne_api.EndpointId) (InvitationCheckpoint, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_fetch_invitation_checkpoint(
				_pointer, FfiConverterBytesINSTANCE.Lower(invitation), FfiConverterSequenceEndpointIdINSTANCE.Lower(peers), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue InvitationCheckpoint
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationCheckpointINSTANCE.Lift(_uniffiRV), nil
	}
}

// Ask `peer` for the membership step after this node's epoch. Read the
// answer with `poll_membership_update`.
func (_self *Client) FetchMembershipUpdate(peer arachne_api.EndpointId, replacePending bool) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_fetch_membership_update(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peer)), FfiConverterBoolINSTANCE.Lower(replacePending), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) FetchRecoveryRange(request RecoveryRangeRequest) (RecoveryRangeStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_fetch_recovery_range(
				_pointer, FfiConverterRecoveryRangeRequestINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryRangeStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryRangeStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// `fetch_recovery_range` for an earlier author epoch that is still in
// the receive window (A3f). `None` is the current epoch.
func (_self *Client) FetchRecoveryRangeAt(request RecoveryRangeRequest, epoch *uint64) (RecoveryRangeStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_fetch_recovery_range_at(
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

func (_self *Client) InspectInvitation(invitation []byte, checkpoint []byte) (InvitationDetails, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_inspect_invitation(
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
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_install_member_policy(
			_pointer, FfiConverterUint64INSTANCE.Lower(revision), FfiConverterSequenceStringINSTANCE.Lower(topics), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Route every topic between all members at `revision` (epoch + 1).
func (_self *Client) InstallWorkspacePolicy(revision uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_install_workspace_policy(
			_pointer, FfiConverterUint64INSTANCE.Lower(revision), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// The registered invitation links, numbered for people.
func (_self *Client) InvitationControls() ([]InvitationControl, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_invitation_controls(
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

// Leave through another member, who commits the departure. Adopt the
// staged removal with `adopt_removal`; that ends the session.
func (_self *Client) LeaveViaPeer(peer arachne_api.EndpointId) (*RemovalCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_leave_via_peer(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peer)), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RemovalCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRemovalCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) MemberRoster() (MemberRoster, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_member_roster(
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

// Members whose leaf still comes from their KeyPackage: they never
// self-updated. Each adds about 82 bytes to every management commit
// (B3c), so a host can predict commit size and nudge those members.
func (_self *Client) MembersWithoutSelfUpdate() (uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_members_without_self_update(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue uint64
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterUint64INSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) Metrics() (WorkspaceMetrics, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_metrics(
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

func (_self *Client) MoqMetrics() (StreamMetrics, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_moq_metrics(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue StreamMetrics
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterStreamMetricsINSTANCE.Lift(_uniffiRV), nil
	}
}

// Nearby endpoints on the local network and the names they announce.
func (_self *Client) NearbyEndpoints() ([]NearbyEndpoint, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_nearby_endpoints(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue []NearbyEndpoint
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSequenceNearbyEndpointINSTANCE.Lift(_uniffiRV), nil
	}
}

// Workspaces that nearby devices advertise.
func (_self *Client) NearbyWorkspaces() (NearbyScan, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_nearby_workspaces(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue NearbyScan
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterNearbyScanINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) NetworkChange() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_network_change(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// The next event of any queue, up to `timeout` (`None`: no timeout).
// `Ok(None)`: the timeout passed, `wake` was called, or the client
// closed while it waited. Queue events repeat until the host drains
// the queue with its poll call; a ready job reports once. After
// `close`, it fails with `Closed`.
func (_self *Client) NextEvent(timeout *time.Duration) (*arachne_api.Event, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_next_event(
				_pointer, FfiConverterOptionalDurationINSTANCE.Lower(timeout), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *arachne_api.Event
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalEventINSTANCE.Lift(_uniffiRV), nil
	}
}

// Service one queued peer-control exchange and report whether one was served.
func (_self *Client) PollControl() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_poll_control(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollInterest() (*InterestObservation, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_interest(
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

// The next authenticated object that the application has not yet
// acknowledged or rejected. It stays pending (also after a restart) until
// an acknowledgement or rejection is adopted: delivery is at least once.
func (_self *Client) PollPendingObject() (*ReceivedProtectedPublication, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_pending_object(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ReceivedProtectedPublication
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalReceivedProtectedPublicationINSTANCE.Lift(_uniffiRV), nil
	}
}

// One presence round with the workspace's members: send this node's
// head to each and read their answers. `announce` marks a restart.
func (_self *Client) PollPresence(announce bool) (PresenceRound, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_presence(
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

// Stage one protected incoming publication without exposing its plaintext.
// Adopt it with `adopt_protected_reception`; core saves it first.
func (_self *Client) PollProtected() (**ProtectedReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_protected(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue **ProtectedReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalProtectedReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollRecoveryRange() (*RecoveryRangeStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_recovery_range(
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

// Anchor after the latest record commit. Without a monotonic anchor
// store, read it after every call and save it outside the database.
func (_self *Client) RecordFreshness() (FreshnessAnchor, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_record_freshness(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue FreshnessAnchor
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterTypeFreshnessAnchorINSTANCE.Lift(_uniffiRV), nil
	}
}

// Forget all workspace state. Returns whether anything changed.
func (_self *Client) ResetWorkspace() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_reset_workspace(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Restore the workspace stored for `workspace`: an active workspace, a
// pending join, or this member's removal (the session then ends). With
// `expected`, the store must match that saved anchor exactly.
func (_self *Client) RestoreWorkspace(workspace arachne_api.WorkspaceId, expected *FreshnessAnchor) (RestoredWorkspace, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_restore_workspace(
				_pointer,
				CFromRustBuffer(arachne_api.LowerToExternalTypeWorkspaceId(workspace)), FfiConverterOptionalTypeFreshnessAnchorINSTANCE.Lower(expected), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RestoredWorkspace
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRestoredWorkspaceINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) RetainedAdmission(authenticatedEndpoint arachne_api.EndpointId, request []byte) (AdmissionReply, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_retained_admission(
				_pointer,
				CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(authenticatedEndpoint)), FfiConverterBytesINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue AdmissionReply
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionReplyINSTANCE.Lift(_uniffiRV), nil
	}
}

// Answer the held admission, leave or offer exchange after its
// transition is durable. `false`: the requester expired; its result
// stays retained for a retry.
func (_self *Client) SendAdmissionReply() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_send_admission_reply(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Hand an invitation to one nearby device.
func (_self *Client) SendNearbyInvitation(peer arachne_api.EndpointId, invitation []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_send_nearby_invitation(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peer)), FfiConverterBytesINSTANCE.Lower(invitation), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Give each later blocking op this deadline. At the deadline the op
// fails with `DeadlineExceeded` and the session stays usable.
func (_self *Client) SetDeadline(deadline *time.Duration) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_set_deadline(
			_pointer, FfiConverterOptionalDurationINSTANCE.Lower(deadline), _uniffiStatus)
		return false
	})
}

func (_self *Client) SetInterest(workspace arachne_api.WorkspaceId, revision uint64, topic string, subscribed bool) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_set_interest(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeWorkspaceId(workspace)), FfiConverterUint64INSTANCE.Lower(revision), FfiConverterStringINSTANCE.Lower(topic), FfiConverterBoolINSTANCE.Lower(subscribed), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// The name this device answers to nearby identity asks.
func (_self *Client) SetNearbyIdentity(name string) error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_set_nearby_identity(
			_pointer, FfiConverterStringINSTANCE.Lower(name), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) StageAdmission(authenticatedEndpoint arachne_api.EndpointId, request []byte) (*WorkspaceCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_admission(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(authenticatedEndpoint)), FfiConverterBytesINSTANCE.Lower(request), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *WorkspaceCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Register a reusable invitation link in shared policy. The link is
// released only by `adopt_invitation`, after the candidate is saved.
func (_self *Client) StageInvitation(expiresAt uint64) (*InvitationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_invitation(
			_pointer, FfiConverterUint64INSTANCE.Lower(expiresAt), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *InvitationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Approve (bind) a personal invitation for one join request. Adopt the
// candidate with `adopt_admission`.
func (_self *Client) StageInvitationApproval(request []byte, attemptId *arachne_api.AttemptId) (*WorkspaceCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_invitation_approval(
			_pointer, FfiConverterBytesINSTANCE.Lower(request), FfiConverterOptionalAttemptIdINSTANCE.Lower(attemptId), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *WorkspaceCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Decline a personal invitation request. Adopt the candidate with
// `adopt_admission`.
func (_self *Client) StageInvitationDecline(request []byte, attemptId *arachne_api.AttemptId) (*WorkspaceCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_invitation_decline(
			_pointer, FfiConverterBytesINSTANCE.Lower(request), FfiConverterOptionalAttemptIdINSTANCE.Lower(attemptId), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *WorkspaceCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Register an invitation link of `kind`. Adopt it with
// `adopt_invitation` after the candidate is saved.
func (_self *Client) StageInvitationOf(expiresAt uint64, kind InvitationKind) (*InvitationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_invitation_of(
			_pointer, FfiConverterUint64INSTANCE.Lower(expiresAt), FfiConverterInvitationKindINSTANCE.Lower(kind), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *InvitationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterInvitationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageJoin(welcome []byte, commits []JoinAdmissionStep) (*JoinCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_join(
			_pointer, FfiConverterBytesINSTANCE.Lower(welcome), FfiConverterSequenceJoinAdmissionStepINSTANCE.Lower(commits), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *JoinCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage an administrator action. Adopt it with `adopt_admission`.
func (_self *Client) StageManagement(action MemberAction) (*WorkspaceCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_management(
			_pointer, FfiConverterMemberActionINSTANCE.Lower(action), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *WorkspaceCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage the application's durable acceptance of a pending object. Save
// then adopt it with `adopt_protected_reception`.
func (_self *Client) StageObjectAcknowledgement(object ReceivedProtectedPublication) (*ProtectedReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_object_acknowledgement(
			_pointer, FfiConverterReceivedProtectedPublicationINSTANCE.Lower(object), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ProtectedReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterProtectedReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage a permanent application rejection of a pending object. Its
// identity stays recorded, so it is never delivered again.
func (_self *Client) StageObjectRejection(object ReceivedProtectedPublication) (*ProtectedReceptionCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_object_rejection(
			_pointer, FfiConverterReceivedProtectedPublicationINSTANCE.Lower(object), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *ProtectedReceptionCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterProtectedReceptionCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageProtectedPublication(workspace arachne_api.WorkspaceId, revision uint64, topic string, id arachne_api.RecordId, payload []byte) (*PublicationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_protected_publication(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeWorkspaceId(workspace)), FfiConverterUint64INSTANCE.Lower(revision), FfiConverterStringINSTANCE.Lower(topic),
			CFromRustBuffer(arachne_api.LowerToExternalTypeRecordId(id)), FfiConverterBytesINSTANCE.Lower(payload), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *PublicationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPublicationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageProtectedPublicationWithCurrent(workspace arachne_api.WorkspaceId, revision uint64, topic string, id arachne_api.RecordId, payload []byte, current *PublicationCurrent) (*PublicationCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_protected_publication_with_current(
			_pointer,
			CFromRustBuffer(arachne_api.LowerToExternalTypeWorkspaceId(workspace)), FfiConverterUint64INSTANCE.Lower(revision), FfiConverterStringINSTANCE.Lower(topic),
			CFromRustBuffer(arachne_api.LowerToExternalTypeRecordId(id)), FfiConverterBytesINSTANCE.Lower(payload), FfiConverterOptionalPublicationCurrentINSTANCE.Lower(current), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *PublicationCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterPublicationCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// `retain_until` is Unix seconds (UTC) by this node's clock; 0 keeps no
// copy for third-party recovery.
func (_self *Client) StageRecoveryRange(retainUntil uint64) (RecoveryStage, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_stage_recovery_range(
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

// The last member leaves alone. Adopt with `adopt_removal`.
func (_self *Client) StageSoloLeave() (*RemovalCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_solo_leave(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RemovalCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRemovalCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Rename the workspace. Adopt the candidate with `adopt_admission`.
func (_self *Client) StageWorkspaceName(workspaceName string) (*WorkspaceCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_workspace_name(
			_pointer, FfiConverterStringINSTANCE.Lower(workspaceName), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *WorkspaceCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Mark this session's signed workspace profile as a service.
// This grants no membership or publication rights.
func (_self *Client) UseServiceProfile() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_use_service_profile(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Park until the session may have work, up to `timeout` (`None`: no
// timeout). Holds no client or session lock. `Ok(true)`: drain the
// queues (or call `next_event`), then call again. `Ok(false)`: the
// timeout passed, `wake` was called, or the client closed.
func (_self *Client) WaitForWork(timeout *time.Duration) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_wait_for_work(
			_pointer, FfiConverterOptionalDurationINSTANCE.Lower(timeout), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Release one waiter (`wait_for_work` or `next_event`) without work,
// for example at host shutdown.
func (_self *Client) Wake() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_wake(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Stop advertising `workspace`, or every workspace for `None`.
func (_self *Client) WithdrawNearbyWorkspace(workspace *arachne_api.WorkspaceId) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_client_withdraw_nearby_workspace(
			_pointer, FfiConverterOptionalWorkspaceIdINSTANCE.Lower(workspace), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) WorkspaceState() (WorkspaceState, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_workspace_state(
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

// Advance a stored join. A pushed admission can return a candidate; adopt
// it through `adopt_join`. All other committed results are already durable.
func (_self *Client) DriveJoin() (JoinProgress, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_drive_join(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue JoinProgress
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinProgressINSTANCE.Lift(_uniffiRV), nil
	}
}

// Serve one queued operation and advance native durable lifecycle work.
// Presence remains part of the result from this same drive operation.
func (_self *Client) DriveWorkspace() (WorkspaceProgress, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_drive_workspace(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue WorkspaceProgress
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterWorkspaceProgressINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollMembershipUpdate() (**MemberUpdate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_membership_update(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue **MemberUpdate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalMemberUpdateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) RequestAdmission(peer arachne_api.EndpointId) (AdmissionResponse, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_request_admission(
				_pointer,
				CFromRustBuffer(arachne_api.LowerToExternalTypeEndpointId(peer)), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue AdmissionResponse
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterAdmissionResponseINSTANCE.Lift(_uniffiRV), nil
	}
}

// Stage the exact received history, including management and self-update steps.
func (_self *Client) StageJoinGrant(grant *AdmissionGrant) (*JoinCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_join_grant(
			_pointer, FfiConverterAdmissionGrantINSTANCE.Lower(grant), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *JoinCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterJoinCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageMembershipUpdate(update *MemberUpdate) (MembershipCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_stage_membership_update(
				_pointer, FfiConverterMemberUpdateINSTANCE.Lower(update), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue MembershipCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterMembershipCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) AdoptCurrentView(candidate *CurrentViewCandidate) (CurrentViewAdoption, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_adopt_current_view(
				_pointer, FfiConverterCurrentViewCandidateINSTANCE.Lower(candidate), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue CurrentViewAdoption
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterCurrentViewAdoptionINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) CancelCurrentView() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_cancel_current_view(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) CancelDirectRecovery() error {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_client_cancel_direct_recovery(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *Client) DiscoverRecoveryCutoff(request RecoveryCutoffRequest) (RecoveryCutoffStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_discover_recovery_cutoff(
				_pointer, FfiConverterRecoveryCutoffRequestINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryCutoffStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryCutoffStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// Fetch the current catalog view from the author or an authorized holder.
// This uses the signed selector, replacement keys and expiry metadata.
func (_self *Client) FetchCurrentView(request CurrentViewRequest) (CurrentViewStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_fetch_current_view(
				_pointer, FfiConverterCurrentViewRequestINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue CurrentViewStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterCurrentViewStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) FetchDirectRecovery(request DirectRecoveryRequest) (DirectRecoveryStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_fetch_direct_recovery(
				_pointer, FfiConverterDirectRecoveryRequestINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue DirectRecoveryStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterDirectRecoveryStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

// The next direct stream gap. No progress is accepted by this read.
func (_self *Client) NextDirectGap() (*DirectRecoveryRequest, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_next_direct_gap(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DirectRecoveryRequest
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalDirectRecoveryRequestINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollCurrentView() (*CurrentViewStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_current_view(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *CurrentViewStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalCurrentViewStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollDirectRecovery() (*DirectRecoveryStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_direct_recovery(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *DirectRecoveryStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalDirectRecoveryStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) PollRecoveryCutoff() (*RecoveryCutoffStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_poll_recovery_cutoff(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RecoveryCutoffStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalRecoveryCutoffStatusINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageCurrentView() (*CurrentViewCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_current_view(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *CurrentViewCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterCurrentViewCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

// Record an authenticated miss only after the recovery sources are exhausted.
func (_self *Client) StageDirectMiss() (*RecoveryCandidate, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_client_stage_direct_miss(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RecoveryCandidate
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryCandidateINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Client) StageDirectRecovery() (RecoveryStage, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_stage_direct_recovery(
				_pointer, _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue RecoveryStage
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRecoveryStageINSTANCE.Lift(_uniffiRV), nil
	}
}

// Start, poll, cancel or revoke a resource transfer. Member authorization
// and absolute-path checks run in the existing native resource service.
// `root` is the blob cache directory. The host must select immutable source
// files and check the content audience before it creates a grant.
func (_self *Client) Resource(request ResourceRequest) (ResourceStatus, error) {
	_pointer := _self.ffiObject.incrementPointer("*Client")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_client_resource(
				_pointer, FfiConverterResourceRequestINSTANCE.Lower(request), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue ResourceStatus
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterResourceStatusINSTANCE.Lift(_uniffiRV), nil
	}
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
				return C.uniffi_arachne_runtime_fn_clone_client(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_client(handle, status)
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

// Owns the sessions of one host and everything they share.
type ContextInterface interface {
	IsSuspended() bool
	Limits() arachne_api.Limits
	// Open a typed client in this context.
	Open(config ClientConfig) (*Client, error)
	Power() arachne_api.PowerProfile
	// Restart what `suspend` stopped and rebind sockets (`network_change`).
	Resume() error
	// Stop background work of every session for a host in the background
	// (Android): gossip overlays (HyParView shuffles, bootstrap retries)
	// are parked and presence rounds stop. Sessions, workspaces, routing
	// policy and queues stay; ops still run. Sessions opened while
	// suspended start suspended. Blocking: call it outside async code.
	// Each session is suspended after its op in flight ends.
	//
	// Idle connections close (the endpoint stays bound; later dials
	// reconnect) and the mDNS service of a LAN or nearby endpoint stops;
	// `resume` starts it again with the current addresses.
	Suspend() error
}

// Owns the sessions of one host and everything they share.
type Context struct {
	ffiObject FfiObject
}

// The lazy process default, for the handle API and foreign bindings.
func ContextDefaultShared() (*Context, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_constructor_context_default_shared(_uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Context
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterContextINSTANCE.Lift(_uniffiRV), nil
	}
}

// A separate context with its own limits, runtime and session table.
// Host-runtime handles stay available only through the Rust constructor.
func ContextOwned(limits arachne_api.Limits, power arachne_api.PowerProfile, workers uint32) (*Context, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_constructor_context_owned(
			CFromRustBuffer(arachne_api.FfiConverterLimitsINSTANCE.LowerExternal(limits)),
			CFromRustBuffer(arachne_api.FfiConverterPowerProfileINSTANCE.LowerExternal(power)), FfiConverterUint32INSTANCE.Lower(workers), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Context
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterContextINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Context) IsSuspended() bool {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_context_is_suspended(
			_pointer, _uniffiStatus)
	}))
}

func (_self *Context) Limits() arachne_api.Limits {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	return arachne_api.FfiConverterLimitsINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_context_limits(
				_pointer, _uniffiStatus),
		}
	}))
}

// Open a typed client in this context.
func (_self *Context) Open(config ClientConfig) (*Client, error) {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_context_open(
			_pointer, FfiConverterClientConfigINSTANCE.Lower(config), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Client
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterClientINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *Context) Power() arachne_api.PowerProfile {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	return arachne_api.FfiConverterPowerProfileINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_context_power(
				_pointer, _uniffiStatus),
		}
	}))
}

// Restart what `suspend` stopped and rebind sockets (`network_change`).
func (_self *Context) Resume() error {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_context_resume(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Stop background work of every session for a host in the background
// (Android): gossip overlays (HyParView shuffles, bootstrap retries)
// are parked and presence rounds stop. Sessions, workspaces, routing
// policy and queues stay; ops still run. Sessions opened while
// suspended start suspended. Blocking: call it outside async code.
// Each session is suspended after its op in flight ends.
//
// Idle connections close (the endpoint stays bound; later dials
// reconnect) and the mDNS service of a LAN or nearby endpoint stops;
// `resume` starts it again with the current addresses.
func (_self *Context) Suspend() error {
	_pointer := _self.ffiObject.incrementPointer("*Context")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_arachne_runtime_fn_method_context_suspend(
			_pointer, _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}
func (object *Context) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterContext struct{}

var FfiConverterContextINSTANCE = FfiConverterContext{}

func (c FfiConverterContext) Lift(handle C.uint64_t) *Context {
	result := &Context{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_context(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_context(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Context).Destroy)
	return result
}

func (c FfiConverterContext) Read(reader io.Reader) *Context {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterContext) Lower(value *Context) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Context")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterContext) Write(writer io.Writer, value *Context) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalContext(handle uint64) *Context {
	return FfiConverterContextINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalContext(value *Context) uint64 {
	return uint64(FfiConverterContextINSTANCE.Lower(value))
}

type FfiDestroyerContext struct{}

func (_ FfiDestroyerContext) Destroy(value *Context) {
	value.Destroy()
}

// An authenticated current view from an authorized holder. Adopt it with
// `adopt_current_view` before reading its objects from the durable inbox.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type CurrentViewCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// An authenticated current view from an authorized holder. Adopt it with
// `adopt_current_view` before reading its objects from the durable inbox.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type CurrentViewCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *CurrentViewCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*CurrentViewCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_currentviewcandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *CurrentViewCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*CurrentViewCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_currentviewcandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *CurrentViewCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterCurrentViewCandidate struct{}

var FfiConverterCurrentViewCandidateINSTANCE = FfiConverterCurrentViewCandidate{}

func (c FfiConverterCurrentViewCandidate) Lift(handle C.uint64_t) *CurrentViewCandidate {
	result := &CurrentViewCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_currentviewcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_currentviewcandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*CurrentViewCandidate).Destroy)
	return result
}

func (c FfiConverterCurrentViewCandidate) Read(reader io.Reader) *CurrentViewCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterCurrentViewCandidate) Lower(value *CurrentViewCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*CurrentViewCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterCurrentViewCandidate) Write(writer io.Writer, value *CurrentViewCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalCurrentViewCandidate(handle uint64) *CurrentViewCandidate {
	return FfiConverterCurrentViewCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalCurrentViewCandidate(value *CurrentViewCandidate) uint64 {
	return uint64(FfiConverterCurrentViewCandidateINSTANCE.Lower(value))
}

type FfiDestroyerCurrentViewCandidate struct{}

func (_ FfiDestroyerCurrentViewCandidate) Destroy(value *CurrentViewCandidate) {
	value.Destroy()
}

// An invitation registration. Adopt it with `adopt_invitation`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type InvitationCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// An invitation registration. Adopt it with `adopt_invitation`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type InvitationCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *InvitationCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*InvitationCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_invitationcandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *InvitationCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*InvitationCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_invitationcandidate_workspace(
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
				return C.uniffi_arachne_runtime_fn_clone_invitationcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_invitationcandidate(handle, status)
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

// A join. Adopt it with `adopt_join`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type JoinCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// A join. Adopt it with `adopt_join`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type JoinCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *JoinCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*JoinCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_joincandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *JoinCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*JoinCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_joincandidate_workspace(
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
				return C.uniffi_arachne_runtime_fn_clone_joincandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_joincandidate(handle, status)
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

// A decoded peer update. Only Core can construct it. Its state is a peer's
// observation until `stage_membership_update` verifies and stages the change.
type MemberUpdateInterface interface {
	Epoch() *uint64
	Fingerprint() *arachne_api.Key32
	Peer() *arachne_api.EndpointId
	State() MemberUpdateState
	Workspace() *arachne_api.WorkspaceId
}

// A decoded peer update. Only Core can construct it. Its state is a peer's
// observation until `stage_membership_update` verifies and stages the change.
type MemberUpdate struct {
	ffiObject FfiObject
}

func (_self *MemberUpdate) Epoch() *uint64 {
	_pointer := _self.ffiObject.incrementPointer("*MemberUpdate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_memberupdate_epoch(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *MemberUpdate) Fingerprint() *arachne_api.Key32 {
	_pointer := _self.ffiObject.incrementPointer("*MemberUpdate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalKey32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_memberupdate_fingerprint(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *MemberUpdate) Peer() *arachne_api.EndpointId {
	_pointer := _self.ffiObject.incrementPointer("*MemberUpdate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalEndpointIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_memberupdate_peer(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *MemberUpdate) State() MemberUpdateState {
	_pointer := _self.ffiObject.incrementPointer("*MemberUpdate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterMemberUpdateStateINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_memberupdate_state(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *MemberUpdate) Workspace() *arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*MemberUpdate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalWorkspaceIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_memberupdate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *MemberUpdate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterMemberUpdate struct{}

var FfiConverterMemberUpdateINSTANCE = FfiConverterMemberUpdate{}

func (c FfiConverterMemberUpdate) Lift(handle C.uint64_t) *MemberUpdate {
	result := &MemberUpdate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_memberupdate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_memberupdate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*MemberUpdate).Destroy)
	return result
}

func (c FfiConverterMemberUpdate) Read(reader io.Reader) *MemberUpdate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterMemberUpdate) Lower(value *MemberUpdate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*MemberUpdate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterMemberUpdate) Write(writer io.Writer, value *MemberUpdate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalMemberUpdate(handle uint64) *MemberUpdate {
	return FfiConverterMemberUpdateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalMemberUpdate(value *MemberUpdate) uint64 {
	return uint64(FfiConverterMemberUpdateINSTANCE.Lower(value))
}

type FfiDestroyerMemberUpdate struct{}

func (_ FfiDestroyerMemberUpdate) Destroy(value *MemberUpdate) {
	value.Destroy()
}

// A protected incoming publication, or an acknowledgement or rejection
// of a pending object. The authenticated plaintext is withheld until
// `adopt_protected_reception`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type ProtectedReceptionCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// A protected incoming publication, or an acknowledgement or rejection
// of a pending object. The authenticated plaintext is withheld until
// `adopt_protected_reception`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type ProtectedReceptionCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *ProtectedReceptionCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*ProtectedReceptionCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_protectedreceptioncandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *ProtectedReceptionCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*ProtectedReceptionCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_protectedreceptioncandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *ProtectedReceptionCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterProtectedReceptionCandidate struct{}

var FfiConverterProtectedReceptionCandidateINSTANCE = FfiConverterProtectedReceptionCandidate{}

func (c FfiConverterProtectedReceptionCandidate) Lift(handle C.uint64_t) *ProtectedReceptionCandidate {
	result := &ProtectedReceptionCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_protectedreceptioncandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_protectedreceptioncandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*ProtectedReceptionCandidate).Destroy)
	return result
}

func (c FfiConverterProtectedReceptionCandidate) Read(reader io.Reader) *ProtectedReceptionCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterProtectedReceptionCandidate) Lower(value *ProtectedReceptionCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*ProtectedReceptionCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterProtectedReceptionCandidate) Write(writer io.Writer, value *ProtectedReceptionCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalProtectedReceptionCandidate(handle uint64) *ProtectedReceptionCandidate {
	return FfiConverterProtectedReceptionCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalProtectedReceptionCandidate(value *ProtectedReceptionCandidate) uint64 {
	return uint64(FfiConverterProtectedReceptionCandidateINSTANCE.Lower(value))
}

type FfiDestroyerProtectedReceptionCandidate struct{}

func (_ FfiDestroyerProtectedReceptionCandidate) Destroy(value *ProtectedReceptionCandidate) {
	value.Destroy()
}

// A protected publication. Adopt it with `adopt_protected_publication`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type PublicationCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// A protected publication. Adopt it with `adopt_protected_publication`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type PublicationCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *PublicationCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*PublicationCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_publicationcandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *PublicationCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*PublicationCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_publicationcandidate_workspace(
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
				return C.uniffi_arachne_runtime_fn_clone_publicationcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_publicationcandidate(handle, status)
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

// A recovery range. Adopt it with `adopt_recovery`.
type RecoveryCandidateInterface interface {
	// See [`WorkspaceCandidate::discard`].
	Discard() (bool, error)
	// Objects the range brings into the durable inbox.
	PublicationCount() uint64
	Workspace() arachne_api.WorkspaceId
}

// A recovery range. Adopt it with `adopt_recovery`.
type RecoveryCandidate struct {
	ffiObject FfiObject
}

// See [`WorkspaceCandidate::discard`].
func (_self *RecoveryCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_recoverycandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Objects the range brings into the durable inbox.
func (_self *RecoveryCandidate) PublicationCount() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_method_recoverycandidate_publication_count(
			_pointer, _uniffiStatus)
	}))
}

func (_self *RecoveryCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*RecoveryCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_recoverycandidate_workspace(
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
				return C.uniffi_arachne_runtime_fn_clone_recoverycandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_recoverycandidate(handle, status)
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

// This member's removal. Adopt it with `adopt_removal`; that ends the session.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type RemovalCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// This member's removal. Adopt it with `adopt_removal`; that ends the session.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type RemovalCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *RemovalCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*RemovalCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_removalcandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *RemovalCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*RemovalCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_removalcandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *RemovalCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterRemovalCandidate struct{}

var FfiConverterRemovalCandidateINSTANCE = FfiConverterRemovalCandidate{}

func (c FfiConverterRemovalCandidate) Lift(handle C.uint64_t) *RemovalCandidate {
	result := &RemovalCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_removalcandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_removalcandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*RemovalCandidate).Destroy)
	return result
}

func (c FfiConverterRemovalCandidate) Read(reader io.Reader) *RemovalCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterRemovalCandidate) Lower(value *RemovalCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*RemovalCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterRemovalCandidate) Write(writer io.Writer, value *RemovalCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalRemovalCandidate(handle uint64) *RemovalCandidate {
	return FfiConverterRemovalCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalRemovalCandidate(value *RemovalCandidate) uint64 {
	return uint64(FfiConverterRemovalCandidateINSTANCE.Lower(value))
}

type FfiDestroyerRemovalCandidate struct{}

func (_ FfiDestroyerRemovalCandidate) Destroy(value *RemovalCandidate) {
	value.Destroy()
}

// Where a session keeps its workspace records. One store per workspace.
//
// `root` is the host's storage root key. It is not the endpoint secret:
// a session without an endpoint secret can persist, and a new endpoint
// identity does not change how the store is read.
//
// With [`StorageConfig::with_anchors`] (monotonic host storage), core saves
// the freshness anchor with every commit and restore requires a match: a
// rolled-back database is refused. Without it the anchor is optional: the
// host may save `record_freshness` itself and pass it to restore.
type StorageConfigInterface interface {
}

// Where a session keeps its workspace records. One store per workspace.
//
// `root` is the host's storage root key. It is not the endpoint secret:
// a session without an endpoint secret can persist, and a new endpoint
// identity does not change how the store is read.
//
// With [`StorageConfig::with_anchors`] (monotonic host storage), core saves
// the freshness anchor with every commit and restore requires a match: a
// rolled-back database is refused. Without it the anchor is optional: the
// host may save `record_freshness` itself and pass it to restore.
type StorageConfig struct {
	ffiObject FfiObject
}

// Open native SQLite storage from a host-private directory and a separate
// 32-byte storage root. The root is validated before a provider is made.
func StorageConfigOpenSqlite(directory string, root []byte) (*StorageConfig, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_arachne_runtime_fn_constructor_storageconfig_open_sqlite(FfiConverterStringINSTANCE.Lower(directory), FfiConverterBytesINSTANCE.Lower(root), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *StorageConfig
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterStorageConfigINSTANCE.Lift(_uniffiRV), nil
	}
}

func (object *StorageConfig) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterStorageConfig struct{}

var FfiConverterStorageConfigINSTANCE = FfiConverterStorageConfig{}

func (c FfiConverterStorageConfig) Lift(handle C.uint64_t) *StorageConfig {
	result := &StorageConfig{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_storageconfig(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_storageconfig(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*StorageConfig).Destroy)
	return result
}

func (c FfiConverterStorageConfig) Read(reader io.Reader) *StorageConfig {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterStorageConfig) Lower(value *StorageConfig) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*StorageConfig")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterStorageConfig) Write(writer io.Writer, value *StorageConfig) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalStorageConfig(handle uint64) *StorageConfig {
	return FfiConverterStorageConfigINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalStorageConfig(value *StorageConfig) uint64 {
	return uint64(FfiConverterStorageConfigINSTANCE.Lower(value))
}

type FfiDestroyerStorageConfig struct{}

func (_ FfiDestroyerStorageConfig) Destroy(value *StorageConfig) {
	value.Destroy()
}

// An admission, administrator action or workspace name change. Adopt it
// with `adopt_admission`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type WorkspaceCandidateInterface interface {
	// Drop the staged change. `false`: it was already used or is no
	// longer staged. A candidate already in storage cannot be
	// discarded (`WrongState`).
	Discard() (bool, error)
	Workspace() arachne_api.WorkspaceId
}

// An admission, administrator action or workspace name change. Adopt it
// with `adopt_admission`.
//
// Opaque, bound to the client that staged it, and usable one time:
// adopt it with its own adopt method, or `discard` it. Dropping it
// without adoption discards it.
type WorkspaceCandidate struct {
	ffiObject FfiObject
}

// Drop the staged change. `false`: it was already used or is no
// longer staged. A candidate already in storage cannot be
// discarded (`WrongState`).
func (_self *WorkspaceCandidate) Discard() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*WorkspaceCandidate")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*arachne_api.ApiError](arachne_api.FfiConverterApiError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_arachne_runtime_fn_method_workspacecandidate_discard(
			_pointer, _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

func (_self *WorkspaceCandidate) Workspace() arachne_api.WorkspaceId {
	_pointer := _self.ffiObject.incrementPointer("*WorkspaceCandidate")
	defer _self.ffiObject.decrementPointer()
	return func(value RustBufferI) arachne_api.WorkspaceId {
		return arachne_api.LiftFromExternalTypeWorkspaceId(RustBufferI(value))
	}(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_method_workspacecandidate_workspace(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *WorkspaceCandidate) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterWorkspaceCandidate struct{}

var FfiConverterWorkspaceCandidateINSTANCE = FfiConverterWorkspaceCandidate{}

func (c FfiConverterWorkspaceCandidate) Lift(handle C.uint64_t) *WorkspaceCandidate {
	result := &WorkspaceCandidate{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_arachne_runtime_fn_clone_workspacecandidate(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_arachne_runtime_fn_free_workspacecandidate(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*WorkspaceCandidate).Destroy)
	return result
}

func (c FfiConverterWorkspaceCandidate) Read(reader io.Reader) *WorkspaceCandidate {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterWorkspaceCandidate) Lower(value *WorkspaceCandidate) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*WorkspaceCandidate")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterWorkspaceCandidate) Write(writer io.Writer, value *WorkspaceCandidate) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalWorkspaceCandidate(handle uint64) *WorkspaceCandidate {
	return FfiConverterWorkspaceCandidateINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalWorkspaceCandidate(value *WorkspaceCandidate) uint64 {
	return uint64(FfiConverterWorkspaceCandidateINSTANCE.Lower(value))
}

type FfiDestroyerWorkspaceCandidate struct{}

func (_ FfiDestroyerWorkspaceCandidate) Destroy(value *WorkspaceCandidate) {
	value.Destroy()
}

// The one restart-safe workspace lifecycle value. Membership and delivery
// details remain separate projections; this value only answers where the
// workspace operation is and why it stopped.
type Activity struct {
	Phase  Phase
	Reason *string
}

func (r *Activity) Destroy() {
	FfiDestroyerPhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
}

type FfiConverterActivity struct{}

var FfiConverterActivityINSTANCE = FfiConverterActivity{}

func (c FfiConverterActivity) Lift(rb RustBufferI) Activity {
	return LiftFromRustBuffer[Activity](c, rb)
}

func (c FfiConverterActivity) Read(reader io.Reader) Activity {
	return Activity{
		FfiConverterPhaseINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterActivity) Lower(value Activity) C.RustBuffer {
	return LowerIntoRustBuffer[Activity](c, value)
}

func (c FfiConverterActivity) LowerExternal(value Activity) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Activity](c, value))
}

func (c FfiConverterActivity) Write(writer io.Writer, value Activity) {
	FfiConverterPhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
}

type FfiDestroyerActivity struct{}

func (_ FfiDestroyerActivity) Destroy(value Activity) {
	value.Destroy()
}

// One admission request that waits for an administrator.
type AdmissionApproval struct {
	AttemptId   arachne_api.AttemptId
	Endpoint    arachne_api.EndpointId
	Request     []byte
	DisplayName *string
	// The invitation approves automatically once an administrator binds it.
	Automatic bool
	// The host was told about this request.
	Delivered bool
	// The administrator's UI marked it as seen.
	Acknowledged bool
}

func (r *AdmissionApproval) Destroy() {
	arachne_api.FfiDestroyerTypeAttemptId{}.Destroy(r.AttemptId)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
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
		arachne_api.FfiConverterTypeAttemptIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeAttemptIdINSTANCE.Write(writer, value.AttemptId)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
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

// One page of pending approvals.
type AdmissionApprovalPage struct {
	Approvals []AdmissionApproval
	// No more rows after this page.
	Complete bool
	// Pass as `after` for the next page.
	NextAfter *arachne_api.AttemptId
}

func (r *AdmissionApprovalPage) Destroy() {
	FfiDestroyerSequenceAdmissionApproval{}.Destroy(r.Approvals)
	FfiDestroyerBool{}.Destroy(r.Complete)
	FfiDestroyerOptionalAttemptId{}.Destroy(r.NextAfter)
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
		FfiConverterOptionalAttemptIdINSTANCE.Read(reader),
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
	FfiConverterOptionalAttemptIdINSTANCE.Write(writer, value.NextAfter)
}

type FfiDestroyerAdmissionApprovalPage struct{}

func (_ FfiDestroyerAdmissionApprovalPage) Destroy(value AdmissionApprovalPage) {
	value.Destroy()
}

type AdmissionAuthorization struct {
	InvitationKey       arachne_api.Key32
	GrantSignature      []byte
	RedemptionSignature []byte
}

func (r *AdmissionAuthorization) Destroy() {
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
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
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
	FfiConverterBytesINSTANCE.Write(writer, value.GrantSignature)
	FfiConverterBytesINSTANCE.Write(writer, value.RedemptionSignature)
}

type FfiDestroyerAdmissionAuthorization struct{}

func (_ FfiDestroyerAdmissionAuthorization) Destroy(value AdmissionAuthorization) {
	value.Destroy()
}

// One admission notice. An attempt ID is present after native queuing;
// rejected pre-queue requests can still carry an approval notice.
type AdmissionNotice struct {
	AttemptId   *arachne_api.AttemptId
	Endpoint    arachne_api.EndpointId
	Request     []byte
	DisplayName *string
	Automatic   bool
}

func (r *AdmissionNotice) Destroy() {
	FfiDestroyerOptionalAttemptId{}.Destroy(r.AttemptId)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerBytes{}.Destroy(r.Request)
	FfiDestroyerOptionalString{}.Destroy(r.DisplayName)
	FfiDestroyerBool{}.Destroy(r.Automatic)
}

type FfiConverterAdmissionNotice struct{}

var FfiConverterAdmissionNoticeINSTANCE = FfiConverterAdmissionNotice{}

func (c FfiConverterAdmissionNotice) Lift(rb RustBufferI) AdmissionNotice {
	return LiftFromRustBuffer[AdmissionNotice](c, rb)
}

func (c FfiConverterAdmissionNotice) Read(reader io.Reader) AdmissionNotice {
	return AdmissionNotice{
		FfiConverterOptionalAttemptIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionNotice) Lower(value AdmissionNotice) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionNotice](c, value)
}

func (c FfiConverterAdmissionNotice) LowerExternal(value AdmissionNotice) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionNotice](c, value))
}

func (c FfiConverterAdmissionNotice) Write(writer io.Writer, value AdmissionNotice) {
	FfiConverterOptionalAttemptIdINSTANCE.Write(writer, value.AttemptId)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterBytesINSTANCE.Write(writer, value.Request)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.DisplayName)
	FfiConverterBoolINSTANCE.Write(writer, value.Automatic)
}

type FfiDestroyerAdmissionNotice struct{}

func (_ FfiDestroyerAdmissionNotice) Destroy(value AdmissionNotice) {
	value.Destroy()
}

type AdmissionReply struct {
	Workspace     arachne_api.WorkspaceId
	Epoch         uint64
	Commit        []byte
	Welcome       []byte
	Authorization AdmissionAuthorization
}

func (r *AdmissionReply) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterBytesINSTANCE.Write(writer, value.Commit)
	FfiConverterBytesINSTANCE.Write(writer, value.Welcome)
	FfiConverterAdmissionAuthorizationINSTANCE.Write(writer, value.Authorization)
}

type FfiDestroyerAdmissionReply struct{}

func (_ FfiDestroyerAdmissionReply) Destroy(value AdmissionReply) {
	value.Destroy()
}

type AdmissionStatus struct {
	State             AdmissionStatusKind
	Peer              *arachne_api.EndpointId
	Reason            *string
	Recovery          *string
	CheckpointPending bool
}

func (r *AdmissionStatus) Destroy() {
	FfiDestroyerAdmissionStatusKind{}.Destroy(r.State)
	FfiDestroyerOptionalEndpointId{}.Destroy(r.Peer)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
	FfiDestroyerOptionalString{}.Destroy(r.Recovery)
	FfiDestroyerBool{}.Destroy(r.CheckpointPending)
}

type FfiConverterAdmissionStatus struct{}

var FfiConverterAdmissionStatusINSTANCE = FfiConverterAdmissionStatus{}

func (c FfiConverterAdmissionStatus) Lift(rb RustBufferI) AdmissionStatus {
	return LiftFromRustBuffer[AdmissionStatus](c, rb)
}

func (c FfiConverterAdmissionStatus) Read(reader io.Reader) AdmissionStatus {
	return AdmissionStatus{
		FfiConverterAdmissionStatusKindINSTANCE.Read(reader),
		FfiConverterOptionalEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterAdmissionStatus) Lower(value AdmissionStatus) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionStatus](c, value)
}

func (c FfiConverterAdmissionStatus) LowerExternal(value AdmissionStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionStatus](c, value))
}

func (c FfiConverterAdmissionStatus) Write(writer io.Writer, value AdmissionStatus) {
	FfiConverterAdmissionStatusKindINSTANCE.Write(writer, value.State)
	FfiConverterOptionalEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Recovery)
	FfiConverterBoolINSTANCE.Write(writer, value.CheckpointPending)
}

type FfiDestroyerAdmissionStatus struct{}

func (_ FfiDestroyerAdmissionStatus) Destroy(value AdmissionStatus) {
	value.Destroy()
}

// Configuration for one workspace-facing runtime client.
type ClientConfig struct {
	Network arachne_api.Network
	Secret  *[]byte
	// Relay, lookup and deadline overrides. `Default` keeps the profile's.
	Transport TransportOptions
	// Record storage. Required to create, join or restore a workspace.
	Storage **StorageConfig
}

func (r *ClientConfig) Destroy() {
	arachne_api.FfiDestroyerNetwork{}.Destroy(r.Network)
	FfiDestroyerOptionalBytes{}.Destroy(r.Secret)
	FfiDestroyerTransportOptions{}.Destroy(r.Transport)
	FfiDestroyerOptionalStorageConfig{}.Destroy(r.Storage)
}

type FfiConverterClientConfig struct{}

var FfiConverterClientConfigINSTANCE = FfiConverterClientConfig{}

func (c FfiConverterClientConfig) Lift(rb RustBufferI) ClientConfig {
	return LiftFromRustBuffer[ClientConfig](c, rb)
}

func (c FfiConverterClientConfig) Read(reader io.Reader) ClientConfig {
	return ClientConfig{
		arachne_api.FfiConverterNetworkINSTANCE.Read(reader),
		FfiConverterOptionalBytesINSTANCE.Read(reader),
		FfiConverterTransportOptionsINSTANCE.Read(reader),
		FfiConverterOptionalStorageConfigINSTANCE.Read(reader),
	}
}

func (c FfiConverterClientConfig) Lower(value ClientConfig) C.RustBuffer {
	return LowerIntoRustBuffer[ClientConfig](c, value)
}

func (c FfiConverterClientConfig) LowerExternal(value ClientConfig) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ClientConfig](c, value))
}

func (c FfiConverterClientConfig) Write(writer io.Writer, value ClientConfig) {
	arachne_api.FfiConverterNetworkINSTANCE.Write(writer, value.Network)
	FfiConverterOptionalBytesINSTANCE.Write(writer, value.Secret)
	FfiConverterTransportOptionsINSTANCE.Write(writer, value.Transport)
	FfiConverterOptionalStorageConfigINSTANCE.Write(writer, value.Storage)
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

type ConnectivityReport struct {
	Workspace    arachne_api.WorkspaceId
	Paths        []PeerRoute
	PathsLimited bool
	ReceiveQueue uint64
	RepairJobs   uint64
}

func (r *ConnectivityReport) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerSequencePeerRoute{}.Destroy(r.Paths)
	FfiDestroyerBool{}.Destroy(r.PathsLimited)
	FfiDestroyerUint64{}.Destroy(r.ReceiveQueue)
	FfiDestroyerUint64{}.Destroy(r.RepairJobs)
}

type FfiConverterConnectivityReport struct{}

var FfiConverterConnectivityReportINSTANCE = FfiConverterConnectivityReport{}

func (c FfiConverterConnectivityReport) Lift(rb RustBufferI) ConnectivityReport {
	return LiftFromRustBuffer[ConnectivityReport](c, rb)
}

func (c FfiConverterConnectivityReport) Read(reader io.Reader) ConnectivityReport {
	return ConnectivityReport{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterSequencePeerRouteINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterConnectivityReport) Lower(value ConnectivityReport) C.RustBuffer {
	return LowerIntoRustBuffer[ConnectivityReport](c, value)
}

func (c FfiConverterConnectivityReport) LowerExternal(value ConnectivityReport) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ConnectivityReport](c, value))
}

func (c FfiConverterConnectivityReport) Write(writer io.Writer, value ConnectivityReport) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterSequencePeerRouteINSTANCE.Write(writer, value.Paths)
	FfiConverterBoolINSTANCE.Write(writer, value.PathsLimited)
	FfiConverterUint64INSTANCE.Write(writer, value.ReceiveQueue)
	FfiConverterUint64INSTANCE.Write(writer, value.RepairJobs)
}

type FfiDestroyerConnectivityReport struct{}

func (_ FfiDestroyerConnectivityReport) Destroy(value ConnectivityReport) {
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

type CurrentViewAdoption struct {
	Workspace WorkspaceInfo
	Cut       uint64
	Pending   uint64
	Stale     uint64
}

func (r *CurrentViewAdoption) Destroy() {
	FfiDestroyerWorkspaceInfo{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Cut)
	FfiDestroyerUint64{}.Destroy(r.Pending)
	FfiDestroyerUint64{}.Destroy(r.Stale)
}

type FfiConverterCurrentViewAdoption struct{}

var FfiConverterCurrentViewAdoptionINSTANCE = FfiConverterCurrentViewAdoption{}

func (c FfiConverterCurrentViewAdoption) Lift(rb RustBufferI) CurrentViewAdoption {
	return LiftFromRustBuffer[CurrentViewAdoption](c, rb)
}

func (c FfiConverterCurrentViewAdoption) Read(reader io.Reader) CurrentViewAdoption {
	return CurrentViewAdoption{
		FfiConverterWorkspaceInfoINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterCurrentViewAdoption) Lower(value CurrentViewAdoption) C.RustBuffer {
	return LowerIntoRustBuffer[CurrentViewAdoption](c, value)
}

func (c FfiConverterCurrentViewAdoption) LowerExternal(value CurrentViewAdoption) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[CurrentViewAdoption](c, value))
}

func (c FfiConverterCurrentViewAdoption) Write(writer io.Writer, value CurrentViewAdoption) {
	FfiConverterWorkspaceInfoINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Cut)
	FfiConverterUint64INSTANCE.Write(writer, value.Pending)
	FfiConverterUint64INSTANCE.Write(writer, value.Stale)
}

type FfiDestroyerCurrentViewAdoption struct{}

func (_ FfiDestroyerCurrentViewAdoption) Destroy(value CurrentViewAdoption) {
	value.Destroy()
}

type CurrentViewRequest struct {
	// None discovers authorized holders through the existing fabric paths.
	Peer      *arachne_api.EndpointId
	Authority arachne_api.MemberId
	Revision  uint64
	Topic     string
	Selector  arachne_api.Key32
}

func (r *CurrentViewRequest) Destroy() {
	FfiDestroyerOptionalEndpointId{}.Destroy(r.Peer)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Authority)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerString{}.Destroy(r.Topic)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.Selector)
}

type FfiConverterCurrentViewRequest struct{}

var FfiConverterCurrentViewRequestINSTANCE = FfiConverterCurrentViewRequest{}

func (c FfiConverterCurrentViewRequest) Lift(rb RustBufferI) CurrentViewRequest {
	return LiftFromRustBuffer[CurrentViewRequest](c, rb)
}

func (c FfiConverterCurrentViewRequest) Read(reader io.Reader) CurrentViewRequest {
	return CurrentViewRequest{
		FfiConverterOptionalEndpointIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
	}
}

func (c FfiConverterCurrentViewRequest) Lower(value CurrentViewRequest) C.RustBuffer {
	return LowerIntoRustBuffer[CurrentViewRequest](c, value)
}

func (c FfiConverterCurrentViewRequest) LowerExternal(value CurrentViewRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[CurrentViewRequest](c, value))
}

func (c FfiConverterCurrentViewRequest) Write(writer io.Writer, value CurrentViewRequest) {
	FfiConverterOptionalEndpointIdINSTANCE.Write(writer, value.Peer)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Authority)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.Selector)
}

type FfiDestroyerCurrentViewRequest struct{}

func (_ FfiDestroyerCurrentViewRequest) Destroy(value CurrentViewRequest) {
	value.Destroy()
}

type DeliveryFailure struct {
	Peer  arachne_api.EndpointId
	Error string
}

func (r *DeliveryFailure) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerString{}.Destroy(r.Error)
}

type FfiConverterDeliveryFailure struct{}

var FfiConverterDeliveryFailureINSTANCE = FfiConverterDeliveryFailure{}

func (c FfiConverterDeliveryFailure) Lift(rb RustBufferI) DeliveryFailure {
	return LiftFromRustBuffer[DeliveryFailure](c, rb)
}

func (c FfiConverterDeliveryFailure) Read(reader io.Reader) DeliveryFailure {
	return DeliveryFailure{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterStringINSTANCE.Write(writer, value.Error)
}

type FfiDestroyerDeliveryFailure struct{}

func (_ FfiDestroyerDeliveryFailure) Destroy(value DeliveryFailure) {
	value.Destroy()
}

type DeliveryReport struct {
	Admitted []arachne_api.EndpointId
	Queued   bool
	Failed   []DeliveryFailure
}

func (r *DeliveryReport) Destroy() {
	FfiDestroyerSequenceEndpointId{}.Destroy(r.Admitted)
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
		FfiConverterSequenceEndpointIdINSTANCE.Read(reader),
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
	FfiConverterSequenceEndpointIdINSTANCE.Write(writer, value.Admitted)
	FfiConverterBoolINSTANCE.Write(writer, value.Queued)
	FfiConverterSequenceDeliveryFailureINSTANCE.Write(writer, value.Failed)
}

type FfiDestroyerDeliveryReport struct{}

func (_ FfiDestroyerDeliveryReport) Destroy(value DeliveryReport) {
	value.Destroy()
}

type DirectRecoveryReady struct {
	Workspace     arachne_api.WorkspaceId
	Author        arachne_api.MemberId
	Peer          arachne_api.EndpointId
	Epoch         uint64
	Revision      uint64
	Topic         string
	After         uint64
	Through       uint64
	PacketCount   uint64
	RetainedBytes uint64
	Attempted     uint64
}

func (r *DirectRecoveryReady) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Author)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerString{}.Destroy(r.Topic)
	FfiDestroyerUint64{}.Destroy(r.After)
	FfiDestroyerUint64{}.Destroy(r.Through)
	FfiDestroyerUint64{}.Destroy(r.PacketCount)
	FfiDestroyerUint64{}.Destroy(r.RetainedBytes)
	FfiDestroyerUint64{}.Destroy(r.Attempted)
}

type FfiConverterDirectRecoveryReady struct{}

var FfiConverterDirectRecoveryReadyINSTANCE = FfiConverterDirectRecoveryReady{}

func (c FfiConverterDirectRecoveryReady) Lift(rb RustBufferI) DirectRecoveryReady {
	return LiftFromRustBuffer[DirectRecoveryReady](c, rb)
}

func (c FfiConverterDirectRecoveryReady) Read(reader io.Reader) DirectRecoveryReady {
	return DirectRecoveryReady{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterDirectRecoveryReady) Lower(value DirectRecoveryReady) C.RustBuffer {
	return LowerIntoRustBuffer[DirectRecoveryReady](c, value)
}

func (c FfiConverterDirectRecoveryReady) LowerExternal(value DirectRecoveryReady) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DirectRecoveryReady](c, value))
}

func (c FfiConverterDirectRecoveryReady) Write(writer io.Writer, value DirectRecoveryReady) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Author)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	FfiConverterUint64INSTANCE.Write(writer, value.After)
	FfiConverterUint64INSTANCE.Write(writer, value.Through)
	FfiConverterUint64INSTANCE.Write(writer, value.PacketCount)
	FfiConverterUint64INSTANCE.Write(writer, value.RetainedBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.Attempted)
}

type FfiDestroyerDirectRecoveryReady struct{}

func (_ FfiDestroyerDirectRecoveryReady) Destroy(value DirectRecoveryReady) {
	value.Destroy()
}

type DirectRecoveryRequest struct {
	Author     arachne_api.MemberId
	Revision   uint64
	Topic      string
	Recipients []arachne_api.MemberId
	After      uint64
	Through    uint64
}

func (r *DirectRecoveryRequest) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Author)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerString{}.Destroy(r.Topic)
	FfiDestroyerSequenceMemberId{}.Destroy(r.Recipients)
	FfiDestroyerUint64{}.Destroy(r.After)
	FfiDestroyerUint64{}.Destroy(r.Through)
}

type FfiConverterDirectRecoveryRequest struct{}

var FfiConverterDirectRecoveryRequestINSTANCE = FfiConverterDirectRecoveryRequest{}

func (c FfiConverterDirectRecoveryRequest) Lift(rb RustBufferI) DirectRecoveryRequest {
	return LiftFromRustBuffer[DirectRecoveryRequest](c, rb)
}

func (c FfiConverterDirectRecoveryRequest) Read(reader io.Reader) DirectRecoveryRequest {
	return DirectRecoveryRequest{
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterSequenceMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterDirectRecoveryRequest) Lower(value DirectRecoveryRequest) C.RustBuffer {
	return LowerIntoRustBuffer[DirectRecoveryRequest](c, value)
}

func (c FfiConverterDirectRecoveryRequest) LowerExternal(value DirectRecoveryRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DirectRecoveryRequest](c, value))
}

func (c FfiConverterDirectRecoveryRequest) Write(writer io.Writer, value DirectRecoveryRequest) {
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Author)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	FfiConverterSequenceMemberIdINSTANCE.Write(writer, value.Recipients)
	FfiConverterUint64INSTANCE.Write(writer, value.After)
	FfiConverterUint64INSTANCE.Write(writer, value.Through)
}

type FfiDestroyerDirectRecoveryRequest struct{}

func (_ FfiDestroyerDirectRecoveryRequest) Destroy(value DirectRecoveryRequest) {
	value.Destroy()
}

// A duration series measured in microseconds since the endpoint started.
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

type EndpointInfo struct {
	EndpointKey    arachne_api.EndpointId
	BoundAddress   string
	WorkspaceReady bool
	Transport      TransportInfo
}

func (r *EndpointInfo) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.EndpointKey)
	FfiDestroyerString{}.Destroy(r.BoundAddress)
	FfiDestroyerBool{}.Destroy(r.WorkspaceReady)
	FfiDestroyerTransportInfo{}.Destroy(r.Transport)
}

type FfiConverterEndpointInfo struct{}

var FfiConverterEndpointInfoINSTANCE = FfiConverterEndpointInfo{}

func (c FfiConverterEndpointInfo) Lift(rb RustBufferI) EndpointInfo {
	return LiftFromRustBuffer[EndpointInfo](c, rb)
}

func (c FfiConverterEndpointInfo) Read(reader io.Reader) EndpointInfo {
	return EndpointInfo{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterTransportInfoINSTANCE.Read(reader),
	}
}

func (c FfiConverterEndpointInfo) Lower(value EndpointInfo) C.RustBuffer {
	return LowerIntoRustBuffer[EndpointInfo](c, value)
}

func (c FfiConverterEndpointInfo) LowerExternal(value EndpointInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[EndpointInfo](c, value))
}

func (c FfiConverterEndpointInfo) Write(writer io.Writer, value EndpointInfo) {
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.EndpointKey)
	FfiConverterStringINSTANCE.Write(writer, value.BoundAddress)
	FfiConverterBoolINSTANCE.Write(writer, value.WorkspaceReady)
	FfiConverterTransportInfoINSTANCE.Write(writer, value.Transport)
}

type FfiDestroyerEndpointInfo struct{}

func (_ FfiDestroyerEndpointInfo) Destroy(value EndpointInfo) {
	value.Destroy()
}

type InterestObservation struct {
	Workspace  arachne_api.WorkspaceId
	Revision   uint64
	Topic      string
	Subscribed bool
	Admission  DeliveryReport
}

func (r *InterestObservation) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
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
	Workspace  arachne_api.WorkspaceId
	Checkpoint []byte
	Peer       arachne_api.EndpointId
}

func (r *InvitationCheckpoint) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerBytes{}.Destroy(r.Checkpoint)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
}

type FfiConverterInvitationCheckpoint struct{}

var FfiConverterInvitationCheckpointINSTANCE = FfiConverterInvitationCheckpoint{}

func (c FfiConverterInvitationCheckpoint) Lift(rb RustBufferI) InvitationCheckpoint {
	return LiftFromRustBuffer[InvitationCheckpoint](c, rb)
}

func (c FfiConverterInvitationCheckpoint) Read(reader io.Reader) InvitationCheckpoint {
	return InvitationCheckpoint{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
	}
}

func (c FfiConverterInvitationCheckpoint) Lower(value InvitationCheckpoint) C.RustBuffer {
	return LowerIntoRustBuffer[InvitationCheckpoint](c, value)
}

func (c FfiConverterInvitationCheckpoint) LowerExternal(value InvitationCheckpoint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[InvitationCheckpoint](c, value))
}

func (c FfiConverterInvitationCheckpoint) Write(writer io.Writer, value InvitationCheckpoint) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterBytesINSTANCE.Write(writer, value.Checkpoint)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
}

type FfiDestroyerInvitationCheckpoint struct{}

func (_ FfiDestroyerInvitationCheckpoint) Destroy(value InvitationCheckpoint) {
	value.Destroy()
}

// One registered invitation link and its controls.
type InvitationControl struct {
	Number        uint64
	Key           arachne_api.Key32
	ExpiresAt     uint64
	Enabled       bool
	Personal      bool
	Automatic     bool
	RequestAccess bool
	Approved      bool
}

func (r *InvitationControl) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.Number)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.Key)
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
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.Key)
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

type InvitationDetails struct {
	Workspace     arachne_api.WorkspaceId
	InvitationKey arachne_api.Key32
	WorkspaceName *string
	Epoch         uint64
	Personal      bool
	Automatic     bool
	ExpiresAt     uint64
}

func (r *InvitationDetails) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
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

type InvitationInfo struct {
	Workspace      arachne_api.WorkspaceId
	WorkspaceName  *string
	Invitation     []byte
	InvitationKey  arachne_api.Key32
	Checkpoint     []byte
	Peer           arachne_api.EndpointId
	BootstrapPeers []arachne_api.EndpointId
	Address        string
	Routes         []RouteHint
}

func (r *InvitationInfo) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerBytes{}.Destroy(r.Invitation)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.InvitationKey)
	FfiDestroyerBytes{}.Destroy(r.Checkpoint)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerSequenceEndpointId{}.Destroy(r.BootstrapPeers)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterSequenceEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterBytesINSTANCE.Write(writer, value.Invitation)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.InvitationKey)
	FfiConverterBytesINSTANCE.Write(writer, value.Checkpoint)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterSequenceEndpointIdINSTANCE.Write(writer, value.BootstrapPeers)
	FfiConverterStringINSTANCE.Write(writer, value.Address)
	FfiConverterSequenceRouteHintINSTANCE.Write(writer, value.Routes)
}

type FfiDestroyerInvitationInfo struct{}

func (_ FfiDestroyerInvitationInfo) Destroy(value InvitationInfo) {
	value.Destroy()
}

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

type JoinRequest struct {
	Workspace        arachne_api.WorkspaceId
	Member           arachne_api.MemberId
	Endpoint         arachne_api.EndpointId
	AdmissionRequest []byte
}

func (r *JoinRequest) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerBytes{}.Destroy(r.AdmissionRequest)
}

type FfiConverterJoinRequest struct{}

var FfiConverterJoinRequestINSTANCE = FfiConverterJoinRequest{}

func (c FfiConverterJoinRequest) Lift(rb RustBufferI) JoinRequest {
	return LiftFromRustBuffer[JoinRequest](c, rb)
}

func (c FfiConverterJoinRequest) Read(reader io.Reader) JoinRequest {
	return JoinRequest{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterBytesINSTANCE.Write(writer, value.AdmissionRequest)
}

type FfiDestroyerJoinRequest struct{}

func (_ FfiDestroyerJoinRequest) Destroy(value JoinRequest) {
	value.Destroy()
}

type MemberInfo struct {
	Id                 arachne_api.MemberId
	Endpoint           arachne_api.EndpointId
	Administrator      bool
	SelfMember         bool
	DisplayName        *string
	Kind               MemberKind
	Presence           Presence
	LastContactAgeMs   *uint64
	PresenceFreshForMs *uint64
}

func (r *MemberInfo) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Id)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
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
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Id)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
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

type MemberRoster struct {
	Workspace             arachne_api.WorkspaceId
	WorkspaceName         *string
	WorkspaceNameRevision uint64
	WorkspaceNameHead     arachne_api.Key32
	Epoch                 uint64
	Members               []MemberInfo
	ProfileCount          uint64
	ProfilesRetained      bool
}

func (r *MemberRoster) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerUint64{}.Destroy(r.WorkspaceNameRevision)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.WorkspaceNameHead)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterUint64INSTANCE.Write(writer, value.WorkspaceNameRevision)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.WorkspaceNameHead)
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

// One workspace a nearby device advertises.
type NearbyAdvertisement struct {
	Peer          arachne_api.EndpointId
	Mode          NearbyMode
	WorkspaceName *string
	Invitation    []byte
}

func (r *NearbyAdvertisement) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerNearbyMode{}.Destroy(r.Mode)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerBytes{}.Destroy(r.Invitation)
}

type FfiConverterNearbyAdvertisement struct{}

var FfiConverterNearbyAdvertisementINSTANCE = FfiConverterNearbyAdvertisement{}

func (c FfiConverterNearbyAdvertisement) Lift(rb RustBufferI) NearbyAdvertisement {
	return LiftFromRustBuffer[NearbyAdvertisement](c, rb)
}

func (c FfiConverterNearbyAdvertisement) Read(reader io.Reader) NearbyAdvertisement {
	return NearbyAdvertisement{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterNearbyModeINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterNearbyAdvertisement) Lower(value NearbyAdvertisement) C.RustBuffer {
	return LowerIntoRustBuffer[NearbyAdvertisement](c, value)
}

func (c FfiConverterNearbyAdvertisement) LowerExternal(value NearbyAdvertisement) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[NearbyAdvertisement](c, value))
}

func (c FfiConverterNearbyAdvertisement) Write(writer io.Writer, value NearbyAdvertisement) {
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterNearbyModeINSTANCE.Write(writer, value.Mode)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterBytesINSTANCE.Write(writer, value.Invitation)
}

type FfiDestroyerNearbyAdvertisement struct{}

func (_ FfiDestroyerNearbyAdvertisement) Destroy(value NearbyAdvertisement) {
	value.Destroy()
}

// A nearby endpoint and the name it announced, if it answered.
type NearbyEndpoint struct {
	Endpoint arachne_api.EndpointId
	Name     *string
}

func (r *NearbyEndpoint) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerOptionalString{}.Destroy(r.Name)
}

type FfiConverterNearbyEndpoint struct{}

var FfiConverterNearbyEndpointINSTANCE = FfiConverterNearbyEndpoint{}

func (c FfiConverterNearbyEndpoint) Lift(rb RustBufferI) NearbyEndpoint {
	return LiftFromRustBuffer[NearbyEndpoint](c, rb)
}

func (c FfiConverterNearbyEndpoint) Read(reader io.Reader) NearbyEndpoint {
	return NearbyEndpoint{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterNearbyEndpoint) Lower(value NearbyEndpoint) C.RustBuffer {
	return LowerIntoRustBuffer[NearbyEndpoint](c, value)
}

func (c FfiConverterNearbyEndpoint) LowerExternal(value NearbyEndpoint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[NearbyEndpoint](c, value))
}

func (c FfiConverterNearbyEndpoint) Write(writer io.Writer, value NearbyEndpoint) {
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Name)
}

type FfiDestroyerNearbyEndpoint struct{}

func (_ FfiDestroyerNearbyEndpoint) Destroy(value NearbyEndpoint) {
	value.Destroy()
}

// The result of one nearby workspace scan.
type NearbyScan struct {
	Workspaces       []NearbyAdvertisement
	EndpointsChecked uint64
	// The scan did not reach every nearby endpoint.
	Limited bool
}

func (r *NearbyScan) Destroy() {
	FfiDestroyerSequenceNearbyAdvertisement{}.Destroy(r.Workspaces)
	FfiDestroyerUint64{}.Destroy(r.EndpointsChecked)
	FfiDestroyerBool{}.Destroy(r.Limited)
}

type FfiConverterNearbyScan struct{}

var FfiConverterNearbyScanINSTANCE = FfiConverterNearbyScan{}

func (c FfiConverterNearbyScan) Lift(rb RustBufferI) NearbyScan {
	return LiftFromRustBuffer[NearbyScan](c, rb)
}

func (c FfiConverterNearbyScan) Read(reader io.Reader) NearbyScan {
	return NearbyScan{
		FfiConverterSequenceNearbyAdvertisementINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterNearbyScan) Lower(value NearbyScan) C.RustBuffer {
	return LowerIntoRustBuffer[NearbyScan](c, value)
}

func (c FfiConverterNearbyScan) LowerExternal(value NearbyScan) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[NearbyScan](c, value))
}

func (c FfiConverterNearbyScan) Write(writer io.Writer, value NearbyScan) {
	FfiConverterSequenceNearbyAdvertisementINSTANCE.Write(writer, value.Workspaces)
	FfiConverterUint64INSTANCE.Write(writer, value.EndpointsChecked)
	FfiConverterBoolINSTANCE.Write(writer, value.Limited)
}

type FfiDestroyerNearbyScan struct{}

func (_ FfiDestroyerNearbyScan) Destroy(value NearbyScan) {
	value.Destroy()
}

// Relays run by the deployment operator.
type OperatorRelay struct {
	// Relay URLs, for example `https://relay.example.org`.
	Urls []string
	// How the relays' TLS certificates are checked.
	Trust RelayTrust
}

func (r *OperatorRelay) Destroy() {
	FfiDestroyerSequenceString{}.Destroy(r.Urls)
	FfiDestroyerRelayTrust{}.Destroy(r.Trust)
}

type FfiConverterOperatorRelay struct{}

var FfiConverterOperatorRelayINSTANCE = FfiConverterOperatorRelay{}

func (c FfiConverterOperatorRelay) Lift(rb RustBufferI) OperatorRelay {
	return LiftFromRustBuffer[OperatorRelay](c, rb)
}

func (c FfiConverterOperatorRelay) Read(reader io.Reader) OperatorRelay {
	return OperatorRelay{
		FfiConverterSequenceStringINSTANCE.Read(reader),
		FfiConverterRelayTrustINSTANCE.Read(reader),
	}
}

func (c FfiConverterOperatorRelay) Lower(value OperatorRelay) C.RustBuffer {
	return LowerIntoRustBuffer[OperatorRelay](c, value)
}

func (c FfiConverterOperatorRelay) LowerExternal(value OperatorRelay) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[OperatorRelay](c, value))
}

func (c FfiConverterOperatorRelay) Write(writer io.Writer, value OperatorRelay) {
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Urls)
	FfiConverterRelayTrustINSTANCE.Write(writer, value.Trust)
}

type FfiDestroyerOperatorRelay struct{}

func (_ FfiDestroyerOperatorRelay) Destroy(value OperatorRelay) {
	value.Destroy()
}

type PeerPolicy struct {
	Peer      arachne_api.EndpointId
	Publish   []string
	Subscribe []string
}

func (r *PeerPolicy) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerSequenceString{}.Destroy(r.Publish)
	FfiDestroyerSequenceString{}.Destroy(r.Subscribe)
}

type FfiConverterPeerPolicy struct{}

var FfiConverterPeerPolicyINSTANCE = FfiConverterPeerPolicy{}

func (c FfiConverterPeerPolicy) Lift(rb RustBufferI) PeerPolicy {
	return LiftFromRustBuffer[PeerPolicy](c, rb)
}

func (c FfiConverterPeerPolicy) Read(reader io.Reader) PeerPolicy {
	return PeerPolicy{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterSequenceStringINSTANCE.Read(reader),
		FfiConverterSequenceStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterPeerPolicy) Lower(value PeerPolicy) C.RustBuffer {
	return LowerIntoRustBuffer[PeerPolicy](c, value)
}

func (c FfiConverterPeerPolicy) LowerExternal(value PeerPolicy) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PeerPolicy](c, value))
}

func (c FfiConverterPeerPolicy) Write(writer io.Writer, value PeerPolicy) {
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Publish)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Subscribe)
}

type FfiDestroyerPeerPolicy struct{}

func (_ FfiDestroyerPeerPolicy) Destroy(value PeerPolicy) {
	value.Destroy()
}

type PeerRoute struct {
	Member arachne_api.MemberId
	Route  RouteKind
	RttMs  uint64
}

func (r *PeerRoute) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Member)
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
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	FfiConverterRouteKindINSTANCE.Write(writer, value.Route)
	FfiConverterUint64INSTANCE.Write(writer, value.RttMs)
}

type FfiDestroyerPeerRoute struct{}

func (_ FfiDestroyerPeerRoute) Destroy(value PeerRoute) {
	value.Destroy()
}

// The outcome of one presence round.
type PresenceRound struct {
	// A member this round started a membership query with.
	SyncPeer *arachne_api.EndpointId
	// Answers that failed, and the first failure's text.
	ResponseErrors uint32
	ResponseError  *string
}

func (r *PresenceRound) Destroy() {
	FfiDestroyerOptionalEndpointId{}.Destroy(r.SyncPeer)
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
		FfiConverterOptionalEndpointIdINSTANCE.Read(reader),
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
	FfiConverterOptionalEndpointIdINSTANCE.Write(writer, value.SyncPeer)
	FfiConverterUint32INSTANCE.Write(writer, value.ResponseErrors)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.ResponseError)
}

type FfiDestroyerPresenceRound struct{}

func (_ FfiDestroyerPresenceRound) Destroy(value PresenceRound) {
	value.Destroy()
}

type Publication struct {
	Workspace arachne_api.WorkspaceId
	Revision  uint64
	Sender    arachne_api.EndpointId
	Topic     string
	Payload   []byte
}

func (r *Publication) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Sender)
	FfiDestroyerString{}.Destroy(r.Topic)
	FfiDestroyerBytes{}.Destroy(r.Payload)
}

type FfiConverterPublication struct{}

var FfiConverterPublicationINSTANCE = FfiConverterPublication{}

func (c FfiConverterPublication) Lift(rb RustBufferI) Publication {
	return LiftFromRustBuffer[Publication](c, rb)
}

func (c FfiConverterPublication) Read(reader io.Reader) Publication {
	return Publication{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterPublication) Lower(value Publication) C.RustBuffer {
	return LowerIntoRustBuffer[Publication](c, value)
}

func (c FfiConverterPublication) LowerExternal(value Publication) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Publication](c, value))
}

func (c FfiConverterPublication) Write(writer io.Writer, value Publication) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Sender)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	FfiConverterBytesINSTANCE.Write(writer, value.Payload)
}

type FfiDestroyerPublication struct{}

func (_ FfiDestroyerPublication) Destroy(value Publication) {
	value.Destroy()
}

// Current-value metadata for a protected publication.
type PublicationCurrent struct {
	Selector       arachne_api.Key32
	ReplacementKey arachne_api.Key32
	// Unix seconds (UTC), by the author's clock. Receivers and holders allow
	// `arachne_delivery::EXPIRY_SKEW_SECONDS` of clock difference.
	ExpiresAt uint64
	Tombstone bool
}

func (r *PublicationCurrent) Destroy() {
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.Selector)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.ReplacementKey)
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
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.Selector)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.ReplacementKey)
	FfiConverterUint64INSTANCE.Write(writer, value.ExpiresAt)
	FfiConverterBoolINSTANCE.Write(writer, value.Tombstone)
}

type FfiDestroyerPublicationCurrent struct{}

func (_ FfiDestroyerPublicationCurrent) Destroy(value PublicationCurrent) {
	value.Destroy()
}

// An authenticated pending object from the durable inbox.
type ReceivedProtectedPublication struct {
	Workspace  arachne_api.WorkspaceId
	Revision   uint64
	Member     arachne_api.MemberId
	Endpoint   arachne_api.EndpointId
	Topic      string
	Id         arachne_api.RecordId
	Sequence   *uint64
	Payload    []byte
	Recipients []arachne_api.MemberId
	// The author epoch in which this object was authenticated.
	Epoch uint64
	// True after this node leaves the branch on which it accepted the object.
	FromLosingBranch bool
	// Author sender counter; identifies the object for acknowledgement.
	Counter uint64
	// Present for a latest-value (current) publication.
	Current *PublicationCurrent
}

func (r *ReceivedProtectedPublication) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerString{}.Destroy(r.Topic)
	arachne_api.FfiDestroyerTypeRecordId{}.Destroy(r.Id)
	FfiDestroyerOptionalUint64{}.Destroy(r.Sequence)
	FfiDestroyerBytes{}.Destroy(r.Payload)
	FfiDestroyerSequenceMemberId{}.Destroy(r.Recipients)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerBool{}.Destroy(r.FromLosingBranch)
	FfiDestroyerUint64{}.Destroy(r.Counter)
	FfiDestroyerOptionalPublicationCurrent{}.Destroy(r.Current)
}

type FfiConverterReceivedProtectedPublication struct{}

var FfiConverterReceivedProtectedPublicationINSTANCE = FfiConverterReceivedProtectedPublication{}

func (c FfiConverterReceivedProtectedPublication) Lift(rb RustBufferI) ReceivedProtectedPublication {
	return LiftFromRustBuffer[ReceivedProtectedPublication](c, rb)
}

func (c FfiConverterReceivedProtectedPublication) Read(reader io.Reader) ReceivedProtectedPublication {
	return ReceivedProtectedPublication{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeRecordIdINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterBytesINSTANCE.Read(reader),
		FfiConverterSequenceMemberIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterOptionalPublicationCurrentINSTANCE.Read(reader),
	}
}

func (c FfiConverterReceivedProtectedPublication) Lower(value ReceivedProtectedPublication) C.RustBuffer {
	return LowerIntoRustBuffer[ReceivedProtectedPublication](c, value)
}

func (c FfiConverterReceivedProtectedPublication) LowerExternal(value ReceivedProtectedPublication) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ReceivedProtectedPublication](c, value))
}

func (c FfiConverterReceivedProtectedPublication) Write(writer io.Writer, value ReceivedProtectedPublication) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterStringINSTANCE.Write(writer, value.Topic)
	arachne_api.FfiConverterTypeRecordIdINSTANCE.Write(writer, value.Id)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Sequence)
	FfiConverterBytesINSTANCE.Write(writer, value.Payload)
	FfiConverterSequenceMemberIdINSTANCE.Write(writer, value.Recipients)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterBoolINSTANCE.Write(writer, value.FromLosingBranch)
	FfiConverterUint64INSTANCE.Write(writer, value.Counter)
	FfiConverterOptionalPublicationCurrentINSTANCE.Write(writer, value.Current)
}

type FfiDestroyerReceivedProtectedPublication struct{}

func (_ FfiDestroyerReceivedProtectedPublication) Destroy(value ReceivedProtectedPublication) {
	value.Destroy()
}

type RecoveryAdoption struct {
	Workspace             arachne_api.WorkspaceId
	Epoch                 uint64
	MemberCount           uint64
	Durable               bool
	RecoveredPublications uint64
	MissingPublications   uint64
}

func (r *RecoveryAdoption) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
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

type RecoveryCutoffRequest struct {
	Peer     arachne_api.EndpointId
	Revision uint64
	Topics   []string
	Epoch    *uint64
}

func (r *RecoveryCutoffRequest) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerUint64{}.Destroy(r.Revision)
	FfiDestroyerSequenceString{}.Destroy(r.Topics)
	FfiDestroyerOptionalUint64{}.Destroy(r.Epoch)
}

type FfiConverterRecoveryCutoffRequest struct{}

var FfiConverterRecoveryCutoffRequestINSTANCE = FfiConverterRecoveryCutoffRequest{}

func (c FfiConverterRecoveryCutoffRequest) Lift(rb RustBufferI) RecoveryCutoffRequest {
	return LiftFromRustBuffer[RecoveryCutoffRequest](c, rb)
}

func (c FfiConverterRecoveryCutoffRequest) Read(reader io.Reader) RecoveryCutoffRequest {
	return RecoveryCutoffRequest{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterSequenceStringINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterRecoveryCutoffRequest) Lower(value RecoveryCutoffRequest) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryCutoffRequest](c, value)
}

func (c FfiConverterRecoveryCutoffRequest) LowerExternal(value RecoveryCutoffRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryCutoffRequest](c, value))
}

func (c FfiConverterRecoveryCutoffRequest) Write(writer io.Writer, value RecoveryCutoffRequest) {
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Topics)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Epoch)
}

type FfiDestroyerRecoveryCutoffRequest struct{}

func (_ FfiDestroyerRecoveryCutoffRequest) Destroy(value RecoveryCutoffRequest) {
	value.Destroy()
}

type RecoveryRangeReady struct {
	Workspace       arachne_api.WorkspaceId
	Author          arachne_api.MemberId
	Peer            arachne_api.EndpointId
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
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Author)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Author)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
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

type RecoveryRangeRequest struct {
	Peer     *arachne_api.EndpointId
	Author   *arachne_api.MemberId
	Revision uint64
	Topics   []string
	After    *uint64
	Through  *uint64
}

func (r *RecoveryRangeRequest) Destroy() {
	FfiDestroyerOptionalEndpointId{}.Destroy(r.Peer)
	FfiDestroyerOptionalMemberId{}.Destroy(r.Author)
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
		FfiConverterOptionalEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalMemberIdINSTANCE.Read(reader),
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
	FfiConverterOptionalEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterOptionalMemberIdINSTANCE.Write(writer, value.Author)
	FfiConverterUint64INSTANCE.Write(writer, value.Revision)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.Topics)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.After)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Through)
}

type FfiDestroyerRecoveryRangeRequest struct{}

func (_ FfiDestroyerRecoveryRangeRequest) Destroy(value RecoveryRangeRequest) {
	value.Destroy()
}

// This member's removal, adopted. The session has ended.
type RemovedMembership struct {
	Workspace    arachne_api.WorkspaceId
	Epoch        uint64
	Member       arachne_api.MemberId
	CommitDigest arachne_api.Key32
}

func (r *RemovedMembership) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.CommitDigest)
}

type FfiConverterRemovedMembership struct{}

var FfiConverterRemovedMembershipINSTANCE = FfiConverterRemovedMembership{}

func (c FfiConverterRemovedMembership) Lift(rb RustBufferI) RemovedMembership {
	return LiftFromRustBuffer[RemovedMembership](c, rb)
}

func (c FfiConverterRemovedMembership) Read(reader io.Reader) RemovedMembership {
	return RemovedMembership{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
	}
}

func (c FfiConverterRemovedMembership) Lower(value RemovedMembership) C.RustBuffer {
	return LowerIntoRustBuffer[RemovedMembership](c, value)
}

func (c FfiConverterRemovedMembership) LowerExternal(value RemovedMembership) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RemovedMembership](c, value))
}

func (c FfiConverterRemovedMembership) Write(writer io.Writer, value RemovedMembership) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.CommitDigest)
}

type FfiDestroyerRemovedMembership struct{}

func (_ FfiDestroyerRemovedMembership) Destroy(value RemovedMembership) {
	value.Destroy()
}

type ResourceTicket struct {
	Hash  arachne_api.Key32
	Size  uint64
	Grant arachne_api.Key32
}

func (r *ResourceTicket) Destroy() {
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.Hash)
	FfiDestroyerUint64{}.Destroy(r.Size)
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(r.Grant)
}

type FfiConverterResourceTicket struct{}

var FfiConverterResourceTicketINSTANCE = FfiConverterResourceTicket{}

func (c FfiConverterResourceTicket) Lift(rb RustBufferI) ResourceTicket {
	return LiftFromRustBuffer[ResourceTicket](c, rb)
}

func (c FfiConverterResourceTicket) Read(reader io.Reader) ResourceTicket {
	return ResourceTicket{
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
	}
}

func (c FfiConverterResourceTicket) Lower(value ResourceTicket) C.RustBuffer {
	return LowerIntoRustBuffer[ResourceTicket](c, value)
}

func (c FfiConverterResourceTicket) LowerExternal(value ResourceTicket) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ResourceTicket](c, value))
}

func (c FfiConverterResourceTicket) Write(writer io.Writer, value ResourceTicket) {
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.Hash)
	FfiConverterUint64INSTANCE.Write(writer, value.Size)
	arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, value.Grant)
}

type FfiDestroyerResourceTicket struct{}

func (_ FfiDestroyerResourceTicket) Destroy(value ResourceTicket) {
	value.Destroy()
}

// A restored pending join. `admission_request` is `None` until the
// invitation checkpoint is known.
type RestoredJoin struct {
	Workspace        arachne_api.WorkspaceId
	Member           arachne_api.MemberId
	Endpoint         arachne_api.EndpointId
	AdmissionRequest *[]byte
}

func (r *RestoredJoin) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(r.Member)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Endpoint)
	FfiDestroyerOptionalBytes{}.Destroy(r.AdmissionRequest)
}

type FfiConverterRestoredJoin struct{}

var FfiConverterRestoredJoinINSTANCE = FfiConverterRestoredJoin{}

func (c FfiConverterRestoredJoin) Lift(rb RustBufferI) RestoredJoin {
	return LiftFromRustBuffer[RestoredJoin](c, rb)
}

func (c FfiConverterRestoredJoin) Read(reader io.Reader) RestoredJoin {
	return RestoredJoin{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterRestoredJoin) Lower(value RestoredJoin) C.RustBuffer {
	return LowerIntoRustBuffer[RestoredJoin](c, value)
}

func (c FfiConverterRestoredJoin) LowerExternal(value RestoredJoin) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RestoredJoin](c, value))
}

func (c FfiConverterRestoredJoin) Write(writer io.Writer, value RestoredJoin) {
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, value.Member)
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Endpoint)
	FfiConverterOptionalBytesINSTANCE.Write(writer, value.AdmissionRequest)
}

type FfiDestroyerRestoredJoin struct{}

func (_ FfiDestroyerRestoredJoin) Destroy(value RestoredJoin) {
	value.Destroy()
}

type RouteHint struct {
	Peer    arachne_api.EndpointId
	Address string
}

func (r *RouteHint) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.Peer)
	FfiDestroyerString{}.Destroy(r.Address)
}

type FfiConverterRouteHint struct{}

var FfiConverterRouteHintINSTANCE = FfiConverterRouteHint{}

func (c FfiConverterRouteHint) Lift(rb RustBufferI) RouteHint {
	return LiftFromRustBuffer[RouteHint](c, rb)
}

func (c FfiConverterRouteHint) Read(reader io.Reader) RouteHint {
	return RouteHint{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterStringINSTANCE.Write(writer, value.Address)
}

type FfiDestroyerRouteHint struct{}

func (_ FfiDestroyerRouteHint) Destroy(value RouteHint) {
	value.Destroy()
}

// Counters of the stream transport. Queued data is not a remote receipt.
type StreamMetrics struct {
	SessionsTotal    uint64
	SessionsActive   uint64
	PacketsSent      uint64
	PacketsReceived  uint64
	RejectedSessions uint64
}

func (r *StreamMetrics) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.SessionsTotal)
	FfiDestroyerUint64{}.Destroy(r.SessionsActive)
	FfiDestroyerUint64{}.Destroy(r.PacketsSent)
	FfiDestroyerUint64{}.Destroy(r.PacketsReceived)
	FfiDestroyerUint64{}.Destroy(r.RejectedSessions)
}

type FfiConverterStreamMetrics struct{}

var FfiConverterStreamMetricsINSTANCE = FfiConverterStreamMetrics{}

func (c FfiConverterStreamMetrics) Lift(rb RustBufferI) StreamMetrics {
	return LiftFromRustBuffer[StreamMetrics](c, rb)
}

func (c FfiConverterStreamMetrics) Read(reader io.Reader) StreamMetrics {
	return StreamMetrics{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterStreamMetrics) Lower(value StreamMetrics) C.RustBuffer {
	return LowerIntoRustBuffer[StreamMetrics](c, value)
}

func (c FfiConverterStreamMetrics) LowerExternal(value StreamMetrics) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[StreamMetrics](c, value))
}

func (c FfiConverterStreamMetrics) Write(writer io.Writer, value StreamMetrics) {
	FfiConverterUint64INSTANCE.Write(writer, value.SessionsTotal)
	FfiConverterUint64INSTANCE.Write(writer, value.SessionsActive)
	FfiConverterUint64INSTANCE.Write(writer, value.PacketsSent)
	FfiConverterUint64INSTANCE.Write(writer, value.PacketsReceived)
	FfiConverterUint64INSTANCE.Write(writer, value.RejectedSessions)
}

type FfiDestroyerStreamMetrics struct{}

func (_ FfiDestroyerStreamMetrics) Destroy(value StreamMetrics) {
	value.Destroy()
}

// The transport services an endpoint was bound with.
type TransportInfo struct {
	// n0's public lookup is in use.
	PublicLookup bool
	// Operator relays replace n0's relays.
	OperatorRelay bool
	// The endpoint can find a peer by key alone (mDNS, n0 lookup or Tor).
	PeerIdLookup bool
	Timeouts     TransportTimeouts
}

func (r *TransportInfo) Destroy() {
	FfiDestroyerBool{}.Destroy(r.PublicLookup)
	FfiDestroyerBool{}.Destroy(r.OperatorRelay)
	FfiDestroyerBool{}.Destroy(r.PeerIdLookup)
	FfiDestroyerTransportTimeouts{}.Destroy(r.Timeouts)
}

type FfiConverterTransportInfo struct{}

var FfiConverterTransportInfoINSTANCE = FfiConverterTransportInfo{}

func (c FfiConverterTransportInfo) Lift(rb RustBufferI) TransportInfo {
	return LiftFromRustBuffer[TransportInfo](c, rb)
}

func (c FfiConverterTransportInfo) Read(reader io.Reader) TransportInfo {
	return TransportInfo{
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterTransportTimeoutsINSTANCE.Read(reader),
	}
}

func (c FfiConverterTransportInfo) Lower(value TransportInfo) C.RustBuffer {
	return LowerIntoRustBuffer[TransportInfo](c, value)
}

func (c FfiConverterTransportInfo) LowerExternal(value TransportInfo) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[TransportInfo](c, value))
}

func (c FfiConverterTransportInfo) Write(writer io.Writer, value TransportInfo) {
	FfiConverterBoolINSTANCE.Write(writer, value.PublicLookup)
	FfiConverterBoolINSTANCE.Write(writer, value.OperatorRelay)
	FfiConverterBoolINSTANCE.Write(writer, value.PeerIdLookup)
	FfiConverterTransportTimeoutsINSTANCE.Write(writer, value.Timeouts)
}

type FfiDestroyerTransportInfo struct{}

func (_ FfiDestroyerTransportInfo) Destroy(value TransportInfo) {
	value.Destroy()
}

// Transport overrides on top of a `Network` profile. Every field left
// `None` keeps the profile default.
type TransportOptions struct {
	// Operator relays. They replace n0's public relays.
	Relay *OperatorRelay
	// n0's public DNS/Pkarr address lookup and publishing. `Some(false)`
	// keeps a WAN endpoint away from n0; pair it with `relay`.
	PublicLookup *bool
	// Deadlines for a slow or constrained link.
	Timeouts *TransportTimeouts
	// Per-op deadline for this client's blocking ops, and for its bind.
	// At the deadline an op fails with `DeadlineExceeded` and the session
	// stays usable. `Client::set_deadline` changes it later.
	Deadline *time.Duration
}

func (r *TransportOptions) Destroy() {
	FfiDestroyerOptionalOperatorRelay{}.Destroy(r.Relay)
	FfiDestroyerOptionalBool{}.Destroy(r.PublicLookup)
	FfiDestroyerOptionalTransportTimeouts{}.Destroy(r.Timeouts)
	FfiDestroyerOptionalDuration{}.Destroy(r.Deadline)
}

type FfiConverterTransportOptions struct{}

var FfiConverterTransportOptionsINSTANCE = FfiConverterTransportOptions{}

func (c FfiConverterTransportOptions) Lift(rb RustBufferI) TransportOptions {
	return LiftFromRustBuffer[TransportOptions](c, rb)
}

func (c FfiConverterTransportOptions) Read(reader io.Reader) TransportOptions {
	return TransportOptions{
		FfiConverterOptionalOperatorRelayINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterOptionalTransportTimeoutsINSTANCE.Read(reader),
		FfiConverterOptionalDurationINSTANCE.Read(reader),
	}
}

func (c FfiConverterTransportOptions) Lower(value TransportOptions) C.RustBuffer {
	return LowerIntoRustBuffer[TransportOptions](c, value)
}

func (c FfiConverterTransportOptions) LowerExternal(value TransportOptions) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[TransportOptions](c, value))
}

func (c FfiConverterTransportOptions) Write(writer io.Writer, value TransportOptions) {
	FfiConverterOptionalOperatorRelayINSTANCE.Write(writer, value.Relay)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.PublicLookup)
	FfiConverterOptionalTransportTimeoutsINSTANCE.Write(writer, value.Timeouts)
	FfiConverterOptionalDurationINSTANCE.Write(writer, value.Deadline)
}

type FfiDestroyerTransportOptions struct{}

func (_ FfiDestroyerTransportOptions) Destroy(value TransportOptions) {
	value.Destroy()
}

// Transport deadlines. Each must be nonzero.
type TransportTimeouts struct {
	// One data exchange or resource admission, including its dial.
	Operation time.Duration
	// One dial.
	Dial time.Duration
	// How long a live broadcast waits for a first overlay neighbor.
	GossipJoin time.Duration
	// How long `close` waits for peers to acknowledge the close. It blocks
	// the caller, so keep it short. The default is 5 s on every network.
	CloseDrain time.Duration
}

func (r *TransportTimeouts) Destroy() {
	FfiDestroyerDuration{}.Destroy(r.Operation)
	FfiDestroyerDuration{}.Destroy(r.Dial)
	FfiDestroyerDuration{}.Destroy(r.GossipJoin)
	FfiDestroyerDuration{}.Destroy(r.CloseDrain)
}

type FfiConverterTransportTimeouts struct{}

var FfiConverterTransportTimeoutsINSTANCE = FfiConverterTransportTimeouts{}

func (c FfiConverterTransportTimeouts) Lift(rb RustBufferI) TransportTimeouts {
	return LiftFromRustBuffer[TransportTimeouts](c, rb)
}

func (c FfiConverterTransportTimeouts) Read(reader io.Reader) TransportTimeouts {
	return TransportTimeouts{
		FfiConverterDurationINSTANCE.Read(reader),
		FfiConverterDurationINSTANCE.Read(reader),
		FfiConverterDurationINSTANCE.Read(reader),
		FfiConverterDurationINSTANCE.Read(reader),
	}
}

func (c FfiConverterTransportTimeouts) Lower(value TransportTimeouts) C.RustBuffer {
	return LowerIntoRustBuffer[TransportTimeouts](c, value)
}

func (c FfiConverterTransportTimeouts) LowerExternal(value TransportTimeouts) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[TransportTimeouts](c, value))
}

func (c FfiConverterTransportTimeouts) Write(writer io.Writer, value TransportTimeouts) {
	FfiConverterDurationINSTANCE.Write(writer, value.Operation)
	FfiConverterDurationINSTANCE.Write(writer, value.Dial)
	FfiConverterDurationINSTANCE.Write(writer, value.GossipJoin)
	FfiConverterDurationINSTANCE.Write(writer, value.CloseDrain)
}

type FfiDestroyerTransportTimeouts struct{}

func (_ FfiDestroyerTransportTimeouts) Destroy(value TransportTimeouts) {
	value.Destroy()
}

type WorkspaceInfo struct {
	Workspace     arachne_api.WorkspaceId
	WorkspaceName *string
	Epoch         uint64
	MemberCount   uint64
	Durable       bool
	Phase         Phase
	Reason        *string
}

func (r *WorkspaceInfo) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalString{}.Destroy(r.WorkspaceName)
	FfiDestroyerUint64{}.Destroy(r.Epoch)
	FfiDestroyerUint64{}.Destroy(r.MemberCount)
	FfiDestroyerBool{}.Destroy(r.Durable)
	FfiDestroyerPhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
}

type FfiConverterWorkspaceInfo struct{}

var FfiConverterWorkspaceInfoINSTANCE = FfiConverterWorkspaceInfo{}

func (c FfiConverterWorkspaceInfo) Lift(rb RustBufferI) WorkspaceInfo {
	return LiftFromRustBuffer[WorkspaceInfo](c, rb)
}

func (c FfiConverterWorkspaceInfo) Read(reader io.Reader) WorkspaceInfo {
	return WorkspaceInfo{
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterPhaseINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.WorkspaceName)
	FfiConverterUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterUint64INSTANCE.Write(writer, value.MemberCount)
	FfiConverterBoolINSTANCE.Write(writer, value.Durable)
	FfiConverterPhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
}

type FfiDestroyerWorkspaceInfo struct{}

func (_ FfiDestroyerWorkspaceInfo) Destroy(value WorkspaceInfo) {
	value.Destroy()
}

// A bounded, read-only snapshot for native adapters and diagnostics.
//
// The snapshot contains local workspace state and counters only. It does not
// initiate repair, dialing, admission or publication work. Qualification
// receipts use a separate redacted projection; do not export this value as a
// public telemetry record because its path members are workspace-local IDs.
type WorkspaceMetrics struct {
	Workspace           arachne_api.WorkspaceId
	Phase               Phase
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
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerPhase{}.Destroy(r.Phase)
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
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
		FfiConverterPhaseINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterPhaseINSTANCE.Write(writer, value.Phase)
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

type WorkspaceProgress struct {
	State            WorkspaceProgressState
	Activity         Activity
	Presence         *PresenceRound
	Workspace        *arachne_api.WorkspaceId
	Epoch            *uint64
	MemberCount      *uint64
	Peer             *arachne_api.EndpointId
	Accepted         *bool
	ReplyQueued      *bool
	RemoteReceipt    bool
	Reason           *string
	Approval         *AdmissionNotice
	NearbyInvitation *[]byte
}

func (r *WorkspaceProgress) Destroy() {
	FfiDestroyerWorkspaceProgressState{}.Destroy(r.State)
	FfiDestroyerActivity{}.Destroy(r.Activity)
	FfiDestroyerOptionalPresenceRound{}.Destroy(r.Presence)
	FfiDestroyerOptionalWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerOptionalUint64{}.Destroy(r.Epoch)
	FfiDestroyerOptionalUint64{}.Destroy(r.MemberCount)
	FfiDestroyerOptionalEndpointId{}.Destroy(r.Peer)
	FfiDestroyerOptionalBool{}.Destroy(r.Accepted)
	FfiDestroyerOptionalBool{}.Destroy(r.ReplyQueued)
	FfiDestroyerBool{}.Destroy(r.RemoteReceipt)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
	FfiDestroyerOptionalAdmissionNotice{}.Destroy(r.Approval)
	FfiDestroyerOptionalBytes{}.Destroy(r.NearbyInvitation)
}

type FfiConverterWorkspaceProgress struct{}

var FfiConverterWorkspaceProgressINSTANCE = FfiConverterWorkspaceProgress{}

func (c FfiConverterWorkspaceProgress) Lift(rb RustBufferI) WorkspaceProgress {
	return LiftFromRustBuffer[WorkspaceProgress](c, rb)
}

func (c FfiConverterWorkspaceProgress) Read(reader io.Reader) WorkspaceProgress {
	return WorkspaceProgress{
		FfiConverterWorkspaceProgressStateINSTANCE.Read(reader),
		FfiConverterActivityINSTANCE.Read(reader),
		FfiConverterOptionalPresenceRoundINSTANCE.Read(reader),
		FfiConverterOptionalWorkspaceIdINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
		FfiConverterOptionalEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalAdmissionNoticeINSTANCE.Read(reader),
		FfiConverterOptionalBytesINSTANCE.Read(reader),
	}
}

func (c FfiConverterWorkspaceProgress) Lower(value WorkspaceProgress) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspaceProgress](c, value)
}

func (c FfiConverterWorkspaceProgress) LowerExternal(value WorkspaceProgress) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspaceProgress](c, value))
}

func (c FfiConverterWorkspaceProgress) Write(writer io.Writer, value WorkspaceProgress) {
	FfiConverterWorkspaceProgressStateINSTANCE.Write(writer, value.State)
	FfiConverterActivityINSTANCE.Write(writer, value.Activity)
	FfiConverterOptionalPresenceRoundINSTANCE.Write(writer, value.Presence)
	FfiConverterOptionalWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.Epoch)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.MemberCount)
	FfiConverterOptionalEndpointIdINSTANCE.Write(writer, value.Peer)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.Accepted)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.ReplyQueued)
	FfiConverterBoolINSTANCE.Write(writer, value.RemoteReceipt)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
	FfiConverterOptionalAdmissionNoticeINSTANCE.Write(writer, value.Approval)
	FfiConverterOptionalBytesINSTANCE.Write(writer, value.NearbyInvitation)
}

type FfiDestroyerWorkspaceProgress struct{}

func (_ FfiDestroyerWorkspaceProgress) Destroy(value WorkspaceProgress) {
	value.Destroy()
}

type WorkspaceState struct {
	EndpointKey    arachne_api.EndpointId
	Workspace      *arachne_api.WorkspaceId
	WorkspaceReady bool
	Durable        bool
	Phase          Phase
	Reason         *string
}

func (r *WorkspaceState) Destroy() {
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(r.EndpointKey)
	FfiDestroyerOptionalWorkspaceId{}.Destroy(r.Workspace)
	FfiDestroyerBool{}.Destroy(r.WorkspaceReady)
	FfiDestroyerBool{}.Destroy(r.Durable)
	FfiDestroyerPhase{}.Destroy(r.Phase)
	FfiDestroyerOptionalString{}.Destroy(r.Reason)
}

type FfiConverterWorkspaceState struct{}

var FfiConverterWorkspaceStateINSTANCE = FfiConverterWorkspaceState{}

func (c FfiConverterWorkspaceState) Lift(rb RustBufferI) WorkspaceState {
	return LiftFromRustBuffer[WorkspaceState](c, rb)
}

func (c FfiConverterWorkspaceState) Read(reader io.Reader) WorkspaceState {
	return WorkspaceState{
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
		FfiConverterOptionalWorkspaceIdINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterPhaseINSTANCE.Read(reader),
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
	arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, value.EndpointKey)
	FfiConverterOptionalWorkspaceIdINSTANCE.Write(writer, value.Workspace)
	FfiConverterBoolINSTANCE.Write(writer, value.WorkspaceReady)
	FfiConverterBoolINSTANCE.Write(writer, value.Durable)
	FfiConverterPhaseINSTANCE.Write(writer, value.Phase)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Reason)
}

type FfiDestroyerWorkspaceState struct{}

func (_ FfiDestroyerWorkspaceState) Destroy(value WorkspaceState) {
	value.Destroy()
}

type AdmissionResponse interface {
	Destroy()
}
type AdmissionResponseStatus struct {
	Status AdmissionStatus
}

func (e AdmissionResponseStatus) Destroy() {
	FfiDestroyerAdmissionStatus{}.Destroy(e.Status)
}

type AdmissionResponseGranted struct {
	Grant *AdmissionGrant
}

func (e AdmissionResponseGranted) Destroy() {
	FfiDestroyerAdmissionGrant{}.Destroy(e.Grant)
}

type FfiConverterAdmissionResponse struct{}

var FfiConverterAdmissionResponseINSTANCE = FfiConverterAdmissionResponse{}

func (c FfiConverterAdmissionResponse) Lift(rb RustBufferI) AdmissionResponse {
	return LiftFromRustBuffer[AdmissionResponse](c, rb)
}

func (c FfiConverterAdmissionResponse) Lower(value AdmissionResponse) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionResponse](c, value)
}

func (c FfiConverterAdmissionResponse) LowerExternal(value AdmissionResponse) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionResponse](c, value))
}
func (FfiConverterAdmissionResponse) Read(reader io.Reader) AdmissionResponse {
	id := readInt32(reader)
	switch id {
	case 1:
		return AdmissionResponseStatus{
			FfiConverterAdmissionStatusINSTANCE.Read(reader),
		}
	case 2:
		return AdmissionResponseGranted{
			FfiConverterAdmissionGrantINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterAdmissionResponse.Read()", id))
	}
}

func (FfiConverterAdmissionResponse) Write(writer io.Writer, value AdmissionResponse) {
	switch variant_value := value.(type) {
	case AdmissionResponseStatus:
		writeInt32(writer, 1)
		FfiConverterAdmissionStatusINSTANCE.Write(writer, variant_value.Status)
	case AdmissionResponseGranted:
		writeInt32(writer, 2)
		FfiConverterAdmissionGrantINSTANCE.Write(writer, variant_value.Grant)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterAdmissionResponse.Write", value))
	}
}

type FfiDestroyerAdmissionResponse struct{}

func (_ FfiDestroyerAdmissionResponse) Destroy(value AdmissionResponse) {
	value.Destroy()
}

type AdmissionStatusKind uint

const (
	AdmissionStatusKindAdmissionNotSent          AdmissionStatusKind = 1
	AdmissionStatusKindAdmissionPending          AdmissionStatusKind = 2
	AdmissionStatusKindAdmissionWaiting          AdmissionStatusKind = 3
	AdmissionStatusKindAdmissionQueued           AdmissionStatusKind = 4
	AdmissionStatusKindApprovalPending           AdmissionStatusKind = 5
	AdmissionStatusKindApprovalRequested         AdmissionStatusKind = 6
	AdmissionStatusKindAdmissionUnavailable      AdmissionStatusKind = 7
	AdmissionStatusKindAdmissionRecoveryRequired AdmissionStatusKind = 8
	AdmissionStatusKindAdmissionReplied          AdmissionStatusKind = 9
)

type FfiConverterAdmissionStatusKind struct{}

var FfiConverterAdmissionStatusKindINSTANCE = FfiConverterAdmissionStatusKind{}

func (c FfiConverterAdmissionStatusKind) Lift(rb RustBufferI) AdmissionStatusKind {
	return LiftFromRustBuffer[AdmissionStatusKind](c, rb)
}

func (c FfiConverterAdmissionStatusKind) Lower(value AdmissionStatusKind) C.RustBuffer {
	return LowerIntoRustBuffer[AdmissionStatusKind](c, value)
}

func (c FfiConverterAdmissionStatusKind) LowerExternal(value AdmissionStatusKind) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[AdmissionStatusKind](c, value))
}
func (FfiConverterAdmissionStatusKind) Read(reader io.Reader) AdmissionStatusKind {
	id := readInt32(reader)
	return AdmissionStatusKind(id)
}

func (FfiConverterAdmissionStatusKind) Write(writer io.Writer, value AdmissionStatusKind) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerAdmissionStatusKind struct{}

func (_ FfiDestroyerAdmissionStatusKind) Destroy(value AdmissionStatusKind) {
}

type CurrentViewStatus interface {
	Destroy()
}
type CurrentViewStatusSourceWaiting struct {
}

func (e CurrentViewStatusSourceWaiting) Destroy() {
}

type CurrentViewStatusPending struct {
	CandidateCount  uint64
	AutomaticSource bool
}

func (e CurrentViewStatusPending) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.CandidateCount)
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
}

type CurrentViewStatusReady struct {
	Cut             uint64
	ValueCount      uint64
	AutomaticSource bool
	Attempted       uint64
}

func (e CurrentViewStatusReady) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Cut)
	FfiDestroyerUint64{}.Destroy(e.ValueCount)
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
	FfiDestroyerUint64{}.Destroy(e.Attempted)
}

type CurrentViewStatusUnavailable struct {
	Attempted       *uint64
	Reason          string
	AutomaticSource bool
}

func (e CurrentViewStatusUnavailable) Destroy() {
	FfiDestroyerOptionalUint64{}.Destroy(e.Attempted)
	FfiDestroyerString{}.Destroy(e.Reason)
	FfiDestroyerBool{}.Destroy(e.AutomaticSource)
}

type CurrentViewStatusCancelled struct {
}

func (e CurrentViewStatusCancelled) Destroy() {
}

type FfiConverterCurrentViewStatus struct{}

var FfiConverterCurrentViewStatusINSTANCE = FfiConverterCurrentViewStatus{}

func (c FfiConverterCurrentViewStatus) Lift(rb RustBufferI) CurrentViewStatus {
	return LiftFromRustBuffer[CurrentViewStatus](c, rb)
}

func (c FfiConverterCurrentViewStatus) Lower(value CurrentViewStatus) C.RustBuffer {
	return LowerIntoRustBuffer[CurrentViewStatus](c, value)
}

func (c FfiConverterCurrentViewStatus) LowerExternal(value CurrentViewStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[CurrentViewStatus](c, value))
}
func (FfiConverterCurrentViewStatus) Read(reader io.Reader) CurrentViewStatus {
	id := readInt32(reader)
	switch id {
	case 1:
		return CurrentViewStatusSourceWaiting{}
	case 2:
		return CurrentViewStatusPending{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterBoolINSTANCE.Read(reader),
		}
	case 3:
		return CurrentViewStatusReady{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterBoolINSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 4:
		return CurrentViewStatusUnavailable{
			FfiConverterOptionalUint64INSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterBoolINSTANCE.Read(reader),
		}
	case 5:
		return CurrentViewStatusCancelled{}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterCurrentViewStatus.Read()", id))
	}
}

func (FfiConverterCurrentViewStatus) Write(writer io.Writer, value CurrentViewStatus) {
	switch variant_value := value.(type) {
	case CurrentViewStatusSourceWaiting:
		writeInt32(writer, 1)
	case CurrentViewStatusPending:
		writeInt32(writer, 2)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.CandidateCount)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
	case CurrentViewStatusReady:
		writeInt32(writer, 3)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Cut)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.ValueCount)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Attempted)
	case CurrentViewStatusUnavailable:
		writeInt32(writer, 4)
		FfiConverterOptionalUint64INSTANCE.Write(writer, variant_value.Attempted)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Reason)
		FfiConverterBoolINSTANCE.Write(writer, variant_value.AutomaticSource)
	case CurrentViewStatusCancelled:
		writeInt32(writer, 5)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterCurrentViewStatus.Write", value))
	}
}

type FfiDestroyerCurrentViewStatus struct{}

func (_ FfiDestroyerCurrentViewStatus) Destroy(value CurrentViewStatus) {
	value.Destroy()
}

type DirectRecoveryStatus interface {
	Destroy()
}
type DirectRecoveryStatusSourceWaiting struct {
}

func (e DirectRecoveryStatusSourceWaiting) Destroy() {
}

type DirectRecoveryStatusPending struct {
	CandidateCount uint64
}

func (e DirectRecoveryStatusPending) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.CandidateCount)
}

type DirectRecoveryStatusReady struct {
	Range DirectRecoveryReady
}

func (e DirectRecoveryStatusReady) Destroy() {
	FfiDestroyerDirectRecoveryReady{}.Destroy(e.Range)
}

type DirectRecoveryStatusSourceUnavailable struct {
	Attempted uint64
	Reason    string
}

func (e DirectRecoveryStatusSourceUnavailable) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Attempted)
	FfiDestroyerString{}.Destroy(e.Reason)
}

type DirectRecoveryStatusCancelled struct {
}

func (e DirectRecoveryStatusCancelled) Destroy() {
}

type FfiConverterDirectRecoveryStatus struct{}

var FfiConverterDirectRecoveryStatusINSTANCE = FfiConverterDirectRecoveryStatus{}

func (c FfiConverterDirectRecoveryStatus) Lift(rb RustBufferI) DirectRecoveryStatus {
	return LiftFromRustBuffer[DirectRecoveryStatus](c, rb)
}

func (c FfiConverterDirectRecoveryStatus) Lower(value DirectRecoveryStatus) C.RustBuffer {
	return LowerIntoRustBuffer[DirectRecoveryStatus](c, value)
}

func (c FfiConverterDirectRecoveryStatus) LowerExternal(value DirectRecoveryStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DirectRecoveryStatus](c, value))
}
func (FfiConverterDirectRecoveryStatus) Read(reader io.Reader) DirectRecoveryStatus {
	id := readInt32(reader)
	switch id {
	case 1:
		return DirectRecoveryStatusSourceWaiting{}
	case 2:
		return DirectRecoveryStatusPending{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 3:
		return DirectRecoveryStatusReady{
			FfiConverterDirectRecoveryReadyINSTANCE.Read(reader),
		}
	case 4:
		return DirectRecoveryStatusSourceUnavailable{
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 5:
		return DirectRecoveryStatusCancelled{}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterDirectRecoveryStatus.Read()", id))
	}
}

func (FfiConverterDirectRecoveryStatus) Write(writer io.Writer, value DirectRecoveryStatus) {
	switch variant_value := value.(type) {
	case DirectRecoveryStatusSourceWaiting:
		writeInt32(writer, 1)
	case DirectRecoveryStatusPending:
		writeInt32(writer, 2)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.CandidateCount)
	case DirectRecoveryStatusReady:
		writeInt32(writer, 3)
		FfiConverterDirectRecoveryReadyINSTANCE.Write(writer, variant_value.Range)
	case DirectRecoveryStatusSourceUnavailable:
		writeInt32(writer, 4)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Attempted)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Reason)
	case DirectRecoveryStatusCancelled:
		writeInt32(writer, 5)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterDirectRecoveryStatus.Write", value))
	}
}

type FfiDestroyerDirectRecoveryStatus struct{}

func (_ FfiDestroyerDirectRecoveryStatus) Destroy(value DirectRecoveryStatus) {
	value.Destroy()
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

type JoinProgress interface {
	Destroy()
}
type JoinProgressStatus struct {
	Status AdmissionStatus
}

func (e JoinProgressStatus) Destroy() {
	FfiDestroyerAdmissionStatus{}.Destroy(e.Status)
}

type JoinProgressAwaitingAdoption struct {
	Candidate *JoinCandidate
}

func (e JoinProgressAwaitingAdoption) Destroy() {
	FfiDestroyerJoinCandidate{}.Destroy(e.Candidate)
}

type JoinProgressJoined struct {
	Workspace WorkspaceInfo
}

func (e JoinProgressJoined) Destroy() {
	FfiDestroyerWorkspaceInfo{}.Destroy(e.Workspace)
}

type FfiConverterJoinProgress struct{}

var FfiConverterJoinProgressINSTANCE = FfiConverterJoinProgress{}

func (c FfiConverterJoinProgress) Lift(rb RustBufferI) JoinProgress {
	return LiftFromRustBuffer[JoinProgress](c, rb)
}

func (c FfiConverterJoinProgress) Lower(value JoinProgress) C.RustBuffer {
	return LowerIntoRustBuffer[JoinProgress](c, value)
}

func (c FfiConverterJoinProgress) LowerExternal(value JoinProgress) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[JoinProgress](c, value))
}
func (FfiConverterJoinProgress) Read(reader io.Reader) JoinProgress {
	id := readInt32(reader)
	switch id {
	case 1:
		return JoinProgressStatus{
			FfiConverterAdmissionStatusINSTANCE.Read(reader),
		}
	case 2:
		return JoinProgressAwaitingAdoption{
			FfiConverterJoinCandidateINSTANCE.Read(reader),
		}
	case 3:
		return JoinProgressJoined{
			FfiConverterWorkspaceInfoINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterJoinProgress.Read()", id))
	}
}

func (FfiConverterJoinProgress) Write(writer io.Writer, value JoinProgress) {
	switch variant_value := value.(type) {
	case JoinProgressStatus:
		writeInt32(writer, 1)
		FfiConverterAdmissionStatusINSTANCE.Write(writer, variant_value.Status)
	case JoinProgressAwaitingAdoption:
		writeInt32(writer, 2)
		FfiConverterJoinCandidateINSTANCE.Write(writer, variant_value.Candidate)
	case JoinProgressJoined:
		writeInt32(writer, 3)
		FfiConverterWorkspaceInfoINSTANCE.Write(writer, variant_value.Workspace)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterJoinProgress.Write", value))
	}
}

type FfiDestroyerJoinProgress struct{}

func (_ FfiDestroyerJoinProgress) Destroy(value JoinProgress) {
	value.Destroy()
}

// An administrator action on a member or an invitation link.
type MemberAction interface {
	Destroy()
}
type MemberActionPromote struct {
	Field0 arachne_api.MemberId
}

func (e MemberActionPromote) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Field0)
}

type MemberActionDemote struct {
	Field0 arachne_api.MemberId
}

func (e MemberActionDemote) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Field0)
}

type MemberActionRemove struct {
	Field0 arachne_api.MemberId
}

func (e MemberActionRemove) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Field0)
}

type MemberActionDisableInvitation struct {
	Field0 arachne_api.Key32
}

func (e MemberActionDisableInvitation) Destroy() {
	arachne_api.FfiDestroyerTypeKey32{}.Destroy(e.Field0)
}

type FfiConverterMemberAction struct{}

var FfiConverterMemberActionINSTANCE = FfiConverterMemberAction{}

func (c FfiConverterMemberAction) Lift(rb RustBufferI) MemberAction {
	return LiftFromRustBuffer[MemberAction](c, rb)
}

func (c FfiConverterMemberAction) Lower(value MemberAction) C.RustBuffer {
	return LowerIntoRustBuffer[MemberAction](c, value)
}

func (c FfiConverterMemberAction) LowerExternal(value MemberAction) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MemberAction](c, value))
}
func (FfiConverterMemberAction) Read(reader io.Reader) MemberAction {
	id := readInt32(reader)
	switch id {
	case 1:
		return MemberActionPromote{
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		}
	case 2:
		return MemberActionDemote{
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		}
	case 3:
		return MemberActionRemove{
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
		}
	case 4:
		return MemberActionDisableInvitation{
			arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterMemberAction.Read()", id))
	}
}

func (FfiConverterMemberAction) Write(writer io.Writer, value MemberAction) {
	switch variant_value := value.(type) {
	case MemberActionPromote:
		writeInt32(writer, 1)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Field0)
	case MemberActionDemote:
		writeInt32(writer, 2)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Field0)
	case MemberActionRemove:
		writeInt32(writer, 3)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Field0)
	case MemberActionDisableInvitation:
		writeInt32(writer, 4)
		arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, variant_value.Field0)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterMemberAction.Write", value))
	}
}

type FfiDestroyerMemberAction struct{}

func (_ FfiDestroyerMemberAction) Destroy(value MemberAction) {
	value.Destroy()
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

type MemberUpdateState interface {
	Destroy()
}
type MemberUpdateStateAvailable struct {
}

func (e MemberUpdateStateAvailable) Destroy() {
}

type MemberUpdateStateCurrent struct {
}

func (e MemberUpdateStateCurrent) Destroy() {
}

type MemberUpdateStateUnavailable struct {
}

func (e MemberUpdateStateUnavailable) Destroy() {
}

type MemberUpdateStateDenied struct {
}

func (e MemberUpdateStateDenied) Destroy() {
}

type MemberUpdateStateStale struct {
}

func (e MemberUpdateStateStale) Destroy() {
}

type MemberUpdateStatePeerBehind struct {
}

func (e MemberUpdateStatePeerBehind) Destroy() {
}

type MemberUpdateStateBranchMismatch struct {
}

func (e MemberUpdateStateBranchMismatch) Destroy() {
}

type MemberUpdateStateNameUpdateAvailable struct {
}

func (e MemberUpdateStateNameUpdateAvailable) Destroy() {
}

type MemberUpdateStateNameCheckpointAvailable struct {
}

func (e MemberUpdateStateNameCheckpointAvailable) Destroy() {
}

type MemberUpdateStateNamePeerBehind struct {
}

func (e MemberUpdateStateNamePeerBehind) Destroy() {
}

type MemberUpdateStateNameConflict struct {
}

func (e MemberUpdateStateNameConflict) Destroy() {
}

type MemberUpdateStateNameUnavailable struct {
}

func (e MemberUpdateStateNameUnavailable) Destroy() {
}

type MemberUpdateStateAwaitingAdoption struct {
}

func (e MemberUpdateStateAwaitingAdoption) Destroy() {
}

// A new advisory state. It grants no rights and carries no mutable protocol data.
type MemberUpdateStateOther struct {
	Name string
}

func (e MemberUpdateStateOther) Destroy() {
	FfiDestroyerString{}.Destroy(e.Name)
}

type FfiConverterMemberUpdateState struct{}

var FfiConverterMemberUpdateStateINSTANCE = FfiConverterMemberUpdateState{}

func (c FfiConverterMemberUpdateState) Lift(rb RustBufferI) MemberUpdateState {
	return LiftFromRustBuffer[MemberUpdateState](c, rb)
}

func (c FfiConverterMemberUpdateState) Lower(value MemberUpdateState) C.RustBuffer {
	return LowerIntoRustBuffer[MemberUpdateState](c, value)
}

func (c FfiConverterMemberUpdateState) LowerExternal(value MemberUpdateState) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MemberUpdateState](c, value))
}
func (FfiConverterMemberUpdateState) Read(reader io.Reader) MemberUpdateState {
	id := readInt32(reader)
	switch id {
	case 1:
		return MemberUpdateStateAvailable{}
	case 2:
		return MemberUpdateStateCurrent{}
	case 3:
		return MemberUpdateStateUnavailable{}
	case 4:
		return MemberUpdateStateDenied{}
	case 5:
		return MemberUpdateStateStale{}
	case 6:
		return MemberUpdateStatePeerBehind{}
	case 7:
		return MemberUpdateStateBranchMismatch{}
	case 8:
		return MemberUpdateStateNameUpdateAvailable{}
	case 9:
		return MemberUpdateStateNameCheckpointAvailable{}
	case 10:
		return MemberUpdateStateNamePeerBehind{}
	case 11:
		return MemberUpdateStateNameConflict{}
	case 12:
		return MemberUpdateStateNameUnavailable{}
	case 13:
		return MemberUpdateStateAwaitingAdoption{}
	case 14:
		return MemberUpdateStateOther{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterMemberUpdateState.Read()", id))
	}
}

func (FfiConverterMemberUpdateState) Write(writer io.Writer, value MemberUpdateState) {
	switch variant_value := value.(type) {
	case MemberUpdateStateAvailable:
		writeInt32(writer, 1)
	case MemberUpdateStateCurrent:
		writeInt32(writer, 2)
	case MemberUpdateStateUnavailable:
		writeInt32(writer, 3)
	case MemberUpdateStateDenied:
		writeInt32(writer, 4)
	case MemberUpdateStateStale:
		writeInt32(writer, 5)
	case MemberUpdateStatePeerBehind:
		writeInt32(writer, 6)
	case MemberUpdateStateBranchMismatch:
		writeInt32(writer, 7)
	case MemberUpdateStateNameUpdateAvailable:
		writeInt32(writer, 8)
	case MemberUpdateStateNameCheckpointAvailable:
		writeInt32(writer, 9)
	case MemberUpdateStateNamePeerBehind:
		writeInt32(writer, 10)
	case MemberUpdateStateNameConflict:
		writeInt32(writer, 11)
	case MemberUpdateStateNameUnavailable:
		writeInt32(writer, 12)
	case MemberUpdateStateAwaitingAdoption:
		writeInt32(writer, 13)
	case MemberUpdateStateOther:
		writeInt32(writer, 14)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Name)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterMemberUpdateState.Write", value))
	}
}

type FfiDestroyerMemberUpdateState struct{}

func (_ FfiDestroyerMemberUpdateState) Destroy(value MemberUpdateState) {
	value.Destroy()
}

type MembershipCandidate interface {
	Destroy()
}
type MembershipCandidateWorkspace struct {
	Candidate *WorkspaceCandidate
}

func (e MembershipCandidateWorkspace) Destroy() {
	FfiDestroyerWorkspaceCandidate{}.Destroy(e.Candidate)
}

type MembershipCandidateRemoval struct {
	Candidate *RemovalCandidate
}

func (e MembershipCandidateRemoval) Destroy() {
	FfiDestroyerRemovalCandidate{}.Destroy(e.Candidate)
}

type FfiConverterMembershipCandidate struct{}

var FfiConverterMembershipCandidateINSTANCE = FfiConverterMembershipCandidate{}

func (c FfiConverterMembershipCandidate) Lift(rb RustBufferI) MembershipCandidate {
	return LiftFromRustBuffer[MembershipCandidate](c, rb)
}

func (c FfiConverterMembershipCandidate) Lower(value MembershipCandidate) C.RustBuffer {
	return LowerIntoRustBuffer[MembershipCandidate](c, value)
}

func (c FfiConverterMembershipCandidate) LowerExternal(value MembershipCandidate) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[MembershipCandidate](c, value))
}
func (FfiConverterMembershipCandidate) Read(reader io.Reader) MembershipCandidate {
	id := readInt32(reader)
	switch id {
	case 1:
		return MembershipCandidateWorkspace{
			FfiConverterWorkspaceCandidateINSTANCE.Read(reader),
		}
	case 2:
		return MembershipCandidateRemoval{
			FfiConverterRemovalCandidateINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterMembershipCandidate.Read()", id))
	}
}

func (FfiConverterMembershipCandidate) Write(writer io.Writer, value MembershipCandidate) {
	switch variant_value := value.(type) {
	case MembershipCandidateWorkspace:
		writeInt32(writer, 1)
		FfiConverterWorkspaceCandidateINSTANCE.Write(writer, variant_value.Candidate)
	case MembershipCandidateRemoval:
		writeInt32(writer, 2)
		FfiConverterRemovalCandidateINSTANCE.Write(writer, variant_value.Candidate)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterMembershipCandidate.Write", value))
	}
}

type FfiDestroyerMembershipCandidate struct{}

func (_ FfiDestroyerMembershipCandidate) Destroy(value MembershipCandidate) {
	value.Destroy()
}

// How a nearby workspace admits people.
type NearbyMode uint

const (
	// A person asks; an administrator approves.
	NearbyModeRequestAccess NearbyMode = 1
	// Anyone nearby with the link may join.
	NearbyModeOpenJoining NearbyMode = 2
)

type FfiConverterNearbyMode struct{}

var FfiConverterNearbyModeINSTANCE = FfiConverterNearbyMode{}

func (c FfiConverterNearbyMode) Lift(rb RustBufferI) NearbyMode {
	return LiftFromRustBuffer[NearbyMode](c, rb)
}

func (c FfiConverterNearbyMode) Lower(value NearbyMode) C.RustBuffer {
	return LowerIntoRustBuffer[NearbyMode](c, value)
}

func (c FfiConverterNearbyMode) LowerExternal(value NearbyMode) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[NearbyMode](c, value))
}
func (FfiConverterNearbyMode) Read(reader io.Reader) NearbyMode {
	id := readInt32(reader)
	return NearbyMode(id)
}

func (FfiConverterNearbyMode) Write(writer io.Writer, value NearbyMode) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerNearbyMode struct{}

func (_ FfiDestroyerNearbyMode) Destroy(value NearbyMode) {
}

// Durable lifecycle phases projected to every adapter.
type Phase uint

const (
	PhaseEmpty         Phase = 1
	PhaseCreating      Phase = 2
	PhaseJoining       Phase = 3
	PhaseSynchronizing Phase = 4
	PhaseActive        Phase = 5
	PhaseRecovering    Phase = 6
	PhaseLeaving       Phase = 7
	PhaseResetting     Phase = 8
	PhaseRemoved       Phase = 9
	PhaseFailed        Phase = 10
)

type FfiConverterPhase struct{}

var FfiConverterPhaseINSTANCE = FfiConverterPhase{}

func (c FfiConverterPhase) Lift(rb RustBufferI) Phase {
	return LiftFromRustBuffer[Phase](c, rb)
}

func (c FfiConverterPhase) Lower(value Phase) C.RustBuffer {
	return LowerIntoRustBuffer[Phase](c, value)
}

func (c FfiConverterPhase) LowerExternal(value Phase) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Phase](c, value))
}
func (FfiConverterPhase) Read(reader io.Reader) Phase {
	id := readInt32(reader)
	return Phase(id)
}

func (FfiConverterPhase) Write(writer io.Writer, value Phase) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerPhase struct{}

func (_ FfiDestroyerPhase) Destroy(value Phase) {
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

type RecoveryCutoffStatus interface {
	Destroy()
}
type RecoveryCutoffStatusPending struct {
}

func (e RecoveryCutoffStatusPending) Destroy() {
}

type RecoveryCutoffStatusDenied struct {
}

func (e RecoveryCutoffStatusDenied) Destroy() {
}

type RecoveryCutoffStatusObserved struct {
	Workspace       arachne_api.WorkspaceId
	Author          arachne_api.MemberId
	Peer            arachne_api.EndpointId
	Epoch           uint64
	Revision        uint64
	Topics          []string
	Head            uint64
	RetainedAfter   uint64
	AcceptedThrough uint64
}

func (e RecoveryCutoffStatusObserved) Destroy() {
	arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(e.Workspace)
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Author)
	arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(e.Peer)
	FfiDestroyerUint64{}.Destroy(e.Epoch)
	FfiDestroyerUint64{}.Destroy(e.Revision)
	FfiDestroyerSequenceString{}.Destroy(e.Topics)
	FfiDestroyerUint64{}.Destroy(e.Head)
	FfiDestroyerUint64{}.Destroy(e.RetainedAfter)
	FfiDestroyerUint64{}.Destroy(e.AcceptedThrough)
}

type FfiConverterRecoveryCutoffStatus struct{}

var FfiConverterRecoveryCutoffStatusINSTANCE = FfiConverterRecoveryCutoffStatus{}

func (c FfiConverterRecoveryCutoffStatus) Lift(rb RustBufferI) RecoveryCutoffStatus {
	return LiftFromRustBuffer[RecoveryCutoffStatus](c, rb)
}

func (c FfiConverterRecoveryCutoffStatus) Lower(value RecoveryCutoffStatus) C.RustBuffer {
	return LowerIntoRustBuffer[RecoveryCutoffStatus](c, value)
}

func (c FfiConverterRecoveryCutoffStatus) LowerExternal(value RecoveryCutoffStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RecoveryCutoffStatus](c, value))
}
func (FfiConverterRecoveryCutoffStatus) Read(reader io.Reader) RecoveryCutoffStatus {
	id := readInt32(reader)
	switch id {
	case 1:
		return RecoveryCutoffStatusPending{}
	case 2:
		return RecoveryCutoffStatusDenied{}
	case 3:
		return RecoveryCutoffStatusObserved{
			arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader),
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
			arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterSequenceStringINSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
			FfiConverterUint64INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRecoveryCutoffStatus.Read()", id))
	}
}

func (FfiConverterRecoveryCutoffStatus) Write(writer io.Writer, value RecoveryCutoffStatus) {
	switch variant_value := value.(type) {
	case RecoveryCutoffStatusPending:
		writeInt32(writer, 1)
	case RecoveryCutoffStatusDenied:
		writeInt32(writer, 2)
	case RecoveryCutoffStatusObserved:
		writeInt32(writer, 3)
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, variant_value.Workspace)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Author)
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, variant_value.Peer)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Epoch)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Revision)
		FfiConverterSequenceStringINSTANCE.Write(writer, variant_value.Topics)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Head)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.RetainedAfter)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.AcceptedThrough)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRecoveryCutoffStatus.Write", value))
	}
}

type FfiDestroyerRecoveryCutoffStatus struct{}

func (_ FfiDestroyerRecoveryCutoffStatus) Destroy(value RecoveryCutoffStatus) {
	value.Destroy()
}

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
	Field0 RecoveryRangeReady
}

func (e RecoveryRangeStatusReady) Destroy() {
	FfiDestroyerRecoveryRangeReady{}.Destroy(e.Field0)
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
		FfiConverterRecoveryRangeReadyINSTANCE.Write(writer, variant_value.Field0)
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

type RecoveryStage interface {
	Destroy()
}
type RecoveryStageCandidate struct {
	Field0 *RecoveryCandidate
}

func (e RecoveryStageCandidate) Destroy() {
	FfiDestroyerRecoveryCandidate{}.Destroy(e.Field0)
}

type RecoveryStageAlreadyCovered struct {
}

func (e RecoveryStageAlreadyCovered) Destroy() {
}

type RecoveryStageNoNewObjects struct {
}

func (e RecoveryStageNoNewObjects) Destroy() {
}

// Automatic recovery: nothing fits the pending bounds until the
// application acknowledges or rejects pending objects. No progress was
// claimed; request the range again after draining.
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
		FfiConverterRecoveryCandidateINSTANCE.Write(writer, variant_value.Field0)
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

// TLS trust for operator relays.
type RelayTrust interface {
	Destroy()
}

// The built-in WebPKI roots.
type RelayTrustWebPki struct {
}

func (e RelayTrustWebPki) Destroy() {
}

// Only these DER-encoded root certificates, for a private CA.
type RelayTrustCustomRoots struct {
	Field0 [][]byte
}

func (e RelayTrustCustomRoots) Destroy() {
	FfiDestroyerSequenceBytes{}.Destroy(e.Field0)
}

type FfiConverterRelayTrust struct{}

var FfiConverterRelayTrustINSTANCE = FfiConverterRelayTrust{}

func (c FfiConverterRelayTrust) Lift(rb RustBufferI) RelayTrust {
	return LiftFromRustBuffer[RelayTrust](c, rb)
}

func (c FfiConverterRelayTrust) Lower(value RelayTrust) C.RustBuffer {
	return LowerIntoRustBuffer[RelayTrust](c, value)
}

func (c FfiConverterRelayTrust) LowerExternal(value RelayTrust) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RelayTrust](c, value))
}
func (FfiConverterRelayTrust) Read(reader io.Reader) RelayTrust {
	id := readInt32(reader)
	switch id {
	case 1:
		return RelayTrustWebPki{}
	case 2:
		return RelayTrustCustomRoots{
			FfiConverterSequenceBytesINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRelayTrust.Read()", id))
	}
}

func (FfiConverterRelayTrust) Write(writer io.Writer, value RelayTrust) {
	switch variant_value := value.(type) {
	case RelayTrustWebPki:
		writeInt32(writer, 1)
	case RelayTrustCustomRoots:
		writeInt32(writer, 2)
		FfiConverterSequenceBytesINSTANCE.Write(writer, variant_value.Field0)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRelayTrust.Write", value))
	}
}

type FfiDestroyerRelayTrust struct{}

func (_ FfiDestroyerRelayTrust) Destroy(value RelayTrust) {
	value.Destroy()
}

type ResourceRequest interface {
	Destroy()
}

// Authorize one accepted member to fetch an immutable host-selected file.
// `root` and `path` must be absolute; the source can be outside the cache root.
type ResourceRequestPrepare struct {
	Member arachne_api.MemberId
	Root   string
	Path   string
}

func (e ResourceRequestPrepare) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Member)
	FfiDestroyerString{}.Destroy(e.Root)
	FfiDestroyerString{}.Destroy(e.Path)
}

type ResourceRequestFetch struct {
	Member arachne_api.MemberId
	Root   string
	Path   string
	Ticket ResourceTicket
}

func (e ResourceRequestFetch) Destroy() {
	arachne_api.FfiDestroyerTypeMemberId{}.Destroy(e.Member)
	FfiDestroyerString{}.Destroy(e.Root)
	FfiDestroyerString{}.Destroy(e.Path)
	FfiDestroyerResourceTicket{}.Destroy(e.Ticket)
}

type ResourceRequestPoll struct {
	Id uint64
}

func (e ResourceRequestPoll) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Id)
}

type ResourceRequestCancel struct {
	Id uint64
}

func (e ResourceRequestCancel) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Id)
}

type ResourceRequestRevoke struct {
	Path *string
}

func (e ResourceRequestRevoke) Destroy() {
	FfiDestroyerOptionalString{}.Destroy(e.Path)
}

type ResourceRequestClear struct {
	Root string
}

func (e ResourceRequestClear) Destroy() {
	FfiDestroyerString{}.Destroy(e.Root)
}

type FfiConverterResourceRequest struct{}

var FfiConverterResourceRequestINSTANCE = FfiConverterResourceRequest{}

func (c FfiConverterResourceRequest) Lift(rb RustBufferI) ResourceRequest {
	return LiftFromRustBuffer[ResourceRequest](c, rb)
}

func (c FfiConverterResourceRequest) Lower(value ResourceRequest) C.RustBuffer {
	return LowerIntoRustBuffer[ResourceRequest](c, value)
}

func (c FfiConverterResourceRequest) LowerExternal(value ResourceRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ResourceRequest](c, value))
}
func (FfiConverterResourceRequest) Read(reader io.Reader) ResourceRequest {
	id := readInt32(reader)
	switch id {
	case 1:
		return ResourceRequestPrepare{
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 2:
		return ResourceRequestFetch{
			arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterResourceTicketINSTANCE.Read(reader),
		}
	case 3:
		return ResourceRequestPoll{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 4:
		return ResourceRequestCancel{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 5:
		return ResourceRequestRevoke{
			FfiConverterOptionalStringINSTANCE.Read(reader),
		}
	case 6:
		return ResourceRequestClear{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterResourceRequest.Read()", id))
	}
}

func (FfiConverterResourceRequest) Write(writer io.Writer, value ResourceRequest) {
	switch variant_value := value.(type) {
	case ResourceRequestPrepare:
		writeInt32(writer, 1)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Member)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Root)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Path)
	case ResourceRequestFetch:
		writeInt32(writer, 2)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, variant_value.Member)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Root)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Path)
		FfiConverterResourceTicketINSTANCE.Write(writer, variant_value.Ticket)
	case ResourceRequestPoll:
		writeInt32(writer, 3)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Id)
	case ResourceRequestCancel:
		writeInt32(writer, 4)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Id)
	case ResourceRequestRevoke:
		writeInt32(writer, 5)
		FfiConverterOptionalStringINSTANCE.Write(writer, variant_value.Path)
	case ResourceRequestClear:
		writeInt32(writer, 6)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Root)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterResourceRequest.Write", value))
	}
}

type FfiDestroyerResourceRequest struct{}

func (_ FfiDestroyerResourceRequest) Destroy(value ResourceRequest) {
	value.Destroy()
}

type ResourceStatus interface {
	Destroy()
}
type ResourceStatusStarted struct {
	Id uint64
}

func (e ResourceStatusStarted) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Id)
}

type ResourceStatusRunning struct {
	Bytes uint64
}

func (e ResourceStatusRunning) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Bytes)
}

type ResourceStatusPrepared struct {
	Ticket ResourceTicket
}

func (e ResourceStatusPrepared) Destroy() {
	FfiDestroyerResourceTicket{}.Destroy(e.Ticket)
}

type ResourceStatusComplete struct {
	Bytes uint64
}

func (e ResourceStatusComplete) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Bytes)
}

type ResourceStatusCancelled struct {
}

func (e ResourceStatusCancelled) Destroy() {
}

type ResourceStatusRevoked struct {
}

func (e ResourceStatusRevoked) Destroy() {
}

type ResourceStatusCleared struct {
}

func (e ResourceStatusCleared) Destroy() {
}

type FfiConverterResourceStatus struct{}

var FfiConverterResourceStatusINSTANCE = FfiConverterResourceStatus{}

func (c FfiConverterResourceStatus) Lift(rb RustBufferI) ResourceStatus {
	return LiftFromRustBuffer[ResourceStatus](c, rb)
}

func (c FfiConverterResourceStatus) Lower(value ResourceStatus) C.RustBuffer {
	return LowerIntoRustBuffer[ResourceStatus](c, value)
}

func (c FfiConverterResourceStatus) LowerExternal(value ResourceStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ResourceStatus](c, value))
}
func (FfiConverterResourceStatus) Read(reader io.Reader) ResourceStatus {
	id := readInt32(reader)
	switch id {
	case 1:
		return ResourceStatusStarted{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 2:
		return ResourceStatusRunning{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 3:
		return ResourceStatusPrepared{
			FfiConverterResourceTicketINSTANCE.Read(reader),
		}
	case 4:
		return ResourceStatusComplete{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	case 5:
		return ResourceStatusCancelled{}
	case 6:
		return ResourceStatusRevoked{}
	case 7:
		return ResourceStatusCleared{}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterResourceStatus.Read()", id))
	}
}

func (FfiConverterResourceStatus) Write(writer io.Writer, value ResourceStatus) {
	switch variant_value := value.(type) {
	case ResourceStatusStarted:
		writeInt32(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Id)
	case ResourceStatusRunning:
		writeInt32(writer, 2)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Bytes)
	case ResourceStatusPrepared:
		writeInt32(writer, 3)
		FfiConverterResourceTicketINSTANCE.Write(writer, variant_value.Ticket)
	case ResourceStatusComplete:
		writeInt32(writer, 4)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Bytes)
	case ResourceStatusCancelled:
		writeInt32(writer, 5)
	case ResourceStatusRevoked:
		writeInt32(writer, 6)
	case ResourceStatusCleared:
		writeInt32(writer, 7)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterResourceStatus.Write", value))
	}
}

type FfiDestroyerResourceStatus struct{}

func (_ FfiDestroyerResourceStatus) Destroy(value ResourceStatus) {
	value.Destroy()
}

// What `restore_workspace` found in record storage.
type RestoredWorkspace interface {
	Destroy()
}
type RestoredWorkspaceActive struct {
	Field0 WorkspaceInfo
}

func (e RestoredWorkspaceActive) Destroy() {
	FfiDestroyerWorkspaceInfo{}.Destroy(e.Field0)
}

type RestoredWorkspaceJoining struct {
	Field0 RestoredJoin
}

func (e RestoredWorkspaceJoining) Destroy() {
	FfiDestroyerRestoredJoin{}.Destroy(e.Field0)
}

// This member was removed. The session has ended.
type RestoredWorkspaceRemoved struct {
	Field0 RemovedMembership
}

func (e RestoredWorkspaceRemoved) Destroy() {
	FfiDestroyerRemovedMembership{}.Destroy(e.Field0)
}

type FfiConverterRestoredWorkspace struct{}

var FfiConverterRestoredWorkspaceINSTANCE = FfiConverterRestoredWorkspace{}

func (c FfiConverterRestoredWorkspace) Lift(rb RustBufferI) RestoredWorkspace {
	return LiftFromRustBuffer[RestoredWorkspace](c, rb)
}

func (c FfiConverterRestoredWorkspace) Lower(value RestoredWorkspace) C.RustBuffer {
	return LowerIntoRustBuffer[RestoredWorkspace](c, value)
}

func (c FfiConverterRestoredWorkspace) LowerExternal(value RestoredWorkspace) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RestoredWorkspace](c, value))
}
func (FfiConverterRestoredWorkspace) Read(reader io.Reader) RestoredWorkspace {
	id := readInt32(reader)
	switch id {
	case 1:
		return RestoredWorkspaceActive{
			FfiConverterWorkspaceInfoINSTANCE.Read(reader),
		}
	case 2:
		return RestoredWorkspaceJoining{
			FfiConverterRestoredJoinINSTANCE.Read(reader),
		}
	case 3:
		return RestoredWorkspaceRemoved{
			FfiConverterRemovedMembershipINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterRestoredWorkspace.Read()", id))
	}
}

func (FfiConverterRestoredWorkspace) Write(writer io.Writer, value RestoredWorkspace) {
	switch variant_value := value.(type) {
	case RestoredWorkspaceActive:
		writeInt32(writer, 1)
		FfiConverterWorkspaceInfoINSTANCE.Write(writer, variant_value.Field0)
	case RestoredWorkspaceJoining:
		writeInt32(writer, 2)
		FfiConverterRestoredJoinINSTANCE.Write(writer, variant_value.Field0)
	case RestoredWorkspaceRemoved:
		writeInt32(writer, 3)
		FfiConverterRemovedMembershipINSTANCE.Write(writer, variant_value.Field0)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterRestoredWorkspace.Write", value))
	}
}

type FfiDestroyerRestoredWorkspace struct{}

func (_ FfiDestroyerRestoredWorkspace) Destroy(value RestoredWorkspace) {
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

type WorkspaceProgressState interface {
	Destroy()
}
type WorkspaceProgressStateIdle struct {
}

func (e WorkspaceProgressStateIdle) Destroy() {
}

type WorkspaceProgressStateWorkspaceCommitted struct {
}

func (e WorkspaceProgressStateWorkspaceCommitted) Destroy() {
}

type WorkspaceProgressStateWorkspaceReplyReady struct {
}

func (e WorkspaceProgressStateWorkspaceReplyReady) Destroy() {
}

type WorkspaceProgressStateWorkspaceNameCommitted struct {
}

func (e WorkspaceProgressStateWorkspaceNameCommitted) Destroy() {
}

type WorkspaceProgressStateSelfUpdateCommitted struct {
}

func (e WorkspaceProgressStateSelfUpdateCommitted) Destroy() {
}

type WorkspaceProgressStateMembershipReplied struct {
}

func (e WorkspaceProgressStateMembershipReplied) Destroy() {
}

type WorkspaceProgressStateAdmissionQueued struct {
}

func (e WorkspaceProgressStateAdmissionQueued) Destroy() {
}

type WorkspaceProgressStateAdmissionReplied struct {
}

func (e WorkspaceProgressStateAdmissionReplied) Destroy() {
}

type WorkspaceProgressStateApprovalRequested struct {
}

func (e WorkspaceProgressStateApprovalRequested) Destroy() {
}

type WorkspaceProgressStateNearbyInvitationReceived struct {
}

func (e WorkspaceProgressStateNearbyInvitationReceived) Destroy() {
}

type WorkspaceProgressStateNearbyInvitationRejected struct {
}

func (e WorkspaceProgressStateNearbyInvitationRejected) Destroy() {
}

type WorkspaceProgressStatePresenceReplied struct {
}

func (e WorkspaceProgressStatePresenceReplied) Destroy() {
}

type WorkspaceProgressStateControlServed struct {
}

func (e WorkspaceProgressStateControlServed) Destroy() {
}

// A new local advisory event. Read the typed activity and roster for authority.
type WorkspaceProgressStateOther struct {
	Name string
}

func (e WorkspaceProgressStateOther) Destroy() {
	FfiDestroyerString{}.Destroy(e.Name)
}

type FfiConverterWorkspaceProgressState struct{}

var FfiConverterWorkspaceProgressStateINSTANCE = FfiConverterWorkspaceProgressState{}

func (c FfiConverterWorkspaceProgressState) Lift(rb RustBufferI) WorkspaceProgressState {
	return LiftFromRustBuffer[WorkspaceProgressState](c, rb)
}

func (c FfiConverterWorkspaceProgressState) Lower(value WorkspaceProgressState) C.RustBuffer {
	return LowerIntoRustBuffer[WorkspaceProgressState](c, value)
}

func (c FfiConverterWorkspaceProgressState) LowerExternal(value WorkspaceProgressState) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[WorkspaceProgressState](c, value))
}
func (FfiConverterWorkspaceProgressState) Read(reader io.Reader) WorkspaceProgressState {
	id := readInt32(reader)
	switch id {
	case 1:
		return WorkspaceProgressStateIdle{}
	case 2:
		return WorkspaceProgressStateWorkspaceCommitted{}
	case 3:
		return WorkspaceProgressStateWorkspaceReplyReady{}
	case 4:
		return WorkspaceProgressStateWorkspaceNameCommitted{}
	case 5:
		return WorkspaceProgressStateSelfUpdateCommitted{}
	case 6:
		return WorkspaceProgressStateMembershipReplied{}
	case 7:
		return WorkspaceProgressStateAdmissionQueued{}
	case 8:
		return WorkspaceProgressStateAdmissionReplied{}
	case 9:
		return WorkspaceProgressStateApprovalRequested{}
	case 10:
		return WorkspaceProgressStateNearbyInvitationReceived{}
	case 11:
		return WorkspaceProgressStateNearbyInvitationRejected{}
	case 12:
		return WorkspaceProgressStatePresenceReplied{}
	case 13:
		return WorkspaceProgressStateControlServed{}
	case 14:
		return WorkspaceProgressStateOther{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterWorkspaceProgressState.Read()", id))
	}
}

func (FfiConverterWorkspaceProgressState) Write(writer io.Writer, value WorkspaceProgressState) {
	switch variant_value := value.(type) {
	case WorkspaceProgressStateIdle:
		writeInt32(writer, 1)
	case WorkspaceProgressStateWorkspaceCommitted:
		writeInt32(writer, 2)
	case WorkspaceProgressStateWorkspaceReplyReady:
		writeInt32(writer, 3)
	case WorkspaceProgressStateWorkspaceNameCommitted:
		writeInt32(writer, 4)
	case WorkspaceProgressStateSelfUpdateCommitted:
		writeInt32(writer, 5)
	case WorkspaceProgressStateMembershipReplied:
		writeInt32(writer, 6)
	case WorkspaceProgressStateAdmissionQueued:
		writeInt32(writer, 7)
	case WorkspaceProgressStateAdmissionReplied:
		writeInt32(writer, 8)
	case WorkspaceProgressStateApprovalRequested:
		writeInt32(writer, 9)
	case WorkspaceProgressStateNearbyInvitationReceived:
		writeInt32(writer, 10)
	case WorkspaceProgressStateNearbyInvitationRejected:
		writeInt32(writer, 11)
	case WorkspaceProgressStatePresenceReplied:
		writeInt32(writer, 12)
	case WorkspaceProgressStateControlServed:
		writeInt32(writer, 13)
	case WorkspaceProgressStateOther:
		writeInt32(writer, 14)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Name)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterWorkspaceProgressState.Write", value))
	}
}

type FfiDestroyerWorkspaceProgressState struct{}

func (_ FfiDestroyerWorkspaceProgressState) Destroy(value WorkspaceProgressState) {
	value.Destroy()
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

type FfiConverterOptionalBool struct{}

var FfiConverterOptionalBoolINSTANCE = FfiConverterOptionalBool{}

func (c FfiConverterOptionalBool) Lift(rb RustBufferI) *bool {
	return LiftFromRustBuffer[*bool](c, rb)
}

func (_ FfiConverterOptionalBool) Read(reader io.Reader) *bool {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterBoolINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalBool) Lower(value *bool) C.RustBuffer {
	return LowerIntoRustBuffer[*bool](c, value)
}

func (c FfiConverterOptionalBool) LowerExternal(value *bool) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*bool](c, value))
}

func (_ FfiConverterOptionalBool) Write(writer io.Writer, value *bool) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterBoolINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalBool struct{}

func (_ FfiDestroyerOptionalBool) Destroy(value *bool) {
	if value != nil {
		FfiDestroyerBool{}.Destroy(*value)
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

type FfiConverterOptionalDuration struct{}

var FfiConverterOptionalDurationINSTANCE = FfiConverterOptionalDuration{}

func (c FfiConverterOptionalDuration) Lift(rb RustBufferI) *time.Duration {
	return LiftFromRustBuffer[*time.Duration](c, rb)
}

func (_ FfiConverterOptionalDuration) Read(reader io.Reader) *time.Duration {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterDurationINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalDuration) Lower(value *time.Duration) C.RustBuffer {
	return LowerIntoRustBuffer[*time.Duration](c, value)
}

func (c FfiConverterOptionalDuration) LowerExternal(value *time.Duration) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*time.Duration](c, value))
}

func (_ FfiConverterOptionalDuration) Write(writer io.Writer, value *time.Duration) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterDurationINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalDuration struct{}

func (_ FfiDestroyerOptionalDuration) Destroy(value *time.Duration) {
	if value != nil {
		FfiDestroyerDuration{}.Destroy(*value)
	}
}

type FfiConverterOptionalMemberUpdate struct{}

var FfiConverterOptionalMemberUpdateINSTANCE = FfiConverterOptionalMemberUpdate{}

func (c FfiConverterOptionalMemberUpdate) Lift(rb RustBufferI) **MemberUpdate {
	return LiftFromRustBuffer[**MemberUpdate](c, rb)
}

func (_ FfiConverterOptionalMemberUpdate) Read(reader io.Reader) **MemberUpdate {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterMemberUpdateINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalMemberUpdate) Lower(value **MemberUpdate) C.RustBuffer {
	return LowerIntoRustBuffer[**MemberUpdate](c, value)
}

func (c FfiConverterOptionalMemberUpdate) LowerExternal(value **MemberUpdate) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**MemberUpdate](c, value))
}

func (_ FfiConverterOptionalMemberUpdate) Write(writer io.Writer, value **MemberUpdate) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterMemberUpdateINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalMemberUpdate struct{}

func (_ FfiDestroyerOptionalMemberUpdate) Destroy(value **MemberUpdate) {
	if value != nil {
		FfiDestroyerMemberUpdate{}.Destroy(*value)
	}
}

type FfiConverterOptionalProtectedReceptionCandidate struct{}

var FfiConverterOptionalProtectedReceptionCandidateINSTANCE = FfiConverterOptionalProtectedReceptionCandidate{}

func (c FfiConverterOptionalProtectedReceptionCandidate) Lift(rb RustBufferI) **ProtectedReceptionCandidate {
	return LiftFromRustBuffer[**ProtectedReceptionCandidate](c, rb)
}

func (_ FfiConverterOptionalProtectedReceptionCandidate) Read(reader io.Reader) **ProtectedReceptionCandidate {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterProtectedReceptionCandidateINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalProtectedReceptionCandidate) Lower(value **ProtectedReceptionCandidate) C.RustBuffer {
	return LowerIntoRustBuffer[**ProtectedReceptionCandidate](c, value)
}

func (c FfiConverterOptionalProtectedReceptionCandidate) LowerExternal(value **ProtectedReceptionCandidate) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**ProtectedReceptionCandidate](c, value))
}

func (_ FfiConverterOptionalProtectedReceptionCandidate) Write(writer io.Writer, value **ProtectedReceptionCandidate) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterProtectedReceptionCandidateINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalProtectedReceptionCandidate struct{}

func (_ FfiDestroyerOptionalProtectedReceptionCandidate) Destroy(value **ProtectedReceptionCandidate) {
	if value != nil {
		FfiDestroyerProtectedReceptionCandidate{}.Destroy(*value)
	}
}

type FfiConverterOptionalStorageConfig struct{}

var FfiConverterOptionalStorageConfigINSTANCE = FfiConverterOptionalStorageConfig{}

func (c FfiConverterOptionalStorageConfig) Lift(rb RustBufferI) **StorageConfig {
	return LiftFromRustBuffer[**StorageConfig](c, rb)
}

func (_ FfiConverterOptionalStorageConfig) Read(reader io.Reader) **StorageConfig {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterStorageConfigINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalStorageConfig) Lower(value **StorageConfig) C.RustBuffer {
	return LowerIntoRustBuffer[**StorageConfig](c, value)
}

func (c FfiConverterOptionalStorageConfig) LowerExternal(value **StorageConfig) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**StorageConfig](c, value))
}

func (_ FfiConverterOptionalStorageConfig) Write(writer io.Writer, value **StorageConfig) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterStorageConfigINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalStorageConfig struct{}

func (_ FfiDestroyerOptionalStorageConfig) Destroy(value **StorageConfig) {
	if value != nil {
		FfiDestroyerStorageConfig{}.Destroy(*value)
	}
}

type FfiConverterOptionalAdmissionNotice struct{}

var FfiConverterOptionalAdmissionNoticeINSTANCE = FfiConverterOptionalAdmissionNotice{}

func (c FfiConverterOptionalAdmissionNotice) Lift(rb RustBufferI) *AdmissionNotice {
	return LiftFromRustBuffer[*AdmissionNotice](c, rb)
}

func (_ FfiConverterOptionalAdmissionNotice) Read(reader io.Reader) *AdmissionNotice {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterAdmissionNoticeINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalAdmissionNotice) Lower(value *AdmissionNotice) C.RustBuffer {
	return LowerIntoRustBuffer[*AdmissionNotice](c, value)
}

func (c FfiConverterOptionalAdmissionNotice) LowerExternal(value *AdmissionNotice) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*AdmissionNotice](c, value))
}

func (_ FfiConverterOptionalAdmissionNotice) Write(writer io.Writer, value *AdmissionNotice) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterAdmissionNoticeINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalAdmissionNotice struct{}

func (_ FfiDestroyerOptionalAdmissionNotice) Destroy(value *AdmissionNotice) {
	if value != nil {
		FfiDestroyerAdmissionNotice{}.Destroy(*value)
	}
}

type FfiConverterOptionalDirectRecoveryRequest struct{}

var FfiConverterOptionalDirectRecoveryRequestINSTANCE = FfiConverterOptionalDirectRecoveryRequest{}

func (c FfiConverterOptionalDirectRecoveryRequest) Lift(rb RustBufferI) *DirectRecoveryRequest {
	return LiftFromRustBuffer[*DirectRecoveryRequest](c, rb)
}

func (_ FfiConverterOptionalDirectRecoveryRequest) Read(reader io.Reader) *DirectRecoveryRequest {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterDirectRecoveryRequestINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalDirectRecoveryRequest) Lower(value *DirectRecoveryRequest) C.RustBuffer {
	return LowerIntoRustBuffer[*DirectRecoveryRequest](c, value)
}

func (c FfiConverterOptionalDirectRecoveryRequest) LowerExternal(value *DirectRecoveryRequest) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*DirectRecoveryRequest](c, value))
}

func (_ FfiConverterOptionalDirectRecoveryRequest) Write(writer io.Writer, value *DirectRecoveryRequest) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterDirectRecoveryRequestINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalDirectRecoveryRequest struct{}

func (_ FfiDestroyerOptionalDirectRecoveryRequest) Destroy(value *DirectRecoveryRequest) {
	if value != nil {
		FfiDestroyerDirectRecoveryRequest{}.Destroy(*value)
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

type FfiConverterOptionalOperatorRelay struct{}

var FfiConverterOptionalOperatorRelayINSTANCE = FfiConverterOptionalOperatorRelay{}

func (c FfiConverterOptionalOperatorRelay) Lift(rb RustBufferI) *OperatorRelay {
	return LiftFromRustBuffer[*OperatorRelay](c, rb)
}

func (_ FfiConverterOptionalOperatorRelay) Read(reader io.Reader) *OperatorRelay {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterOperatorRelayINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalOperatorRelay) Lower(value *OperatorRelay) C.RustBuffer {
	return LowerIntoRustBuffer[*OperatorRelay](c, value)
}

func (c FfiConverterOptionalOperatorRelay) LowerExternal(value *OperatorRelay) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*OperatorRelay](c, value))
}

func (_ FfiConverterOptionalOperatorRelay) Write(writer io.Writer, value *OperatorRelay) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterOperatorRelayINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalOperatorRelay struct{}

func (_ FfiDestroyerOptionalOperatorRelay) Destroy(value *OperatorRelay) {
	if value != nil {
		FfiDestroyerOperatorRelay{}.Destroy(*value)
	}
}

type FfiConverterOptionalPresenceRound struct{}

var FfiConverterOptionalPresenceRoundINSTANCE = FfiConverterOptionalPresenceRound{}

func (c FfiConverterOptionalPresenceRound) Lift(rb RustBufferI) *PresenceRound {
	return LiftFromRustBuffer[*PresenceRound](c, rb)
}

func (_ FfiConverterOptionalPresenceRound) Read(reader io.Reader) *PresenceRound {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterPresenceRoundINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalPresenceRound) Lower(value *PresenceRound) C.RustBuffer {
	return LowerIntoRustBuffer[*PresenceRound](c, value)
}

func (c FfiConverterOptionalPresenceRound) LowerExternal(value *PresenceRound) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*PresenceRound](c, value))
}

func (_ FfiConverterOptionalPresenceRound) Write(writer io.Writer, value *PresenceRound) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterPresenceRoundINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalPresenceRound struct{}

func (_ FfiDestroyerOptionalPresenceRound) Destroy(value *PresenceRound) {
	if value != nil {
		FfiDestroyerPresenceRound{}.Destroy(*value)
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

type FfiConverterOptionalReceivedProtectedPublication struct{}

var FfiConverterOptionalReceivedProtectedPublicationINSTANCE = FfiConverterOptionalReceivedProtectedPublication{}

func (c FfiConverterOptionalReceivedProtectedPublication) Lift(rb RustBufferI) *ReceivedProtectedPublication {
	return LiftFromRustBuffer[*ReceivedProtectedPublication](c, rb)
}

func (_ FfiConverterOptionalReceivedProtectedPublication) Read(reader io.Reader) *ReceivedProtectedPublication {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterReceivedProtectedPublicationINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalReceivedProtectedPublication) Lower(value *ReceivedProtectedPublication) C.RustBuffer {
	return LowerIntoRustBuffer[*ReceivedProtectedPublication](c, value)
}

func (c FfiConverterOptionalReceivedProtectedPublication) LowerExternal(value *ReceivedProtectedPublication) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*ReceivedProtectedPublication](c, value))
}

func (_ FfiConverterOptionalReceivedProtectedPublication) Write(writer io.Writer, value *ReceivedProtectedPublication) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterReceivedProtectedPublicationINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalReceivedProtectedPublication struct{}

func (_ FfiDestroyerOptionalReceivedProtectedPublication) Destroy(value *ReceivedProtectedPublication) {
	if value != nil {
		FfiDestroyerReceivedProtectedPublication{}.Destroy(*value)
	}
}

type FfiConverterOptionalTransportTimeouts struct{}

var FfiConverterOptionalTransportTimeoutsINSTANCE = FfiConverterOptionalTransportTimeouts{}

func (c FfiConverterOptionalTransportTimeouts) Lift(rb RustBufferI) *TransportTimeouts {
	return LiftFromRustBuffer[*TransportTimeouts](c, rb)
}

func (_ FfiConverterOptionalTransportTimeouts) Read(reader io.Reader) *TransportTimeouts {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTransportTimeoutsINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTransportTimeouts) Lower(value *TransportTimeouts) C.RustBuffer {
	return LowerIntoRustBuffer[*TransportTimeouts](c, value)
}

func (c FfiConverterOptionalTransportTimeouts) LowerExternal(value *TransportTimeouts) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*TransportTimeouts](c, value))
}

func (_ FfiConverterOptionalTransportTimeouts) Write(writer io.Writer, value *TransportTimeouts) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTransportTimeoutsINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTransportTimeouts struct{}

func (_ FfiDestroyerOptionalTransportTimeouts) Destroy(value *TransportTimeouts) {
	if value != nil {
		FfiDestroyerTransportTimeouts{}.Destroy(*value)
	}
}

type FfiConverterOptionalEvent struct{}

var FfiConverterOptionalEventINSTANCE = FfiConverterOptionalEvent{}

func (c FfiConverterOptionalEvent) Lift(rb RustBufferI) *arachne_api.Event {
	return LiftFromRustBuffer[*arachne_api.Event](c, rb)
}

func (_ FfiConverterOptionalEvent) Read(reader io.Reader) *arachne_api.Event {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterEventINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalEvent) Lower(value *arachne_api.Event) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.Event](c, value)
}

func (c FfiConverterOptionalEvent) LowerExternal(value *arachne_api.Event) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.Event](c, value))
}

func (_ FfiConverterOptionalEvent) Write(writer io.Writer, value *arachne_api.Event) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterEventINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalEvent struct{}

func (_ FfiDestroyerOptionalEvent) Destroy(value *arachne_api.Event) {
	if value != nil {
		arachne_api.FfiDestroyerEvent{}.Destroy(*value)
	}
}

type FfiConverterOptionalCurrentViewStatus struct{}

var FfiConverterOptionalCurrentViewStatusINSTANCE = FfiConverterOptionalCurrentViewStatus{}

func (c FfiConverterOptionalCurrentViewStatus) Lift(rb RustBufferI) *CurrentViewStatus {
	return LiftFromRustBuffer[*CurrentViewStatus](c, rb)
}

func (_ FfiConverterOptionalCurrentViewStatus) Read(reader io.Reader) *CurrentViewStatus {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterCurrentViewStatusINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalCurrentViewStatus) Lower(value *CurrentViewStatus) C.RustBuffer {
	return LowerIntoRustBuffer[*CurrentViewStatus](c, value)
}

func (c FfiConverterOptionalCurrentViewStatus) LowerExternal(value *CurrentViewStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*CurrentViewStatus](c, value))
}

func (_ FfiConverterOptionalCurrentViewStatus) Write(writer io.Writer, value *CurrentViewStatus) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterCurrentViewStatusINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalCurrentViewStatus struct{}

func (_ FfiDestroyerOptionalCurrentViewStatus) Destroy(value *CurrentViewStatus) {
	if value != nil {
		FfiDestroyerCurrentViewStatus{}.Destroy(*value)
	}
}

type FfiConverterOptionalDirectRecoveryStatus struct{}

var FfiConverterOptionalDirectRecoveryStatusINSTANCE = FfiConverterOptionalDirectRecoveryStatus{}

func (c FfiConverterOptionalDirectRecoveryStatus) Lift(rb RustBufferI) *DirectRecoveryStatus {
	return LiftFromRustBuffer[*DirectRecoveryStatus](c, rb)
}

func (_ FfiConverterOptionalDirectRecoveryStatus) Read(reader io.Reader) *DirectRecoveryStatus {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterDirectRecoveryStatusINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalDirectRecoveryStatus) Lower(value *DirectRecoveryStatus) C.RustBuffer {
	return LowerIntoRustBuffer[*DirectRecoveryStatus](c, value)
}

func (c FfiConverterOptionalDirectRecoveryStatus) LowerExternal(value *DirectRecoveryStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*DirectRecoveryStatus](c, value))
}

func (_ FfiConverterOptionalDirectRecoveryStatus) Write(writer io.Writer, value *DirectRecoveryStatus) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterDirectRecoveryStatusINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalDirectRecoveryStatus struct{}

func (_ FfiDestroyerOptionalDirectRecoveryStatus) Destroy(value *DirectRecoveryStatus) {
	if value != nil {
		FfiDestroyerDirectRecoveryStatus{}.Destroy(*value)
	}
}

type FfiConverterOptionalRecoveryCutoffStatus struct{}

var FfiConverterOptionalRecoveryCutoffStatusINSTANCE = FfiConverterOptionalRecoveryCutoffStatus{}

func (c FfiConverterOptionalRecoveryCutoffStatus) Lift(rb RustBufferI) *RecoveryCutoffStatus {
	return LiftFromRustBuffer[*RecoveryCutoffStatus](c, rb)
}

func (_ FfiConverterOptionalRecoveryCutoffStatus) Read(reader io.Reader) *RecoveryCutoffStatus {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterRecoveryCutoffStatusINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalRecoveryCutoffStatus) Lower(value *RecoveryCutoffStatus) C.RustBuffer {
	return LowerIntoRustBuffer[*RecoveryCutoffStatus](c, value)
}

func (c FfiConverterOptionalRecoveryCutoffStatus) LowerExternal(value *RecoveryCutoffStatus) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*RecoveryCutoffStatus](c, value))
}

func (_ FfiConverterOptionalRecoveryCutoffStatus) Write(writer io.Writer, value *RecoveryCutoffStatus) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterRecoveryCutoffStatusINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalRecoveryCutoffStatus struct{}

func (_ FfiDestroyerOptionalRecoveryCutoffStatus) Destroy(value *RecoveryCutoffStatus) {
	if value != nil {
		FfiDestroyerRecoveryCutoffStatus{}.Destroy(*value)
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

type FfiConverterOptionalAttemptId struct{}

var FfiConverterOptionalAttemptIdINSTANCE = FfiConverterOptionalAttemptId{}

func (c FfiConverterOptionalAttemptId) Lift(rb RustBufferI) *arachne_api.AttemptId {
	return LiftFromRustBuffer[*arachne_api.AttemptId](c, rb)
}

func (_ FfiConverterOptionalAttemptId) Read(reader io.Reader) *arachne_api.AttemptId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterTypeAttemptIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalAttemptId) Lower(value *arachne_api.AttemptId) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.AttemptId](c, value)
}

func (c FfiConverterOptionalAttemptId) LowerExternal(value *arachne_api.AttemptId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.AttemptId](c, value))
}

func (_ FfiConverterOptionalAttemptId) Write(writer io.Writer, value *arachne_api.AttemptId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterTypeAttemptIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalAttemptId struct{}

func (_ FfiDestroyerOptionalAttemptId) Destroy(value *arachne_api.AttemptId) {
	if value != nil {
		arachne_api.FfiDestroyerTypeAttemptId{}.Destroy(*value)
	}
}

type FfiConverterOptionalEndpointId struct{}

var FfiConverterOptionalEndpointIdINSTANCE = FfiConverterOptionalEndpointId{}

func (c FfiConverterOptionalEndpointId) Lift(rb RustBufferI) *arachne_api.EndpointId {
	return LiftFromRustBuffer[*arachne_api.EndpointId](c, rb)
}

func (_ FfiConverterOptionalEndpointId) Read(reader io.Reader) *arachne_api.EndpointId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalEndpointId) Lower(value *arachne_api.EndpointId) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.EndpointId](c, value)
}

func (c FfiConverterOptionalEndpointId) LowerExternal(value *arachne_api.EndpointId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.EndpointId](c, value))
}

func (_ FfiConverterOptionalEndpointId) Write(writer io.Writer, value *arachne_api.EndpointId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalEndpointId struct{}

func (_ FfiDestroyerOptionalEndpointId) Destroy(value *arachne_api.EndpointId) {
	if value != nil {
		arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(*value)
	}
}

type FfiConverterOptionalKey32 struct{}

var FfiConverterOptionalKey32INSTANCE = FfiConverterOptionalKey32{}

func (c FfiConverterOptionalKey32) Lift(rb RustBufferI) *arachne_api.Key32 {
	return LiftFromRustBuffer[*arachne_api.Key32](c, rb)
}

func (_ FfiConverterOptionalKey32) Read(reader io.Reader) *arachne_api.Key32 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterTypeKey32INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalKey32) Lower(value *arachne_api.Key32) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.Key32](c, value)
}

func (c FfiConverterOptionalKey32) LowerExternal(value *arachne_api.Key32) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.Key32](c, value))
}

func (_ FfiConverterOptionalKey32) Write(writer io.Writer, value *arachne_api.Key32) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterTypeKey32INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalKey32 struct{}

func (_ FfiDestroyerOptionalKey32) Destroy(value *arachne_api.Key32) {
	if value != nil {
		arachne_api.FfiDestroyerTypeKey32{}.Destroy(*value)
	}
}

type FfiConverterOptionalMemberId struct{}

var FfiConverterOptionalMemberIdINSTANCE = FfiConverterOptionalMemberId{}

func (c FfiConverterOptionalMemberId) Lift(rb RustBufferI) *arachne_api.MemberId {
	return LiftFromRustBuffer[*arachne_api.MemberId](c, rb)
}

func (_ FfiConverterOptionalMemberId) Read(reader io.Reader) *arachne_api.MemberId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalMemberId) Lower(value *arachne_api.MemberId) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.MemberId](c, value)
}

func (c FfiConverterOptionalMemberId) LowerExternal(value *arachne_api.MemberId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.MemberId](c, value))
}

func (_ FfiConverterOptionalMemberId) Write(writer io.Writer, value *arachne_api.MemberId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalMemberId struct{}

func (_ FfiDestroyerOptionalMemberId) Destroy(value *arachne_api.MemberId) {
	if value != nil {
		arachne_api.FfiDestroyerTypeMemberId{}.Destroy(*value)
	}
}

type FfiConverterOptionalWorkspaceId struct{}

var FfiConverterOptionalWorkspaceIdINSTANCE = FfiConverterOptionalWorkspaceId{}

func (c FfiConverterOptionalWorkspaceId) Lift(rb RustBufferI) *arachne_api.WorkspaceId {
	return LiftFromRustBuffer[*arachne_api.WorkspaceId](c, rb)
}

func (_ FfiConverterOptionalWorkspaceId) Read(reader io.Reader) *arachne_api.WorkspaceId {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalWorkspaceId) Lower(value *arachne_api.WorkspaceId) C.RustBuffer {
	return LowerIntoRustBuffer[*arachne_api.WorkspaceId](c, value)
}

func (c FfiConverterOptionalWorkspaceId) LowerExternal(value *arachne_api.WorkspaceId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*arachne_api.WorkspaceId](c, value))
}

func (_ FfiConverterOptionalWorkspaceId) Write(writer io.Writer, value *arachne_api.WorkspaceId) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		arachne_api.FfiConverterTypeWorkspaceIdINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalWorkspaceId struct{}

func (_ FfiDestroyerOptionalWorkspaceId) Destroy(value *arachne_api.WorkspaceId) {
	if value != nil {
		arachne_api.FfiDestroyerTypeWorkspaceId{}.Destroy(*value)
	}
}

type FfiConverterOptionalTypeFreshnessAnchor struct{}

var FfiConverterOptionalTypeFreshnessAnchorINSTANCE = FfiConverterOptionalTypeFreshnessAnchor{}

func (c FfiConverterOptionalTypeFreshnessAnchor) Lift(rb RustBufferI) *FreshnessAnchor {
	return LiftFromRustBuffer[*FreshnessAnchor](c, rb)
}

func (_ FfiConverterOptionalTypeFreshnessAnchor) Read(reader io.Reader) *FreshnessAnchor {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterTypeFreshnessAnchorINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalTypeFreshnessAnchor) Lower(value *FreshnessAnchor) C.RustBuffer {
	return LowerIntoRustBuffer[*FreshnessAnchor](c, value)
}

func (c FfiConverterOptionalTypeFreshnessAnchor) LowerExternal(value *FreshnessAnchor) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*FreshnessAnchor](c, value))
}

func (_ FfiConverterOptionalTypeFreshnessAnchor) Write(writer io.Writer, value *FreshnessAnchor) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterTypeFreshnessAnchorINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalTypeFreshnessAnchor struct{}

func (_ FfiDestroyerOptionalTypeFreshnessAnchor) Destroy(value *FreshnessAnchor) {
	if value != nil {
		FfiDestroyerTypeFreshnessAnchor{}.Destroy(*value)
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

type FfiConverterSequenceBytes struct{}

var FfiConverterSequenceBytesINSTANCE = FfiConverterSequenceBytes{}

func (c FfiConverterSequenceBytes) Lift(rb RustBufferI) [][]byte {
	return LiftFromRustBuffer[[][]byte](c, rb)
}

func (c FfiConverterSequenceBytes) Read(reader io.Reader) [][]byte {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([][]byte, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterBytesINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceBytes) Lower(value [][]byte) C.RustBuffer {
	return LowerIntoRustBuffer[[][]byte](c, value)
}

func (c FfiConverterSequenceBytes) LowerExternal(value [][]byte) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[][]byte](c, value))
}

func (c FfiConverterSequenceBytes) Write(writer io.Writer, value [][]byte) {
	if len(value) > math.MaxInt32 {
		panic("[][]byte is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterBytesINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceBytes struct{}

func (FfiDestroyerSequenceBytes) Destroy(sequence [][]byte) {
	for _, value := range sequence {
		FfiDestroyerBytes{}.Destroy(value)
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

type FfiConverterSequenceNearbyAdvertisement struct{}

var FfiConverterSequenceNearbyAdvertisementINSTANCE = FfiConverterSequenceNearbyAdvertisement{}

func (c FfiConverterSequenceNearbyAdvertisement) Lift(rb RustBufferI) []NearbyAdvertisement {
	return LiftFromRustBuffer[[]NearbyAdvertisement](c, rb)
}

func (c FfiConverterSequenceNearbyAdvertisement) Read(reader io.Reader) []NearbyAdvertisement {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]NearbyAdvertisement, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterNearbyAdvertisementINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceNearbyAdvertisement) Lower(value []NearbyAdvertisement) C.RustBuffer {
	return LowerIntoRustBuffer[[]NearbyAdvertisement](c, value)
}

func (c FfiConverterSequenceNearbyAdvertisement) LowerExternal(value []NearbyAdvertisement) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]NearbyAdvertisement](c, value))
}

func (c FfiConverterSequenceNearbyAdvertisement) Write(writer io.Writer, value []NearbyAdvertisement) {
	if len(value) > math.MaxInt32 {
		panic("[]NearbyAdvertisement is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterNearbyAdvertisementINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceNearbyAdvertisement struct{}

func (FfiDestroyerSequenceNearbyAdvertisement) Destroy(sequence []NearbyAdvertisement) {
	for _, value := range sequence {
		FfiDestroyerNearbyAdvertisement{}.Destroy(value)
	}
}

type FfiConverterSequenceNearbyEndpoint struct{}

var FfiConverterSequenceNearbyEndpointINSTANCE = FfiConverterSequenceNearbyEndpoint{}

func (c FfiConverterSequenceNearbyEndpoint) Lift(rb RustBufferI) []NearbyEndpoint {
	return LiftFromRustBuffer[[]NearbyEndpoint](c, rb)
}

func (c FfiConverterSequenceNearbyEndpoint) Read(reader io.Reader) []NearbyEndpoint {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]NearbyEndpoint, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterNearbyEndpointINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceNearbyEndpoint) Lower(value []NearbyEndpoint) C.RustBuffer {
	return LowerIntoRustBuffer[[]NearbyEndpoint](c, value)
}

func (c FfiConverterSequenceNearbyEndpoint) LowerExternal(value []NearbyEndpoint) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]NearbyEndpoint](c, value))
}

func (c FfiConverterSequenceNearbyEndpoint) Write(writer io.Writer, value []NearbyEndpoint) {
	if len(value) > math.MaxInt32 {
		panic("[]NearbyEndpoint is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterNearbyEndpointINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceNearbyEndpoint struct{}

func (FfiDestroyerSequenceNearbyEndpoint) Destroy(sequence []NearbyEndpoint) {
	for _, value := range sequence {
		FfiDestroyerNearbyEndpoint{}.Destroy(value)
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

type FfiConverterSequenceEndpointId struct{}

var FfiConverterSequenceEndpointIdINSTANCE = FfiConverterSequenceEndpointId{}

func (c FfiConverterSequenceEndpointId) Lift(rb RustBufferI) []arachne_api.EndpointId {
	return LiftFromRustBuffer[[]arachne_api.EndpointId](c, rb)
}

func (c FfiConverterSequenceEndpointId) Read(reader io.Reader) []arachne_api.EndpointId {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]arachne_api.EndpointId, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, arachne_api.FfiConverterTypeEndpointIdINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceEndpointId) Lower(value []arachne_api.EndpointId) C.RustBuffer {
	return LowerIntoRustBuffer[[]arachne_api.EndpointId](c, value)
}

func (c FfiConverterSequenceEndpointId) LowerExternal(value []arachne_api.EndpointId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]arachne_api.EndpointId](c, value))
}

func (c FfiConverterSequenceEndpointId) Write(writer io.Writer, value []arachne_api.EndpointId) {
	if len(value) > math.MaxInt32 {
		panic("[]arachne_api.EndpointId is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		arachne_api.FfiConverterTypeEndpointIdINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceEndpointId struct{}

func (FfiDestroyerSequenceEndpointId) Destroy(sequence []arachne_api.EndpointId) {
	for _, value := range sequence {
		arachne_api.FfiDestroyerTypeEndpointId{}.Destroy(value)
	}
}

type FfiConverterSequenceMemberId struct{}

var FfiConverterSequenceMemberIdINSTANCE = FfiConverterSequenceMemberId{}

func (c FfiConverterSequenceMemberId) Lift(rb RustBufferI) []arachne_api.MemberId {
	return LiftFromRustBuffer[[]arachne_api.MemberId](c, rb)
}

func (c FfiConverterSequenceMemberId) Read(reader io.Reader) []arachne_api.MemberId {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]arachne_api.MemberId, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, arachne_api.FfiConverterTypeMemberIdINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceMemberId) Lower(value []arachne_api.MemberId) C.RustBuffer {
	return LowerIntoRustBuffer[[]arachne_api.MemberId](c, value)
}

func (c FfiConverterSequenceMemberId) LowerExternal(value []arachne_api.MemberId) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]arachne_api.MemberId](c, value))
}

func (c FfiConverterSequenceMemberId) Write(writer io.Writer, value []arachne_api.MemberId) {
	if len(value) > math.MaxInt32 {
		panic("[]arachne_api.MemberId is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		arachne_api.FfiConverterTypeMemberIdINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceMemberId struct{}

func (FfiDestroyerSequenceMemberId) Destroy(sequence []arachne_api.MemberId) {
	for _, value := range sequence {
		arachne_api.FfiDestroyerTypeMemberId{}.Destroy(value)
	}
}

/**
 * Typealias from the type name used in the UDL file to the builtin type.  This
 * is needed because the UDL type name is used in function/method signatures.
 * It's also what we have an external type that references a custom type.
 */
type FreshnessAnchor = []byte
type FfiConverterTypeFreshnessAnchor = FfiConverterBytes
type FfiDestroyerTypeFreshnessAnchor = FfiDestroyerBytes

var FfiConverterTypeFreshnessAnchorINSTANCE = FfiConverterBytes{}

func LiftFromExternalTypeFreshnessAnchor(value ExternalCRustBuffer) FreshnessAnchor {
	return FfiConverterTypeFreshnessAnchorINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeFreshnessAnchor(value FreshnessAnchor) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeFreshnessAnchorINSTANCE.Lower(value))
}

// Start with the profile defaults; attach a persistent key and storage before
// opening a workspace client.
func DefaultClientConfig(network arachne_api.Network) ClientConfig {
	return FfiConverterClientConfigINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_func_default_client_config(
				CFromRustBuffer(arachne_api.FfiConverterNetworkINSTANCE.LowerExternal(network)), _uniffiStatus),
		}
	}))
}

// Native transport defaults for foreign bindings.
func DefaultTransportOptions() TransportOptions {
	return FfiConverterTransportOptionsINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_runtime_fn_func_default_transport_options(_uniffiStatus),
		}
	}))
}
