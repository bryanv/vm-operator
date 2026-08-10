# wcp-testbed-setup

A small CLI that automates day-1 setup of a WCP (vSphere Supervisor)
testbed:

1. Ensures a subscribed content library named `vmservice` exists (creating
   and syncing it from a subscription URL if it doesn't), unless
   `-content-library` points at a different, already-existing library --
   in that case the `vmservice` library is left alone entirely.
2. Creates a new Supervisor namespace on a Namespaces-enabled cluster.
3. Associates the namespace with VM classes, a content library, and storage
   policies.

It talks to vCenter directly via govmomi (`vapi/library`, `vapi/namespace`,
`pbm`) — no SSH or DCLI involved. It is idempotent: re-running against an
existing content library or namespace is a no-op.

## Usage

```sh
go run ./hack/wcp-testbed-setup \
  -testbed /path/to/testbed.json \
  -namespace demo-ns
```

`-testbed` also accepts an `http(s)://` URL (e.g. a UTS deliverable blob
link). `-cluster` and `-datacenter` may both be omitted, in which case the
first one found of each is used — pass them explicitly when a testbed has
more than one (very few do).

### Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-testbed` | yes | — | Path or `http(s)://` URL to testbed.json |
| `-namespace` | yes | — | Name of the namespace to create |
| `-cluster` | no | *(first cluster found)* | Name of the Namespaces-enabled cluster to create the namespace on |
| `-datacenter` | no | *(first datacenter found)* | Name of the datacenter containing the cluster |
| `-zones` | no | *(all zones on the cluster, if more than one)* | Comma-separated vSphere Zone names to bind the namespace to |
| `-vm-classes` | no | *(all classes)* | Comma-separated VM class IDs (e.g. `best-effort-small`) to associate; empty means every VM class known to vCenter |
| `-content-library` | no | *(the ensured `vmservice` library)* | Name of an existing content library to associate with the namespace instead; when set, the `vmservice` library is not created/ensured at all |
| `-storage-policies` | no | `wcpglobal-storage-profile,vm-encryption-policy` | Comma-separated storage policy names to associate (each gets a fixed 512000 MiB quota) |
| `-cl-subscription-url` | no | `https://wp-content-pstg.broadcom.com/vmsvc/lib.json` | Subscription URL for the `vmservice` content library |
| `-datastore` | no | *(auto-pick)* | Datastore to back the `vmservice` content library; auto-picks from `vsanDatastore`, `sharedVmfs-0`, `nfs0-1` |
| `-insecure` | no | `true` | Skip TLS certificate verification (testbeds typically use self-signed certs) |

A storage policy named in `-storage-policies` that doesn't exist on the
target vCenter is skipped with a warning rather than failing the run; the
run fails only if none of the requested policies resolve.

If a requested `-cluster` isn't Namespaces-enabled, or `-namespace` already
exists, the tool reports that clearly and exits accordingly (namespace
already existing is treated as success/no-op).

Namespace-to-zone binding relies on an unreleased/experimental vAPI field not
exposed by govmomi's typed client, so it's sent as a best-effort raw REST
call: if the target vCenter build doesn't support it, a warning is logged
and the rest of the setup (which already succeeded via the stable API) is
unaffected.
