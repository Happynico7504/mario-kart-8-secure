package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// RecordRoomHistory snapshots a room into room_history right before it's destroyed, so a
// room that goes inactive is archived rather than simply vanishing - mirrors wsc-secure's
// dbRecordMatch/match_history, except MK8 has no CloseParticipation-equivalent "the race
// really started" signal to trigger on, so the natural MK8-side moment is "about to be
// cleared as inactive" instead (see clearInactiveRooms in nex/status_server.go).
func RecordRoomHistory(info RoomInfo) {
	players := make([]int64, 0, len(info.Players))
	for _, p := range info.Players {
		players = append(players, int64(p))
	}
	roomHistoryCollection.InsertOne(context.TODO(), bson.D{
		{"gid", int64(info.GID)},
		{"host", int64(info.Host)},
		{"gamemode", int64(info.GameMode)},
		{"region", int64(info.Region)},
		{"players", players},
		{"player_count", info.PlayerCount},
		{"started_at", info.CreatedAt.Unix()},
		{"ended_at", time.Now().Unix()},
	})
}

// RoomHistoryInfo describes one archived room for the /status endpoint's room_history list.
type RoomHistoryInfo struct {
	GID         uint32
	Host        uint32
	GameMode    uint32
	Region      uint32
	PlayerCount int64
	Players     []uint32
	StartedAt   time.Time
	EndedAt     time.Time
}

// GetRecentRoomHistory returns room_history documents from the last 24 hours, newest first -
// mirrors wsc-secure's dbGetRecentMatches (same window, same 200 cap).
func GetRecentRoomHistory() []RoomHistoryInfo {
	since := time.Now().Add(-24 * time.Hour).Unix()
	opts := options.Find().SetSort(bson.D{{"ended_at", -1}}).SetLimit(200)
	cur, err := roomHistoryCollection.Find(
		context.TODO(),
		bson.D{{"ended_at", bson.D{{"$gte", since}}}},
		opts,
	)
	if err != nil {
		return nil
	}
	defer cur.Close(context.TODO())

	var history []RoomHistoryInfo
	for cur.Next(context.TODO()) {
		var d bson.M
		if cur.Decode(&d) != nil {
			continue
		}
		h := RoomHistoryInfo{
			GID:         uint32(bsonInt(d["gid"])),
			Host:        uint32(bsonInt(d["host"])),
			GameMode:    uint32(bsonInt(d["gamemode"])),
			Region:      uint32(bsonInt(d["region"])),
			PlayerCount: bsonInt(d["player_count"]),
			StartedAt:   time.Unix(bsonInt(d["started_at"]), 0),
			EndedAt:     time.Unix(bsonInt(d["ended_at"]), 0),
		}
		if raw, ok := d["players"].(bson.A); ok {
			for _, p := range raw {
				if v := bsonInt(p); v != 0 {
					h.Players = append(h.Players, uint32(v))
				}
			}
		}
		history = append(history, h)
	}
	return history
}
