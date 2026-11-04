import "std/net.ll"
import "std/http.ll"
import "std/fs.ll"
import "std/strfmt.ll"
import "std/test.ll"

bind main = () {
	let ll = for (i in l): #[int: int] MapEntry[key: i, value: i * 2]

	println(ll)
}

bind l = for (x in 1..=4): [int] x

@test("pass")
bind t = () assert_eq(1, 1)


@test("fail")
bind t = () assert_eq(1, 2)