package yamlsrc

import "testing"

func TestYAMLKeysAndDependencies(t *testing.T) {
	r, err := New().ExtractFile("Taskfile.yml", []byte("version: '3'\ntasks:\n  build:\n    cmds: [go build ./...]\n  check:\n    deps: [build, '{{.TASK}}']\nquoted.key: yes\nquoted:\n  key: no\n---\nquoted: other\n"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range r.Symbols {
		if seen[s.FQN] {
			t.Fatalf("colliding identity %s", s.FQN)
		}
		seen[s.FQN] = true
		if s.EndLine < s.StartLine {
			t.Fatalf("range %+v", s)
		}
	}
	if len(r.References) != 1 || r.References[0].ToFQN != KeyFQN("Taskfile.yml", 0, "tasks", "build") {
		t.Fatalf("refs=%+v", r.References)
	}
	for _, bad := range []string{"x: [", "x: 1\nx: 2"} {
		if _, err := New().ExtractFile("bad.yml", []byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := New().ExtractFile("alias.yml", []byte("loop: &loop {self: *loop}")); err != nil {
		t.Fatal(err)
	}
	r, err = New().ExtractFile(".github/workflows/ci.yml", []byte("jobs:\n  test: {}\n  release:\n    needs: test\n"))
	if err != nil || len(r.References) != 1 {
		t.Fatalf("workflow=%+v err=%v", r, err)
	}
}

func TestFoldedScalarSourcePreserved(t *testing.T) {
	src := "description: >-\n  first line\n  second line\n---\nother: true\n"
	r, err := New().ExtractFile("config.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Symbols) != 2 || r.Symbols[0].EndLine != 3 || r.Symbols[0].Source != "description: >-\n  first line\n  second line" {
		t.Fatalf("lost scalar source: %+v", r.Symbols)
	}
}
