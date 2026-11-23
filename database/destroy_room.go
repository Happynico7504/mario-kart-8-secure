package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// DestroyRoom archives the room into room_history (see RecordRoomHistory) before actually
// deleting it, so every path that destroys a room - the inactive-room sweep, a room emptying
// out as its last player leaves (RemovePlayerFromRoom), any future caller - moves it to the
// dashboard's history section instead of just making it vanish. A gid that's already gone
// (double-destroy, e.g. two cleanup paths racing on the same empty room) is a no-op: there's
// nothing left to archive and nothing left to delete.
func DestroyRoom(gid uint32) {
	var d bson.M
	if err := roomsCollection.FindOne(context.TODO(), bson.D{{"gid", gid}}).Decode(&d); err == nil {
		info := RoomInfo{
			GID:         uint32(bsonInt(d["gid"])),
			Host:        uint32(bsonInt(d["host"])),
			GameMode:    uint32(bsonInt(d["gamemode"])),
			Region:      uint32(bsonInt(d["region"])),
			PlayerCount: bsonInt(d["player_count"]),
		}
		if t, ok := d["created_at"].(primitive.DateTime); ok {
			info.CreatedAt = t.Time()
		} else {
			info.CreatedAt = time.Now()
		}
		if raw, ok := d["players"].(bson.A); ok {
			for _, p := range raw {
				if v := bsonInt(p); v != 0 {
					if v < 0 {
						v = -v
					}
					info.Players = append(info.Players, uint32(v))
				}
			}
		}
		RecordRoomHistory(info)
	}

	_, err := roomsCollection.DeleteOne(context.TODO(), bson.D{{"gid", gid}})
	if err != nil {
		panic(err)
	}
}
