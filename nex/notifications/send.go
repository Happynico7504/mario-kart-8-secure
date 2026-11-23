package nex_notifications

import (
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	nex "github.com/PretendoNetwork/nex-go"
)

// SendTo pushes an unsolicited NotificationEvent RMC request to targetPID, if they're
// currently connected. This is how the real NEX Notifications protocol tells an
// already-waiting client about something it has no reason to poll for - e.g. telling
// a solo host, sitting in their just-created gathering, that a partner has now
// joined, instead of leaving the host to find out only via its own internal ~35-45s
// "give up on a lonely session" timer (see auto_matchmake_with_search_criteria_postpone.go).
func SendTo(targetPID uint32, event *NotificationEvent) {
	targetClient := globals.NEXServer.FindClientFromPID(targetPID)
	if targetClient == nil {
		return
	}

	eventStream := nex.NewStreamOut(globals.NEXServer)
	eventStream.WriteStructure(event)

	rmcRequest := nex.NewRMCRequest()
	rmcRequest.SetProtocolID(ProtocolID)
	// Fire-and-forget push, not a reply to any pending call - offset well clear of the
	// client's own sequential call IDs so it can never collide with a real call.
	rmcRequest.SetCallID(0x7FFFFFFF)
	rmcRequest.SetMethodID(MethodProcessNotificationEvent)
	rmcRequest.SetParameters(eventStream.Bytes())

	rmcRequestBytes := rmcRequest.Bytes()

	pushPacket, err := nex.NewPacketV1(targetClient, nil)
	if err != nil {
		return
	}

	pushPacket.SetVersion(1)
	pushPacket.SetSource(0xA1)
	pushPacket.SetDestination(0xAF)
	pushPacket.SetType(nex.DataPacket)
	pushPacket.SetPayload(rmcRequestBytes)

	pushPacket.AddFlag(nex.FlagNeedsAck)
	pushPacket.AddFlag(nex.FlagReliable)

	globals.NEXServer.Send(pushPacket)
}
