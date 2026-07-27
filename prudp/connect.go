package prudp

import (
	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	nex "github.com/PretendoNetwork/nex-go"
)

func Connect(packet *nex.PacketV1) {
	payload := packet.Payload()
	stream := nex.NewStreamIn(payload, globals.NEXServer)

	// Buffer 1: TicketInternalData encrypted with DeriveKerberosKey(PID=2, serverPassword).
	// The outer ticket (sessionKey+targetPID+internalData encrypted with userKey) is decrypted
	// client-side; only the internalData portion reaches us here.
	ticketBytes, _ := stream.ReadBuffer()
	serverKey := nex.DeriveKerberosKey(2, []byte(globals.NEXServer.KerberosPassword()))
	ticketCipher := nex.NewKerberosEncryption(serverKey)
	decryptedInternal := ticketCipher.Decrypt(ticketBytes)

	// TicketInternalData layout: DateTime (8 bytes) + userPID (4 bytes) + sessionKey (KerberosKeySize bytes)
	internalStream := nex.NewStreamIn(decryptedInternal, globals.NEXServer)
	_ = internalStream.ReadDateTime()
	_ = internalStream.ReadUInt32LE() // userPID (also in check data below)
	sessionKey := internalStream.ReadBytesNext(int64(globals.NEXServer.KerberosKeySize()))

	// Buffer 2: check data encrypted with session key
	checkData, _ := stream.ReadBuffer()
	checkCipher := nex.NewKerberosEncryption(sessionKey)
	checkDataDecrypted := checkCipher.Decrypt(checkData)

	checkDataStream := nex.NewStreamIn(checkDataDecrypted, globals.NEXServer)
	userPID := checkDataStream.ReadUInt32LE()
	packet.Sender().SetPID(userPID)
	_ = checkDataStream.ReadUInt32LE() // CID of secure server station URL
	responseCheck := checkDataStream.ReadUInt32LE()

	responseValueStream := nex.NewStreamOut(globals.NEXServer)
	responseValueStream.WriteUInt32LE(responseCheck + 1)

	responseValueBufferStream := nex.NewStreamOut(globals.NEXServer)
	responseValueBufferStream.WriteBuffer(responseValueStream.Bytes())

	globals.NEXServer.AcknowledgePacket(packet, responseValueBufferStream.Bytes())

	packet.Sender().UpdateRC4Key(sessionKey)
	packet.Sender().SetSessionKey(sessionKey)

	if !database.DoesUserExist(userPID) {
		database.AddNewUser(userPID)
	}
}
