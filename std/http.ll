// Lowlight Standard Library: std/http.ll
// HTTP/1.1 Client, Server, URL parsing, Headers, Requests, Responses,
// ResponseWriter, and ServeMux multiplexer.
// Idiomatic Lowlight design inspired by Go's `net/http`.

import "std/net.ll" as net

extern ll_tls_available: (): int
extern ll_tls_client_handshake: (fd: int, host: ptr[u8]): ptr[u8]
extern ll_tls_send_all: (session: ptr[u8], data: ptr[u8], len: int): int
extern ll_tls_recv: (session: ptr[u8], buf: ptr[u8], max_len: int, timeout_ms: int): int
extern ll_tls_close: (session: ptr[u8])
extern ll_tls_server_ctx_new: (cert_file: ptr[u8], key_file: ptr[u8]): ptr[u8]
extern ll_tls_server_ctx_free: (ctx: ptr[u8])
extern ll_tls_server_handshake: (server_ctx: ptr[u8], fd: int): ptr[u8]

// ============================================================================
// 1. HTTP Status Codes & Status Texts
// ============================================================================

bind STATUS_CONTINUE = 100
bind STATUS_SWITCHING_PROTOCOLS = 101

bind STATUS_OK = 200
bind STATUS_CREATED = 201
bind STATUS_ACCEPTED = 202
bind STATUS_NON_AUTHORITATIVE_INFO = 203
bind STATUS_NO_CONTENT = 204
bind STATUS_RESET_CONTENT = 205
bind STATUS_PARTIAL_CONTENT = 206

bind STATUS_MULTIPLE_CHOICES = 300
bind STATUS_MOVED_PERMANENTLY = 301
bind STATUS_FOUND = 302
bind STATUS_SEE_OTHER = 303
bind STATUS_NOT_MODIFIED = 304
bind STATUS_TEMPORARY_REDIRECT = 307
bind STATUS_PERMANENT_REDIRECT = 308

bind STATUS_BAD_REQUEST = 400
bind STATUS_UNAUTHORIZED = 401
bind STATUS_PAYMENT_REQUIRED = 402
bind STATUS_FORBIDDEN = 403
bind STATUS_NOT_FOUND = 404
bind STATUS_METHOD_NOT_ALLOWED = 405
bind STATUS_NOT_ACCEPTABLE = 406
bind STATUS_PROXY_AUTH_REQUIRED = 407
bind STATUS_REQUEST_TIMEOUT = 408
bind STATUS_CONFLICT = 409
bind STATUS_GONE = 410
bind STATUS_LENGTH_REQUIRED = 411
bind STATUS_PAYLOAD_TOO_LARGE = 413
bind STATUS_URI_TOO_LONG = 414
bind STATUS_UNSUPPORTED_MEDIA_TYPE = 415
bind STATUS_TOO_MANY_REQUESTS = 429

bind STATUS_INTERNAL_SERVER_ERROR = 500
bind STATUS_NOT_IMPLEMENTED = 501
bind STATUS_BAD_GATEWAY = 502
bind STATUS_SERVICE_UNAVAILABLE = 503
bind STATUS_GATEWAY_TIMEOUT = 504
bind STATUS_HTTP_VERSION_NOT_SUPPORTED = 505

// status_text returns the standard RFC status message for an HTTP status code.
bind status_text = (code: int): string {
    match (code)
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
        else: "Unknown"
}

// ============================================================================
// 2. URL Parsing & Percent-Encoding
// ============================================================================

define URL struct [
    raw: string,
    scheme: string,
    host: string,
    hostname: string,
    port: int,
    path: string,
    raw_query: string,
    query: #[string: string],
]

bind URL.query_param = (self, key: string): ?string {
    self.query[key]
}

bind URL.to_string = (self): string {
    if (self.scheme.is_empty()) {
        if (self.raw_query.is_empty()) self.path.copy() else "${self.path}?${self.raw_query}"
    } else {
        let hp = if (self.port == 80 or self.port == 443) self.hostname.copy() else "${self.hostname}:${self.port}"
        if (self.raw_query.is_empty()) "${self.scheme}://${hp}${self.path}" else "${self.scheme}://${hp}${self.path}?${self.raw_query}"
    }
}

bind hex_digit_val = (b: u8): ?int {
    if (b >= 48 and b <= 57) {
        int(b - 48)
    } else if (b >= 97 and b <= 102) {
        int(b - 97 + 10)
    } else if (b >= 65 and b <= 70) {
        int(b - 65 + 10)
    } else {
        none
    }
}

bind byte_to_hex_str = (b: u8): string {
    let digits = "0123456789ABCDEF"
    let hi = int(b >> 4)
    let lo = int(b & 0x0F)
    let out = ""
    out.push_byte(digits.byte_at(hi))
    out.push_byte(digits.byte_at(lo))
    out
}

bind is_url_unreserved = (b: u8): bool {
    (b >= 65 and b <= 90) or
    (b >= 97 and b <= 122) or
    (b >= 48 and b <= 57) or
    b == 45 or b == 95 or b == 46 or b == 126
}

bind url_encode = (s: string): string {
    let out = ""
    let bytes = s.bytes()
    for (b in bytes) {
        if (is_url_unreserved(b)) {
            out.push_byte(b)
        } else if (b == 32) {
            out.push("%20")
        } else {
            out.push("%")
            out.push(byte_to_hex_str(b))
        }
    }
    out
}

bind url_decode = (s: string): string {
    let out = ""
    let n = s.len()
    let i = 0
    for (i < n) {
        let b = s.byte_at(i)
        if (b == 37 and i + 2 < n) {
            let h1? = hex_digit_val(s.byte_at(i + 1))
            let h2? = hex_digit_val(s.byte_at(i + 2))
            if (h1? != none and h2? != none) {
                let dec_byte = u8(((h1? ?? 0) << 4) | (h2? ?? 0))
                out.push_byte(dec_byte)
                i += 3
                next <-
            }
        } else if (b == 43) {
            out.push_byte(32)
            i += 1
            next <-
        }
        out.push_byte(b)
        i += 1
    }
    out
}

bind parse_url = (raw: string): Result[URL, string] {
    let scheme = ""
    let rest = raw.copy()
    let default_port = 80

    if (raw.starts_with("http://")) {
        scheme = "http"
        rest = raw[7..raw.len()]
        default_port = 80
    } else if (raw.starts_with("https://")) {
        scheme = "https"
        rest = raw[8..raw.len()]
        default_port = 443
    }

    let host = ""
    let hostname = ""
    let port = default_port
    let path_and_query = "/"

    if (scheme != "") {
        let slash? = rest.index_of("/")
        if (slash?) {
            host = rest[0..slash]
            path_and_query = rest[slash..rest.len()]
        } else {
            let q? = rest.index_of("?")
            if (q?) {
                host = rest[0..q]
                path_and_query = rest[q..rest.len()]
            } else {
                host = rest.copy()
                path_and_query = "/"
            }
        }

        let colon? = host.index_of(":")
        if (colon?) {
            hostname = host[0..colon]
            let port_str = host[(colon + 1)..host.len()]
            port = parse_int(port_str) ?? default_port
        } else {
            hostname = host.copy()
            port = default_port
        }
    } else {
        path_and_query = rest.copy()
    }

    let path = "/"
    let raw_query = ""
    let q_idx? = path_and_query.index_of("?")
    if (q_idx?) {
        path = path_and_query[0..q_idx]
        raw_query = path_and_query[(q_idx + 1)..path_and_query.len()]
    } else {
        path = path_and_query.copy()
    }

    if (path.is_empty()) {
        path = "/"
    }

    let query: #[string: string] = #[]
    if (not raw_query.is_empty()) {
        let pairs = raw_query.split("&")
        for (pair in pairs) {
            let eq? = pair.index_of("=")
            if (eq?) {
                let k = url_decode(pair[0..eq])
                let v = url_decode(pair[(eq + 1)..pair.len()])
                query[k] = v
            } else if (not pair.is_empty()) {
                let k = url_decode(pair)
                query[k] = ""
            }
        }
    }

    Result[URL, string].ok(URL[
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

define Request struct [
    method: string,
    url: string,
    path: string,
    proto: string,
    headers: #[string: string],
    query: #[string: string],
    body: string,
    remote_addr: string,
]

bind Request.new = (method: string, raw_url: string, body: string): Request {
    let url_res = parse_url(raw_url)
    let u = if (url_res.is_ok()) url_res.unwrap() else URL[
        raw: raw_url.copy(),
        scheme: "",
        host: "",
        hostname: "",
        port: 80,
        path: if (raw_url.starts_with("/")) raw_url.copy() else "/",
        raw_query: "",
        query: #[],
    ]

    let hdrs: #[string: string] = #[]
    if (not u.host.is_empty()) {
        hdrs["host"] = u.host.copy()
    }
    if (not body.is_empty()) {
        hdrs["content-length"] = "${body.len()}"
    }

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

bind Request.header = (self, name: string): ?string {
    let lower = name.to_lower()
    let direct? = self.headers[lower]
    if (direct? != none) func <- direct?
    for (k, v in self.headers) {
        if (k.to_lower() == lower) {
            func <- v.copy()
        }
    }
    none
}

bind Request.set_header = (&self, name: string, value: string) {
    self.headers[name.to_lower()] = value.copy()
}

bind Request.query_param = (self, name: string): ?string {
    self.query[name]
}

bind Request.format = (self): string {
    let out = ""
    let m = if (self.method.is_empty()) "GET" else self.method.copy()
    let p = if (self.path.is_empty()) "/" else self.path.copy()
    let pr = if (self.proto.is_empty()) "HTTP/1.1" else self.proto.copy()
    out.push("$m $p $pr\r\n")
    for (k, v in self.headers) {
        out.push("${k}: ${v}\r\n")
    }
    out.push("\r\n")
    if (not self.body.is_empty()) {
        out.push(self.body)
    }
    out
}

bind Request.parse = (raw: string): Result[Request, string] {
    let header_end? = raw.index_of("\r\n\r\n")
    let sep_len = 4
    let header_end = 0

    if (header_end? != none) {
        header_end = header_end? ?? 0
        sep_len = 4
    } else {
        let lf_end? = raw.index_of("\n\n")
        if (lf_end? != none) {
            header_end = lf_end? ?? 0
            sep_len = 2
        } else {
            func <- Result[Request, string].fail("malformed HTTP request: missing header terminator")
        }
    }

    let header_part = raw[0..header_end]
    let body_part = raw[(header_end + sep_len)..raw.len()]

    let lines = header_part.split("\n")
    if (lines.is_empty()) {
        func <- Result[Request, string].fail("empty request")
    }

    let req_line = (lines.get(0) ?? "").trim()
    let parts = req_line.split(" ")
    if (parts.len() < 2) {
        func <- Result[Request, string].fail("invalid HTTP request line: $req_line")
    }

    let method = (parts.get(0) ?? "").to_upper()
    let raw_target = parts.get(1) ?? "/"
    let proto = if (parts.len() >= 3) (parts.get(2) ?? "HTTP/1.1") else "HTTP/1.1"

    let url_res = parse_url(raw_target)
    let parsed_url = if (url_res.is_ok()) url_res.unwrap() else URL[
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
    for (i in 1..lines.len()) {
        let line = (lines.get(i) ?? "").trim()
        if (line.is_empty()) next <-
        let colon? = line.index_of(":")
        if (colon?) {
            let k = line[0..colon].trim().to_lower()
            let v = line[(colon + 1)..line.len()].trim()
            headers[k] = v
        }
    }

    Result[Request, string].ok(Request[
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

define Response struct [
    status_code: int,
    status: string,
    proto: string,
    headers: #[string: string],
    body: string,
]

bind Response.new = (status_code: int, body: string): Response {
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

bind Response.header = (self, name: string): ?string {
    let lower = name.to_lower()
    let direct? = self.headers[lower]
    if (direct? != none) func <- direct?
    for (k, v in self.headers) {
        if (k.to_lower() == lower) {
            func <- v.copy()
        }
    }
    none
}

bind Response.set_header = (&self, name: string, value: string) {
    self.headers[name.to_lower()] = value.copy()
}

bind Response.is_success = (self): bool {
    self.status_code >= 200 and self.status_code < 300
}

bind Response.text = (self): string {
    self.body.copy()
}

bind Response.format = (self): string {
    let out = ""
    let pr = if (self.proto.is_empty()) "HTTP/1.1" else self.proto.copy()
    let code = if (self.status_code <= 0) 200 else self.status_code
    let msg = if (self.status.is_empty()) status_text(code) else self.status.copy()
    out.push("$pr $code $msg\r\n")
    for (k, v in self.headers) {
        out.push("${k}: ${v}\r\n")
    }
    out.push("\r\n")
    if (not self.body.is_empty()) {
        out.push(self.body)
    }
    out
}

bind Response.parse = (raw: string): Result[Response, string] {
    let header_end? = raw.index_of("\r\n\r\n")
    let sep_len = 4
    let header_end = 0

    if (header_end? != none) {
        header_end = header_end? ?? 0
        sep_len = 4
    } else {
        let lf_end? = raw.index_of("\n\n")
        if (lf_end? != none) {
            header_end = lf_end? ?? 0
            sep_len = 2
        } else {
            func <- Result[Response, string].fail("malformed HTTP response: missing header terminator")
        }
    }

    let header_part = raw[0..header_end]
    let body_part = raw[(header_end + sep_len)..raw.len()]

    let lines = header_part.split("\n")
    if (lines.is_empty()) {
        func <- Result[Response, string].fail("empty response")
    }

    let status_line = (lines.get(0) ?? "").trim()
    let parts = status_line.split(" ")
    if (parts.len() < 2) {
        func <- Result[Response, string].fail("invalid HTTP status line: $status_line")
    }

    let proto = parts.get(0) ?? "HTTP/1.1"
    let code_str = parts.get(1) ?? "200"
    let status_code = parse_int(code_str) ?? 200

    let status_msg = ""
    if (parts.len() >= 3) {
        let msg_parts: [string] = []
        for (i in 2..parts.len()) {
            msg_parts.push((parts.get(i) ?? "").copy())
        }
        status_msg = msg_parts.join(" ")
    } else {
        status_msg = status_text(status_code)
    }

    let headers: #[string: string] = #[]
    for (i in 1..lines.len()) {
        let line = (lines.get(i) ?? "").trim()
        if (line.is_empty()) next <-
        let colon? = line.index_of(":")
        if (colon?) {
            let k = line[0..colon].trim().to_lower()
            let v = line[(colon + 1)..line.len()].trim()
            headers[k] = v
        }
    }

    Result[Response, string].ok(Response[
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

define ResponseWriter struct [
    status_code: int,
    headers: #[string: string],
    body: string,
    header_written: bool,
]

bind ResponseWriter.new = (): ResponseWriter {
    ResponseWriter[
        status_code: 200,
        headers: #[],
        body: "",
        header_written: false,
    ]
}

bind ResponseWriter.write_header = (&self, code: int) {
    if (not self.header_written) {
        self.status_code = code
        self.header_written = true
    }
}

bind ResponseWriter.set_header = (&self, key: string, val: string) {
    self.headers[key.to_lower()] = val.copy()
}

bind ResponseWriter.header = (self, key: string): ?string {
    self.headers[key.to_lower()]
}

bind ResponseWriter.write = (&self, text: string) {
    if (not self.header_written) {
        self.write_header(200)
    }
    self.body.push(text)
}

bind ResponseWriter.finish = (self): Response {
    let hdrs = self.headers.copy()
    if (hdrs["content-length"] == none) {
        hdrs["content-length"] = "${self.body.len()}"
    }
    if (hdrs["content-type"] == none and not self.body.is_empty()) {
        hdrs["content-type"] = "text/plain; charset=utf-8"
    }

    Response[
        status_code: if (self.status_code > 0) self.status_code else 200,
        status: status_text(if (self.status_code > 0) self.status_code else 200),
        proto: "HTTP/1.1",
        headers: hdrs,
        body: self.body.copy(),
    ]
}

// ============================================================================
// 6. Router & Multiplexer (ServeMux)
// ============================================================================

define Route struct [
    pattern: string,
    is_prefix: bool,
    handler: (Request, &ResponseWriter),
]

bind Route.copy = (self): Route {
    Route[
        pattern: self.pattern.copy(),
        is_prefix: self.is_prefix,
        handler: self.handler.copy(),
    ]
}

define ServeMux struct [
    routes: [Route],
]

bind ServeMux.new = (): ServeMux {
    ServeMux[routes: []]
}

bind ServeMux.copy = (self): ServeMux {
    let copied: [Route] = []
    for (r in self.routes) {
        copied.push(r.copy())
    }
    ServeMux[routes: own copied]
}

bind ServeMux.handle_func = (&self, pattern: string, handler: (Request, &ResponseWriter)) {
    let is_prefix = pattern.ends_with("/")
    self.routes.push(Route[
        pattern: pattern.copy(),
        is_prefix: is_prefix,
        handler: handler.copy(),
    ])
}

bind ServeMux.serve_http = (self, req: Request, res: &ResponseWriter) {
    // 1. Check exact match
    for (r in self.routes) {
        if (not r.is_prefix and r.pattern == req.path) {
            let h = r.handler.copy()
            h(req, &res)
            func <-
        }
    }

    // 2. Check prefix match (prefer longest prefix)
    let best_idx?: ?int = none
    let best_len = -1
    for (i, r in self.routes) {
        if (r.is_prefix and req.path.starts_with(r.pattern)) {
            if (r.pattern.len() > best_len) {
                best_len = r.pattern.len()
                best_idx? = i
            }
        }
    }

    if (best_idx?) {
        let route? = self.routes.get(best_idx)
        if (route?) {
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

bind read_http_message = (fd: int, timeout_ms: int): Result[string, string] {
    let buf = ""
    let chunk_size = 4096
    unsafe {
        let raw_chunk = alloc_raw[u8](chunk_size)
        let total_needed = -1
        let sep_found = false

        for (true) {
            let n = net.ll_net_recv(fd, raw_chunk, chunk_size, timeout_ms)
            if (n < 0) {
                free_raw(raw_chunk)
                if (n == -2) func <- Result[string, string].fail("timeout waiting for HTTP data")
                func <- Result[string, string].fail("socket error during recv")
            }
            if (n == 0) {
                for <-
            }
            buf.push_bytes(raw_chunk, n)

            if (not sep_found) {
                let sep? = buf.index_of("\r\n\r\n")
                if (sep? != none) {
                    sep_found = true
                    let header_end = sep? ?? 0
                    let header_text = buf[0..header_end].to_lower()
                    let cl_idx? = header_text.index_of("content-length:")
                    if (cl_idx? != none) {
                        let cl_start = (cl_idx? ?? 0) + 15
                        let rest = header_text[cl_start..header_text.len()]
                        let line_end? = rest.index_of("\r\n")
                        let cl_val_str = if (line_end? != none) rest[0..(line_end? ?? 0)].trim() else rest.trim()
                        let cl? = parse_int(cl_val_str)
                        if (cl? != none) {
                            total_needed = header_end + 4 + (cl? ?? 0)
                        }
                    } else {
                        let first_line_end? = header_text.index_of("\r\n")
                        let first_line = if (first_line_end? != none) header_text[0..(first_line_end? ?? 0)] else header_text
                        if (first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with("delete ") or first_line.starts_with("options ")) {
                            total_needed = header_end + 4
                        }
                    }
                } else {
                    let lf_sep? = buf.index_of("\n\n")
                    if (lf_sep? != none) {
                        sep_found = true
                        let header_end = lf_sep? ?? 0
                        let header_text = buf[0..header_end].to_lower()
                        let cl_idx? = header_text.index_of("content-length:")
                        if (cl_idx? != none) {
                            let cl_start = (cl_idx? ?? 0) + 15
                            let rest = header_text[cl_start..header_text.len()]
                            let line_end? = rest.index_of("\n")
                            let cl_val_str = if (line_end? != none) rest[0..(line_end? ?? 0)].trim() else rest.trim()
                            let cl? = parse_int(cl_val_str)
                            if (cl? != none) {
                                total_needed = header_end + 2 + (cl? ?? 0)
                            }
                        } else {
                            let first_line_end? = header_text.index_of("\n")
                            let first_line = if (first_line_end? != none) header_text[0..(first_line_end? ?? 0)] else header_text
                            if (first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with("delete ") or first_line.starts_with("options ")) {
                                total_needed = header_end + 2
                            }
                        }
                    }
                }
            }

            if (total_needed >= 0 and buf.len() >= total_needed) {
                for <-
            }
        }

        free_raw(raw_chunk)
    }

    if (buf.is_empty()) {
        Result[string, string].fail("empty HTTP message")
    } else {
        Result[string, string].ok(own buf)
    }
}

// TlsSession wraps an active TLS session and ensures proper cleanup via Drop.
define TlsSession struct [
    raw: ptr[u8],
]

bind TlsSession.drop = (&self) {
    unsafe {
        if (self.raw != null_ptr[u8]()) {
            ll_tls_close(self.raw)
            self.raw = null_ptr[u8]()
        }
    }
}

bind TlsSession.close = (&self) {
    unsafe {
        if (self.raw != null_ptr[u8]()) {
            ll_tls_close(self.raw)
            self.raw = null_ptr[u8]()
        }
    }
}

bind TlsSession.send = (&self, data: string): Result[int, string] {
    let is_null = unsafe (self.raw == null_ptr[u8]())
    if (is_null) {
        func <- Result[int, string].fail("send on closed TLS session")
    }
    let res = unsafe ll_tls_send_all(self.raw, data.c_str(), data.len())
    if (res == 0) {
        Result[int, string].ok(data.len())
    } else {
        Result[int, string].fail("failed to send data over TLS")
    }
}

bind read_http_message_tls = (session: ptr[u8], timeout_ms: int): Result[string, string] {
    let buf = ""
    let chunk_size = 4096
    let total_needed = -1
    let sep_found = false

    unsafe {
        let raw_chunk = alloc_raw[u8](chunk_size)

        for (true) {
            let n = ll_tls_recv(session, raw_chunk, chunk_size, timeout_ms)
            if (n < 0) {
                free_raw(raw_chunk)
                if (n == -2) func <- Result[string, string].fail("timeout waiting for HTTP data over TLS")
                func <- Result[string, string].fail("TLS error during recv")
            }
            if (n == 0) {
                for <-
            }
            buf.push_bytes(raw_chunk, n)

            if (not sep_found) {
                let sep? = buf.index_of("\r\n\r\n")
                if (sep? != none) {
                    sep_found = true
                    let header_end = sep? ?? 0
                    let header_text = buf[0..header_end].to_lower()
                    let cl_idx? = header_text.index_of("content-length:")
                    if (cl_idx? != none) {
                        let cl_start = (cl_idx? ?? 0) + 15
                        let rest = header_text[cl_start..header_text.len()]
                        let line_end? = rest.index_of("\r\n")
                        let cl_val_str = if (line_end? != none) rest[0..(line_end? ?? 0)].trim() else rest.trim()
                        let cl? = parse_int(cl_val_str)
                        if (cl? != none) {
                            total_needed = header_end + 4 + (cl? ?? 0)
                        }
                    } else {
                        let first_line_end? = header_text.index_of("\r\n")
                        let first_line = if (first_line_end? != none) header_text[0..(first_line_end? ?? 0)] else header_text
                        if (first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with("delete ") or first_line.starts_with("options ")) {
                            total_needed = header_end + 4
                        }
                    }
                } else {
                    let lf_sep? = buf.index_of("\n\n")
                    if (lf_sep? != none) {
                        sep_found = true
                        let header_end = lf_sep? ?? 0
                        let header_text = buf[0..header_end].to_lower()
                        let cl_idx? = header_text.index_of("content-length:")
                        if (cl_idx? != none) {
                            let cl_start = (cl_idx? ?? 0) + 15
                            let rest = header_text[cl_start..header_text.len()]
                            let line_end? = rest.index_of("\n")
                            let cl_val_str = if (line_end? != none) rest[0..(line_end? ?? 0)].trim() else rest.trim()
                            let cl? = parse_int(cl_val_str)
                            if (cl? != none) {
                                total_needed = header_end + 2 + (cl? ?? 0)
                            }
                        } else {
                            let first_line_end? = header_text.index_of("\n")
                            let first_line = if (first_line_end? != none) header_text[0..(first_line_end? ?? 0)] else header_text
                            if (first_line.starts_with("get ") or first_line.starts_with("head ") or first_line.starts_with("delete ") or first_line.starts_with("options ")) {
                                total_needed = header_end + 2
                            }
                        }
                    }
                }
            }

            if (total_needed >= 0 and buf.len() >= total_needed) {
                for <-
            }
        }

        free_raw(raw_chunk)
    }

    if (buf.is_empty()) {
        Result[string, string].fail("empty HTTP message")
    } else {
        Result[string, string].ok(own buf)
    }
}

// ============================================================================
// 8. HTTP Client
// ============================================================================

define Client struct [
    timeout_ms: int,
    headers: #[string: string],
]

bind Client.new = (): Client {
    Client[
        timeout_ms: 15000,
        headers: #[],
    ]
}

bind Client.do = (self, req: Request): Result[Response, string] {
    let url_res = parse_url(req.url)
    if (url_res.is_err()) {
        func <- Result[Response, string].fail("invalid request URL: ${req.url}")
    }
    let u = url_res.unwrap()
    let is_tls = (u.scheme == "https" or u.port == 443)
    let host = if (not u.hostname.is_empty()) u.hostname.copy() else "127.0.0.1"
    let port = if (u.port > 0) u.port else if (is_tls) 443 else 80

    let mut_req = req.copy()
    if (mut_req.header("host") == none) {
        mut_req.set_header("Host", if (port == 80 or port == 443) host.copy() else "${host}:${port}")
    }
    if (mut_req.header("user-agent") == none) {
        mut_req.set_header("User-Agent", "Lowlight-HTTP/1.0")
    }
    if (mut_req.header("accept") == none) {
        mut_req.set_header("Accept", "*/*")
    }
    if (mut_req.header("connection") == none) {
        mut_req.set_header("Connection", "close")
    }
    if (not mut_req.body.is_empty() and mut_req.header("content-length") == none) {
        mut_req.set_header("Content-Length", "${mut_req.body.len()}")
    }
    for (k, v in self.headers) {
        if (mut_req.header(k) == none) {
            mut_req.set_header(k.copy(), v.copy())
        }
    }

    let wire = mut_req.format()
    let stream_res = net.TcpStream.connect(host, port, self.timeout_ms)
    if (stream_res.is_err()) {
        func <- Result[Response, string].fail(stream_res.err?.copy() ?? "connection failed")
    }
    let stream = stream_res.unwrap()

    if (is_tls) {
        let avail = unsafe ll_tls_available()
        if (avail == 0) {
            func <- Result[Response, string].fail("HTTPS requires libssl (OpenSSL 1.1 / 3.0), which could not be loaded dynamically")
        }
        let raw_sess = unsafe ll_tls_client_handshake(stream.fd, host.c_str())
        let is_null = unsafe (raw_sess == null_ptr[u8]())
        if (is_null) {
            func <- Result[Response, string].fail("TLS handshake failed connecting to ${host}:${port}")
        }
        let tls_sess = TlsSession[raw: raw_sess]

        let send_res = tls_sess.send(wire)
        if (send_res.is_err()) {
            func <- Result[Response, string].fail("failed to send HTTP request over TLS")
        }

        let msg_res = read_http_message_tls(tls_sess.raw, self.timeout_ms)
        if (msg_res.is_err()) {
            func <- Result[Response, string].fail(msg_res.err?.copy() ?? "failed to read HTTP response over TLS")
        }
        let raw_resp = msg_res.unwrap()
        Response.parse(raw_resp)
    } else {
        let send_res = stream.send(wire)
        if (send_res.is_err()) {
            func <- Result[Response, string].fail("failed to send HTTP request")
        }

        let msg_res = read_http_message(stream.fd, self.timeout_ms)
        if (msg_res.is_err()) {
            func <- Result[Response, string].fail(msg_res.err?.copy() ?? "failed to read HTTP response")
        }
        let raw_resp = msg_res.unwrap()
        Response.parse(raw_resp)
    }
}

bind Client.get = (self, url: string): Result[Response, string] {
    let req = Request.new("GET", url, "")
    self.do(req)
}

bind Client.post = (self, url: string, content_type: string, body: string): Result[Response, string] {
    let req = Request.new("POST", url, body)
    req.set_header("Content-Type", content_type)
    self.do(req)
}

bind get = (url: string): Result[Response, string] {
    let c = Client.new()
    c.get(url)
}

bind post = (url: string, content_type: string, body: string): Result[Response, string] {
    let c = Client.new()
    c.post(url, content_type, body)
}

// ============================================================================
// 9. HTTP Server
// ============================================================================

define Server struct [
    addr: string,
    port: int,
    listener_fd: int,
    read_timeout_ms: int,
    write_timeout_ms: int,
]

bind Server.new = (addr: string): Server {
    Server[
        addr: addr.copy(),
        port: 0,
        listener_fd: -1,
        read_timeout_ms: 10000,
        write_timeout_ms: 10000,
    ]
}

bind Server.drop = (&self) {
    self.close()
}

bind Server.close = (&self) {
    if (self.listener_fd >= 0) {
        unsafe { net.ll_net_close(self.listener_fd) }
        self.listener_fd = -1
    }
}

bind Server.listen = (&self): Result[int, string] {
    let host = "0.0.0.0"
    let port = 8080

    if (not self.addr.is_empty()) {
        let colon? = self.addr.index_of(":")
        if (colon?) {
            let h = self.addr[0..colon].trim()
            if (not h.is_empty()) {
                host = h
            }
            let p_str = self.addr[(colon + 1)..self.addr.len()].trim()
            let p? = parse_int(p_str)
            if (p? != none) {
                port = p? ?? 8080
            }
        } else {
            let p? = parse_int(self.addr)
            if (p? != none) {
                port = p? ?? 8080
            } else {
                host = self.addr.copy()
            }
        }
    }

    let sfd = unsafe {
        net.ll_net_tcp_listen(host.c_str(), port, 128)
    }
    if (sfd < 0) {
        func <- Result[int, string].fail("failed to bind server on ${self.addr}")
    }
    self.listener_fd = sfd
    let actual_port = unsafe { net.ll_net_tcp_port(sfd) }
    self.port = if (actual_port > 0) actual_port else port
    Result[int, string].ok(self.port)
}

bind Server.serve_conn = (&self, conn_fd: int, handler: (Request, &ResponseWriter)): bool {
    let raw_req_res = read_http_message(conn_fd, self.read_timeout_ms)
    if (raw_req_res.is_err()) {
        unsafe { net.ll_net_close(conn_fd) }
        func <- false
    }
    let raw_req = raw_req_res.unwrap()
    let req_res = Request.parse(raw_req)
    if (req_res.is_err()) {
        let bad_resp = "HTTP/1.1 400 Bad Request\r\nContent-Length: 15\r\nConnection: close\r\n\r\n400 Bad Request"
        unsafe {
            net.ll_net_send_all(conn_fd, bad_resp.c_str(), bad_resp.len())
            net.ll_net_close(conn_fd)
        }
        func <- false
    }
    let req = req_res.unwrap()
    let rw = ResponseWriter.new()
    handler(req, &rw)

    let resp = rw.finish()
    let wire = resp.format()
    unsafe {
        net.ll_net_send_all(conn_fd, wire.c_str(), wire.len())
        net.ll_net_close(conn_fd)
    }
    true
}

bind Server.serve_one = (&self, handler: (Request, &ResponseWriter), timeout_ms: int): Result[bool, string] {
    if (self.listener_fd < 0) {
        let lr = self.listen()
        if (lr.is_err()) {
            func <- Result[bool, string].fail(lr.err?.copy() ?? "listen failed")
        }
    }
    let client_fd = unsafe {
        net.ll_net_tcp_accept(self.listener_fd, timeout_ms)
    }
    if (client_fd < 0) {
        if (client_fd == -2) func <- Result[bool, string].fail("timeout waiting for connection")
        func <- Result[bool, string].fail("accept error")
    }
    let ok = self.serve_conn(client_fd, handler)
    Result[bool, string].ok(ok)
}

bind Server.serve = (&self, handler: (Request, &ResponseWriter), max_requests: int): Result[int, string] {
    if (self.listener_fd < 0) {
        let lr = self.listen()
        if (lr.is_err()) {
            func <- Result[int, string].fail(lr.err?.copy() ?? "listen failed")
        }
    }
    let served = 0
    for (max_requests <= 0 or served < max_requests) {
        let client_fd = unsafe {
            net.ll_net_tcp_accept(self.listener_fd, -1)
        }
        if (client_fd >= 0) {
            let ok = self.serve_conn(client_fd, handler)
            if (ok) {
                served += 1
            }
        }
    }
    Result[int, string].ok(served)
}

bind listen_and_serve = (addr: string, handler: (Request, &ResponseWriter)): Result[bool, string] {
    let s = Server.new(addr)
    let res = s.serve(handler, 0)
    if (res.is_ok()) {
        Result[bool, string].ok(true)
    } else {
        Result[bool, string].fail(res.err?.copy() ?? "server error")
    }
}

bind listen_and_serve_mux = (addr: string, mux: ServeMux): Result[bool, string] {
    bind mux_copy = mux.copy()
    listen_and_serve(addr, (req: Request, res: &ResponseWriter) {
        mux_copy.serve_http(req, &res)
    })
}

// ============================================================================
// 10. Testing Utilities (ResponseRecorder & Mock Transport)
// ============================================================================

define ResponseRecorder struct [
    writer: ResponseWriter,
]

bind ResponseRecorder.new = (): ResponseRecorder {
    ResponseRecorder[writer: ResponseWriter.new()]
}

bind ResponseRecorder.result = (self): Response {
    self.writer.finish()
}

// test_handler directly executes an HTTP handler function against a request in memory.
bind test_handler = (handler: (Request, &ResponseWriter), req: Request): Response {
    let rw = ResponseWriter.new()
    handler(req, &rw)
    rw.finish()
}

// test_roundtrip tests client and server logic over a kernel socketpair without external network ports.
bind test_roundtrip = (handler: (Request, &ResponseWriter), req: Request): Result[Response, string] {
    let pair_res = net.socketpair()
    if (pair_res.is_err()) {
        func <- Result[Response, string].fail("socketpair failed")
    }
    let pair = pair_res.unwrap()

    let req_wire = req.format()
    let send_res = pair.first.send(req_wire)
    if (send_res.is_err()) {
        func <- Result[Response, string].fail("failed to send request to socketpair")
    }

    let srv_req_res = read_http_message(pair.second.fd, 2000)
    if (srv_req_res.is_err()) {
        func <- Result[Response, string].fail("server failed to read request")
    }
    let parsed_req_res = Request.parse(srv_req_res.unwrap())
    if (parsed_req_res.is_err()) {
        func <- Result[Response, string].fail("server failed to parse request")
    }
    let srv_req = parsed_req_res.unwrap()

    let rw = ResponseWriter.new()
    handler(srv_req, &rw)
    let resp = rw.finish()
    let resp_wire = resp.format()

    let srv_send_res = pair.second.send(resp_wire)
    if (srv_send_res.is_err()) {
        func <- Result[Response, string].fail("server failed to send response")
    }

    let client_read_res = read_http_message(pair.first.fd, 2000)
    if (client_read_res.is_err()) {
        func <- Result[Response, string].fail("client failed to read response")
    }
    let client_wire = client_read_res.unwrap()
    Response.parse(client_wire)
}
