// std/http — HTTP/1.1 client, server, router, URL parser, and test utilities.
//
// Provides [`URL`], [`Request`], [`Response`], [`ResponseWriter`], [`ServeMux`],
// blocking [`Client`] (HTTP and dynamic OpenSSL HTTPS), [`Server`], and in-memory
// testing helpers ([`ResponseRecorder`], [`test_handler`], [`test_roundtrip`]).

import "std/net"

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
bind status_text = (code: int) string:
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

// Parsed HTTP or HTTPS [`URL`] components including percent-decoded query parameters.
define URL struct [
	// Original unparsed URL string.
	raw: string,
	// URL scheme such as `"http"` or `"https"`, or empty for relative paths.
	scheme: string,
	// Host and optional port string (`"example.com:8080"`).
	host: string,
	// Hostname without port (`"example.com"`).
	hostname: string,
	// Port number (`80` or `443` inferred by scheme if omitted).
	port: int,
	// Normalized path component starting with `"/"`.
	path: string,
	// Raw query string without leading `"?"`.
	raw_query: string,
	// Percent-decoded query parameter key-value map.
	query: #[string: string],
]

// Returns the parsed query parameter value for `key`, or `none` if absent.
bind URL.query_param = (self, key: string) ?string: self.query[key]

// Reconstructs a normalized URL or relative path string from this [`URL`].
bind URL.to_string = (self) string:
	if(self.scheme.is_empty()):
		if(self.raw_query.is_empty()): self.path.copy() else: "${self.path}?${self.raw_query}"
	else: {
		let hp = if(self.port == 80 or self.port == 443): self.hostname.copy()
		else: "${self.hostname}:${self.port}"
		if(self.raw_query.is_empty()): "${self.scheme}://$hp${self.path}"
		else: "${self.scheme}://$hp${self.path}?${self.raw_query}"
	}

bind _hex_digit_val = (b: u8) ?int:
	if(b >= 48 and b <= 57): int(b - 48)
	else if(b >= 97 and b <= 102): int(b - 97 + 10)
	else if(b >= 65 and b <= 70): int(b - 65 + 10)
	else: none

bind _byte_to_hex_str = (b: u8) string: {
	let digits = "0123456789ABCDEF"
	let hi = int(b >> 4)
	let lo = int(b & 0x0F)
	let out = ""
	out.push_byte(digits.byte_at(hi))
	out.push_byte(digits.byte_at(lo))
	out
}

bind _is_url_unreserved = (b: u8) bool:
	(b >= 65 and b <= 90) or (b >= 97 and b <= 122) or (b >= 48 and b <= 57) or b == 45 or b == 95 or b == 46 or b == 126

// Percent-encodes non-unreserved bytes in `s` (encoding spaces as `"%20"`).
bind url_encode = (s: string) string: {
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
bind url_decode = (s: string) string: {
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

// Parses a full HTTP/HTTPS URL or relative request target into a structured [`URL`].
bind parse_url = (raw: string) Result[URL]: {
	let scheme = ""
	let rest = raw.copy()
	let default_port = 80

	if(raw.starts_with("http://")): {
		scheme = "http"
		rest = raw[7..raw.len()]
		default_port = 80
	} else if(raw.starts_with("https://")): {
		scheme = "https"
		rest = raw[8..raw.len()]
		default_port = 443
	}

	let host = ""
	let hostname = ""
	let port = default_port
	let path_and_query = "/"

	if(scheme != ""): {
		let slash? = rest.index_of("/")
		if(slash?): {
			host = rest[0..slash]
			path_and_query = rest[slash..rest.len()]
		} else: {
			let q? = rest.index_of("?")
			if(q?): {
				host = rest[0..q]
				path_and_query = rest[q..rest.len()]
			} else: {
				host = rest.copy()
				path_and_query = "/"
			}
		}

		let colon? = host.index_of(":")
		if(colon?): {
			hostname = host[0..colon]
			let port_str = host[colon + 1..host.len()]
			port = parse_int(port_str) ?? default_port
		} else: {
			hostname = host.copy()
			port = default_port
		}
	} else: path_and_query = rest.copy()

	let path = "/"
	let raw_query = ""
	let q_idx? = path_and_query.index_of("?")
	if(q_idx?): {
		path = path_and_query[0..q_idx]
		raw_query = path_and_query[q_idx + 1..path_and_query.len()]
	} else: path = path_and_query.copy()

	if(path.is_empty()): path = "/"

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

	Result[URL].ok(URL[
		raw: raw.copy(),
		scheme: scheme,
		host: host,
		hostname: hostname,
		port: port,
		path: path,
		raw_query: raw_query,
		query: query,
	])
}

// ============================================================================
// 3. HTTP Request
// ============================================================================

// HTTP request message constructed by callers or parsed off the wire by [`Server`].
define Request struct [
	// Uppercase HTTP method (`"GET"`, `"POST"`, etc.).
	method: string,
	// Full target URL string.
	url: string,
	// Normalized request path (e.g. `"/api/items"`).
	path: string,
	// Protocol version string (defaults to `"HTTP/1.1"`).
	proto: string,
	// Lowercased HTTP header map.
	headers: #[string: string],
	// Parsed URL query parameters.
	query: #[string: string],
	// Request payload body bytes as a string.
	body: string,
	// Client remote network address if populated.
	remote_addr: string,
]

// Constructs a [`Request`] for `method`, `raw_url`, and optional `body`, setting `host` and `content-length` headers.
bind Request.new = (method: string, raw_url: string, body: string) Request: {
	let url_res! = parse_url(raw_url)
	let u = if(url_res!.is_ok()): url_res!.unwrap()
	else:
		URL[
			raw: raw_url.copy(),
			scheme: "",
			host: "",
			hostname: "",
			port: 80,
			path: if(raw_url.starts_with("/")): raw_url.copy() else: "/",
			raw_query: "",
			query: #[],
		]

	let hdrs: #[string: string] = #[]
	if(not u.host.is_empty()): hdrs["host"] = u.host.copy()
	if(not body.is_empty()): hdrs["content-length"] = "${body.len()}"

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

// Performs a case-insensitive header lookup for `name`, returning `none` if absent.
bind Request.header = (self, name: string) ?string: {
	let lower = name.to_lower()
	let direct? = self.headers[lower]
	if(direct? != none): func <- direct?
	for(k, v in self.headers): if(k.to_lower() == lower): func <- v.copy()
	none
}

// Sets header `name` (normalized to lowercase) to `value`.
bind Request.set_header = (&self, name: string, value: string): {
	self.headers[name.to_lower()] = value.copy()
}

// Returns the URL query parameter value for `name`, or `none` if absent.
bind Request.query_param = (self, name: string) ?string: self.query[name]

bind _strip_crlf = (s: string) string: {
	let has_bad = false
	for(b in s.bytes()):
		if(b == 13 or b == 10 or b == 0): {
			has_bad = true
			for <-
		}
	if(not has_bad): func <- s.copy()
	let out = ""
	for(b in s.bytes()):
		if(b != 13 and b != 10 and b != 0): out.push_byte(b)
	out
}

bind _append_sanitized_headers = (out: &string, headers: #[string: string]): {
	for(k, v in headers): {
		let hk = _strip_crlf(k)
		if(hk.is_empty()): next <-
		let hv = _strip_crlf(v)
		out.push("$hk: $hv\r\n")
	}
}

// Serializes the request into HTTP/1.1 wire bytes (request line, CRLF headers, and body).
bind Request.format = (self) string: {
	let out = ""
	let m = _strip_crlf(if(self.method.is_empty()): "GET" else: self.method.copy())
	let target = _strip_crlf(if(self.path.is_empty()): "/" else: self.path.copy())
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
	_append_sanitized_headers(&out, self.headers)
	out.push("\r\n")
	if(not self.body.is_empty()): out.push(self.body)
	out
}

// Parses raw HTTP/1.1 wire bytes into a [`Request`], returning [`Result.Err`] if the request line or framing is malformed.
bind Request.parse = (raw: string) Result[Request]: {
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
		} else:
			func <- Result[Request].fail(
				Error.new("malformed HTTP request: missing header terminator"),
			)
	}

	let header_part = raw[0..header_end]
	let body_part = raw[header_end + sep_len..raw.len()]

	let lines = header_part.split("\n")
	if(lines.is_empty()): func <- Result[Request].fail(Error.new("empty request"))

	let req_line = (lines.get(0) ?? "").trim()
	let parts = req_line.split(" ")
	if(parts.len() < 2):
		func <- Result[Request].fail(Error.new("invalid HTTP request line: $req_line"))

	let method = (parts.get(0) ?? "").to_upper()
	let raw_target = parts.get(1) ?? "/"
	let proto = if(parts.len() >= 3): parts.get(2) ?? "HTTP/1.1" else: "HTTP/1.1"

	let url_res! = parse_url(raw_target)
	let parsed_url = if(url_res!.is_ok()): url_res!.unwrap()
	else:
		URL[
			raw: raw_target.copy(),
			scheme: "",
			host: "",
			hostname: "",
			port: 80,
			path: raw_target.copy(),
			raw_query: "",
			query: #[],
		]

	let headers: #[string: string] = #[]
	for(i in 1..lines.len()): {
		let line = (lines.get(i) ?? "").trim()
		if(line.is_empty()): next <-
		let colon? = line.index_of(":")
		if(colon?): {
			let k = line[0..colon].trim().to_lower()
			let v = line[colon + 1..line.len()].trim()
			headers[k] = v
		}
	}

	Result[Request].ok(Request[
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
// 4. HTTP Response
// ============================================================================

// HTTP response message produced by handlers ([`ResponseWriter.finish`]) or returned by [`Client.do`].
define Response struct [
	// Numeric HTTP status code (e.g. `200`, `404`).
	status_code: int,
	// HTTP reason phrase (e.g. `"OK"`).
	status: string,
	// Protocol version string (e.g. `"HTTP/1.1"`).
	proto: string,
	// Lowercased response header map.
	headers: #[string: string],
	// Response payload body.
	body: string,
]

// Constructs a `"200"`/status-text HTTP/1.1 [`Response`] with `body` and default `"text/plain; charset=utf-8"` content type.
bind Response.new = (status_code: int, body: string) Response: {
	let hdrs: #[string: string] = #[]
	hdrs["content-length"] = "${body.len()}"
	hdrs["content-type"] = "text/plain; charset=utf-8"

	Response[
		status_code: status_code,
		status: status_text(status_code),
		proto: "HTTP/1.1",
		headers: hdrs,
		body: body.copy(),
	]
}

// Performs a case-insensitive header lookup for `name`, returning `none` if absent.
bind Response.header = (self, name: string) ?string: {
	let lower = name.to_lower()
	let direct? = self.headers[lower]
	if(direct? != none): func <- direct?
	for(k, v in self.headers): if(k.to_lower() == lower): func <- v.copy()
	none
}

// Sets response header `name` (normalized to lowercase) to `value`.
bind Response.set_header = (&self, name: string, value: string): {
	self.headers[name.to_lower()] = value.copy()
}

// Reports whether `status_code` is in the 2xx success range (`200..300`).
bind Response.is_success = (self) bool: self.status_code >= 200 and self.status_code < 300

// Returns a copy of the response body string.
bind Response.text = (self) string: self.body.copy()

// Serializes this [`Response`] into HTTP/1.1 wire bytes (status line, headers, and body).
bind Response.format = (self) string: {
	let out = ""
	let pr = _strip_crlf(if(self.proto.is_empty()): "HTTP/1.1" else: self.proto.copy())
	let code = if(self.status_code <= 0): 200 else: self.status_code
	let msg = _strip_crlf(if(self.status.is_empty()): status_text(code) else: self.status.copy())
	out.push("$pr $code $msg\r\n")
	_append_sanitized_headers(&out, self.headers)
	out.push("\r\n")
	if(not self.body.is_empty()): out.push(self.body)
	out
}

// Parses raw HTTP/1.1 wire bytes into a [`Response`], failing if headers or status line are malformed.
bind Response.parse = (raw: string) Result[Response]: {
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
		} else:
			func <- Result[Response].fail(
				Error.new("malformed HTTP response: missing header terminator"),
			)
	}

	let header_part = raw[0..header_end]
	let body_part = raw[header_end + sep_len..raw.len()]

	let lines = header_part.split("\n")
	if(lines.is_empty()): func <- Result[Response].fail(Error.new("empty response"))

	let status_line = (lines.get(0) ?? "").trim()
	let parts = status_line.split(" ")
	if(parts.len() < 2):
		func <- Result[Response].fail(Error.new("invalid HTTP status line: $status_line"))

	let proto = parts.get(0) ?? "HTTP/1.1"
	let code_str = parts.get(1) ?? "200"
	let status_code = parse_int(code_str) ?? 200

	let status_msg = ""
	if(parts.len() >= 3): {
		let msg_parts: [string] = []
		for(i in 2..parts.len()): msg_parts.push((parts.get(i) ?? "").copy())
		status_msg = msg_parts.join(" ")
	} else: status_msg = status_text(status_code)

	let headers: #[string: string] = #[]
	for(i in 1..lines.len()): {
		let line = (lines.get(i) ?? "").trim()
		if(line.is_empty()): next <-
		let colon? = line.index_of(":")
		if(colon?): {
			let k = line[0..colon].trim().to_lower()
			let v = line[colon + 1..line.len()].trim()
			headers[k] = v
		}
	}

	Result[Response].ok(Response[
		status_code: status_code,
		status: status_msg,
		proto: proto,
		headers: headers,
		body: body_part.copy(),
	])
}

// ============================================================================
// 5. ResponseWriter (for HTTP handlers)
// ============================================================================

// Incremental builder used by HTTP server handlers to assemble an outgoing [`Response`].
//
// Call [`ResponseWriter.set_header`] and [`ResponseWriter.write_header`] before
// [`ResponseWriter.write`] when customizing headers or status codes, then convert
// to a [`Response`] via [`ResponseWriter.finish`].
define ResponseWriter struct [
	// HTTP status code recorded for the response (defaults to `200`).
	status_code: int,
	// Lowercased response header map.
	headers: #[string: string],
	// Accumulated response body payload.
	body: string,
	_header_written: bool,
]

// Creates a blank [`ResponseWriter`] initialized to `200 OK` with empty headers and body.
bind ResponseWriter.new = () ResponseWriter:
	ResponseWriter[
		status_code: 200,
		headers: #[],
		body: "",
		_header_written: false,
	]

// Records the HTTP status `code` if headers have not yet been written.
//
// Subsequent calls after headers have been committed (either directly via
// [`ResponseWriter.write_header`] or implicitly via [`ResponseWriter.write`])
// have no effect.
bind ResponseWriter.write_header = (&self, code: int):
	if(not self._header_written): {
		self.status_code = code
		self._header_written = true
	}

// Sets response header `key` (normalized to lowercase) to a copy of `val`.
bind ResponseWriter.set_header = (&self, key: string, val: string): {
	self.headers[key.to_lower()] = val.copy()
}

// Returns the value of response header `key` using case-insensitive lookup, or `none`.
bind ResponseWriter.header = (self, key: string) ?string: self.headers[key.to_lower()]

// Appends `text` to the response body, committing status `200` if no status was set yet.
bind ResponseWriter.write = (&self, text: string): {
	if(not self._header_written): self.write_header(200)
	self.body.push(text)
}

// Finalizes defaults (`content-length` and `content-type`) and returns the built [`Response`].
//
// If `content-type` is unset and the body is non-empty, it defaults to
// `"text/plain; charset=utf-8"`. Status codes `<= 0` default to `200`.
bind ResponseWriter.finish = (self) Response: {
	let hdrs = self.headers.copy()
	if(hdrs["content-length"] == none): hdrs["content-length"] = "${self.body.len()}"
	if(hdrs["content-type"] == none and not self.body.is_empty()):
		hdrs["content-type"] = "text/plain; charset=utf-8"

	Response[
		status_code: if(self.status_code > 0): self.status_code else: 200,
		status: status_text(if(self.status_code > 0): self.status_code else: 200),
		proto: "HTTP/1.1",
		headers: hdrs,
		body: self.body.copy(),
	]
}

// ============================================================================
// 6. Router & Multiplexer (ServeMux)
// ============================================================================

define _Route struct [
	pattern: string,
	is_prefix: bool,
	handler: (Request, &ResponseWriter),
]

bind _Route.copy = (self) _Route:
	_Route[
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
	_routes: [_Route],
]

// Creates an empty [`ServeMux`] with no registered routes.
bind ServeMux.new = () ServeMux: ServeMux[_routes: []]

// Returns a deep copy of this [`ServeMux`] and all registered route handlers.
bind ServeMux.copy = (self) ServeMux: {
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
bind ServeMux.handle_func = (&self, pattern: string, handler: (Request, &ResponseWriter)): {
	let is_prefix = pattern.ends_with("/")
	self._routes.push(_Route[
		pattern: pattern.copy(),
		is_prefix: is_prefix,
		handler: handler.copy(),
	])
}

// Dispatches `req` to the matching handler or writes a `404` fallback into `res`.
bind ServeMux.serve_http = (self, req: Request, res: &ResponseWriter): {
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
// 7. Internal Socket Message Reading Helper
// ============================================================================

bind _read_http_message = (fd: int, timeout_ms: int) Result[string]: {
	let buf = ""
	let chunk_size = 4096
	unsafe {
		let raw_chunk = alloc_raw[u8](chunk_size)
		let total_needed = -1
		let sep_found = false

		for(true): {
			let n = sys_net_recv(fd, raw_chunk, chunk_size, timeout_ms)
			if(n < 0): {
				free_raw(raw_chunk)
				if(n == -2):
					func <- Result[string].fail(
						Error.coded("timeout", "timeout waiting for HTTP data"),
					)
				func <- Result[string].fail(Error.new("socket error during recv"))
			}
			if(n == 0): for <-
			buf.push_bytes(raw_chunk, n)

			if(not sep_found): {
				let sep? = buf.index_of("\r\n\r\n")
				if(sep? != none): {
					sep_found = true
					let header_end = sep? ?? 0
					let header_text = buf[0..header_end].to_lower()
					let cl_idx? = header_text.index_of("content-length:")
					if(cl_idx? != none): {
						let cl_start = (cl_idx? ?? 0) + 15
						let rest = header_text[cl_start..header_text.len()]
						let line_end? = rest.index_of("\r\n")
						let cl_val_str = if(line_end? != none): rest[0..(line_end? ?? 0)].trim()
						else: rest.trim()
						let cl? = parse_int(cl_val_str)
						if(cl? != none): total_needed = header_end + 4 + (cl? ?? 0)
					} else: {
						let first_line_end? = header_text.index_of("\r\n")
						let first_line = if(first_line_end? != none):
							header_text[0..(first_line_end? ?? 0)]
						else: header_text
						if(first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with(
							"delete ",
						) or first_line.starts_with("options ")): total_needed = header_end + 4
					}
				} else: {
					let lf_sep? = buf.index_of("\n\n")
					if(lf_sep? != none): {
						sep_found = true
						let header_end = lf_sep? ?? 0
						let header_text = buf[0..header_end].to_lower()
						let cl_idx? = header_text.index_of("content-length:")
						if(cl_idx? != none): {
							let cl_start = (cl_idx? ?? 0) + 15
							let rest = header_text[cl_start..header_text.len()]
							let line_end? = rest.index_of("\n")
							let cl_val_str = if(line_end? != none): rest[0..(line_end? ?? 0)].trim()
							else: rest.trim()
							let cl? = parse_int(cl_val_str)
							if(cl? != none): total_needed = header_end + 2 + (cl? ?? 0)
						} else: {
							let first_line_end? = header_text.index_of("\n")
							let first_line = if(first_line_end? != none):
								header_text[0..(first_line_end? ?? 0)]
							else: header_text
							if(first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with(
								"delete ",
							) or first_line.starts_with("options ")): total_needed = header_end + 2
						}
					}
				}
			}

			if(total_needed >= 0 and buf.len() >= total_needed): for <-
		}

		free_raw(raw_chunk)
	}

	if(buf.is_empty()): Result[string].fail(Error.new("empty HTTP message"))
	else: Result[string].ok(own buf)
}

// _TlsSession wraps an active TLS session and ensures proper cleanup via Drop.
define _TlsSession struct [
	_raw: ptr[u8],
]

bind _TlsSession.drop = (&self):
	unsafe {
		if(self._raw != null_ptr[u8]()): {
			sys_tls_close(self._raw)
			self._raw = null_ptr[u8]()
		}
	}

bind _TlsSession.close = (&self):
	unsafe {
		if(self._raw != null_ptr[u8]()): {
			sys_tls_close(self._raw)
			self._raw = null_ptr[u8]()
		}
	}

bind _TlsSession.send = (&self, data: string) Result[int]: {
	let is_null = unsafe (self._raw == null_ptr[u8]())
	if(is_null): func <- Result[int].fail(Error.new("send on closed TLS session"))
	let res = unsafe sys_tls_send_all(self._raw, data.c_str(), data.len())
	if(res == 0): Result[int].ok(data.len())
	else: Result[int].fail(Error.new("failed to send data over TLS"))
}

bind _read_http_message_tls = (session: ptr[u8], timeout_ms: int) Result[string]: {
	let buf = ""
	let chunk_size = 4096
	let total_needed = -1
	let sep_found = false

	unsafe {
		let raw_chunk = alloc_raw[u8](chunk_size)

		for(true): {
			let n = sys_tls_recv(session, raw_chunk, chunk_size, timeout_ms)
			if(n < 0): {
				free_raw(raw_chunk)
				if(n == -2):
					func <- Result[string].fail(
						Error.coded("timeout", "timeout waiting for HTTP data over TLS"),
					)
				func <- Result[string].fail(Error.new("TLS error during recv"))
			}
			if(n == 0): for <-
			buf.push_bytes(raw_chunk, n)

			if(not sep_found): {
				let sep? = buf.index_of("\r\n\r\n")
				if(sep? != none): {
					sep_found = true
					let header_end = sep? ?? 0
					let header_text = buf[0..header_end].to_lower()
					let cl_idx? = header_text.index_of("content-length:")
					if(cl_idx? != none): {
						let cl_start = (cl_idx? ?? 0) + 15
						let rest = header_text[cl_start..header_text.len()]
						let line_end? = rest.index_of("\r\n")
						let cl_val_str = if(line_end? != none): rest[0..(line_end? ?? 0)].trim()
						else: rest.trim()
						let cl? = parse_int(cl_val_str)
						if(cl? != none): total_needed = header_end + 4 + (cl? ?? 0)
					} else: {
						let first_line_end? = header_text.index_of("\r\n")
						let first_line = if(first_line_end? != none):
							header_text[0..(first_line_end? ?? 0)]
						else: header_text
						if(first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with(
							"delete ",
						) or first_line.starts_with("options ")): total_needed = header_end + 4
					}
				} else: {
					let lf_sep? = buf.index_of("\n\n")
					if(lf_sep? != none): {
						sep_found = true
						let header_end = lf_sep? ?? 0
						let header_text = buf[0..header_end].to_lower()
						let cl_idx? = header_text.index_of("content-length:")
						if(cl_idx? != none): {
							let cl_start = (cl_idx? ?? 0) + 15
							let rest = header_text[cl_start..header_text.len()]
							let line_end? = rest.index_of("\n")
							let cl_val_str = if(line_end? != none): rest[0..(line_end? ?? 0)].trim()
							else: rest.trim()
							let cl? = parse_int(cl_val_str)
							if(cl? != none): total_needed = header_end + 2 + (cl? ?? 0)
						} else: {
							let first_line_end? = header_text.index_of("\n")
							let first_line = if(first_line_end? != none):
								header_text[0..(first_line_end? ?? 0)]
							else: header_text
							if(first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with(
								"delete ",
							) or first_line.starts_with("options ")): total_needed = header_end + 2
						}
					}
				}
			}

			if(total_needed >= 0 and buf.len() >= total_needed): for <-
		}

		free_raw(raw_chunk)
	}

	if(buf.is_empty()): Result[string].fail(Error.new("empty HTTP message"))
	else: Result[string].ok(own buf)
}

// ============================================================================
// 8. HTTP Client
// ============================================================================

// Configurable HTTP/HTTPS client with default headers and request timeout.
define Client struct [
	// Socket connect, write, and response-read timeout in milliseconds (default `15000`).
	timeout_ms: int,
	// Default headers attached to outgoing requests when not already present on the [`Request`].
	headers: #[string: string],
]

// Creates a [`Client`] with a `15000` ms timeout and no default headers.
bind Client.new = () Client:
	Client[
		timeout_ms: 15000,
		headers: #[],
	]

// Executes `req` over HTTP or HTTPS (`https://` or port `443`) and returns the parsed [`Response`].
//
// Populates `Host`, `User-Agent`, `Accept`, `Connection: close`, `Content-Length` (when the body
// is non-empty), and any client-level default headers if not already set on `req`.
//
// # Errors
// Returns [`Result.fail`] if `req.url` cannot be parsed, the TCP connection fails, TLS
// initialization or handshake fails for HTTPS requests (which dynamically requires OpenSSL `libssl`),
// sending/receiving fails, or the server response is malformed.
bind Client.do = (self, req: Request) Result[Response]: {
	let url_res! = parse_url(req.url)
	if(url_res!.is_err()):
		func <- Result[Response].fail(url_res!.unwrap_err().wrap("invalid request URL: ${req.url}"))
	let u = url_res!.unwrap()
	let is_tls = u.scheme == "https" or u.port == 443
	let host = if(not u.hostname.is_empty()): u.hostname.copy() else: "127.0.0.1"
	let port = if(u.port > 0): u.port
	else if(is_tls): 443
	else: 80

	let mut_req = req.copy()
	if(mut_req.header("host") == none):
		mut_req.set_header("Host", if(port == 80 or port == 443): host.copy() else: "$host:$port")
	if(mut_req.header("user-agent") == none): mut_req.set_header("User-Agent", "Lowlight-HTTP/1.0")
	if(mut_req.header("accept") == none): mut_req.set_header("Accept", "*/*")
	if(mut_req.header("connection") == none): mut_req.set_header("Connection", "close")
	if(not mut_req.body.is_empty() and mut_req.header("content-length") == none):
		mut_req.set_header("Content-Length", "${mut_req.body.len()}")
	for(k, v in self.headers): if(mut_req.header(k) == none): mut_req.set_header(k.copy(), v.copy())

	let wire = mut_req.format()
	let stream_res! = net.TcpStream.connect(host, port, self.timeout_ms)
	if(stream_res!.is_err()):
		func <- Result[Response].fail(stream_res!.unwrap_err().wrap("connection failed"))
	let stream = stream_res!.unwrap()

	if(is_tls): {
		let avail = unsafe sys_tls_available()
		if(avail == 0):
			func <- Result[Response].fail(
				Error.new(
					"HTTPS requires libssl (OpenSSL 1.1 / 3.0), which could not be loaded dynamically",
				),
			)
		let raw_sess = unsafe sys_tls_client_handshake(stream.raw_fd(), host.c_str())
		let is_null = unsafe (raw_sess == null_ptr[u8]())
		if(is_null):
			func <- Result[Response].fail(
				Error.new("TLS handshake failed connecting to $host:$port"),
			)
		let tls_sess = _TlsSession[_raw: raw_sess]

		let send_res! = tls_sess.send(wire)
		if(send_res!.is_err()):
			func <- Result[Response].fail(
				send_res!.unwrap_err().wrap("failed to send HTTP request over TLS"),
			)

		let msg_res! = _read_http_message_tls(tls_sess._raw, self.timeout_ms)
		if(msg_res!.is_err()):
			func <- Result[Response].fail(
				msg_res!.unwrap_err().wrap("failed to read HTTP response over TLS"),
			)
		let raw_resp = msg_res!.unwrap()
		Response.parse(raw_resp).wrap_err("failed to parse HTTP response")
	} else: {
		let send_res! = stream.send(wire)
		if(send_res!.is_err()):
			func <- Result[Response].fail(send_res!.unwrap_err().wrap("failed to send HTTP request"))

		let msg_res! = _read_http_message(stream.raw_fd(), self.timeout_ms)
		if(msg_res!.is_err()):
			func <- Result[Response].fail(msg_res!.unwrap_err().wrap("failed to read HTTP response"))
		let raw_resp = msg_res!.unwrap()
		Response.parse(raw_resp).wrap_err("failed to parse HTTP response")
	}
}

bind Client._send_method = (
	self,
	method: string,
	url: string,
	content_type?: ?string,
	body: string,
) Result[Response]: {
	let req = Request.new(method, url, body)
	if(content_type? as ct): req.set_header("Content-Type", ct)
	self.do(req)
}

// Sends an HTTP `GET` request to `url` using this client's configuration.
bind Client.get = (self, url: string) Result[Response]:
	self._send_method("GET", url, none, "")

// Sends an HTTP `POST` request with `content_type` and `body` to `url`.
bind Client.post = (self, url: string, content_type: string, body: string) Result[Response]:
	self._send_method("POST", url, content_type, body)

// Sends an HTTP `PUT` request with `content_type` and `body` to `url`.
bind Client.put = (self, url: string, content_type: string, body: string) Result[Response]:
	self._send_method("PUT", url, content_type, body)

// Sends an HTTP `PATCH` request with `content_type` and `body` to `url`.
bind Client.patch = (self, url: string, content_type: string, body: string) Result[Response]:
	self._send_method("PATCH", url, content_type, body)

// Sends an HTTP `DELETE` request to `url` using this client's configuration.
bind Client.delete = (self, url: string) Result[Response]:
	self._send_method("DELETE", url, none, "")

// Sends a one-off HTTP `GET` request to `url` with default [`Client`] settings.
bind get = (url: string) Result[Response]: {
	let c = Client.new()
	c.get(url)
}

// Sends a one-off HTTP `POST` request with `content_type` and `body` to `url`.
bind post = (url: string, content_type: string, body: string) Result[Response]: {
	let c = Client.new()
	c.post(url, content_type, body)
}

// Sends a one-off HTTP `PUT` request with `content_type` and `body` to `url`.
bind put = (url: string, content_type: string, body: string) Result[Response]: {
	let c = Client.new()
	c.put(url, content_type, body)
}

// Sends a one-off HTTP `PATCH` request with `content_type` and `body` to `url`.
bind patch = (url: string, content_type: string, body: string) Result[Response]: {
	let c = Client.new()
	c.patch(url, content_type, body)
}

// Sends a one-off HTTP `DELETE` request to `url` with default [`Client`] settings.
bind delete = (url: string) Result[Response]: {
	let c = Client.new()
	c.delete(url)
}

// ============================================================================
// 9. HTTP Server
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
bind Server.new = (addr: string) Server:
	Server[
		addr: addr.copy(),
		port: 0,
		listener_fd: -1,
		read_timeout_ms: 10000,
		write_timeout_ms: 10000,
	]

// Closes the underlying listener socket when the [`Server`] goes out of scope.
bind Server.drop = (&self): self.close()

// Closes the listener socket if currently open and resets `listener_fd` to `-1`.
bind Server.close = (&self):
	if(self.listener_fd >= 0): {
		unsafe sys_net_close(self.listener_fd)
		self.listener_fd = -1
	}

// Binds the TCP listener socket according to `self.addr` and returns the bound port.
//
// Supports `"host:port"`, `"port"`, or `"host"` strings, defaulting omitted host to
// `"0.0.0.0"` and omitted port to `8080`. Passing port `0` selects an ephemeral OS port
// stored in `self.port`.
//
// # Errors
// Returns [`Result.fail`] if binding or listening on the address fails.
bind Server.listen = (&self) Result[int]: {
	let host = "0.0.0.0"
	let port = 8080

	if(not self.addr.is_empty()): {
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

	let sfd = unsafe sys_tcp_listen(host.c_str(), port, 128)
	if(sfd < 0): func <- Result[int].fail(Error.new("failed to bind server on ${self.addr}"))
	self.listener_fd = sfd
	let actual_port = unsafe sys_tcp_port(sfd)
	self.port = if(actual_port > 0): actual_port else: port
	Result[int].ok(self.port)
}

// Reads one HTTP request from `conn_fd`, invokes `handler`, writes the response, and closes `conn_fd`.
//
// Malformed requests receive a `400 Bad Request` response and return `false`.
bind Server.serve_conn = (&self, conn_fd: int, handler: (Request, &ResponseWriter)) bool: {
	let raw_req_res! = _read_http_message(conn_fd, self.read_timeout_ms)
	if(raw_req_res!.is_err()): {
		unsafe sys_net_close(conn_fd)
		func <- false
	}
	let raw_req = raw_req_res!.unwrap()
	let req_res! = Request.parse(raw_req)
	if(req_res!.is_err()): {
		let bad_resp = "HTTP/1.1 400 Bad Request\r\nContent-Length: 15\r\nConnection: close\r\n\r\n400 Bad Request"
		unsafe {
			let _ = sys_net_send_all(conn_fd, bad_resp.c_str(), bad_resp.len())
			sys_net_close(conn_fd)
		}
		func <- false
	}
	let req = req_res!.unwrap()
	let rw = ResponseWriter.new()
	handler(req, &rw)

	let resp = rw.finish()
	let wire = resp.format()
	unsafe {
		let _ = sys_net_send_all(conn_fd, wire.c_str(), wire.len())
		sys_net_close(conn_fd)
	}
	true
}

// Accepts and serves a single connection, waiting up to `timeout_ms` milliseconds.
//
// Calls [`Server.listen`] automatically if the server is not already listening.
//
// # Errors
// Returns a coded `"timeout"` [`Error`] when `timeout_ms` expires before a client connects,
// or an error if listening or accepting fails.
bind Server.serve_one = (&self, handler: (Request, &ResponseWriter), timeout_ms: int) Result[bool]: {
	if(self.listener_fd < 0): {
		let lr! = self.listen()
		if(lr!.is_err()): func <- Result[bool].fail(lr!.unwrap_err().wrap("listen failed"))
	}
	let client_fd = unsafe sys_tcp_accept(self.listener_fd, timeout_ms)
	if(client_fd < 0): {
		if(client_fd == -2):
			func <- Result[bool].fail(Error.coded("timeout", "timeout waiting for connection"))
		func <- Result[bool].fail(Error.new("accept error"))
	}
	let ok = self.serve_conn(client_fd, handler)
	Result[bool].ok(ok)
}

// Runs the accept loop, handling connections sequentially with `handler`.
//
// Stops once `max_requests` valid requests have been served, or runs indefinitely when
// `max_requests <= 0`. Returns the total number of valid requests served.
bind Server.serve = (&self, handler: (Request, &ResponseWriter), max_requests: int) Result[int]: {
	if(self.listener_fd < 0): {
		let lr! = self.listen()
		if(lr!.is_err()): func <- Result[int].fail(lr!.unwrap_err().wrap("listen failed"))
	}
	let served = 0
	for(max_requests <= 0 or served < max_requests): {
		let client_fd = unsafe sys_tcp_accept(self.listener_fd, -1)
		if(client_fd >= 0): {
			let ok = self.serve_conn(client_fd, handler)
			if(ok): served += 1
		}
	}
	Result[int].ok(served)
}

// Starts a [`Server`] bound to `addr` and serves requests indefinitely using `handler`.
bind listen_and_serve = (addr: string, handler: (Request, &ResponseWriter)) Result[bool]: {
	let s = Server.new(addr)
	let res! = s.serve(handler, 0)
	if(res!.is_ok()): Result[bool].ok(true)
	else: Result[bool].fail(res!.unwrap_err().wrap("server error"))
}

// Starts a [`Server`] bound to `addr` and dispatches requests through `mux`.
bind listen_and_serve_mux = (addr: string, mux: ServeMux) Result[bool]: {
	let mux_copy = mux.copy()
	listen_and_serve(
		addr,
		(req: Request, res: &ResponseWriter): mux_copy.serve_http(req, &res),
	)
}

// ============================================================================
// 10. Testing Utilities (ResponseRecorder & Mock Transport)
// ============================================================================

// In-memory response recorder wrapping a [`ResponseWriter`] for handler unit tests.
define ResponseRecorder struct [
	// Underlying [`ResponseWriter`] capturing status, headers, and body writes.
	writer: ResponseWriter,
]

// Creates a fresh [`ResponseRecorder`].
bind ResponseRecorder.new = () ResponseRecorder: ResponseRecorder[writer: ResponseWriter.new()]

// Returns the recorded HTTP response by finalizing [`ResponseRecorder.writer`].
bind ResponseRecorder.result = (self) Response: self.writer.finish()

// Executes `handler` directly against `req` in memory and returns the resulting [`Response`].
bind test_handler = (handler: (Request, &ResponseWriter), req: Request) Response: {
	let rw = ResponseWriter.new()
	handler(req, &rw)
	rw.finish()
}

// Simulates an HTTP wire roundtrip over a kernel socketpair without opening TCP ports.
//
// Serializes `req` with [`Request.format`], reads and parses it on the server end, invokes
// `handler`, serializes the reply, and parses the client-side [`Response`].
bind test_roundtrip = (handler: (Request, &ResponseWriter), req: Request) Result[Response]: {
	let pair_res! = net.socketpair()
	if(pair_res!.is_err()):
		func <- Result[Response].fail(pair_res!.unwrap_err().wrap("socketpair failed"))
	let pair = pair_res!.unwrap()

	let req_wire = req.format()
	let send_res! = pair.first.send(req_wire)
	if(send_res!.is_err()):
		func <- Result[Response].fail(
			send_res!.unwrap_err().wrap("failed to send request to socketpair"),
		)

	let srv_req_res! = _read_http_message(pair.second.raw_fd(), 2000)
	if(srv_req_res!.is_err()):
		func <- Result[Response].fail(
			srv_req_res!.unwrap_err().wrap("server failed to read request"),
		)
	let parsed_req_res! = Request.parse(srv_req_res!.unwrap())
	if(parsed_req_res!.is_err()):
		func <- Result[Response].fail(
			parsed_req_res!.unwrap_err().wrap("server failed to parse request"),
		)
	let srv_req = parsed_req_res!.unwrap()

	let rw = ResponseWriter.new()
	handler(srv_req, &rw)
	let resp = rw.finish()
	let resp_wire = resp.format()

	let srv_send_res! = pair.second.send(resp_wire)
	if(srv_send_res!.is_err()):
		func <- Result[Response].fail(
			srv_send_res!.unwrap_err().wrap("server failed to send response"),
		)

	let client_read_res! = _read_http_message(pair.first.raw_fd(), 2000)
	if(client_read_res!.is_err()):
		func <- Result[Response].fail(
			client_read_res!.unwrap_err().wrap("client failed to read response"),
		)
	let client_wire = client_read_res!.unwrap()
	Response.parse(client_wire)
}

// -----------------------------------------------------------------------------
// Inline Unit Tests
// -----------------------------------------------------------------------------

@test("http status codes and standard RFC status text")
bind test_http_status_codes_and_texts = (): {
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
bind test_http_url_parsing = (): {
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
bind test_http_request_response_wire = (): {
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
bind test_http_serve_mux_and_handler = (): {
	let mux = ServeMux.new()
	mux.handle_func(
		"/hello",
		(req: Request, rw: &ResponseWriter): {
			rw.write_header(STATUS_OK)
			rw.write("hello from mux")
		},
	)
	mux.handle_func(
		"/greet",
		(req: Request, rw: &ResponseWriter): {
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
bind test_http_socketpair_roundtrip = (): {
	let handler = (req: Request, rw: &ResponseWriter): {
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
bind test_http_query_and_crlf_guards = (): {
	let req = Request.new("GET", "/search?q=hello+world", "")
	req.set_header("X-Safe", "val1\r\nEvil: injected")
	req.set_header("X-Key\r\nBad", "v\n2")
	req.method = "GET\r\nInjected"
	req.proto = "HTTP/1.1\r\nSmuggled"
	let wire = req.format()
	assert(wire.starts_with("GETInjected /search?q=hello%20world HTTP/1.1Smuggled\r\n"), "query and start line sanitized")
	assert(not wire.contains("\r\nEvil:"), "injected header line prevented in Request.format")
	let parsed = Request.parse(wire).unwrap()
	assert(parsed.header("evil") == none, "Evil header not present")
	assert((parsed.query_param("q") ?? "") == "hello world", "query param survives Request.format roundtrip")

	let resp = Response.new(200, "ok")
	resp.status = "OK\r\nInjected-Resp: 1"
	resp.set_header("X-Resp", "a\r\nSet-Cookie: bad=1")
	let rwire = resp.format()
	assert(not rwire.contains("\r\nSet-Cookie:"), "injected header line prevented in Response.format")
	assert(not rwire.contains("\r\nInjected-Resp:"), "injected status header prevented in Response.format")
	let rparsed = Response.parse(rwire).unwrap()
	assert(rparsed.header("set-cookie") == none, "Set-Cookie header not present")
}
