# Catalog capacity qualification

Run `make test-catalog-capacity` to check the real decoded retention limit. The target creates a new evidence directory for each run. It executes the same original-backup case with race detection and with `CGO_ENABLED=0`. The required `Authorization Capacity` CI job executes this target and retains its evidence. Its protection identity and authorization churn check remain unchanged.

The large fixture has the `catalogcapacity` build tag. It has no runtime skip. Ordinary native platform tests retain the smaller topology, selection, retention, interruption, import, and recovery contracts. The capacity runner refuses a missing, duplicate, skipped, or failed named result. A package must also report completion.

Final qualification uses Go 1.27.2 and the exact published Starmap module with `GOWORK=off`. The runner rejects a producer module replacement and an inherited workspace setting other than `off`.

The regression starts a real offline runtime. It writes two legal historical capsules through the producer's retention owner. Each capsule has a highly compressed recovery record whose decoded size exceeds 128 MiB. The source uses persistent Badger, SQLite, the canonical file inventory, and an actual backup bundle. The compiler reads that original backup.

Both historical capsules and the original selected capsule must remain retained in exactly two native producer batches. Historical lineage bytes grant no current authority.

The compiler checks each original capsule once through the producer's retention comparison factory. It keeps private immutable comparisons beside the detached inventory. Packing and capacity evidence reuse their byte counts. Retained-input checks validate native ownership, completed batches, entries, and envelope hashes without decoding unchanged historical inputs. Recovery restart rebuilds comparisons from the verified original backup. Selected replay and current permission checks remain separate.

The decoded lane requires at least 12 GiB of host RAM and 2 GiB of free disk. It sets a 4 GiB Go memory target, an 8 GiB sampled process-tree RSS bound, and a 1 GiB allocated scratch-disk bound. The race test has a six-minute test deadline and an eight-minute process deadline. The pure-Go test has a three-minute test deadline and a four-minute process deadline. These process deadlines include compilation. The required job has a twenty-minute total allowance for both profiles, the three-minute authorization check, and setup.

Evidence records the source head, tracked diff and untracked input digests, published producer module, toolchain, command, and test-event digest. Resource measurements include elapsed time, native process high-water RSS, sampled process-tree peak RSS, and sampled allocated scratch disk. Native high-water RSS describes the largest process. Sampled process-tree RSS includes simultaneous descendants and can miss a brief peak between samples.

The runner stops only its own process group on a budget failure and retains failure evidence. Scratch accounting includes allocated blocks and counts each native inode once. It does not count a sparse file's logical length as allocated disk.

These limits describe different resources:

| Contract | Limit | Counted bytes |
| --- | --- | --- |
| Retained fleet inventory | 96 entries and 2 GiB | Serialized fleet snapshots |
| Canonical catalog payload | 32 MiB | Original catalog JSON |
| Retention input and batch | 256 MiB, independently | Manifest, payload, compressed exported recovery, exact source descriptor; separately, decoded recovery records |
| Retained envelope parser | 512 MiB | Encoded and decoded envelope JSON, including expansion |
| Native topology transaction | 128 mutations and 4 MiB | Keys, preimages, and new values; the compiler reserves marker space |
| History payloads | 64 MiB | Typed history step payloads |
| History catalog assets | 16 GiB | Retained encoded native stage assets |
| Original-backup compiler record | 64 KiB | Original identities, counts, and digests |

The 512 MiB envelope limit is a parser safety ceiling. It does not promise a 512 MiB indivisible retention input. Native retention requires each input to fit its existing 256 MiB combined raw budget and decoded budget. An envelope that passes passive checks proves structure, rather than completed retention. Original local descriptors and fleet publications have separate source limits. A complete maximum-capacity proof must show each original capsule's provenance and both byte dimensions without replacing or dropping source bytes.

The combined 96-entry, 2 GiB workload remains a separate qualification requirement. Passing the decoded regression or the individual limit checks does not qualify that workload, the complete application, or disaster-recovery RPO/RTO. The maximum procedure must preserve distinct accepted and candidate selections, rollback history, original reconstruction capsules, exact native import positions, and closed SQL ownership. It must record actual batches, transaction sizes, interruption and retry results, peak memory, and disk use.

The maximum procedure has source and compilation checks. Its runtime qualification remains UNVERIFIED. Run `make test-catalog-maximum` only in its coordinated local or dedicated host window.

The profile sets a 12 GiB Go memory target, a 16 GiB sampled RSS stop, 24 GiB scratch disk, and a thirty-minute process deadline. Its nested Go test has a twenty-eight-minute deadline. The profile requires at least 24 GiB host RAM and 26 GiB free disk. Standard hosted CI does not execute it.

The procedure creates a genuine offline runtime accepted generation and a different candidate from a local observation. Native retention writes 94 large historical capsules. The compiler derives its census from the complete original backup and writes actual fleet snapshots within 8 MiB of the 2 GiB limit. It replays both selected inputs before materializing the directional selection. Persistent Badger replay holds the exact closed SQLite import guard for every catalog mutation. A second full closed backup supplies the reverse transfer.

The result must preserve all 96 generation and recovery identities, the 32 retained rollback entries, and distinct accepted and candidate selections. Evidence records raw and decoded producer batch totals and each native transaction's count and bytes. It also records both compiler seals, three original backup identities, and two committed lost-reply retries. Component barrier release permits the next backup while the SQL witness remains closed. This procedure never approves gateway admission.

GitHub documents 16 GB RAM and 14 GB storage for standard public Ubuntu runners. The private variant has 8 GB RAM. A hosted maximum recipe needs its own measured resource contract before use. [GitHub-hosted runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
