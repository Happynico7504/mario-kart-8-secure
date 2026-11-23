package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// GetRoomPlayerPIDs returns the distinct, non-empty player PIDs currently assigned to
// a room's 12 slots. A slot holds a negative PID for a local guest player sharing the
// same console as that PID's owner (see AddPlayerToRoom), so abs() collapses both
// back to the single real connection that owns them.
func GetRoomPlayerPIDs(gid uint32) []uint32 {
	var result bson.M

	err := roomsCollection.FindOne(context.TODO(), bson.D{{"gid", gid}}, options.FindOne()).Decode(&result)
	if err != nil {
		panic(err)
	}

	playerList := result["players"].(bson.A)
	seen := make(map[uint32]bool)
	pids := make([]uint32, 0, len(playerList))
	for _, entry := range playerList {
		value := entry.(int64)
		if value == 0 {
			continue
		}
		if value < 0 {
			value = -value
		}
		pid := uint32(value)
		if !seen[pid] {
			seen[pid] = true
			pids = append(pids, pid)
		}
	}

	return pids
}
