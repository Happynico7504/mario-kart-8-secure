// Package p2p is mk8-secure's client for the shared relayhub P2P tunnel
// (relaylink/p2p.go + relayhub/p2p.go), already used by wsc-secure. A console whose P2P can't
// reach its partner directly (CGNAT, a long international path) is told its partner lives at a
// UDP tunnel the hub opened, instead of the real address - the tunnel forwards packets
// untouched.
//
// This is simpler than wsc-secure's version: WSC's P2P traffic is PIA, which embeds peer
// addresses inside the game packets themselves, so the tunnel and wsc-secure both have to
// rewrite and re-sign those in-band addresses for 3+ players. MK8's P2P is plain PRUDP between
// consoles - every address a console learns about another one comes from an RMC call this
// server already controls (GetSessionURLs, RequestProbeInitiationExt's InitiateProbe), so
// handing out the tunnel's address there is the whole job. Nothing inside a P2P packet needs
// touching.
package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/PretendoNetwork/mario-kart-8-secure/database"
	"github.com/PretendoNetwork/mario-kart-8-secure/matchsetup"
	nex "github.com/PretendoNetwork/nex-go"
)

// hubURL is the relayhub's local HTTP endpoint - same one wsc-secure talks to, since the hub's
// P2P router is already game-agnostic (keyed by an arbitrary string, see Key below). Override
// with MK8_P2P_HUB_URL if mk8-secure ever runs on a different box than the hub.
var hubURL = "http://127.0.0.1:9401"

func init() {
	if v := os.Getenv("MK8_P2P_HUB_URL"); v != "" {
		hubURL = v
	}
}

var httpClient = &http.Client{Timeout: 1500 * time.Millisecond}

type station struct {
	PID  uint32 `json:"pid"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
	Hard string `json:"hard,omitempty"`
}

type tunnel struct {
	Instance string         `json:"instance"`
	IP       string         `json:"ip"`
	Ports    map[uint32]int `json:"ports"`
	Reason   string         `json:"reason"`
	at       time.Time
}

// tunnels holds the gatherings that currently have a tunnel (gid -> *tunnel). A gathering
// asked for one once and refused stays direct - its consoles already know each other's real
// addresses, and introducing a tunnel partway through would just confuse them.
var tunnels sync.Map

func key(gid uint32) string { return "mk8:" + strconv.FormatUint(uint64(gid), 10) }

// stationFor describes a console as the hub needs it: its secure-connection source address
// (for region placement) and its public (type=3) station URL's port (where its P2P traffic
// comes from).
func stationFor(pid uint32) (station, bool) {
	urls := database.GetPlayerURLs(pid)
	if len(urls) == 0 {
		return station{}, false
	}

	st := station{PID: pid}
	if reason := matchsetup.Reason(pid); reason != "" {
		st.Hard = reason
	}
	for _, u := range urls {
		su := nex.NewStationURL(u)
		if su.Type() != "3" {
			continue
		}
		st.IP = su.Address()
		if port, err := strconv.Atoi(su.Port()); err == nil {
			st.Port = port
		}
	}
	if net.ParseIP(st.IP) == nil {
		return station{}, false
	}
	return st, true
}

// Open asks the hub for gid's tunnel, adding host and any other current room members it
// doesn't have yet. existingOnly: only extend a tunnel the gathering already has, never
// create one - used once its consoles may already have been told each other's real addresses
// (a later RequestProbeInitiationExt call), so introducing a tunnel then would leave them
// split between two different ideas of where their partner lives.
func Open(gid, host uint32, existingOnly bool) *tunnel {
	if gid == 0 {
		return nil
	}
	old, had := tunnels.Load(gid)
	if existingOnly && !had {
		return nil
	}

	members := database.GetRoomPlayerPIDs(gid)
	ordered := []uint32{host}
	for _, pid := range members {
		if pid != host {
			ordered = append(ordered, pid)
		}
	}

	var stations []station
	for _, pid := range ordered {
		if st, ok := stationFor(pid); ok {
			stations = append(stations, st)
		}
	}
	if len(stations) < 2 && !had {
		return nil
	}

	body, _ := json.Marshal(map[string]any{"key": key(gid), "stations": stations})
	resp, err := httpClient.Post(hubURL+"/p2p/open", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("P2PTunnel: gid=%d hub unreachable: %v\n", gid, err)
		if had {
			return old.(*tunnel)
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if had {
			return old.(*tunnel)
		}
		return nil
	}

	var t tunnel
	if json.NewDecoder(resp.Body).Decode(&t) != nil || net.ParseIP(t.IP) == nil {
		return nil
	}
	t.at = time.Now()
	tunnels.Store(gid, &t)
	if !had {
		var hard []string
		for _, s := range stations {
			if s.Hard != "" {
				hard = append(hard, fmt.Sprintf("%d: %s", s.PID, s.Hard))
			}
		}
		fmt.Printf("P2PTunnel: gid=%d via %s %s ports=%v (%s) hard=%v\n", gid, t.Instance, t.IP, t.Ports, t.Reason, hard)
	}
	return &t
}

// Close tells the hub to drop gid's tunnel. Not required for correctness - the hub closes
// idle tunnels (90s) and caps their lifetime (4h) on its own - but tidy to call once a match
// is known to be over.
func Close(gid uint32) {
	tunnels.Delete(gid)
	body, _ := json.Marshal(map[string]any{"key": key(gid)})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, hubURL+"/p2p/close", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := httpClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Alias rewrites a station URL to pid's alias address on gid's tunnel, if it has one. The
// console's real natm/natf stay in the URL - only the address/port the other side sends to
// changes - exactly as wsc-secure's p2pAlias does, for the same reason: both sides still
// behave as they would in a direct connection, just sending to the tunnel instead.
func Alias(urlStr string, t *tunnel, pid uint32) (string, bool) {
	if t == nil {
		return urlStr, false
	}
	port := t.Ports[pid]
	if port == 0 {
		return urlStr, false
	}
	u := nex.NewStationURL(urlStr)
	u.SetAddress(t.IP)
	u.SetPort(strconv.Itoa(port))
	return u.EncodeToString(), true
}

// ConnectionFor reports whether gid currently has an open tunnel, and which instance it's on
// ("main" or a relay id) - used by the /status endpoint for the relay-admin dashboard.
func ConnectionFor(gid uint32) (via string, ok bool) {
	v, had := tunnels.Load(gid)
	if !had {
		return "", false
	}
	return v.(*tunnel).Instance, true
}

// Janitor forgets tunnels of gatherings that are long gone - the tunnel hosts close idle
// tunnels themselves, this just keeps this process's own map from growing forever.
func Janitor() {
	for range time.Tick(time.Minute) {
		tunnels.Range(func(k, v any) bool {
			if time.Since(v.(*tunnel).at) > 4*time.Hour {
				tunnels.Delete(k)
			}
			return true
		})
	}
}
