package graph

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLexicalTerms(t *testing.T) {
	cases := map[string][]string{
		"how does signup work":            {"signup"},
		"where is billing entitlement?":   {"bill", "entitlement"},
		"how are answers validated":       {"answer", "validat"},
		"validation of processes":         {"validat", "process"},
		"access rules":                    {"access", "rule"},
		"signupUser":                      {"signupuser"},
		"auth/signup route.ts":            {"auth", "signup", "route"},
		"Rate limit RATE limit":           {"rate", "limit"},
		"is it ok":                        {},
		"what does the parse_selector do": {"parse", "selector"},
	}
	for q, want := range cases {
		got := lexicalTerms(q)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("lexicalTerms(%q) = %v, want %v", q, got, want)
		}
	}
}

// lexicalFixture builds a small TS-shaped project: the signup implementation
// lives under an auth/signup route path, plus decoys that only match the
// question's scaffolding words.
func lexicalFixture(t *testing.T, s *Store) int64 {
	t.Helper()
	pid, err := s.UpsertProject("p", "/p", "typescript")
	if err != nil {
		t.Fatal(err)
	}
	add := func(file, sym, kind, doc string) {
		t.Helper()
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: file, Symbol: sym, FQN: sym, Kind: kind,
			Language: "typescript", StartLine: 1, EndLine: 2, Docstring: doc, SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	add("src/lib/auth/user-service.ts", "signupUser", KindFunction, "Creates an account.")
	add("src/app/api/auth/signup/route.ts", "POST", KindFunction, "")
	add("src/lib/queue.ts", "workQueue", KindVariable, "")
	add("src/lib/noop.ts", "doesNothing", KindFunction, "")
	add("src/lib/mail.ts", "sendWelcome", KindFunction, "Sent after signup completes.")
	add("src/app/api/auth/signup/route.ts", "", KindFile, "")
	return pid
}

func TestLexicalSearchFindsQuestionSubject(t *testing.T) {
	s := openTest(t)
	pid := lexicalFixture(t, s)

	res, err := s.LexicalSearch(pid, "how does signup work", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range res {
		got[m.Node.Symbol] = m.MatchedIn
		if m.Node.Kind == KindFile {
			t.Errorf("file node returned: %+v", m.Node)
		}
	}
	want := map[string]string{"signupUser": "symbol", "POST": "path", "sendWelcome": "docstring"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LexicalSearch = %v, want %v (stopwords must not match workQueue/doesNothing)", got, want)
	}
	if len(res) == 0 || res[0].Node.Symbol != "signupUser" {
		t.Errorf("symbol-name match should rank first (bm25 weights), got %v", res)
	}
}

func TestLexicalSearchPrefersBroaderCoverage(t *testing.T) {
	s := openTest(t)
	pid := lexicalFixture(t, s)

	// "auth signup": POST matches both words (via its path) and must beat
	// sendWelcome, which matches only "signup".
	res, err := s.LexicalSearch(pid, "auth signup", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) < 2 || res[len(res)-1].Node.Symbol != "sendWelcome" {
		t.Errorf("single-word match should rank last, got %v", symbolsOf(res))
	}
}

func TestLexicalSearchTracksNodeWrites(t *testing.T) {
	s := openTest(t)
	pid := lexicalFixture(t, s)

	if err := s.DeleteNodesInFile(pid, "src/lib/auth/user-service.ts"); err != nil {
		t.Fatal(err)
	}
	res, err := s.LexicalSearch(pid, "signupUser", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("deleted node still searchable: %v", symbolsOf(res))
	}

	if _, err := s.db.Exec("UPDATE nodes SET symbol='registerAccount', fqn='registerAccount' WHERE symbol='doesNothing'"); err != nil {
		t.Fatal(err)
	}
	res, err = s.LexicalSearch(pid, "register account", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Node.Symbol != "registerAccount" {
		t.Errorf("renamed node not reindexed: %v", symbolsOf(res))
	}

	// Other projects never leak into results.
	other, _ := s.UpsertProject("q", "/q", "typescript")
	if _, err := s.AddNode(&Node{ProjectID: other, FilePath: "a.ts", Symbol: "signupElsewhere", Kind: KindFunction,
		Language: "typescript", SourceHash: "h"}); err != nil {
		t.Fatal(err)
	}
	res, _ = s.LexicalSearch(pid, "signupElsewhere", 10)
	if len(res) != 0 {
		t.Errorf("cross-project leak: %v", symbolsOf(res))
	}
}

func TestMigrateV9BackfillsLexicalIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v9.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pid := lexicalFixture(t, old)
	// Simulate a v9 database: no FTS objects, nodes already present.
	for _, stmt := range []string{
		"DROP TRIGGER nodes_fts_au", "DROP TABLE nodes_fts", "DROP TABLE nodes_fts_ids",
		"DROP TABLE nodes_fts_dirty", "PRAGMA user_version=9",
	} {
		if _, err := old.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open v9 graph: %v", err)
	}
	defer s.Close()
	res, err := s.LexicalSearch(pid, "signup", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Errorf("backfilled lexical index = %v, want signupUser, POST, sendWelcome", symbolsOf(res))
	}
}

func symbolsOf(ms []SymbolMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Node.Symbol
	}
	return out
}

func TestSyncLexicalReconcilesAndIgnoresVecIDUpdates(t *testing.T) {
	s := openTest(t)
	pid := lexicalFixture(t, s)
	count := func(table string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("nodes_fts_ids") != 0 {
		t.Fatal("node inserts must not touch the lexical index (it syncs in bulk)")
	}
	if err := s.SyncLexical(); err != nil {
		t.Fatal(err)
	}
	if count("nodes_fts_dirty") != 0 || count("nodes_fts_ids") != count("nodes") {
		t.Fatalf("after sync: dirty=%d ids=%d nodes=%d", count("nodes_fts_dirty"), count("nodes_fts_ids"), count("nodes"))
	}
	// The embed pass rewrites vec_id on every node; it must not requeue them.
	if _, err := s.db.Exec("UPDATE nodes SET vec_id = 'v', updated_at = 'now'"); err != nil {
		t.Fatal(err)
	}
	if n := count("nodes_fts_dirty"); n != 0 {
		t.Errorf("vec_id update queued %d ids", n)
	}
	// Delete then re-add a file (the incremental reindex shape) twice: the
	// index must hold exactly the live nodes, with no duplicate hits.
	for i := 0; i < 2; i++ {
		if err := s.DeleteNodesInFile(pid, "src/lib/auth/user-service.ts"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddNode(&Node{ProjectID: pid, FilePath: "src/lib/auth/user-service.ts", Symbol: "signupUser", FQN: "signupUser",
			Kind: KindFunction, Language: "typescript", StartLine: 1, EndLine: 2, SourceHash: "h"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SyncLexical(); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.LexicalSearch(pid, "signupUser", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("re-added node hits = %v, want exactly one", symbolsOf(res))
	}
	if count("nodes_fts_ids") != count("nodes") {
		t.Errorf("ids=%d nodes=%d after churn", count("nodes_fts_ids"), count("nodes"))
	}
}

func TestSyncLexicalSafetyNetReindexesInPlaceUpdates(t *testing.T) {
	s := openTest(t)
	pid := lexicalFixture(t, s)
	if err := s.SyncLexical(); err != nil {
		t.Fatal(err)
	}
	// No production path updates node text in place today; if one ever does,
	// the UPDATE trigger flags the id and the next search must see the change.
	if _, err := s.db.Exec("UPDATE nodes SET docstring = 'handles password reset' WHERE symbol = 'workQueue'"); err != nil {
		t.Fatal(err)
	}
	res, err := s.LexicalSearch(pid, "password reset", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Node.Symbol != "workQueue" || res[0].MatchedIn != "docstring" {
		t.Errorf("in-place update not reindexed: %v", symbolsOf(res))
	}
}

func TestLexicalSearchRanksProductionBeforeTests(t *testing.T) {
	s := openTest(t)
	pid, _ := s.UpsertProject("p", "/p", "go")
	for _, n := range []Node{
		{FilePath: "sync_test.go", Symbol: "TestSyncLexical", Kind: KindTest},
		{FilePath: "sync.go", Symbol: "syncLexicalIndex", Kind: KindFunction},
	} {
		n.ProjectID, n.Language, n.SourceHash, n.FQN = pid, "go", "h", n.Symbol
		if _, err := s.AddNode(&n); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.LexicalSearch(pid, "sync lexical", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := symbolsOf(res); len(got) != 2 || got[0] != "syncLexicalIndex" {
		t.Errorf("ranking = %v, want production code first", got)
	}
}

// Equal coverage ranks behavior before data, inflections still match, and the
// score is the fraction of query words matched.
func TestLexicalSearchPrefersCodeAndStems(t *testing.T) {
	s := openTest(t)
	pid, err := s.UpsertProject("p", "/p", "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []Node{
		{Symbol: "ValidatedBy", FQN: "answers.ValidatedBy", Kind: KindVariable, FilePath: "answers/keys.go"},
		{Symbol: "validateSurvey", FQN: "survey.validateSurvey", Kind: KindFunction, FilePath: "survey/check.go"},
	} {
		n.ProjectID, n.Language, n.StartLine, n.EndLine, n.SourceHash = pid, "go", 1, 2, "h"
		if _, err := s.AddNode(&n); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.LexicalSearch(pid, "where is validation", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Node.Symbol != "validateSurvey" {
		t.Fatalf("want the function first via the stem, got %v", symbolsOf(res))
	}
	if res[0].Score != 1 {
		t.Errorf("score = %v, want 1 (every content word matched)", res[0].Score)
	}
}
