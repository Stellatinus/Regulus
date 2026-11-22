package main

import (
	"os"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
)

func TestCanonicalKeepsTheRootAndItsMeaning(t *testing.T) {
	old, err := os.ReadFile("../../internal/update/trusted_root.json")
	if err != nil {
		t.Fatal(err)
	}
	out, err := canonical(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.NewTrustedRootFromJSON(out); err != nil {
		t.Fatalf("the canonical form is not a trusted root any more: %v", err)
	}
	same, err := sameRoot(old, out)
	if err != nil || !same {
		t.Fatalf("same = %v (%v), want the same data in another layout", same, err)
	}
	again, _ := canonical(out)
	if string(again) != string(out) {
		t.Error("the canonical form is not stable")
	}
	if same, _ := sameRoot(old, []byte(`{"mediaType":"x"}`)); same {
		t.Error("different data compared equal")
	}
}
