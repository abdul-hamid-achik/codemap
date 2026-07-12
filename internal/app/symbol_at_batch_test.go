package app

import (
	"testing"
)

func TestSymbolAtBatch(t *testing.T) {
	svc, proj := relatedProj(t)
	// a.go:2 is Helper's def line, a.go:3 is Run's def line, a.go:999 is a miss.
	rep, err := svc.SymbolAtBatch(proj, []FilePosition{
		{File: "a.go", Line: 2},
		{File: "a.go", Line: 3},
		{File: "a.go", Line: 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indexed {
		t.Fatalf("indexed project must report indexed=true, got %+v", rep)
	}
	if rep.Requested != 3 || len(rep.Results) != rep.Requested {
		t.Fatalf("len(Results) should equal Requested, got requested=%d results=%d", rep.Requested, len(rep.Results))
	}
	if rep.Results[0].Symbol != "Helper" || rep.Results[0].Resolution != "exact" {
		t.Errorf("a.go:2 = %+v, want Helper/exact", rep.Results[0])
	}
	if rep.Results[1].Symbol != "Run" || rep.Results[1].Resolution != "exact" {
		t.Errorf("a.go:3 = %+v, want Run/exact", rep.Results[1])
	}
	if rep.Results[2].Resolution != "none" || rep.Results[2].Symbol != "" {
		t.Errorf("a.go:999 (miss) = %+v, want resolution=none and no symbol", rep.Results[2])
	}
}

func TestSymbolAtBatchCap(t *testing.T) {
	svc, proj := relatedProj(t)
	positions := make([]FilePosition, 30)
	for i := range positions {
		positions[i] = FilePosition{File: "a.go", Line: 2}
	}
	rep, err := svc.SymbolAtBatch(proj, positions)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != symbolAtBatchMax {
		t.Errorf("batch should cap results at %d, got %d", symbolAtBatchMax, len(rep.Results))
	}
	if rep.Note == "" {
		t.Error("a >25 batch should note the elision")
	}
}

func TestSymbolAtBatchUnindexed(t *testing.T) {
	isolate(t)
	sess, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	rep, err := NewService(sess).SymbolAtBatch(t.TempDir(), []FilePosition{{File: "main.go", Line: 1}})
	if err != nil {
		t.Fatalf("unindexed project must not error: %v", err)
	}
	if rep.Indexed || len(rep.Results) != 0 {
		t.Errorf("unindexed → {indexed:false, results:[]}, got %+v", rep)
	}
}
