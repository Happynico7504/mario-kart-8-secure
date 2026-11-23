package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
)

type SessionInfo struct {
	PID         uint32
	ConnectedAt int64
}

// GetAllSessions reads the persistent sessions collection (Mongo-backed, cleaned up on
// Register and Disconnect - see nex/secure-connection/register.go and nex/server.go's
// Disconnect handler) instead of the in-memory-only globals.ConnectedPIDs map the /status
// dashboard used before. Same live data, but backed by a real store instead of a sync.Map
// nothing outside this process can see or that a restart wipes without a trace.
func GetAllSessions() []SessionInfo {
	cursor, err := sessionsCollection.Find(context.TODO(), bson.D{})
	if err != nil {
		return nil
	}
	defer cursor.Close(context.TODO())

	var sessions []SessionInfo
	for cursor.Next(context.TODO()) {
		var doc struct {
			PID         int64 `bson:"pid"`
			ConnectedAt int64 `bson:"connected_at"`
		}
		if err := cursor.Decode(&doc); err != nil {
			continue
		}
		sessions = append(sessions, SessionInfo{PID: uint32(doc.PID), ConnectedAt: doc.ConnectedAt})
	}
	return sessions
}
