package replication

import (
	"context"
	"database/sql"
	"errors"
)

// sqlReplicaStatus reads every replication channel this server applies. The
// statement spelling exists on every supported version (docs/COMPAT.md entry
// 6), so no version branch is involved.
const sqlReplicaStatus = "SHOW REPLICA STATUS"

// Promised columns of SHOW REPLICA STATUS. Columns the server reports beyond
// these are ignored, so a future server version, or one started with
// --show-replica-auth-info, does not break the fact.
const (
	colChannelName         = "Channel_Name"
	colReplicaIORunning    = "Replica_IO_Running"
	colReplicaSQLRunning   = "Replica_SQL_Running"
	colSecondsBehindSource = "Seconds_Behind_Source"
	colLastIOErrno         = "Last_IO_Errno"
	colLastIOError         = "Last_IO_Error"
	colLastSQLErrno        = "Last_SQL_Errno"
	colLastSQLError        = "Last_SQL_Error"
	colRetrievedGTIDSet    = "Retrieved_Gtid_Set"
	colExecutedGTIDSet     = "Executed_Gtid_Set"
	colSourceHost          = "Source_Host"
	colSourcePort          = "Source_Port"
)

// errMissingColumn means the server's result set omitted a column this fact
// promises to report. The fact fails rather than reporting a zero value that
// would be indistinguishable from a real one.
var errMissingColumn = errors.New("promised column missing from result set")

// ChannelStatus reports one replication channel's state, as the replica sees
// it. Every string field carries the server's own spelling, unnormalized.
//
// ChannelStatus is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type ChannelStatus struct {
	// ChannelName is the channel's name in the server's spelling. The empty
	// name is the default channel, which is a name rather than an absence.
	ChannelName string `json:"channel_name"`
	// IORunning is the raw Replica_IO_Running value: Yes, No, or Connecting.
	// It is reported unnormalized, so Connecting stays distinguishable from
	// both running and stopped.
	IORunning string `json:"io_running"`
	// SQLRunning is the raw Replica_SQL_Running value: Yes or No.
	SQLRunning string `json:"sql_running"`
	// SecondsBehindSource is the server's own lag estimate. Valid is false if
	// and only if the server sent SQL NULL; an undecodable value is reported
	// as an error instead, never as an invalid value. The JSON shape is Go's
	// sql.NullInt64 encoding, an object with Int64 and Valid members.
	SecondsBehindSource sql.NullInt64 `json:"seconds_behind_source"`
	// LastIOErrno is the receiver thread's last error number. Zero with an
	// empty LastIOError is the server's documented no-error state, which is
	// also what a deliberately stopped channel reports.
	LastIOErrno int `json:"last_io_errno"`
	// LastIOError is the receiver thread's last error message.
	LastIOError string `json:"last_io_error"`
	// LastSQLErrno is the applier thread's last error number, under the same
	// no-error rule as LastIOErrno.
	LastSQLErrno int `json:"last_sql_errno"`
	// LastSQLError is the applier thread's last error message.
	LastSQLError string `json:"last_sql_error"`
	// RetrievedGTIDSet is an opaque GTID set; the library never parses one
	// (docs/COMPAT.md entry 21).
	RetrievedGTIDSet string `json:"retrieved_gtid_set"`
	// ExecutedGTIDSet is an opaque GTID set under the same rule.
	ExecutedGTIDSet string `json:"executed_gtid_set"`
	// SourceHost is the host this channel replicates from.
	SourceHost string `json:"source_host"`
	// SourcePort is the port this channel replicates from.
	SourcePort uint16 `json:"source_port"`
}

// ReplicaStatus reports one ChannelStatus per replication channel, in the
// order the server returned them. A server that is not a replica returns an
// empty slice and no error: having no channels is an answer, not a failure.
//
// The returned slice is owned by the caller and built fresh per call.
//
// ReplicaStatus is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) ReplicaStatus(ctx context.Context) ([]ChannelStatus, error) {
	if err := i.validate(opReplicaStatus); err != nil {
		return nil, err
	}

	rows, err := i.q.QueryContext(ctx, sqlReplicaStatus)
	if err != nil {
		return nil, newOpError(opReplicaStatus, "", "", err)
	}

	return parseReplicaStatus(rows)
}

// parseReplicaStatus owns the row lifecycle: the rows are always closed, and
// the error reported is the first of scan, iteration, and close failures.
func parseReplicaStatus(rows *sql.Rows) (channels []ChannelStatus, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			err = newOpError(opReplicaStatus, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opReplicaStatus, rows, promisedReplicaStatusColumns())
	if err != nil {
		return nil, err
	}

	values, targets := index.scanTargets()
	channels = []ChannelStatus{}

	for rows.Next() {
		if scanErr := rows.Scan(targets...); scanErr != nil {
			return nil, newOpError(opReplicaStatus, "", "", scanErr)
		}

		channel, decodeErr := decodeChannelStatus(index, values)
		if decodeErr != nil {
			return nil, decodeErr
		}

		channels = append(channels, channel)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, newOpError(opReplicaStatus, "", "", iterErr)
	}

	return channels, nil
}

func promisedReplicaStatusColumns() []string {
	return []string{
		colChannelName,
		colReplicaIORunning,
		colReplicaSQLRunning,
		colSecondsBehindSource,
		colLastIOErrno,
		colLastIOError,
		colLastSQLErrno,
		colLastSQLError,
		colRetrievedGTIDSet,
		colExecutedGTIDSet,
		colSourceHost,
		colSourcePort,
	}
}

// decodeChannelStatus decodes one row. Channel_Name decodes first so that
// every later failure in the row names the channel it belongs to; if the name
// itself cannot be decoded, the error carries no channel rather than a guess.
func decodeChannelStatus(index columnIndex, values []any) (ChannelStatus, error) {
	row := rowDecoder{op: opReplicaStatus, index: index, values: values}

	var channel ChannelStatus
	if err := decodeColumn(row, colChannelName, decodeString, &channel.ChannelName); err != nil {
		return ChannelStatus{}, err
	}
	row.channel = channel.ChannelName

	if err := decodeColumn(row, colReplicaIORunning, decodeString, &channel.IORunning); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colReplicaSQLRunning, decodeString, &channel.SQLRunning); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(
		row, colSecondsBehindSource, decodeNullSeconds, &channel.SecondsBehindSource,
	); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colLastIOErrno, decodeInt, &channel.LastIOErrno); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colLastIOError, decodeString, &channel.LastIOError); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colLastSQLErrno, decodeInt, &channel.LastSQLErrno); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colLastSQLError, decodeString, &channel.LastSQLError); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(
		row, colRetrievedGTIDSet, decodeString, &channel.RetrievedGTIDSet,
	); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colExecutedGTIDSet, decodeString, &channel.ExecutedGTIDSet); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colSourceHost, decodeString, &channel.SourceHost); err != nil {
		return ChannelStatus{}, err
	}
	if err := decodeColumn(row, colSourcePort, decodeUint16, &channel.SourcePort); err != nil {
		return ChannelStatus{}, err
	}

	return channel, nil
}

// columnIndex locates a fact's promised columns within one result set, so that
// a column is read by name rather than by a position the server is free to
// change.
type columnIndex struct {
	positions map[string]int
	width     int
}

// indexColumns reads the result set's column names once and proves every
// promised column is present before any row is decoded. Extra columns are
// ignored; a missing promised column fails the fact and names itself.
func indexColumns(op string, rows *sql.Rows, promised []string) (columnIndex, error) {
	names, err := rows.Columns()
	if err != nil {
		return columnIndex{}, newOpError(op, "", "", err)
	}

	positions := make(map[string]int, len(names))
	for position, name := range names {
		if _, seen := positions[name]; !seen {
			positions[name] = position
		}
	}

	for _, name := range promised {
		if _, ok := positions[name]; !ok {
			return columnIndex{}, newOpError(op, "", name, errMissingColumn)
		}
	}

	return columnIndex{positions: positions, width: len(names)}, nil
}

// scanTargets returns a row buffer and the pointers Scan writes through. The
// buffer spans every column the server sent, including unknown ones, because
// Scan requires one destination per column.
func (index columnIndex) scanTargets() (values, targets []any) {
	values = make([]any, index.width)
	targets = make([]any, index.width)
	for position := range values {
		targets[position] = &values[position]
	}

	return values, targets
}

// rowDecoder decodes promised columns out of one scanned row, attributing any
// failure to the operation, channel, and column responsible for it.
type rowDecoder struct {
	op      string
	channel string
	index   columnIndex
	values  []any
}

func decodeColumn[T any](
	row rowDecoder,
	column string,
	decode func(any) (T, error),
	target *T,
) error {
	decoded, err := decode(row.values[row.index.positions[column]])
	if err != nil {
		return newOpError(row.op, row.channel, column, err)
	}
	*target = decoded

	return nil
}
