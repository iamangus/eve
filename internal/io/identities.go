package io

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	dir    string
	byKey  map[string]string // "type/address" -> identity name
	byName map[string]*Identity
	owner  *Identity
	other  Identity
}

func LoadResolver(dir string) (*Resolver, error) {
	r := &Resolver{
		dir:    dir,
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

// List returns a copy of all configured identities, sorted by name.
func (r *Resolver) List() []Identity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Identity, 0, len(r.byName))
	for _, id := range r.byName {
		out = append(out, *id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Upsert creates or replaces an identity and persists the change. Identity
// names are immutable and act as the map key, so a rename is expressed as a
// delete + create. At most one identity may own a given {type,address}; when
// an identity is marked owner it demotes any previous owner. The reserved
// "other" name is rejected.
func (r *Resolver) Upsert(in Identity) error {
	if in.Name == "" {
		return errors.New("identity name required")
	}
	if in.Name == "other" {
		return errors.New("reserved identity name: other")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	channels := make([]IdentityChannel, 0, len(in.Channels))
	for _, c := range in.Channels {
		if c.Address == "" {
			continue
		}
		channels = append(channels, c)
	}

	if existing, ok := r.byName[in.Name]; ok {
		// Remove stale provenance keys before applying the new channel set.
		for _, c := range existing.Channels {
			delete(r.byKey, key(c.Type, c.Address))
		}
		if in.Owner {
			r.demoteOwnerLocked()
			existing.Owner = true
			r.owner = existing
		} else {
			existing.Owner = false
			if r.owner != nil && r.owner.Name == in.Name {
				r.owner = nil
			}
		}
		existing.Channels = channels
		for _, c := range channels {
			r.byKey[key(c.Type, c.Address)] = in.Name
		}
		return r.saveLocked()
	}

	// New identity.
	id := &Identity{Name: in.Name, Channels: channels}
	if in.Owner {
		r.demoteOwnerLocked()
		id.Owner = true
		r.owner = id
	}
	r.byName[id.Name] = id
	for _, c := range channels {
		r.byKey[key(c.Type, c.Address)] = id.Name
	}
	return r.saveLocked()
}

// Delete removes an identity and persists the change. The owner identity and
// the reserved "other" fallback cannot be deleted.
func (r *Resolver) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "other" {
		return errors.New("reserved identity name: other")
	}
	id, ok := r.byName[name]
	if !ok {
		return fmt.Errorf("identity not found: %s", name)
	}
	if id.Owner {
		return errors.New("cannot delete the owner identity")
	}
	for _, c := range id.Channels {
		delete(r.byKey, key(c.Type, c.Address))
	}
	delete(r.byName, name)
	return r.saveLocked()
}

// demoteOwnerLocked clears the owner flag on the current owner. Caller must
// hold r.mu.
func (r *Resolver) demoteOwnerLocked() {
	if r.owner != nil {
		r.owner.Owner = false
		r.owner = nil
	}
}

// saveLocked writes the current identities to disk atomically. Caller must
// hold r.mu.
func (r *Resolver) saveLocked() error {
	list := make([]Identity, 0, len(r.byName))
	for _, id := range r.byName {
		list = append(list, *id)
	}
	data, err := json.MarshalIndent(identitiesFile{Identities: list}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identities.json: %w", err)
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return fmt.Errorf("ensure data dir: %w", err)
	}
	path := filepath.Join(r.dir, "identities.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write identities.json tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename identities.json: %w", err)
	}
	return nil
}
