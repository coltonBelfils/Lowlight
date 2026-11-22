// Title: Destruction
// Category: Ownership
// Order: 52

// Resources are destroyed when their owner's scope ends, in
// reverse order of declaration. `drop` runs custom cleanup.
define Connection struct [
	host: string,
]

bind Connection.open = fn(host: string) Connection: {
	println("open ${host}")
	Connection[host: host.copy()]
}

bind Connection.drop = fn(&self): println("close ${self.host}")

bind main = fn(): {
	let a = Connection.open("db")
	{
		let b = Connection.open("cache")
		println("using cache")
	}
	// `c` is declared last, so it is closed first.
	let c = Connection.open("queue")
	println("end of main")
}
