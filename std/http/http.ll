// std/http — HTTP/1.1 client, server, router, URL parser, and test utilities.
//
// Provides [`Url`], [`Request`], [`Response`], [`ResponseWriter`], [`ServeMux`],
// blocking [`Client`] (HTTP and dynamic OpenSSL HTTPS), [`Server`], and in-memory
// testing helpers ([`ResponseRecorder`], [`test_handler`], [`test_roundtrip`]).

import "std/net"
import "std/os"
import "std/time"

// ============================================================================
// 1. HTTP Status Codes & Status Texts
// ============================================================================

// HTTP 100 Continue.
bind STATUS_CONTINUE = 100

// HTTP 101 Switching Protocols.
bind STATUS_SWITCHING_PROTOCOLS = 101

// HTTP 200 OK.
bind STATUS_OK = 200

// HTTP 201 Created.
bind STATUS_CREATED = 201

// HTTP 202 Accepted.
bind STATUS_ACCEPTED = 202

// HTTP 203 Non-Authoritative Information.
bind STATUS_NON_AUTHORITATIVE_INFO = 203

// HTTP 204 No Content.
bind STATUS_NO_CONTENT = 204

// HTTP 205 Reset Content.
bind STATUS_RESET_CONTENT = 205

// HTTP 206 Partial Content.
bind STATUS_PARTIAL_CONTENT = 206

// HTTP 300 Multiple Choices.
bind STATUS_MULTIPLE_CHOICES = 300

// HTTP 301 Moved Permanently.
bind STATUS_MOVED_PERMANENTLY = 301

// HTTP 302 Found.
bind STATUS_FOUND = 302

// HTTP 303 See Other.
bind STATUS_SEE_OTHER = 303

// HTTP 304 Not Modified.
bind STATUS_NOT_MODIFIED = 304

// HTTP 307 Temporary Redirect.
bind STATUS_TEMPORARY_REDIRECT = 307

// HTTP 308 Permanent Redirect.
bind STATUS_PERMANENT_REDIRECT = 308

// HTTP 400 Bad Request.
bind STATUS_BAD_REQUEST = 400

// HTTP 401 Unauthorized.
bind STATUS_UNAUTHORIZED = 401

// HTTP 402 Payment Required.
bind STATUS_PAYMENT_REQUIRED = 402

// HTTP 403 Forbidden.
bind STATUS_FORBIDDEN = 403

// HTTP 404 Not Found.
bind STATUS_NOT_FOUND = 404

// HTTP 405 Method Not Allowed.
bind STATUS_METHOD_NOT_ALLOWED = 405

// HTTP 406 Not Acceptable.
bind STATUS_NOT_ACCEPTABLE = 406

// HTTP 407 Proxy Authentication Required.
bind STATUS_PROXY_AUTH_REQUIRED = 407

// HTTP 408 Request Timeout.
bind STATUS_REQUEST_TIMEOUT = 408

// HTTP 409 Conflict.
bind STATUS_CONFLICT = 409

// HTTP 410 Gone.
bind STATUS_GONE = 410

// HTTP 411 Length Required.
bind STATUS_LENGTH_REQUIRED = 411

// HTTP 413 Payload Too Large.
bind STATUS_PAYLOAD_TOO_LARGE = 413

// HTTP 414 URI Too Long.
bind STATUS_URI_TOO_LONG = 414

// HTTP 415 Unsupported Media Type.
bind STATUS_UNSUPPORTED_MEDIA_TYPE = 415

// HTTP 429 Too Many Requests.
bind STATUS_TOO_MANY_REQUESTS = 429

// HTTP 500 Internal Server Error.
bind STATUS_INTERNAL_SERVER_ERROR = 500

// HTTP 501 Not Implemented.
bind STATUS_NOT_IMPLEMENTED = 501

// HTTP 502 Bad Gateway.
bind STATUS_BAD_GATEWAY = 502

// HTTP 503 Service Unavailable.
bind STATUS_SERVICE_UNAVAILABLE = 503

// HTTP 504 Gateway Timeout.
bind STATUS_GATEWAY_TIMEOUT = 504

// HTTP 505 HTTP Version Not Supported.
bind STATUS_HTTP_VERSION_NOT_SUPPORTED = 505

// Returns the standard RFC reason phrase for HTTP status `code`, or `"Unknown"` if unrecognized.
bind status_text = fn(code: int) string:
	match(code):
		when 100: "Continue"
		when 101: "Switching Protocols"
		when 200: "OK"
		when 201: "Created"
		when 202: "Accepted"
		when 203: "Non-Authoritative Information"
		when 204: "No Content"
		when 205: "Reset Content"
		when 206: "Partial Content"
		when 300: "Multiple Choices"
		when 301: "Moved Permanently"
		when 302: "Found"
		when 303: "See Other"
		when 304: "Not Modified"
		when 307: "Temporary Redirect"
		when 308: "Permanent Redirect"
		when 400: "Bad Request"
		when 401: "Unauthorized"
		when 402: "Payment Required"
		when 403: "Forbidden"
		when 404: "Not Found"
		when 405: "Method Not Allowed"
		when 406: "Not Acceptable"
		when 407: "Proxy Authentication Required"
		when 408: "Request Timeout"
		when 409: "Conflict"
		when 410: "Gone"
		when 411: "Length Required"
		when 413: "Payload Too Large"
		when 414: "URI Too Long"
		when 415: "Unsupported Media Type"
		when 429: "Too Many Requests"
		when 500: "Internal Server Error"
		when 501: "Not Implemented"
		when 502: "Bad Gateway"
		when 503: "Service Unavailable"
		when 504: "Gateway Timeout"
		when 505: "HTTP Version Not Supported"
		default: "Unknown"

// ============================================================================
// 2. URL Parsing & Percent-Encoding
// ============================================================================

// Parsed HTTP or HTTPS [`Url`] components including percent-decoded query parameters.
@derive(Copyable)
define Url struct [
	// Original unparsed URL string.
	raw: string,
	// URL scheme such as `"http"` or `"https"`, or empty for relative paths.
	scheme: string,
	// Host and optional port as written (`"example.com:8080"`, `"[::1]:8080"`), without user information.
	host: string,
	// Hostname without port or brackets (`"example.com"`, `"::1"`).
	hostname: string,
	// Port number, or the scheme's default port (see [`default_port`]) when omitted.
	port: int,
	// Percent-decoded path starting with `"/"`. A `'+'` in a path is a literal plus sign.
	path: string,
	// Raw query string without leading `"?"`.
	raw_query: string,
	// Percent-decoded query parameter key-value map (`'+'` decodes to a space).
	query: #[string: string],
	// Path exactly as written, still percent-encoded (`"/a%20b"`); empty for synthesized URLs.
	raw_path: string = "",
	// Fragment after `"#"`, as written; never sent to servers.
	fragment: string = "",
	// Percent-decoded user name from `user:password@host`, or empty.
	username: string = "",
	// Percent-decoded password from `user:password@host`, or `none` when absent.
	password?: ?string,
]

// Returns the parsed query parameter value for `key`, or `none` if absent.
bind Url.query_param = fn(self, key: string) ?string: self.query[key]

// Returns the default port of a URL scheme: `80` for `http` and `ws`, `443` for
// `https` and `wss`, and `0` for unknown schemes.
bind default_port = fn(scheme: string) int:
	match(scheme.to_lower()):
		when "http", "ws": 80
		when "https", "wss": 443
		default: 0

// Reconstructs a normalized URL or relative path string from this [`Url`].
//
// The port is omitted when it is the scheme's default, so `http://x:443/` keeps its port
// while `https://x:443/` does not. User information, the query, and the fragment are kept.
bind Url.to_string = fn(self) string: {
	let out = ""
	if(not self.scheme.is_empty()): {
		out.push("${self.scheme}://")
		if(not self.username.is_empty() or self.password? != none): {
			out.push(url_encode(self.username))
			if(self.password? as pw): out.push(":${url_encode(pw)}")
			out.push("@")
		}
		out.push(if(self.hostname.contains(":")): "[${self.hostname}]" else: self.hostname.copy())
		if(self.port > 0 and self.port != default_port(self.scheme)): out.push(":${self.port}")
	}
	out.push(if(self.raw_path.is_empty()): self.path.copy() else: self.raw_path.copy())
	if(not self.raw_query.is_empty()): out.push("?${self.raw_query}")
	if(not self.fragment.is_empty()): out.push("#${self.fragment}")
	out
}

bind _hex_digit_val = fn(b: u8) ?int:
	if(b >= 48 and b <= 57): int(b - 48)
	else if(b >= 97 and b <= 102): int(b - 97 + 10)
	else if(b >= 65 and b <= 70): int(b - 65 + 10)
	else: none

bind _byte_to_hex_str = fn(b: u8) string: {
	let digits = "0123456789ABCDEF"
	let hi = int(b >> 4)
	let lo = int(b & 0x0F)
	let out = ""
	out.push_byte(digits.byte_at(hi))
	out.push_byte(digits.byte_at(lo))
	out
}

bind _is_url_unreserved = fn(b: u8) bool: (b >= 65 and b <= 90) or (b >= 97 and b <= 122) or (b >= 48 and b <= 57) or b == 45 or b == 95 or b == 46 or b == 126

// Percent-encodes non-unreserved bytes in `s` (encoding spaces as `"%20"`).
bind url_encode = fn(s: string) string: {
	let out = ""
	let bytes = s.bytes()
	for(b in bytes):
		if(_is_url_unreserved(b)): out.push_byte(b)
		else if(b == 32): out.push("%20")
		else: {
			out.push("%")
			out.push(_byte_to_hex_str(b))
		}
	out
}

// Decodes `%XX` hex escape sequences and `'+'` spaces in `s`.
bind url_decode = fn(s: string) string: {
	let out = ""
	let n = s.len()
	let i = 0
	for(i < n): {
		let b = s.byte_at(i)
		if(b == 37 and i + 2 < n): {
			let h1? = _hex_digit_val(s.byte_at(i + 1))
			let h2? = _hex_digit_val(s.byte_at(i + 2))
			if(h1? != none and h2? != none): {
				let dec_byte = u8((h1? ?? 0) << 4 | (h2? ?? 0))
				out.push_byte(dec_byte)
				i += 3
				next <-
			}
		} else if(b == 43): {
			out.push_byte(32)
			i += 1
			next <-
		}
		out.push_byte(b)
		i += 1
	}
	out
}

// Decodes `%XX` hex escape sequences in a URL path. Unlike [`url_decode`], `'+'` is kept
// as a literal plus sign, since only query strings use `'+'` for spaces.
bind url_path_decode = fn(s: string) string: {
	let out = ""
	let n = s.len()
	let i = 0
	while(i < n): {
		let b = s.byte_at(i)
		if(b == 37 and i + 2 < n): {
			if(_hex_digit_val(s.byte_at(i + 1)) as h1): if(_hex_digit_val(s.byte_at(i + 2)) as h2): {
				out.push_byte(u8(h1 << 4 | h2))
				i += 3
				next <-
			}
		}
		out.push_byte(b)
		i += 1
	}
	out
}

// Parses a full HTTP/HTTPS URL or relative request target into a structured [`Url`].
//
// Accepts `scheme://[user[:password]@]host[:port][/path][?query][#fragment]` and relative
// targets such as `/path?query`. The path is percent-decoded into [`Url.path`] (keeping
// `'+'`), query parameters are decoded with `'+'` as a space, and an omitted port takes
// the scheme's [`default_port`].
bind parse_url = fn(raw: string) Result[Url]: {
	// The fragment is everything after the first `#`, and is never part of the path or query.
	let fragment = ""
	let rest = raw.copy()
	if(raw.index_of("#") as hash): {
		fragment = raw[hash + 1..]
		rest = raw[..hash]
	}

	let scheme = ""
	if(rest.index_of("://") as sep): {
		let candidate = rest[..sep]
		if(not candidate.is_empty() and not candidate.contains("/") and not candidate.contains("?")): {
			scheme = candidate.to_lower()
			rest = rest[sep + 3..]
		}
	}
	let scheme_port = default_port(scheme)

	let host = ""
	let hostname = ""
	let port = scheme_port
	let username = ""
	let password?: ?string = none
	let path_and_query = "/"

	if(scheme != ""): {
		// The authority runs up to the first `/` or `?`.
		let end = rest.len()
		if(rest.index_of("/") as slash): end = slash
		if(rest.index_of("?") as q): if(q < end): end = q
		let authority = rest[..end]
		path_and_query = if(end < rest.len()): rest[end..] else: "/"

		// User information is everything before the last `@`.
		host = authority.copy()
		let at = -1
		for(i, c in authority): if(c == '@'): at = i
		if(at >= 0): {
			let userinfo = authority[..at]
			host = authority[at + 1..]
			if(userinfo.index_of(":") as colon): {
				username = url_path_decode(userinfo[..colon])
				password? = url_path_decode(userinfo[colon + 1..])
			} else: username = url_path_decode(userinfo)
		}

		if(host.starts_with("[")): {
			// An IPv6 literal: `[addr]` or `[addr]:port`.
			let close = host.index_of("]") ?? func <- err(Error.coded("invalid_url", "missing ']' in IPv6 host: $host"))
			hostname = host[1..close]
			let after = host[close + 1..]
			if(after.starts_with(":")): port = parse_int(after[1..]) ?? scheme_port
			else if(not after.is_empty()): func <- err(Error.coded("invalid_url", "unexpected text after IPv6 host: $host"))
		} else if(host.index_of(":") as colon): {
			hostname = host[0..colon]
			port = parse_int(host[colon + 1..]) ?? scheme_port
		} else: hostname = host.copy()
	} else: path_and_query = rest.copy()

	let raw_path = "/"
	let raw_query = ""
	if(path_and_query.index_of("?") as q_idx): {
		raw_path = path_and_query[0..q_idx]
		raw_query = path_and_query[q_idx + 1..path_and_query.len()]
	} else: raw_path = path_and_query.copy()

	if(raw_path.is_empty()): raw_path = "/"
	let path = url_path_decode(raw_path)

	let query: #[string: string] = #[]
	if(not raw_query.is_empty()): {
		let pairs = raw_query.split("&")
		for(pair in pairs): {
			let eq? = pair.index_of("=")
			if(eq?): {
				let k = url_decode(pair[0..eq])
				let v = url_decode(pair[eq + 1..pair.len()])
				query[k] = v
			} else if(not pair.is_empty()): {
				let k = url_decode(pair)
				query[k] = ""
			}
		}
	}

	value(Url[
		raw: raw.copy(),
		scheme: scheme,
		host: host,
		hostname: hostname,
		port: port,
		path: path,
		raw_query: raw_query,
		query: query,
		raw_path: raw_path,
		fragment: fragment,
		username: username,
		password?: password?,
	])
}

// ============================================================================
// 3. Header Fields
// ============================================================================

// One HTTP header field: a name as written on the wire and its value.
@derive(Copyable)
define HeaderField struct [
	// Field name with its original capitalization (`"Content-Type"`).
	name: string,
	// Field value with surrounding whitespace removed.
	value: string,
]

// An ordered list of HTTP header fields.
//
// Names compare case-insensitively (ASCII only, as RFC 9110 specifies), keep the spelling they
// were set with, and may repeat: `Set-Cookie` and other list-valued fields keep every value
// in order. Use [`Headers.get`] for the first value and [`Headers.values`] for all of them.
@derive(Copyable)
define Headers struct [
	_fields: [HeaderField]
]

// Creates an empty [`Headers`].
bind Headers.new = fn() Headers: Headers[_fields: []]

// Reports whether two header names are equal ignoring ASCII case.
bind header_name_eq = fn(a: string, b: string) bool: {
	if(a.len() != b.len()): func <- false
	for(i in 0..a.len()): {
		let x = a.byte_at(i)
		let y = b.byte_at(i)
		if(x == y): next <-
		let lx = if(x >= 65 and x <= 90): x + 32 else: x
		let ly = if(y >= 65 and y <= 90): y + 32 else: y
		if(lx != ly): func <- false
	}
	true
}

// Returns the first value of field `name`, or `none` if it is absent.
bind Headers.get = fn(self, name: string) ?string: {
	for(f in self._fields): if(header_name_eq(f.name, name)): func <- f.value.copy()
	none
}

// Returns every value of field `name`, in the order they appear.
bind Headers.values = fn(self, name: string) [string]: for(f in self._fields) [string]: {
	if(not header_name_eq(f.name, name)): next <-
	f.value.copy()
}

// Reports whether field `name` is present.
bind Headers.has = fn(self, name: string) bool: {
	for(f in self._fields): if(header_name_eq(f.name, name)): func <- true
	false
}

// Appends a field, keeping any existing fields with the same name.
bind Headers.add = fn(&self, name: string, value: string): self._fields.push(HeaderField[name: name.copy(), value: value.copy()])

// Sets field `name` to the single value `value`: the first existing field with that name is
// updated in place and later ones are removed, or the field is appended if absent.
bind Headers.set = fn(&self, name: string, value: string): {
	let kept: [HeaderField] = []
	let done = false
	for(f in self._fields): {
		if(not header_name_eq(f.name, name)): kept.push(f.copy())
		else if(not done): {
			kept.push(HeaderField[name: f.name.copy(), value: value.copy()])
			done = true
		}
	}
	if(not done): kept.push(HeaderField[name: name.copy(), value: value.copy()])
	self._fields = own kept
}

// Removes every field named `name` and returns how many were removed.
bind Headers.remove = fn(&self, name: string) int: {
	let kept = for(f in self._fields) [HeaderField]: {
		if(header_name_eq(f.name, name)): next <-
		f.copy()
	}
	let removed = self._fields.len() - kept.len()
	self._fields = own kept
	removed
}

// Returns the number of fields, counting repeated names separately.
bind Headers.len = fn(self) int: self._fields.len()

// Reports whether there are no fields.
bind Headers.is_empty = fn(self) bool: self._fields.is_empty()

// Returns a copy of every field, in order.
bind Headers.fields = fn(self) [HeaderField]: self._fields.copy()

// Reports whether `name` is a valid field name: a non-empty RFC 9110 token.
bind _is_token = fn(name: string) bool: {
	if(name.is_empty()): func <- false
	for(i in 0..name.len()): {
		let b = name.byte_at(i)
		let ok = (b >= 48 and b <= 57) or (b >= 65 and b <= 90) or (b >= 97 and b <= 122) or "!#\$%&'*+-.^_`|~".contains(string_from_byte(b))
		if(not ok): func <- false
	}
	true
}

bind string_from_byte = fn(b: u8) string: {
	let s = ""
	s.push_byte(b)
	s
}

// Parses header lines (each `Name: value`, without line terminators) into [`Headers`].
//
// Rejects field names that are not tokens, including names with whitespace before the colon,
// which RFC 9112 forbids. A line starting with a space or tab continues the previous value
// (obsolete line folding) and is joined with a single space.
bind _parse_header_lines = fn(lines: [string], start: int) Result[Headers]: {
	let h = Headers.new()
	for(i, raw in lines): {
		if(i < start): next <-
		let line = if(raw.ends_with("\r")): raw[..raw.len() - 1] else: raw[..]
		if(line.is_empty()): next <-
		let first = line.byte_at(0)
		if(first == 32 or first == 9): {
			let n = h._fields.len()
			if(n == 0): func <- err(Error.coded("malformed", "header continuation line before any field"))
			h._fields[n - 1].value.push(" ${line.trim()}")
			next <-
		}
		let colon = line.index_of(":") ?? func <- err(Error.coded("malformed", "header line without ':': $line"))
		let name = line[..colon]
		if(not _is_token(name)): func <- err(Error.coded("malformed", "invalid header field name '$name'"))
		h.add(name, line[colon + 1..].trim())
	}
	value(h)
}

// Writes header fields as `Name: value` lines, removing CR, LF, and NUL bytes so values
// cannot inject extra lines.
bind _append_header_fields = fn(out: &string, headers: Headers):
	for(f in headers._fields): {
		let name = _strip_crlf(f.name)
		if(name.is_empty()): next <-
		out.push("$name: ${_strip_crlf(f.value)}\r\n")
	}

// ============================================================================
// 4. HTTP Request
// ============================================================================

// HTTP request message constructed by callers or parsed off the wire by [`Server`].
@derive(Copyable)
define Request struct [
	// Uppercase HTTP method (`"GET"`, `"POST"`, etc.).
	method: string,
	// Full target URL string.
	url: string,
	// Normalized request path (e.g. `"/api/items"`).
	path: string,
	// Protocol version string (defaults to `"HTTP/1.1"`).
	proto: string,
	// Header fields, in order (see [`Headers`]).
	headers: Headers,
	// Parsed URL query parameters.
	query: #[string: string],
	// Request payload body bytes as a string.
	body: string,
	// Client address on the server side (`"127.0.0.1:52000"`, `"[::1]:52000"`), or empty.
	remote_addr: string,
]

// Constructs a [`Request`] for `method`, `raw_url`, and optional `body`, setting `host` and `content-length` headers.
bind Request.new = fn(method: string, raw_url: string, body: string) Request: {
	let u = parse_url(raw_url) ?? Url[
		raw: raw_url.copy(),
		scheme: "",
		host: "",
		hostname: "",
		port: 80,
		path: if(raw_url.starts_with("/")): raw_url.copy() else: "/",
		raw_query: "",
		query: #[],
	]

	let hdrs = Headers.new()
	if(not u.host.is_empty()): hdrs.set("Host", u.host)
	if(not body.is_empty()): hdrs.set("Content-Length", "${body.len()}")

	Request[
		method: method.to_upper(),
		url: u.raw.copy(),
		path: u.path.copy(),
		proto: "HTTP/1.1",
		headers: hdrs,
		query: u.query.copy(),
		body: body.copy(),
		remote_addr: "",
	]
}

// Returns the first value of header `name` (compared case-insensitively), or `none`.
bind Request.header = fn(self, name: string) ?string: self.headers.get(name)

// Returns every value of header `name`, in order.
bind Request.header_values = fn(self, name: string) [string]: self.headers.values(name)

// Sets header `name` to a single `value`, replacing any existing values.
bind Request.set_header = fn(&self, name: string, value: string): self.headers.set(name, value)

// Adds a value for header `name`, keeping existing values.
bind Request.add_header = fn(&self, name: string, value: string): self.headers.add(name, value)

// Returns the URL query parameter value for `name`, or `none` if absent.
bind Request.query_param = fn(self, name: string) ?string: self.query[name]

bind _strip_crlf = fn(s: string) string: {
	let has_bad = false
	for(b in s.bytes()):
		if(b == 13 or b == 10 or b == 0): {
			has_bad = true
			for <-
		}
	if(not has_bad): func <- s.copy()
	let out = ""
	for(b in s.bytes()): if(b != 13 and b != 10 and b != 0): out.push_byte(b)
	out
}

// Percent-encodes a decoded path for a request line: unreserved characters, `/`, and the
// RFC 3986 sub-delimiters, `:`, and `@` stay as they are.
bind _escape_path = fn(path: string) string: {
	let out = ""
	for(b in path.bytes()): {
		let keep = _is_url_unreserved(b) or "/!\$&'()*+,;=:@".contains(string_from_byte(b))
		if(keep): out.push_byte(b)
		else: {
			out.push("%")
			out.push(_byte_to_hex_str(b))
		}
	}
	out
}

// Serializes the request into HTTP/1.1 wire bytes (request line, CRLF headers, and body).
bind Request.format = fn(self) string: {
	let out = ""
	let m = _strip_crlf(if(self.method.is_empty()): "GET" else: self.method.copy())
	let target = _strip_crlf(if(self.path.is_empty()): "/" else: _escape_path(self.path))
	if(not self.query.is_empty()): {
		let first = not target.contains("?")
		for(k, v in self.query): {
			if(first): {
				target.push("?")
				first = false
			} else: target.push("&")
			target.push(url_encode(k))
			target.push("=")
			target.push(url_encode(v))
		}
	}
	let pr = _strip_crlf(if(self.proto.is_empty()): "HTTP/1.1" else: self.proto.copy())
	out.push("$m $target $pr\r\n")
	_append_header_fields(&out, self.headers)
	out.push("\r\n")
	if(not self.body.is_empty()): out.push(self.body)
	out
}

// Parses raw HTTP/1.1 wire bytes into a [`Request`], returning [`Result.err`] if the request line or framing is malformed.
bind Request.parse = fn(raw: string) Result[Request]: {
	let header_end? = raw.index_of("\r\n\r\n")
	let sep_len = 4
	let header_end = 0

	if(header_end? != none): {
		header_end = header_end? ?? 0
		sep_len = 4
	} else: {
		let lf_end? = raw.index_of("\n\n")
		if(lf_end? != none): {
			header_end = lf_end? ?? 0
			sep_len = 2
		} else: func <- err(
			Error.new("malformed HTTP request: missing header terminator")
		)
	}

	let header_part = raw[0..header_end]
	let body_part = raw[header_end + sep_len..raw.len()]

	let lines = header_part.split("\n")
	if(lines.is_empty()): func <- err(Error.new("empty request"))

	let req_line = (lines.get(0) ?? "").trim()
	let parts = req_line.split(" ")
	if(parts.len() < 2): func <- err(Error.new("invalid HTTP request line: $req_line"))

	let method = (parts.get(0) ?? "").to_upper()
	let raw_target = parts.get(1) ?? "/"
	let proto = if(parts.len() >= 3): parts.get(2) ?? "HTTP/1.1" else: "HTTP/1.1"

	let parsed_url = parse_url(raw_target) ?? Url[
		raw: raw_target.copy(),
		scheme: "",
		host: "",
		hostname: "",
		port: 80,
		path: raw_target.copy(),
		raw_query: "",
		query: #[],
	]

	let headers = _parse_header_lines(lines, 1): default err(own e): func <- err(e)

	value(Request[
		method: method,
		url: parsed_url.raw.copy(),
		path: parsed_url.path.copy(),
		proto: proto,
		headers: headers,
		query: parsed_url.query.copy(),
		body: body_part.copy(),
		remote_addr: "",
	])
}

// ============================================================================
// 5. HTTP Response
// ============================================================================

// HTTP response message produced by handlers ([`ResponseWriter.finish`]) or returned by [`Client.do`].
define Response struct [
	// Numeric HTTP status code (e.g. `200`, `404`).
	status_code: int,
	// HTTP reason phrase (e.g. `"OK"`).
	status: string,
	// Protocol version string (e.g. `"HTTP/1.1"`).
	proto: string,
	// Header fields, in order (see [`Headers`]).
	headers: Headers,
	// Response payload body.
	body: string,
]

// Constructs a `"200"`/status-text HTTP/1.1 [`Response`] with `body` and default `"text/plain; charset=utf-8"` content type.
bind Response.new = fn(status_code: int, body: string) Response: {
	let hdrs = Headers.new()
	hdrs.set("Content-Length", "${body.len()}")
	hdrs.set("Content-Type", "text/plain; charset=utf-8")

	Response[
		status_code: status_code,
		status: status_text(status_code),
		proto: "HTTP/1.1",
		headers: hdrs,
		body: body.copy(),
	]
}

// Returns the first value of header `name` (compared case-insensitively), or `none`.
bind Response.header = fn(self, name: string) ?string: self.headers.get(name)

// Returns every value of header `name`, in order (for example, every `Set-Cookie`).
bind Response.header_values = fn(self, name: string) [string]: self.headers.values(name)

// Sets header `name` to a single `value`, replacing any existing values.
bind Response.set_header = fn(&self, name: string, value: string): self.headers.set(name, value)

// Adds a value for header `name`, keeping existing values.
bind Response.add_header = fn(&self, name: string, value: string): self.headers.add(name, value)

// Reports whether `status_code` is in the 2xx success range (`200..300`).
bind Response.is_success = fn(self) bool: self.status_code >= 200 and self.status_code < 300

// Returns a copy of the response body string.
bind Response.text = fn(self) string: self.body.copy()

// Serializes this [`Response`] into HTTP/1.1 wire bytes (status line, headers, and body).
bind Response.format = fn(self) string: {
	let out = ""
	let pr = _strip_crlf(if(self.proto.is_empty()): "HTTP/1.1" else: self.proto.copy())
	let code = if(self.status_code <= 0): 200 else: self.status_code
	let msg = _strip_crlf(if(self.status.is_empty()): status_text(code) else: self.status.copy())
	out.push("$pr $code $msg\r\n")
	_append_header_fields(&out, self.headers)
	out.push("\r\n")
	if(not self.body.is_empty()): out.push(self.body)
	out
}

// Parses raw HTTP/1.1 wire bytes into a [`Response`], failing if headers or status line are malformed.
//
// A `Transfer-Encoding: chunked` body is decoded, and its trailer fields are added to the headers.
bind Response.parse = fn(raw: string) Result[Response]: {
	let header_end? = raw.index_of("\r\n\r\n")
	let sep_len = 4
	let header_end = 0

	if(header_end? != none): {
		header_end = header_end? ?? 0
		sep_len = 4
	} else: {
		let lf_end? = raw.index_of("\n\n")
		if(lf_end? != none): {
			header_end = lf_end? ?? 0
			sep_len = 2
		} else: func <- err(
			Error.new("malformed HTTP response: missing header terminator")
		)
	}

	let header_part = raw[0..header_end]
	let body_part = raw[header_end + sep_len..raw.len()]

	let lines = header_part.split("\n")
	if(lines.is_empty()): func <- err(Error.new("empty response"))

	let status_line = (lines.get(0) ?? "").trim()
	let parts = status_line.split(" ")
	if(parts.len() < 2): func <- err(Error.new("invalid HTTP status line: $status_line"))

	let proto = parts.get(0) ?? "HTTP/1.1"
	let code_str = parts.get(1) ?? "200"
	let status_code = parse_int(code_str) ?? 200

	let status_msg = ""
	if(parts.len() >= 3): {
		let msg_parts: [string] = []
		for(i in 2..parts.len()): msg_parts.push((parts.get(i) ?? "").copy())
		status_msg = msg_parts.join(" ")
	} else: status_msg = status_text(status_code)

	let headers = _parse_header_lines(lines, 1): default err(own e): func <- err(e)
	let body = body_part.copy()
	if(_is_chunked(headers.get("transfer-encoding") ?? "")): {
		let decoded = _scan_chunked(body_part):
			when incomplete: func <- err(Error.coded("malformed", "chunked body is incomplete"))
			default malformed(why): func <- err(Error.coded("malformed", "malformed chunked body: $why"))
		let text, _, trailers = unpack decoded
		for(f in trailers): headers.add(f.name, f.value)
		body = own text
	}

	value(Response[
		status_code: status_code,
		status: status_msg,
		proto: proto,
		headers: headers,
		body: body,
	])
}

// ============================================================================
// 6. ResponseWriter (for HTTP handlers)
// ============================================================================

// Incremental builder used by HTTP server handlers to assemble an outgoing [`Response`].
//
// Call [`ResponseWriter.set_header`] and [`ResponseWriter.write_header`] before
// [`ResponseWriter.write`] when customizing headers or status codes, then convert
// to a [`Response`] via [`ResponseWriter.finish`].
define ResponseWriter struct [
	// HTTP status code recorded for the response (defaults to `200`).
	status_code: int,
	// Header fields, in order (see [`Headers`]).
	headers: Headers,
	// Accumulated response body payload.
	body: string,
	_header_written: bool,
]

// Creates a blank [`ResponseWriter`] initialized to `200 OK` with empty headers and body.
bind ResponseWriter.new = fn() ResponseWriter: ResponseWriter[
	status_code: 200,
	headers: Headers.new(),
	body: "",
	_header_written: false,
]

// Records the HTTP status `code` if headers have not yet been written.
//
// Subsequent calls after headers have been committed (either directly via
// [`ResponseWriter.write_header`] or implicitly via [`ResponseWriter.write`])
// have no effect.
bind ResponseWriter.write_header = fn(&self, code: int):
	if(not self._header_written): {
		self.status_code = code
		self._header_written = true
	}

// Sets response header `key` to a single value `val`, replacing any existing values.
bind ResponseWriter.set_header = fn(&self, key: string, val: string): self.headers.set(key, val)

// Adds a value for response header `key`, keeping existing values (for example, several
// `Set-Cookie` fields).
bind ResponseWriter.add_header = fn(&self, key: string, val: string): self.headers.add(key, val)

// Returns the first value of response header `key` (compared case-insensitively), or `none`.
bind ResponseWriter.header = fn(self, key: string) ?string: self.headers.get(key)

// Appends `text` to the response body, committing status `200` if no status was set yet.
bind ResponseWriter.write = fn(&self, text: string): {
	if(not self._header_written): self.write_header(200)
	self.body.push(text)
}

// Finalizes defaults (`content-length` and `content-type`) and returns the built [`Response`].
//
// If `content-type` is unset and the body is non-empty, it defaults to
// `"text/plain; charset=utf-8"`. Status codes `<= 0` default to `200`.
bind ResponseWriter.finish = fn(self) Response: {
	let hdrs = self.headers.copy()
	if(not hdrs.has("content-length")): hdrs.set("Content-Length", "${self.body.len()}")
	if(not hdrs.has("content-type") and not self.body.is_empty()): hdrs.set("Content-Type", "text/plain; charset=utf-8")

	Response[
		status_code: if(self.status_code > 0): self.status_code else: 200,
		status: status_text(if(self.status_code > 0): self.status_code else: 200),
		proto: "HTTP/1.1",
		headers: hdrs,
		body: self.body.copy(),
	]
}

// ============================================================================
// 7. Router & Multiplexer (ServeMux)
// ============================================================================

define _Route struct [
	pattern: string,
	is_prefix: bool,
	handler: cl(Request, &ResponseWriter),
]

bind _Route.copy = fn(self) _Route: _Route[
	pattern: self.pattern.copy(),
	is_prefix: self.is_prefix,
	handler: self.handler.copy(),
]

// HTTP path multiplexer routing incoming [`Request`] values to registered handlers.
//
// Exact path patterns take priority over trailing-slash prefix patterns; among
// multiple matching prefixes, the longest pattern wins. Unmatched paths fall
// back to `404 Not Found`.
define ServeMux struct [
	_routes: [_Route]
]

// Creates an empty [`ServeMux`] with no registered routes.
bind ServeMux.new = fn() ServeMux: ServeMux[_routes: []]

// Returns a deep copy of this [`ServeMux`] and all registered route handlers.
bind ServeMux.copy = fn(self) ServeMux: {
	let copied: [_Route] = []
	for(r in self._routes): copied.push(r.copy())
	ServeMux[_routes: own copied]
}

// Registers `handler` for requests matching `pattern`.
//
// - `pattern`: Route path. A trailing `"/"` registers a subtree prefix match;
//   any other path requires an exact match against `req.path`.
// - `handler`: Callback invoked with the incoming [`Request`] and a mutable
//   [`ResponseWriter`].
bind ServeMux.handle_func = fn(&self, pattern: string, handler: cl(Request, &ResponseWriter)): {
	let is_prefix = pattern.ends_with("/")
	self._routes.push(_Route[
		pattern: pattern.copy(),
		is_prefix: is_prefix,
		handler: handler.copy(),
	])
}

// Dispatches `req` to the matching handler or writes a `404` fallback into `res`.
bind ServeMux.serve_http = fn(self, req: Request, res: &ResponseWriter): {
	// 1. Check exact match
	for(r in self._routes):
		if(not r.is_prefix and r.pattern == req.path): {
			let h = r.handler.copy()
			h(req, &res)
			func <-
		}

	// 2. Check prefix match (prefer longest prefix)
	let best_idx?: ?int = none
	let best_len = -1
	for(i, r in self._routes):
		if(r.is_prefix and req.path.starts_with(r.pattern)):
			if(r.pattern.len() > best_len): {
				best_len = r.pattern.len()
				best_idx? = i
			}

	if(best_idx?): {
		let route? = self._routes.get(best_idx)
		if(route?): {
			let h = route.handler.copy()
			h(req, &res)
			func <-
		}
	}

	// 3. Fallback: 404 Not Found
	res.write_header(STATUS_NOT_FOUND)
	res.set_header("Content-Type", "text/plain; charset=utf-8")
	res.write("404 page not found\n")
}

// ============================================================================
// 8. Internal Socket Message Reading Helper
// ============================================================================

// Reads HTTP/1.1 messages from a socket or TLS session, one at a time.
//
// Bytes received past the end of one message stay in `buf` for the next one, so a client
// may pipeline several requests on one connection. Framing follows RFC 9112: a chunked
// `Transfer-Encoding` wins over `Content-Length`; responses to `HEAD`, and `1xx`, `204`,
// and `304` responses, have no body; a request without either has no body; a response
// without either runs until the connection closes.
define _MessageReader struct [
	fd: int,
	tls: ptr[u8],
	buf: string,
	eof: bool,
]

bind _MessageReader.plain = fn(fd: int) _MessageReader:
	_MessageReader[fd: fd, tls: unsafe null_ptr[u8](), buf: "", eof: false]

bind _MessageReader.over_tls = fn(session: ptr[u8]) _MessageReader:
	_MessageReader[fd: -1, tls: session, buf: "", eof: false]

// Reads more bytes into `buf`, returning how many arrived (`0` at end of stream).
bind _MessageReader.fill = fn(&self, timeout_ms: int) Result[int]: {
	if(self.eof): func <- value(0)
	let chunk_size = 8192
	let n = unsafe {
		let chunk = alloc_raw[u8](chunk_size)
		let got = if(is_null(self.tls)): net.raw_recv(self.fd, chunk, chunk_size, timeout_ms)
		else: sys_tls_recv(self.tls, chunk, chunk_size, timeout_ms)
		if(got > 0): self.buf.push_bytes(chunk, got)
		free_raw(chunk)
		got
	}
	if(n == -2): func <- err(Error.coded("timeout", "timeout waiting for HTTP data"))
	if(n < 0): func <- err(Error.new("connection error while reading HTTP data"))
	if(n == 0): self.eof = true
	value(n)
}

// Reports whether a `Transfer-Encoding` value ends with the `chunked` coding.
bind _is_chunked = fn(te: string) bool: {
	let codings = te.split(",")
	if(codings.is_empty()): func <- false
	codings[codings.len() - 1].trim().to_lower() == "chunked"
}

// The outcome of scanning a chunked body: the decoded body, the number of bytes it occupied
// (including the final chunk and trailers), and its trailer fields.
define _ChunkScan result [
	value: +[string, int, [HeaderField]],
	incomplete,
	malformed: string,
]

// Returns the index of the next `\n` at or after `from`, or `none`.
bind _line_end = fn(data: string, from: int) ?int: {
	for(i in from..data.len()): if(data.byte_at(i) == 10): func <- i
	none
}

// Decodes a chunked body that starts at the beginning of `data`.
bind _scan_chunked = fn(data: string) _ChunkScan: {
	let body = ""
	let pos = 0
	while(true): {
		let eol = _line_end(data, pos) ?? func <- incomplete
		let size_line = data[pos..eol].trim()
		let hex = if(size_line.index_of(";") as semi): size_line[..semi].trim() else: size_line.copy()
		let size = parse_int_radix(hex, 16) ?? func <- malformed("invalid chunk size '$hex'")
		if(size < 0): func <- malformed("negative chunk size")
		pos = eol + 1
		if(size == 0): {
			let trailers: [HeaderField] = []
			while(true): {
				let tl_end = _line_end(data, pos) ?? func <- incomplete
				let line = data[pos..tl_end].trim()
				pos = tl_end + 1
				if(line.is_empty()): while <-
				if(line.index_of(":") as c): trailers.push(HeaderField[name: line[..c].trim(), value: line[c + 1..].trim()])
			}
			func <- value(+[own body, pos, own trailers])
		}
		if(data.len() < pos + size + 1): func <- incomplete
		body.push(data[pos..pos + size])
		pos += size
		if(data.byte_at(pos) == 13): {
			if(data.len() < pos + 2): func <- incomplete
			if(data.byte_at(pos + 1) != 10): func <- malformed("missing line break after chunk data")
			pos += 2
		} else if(data.byte_at(pos) == 10): pos += 1
		else: func <- malformed("missing line break after chunk data")
	}
	incomplete
}

// Returns the status code from a response status line, or `0`.
bind _status_of = fn(status_line: string) int: {
	let parts = status_line.trim().split(" ")
	if(parts.len() < 2): func <- 0
	parse_int(parts[1]) ?? 0
}

// Reads one complete message and returns it re-framed with a `Content-Length` body.
//
// `request_method` is empty when reading a request, and the method of the request being
// answered when reading a response. Chunked bodies are decoded (trailers become header
// fields), and the rest of the buffer is kept for the next message.
//
// # Errors
// A `"eof"` error when the stream ends before a message starts, `"timeout"` when no data
// arrives in time, and `"malformed"` for invalid framing.
bind _MessageReader.read_message = fn(&self, request_method: string, timeout_ms: int) Result[string]: {
	// Skip blank lines that may precede a request line.
	while(self.buf.starts_with("\r\n") or self.buf.starts_with("\n")):
		self.buf = if(self.buf.starts_with("\r\n")): self.buf[2..].copy() else: self.buf[1..].copy()

	// 1. The header block.
	let head_end = -1
	let sep_len = 0
	while(head_end < 0): {
		let crlf = self.buf.index_of("\r\n\r\n") ?? -1
		let lf = self.buf.index_of("\n\n") ?? -1
		if(crlf >= 0 and (lf < 0 or crlf <= lf)): {
			head_end = crlf
			sep_len = 4
			while <-
		}
		if(lf >= 0): {
			head_end = lf
			sep_len = 2
			while <-
		}
		let n = self.fill(timeout_ms): default err(own e): func <- err(e)
		if(n == 0): {
			if(self.buf.trim().is_empty()): func <- err(Error.coded("eof", "connection closed before an HTTP message"))
			func <- err(Error.coded("malformed", "connection closed in the middle of HTTP headers"))
		}
	}
	let lines = self.buf[..head_end].split("\n")
	let start_line = lines[0].trim()
	let fields = _parse_header_lines(lines, 1): default err(own e): func <- err(e)
	let body_start = head_end + sep_len

	// 2. Decide how the body is framed.
	let is_response = not request_method.is_empty()
	let status = if(is_response): _status_of(start_line) else: 0
	let no_body = is_response and (request_method.to_upper() == "HEAD" or (status >= 100 and status < 200) or status == 204 or status == 304)
	let chunked = _is_chunked(fields.get("transfer-encoding") ?? "")
	let body = ""
	let consumed = body_start
	if(no_body): {}
	else if(chunked): {
		while(true): {
			let decoded = _scan_chunked(self.buf[body_start..]):
				when incomplete: {
					let n = self.fill(timeout_ms): default err(own e): func <- err(e)
					if(n == 0): func <- err(Error.coded("malformed", "connection closed in the middle of a chunked body"))
					next <-
				}
				default malformed(why): func <- err(Error.coded("malformed", "malformed chunked body: $why"))
			let text, used, trailers = unpack decoded
			body = own text
			consumed = body_start + used
			fields.remove("transfer-encoding")
			for(f in trailers): fields.add(f.name, f.value)
			fields.set("Content-Length", "${body.len()}")
			while <-
		}
	} else if(fields.get("content-length") as cl_text): {
		// Repeated or comma-separated lengths must agree.
		let length = -1
		for(part in cl_text.split(",")): {
			let v = parse_int(part.trim()) ?? func <- err(Error.coded("malformed", "invalid Content-Length '$cl_text'"))
			if(v < 0 or (length >= 0 and v != length)): func <- err(Error.coded("malformed", "invalid Content-Length '$cl_text'"))
			length = v
		}
		for(other in fields.values("content-length")):
			if(parse_int(other.trim()) != length): func <- err(Error.coded("malformed", "conflicting Content-Length fields"))
		while(self.buf.len() < body_start + length): {
			let n = self.fill(timeout_ms): default err(own e): func <- err(e)
			if(n == 0): func <- err(Error.coded("malformed", "connection closed before the body was complete"))
		}
		body = self.buf[body_start..body_start + length].copy()
		consumed = body_start + length
	} else if(is_response): {
		while(not self.eof): {
			let _ = self.fill(timeout_ms): default err(own e): func <- err(e)
		}
		body = self.buf[body_start..].copy()
		consumed = self.buf.len()
		fields.set("Content-Length", "${body.len()}")
	}

	// 3. Re-frame the message and keep any pipelined bytes.
	let msg = "$start_line\r\n"
	_append_header_fields(&msg, fields)
	msg.push("\r\n")
	msg.push(body)
	self.buf = self.buf[consumed..].copy()
	value(own msg)
}

// Reads one HTTP message from `fd` (compatibility wrapper around [`_MessageReader`]).
bind _read_http_message = fn(fd: int, timeout_ms: int) Result[string]: {
	let r = _MessageReader.plain(fd)
	r.read_message("", timeout_ms)
}

// _TlsSession wraps an active TLS session and ensures proper cleanup via Drop.
define _TlsSession struct [
	_raw: ptr[u8]
]

bind _TlsSession.drop = fn(&self):
	unsafe {
		if(self._raw != null_ptr[u8]()): {
			sys_tls_close(self._raw)
			self._raw = null_ptr[u8]()
		}
	}

bind _TlsSession.close = fn(&self):
	unsafe {
		if(self._raw != null_ptr[u8]()): {
			sys_tls_close(self._raw)
			self._raw = null_ptr[u8]()
		}
	}

bind _TlsSession.send = fn(&self, data: string) Result[int]: {
	let is_null = unsafe (self._raw == null_ptr[u8]())
	if(is_null): func <- err(Error.new("send on closed TLS session"))
	let res = unsafe sys_tls_send_all(self._raw, data.c_str(), data.len())
	if(res == 0): value(data.len()) else: err(Error.new("failed to send data over TLS"))
}

// ============================================================================
// 9. HTTP Client
// ============================================================================

// Configurable HTTP/HTTPS client with default headers, a request timeout, and redirects.
define Client struct [
	// Socket connect, write, and response-read timeout in milliseconds (default `15000`).
	timeout_ms: int,
	// Default headers attached to outgoing requests when not already present on the [`Request`].
	headers: Headers,
	// How many redirects [`Client.do`] follows before failing; `0` returns redirects as is.
	max_redirects: int = 10,
]

// Creates a [`Client`] with a `15000` ms timeout, no default headers, and up to 10 redirects.
bind Client.new = fn() Client: Client[
	timeout_ms: 15000,
	headers: Headers.new(),
	max_redirects: 10,
]

// The result of an HTTP request ([`Client.do`], [`get`], [`post`], ...). A response with an
// error status such as `404` or `500` is still a `value`: check [`Response.status_code`].
//
// ```lowlight
// let resp = http.get(url):
// 	when unreachable(_), timeout(_): func <- retry_later()
// 	default err(own e): func <- err(e)
// ```
@must_use
define RequestResult result [
	// The server's response.
	value: Response,
	// The server couldn't be reached: its host name didn't resolve, or it refused the connection.
	unreachable: Error,
	// The server didn't connect or answer within the client's timeout.
	timeout: Error,
	// Anything else: an invalid URL, a TLS failure, a failed send, a malformed response, or too
	// many redirects (code `"too_many_redirects"`).
	err: Error,
]

// Executes `req` over HTTP or HTTPS (`https://` or port `443`) and returns the parsed [`Response`].
//
// Populates `Host`, `User-Agent`, `Accept`, `Connection: close`, `Content-Length` (when the body
// is non-empty), and any client-level default headers if not already set on `req`.
//
// Follows `301`, `302`, `303`, `307`, and `308` redirects up to [`Client.max_redirects`]: `303`
// (and `301`/`302` after a `POST`) continue with a bodiless `GET`, while `307` and `308` repeat the
// method and body. `Authorization` and `Cookie` headers are not sent to a different host.
//
// # Errors
// [`RequestResult.unreachable`] if the host doesn't resolve or refuses the connection;
// [`RequestResult.timeout`] if connecting or reading the response takes longer than
// [`Client.timeout_ms`]; [`RequestResult.err`] if `req.url` can't be parsed, TLS isn't available
// or the handshake fails (HTTPS loads OpenSSL `libssl` dynamically), sending fails, the
// response is malformed, or more than `max_redirects` redirects occur (code
// `"too_many_redirects"`).
bind Client.do = fn(self, req: Request) RequestResult: self.do_with(req, _client_round_trip)

// Like [`Client.do`], but sends each request (the original and every redirect) through
// `transport` instead of the network. Tests use it to exercise redirect handling.
bind Client.do_with = fn(self, req: Request, transport: cl(Client, Request) RequestResult) RequestResult: {
	let cur = req.copy()
	let hops = 0
	while(true): {
		let resp = transport(self, cur):
			when unreachable(own e): func <- unreachable(e)
			when timeout(own e): func <- timeout(e)
			default err(own e): func <- err(e)
		if(not is_redirect(resp.status_code) or self.max_redirects <= 0): func <- value(resp)
		let location = resp.header("location") ?? func <- value(resp)
		if(hops >= self.max_redirects): func <- err(
			Error.coded("too_many_redirects", "stopped after ${self.max_redirects} redirects")
		)
		let base = parse_url(cur.url): default err(own e): func <- err(e.wrap("invalid request URL: ${cur.url}"))
		let next_url = resolve_reference(base, location)
		let next_base = parse_url(next_url): default err(own e): func <- err(e.wrap("invalid redirect target: $location"))
		let code = resp.status_code
		let method = if(code == 303 and cur.method != "HEAD"): "GET"
		else if((code == 301 or code == 302) and cur.method == "POST"): "GET"
		else: cur.method.copy()
		let keep_body = (code == 307 or code == 308)
		let next = Request.new(method, next_url, if(keep_body): cur.body.copy() else: "")
		let same_host = header_name_eq(base.host, next_base.host)
		for(f in cur.headers.fields()): {
			if(header_name_eq(f.name, "host") or header_name_eq(f.name, "content-length")): next <-
			if(not same_host and (header_name_eq(f.name, "authorization") or header_name_eq(f.name, "cookie"))): next <-
			if(not keep_body and header_name_eq(f.name, "content-type")): next <-
			next.add_header(f.name, f.value)
		}
		cur = own next
		hops += 1
	}
	err(Error.new("redirect loop ended unexpectedly"))
}

// Reports whether `code` is a redirect that [`Client.do`] follows.
bind is_redirect = fn(code: int) bool: code == 301 or code == 302 or code == 303 or code == 307 or code == 308

// Resolves a `Location` value (or any URL reference) against `base`, as RFC 3986 §5.2 describes:
// absolute URLs are returned unchanged, and `//host/path`, `/path`, `?query`, and relative
// paths are combined with the base's scheme, host, and directory.
bind resolve_reference = fn(base: Url, reference: string) string: {
	if(reference.index_of("://") as i): if(not reference[..i].contains("/")): func <- reference.copy()
	let origin = if(base.scheme.is_empty()): "" else: "${base.scheme}://${base.host}"
	if(reference.starts_with("//")): func <- (if(base.scheme.is_empty()): "http" else: base.scheme.copy()) + ":" + reference
	let base_path = if(base.raw_path.is_empty()): base.path.copy() else: base.raw_path.copy()
	if(reference.starts_with("/")): func <- origin + _remove_dot_segments(reference)
	if(reference.is_empty() or reference.starts_with("#")): func <- origin + base_path + (if(base.raw_query.is_empty()): "" else: "?${base.raw_query}") + reference
	if(reference.starts_with("?")): func <- origin + base_path + reference
	let dir = base_path[..(_last_slash(base_path) + 1)]
	origin + _remove_dot_segments(dir + reference)
}

bind _last_slash = fn(s: string) int: {
	let at = -1
	for(i in 0..s.len()): if(s.byte_at(i) == 47): at = i
	at
}

// Removes `.` and `..` segments from an absolute path (RFC 3986 §5.2.4), leaving any query or
// fragment untouched.
bind _remove_dot_segments = fn(path: string) string: {
	let cut = path.len()
	for(i in 0..path.len()): {
		let b = path.byte_at(i)
		if(b == 63 or b == 35): {
			cut = i
			for <-
		}
	}
	let segs = path[..cut].split("/")
	let out: [string] = []
	for(i, seg in segs): {
		let last = i == segs.len() - 1
		if(seg == "." or seg == ".."): {
			if(seg == ".." and out.len() > 1): {
				let _? = out.pop()
			}
			if(last): out.push("")
			next <-
		}
		out.push(seg.copy())
	}
	let result = join_strings(out, "/")
	if(not result.starts_with("/")): result = "/" + result
	result + path[cut..]
}

// Sends one request over the network without following redirects.
bind _client_round_trip = fn(client: Client, req: Request) RequestResult: {
	let u = parse_url(req.url):
		default err(own e): func <- err(e.wrap("invalid request URL: ${req.url}"))
	let is_tls = u.scheme == "https" or u.port == 443
	let host = if(not u.hostname.is_empty()): u.hostname.copy() else: "127.0.0.1"
	let port = if(u.port > 0): u.port
	else if(is_tls): 443
	else: 80

	let mut_req = req.copy()
	if(mut_req.header("host") == none): {
		let shown = if(host.contains(":")): "[$host]" else: host.copy()
		mut_req.set_header("Host", if(port == default_port(u.scheme)): shown else: "$shown:$port")
	}
	if(mut_req.header("user-agent") == none): mut_req.set_header("User-Agent", "Lowlight-HTTP/1.0")
	if(mut_req.header("accept") == none): mut_req.set_header("Accept", "*/*")
	if(mut_req.header("connection") == none): mut_req.set_header("Connection", "close")
	if(not mut_req.body.is_empty() and mut_req.header("content-length") == none): mut_req.set_header(
		"Content-Length",
		"${mut_req.body.len()}",
	)
	for(f in client.headers.fields()): if(not mut_req.headers.has(f.name)): mut_req.add_header(f.name, f.value)

	let wire = mut_req.format()
	let stream = net.TcpStream.connect(host, port, client.timeout_ms):
		when not_found(own e): func <- unreachable(e.wrap("connection failed"))
		when refused(own e): func <- unreachable(e.wrap("connection failed"))
		when timeout(own e): func <- timeout(e.wrap("connection failed"))
		default err(own e): func <- err(e.wrap("connection failed"))

	if(is_tls): {
		let avail = unsafe sys_tls_available()
		if(avail == 0): func <- err(
			Error.new(
				"HTTPS requires libssl (OpenSSL 1.1 / 3.0), which could not be loaded dynamically"
			)
		)
		let raw_sess = unsafe sys_tls_client_handshake(stream.raw_fd(), host.c_str())
		let is_null = unsafe (raw_sess == null_ptr[u8]())
		if(is_null): func <- err(
			Error.new("TLS handshake failed connecting to $host:$port")
		)
		let tls_sess = _TlsSession[_raw: raw_sess]

		tls_sess.send(wire):
			default err(own e): func <- err(e.wrap("failed to send HTTP request over TLS"))

		let reader = _MessageReader.over_tls(tls_sess._raw)
		let raw_resp = reader.read_message(mut_req.method, client.timeout_ms):
			default err(own e): func <- _read_failure(own e, "failed to read HTTP response over TLS")
		let resp = Response.parse(raw_resp):
			default err(own e): func <- err(e.wrap("failed to parse HTTP response"))
		value(own resp)
	} else: {
		stream.send(wire):
			default err(own e): func <- err(e.wrap("failed to send HTTP request"))

		let reader = _MessageReader.plain(stream.raw_fd())
		let raw_resp = reader.read_message(mut_req.method, client.timeout_ms):
			default err(own e): func <- _read_failure(own e, "failed to read HTTP response")
		let resp = Response.parse(raw_resp):
			default err(own e): func <- err(e.wrap("failed to parse HTTP response"))
		value(own resp)
	}
}

// A failure reading a response: `timeout` when the reader timed out, otherwise `err`.
bind _read_failure = fn(e: own Error, context: string) RequestResult:
	if(e.has_code("timeout")): timeout(e.wrap(context)) else: err(e.wrap(context))
bind Client._send_method = fn(self, method: string, url: string, content_type?: ?string, body: string) RequestResult: {
	let req = Request.new(method, url, body)
	if(content_type? as ct): req.set_header("Content-Type", ct)
	self.do(req)
}

// Sends an HTTP `GET` request to `url` using this client's configuration.
bind Client.get = fn(self, url: string) RequestResult: self._send_method("GET", url, none, "")

// Sends an HTTP `POST` request with `content_type` and `body` to `url`.
bind Client.post = fn(self, url: string, content_type: string, body: string) RequestResult: self._send_method(
	"POST",
	url,
	content_type,
	body,
)

// Sends an HTTP `PUT` request with `content_type` and `body` to `url`.
bind Client.put = fn(self, url: string, content_type: string, body: string) RequestResult: self._send_method(
	"PUT",
	url,
	content_type,
	body,
)

// Sends an HTTP `PATCH` request with `content_type` and `body` to `url`.
bind Client.patch = fn(self, url: string, content_type: string, body: string) RequestResult: self._send_method(
	"PATCH",
	url,
	content_type,
	body,
)

// Sends an HTTP `DELETE` request to `url` using this client's configuration.
bind Client.delete = fn(self, url: string) RequestResult: self._send_method(
	"DELETE",
	url,
	none,
	"",
)

// Sends a one-off HTTP `GET` request to `url` with default [`Client`] settings.
bind get = fn(url: string) RequestResult: {
	let c = Client.new()
	c.get(url)
}

// Sends a one-off HTTP `POST` request with `content_type` and `body` to `url`.
bind post = fn(url: string, content_type: string, body: string) RequestResult: {
	let c = Client.new()
	c.post(url, content_type, body)
}

// Sends a one-off HTTP `PUT` request with `content_type` and `body` to `url`.
bind put = fn(url: string, content_type: string, body: string) RequestResult: {
	let c = Client.new()
	c.put(url, content_type, body)
}

// Sends a one-off HTTP `PATCH` request with `content_type` and `body` to `url`.
bind patch = fn(url: string, content_type: string, body: string) RequestResult: {
	let c = Client.new()
	c.patch(url, content_type, body)
}

// Sends a one-off HTTP `DELETE` request to `url` with default [`Client`] settings.
bind delete = fn(url: string) RequestResult: {
	let c = Client.new()
	c.delete(url)
}

// ============================================================================
// 10. HTTP Server
// ============================================================================

// Synchronous HTTP/1.1 TCP server binding to an address and dispatching requests.
//
// Owns the underlying listener socket descriptor and closes it automatically via
// [`Server.drop`] when dropped.
define Server struct [
	// Bind specification such as `"127.0.0.1:8080"`, `"8080"`, or `"0.0.0.0:0"`.
	addr: string,
	// Actual bound TCP port populated after [`Server.listen`] succeeds.
	port: int,
	// Underlying OS listener file descriptor (`-1` when closed or unbound).
	listener_fd: int,
	// Per-connection request read timeout in milliseconds (default `10000`).
	read_timeout_ms: int,
	// Per-connection response write timeout in milliseconds (default `10000`).
	write_timeout_ms: int,
]

// Creates a [`Server`] configured to listen on `addr` with `10000` ms read/write timeouts.
bind Server.new = fn(addr: string) Server: Server[
	addr: addr.copy(),
	port: 0,
	listener_fd: -1,
	read_timeout_ms: 10000,
	write_timeout_ms: 10000,
]

// Closes the underlying listener socket when the [`Server`] goes out of scope.
bind Server.drop = fn(&self): self.close()

// Closes the listener socket if currently open and resets `listener_fd` to `-1`.
bind Server.close = fn(&self):
	if(self.listener_fd >= 0): {
		net.raw_close(self.listener_fd)
		self.listener_fd = -1
	}

// The result of [`Server.listen`].
@must_use
define ListenResult result [
	// The port the server is listening on.
	value: int,
	// Something else already listens on the address.
	in_use: Error,
	// Any other failure, with the OS's description.
	err: Error,
]

// The result of [`Server.serve_one`].
@must_use
define ServeOneResult result [
	// Whether a request was handled on the connection.
	value: bool,
	// No client connected within the timeout.
	timeout: Error,
	// Listening or accepting failed, with the OS's description.
	err: Error,
]

// Binds the TCP listener socket according to `self.addr` and returns the bound port.
//
// Supports `"host:port"`, `"port"`, or `"host"` strings, defaulting omitted host to
// `"0.0.0.0"` and omitted port to `8080`. Passing port `0` selects an ephemeral OS port
// stored in `self.port`.
//
// # Errors
// [`ListenResult.in_use`] if something else already listens on the address;
// [`ListenResult.err`] if the address is invalid or binding fails for another reason, with the
// OS's description.
bind Server.listen = fn(&self) ListenResult: {
	let host = "0.0.0.0"
	let port = 8080

	if(self.addr.starts_with("[")): {
		// An IPv6 address: `[addr]` or `[addr]:port`.
		let close = self.addr.index_of("]") ?? func <- err(Error.new("invalid server address ${self.addr}"))
		host = self.addr[1..close]
		let rest = self.addr[close + 1..]
		if(rest.starts_with(":")): port = parse_int(rest[1..]) ?? 8080
	} else if(not self.addr.is_empty()): {
		let colon? = self.addr.index_of(":")
		if(colon?): {
			let h = self.addr[0..colon].trim()
			if(not h.is_empty()): host = h
			let p_str = self.addr[colon + 1..self.addr.len()].trim()
			let p? = parse_int(p_str)
			if(p? != none): port = p? ?? 8080
		} else: {
			let p? = parse_int(self.addr)
			if(p? != none): port = p? ?? 8080 else: host = self.addr.copy()
		}
	}

	let sfd = net.raw_tcp_listen(host, port, 128)
	if(sfd < 0): {
		let code = os.last_errno()
		let e = os.error_from_errno(code, "failed to listen on ${self.addr}")
		func <- if(os.errno_name(code) == "EADDRINUSE"): in_use(e) else: err(e)
	}
	self.listener_fd = sfd
	let actual_port = net.raw_tcp_port(sfd)
	self.port = if(actual_port > 0): actual_port else: port
	value(self.port)
}

// Formats the remote address of a connected socket, or returns an empty string when unknown.
bind _peer_addr = fn(fd: int) string: net.peer_addr_of(fd) ?? ""

// Serves the HTTP requests on connection `conn_fd`, then closes it.
//
// Requests the client has already pipelined on the connection are answered in order; the
// connection closes once no further request is waiting, or when the client asks for it with
// `Connection: close`. Responses to `HEAD` carry headers only. A malformed request gets a
// `400 Bad Request`. The handler sees the client's address in [`Request.remote_addr`].
//
// Returns `true` if at least one request was handled.
bind Server.serve_conn = fn(&self, conn_fd: int, handler: cl(Request, &ResponseWriter)) bool: {
	let reader = _MessageReader.plain(conn_fd)
	let remote = _peer_addr(conn_fd)
	let served = false
	while(true): {
		let raw_req = reader.read_message("", self.read_timeout_ms):
			default err(own e): {
				if(e.has_code("malformed")): {
					let bad = "HTTP/1.1 400 Bad Request\r\nContent-Length: 15\r\nConnection: close\r\n\r\n400 Bad Request"
					let _ = unsafe net.raw_send_all(conn_fd, bad.c_str(), bad.len())
				}
				while <-
			}
		let req = Request.parse(raw_req):
			default err(own e): {
				let bad = "HTTP/1.1 400 Bad Request\r\nContent-Length: 15\r\nConnection: close\r\n\r\n400 Bad Request"
				let _ = unsafe net.raw_send_all(conn_fd, bad.c_str(), bad.len())
				while <-
			}
		req.remote_addr = remote.copy()
		let rw = ResponseWriter.new()
		handler(req, &rw)
		let resp = rw.finish()
		if(req.method == "HEAD"): resp.body = ""

		let client_closes = (req.header("connection") ?? "").to_lower().contains("close") or req.proto == "HTTP/1.0"
		let close = client_closes or reader.buf.trim().is_empty()
		if(close): resp.set_header("Connection", "close")
		let wire = resp.format()
		let sent = unsafe net.raw_send_all(conn_fd, wire.c_str(), wire.len())
		served = true
		if(close or sent != 0): while <-
	}
	net.raw_close(conn_fd)
	served
}

// Accepts and serves a single connection, waiting up to `timeout_ms` milliseconds.
//
// Calls [`Server.listen`] automatically if the server is not already listening.
//
// # Errors
// [`ServeOneResult.timeout`] when `timeout_ms` expires before a client connects;
// [`ServeOneResult.err`] if listening or accepting fails.
bind Server.serve_one = fn(&self, handler: cl(Request, &ResponseWriter), timeout_ms: int) ServeOneResult: {
	if(self.listener_fd < 0): self.listen():
		when in_use(own e): func <- err(e)
		default err(own e): func <- err(e)
	let client_fd = net.raw_tcp_accept(self.listener_fd, timeout_ms)
	if(client_fd < 0): {
		if(client_fd == -2): func <- timeout(Error.new("no connection within $timeout_ms ms"))
		func <- err(os.error_from_errno(os.last_errno(), "failed to accept a connection"))
	}
	let ok = self.serve_conn(client_fd, handler)
	value(ok)
}

// Runs the accept loop, handling connections sequentially with `handler`.
//
// Stops once `max_requests` valid requests have been served, or runs indefinitely when
// `max_requests <= 0`. Returns the total number of valid requests served.
//
// A failed `accept` (for example, when the process is out of file descriptors) is retried
// after a delay that starts at 5 ms and doubles up to 1 s, instead of spinning; the delay
// resets after the next successful accept.
//
// # Errors
// Returns an error if listening fails, or a `"closed"` error if the listener is closed.
bind Server.serve = fn(&self, handler: cl(Request, &ResponseWriter), max_requests: int) Result[int]: {
	if(self.listener_fd < 0): self.listen():
		when in_use(own e): func <- err(e)
		default err(own e): func <- err(e)
	let served = 0
	let backoff_ms = 0
	while(max_requests <= 0 or served < max_requests): {
		if(self.listener_fd < 0): func <- err(Error.coded("closed", "server listener is closed"))
		let client_fd = net.raw_tcp_accept(self.listener_fd, -1)
		if(client_fd < 0): {
			backoff_ms = _next_accept_backoff(backoff_ms)
			time.sleep_ms(backoff_ms)
			next <-
		}
		backoff_ms = 0
		if(self.serve_conn(client_fd, handler)): served += 1
	}
	value(served)
}

// Returns the next accept-retry delay in milliseconds: 5 ms after the first failure, then
// doubling, capped at 1 s.
bind _next_accept_backoff = fn(prev_ms: int) int:
	if(prev_ms <= 0): 5 else: min(prev_ms * 2, 1000)

// Starts a [`Server`] bound to `addr` and serves requests indefinitely using `handler`.
bind listen_and_serve = fn(addr: string, handler: cl(Request, &ResponseWriter)) Result[bool]: {
	let s = Server.new(addr)
	s.serve(handler, 0):
		default err(own e): func <- err(e.wrap("server error"))
	value(true)
}

// Starts a [`Server`] bound to `addr` and dispatches requests through `mux`.
bind listen_and_serve_mux = fn(addr: string, mux: ServeMux) Result[bool]: {
	let mux_copy = mux.copy()
	listen_and_serve(
		addr,
		cl(req: Request, res: &ResponseWriter): mux_copy.serve_http(req, &res),
	)
}

// ============================================================================
// 11. Testing Utilities (ResponseRecorder & Mock Transport)
// ============================================================================

// In-memory response recorder wrapping a [`ResponseWriter`] for handler unit tests.
define ResponseRecorder struct [
	// Underlying [`ResponseWriter`] capturing status, headers, and body writes.
	writer: ResponseWriter
]

// Creates a fresh [`ResponseRecorder`].
bind ResponseRecorder.new = fn() ResponseRecorder: ResponseRecorder[writer: ResponseWriter.new()]

// Returns the recorded HTTP response by finalizing [`ResponseRecorder.writer`].
bind ResponseRecorder.result = fn(self) Response: self.writer.finish()

// Executes `handler` directly against `req` in memory and returns the resulting [`Response`].
bind test_handler = fn(handler: cl(Request, &ResponseWriter), req: Request) Response: {
	let rw = ResponseWriter.new()
	handler(req, &rw)
	rw.finish()
}

// Simulates an HTTP wire roundtrip over a kernel socketpair without opening TCP ports.
//
// Serializes `req` with [`Request.format`], reads and parses it on the server end, invokes
// `handler`, serializes the reply, and parses the client-side [`Response`].
bind test_roundtrip = fn(handler: cl(Request, &ResponseWriter), req: Request) Result[Response]: {
	let pair = net.socketpair():
		default err(own e): func <- err(e.wrap("socketpair failed"))

	pair.first.send(req.format()):
		default err(own e): func <- err(e.wrap("failed to send request to socketpair"))

	let srv_reader = _MessageReader.plain(pair.second.raw_fd())
	let srv_wire = srv_reader.read_message("", 2000):
		default err(own e): func <- err(e.wrap("server failed to read request"))
	let srv_req = Request.parse(srv_wire):
		default err(own e): func <- err(e.wrap("server failed to parse request"))

	let rw = ResponseWriter.new()
	handler(srv_req, &rw)
	let resp = rw.finish()
	let resp_wire = resp.format()

	pair.second.send(resp_wire):
		default err(own e): func <- err(e.wrap("server failed to send response"))

	let client_reader = _MessageReader.plain(pair.first.raw_fd())
	let client_wire = client_reader.read_message(req.method, 2000):
		default err(own e): func <- err(e.wrap("client failed to read response"))
	Response.parse(client_wire)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("http status codes and standard RFC status text")
bind test_http_status_codes_and_texts = fn(): {
	assert(status_text(STATUS_OK) == "OK", "200 is OK")
	assert(status_text(STATUS_CREATED) == "Created", "201 is Created")
	assert(status_text(STATUS_BAD_REQUEST) == "Bad Request", "400 is Bad Request")
	assert(status_text(STATUS_NOT_FOUND) == "Not Found", "404 is Not Found")
	assert(
		status_text(STATUS_INTERNAL_SERVER_ERROR) == "Internal Server Error",
		"500 is Internal Server Error",
	)
}

@test("http URL parsing: scheme, hostname, port, path, query parameters")
bind test_http_url_parsing = fn(): {
	let u_res! = parse_url("http://api.example.com:8080/v1/users?page=2&limit=50")
	assert(u_res!.is_ok(), "parse_url succeeds")
	let u = u_res!.unwrap()

	assert(u.scheme == "http", "scheme is http")
	assert(u.hostname == "api.example.com", "hostname matches")
	assert(u.port == 8080, "port is 8080")
	assert(u.path == "/v1/users", "path matches")

	let p? = u.query_param("page")
	assert(p? == "2", "query param page is 2")
	let lim? = u.query_param("limit")
	assert(lim? == "50", "query param limit is 50")
	let missing? = u.query_param("nonexistent")
	assert(missing? == none, "missing query param is none")
}

@test("http Request and Response serialization and wire parsing")
bind test_http_request_response_wire = fn(): {
	let req = Request.new("POST", "/submit", "sample payload")
	let wire = req.format()

	let parsed_req_res! = Request.parse(wire)
	assert(parsed_req_res!.is_ok(), "Request.parse succeeds")
	let pr = parsed_req_res!.unwrap()
	assert(pr.method == "POST", "parsed method is POST")
	assert(pr.path == "/submit", "parsed path is /submit")
	assert(pr.body == "sample payload", "parsed body matches")

	let resp = Response.new(200, "{\"ok\":true}")
	assert(resp.is_success(), "200 is success")
	let resp_wire = resp.format()

	let parsed_resp_res! = Response.parse(resp_wire)
	assert(parsed_resp_res!.is_ok(), "Response.parse succeeds")
	let pres = parsed_resp_res!.unwrap()
	assert(pres.status_code == 200, "parsed status_code is 200")
	assert(pres.body == "{\"ok\":true}", "parsed response body matches")
}

@test("http ServeMux routing and ResponseRecorder in-memory handling")
bind test_http_serve_mux_and_handler = fn(): {
	let mux = ServeMux.new()
	mux.handle_func(
		"/hello",
		fn(req: Request, rw: &ResponseWriter): {
			rw.write_header(STATUS_OK)
			rw.write("hello from mux")
		},
	)
	mux.handle_func(
		"/greet",
		fn(req: Request, rw: &ResponseWriter): {
			rw.write_header(STATUS_CREATED)
			rw.write("greetings")
		},
	)

	let req1 = Request.new("GET", "/hello", "")
	let rec1 = ResponseRecorder.new()
	mux.serve_http(req1, &rec1.writer)
	let resp1 = rec1.result()
	assert(resp1.status_code == 200, "status 200 for /hello")
	assert(resp1.body == "hello from mux", "body matches for /hello")

	let req2 = Request.new("GET", "/greet", "")
	let rec2 = ResponseRecorder.new()
	mux.serve_http(req2, &rec2.writer)
	let resp2 = rec2.result()
	assert(resp2.status_code == 201, "status 201 for /greet")
	assert(resp2.body == "greetings", "body matches for /greet")

	let req3 = Request.new("GET", "/nonexistent", "")
	let rec3 = ResponseRecorder.new()
	mux.serve_http(req3, &rec3.writer)
	let resp3 = rec3.result()
	assert(resp3.status_code == 404, "status 404 for unknown route")
}

@test("http client-server roundtrip over socketpair")
bind test_http_socketpair_roundtrip = fn(): {
	let handler = fn(req: Request, rw: &ResponseWriter): {
		rw.write_header(STATUS_OK)
		rw.write("echo: ${req.body}")
	}

	let req = Request.new("POST", "/echo", "hello world")
	let resp_res! = test_roundtrip(handler, req)
	assert(resp_res!.is_ok(), "test_roundtrip succeeds")
	let resp = resp_res!.unwrap()
	assert(resp.status_code == 200, "roundtrip status 200")
	assert(resp.body == "echo: hello world", "roundtrip echo body matches")
}

@test("http Request query serialization and CRLF header-injection guards")
bind test_http_query_and_crlf_guards = fn(): {
	let req = Request.new("GET", "/search?q=hello+world", "")
	req.set_header("X-Safe", "val1\r\nEvil: injected")
	req.set_header("X-Key\r\nBad", "v\n2")
	req.method = "GET\r\nInjected"
	req.proto = "HTTP/1.1\r\nSmuggled"
	let wire = req.format()
	assert(
		wire.starts_with("GETInjected /search?q=hello%20world HTTP/1.1Smuggled\r\n"),
		"query and start line sanitized",
	)
	assert(not wire.contains("\r\nEvil:"), "injected header line prevented in Request.format")
	let parsed = Request.parse(wire).unwrap()
	assert(parsed.header("evil") == none, "Evil header not present")
	assert(
		(parsed.query_param("q") ?? "") == "hello world",
		"query param survives Request.format roundtrip",
	)

	let resp = Response.new(200, "ok")
	resp.status = "OK\r\nInjected-Resp: 1"
	resp.set_header("X-Resp", "a\r\nSet-Cookie: bad=1")
	let rwire = resp.format()
	assert(
		not rwire.contains("\r\nSet-Cookie:"),
		"injected header line prevented in Response.format",
	)
	assert(
		not rwire.contains("\r\nInjected-Resp:"),
		"injected status header prevented in Response.format",
	)
	let rparsed = Response.parse(rwire).unwrap()
	assert(rparsed.header("set-cookie") == none, "Set-Cookie header not present")
}

@test("http URL default ports depend on the scheme")
bind test_http_url_default_ports = fn(): {
	assert(default_port("http") == 80 and default_port("HTTPS") == 443, "http and https defaults")
	assert(default_port("ws") == 80 and default_port("wss") == 443, "ws and wss defaults")
	assert(default_port("gopher") == 0, "unknown schemes have no default")

	let plain_on_443 = parse_url("http://example.com:443/x").unwrap()
	assert(plain_on_443.port == 443, "explicit port kept")
	assert(plain_on_443.to_string() == "http://example.com:443/x", "non-default port printed for http")

	let tls_on_443 = parse_url("https://example.com:443/x").unwrap()
	assert(tls_on_443.to_string() == "https://example.com/x", "default port omitted for https")

	let tls_on_80 = parse_url("https://example.com:80/").unwrap()
	assert(tls_on_80.to_string() == "https://example.com:80/", "port 80 is not the https default")

	let implicit = parse_url("https://example.com").unwrap()
	assert(implicit.port == 443 and implicit.path == "/", "implicit https port and root path")
}

@test("http URL paths decode %XX but keep '+'; queries decode '+' as a space")
bind test_http_url_path_vs_query_plus = fn(): {
	let u = parse_url("http://h/a+b/c%20d/e%2Bf?q=a+b&r=c%2Bd").unwrap()
	assert(u.path == "/a+b/c d/e+f", "path keeps '+' and decodes %XX")
	assert(u.raw_path == "/a+b/c%20d/e%2Bf", "raw path preserved")
	assert(u.query_param("q") == "a b", "query '+' is a space")
	assert(u.query_param("r") == "c+d", "query %2B is a plus")
	assert(u.to_string() == "http://h/a+b/c%20d/e%2Bf?q=a+b&r=c%2Bd", "round-trips with the raw path")
	assert(url_path_decode("x+y%41") == "x+yA", "url_path_decode keeps '+'")
	assert(url_decode("x+y%41") == "x yA", "url_decode turns '+' into a space")

	let rel = parse_url("/files/a+b.txt?download=1").unwrap()
	assert(rel.path == "/files/a+b.txt", "relative target path keeps '+'")
	let req = Request.new("GET", "/files/a%20b.txt", "")
	assert(req.path == "/files/a b.txt", "request path is decoded")
}

@test("http URL fragments and user:password@ information")
bind test_http_url_fragment_and_userinfo = fn(): {
	let u = parse_url("https://ada:s%40cret@example.com:8443/docs/page?x=1#intro?not-query").unwrap()
	assert(u.username == "ada", "username")
	assert(u.password? == "s@cret", "password is percent-decoded")
	assert(u.host == "example.com:8443" and u.hostname == "example.com", "host excludes userinfo")
	assert(u.port == 8443, "explicit port")
	assert(u.path == "/docs/page" and u.raw_query == "x=1", "path and query exclude the fragment")
	assert(u.fragment == "intro?not-query", "fragment is everything after '#'")
	assert(u.to_string() == "https://ada:s%40cret@example.com:8443/docs/page?x=1#intro?not-query", "round-trip")

	let user_only = parse_url("http://bob@h/").unwrap()
	assert(user_only.username == "bob" and user_only.password? == none, "user without password")
	assert(user_only.to_string() == "http://bob@h/", "user without password round-trips")

	let empty_pw = parse_url("http://bob:@h/").unwrap()
	assert(empty_pw.password? == "", "empty password is present, not none")

	let frag_only = parse_url("/guide#install").unwrap()
	assert(frag_only.path == "/guide" and frag_only.fragment == "install", "relative fragment")
	assert(frag_only.to_string() == "/guide#install", "relative fragment round-trips")

	let at_in_path = parse_url("http://h/users/@me").unwrap()
	assert(at_in_path.username == "" and at_in_path.hostname == "h", "'@' in the path is not userinfo")
}

@test("http Server accept retry delay backs off and is capped")
bind test_http_accept_backoff = fn(): {
	assert(_next_accept_backoff(0) == 5, "first retry after 5 ms")
	assert(_next_accept_backoff(5) == 10 and _next_accept_backoff(320) == 640, "doubles")
	assert(_next_accept_backoff(640) == 1000 and _next_accept_backoff(1000) == 1000, "capped at 1 s")
}

@test("http Server fills in Request.remote_addr")
bind test_http_server_remote_addr = fn(): {
	let srv = Server.new("127.0.0.1:0")
	let port = srv.listen() ?? -1
	if(port <= 0): func <-     // no loopback networking available here
	let conn = net.TcpStream.connect("127.0.0.1", port, 2000):
		default: func <-
	let sent = conn.send("GET /who HTTP/1.1\r\nHost: localhost\r\n\r\n") ?? 0
	assert(sent > 0, "request sent")
	let served = srv.serve_one(fn(req: Request, rw: &ResponseWriter): rw.write(req.remote_addr), 2000) ?? false
	assert(served, "one request served")
	let reply = conn.recv(4096, 2000) ?? ""
	let body_at = reply.index_of("\r\n\r\n") ?? -1
	assert(body_at >= 0, "response has a body")
	let body = reply[body_at + 4..]
	assert(body.starts_with("127.0.0.1:"), "remote_addr is the client's loopback address")
	let client_port = parse_int(body["127.0.0.1:".len()..]) ?? 0
	assert(client_port > 0 and client_port != port, "remote_addr carries the client's own port")
}

@test("http Headers: case-insensitive names, repeated fields, set, and remove")
bind test_http_headers_multi_value = fn(): {
	let h = Headers.new()
	h.add("Set-Cookie", "a=1")
	h.add("Content-Type", "text/plain")
	h.add("set-cookie", "b=2")
	assert(h.get("SET-COOKIE") == "a=1", "get returns the first value, any case")
	assert(join_strings(h.values("Set-Cookie"), "|") == "a=1|b=2", "values keeps every field in order")
	assert(h.len() == 3, "repeated names count separately")
	h.set("content-type", "text/html")
	assert(join_strings(h.values("Content-Type"), "|") == "text/html", "set replaces the value")
	assert(h.fields()[1].name == "Content-Type", "set keeps the original name and position")
	assert(h.remove("SET-cookie") == 2 and not h.has("set-cookie"), "remove drops every value")
	assert(header_name_eq("X-Request-Id", "x-request-ID") and not header_name_eq("x-content-length", "content-length"), "exact name comparison")

	let rw = ResponseWriter.new()
	rw.add_header("Set-Cookie", "session=xyz")
	rw.add_header("Set-Cookie", "theme=dark")
	rw.write("ok")
	let wire = rw.finish().format()
	let parsed = Response.parse(wire).unwrap()
	assert(join_strings(parsed.header_values("set-cookie"), "|") == "session=xyz|theme=dark", "both cookies survive the wire")
}

@test("http header parsing rejects invalid names and joins folded lines")
bind test_http_header_parsing_rules = fn(): {
	assert(Request.parse("GET / HTTP/1.1\r\nBad Name: x\r\n\r\n").is_err(), "space inside a name")
	assert(Request.parse("GET / HTTP/1.1\r\nHost : x\r\n\r\n").is_err(), "space before the colon")
	assert(Request.parse("GET / HTTP/1.1\r\nno-colon\r\n\r\n").is_err(), "line without a colon")
	let folded = Request.parse("GET / HTTP/1.1\r\nX-Long: one\r\n two\r\n\r\n").unwrap()
	assert(folded.header("x-long") == "one two", "obsolete folding joins with a space")
}

@test("http message reader frames by exact Content-Length, chunked bodies, and HEAD")
bind test_http_reader_framing = fn(): {
	let pair = net.socketpair().unwrap()
	let wire = "HTTP/1.1 200 OK\r\nX-Content-Length: 99\r\nContent-Length: 3\r\n\r\nabc"
	wire.push("HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n4;ext=1\r\nWiki\r\n5\r\npedia\r\n0\r\nX-Trailer: done\r\n\r\n")
	wire.push("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n")
	wire.push("HTTP/1.1 204 No Content\r\nContent-Length: 7\r\n\r\n")
	let _ = pair.first.send(wire) ?? 0
	let reader = _MessageReader.plain(pair.second.raw_fd())

	let first = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	assert(first.body == "abc", "X-Content-Length does not frame the body")

	let chunked = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	assert(chunked.body == "Wikipedia", "chunked body decoded, extensions ignored")
	assert(chunked.header("x-trailer") == "done", "trailer becomes a header")
	assert(chunked.header("content-length") == "9" and not chunked.headers.has("transfer-encoding"), "re-framed")

	let head = Response.parse(reader.read_message("HEAD", 2000).unwrap()).unwrap()
	assert(head.body.is_empty() and head.header("content-length") == "1000", "HEAD response has no body")

	let no_content = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	assert(no_content.status_code == 204 and no_content.body.is_empty(), "204 has no body")
	assert(reader.buf.is_empty(), "nothing left over")
}

@test("http message reader keeps pipelined requests buffered")
bind test_http_reader_pipelining = fn(): {
	let pair = net.socketpair().unwrap()
	let two = "POST /a HTTP/1.1\r\nContent-Length: 2\r\n\r\nhiGET /b HTTP/1.1\r\nHost: x\r\n\r\n"
	let _ = pair.first.send(two) ?? 0
	let reader = _MessageReader.plain(pair.second.raw_fd())
	let a = Request.parse(reader.read_message("", 2000).unwrap()).unwrap()
	let b = Request.parse(reader.read_message("", 2000).unwrap()).unwrap()
	assert(a.path == "/a" and a.body == "hi", "first request and its body")
	assert(b.path == "/b" and b.body.is_empty(), "second request read from the buffered remainder")

	let parsed = Response.parse("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n").unwrap()
	assert(parsed.body == "abc", "Response.parse decodes chunked bodies too")
}

@test("http requests escape decoded paths on the wire")
bind test_http_request_path_escaping = fn(): {
	let req = Request.new("GET", "/files/a%20b%3F.txt", "")
	assert(req.path == "/files/a b?.txt", "path is decoded")
	assert(req.format().starts_with("GET /files/a%20b%3F.txt HTTP/1.1\r\n"), "request line is escaped")
}

@test("http URL references resolve against a base URL")
bind test_http_resolve_reference = fn(): {
	let base = parse_url("https://example.com:8443/docs/guide/intro?x=1").unwrap()
	assert(resolve_reference(base, "http://other.test/p") == "http://other.test/p", "absolute URL")
	assert(resolve_reference(base, "//cdn.test/a.js") == "https://cdn.test/a.js", "scheme-relative")
	assert(resolve_reference(base, "/login") == "https://example.com:8443/login", "absolute path")
	assert(resolve_reference(base, "setup") == "https://example.com:8443/docs/guide/setup", "relative path")
	assert(resolve_reference(base, "../api/./list") == "https://example.com:8443/docs/api/list", "dot segments")
	assert(resolve_reference(base, "?page=2") == "https://example.com:8443/docs/guide/intro?page=2", "query only")
}

bind _fake_transport = fn(c: Client, r: Request) RequestResult: {
	let resp = match(r.path):
		when "/start": Response.new(302, "")
		when "/second": Response.new(301, "")
		when "/form": Response.new(303, "")
		when "/keep": Response.new(307, "")
		when "/loop": Response.new(302, "")
		default: Response.new(200, "${r.method} ${r.url} body=${r.body} auth=${r.header("authorization") ?? "-"}")
	match(r.path):
		when "/start": resp.set_header("Location", "/second")
		when "/second": resp.set_header("Location", "http://other.test/final")
		when "/form": resp.set_header("Location", "/done")
		when "/keep": resp.set_header("Location", "/kept")
		when "/loop": resp.set_header("Location", "/loop")
		default: {}
	value(resp)
}

@test("http Client follows redirects with the right method, body, and headers")
bind test_http_client_redirects = fn(): {
	let c = Client.new()
	let req = Request.new("GET", "http://example.com/start", "")
	req.set_header("Authorization", "Bearer secret")
	let final = c.do_with(req, _fake_transport): default panic
	assert(final.body == "GET http://other.test/final body= auth=-", "followed two hops; auth dropped across hosts")

	let same_host = Request.new("GET", "http://example.com/keep", "")
	same_host.set_header("Authorization", "Bearer secret")
	let kept_auth = c.do_with(same_host, _fake_transport): default panic
	assert(kept_auth.body.ends_with("auth=Bearer secret"), "auth kept on the same host")

	let post = c.do_with(Request.new("POST", "http://example.com/form", "a=1"), _fake_transport): default panic
	assert(post.body == "GET http://example.com/done body= auth=-", "303 becomes a bodiless GET")

	let keep = c.do_with(Request.new("POST", "http://example.com/keep", "a=1"), _fake_transport): default panic
	assert(keep.body == "POST http://example.com/kept body=a=1 auth=-", "307 repeats the method and body")

	let looped = false
	c.do_with(Request.new("GET", "http://example.com/loop", ""), _fake_transport):
		when err(e): looped = e.has_code("too_many_redirects")
		default panic
	assert(looped, "redirect loops stop")

	let manual = Client.new()
	manual.max_redirects = 0
	let first = manual.do_with(Request.new("GET", "http://example.com/start", ""), _fake_transport): default panic
	assert(first.status_code == 302 and first.header("location") == "/second", "max_redirects = 0 returns the redirect")
}

@test("http IPv6 hosts in URLs and canonical IPv6 formatting")
bind test_http_ipv6 = fn(): {
	let u = parse_url("http://[2001:db8::1]:8080/x?y=1").unwrap()
	assert(u.hostname == "2001:db8::1" and u.port == 8080, "bracketed host and port")
	assert(u.host == "[2001:db8::1]:8080", "host keeps brackets")
	assert(u.to_string() == "http://[2001:db8::1]:8080/x?y=1", "round-trips with brackets")
	let d = parse_url("https://[::1]/").unwrap()
	assert(d.hostname == "::1" and d.port == 443, "default port with an IPv6 host")
	assert(parse_url("http://[::1/").is_err(), "unterminated bracket is an error")
	assert(parse_url("http://[::1]x/").is_err(), "text after the bracket is an error")

	assert(net.format_ipv6([0, 0, 0, 0, 0, 0, 0, 1]) == "::1", "loopback")
	assert(net.format_ipv6([0, 0, 0, 0, 0, 0, 0, 0]) == "::", "unspecified")
	assert(net.format_ipv6([0x2001, 0xdb8, 0, 0, 0, 0, 0, 1]) == "2001:db8::1", "longest zero run")
	assert(net.format_ipv6([0x2001, 0xdb8, 0, 0, 1, 0, 0, 1]) == "2001:db8::1:0:0:1", "first of equal runs")
	assert(net.format_ipv6([1, 0, 2, 3, 4, 5, 6, 7]) == "1:0:2:3:4:5:6:7", "a single zero is not shortened")
	assert(net.format_ipv6([0, 0, 0, 0, 0, 0xffff, 0x7f00, 1]) == "::ffff:127.0.0.1", "IPv4-mapped")
}

@test("http Server answers pipelined requests and HEAD over a real connection")
bind test_http_server_pipelining = fn(): {
	let srv = Server.new("127.0.0.1:0")
	let port = srv.listen() ?? -1
	if(port <= 0): func <-
	let conn = net.TcpStream.connect("127.0.0.1", port, 2000):
		default: func <-
	let reqs = "GET /one HTTP/1.1\r\nHost: x\r\n\r\nHEAD /two HTTP/1.1\r\nHost: x\r\n\r\nGET /three HTTP/1.1\r\nHost: x\r\n\r\n"
	let _ = conn.send(reqs) ?? 0
	let served = srv.serve_one(fn(req: Request, rw: &ResponseWriter): rw.write("path=${req.path}"), 2000) ?? false
	assert(served, "connection served")
	let reader = _MessageReader.plain(conn.raw_fd())
	let r1 = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	let r2 = Response.parse(reader.read_message("HEAD", 2000).unwrap()).unwrap()
	let r3 = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	assert(r1.body == "path=/one", "first pipelined response")
	assert(r2.body.is_empty() and r2.header("content-length") == "9", "HEAD response has headers only")
	assert(r3.body == "path=/three", "third pipelined response")
	assert(r3.header("connection") == "close", "the last response closes the connection")
}

@test("http Server reports IPv6 clients in remote_addr")
bind test_http_server_remote_addr_ipv6 = fn(): {
	let srv = Server.new("[::1]:0")
	let port = srv.listen() ?? -1
	if(port <= 0): func <-     // no IPv6 loopback available here
	let conn = net.TcpStream.connect("::1", port, 2000):
		default: func <-
	let _ = conn.send("GET / HTTP/1.1\r\nHost: x\r\n\r\n") ?? 0
	let _ = srv.serve_one(fn(req: Request, rw: &ResponseWriter): rw.write(req.remote_addr), 2000) ?? false
	let reader = _MessageReader.plain(conn.raw_fd())
	let resp = Response.parse(reader.read_message("GET", 2000).unwrap()).unwrap()
	assert(resp.body.starts_with("[::1]:"), "remote_addr is the bracketed IPv6 loopback")
}
