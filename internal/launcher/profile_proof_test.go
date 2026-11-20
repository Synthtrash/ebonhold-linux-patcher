//go:build leanproof

package launcher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileIsolationProof(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lean", "-DwarningAsError=true", "--run", "ProfileIsolation.lean")
	cmd.Dir = "testdata/proofs"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run Lean 4 (testdata/proofs/lean-toolchain): %v\n%s", err, out)
	}
	type folders struct{ Regular, PTR []string }
	var cases []struct {
		Before                         folders
		GameSlug                       string
		SelectedFolder, ProposedFolder []string
		After                          *folders // nil means the edit must be rejected
	}
	if err := json.Unmarshal(out, &cases); err != nil {
		t.Fatalf("read conformance cases from Lean: %v", err)
	}

	base := t.TempDir()
	root := func(parts []string) string {
		if parts == nil {
			return ""
		}
		path := filepath.Join(base, filepath.Join(parts...))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var accepted, rejected int
	for i, c := range cases {
		p := Preferences{ProductionRoot: root(c.Before.Regular), PTRRoot: root(c.Before.PTR), GameSlug: c.GameSlug}
		if got, want := p.RootForProfile(p.GameSlug), root(c.SelectedFolder); got != want {
			t.Fatalf("case %d: selecting %s uses %q, want %q", i, p.GameSlug, got, want)
		}
		proposed := root(c.ProposedFolder)
		err := p.ValidateRootForProfile(p.GameSlug, proposed)
		if (err == nil) != (c.After != nil) {
			t.Fatalf("case %d: editing %s to %q with folders %+v: got %v, want acceptance=%t", i, p.GameSlug, proposed, c.Before, err, c.After != nil)
		}
		if c.After == nil {
			rejected++
			continue
		}
		accepted++
		p.SetRootForProfile(p.GameSlug, proposed)
		if regular, ptr := root(c.After.Regular), root(c.After.PTR); p.ProductionRoot != regular || p.PTRRoot != ptr {
			t.Fatalf("case %d: after editing %s, folders = (%q, %q), want (%q, %q)", i, p.GameSlug, p.ProductionRoot, p.PTRRoot, regular, ptr)
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatalf("Lean must supply both outcomes, got %d accepted and %d rejected", accepted, rejected)
	}
	t.Logf("Go agreed with Lean on %d profile edits (%d accepted, %d rejected)", len(cases), accepted, rejected)
}
