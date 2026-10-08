package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillHome points every harness location at a temp home and returns it.
func skillHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MINERVA_AGENTS_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HERMES_HOME", "")
	return home
}

func targetByHarness(rep SkillReport, name string) (SkillTarget, bool) {
	for _, t := range rep.Targets {
		if t.Harness == name {
			return t, true
		}
	}
	return SkillTarget{}, false
}

func TestInstallAgentSkillLinksDetectedHarnesses(t *testing.T) {
	home := skillHome(t)
	for _, d := range []string{".claude", ".hermes/skills/.archive", ".omp"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := InstallAgentSkill(SkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(home, ".agents", "skills", "using-codemap")
	if rep.Action != "created" || rep.Skill != filepath.Join(canonical, "SKILL.md") {
		t.Fatalf("canonical = %s %s", rep.Action, rep.Skill)
	}
	got, _ := os.ReadFile(rep.Skill)
	if string(got) != RenderPlaybook(FormatAgentSkill) {
		t.Fatal("canonical SKILL.md is not the rendered skill")
	}
	for _, name := range []string{"claude", "hermes"} {
		tg, ok := targetByHarness(rep, name)
		if !ok || tg.Action != "linked" {
			t.Fatalf("%s target = %+v", name, tg)
		}
		if dest, err := os.Readlink(tg.Path); err != nil || filepath.IsAbs(dest) {
			t.Fatalf("%s must be a relative symlink, got %q %v", name, dest, err)
		}
		if b, err := os.ReadFile(filepath.Join(tg.Path, "SKILL.md")); err != nil || string(b) != string(got) {
			t.Fatalf("%s link does not reach the skill: %v", name, err)
		}
	}
	if tg, _ := targetByHarness(rep, "omp"); tg.Action != "native" {
		t.Fatalf("omp reads ~/.agents/skills natively, got %+v", tg)
	}
	for _, name := range []string{"codex", "opencode"} {
		if _, ok := targetByHarness(rep, name); ok {
			t.Fatalf("%s is not installed and was not requested", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".hermes", "skills", ".archive")); err != nil {
		t.Fatal("hermes' own entries must survive")
	}

	again, err := InstallAgentSkill(SkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" {
		t.Fatalf("reinstall = %s", again.Action)
	}
	for _, tg := range again.Targets {
		if tg.Action != "unchanged" && tg.Action != "native" {
			t.Fatalf("reinstall %s = %s", tg.Harness, tg.Action)
		}
	}
}

func TestInstallAgentSkillNeverReplacesAForeignSkill(t *testing.T) {
	home := skillHome(t)
	mine := filepath.Join(home, ".codex", "skills", "using-codemap")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mine, "SKILL.md"), []byte("---\nname: using-codemap\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := InstallAgentSkill(SkillOptions{Harnesses: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if tg, _ := targetByHarness(rep, "codex"); tg.Action != "skipped" {
		t.Fatalf("a hand-written skill must be skipped, got %+v", tg)
	}
	if b, _ := os.ReadFile(filepath.Join(mine, "SKILL.md")); !strings.Contains(string(b), "mine") {
		t.Fatal("hand-written skill was modified")
	}
	if rep, _ = InstallAgentSkill(SkillOptions{Harnesses: []string{"codex"}, Remove: true}); rep.Targets[0].Action != "skipped" {
		t.Fatalf("remove must leave a hand-written skill, got %+v", rep.Targets[0])
	}

	rep, err = InstallAgentSkill(SkillOptions{Harnesses: []string{"codex"}, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if tg, _ := targetByHarness(rep, "codex"); tg.Action != "linked" {
		t.Fatalf("--force must replace it, got %+v", tg)
	}
}

func TestInstallAgentSkillRefusesAForeignLibrarySkill(t *testing.T) {
	home := skillHome(t)
	lib := filepath.Join(home, ".agents", "skills", "using-codemap")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "SKILL.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAgentSkill(SkillOptions{}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want a --force hint, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(lib, "SKILL.md")); string(b) != "mine\n" {
		t.Fatal("library skill was modified")
	}
}

func TestInstallAgentSkillCopyDryRunAndRemove(t *testing.T) {
	home := skillHome(t)
	t.Setenv("MINERVA_AGENTS_DIR", filepath.Join(home, "lib"))

	rep, err := InstallAgentSkill(SkillOptions{Harnesses: []string{"opencode"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != "created" || !rep.DryRun {
		t.Fatalf("dry run = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(home, "lib")); err == nil {
		t.Fatal("dry run wrote the library")
	}

	rep, err = InstallAgentSkill(SkillOptions{Harnesses: []string{"opencode"}, Copy: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skill != filepath.Join(home, "lib", "skills", "using-codemap", "SKILL.md") {
		t.Fatalf("MINERVA_AGENTS_DIR not honored: %s", rep.Skill)
	}
	tg := rep.Targets[0]
	if fi, err := os.Lstat(tg.Path); err != nil || tg.Action != "copied" || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("copy target = %+v %v", tg, err)
	}

	rep, err = InstallAgentSkill(SkillOptions{Harnesses: []string{"opencode"}, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Action != "removed" || rep.Targets[0].Action != "removed" {
		t.Fatalf("remove = %+v", rep)
	}
	for _, p := range []string{tg.Path, filepath.Dir(rep.Skill)} {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s survived remove", p)
		}
	}
}

func TestInstallAgentSkillSkipsClaudeWhenThePluginShipsIt(t *testing.T) {
	home := skillHome(t)
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(`{"plugins":{"codemap@codemap":[{}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := InstallAgentSkill(SkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tg, _ := targetByHarness(rep, "claude"); tg.Action != "skipped" || !strings.Contains(tg.Reason, "plugin") {
		t.Fatalf("claude with the plugin = %+v", tg)
	}
	rep, err = InstallAgentSkill(SkillOptions{Harnesses: []string{"claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if tg, _ := targetByHarness(rep, "claude"); tg.Action != "linked" {
		t.Fatalf("an explicit --harness claude links anyway, got %+v", tg)
	}
}

func TestInstallAgentSkillUnknownHarness(t *testing.T) {
	skillHome(t)
	if _, err := InstallAgentSkill(SkillOptions{Harnesses: []string{"vim"}}); err == nil || !strings.Contains(err.Error(), "hermes") {
		t.Fatalf("want the valid-harness list, got %v", err)
	}
}

// TestPlaybookSyncAgentSkill pins the checked-in portable skill (installable
// with `minerva skill install abdul-hamid-achik/codemap/integrations/agent-skills/using-codemap`)
// to RenderPlaybook(FormatAgentSkill).
func TestPlaybookSyncAgentSkill(t *testing.T) {
	path := filepath.Join(repoRootForTest(t), "integrations", "agent-skills", "using-codemap", "SKILL.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in skill: %v", err)
	}
	if string(got) != RenderPlaybook(FormatAgentSkill) {
		t.Errorf("integrations/agent-skills/using-codemap/SKILL.md is stale.\n" +
			"run: go run ./cmd/codemap agent playbook --format skill > integrations/agent-skills/using-codemap/SKILL.md")
	}
}

func TestAgentSkillTeachesBothSurfaces(t *testing.T) {
	s := RenderPlaybook(FormatAgentSkill)
	if !strings.HasPrefix(s, "---\nname: using-codemap\ndescription: ") {
		t.Fatal("skill must lead with name + description frontmatter")
	}
	for _, want := range []string{agentSkillMarker, "codemap_context", "`codemap status --json`", "`codemap index`", "-C <dir>", "codemap file-impact --json"} {
		if !strings.Contains(s, want) {
			t.Errorf("skill must contain %q", want)
		}
	}
	front := strings.SplitN(strings.TrimPrefix(s, "---\n"), "\n---\n", 2)[0]
	for _, line := range strings.Split(front, "\n") {
		if _, v, _ := strings.Cut(line, ": "); strings.Contains(v, ": ") {
			t.Errorf("frontmatter value %q needs YAML quoting", v)
		}
	}
}
