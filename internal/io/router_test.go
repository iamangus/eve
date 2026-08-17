package io

import "testing"

func TestHasNonOwnerTreatsLegacyOwnerAsCurrentOwner(t *testing.T) {
	router := &Router{ident: &Resolver{owner: &Identity{Name: "Angus", Owner: true}}}

	if router.hasNonOwner([]string{"owner"}) {
		t.Fatal("legacy owner participant was treated as a non-owner")
	}
	if router.hasNonOwner([]string{"Angus"}) {
		t.Fatal("current owner participant was treated as a non-owner")
	}
	if !router.hasNonOwner([]string{"other"}) {
		t.Fatal("other participant was not treated as a non-owner")
	}
}
