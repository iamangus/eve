package io

// InboundMessage is the canonical form of a message arriving from any channel
// adapter. Text is plain text; for audio/voice channels it is the STT
// transcript. ThreadRef carries the medium's threading identifier (email
// Message-ID, matrix room id, etc.) so replies can be threaded correctly.
type InboundMessage struct {
	Channel   ChannelType `json:"channel"`
	Sender    string      `json:"sender"` // identity name or raw address
	Text      string      `json:"text"`
	ThreadRef string      `json:"thread_ref,omitempty"`
}

// OutboundMessage is the canonical form of a message being delivered through
// a channel adapter. ConversationID identifies the owner conversation thread
// the message belongs to (web delivery appends it to the chat transcript).
type OutboundMessage struct {
	Channel        ChannelType `json:"channel"`
	Recipient      string      `json:"recipient,omitempty"`
	Text           string      `json:"text"`
	ThreadRef      string      `json:"thread_ref,omitempty"`
	ConversationID string      `json:"conversation_id,omitempty"`
}

// Purpose classifies why a message is being sent. The routing agent uses it
// to weigh urgency and channel suitability.
const (
	PurposeReply        = "reply"        // direct answer to a user message
	PurposeNotification = "notification" // background task/event completed
	PurposeReminder     = "reminder"     // scheduled reminder
	PurposeQuestion     = "question"     // Eve needs input from the user
)

// SendRequest describes an outgoing message that needs routing. Origin is the
// channel the triggering message came in on (empty for purely proactive
// sends). Participants lists identity names in the conversation; a
// non-owner participant mechanically pins the destination to the origin.
type SendRequest struct {
	ConversationID string
	Content        string
	Purpose        string
	Origin         ChannelType
	OriginThread   string
	Recipient      string // override of the destination's default recipient (e.g. the sender's email address)
	Participants   []string
	ForceChannel   string // explicit channel override (e.g. Eve answered a direct question on web)
}
