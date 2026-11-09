// examples/http_get_client.ll
// Demonstrates how to send HTTP GET requests in Lowlight:
// 1. Using the quick convenience function `http.get()`
// 2. Using `http.Request.new()` and `http.Client.do()` for customized headers

import "std/http.ll" as http
import "std/fs.ll"

bind main = () -> {
    // =========================================================================
    // 1. Quick GET Request using convenience `http.get(url)`
    // =========================================================================
    println("--- 1. Quick GET with http.get() ---")
    let res1 = http.get("https://httpbin.org/uuid")
    if (res1.is_ok()) {
        let resp = res1.unwrap()
        println("Status: ${resp.status_code} ${resp.status}")
        println("Body: ${resp.text()}")
    } else {
        println("Request failed: ${res1.err?.copy() ?? "error"}")
    }

    // =========================================================================
    // 2. Configured GET Request using `http.Request.new()` and `http.Client`
    // =========================================================================
    println("\n--- 2. Custom GET with http.Request.new() and Client.do() ---")
    // Construct the request: method, URL, and body ("" for GET)
    let req = http.Request.new("GET", "https://httpbin.org/uuid", "")

    // Set custom headers
    req.set_header("User-Agent", "LowlightClient/1.0")

    // Send via Client
    let client = http.Client.new()
    let res2 = client.do(req)
    if (res2.is_ok()) {
        let resp = res2.unwrap()
        println("Status:       ${resp.status_code} ${resp.status}")
        println("Content-Type: ${resp.header("content-type") ?? "none"}")
        println("Response:     ${resp.body}")
    } else {
        println("Request failed: ${res2.err?.copy() ?? "error"}")
    }
}
