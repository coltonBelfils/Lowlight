// Title: Sorting
// Category: Collections
// Order: 34

import "std/sort"

define Person struct [
	name: string,
	age: int,
]

bind main = fn(): {
	// `std/sort` adds sorting methods to lists.
	let xs = [5, 3, 9, 1]
	xs.sort()
	println(xs)

	// `sort_by` takes a comparison returning <0, 0, or >0.
	let people = [Person[name: "Ada", age: 36], Person[name: "Lin", age: 21], Person[name: "Bo", age: 40]]
	people.sort_by(fn(a: Person, b: Person) int: a.age - b.age)
	for(p in people): print("${p.name} ")
	println()

	// Binary search on a sorted list.
	println(xs.binary_search(9) ?? -1)
}
