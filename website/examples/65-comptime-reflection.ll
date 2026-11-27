// Title: Compile-time reflection and @derivable
// Category: Compile time
// Order: 65

// Custom interfaces can attach a `@derivable` generic helper that
// uses static reflection (`comp.*`) instead of boilerplate code.
@derivable(kv_string: _derive_kv)
define KvString interface [
	kv_string: fn(self) string,
]

const _derive_kv[T] = fn(v: T) string: {
	let pairs: [string] = []
	comptime for(f in comp.fields_of[T]()):
		pairs.push("${f.name}=${v.field(f)}")
	"${comptime comp.type_name[T]()}{${pairs.join(", ")}}"
}

@derive(KvString)
define Endpoint struct [
	host: string,
	port: int,
	tls: bool = true,
]

// Collecting `comptime for(...) T:` unrolls struct field initialization
// at compile time, using defaults on `next <-`.
const default_copy[T] = fn(override_port: int) T:
	comptime for(f in comp.fields_of[T]()) T: {
		comptime if(f.name == "port"): override_port
		else comptime if(f.has_default): { next <- }
		else: "0.0.0.0"
	}

bind main = fn(): {
	let ep = default_copy[Endpoint](8443)
	println(ep.kv_string())
}
