// Package matchsetup ports wsc-secure's "Connection Hall of Shame" to mk8-secure: when a
// player's match setup keeps failing (bad NAT, a connection that signals fine but never
// actually holds), stop repeatedly pairing them with real opponents who'd just waste time on
// another doomed match. mk8-secure has no EndParticipation-style "I'm leaving" RMC call to
// hook (confirmed live 2026-11-08 - consoles just send a raw PRUDP DisconnectPacket), so the
// quick-exit signal here is the transport-level Disconnect event instead of an RMC method.
package matchsetup

import (
	"fmt"
	"sync"
	"time"
)

// setupBlocked holds pids whose last match setup failed, why, and when their matchmaking
// block expires. A blocked pid always gets its own solo gathering rather than joining a real
// waiting player, and a gathering hosted by a blocked pid is skipped by other searchers - see
// auto_matchmake_with_search_criteria_postpone.go.
var setupBlocked sync.Map // uint32 pid -> setupBlock

type setupBlock struct {
	until  time.Time
	reason string
}

const BlockDuration = 15 * time.Minute

// Block (re)starts pid's block. One failed setup often shows up twice (a failed NAT
// traversal, then the disconnect that follows it), so a block already running keeps its
// first reason instead of being overwritten by the second symptom of the same failure.
func Block(pid uint32, reason string) {
	if b, ok := setupBlocked.Load(pid); ok && time.Now().Before(b.(setupBlock).until) {
		return
	}
	fmt.Printf("ConnectionHallOfShame: PID=%d blocked for %s (%s)\n", pid, BlockDuration, reason)
	setupBlocked.Store(pid, setupBlock{until: time.Now().Add(BlockDuration), reason: reason})
}

func IsBlocked(pid uint32) bool {
	b, ok := setupBlocked.Load(pid)
	if !ok {
		return false
	}
	if time.Now().After(b.(setupBlock).until) {
		setupBlocked.Delete(pid)
		return false
	}
	return true
}

// ShameEntry is one blocked pid, for the /status endpoint behind relay-admin's dashboard.
type ShameEntry struct {
	PID    uint32
	Until  time.Time
	Reason string
}

// Shame lists every pid currently blocked, pruning any whose block has expired.
func Shame() []ShameEntry {
	now := time.Now()
	var entries []ShameEntry
	setupBlocked.Range(func(k, v interface{}) bool {
		b := v.(setupBlock)
		if now.After(b.until) {
			setupBlocked.Delete(k)
			return true
		}
		entries = append(entries, ShameEntry{PID: k.(uint32), Until: b.until, Reason: b.reason})
		return true
	})
	return entries
}

// Reason returns why pid is blocked, or "" if it isn't.
func Reason(pid uint32) string {
	b, ok := setupBlocked.Load(pid)
	if !ok || time.Now().After(b.(setupBlock).until) {
		return ""
	}
	return b.(setupBlock).reason
}

// p2pStarted marks a gathering whose NAT traversal actually succeeded (ReportNATTraversalResult
// result=true), so a later Disconnect from one of its players isn't mistaken for a failed setup.
var p2pStarted sync.Map // uint32 gid -> time.Time

func MarkP2PStarted(gid uint32) {
	p2pStarted.Store(gid, time.Now())
}

func IsP2PStarted(gid uint32) bool {
	_, ok := p2pStarted.Load(gid)
	return ok
}

// joinedAt tracks when a player last joined a gathering as a joiner (not its host/creator).
// Used to detect quick-exit loops: join an existing room -> NAT setup never completes ->
// disconnect within 30s.
var joinedAt sync.Map // uint32 pid -> joinRecord

type joinRecord struct {
	gid  uint32
	when time.Time
}

const QuickExitWindow = 30 * time.Second

func RecordJoin(pid, gid uint32) {
	joinedAt.Store(pid, joinRecord{gid: gid, when: time.Now()})
}

// failCount tracks consecutive quick-exit failures per gathering, so a host whose room keeps
// failing to connect joiners (not just one unlucky joiner) gets blocked too.
var failCount sync.Map // uint32 gid -> int

const maxGatheringFailures = 3

// HandleDisconnect runs whenever a client disconnects (see nex/server.go's "Disconnect"
// event hook). If pid recently joined a gathering as a joiner and that gathering's NAT
// traversal never actually succeeded, this disconnect is almost certainly setup failure, not
// a clean exit - block pid, and if the same gathering has now failed 3 times in a row, block
// its host too and mark it closed so FindRoom stops matching new searchers into it.
func HandleDisconnect(pid uint32, hostOf func(gid uint32) uint32, closeGathering func(gid uint32)) {
	rec, ok := joinedAt.LoadAndDelete(pid)
	if !ok {
		return
	}
	jr := rec.(joinRecord)
	if IsP2PStarted(jr.gid) || time.Since(jr.when) >= QuickExitWindow {
		return
	}

	Block(pid, "left during match setup")

	n := 0
	if v, loaded := failCount.Load(jr.gid); loaded {
		n = v.(int)
	}
	n++
	if n >= maxGatheringFailures {
		fmt.Printf("ConnectionHallOfShame: gid=%d had %d consecutive quick-exits, closing\n", jr.gid, n)
		closeGathering(jr.gid)
		if host := hostOf(jr.gid); host != 0 && host != pid {
			Block(host, "nobody could connect to their match")
		}
		failCount.Delete(jr.gid)
	} else {
		failCount.Store(jr.gid, n)
	}
}

// ClearGatheringFailures resets a gathering's quick-exit counter after a successful NAT
// traversal, so one earlier bad pairing doesn't count against a gathering that's now fine.
func ClearGatheringFailures(gid uint32) {
	failCount.Delete(gid)
}
