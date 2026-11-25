// std/net — TCP sockets, listeners, and bidirectional socket pairs.
//
// Provides [`TcpStream`], [`TcpListener`], and [`SocketPair`] with deterministic
// [`Drop`] cleanup for socket descriptors and timeout-aware I/O. Failures carry the OS's
// description; the ones callers commonly handle on their own (a refused connection, a timeout,
// the peer closing the connection) are separate members of each result.

import "std/os"

comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
	extern socket as _net_socket: fn(domain: i32, stype: i32, proto: i32) i32
	extern setsockopt as _net_setsockopt: fn(fd: i32, level: i32, optname: i32, optval: ptr[u8], optlen: u32) i32
	extern getsockopt as _net_getsockopt: fn(fd: i32, level: i32, optname: i32, optval: ptr[u8], optlen: ptr[u32]) i32
	extern bind as _net_bind: fn(fd: i32, addr: ptr[u8], addrlen: u32) i32
	extern listen as _net_listen: fn(fd: i32, backlog: i32) i32
	extern accept as _net_accept: fn(fd: i32, addr: ptr[u8], addrlen: ptr[u32]) i32
	extern connect as _net_connect: fn(fd: i32, addr: ptr[u8], addrlen: u32) i32
	extern send as _net_send: fn(fd: i32, buf: ptr[u8], len: int, flags: i32) int
	extern recv as _net_recv: fn(fd: i32, buf: ptr[u8], len: int, flags: i32) int
	extern shutdown as _net_shutdown: fn(fd: i32, how: i32) i32
	extern close as _net_close: fn(fd: i32) i32
	extern getsockname as _net_getsockname: fn(fd: i32, addr: ptr[u8], addrlen: ptr[u32]) i32
	extern getpeername as _net_getpeername: fn(fd: i32, addr: ptr[u8], addrlen: ptr[u32]) i32
	extern poll as _net_poll: fn(fds: ptr[u8], nfds: u32, timeout_ms: i32) i32
	extern socketpair as _net_socketpair: fn(domain: i32, stype: i32, proto: i32, sv: ptr[i32]) i32
	extern getaddrinfo as _net_getaddrinfo: fn(node: ptr[u8], service: ptr[u8], hints: ptr[u8], res: ptr[ptr[u8]]) i32
	extern freeaddrinfo as _net_freeaddrinfo: fn(res: ptr[u8])
} else comptime if(comp.TARGET.os == comp.TargetOs.linux): {
	extern getaddrinfo as _net_getaddrinfo: fn(node: ptr[u8], service: ptr[u8], hints: ptr[u8], res: ptr[ptr[u8]]) i32
	extern freeaddrinfo as _net_freeaddrinfo: fn(res: ptr[u8])
}

bind _net_syscall_res = fn(rc: int) int: {
	if(rc < 0): {
		os.set_last_errno(-rc)
		func <- -1
	}
	rc
}

bind _darwin_set_nosigpipe = fn(fd: int) bool: {
	comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let opt = alloc_raw[i32](1)
			ptr_write(opt, i32(1))
			let rc = _net_setsockopt(i32(fd), i32(65535), i32(4130), ptr_cast[u8](opt), u32(4))
			free_raw(opt)
			rc == i32(0)
		}
	} else: {
		let _ = fd
		true
	}
}

bind _net_get_flags = fn(fd: int) int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_fcntl = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 25 else: 72
		_net_syscall_res(unsafe syscall(sys_fcntl, fd, 3, 0, 0, 0, 0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		_net_syscall_res(unsafe syscall(92, fd, 3, 0, 0, 0, 0))
	} else: {
		let _ = fd
		-1
	}
}

bind _net_set_flags = fn(fd: int, flags: int) int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_fcntl = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 25 else: 72
		_net_syscall_res(unsafe syscall(sys_fcntl, fd, 4, flags, 0, 0, 0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		_net_syscall_res(unsafe syscall(92, fd, 4, flags, 0, 0, 0))
	} else: {
		let _ = fd + flags
		-1
	}
}

bind _net_get_so_error = fn(fd: int) int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let opt = alloc_raw[i32](1)
			ptr_write(opt, i32(0))
			let len_slot = alloc_raw[u32](1)
			ptr_write(len_slot, u32(4))
			let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
				let sys_getsockopt = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 209 else: 55
				_net_syscall_res(
					syscall(sys_getsockopt, fd, 1, 4, int(ptr_addr(opt)), int(ptr_addr(len_slot)), 0),
				)
			} else: int(_net_getsockopt(i32(fd), i32(65535), i32(4103), ptr_cast[u8](opt), len_slot))
			let val = int(ptr_read(opt))
			free_raw(len_slot)
			free_raw(opt)
			if(rc == 0): val
			else: {
				let e = os.last_errno()
				if(e != 0): e else: 22
			}
		}
	} else: {
		let _ = fd
		22
	}
}

bind _net_poll_event = fn(fd: int, events: int, timeout_ms: int) int: {
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_ppoll = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 73 else: 271
		unsafe {
			let pfd = alloc_raw[u8](8)
			fill_bytes(pfd, u8(0), 8)
			ptr_write(ptr_cast[i32](pfd), i32(fd))
			ptr_write(ptr_add(pfd, 4), u8(events))
			let ts = alloc_raw[int](2)
			let ts_ptr = if(timeout_ms < 0): 0 else: {
				ptr_write(ts, timeout_ms / 1000)
				ptr_write(ptr_add(ts, 1), (timeout_ms % 1000) * 1000000)
				int(ptr_addr(ts))
			}
			let rc = syscall(sys_ppoll, int(ptr_addr(pfd)), 1, ts_ptr, 0, 0, 0)
			free_raw(ts)
			free_raw(pfd)
			if(rc < 0): {
				os.set_last_errno(-rc)
				func <- -1
			}
			rc
		}
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let pfd = alloc_raw[u8](8)
			fill_bytes(pfd, u8(0), 8)
			ptr_write(ptr_cast[i32](pfd), i32(fd))
			ptr_write(ptr_add(pfd, 4), u8(events))
			let t = if(timeout_ms < 0): i32(-1) else: i32(timeout_ms)
			let rc = int(_net_poll(pfd, u32(1), t))
			free_raw(pfd)
			rc
		}
	} else: {
		let _ = fd + events + timeout_ms
		-1
	}
}

bind _net_poll_read = fn(fd: int, timeout_ms: int) int: {
	if(timeout_ms < 0): func <- 1
	_net_poll_event(fd, 1, timeout_ms)
}

bind _sockaddr_is_inet = fn(sa: ptr[u8]) bool: {
	unsafe {
		let b0 = int(ptr_read(sa))
		let b1 = int(ptr_read(ptr_add(sa, 1)))
		comptime if(comp.TARGET.os == comp.TargetOs.darwin): (b1 == 2 or b1 == 30)
		else: (b0 == 2 and b1 == 0) or (b0 == 10 and b1 == 0)
	}
}

// Closes raw socket descriptor `fd` when non-negative.
bind raw_close = fn(fd: int): {
	if(fd < 0): func <-
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_close = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 57 else: 3
		let _ = unsafe syscall(sys_close, fd, 0, 0, 0, 0, 0)
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		let _ = unsafe _net_close(i32(fd))
	}
}

// Sends all `len` bytes from `buf` over socket `fd`, returning `0` on success or `-1` on failure.
bind raw_send_all = fn(fd: int, buf: ptr[u8], length: int) int: {
	if(fd < 0): func <- -1
	if(length <= 0): func <- 0
	if(unsafe is_null(buf)): func <- -1
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_sendto = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 206 else: 44
		let sent = 0
		for(sent < length): {
			let n = unsafe syscall(
				sys_sendto,
				fd,
				int(ptr_addr(ptr_add(buf, sent))),
				length - sent,
				16384,
				0,
				0,
			)
			if(n == -4): next <-
			if(n <= 0): {
				if(n < 0): os.set_last_errno(-n)
				func <- -1
			}
			sent += n
		}
		0
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		let flags = if(_darwin_set_nosigpipe(fd)): i32(0) else: i32(524288)
		let sent = 0
		for(sent < length): {
			let n = unsafe _net_send(i32(fd), ptr_add(buf, sent), length - sent, flags)
			if(n < 0 and os.last_errno() == 4): next <-
			if(n <= 0): func <- -1
			sent += n
		}
		0
	} else: -1
}

// Receives up to `max_len` bytes from `fd` with optional millisecond timeout (`timeout_ms < 0`
// waits indefinitely). Returns `> 0` bytes read, `0` on EOF, `-2` on timeout, or `-1` on error.
bind raw_recv = fn(fd: int, buf: ptr[u8], max_len: int, timeout_ms: int) int: {
	if(fd < 0 or max_len <= 0 or unsafe is_null(buf)): func <- -1
	if(timeout_ms >= 0): {
		let pr = _net_poll_read(fd, timeout_ms)
		if(pr == 0): func <- -2
		if(pr < 0): func <- -1
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_recvfrom = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 207 else: 45
		for(true): {
			let n = unsafe syscall(sys_recvfrom, fd, int(ptr_addr(buf)), max_len, 0, 0, 0)
			if(n == -4): next <-
			if(n < 0): {
				os.set_last_errno(-n)
				func <- -1
			}
			func <- n
		}
		-1
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		for(true): {
			let n = unsafe _net_recv(i32(fd), buf, max_len, i32(0))
			if(n < 0 and os.last_errno() == 4): next <-
			if(n < 0): func <- -1
			func <- n
		}
		-1
	} else: -1
}

// Queries bound local port of TCP socket `fd`, or `-1` on error.
bind raw_tcp_port = fn(fd: int) int: {
	if(fd < 0): func <- -1
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let sa = alloc_raw[u8](128)
			fill_bytes(sa, u8(0), 128)
			let len_slot = alloc_raw[u32](1)
			ptr_write(len_slot, u32(128))
			let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
				let sys_getsockname = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 204 else: 51
				_net_syscall_res(
					syscall(sys_getsockname, fd, int(ptr_addr(sa)), int(ptr_addr(len_slot)), 0, 0, 0),
				)
			} else: int(_net_getsockname(i32(fd), sa, len_slot))
			let port = if(rc == 0 and _sockaddr_is_inet(sa)): {
				(int(ptr_read(ptr_add(sa, 2))) << 8) | int(ptr_read(ptr_add(sa, 3)))
			} else: -1
			free_raw(len_slot)
			free_raw(sa)
			port
		}
	} else: -1
}

// Binds and listens on `host:port`, returning listener fd or `-1` on failure.
bind raw_tcp_listen = fn(host: string, port: int, backlog: int) int: {
	if(host.contains_byte(u8(0)) or port < 0 or port > 65535): {
		os.set_last_errno(22)
		func <- -1
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let port_str = "$port"
		let hints = unsafe alloc_raw[u8](48)
		unsafe fill_bytes(hints, u8(0), 48)
		let res_slot = unsafe alloc_raw[ptr[u8]](1)
		unsafe ptr_write(res_slot, null_ptr[u8]())
		let use_any = host.len() == 0 or host == "0.0.0.0" or host == "*"
		unsafe {
			ptr_write(ptr_cast[i32](hints), i32(1))
			ptr_write(ptr_cast[i32](ptr_add(hints, 4)), if(use_any): i32(2) else: i32(0))
			ptr_write(ptr_cast[i32](ptr_add(hints, 8)), i32(1))
		}
		let node_ptr = if(use_any): unsafe null_ptr[u8]() else: unsafe host.c_str()
		let gai_rc = unsafe _net_getaddrinfo(node_ptr, port_str.c_str(), hints, res_slot)
		let res0 = unsafe ptr_read(res_slot)
		unsafe free_raw(res_slot)
		unsafe free_raw(hints)
		if(gai_rc != i32(0) or unsafe is_null(res0)): {
			if(unsafe not is_null(res0)): unsafe _net_freeaddrinfo(res0)
			os.set_last_errno(22)
			func <- -1
		}
		let sfd = -1
		let last_err = 0
		let opt_slot = unsafe alloc_raw[i32](1)
		unsafe ptr_write(opt_slot, i32(1))
		let addr_off = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 32 else: 24
		let bl = if(backlog > 0): backlog else: 128
		let rp = res0
		unsafe {
			while(not is_null(rp)): {
				let af = int(ptr_read(ptr_cast[i32](ptr_add(rp, 4))))
				let stype = int(ptr_read(ptr_cast[i32](ptr_add(rp, 8))))
				let proto = int(ptr_read(ptr_cast[i32](ptr_add(rp, 12))))
				let alen = int(ptr_read(ptr_cast[u32](ptr_add(rp, 16))))
				let addr = ptr_read(ptr_cast[ptr[u8]](ptr_add(rp, addr_off)))
				let next_rp = ptr_read(ptr_cast[ptr[u8]](ptr_add(rp, 40)))
				let fd = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
					let sys_sock = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 198 else: 41
					_net_syscall_res(syscall(sys_sock, af, stype, proto, 0, 0, 0))
				} else: int(_net_socket(i32(af), i32(stype), i32(proto)))
				if(fd < 0): {
					let e = os.last_errno()
					if(e != 0): last_err = e
				} else: {
					comptime if(comp.TARGET.os == comp.TargetOs.linux): {
						let sys_so = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 208 else: 54
						let sys_bind = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 200 else: 49
						let sys_listen = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 201 else: 50
						let _ = syscall(sys_so, fd, 1, 2, int(ptr_addr(opt_slot)), 4, 0)
						let brc = _net_syscall_res(
							syscall(sys_bind, fd, int(ptr_addr(addr)), alen, 0, 0, 0),
						)
						let lrc = if(brc == 0): _net_syscall_res(
							syscall(sys_listen, fd, bl, 0, 0, 0, 0),
						) else: -1
						if(lrc == 0): {
							sfd = fd
							while <-
						}
					} else: {
						let _ = _net_setsockopt(i32(fd), i32(65535), i32(4), ptr_cast[u8](opt_slot), u32(4))
						let _ = _net_setsockopt(i32(fd), i32(65535), i32(4130), ptr_cast[u8](opt_slot), u32(4))
						let brc = _net_bind(i32(fd), addr, u32(alen))
						let lrc = if(brc == i32(0)): _net_listen(i32(fd), i32(bl)) else: i32(-1)
						if(lrc == i32(0)): {
							sfd = fd
							while <-
						}
					}
					let saved = os.last_errno()
					if(saved != 0): last_err = saved
					raw_close(fd)
				}
				rp = next_rp
			}
			free_raw(opt_slot)
			_net_freeaddrinfo(res0)
		}
		if(sfd < 0): os.set_last_errno(if(last_err != 0): last_err else: 22)
		sfd
	} else: {
		let _ = host.len() + backlog
		-1
	}
}

// Accepts next connection on `listener_fd` within `timeout_ms`. Returns client fd, `-2` on
// timeout, or `-1` on error.
bind raw_tcp_accept = fn(listener_fd: int, timeout_ms: int) int: {
	if(listener_fd < 0): func <- -1
	if(timeout_ms >= 0): {
		let pr = _net_poll_read(listener_fd, timeout_ms)
		if(pr == 0): func <- -2
		if(pr < 0): func <- -1
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_accept = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 202 else: 43
		for(true): {
			let cfd = unsafe syscall(sys_accept, listener_fd, 0, 0, 0, 0, 0)
			if(cfd == -4): next <-
			if(cfd < 0): {
				os.set_last_errno(-cfd)
				func <- -1
			}
			func <- cfd
		}
		-1
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		for(true): {
			let cfd = int(unsafe _net_accept(i32(listener_fd), null_ptr[u8](), null_ptr[u32]()))
			if(cfd < 0): {
				if(os.last_errno() == 4): next <-
				func <- -1
			}
			let _ = _darwin_set_nosigpipe(cfd)
			func <- cfd
		}
		-1
	} else: -1
}

bind _raw_tcp_connect = fn(host: string, port: int, timeout_ms: int) int: {
	if(host.len() == 0 or host.contains_byte(u8(0)) or port <= 0 or port > 65535): {
		os.set_last_errno(22)
		func <- -2
	}
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let port_str = "$port"
		let hints = unsafe alloc_raw[u8](48)
		unsafe fill_bytes(hints, u8(0), 48)
		let res_slot = unsafe alloc_raw[ptr[u8]](1)
		unsafe ptr_write(res_slot, null_ptr[u8]())
		unsafe ptr_write(ptr_cast[i32](ptr_add(hints, 8)), i32(1))
		let gai_rc = unsafe _net_getaddrinfo(host.c_str(), port_str.c_str(), hints, res_slot)
		let res0 = unsafe ptr_read(res_slot)
		unsafe free_raw(res_slot)
		unsafe free_raw(hints)
		if(gai_rc != i32(0) or unsafe is_null(res0)): {
			if(unsafe not is_null(res0)): unsafe _net_freeaddrinfo(res0)
			os.set_last_errno(2)
			func <- -1
		}

		let cfd = -2
		let last_err = 0
		let addr_off = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 32 else: 24
		let o_nonblock = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 4 else: 2048
		let einprog = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 36 else: 115
		let etimedout = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 60 else: 110
		let rp = res0
		unsafe {
			while(not is_null(rp)): {
				let af = int(ptr_read(ptr_cast[i32](ptr_add(rp, 4))))
				let stype = int(ptr_read(ptr_cast[i32](ptr_add(rp, 8))))
				let proto = int(ptr_read(ptr_cast[i32](ptr_add(rp, 12))))
				let alen = int(ptr_read(ptr_cast[u32](ptr_add(rp, 16))))
				let addr = ptr_read(ptr_cast[ptr[u8]](ptr_add(rp, addr_off)))
				let next_rp = ptr_read(ptr_cast[ptr[u8]](ptr_add(rp, 40)))
				let fd = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
					let sys_sock = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 198 else: 41
					_net_syscall_res(syscall(sys_sock, af, stype, proto, 0, 0, 0))
				} else: int(_net_socket(i32(af), i32(stype), i32(proto)))
				if(fd < 0): {
					let e = os.last_errno()
					if(e != 0): last_err = e
				} else: {
					let _ = _darwin_set_nosigpipe(fd)
					if(timeout_ms < 0): {
						let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
							let sys_connect = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 203 else: 42
							_net_syscall_res(
								syscall(sys_connect, fd, int(ptr_addr(addr)), alen, 0, 0, 0),
							)
						} else: int(_net_connect(i32(fd), addr, u32(alen)))
						if(rc == 0): {
							cfd = fd
							while <-
						}
						let cerr = os.last_errno()
						if(cerr == 4): {
							let pr = _net_poll_event(fd, 4, -1)
							if(pr > 0): {
								let serr = _net_get_so_error(fd)
								if(serr == 0): {
									cfd = fd
									while <-
								}
								last_err = serr
							} else: {
								let pe = os.last_errno()
								if(pe != 0): last_err = pe
							}
						} else if(cerr != 0): {
							last_err = cerr
						}
						raw_close(fd)
					} else: {
						let orig_flags = _net_get_flags(fd)
						if(orig_flags < 0 or _net_set_flags(fd, orig_flags | o_nonblock) < 0): {
							let fe = os.last_errno()
							if(fe != 0): last_err = fe
							raw_close(fd)
						} else: {
							let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
								let sys_connect = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 203 else: 42
								_net_syscall_res(
									syscall(sys_connect, fd, int(ptr_addr(addr)), alen, 0, 0, 0),
								)
							} else: int(_net_connect(i32(fd), addr, u32(alen)))
							if(rc == 0): {
								if(_net_set_flags(fd, orig_flags) == 0): {
									cfd = fd
									while <-
								}
								let fe = os.last_errno()
								if(fe != 0): last_err = fe
								raw_close(fd)
							} else: {
								let cerr = os.last_errno()
								if(cerr == einprog or cerr == 4): {
									let pr = _net_poll_event(fd, 4, timeout_ms)
									if(pr == 0): {
										last_err = etimedout
										raw_close(fd)
									} else if(pr < 0): {
										let pe = os.last_errno()
										last_err = if(pe != 0): pe else: 22
										raw_close(fd)
									} else: {
										let serr = _net_get_so_error(fd)
										if(serr == 0): {
											if(_net_set_flags(fd, orig_flags) == 0): {
												cfd = fd
												while <-
											}
											let fe = os.last_errno()
											if(fe != 0): last_err = fe
										} else: {
											last_err = serr
										}
										raw_close(fd)
									}
								} else: {
									if(cerr != 0): last_err = cerr
									raw_close(fd)
								}
							}
						}
					}
				}
				rp = next_rp
			}
			_net_freeaddrinfo(res0)
		}
		if(cfd < 0): os.set_last_errno(if(last_err != 0): last_err else: 22)
		cfd
	} else: {
		let _ = timeout_ms
		-2
	}
}

// The result of [`TcpStream.connect`] and [`dial_tcp`].
@must_use
define ConnectResult result [
	// The connected stream.
	value: TcpStream,
	// The host name couldn't be resolved.
	not_found: Error,
	// The host refused the connection: nothing is listening on that port.
	refused: Error,
	// The connection didn't complete within the timeout.
	timeout: Error,
	// Any other failure (an unreachable network, ...), with the OS's description.
	err: Error,
]

// The result of [`TcpStream.recv`] and [`TcpStream.recv_bytes`].
@must_use
define RecvResult[T] result [
	// The data received: at least one byte.
	value: T,
	// The peer closed the connection (or shut down its writing side); no more data will come.
	eof: Error,
	// No data arrived within the timeout.
	timeout: Error,
	// The stream is closed or the read failed, with the OS's description.
	err: Error,
]

// The result of [`TcpListener.accept`].
@must_use
define AcceptResult result [
	// The accepted connection.
	value: TcpStream,
	// No connection arrived within the timeout.
	timeout: Error,
	// The listener is closed or the accept failed, with the OS's description.
	err: Error,
]

// The result of [`TcpListener.listen`] and [`listen_tcp`].
@must_use
define ListenResult result [
	// The listening socket.
	value: TcpListener,
	// Another socket is already listening on that address and port.
	in_use: Error,
	// Any other failure (an address that can't be resolved, permission denied for a low port,
	// ...), with the OS's description.
	err: Error,
]

// Connected bidirectional TCP socket stream.
//
// Implements [`Drop`] to close the underlying socket descriptor automatically at scope exit.
//
// # Example
// ```lowlight
// let conn = dial_tcp("127.0.0.1", 8080): default panic
// conn.send("GET / HTTP/1.0\r\n\r\n"): default panic
// ```
define TcpStream struct [
	_fd: int
]

// Returns the underlying OS file descriptor, or `-1` if closed.
bind TcpStream.raw_fd = fn(self) int: self._fd

// Automatically closes the socket descriptor if still open when `self` leaves scope.
bind TcpStream.drop = fn(&self):
	if(self._fd >= 0): {
		raw_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the TCP stream descriptor early, setting it to `-1`.
bind TcpStream.close = fn(&self):
	if(self._fd >= 0): {
		raw_close(self._fd)
		self._fd = -1
	}

// Reports whether the stream holds an open socket descriptor (`raw_fd >= 0`).
bind TcpStream.is_open = fn(self) bool: self._fd >= 0

// Writes all bytes of `data` to the socket and returns the number of bytes sent.
//
// # Errors
// Fails if the stream is closed or the send fails (for example, the peer reset the connection),
// with the OS's description.
bind TcpStream.send = fn(&self, data: string) Result[int]: {
	if(self._fd < 0): func <- err(Error.new("send on closed TcpStream"))
	let res = unsafe raw_send_all(self._fd, data.c_str(), data.len())
	if(res == 0): value(data.len()) else: err(os.error_from_errno(os.last_errno(), "failed to send"))
}

// Reads up to `max_len` bytes (defaulting to `4096` when `max_len <= 0`) waiting up to `timeout_ms`.
//
// Returns the bytes received by a single OS read, which may be fewer than `max_len`.
//
// - `max_len`: Maximum byte count to read in one call.
// - `timeout_ms`: Timeout in milliseconds (negative waits indefinitely).
//
// # Errors
// [`RecvResult.eof`] when the peer has closed the connection, [`RecvResult.timeout`] when no
// data arrives within `timeout_ms`, and [`RecvResult.err`] on a closed stream or a failed read.
bind TcpStream.recv = fn(&self, max_len: int, timeout_ms: int) RecvResult[string]: {
	if(self._fd < 0): func <- err(Error.new("recv on closed TcpStream"))
	let limit = if(max_len <= 0): 4096 else: max_len
	unsafe {
		let raw = alloc_raw[u8](limit)
		let n = raw_recv(self._fd, raw, limit, timeout_ms)
		if(n <= 0): {
			let code = os.last_errno()
			free_raw(raw)
			if(n == 0): func <- eof(Error.new("connection closed by peer"))
			if(n == -2): func <- timeout(Error.new("no data within $timeout_ms ms"))
			func <- err(os.error_from_errno(code, "failed to receive"))
		}
		let out = str_from_raw(raw, n)
		free_raw(raw)
		value(own out)
	}
}

// Writes all of `data` to the socket and returns the number of bytes sent.
//
// # Errors
// Fails if the stream is closed or the send fails, with the OS's description.
bind TcpStream.send_bytes = fn(&self, data: [u8]) Result[int]: {
	if(self._fd < 0): func <- err(Error.new("send on closed TcpStream"))
	if(data.len() == 0): func <- value(0)
	let res = unsafe raw_send_all(self._fd, data.data, data.len())
	if(res == 0): value(data.len()) else: err(os.error_from_errno(os.last_errno(), "failed to send"))
}

// Reads up to `max_len` bytes (defaulting to `4096` when `max_len <= 0`) as raw bytes, waiting up
// to `timeout_ms` (negative waits indefinitely). Like [`TcpStream.recv`], one OS read may return
// fewer bytes than `max_len`.
//
// # Errors
// [`RecvResult.eof`] when the peer has closed the connection, [`RecvResult.timeout`] when no
// data arrives within `timeout_ms`, and [`RecvResult.err`] on a closed stream or a failed read.
bind TcpStream.recv_bytes = fn(&self, max_len: int, timeout_ms: int) RecvResult[[u8]]: {
	if(self._fd < 0): func <- err(Error.new("recv on closed TcpStream"))
	let limit = if(max_len <= 0): 4096 else: max_len
	unsafe {
		let out = List[u8].with_capacity(limit)
		let n = raw_recv(self._fd, out.data, limit, timeout_ms)
		if(n <= 0): {
			let code = os.last_errno()
			if(n == 0): func <- eof(Error.new("connection closed by peer"))
			if(n == -2): func <- timeout(Error.new("no data within $timeout_ms ms"))
			func <- err(os.error_from_errno(code, "failed to receive"))
		}
		out.len = n
		if(limit > 65536 and n * 4 < limit): {
			let shrunk = out.copy()
			func <- value(own shrunk)
		}
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
	let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
		let sys_shutdown = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 210 else: 48
		_net_syscall_res(unsafe syscall(sys_shutdown, self._fd, code, 0, 0, 0, 0))
	} else comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		int(unsafe _net_shutdown(i32(self._fd), i32(code)))
	} else: -1
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
	let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let opt = alloc_raw[i32](1)
			ptr_write(opt, if(on): i32(1) else: i32(0))
			let r = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
				let sys_so = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 208 else: 54
				_net_syscall_res(syscall(sys_so, self._fd, 6, 1, int(ptr_addr(opt)), 4, 0))
			} else: int(_net_setsockopt(i32(self._fd), i32(6), i32(1), ptr_cast[u8](opt), u32(4)))
			free_raw(opt)
			r
		}
	} else: -1
	if(rc == 0): value(true) else: err(Error.new("failed to set TCP_NODELAY"))
}

bind _format_sock_addr = fn(fd: int, peer: bool) ?string: {
	if(fd < 0): func <- none
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let sa = alloc_raw[u8](128)
			fill_bytes(sa, u8(0), 128)
			let len_slot = alloc_raw[u32](1)
			ptr_write(len_slot, u32(128))
			let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
				let sys_num = if(peer): comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 205 else: 52
				else: comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 204 else: 51
				_net_syscall_res(syscall(sys_num, fd, int(ptr_addr(sa)), int(ptr_addr(len_slot)), 0, 0, 0))
			} else: {
				if(peer): int(_net_getpeername(i32(fd), sa, len_slot))
				else: int(_net_getsockname(i32(fd), sa, len_slot))
			}
			if(rc != 0): {
				free_raw(len_slot)
				free_raw(sa)
				func <- none
			}
			let b0 = int(ptr_read(sa))
			let b1 = int(ptr_read(ptr_add(sa, 1)))
			let is_v4 = (b0 == 16 and b1 == 2) or (b0 == 2 and b1 == 0)
			let is_v6 = (b0 == 28 and b1 == 30) or (b0 == 10 and b1 == 0)
			let port = (int(ptr_read(ptr_add(sa, 2))) << 8) | int(ptr_read(ptr_add(sa, 3)))
			if(is_v4): {
				let ip = (int(ptr_read(ptr_add(sa, 4))) << 24) |
					(int(ptr_read(ptr_add(sa, 5))) << 16) |
					(int(ptr_read(ptr_add(sa, 6))) << 8) |
					int(ptr_read(ptr_add(sa, 7)))
				free_raw(len_slot)
				free_raw(sa)
				func <- "${_format_ipv4(ip)}:$port"
			}
			if(is_v6): {
				let groups: [int] = []
				for(i in 0..8): {
					let g = (int(ptr_read(ptr_add(sa, 8 + i * 2))) << 8) |
						int(ptr_read(ptr_add(sa, 9 + i * 2)))
					groups.push(g)
				}
				free_raw(len_slot)
				free_raw(sa)
				func <- "[${format_ipv6(groups)}]:$port"
			}
			free_raw(len_slot)
			free_raw(sa)
			none
		}
	} else: {
		let _ = peer
		none
	}
}

// Returns the remote address of connected socket descriptor `fd`, or `none` if unknown.
bind peer_addr_of = fn(fd: int) ?string: _format_sock_addr(fd, true)

// Returns the local address of socket descriptor `fd`, or `none` if unknown.
bind local_addr_of = fn(fd: int) ?string: _format_sock_addr(fd, false)

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
// ```lowlight
// let conn = net.dial_tcp("localhost", 8080):
// 	when refused(_): func <- println("the server isn't running")
// 	default panic
// ```
//
// # Errors
// [`ConnectResult.not_found`] if `host` can't be resolved, [`ConnectResult.refused`] if nothing
// is listening, [`ConnectResult.timeout`] if the connection doesn't complete in time, and
// [`ConnectResult.err`] for anything else, with the OS's description.
bind TcpStream.connect = fn(host: string, port: int, timeout_ms: int) ConnectResult: {
	let client_fd = _raw_tcp_connect(host, port, timeout_ms)
	if(client_fd >= 0): func <- value(TcpStream[_fd: client_fd])
	if(client_fd == -1): func <- not_found(Error.coded("not_found", "failed to resolve host \"$host\""))
	let code = os.last_errno()
	let e = os.error_from_errno(code, "failed to connect to $host:$port")
	match(os.errno_name(code)):
		when "ECONNREFUSED": refused(e)
		when "ETIMEDOUT", "EINPROGRESS", "EAGAIN": timeout(e)
		default: err(e)
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
		raw_close(self._fd)
		self._fd = -1
	}

// Explicitly closes the listener socket.
bind TcpListener.close = fn(&self):
	if(self._fd >= 0): {
		raw_close(self._fd)
		self._fd = -1
	}

// Returns the actual bound TCP port (useful when listening on ephemeral port `0`).
bind TcpListener.port = fn(self) int: self._bound_port

// Binds and listens for incoming TCP connections on `host:port`.
//
// Pass `0` for `port` to request an ephemeral port from the OS, retrievable via [`TcpListener.port`].
//
// # Errors
// [`ListenResult.in_use`] if something else already listens on `host:port`;
// [`ListenResult.err`] if the socket can't be bound or put into the listening state for any
// other reason, with the OS's description.
bind TcpListener.listen = fn(host: string, port: int) ListenResult: {
	let sfd = raw_tcp_listen(host, port, 128)
	if(sfd < 0): {
		let code = os.last_errno()
		let e = os.error_from_errno(code, "failed to listen on $host:$port")
		func <- if(os.errno_name(code) == "EADDRINUSE"): in_use(e) else: err(e)
	}
	let actual_port = raw_tcp_port(sfd)
	value(TcpListener[
		_fd: sfd,
		_bound_port: if(actual_port > 0): actual_port else: port,
	])
}

// Accepts the next incoming TCP connection, waiting at most `timeout_ms` milliseconds.
//
// # Errors
// [`AcceptResult.timeout`] if no connection arrives within `timeout_ms`; [`AcceptResult.err`]
// on a closed listener or a failed accept, with the OS's description.
bind TcpListener.accept = fn(&self, timeout_ms: int) AcceptResult: {
	if(self._fd < 0): func <- err(Error.new("accept on closed TcpListener"))
	let cfd = raw_tcp_accept(self._fd, timeout_ms)
	if(cfd >= 0): value(TcpStream[_fd: cfd])
	else if(cfd == -2): timeout(Error.new("no connection within $timeout_ms ms"))
	else: err(os.error_from_errno(os.last_errno(), "failed to accept a connection"))
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
	comptime if(comp.TARGET.os == comp.TargetOs.linux or comp.TARGET.os == comp.TargetOs.darwin): {
		let pair = unsafe {
			let sv = alloc_raw[i32](2)
			let rc = comptime if(comp.TARGET.os == comp.TargetOs.linux): {
				let sys_sp = comptime if(comp.TARGET.arch == comp.TargetArch.arm64): 199 else: 53
				_net_syscall_res(syscall(sys_sp, 1, 1, 0, int(ptr_addr(sv)), 0, 0))
			} else: int(_net_socketpair(i32(1), i32(1), i32(0), sv))
			if(rc != 0): {
				let code = os.last_errno()
				free_raw(sv)
				func <- err(os.error_from_errno(code, "failed to create a socket pair"))
			}
			let f0 = int(ptr_read(sv))
			let f1 = int(ptr_read(ptr_add(sv, 1)))
			free_raw(sv)
			let _ = _darwin_set_nosigpipe(f0)
			let _ = _darwin_set_nosigpipe(f1)
			SocketPair[
				first: TcpStream[_fd: f0],
				second: TcpStream[_fd: f1],
			]
		}
		value(own pair)
	} else: err(Error.new("socketpair is unsupported on this target"))
}

// Connects to `host:port` using a default `10_000 ms` timeout (shorthand for [`TcpStream.connect`]).
bind dial_tcp = fn(host: string, port: int) ConnectResult: TcpStream.connect(host, port, 10000)

// Binds and listens for TCP connections on `host:port` (shorthand for [`TcpListener.listen`]).
bind listen_tcp = fn(host: string, port: int) ListenResult: TcpListener.listen(host, port)

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("net bidirectional data transfer over socketpair")
bind test_net_socketpair_bidirectional = fn(): {
	let pair = socketpair(): default panic

	assert(pair.first.is_open(), "pair.first is open")
	assert(pair.second.is_open(), "pair.second is open")

	pair.first.send("ping from first"): default panic
	let got1 = pair.second.recv(1024, 1000): default panic
	assert(got1 == "ping from first", "second received expected payload")

	pair.second.send("pong from second"): default panic
	let got2 = pair.first.recv(1024, 1000): default panic
	assert(got2 == "pong from second", "first received expected payload")

	pair.first.close()
	pair.second.close()
	assert(not pair.first.is_open(), "first is closed")
	assert(not pair.second.is_open(), "second is closed")
}

@test("net TcpListener ephemeral port binding and teardown")
bind test_net_listener_bind_and_port = fn(): {
	let l = TcpListener.listen("127.0.0.1", 0): default panic

	let bound_p = l.port()
	assert(bound_p > 0, "ephemeral port is positive integer")

	let seen = ""
	TcpListener.listen("127.0.0.1", bound_p):
		when in_use(e): seen = if(e.has_code("EADDRINUSE")): "in_use" else: "no code"
		default err(_): seen = "err"
	assert(seen == "in_use", "listening on a port that's taken is `in_use`")

	let port = l.port()
	l.close()
	assert(not l.is_open(), "listener fd invalidated after close")
	TcpStream.connect("no-such-host.invalid", 80, 1000):
		when not_found(_): seen = "not_found"
		default: seen = "other"
	assert(seen == "not_found", "an unresolvable host is `not_found`")
	let loopback_blocked = false
	TcpStream.connect("127.0.0.1", port, 1000):
		when refused(_): seen = "refused"
		when err(e): {
			if(e.has_code("EPERM")): loopback_blocked = true
			seen = "err"
		}
		default: seen = "other"
	if(not loopback_blocked): assert(seen == "refused", "connecting to a closed port is `refused`")
}

@test("net closed TcpStream error handling")
bind test_net_closed_stream_errors = fn(): {
	let s = TcpStream[_fd: -1]
	assert(not s.is_open(), "stream with fd -1 is not open")
	assert(s.send("test").is_err(), "send on closed stream returns error")
	let seen = ""
	s.recv(512, 100):
		when eof(_), timeout(_): seen = "other"
		default err(_): seen = "err"
	assert(seen == "err", "recv on closed stream is `err`")
}

@test("net recv distinguishes clean EOF and timeout")
bind test_net_recv_eof_and_timeout = fn(): {
	let pair = socketpair(): default panic
	let seen = ""
	pair.first.recv(64, 10):
		when timeout(_): seen = "timeout"
		default: seen = "other"
	assert(seen == "timeout", "recv with no data is `timeout`")

	let payload = "bin\u{0}ary\u{1}"
	pair.second.send(payload): default panic
	let got = pair.first.recv(64, 1000): default panic
	assert(got == payload, "payload with NUL bytes round-trips")

	pair.second.close()
	pair.first.recv(64, 1000):
		when eof(_): seen = "eof"
		default: seen = "other"
	assert(seen == "eof", "recv after peer close is `eof`")
}

@test("net send_bytes and recv_bytes carry raw bytes, including zeros")
bind test_net_bytes = fn(): {
	let pair = socketpair(): default panic
	let data: [u8] = [u8(0), u8(255), u8(10), u8(0), u8(128)]
	assert(pair.first.send_bytes(data).unwrap() == 5, "sent five bytes")
	let got = pair.second.recv_bytes(16, 1000): default panic
	assert(
		got.len() == 5 and got[0] == u8(0) and got[1] == u8(255) and got[4] == u8(128),
		"bytes arrive unchanged",
	)
}

@test("net shutdown(write) ends the peer's stream but keeps reading open")
bind test_net_shutdown = fn(): {
	let pair = socketpair(): default panic
	pair.first.send("last words"): default panic
	assert(pair.first.shutdown(Shutdown.write).is_ok(), "shutdown write")
	let before = pair.second.recv(64, 1000): default panic
	assert(before == "last words", "data before shutdown arrives")
	let seen = ""
	pair.second.recv(64, 1000):
		when eof(_): seen = "eof"
		default: seen = "other"
	assert(seen == "eof", "then end of stream")
	pair.second.send("reply"): default panic
	let reply = pair.first.recv(64, 1000): default panic
	assert(reply == "reply", "the shut-down side can still read")
	let closed = TcpStream[_fd: -1]
	assert(closed.shutdown(Shutdown.both).is_err(), "closed streams report an error")
}

@test("net local_addr, peer_addr, and set_nodelay on a loopback connection")
bind test_net_addresses = fn(): {
	let pair = socketpair(): default panic
	assert(pair.first.peer_addr() == none, "Unix sockets have no IP address")

	let listener = listen_tcp("127.0.0.1", 0):
		default: func <-     // no loopback networking available here
	let client = dial_tcp("127.0.0.1", listener.port()):
		when err(e): {
			if(e.has_code("EPERM")): func <-
			panic(e.message())
		}
		default panic
	let server_side = listener.accept(2000): default panic
	let client_local = client.local_addr() ?? ""
	assert(client_local.starts_with("127.0.0.1:"), "client local address")
	assert(server_side.peer_addr() == client_local, "the server sees the client's address")
	assert(
		client.peer_addr() == "127.0.0.1:${listener.port()}",
		"the client's peer is the listener port",
	)
	assert(
		client.set_nodelay(true).is_ok() and client.set_nodelay(false).is_ok(),
		"TCP_NODELAY toggles",
	)
}

@test("net format_ipv6 writes RFC 5952 canonical text")
bind test_net_format_ipv6 = fn(): {
	assert(format_ipv6([0, 0, 0, 0, 0, 0, 0, 1]) == "::1", "loopback")
	assert(format_ipv6([0x2001, 0xdb8, 0, 0, 0, 0, 0, 1]) == "2001:db8::1", "longest zero run")
	assert(format_ipv6([0, 0, 0, 0, 0, 0xffff, 0x7f00, 1]) == "::ffff:127.0.0.1", "IPv4-mapped")
}

@test("net connect restores blocking flags, clears timeout leak, checks AF_INET/NUL, and zero-copy bytes")
bind test_net_connect_safety_and_zero_copy = fn(): {
	let pair = socketpair(): default panic
	assert(raw_tcp_port(pair.first.raw_fd()) == -1, "raw_tcp_port rejects AF_UNIX socketpair")

	// Verify fcntl O_NONBLOCK roundtrip, POLLOUT readiness, and SO_ERROR == 0 directly on socketpair
	let o_nb = comptime if(comp.TARGET.os == comp.TargetOs.darwin): 4 else: 2048
	let orig_fl = _net_get_flags(pair.first.raw_fd())
	assert(orig_fl >= 0 and (orig_fl & o_nb) == 0, "socketpair starts blocking")
	assert(_net_set_flags(pair.first.raw_fd(), orig_fl | o_nb) == 0, "set O_NONBLOCK succeeds")
	assert((_net_get_flags(pair.first.raw_fd()) & o_nb) != 0, "O_NONBLOCK bit observed via F_GETFL")
	assert(_net_poll_event(pair.first.raw_fd(), 4, 0) >= 1, "POLLOUT ready on writable socket")
	assert(_net_get_so_error(pair.first.raw_fd()) == 0, "SO_ERROR is 0 on healthy socket")
	assert(_net_set_flags(pair.first.raw_fd(), orig_fl) == 0, "restore blocking flags succeeds")
	assert((_net_get_flags(pair.first.raw_fd()) & o_nb) == 0, "blocking mode restored cleanly")

	// Zero-copy slice send_bytes and recv_bytes roundtrip
	let src_bytes: [u8] = [u8(9), u8(0), u8(42), u8(255), u8(17), u8(88)]
	let view = src_bytes[1..5]
	assert(pair.first.send_bytes(view).unwrap() == 4, "send_bytes slice sends 4 bytes")
	let got_view = pair.second.recv_bytes(16, 1000): default panic
	assert(
		got_view.len() == 4 and got_view[0] == u8(0) and got_view[1] == u8(42) and got_view[3] == u8(17),
		"sliced bytes roundtrip cleanly",
	)

	// Interior NUL and invalid arg validation
	let nul_host = "127.0.0.1"
	nul_host.push_byte(u8(0))
	nul_host.push("x")
	let got_listen_nul = false
	TcpListener.listen(nul_host, 0):
		when err(e): got_listen_nul = e.has_code("EINVAL")
		default: {}
	assert(got_listen_nul, "listen rejects interior NUL host with EINVAL")

	let got_conn_nul = false
	TcpStream.connect(nul_host, 80, 100):
		when err(e): got_conn_nul = e.has_code("EINVAL")
		default: {}
	assert(got_conn_nul, "connect rejects interior NUL host as err(EINVAL), not not_found")

	let got_conn_empty = false
	TcpStream.connect("", 80, 100):
		when err(e): got_conn_empty = e.has_code("EINVAL")
		default: {}
	assert(got_conn_empty, "connect rejects empty host as err(EINVAL), not not_found")

	let got_conn_port = false
	TcpStream.connect("127.0.0.1", 0, 100):
		when err(e): got_conn_port = e.has_code("EINVAL")
		default: {}
	assert(got_conn_port, "connect rejects port 0 as err(EINVAL), not not_found")

	// Loopback connect check: O_NONBLOCK restored, SO_RCVTIMEO not leaked, refused preserves errno.
	let listener = listen_tcp("127.0.0.1", 0):
		default: func <-
	let port = listener.port()
	let client = TcpStream.connect("127.0.0.1", port, 500):
		when err(e): {
			if(e.has_code("EPERM")): func <-
			panic(e.message())
		}
		default panic
	let server_side = listener.accept(1000): default panic
	let fl = _net_get_flags(client.raw_fd())
	assert(fl >= 0 and (fl & o_nb) == 0, "connected socket is restored to blocking mode")

	comptime if(comp.TARGET.os == comp.TargetOs.darwin): {
		unsafe {
			let tv = alloc_raw[int](2)
			ptr_write(tv, -1)
			ptr_write(ptr_add(tv, 1), -1)
			let l = alloc_raw[u32](1)
			ptr_write(l, u32(16))
			assert(_net_getsockopt(i32(client.raw_fd()), i32(65535), i32(4102), ptr_cast[u8](tv), l) == i32(0), "getsockopt SO_RCVTIMEO succeeds")
			assert(ptr_read(tv) == 0 and ptr_read(ptr_add(tv, 1)) == 0, "connect timeout does not leak into SO_RCVTIMEO")
			free_raw(l)
			free_raw(tv)
		}
	}

	server_side.send("hi"): default panic
	let got_hi = client.recv(16, 1000): default panic
	assert(got_hi == "hi", "loopback recv succeeds after timed connect")
	client.close()
	server_side.close()

	// Zero-timeout connect must never misclassify as not_found
	let zero_ok_or_timeout = false
	match(TcpStream.connect("127.0.0.1", port, 0)):
		when value(_): zero_ok_or_timeout = true
		when timeout(_): zero_ok_or_timeout = true
		default: {}
	assert(zero_ok_or_timeout, "zero-timeout connect completes or times out cleanly")
	let _ = listener.accept(50)

	// Closed port -> ConnectionRefused (`errno` preserved across freeaddrinfo)
	listener.close()
	let refused_ok = false
	TcpStream.connect("127.0.0.1", port, 1000):
		when refused(_): refused_ok = true
		default: {}
	assert(refused_ok, "closed loopback port reports refused across freeaddrinfo")
}
