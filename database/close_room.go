package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
)

// CloseRoom marks a room as closed so FindRoom stops matching new searchers into it - used
// when a gathering has failed to connect its joiners repeatedly (see matchsetup package).
func CloseRoom(gid uint32) {
	_, err := roomsCollection.UpdateOne(context.TODO(), bson.D{{"gid", gid}}, bson.D{{"$set", bson.D{{"closed", true}}}})
	if err != nil {
		panic(err)
	}
}
