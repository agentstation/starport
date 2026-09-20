# Cache storage

Application composition uses process memory for response, model, and extraction caches, independent of the durable storage backend.
The cache manager accepts a cache-owned interface. It cannot accept the application's durable KV handle directly.
An injected store requires the explicit `distributed` strategy. The manager owns that store after successful construction.

The default Ristretto capacities are 256 MiB for responses, 16 MiB for models, and 16 MiB for extraction records.
These capacities bound cache cost, not total process memory.
Extraction caching retains its independent lifecycle when the operator disables response caching.

CSP12.1 must qualify shared-service configuration, bounded fills, and stream accumulation limits.
The legacy layered and hybrid constructors remain outside application composition.

Refills require the value and its lifetime from the same record version. Badger and Valkey provide this atomic read contract.
Backends without finite lifetime evidence skip local refill. Each retained entry checks its source deadline independently of the cache queue.
Writes invalidate local entries. A later read must prove the stored version's actual expiry.

No production qualification or latency improvement follows from this interface change alone.

## Optional response fills

Response fills use two lifecycle-owned workers. The queue limits queued and active work to 1,024 entries and 4 MiB of charged bytes.
The byte charge includes copied keys, copied payloads, and 64 bytes per job. It does not measure total process or transport memory.
Admission drops optional work when capacity or the admission lock is unavailable.
`SetResponse` does not confirm persistence or a subsequent cache hit.

Writes have a 500 ms context deadline. Cache-only adapters must honor cancellation for blocking work.
Shutdown cancels fills and joins workers before closing the stores.
The admin info response reports limits, retained work, completed fills, failures, and drops under `response_cache`.
It exposes no cache keys or payloads.

Injected response-cache reads have a 2 ms deadline. Local response reads use no additional timer.
Current authorization and catalog checks still govern cached delivery.
Model and extraction fills and full shared-service and stream qualification remain under CSP12.1.

## Stream retention

The canonical response-cache buffer limits each stream to 256 KiB of charged input and 1,024 events.
Either limit discards all accumulated cache input and prevents further accumulation for that stream.
Delivery continues with the original events. EOF, upstream failure, and close release retained input.
The byte check precedes cloning and includes media, tool arguments, probability data, metadata, and strings.
Copies own their strings so a short substring cannot retain a large provider buffer.

Fixed structure charges and doubled payload charges allow for slice growth and allocation rounding on supported 64-bit hosts.
The charge is an admission bound. Full retained-heap and concurrent-stream qualification remain required.
EOF response reconstruction and encoding remain synchronous and need separate latency qualification.

## Dedicated shared response cache

The default `STARPORT_CACHE_BACKEND=local` uses process memory.
Select `STARPORT_CACHE_BACKEND=valkey` with an explicit `STARPORT_CACHE_URL` and `STARPORT_CACHE_NAMESPACE` for shared response bytes.
Model and extraction caches remain local.
Namespaces contain 1 through 64 letters, digits, underscores, dots, or hyphens.
Use the same namespace only for replicas of one deployment. Keys use the `starport:cache:v1:<namespace>:` prefix.

The URI accepts credentials and an optional database number.
Use `valkeys://` or `rediss://` for TLS with system trust and hostname verification.
Plaintext is available on loopback. Remote plaintext requires `STARPORT_CACHE_ALLOW_INSECURE=true`.
Query options, fragments, and additional endpoints are invalid.
Redis URI syntax does not establish Redis compatibility qualification.

The cache service must be separate from durable KV. A different database or namespace does not isolate service eviction.
Validation rejects the same declared host and port, including common loopback aliases.
Operators must also check DNS aliases, proxies, and actual service ownership.
The shared cache needs deployment-owned capacity and eviction settings.

Connection work runs in the background with bounded connection attempts and one-second recovery polling.
Startup continues while the optional service is unavailable. Reads become misses through the existing proxy error path.
Cache failure does not change permission or budget admission.

The connection owner retries initial failures. The client reconnects after connection loss.
Shutdown cancels connection work and closes the dedicated client.
The admin `response_cache.shared` status reports the configured service and its availability, without connection values.

Scratch development rejects shared-cache configuration. Use an initialized deployment to test the shared-service recipe.
Native TLS deployment, aggregate transport memory, and cache-outage latency remain part of production acceptance.

## Shared-cache trust and access

`STARPORT_CACHE_CA_FILE` selects an explicit PEM trust bundle for a TLS endpoint.
The connection uses the bundle's roots. The loader accepts a regular file of at most 1 MiB.
File access remains under deployment control. The file appears as `cache-ca` in the configuration file inventory.

Relative paths require `STARPORT_RELATIVE_PATH_BASE=config` and resolve under the selected configuration directory.
The reader confines the final file lookup to its parent directory.
Replace the bundle and restart Starport to change the active trust roots.

Provision a separate cache user for each deployment. Its ACL must restrict keys to `starport:cache:v1:<namespace>:*`.
The cache needs these commands: `hello`, `ping`, `select`, `client|setname`, `client|setinfo`, `get`, `strlen`, `set`, `eval`, `evalsha`, and `script|load`.
The service administrator owns ACL changes. Starport does not grant itself access or claim another namespace.

Replicas of one deployment can share the scoped credential. Independent deployments must use distinct credentials and namespaces.

The connection monitor writes and reads the reserved `__starport_health_v1__` key under its namespace.
The probe contains one fixed byte and expires after one second. It contains no caller data.
A denied probe keeps the optional cache unavailable. It does not block required gateway operations.

The admin `response_cache.shared.state` field supplies a fixed diagnostic code.

| State | Recovery |
| --- | --- |
| `connecting` | Wait for the bounded initial connection attempt. |
| `ready` | The latest namespace probe succeeded. |
| `unavailable` | Check the endpoint, network, and service. |
| `tls_untrusted` | Configure the issuing CA and restart. |
| `tls_hostname_mismatch` | Use the certificate's valid endpoint name or replace the certificate. |
| `tls_certificate_invalid` | Check certificate dates and constraints, then replace invalid material. |
| `authentication_failed` | Correct the cache credential and restart. |
| `namespace_denied` | Correct the selected namespace or its server ACL. |
| `closed` | The application closed the connection owner. |

The TLS tests terminate TLS in a local relay before real Valkey traffic.
They cover trusted, unknown, expired, and wrong-host certificates. Native service deployments still require CSP15 qualification.
