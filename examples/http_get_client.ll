// examples/http_get_client.ll
// Demonstrates how to send HTTP GET requests in Lowlight:
// 1. Using the quick convenience function `http.get()`
// 2. Using `http.Request.new()` and `http.Client.do()` for customized headers

import "std/http"

bind main = fn(): {
    // =========================================================================
    // 1. Quick GET Request using convenience `http.get(url)`
    // =========================================================================
    println("--- 1. Quick GET with http.get() ---")
    let resp = http.get("https://httpbin.org/uuid"):
        when unreachable(e): {
            println("Server unreachable: ${e.summary()}")
            func <-
        }
        when timeout(_): {
            println("The server took too long to answer")
            func <-
        }
        default err(e): {
            println("Request failed: ${e.summary()}")
            func <-
        }
    println("Status: ${resp.status_code} ${resp.status}")
    println("Body: ${resp.text()}")

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
    let resp2 = client.do(req):
        default: {
            println("Request failed")
            func <-
        }
    println("Status:       ${resp2.status_code} ${resp2.status}")
    println("Content-Type: ${resp2.header("content-type") ?? "none"}")
    println("Response:     ${resp2.body}")
}
