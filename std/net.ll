// Lowlight Standard Library: std/net.ll
// TCP networking primitives: TcpStream, TcpListener, socketpair, and connection helpers.

import "std/fs.ll"

extern ll_net_tcp_listen: (host: ptr[u8], port: int, backlog: int): int
extern ll_net_tcp_port: (fd: int): int
extern ll_net_tcp_accept: (server_fd: int, timeout_ms: int): int
extern ll_net_tcp_connect: (host: ptr[u8], port: int, timeout_ms: int): int
extern ll_net_send_all: (fd: int, data: ptr[u8], len: int): int
extern ll_net_recv: (fd: int, buf: ptr[u8], max_len: int, timeout_ms: int): int
extern ll_net_close: (fd: int)
extern ll_net_shutdown: (fd: int, how: int)
extern ll_net_set_timeout: (fd: int, timeout_ms: int): int
extern ll_net_socketpair: (out_fds: ptr[int]): int

// TcpStream represents an active TCP socket connection.
// It implements Drop to automatically close the underlying file descriptor at scope exit.
define TcpStream struct [
    fd: int,
]

bind TcpStream.drop = (&self) {
    if (self.fd >= 0) {
        unsafe ll_net_close(self.fd)
        self.fd = -1
    }
}

bind TcpStream.close = (&self) {
    if (self.fd >= 0) {
        unsafe ll_net_close(self.fd)
        self.fd = -1
    }
}

bind TcpStream.is_open = (self): bool {
    self.fd >= 0
}

bind TcpStream.send = (&self, data: string): Result[int, string] {
    if (self.fd < 0) {
        func <- Result[int, string].fail("send on closed TcpStream")
    }
    let res = unsafe ll_net_send_all(self.fd, data.c_str(), data.len())
    if (res == 0) {
        Result[int, string].ok(data.len())
    } else {
        Result[int, string].fail("failed to send data over TCP socket")
    }
}

bind TcpStream.recv = (&self, max_len: int, timeout_ms: int): Result[string, string] {
    if (self.fd < 0) {
        func <- Result[string, string].fail("recv on closed TcpStream")
    }
    let limit = if (max_len <= 0) 4096 else max_len
    let buf = ""
    unsafe {
        let raw = alloc_raw[u8](limit)
        let n = ll_net_recv(self.fd, raw, limit, timeout_ms)
        if (n < 0) {
            free_raw(raw)
            if (n == -2) func <- Result[string, string].fail("read timeout")
            func <- Result[string, string].fail("recv error")
        }
        for (i in 0..n) {
            buf.push_byte(*ptr_add(raw, i))
        }
        free_raw(raw)
    }
    Result[string, string].ok(own buf)
}

bind TcpStream.connect = (host: string, port: int, timeout_ms: int): Result[TcpStream, string] {
    let client_fd = unsafe ll_net_tcp_connect(host.c_str(), port, timeout_ms)
    if (client_fd >= 0) {
        Result[TcpStream, string].ok(TcpStream[fd: client_fd])
    } else if (client_fd == -1) {
        Result[TcpStream, string].fail("failed to resolve host: $host")
    } else {
        Result[TcpStream, string].fail("failed to connect to ${host}:${port}")
    }
}

// TcpListener represents a bound, listening TCP server socket.
// It implements Drop to automatically close the listening socket at scope exit.
define TcpListener struct [
    fd: int,
    bound_port: int,
]

bind TcpListener.drop = (&self) {
    if (self.fd >= 0) {
        unsafe ll_net_close(self.fd)
        self.fd = -1
    }
}

bind TcpListener.close = (&self) {
    if (self.fd >= 0) {
        unsafe ll_net_close(self.fd)
        self.fd = -1
    }
}

bind TcpListener.port = (self): int {
    self.bound_port
}

bind TcpListener.listen = (host: string, port: int): Result[TcpListener, string] {
    let sfd = unsafe ll_net_tcp_listen(host.c_str(), port, 128)
    if (sfd < 0) {
        func <- Result[TcpListener, string].fail("failed to bind TCP listener on ${host}:${port}")
    }
    let actual_port = unsafe ll_net_tcp_port(sfd)
    Result[TcpListener, string].ok(TcpListener[
        fd: sfd,
        bound_port: if (actual_port > 0) actual_port else port,
    ])
}

bind TcpListener.accept = (&self, timeout_ms: int): Result[TcpStream, string] {
    if (self.fd < 0) {
        func <- Result[TcpStream, string].fail("accept on closed TcpListener")
    }
    let cfd = unsafe ll_net_tcp_accept(self.fd, timeout_ms)
    if (cfd >= 0) {
        Result[TcpStream, string].ok(TcpStream[fd: cfd])
    } else if (cfd == -2) {
        Result[TcpStream, string].fail("accept timeout")
    } else {
        Result[TcpStream, string].fail("failed to accept incoming TCP connection")
    }
}

// SocketPair represents a pair of interconnected, bidirectional TcpStreams.
define SocketPair struct [
    first: TcpStream,
    second: TcpStream,
]

// socketpair creates a connected pair of bidirectional TcpStreams (using kernel AF_UNIX socketpair).
// Ideal for in-memory testing, pipelines, and inter-process communication.
bind socketpair = (): Result[SocketPair, string] {
    let pair = unsafe {
        let p = alloc_raw[int](2)
        let rc = ll_net_socketpair(p)
        if (rc != 0) {
            free_raw(p)
            func <- Result[SocketPair, string].fail("failed to create socketpair")
        }
        let f0 = ptr_read(p)
        let f1 = ptr_read(ptr_add(p, 1))
        free_raw(p)
        SocketPair[
            first: TcpStream[fd: f0],
            second: TcpStream[fd: f1],
        ]
    }
    Result[SocketPair, string].ok(own pair)
}

bind dial_tcp = (host: string, port: int): Result[TcpStream, string] {
    TcpStream.connect(host, port, 10000)
}

bind listen_tcp = (host: string, port: int): Result[TcpListener, string] {
    TcpListener.listen(host, port)
}
