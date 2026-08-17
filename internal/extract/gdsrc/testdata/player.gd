# Player character controller
class_name Player
extends CharacterBody2D

# Maximum health points
const MAX_HP = 100
# Current health
var hp: int = MAX_HP
# Movement speed
@export var speed: float = 200.0

# Emitted when player takes damage
signal hurt(old_hp: int, new_hp: int)
# Emitted when player dies
signal died

# State machine for player actions
enum State {
	IDLE,
	RUNNING,
	JUMPING,
	FALLING
}

var current_state: State = State.IDLE

# Inner state class
class StateData:
	var time_entered: float
	var previous_state: int
	
	func reset():
		time_entered = 0.0
		previous_state = 0

# Called when the node enters the scene tree
func _ready():
	print("Player initialized")
	connect_signals()
	setup_physics()

# Process physics each frame
func _physics_process(delta: float) -> void:
	handle_movement(delta)
	move_and_slide()

# Apply damage to the player
func take_damage(amount: int) -> void:
	var old_hp = hp
	hp = max(0, hp - amount)
	hurt.emit(old_hp, hp)
	if hp <= 0:
		die()

# Handle player death
func die():
	died.emit()
	queue_free()

# Get normalized input vector
func get_input_vector() -> Vector2:
	var input = Vector2.ZERO
	if Input.is_action_pressed("move_right"):
		input.x += 1
	if Input.is_action_pressed("move_left"):
		input.x -= 1
	return input.normalized()

# Single-line helper function
func is_alive() -> bool: return hp > 0

# Static helper
static func create_default() -> Player:
	var player = Player.new()
	player.hp = MAX_HP
	return player
