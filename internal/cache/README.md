# Cache storage

Application composition uses process memory for response, model, and extraction caches, independent of the durable storage backend.
The cache manager accepts a cache-owned interface. It cannot accept the application's durable KV handle directly.
An injected store requires the explicit `distributed` strategy. The manager owns that store after successful construction.

The default Ristretto capacities are 256 MiB for responses, 16 MiB for models, and 16 MiB for extraction records.
These capacities bound cache cost, not total process memory.
Extraction caching retains its independent lifecycle when the operator disables response caching.

CSP12.1 still owns shared-service configuration, bounded asynchronous fills, and stream accumulation limits.
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
Model and extraction fills, dedicated shared-service configuration, and stream limits remain under CSP12.1.

## Stream retention

The canonical response-cache buffer limits each stream to 256 KiB of charged input and 1,024 events.
Either limit discards all accumulated cache input and prevents further accumulation for that stream.
Delivery continues with the original events. EOF, upstream failure, and close release retained input.
The byte check precedes cloning and includes media, tool arguments, probability data, metadata, and strings.
Copies own their strings so a short substring cannot retain a large provider buffer.

Fixed structure charges and doubled payload charges allow for slice growth and allocation rounding on supported 64-bit hosts.
The charge is an admission bound. Full retained-heap and concurrent-stream qualification remain required.
EOF response reconstruction and encoding remain synchronous and need separate latency qualification.
