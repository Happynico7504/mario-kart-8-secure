package nex_nat_traversal

import (
	"fmt"
	"strconv"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/p2p"
	nex "github.com/PretendoNetwork/nex-go"
	nat_traversal "github.com/PretendoNetwork/nex-protocols-go/nat-traversal"
)

func RequestProbeInitiationExt(err error, client *nex.Client, callID uint32, targetList []string, stationToProbe string) {
	fmt.Printf("RequestProbeInitiationExt: PID=%d stationToProbe=%q targetList=%v\n", client.PID(), stationToProbe, targetList)

	rmcResponse := nex.NewRMCResponse(nat_traversal.ProtocolID, callID)
	rmcResponse.SetSuccess(nat_traversal.MethodRequestProbeInitiationExt, nil)

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

	// A caller that reaches its partner through the gathering's P2P tunnel (opened
	// earlier, from GetSessionURLs) must be relayed to the target as living at the
	// caller's OWN alias on that same tunnel - otherwise the target would try to
	// answer the caller's real address directly while the caller sends from the
	// tunnel, and the two would never see each other's packets.
	probeStation := stationToProbe
	if gid := database.FindRoomForPID(client.PID()); gid != 0 {
		if host, ok := database.TryGetRoomHost(gid); ok {
			if t := p2p.Open(gid, host, true); t != nil {
				if aliased, ok := p2p.Alias(stationToProbe, t, client.PID()); ok {
					probeStation = aliased
					fmt.Printf("RequestProbeInitiationExt: PID=%d probe via tunnel %s\n", client.PID(), aliased)
				}
			}
		}
	}

	rmcMessage := nex.RMCRequest{}
	rmcMessage.SetProtocolID(nat_traversal.ProtocolID)
	rmcMessage.SetCallID(0xffff0000 + callID)
	rmcMessage.SetMethodID(nat_traversal.MethodInitiateProbe)
	rmcRequestStream := nex.NewStreamOut(globals.NEXServer)
	rmcRequestStream.WriteString(probeStation)
	rmcRequestBody := rmcRequestStream.Bytes()
	rmcMessage.SetParameters(rmcRequestBody)
	rmcMessageBytes := rmcMessage.Bytes()

	for _, target := range targetList {
		targetUrl := nex.NewStationURL(target)
		targetRvcID, atoiErr := strconv.Atoi(targetUrl.RVCID())
		targetClient := globals.NEXServer.FindClientFromConnectionID(uint32(targetRvcID))
		fmt.Printf("RequestProbeInitiationExt: relaying InitiateProbe to target=%q rvcid=%d (parseErr=%v) found=%v\n", target, targetRvcID, atoiErr, targetClient != nil)
		if targetClient != nil {
			messagePacket, _ := nex.NewPacketV1(targetClient, nil)
			messagePacket.SetVersion(1)
			messagePacket.SetSource(0xA1)
			messagePacket.SetDestination(0xAF)
			messagePacket.SetType(nex.DataPacket)
			messagePacket.SetPayload(rmcMessageBytes)

			messagePacket.AddFlag(nex.FlagNeedsAck)
			messagePacket.AddFlag(nex.FlagReliable)

			globals.NEXServer.Send(messagePacket)
		}
	}
}
