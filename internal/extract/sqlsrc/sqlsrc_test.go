package sqlsrc

import (
	"strings"
	"testing"
)

func TestSQLStructureAndEvidence(t *testing.T) {
	source := `/*
-- name: NotAQuery :one
CREATE TABLE fake (id int);
*/
CREATE TABLE public.sessions (id integer PRIMARY KEY);
CREATE VIEW recent AS SELECT * FROM public.sessions;
-- name: GetSession :one
WITH picked AS (SELECT * FROM public.sessions) SELECT * FROM picked;
-- name: SaveSession :exec
INSERT INTO public.sessions (id) VALUES (1);
SELECT ';', 'FROM fake; -- name: Nope :one' FROM public.sessions;
DO $body$ BEGIN DELETE FROM fake; END $body$;
`
	r, err := New().ExtractFile("queries.sql", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Symbols) != 5 {
		t.Fatalf("symbols=%+v", r.Symbols)
	}
	for _, s := range r.Symbols {
		if s.Name == "NotAQuery" || strings.Contains(s.Source, "$body$") {
			t.Fatalf("comment/body leaked: %+v", s)
		}
	}
	reads, writes := 0, 0
	for _, ref := range r.References {
		if ref.To != "public.sessions" {
			t.Fatalf("false relation %+v", ref)
		}
		if ref.Kind == "reads" {
			reads++
		}
		if ref.Kind == "writes" {
			writes++
		}
	}
	if reads != 3 || writes != 1 {
		t.Fatalf("refs=%+v", r.References)
	}
	shifted, err := New().ExtractFile("queries.sql", []byte("\n\n"+source))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range r.Symbols {
		if shifted.Symbols[i].FQN != s.FQN || shifted.Symbols[i].StartLine != s.StartLine+2 {
			t.Fatal("line shift changed identity")
		}
	}
	for _, bad := range []string{"SELECT 'oops", "/* open", "DO $$open"} {
		if _, err := New().ExtractFile("bad.sql", []byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
