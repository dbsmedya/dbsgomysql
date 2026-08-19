package replication

import "context"

// SQL read by the variable facts. Each fact issues exactly one statement, and
// the text is part of the package's contract: a consumer auditing a server's
// query log sees these statements and no others.
const (
	sqlBinaryLogEnabled  = "SELECT @@GLOBAL.log_bin"
	sqlGTIDStatus        = "SELECT @@GLOBAL.gtid_mode, @@GLOBAL.gtid_executed, @@GLOBAL.gtid_purged"
	sqlReplicationConfig = "SELECT @@GLOBAL.read_only, @@GLOBAL.super_read_only, " +
		"@@GLOBAL.server_id, @@GLOBAL.log_replica_updates, @@GLOBAL.replica_parallel_workers"
)

// Result-column names, used to attribute a decode failure to the variable that
// caused it.
const (
	varLogBin                 = "@@GLOBAL.log_bin"
	varGTIDMode               = "@@GLOBAL.gtid_mode"
	varGTIDExecuted           = "@@GLOBAL.gtid_executed"
	varGTIDPurged             = "@@GLOBAL.gtid_purged"
	varReadOnly               = "@@GLOBAL.read_only"
	varSuperReadOnly          = "@@GLOBAL.super_read_only"
	varServerID               = "@@GLOBAL.server_id"
	varLogReplicaUpdates      = "@@GLOBAL.log_replica_updates"
	varReplicaParallelWorkers = "@@GLOBAL.replica_parallel_workers"
)

// GTIDStatus reports the server's GTID mode and its two GTID sets.
//
// GTIDStatus is a value with no reference fields: copying it copies the whole
// fact, and it is safe for concurrent use.
type GTIDStatus struct {
	// Mode is the raw server value of gtid_mode: OFF, OFF_PERMISSIVE,
	// ON_PERMISSIVE, or ON. It is reported as the server spells it and is
	// never normalized, so an unfamiliar value reaches the consumer intact.
	Mode string `json:"mode"`
	// Executed is gtid_executed, an opaque set. The library never parses a
	// GTID set: from MySQL 8.4 a set may contain three-part UUID:TAG:NUMBER
	// entries alongside the original two-part form, and the manual does not
	// settle the tag's maximum length (docs/COMPAT.md entry 21). Interpreting
	// or comparing sets is the consumer's affair.
	Executed string `json:"executed"`
	// Purged is gtid_purged, an opaque set under the same rule as Executed.
	Purged string `json:"purged"`
}

// Config reports the server-scoped variables that decide whether a server can
// act as a source or a replica. The Inspector is already server-scoped, so the
// type needs no further qualification: consumers write replication.Config.
//
// Config is a value with no reference fields: copying it copies the whole
// fact, and it is safe for concurrent use.
type Config struct {
	// ReadOnly is read_only. On a replica its value is independent of the
	// source's.
	ReadOnly bool `json:"read_only"`
	// SuperReadOnly is super_read_only.
	SuperReadOnly bool `json:"super_read_only"`
	// ServerID is server_id, the identity a replica registers with a source.
	ServerID uint32 `json:"server_id"`
	// LogReplicaUpdates is log_replica_updates, which decides whether a
	// replica writes replicated updates to its own binary log.
	LogReplicaUpdates bool `json:"log_replica_updates"`
	// ReplicaParallelWorkers is replica_parallel_workers, the applier's worker
	// count. Zero means the single-threaded applier, which is reachable only
	// on MySQL 8.x within the supported range: from 9.3.0 the minimum is 1, so
	// a consumer that branches on zero carries a dead branch on 9.x
	// (docs/COMPAT.md entry 23).
	ReplicaParallelWorkers int `json:"replica_parallel_workers"`
}

// BinaryLogEnabled reports whether the server writes a binary log, by reading
// log_bin. A server with binary logging disabled can be neither a source nor a
// replica that logs its own updates.
//
// BinaryLogEnabled is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) BinaryLogEnabled(ctx context.Context) (bool, error) {
	if err := i.validate(opBinaryLogEnabled); err != nil {
		return false, err
	}

	var rawLogBin any
	if err := i.q.QueryRowContext(ctx, sqlBinaryLogEnabled).Scan(&rawLogBin); err != nil {
		return false, newOpError(opBinaryLogEnabled, "", "", err)
	}

	var enabled bool
	if err := decodeField(opBinaryLogEnabled, varLogBin, rawLogBin, decodeBool, &enabled); err != nil {
		return false, err
	}

	return enabled, nil
}

// GTIDStatus reports the server's GTID mode and sets. The sets are returned as
// opaque strings; see GTIDStatus.Executed for why the library never parses one.
//
// GTIDStatus is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) GTIDStatus(ctx context.Context) (GTIDStatus, error) {
	if err := i.validate(opGTIDStatus); err != nil {
		return GTIDStatus{}, err
	}

	var rawMode, rawExecuted, rawPurged any
	row := i.q.QueryRowContext(ctx, sqlGTIDStatus)
	if err := row.Scan(&rawMode, &rawExecuted, &rawPurged); err != nil {
		return GTIDStatus{}, newOpError(opGTIDStatus, "", "", err)
	}

	var status GTIDStatus
	if err := decodeField(opGTIDStatus, varGTIDMode, rawMode, decodeString, &status.Mode); err != nil {
		return GTIDStatus{}, err
	}
	if err := decodeField(opGTIDStatus, varGTIDExecuted, rawExecuted, decodeString, &status.Executed); err != nil {
		return GTIDStatus{}, err
	}
	if err := decodeField(opGTIDStatus, varGTIDPurged, rawPurged, decodeString, &status.Purged); err != nil {
		return GTIDStatus{}, err
	}

	return status, nil
}

// ReplicationConfig reports the server-scoped replication variables. It reads
// only the spellings valid across every supported version, so no version
// branch is involved (docs/COMPAT.md entry 23).
//
// ReplicationConfig is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) ReplicationConfig(ctx context.Context) (Config, error) {
	if err := i.validate(opReplicationConfig); err != nil {
		return Config{}, err
	}

	var rawReadOnly, rawSuperReadOnly, rawServerID, rawLogReplicaUpdates, rawWorkers any
	row := i.q.QueryRowContext(ctx, sqlReplicationConfig)
	err := row.Scan(
		&rawReadOnly,
		&rawSuperReadOnly,
		&rawServerID,
		&rawLogReplicaUpdates,
		&rawWorkers,
	)
	if err != nil {
		return Config{}, newOpError(opReplicationConfig, "", "", err)
	}

	var config Config
	if err := decodeField(
		opReplicationConfig, varReadOnly, rawReadOnly, decodeBool, &config.ReadOnly,
	); err != nil {
		return Config{}, err
	}
	if err := decodeField(
		opReplicationConfig, varSuperReadOnly, rawSuperReadOnly, decodeBool, &config.SuperReadOnly,
	); err != nil {
		return Config{}, err
	}
	if err := decodeField(
		opReplicationConfig, varServerID, rawServerID, decodeUint32, &config.ServerID,
	); err != nil {
		return Config{}, err
	}
	if err := decodeField(
		opReplicationConfig, varLogReplicaUpdates, rawLogReplicaUpdates, decodeBool,
		&config.LogReplicaUpdates,
	); err != nil {
		return Config{}, err
	}
	if err := decodeField(
		opReplicationConfig, varReplicaParallelWorkers, rawWorkers, decodeInt,
		&config.ReplicaParallelWorkers,
	); err != nil {
		return Config{}, err
	}

	return config, nil
}

// decodeField decodes one result column into target, attributing any failure
// to the column that caused it. The column is named by the error, never
// guessed by the caller reading a zero value.
func decodeField[T any](
	op, column string,
	raw any,
	decode func(any) (T, error),
	target *T,
) error {
	decoded, err := decode(raw)
	if err != nil {
		return newOpError(op, "", column, err)
	}
	*target = decoded

	return nil
}
