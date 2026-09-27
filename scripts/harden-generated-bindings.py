#!/usr/bin/env python3
"""Apply security fixes that UniFFI cannot express in its record metadata."""

from pathlib import Path
import sys


def replace_once(path: Path, old: str, new: str) -> None:
    source = path.read_text()
    if source.count(old) != 1:
        raise SystemExit(f"expected one hardening target in {path}")
    path.write_text(source.replace(old, new))


root = Path(sys.argv[1])
replace_once(
    root / "python/arachne_generated/arachne_runtime.py",
    'return "ClientConfig(network={}, secret={}, transport={}, storage={})".format(self.network, self.secret, self.transport, self.storage)',
    'return "ClientConfig(network={}, secret={}, transport={}, storage={})".format(self.network, "[REDACTED]" if self.secret is not None else None, self.transport, self.storage)',
)
replace_once(
    root / "go/arachne_runtime/arachne_runtime.go",
    """type ClientConfig struct {
\tNetwork arachne_api.Network
\tSecret  *[]byte
\t// Relay, lookup and deadline overrides. `Default` keeps the profile's.
\tTransport TransportOptions
\t// Record storage. Required to create, join or restore a workspace.
\tStorage **StorageConfig
}
""",
    """type ClientConfig struct {
\tNetwork arachne_api.Network
\tSecret  *[]byte
\t// Relay, lookup and deadline overrides. `Default` keeps the profile's.
\tTransport TransportOptions
\t// Record storage. Required to create, join or restore a workspace.
\tStorage **StorageConfig
}

func (r ClientConfig) String() string {
\tsecret := "<nil>"
\tif r.Secret != nil {
\t\tsecret = "[REDACTED]"
\t}
\treturn fmt.Sprintf("ClientConfig{Network:%v Secret:%s Transport:%v Storage:%v}", r.Network, secret, r.Transport, r.Storage)
}

func (r ClientConfig) GoString() string {
\treturn r.String()
}
""",
)
replace_once(
    root / "kotlin/org/arachne/core/runtime/arachne_runtime.kt",
    """    companion object
}

/**
 * @suppress
 */
public object FfiConverterTypeClientConfig:""",
    """    override fun toString(): String =
        "ClientConfig(network=${`network`}, secret=${if (`secret` == null) null else "[REDACTED]"}, transport=${`transport`}, storage=${`storage`})"

    companion object
}

/**
 * @suppress
 */
public object FfiConverterTypeClientConfig:""",
)
replace_once(
    root / "swift/ArachneRuntime/ArachneRuntime.swift",
    r"""#if compiler(>=6)
extension ClientConfig: Sendable {}
#endif
""",
    r"""extension ClientConfig: CustomStringConvertible {
    public var description: String {
        "ClientConfig(network: \(network), secret: \(secret == nil ? "nil" : "[REDACTED]"), transport: \(transport), storage: \(String(describing: storage)))"
    }
}

#if compiler(>=6)
extension ClientConfig: Sendable {}
#endif
""",
)
replace_once(
    root / "kotlin/org/arachne/core/runtime/arachne_runtime.kt",
    "    var `commitDigest`: Key32\n    , \n    /**\n     * Freshness anchor",
    "    var `commitDigest`: Key32\n    ,\n    /**\n     * Freshness anchor",
)
replace_once(
    root / "swift/ArachneRuntime/ArachneRuntime.swift",
    "public init(workspace: WorkspaceId, epoch: UInt64, member: MemberId, commitDigest: Key32, \n",
    "public init(workspace: WorkspaceId, epoch: UInt64, member: MemberId, commitDigest: Key32,\n",
)
replace_once(
    root / "swift/ArachneRuntime/ArachneRuntime.swift",
    "commitDigest: FfiConverterTypeKey32.read(from: &buf), \n                freshness:",
    "commitDigest: FfiConverterTypeKey32.read(from: &buf),\n                freshness:",
)
