package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TryGetRoomHost returns a room's host PID, or (0, false) if the room no longer exists -
// unlike GetRoomInfo, which panics in that case. Used from contexts (like a disconnect
// handler firing after the room may already be gone) where a missing room is routine, not
// an error worth crashing the process over.
func TryGetRoomHost(gid uint32) (uint32, bool) {
	var result bson.M

	err := roomsCollection.FindOne(context.TODO(), bson.D{{"gid", gid}}, options.FindOne()).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return 0, false
		}
		panic(err)
	}

	return uint32(result["host"].(int64)), true
}
