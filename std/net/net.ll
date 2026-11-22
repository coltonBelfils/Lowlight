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
	_fd: int
]

// Returns the underlying OS file descriptor, or `-1` if closed.
bind TcpStream.raw_fd = fn(self) int: self._fd

// Automatically closes the socket descriptor if still open when `self` leaves scope.
bind TcpStream.drop = fn(&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the TCP stream descriptor early, setting it to `-1`.
bind TcpStream.close = fn(&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Reports whether the stream holds an open socket descriptor (`raw_fd >= 0`).
bind TcpStream.is_open = fn(self) bool: self._fd >= 0

// Writes all bytes of `data` to the socket and returns the number of bytes sent.
//
// # Errors
// Fails if the stream is closed or the underlying OS send fails.
bind TcpStream.send = fn(&self, data: string) Result[int]: {
	if(self._fd < 0): func <- err(Error.new("send on closed TcpStream"))
	let res = unsafe sys_net_send_all(self._fd, data.c_str(), data.len())
	if(res == 0): value(data.len()) else: err(Error.new("failed to send data over TCP socket"))
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
bind TcpStream.recv = fn(&self, max_len: int, timeout_ms: int) Result[string]: {
	if(self._fd < 0): func <- err(Error.new("recv on closed TcpStream"))
	let limit = if(max_len <= 0): 4096 else: max_len
	unsafe {
		let raw = alloc_raw[u8](limit)
		let n = sys_net_recv(self._fd, raw, limit, timeout_ms)
		if(n <= 0): {
			free_raw(raw)
			if(n == 0): func <- err(Error.coded("eof", "connection closed by peer"))
			if(n == -2): func <- err(Error.coded("timeout", "read timeout"))
			func <- err(Error.new("recv error"))
		}
		let out = str_from_raw(raw, n)
		free_raw(raw)
		value(own out)
	}
}

// Writes all of `data` to the socket and returns the number of bytes sent.
//
// # Errors
// Fails if the stream is closed or the underlying OS send fails.
bind TcpStream.send_bytes = fn(&self, data: [u8]) Result[int]: {
	if(self._fd < 0): func <- err(Error.new("send on closed TcpStream"))
	let buf = string.with_capacity(data.len())
	for(b in data): buf.push_byte(b)
	let res = unsafe sys_net_send_all(self._fd, buf.c_str(), buf.len())
	if(res == 0): value(data.len()) else: err(Error.new("failed to send data over TCP socket"))
}

// Reads up to `max_len` bytes (defaulting to `4096` when `max_len <= 0`) as raw bytes, waiting up
// to `timeout_ms` (negative waits indefinitely). Like [`TcpStream.recv`], one OS read may return
// fewer bytes than `max_len`.
//
// # Errors
// Fails with code `"eof"` when the peer has closed the connection, `"timeout"` when no data
// arrives in time, or a generic [`Error`] on closed or failed sockets.
bind TcpStream.recv_bytes = fn(&self, max_len: int, timeout_ms: int) Result[[u8]]: {
	if(self._fd < 0): func <- err(Error.new("recv on closed TcpStream"))
	let limit = if(max_len <= 0): 4096 else: max_len
	unsafe {
		let raw = alloc_raw[u8](limit)
		let n = sys_net_recv(self._fd, raw, limit, timeout_ms)
		if(n <= 0): {
			free_raw(raw)
			if(n == 0): func <- err(Error.coded("eof", "connection closed by peer"))
			if(n == -2): func <- err(Error.coded("timeout", "read timeout"))
			func <- err(Error.new("recv error"))
		}
		let out = for(i in 0..n) [u8]: ptr_read(ptr_add(raw, i))
		free_raw(raw)
		value(own out)
	}
}

// Which directions [`TcpStream.shutdown`] closes.
define Shutdown enum [
	// No more reads: further `recv` calls report end of stream.
	read,
	// No more writes: the peer sees end of stream after the data already sent.
	write,
	// Both directions.
	both,
]

// Shuts down one or both directions of the connection without closing the socket.
//
// Shutting down `write` is how a client tells a server it has finished sending (the server
// reads end of stream) while still reading the reply.
//
// # Errors
// Fails if the stream is closed or the OS rejects the call (for example, if not connected).
bind TcpStream.shutdown = fn(&self, how: Shutdown) Result[bool]: {
	if(self._fd < 0): func <- err(Error.new("shutdown on closed TcpStream"))
	let code = match(how):
		when read: 0
		when write: 1
		default both: 2
	let rc = unsafe sys_net_shutdown(self._fd, code)
	if(rc == 0): value(true) else: err(Error.new("failed to shut down TCP stream"))
}

// Returns the remote address as `"a.b.c.d:port"` or `"[v6]:port"`, or `none` if unknown.
bind TcpStream.peer_addr = fn(self) ?string: peer_addr_of(self._fd)

// Returns the local address as `"a.b.c.d:port"` or `"[v6]:port"`, or `none` if unknown.
bind TcpStream.local_addr = fn(self) ?string: local_addr_of(self._fd)

// Enables or disables `TCP_NODELAY`. With it on, small writes are sent immediately instead of
// being coalesced (Nagle's algorithm), which lowers latency for request/response protocols.
//
// # Errors
// Fails if the stream is closed or the socket does not support the option.
bind TcpStream.set_nodelay = fn(&self, on: bool) Result[bool]: {
	if(self._fd < 0): func <- err(Error.new("set_nodelay on closed TcpStream"))
	let rc = unsafe sys_tcp_set_nodelay(self._fd, if(on): 1 else: 0)
	if(rc == 0): value(true) else: err(Error.new("failed to set TCP_NODELAY"))
}

// Returns the remote address of connected socket descriptor `fd`, or `none` if unknown.
bind peer_addr_of = fn(fd: int) ?string: {
	let port = unsafe sys_tcp_peer_port(fd)
	if(port < 0): func <- none
	let ip = unsafe sys_tcp_peer_ipv4(fd)
	if(ip >= 0): func <- "${_format_ipv4(ip)}:$port"
	let groups = for(i in 0..8) [int]: unsafe sys_tcp_peer_ipv6_group(fd, i)
	if(groups[0] < 0): func <- none
	"[${format_ipv6(groups)}]:$port"
}

// Returns the local address of socket descriptor `fd`, or `none` if unknown.
bind local_addr_of = fn(fd: int) ?string: {
	let port = unsafe sys_tcp_port(fd)
	if(port < 0): func <- none
	let ip = unsafe sys_tcp_local_ipv4(fd)
	if(ip >= 0): func <- "${_format_ipv4(ip)}:$port"
	let groups = for(i in 0..8) [int]: unsafe sys_tcp_local_ipv6_group(fd, i)
	if(groups[0] < 0): func <- none
	"[${format_ipv6(groups)}]:$port"
}

bind _format_ipv4 = fn(ip: int) string: "${(ip >> 24) & 255}.${(ip >> 16) & 255}.${(ip >> 8) & 255}.${ip & 255}"

bind _hex16 = fn(n: int) string: {
	if(n == 0): func <- "0"
	let digits = "0123456789abcdef"
	let out = ""
	let started = false
	for(shift in [12, 8, 4, 0]): {
		let d = (n >> shift) & 15
		if(d == 0 and not started): next <-
		started = true
		out.push_byte(digits.byte_at(d))
	}
	out
}

// Formats eight 16-bit groups as an IPv6 address in RFC 5952 canonical form: lowercase hex,
// no leading zeros, the longest run of two or more zero groups shortened to `::`, and
// IPv4-mapped addresses written as `::ffff:a.b.c.d`.
bind format_ipv6 = fn(groups: [int]) string: {
	if(groups.len() != 8): func <- ""
	let mapped = groups[0] == 0 and groups[1] == 0 and groups[2] == 0 and groups[3] == 0 and groups[4] == 0 and groups[5] == 65535
	if(mapped): func <- "::ffff:${_format_ipv4(groups[6] << 16 | groups[7])}"
	let best_at = -1
	let best_len = 0
	let run_at = -1
	for(i in 0..8): {
		if(groups[i] == 0): {
			if(run_at < 0): run_at = i
			let run_len = i - run_at + 1
			if(run_len > best_len): {
				best_at = run_at
				best_len = run_len
			}
		} else: run_at = -1
	}
	if(best_len < 2): best_at = -1
	let out = ""
	let i = 0
	while(i < 8): {
		if(i == best_at): {
			out.push("::")
			i += best_len
			next <-
		}
		if(not out.is_empty() and not out.ends_with(":")): out.push(":")
		out.push(_hex16(groups[i]))
		i += 1
	}
	out
}


// Opens a TCP connection to `host:port` waiting at most `timeout_ms` milliseconds.
//
// # Errors
// Fails if host resolution fails or the connection cannot be established within `timeout_ms`.
bind TcpStream.connect = fn(host: string, port: int, timeout_ms: int) Result[TcpStream]: {
	let client_fd = unsafe sys_tcp_connect(host.c_str(), port, timeout_ms)
	if(client_fd >= 0): value(TcpStream[_fd: client_fd])
	else if(client_fd == -1): err(Error.new("failed to resolve host: $host"))
	else: err(Error.new("failed to connect to $host:$port"))
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
bind TcpListener.raw_fd = fn(self) int: self._fd

// Reports whether the listener socket is still open.
bind TcpListener.is_open = fn(self) bool: self._fd >= 0

// Automatically closes the listener socket at scope exit if still open.
bind TcpListener.drop = fn(&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the listener socket.
bind TcpListener.close = fn(&self):
	if(self._fd >= 0): {
		unsafe sys_net_close(self._fd)
		self._fd = -1
	}

// Returns the actual bound TCP port (useful when listening on ephemeral port `0`).
bind TcpListener.port = fn(self) int: self._bound_port

// Binds and listens for incoming TCP connections on `host:port`.
//
// Pass `0` for `port` to request an ephemeral port from the OS, retrievable via [`TcpListener.port`].
//
// # Errors
// Fails if the socket cannot be bound or placed into listen state.
bind TcpListener.listen = fn(host: string, port: int) Result[TcpListener]: {
	let sfd = unsafe sys_tcp_listen(host.c_str(), port, 128)
	if(sfd < 0): func <- err(Error.new("failed to bind TCP listener on $host:$port"))
	let actual_port = unsafe sys_tcp_port(sfd)
	value(TcpListener[
		_fd: sfd,
		_bound_port: if(actual_port > 0): actual_port else: port,
	])
}

// Accepts the next incoming TCP connection, waiting at most `timeout_ms` milliseconds.
//
// # Errors
// Fails with code `"timeout"` on accept timeout, or a generic [`Error`] on closed/failed listeners.
bind TcpListener.accept = fn(&self, timeout_ms: int) Result[TcpStream]: {
	if(self._fd < 0): func <- err(Error.new("accept on closed TcpListener"))
	let cfd = unsafe sys_tcp_accept(self._fd, timeout_ms)
	if(cfd >= 0): value(TcpStream[_fd: cfd])
	else if(cfd == -2): err(Error.coded("timeout", "accept timeout"))
	else: err(Error.new("failed to accept incoming TCP connection"))
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
bind socketpair = fn() Result[SocketPair]: {
	let pair = unsafe {
		let p = alloc_raw[int](2)
		let rc = sys_net_socketpair(p)
		if(rc != 0): {
			free_raw(p)
			func <- err(Error.new("failed to create socketpair"))
		}
		let f0 = ptr_read(p)
		let f1 = ptr_read(ptr_add(p, 1))
		free_raw(p)
		SocketPair[
			first: TcpStream[_fd: f0],
			second: TcpStream[_fd: f1],
		]
	}
	value(own pair)
}

// Connects to `host:port` using a default `10_000 ms` timeout (shorthand for [`TcpStream.connect`]).
bind dial_tcp = fn(host: string, port: int) Result[TcpStream]: TcpStream.connect(host, port, 10000)

// Binds and listens for TCP connections on `host:port` (shorthand for [`TcpListener.listen`]).
bind listen_tcp = fn(host: string, port: int) Result[TcpListener]: TcpListener.listen(host, port)

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("net bidirectional data transfer over socketpair")
bind test_net_socketpair_bidirectional = fn(): {
	let pair_res! = socketpair()
	assert(pair_res!.is_ok(), "socketpair succeeds")
	let pair = pair_res!.unwrap()

	assert(pair.first.is_open(), "pair.first is open")
	assert(pair.second.is_open(), "pair.second is open")

	let send1_res! = pair.first.send("ping from first")
	assert(send1_res!.is_ok(), "send from first succeeds")

	let recv1_res! = pair.second.recv(1024, 1000)
	assert(recv1_res!.is_ok(), "recv on second succeeds")
	assert(recv1_res!.unwrap() == "ping from first", "second received expected payload")

	let send2_res! = pair.second.send("pong from second")
	assert(send2_res!.is_ok(), "send from second succeeds")

	let recv2_res! = pair.first.recv(1024, 1000)
	assert(recv2_res!.is_ok(), "recv on first succeeds")
	assert(recv2_res!.unwrap() == "pong from second", "first received expected payload")

	pair.first.close()
	pair.second.close()
	assert(not pair.first.is_open(), "first is closed")
	assert(not pair.second.is_open(), "second is closed")
}

@test("net TcpListener ephemeral port binding and teardown")
bind test_net_listener_bind_and_port = fn(): {
	let l_res! = TcpListener.listen("127.0.0.1", 0)
	assert(l_res!.is_ok(), "listen on ephemeral port 0 succeeds")
	let l = l_res!.unwrap()

	let bound_p = l.port()
	assert(bound_p > 0, "ephemeral port is positive integer")

	l.close()
	assert(not l.is_open(), "listener fd invalidated after close")
}

@test("net closed TcpStream error handling")
bind test_net_closed_stream_errors = fn(): {
	let s = TcpStream[_fd: -1]
	assert(not s.is_open(), "stream with fd -1 is not open")

	let send_res! = s.send("test")
	assert(send_res!.is_err(), "send on closed stream returns error")

	let recv_res! = s.recv(512, 100)
	assert(recv_res!.is_err(), "recv on closed stream returns error")
}

@test("net recv distinguishes clean EOF and timeout")
bind test_net_recv_eof_and_timeout = fn(): {
	let pair = socketpair().unwrap()
	let timeout_res! = pair.first.recv(64, 10)
	assert(timeout_res!.is_err(), "recv with no data times out")
	assert(timeout_res!.unwrap_err().has_code("timeout"), "timeout has code \"timeout\"")

	let payload = "bin\u{0}ary\u{1}"
	assert(pair.second.send(payload).is_ok(), "send succeeds")
	let got! = pair.first.recv(64, 1000)
	assert(got!.is_ok(), "recv after send succeeds")
	assert(got!.unwrap() == payload, "payload with NUL bytes round-trips")

	pair.second.close()
	let eof_res! = pair.first.recv(64, 1000)
	assert(eof_res!.is_err(), "recv after peer close fails")
	assert(eof_res!.unwrap_err().has_code("eof"), "peer close has code \"eof\"")
}

@test("net send_bytes and recv_bytes carry raw bytes, including zeros")
bind test_net_bytes = fn(): {
	let pair = socketpair().unwrap()
	let data: [u8] = [u8(0), u8(255), u8(10), u8(0), u8(128)]
	assert(pair.first.send_bytes(data).unwrap() == 5, "sent five bytes")
	let got = pair.second.recv_bytes(16, 1000).unwrap()
	assert(got.len() == 5 and got[0] == u8(0) and got[1] == u8(255) and got[4] == u8(128), "bytes arrive unchanged")
}

@test("net shutdown(write) ends the peer's stream but keeps reading open")
bind test_net_shutdown = fn(): {
	let pair = socketpair().unwrap()
	let _ = pair.first.send("last words") ?? 0
	assert(pair.first.shutdown(Shutdown.write).is_ok(), "shutdown write")
	assert(pair.second.recv(64, 1000).unwrap() == "last words", "data before shutdown arrives")
	let eof! = pair.second.recv(64, 1000)
	assert(eof!.peek_err()?.has_code("eof") ?? false, "then end of stream")
	let _ = pair.second.send("reply") ?? 0
	assert(pair.first.recv(64, 1000).unwrap() == "reply", "the shut-down side can still read")
	let closed = TcpStream[_fd: -1]
	assert(closed.shutdown(Shutdown.both).is_err(), "closed streams report an error")
}

@test("net local_addr, peer_addr, and set_nodelay on a loopback connection")
bind test_net_addresses = fn(): {
	let listener! = listen_tcp("127.0.0.1", 0)
	if(listener!.is_err()): func <-     // no loopback networking available here
	let listener = listener!.unwrap()
	let client = dial_tcp("127.0.0.1", listener.port()).unwrap()
	let server_side = listener.accept(2000).unwrap()
	let client_local = client.local_addr() ?? ""
	assert(client_local.starts_with("127.0.0.1:"), "client local address")
	assert(server_side.peer_addr() == client_local, "the server sees the client's address")
	assert(client.peer_addr() == "127.0.0.1:${listener.port()}", "the client's peer is the listener port")
	assert(client.set_nodelay(true).is_ok() and client.set_nodelay(false).is_ok(), "TCP_NODELAY toggles")

	let pair = socketpair().unwrap()
	assert(pair.first.peer_addr() == none, "Unix sockets have no IP address")
}

@test("net format_ipv6 writes RFC 5952 canonical text")
bind test_net_format_ipv6 = fn(): {
	assert(format_ipv6([0, 0, 0, 0, 0, 0, 0, 1]) == "::1", "loopback")
	assert(format_ipv6([0x2001, 0xdb8, 0, 0, 0, 0, 0, 1]) == "2001:db8::1", "longest zero run")
	assert(format_ipv6([0, 0, 0, 0, 0, 0xffff, 0x7f00, 1]) == "::ffff:127.0.0.1", "IPv4-mapped")
}
