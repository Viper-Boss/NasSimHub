package nassimhubnode_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestReleaseStoreKeepsWhatWasSignedAndNothingItCannotAccountFor(t *testing.T) {
	signer := newPublisher(t)
	classical, postQuantum := signer.keyrings()
	directory := t.TempDir()
	store, err := nassimhubnode.OpenNodeReleaseStore(nassimhubnode.NodeReleaseStoreOptions{
		Dir: directory, Keyring: classical, PQKeyring: postQuantum, MaxReleases: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := bytes.Repeat([]byte("agent"), 1000)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	if _, err := store.ImportManifest(raw); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(directory, "r-1.1.0", "manifest.json"))
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatalf("the manifest must be stored byte for byte: %v", err)
	}
	// Importing the same bytes again is idempotent; different bytes under the
	// same release id are refused.
	if _, err := store.ImportManifest(raw); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if _, err := store.ImportManifest(signer.release("r-1.1.0", "1.1.1", artifact)); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("a second manifest under one release id must be refused: %v", err)
	}
	// Short, long and altered artifacts are refused and leave nothing behind.
	for name, body := range map[string][]byte{
		"short": artifact[:len(artifact)-1], "long": append(append([]byte{}, artifact...), 0),
		"altered": append([]byte{'X'}, artifact[1:]...),
	} {
		if _, err := store.ImportArtifact("r-1.1.0", bytes.NewReader(body)); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
			t.Fatalf("%s artifact: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(directory, "r-1.1.0"))
	if len(entries) != 2 {
		t.Fatalf("a refused artifact must leave no file behind: %v", entries)
	}
	if release, err := store.ImportArtifact("r-1.1.0", bytes.NewReader(artifact)); err != nil || !release.ArtifactReady {
		t.Fatalf("artifact: %+v %v", release, err)
	}
	if _, err := store.ImportArtifact("../r-1.1.0", bytes.NewReader(artifact)); !errors.Is(err, nassimhubnode.ErrNodeReleaseUnknown) {
		t.Fatalf("a release id is never a path: %v", err)
	}

	// The store is bounded.
	if _, err := store.ImportManifest(signer.release("r-1.2.0", "1.2.0", artifact)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportManifest(signer.release("r-1.3.0", "1.3.0", artifact)); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("the store must be bounded: %v", err)
	}
	releases := store.Releases()
	if len(releases) != 2 || releases[0].Version != "1.2.0" || releases[1].Version != "1.1.0" {
		t.Fatalf("releases are listed newest first: %+v", releases)
	}

	// A stored artifact that was damaged afterwards is not offered as ready.
	path := filepath.Join(directory, "r-1.1.0", "artifact")
	if err := os.WriteFile(path, artifact[:10], 0o600); err != nil {
		t.Fatal(err)
	}
	if release, err := store.Release("r-1.1.0"); err != nil || release.ArtifactReady {
		t.Fatalf("a truncated artifact is not ready: %+v %v", release, err)
	}

	// A store opened WITHOUT the publisher's key no longer lists what it
	// cannot verify... and one with other keys refuses it.
	other := newPublisher(t)
	otherClassical, otherPQ := other.keyrings()
	strict, err := nassimhubnode.OpenNodeReleaseStore(nassimhubnode.NodeReleaseStoreOptions{Dir: directory, Keyring: otherClassical, PQKeyring: otherPQ})
	if err != nil {
		t.Fatal(err)
	}
	if listed := strict.Releases(); len(listed) != 0 {
		t.Fatalf("releases signed by a key that is no longer trusted must not be offered: %+v", listed)
	}

	if err := store.Remove("r-1.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("r-1.2.0"); !errors.Is(err, nassimhubnode.ErrNodeReleaseUnknown) {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestReleaseStoreRequiresDualSignaturesWhenToldTo(t *testing.T) {
	if !proto.EnableStandardPQ() {
		t.Skip("needs crypto/mldsa")
	}
	signer := newPublisher(t)
	classical, postQuantum := signer.keyrings()
	store, err := nassimhubnode.OpenNodeReleaseStore(nassimhubnode.NodeReleaseStoreOptions{
		Dir: t.TempDir(), Keyring: classical, PQKeyring: postQuantum, SignaturePolicy: proto.OTASignatureRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("agent")
	dual := signer.release("r-dual", "1.1.0", artifact)
	if release, err := store.ImportManifest(dual); err != nil || release.Signatures != string(proto.OTADualSigned) {
		t.Fatalf("dual-signed: %+v %v", release, err)
	}
	classicalOnly := *signer
	classicalOnly.pq = nil
	if _, err := store.ImportManifest(classicalOnly.release("r-classical", "1.1.0", artifact)); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("a classical-only manifest must be refused under a required policy: %v", err)
	}
}
