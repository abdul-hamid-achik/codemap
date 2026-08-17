package gdsrc

import (
	"strings"
	"testing"

	"github.com/abdul-hamid-achik/codemap/internal/extract"
)

func TestGDScriptBasicExtraction(t *testing.T) {
	src := `# Player controller
class_name Player
extends CharacterBody2D

# Player health
var hp: int = 100
const MAX_HP = 100

signal died
signal hurt(old_hp, new_hp)

# Called when the node enters the scene tree
func _ready():
	print("Player ready")
	setup_signals()

# Apply damage to player
func take_damage(amount: int) -> void:
	var old_hp = hp
	hp -= amount
	hurt.emit(old_hp, hp)
	if hp <= 0:
		died.emit()
`
	ext := New()
	res, err := ext.ExtractFile("player.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	if res.Language != "gdscript" {
		t.Errorf("wrong language: got %q, want gdscript", res.Language)
	}

	// Should have: Player class, hp var, MAX_HP const, died signal, hurt signal, _ready func, take_damage func
	if len(res.Symbols) < 7 {
		t.Errorf("expected at least 7 symbols, got %d", len(res.Symbols))
	}

	// Check class_name Player
	if res.Symbols[0].Name != "Player" || res.Symbols[0].Kind != extract.KindClass {
		t.Errorf("first symbol should be Player class, got %s %s", res.Symbols[0].Name, res.Symbols[0].Kind)
	}

	// Check functions
	var readyFound, takeDamageFound bool
	for _, sym := range res.Symbols {
		if sym.Name == "_ready" && sym.Kind == extract.KindMethod {
			readyFound = true
			if sym.StartLine != 13 {
				t.Errorf("_ready should start at line 13, got %d", sym.StartLine)
			}
		}
		if sym.Name == "take_damage" && sym.Kind == extract.KindMethod {
			takeDamageFound = true
		}
	}
	if !readyFound {
		t.Error("_ready method not found")
	}
	if !takeDamageFound {
		t.Error("take_damage method not found")
	}

	// Check references
	var setupSignalsCall, emitCall bool
	for _, ref := range res.References {
		if ref.To == "setup_signals" && ref.Kind == extract.RefCalls {
			setupSignalsCall = true
		}
		if ref.To == "emit" && ref.Kind == extract.RefCalls {
			emitCall = true
		}
	}
	if !setupSignalsCall {
		t.Error("setup_signals call not found")
	}
	if !emitCall {
		t.Error("emit call not found")
	}
}

func TestGDScriptInnerClass(t *testing.T) {
	src := `class_name Outer

class Inner:
	func inner_method():
		pass

func outer_method():
	var obj = Inner.new()
`
	ext := New()
	res, err := ext.ExtractFile("outer.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Should have: Outer class, Inner class, inner_method, outer_method
	if len(res.Symbols) < 4 {
		t.Errorf("expected at least 4 symbols, got %d", len(res.Symbols))
	}

	// Check Inner class
	var innerClass *extract.Symbol
	for i := range res.Symbols {
		if res.Symbols[i].Name == "Inner" {
			innerClass = &res.Symbols[i]
			break
		}
	}
	if innerClass == nil {
		t.Fatal("Inner class not found")
	}
	if innerClass.FQN != "Outer.Inner" {
		t.Errorf("Inner FQN wrong: got %q, want Outer.Inner", innerClass.FQN)
	}

	// Check inner_method FQN
	var innerMethod *extract.Symbol
	for i := range res.Symbols {
		if res.Symbols[i].Name == "inner_method" {
			innerMethod = &res.Symbols[i]
			break
		}
	}
	if innerMethod == nil {
		t.Fatal("inner_method not found")
	}
	if innerMethod.FQN != "Outer.Inner.inner_method" {
		t.Errorf("inner_method FQN wrong: got %q, want Outer.Inner.inner_method", innerMethod.FQN)
	}

	// Check Inner.new() reference
	var newRef bool
	for _, ref := range res.References {
		if ref.To == "Inner" && ref.Kind == extract.RefReferences {
			newRef = true
			break
		}
	}
	if !newRef {
		t.Logf("All references: %+v", res.References)
		t.Error("Inner.new() reference not found")
	}
}

func TestGDScriptPreloadImports(t *testing.T) {
	src := `extends Node2D

var Scene = preload("res://scenes/player.tscn")
var Script = load("res://scripts/helper.gd")

func _ready():
	var instance = Scene.instantiate()
`
	ext := New()
	res, err := ext.ExtractFile("main.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Should have 2 imports: player.tscn and helper.gd
	if len(res.Imports) < 2 {
		t.Fatalf("expected at least 2 imports, got %d", len(res.Imports))
	}

	hasPlayer := false
	hasHelper := false
	for _, imp := range res.Imports {
		if imp == "scenes/player.tscn" {
			hasPlayer = true
		}
		if imp == "scripts/helper.gd" {
			hasHelper = true
		}
	}
	if !hasPlayer {
		t.Error("player.tscn import not found")
	}
	if !hasHelper {
		t.Error("helper.gd import not found")
	}
}

func TestGDScriptEnum(t *testing.T) {
	src := `enum State {IDLE, RUN, JUMP}
enum {ONE = 1, TWO = 2}

var current_state: State = State.IDLE
`
	ext := New()
	res, err := ext.ExtractFile("states.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Should have: State enum, anonymous Enum, current_state var
	if len(res.Symbols) < 3 {
		t.Errorf("expected at least 3 symbols, got %d", len(res.Symbols))
	}

	var stateEnum *extract.Symbol
	for i := range res.Symbols {
		if res.Symbols[i].Name == "State" {
			stateEnum = &res.Symbols[i]
			break
		}
	}
	if stateEnum == nil {
		t.Fatal("State enum not found")
	}
	if stateEnum.Kind != extract.KindType {
		t.Errorf("State should be KindType, got %s", stateEnum.Kind)
	}
}

func TestGDScriptSingleLineFunc(t *testing.T) {
	src := `func get_speed() -> float: return 100.0

func calculate(x: int): return x * 2
`
	ext := New()
	res, err := ext.ExtractFile("utils.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	if len(res.Symbols) < 2 {
		t.Errorf("expected 2 symbols, got %d", len(res.Symbols))
	}

	for _, sym := range res.Symbols {
		if sym.Name == "get_speed" || sym.Name == "calculate" {
			if sym.Source == "" {
				t.Errorf("%s should have source, got empty", sym.Name)
			}
			if sym.StartLine != sym.EndLine {
				t.Errorf("%s should be single-line, got start=%d end=%d", sym.Name, sym.StartLine, sym.EndLine)
			}
		}
	}
}

func TestGDScriptComments(t *testing.T) {
	src := `# This is a comment
# Multi-line doc
# about this function
func foo():
	# print("this should not be a call")
	actual_call()  # trailing comment
`
	ext := New()
	res, err := ext.ExtractFile("test.gd", []byte(src))
	if err != nil {
		t.Fatalf("ExtractFile failed: %v", err)
	}

	// Should have foo function
	if len(res.Symbols) < 1 {
		t.Fatal("expected at least 1 symbol")
	}

	// Check docstring
	if !strings.Contains(res.Symbols[0].Docstring, "Multi-line") {
		t.Errorf("docstring wrong: %q", res.Symbols[0].Docstring)
	}

	// Should have actual_call but not print
	var actualCallFound, printFound bool
	for _, ref := range res.References {
		if ref.To == "actual_call" {
			actualCallFound = true
		}
		if ref.To == "print" {
			printFound = true
		}
	}
	if !actualCallFound {
		t.Error("actual_call not found")
	}
	if printFound {
		t.Error("print from comment should not be found")
	}
}

func TestGDScriptTestPath(t *testing.T) {
	tests := []struct {
		path   string
		isTest bool
	}{
		{"player_test.gd", true},
		{"test_player.gd", true},
		{"player.gd", false},
		{"test.gd", false},
	}

	for _, tt := range tests {
		got := isTestPath(tt.path)
		if got != tt.isTest {
			t.Errorf("isTestPath(%q) = %v, want %v", tt.path, got, tt.isTest)
		}
	}
}
