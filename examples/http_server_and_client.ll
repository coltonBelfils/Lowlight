// examples/http_server_and_client.ll
// Showcases Lowlight's standard HTTP package (`std/http.ll`):
// - URL parsing and query parameters (`http.parse_url`, `url.query_param`)
// - Request & Response modeling (`http.Request`, `http.Response`)
// - ResponseWriter with headers and streaming chunks (`http.ResponseWriter`)
// - Route matching and dispatch with `http.ServeMux` (exact and prefix matching)
// - Kernel socketpair testing and HTTP client requests

import "std/http.ll" as http
import "std/fs.ll"

bind main = () {
    println("==================================================")
    println(" Lowlight Standard HTTP Showcase (std/http.ll)    ")
    println("==================================================")

    // 1. URL parsing & query extraction
    let api_url_res = http.parse_url("http://localhost:8080/api/v1/search?q=lowlight&format=json&limit=10")
    assert(api_url_res.is_ok())
    let u = api_url_res.unwrap()
    println("1. Parsed URL:")
    println("   Scheme:   ${u.scheme}")
    println("   Host:     ${u.host}")
    println("   Path:     ${u.path}")
    println("   Query:    q=${u.query_param("q") ?? ""}, format=${u.query_param("format") ?? ""}, limit=${u.query_param("limit") ?? ""}")

    // 2. Set up ServeMux router
    let mux = http.ServeMux.new()

    // Route: "/" index (idiomatic Go pattern: verify exact path or handle root)
    mux.handle_func("/", (req: http.Request, res: &http.ResponseWriter) {
        if (req.path != "/") {
            res.write_header(http.STATUS_NOT_FOUND)
            res.write("404 page not found\n")
            func <-
        }
        res.set_header("Content-Type", "text/plain; charset=utf-8")
        res.write("Welcome to Lowlight HTTP Server!")
    })

    // Route: "/api/hello"
    mux.handle_func("/api/hello", (req: http.Request, res: &http.ResponseWriter) {
        let name = req.query_param("name") ?? "friend"
        res.set_header("Content-Type", "application/json")
        res.write("{\"greeting\":\"Hello, ${name}!\"}")
    })

    // Route: "/api/echo" (POST handler with request body inspection)
    mux.handle_func("/api/echo", (req: http.Request, res: &http.ResponseWriter) {
        if (req.method != "POST") {
            res.write_header(http.STATUS_METHOD_NOT_ALLOWED)
            res.write("{\"error\":\"Method Not Allowed\"}")
            func <-
        }
        res.set_header("Content-Type", "application/json")
        res.write_header(http.STATUS_OK)
        res.write("{\"echo_body\": \"${req.body}\", \"received_bytes\": ${req.body.len()}}")
    })

    // 3. Dispatch simulated requests through ServeMux
    println("\n2. Testing ServeMux In-Memory Dispatch:")

    let m1 = mux.copy()
    let req_home = http.Request.new("GET", "/", "")
    let res_home = http.test_handler((r: http.Request, w: &http.ResponseWriter) {
        m1.serve_http(r, &w)
    }, req_home)
    println("   GET / -> Status: ${res_home.status_code}, Body: ${res_home.body}")
    assert(res_home.status_code == 200)

    let m2 = mux.copy()
    let req_hello = http.Request.new("GET", "/api/hello?name=Developer", "")
    let res_hello = http.test_handler((r: http.Request, w: &http.ResponseWriter) {
        m2.serve_http(r, &w)
    }, req_hello)
    println("   GET /api/hello?name=Developer -> Status: ${res_hello.status_code}, Body: ${res_hello.body}")
    assert(res_hello.status_code == 200)
    assert(res_hello.body.contains("Hello, Developer!"))

    let m3 = mux.copy()
    let req_404 = http.Request.new("GET", "/missing/page", "")
    let res_404 = http.test_handler((r: http.Request, w: &http.ResponseWriter) {
        m3.serve_http(r, &w)
    }, req_404)
    println("   GET /missing/page -> Status: ${res_404.status_code}, Body: ${res_404.body.trim()}")
    assert(res_404.status_code == 404)

    // 4. Full End-to-End Kernel Socketpair Roundtrip
    println("\n3. Full Socket-Level Kernel Client/Server Roundtrip:")
    let req_echo = http.Request.new("POST", "/api/echo", "Hello from client over kernel socket!")
    req_echo.set_header("Content-Type", "application/json")

    let m4 = mux.copy()
    let roundtrip_res = http.test_roundtrip((r: http.Request, w: &http.ResponseWriter) {
        m4.serve_http(r, &w)
    }, req_echo)
    assert(roundtrip_res.is_ok())
    let resp = roundtrip_res.unwrap()
    println("   POST /api/echo (over socket) -> Status: ${resp.status_code} ${resp.status}")
    println("   Response Content-Type: ${resp.header("content-type") ?? "none"}")
    println("   Response Body:         ${resp.body}")
    assert(resp.status_code == 200)
    assert(resp.body.contains("Hello from client over kernel socket!"))

    println("\n✓ All HTTP client and server showcase demonstrations completed successfully!")
}
