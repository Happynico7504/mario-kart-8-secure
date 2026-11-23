package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func AddPlayerSession(pid uint32, urls []string, ip string, port string) {
	_, err := sessionsCollection.InsertOne(context.TODO(), bson.D{{"pid", pid}, {"urls", urls}, {"ip", ip}, {"port", port}, {"connected_at", time.Now().Unix()}})
	if err != nil {
		panic(err)
	}
}
