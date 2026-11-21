// examples/http_server.ll
// Demonstrates running an HTTP server in Lowlight:
// 1. Setting up route handlers using `http.ServeMux`
// 2. Reading request methods, paths, headers, and query parameters
// 3. Responding with HTML, JSON, and custom status codes via `http.ResponseWriter`
// 4. Starting the server on `127.0.0.1:8080`

import "std/http"

bind main = (): {
    let mux = http.ServeMux.new()

    // 1. Root route: serve HTML
    mux.handle_func("/", (req: http.Request, res: &http.ResponseWriter): {
        if (req.path != "/"): {
            res.write_header(http.STATUS_NOT_FOUND)
            res.set_header("Content-Type", "text/plain; charset=utf-8")
            res.write("404 Not Found\n")
            func <-
        }
        res.set_header("Content-Type", "text/html; charset=utf-8")
        res.write("<h1>Hello from Lowlight HTTP Server!</h1>\n<p>Visit <a href='/api/hello?name=Developer'>/api/hello?name=Developer</a></p>\n")
    })

    // 2. JSON API endpoint with query parameters
    mux.handle_func("/api/hello", (req: http.Request, res: &http.ResponseWriter): {
        let name = req.query_param("name") ?? "World"
        res.set_header("Content-Type", "application/json")
        res.write("{\"message\": \"Hello, ${name}!\"}\n")
    })

    // 3. POST endpoint reading request body
    mux.handle_func("/api/echo", (req: http.Request, res: &http.ResponseWriter): {
        if (req.method != "POST"): {
            res.write_header(http.STATUS_METHOD_NOT_ALLOWED)
            res.write("Method Not Allowed\n")
            func <-
        }
        res.set_header("Content-Type", "application/json")
        res.write("{\"received\": \"${req.body}\", \"bytes\": ${req.body.len()}}\n")
    })

    let addr = "127.0.0.1:8080"
    println("Starting Lowlight HTTP server on http://${addr}/ ...")
    println("Press Ctrl+C to stop.")

    let srv_res! = http.listen_and_serve_mux(addr, mux)
    if (srv_res!.is_err()): {
        println("Server failed: ${srv_res!.unwrap_err()}")
    }
}
