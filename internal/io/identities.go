package io

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Identity is a known person who interacts with Eve. The owner identity is
// the user; other identities are third parties Eve may converse with on
// shared channels (email threads, matrix rooms). Each identity maps one or
// more {channel, address} pairs, resolved from inbound message provenance.
type Identity struct {
	Name     string            `json:"name"`
	Owner    bool              `json:"owner,omitempty"`
	Channels []IdentityChannel `json:"channels"`
}

type IdentityChannel struct {
	Type    ChannelType `json:"type"`
	Address string      `json:"address"`
}

// identitiesFile is the on-disk format of DATA_DIR/identities.json.
type identitiesFile struct {
	Identities []Identity `json:"identities"`
}

// Resolver maps inbound {channel, address} provenance to identities. Unknown
// senders resolve to a generic "other" identity.
type Resolver struct {
	mu     sync.RWMutex
	byKey  map[string]string // "type/address" -> identity name
	byName map[string]*Identity
	owner  *Identity
	other  Identity
}

func LoadResolver(dir string) (*Resolver, error) {
	r := &Resolver{
		byKey:  make(map[string]string),
		byName: make(map[string]*Identity),
		other: Identity{
			Name:  "other",
			Owner: false,
		},
	}
	data, err := os.ReadFile(filepath.Join(dir, "identities.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No identities configured: single owner with the web channel.
			r.owner = &Identity{Name: "owner", Owner: true, Channels: []IdentityChannel{{Type: ChannelWeb, Address: "owner"}}}
			r.byName["owner"] = r.owner
			r.byKey[key(ChannelWeb, "owner")] = "owner"
			return r, nil
		}
		return nil, fmt.Errorf("read identities.json: %w", err)
	}
	var f identitiesFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse identities.json: %w", err)
	}
	for i := range f.Identities {
		id := &f.Identities[i]
		if id.Name == "" {
			continue
		}
		r.byName[id.Name] = id
		if id.Owner && r.owner == nil {
			r.owner = id
		}
		for _, c := range id.Channels {
			if c.Address == "" {
				continue
			}
			r.byKey[key(c.Type, c.Address)] = id.Name
		}
	}
	if r.owner == nil {
		r.owner = &Identity{Name: "owner", Owner: true}
		r.byName["owner"] = r.owner
	}
	return r, nil
}

func key(t ChannelType, address string) string {
	return string(t) + "/" + address
}

// Resolve returns the identity for a given {channel, address}. Unknown
// senders resolve to a generic "other" identity.
func (r *Resolver) Resolve(t ChannelType, address string) *Identity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name, ok := r.byKey[key(t, address)]; ok {
		if id, ok := r.byName[name]; ok {
			return id
		}
	}
	o := r.other
	return &o
}

// ResolveName returns the identity by name, or nil.
func (r *Resolver) ResolveName(name string) *Identity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byName[name]
}

// Owner returns the owner identity.
func (r *Resolver) Owner() *Identity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.owner == nil {
		o := r.other
		return &o
	}
	return r.owner
}

// IsOwner reports whether the given identity name is the owner.
func (r *Resolver) IsOwner(name string) bool {
	return r.Owner().Name == name
}
