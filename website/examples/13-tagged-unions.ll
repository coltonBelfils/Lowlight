// Title: Enums with data
// Category: Types
// Order: 13

// Variants can carry one value each, making a tagged union.
define Json enum [
	null,
	boolean: bool,
	number: float,
	text: string,
	list: [Json],
]

// `match` takes the union apart. Payloads are bound by name.
bind render = (j: Json) string:
	match(j):
		when null: "null"
		when boolean(b): b.to_string()
		when number(n): n.to_string()
		when text(s): "\"$s\""
		default list(items): {
			let parts = for(it in items) [string]: render(it)
			"[" + join_strings(parts, ", ") + "]"
		}

bind main = (): {
	let doc = Json.list([Json.number(1.5), Json.text("hi"), Json.boolean(true), Json.null])
	println(render(doc))
}
