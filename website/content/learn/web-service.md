---
title: "Tutorial: a web service"
nav: A web service
summary: Build a small HTTP API with std/http, test its handlers in-process, and serve it.
order: 4
group: Tutorials
---

# Tutorial: a web service

`std/http` contains an HTTP/1.1 client and server. In this tutorial you'll build a small key-value API:

| Request | Does |
|---|---|
| `GET /health` | responds `ok` |
| `GET /greet?name=Ada` | responds `hello, Ada` |
| `GET /items` | lists the stored items |
| `POST /items` with a body | stores the body as a new item |

You'll test every handler without opening a socket, then serve the API for real.

## 1. Handlers and a router

A handler is a function that receives the request and a `ResponseWriter` to fill in. A `ServeMux` routes paths to handlers:

```lowlight
import "std/http"

bind main = fn(): {
	let mux = http.ServeMux.new()
	mux.handle_func("/health", fn(req: http.Request, res: &http.ResponseWriter): res.write("ok\n"))
	mux.handle_func("/greet", fn(req: http.Request, res: &http.ResponseWriter): {
		let name = req.query_param("name") ?? "world"
		res.set_header("Content-Type", "text/plain; charset=utf-8")
		res.write("hello, $name\n")
	})

	// Exercise the router directly: no network needed.
	let w = http.ResponseWriter.new()
	mux.serve_http(http.Request.new("GET", "/greet?name=Ada", ""), &w)
	let resp = w.finish()
	print("${resp.status_code} ${resp.body}")
}
```

```output
200 hello, Ada
```

- The writer is passed as `&http.ResponseWriter`: the handler changes it, and the `&` in the type says so.
- `req.query_param` returns an optional, because the parameter may be absent.
- `mux.serve_http(request, &writer)` is exactly what the server calls for each connection. Calling it yourself is how you test handlers.

## 2. State shared between requests

Handlers are closures. A closure **moves** the values it captures, and a captured value is read-only inside it, so mutable state must live somewhere the handler can reach by reference. The simplest choice for a single-threaded server is a small store type whose methods take `&self`, owned by the closure that uses it.

Here the store is captured by the one handler that serves both methods of `/items`:

```lowlight
import "std/http"

define Store struct [
	items: [string],
]

bind Store.add = fn(&self, item: string): self.items.push(item.copy())

bind Store.render = fn(self) string: {
	let out = ""
	for(i, item in self.items): out.push("${i + 1}. $item\n")
	out
}

bind route_items = fn(store: &Store, req: http.Request, res: &http.ResponseWriter): {
	match(req.method):
		when "GET": res.write(store.render())
		when "POST": {
			if(req.body.is_empty()): {
				res.write_header(http.STATUS_BAD_REQUEST)
				res.write("empty item\n")
				func <-
			}
			store.add(req.body)
			res.write_header(http.STATUS_CREATED)
			res.write("stored\n")
		}
		default: res.write_header(http.STATUS_METHOD_NOT_ALLOWED)
}

bind call = fn(store: &Store, method: string, path: string, body: string): {
	let w = http.ResponseWriter.new()
	route_items(&store, http.Request.new(method, path, body), &w)
	let r = w.finish()
	print("$method $path -> ${r.status_code} ${r.body}")
	if(r.body.is_empty()): println()
}

bind main = fn(): {
	let store = Store[items: []]
	call(&store, "POST", "/items", "milk")
	call(&store, "POST", "/items", "bread")
	call(&store, "POST", "/items", "")
	call(&store, "GET", "/items", "")
	call(&store, "DELETE", "/items", "")
}
```

```output
POST /items -> 201 stored
POST /items -> 201 stored
POST /items -> 400 empty item
GET /items -> 200 1. milk
2. bread
DELETE /items -> 405 
```

`route_items` takes the store as `&Store`, so it is an ordinary function you can test directly, as `main` does here. Even passing along a borrow you were given is marked: `call` writes `route_items(&store, ...)`. Notice how every effect is visible: `store.add` changes the store through `&self`, `res.write_header` changes the writer, and nothing else can.

## 3. Serve it

To serve for real, register the handlers and call `http.listen_and_serve_mux`. The store moves into the `/items` closure, which owns it for the life of the server:

```lowlight no-run
import "std/http"

define Store struct [
	items: [string],
]

bind Store.add = fn(&self, item: string): self.items.push(item.copy())

bind Store.render = fn(self) string: {
	let out = ""
	for(i, item in self.items): out.push("${i + 1}. $item\n")
	out
}

bind main = fn() int: {
	let mux = http.ServeMux.new()
	mux.handle_func("/health", fn(req: http.Request, res: &http.ResponseWriter): res.write("ok\n"))
	mux.handle_func("/greet", fn(req: http.Request, res: &http.ResponseWriter): {
		let name = req.query_param("name") ?? "world"
		res.write("hello, $name\n")
	})
	let addr = "127.0.0.1:8080"
	println("listening on http://$addr")
	let served = http.listen_and_serve_mux(addr, mux):
		default err(own e): {
			eprintln(e.wrap("serving $addr").summary())
			func <- 1
		}
	0
}
```

```bash
lowlight run server.ll
curl 'localhost:8080/greet?name=Ada'
```

`listen_and_serve_mux` returns a `Result`, because binding the port can fail (for example, when another program already uses it). The result chain turns that into a message and an exit status.

## 4. Calling other services

The client side mirrors the server:

```lowlight no-run
import "std/http"

bind main = fn() int: {
	let client = http.Client.new()
	let resp = client.get("http://127.0.0.1:8080/health"):
		default err(own e): {
			eprintln(e.summary())
			func <- 1
		}
	println("${resp.status_code}: ${resp.text()}")
	0
}
```

`std/http` also supports HTTPS (through the system's OpenSSL, loaded at run time), request headers, `post`, `put`, and `patch`. See the [package reference](/std/http.html).

## What you practiced

- Closures as handlers, and what capturing means for ownership.
- `&` parameters as the visible way to change a writer or a store.
- Testing handlers in-process by calling `serve_http` yourself.
- Turning a `Result` from the outside world into a clear message at the program's edge.

## Exercises

1. Add `DELETE /items?index=N`, responding `404` when the index is out of range.
2. Respond with JSON when the request has `Accept: application/json` (`req.header("Accept")`).
3. Write `@test` functions for `route_items` that check status codes and bodies.
