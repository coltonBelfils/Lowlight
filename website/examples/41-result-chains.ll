// Title: Result chains
// Category: Failures
// Order: 41

define Login result [
	value: string,
	unknown_user: string,
	wrong_password,
	locked: int,
]

bind login = (user: string, pass: string) Login: {
	if(user != "ada"): func <- unknown_user(user.copy())
	if(pass == "frozen"): func <- locked(30)
	if(pass != "secret"): func <- wrong_password
	value("token-123")
}

// A result chain handles each failure by name and evaluates to
// the success value. Arms either produce a replacement or unwind.
bind attempt = (user: string, pass: string) string: {
	let token = login(user, pass):
		when unknown_user(u): func <- "no such user: $u"
		when locked(mins): func <- "locked for $mins minutes"
		default wrong_password: "guest-token"
	"signed in with $token"
}

bind main = (): {
	println(attempt("ada", "secret"))
	println(attempt("bob", "x"))
	println(attempt("ada", "nope"))
	println(attempt("ada", "frozen"))
}
