package io

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityAddressesAreUniqueAndChannelScoped(t *testing.T) {
	r, err := LoadResolver(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := Identity{Name: "owner", Owner: true, Channels: []IdentityChannel{{Type: ChannelWeb, Address: "owner"}, {Type: ChannelMatrix, Address: "@owner:test"}}}
	if err := r.Upsert(owner); err != nil {
		t.Fatal(err)
	}
	if !r.Resolve(ChannelMatrix, "@owner:test").Owner || r.Resolve(ChannelMatrix, "owner").Owner {
		t.Fatal("identity name was accepted as a channel address")
	}
	copy := r.Owner()
	copy.Name = "altered"
	copy.Channels[0].Address = "altered"
	if r.Owner().Name != "owner" || !r.Resolve(ChannelWeb, "owner").Owner {
		t.Fatal("an identity snapshot changed the resolver")
	}
	if err := r.Upsert(Identity{Name: "contact", Channels: []IdentityChannel{{Type: ChannelMatrix, Address: "@owner:test"}}}); err == nil {
		t.Fatal("another identity claimed the owner's channel address")
	}
	if !r.Resolve(ChannelMatrix, "@owner:test").Owner || r.ResolveName("contact") != nil {
		t.Fatal("rejected channel claim changed the resolver")
	}
	if err := r.Upsert(Identity{Name: "owner", Owner: false}); err == nil {
		t.Fatal("owner was demoted without a replacement")
	}
}

func TestIdentityWriteFailureDoesNotChangeResolution(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "identities.json.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	contact := Identity{Name: "contact", Channels: []IdentityChannel{{Type: ChannelEmail, Address: "contact@example.test"}}}
	if err := r.Upsert(contact); err == nil {
		t.Fatal("expected identity save to fail")
	}
	if r.ResolveName("contact") != nil || r.Resolve(ChannelEmail, "contact@example.test").Name != "other" {
		t.Fatal("failed identity write changed in-memory resolution")
	}
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(contact); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("contact"); err == nil {
		t.Fatal("expected delete save to fail")
	}
	if r.Resolve(ChannelEmail, "contact@example.test").Name != "contact" {
		t.Fatal("failed delete removed a live channel address")
	}
}

func TestIdentityLoaderRejectsAmbiguousAddresses(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"identities":[{"name":"a","owner":true,"channels":[{"type":"email","address":"same@example.test"}]},{"name":"b","channels":[{"type":"email","address":"same@example.test"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "identities.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResolver(dir); err == nil {
		t.Fatal("ambiguous identity file loaded")
	}
}
