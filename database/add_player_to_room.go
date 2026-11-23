package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func AddPlayerToRoom(gid uint32, pid uint32, addplayercount uint32) {
	var result bson.M

	err := roomsCollection.FindOne(context.TODO(), bson.D{{"gid", gid}}, options.FindOne()).Decode(&result)
	if err != nil {
		panic(err)
	}

	oldPlayerList := result["players"].(bson.A)
	newPlayerList := make([]int64, 12)
	for i := 0; i < 12; i++ {
		if oldPlayerList[i].(int64) == int64(pid) || oldPlayerList[i].(int64) == -1*int64(pid) {
			newPlayerList[i] = 0
		} else {
			newPlayerList[i] = oldPlayerList[i].(int64)
		}
	}
	unassignedPlayers := addplayercount
	needToAddGuest := (unassignedPlayers > 1)
	for i := 0; i < 12; i++ {
		if newPlayerList[i] == 0 && unassignedPlayers > 0 {
			if unassignedPlayers == 1 && needToAddGuest {
				newPlayerList[i] = -1 * int64(pid)
				needToAddGuest = false
			} else {
				newPlayerList[i] = int64(pid)
			}
			unassignedPlayers--
		}
	}

	// Used to be result["player_count"] + addplayercount - but a player who searches
	// again while already seated (confirmed live 2026-11-05: the same PID called
	// AutoMatchmakeWithSearchCriteria_Postpone 2-3 times over one session, each
	// re-stamping NAT info) has their old slot(s) cleared and re-added above with no
	// net change in occupancy, yet that arithmetic still added addplayercount on top
	// of the already-correct count every time - inflating it to 4-5 "players" in a
	// room that only ever had the 2 real consoles connected. Recount the actual slots
	// instead of trusting incremental arithmetic.
	var newplayercount int64
	for _, pid := range newPlayerList {
		if pid != 0 {
			newplayercount++
		}
	}

	_, err = roomsCollection.UpdateOne(context.TODO(), bson.D{{"gid", gid}}, bson.D{{"$set", bson.D{{"players", newPlayerList}, {"player_count", newplayercount}}}})
	if err != nil {
		panic(err)
	}
}
