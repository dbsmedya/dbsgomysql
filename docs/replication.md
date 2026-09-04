# pkg/replication — Consumer Guide

> **Status:** six facts and all five catalog checks are implemented. Track
> shipped changes in [CHANGELOG.md](../CHANGELOG.md).

`pkg/replication` has two layers built on one connection:

- a **facts layer** that answers typed questions about one server's
  replication state, and
- a **checks layer** that turns those facts into named findings with a
  documented rationale.

It is a sibling of [`pkg/validations`](validations.md), not a dependent: the
two packages share a shape and no code, so neither drags the other into your
build.

## Facts, not policy

The library never decides whether something is a problem. `SECONDS_BEHIND_SOURCE_WITHIN`
reports that a channel's reported lag is beyond the bound *you* supplied;
whether that means pause the job, page someone, or carry on is your policy.
**Findings carry no severity** — not even a default for you to remap, because a
default is a decision, and this one is yours.

The contract is four lines:

- a **fact** describes the server's replication state;
- a **check** returns findings when its predicate is not satisfied;
- **no findings** means the check passed for the state inspected;
- an **error** means the inspection could not be completed.

Everything the server reports as a string — thread states, GTID mode — is
returned to you as the server spelled it. Interpretation happens in checks,
never in facts.

## Getting an Inspector

You supply the connection. The library never opens, configures, or closes one,
and never imports a driver.

```go
import (
    "database/sql"

    _ "github.com/go-sql-driver/mysql" // your choice of driver
    "github.com/dbsmedya/dbsgomysql/pkg/replication"
)

db, err := sql.Open("mysql", dsn)
if err != nil {
    return err
}
defer db.Close()

insp := replication.NewInspector(db)
```

`NewInspector` accepts anything with `QueryContext` and `QueryRowContext`, so a
`*sql.DB`, `*sql.Conn`, and `*sql.Tx` all work. It is server-scoped: unlike the
validations Inspector it takes no schema, because nothing it reads is
schema-qualified. It performs no I/O, so a `nil` connection is reported by the
first fact call as `replication.ErrNilQuerier` rather than at construction.

Point it at the server whose state you want. A replica's channel status lives
on the replica; the list of registered replicas lives on the source.

## The facts

Every call takes a `context.Context` first and returns `(fact, error)`.

| Fact | Reads | Privilege | Answers |
|---|---|---|---|
| `ReplicaStatus(ctx)` | `SHOW REPLICA STATUS` | `REPLICATION CLIENT` | one `ChannelStatus` per replication channel |
| `BinaryLogEnabled(ctx)` | `@@GLOBAL.log_bin` | — | is this server writing a binary log? |
| `BinaryLogStatus(ctx)` | `SHOW BINARY LOG STATUS`, or `SHOW MASTER STATUS` on 8.0 | `REPLICATION CLIENT` | the current binary log coordinates |
| `GTIDStatus(ctx)` | `@@GLOBAL.gtid_mode`, `gtid_executed`, `gtid_purged` | — | GTID mode and both sets |
| `ReplicationConfig(ctx)` | five `@@GLOBAL` variables | — | read-only state, server id, applier configuration |
| `RegisteredReplicas(ctx)` | `SHOW REPLICAS` | **`REPLICATION SLAVE`** | which replicas have registered with this source |

The three variable facts read global system variables. The statement-backed
facts are the ones carrying a documented privilege requirement, and
`RegisteredReplicas` needs a *different* grant from the other two:
`REPLICATION SLAVE`, not `REPLICATION CLIENT`. An account that can read every
status fact can still be refused there, and that refusal arrives as an error,
never as an empty list.

```go
channels, err := insp.ReplicaStatus(ctx)
enabled, err := insp.BinaryLogEnabled(ctx)
status, err := insp.BinaryLogStatus(ctx)
gtid, err := insp.GTIDStatus(ctx)
config, err := insp.ReplicationConfig(ctx)
replicas, err := insp.RegisteredReplicas(ctx)
```

The configuration fact's type is `replication.Config`; the method that returns
it is `ReplicationConfig`.

Returned slices and pointers are yours: each call builds them fresh, so you can
mutate a result without affecting the next call.

### Channels

`ReplicaStatus` always reads every channel — it never issues `FOR CHANNEL`.
Filter in Go if you want one:

```go
for i := range channels {
    if channels[i].ChannelName == "cdc" {
        // ...
    }
}
```

The default channel's name is the empty string, which is the server's own
spelling, not a placeholder for "unnamed".

`SecondsBehindSource` is a `sql.NullInt64`, and its invalidity means exactly
one thing: **the server sent SQL `NULL`**. A missing column, an undecodable
value, or an integer that will not fit its field is an error naming the channel
and the column that caused it — never a silent zero. That distinction is the
whole point of the type: a zero you cannot distinguish from "unknown" is worse
than an error.

## What absence means

| Fact | Empty result means |
|---|---|
| `ReplicaStatus` | **This server is not a replica.** An empty slice is a real answer: a successful statement is authoritative, and an unprivileged account gets an error instead. |
| `BinaryLogStatus` | **Binary logging is disabled.** The fact returns `(nil, nil)` — a nil pointer with no error — which you can cross-check against `BinaryLogEnabled`. |
| `RegisteredReplicas` | **Nothing.** See below; this one is never proof of absence. |

`BinaryLogStatus`'s `File` may also be the empty string on a server that
previously ran with logging disabled. The field reports what the server said
rather than normalizing it away.

## `RegisteredReplicas` is a registration history, not a topology

An empty slice does **not** mean this server has no replicas, and a returned
row does **not** mean that replica is connected right now. These limits apply,
none of them errors:

- A replica that has never connected leaves no row. The source cannot tell
  "no replica exists" from "one exists and has not reached me yet".
- Rows cover replicas that *are or have been* connected, so an entry may be
  stale — the replica may have disconnected long ago.
- `Host` and `Port` are self-reported by each replica and unverified. **`Host`
  may be empty for a listed replica**: a replica started without `report_host`
  registers all the same, and the source then knows it exists without knowing
  where to reach it. An empty `Host` is data, not a row to skip.
- `Port` is the port the replica reported. An unset `report_port` normally
  reports the replica's actual listening port, so **zero means only that the
  server returned zero** — never that the option was unset.

If you need to know which replicas are streaming *now*, this fact is not that
answer, and neither is any other statement MySQL offers for asynchronous
replication. [COMPAT.md](COMPAT.md) entry 22 records the manual's claims here,
which the server contradicts on every supported version, and the live output
that settles it.

## GTID sets are opaque

`GTIDStatus.Executed`, `GTIDStatus.Purged`, `ChannelStatus.RetrievedGTIDSet`,
`ChannelStatus.ExecutedGTIDSet`, and `BinaryLogStatus.ExecutedGTIDSet` are
returned as strings, exactly as the server sent them. The library never parses
one, and neither should you without knowing what you are taking on: from MySQL
8.4 a set may contain **tagged** GTIDs in a three-part `UUID:TAG:NUMBER` form
alongside the original two-part `UUID:NUMBER`, and a parser written to the
two-part shape mis-reads them. The manual does not even agree with itself about
the maximum tag length ([COMPAT.md](COMPAT.md) entry 21).

Comparing sets is a server-side operation. If you need "has the replica
executed this set?", ask the server — `WAIT_FOR_EXECUTED_GTID_SET` and
`GTID_SUBSET` exist for it. If you only need to know whether a particular
transaction identifier appears, a substring test is the most you should do.

## What `SECONDS_BEHIND_SOURCE_WITHIN` bounds — and what it does not

The check's ID names the server's own column deliberately. It bounds
`Seconds_Behind_Source`, the server's **reported estimate**, and nothing
stronger. The manuals for all three supported versions document that estimate's
limits:

- it can read `0` across a connection the receiver has not yet noticed is
  broken, for as long as `replica_net_timeout`;
- it can oscillate between `0` and a large value while the receiver queues an
  old-timestamped event;
- `SHOW REPLICA STATUS` is nonblocking, so a snapshot taken during a
  `STOP REPLICA` may not be the latest state.

A multithreaded applier's low-water-mark position is a property of
`Exec_source_log_pos`, which this check does not read (refman §19.5.1.34,
"Replication and Transaction Inconsistencies").

Reading all three checks from a **single** snapshot removes any gap between
"configured?", "running?", and "lagging?" — they cannot disagree about the
state they describe. It does not prove freshness, and it is not a hard lag
guarantee. If your job needs one, this check is not it; build the guarantee on
top, from your own written-and-read marker or a GTID wait.

A `NULL` estimate fails the check. `NULL` means the estimate is unknowable
because a replication thread is stopped, and a job gating on lag must not
proceed on an unknowable number.

## The checks

Pure functions over facts: `[]Finding`, no error return, stable IDs, no
severity.

| ID | Input | Finding when | Rationale |
|---|---|---|---|
| `BINARY_LOG_ENABLED` | `bool` | binary logging is off | a server without binary logging cannot serve as a replication source and supports no binlog-based capture or point-in-time recovery |
| `REPLICATION_CONFIGURED` | `[]ChannelStatus` | zero channels | a consumer expecting to monitor replication here is pointed at a server that has none |
| `REPLICATION_CHANNELS_RUNNING` | `[]ChannelStatus` | any channel whose receiver or applier is not `Yes` | a stopped channel accumulates lag and drift silently; the finding carries that channel's last errors, which may help diagnose the stop — the server records none for a deliberate stop, and `Connecting` is a failing state that may carry no error |
| `SECONDS_BEHIND_SOURCE_WITHIN` | `[]ChannelStatus`, your `maxSeconds` | any channel whose reported estimate is `NULL` or above your bound | the server's reported lag beyond your bound means the replica trails the source; `NULL` means the estimate is unknowable because a thread is stopped |
| `GTID_MODE_ON` | `GTIDStatus` | mode is not `ON` | consumers that coordinate by GTID — resume, failover, CDC positioning — need `ON`; in other modes the GTID sets describe only part of the write history |

`Catalog()` returns all five sorted by ID, and `LookupCheck(id)` looks one up
by exact ID — never case-folded.

**The checks fail closed.** A check passes only on the exact proven value:
`"Yes"` for a running thread, `"ON"` for GTID mode. Every other string the
server might send — including values that do not exist today — produces a
finding. A future server state becomes something you can see, never a silent
pass.

Two consequences worth knowing:

- A channel reporting `Connecting` produces a `REPLICATION_CHANNELS_RUNNING`
  finding. That is deliberate: reconnecting is not running.
- `maxSeconds` is never clamped and never defaulted. A negative bound produces
  a finding for every channel you supplied, because no reported estimate can
  satisfy it. A channel reporting exactly `maxSeconds` passes.

A stopped applier fires `REPLICATION_CHANNELS_RUNNING` *and*
`SECONDS_BEHIND_SOURCE_WITHIN`, each under its own ID, so you can tell
"lagging" from "stopped".

## Consuming from a job loop

The intended shape: **one round trip per tick, then three pure checks over that
single snapshot.**

```go
// gate runs the three replica-side checks over one snapshot. Their order is
// cosmetic, since every finding carries its own ID; that all three run is
// not — see below.
func gate(channels []replication.ChannelStatus, max int64) []replication.Finding {
    findings := replication.CheckReplicationConfigured(channels)
    findings = append(findings, replication.CheckReplicationChannelsRunning(channels)...)
    findings = append(findings, replication.CheckSecondsBehindSourceWithin(channels, max)...)

    return findings
}

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-ticker.C:
    }

    channels, err := insp.ReplicaStatus(ctx)
    if err != nil {
        // Fail closed. A fact you could not read is not evidence of a healthy
        // replica, and treating the error as "no findings" is how a monitoring
        // loop goes quiet at the worst moment.
        if !handleUnknownReplicationState(err) {
            return err
        }

        continue
    }

    if findings := gate(channels, 30); len(findings) > 0 {
        // Whether this pauses the batch, aborts the run, or only annotates a
        // log line is your policy — the library has no opinion and ships no
        // severity to inherit one from.
        pauseWork(findings)

        continue
    }

    doOneBatch(ctx)
}
```

Three things about this shape are load-bearing:

- **`REPLICATION_CONFIGURED` runs first, and always.** Without it, a server
  with no replication at all passes the gate silently: the other two checks
  have nothing to iterate and return nothing. An empty snapshot must fail the
  gate, never pass it.
- **If you filter to a named channel, run the gate on the filtered slice.** A
  filter that matches nothing yields an empty slice, and `REPLICATION_CONFIGURED`
  then fails the gate exactly as it would for an unconfigured server. That is
  what turns a channel-name typo into a visible failure instead of a silent
  all-clear.
- **A fact error is fail-closed, and the response is yours.** The library
  distinguishes "I could not look" from "I looked and all is well"; what you do
  about the first — retry, pause, abort — is policy it deliberately leaves to
  you.

Source-side gates compose the same way over `BinaryLogEnabled` and
`GTIDStatus`:

```go
enabled, err := insp.BinaryLogEnabled(ctx)
if err != nil {
    return err
}
gtid, err := insp.GTIDStatus(ctx)
if err != nil {
    return err
}

findings := replication.CheckBinaryLogEnabled(enabled)
findings = append(findings, replication.CheckGTIDModeOn(gtid)...)
```

## Findings

```go
type Finding struct {
    Check    string   // stable ID, e.g. "REPLICATION_CHANNELS_RUNNING"
    Message  string   // human-readable, including the rationale
    Channels []string // server spellings; nil (JSON null) for server-scoped checks
    Facts    any      // typed payload — never parse the message
}
```

Branch on `Check` and read `Facts`. Message text is for humans and is not part
of the compatibility contract. `Channels` is this package's analog of
`validations.Finding.Tables`, and the default channel appears in it as the
empty string. A server-scoped finding carries `Channels` nil, which encodes as
`null`, not `[]`.

`Facts` carries the whole fact that produced the finding — for the two
channel-scoped checks, that channel's complete `ChannelStatus`, last errors
included. A deliberate `STOP REPLICA SQL_THREAD` records **no** error, so
`LastSQLErrno 0` with an empty `LastSQLError` is the server's documented "no
error", not a decoding gap.

## Errors versus findings

The distinction is strict:

- A **finding** describes replication state. A stopped applier is a finding.
- An **error** describes the inspection failing. An unreachable server, a
  missing privilege, or an undecodable column is an error.

Fact methods return `(fact, error)`; checks return `[]Finding` and no error —
they inspect nothing, so there is nothing for them to fail at.

Every fact error is a `*replication.OpError` naming the operation, and where
the failure is attributable, the channel and the column:

```go
var opErr *replication.OpError
if errors.As(err, &opErr) {
    log.Printf("replication fact %q failed on channel %q column %q: %v",
        opErr.Op, opErr.Channel, opErr.Column, opErr.Unwrap())
}
```

`BinaryLogStatus` is the one fact that may issue two statements, because the
statement's name differs across the supported range. When both fail, the
returned error carries **both** causes, each named by the statement that
produced it, and `errors.Is` and `errors.As` reach either one.
The rendered message separates the two with `; ` rather than a newline, each
cause's own text kept as the driver produced it. A primary failure caused by
the context — cancellation or an expired deadline — is returned alone; the
fallback is not attempted, since it could not answer either. On MySQL 8.0 every
successful call is two round trips, because the primary spelling fails with a
parse error there ([COMPAT.md](COMPAT.md) entry 20).

The library never panics and never logs.

## Concurrency

Thread-safety is documented on every exported type. `Inspector` is immutable
and safe for concurrent use when the `Querier` you supply is safe for concurrent
use. Checks are pure and safe for concurrent use provided you do not mutate the
slices you pass them. Mutable slices in returned facts and findings require the
usual caller synchronization.

## MySQL versions

Supported floor is MySQL 8.0.40; tested against 8.0, 8.4, and 9.7 on a live
source-replica topology. The package issues one spelling of every statement and
variable it reads, valid across the whole range, with exactly one exception:
the source-status statement, which genuinely differs and which the library
bridges for you.

Version-specific behavior the library absorbs on your behalf is catalogued in
[COMPAT.md](COMPAT.md) — entries 6 and 20 through 23 cover this package, and
they are worth reading before you debug a surprising result.
