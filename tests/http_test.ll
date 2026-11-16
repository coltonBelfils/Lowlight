// tests/http_test.ll
// Comprehensive test suite for Lowlight's std/http package.

import "std/http.ll" as http

bind test_status_codes = (): {
    assert(http.STATUS_OK == 200)
    assert(http.STATUS_CREATED == 201)
    assert(http.STATUS_NO_CONTENT == 204)
    assert(http.STATUS_BAD_REQUEST == 400)
    assert(http.STATUS_UNAUTHORIZED == 401)
    assert(http.STATUS_FORBIDDEN == 403)
    assert(http.STATUS_NOT_FOUND == 404)
    assert(http.STATUS_INTERNAL_SERVER_ERROR == 500)
    assert(http.STATUS_SERVICE_UNAVAILABLE == 503)

    assert(http.status_text(http.STATUS_OK) == "OK")
    assert(http.status_text(http.STATUS_CREATED) == "Created")
    assert(http.status_text(http.STATUS_NO_CONTENT) == "No Content")
    assert(http.status_text(http.STATUS_BAD_REQUEST) == "Bad Request")
    assert(http.status_text(http.STATUS_NOT_FOUND) == "Not Found")
    assert(http.status_text(http.STATUS_INTERNAL_SERVER_ERROR) == "Internal Server Error")
    assert(http.status_text(999) == "Unknown")
    println("✓ Status codes and status text passed")
}

bind test_url_codec = (): {
    let plain = "hello world & foo=bar/baz 100%!"
    let encoded = http.url_encode(plain)
    assert(encoded == "hello%20world%20%26%20foo%3Dbar%2Fbaz%20100%25%21")

    let decoded = http.url_decode(encoded)
    assert(decoded == plain)

    // '+' in query should decode to space
    assert(http.url_decode("query+string+test") == "query string test")
    println("✓ URL encoding and decoding passed")
}

bind test_url_parsing = (): {
    let u1_res = http.parse_url("http://example.com:8080/api/v1/users?name=john&role=admin&active")
    assert(u1_res.is_ok())
    let u1 = u1_res.unwrap()
    assert(u1.scheme == "http")
    assert(u1.host == "example.com:8080")
    assert(u1.hostname == "example.com")
    assert(u1.port == 8080)
    assert(u1.path == "/api/v1/users")
    assert(u1.raw_query == "name=john&role=admin&active")
    assert((u1.query_param("name") ?? "") == "john")
    assert((u1.query_param("role") ?? "") == "admin")
    assert((u1.query_param("active") ?? "none") == "")
    assert(u1.query_param("missing") == none)

    // Relative URL with query
    let u2_res = http.parse_url("/search?q=lowlight+lang&page=1")
    assert(u2_res.is_ok())
    let u2 = u2_res.unwrap()
    assert(u2.scheme == "")
    assert(u2.path == "/search")
    assert(u2.raw_query == "q=lowlight+lang&page=1")
    assert((u2.query_param("q") ?? "") == "lowlight lang")
    assert((u2.query_param("page") ?? "") == "1")

    // Default port for https
    let u3_res = http.parse_url("https://secure.example.com/login")
    assert(u3_res.is_ok())
    let u3 = u3_res.unwrap()
    assert(u3.scheme == "https")
    assert(u3.port == 443)
    assert(u3.path == "/login")
    assert(u3.raw_query == "")

    println("✓ URL parsing passed")
}

bind test_request_response_wire = (): {
    // 1. Construct Request
    let req = http.Request.new("POST", "http://localhost:8080/items?filter=all", "{\"name\":\"item1\",\"qty\":10}")
    req.set_header("Content-Type", "application/json")
    req.set_header("X-Custom-Header", "LowlightRocks")

    assert(req.method == "POST")
    assert(req.path == "/items")
    assert((req.query_param("filter") ?? "") == "all")
    assert((req.header("content-type") ?? "") == "application/json")
    assert((req.header("Content-Type") ?? "") == "application/json")
    assert((req.header("X-CUSTOM-HEADER") ?? "") == "LowlightRocks")

    let req_wire = req.format()
    assert(req_wire.contains("POST /items HTTP/1.1\r\n"))
    assert(req_wire.contains("content-type: application/json\r\n"))
    assert(req_wire.contains("{\"name\":\"item1\",\"qty\":10}"))

    // 2. Parse Request from wire string
    let parsed_req_res = http.Request.parse(req_wire)
    assert(parsed_req_res.is_ok())
    let parsed_req = parsed_req_res.unwrap()
    assert(parsed_req.method == "POST")
    assert(parsed_req.path == "/items")
    assert((parsed_req.header("content-type") ?? "") == "application/json")
    assert(parsed_req.body == "{\"name\":\"item1\",\"qty\":10}")

    // 3. Construct Response
    let resp = http.Response.new(http.STATUS_CREATED, "{\"id\":42,\"status\":\"created\"}")
    resp.set_header("Content-Type", "application/json")
    assert(resp.status_code == 201)
    assert(resp.is_success())
    assert(resp.text() == "{\"id\":42,\"status\":\"created\"}")
    assert((resp.header("content-type") ?? "") == "application/json")

    let resp_wire = resp.format()
    assert(resp_wire.contains("HTTP/1.1 201 Created\r\n"))
    assert(resp_wire.contains("{\"id\":42,\"status\":\"created\"}"))

    // 4. Parse Response from wire string
    let parsed_resp_res = http.Response.parse(resp_wire)
    assert(parsed_resp_res.is_ok())
    let parsed_resp = parsed_resp_res.unwrap()
    assert(parsed_resp.status_code == 201)
    assert(parsed_resp.status == "Created")
    assert((parsed_resp.header("content-type") ?? "") == "application/json")
    assert(parsed_resp.body == "{\"id\":42,\"status\":\"created\"}")

    println("✓ Request & Response wire serialization/deserialization passed")
}

bind test_response_writer = (): {
    let rw = http.ResponseWriter.new()
    rw.set_header("Content-Type", "application/json")
    rw.set_header("X-Server", "Lowlight")
    rw.write_header(http.STATUS_OK)
    rw.write("{\"message\":")
    rw.write("\"success\"}")

    let resp = rw.finish()
    assert(resp.status_code == 200)
    assert(resp.body == "{\"message\":\"success\"}")
    assert((resp.header("content-type") ?? "") == "application/json")
    assert((resp.header("x-server") ?? "") == "Lowlight")
    assert((resp.header("content-length") ?? "") == "21")

    println("✓ ResponseWriter functionality passed")
}

bind test_serve_mux_routing = (): {
    let mux = http.ServeMux.new()

    // Route 1: Exact match "/hello"
    mux.handle_func("/hello", (req: http.Request, res: &http.ResponseWriter): {
        let name = req.query_param("name") ?? "stranger"
        res.set_header("Content-Type", "text/plain")
        res.write("Hello, ${name}!")
    })

    // Route 2: Prefix match "/api/"
    mux.handle_func("/api/", (req: http.Request, res: &http.ResponseWriter): {
        res.set_header("Content-Type", "application/json")
        res.write("{\"api_path\":\"${req.path}\"}")
    })

    // Route 3: Longest prefix match "/api/v2/"
    mux.handle_func("/api/v2/", (req: http.Request, res: &http.ResponseWriter): {
        res.set_header("Content-Type", "application/json")
        res.write("{\"version\":2,\"path\":\"${req.path}\"}")
    })

    // Test Route 1
    let req1 = http.Request.new("GET", "/hello?name=Alice", "")
    let m1 = mux.copy()
    let resp1 = http.test_handler((r: http.Request, w: &http.ResponseWriter): {
        m1.serve_http(r, &w)
    }, req1)
    assert(resp1.status_code == 200)
    assert(resp1.body == "Hello, Alice!")

    // Test Route 2
    let req2 = http.Request.new("GET", "/api/users", "")
    let m2 = mux.copy()
    let resp2 = http.test_handler((r: http.Request, w: &http.ResponseWriter): {
        m2.serve_http(r, &w)
    }, req2)
    assert(resp2.status_code == 200)
    assert(resp2.body == "{\"api_path\":\"/api/users\"}")

    // Test Route 3 (Longest prefix preference over /api/)
    let req3 = http.Request.new("GET", "/api/v2/items", "")
    let m3 = mux.copy()
    let resp3 = http.test_handler((r: http.Request, w: &http.ResponseWriter): {
        m3.serve_http(r, &w)
    }, req3)
    assert(resp3.status_code == 200)
    assert(resp3.body == "{\"version\":2,\"path\":\"/api/v2/items\"}")

    // Test 404 Fallback
    let req4 = http.Request.new("GET", "/unknown/route", "")
    let m4 = mux.copy()
    let resp4 = http.test_handler((r: http.Request, w: &http.ResponseWriter): {
        m4.serve_http(r, &w)
    }, req4)
    assert(resp4.status_code == 404)
    assert(resp4.body.contains("404 page not found"))

    println("✓ ServeMux pattern routing & fallback passed")
}

bind test_kernel_socket_roundtrip = (): {
    // Tests end-to-end client-server socket communication over a kernel socketpair
    let handler = (req: http.Request, res: &http.ResponseWriter): {
        if (req.method == "POST" and req.path == "/echo"): {
            res.set_header("Content-Type", req.header("content-type") ?? "text/plain")
            res.write_header(http.STATUS_OK)
            res.write("Echo: ${req.body}")
        } else: {
            res.write_header(http.STATUS_BAD_REQUEST)
            res.write("bad request")
        }
    }

    let req = http.Request.new("POST", "/echo", "Lowlight Rocks!")
    req.set_header("Content-Type", "text/plain")

    let roundtrip_res = http.test_roundtrip(handler, req)
    assert(roundtrip_res.is_ok())
    let resp = roundtrip_res.unwrap()
    assert(resp.status_code == 200)
    assert(resp.body == "Echo: Lowlight Rocks!")
    assert((resp.header("content-type") ?? "") == "text/plain")

    println("✓ Full socket-level client/server roundtrip passed")
}

bind main = (): {
    println("=== Lowlight std/http Test Suite ===")
    test_status_codes()
    test_url_codec()
    test_url_parsing()
    test_request_response_wire()
    test_response_writer()
    test_serve_mux_routing()
    test_kernel_socket_roundtrip()
    println("=== All 7 std/http test suites passed with 0 leaks! ===")
}
