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
