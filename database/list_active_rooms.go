package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// RoomInfo describes one room for the /status endpoint (relay-admin's /mk8-public dashboard).
type RoomInfo struct {
	GID         uint32
	Host        uint32
	GameMode    uint32
	Region      uint32
	PlayerCount int64
	Players     []uint32
	CreatedAt   time.Time
	Closed      bool
}

// ListActiveRooms returns every room created within the last hour (older ones are just
// matchmaking history nobody will reconnect to - see roomStaleAfter for the much shorter
// window FindRoom itself uses).
func ListActiveRooms() []RoomInfo {
	cur, err := roomsCollection.Find(context.TODO(), bson.D{{"created_at", bson.D{{"$gte", time.Now().Add(-time.Hour)}}}})
	if err != nil {
		return nil
	}
	defer cur.Close(context.TODO())

	var rooms []RoomInfo
	for cur.Next(context.TODO()) {
		var d bson.M
		if cur.Decode(&d) != nil {
			continue
		}
		r := RoomInfo{
			GID:         uint32(bsonInt(d["gid"])),
			Host:        uint32(bsonInt(d["host"])),
			GameMode:    uint32(bsonInt(d["gamemode"])),
			Region:      uint32(bsonInt(d["region"])),
			PlayerCount: bsonInt(d["player_count"]),
		}
		r.Closed, _ = d["closed"].(bool)
		if t, ok := d["created_at"].(primitive.DateTime); ok {
			r.CreatedAt = t.Time()
		}
		if raw, ok := d["players"].(bson.A); ok {
			for _, p := range raw {
				if v := bsonInt(p); v != 0 {
					if v < 0 {
						v = -v
					}
					r.Players = append(r.Players, uint32(v))
				}
			}
		}
		rooms = append(rooms, r)
	}
	return rooms
}

func bsonInt(v interface{}) int64 {
	switch x := v.(type) {
	case int32:
		return int64(x)
	case int64:
		return x
	default:
		return 0
	}
}
