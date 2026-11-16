// Lowlight Standard Library: std/net.ll
// TCP networking primitives: TcpStream, TcpListener, socketpair, and connection helpers.

// TcpStream represents an active TCP socket connection.
// It implements Drop to automatically close the underlying file descriptor at scope exit.
define TcpStream struct [
    _fd: int,
]

bind TcpStream.raw_fd = (self) int: {
    self._fd
}

bind TcpStream.drop = (&self): {
    if (self._fd >= 0): {
        unsafe sys_net_close(self._fd)
        self._fd = -1
    }
}

bind TcpStream.close = (&self): {
    if (self._fd >= 0): {
        unsafe sys_net_close(self._fd)
        self._fd = -1
    }
}

bind TcpStream.is_open = (self) bool: {
    self._fd >= 0
}

bind TcpStream.send = (&self, data: string) Result[int]: {
    if (self._fd < 0): {
        func <- Result[int].fail(Error.new("send on closed TcpStream"))
    }
    let res = unsafe sys_net_send_all(self._fd, data.c_str(), data.len())
    if (res == 0): {
        Result[int].ok(data.len())
    } else: {
        Result[int].fail(Error.new("failed to send data over TCP socket"))
    }
}

bind TcpStream.recv = (&self, max_len: int, timeout_ms: int) Result[string]: {
    if (self._fd < 0): {
        func <- Result[string].fail(Error.new("recv on closed TcpStream"))
    }
    let limit = if (max_len <= 0): 4096 else: max_len
    let buf = ""
    unsafe {
        let raw = alloc_raw[u8](limit)
        let n = sys_net_recv(self._fd, raw, limit, timeout_ms)
        if (n < 0): {
            free_raw(raw)
            if (n == -2): func <- Result[string].fail(Error.coded("timeout", "read timeout"))
            func <- Result[string].fail(Error.new("recv error"))
        }
        for (i in 0..n): {
            buf.push_byte(*ptr_add(raw, i))
        }
        free_raw(raw)
    }
    Result[string].ok(own buf)
}

bind TcpStream.connect = (host: string, port: int, timeout_ms: int) Result[TcpStream]: {
    let client_fd = unsafe sys_tcp_connect(host.c_str(), port, timeout_ms)
    if (client_fd >= 0): {
        Result[TcpStream].ok(TcpStream[_fd: client_fd])
    } else if (client_fd == -1): {
        Result[TcpStream].fail(Error.new("failed to resolve host: $host"))
    } else: {
        Result[TcpStream].fail(Error.new("failed to connect to ${host}:${port}"))
    }
}

// TcpListener represents a bound, listening TCP server socket.
// It implements Drop to automatically close the listening socket at scope exit.
define TcpListener struct [
    _fd: int,
    _bound_port: int,
]

bind TcpListener.raw_fd = (self) int: {
    self._fd
}

bind TcpListener.is_open = (self) bool: {
    self._fd >= 0
}

bind TcpListener.drop = (&self): {
    if (self._fd >= 0): {
        unsafe sys_net_close(self._fd)
        self._fd = -1
    }
}

bind TcpListener.close = (&self): {
    if (self._fd >= 0): {
        unsafe sys_net_close(self._fd)
        self._fd = -1
    }
}

bind TcpListener.port = (self) int: {
    self._bound_port
}

bind TcpListener.listen = (host: string, port: int) Result[TcpListener]: {
    let sfd = unsafe sys_tcp_listen(host.c_str(), port, 128)
    if (sfd < 0): {
        func <- Result[TcpListener].fail(Error.new("failed to bind TCP listener on ${host}:${port}"))
    }
    let actual_port = unsafe sys_tcp_port(sfd)
    Result[TcpListener].ok(TcpListener[
        _fd: sfd,
        _bound_port: if (actual_port > 0): actual_port else: port,
    ])
}

bind TcpListener.accept = (&self, timeout_ms: int) Result[TcpStream]: {
    if (self._fd < 0): {
        func <- Result[TcpStream].fail(Error.new("accept on closed TcpListener"))
    }
    let cfd = unsafe sys_tcp_accept(self._fd, timeout_ms)
    if (cfd >= 0): {
        Result[TcpStream].ok(TcpStream[_fd: cfd])
    } else if (cfd == -2): {
        Result[TcpStream].fail(Error.coded("timeout", "accept timeout"))
    } else: {
        Result[TcpStream].fail(Error.new("failed to accept incoming TCP connection"))
    }
}

// SocketPair represents a pair of interconnected, bidirectional TcpStreams.
define SocketPair struct [
    first: TcpStream,
    second: TcpStream,
]

// socketpair creates a connected pair of bidirectional TcpStreams (using kernel AF_UNIX socketpair).
// Ideal for in-memory testing, pipelines, and inter-process communication.
bind socketpair = () Result[SocketPair]: {
    let pair = unsafe {
        let p = alloc_raw[int](2)
        let rc = sys_net_socketpair(p)
        if (rc != 0): {
            free_raw(p)
            func <- Result[SocketPair].fail(Error.new("failed to create socketpair"))
        }
        let f0 = ptr_read(p)
        let f1 = ptr_read(ptr_add(p, 1))
        free_raw(p)
        SocketPair[
            first: TcpStream[_fd: f0],
            second: TcpStream[_fd: f1],
        ]
    }
    Result[SocketPair].ok(own pair)
}

bind dial_tcp = (host: string, port: int) Result[TcpStream]: {
    TcpStream.connect(host, port, 10000)
}

bind listen_tcp = (host: string, port: int) Result[TcpListener]: {
    TcpListener.listen(host, port)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("net bidirectional data transfer over socketpair")
bind test_net_socketpair_bidirectional = (): {
    let pair_res = socketpair()
    assert(pair_res.is_ok(), "socketpair succeeds")
    let pair = pair_res.unwrap()

    assert(pair.first.is_open(), "pair.first is open")
    assert(pair.second.is_open(), "pair.second is open")

    let send1_res = pair.first.send("ping from first")
    assert(send1_res.is_ok(), "send from first succeeds")

    let recv1_res = pair.second.recv(1024, 1000)
    assert(recv1_res.is_ok(), "recv on second succeeds")
    assert(recv1_res.unwrap() == "ping from first", "second received expected payload")

    let send2_res = pair.second.send("pong from second")
    assert(send2_res.is_ok(), "send from second succeeds")

    let recv2_res = pair.first.recv(1024, 1000)
    assert(recv2_res.is_ok(), "recv on first succeeds")
    assert(recv2_res.unwrap() == "pong from second", "first received expected payload")

    pair.first.close()
    pair.second.close()
    assert(not pair.first.is_open(), "first is closed")
    assert(not pair.second.is_open(), "second is closed")
}

@test("net TcpListener ephemeral port binding and teardown")
bind test_net_listener_bind_and_port = (): {
    let l_res = TcpListener.listen("127.0.0.1", 0)
    assert(l_res.is_ok(), "listen on ephemeral port 0 succeeds")
    let l = l_res.unwrap()

    let bound_p = l.port()
    assert(bound_p > 0, "ephemeral port is positive integer")

    l.close()
    assert(not l.is_open(), "listener fd invalidated after close")
}

@test("net closed TcpStream error handling")
bind test_net_closed_stream_errors = (): {
    let s = TcpStream[_fd: -1]
    assert(not s.is_open(), "stream with fd -1 is not open")

    let send_res = s.send("test")
    assert(send_res.is_err(), "send on closed stream returns error")

    let recv_res = s.recv(512, 100)
    assert(recv_res.is_err(), "recv on closed stream returns error")
}

