#ifndef ARACHNE_SDK_H
#define ARACHNE_SDK_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

enum ArachneNetwork {
  ARACHNE_NETWORK_DIRECT = 0,
  ARACHNE_NETWORK_LAN = 1,
  ARACHNE_NETWORK_NEARBY = 2,
  ARACHNE_NETWORK_WAN = 3,
  ARACHNE_NETWORK_RELAY_ONLY = 4,
  ARACHNE_NETWORK_WAN_ONLY = 5
};

typedef struct {
  uint8_t *data;
  size_t len;
} ArachneBuffer;

typedef struct {
  int32_t status;
  ArachneBuffer value;
} ArachneResult;

typedef struct {
  int32_t status;
  ArachneBuffer value;
  ArachneBuffer snapshot;
} ArachneStoredResult;

/*
 * Returned buffers are owned by this library and must be freed exactly once
 * with arachne_sdk_buffer_free. On status 0, value contains success data; on
 * nonzero status it contains UTF-8 error text. Stored results keep snapshot
 * bytes separate from JSON.
 *
 * A nonempty endpoint secret must be exactly 32 bytes. JSON requests are
 * bounded to 128 KiB. The stored snapshot input is bounded to 1 MiB and may
 * have a smaller operation-specific limit.
 */
ArachneResult arachne_sdk_open(uint32_t network, const uint8_t *secret,
                               size_t secret_len);
ArachneResult arachne_sdk_execute(int64_t handle, const uint8_t *request,
                                  size_t request_len);
ArachneStoredResult arachne_sdk_execute_stored(
    int64_t handle, const uint8_t *request, size_t request_len,
    const uint8_t *snapshot, size_t snapshot_len);
ArachneResult arachne_sdk_describe(int64_t handle);
ArachneResult arachne_sdk_enable_record_storage(
    int64_t handle, const uint8_t *path, size_t path_len,
    const uint8_t *root, size_t root_len);
ArachneResult arachne_sdk_restore_record_storage(
    int64_t handle, const uint8_t *path, size_t path_len,
    const uint8_t *root, size_t root_len,
    const uint8_t *workspace, size_t workspace_len);
ArachneResult arachne_sdk_save_candidate(
    int64_t handle, const uint8_t *snapshot, size_t snapshot_len);
ArachneResult arachne_sdk_cancel(int64_t handle);
/* Success value contains one ASCII digit: '1' for ready or '0' for closed. */
ArachneResult arachne_sdk_wait_for_work(int64_t handle);
ArachneResult arachne_sdk_close(int64_t handle);
/* Only free buffers returned by this library; pass their original data/len. */
void arachne_sdk_buffer_free(uint8_t *data, size_t len);

#ifdef __cplusplus
}
#endif

#endif
