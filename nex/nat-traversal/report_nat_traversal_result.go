package nex_nat_traversal

import (
	"fmt"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/matchsetup"
	nex "github.com/PretendoNetwork/nex-go"
	nat_traversal "github.com/PretendoNetwork/nex-protocols-go/nat-traversal"
)

// ReportNATTraversalResult was unimplemented, so the library answered it with
// "not implemented" and never sent an RMC response at all - the console calls this
// right as a race starts (after NAT probing) and, getting no reply, disconnected
// instead of proceeding into the race. A bare success ack is all WSC's working
// implementation sends for the single-gathering case; it has no hole-punch exemption
// state to clear here.
func ReportNATTraversalResult(err error, client *nex.Client, callID uint32, cid uint32, result bool, rtt uint32) {
	fmt.Printf("ReportNATTraversalResult: PID=%d connectionID=%d cid=%d result=%v rtt=%d\n", client.PID(), client.ConnectionID(), cid, result, rtt)

	// See matchsetup.HandleDisconnect's comment - a true result here is the only signal
	// we have that this gathering's P2P connection ever actually got going, as opposed
	// to the signaling-succeeded-but-never-held pattern that's indistinguishable from a
	// clean disconnect otherwise.
	if gid := database.FindRoomForPID(client.PID()); gid != 0 {
		if result {
			matchsetup.MarkP2PStarted(gid)
			matchsetup.ClearGatheringFailures(gid)
		} else {
			matchsetup.Block(client.PID(), "NAT traversal failed")
		}
	}

	rmcResponse := nex.NewRMCResponse(nat_traversal.ProtocolID, callID)
	rmcResponse.SetSuccess(nat_traversal.MethodReportNATTraversalResult, nil)

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
