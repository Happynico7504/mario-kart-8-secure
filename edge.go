package main

// MK8 edge support (main side). A player's PRUDP session can be terminated on a regional
// relay ("edge", see relayd/mk8edge): the relay does the Kerberos handshake, acks and
// keepalives locally and forwards complete RMC calls here. To the game logic such a player
// is an ordinary *nex.Client, except that everything the server would send to it is handed
// to a hook (nex-go Client.SetOutHook) instead of a UDP socket, so the protocol library's own
// replies are covered as well as mk8-secure's. Ported from wsc-secure/edge.go - see that
// file's doc comment and relayd/deploy/WSC-EDGE.md for the full design; this is the same
// architecture with MK8's own bookkeeping (globals.ConnectedPIDs, database.RemovePlayerFromRoom)
// in place of WSC's (connectedPIDs/currentClient/dbLeaveAllGatherings).
//
// Off unless MK8_EDGE=1 or the file mk8-secure/edge.enabled exists. Listens on loopback
// only; the relay hub is the sole caller.
//
//	hub -> here   POST /edge/open   {relay,pid,ip,port}
//	              POST /edge/rmc    {relay,pid,call,proto,method,params}
//	              POST /edge/alive  {pids}          (packets seen from these players lately)
//	              POST /edge/close  {relay,pid}
//	here -> hub   POST <hub>/mk8-edge/out {relay,pid,payload}   (an RMC message for the player)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	nex "github.com/PretendoNetwork/nex-go"
)

type edgeSession struct {
	created time.Time
	relay   string
	client  *nex.Client
	out     chan []byte // RMC messages for the player, sent in order by one goroutine
	done    chan struct{}
}

var (
	edgeSessions sync.Map // uint32 pid -> *edgeSession
	edgeHubURL   = "http://127.0.0.1:9401"
)

func edgeEnabled() bool {
	if os.Getenv("MK8_EDGE") == "1" {
		return true
	}
	_, err := os.Stat("edge.enabled")
	return err == nil
}

func startEdgeServer() {
	if !edgeEnabled() {
		return
	}
	if v := os.Getenv("MK8_EDGE_HUB"); v != "" {
		edgeHubURL = v
	}
	addr := os.Getenv("MK8_EDGE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:9453"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/edge/open", edgeHandler(handleEdgeOpen))
	mux.HandleFunc("/edge/rmc", edgeHandler(handleEdgeRMC))
	mux.HandleFunc("/edge/alive", edgeHandler(handleEdgeAlive))
	mux.HandleFunc("/edge/close", edgeHandler(handleEdgeClose))
	mux.HandleFunc("/edge/stats", edgeHandler(handleEdgeStats))
	mux.HandleFunc("/edge/trace", edgeHandler(handleEdgeTrace))
	fmt.Printf("Edge: accepting relay-terminated sessions on %s (hub %s)\n", addr, edgeHubURL)
	go edgeJanitor()
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			fmt.Printf("Edge: listener stopped: %v\n", err)
		}
	}()
}

type edgeMsg struct {
	Relay  string   `json:"relay"`
	PID    uint32   `json:"pid"`
	IP     string   `json:"ip"`
	Port   int      `json:"port"`
	Call   uint32   `json:"call"`
	Proto  uint8    `json:"proto"`
	Custom uint16   `json:"custom"`
	Method uint32   `json:"method"`
	Params []byte   `json:"params"`
	PIDs   []uint32 `json:"pids"`

	// connectivity measurements taken on the relay (see relaylink.WSCStats - the type is
	// reused as-is for MK8, see mk8-secure/p2p's doc comment on reusing generic relaylink types)
	Loss       string  `json:"loss"`
	AvgRTT     string  `json:"avg_rtt"`
	PRUDPRTTMs float64 `json:"prudp_rtt_ms"`
	PRUDPMinMs float64 `json:"prudp_min_ms"`
	Samples    int     `json:"samples"`

	Reason string `json:"reason"` // traceroute trigger
	Output string `json:"output"` // traceroute output
}

func edgeHandler(fn func(m *edgeMsg) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var m edgeMsg
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&m); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Printf("Edge: %s panicked: %v\n", r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		if err := fn(&m); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleEdgeOpen is the Connect handler for a session whose handshake happened on the edge
// (the relay already verified the Kerberos ticket).
func handleEdgeOpen(m *edgeMsg) error {
	ip := net.ParseIP(m.IP)
	if m.PID == 0 || ip == nil || m.Port <= 0 || m.Port > 65535 {
		return fmt.Errorf("bad open")
	}
	client := nex.NewClient(&net.UDPAddr{IP: ip, Port: m.Port}, globals.NEXServer)
	client.SetPID(m.PID)
	client.SetConnected(true)
	s := &edgeSession{created: time.Now(), relay: m.Relay, client: client, out: make(chan []byte, 256), done: make(chan struct{})}
	client.SetOutHook(func(p nex.PacketInterface) { s.deliver(p) })
	globals.NEXServer.RegisterClient(client)

	// A reconnect replaces the previous session for this PID (real or edge), like the
	// Connect handler does for a second connection.
	if old, ok := edgeSessions.Swap(m.PID, s); ok {
		old.(*edgeSession).close()
	}
	globals.ConnectedPIDs.Store(m.PID, time.Now())
	if gid := database.FindRoomForPID(m.PID); gid != 0 {
		database.RemovePlayerFromRoom(gid, m.PID) // clear any stale room from a previous session
	}
	go s.pump()
	fmt.Printf("Edge: connect PID=%d (edge %s, %s:%d)\n", m.PID, m.Relay, m.IP, m.Port)
	return nil
}

func handleEdgeRMC(m *edgeMsg) error {
	v, ok := edgeSessions.Load(m.PID)
	if !ok {
		return fmt.Errorf("no edge session for pid %d", m.PID)
	}
	s := v.(*edgeSession)
	if s.relay != m.Relay {
		return fmt.Errorf("pid %d belongs to another relay", m.PID)
	}
	req := nex.NewRMCRequest()
	req.SetProtocolID(m.Proto)
	req.SetCustomID(m.Custom)
	req.SetCallID(m.Call)
	req.SetMethodID(m.Method)
	req.SetParameters(m.Params)
	pkt, err := nex.NewPacketV1(s.client, nil)
	if err != nil {
		return err
	}
	pkt.SetVersion(1)
	pkt.SetType(nex.DataPacket)
	pkt.SetRMCRequest(req)
	globals.NEXServer.Emit("Data", pkt) // every registered handler, including the protocol library's
	return nil
}

func handleEdgeAlive(m *edgeMsg) error {
	for _, pid := range m.PIDs {
		if v, ok := edgeSessions.Load(pid); ok && v.(*edgeSession).relay == m.Relay {
			globals.ConnectedPIDs.Store(pid, time.Now())
		}
	}
	return nil
}

func handleEdgeClose(m *edgeMsg) error {
	v, ok := edgeSessions.Load(m.PID)
	if !ok {
		return nil
	}
	s := v.(*edgeSession)
	if s.relay != m.Relay {
		return fmt.Errorf("pid %d belongs to another relay", m.PID)
	}
	if !edgeSessions.CompareAndDelete(m.PID, s) {
		return nil // a newer session already replaced it
	}
	pkt, err := nex.NewPacketV1(s.client, nil)
	if err == nil {
		globals.NEXServer.Emit("Disconnect", pkt) // the normal cleanup (matchsetup.HandleDisconnect, ConnectedPIDs.Delete)
	}
	s.close()
	globals.NEXServer.UnregisterClient(s.client)
	return nil
}

func (s *edgeSession) close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// deliver receives what the server would have sent to the player. Only data packets carry
// an RMC message; anything else (a disconnect, an ack) means nothing to the edge.
func (s *edgeSession) deliver(p nex.PacketInterface) {
	if p.Type() != nex.DataPacket {
		return
	}
	payload := append([]byte(nil), p.Payload()...)
	select {
	case s.out <- payload:
	default:
		fmt.Printf("Edge: PID=%d outbound queue full, dropping a message\n", s.client.PID())
	}
}

// pump sends the session's messages to the hub in order, one at a time.
func (s *edgeSession) pump() {
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		select {
		case <-s.done:
			return
		case payload := <-s.out:
			body, _ := json.Marshal(struct {
				Relay   string `json:"relay"`
				PID     uint32 `json:"pid"`
				Payload []byte `json:"payload"`
			}{s.relay, s.client.PID(), payload})
			resp, err := client.Post(edgeHubURL+"/mk8-edge/out", "application/json", bytes.NewReader(body))
			if err != nil {
				fmt.Printf("Edge: PID=%d could not reach the hub: %v\n", s.client.PID(), err)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode >= 300 {
				fmt.Printf("Edge: PID=%d hub refused a message: %s\n", s.client.PID(), resp.Status)
			}
		}
	}
}

// edgeJanitor releases edge sessions nobody closed - the hub restarting is the main case
// (no close is ever sent then).
func edgeJanitor() {
	for range time.Tick(10 * time.Second) {
		edgeSessions.Range(func(k, v any) bool {
			pid, s := k.(uint32), v.(*edgeSession)
			if time.Since(s.created) < 30*time.Second {
				return true // just opened: give the registration time to settle
			}
			if at, ok := globals.ConnectedPIDs.Load(pid); ok && time.Since(at.(time.Time)) < 15*time.Second {
				return true // still getting alive reports
			}
			if edgeSessions.CompareAndDelete(pid, s) {
				s.close()
				globals.NEXServer.UnregisterClient(s.client)
				fmt.Printf("Edge: PID=%d released an orphaned session (%s)\n", pid, s.relay)
			}
			return true
		})
	}
}

// handleEdgeStats logs what a relay measured toward one of its players, in the same
// PlayerPing/PlayerRTT shape wsc-secure's direct-path logging uses, so one grep compares both.
func handleEdgeStats(m *edgeMsg) error {
	v, ok := edgeSessions.Load(m.PID)
	if !ok || v.(*edgeSession).relay != m.Relay {
		return fmt.Errorf("no edge session for pid %d on relay %s", m.PID, m.Relay)
	}
	via := "edge:" + m.Relay
	fmt.Printf("PlayerPing: PID=%d ip=%s loss=%s avgRTT=%s via=%s\n", m.PID, m.IP, m.Loss, m.AvgRTT, via)
	if m.Samples == 0 {
		fmt.Printf("PlayerRTT: PID=%d ip=%s via=%s prudp=? samples=0\n", m.PID, m.IP, via)
	} else {
		fmt.Printf("PlayerRTT: PID=%d ip=%s via=%s prudp=%.1fms min=%.1fms samples=%d\n", m.PID, m.IP, via, m.PRUDPRTTMs, m.PRUDPMinMs, m.Samples)
	}
	return nil
}

// handleEdgeTrace logs a traceroute the relay ran toward one of its players.
func handleEdgeTrace(m *edgeMsg) error {
	v, ok := edgeSessions.Load(m.PID)
	if !ok || v.(*edgeSession).relay != m.Relay {
		return fmt.Errorf("no edge session for pid %d on relay %s", m.PID, m.Relay)
	}
	fmt.Printf("PlayerTraceroute: PID=%d ip=%s reason=%s via=edge:%s\n%s", m.PID, m.IP, m.Reason, m.Relay, m.Output)
	return nil
}
