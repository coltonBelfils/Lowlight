// Title: Constants
// Category: Basics
// Order: 4

// Top-level `bind` declarations are constants. By convention
// their names are in SCREAMING_SNAKE_CASE.
bind MAX_USERS = 100
bind GREETING = "welcome"
bind RATE = 1.5

// Constants can be computed from other constants, in any order.
bind DOUBLE_MAX = MAX_USERS * 2

bind main = (): {
	println(MAX_USERS)
	println(DOUBLE_MAX)
	println("$GREETING at rate $RATE")
}
