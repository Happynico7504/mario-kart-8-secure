package nex

import (
	"fmt"
	"os"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/p2p"
	"github.com/PretendoNetwork/mario-kart-8-secure/prudp"
	"github.com/PretendoNetwork/nex-go"
)

func StartNEXServer() {
	globals.NEXServer = nex.NewServer()
	globals.NEXServer.SetPRUDPVersion(1)
	globals.NEXServer.SetDefaultNEXVersion(&nex.NEXVersion{
		Major: 3,
		Minor: 5,
		Patch: 4,
	})
	globals.NEXServer.SetKerberosPassword(os.Getenv("KERBEROS_PASSWORD"))
	globals.NEXServer.SetAccessKey(globals.Config.AccessKey)

	// ROOT CAUSE of the ~40-47s disconnects confirmed live 2026-11-15 via packet
	// capture: AcknowledgePacket's aggregate-ack path (vendor server.go) only writes
	// a real MultiAck payload (substream ID + sequence ID) when
	// server.PRUDPProtocolMinorVersion() >= 2 - and nothing here ever called
	// SetPRUDPProtocolMinorVersion, so it stayed at its zero-value default (0),
	// meaning every aggregate ack this server ever sent was empty. The capture
	// showed the console retransmitting the exact same encrypted reliable packets
	// (Register, AutoMatchmake, ReportNATProperties, ReplaceURL, SendReport - any
	// reliable call) every ~1.5-2s for the entire session, because our ack never
	// told it which packet was actually acknowledged - completely independent of
	// matchmaking state, NAT success, or player count, matching every observed
	// disconnect. wsc-secure's main.go already calls this with value 3 and never
	// hit this; mk8-secure simply never called it at all.
	globals.NEXServer.SetPRUDPProtocolMinorVersion(3)

	// nex-go's default PingTimeout is 5s (server.go's NewServer default), and its
	// StartTimeoutTimer kicks a client that goes quiet for 2x that (5s to send an
	// unsolicited ping, another 5s before Kick) - observed live 2026-11-04: a host's
	// connectionID jumped mid-match (15 -> 17, i.e. kicked and reconnected) during the
	// several-second gaps between NAT traversal round trips, leaving the joiner's
	// RequestProbeInitiationExt targeting a station URL whose connection no longer
	// existed ("found=false" relaying InitiateProbe) - the server-side cause of
	// "could not connect to partner's device". WSC hit the same vendor timer issue
	// (see feedback_wsc_ping_timeout in project memory) and disabled it the same way.
	globals.NEXServer.SetPingTimeout(3600)

	globals.NEXServer.On("Data", func(packet *nex.PacketV1) {
		request := packet.RMCRequest()

		fmt.Println("==MK8 - Secure==")
		fmt.Printf("Protocol ID: %#v\n", request.ProtocolID())
		fmt.Printf("Method ID: %#v\n", request.MethodID())
		fmt.Println("=================")
	})

	globals.NEXServer.On("Connect", prudp.Connect)

	// Diagnostic: find out whether the console's mid-race disconnect ever reaches us
	// as an actual DisconnectPacket/Kick (and if so, from which PID/connectionID) or
	// is purely client-side with no corresponding server-side event at all.
	globals.NEXServer.On("Disconnect", func(packet *nex.PacketV1) {
		client := packet.Sender()
		fmt.Printf("EVENT Disconnect: PID=%d connectionID=%d address=%v\n", client.PID(), client.ConnectionID(), client.Address())
		handlePlayerGone(client.PID())
	})
	globals.NEXServer.On("Kick", func(packet *nex.PacketV1) {
		client := packet.Sender()
		fmt.Printf("EVENT Kick: PID=%d connectionID=%d address=%v\n", client.PID(), client.ConnectionID(), client.Address())
	})
	// Fires for every packet type (acks included), after the type-specific handler
	// above - the same signal IncreasePingTimeoutTime already resets on. Feeds
	// watchStaleSessions below; see globals.LastPacketAt's own doc comment for why
	// this exists alongside the Disconnect handler instead of replacing it.
	globals.NEXServer.On("Packet", func(packet *nex.PacketV1) {
		if pid := packet.Sender().PID(); pid != 0 {
			globals.LastPacketAt.Store(pid, time.Now())
		}
	})

	registerNEXProtocols()

	go p2p.Janitor()
	go startStatusServer()
	go clearInactiveRoomsHourly()
	go watchStaleSessions()

	// nex-go's Listen() never raises the UDP socket's receive buffer above the OS
	// default (net.core.rmem_default, ~208KB on this box), so a burst of traffic
	// while this process is briefly busy elsewhere can get silently dropped by the
	// kernel before our code ever sees it - invisible to every log line here, and
	// a plausible cause of "random" in-race disconnects. Fixed for wsc-secure
	// 2026-09-15 the same way; applying it here too (see feedback_wsc_udp_buffer_overflow
	// in project memory). Poll for the socket because Listen() creates it internally
	// and blocks immediately after - there's no earlier hook, so this must run
	// concurrently from another goroutine right after the socket exists.
	go func() {
		for i := 0; i < 200; i++ {
			if sock := globals.NEXServer.Socket(); sock != nil {
				const udpBufSize = 64 * 1024 * 1024 // 64MB - rmem_max/wmem_max raised to 512MB 2026-09-15
				if err := sock.SetReadBuffer(udpBufSize); err != nil {
					fmt.Println("mk8-secure: SetReadBuffer failed:", err)
				} else {
					fmt.Printf("mk8-secure: UDP read buffer set to %d bytes\n", udpBufSize)
				}
				if err := sock.SetWriteBuffer(udpBufSize); err != nil {
					fmt.Println("mk8-secure: SetWriteBuffer failed:", err)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		fmt.Println("mk8-secure: PRUDP socket never became ready - could not raise UDP buffer size")
	}()

	globals.NEXServer.Listen(":" + globals.Config.ServerPort)
}
