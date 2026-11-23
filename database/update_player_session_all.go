package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// UpdatePlayerSessionAll also refreshes connected_at - a reconnect (existingSession=true)
// is still a fresh PRUDP connection, not a continuation of the old one, so the dashboard's
// "connected for" timer should restart here too, not just on a brand-new AddPlayerSession.
func UpdatePlayerSessionAll(pid uint32, urls []string, ip string, port string) {
	_, err := sessionsCollection.UpdateOne(context.TODO(), bson.D{{"pid", pid}}, bson.D{{"$set", bson.D{{"pid", pid}, {"urls", urls}, {"ip", ip}, {"port", port}, {"connected_at", time.Now().Unix()}}}})
	if err != nil {
		panic(err)
	}
}
