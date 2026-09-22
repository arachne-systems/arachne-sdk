//! Small C ABI shared by the Go, Python, and Swift adapters.
//!
//! The foreign API carries bounded JSON requests to the runtime. Staged
//! snapshots use a separate byte buffer so their exact bytes survive every
//! language boundary.

use std::{
    panic::{AssertUnwindSafe, catch_unwind},
    path::Path,
    ptr, slice,
};

// ponytail: cap foreign snapshot input at 1 MiB; raise with the core sealed-bundle limit if formats grow.
const MAX_SNAPSHOT_INPUT: usize = 1024 * 1024;
const MAX_PATH_INPUT: usize = 4096;

#[repr(C)]
pub struct ArachneBuffer {
    pub data: *mut u8,
    pub len: usize,
}

#[repr(C)]
pub struct ArachneResult {
    /// 0 means success, 1 means a runtime/input error, and 2 means a trapped panic.
    pub status: i32,
    /// Success data, or UTF-8 error text when status is nonzero.
    pub value: ArachneBuffer,
}

#[repr(C)]
pub struct ArachneStoredResult {
    /// 0 means success, 1 means a runtime/input error, and 2 means a trapped panic.
    pub status: i32,
    /// JSON result on success, or UTF-8 error text when status is nonzero.
    pub value: ArachneBuffer,
    /// Opaque staged snapshot on success; empty on error.
    pub snapshot: ArachneBuffer,
}

fn into_buffer(bytes: Vec<u8>) -> ArachneBuffer {
    let boxed = bytes.into_boxed_slice();
    let len = boxed.len();
    let data = Box::into_raw(boxed) as *mut u8;
    ArachneBuffer { data, len }
}

fn response(operation: impl FnOnce() -> std::result::Result<Vec<u8>, String>) -> ArachneResult {
    match catch_unwind(AssertUnwindSafe(operation)) {
        Ok(Ok(value)) => ArachneResult {
            status: 0,
            value: into_buffer(value),
        },
        Ok(Err(error)) => ArachneResult {
            status: 1,
            value: into_buffer(error.into_bytes()),
        },
        Err(_) => ArachneResult {
            status: 2,
            value: into_buffer(b"panic trapped in Arachne SDK".to_vec()),
        },
    }
}

fn stored_response(
    operation: impl FnOnce() -> std::result::Result<[Vec<u8>; 2], String>,
) -> ArachneStoredResult {
    match catch_unwind(AssertUnwindSafe(operation)) {
        Ok(Ok([value, snapshot])) => ArachneStoredResult {
            status: 0,
            value: into_buffer(value),
            snapshot: into_buffer(snapshot),
        },
        Ok(Err(error)) => ArachneStoredResult {
            status: 1,
            value: into_buffer(error.into_bytes()),
            snapshot: into_buffer(Vec::new()),
        },
        Err(_) => ArachneStoredResult {
            status: 2,
            value: into_buffer(b"panic trapped in Arachne SDK".to_vec()),
            snapshot: into_buffer(Vec::new()),
        },
    }
}

/// Read a foreign-owned input after enforcing the limit before constructing a slice.
///
/// # Safety
/// For nonzero `len`, `data` must point to `len` readable bytes.
unsafe fn input<'a>(
    data: *const u8,
    len: usize,
    limit: usize,
    name: &str,
) -> std::result::Result<&'a [u8], String> {
    if len > limit {
        return Err(format!("{name} exceeds the SDK limit"));
    }
    if len == 0 {
        return Ok(&[]);
    }
    if data.is_null() {
        return Err(format!("{name} pointer is null"));
    }
    // SAFETY: the caller contract is documented on each exported function.
    Ok(unsafe { slice::from_raw_parts(data, len) })
}

fn open_profile(network: u32, secret: &[u8]) -> std::result::Result<i64, String> {
    let secret: Option<[u8; 32]> = match secret.len() {
        0 => None,
        32 => Some(secret.try_into().expect("checked 32-byte endpoint secret")),
        _ => return Err("endpoint secret must be empty or exactly 32 bytes".into()),
    };
    let required_secret = || {
        secret
            .as_ref()
            .ok_or_else(|| "this network profile requires a 32-byte endpoint secret".to_owned())
    };

    match network {
        0 => arachne_runtime::create(secret.as_ref()),
        1 => arachne_runtime::create_lan(required_secret()?),
        2 => arachne_runtime::create_nearby(required_secret()?),
        3 => arachne_runtime::create_wan(required_secret()?),
        4 => arachne_runtime::create_relay(required_secret()?),
        5 => arachne_runtime::create_wan_only(required_secret()?),
        _ => Err("unknown network profile".into()),
    }
}

/// Open an endpoint. Network values are defined in `arachne_sdk.h`.
///
/// # Safety
/// For nonzero `secret_len`, `secret` must point to `secret_len` readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_open(
    network: u32,
    secret: *const u8,
    secret_len: usize,
) -> ArachneResult {
    response(|| {
        // SAFETY: the exported function documents the foreign pointer contract.
        let secret = unsafe { input(secret, secret_len, 32, "endpoint secret")? };
        Ok(open_profile(network, secret)?.to_string().into_bytes())
    })
}

/// Execute one core JSON request. The request must contain its `op` field.
///
/// # Safety
/// For nonzero `request_len`, `request` must point to `request_len` readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_execute(
    handle: i64,
    request: *const u8,
    request_len: usize,
) -> ArachneResult {
    response(|| {
        // SAFETY: the exported function documents the foreign pointer contract.
        let request = unsafe {
            input(
                request,
                request_len,
                arachne_runtime::MAX_REQUEST,
                "request",
            )?
        };
        arachne_runtime::execute(handle, request)
    })
}

/// Execute a request whose staged input or output includes an opaque snapshot.
///
/// # Safety
/// Each nonzero input length requires a pointer to that many readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_execute_stored(
    handle: i64,
    request: *const u8,
    request_len: usize,
    snapshot: *const u8,
    snapshot_len: usize,
) -> ArachneStoredResult {
    stored_response(|| {
        // SAFETY: the exported function documents the foreign pointer contract.
        let request = unsafe {
            input(
                request,
                request_len,
                arachne_runtime::MAX_REQUEST,
                "request",
            )?
        };
        // SAFETY: the exported function documents the foreign pointer contract.
        let snapshot = unsafe { input(snapshot, snapshot_len, MAX_SNAPSHOT_INPUT, "snapshot")? };
        arachne_runtime::execute_stored(handle, request, snapshot)
    })
}

/// Return the same endpoint projection exposed by the typed Rust client.
#[unsafe(no_mangle)]
pub extern "C" fn arachne_sdk_describe(handle: i64) -> ArachneResult {
    response(|| Ok(arachne_runtime::describe(handle)?.into_bytes()))
}

/// Attach caller-managed encrypted record storage to this endpoint.
///
/// # Safety
/// Both slices must point to their declared number of readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_enable_record_storage(
    handle: i64,
    path: *const u8,
    path_len: usize,
    root: *const u8,
    root_len: usize,
) -> ArachneResult {
    response(|| {
        // SAFETY: the exported function documents each foreign pointer contract.
        let path = unsafe { input(path, path_len, MAX_PATH_INPUT, "storage path")? };
        // SAFETY: the exported function documents each foreign pointer contract.
        let root = unsafe { input(root, root_len, 32, "storage root")? };
        let root: &[u8; 32] = root
            .try_into()
            .map_err(|_| "storage root must be exactly 32 bytes".to_owned())?;
        let path = std::str::from_utf8(path).map_err(|_| "storage path must be UTF-8")?;
        arachne_runtime::enable_record_storage(handle, Path::new(path), root)?;
        Ok(Vec::new())
    })
}

/// Restore a workspace from caller-managed encrypted record storage.
///
/// # Safety
/// Both slices must point to their declared number of readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_restore_record_storage(
    handle: i64,
    path: *const u8,
    path_len: usize,
    root: *const u8,
    root_len: usize,
    workspace: *const u8,
    workspace_len: usize,
) -> ArachneResult {
    response(|| {
        // SAFETY: the exported function documents each foreign pointer contract.
        let path = unsafe { input(path, path_len, MAX_PATH_INPUT, "storage path")? };
        // SAFETY: the exported function documents each foreign pointer contract.
        let root = unsafe { input(root, root_len, 32, "storage root")? };
        // SAFETY: the exported function documents each foreign pointer contract.
        let workspace = unsafe { input(workspace, workspace_len, 32, "workspace ID")? };
        let root: &[u8; 32] = root
            .try_into()
            .map_err(|_| "storage root must be exactly 32 bytes".to_owned())?;
        let workspace: [u8; 32] = workspace
            .try_into()
            .map_err(|_| "workspace ID must be exactly 32 bytes".to_owned())?;
        let path = std::str::from_utf8(path).map_err(|_| "storage path must be UTF-8")?;
        let value =
            arachne_runtime::restore_record_storage(handle, Path::new(path), root, workspace)?;
        Ok(value.to_string().into_bytes())
    })
}

/// Durably save the exact staged candidate before adopting it.
///
/// # Safety
/// For nonzero `snapshot_len`, `snapshot` must point to that many readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_save_candidate(
    handle: i64,
    snapshot: *const u8,
    snapshot_len: usize,
) -> ArachneResult {
    response(|| {
        // SAFETY: the exported function documents the foreign pointer contract.
        let snapshot = unsafe { input(snapshot, snapshot_len, MAX_SNAPSHOT_INPUT, "snapshot")? };
        arachne_runtime::save_candidate(handle, snapshot)?;
        Ok(Vec::new())
    })
}

/// Interrupt outbound control exchanges.
#[unsafe(no_mangle)]
pub extern "C" fn arachne_sdk_cancel(handle: i64) -> ArachneResult {
    response(|| arachne_runtime::cancel(handle).map(|()| Vec::new()))
}

/// Park until the endpoint may have work. The success value is `1` or `0`.
#[unsafe(no_mangle)]
pub extern "C" fn arachne_sdk_wait_for_work(handle: i64) -> ArachneResult {
    response(|| arachne_runtime::wait_for_work(handle).map(|ready| vec![u8::from(ready) + b'0']))
}

/// Close an endpoint handle.
#[unsafe(no_mangle)]
pub extern "C" fn arachne_sdk_close(handle: i64) -> ArachneResult {
    response(|| arachne_runtime::close(handle).map(|()| Vec::new()))
}

/// Free a buffer returned by this library. Do not pass foreign-owned memory.
///
/// # Safety
/// `data` and `len` must be the pair returned in an `ArachneBuffer` by this library.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn arachne_sdk_buffer_free(data: *mut u8, len: usize) {
    if data.is_null() {
        return;
    }
    let slice = ptr::slice_from_raw_parts_mut(data, len);
    // SAFETY: the caller contract requires the exact buffer pair returned above.
    unsafe { drop(Box::from_raw(slice)) };
}
