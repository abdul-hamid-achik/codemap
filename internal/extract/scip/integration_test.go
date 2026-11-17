package scip

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestScipJavaIntegration indexes a tiny real Java fixture with scip-java and
// imports the produced index. It skips cleanly (with the reason) when either
// scip-java or java is not on PATH — the hermetic snapshot tests above cover
// the adapter without them.
func TestScipJavaIntegration(t *testing.T) {
	scipJava, err := exec.LookPath("scip-java")
	if err != nil {
		t.Skipf("scip-java not found on PATH (%v); skipping real-indexer integration test", err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		t.Skipf("java not found on PATH (%v); skipping real-indexer integration test", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "main", "java", "com", "example")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	java := `package com.example;

public class Greeter {
    public String greet(String name) {
        return "hello " + name;
    }
}
`
	if err := os.WriteFile(filepath.Join(src, "Greeter.java"), []byte(java), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "index.scip")
	cmd := exec.Command(scipJava, "index", "--project-root", dir, "--output", out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("scip-java failed on the fixture (%v: %s); skipping integration test", err, combined)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse scip-java output: %v", err)
	}
	sum, err := Import(idx, Options{ProjectRoot: dir}, func(Batch) error { return nil })
	if err != nil {
		t.Fatalf("import scip-java index: %v", err)
	}
	if sum.Producer == "" || sum.Producer == "unknown" {
		t.Errorf("producer = %q, want scip-java's tool_info name", sum.Producer)
	}
	if sum.Defs == 0 {
		t.Errorf("no definitions imported from a real scip-java index")
	}
}
