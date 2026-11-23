package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetRoomPlayerCount returns 0 for a gid that no longer exists instead of panicking -
// confirmed live 2026-11-15 crashing the whole process: sendSelfHeartbeat polls this on
// a timer for up to 10 minutes after a solo host's search, and ClearInactiveRooms (or
// any other room deletion) can legitimately destroy that room out from under it before
// the timer ends. A missing room has 0 players, same as any other room nobody's in.
func GetRoomPlayerCount(gid uint32) uint32 {
	var result bson.M

	err := roomsCollection.FindOne(context.TODO(), bson.D{{"gid", gid}}, options.FindOne()).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return 0
		}
		panic(err)
	}

	return uint32(result["player_count"].(int64))
}
