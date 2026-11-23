package nex

import (
	"fmt"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/matchsetup"
)

// staleSessionThreshold is how long a registered PID can go without ANY inbound packet
// before watchStaleSessions treats it as gone - closed the game, crashed, lost power, or
// the network just vanished without a clean DisconnectPacket. 60s is comfortably above
// any normal gap seen live (matchmaking waits, NAT traversal round trips all land well
// under this), while still catching a closed game within a minute instead of up to an
// hour - PingTimeout is 3600s (see StartNEXServer's own comment on why that can't just be
// lowered back down).
const staleSessionThreshold = 60 * time.Second

// watchStaleSessions is the same idea as wsc-secure's watchStaleConnections: a plain
// ticker checking lastPacketAt, not another vendor timer callback. On a hit it runs the
// exact same cleanup a real Disconnect would (handlePlayerGone) but - like WSC's version -
// deliberately never calls server.Kick(). A false positive here just drops the player off
// the dashboard and out of their room; if a packet from them still comes in later, it's
// handled normally and they just look freshly reconnected.
func watchStaleSessions() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		globals.ConnectedPIDs.Range(func(k, _ interface{}) bool {
			pid := k.(uint32)
			last, ok := globals.LastPacketAt.Load(pid)
			if !ok {
				return true
			}
			if now.Sub(last.(time.Time)) < staleSessionThreshold {
				return true
			}
			fmt.Printf("watchStaleSessions: PID=%d quiet for %s, treating as gone\n", pid, now.Sub(last.(time.Time)).Round(time.Second))
			handlePlayerGone(pid)
			return true
		})
	}
}

// handlePlayerGone is the one place "this player is no longer here" cleanup happens,
// whether that's a real DisconnectPacket or watchStaleSessions giving up on them:
//   - drops them from the dashboard's live state (globals.ConnectedPIDs, the persistent
//     sessions collection, and the ping-staleness tracker itself)
//   - removes them from whatever room they were in, so a closed game doesn't leave a
//     phantom slot (and, previously, a phantom HOST) behind forever
//   - if they were that room's host and other players are still in it, hands ownership
//     to one of them instead of leaving the room host-less until it eventually gets
//     swept as inactive
//   - feeds the Connection Hall of Shame the same way a real disconnect always has
func handlePlayerGone(pid uint32) {
	globals.ConnectedPIDs.Delete(pid)
	globals.LastPacketAt.Delete(pid)
	// DeletePlayerSession existed since the original fork but was never called from
	// anywhere - sessions written by Register (nex/secure-connection/register.go)
	// accumulated in Mongo forever, with no way to tell a truly-connected player from
	// a PID that connected once months ago. Needed now that the dashboard's persistent
	// session view (see /status) treats this collection as live state.
	database.DeletePlayerSession(pid)

	if gid := database.FindRoomForPID(pid); gid != 0 {
		host, hostKnown := database.TryGetRoomHost(gid)
		var remaining []uint32
		for _, p := range database.GetRoomPlayerPIDs(gid) {
			if p != pid {
				remaining = append(remaining, p)
			}
		}
		database.RemovePlayerFromRoom(gid, pid) // destroys the room itself if that emptied it
		if hostKnown && host == pid && len(remaining) > 0 {
			fmt.Printf("handlePlayerGone: PID=%d was host of gid=%d, handing off to PID=%d\n", pid, gid, remaining[0])
			database.UpdateRoomHost(gid, remaining[0])
		}
	}

	// Port of wsc-secure's "Connection Hall of Shame": confirmed live 2026-11-08 across
	// 3 real players that NAT traversal signaling can fully succeed (found=true both
	// ways, ReportNATTraversalResult result=true with a real RTT) and the console still
	// disconnects itself 10-40s later - a P2P connection that signals fine but never
	// actually holds. Detect that pattern (joined a room, NAT traversal never actually
	// started, disconnected within 30s) and isolate the player instead of letting them
	// keep eating into real opponents' matchmaking attempts.
	matchsetup.HandleDisconnect(pid, func(gid uint32) uint32 {
		host, _ := database.TryGetRoomHost(gid)
		return host
	}, database.CloseRoom)
}
