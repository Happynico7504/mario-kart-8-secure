package nex_matchmake_extension

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/matchsetup"
	nex_notifications "github.com/PretendoNetwork/mario-kart-8-secure/nex/notifications"
	nex "github.com/PretendoNetwork/nex-go"
	match_making "github.com/PretendoNetwork/nex-protocols-go/match-making"
	matchmake_extension "github.com/PretendoNetwork/nex-protocols-go/matchmake-extension"
)

// matchWaitTimeout/matchWaitPoll: a small grace window, safely under the client's
// own per-RPC-call timeout (confirmed live 2026-11-04 - holding this response for 3
// minutes made the client fail before even reaching the lobby), to let two players
// who searched within the same instant land in one response instead of racing each
// other into separate "matched" states. The real fix for a longer wait is the
// Notifications::ProcessNotificationEvent push below, sent once an actual partner
// joins - the client's own ~35-45s "give up on a lonely session" timer only starts
// once it RECEIVES a matched response, so a host who's still waiting here never
// receives one and never starts that clock in the first place.
const matchWaitTimeout = 4 * time.Second
const matchWaitPoll = 500 * time.Millisecond

// roomMatchMu serializes the find-room/create-room/join-room sequence below. Each
// AutoMatchmakeWithSearchCriteria_Postpone call runs in its own goroutine
// (nex-protocols-go's HandlePacket does "go protocol.Handle..."), so two consoles
// searching within milliseconds of each other could both run FindRoom before either
// committed its AddPlayerToRoom, see no existing room, and each create its own -
// leaving both players "matched" into separate one-player rooms with no partner to
// connect to ("could not connect to partner's device").
var roomMatchMu sync.Mutex

var regionList = []string{"Worldwide", "Japan", "United States", "Europe", "Korea", "China", "Taiwan"}
var gameModes = []string{"VS Race", "Battle"}
var ccList = []string{"Unk", "200cc", "50cc", "100cc", "150cc", "Mirror", "BattleCC"}
var itemModes = []string{"Unk1", "Unk2", "Unk3", "Unk4", "Unk5", "Normal", "Unk7", "All Items", "Shells Only", "Bananas Only", "Mushrooms Only", "Bob-ombs Only", "No Items", "No Items or Coins", "Frantic"}
var vehicleModes = []string{"All Vehicles", "Karts Only", "Bikes Only"}
var controllerModes = []string{"Unk", "Tilt Only", "All Controls"}
var dlcModes = []string{"No DLC", "DLC Pack 1 Only", "DLC Pack 2 Only", "Both DLC Packs"}

// safeIndex looks up idx in list, or returns "Unk" if idx is out of range. Several of
// these lookup tables are shorter than the bit width their packed attribute occupies
// (e.g. itemModes has 15 entries for a 4-bit field, vehicleModes/controllerModes have
// 3 for a 2-bit field), so an otherwise-valid value can still land past the end.
func safeIndex(list []string, idx uint32) string {
	if int(idx) >= len(list) {
		return "Unk"
	}
	return list[idx]
}

func AutoMatchmakeWithSearchCriteria_Postpone(err error, client *nex.Client, callID uint32, searchCriteria []*match_making.MatchmakeSessionSearchCriteria, anyGathering *nex.DataHolder, message string) {

	// * From Jon: This was added here because this function had the wrong signature
	// * I have no idea if this works at all, I just got it to build
	matchmakeSession := anyGathering.ObjectData().(*match_making.MatchmakeSession)

	// A search (as opposed to updating an existing session) sends a near-empty template
	// gathering: Attributes comes back with fewer than the 6 slots this handler indexes,
	// which segfaulted the whole server on every search. Pad it out instead of crashing;
	// the padded slots just read as attribute 0 ("Worldwide"/"Unk"/etc) until the real
	// per-slot meaning for a bare search request is reverse-engineered.
	for len(matchmakeSession.Attributes) < 6 {
		matchmakeSession.Attributes = append(matchmakeSession.Attributes, 0)
	}
	if int(matchmakeSession.GameMode) >= len(gameModes) {
		matchmakeSession.GameMode = 0
	}

	gameConfig := matchmakeSession.Attributes[2]
	region := matchmakeSession.Attributes[3]
	dlcAttr := matchmakeSession.Attributes[5] & 0xF
	var vacantParticipants uint16
	if len(searchCriteria) > 0 {
		vacantParticipants = searchCriteria[0].VacantParticipants
	}

	shifted := gameConfig
	fmt.Println(strconv.FormatUint(uint64(gameConfig), 2))
	fmt.Println("===== MATCHMAKE SESSION JOIN =====")
	fmt.Println("REGION: " + safeIndex(regionList, region))
	fmt.Println("GAME MODE: " + safeIndex(gameModes, matchmakeSession.GameMode))
	fmt.Println("CC: " + safeIndex(ccList, shifted%0b111))
	shifted = shifted >> 12
	fmt.Println("DLC MODE: " + safeIndex(dlcModes, dlcAttr))
	fmt.Println("ITEM MODE: " + safeIndex(itemModes, shifted%0b1111))
	shifted = shifted >> 8
	fmt.Println("VEHICLE MODE: " + safeIndex(vehicleModes, shifted%0b11))
	shifted = shifted >> 4
	fmt.Println("CONTROLLER MODE: " + safeIndex(controllerModes, shifted%0b11))
	fmt.Println("HAVE GUEST PLAYER: " + strconv.FormatBool(vacantParticipants > 1))

	roomMatchMu.Lock()
	var gid uint32 = math.MaxUint32
	// A player whose last match setup failed (bad NAT, or a connection that signaled
	// fine but never held - see matchsetup package) always gets a fresh solo
	// gathering instead of searching for a real one to join, exactly like
	// wsc-secure's Connection Hall of Shame. Isolated in both directions: the check
	// just below also skips any candidate room hosted by a currently-blocked pid.
	if !matchsetup.IsBlocked(client.PID()) {
		gid = database.FindRoom(matchmakeSession.GameMode, true, region, gameConfig, uint32(vacantParticipants), dlcAttr)
	}
	isNewRoom := false
	if gid != math.MaxUint32 {
		// FindRoom only checks room age, not whether its host is still connected - a
		// player who dc'd earlier leaves their room matchable (within roomStaleAfter)
		// with no live client behind it, so the next searcher gets paired with a dead
		// host and RequestProbeInitiationExt relays to them with found=false ("dc
		// without other players"). Treat a room whose host has disconnected as if it
		// didn't match, so a fresh room gets created instead.
		existingHostPID, _, _, _, _ := database.GetRoomInfo(gid)
		if globals.NEXServer.FindClientFromPID(existingHostPID) == nil || matchsetup.IsBlocked(existingHostPID) {
			gid = math.MaxUint32
		}
	}
	if gid == math.MaxUint32 {
		gid = database.NewRoom(client.PID(), matchmakeSession.GameMode, true, region, gameConfig, uint32(vacantParticipants), dlcAttr)
		isNewRoom = true
	}
	var existingPIDs []uint32
	if !isNewRoom {
		existingPIDs = database.GetRoomPlayerPIDs(gid) // snapshot before this player is added
		matchsetup.RecordJoin(client.PID(), gid)
	}
	database.AddPlayerToRoom(gid, client.PID(), uint32(vacantParticipants))
	roomMatchMu.Unlock()

	playerCount := database.GetRoomPlayerCount(gid)

	// We're joining a room that already had someone else waiting in it - tell them
	// immediately instead of leaving them to find out only from their own matchmake
	// response (which they already have) or their lonely-session timeout (which this
	// pre-empts). See matchWaitTimeout's comment above for why this matters.
	if len(existingPIDs) > 0 {
		event := nex_notifications.NewNotificationEvent()
		event.PIDSource = client.PID()
		event.Type = nex_notifications.NotificationTypeParticipationEventParticipate
		event.Param1 = gid
		event.Param2 = client.PID()
		event.StrParam = message
		event.Param3 = playerCount
		for _, pid := range existingPIDs {
			if pid != client.PID() {
				nex_notifications.SendTo(pid, event)
			}
		}
	}

	// Don't tell THIS client it's matched until either someone else is actually in
	// the room, or we give up waiting - see matchWaitTimeout above.
	waitDeadline := time.Now().Add(matchWaitTimeout)
	for playerCount < 2 && time.Now().Before(waitDeadline) {
		time.Sleep(matchWaitPoll)
		playerCount = database.GetRoomPlayerCount(gid)
	}

	hostpid, gamemode, region, gconfig, dlcmode := database.GetRoomInfo(gid)
	sessionKey := "00000000000000000000000000000000"

	matchmakeSession.Gathering.ID = gid
	matchmakeSession.Gathering.OwnerPID = hostpid
	matchmakeSession.Gathering.HostPID = hostpid
	// The client decides, purely from fields in THIS single response (it never
	// retries AutoMatchmakeWithSearchCriteria_Postpone - confirmed live, only one call
	// per search attempt), whether to proceed straight to race setup or keep showing
	// its "searching" UI. A VS Race needs at least 2 racers, so tell the client that
	// explicitly; ParticipationCount reflects how many are actually in the room now.
	matchmakeSession.Gathering.MinimumParticipants = 2
	matchmakeSession.ParticipationCount = playerCount
	fmt.Printf("AutoMatchmake: PID=%d gid=%d playerCount=%d\n", client.PID(), gid, playerCount)
	matchmakeSession.SessionKey = []byte(sessionKey)
	matchmakeSession.GameMode = gamemode
	matchmakeSession.Attributes[3] = region
	matchmakeSession.Attributes[2] = gconfig
	matchmakeSession.Attributes[5] = dlcmode

	rmcResponseStream := nex.NewStreamOut(globals.NEXServer)
	rmcResponseStream.WriteString("MatchmakeSession")
	lengthStream := nex.NewStreamOut(globals.NEXServer)
	lengthStream.WriteStructure(matchmakeSession.Gathering)
	lengthStream.WriteStructure(matchmakeSession)
	matchmakeSessionLength := uint32(len(lengthStream.Bytes()))
	rmcResponseStream.WriteUInt32LE(matchmakeSessionLength + 4)
	rmcResponseStream.WriteUInt32LE(matchmakeSessionLength)
	rmcResponseStream.WriteStructure(matchmakeSession.Gathering)
	rmcResponseStream.WriteStructure(matchmakeSession)

	rmcResponseBody := rmcResponseStream.Bytes()

	rmcResponse := nex.NewRMCResponse(matchmake_extension.ProtocolID, callID)
	rmcResponse.SetSuccess(matchmake_extension.MethodAutoMatchmakeWithSearchCriteria_Postpone, rmcResponseBody)

	rmcResponseBytes := rmcResponse.Bytes()

	responsePacket, _ := nex.NewPacketV1(client, nil)

	responsePacket.SetVersion(1)
	responsePacket.SetSource(0xA1)
	responsePacket.SetDestination(0xAF)
	responsePacket.SetType(nex.DataPacket)
	responsePacket.SetPayload(rmcResponseBytes)

	responsePacket.AddFlag(nex.FlagNeedsAck)
	responsePacket.AddFlag(nex.FlagReliable)

	globals.NEXServer.Send(responsePacket)

	// DISABLED 2026-11-15 for an ablation test: every solo session observed live
	// (tonight and in the 2026-11-08 logs) disconnects at a near-exact ~40-47s mark
	// from Register, with every vendor-side kick/ping/resend mechanism ruled out as
	// the cause. This heartbeat sends the host a real, unsolicited, semantically odd
	// NotificationEvent every 15s (ParticipationEvent naming the host as having
	// "joined" its own gathering) - exactly the kind of unexpected push that could
	// make a real console decide to disconnect on purpose rather than time out.
	// Turning it off to see whether solo sessions survive past ~40-47s without it.
	// If they do, this was never a benign no-op speculative fix - it was the cause.
	// if playerCount < 2 {
	// 	go sendSelfHeartbeat(gid, client.PID())
	// }
}

const selfHeartbeatInterval = 15 * time.Second
const selfHeartbeatMaxDuration = 10 * time.Minute

func sendSelfHeartbeat(gid uint32, pid uint32) {
	deadline := time.Now().Add(selfHeartbeatMaxDuration)
	for time.Now().Before(deadline) {
		time.Sleep(selfHeartbeatInterval)

		if globals.NEXServer.FindClientFromPID(pid) == nil {
			return // disconnected - nothing left to keep alive
		}
		if database.GetRoomPlayerCount(gid) >= 2 {
			return // a real partner joined; the join-triggered push already covers this
		}

		event := nex_notifications.NewNotificationEvent()
		event.PIDSource = pid
		event.Type = nex_notifications.NotificationTypeParticipationEventParticipate
		event.Param1 = gid
		event.Param2 = pid
		event.StrParam = ""
		event.Param3 = 1
		nex_notifications.SendTo(pid, event)
	}
}
