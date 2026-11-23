package nex_secure_connection

import (
	"fmt"
	"strconv"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	nex "github.com/PretendoNetwork/nex-go"
	secure_connection "github.com/PretendoNetwork/nex-protocols-go/secure-connection"
)

func Register(err error, client *nex.Client, callID uint32, stationUrls []*nex.StationURL) {
	fmt.Printf("Register: PID=%d oldConnectionID=%d existingSession=%v\n", client.PID(), client.ConnectionID(), database.DoesSessionExist(client.PID()))
	if _, already := globals.ConnectedPIDs.Load(client.PID()); !already {
		globals.ConnectedPIDs.Store(client.PID(), time.Now())
	}
	localStation := stationUrls[0]
	localStationURL := localStation.EncodeToString()
	connectionId := uint32(globals.NEXServer.ConnectionIDCounter().Increment())
	client.SetConnectionID(connectionId)
	client.SetLocalStationURL(localStationURL)

	address := client.Address().IP.String()
	port := strconv.Itoa(client.Address().Port)
	natf := "0"
	natm := "0"
	type_ := "3"

	// Consoles call Register more than once per logical connection (periodic
	// re-registration), and this handler used to always reset natm/natf to "0" -
	// silently wiping whatever ReportNATProperties had already stamped onto this
	// PID's station URLs, so the partner's later probe used stale/default NAT info.
	// Preserve any already-stamped natm/natf from the existing session's type-3 URL.
	sessionExists := database.DoesSessionExist(client.PID())
	if sessionExists {
		for _, oldURLString := range database.GetPlayerURLs(client.PID()) {
			oldURL := nex.NewStationURL(oldURLString)
			if oldURL.Type() == "3" && (oldURL.Natm() != "" && oldURL.Natm() != "0" || oldURL.Natf() != "" && oldURL.Natf() != "0") {
				natm = oldURL.Natm()
				natf = oldURL.Natf()
				break
			}
		}
	}

	localStation.SetAddress(address)
	localStation.SetPort(port)
	localStation.SetNatf(natf)
	localStation.SetNatm(natm)
	localStation.SetType(type_)

	globalStationURL := localStation.EncodeToString()

	if !sessionExists {
		database.AddPlayerSession(client.PID(), []string{localStationURL, globalStationURL}, address, port)
	} else {
		database.UpdatePlayerSessionAll(client.PID(), []string{localStationURL, globalStationURL}, address, port)
	}

	rmcResponseStream := nex.NewStreamOut(globals.NEXServer)

	rmcResponseStream.WriteUInt32LE(0x10001) // Success
	rmcResponseStream.WriteUInt32LE(connectionId)
	rmcResponseStream.WriteString(globalStationURL)

	rmcResponseBody := rmcResponseStream.Bytes()

	// Build response packet
	rmcResponse := nex.NewRMCResponse(secure_connection.ProtocolID, callID)
	rmcResponse.SetSuccess(secure_connection.MethodRegister, rmcResponseBody)

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
