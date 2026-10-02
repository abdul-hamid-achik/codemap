package index

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/graph"
)

// TestTSCallCandidatesDuplicateFQNInOneFile proves a call is attributed to the
// declaration it sits in when the same FQN is declared twice in one file (as
// repeated test callbacks and overloads do), not to the first declaration.
func TestTSCallCandidatesDuplicateFQNInOneFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dup.ts", strings.Join([]string{
		`function one() {}`, // 1
		`function two() {}`, // 2
		`function dup() {`,  // 3
		`  one();`,          // 4
		`}`,                 // 5
		`function dup() {`,  // 6
		`  two();`,          // 7
		`}`,                 // 8
		``,
	}, "\n"))
	g, pid := indexScanFake(t, dir, &scanFake{}, Options{})
	nodes, err := g.ProjectNodes(pid)
	if err != nil {
		t.Fatal(err)
	}
	startOf := map[int64]int{}
	nameOf := map[int64]string{}
	for _, n := range nodes {
		startOf[n.ID] = n.StartLine
		nameOf[n.ID] = n.FQN
	}
	edges, err := g.ProjectEdges(pid)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range edges {
		if e.EdgeType == graph.EdgeCalls {
			got = append(got, fmt.Sprintf("%s@%d>%s", nameOf[e.SourceID], startOf[e.SourceID], nameOf[e.TargetID]))
		}
	}
	sort.Strings(got)
	if want := "dup@3>one,dup@6>two"; strings.Join(got, ",") != want {
		t.Errorf("edges = %v, want %s", got, want)
	}
}
