// Title: Maps
// Category: Collections
// Order: 31

bind main = (): {
	// `#[K: V]` is a hash map. Iteration follows insertion order.
	let stock = #["apples": 5, "pears": 2]
	stock["plums"] = 7
	stock["apples"] = 4

	// Looking up a key gives an optional.
	println(stock["pears"] ?? 0)
	println(stock["kiwis"] ?? 0)
	println(stock.has("plums"))

	for(fruit, n in stock): println("$fruit: $n")

	let gone? = stock.remove("pears")
	println("${gone? ?? 0} ${stock.len()}")
	println(stock.keys())
}
