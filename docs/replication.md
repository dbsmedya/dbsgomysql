# pkg/replication — Consumer Guide

> **Status:** nine facts and all nine catalog checks are implemented. Track
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

Every `SELECT` the library issues ends with its own `LIMIT`, so a
`sql_select_limit` inherited from the server's global value or set on the
session cannot truncate a fact. The `SHOW` statements it issues were measured
unaffected by the variable; `SHOW VARIABLES` is not, which is why the group
name is not read with it (see [COMPAT.md](COMPAT.md) entry 35). Your own
queries on the same connection are still capped by it.

## The facts

Every call takes a `context.Context` first and returns `(fact, error)`.

| Fact | Reads | Privilege | Answers |
|---|---|---|---|
| `ReplicaStatus(ctx)` | `SHOW REPLICA STATUS` | `REPLICATION CLIENT` | one `ChannelStatus` per asynchronous replication channel |
| `BinaryLogEnabled(ctx)` | `@@GLOBAL.log_bin` | — | is this server writing a binary log? |
| `BinaryLogStatus(ctx)` | `SHOW BINARY LOG STATUS`, or `SHOW MASTER STATUS` on 8.0 | `REPLICATION CLIENT` | the current binary log coordinates |
| `GTIDStatus(ctx)` | `@@GLOBAL.gtid_mode`, `gtid_executed`, `gtid_purged` | — | GTID mode and both sets |
| `ReplicationConfig(ctx)` | five `@@GLOBAL` variables | — | read-only state, server id, applier configuration |
| `RegisteredReplicas(ctx)` | `SHOW REPLICAS` | **`REPLICATION SLAVE`** | which replicas have registered with this source |
| `GroupReplication(ctx)` | `information_schema.PLUGINS`, then `performance_schema.global_variables` | — | is the Group Replication plugin active, and which group name is configured? |
| `GroupMembers(ctx)` | `performance_schema.replication_group_members` | **`SELECT` on that table** | this member's view of its group: one `GroupMember` per row, normally one per member |
| `GroupMemberStats(ctx)` | `performance_schema.replication_group_member_stats` | **`SELECT` on that table** | the certification and applier counters this member holds, one `GroupMemberStats` per row, normally one per member |

The three variable facts read global system variables, and
`GroupReplication` needs no privilege beyond connecting either. The other
statement-backed facts carry a documented privilege requirement, and
`RegisteredReplicas` needs a *different* grant from `ReplicaStatus` and
`BinaryLogStatus`: `REPLICATION SLAVE`, not `REPLICATION CLIENT`.
`GroupMembers` and `GroupMemberStats` each need `SELECT` on their own
Performance Schema table, which neither of those grants gives. An account that
can read every status fact can still be refused there, and that refusal arrives
as an error, never as an empty list. `GroupReplication` reads Performance
Schema too, but only `global_variables`, which a USAGE-only account can read
([COMPAT.md](COMPAT.md) entry 30).

```go
channels, err := insp.ReplicaStatus(ctx)
enabled, err := insp.BinaryLogEnabled(ctx)
status, err := insp.BinaryLogStatus(ctx)
gtid, err := insp.GTIDStatus(ctx)
config, err := insp.ReplicationConfig(ctx)
replicas, err := insp.RegisteredReplicas(ctx)
gr, err := insp.GroupReplication(ctx)
members, err := insp.GroupMembers(ctx)
stats, err := insp.GroupMemberStats(ctx)
```

The configuration fact's type is `replication.Config`; the method that returns
it is `ReplicationConfig`.

Returned slices and pointers are yours: each call builds them fresh, so you can
mutate a result without affecting the next call.

### Channels

`ReplicaStatus` reads every channel `SHOW REPLICA STATUS` lists — it never
issues `FOR CHANNEL`. On a Group Replication member that list omits the
group's own channels; see [Group Replication](#group-replication).
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
| `ReplicaStatus` | **This server has no asynchronous replication channel.** That includes a Group Replication member that replicates nothing asynchronously ([COMPAT.md](COMPAT.md) entry 29). An empty slice is a real answer: a successful statement is authoritative, and an unprivileged account gets an error instead. |
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

## Group Replication

### Is this server configured for a group?

`GroupReplication` answers two questions with two statements, and needs no
privilege beyond connecting:

| Server state | `PluginStatus` | `GroupName` |
|---|---|---|
| plugin not installed | `""` | `""` |
| plugin installed but `DISABLED` | `"DISABLED"` | `""` |
| plugin `ACTIVE`, no group name set | `"ACTIVE"` | `""` |
| configured member, in any member state | `"ACTIVE"` | the group's UUID |

- **Absence is a state, never an error.** An error always means the read
  failed: transport, privilege, more than one row, a `NULL`, or an
  undecodable value. Both statements must succeed; you never get half a fact.
- **A `DISABLED` plugin hides the group-name variable** exactly as a missing
  plugin does, which is why the fact reads the plugin's own status
  ([COMPAT.md](COMPAT.md) entry 30). Never read
  `@@GLOBAL.group_replication_group_name` yourself: it raises error 1193
  whenever the variable is not registered.
- **The group name comes from `performance_schema.global_variables`,** which
  any account can read — unlike the two Performance Schema tables below — and
  which is populated even with Performance Schema disabled. It is not read
  with `SHOW GLOBAL VARIABLES`: a `sql_select_limit` of 0 empties that
  statement ([COMPAT.md](COMPAT.md) entry 35).
- **A non-empty `GroupName` proves configuration, not membership.** A server
  can carry a name and never have joined a running group. What that means for
  your job is your policy.
- **The two fields are not one snapshot.** Each reports what its own statement
  saw; a plugin installed between them can show `PluginStatus ""` with a name.
- Values are spelled as the server sent them; `PluginStatus` may be any
  documented status (`ACTIVE`, `INACTIVE`, `DISABLED`, `DELETING`, `DELETED`)
  or one this release does not know.

### Who is in the group, and how far behind are they?

`GroupMembers` reads this member's view of its group, and `GroupMemberStats`
the certification and applier counters it holds for each member. Each needs
`SELECT` on its own Performance Schema table; `USAGE` alone is refused with
error 1142, and that refusal is an error, never an empty slice. (The
`global_variables` table `GroupReplication` reads is the exception: it serves
`SHOW VARIABLES`, which needs no privilege.)

What the two tables hold depends on the server's state, and not always as the
manual says ([COMPAT.md](COMPAT.md) entries 31 and 32):

| Server state | `GroupMembers` | `GroupMemberStats` |
|---|---|---|
| plugin not installed, or `DISABLED` | empty | empty |
| plugin `ACTIVE`, Group Replication never started — 8.0, 8.4 | one `OFFLINE` row with no member ID, host, role, version or stack, and an invalid `Port`; `Self` false | one row of zeros with no view or member ID; `Self` false |
| the same on 9.7 | one `OFFLINE` row identifying the server; no role; `Self` true | as on 8.x: anonymous, `Self` false |
| stopped after participating | one `OFFLINE` row of its own; no role; `Self` true | one row of zeros of its own, no view ID; `Self` true |
| member of a running group | one row per member in this member's view | one row per member, refreshed periodically |

- **The view is this member's, never the group's configured size.** A peer
  that leaves disappears from the others' view rather than being listed
  `OFFLINE`, and a member cut off from the group reports the others
  `UNREACHABLE` rather than guessing.
- **`Self` is computed by the server** (`MEMBER_ID = @@server_uuid`). On 9.7 a
  never-started server is `Self` in the members table and anonymous in the
  stats table, so the two facts can disagree about the same server.
- **Zero counters are not a healthy backlog.** A server that is not in a
  running group reports a row of zeros. Read the counters only alongside a
  `GroupMembers` snapshot that shows the member `ONLINE`.
- **A remote member's counters are periodic copies**, refreshed on the
  group's flow-control period, so they can differ slightly from that
  member's own.
- `Port` follows `SecondsBehindSource`'s rule: invalid means only that the
  server sent `NULL`. Values are spelled as sent: `Version` omits a
  distribution's build suffix (`8.0.46`, not `8.0.46-37`), and
  `CommunicationStack` reads `MySQL` or `XCom`.

### Gating on a group

The four Group Replication checks compose the way the asynchronous ones do,
with one rule the checks cannot enforce for you:

```go
members, err := insp.GroupMembers(ctx)
if err != nil {
    return err // fail closed: a view you could not read is not a healthy group
}
stats, err := insp.GroupMemberStats(ctx)
if err != nil {
    return err
}

findings := replication.CheckGroupReplicationLocalMemberOnline(members)
findings = append(findings, replication.CheckGroupReplicationMembersOnline(members)...)
findings = append(findings, replication.CheckGroupReplicationApplierQueueWithin(stats, 100)...)
findings = append(findings, replication.CheckGroupReplicationCertifierQueueWithin(stats, 100)...)
if len(findings) > 0 {
    return gateFailed(findings) // act on findings first
}

// No findings: every member in this view is ONLINE, this server's own row
// among them. The checks still pass an empty stats slice, one missing a
// member, and a stopped member's own row of zeros. Require, for every member
// you gate on — this one included — a stats row with its MemberID and a
// non-empty ViewID, and treat anything else as unavailable data under your
// own policy (retry, pause). runningStatsRow, gateFailed and
// errNoRunningStats are yours to write.
for _, member := range members {
    if !runningStatsRow(stats, member.MemberID) {
        return errNoRunningStats(member.MemberID)
    }
}
```

- **`GROUP_REPLICATION_LOCAL_MEMBER_ONLINE` runs first, and always.** Without
  it, a server that is not a running member — no plugin, never started,
  stopped — passes: the other checks have nothing to iterate. It guards the
  members input only.
- **Act on findings before the coverage rule.** A stopped or never-started
  server has no running stats row either, so checking coverage first would
  report it as unavailable data to retry on, rather than as the finding that
  it is not a running member.
- **An own `ONLINE` row does not prove this member is in the majority.** An
  expelled member "sees itself as online and the other members as
  unreachable" (refman 8.4, "Responses to Failure Detection and Network
  Partitioning"), so `GROUP_REPLICATION_LOCAL_MEMBER_ONLINE` passes it;
  `GROUP_REPLICATION_MEMBERS_ONLINE` is what fires on those `UNREACHABLE`
  rows. Run both.
- **Members and stats are separate reads**, and the stats are periodic
  copies, so they are two observations, not one snapshot. A members read can
  show a member `ONLINE` a moment before the stats read finds it stopped —
  its own row then has an empty `ViewID` and zero counters, which the queue
  checks pass. That is why the coverage rule above lives in your gate rather
  than in a check, and why it asks for a `ViewID`. It still does not make the
  two reads one snapshot or remove the staleness of remote rows.
- **`GROUP_REPLICATION_MEMBERS_ONLINE` describes the supplied view only.** It
  cannot prove the group's configured size or its fault tolerance.

### The asynchronous facts on a group member

- `ReplicaStatus` on a member lists **no Group Replication channel** — not the
  applier channel, which the manual says is excluded, and not the recovery
  channel either, which the manual implies is listed. That holds while a
  member is `RECOVERING` with its recovery channel running
  ([COMPAT.md](COMPAT.md) entry 29). An ordinary asynchronous channel on a
  member is still listed.
- So on a member with no asynchronous channel `REPLICATION_CONFIGURED` fires,
  and the channel checks judge only asynchronous channels. To ask whether a
  server is a running group member, use
  `GROUP_REPLICATION_LOCAL_MEMBER_ONLINE` (see [Gating on a group](#gating-on-a-group)).
- **`RegisteredReplicas` on a donor lists a member recovering from it**, and
  can go on listing it for a while after it is `ONLINE` — until the donor's
  dump thread for that connection ends ([COMPAT.md](COMPAT.md) entry 33). This
  is the fact's registration-history contract at work. If you
  know the group's current members, dropping rows whose `ReplicaUUID` matches
  one of them is a reasonable filter for *known current peers* — but it is not
  a classifier: a match proves identity, not why the server registered or
  whether it is connected, and a non-match does not prove an asynchronous
  replica, because a former member can leave the group's view while its row is
  still listed.

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
| `REPLICATION_CONFIGURED` | `[]ChannelStatus` | zero channels | a consumer expecting to monitor asynchronous replication here is pointed at a server that has none; a Group Replication member's own channels are not counted |
| `REPLICATION_CHANNELS_RUNNING` | `[]ChannelStatus` | any channel whose receiver or applier is not `Yes` | a stopped channel accumulates lag and drift silently; the finding carries that channel's last errors, which may help diagnose the stop — the server records none for a deliberate stop, and `Connecting` is a failing state that may carry no error |
| `SECONDS_BEHIND_SOURCE_WITHIN` | `[]ChannelStatus`, your `maxSeconds` | any channel whose reported estimate is `NULL` or above your bound | the server's reported lag beyond your bound means the replica trails the source; `NULL` means the estimate is unknowable because a thread is stopped |
| `GTID_MODE_ON` | `GTIDStatus` | mode is not `ON` | consumers that coordinate by GTID — resume, failover, CDC positioning — need `ON`; in other modes the GTID sets describe only part of the write history |
| `GROUP_REPLICATION_LOCAL_MEMBER_ONLINE` | `[]GroupMember` | no row is `Self` with state exactly `ONLINE` | a member is only fully synchronized with the group, and participating in it, when it is `ONLINE`; a server with no row of its own, including one without an active plugin, fails rather than passes |
| `GROUP_REPLICATION_MEMBERS_ONLINE` | `[]GroupMember` | any member whose state is not exactly `ONLINE` | this view does not report that member `ONLINE`, the only fully synchronized, participating state; an `UNREACHABLE` row is a suspicion, and the queried member may be the one cut off |
| `GROUP_REPLICATION_APPLIER_QUEUE_WITHIN` | `[]GroupMemberStats`, your `maxTransactions` | any member whose applier queue is above your bound | an application backlog on that member — not an exact measure of how far its data trails the group |
| `GROUP_REPLICATION_CERTIFIER_QUEUE_WITHIN` | `[]GroupMemberStats`, your `maxTransactions` | any member whose certification queue is above your bound | a backlog of transactions waiting for conflict detection |

`Catalog()` returns all nine sorted by ID, and `LookupCheck(id)` looks one up
by exact ID — never case-folded. Each entry's `Phase` names the release that
delivered it: `v1.1.0` for the asynchronous checks, `v1.3.0` for the Group
Replication ones.

**The checks fail closed.** A check passes only on the exact proven value:
`"Yes"` for a running thread, `"ON"` for GTID mode, `"ONLINE"` for a group
member. Every other string the
server might send — including values that do not exist today — produces a
finding. A future server state becomes something you can see, never a silent
pass.

Two consequences worth knowing:

- A channel reporting `Connecting` produces a `REPLICATION_CHANNELS_RUNNING`
  finding. That is deliberate: reconnecting is not running.
- `maxSeconds` is never clamped and never defaulted. A negative bound produces
  a finding for every channel you supplied, because no reported estimate can
  satisfy it. A channel reporting exactly `maxSeconds` passes. The two queue
  checks' `maxTransactions` follows the same rules, and a queue above
  `math.MaxInt64` is compared exactly, never narrowed into a pass.

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
  with no asynchronous channel passes the gate silently: the other two checks
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

`GroupMembers` and `GroupMemberStats` rows all share one channel, so a decode
failure after a row's `MEMBER_ID` has decoded also names that member: the
cause begins `member <uuid>: `. `OpError` gains no field for it, and
`errors.Is` and `errors.As` still reach the underlying cause. A failure on an
anonymous row, or on `MEMBER_ID` itself, names no member.

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
