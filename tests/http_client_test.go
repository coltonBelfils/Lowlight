package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lowlight/internal/driver"
)

func TestHTTPClientPutPatchDeleteEndToEnd(t *testing.T) {
	t.Parallel()
	// 1. Both LLVM and C11 strict parity + leak check on Client/module helpers and roundtrips.
	strictSrc := `
import "std/http"

bind main = fn(): {
	let handler = cl(req: http.Request, res: &http.ResponseWriter): {
		let tag = req.query_param("tag") ?? ""
		let ct = req.header("content-type") ?? ""
		res.write_header(http.STATUS_OK)
		res.write("${req.method}|${tag}|${ct}|${req.body}")
	}

	let r1 = http.Request.new("PUT", "http://example.com/items/1?tag=v1", "{\"ok\":true}")
	r1.set_header("Content-Type", "application/json")
	let resp1 = http.test_roundtrip(handler, r1).unwrap()
	assert(resp1.body == "PUT|v1|application/json|{\"ok\":true}")

	let r2 = http.Request.new("PATCH", "http://example.com/items/1?tag=v2", "delta")
	r2.set_header("Content-Type", "text/plain")
	let resp2 = http.test_roundtrip(handler, r2).unwrap()
	assert(resp2.body == "PATCH|v2|text/plain|delta")

	let r3 = http.Request.new("DELETE", "http://example.com/items/1?tag=v3", "")
	let resp3 = http.test_roundtrip(handler, r3).unwrap()
	assert(resp3.body == "DELETE|v3||")

	// Also exercise Client.put / Client.patch / Client.delete and module helpers on closed port / invalid URL (0 leaks).
	let c = http.Client.new()
	assert(c.put("://bad", "text/plain", "x").is_err())
	assert(c.patch("://bad", "text/plain", "x").is_err())
	assert(c.delete("://bad").is_err())
	assert(http.put("127.0.0.1:0", "text/plain", "x").is_err())
	println("ok_client_strict")
}
`
	if out := runInlineStrict(t, "http_client_methods_strict", strictSrc, false); strings.TrimSpace(out) != "ok_client_strict" {
		t.Fatalf("unexpected stdout: %q", out)
	}

	// 2. Full wire verification of Client.put, Client.patch, Client.delete, http.put, http.patch, http.delete
	// via an in-process UNIX socketpair server stubbing ll_net_tcp_connect (sandbox-safe).
	clientWireSrc := `
import "std/http"

bind main = fn(): {
	let c = http.Client.new()

	let r_put = c.put("http://example.test/items/1?tag=v1", "application/json", "{\"ok\":true}").unwrap()
	assert(r_put.status_code == 200)
	assert(r_put.body.starts_with("PUT /items/1?tag=v1 HTTP/1.1\r\n"))
	assert(r_put.body.contains("Content-Type: application/json\r\n"))
	assert(r_put.body.ends_with("\r\n\r\n{\"ok\":true}"))

	let r_patch = c.patch("http://example.test/items/1?tag=v2", "text/plain", "delta").unwrap()
	assert(r_patch.status_code == 200)
	assert(r_patch.body.starts_with("PATCH /items/1?tag=v2 HTTP/1.1\r\n"))
	assert(r_patch.body.contains("Content-Type: text/plain\r\n"))
	assert(r_patch.body.ends_with("\r\n\r\ndelta"))

	let r_del = c.delete("http://example.test/items/1?tag=v3").unwrap()
	assert(r_del.status_code == 200)
	assert(r_del.body.starts_with("DELETE /items/1?tag=v3 HTTP/1.1\r\n"))
	assert(r_del.body.ends_with("\r\n\r\n"))

	let t_put = http.put("http://example.test/one?tag=top1", "text/plain", "a").unwrap()
	assert(t_put.body.starts_with("PUT /one?tag=top1 HTTP/1.1\r\n"))
	assert(t_put.body.ends_with("\r\n\r\na"))

	let t_patch = http.patch("http://example.test/one?tag=top2", "text/plain", "b").unwrap()
	assert(t_patch.body.starts_with("PATCH /one?tag=top2 HTTP/1.1\r\n"))
	assert(t_patch.body.ends_with("\r\n\r\nb"))

	let t_del = http.delete("http://example.test/one?tag=top3").unwrap()
	assert(t_del.body.starts_with("DELETE /one?tag=top3 HTTP/1.1\r\n"))

	println("ok_client_wire")
}
`
	root := projectRoot(t)
	llvmSrc, _, err := driver.EmitLLVM(driver.Options{
		EntryPath:   filepath.Join(t.TempDir(), "http_client_wire.ll"),
		SrcOverride: clientWireSrc,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("EmitLLVM failed: %v", err)
	}
	patchedLL := strings.ReplaceAll(llvmSrc, "call i64 @ll_net_tcp_connect(", "call i64 @mock_tcp_connect(")
	if patchedLL == llvmSrc {
		t.Fatal("expected LLVM IR to contain @ll_net_tcp_connect call")
	}
	patchedLL += "\ndeclare i64 @mock_tcp_connect(ptr, i64, i64)\n"

	cDriver := `
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static void* echo_http_worker(void* arg) {
    int fd = *(int*)arg;
    free(arg);
    char req[16384];
    size_t total = 0;
    size_t header_end = 0;
    int have_hdr = 0;
    size_t content_len = 0;

    while (total < sizeof(req) - 1) {
        ssize_t n = recv(fd, req + total, sizeof(req) - 1 - total, 0);
        if (n <= 0) break;
        total += (size_t)n;
        req[total] = '\0';
        if (!have_hdr) {
            char* sep = strstr(req, "\r\n\r\n");
            if (sep) {
                have_hdr = 1;
                header_end = (size_t)(sep - req) + 4;
                const char* cl = strstr(req, "\r\ncontent-length:");
                if (!cl) cl = strstr(req, "\r\nContent-Length:");
                if (cl && cl < sep) {
                    cl += 17;
                    while (*cl == ' ' || *cl == '\t') cl++;
                    content_len = (size_t)strtoll(cl, NULL, 10);
                }
            }
        }
        if (have_hdr && total >= header_end + content_len) {
            break;
        }
    }

    char resp_hdr[256];
    int hlen = snprintf(resp_hdr, sizeof(resp_hdr),
        "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %zu\r\nConnection: close\r\n\r\n",
        total);
    if (hlen > 0) {
        (void)send(fd, resp_hdr, (size_t)hlen, 0);
        if (total > 0) (void)send(fd, req, total, 0);
    }
    close(fd);
    return NULL;
}

int64_t mock_tcp_connect(const uint8_t* host, int64_t port, int64_t timeout_ms) {
    (void)host;
    (void)port;
    (void)timeout_ms;
    int fds[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds) != 0) return -2;
    int* peer_fd = (int*)malloc(sizeof(int));
    if (!peer_fd) {
        close(fds[0]);
        close(fds[1]);
        return -2;
    }
    *peer_fd = fds[1];
    pthread_t th;
    if (pthread_create(&th, NULL, echo_http_worker, peer_fd) != 0) {
        free(peer_fd);
        close(fds[0]);
        close(fds[1]);
        return -2;
    }
    pthread_detach(th);
    return (int64_t)fds[0];
}
`
	tmp := t.TempDir()
	llPath := filepath.Join(tmp, "http_client_wire.ll")
	drvPath := filepath.Join(tmp, "mock_tcp_drv.c")
	binPath := filepath.Join(tmp, "http_client_wire_bin")
	if err := os.WriteFile(llPath, []byte(patchedLL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drvPath, []byte(cDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	cc := resolveStrictCC()
	args := append([]string{"-Wno-override-module", "-g", "-O0", "-o", binPath, llPath, drvPath}, strictCFlags...)
	args = append(args, "-lm", "-ldl", "-lpthread")
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile failed: %v\n%s", err, string(out))
	}
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "LOWLIGHT_CHECK_LEAKS=1", "ASAN_OPTIONS=detect_leaks=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "ok_client_wire" {
		t.Fatalf("unexpected stdout: %q", string(out))
	}
}
