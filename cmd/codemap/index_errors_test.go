/* Copyright © 2026 abdul hamid <abdulachik@icloud.com> */
package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/index"
)

func TestPrintFileErrorsSummarizesFloods(t *testing.T) {
	var errs []index.FileError
	for i := 0; i < 40; i++ {
		f := fmt.Sprintf("src/f%d.ts", i)
		errs = append(errs, index.FileError{File: f, Err: "precise: typescript documentSymbol returned no symbols for " + f})
	}
	errs = append(errs, index.FileError{File: "a.py", Err: "boom"})
	errs = append(errs,
		index.FileError{File: "c.ts", Err: "precise: prepare call hierarchy for wrapper.mocks.$t returned no item"},
		index.FileError{File: "d.ts", Err: "precise: prepare call hierarchy for <unknown>.create returned no item"})
	for i := 0; i < 4; i++ {
		errs = append(errs, index.FileError{File: "b.ts", Err: fmt.Sprintf("precise: %d edge(s) did not join (target x.ts:%d)", i+1, 400+i)})
	}
	var buf bytes.Buffer
	printFileErrors(&buf, errs)
	out := buf.String()
	if got := strings.Count(out, "  ! src/"); got != fileErrorsShown {
		t.Fatalf("verbatim lines = %d, want %d:\n%s", got, fileErrorsShown, out)
	}
	if !strings.Contains(out, "2 × precise: prepare call hierarchy for <symbol> returned no item") {
		t.Fatalf("symbol names must not split a kind:\n%s", out)
	}
	if !strings.Contains(out, "37 more file error(s)") || !strings.Contains(out, "4 × precise: N edge(s) did not join (…)") ||
		!strings.Contains(out, "30 × precise: typescript documentSymbol returned no symbols for <file>") ||
		!strings.Contains(out, "1 other kind(s)") {
		t.Fatalf("summary missing:\n%s", out)
	}

	buf.Reset()
	printFileErrors(&buf, errs[:3])
	if strings.Contains(buf.String(), "more file error") || strings.Count(buf.String(), "  ! ") != 3 {
		t.Fatalf("short lists print verbatim:\n%s", buf.String())
	}
}
