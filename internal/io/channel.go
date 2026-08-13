package io

import "time"

// ChannelType identifies a communication medium. Adapters translate between
// the canonical message model and each medium's native protocol.
type ChannelType string

const (
	ChannelWeb    ChannelType = "web"
	ChannelEmail  ChannelType = "email"
	ChannelMatrix ChannelType = "matrix"
	ChannelSMS    ChannelType = "sms"
	ChannelVoice  ChannelType = "voice"
)

// Channel is a static description of a medium registered with the IO manager.
type Channel struct {
	ID               string      `json:"id"`
	Type             ChannelType `json:"type"`
	Name             string      `json:"name"`
	Input            bool        `json:"input"`
	Output           bool        `json:"output"`
	Streams          bool        `json:"streams"`
	RichText         bool        `json:"rich_text"`
	Reachable        bool        `json:"reachable"`
	DefaultRecipient string      `json:"default_recipient,omitempty"`
	Preference       int         `json:"preference"`
}

// Presence is the runtime state of a channel: whether the user is currently
// reachable on it and when we last saw activity. The web channel heartbeats;
// async channels rely on inbound activity timestamps.
type Presence struct {
	Connected    bool      `json:"connected"`
	LastActivity time.Time `json:"last_activity,omitempty"`
}

// EndpointSnapshot pairs a channel's static description with its runtime
// presence. This is what the router and the routing agent reason over.
type EndpointSnapshot struct {
	Channel
	Presence Presence `json:"presence"`
}

func (e EndpointSnapshot) ReachableNow() bool {
	return e.Reachable || e.Presence.Connected
}
