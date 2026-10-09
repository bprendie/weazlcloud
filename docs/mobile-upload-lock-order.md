# Upload locks and lifecycle

October 9, 2026 implementation. This is an internal server contract.

| Operation | Acquisition order | Long work |
| --- | --- | --- |
| Grant read | user-state mutex | No I/O or callbacks |
| Device/account invalidation | publication write barrier → user-state mutex | User-state persistence; no Library/parts callbacks |
| Guarded publication | caller's short mutation lock → publication read barrier → short user-state validation | State mutex released before callback; callback may persist catalog/session |
| Photo catalog batch | Library mutation lock → publication read barrier → Catalog mutex | One durable encrypted catalog write per batch |
| Part admission | session gate → short manager bookkeeping/quota checks | Validate header and capture staging generation |
| Part receiving | receiver lifecycle lease only | Network read, bounded encryption, private temp file, file sync |
| Part commit | session gate → publication read barrier → short user-state validation | Revalidate generation, persist receipt and header; no network read |
| Cancel | session gate → short manager bookkeeping, then release gate | Block new receives and cancel/join receivers outside gate; reacquire gate for coordinator cleanup |
| Owner release | short manager bookkeeping, then release manager mutex | Cancel/join receivers and finalizers, join metadata gates, then release quota |

User-state reads never acquire the publication barrier. A pending invalidation
writer can block a later publication, but cannot stop ordinary grant checks.
Publication callbacks must not call authorization mutation methods or acquire a
second publication lease. Invalidation methods release their barrier/state locks
before higher-level lifecycle teardown or resource cleanup. The barrier is global
within the user store; it protects current ordering without holding the global
state mutex through expensive catalog persistence.

Receivers are tracked independently of finalizers. The finalizer cannot start
while a receiver is active for that upload; cancellation/sweep cannot delete an
active receiver's files. Receiver cancellation closes the body; the desk reader
also sets the request read deadline so a real server network Read is interrupted.
Registry/vault session contexts propagate cancellation. Body keys are cleared
only after the writer closes and its temp file is cleaned up.

Each receiver has its own encrypted temp file. Identical concurrent parts may
receive within the bounded receiver pool, but their short commits compare the
existing durable receipt under one session gate. Only the first valid commit
publishes a receipt/counters. A conflicting checksum never overwrites that part.
This uses atomic receipt comparison instead of holding a per-part mutex through
a client-controlled body. There is no unbounded queue of per-part waiters.

Session Key and CreatedAt identify the captured staging generation. Cancelled,
restarted, wrong-device, or expired generations cannot publish a late part. The
commit recomputes counters from durable receipts under the gate. Restart still
uses the existing data/receipt/header reconciliation, with no schema change.

Pending-work admission reconstructs counts/declared bytes from durable .live
headers, with new admission serialized under admissionMu. It reads atomically
written headers without acquiring other session gates; it never waits for body
receivers. Stale terminal markers are ignored; already accepted sessions bypass
new-work limits. Disk/quota reservations keep their existing independent checks.
