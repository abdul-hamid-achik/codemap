package gdsrc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

// TestPlayerFixture validates the realistic player.gd fixture for FQNs, ranges, and kinds.
func TestPlayerFixture(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "player.gd"))
	if err != nil {
		t.Fatalf("Failed to read player.gd: %v", err)
	}

	ext := New()
	res, err := ext.ExtractFile("testdata/player.gd", src)
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Expected symbols with FQNs, kinds, and line ranges
	expected := []struct {
		name      string
		fqn       string
		kind      string
		startLine int
	}{
		{"Player", "Player", extract.KindClass, 2},
		{"MAX_HP", "Player.MAX_HP", extract.KindVariable, 6},
		{"hp", "Player.hp", extract.KindVariable, 8},
		{"speed", "Player.speed", extract.KindVariable, 10},
		{"hurt", "Player.hurt", extract.KindVariable, 13},
		{"died", "Player.died", extract.KindVariable, 15},
		{"State", "Player.State", extract.KindType, 18},
		{"current_state", "Player.current_state", extract.KindVariable, 25},
		{"StateData", "Player.StateData", extract.KindClass, 28},
		{"time_entered", "Player.StateData.time_entered", extract.KindVariable, 29},
		{"previous_state", "Player.StateData.previous_state", extract.KindVariable, 30},
		{"reset", "Player.StateData.reset", extract.KindMethod, 32},
		{"_ready", "Player._ready", extract.KindMethod, 37},
		{"_physics_process", "Player._physics_process", extract.KindMethod, 43},
		{"take_damage", "Player.take_damage", extract.KindMethod, 48},
		{"die", "Player.die", extract.KindMethod, 56},
		{"get_input_vector", "Player.get_input_vector", extract.KindMethod, 61},
		{"is_alive", "Player.is_alive", extract.KindMethod, 70},
		{"create_default", "Player.create_default", extract.KindMethod, 73},
	}

	if len(res.Symbols) != len(expected) {
		t.Errorf("Expected %d symbols, got %d", len(expected), len(res.Symbols))
		for i, sym := range res.Symbols {
			t.Logf("  [%d] %s (%s) at line %d", i, sym.FQN, sym.Kind, sym.StartLine)
		}
	}

	for _, exp := range expected {
		found := false
		for _, sym := range res.Symbols {
			if sym.Name == exp.name && sym.FQN == exp.fqn {
				found = true
				if sym.Kind != exp.kind {
					t.Errorf("%s: expected kind %s, got %s", exp.fqn, exp.kind, sym.Kind)
				}
				if sym.StartLine != exp.startLine {
					t.Errorf("%s: expected start line %d, got %d", exp.fqn, exp.startLine, sym.StartLine)
				}
				if sym.EndLine < sym.StartLine {
					t.Errorf("%s: invalid range [%d:%d]", exp.fqn, sym.StartLine, sym.EndLine)
				}
				break
			}
		}
		if !found {
			t.Errorf("Symbol not found: %s (%s)", exp.fqn, exp.kind)
		}
	}

	// Verify call references (name-based)
	var setupCall, moveAndSlideCall, maxCall, queueFreeCall bool
	for _, ref := range res.References {
		switch {
		case ref.To == "connect_signals" && ref.From == "Player._ready":
			setupCall = true
		case ref.To == "move_and_slide" && ref.From == "Player._physics_process":
			moveAndSlideCall = true
		case ref.To == "max" && ref.From == "Player.take_damage":
			maxCall = true
		case ref.To == "queue_free" && ref.From == "Player.die":
			queueFreeCall = true
		}
	}
	if !setupCall {
		t.Error("connect_signals call from _ready not found")
	}
	if !moveAndSlideCall {
		t.Error("move_and_slide call from _physics_process not found")
	}
	if !maxCall {
		t.Error("max call from take_damage not found")
	}
	if !queueFreeCall {
		t.Error("queue_free call from die not found")
	}
}

// TestEnemyTestFixture validates the enemy_test.gd fixture for test detection and FQNs.
func TestEnemyTestFixture(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "enemy_test.gd"))
	if err != nil {
		t.Fatalf("Failed to read enemy_test.gd: %v", err)
	}

	ext := New()
	res, err := ext.ExtractFile("testdata/enemy_test.gd", src)
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Expected test functions
	expected := []struct {
		name string
		kind string
	}{
		{"before_each", extract.KindTest},
		{"test_enemy_follows_player", extract.KindTest},
		{"test_enemy_attacks_in_range", extract.KindTest},
	}

	for _, exp := range expected {
		found := false
		for _, sym := range res.Symbols {
			if sym.Name == exp.name {
				found = true
				if sym.Kind != exp.kind {
					t.Errorf("%s: expected kind %s, got %s", exp.name, exp.kind, sym.Kind)
				}
				break
			}
		}
		if !found {
			t.Errorf("Test function not found: %s", exp.name)
		}
	}

	// Verify Enemy and Player references
	var enemyNewRef, playerNewRef bool
	for _, ref := range res.References {
		if ref.To == "Enemy" && ref.Kind == extract.RefReferences {
			enemyNewRef = true
		}
		if ref.To == "Player" && ref.Kind == extract.RefReferences {
			playerNewRef = true
		}
	}
	if !enemyNewRef {
		t.Error("Enemy.new() reference not found")
	}
	if !playerNewRef {
		t.Error("Player.new() reference not found")
	}
}
