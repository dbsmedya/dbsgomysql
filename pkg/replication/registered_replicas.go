package replication

import (
	"context"
	"database/sql"
)

// sqlRegisteredReplicas lists the replicas registered with this source. The
// statement exists from MySQL 8.0.22, so it is available on every supported
// version. It requires REPLICATION SLAVE, which differs from the REPLICATION
// CLIENT the status facts need (docs/COMPAT.md entry 22).
const sqlRegisteredReplicas = "SHOW REPLICAS"

// Promised columns of SHOW REPLICAS, spelled as the server spells them —
// verified live on MySQL 8.0.46, 8.4.9, and 9.7.1. The manual's own example
// output prints Server_id and Source_id with a lowercase id; no supported
// server does (docs/COMPAT.md entry 22). The two identity columns carry the
// Reg prefix to keep them apart from the SHOW REPLICA STATUS columns, whose
// names follow a different convention.
const (
	colRegServerID = "Server_Id"
	colHost        = "Host"
	colPort        = "Port"
	colRegSourceID = "Source_Id"
	colReplicaUUID = "Replica_UUID"
)

// RegisteredReplica is one replica as the source knows it — which is to say,
// as that replica described itself when it registered.
//
// RegisteredReplica is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type RegisteredReplica struct {
	// ServerID is the replica's server_id.
	ServerID uint32 `json:"server_id"`
	// Host is the replica's self-reported report_host, not the address the
	// connection came from. The server reports it this way deliberately,
	// because a socket peer address may not be usable to reach the replica.
	// Nothing verifies that the value resolves or is reachable.
	//
	// Host may be empty for a listed replica: a replica started without
	// report_host still registers, and the source then knows it exists
	// without knowing where to reach it. An empty Host is therefore data the
	// server returned, never a row to discard.
	Host string `json:"host"`
	// Port is the port the replica reported. An unset report_port normally
	// yields the replica's actual listening port rather than zero, so zero
	// means only that the server returned zero — never that report_port was
	// unset. Either way it is a legitimate value, not a decode failure.
	Port uint16 `json:"port"`
	// SourceID is the server_id of the source this replica registered with.
	SourceID uint32 `json:"source_id"`
	// ReplicaUUID is the replica's server UUID.
	ReplicaUUID string `json:"replica_uuid"`
}

// RegisteredReplicas reports the replicas registered with this server, in the
// order the server returned them.
//
// # This list is never proof of absence
//
// An empty slice does not mean no replicas exist, and a non-empty one is not a
// list of currently connected replicas. These limits apply, none of them errors
// (docs/COMPAT.md entry 22):
//
//   - A replica that has never connected leaves no row. The source cannot
//     distinguish "no replica exists" from "one exists and has not reached me
//     yet", so the list is a registration history rather than a topology.
//   - The rows cover replicas that are or have been connected, so an entry may
//     be stale — the replica may have disconnected long ago.
//   - Host is self-reported by each replica; the source does not verify it, and
//     it may be empty for a replica that registered without report_host.
//   - Port is self-reported and unverified. An unset report_port normally yields
//     the replica's actual listening port; zero means only that the server
//     returned zero.
//
// Callers must therefore never read an empty result as "this server has no
// replicas", nor a returned row as "this replica is connected now". The fact
// answers what the source was told, which is the only question the server can
// answer; no Performance Schema table answers the stronger one for
// asynchronous replication.
//
// The returned slice is owned by the caller and built fresh per call.
//
// RegisteredReplicas is safe for concurrent use when the Inspector's Querier
// is.
func (i *Inspector) RegisteredReplicas(ctx context.Context) ([]RegisteredReplica, error) {
	if err := i.validate(opRegisteredReplicas); err != nil {
		return nil, err
	}

	rows, err := i.q.QueryContext(ctx, sqlRegisteredReplicas)
	if err != nil {
		return nil, newOpError(opRegisteredReplicas, "", "", err)
	}

	return parseRegisteredReplicas(rows)
}

// parseRegisteredReplicas owns the row lifecycle: the rows are always closed,
// and the error reported is the first of scan, iteration, and close failures.
func parseRegisteredReplicas(rows *sql.Rows) (replicas []RegisteredReplica, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			replicas = nil
			err = newOpError(opRegisteredReplicas, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opRegisteredReplicas, rows, promisedRegisteredReplicasColumns())
	if err != nil {
		return nil, err
	}

	values, targets := index.scanTargets()
	replicas = []RegisteredReplica{}

	for rows.Next() {
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return nil, newOpError(opRegisteredReplicas, "", "", scanErr)
		}

		replica, decodeErr := decodeRegisteredReplica(index, values)
		if decodeErr != nil {
			return nil, decodeErr
		}

		replicas = append(replicas, replica)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, newOpError(opRegisteredReplicas, "", "", iterErr)
	}

	return replicas, nil
}

func promisedRegisteredReplicasColumns() []string {
	return []string{
		colRegServerID,
		colHost,
		colPort,
		colRegSourceID,
		colReplicaUUID,
	}
}

func decodeRegisteredReplica(index columnIndex, values []any) (RegisteredReplica, error) {
	row := rowDecoder{op: opRegisteredReplicas, index: index, values: values}

	var replica RegisteredReplica
	if err := decodeColumn(row, colRegServerID, decodeUint32, &replica.ServerID); err != nil {
		return RegisteredReplica{}, err
	}
	if err := decodeColumn(row, colHost, decodeString, &replica.Host); err != nil {
		return RegisteredReplica{}, err
	}
	if err := decodeColumn(row, colPort, decodeUint16, &replica.Port); err != nil {
		return RegisteredReplica{}, err
	}
	if err := decodeColumn(row, colRegSourceID, decodeUint32, &replica.SourceID); err != nil {
		return RegisteredReplica{}, err
	}
	if err := decodeColumn(row, colReplicaUUID, decodeString, &replica.ReplicaUUID); err != nil {
		return RegisteredReplica{}, err
	}

	return replica, nil
}
