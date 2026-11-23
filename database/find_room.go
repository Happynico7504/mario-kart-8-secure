package database

import (
	"context"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// roomStaleAfter bounds how old a room FindRoom will still match. Rooms are never
// deleted, so without this, two players searching with the same (near-always-zero,
// since a plain search sends empty criteria) gamemode/region/gameconfig/dlcmode would
// match against every room anyone has ever created - with no sort, Mongo can hand back
// a different stale room on each call, so the two players' searches land in different
// long-dead rooms instead of each other's live one and never find a real partner.
const roomStaleAfter = 2 * time.Minute

func FindRoom(gamemode uint32, public bool, region uint32, gameconfig uint32, vacantcount uint32, dlcmode uint32) uint32 {
	var result bson.M
	maxplayersinroom := 12 - vacantcount
	filter := bson.D{
		{"gamemode", gamemode},
		{"public", public},
		{"region", region},
		{"gameconfig", gameconfig},
		{"player_count", bson.D{{"$lte", maxplayersinroom}}},
		{"dlc_mode", dlcmode},
		{"created_at", bson.D{{"$gte", time.Now().Add(-roomStaleAfter)}}},
		{"closed", bson.D{{"$ne", true}}}}

	err := roomsCollection.FindOne(context.TODO(), filter, options.FindOne().SetSort(bson.D{{"created_at", -1}})).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return math.MaxUint32
		} else {
			panic(err)
		}
	} else {
		return uint32(result["gid"].(int64))
	}
}
