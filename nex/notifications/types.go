package nex_notifications

import (
	nex "github.com/PretendoNetwork/nex-go"
)

// ProtocolID is the protocol ID for the Notifications protocol (confirmed against
// nex-protocols-go/v2's notifications/protocol.go - this vendored v1 copy predates
// that package existing at all, which is why mk8-secure never had push notification
// support despite the real NEX protocol having it since day one).
const ProtocolID = 0xE

// MethodProcessNotificationEvent is the method ID for ProcessNotificationEvent
const MethodProcessNotificationEvent = 0x1

// NotificationCategoryParticipationEvent is delivered when a user's participation in
// a gathering changes (join/leave/disconnect). See ParticipationEvents for subtypes.
const NotificationCategoryParticipationEvent = 3

// ParticipationEventsParticipate is delivered to the OTHER participants already in a
// gathering when a new player joins it.
const ParticipationEventsParticipate = 1

// BuildParticipationEventType builds the NotificationEvent.Type for a
// NotificationCategoryParticipationEvent/ParticipationEventsParticipate event, per
// NotificationCategory.Build() in the real protocol: category*1000 + subtype.
const NotificationTypeParticipationEventParticipate = NotificationCategoryParticipationEvent*1000 + ParticipationEventsParticipate

// NotificationEvent is the sole parameter of Notifications::ProcessNotificationEvent.
// Field set matches NEX 3.4-3.x wire format (Param3 added in 3.4, MapParam is 4.0+
// only and mk8-secure is pinned to NEX 3.5.4 - see nex/server.go's SetDefaultNEXVersion).
type NotificationEvent struct {
	PIDSource uint32
	Type      uint32
	Param1    uint32
	Param2    uint32
	StrParam  string
	Param3    uint32

	*nex.Structure
}

// Hierarchy returns the Structure hierarchy (NotificationEvent has none - it's flat)
func (event *NotificationEvent) Hierarchy() []nex.StructureInterface {
	return make([]nex.StructureInterface, 0)
}

// ExtractFromStream extracts a NotificationEvent structure from a stream
func (event *NotificationEvent) ExtractFromStream(stream *nex.StreamIn) error {
	var err error

	event.PIDSource = stream.ReadUInt32LE()
	event.Type = stream.ReadUInt32LE()
	event.Param1 = stream.ReadUInt32LE()
	event.Param2 = stream.ReadUInt32LE()
	event.StrParam, err = stream.ReadString()
	if err != nil {
		return err
	}
	event.Param3 = stream.ReadUInt32LE()

	return nil
}

// Bytes encodes the NotificationEvent and returns a byte array
func (event *NotificationEvent) Bytes(stream *nex.StreamOut) []byte {
	stream.WriteUInt32LE(event.PIDSource)
	stream.WriteUInt32LE(event.Type)
	stream.WriteUInt32LE(event.Param1)
	stream.WriteUInt32LE(event.Param2)
	stream.WriteString(event.StrParam)
	stream.WriteUInt32LE(event.Param3)

	return stream.Bytes()
}

// NewNotificationEvent returns a new NotificationEvent
func NewNotificationEvent() *NotificationEvent {
	return &NotificationEvent{Structure: &nex.Structure{}}
}
