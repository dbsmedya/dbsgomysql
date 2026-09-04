package replication

import (
	"fmt"
	"strconv"
)

// The fail-closed rule governs every check here: a check passes only on the
// exact proven value, and every other server string — including values that do
// not exist today — produces a finding. A future server state becomes a
// visible finding, never a silent pass.
const (
	runningYes = "Yes"
	gtidModeOn = "ON"
)

// CheckBinaryLogEnabled reports a finding when the server does not write a
// binary log.
//
// A server without binary logging cannot serve as a replication source, and no
// binlog-based capture or point-in-time recovery is possible on it. The
// finding carries the false value that caused it.
//
// CheckBinaryLogEnabled is safe for concurrent use.
func CheckBinaryLogEnabled(enabled bool) []Finding {
	if enabled {
		return nil
	}

	return []Finding{{
		Check:   IDBinaryLogEnabled,
		Message: findingMessage(IDBinaryLogEnabled, "Binary logging is disabled"),
		Facts:   enabled,
	}}
}

// CheckReplicationConfigured reports a finding when the server has no
// replication channels at all.
//
// This is the check that makes an unconfigured server fail a replication gate:
// the per-channel checks have nothing to report on an empty snapshot, so a
// consumer that omits this one lets a server with no replication pass. That
// applies equally to a snapshot filtered down to no channels.
//
// CheckReplicationConfigured is safe for concurrent use when channels is not
// mutated concurrently.
func CheckReplicationConfigured(channels []ChannelStatus) []Finding {
	if len(channels) > 0 {
		return nil
	}

	return []Finding{{
		Check:   IDReplicationConfigured,
		Message: findingMessage(IDReplicationConfigured, "The server has no replication channels"),
		Facts:   channels,
	}}
}

// CheckReplicationChannelsRunning reports a finding for each channel whose
// receiver or applier thread is not running, preserving the order of the
// channels supplied.
//
// A channel passes only when both threads report the exact value Yes. No,
// Connecting, and any value the server may report in future all produce a
// finding: Connecting is a failing state, and an unrecognized value is not
// evidence of health. Each finding carries the channel's full ChannelStatus,
// including its last-error fields, which may help diagnose the stop — a
// deliberate stop records no error at all, so empty error fields are normal
// rather than surprising.
//
// CheckReplicationChannelsRunning is safe for concurrent use when channels is
// not mutated concurrently.
func CheckReplicationChannelsRunning(channels []ChannelStatus) []Finding {
	// Indexed rather than ranged by value: a ChannelStatus is large enough that
	// copying one per iteration is wasteful, and the only copy this check wants
	// is the one it hands to Facts.
	var findings []Finding
	for index := range channels {
		if channels[index].IORunning == runningYes && channels[index].SQLRunning == runningYes {
			continue
		}

		summary := fmt.Sprintf(
			"Replication channel %q is not running: receiver %s, applier %s",
			channels[index].ChannelName,
			channels[index].IORunning,
			channels[index].SQLRunning,
		)
		findings = append(findings, Finding{
			Check:    IDReplicationChannelsRunning,
			Message:  findingMessage(IDReplicationChannelsRunning, summary),
			Channels: []string{channels[index].ChannelName},
			Facts:    channels[index],
		})
	}

	return findings
}

// CheckSecondsBehindSourceWithin reports a finding for each channel whose
// reported lag is unknown or exceeds maxSeconds, preserving the order of the
// channels supplied.
//
// The caller supplies the bound, so no threshold policy enters the library. A
// channel reporting exactly maxSeconds passes; one reporting more fails; and a
// channel reporting SQL NULL fails closed, because NULL means the estimate is
// unknowable — a replication thread is stopped — and a job that gates on lag
// must not proceed on an unknowable estimate. A negative maxSeconds produces a
// finding for every supplied channel, since no reported estimate can satisfy
// it; the value is never clamped and never defaulted. An empty channel slice
// produces no findings: detecting an unconfigured server is
// CheckReplicationConfigured's job.
//
// # What this bounds, and what it does not
//
// The check bounds Seconds_Behind_Source, the server's own reported estimate,
// and nothing stronger. The manuals document that estimate's limits: it can
// read 0 across a broken connection the receiver has not yet noticed, it can
// oscillate while the receiver queues an old-timestamped event, and SHOW
// REPLICA STATUS is nonblocking, so a snapshot taken during STOP REPLICA may
// not be the latest state. A multithreaded applier's low-water-mark position is
// a property of Exec_source_log_pos, which this check does not read (refman
// §19.5.1.34, "Replication and Transaction Inconsistencies"). Reading one
// snapshot removes the gap between this check and
// CheckReplicationChannelsRunning; it does not prove freshness or actual lag.
//
// CheckSecondsBehindSourceWithin is safe for concurrent use when channels is
// not mutated concurrently.
func CheckSecondsBehindSourceWithin(channels []ChannelStatus, maxSeconds int64) []Finding {
	// Indexed rather than ranged by value, for the reason given in
	// CheckReplicationChannelsRunning.
	var findings []Finding
	for index := range channels {
		lag := channels[index].SecondsBehindSource

		var summary string
		switch {
		case !lag.Valid:
			summary = fmt.Sprintf(
				"Replication channel %q reports an unknown Seconds_Behind_Source",
				channels[index].ChannelName,
			)
		// Ordered ahead of the comparison below, which a negative reported
		// estimate would otherwise satisfy: -2 is not above -1, yet no
		// estimate can satisfy a negative bound. A NULL lag is matched first,
		// so it keeps its own accurate message here too.
		case maxSeconds < 0:
			summary = fmt.Sprintf(
				"Replication channel %q reports Seconds_Behind_Source %s against the negative bound %s, which no reported estimate can satisfy",
				channels[index].ChannelName,
				strconv.FormatInt(lag.Int64, 10),
				strconv.FormatInt(maxSeconds, 10),
			)
		case lag.Int64 > maxSeconds:
			summary = fmt.Sprintf(
				"Replication channel %q reports Seconds_Behind_Source %s, above the bound %s",
				channels[index].ChannelName,
				strconv.FormatInt(lag.Int64, 10),
				strconv.FormatInt(maxSeconds, 10),
			)
		default:
			continue
		}

		findings = append(findings, Finding{
			Check:    IDSecondsBehindSourceWithin,
			Message:  findingMessage(IDSecondsBehindSourceWithin, summary),
			Channels: []string{channels[index].ChannelName},
			Facts:    channels[index],
		})
	}

	return findings
}

// CheckGTIDModeOn reports a finding when the server's GTID mode is not ON.
//
// Consumers that coordinate by GTID — resume, failover, CDC positioning — need
// ON; in the other modes the GTID sets describe only part of the write
// history. The check passes only on the exact value ON, so OFF_PERMISSIVE,
// ON_PERMISSIVE, and any unrecognized value all produce a finding. The finding
// carries the whole GTIDStatus.
//
// CheckGTIDModeOn is safe for concurrent use.
func CheckGTIDModeOn(status GTIDStatus) []Finding {
	if status.Mode == gtidModeOn {
		return nil
	}

	summary := fmt.Sprintf("GTID mode is %q rather than ON", status.Mode)

	return []Finding{{
		Check:   IDGTIDModeOn,
		Message: findingMessage(IDGTIDModeOn, summary),
		Facts:   status,
	}}
}
