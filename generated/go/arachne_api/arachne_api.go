package arachne_api

// #include <arachne_api.h>
import "C"

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
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
		C.ffi_arachne_api_rustbuffer_free(cb.inner, status)
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
		return C.ffi_arachne_api_rustbuffer_from_bytes(foreign, status)
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
		return C.ffi_arachne_api_uniffi_contract_version()
	})
	if bindingsContractVersion != int(scaffoldingContractVersion) {
		// If this happens try cleaning and rebuilding your project
		panic("arachne_api: UniFFI contract version mismatch")
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_api_checksum_func_api_error_code()
		})
		if checksum != 5828 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_api: uniffi_arachne_api_checksum_func_api_error_code: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_api_checksum_func_api_version()
		})
		if checksum != 5163 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_api: uniffi_arachne_api_checksum_func_api_version: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_arachne_api_checksum_func_default_limits()
		})
		if checksum != 19389 {
			// If this happens try cleaning and rebuilding your project
			panic("arachne_api: uniffi_arachne_api_checksum_func_default_limits: UniFFI API checksum mismatch")
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

// What this build and client support (returned by `Client::capabilities`,
// ADR step 6).
//
// Limits belong to the client's runtime context.
type Capabilities struct {
	ApiVersion uint32
	Networks   []Network
	Features   []Feature
	Limits     Limits
}

func (r *Capabilities) Destroy() {
	FfiDestroyerUint32{}.Destroy(r.ApiVersion)
	FfiDestroyerSequenceNetwork{}.Destroy(r.Networks)
	FfiDestroyerSequenceFeature{}.Destroy(r.Features)
	FfiDestroyerLimits{}.Destroy(r.Limits)
}

type FfiConverterCapabilities struct{}

var FfiConverterCapabilitiesINSTANCE = FfiConverterCapabilities{}

func (c FfiConverterCapabilities) Lift(rb RustBufferI) Capabilities {
	return LiftFromRustBuffer[Capabilities](c, rb)
}

func (c FfiConverterCapabilities) Read(reader io.Reader) Capabilities {
	return Capabilities{
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterSequenceNetworkINSTANCE.Read(reader),
		FfiConverterSequenceFeatureINSTANCE.Read(reader),
		FfiConverterLimitsINSTANCE.Read(reader),
	}
}

func (c FfiConverterCapabilities) Lower(value Capabilities) C.RustBuffer {
	return LowerIntoRustBuffer[Capabilities](c, value)
}

func (c FfiConverterCapabilities) LowerExternal(value Capabilities) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Capabilities](c, value))
}

func (c FfiConverterCapabilities) Write(writer io.Writer, value Capabilities) {
	FfiConverterUint32INSTANCE.Write(writer, value.ApiVersion)
	FfiConverterSequenceNetworkINSTANCE.Write(writer, value.Networks)
	FfiConverterSequenceFeatureINSTANCE.Write(writer, value.Features)
	FfiConverterLimitsINSTANCE.Write(writer, value.Limits)
}

type FfiDestroyerCapabilities struct{}

func (_ FfiDestroyerCapabilities) Destroy(value Capabilities) {
	value.Destroy()
}

// Resource limits of one runtime `Context` (ADR step 3). Every session of
// the context counts toward them; two contexts never share them.
//
// The defaults suit a desktop host and parallel tests. A phone host sets
// lower values, for example `Limits::default().with_max_sessions(8)`.
type Limits struct {
	// Live sessions (endpoints). Sessions that are still binding count too.
	MaxSessions uint32
	// Gossip overlay paths over all sessions. One workspace uses at most 5.
	MaxOverlayPaths uint32
}

func (r *Limits) Destroy() {
	FfiDestroyerUint32{}.Destroy(r.MaxSessions)
	FfiDestroyerUint32{}.Destroy(r.MaxOverlayPaths)
}

type FfiConverterLimits struct{}

var FfiConverterLimitsINSTANCE = FfiConverterLimits{}

func (c FfiConverterLimits) Lift(rb RustBufferI) Limits {
	return LiftFromRustBuffer[Limits](c, rb)
}

func (c FfiConverterLimits) Read(reader io.Reader) Limits {
	return Limits{
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
	}
}

func (c FfiConverterLimits) Lower(value Limits) C.RustBuffer {
	return LowerIntoRustBuffer[Limits](c, value)
}

func (c FfiConverterLimits) LowerExternal(value Limits) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Limits](c, value))
}

func (c FfiConverterLimits) Write(writer io.Writer, value Limits) {
	FfiConverterUint32INSTANCE.Write(writer, value.MaxSessions)
	FfiConverterUint32INSTANCE.Write(writer, value.MaxOverlayPaths)
}

type FfiDestroyerLimits struct{}

func (_ FfiDestroyerLimits) Destroy(value Limits) {
	value.Destroy()
}

// The error of every public operation.
//
// Make it where the failure happens. Do not guess it from message text.
//
// # Pairing rule
//
// Variants that carry a `code` accept only codes from their own group:
//
// | Variant         | Codes                                              |
// | --------------- | -------------------------------------------------- |
// | `Storage`       | 300-399 (`StorageFailed`, `StorageCorrupt`, `CandidateStale`, `FormatNotSupported`) |
// | `Transport`     | 400-499                                            |
// | `Authorization` | 500-599                                            |
// | `State`         | `WrongState`, `Unsupported`, 600-699               |
//
// The fields of an enum variant are public in Rust, so the compiler cannot
// stop a wrong pair. Make errors with the checked constructors
// ([`ApiError::new`] and the named ones such as [`ApiError::timeout`]); they
// always pick the right variant. [`ApiError::is_well_formed`] checks a value,
// and deserialization rejects a wrong pair.
//
// # No secrets
//
// `detail`, `reason`, `resource` and `kind` are for people. They must never
// hold key material, invitation tokens, snapshots, plaintext payloads or
// other secret bytes. Put only fixed text, lengths, counts and public IDs in
// them.
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

// Not in the ADR sketch; added so `ErrorCode::InvalidId` has a variant.
type ApiErrorInvalidId struct {
	Kind   string
	Reason string
}

// Not in the ADR sketch; added so `ErrorCode::InvalidId` has a variant.
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

// A resource is full for now (a queue, a worker pool). A later retry can
// succeed. `limit` is 0 when the bound is not one fixed number.
type ApiErrorCapacityExceeded struct {
	Resource string
	Limit    uint64
	Detail   string
}

// A resource is full for now (a queue, a worker pool). A later retry can
// succeed. `limit` is 0 when the bound is not one fixed number.
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

// A fixed limit is reached (sessions, advertisements, overlay paths).
// It stays reached until something is released. `limit` is 0 when the
// bound is not one fixed number. Added in API version 2.
type ApiErrorLimitReached struct {
	Resource string
	Limit    uint64
	Detail   string
}

// A fixed limit is reached (sessions, advertisements, overlay paths).
// It stays reached until something is released. `limit` is 0 when the
// bound is not one fixed number. Added in API version 2.
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

// A stable numeric error code.
//
// Ranges:
//
// | Range   | Meaning                          |
// | ------- | -------------------------------- |
// | 1-99    | Lifecycle (closed, cancelled)    |
// | 100-199 | Input and state                  |
// | 200-299 | Capacity and limits              |
// | 300-399 | Storage and candidates           |
// | 400-499 | Transport and peers              |
// | 500-599 | Authorization and membership     |
// | 600-699 | Group consistency                |
// | 900-999 | Internal                         |
//
// Rules: a number is never changed and never reused. New codes are added only
// at the end of a range. A new code increments [`crate::API_VERSION`]. The
// golden test in `tests/error_codes.rs` pins every value.
//
// On the wire (serde) a code is its number.
type ErrorCode uint32

const (
	ErrorCodeClosed           ErrorCode = 1
	ErrorCodeCancelled        ErrorCode = 2
	ErrorCodeDeadlineExceeded ErrorCode = 3
	ErrorCodeInvalidInput     ErrorCode = 100
	ErrorCodeInvalidId        ErrorCode = 101
	ErrorCodeWrongState       ErrorCode = 102
	ErrorCodeUnsupported      ErrorCode = 103
	// A resource is full for now (a queue, a worker pool); retry later.
	ErrorCodeCapacityExceeded ErrorCode = 200
	// A fixed limit is reached (sessions, advertisements, overlay paths).
	ErrorCodeLimitReached   ErrorCode = 201
	ErrorCodeStorageFailed  ErrorCode = 300
	ErrorCodeStorageCorrupt ErrorCode = 301
	// A staged candidate no longer matches the state it was staged from
	// (ADR decision 5). It stays in the storage range on purpose: a
	// candidate is a staged storage record, and "stale" means its stored
	// basis moved. Numbers never move, so this is also the stable place.
	ErrorCodeCandidateStale ErrorCode = 302
	// Stored data has a format this build does not read: newer than it
	// supports, or older than the first supported format (no legacy readers).
	ErrorCodeFormatNotSupported ErrorCode = 303
	ErrorCodePeerUnreachable    ErrorCode = 400
	ErrorCodeTimeout            ErrorCode = 401
	ErrorCodeTransportFailed    ErrorCode = 402
	ErrorCodeNotAuthorized      ErrorCode = 500
	ErrorCodeInvitationInvalid  ErrorCode = 501
	ErrorCodeInvitationExpired  ErrorCode = 502
	ErrorCodeNotMember          ErrorCode = 503
	ErrorCodeEpochMismatch      ErrorCode = 600
	ErrorCodePolicyMismatch     ErrorCode = 601
	ErrorCodeInternal           ErrorCode = 900
)

// Stable numeric code; independent of a language enum's ordinal.
func (_self ErrorCode) Number() uint32 {
	_selfBuf := FfiConverterErrorCodeINSTANCE.Lower(_self)

	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_arachne_api_fn_method_errorcode_number(
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
		return ErrorCodeFormatNotSupported
	case 14:
		return ErrorCodePeerUnreachable
	case 15:
		return ErrorCodeTimeout
	case 16:
		return ErrorCodeTransportFailed
	case 17:
		return ErrorCodeNotAuthorized
	case 18:
		return ErrorCodeInvitationInvalid
	case 19:
		return ErrorCodeInvitationExpired
	case 20:
		return ErrorCodeNotMember
	case 21:
		return ErrorCodeEpochMismatch
	case 22:
		return ErrorCodePolicyMismatch
	case 23:
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
	case ErrorCodeFormatNotSupported:
		writeInt32(writer, 13)
	case ErrorCodePeerUnreachable:
		writeInt32(writer, 14)
	case ErrorCodeTimeout:
		writeInt32(writer, 15)
	case ErrorCodeTransportFailed:
		writeInt32(writer, 16)
	case ErrorCodeNotAuthorized:
		writeInt32(writer, 17)
	case ErrorCodeInvitationInvalid:
		writeInt32(writer, 18)
	case ErrorCodeInvitationExpired:
		writeInt32(writer, 19)
	case ErrorCodeNotMember:
		writeInt32(writer, 20)
	case ErrorCodeEpochMismatch:
		writeInt32(writer, 21)
	case ErrorCodePolicyMismatch:
		writeInt32(writer, 22)
	case ErrorCodeInternal:
		writeInt32(writer, 23)
	default:
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterErrorCode.Write", value))
	}
}

type FfiDestroyerErrorCode struct{}

func (_ FfiDestroyerErrorCode) Destroy(value ErrorCode) {
}

// One event from `Client::next_event` (ADR step 4). An event names the
// queue or job that has work; the host then calls that queue's poll or
// stage call. It carries no payload, so no data is copied twice.
//
// Queue events (`Control`, `MembershipChanged`, `ProtectedReceived`,
// `PublicationReceived`) repeat until the host drains the queue. Job
// events (`RecoveryReady`, `CurrentViewReady`, `InterestChanged`,
// `Presence`) come once per ready job.
type Event uint

const (
	// Reserved; admission requests arrive as `Control`.
	EventAdmissionRequest Event = 1
	// Membership steps from gossip: `poll_membership_update`.
	EventMembershipChanged Event = 2
	// A protected delivery: `poll_protected`.
	EventProtectedReceived Event = 3
	// A recovery range ended: `poll_recovery_range`.
	EventRecoveryReady Event = 4
	// A current-view repair ended: `poll_current_view`.
	EventCurrentViewReady Event = 5
	// An interest announcement ended: `poll_interest`.
	EventInterestChanged Event = 6
	// A presence answer came back: `poll_presence`.
	EventPresence Event = 7
	// Reserved; nearby invitations arrive as `Control`.
	EventNearbyInvitation Event = 8
	// The runtime ended the session (for example a removal). Call `close`.
	EventClosed Event = 9
	// Peer control requests wait (admission, profile, presence, nearby):
	// `poll_admission` / `poll_control` until it has nothing.
	EventControl Event = 10
	// An unprotected fixture publication (no workspace): `poll`.
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

// An optional runtime feature that a host can test for.
type Feature uint

const (
	FeatureResourceTransfer Feature = 1
)

type FfiConverterFeature struct{}

var FfiConverterFeatureINSTANCE = FfiConverterFeature{}

func (c FfiConverterFeature) Lift(rb RustBufferI) Feature {
	return LiftFromRustBuffer[Feature](c, rb)
}

func (c FfiConverterFeature) Lower(value Feature) C.RustBuffer {
	return LowerIntoRustBuffer[Feature](c, value)
}

func (c FfiConverterFeature) LowerExternal(value Feature) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Feature](c, value))
}
func (FfiConverterFeature) Read(reader io.Reader) Feature {
	id := readInt32(reader)
	return Feature(id)
}

func (FfiConverterFeature) Write(writer io.Writer, value Feature) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerFeature struct{}

func (_ FfiDestroyerFeature) Destroy(value Feature) {
}

// A network mode for a client.
//
// `Tor` always exists, so generated bindings are the same for every build.
// When the runtime is built without Tor, opening a client with `Tor` gives
// `ErrorCode::Unsupported` and `Capabilities::networks` does not list it.
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

// How often background work runs (ADR step 4). `Low` is for an Android
// host in the background: longer presence and interest intervals.
type PowerProfile uint

const (
	PowerProfileNormal PowerProfile = 1
	PowerProfileLow    PowerProfile = 2
)

type FfiConverterPowerProfile struct{}

var FfiConverterPowerProfileINSTANCE = FfiConverterPowerProfile{}

func (c FfiConverterPowerProfile) Lift(rb RustBufferI) PowerProfile {
	return LiftFromRustBuffer[PowerProfile](c, rb)
}

func (c FfiConverterPowerProfile) Lower(value PowerProfile) C.RustBuffer {
	return LowerIntoRustBuffer[PowerProfile](c, value)
}

func (c FfiConverterPowerProfile) LowerExternal(value PowerProfile) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PowerProfile](c, value))
}
func (FfiConverterPowerProfile) Read(reader io.Reader) PowerProfile {
	id := readInt32(reader)
	return PowerProfile(id)
}

func (FfiConverterPowerProfile) Write(writer io.Writer, value PowerProfile) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerPowerProfile struct{}

func (_ FfiDestroyerPowerProfile) Destroy(value PowerProfile) {
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

type FfiConverterSequenceFeature struct{}

var FfiConverterSequenceFeatureINSTANCE = FfiConverterSequenceFeature{}

func (c FfiConverterSequenceFeature) Lift(rb RustBufferI) []Feature {
	return LiftFromRustBuffer[[]Feature](c, rb)
}

func (c FfiConverterSequenceFeature) Read(reader io.Reader) []Feature {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]Feature, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterFeatureINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceFeature) Lower(value []Feature) C.RustBuffer {
	return LowerIntoRustBuffer[[]Feature](c, value)
}

func (c FfiConverterSequenceFeature) LowerExternal(value []Feature) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]Feature](c, value))
}

func (c FfiConverterSequenceFeature) Write(writer io.Writer, value []Feature) {
	if len(value) > math.MaxInt32 {
		panic("[]Feature is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterFeatureINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceFeature struct{}

func (FfiDestroyerSequenceFeature) Destroy(sequence []Feature) {
	for _, value := range sequence {
		FfiDestroyerFeature{}.Destroy(value)
	}
}

type FfiConverterSequenceNetwork struct{}

var FfiConverterSequenceNetworkINSTANCE = FfiConverterSequenceNetwork{}

func (c FfiConverterSequenceNetwork) Lift(rb RustBufferI) []Network {
	return LiftFromRustBuffer[[]Network](c, rb)
}

func (c FfiConverterSequenceNetwork) Read(reader io.Reader) []Network {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]Network, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterNetworkINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceNetwork) Lower(value []Network) C.RustBuffer {
	return LowerIntoRustBuffer[[]Network](c, value)
}

func (c FfiConverterSequenceNetwork) LowerExternal(value []Network) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]Network](c, value))
}

func (c FfiConverterSequenceNetwork) Write(writer io.Writer, value []Network) {
	if len(value) > math.MaxInt32 {
		panic("[]Network is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterNetworkINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceNetwork struct{}

func (FfiDestroyerSequenceNetwork) Destroy(sequence []Network) {
	for _, value := range sequence {
		FfiDestroyerNetwork{}.Destroy(value)
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
type PublicationId = string
type FfiConverterTypePublicationId = FfiConverterString
type FfiDestroyerTypePublicationId = FfiDestroyerString

var FfiConverterTypePublicationIdINSTANCE = FfiConverterString{}

func LiftFromExternalTypePublicationId(value ExternalCRustBuffer) PublicationId {
	return FfiConverterTypePublicationIdINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypePublicationId(value PublicationId) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypePublicationIdINSTANCE.Lower(value))
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
type TopicName = string
type FfiConverterTypeTopicName = FfiConverterString
type FfiDestroyerTypeTopicName = FfiDestroyerString

var FfiConverterTypeTopicNameINSTANCE = FfiConverterString{}

func LiftFromExternalTypeTopicName(value ExternalCRustBuffer) TopicName {
	return FfiConverterTypeTopicNameINSTANCE.Lift(RustBufferFromExternal(value))
}

func LowerToExternalTypeTopicName(value TopicName) ExternalCRustBuffer {
	return RustBufferFromC(FfiConverterTypeTopicNameINSTANCE.Lower(value))
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

// Read a stable code in languages that do not generate methods on errors.
func ApiErrorCode(error *ApiError) ErrorCode {
	return FfiConverterErrorCodeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_api_fn_func_api_error_code(FfiConverterApiErrorINSTANCE.Lower(error), _uniffiStatus),
		}
	}))
}

// The contract version used by this build.
func ApiVersion() uint32 {
	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_arachne_api_fn_func_api_version(_uniffiStatus)
	}))
}

// Resource limits used when the host does not supply overrides.
func DefaultLimits() Limits {
	return FfiConverterLimitsINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_arachne_api_fn_func_default_limits(_uniffiStatus),
		}
	}))
}
