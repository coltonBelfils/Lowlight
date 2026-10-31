import "std/net.ll"
import "std/http.ll"
import "std/fs.ll"

bind main = () {
	println(abc(1))
	println()
	println(abc(2))
	println()
	println(abc(3))
}

bind abc = (i: int): int {
    println(i)
    if (i > 10000) <- -1
    i + abc(i*i+1)
}