// examples/tcp_echo_server.ll
// Demonstrates running a raw TCP socket server in Lowlight using `std/net.ll`:
// 1. Binding a listening socket with `net.TcpListener.listen(host, port)`
// 2. Accepting incoming connections with `listener.accept(timeout_ms)`
// 3. Reading data with `stream.recv(max_len, timeout_ms)`
// 4. Writing replies with `stream.send(data)`
// 5. Automatic connection cleanup via RAII Drop on `TcpStream`

import "std/net.ll" as net
import "std/fs.ll"

bind main = () {
    let host = "127.0.0.1"
    let port = 9000

    let listener_res = net.TcpListener.listen(host, port)
    if (listener_res.is_err()) {
        println("Failed to start TCP listener: ${listener_res.err?.copy() ?? "error"}")
        func <-
    }
    let listener = listener_res.unwrap()
    println("TCP Echo server listening on ${host}:${listener.port()} ...")
    println("Test with: nc 127.0.0.1 ${listener.port()}")

    for (true) {
        // Accept incoming client connection (timeout: -1 for blocking wait)
        let conn_res = listener.accept(-1)
        if (conn_res.is_err()) {
            println("Accept error: ${conn_res.err?.copy() ?? "error"}")
            next <-
        }
        let stream = conn_res.unwrap()
        println("Accepted incoming client connection!")

        // Send a welcome message
        stream.send("Welcome to Lowlight TCP Server! Send text and it will echo back.\n")

        // Read and echo back
        for (true) {
            let data_res = stream.recv(4096, 10000)
            if (data_res.is_err()) {
                // Connection closed or timed out
                for <-
            }
            let data = data_res.unwrap()
            if (data.is_empty()) {
                // Client disconnected (EOF)
                for <-
            }

            println("Received: ${data.trim()}")
            stream.send("Echo: ${data}")

            if (data.trim() == "quit" or data.trim() == "exit") {
                stream.send("Goodbye!\n")
                for <-
            }
        }
        // `stream` closes automatically when it goes out of scope here via `TcpStream.drop`!
        println("Client disconnected.")
    }
}
