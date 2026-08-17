# Test file for enemy AI
extends GutTest

var enemy
var player

func before_each():
	enemy = Enemy.new()
	player = Player.new()

func test_enemy_follows_player():
	enemy.set_target(player)
	assert_not_null(enemy.target)

func test_enemy_attacks_in_range():
	enemy.position = Vector2(0, 0)
	player.position = Vector2(10, 0)
	var can_attack = enemy.can_attack()
	assert_true(can_attack)
