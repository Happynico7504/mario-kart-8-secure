package nex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/globals"
	"github.com/PretendoNetwork/mario-kart-8-secure/matchsetup"
	"github.com/PretendoNetwork/mario-kart-8-secure/p2p"
	nex "github.com/PretendoNetwork/nex-go"
)

// sessionInfo/roomInfo/shameInfo mirror wsc-secure's /status JSON shape (same field names
// relay-admin's dashboard already expects for WSC) so the mk8-public dashboard can reuse its
// rendering code, just pointed at a different status URL and a slightly different schema
// (gatherings here are "rooms": no club/friend-session concepts in MK8).
type sessionInfo struct {
	PID         int64 `json:"pid"`
	NATm        int64 `json:"natm"`
	ConnectedAt int64 `json:"connected_at"`
}

type roomInfo struct {
	GID         int64   `json:"gid"`
	Host        int64   `json:"host"`
	GameMode    int64   `json:"game_mode"`
	Region      int64   `json:"region"`
	PlayerCount int64   `json:"player_count"`
	Players     []int64 `json:"players"`
	CreatedAt   int64   `json:"created_at"`
	Closed      bool    `json:"closed"`
	Connection  string  `json:"connection,omitempty"` // "tunnel" or "direct"; empty while nobody has joined yet
	TunnelVia   string  `json:"tunnel_via,omitempty"`
}

type shameInfo struct {
	PID          int64  `json:"pid"`
	BlockedUntil int64  `json:"blocked_until"`
	Reason       string `json:"reason"`
}

type roomHistoryEntry struct {
	GID         int64   `json:"gid"`
	Host        int64   `json:"host"`
	GameMode    int64   `json:"game_mode"`
	Region      int64   `json:"region"`
	PlayerCount int64   `json:"player_count"`
	Players     []int64 `json:"players"`
	StartedAt   int64   `json:"started_at"`
	EndedAt     int64   `json:"ended_at"`
}

// natmFor reads a player's NAT mapping type off their stamped type=3 station URL, or 0 if
// they haven't been NAT-probed yet.
func natmFor(pid uint32) int64 {
	for _, u := range database.GetPlayerURLs(pid) {
		su := nex.NewStationURL(u)
		if su.Type() != "3" {
			continue
		}
		var natm int64
		fmt.Sscanf(su.Natm(), "%d", &natm)
		return natm
	}
	return 0
}

// roomInactive reports whether a room should be offered for cleanup: its host is no longer
// connected, or it has no players left. Age alone is deliberately NOT a criterion - a room
// with real players in it can and should last indefinitely (a long race session is not
// "stale" just because it's old).
func roomInactive(d database.RoomInfo) bool {
	if globals.NEXServer.FindClientFromPID(d.Host) == nil {
		return true
	}
	return d.PlayerCount == 0
}

// clearInactiveRooms destroys every non-closed room that roomInactive flags, logs what it
// did, and returns the cleared gids. Shared by the dashboard's manual button
// (/status/clear-inactive-rooms) and the hourly automatic sweep below.
func clearInactiveRooms() []uint32 {
	var cleared []uint32
	for _, d := range database.ListActiveRooms() {
		if d.Closed {
			continue
		}
		if roomInactive(d) {
			database.DestroyRoom(d.GID) // archives into room_history before deleting
			cleared = append(cleared, d.GID)
		}
	}
	if cleared == nil {
		cleared = []uint32{}
	}
	fmt.Printf("ClearInactiveRooms: cleared %d room(s): %v\n", len(cleared), cleared)
	return cleared
}

// clearInactiveRoomsHourly runs the same cleanup the dashboard's button triggers, on its
// own, so rooms left behind by players who never come back to the dashboard don't just
// accumulate until someone happens to click it.
func clearInactiveRoomsHourly() {
	for range time.Tick(time.Hour) {
		clearInactiveRooms()
	}
}

func startStatusServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/status/clear-inactive-rooms", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		cleared := clearInactiveRooms()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"cleared": cleared, "count": len(cleared)})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		// Sourced from the persistent sessions collection (Mongo-backed, cleaned up on
		// Register/Disconnect and by watchStaleSessions) instead of the in-memory-only
		// globals.ConnectedPIDs map - a sync.Map nothing outside this process could see
		// and that told you nothing once the process itself had restarted. Still gated
		// on FindClientFromPID here too: watchStaleSessions only polls every 10s with a
		// 60s staleness threshold, so a session doc can briefly lag a real disconnect by
		// up to a minute - this check is instant and authoritative, so the dashboard
		// itself never shows a player who isn't actually still connected right now, even
		// in that window.
		var sessions []sessionInfo
		for _, s := range database.GetAllSessions() {
			if globals.NEXServer.FindClientFromPID(s.PID) == nil {
				continue
			}
			sessions = append(sessions, sessionInfo{
				PID:         int64(s.PID),
				NATm:        natmFor(s.PID),
				ConnectedAt: s.ConnectedAt,
			})
		}
		if sessions == nil {
			sessions = []sessionInfo{}
		}

		var rooms []roomInfo
		for _, d := range database.ListActiveRooms() {
			ri := roomInfo{
				GID:         int64(d.GID),
				Host:        int64(d.Host),
				GameMode:    int64(d.GameMode),
				Region:      int64(d.Region),
				PlayerCount: d.PlayerCount,
				CreatedAt:   d.CreatedAt.Unix(),
				Closed:      d.Closed,
			}
			for _, p := range d.Players {
				ri.Players = append(ri.Players, int64(p))
			}
			if via, ok := p2p.ConnectionFor(d.GID); ok {
				ri.Connection, ri.TunnelVia = "tunnel", via
			} else if d.PlayerCount >= 2 {
				ri.Connection = "direct"
			}
			rooms = append(rooms, ri)
		}
		if rooms == nil {
			rooms = []roomInfo{}
		}

		shame := matchsetup.Shame()
		shameOut := make([]shameInfo, 0, len(shame))
		for _, s := range shame {
			shameOut = append(shameOut, shameInfo{PID: int64(s.PID), BlockedUntil: s.Until.Unix(), Reason: s.Reason})
		}

		var history []roomHistoryEntry
		for _, h := range database.GetRecentRoomHistory() {
			he := roomHistoryEntry{
				GID:         int64(h.GID),
				Host:        int64(h.Host),
				GameMode:    int64(h.GameMode),
				Region:      int64(h.Region),
				PlayerCount: h.PlayerCount,
				StartedAt:   h.StartedAt.Unix(),
				EndedAt:     h.EndedAt.Unix(),
			}
			for _, p := range h.Players {
				he.Players = append(he.Players, int64(p))
			}
			history = append(history, he)
		}
		if history == nil {
			history = []roomHistoryEntry{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sessions":                 sessions,
			"rooms":                    rooms,
			"connection_hall_of_shame": shameOut,
			"room_history":             history,
		})
	})
	if err := http.ListenAndServe("127.0.0.1:9016", mux); err != nil {
		fmt.Println("mk8-secure status server:", err)
	}
}
