// Title: HTTP handlers
// Category: Standard library
// Order: 74

import "std/http"

// Handlers receive the request and a writer for the response.
bind hello = fn(req: http.Request, res: &http.ResponseWriter): {
	let name = req.query_param("name") ?? "world"
	res.set_header("Content-Type", "text/plain")
	res.write("hello, $name")
}

bind main = fn(): {
	let mux = http.ServeMux.new()
	mux.handle_func("/hello", hello)

	// Exercise the router in-process, the way a test would. A real
	// server calls `http.listen_and_serve_mux("127.0.0.1:8080", mux)`.
	for(path in ["/hello?name=Ada", "/hello", "/missing"]): {
		let w = http.ResponseWriter.new()
		mux.serve_http(http.Request.new("GET", path, ""), &w)
		let r = w.finish()
		println("$path -> ${r.status_code} ${r.body.trim()}")
	}
}
