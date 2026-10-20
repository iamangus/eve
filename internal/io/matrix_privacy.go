package io

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var ErrMatrixNotPrivate = errors.New("matrix room is not private")

func (c MatrixConfig) request(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Homeserver, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("matrix state lookup: %s", resp.Status)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(out)
}

func (c MatrixConfig) privateRoom(ctx context.Context, room string, resolver *Resolver) error {
	_, err := c.privateMembers(ctx, room, resolver)
	return err
}

func (c MatrixConfig) privateMembers(ctx context.Context, room string, resolver *Resolver) ([]id.UserID, error) {
	if resolver == nil || !strings.HasPrefix(room, "!") || c.UserID == "" {
		return nil, fmt.Errorf("matrix: private room identity is not configured")
	}
	var state struct {
		Joined map[string]json.RawMessage `json:"joined"`
	}
	if err := c.request(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/joined_members", &state); err != nil {
		return nil, err
	}
	if len(state.Joined) != 2 {
		return nil, fmt.Errorf("%w: room must contain exactly Eve and the owner", ErrMatrixNotPrivate)
	}
	if _, ok := state.Joined[c.UserID]; !ok {
		return nil, fmt.Errorf("%w: Eve is not a member", ErrMatrixNotPrivate)
	}
	for user := range state.Joined {
		if user != c.UserID && !resolver.Resolve(ChannelMatrix, user).Owner {
			return nil, fmt.Errorf("%w: other room member is not the owner", ErrMatrixNotPrivate)
		}
	}
	members := make([]id.UserID, 0, 2)
	for user := range state.Joined {
		members = append(members, id.UserID(user))
	}
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	return members, nil
}

func (e *MatrixE2EE) setPrivacyPolicy(cfg MatrixConfig, resolver *Resolver) {
	e.privateMembers = func(ctx context.Context, room id.RoomID) ([]id.UserID, error) {
		return cfg.privateMembers(ctx, room.String(), resolver)
	}
	defaultPolicy := e.mach.AllowKeyShare
	e.mach.AllowKeyShare = func(ctx context.Context, device *id.Device, info event.RequestedKeyInfo) *crypto.KeyShareRejection {
		members, err := e.privateMembers(ctx, info.RoomID)
		if err != nil {
			return &crypto.KeyShareRejectNoResponse
		}
		allowed := false
		for _, member := range members {
			if device.UserID == member {
				allowed = true
			}
		}
		if !allowed {
			return &crypto.KeyShareRejectNotRecipient
		}
		return defaultPolicy(ctx, device, info)
	}
}

func (c MatrixConfig) ownerRoom(ctx context.Context, resolver *Resolver) (string, error) {
	if c.OwnerRoom != "" {
		return c.OwnerRoom, c.privateRoom(ctx, c.OwnerRoom, resolver)
	}
	var joined struct {
		Rooms []string `json:"joined_rooms"`
	}
	if err := c.request(ctx, "/_matrix/client/v3/joined_rooms", &joined); err != nil {
		return "", err
	}
	sort.Strings(joined.Rooms)
	for _, room := range joined.Rooms {
		if c.privateRoom(ctx, room, resolver) == nil {
			return room, nil
		}
	}
	return "", fmt.Errorf("matrix: no room contains exactly Eve and the owner")
}
