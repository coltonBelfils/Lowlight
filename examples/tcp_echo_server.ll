// examples/tcp_echo_server.ll
// Demonstrates running a raw TCP socket server in Lowlight using `std/net`:
// 1. Binding a listening socket with `net.TcpListener.listen(host, port)`
// 2. Accepting incoming connections with `listener.accept(timeout_ms)`
// 3. Reading data with `stream.recv(max_len, timeout_ms)`
// 4. Writing replies with `stream.send(data)`
// 5. Automatic connection cleanup via RAII Drop on `TcpStream`

import "std/net"

bind main = fn(): {
    let host = "127.0.0.1"
    let port = 9000

    let listener = net.TcpListener.listen(host, port):
        when in_use(_): {
            println("Port ${port} is already in use; stop the other server or pick another port")
            func <-
        }
        default err(e): {
            println("Failed to start TCP listener: ${e.summary()}")
            func <-
        }
    println("TCP Echo server listening on ${host}:${listener.port()} ...")
    println("Test with: nc 127.0.0.1 ${listener.port()}")

    for (true): {
        // Accept incoming client connection (timeout: -1 for blocking wait)
        let stream = listener.accept(-1):
            default: next <-
        println("Accepted incoming client connection!")

        // Send a welcome message
        stream.send("Welcome to Lowlight TCP Server! Send text and it will echo back.\n"):
            default: next <-

        // Read and echo back
        for (true): {
            let data = stream.recv(4096, 10000):
                when eof(_): for <-        // the client disconnected
                when timeout(_): for <-    // no data within 10 s
                default err(e): {
                    println("Read error: ${e.summary()}")
                    for <-
                }

            println("Received: ${data.trim()}")
            stream.send("Echo: ${data}"):
                default: for <-

            if (data.trim() == "quit" or data.trim() == "exit"): {
                let _ = stream.send("Goodbye!\n")
                for <-
            }
        }
        // `stream` closes automatically when it goes out of scope here via `TcpStream.drop`!
        println("Client disconnected.")
    }
}
