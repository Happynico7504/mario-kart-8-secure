package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// FindRoomForPID returns the gid of the room pid currently occupies a slot in (host or
// guest, either sign - see AddPlayerToRoom), or 0 if none.
func FindRoomForPID(pid uint32) uint32 {
	var result bson.M

	filter := bson.D{{"players", bson.D{{"$in", []int64{int64(pid), -int64(pid)}}}}}
	err := roomsCollection.FindOne(context.TODO(), filter, options.FindOne()).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return 0
		}
		panic(err)
	}

	return uint32(result["gid"].(int64))
}
