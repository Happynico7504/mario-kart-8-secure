package nex_match_making

import (
	"fmt"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/p2p"
	nex "github.com/PretendoNetwork/nex-go"
	match_making "github.com/PretendoNetwork/nex-protocols-go/match-making"
)

func GetSessionURLs(err error, client *nex.Client, callID uint32, gatheringId uint32) {
	var stationUrlStrings []string

	hostpid, _, _, _, _ := database.GetRoomInfo(gatheringId)

	stationUrlStrings = database.GetPlayerURLs(hostpid)

	// A tunneled gathering: the caller reaches the host through the hub's P2P tunnel
	// instead of directly - see the p2p package's doc comment for why mk8-secure's
	// version of this (unlike wsc-secure's) needs no in-band packet rewriting.
	if t := p2p.Open(gatheringId, hostpid, false); t != nil {
		for i, u := range stationUrlStrings {
			if nex.NewStationURL(u).Type() != "3" {
				continue
			}
			if aliased, ok := p2p.Alias(u, t, hostpid); ok {
				stationUrlStrings[i] = aliased
			}
		}
	}

	fmt.Printf("GetSessionURLs: callerPID=%d gid=%d hostPID=%d urls=%v\n", client.PID(), gatheringId, hostpid, stationUrlStrings)

	rmcResponseStream := nex.NewStreamOut(globals.NEXServer)
	rmcResponseStream.WriteListString(stationUrlStrings)

	rmcResponseBody := rmcResponseStream.Bytes()

	// Build response packet
	rmcResponse := nex.NewRMCResponse(match_making.ProtocolID, callID)
	rmcResponse.SetSuccess(match_making.MethodGetSessionURLs, rmcResponseBody)

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
}
