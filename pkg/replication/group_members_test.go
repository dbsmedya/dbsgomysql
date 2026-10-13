package replication

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

// The statement, spelled independently of the library's constant.
const (
	testSQLGroupMembers = "SELECT CHANNEL_NAME, MEMBER_ID, MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, " +
		"MEMBER_ROLE, MEMBER_VERSION, MEMBER_COMMUNICATION_STACK, " +
		"MEMBER_ID = @@server_uuid AS IS_SELF " +
		"FROM performance_schema.replication_group_members LIMIT 18446744073709551615"

	testMatchGroupMembers = "performance_schema.replication_group_members"
	testApplierChannel    = "group_replication_applier"
	testMemberA           = "11111111-1111-1111-1111-111111111111"
	testMemberB           = "22222222-2222-2222-2222-222222222222"
	testStateOnline       = "ONLINE"
	testStateOffline      = "OFFLINE"
)

func groupMembersColumns() []string {
	return []string{
		"CHANNEL_NAME", "MEMBER_ID", "MEMBER_HOST", "MEMBER_PORT", "MEMBER_STATE",
		"MEMBER_ROLE", "MEMBER_VERSION", "MEMBER_COMMUNICATION_STACK", "IS_SELF",
	}
}

// groupMemberRow builds one row aligned with groupMembersColumns, mixing the
// integer and text deliveries a driver may produce.
func groupMemberRow(id string, port driver.Value, state, role string, self driver.Value) []driver.Value {
	return []driver.Value{
		[]byte(testApplierChannel), []byte(id), []byte("gr-" + id[:1]), port, []byte(state),
		[]byte(role), []byte("8.4.11"), []byte("MySQL"), self,
	}
}

func scriptGroupMembers(t *testing.T, columns []string, rows [][]driver.Value) *sql.DB {
	t.Helper()

	db := testsupport.OpenScriptedDB(testsupport.ScriptedQuery{
		Match: testMatchGroupMembers, Columns: columns, Rows: rows,
	})
	t.Cleanup(func() { db.Close() })

	return db
}

// TestGroupMembersStates pins the shapes COMPAT entry 31 records, decoded
// through the fact.
func TestGroupMembersStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rows [][]driver.Value
		want []GroupMember
	}{
		{
			name: "no active plugin: no rows",
			rows: nil,
			want: []GroupMember{},
		},
		{
			name: "never started on 8.0 and 8.4: anonymous OFFLINE row, NULL port",
			rows: [][]driver.Value{{
				[]byte(testApplierChannel), []byte(""), []byte(""), nil, []byte(testStateOffline),
				[]byte(""), []byte(""), []byte(""), int64(0),
			}},
			want: []GroupMember{{ChannelName: testApplierChannel, State: testStateOffline}},
		},
		{
			name: "never started on 9.7: identified OFFLINE row",
			rows: [][]driver.Value{{
				[]byte(testApplierChannel), []byte(testMemberA), []byte("host-a"), int64(3306),
				[]byte(testStateOffline), []byte(""), []byte("9.7.1"), []byte("XCom"), []byte("1"),
			}},
			want: []GroupMember{{
				ChannelName: testApplierChannel, MemberID: testMemberA, Host: "host-a",
				Port: sql.NullInt64{Int64: 3306, Valid: true}, State: testStateOffline,
				Version: "9.7.1", CommunicationStack: "XCom", Self: true,
			}},
		},
		{
			name: "formed group: one row per member, server order kept",
			rows: [][]driver.Value{
				groupMemberRow(testMemberB, []byte("3306"), testStateOnline, "SECONDARY", int64(0)),
				groupMemberRow(testMemberA, int64(3306), testStateOnline, "PRIMARY", uint64(1)),
			},
			want: []GroupMember{
				{
					ChannelName: testApplierChannel, MemberID: testMemberB, Host: "gr-2",
					Port: sql.NullInt64{Int64: 3306, Valid: true}, State: testStateOnline,
					Role: "SECONDARY", Version: "8.4.11", CommunicationStack: "MySQL",
				},
				{
					ChannelName: testApplierChannel, MemberID: testMemberA, Host: "gr-1",
					Port: sql.NullInt64{Int64: 3306, Valid: true}, State: testStateOnline,
					Role: "PRIMARY", Version: "8.4.11", CommunicationStack: "MySQL", Self: true,
				},
			},
		},
		{
			name: "unfamiliar state is reported intact",
			rows: [][]driver.Value{
				groupMemberRow(testMemberA, int64(3306), "SOMETHING_NEW", "", int64(1)),
			},
			want: []GroupMember{{
				ChannelName: testApplierChannel, MemberID: testMemberA, Host: "gr-1",
				Port: sql.NullInt64{Int64: 3306, Valid: true}, State: "SOMETHING_NEW",
				Version: "8.4.11", CommunicationStack: "MySQL", Self: true,
			}},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := scriptGroupMembers(t, groupMembersColumns(), testCase.rows)
			got, err := NewInspector(db).GroupMembers(t.Context())
			if err != nil {
				t.Fatalf("GroupMembers() returned error %v, want nil", err)
			}
			if got == nil {
				t.Fatal("GroupMembers() = nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("GroupMembers() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestGroupMembersReadsColumnsByName interleaves an unknown column and
// reorders the promised ones: a positional reader would decode the wrong
// values here.
func TestGroupMembersReadsColumnsByName(t *testing.T) {
	t.Parallel()

	columns := []string{
		"IS_SELF", "MEMBER_STATE", "EXTRA_COLUMN", "MEMBER_PORT", "MEMBER_COMMUNICATION_STACK",
		"MEMBER_VERSION", "MEMBER_ROLE", "MEMBER_HOST", "MEMBER_ID", "CHANNEL_NAME",
	}
	row := []driver.Value{
		int64(1), []byte(testStateOnline), []byte("ignored"), int64(33061), []byte("XCom"),
		[]byte("9.7.1"), []byte("PRIMARY"), []byte("host-a"), []byte(testMemberA), []byte(testApplierChannel),
	}

	db := scriptGroupMembers(t, columns, [][]driver.Value{row})
	got, err := NewInspector(db).GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("GroupMembers() returned error %v, want nil", err)
	}

	want := []GroupMember{{
		ChannelName: testApplierChannel, MemberID: testMemberA, Host: "host-a",
		Port: sql.NullInt64{Int64: 33061, Valid: true}, State: testStateOnline, Role: "PRIMARY",
		Version: "9.7.1", CommunicationStack: "XCom", Self: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GroupMembers() = %#v, want %#v", got, want)
	}
}

// TestGroupMembersStatement pins the exact statement and that it is the only
// one: Self is computed by the server, never by a second read.
func TestGroupMembersStatement(t *testing.T) {
	t.Parallel()

	var log []string
	db := testsupport.OpenScriptedDBWithLog(&log, testsupport.ScriptedQuery{
		Match: testMatchGroupMembers, Columns: groupMembersColumns(),
	})
	defer db.Close()

	if _, err := NewInspector(db).GroupMembers(t.Context()); err != nil {
		t.Fatalf("GroupMembers() returned error %v, want nil", err)
	}
	if want := []string{testSQLGroupMembers}; !slices.Equal(log, want) {
		t.Errorf("statements = %q, want %q", log, want)
	}
}

func TestGroupMembersErrors(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	closeFailure := errors.New("close failed")
	valid := groupMemberRow(testMemberA, int64(3306), testStateOnline, "PRIMARY", int64(1))

	withValue := func(position int, value driver.Value) []driver.Value {
		row := slices.Clone(valid)
		row[position] = value

		return row
	}

	cases := []struct {
		name        string
		query       testsupport.ScriptedQuery
		wantChannel string
		wantColumn  string
		wantCause   error
	}{
		{
			name:      "statement fails",
			query:     testsupport.ScriptedQuery{Match: testMatchGroupMembers, Err: queryFailure},
			wantCause: queryFailure,
		},
		{
			name: "promised column missing",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns()[:8],
			},
			wantColumn: "IS_SELF",
			wantCause:  errMissingColumn,
		},
		{
			name: "NULL channel name carries no channel",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{withValue(0, nil)},
			},
			wantColumn: "CHANNEL_NAME",
			wantCause:  errUnexpectedNULL,
		},
		{
			name: "NULL member state",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{withValue(4, nil)},
			},
			wantChannel: testApplierChannel,
			wantColumn:  "MEMBER_STATE",
			wantCause:   errUnexpectedNULL,
		},
		{
			name: "undecodable port",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{withValue(3, []byte("port"))},
			},
			wantChannel: testApplierChannel,
			wantColumn:  "MEMBER_PORT",
		},
		{
			name: "NULL IS_SELF",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{withValue(8, nil)},
			},
			wantChannel: testApplierChannel,
			wantColumn:  "IS_SELF",
			wantCause:   errUnexpectedNULL,
		},
		{
			name: "IS_SELF outside 0 and 1",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{withValue(8, int64(2))},
			},
			wantChannel: testApplierChannel,
			wantColumn:  "IS_SELF",
			wantCause:   errUnrecognizedValue,
		},
		{
			name: "iteration fails",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{valid}, RowsErr: iterationFailure,
			},
			wantCause: iterationFailure,
		},
		{
			name: "close fails",
			query: testsupport.ScriptedQuery{
				Match: testMatchGroupMembers, Columns: groupMembersColumns(),
				Rows: [][]driver.Value{valid}, CloseErr: closeFailure, PendingResultSet: true,
			},
			wantCause: closeFailure,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			db := testsupport.OpenScriptedDB(testCase.query)
			defer db.Close()

			got, err := NewInspector(db).GroupMembers(t.Context())
			if err == nil {
				t.Fatalf("GroupMembers() = %#v, nil; want an error", got)
			}
			if got != nil {
				t.Errorf("GroupMembers() returned %#v with its error, want nil", got)
			}
			assertOpError(t, err, "group_members", testCase.wantChannel, testCase.wantColumn)
			if testCase.wantCause != nil && !errors.Is(err, testCase.wantCause) {
				t.Errorf("error %v does not wrap %v", err, testCase.wantCause)
			}
		})
	}
}

func TestGroupMembersNilQuerier(t *testing.T) {
	t.Parallel()

	_, err := NewInspector(nil).GroupMembers(t.Context())
	if !errors.Is(err, ErrNilQuerier) {
		t.Fatalf("GroupMembers() on a nil Querier returned %v, want ErrNilQuerier", err)
	}
	assertOpError(t, err, "group_members", "", "")
}

// TestGroupMembersCanceledContext proves a canceled context is an error, never
// an empty slice that reads as "no active plugin".
func TestGroupMembersCanceledContext(t *testing.T) {
	t.Parallel()

	db := scriptGroupMembers(t, groupMembersColumns(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := NewInspector(db).GroupMembers(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GroupMembers() on a canceled context = %#v, %v; want context.Canceled", got, err)
	}
	assertOpError(t, err, "group_members", "", "")
}

func TestGroupMembersFreshSlices(t *testing.T) {
	t.Parallel()

	db := scriptGroupMembers(t, groupMembersColumns(), [][]driver.Value{
		groupMemberRow(testMemberA, int64(3306), testStateOnline, "PRIMARY", int64(1)),
	})
	inspector := NewInspector(db)

	first, err := inspector.GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("first GroupMembers() returned error %v", err)
	}
	first[0].State = "TAMPERED"

	second, err := inspector.GroupMembers(t.Context())
	if err != nil {
		t.Fatalf("second GroupMembers() returned error %v", err)
	}
	if second[0].State != testStateOnline {
		t.Errorf("second GroupMembers()[0].State = %q, want %q", second[0].State, testStateOnline)
	}
}

func TestGroupMemberJSONContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		member GroupMember
		want   string
	}{
		{
			name: "port present",
			member: GroupMember{
				ChannelName: testApplierChannel, MemberID: testMemberA, Host: "gr1",
				Port: sql.NullInt64{Int64: 3306, Valid: true}, State: testStateOnline,
				Role: "PRIMARY", Version: "8.4.11", CommunicationStack: "MySQL", Self: true,
			},
			want: `{"channel_name":"group_replication_applier","member_id":"` + testMemberA + `",` +
				`"host":"gr1","port":{"Int64":3306,"Valid":true},"state":"ONLINE","role":"PRIMARY",` +
				`"version":"8.4.11","communication_stack":"MySQL","self":true}`,
		},
		{
			name:   "port NULL",
			member: GroupMember{ChannelName: testApplierChannel, State: testStateOffline},
			want: `{"channel_name":"group_replication_applier","member_id":"","host":"",` +
				`"port":{"Int64":0,"Valid":false},"state":"OFFLINE","role":"","version":"",` +
				`"communication_stack":"","self":false}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(testCase.member)
			if err != nil {
				t.Fatalf("json.Marshal returned error %v, want nil", err)
			}
			if string(encoded) != testCase.want {
				t.Errorf("json.Marshal = %s, want %s", encoded, testCase.want)
			}
		})
	}
}

// TestGroupMembersErrorNamesMember proves a decode failure after MEMBER_ID
// names that member, and that an anonymous row names none: every row shares
// the one channel, so the channel alone does not identify the object.
func TestGroupMembersErrorNamesMember(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		id       string
		wantName bool
	}{
		{name: "identified row", id: testMemberA, wantName: true},
		{name: "anonymous row", id: "", wantName: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			row := []driver.Value{
				[]byte(testApplierChannel), []byte(testCase.id), []byte(""), []byte("port"),
				[]byte(testStateOffline), []byte(""), []byte(""), []byte(""), int64(0),
			}
			db := scriptGroupMembers(t, groupMembersColumns(), [][]driver.Value{row})

			_, err := NewInspector(db).GroupMembers(t.Context())
			assertOpError(t, err, "group_members", testApplierChannel, "MEMBER_PORT")
			named := strings.Contains(err.Error(), "member "+testMemberA+": ")
			if named != testCase.wantName {
				t.Errorf("error %q names member %s = %t, want %t", err, testMemberA, named, testCase.wantName)
			}
			if testCase.wantName && strings.Contains(err.Error(), "member : ") {
				t.Errorf("error %q names an empty member", err)
			}
		})
	}
}

// TestGroupMembersMemberIDFailureNamesNoMember pins that a failure of MEMBER_ID
// itself names no member, even when an earlier row decoded one: the UUID a
// row's error names is that row's own, never a neighbor's.
func TestGroupMembersMemberIDFailureNamesNoMember(t *testing.T) {
	t.Parallel()

	first := groupMemberRow(testMemberA, int64(3306), testStateOnline, "PRIMARY", int64(1))
	second := groupMemberRow(testMemberB, int64(3306), testStateOnline, "SECONDARY", int64(0))
	second[1] = nil
	db := scriptGroupMembers(t, groupMembersColumns(), [][]driver.Value{first, second})

	_, err := NewInspector(db).GroupMembers(t.Context())
	assertOpError(t, err, "group_members", testApplierChannel, "MEMBER_ID")
	if strings.Contains(err.Error(), "member ") {
		t.Errorf("error %q names a member, want none: MEMBER_ID itself failed", err)
	}
	if !errors.Is(err, errUnexpectedNULL) {
		t.Errorf("error %v does not wrap errUnexpectedNULL", err)
	}
}
