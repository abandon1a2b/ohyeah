package model

import "testing"

func TestIndexMetadataNormalizesOmittedCollections(t *testing.T) {
	a := MemoryUnit{ID: "x", ContentHash: "v1", Content: "full", Repos: []string{}, Metadata: map[string]string{}}
	b := MemoryUnit{ID: "x", ContentHash: "v1", Content: "excerpt"}
	if !EqualIndexMetadata(a, b) {
		t.Fatal("equivalent JSON metadata rejected")
	}
	b.ContentHash = "v2"
	if EqualIndexMetadata(a, b) {
		t.Fatal("stale content accepted")
	}
	b.ContentHash = "v1"
	b.ProjectID = "other"
	if EqualIndexMetadata(a, b) {
		t.Fatal("changed authorization scope accepted")
	}
}
