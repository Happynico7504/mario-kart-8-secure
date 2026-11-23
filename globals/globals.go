package globals

import (
	"sync"

	"github.com/PretendoNetwork/mario-kart-8-secure/types"
	nex "github.com/PretendoNetwork/nex-go"
	"github.com/PretendoNetwork/plogger-go"
)

var Logger = plogger.NewLogger()
var NEXServer *nex.Server
var Config *types.ServerConfig
var MatchmakingState []*types.MatchmakingData

// ConnectedPIDs tracks currently-registered players (uint32 PID -> connected-at time).
// nex-go v1's Server has no exported way to list its connected clients, so Register and
// the Disconnect event hook (see nex/server.go, nex/secure-connection/register.go) keep this
// in sync themselves - used by the /status endpoint for the relay-admin dashboard.
var ConnectedPIDs sync.Map // uint32 -> time.Time

// LastPacketAt tracks, per PID, the last time ANY inbound packet was seen (any type - the
// same signal IncreasePingTimeoutTime already resets on, read via nex-go's generic "Packet"
// event). A player who closes the game without the console sending a clean DisconnectPacket
// (crash, power loss, yanked network cable) never reaches our Disconnect handler at all -
// with PingTimeout set to 3600s (see nex/server.go's own comment on why), the vendor's own
// kick timer wouldn't notice for up to an hour either. watchStaleSessions (nex/server.go)
// polls this on its own short cycle to catch that case, mirroring wsc-secure's
// watchStaleConnections - but, like that one, deliberately never calls server.Kick(): a
// false positive here just drops the player off the dashboard and out of their room, not
// off the live PRUDP session, so a stray later packet still gets through unaffected.
var LastPacketAt sync.Map // uint32 -> time.Time
