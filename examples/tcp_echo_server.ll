// examples/tcp_echo_server.ll
// Demonstrates running a raw TCP socket server in Lowlight using `std/net`:
// 1. Binding a listening socket with `net.TcpListener.listen(host, port)`
// 2. Accepting incoming connections with `listener.accept(timeout_ms)`
// 3. Reading data with `stream.recv(max_len, timeout_ms)`
// 4. Writing replies with `stream.send(data)`
// 5. Automatic connection cleanup via RAII Drop on `TcpStream`

import "std/net"

bind main = (): {
    let host = "127.0.0.1"
    let port = 9000

    let listener_res = net.TcpListener.listen(host, port)
    if (listener_res.is_err()): {
        println("Failed to start TCP listener: ${listener_res.unwrap_err()}")
        func <-
    }
    let listener = listener_res.unwrap()
    println("TCP Echo server listening on ${host}:${listener.port()} ...")
    println("Test with: nc 127.0.0.1 ${listener.port()}")

    for (true): {
        // Accept incoming client connection (timeout: -1 for blocking wait)
        let conn_res = listener.accept(-1)
        if (conn_res.is_err()): {
            println("Accept error: ${conn_res.unwrap_err()}")
            next <-
        }
        let stream = conn_res.unwrap()
        println("Accepted incoming client connection!")

        // Send a welcome message
        if (stream.send("Welcome to Lowlight TCP Server! Send text and it will echo back.\n").is_err()): {
            next <-
        }

        // Read and echo back
        for (true): {
            let data_res = stream.recv(4096, 10000)
            if (data_res.is_err()): {
                // `"eof"`: client disconnected cleanly; `"timeout"`: no data within 10s
                for <-
            }
            let data = data_res.unwrap()

            println("Received: ${data.trim()}")
            if (stream.send("Echo: ${data}").is_err()): {
                for <-
            }

            if (data.trim() == "quit" or data.trim() == "exit"): {
                let _ = stream.send("Goodbye!\n")
                for <-
            }
        }
        // `stream` closes automatically when it goes out of scope here via `TcpStream.drop`!
        println("Client disconnected.")
    }
}
