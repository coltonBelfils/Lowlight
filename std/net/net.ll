// std/net — TCP sockets, listeners, and bidirectional socket pairs.
//
// Provides [`TcpStream`], [`TcpListener`], and [`SocketPair`] with deterministic
// [`Drop`] cleanup for socket descriptors and timeout-aware I/O returning [`Result`].

// Connected bidirectional TCP socket stream.
//
// Implements [`Drop`] to close the underlying socket descriptor automatically at scope exit.
//
// # Example
// ```lowlight
// let conn = dial_tcp("127.0.0.1", 8080).unwrap()
// let _ = conn.send("GET / HTTP/1.0\r\n\r\n")
// ```
define TcpStream struct [
	_fd: int,
]

// Returns the underlying OS file descriptor, or `-1` if closed.
bind TcpStream.raw_fd = (self) int: self._fd

// Automatically closes the socket descriptor if still open when `self` leaves scope.
bind TcpStream.drop = (&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the TCP stream descriptor early, setting it to `-1`.
bind TcpStream.close = (&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Reports whether the stream holds an open socket descriptor (`raw_fd >= 0`).
bind TcpStream.is_open = (self) bool: self._fd >= 0

// Writes all bytes of `data` to the socket and returns the number of bytes sent.
//
// # Errors
// Fails if the stream is closed or the underlying OS send fails.
bind TcpStream.send = (&self, data: string) Result[int]: {
	if(self._fd < 0): func <- Result[int].fail(Error.new("send on closed TcpStream"))
	let res = unsafe sys_net_send_all(self._fd, data.c_str(), data.len())
	if(res == 0): Result[int].ok(data.len())
	else: Result[int].fail(Error.new("failed to send data over TCP socket"))
}

// Reads up to `max_len` bytes (defaulting to `4096` when `max_len <= 0`) waiting up to `timeout_ms`.
//
// Returns the bytes received by a single OS read, which may be fewer than `max_len`.
//
// - `max_len`: Maximum byte count to read in one call.
// - `timeout_ms`: Timeout in milliseconds (negative waits indefinitely).
//
// # Errors
// Fails with error code `"eof"` when the peer has closed the connection cleanly, `"timeout"`
// when no data arrives within `timeout_ms`, or a generic [`Error`] on closed/failed sockets.
bind TcpStream.recv = (&self, max_len: int, timeout_ms: int) Result[string]: {
	if(self._fd < 0): func <- Result[string].fail(Error.new("recv on closed TcpStream"))
	let limit = if(max_len <= 0): 4096 else: max_len
	unsafe {
		let raw = alloc_raw[u8](limit)
		let n = sys_net_recv(self._fd, raw, limit, timeout_ms)
		if(n <= 0): {
			free_raw(raw)
			if(n == 0): func <- Result[string].fail(Error.coded("eof", "connection closed by peer"))
			if(n == -2): func <- Result[string].fail(Error.coded("timeout", "read timeout"))
			func <- Result[string].fail(Error.new("recv error"))
		}
		let out = str_from_raw(raw, n)
		free_raw(raw)
		Result[string].ok(own out)
	}
}

// Opens a TCP connection to `host:port` waiting at most `timeout_ms` milliseconds.
//
// # Errors
// Fails if host resolution fails or the connection cannot be established within `timeout_ms`.
bind TcpStream.connect = (host: string, port: int, timeout_ms: int) Result[TcpStream]: {
	let client_fd = unsafe sys_tcp_connect(host.c_str(), port, timeout_ms)
	if(client_fd >= 0): Result[TcpStream].ok(TcpStream[_fd: client_fd])
	else if(client_fd == -1): Result[TcpStream].fail(Error.new("failed to resolve host: $host"))
	else: Result[TcpStream].fail(Error.new("failed to connect to $host:$port"))
}

// Bound, listening TCP server socket.
//
// Implements [`Drop`] to close the listening socket when leaving scope. Pass port `0`
// to [`TcpListener.listen`] to bind an ephemeral OS-assigned port.
define TcpListener struct [
	_fd: int,
	_bound_port: int,
]

// Returns the underlying listening socket descriptor, or `-1` if closed.
bind TcpListener.raw_fd = (self) int: self._fd

// Reports whether the listener socket is still open.
bind TcpListener.is_open = (self) bool: self._fd >= 0

// Automatically closes the listener socket at scope exit if still open.
bind TcpListener.drop = (&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the listener socket.
bind TcpListener.close = (&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Returns the actual bound TCP port (useful when listening on ephemeral port `0`).
bind TcpListener.port = (self) int: self._bound_port

// Binds and listens for incoming TCP connections on `host:port`.
//
// Pass `0` for `port` to request an ephemeral port from the OS, retrievable via [`TcpListener.port`].
//
// # Errors
// Fails if the socket cannot be bound or placed into listen state.
bind TcpListener.listen = (host: string, port: int) Result[TcpListener]: {
	let sfd = unsafe sys_tcp_listen(host.c_str(), port, 128)
	if(sfd < 0):
		func <- Result[TcpListener].fail(Error.new("failed to bind TCP listener on $host:$port"))
	let actual_port = unsafe sys_tcp_port(sfd)
	Result[TcpListener].ok(TcpListener[
		_fd: sfd,
		_bound_port: if(actual_port > 0): actual_port else: port,
	])
}

// Accepts the next incoming TCP connection, waiting at most `timeout_ms` milliseconds.
//
// # Errors
// Fails with code `"timeout"` on accept timeout, or a generic [`Error`] on closed/failed listeners.
bind TcpListener.accept = (&self, timeout_ms: int) Result[TcpStream]: {
	if(self._fd < 0): func <- Result[TcpStream].fail(Error.new("accept on closed TcpListener"))
	let cfd = unsafe sys_tcp_accept(self._fd, timeout_ms)
	if(cfd >= 0): Result[TcpStream].ok(TcpStream[_fd: cfd])
	else if(cfd == -2): Result[TcpStream].fail(Error.coded("timeout", "accept timeout"))
	else: Result[TcpStream].fail(Error.new("failed to accept incoming TCP connection"))
}

// Pair of interconnected bidirectional [`TcpStream`] endpoints.
define SocketPair struct [
	// First socket endpoint.
	first: TcpStream,
	// Second connected socket endpoint.
	second: TcpStream,
]

// Creates a connected pair of bidirectional [`TcpStream`] sockets backed by an OS `socketpair`.
//
// Useful for in-memory protocol tests and local IPC pipelines.
//
// # Errors
// Fails if the kernel `socketpair` call fails.
bind socketpair = () Result[SocketPair]: {
	let pair = unsafe {
		let p = alloc_raw[int](2)
		let rc = sys_net_socketpair(p)
		if(rc != 0): {
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

// Connects to `host:port` using a default `10_000 ms` timeout (shorthand for [`TcpStream.connect`]).
bind dial_tcp = (host: string, port: int) Result[TcpStream]: TcpStream.connect(host, port, 10000)

// Binds and listens for TCP connections on `host:port` (shorthand for [`TcpListener.listen`]).
bind listen_tcp = (host: string, port: int) Result[TcpListener]: TcpListener.listen(host, port)

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

@test("net recv distinguishes clean EOF and timeout")
bind test_net_recv_eof_and_timeout = (): {
	let pair = socketpair().unwrap()
	let timeout_res = pair.first.recv(64, 10)
	assert(timeout_res.is_err(), "recv with no data times out")
	assert(timeout_res.unwrap_err().has_code("timeout"), "timeout has code \"timeout\"")

	let payload = "bin\u{0}ary\u{1}"
	assert(pair.second.send(payload).is_ok(), "send succeeds")
	let got = pair.first.recv(64, 1000)
	assert(got.is_ok(), "recv after send succeeds")
	assert(got.unwrap() == payload, "payload with NUL bytes round-trips")

	pair.second.close()
	let eof_res = pair.first.recv(64, 1000)
	assert(eof_res.is_err(), "recv after peer close fails")
	assert(eof_res.unwrap_err().has_code("eof"), "peer close has code \"eof\"")
}
