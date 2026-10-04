package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchSuccessWithHeaders(t *testing.T) {
	var gotReferer, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "\x89PNG fake-data")
	}))
	defer srv.Close()

	c := New(Options{Headers: map[string]string{"Referer": "https://old.example.com/"}})
	f, err := c.Fetch(context.Background(), srv.URL+"/img/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if gotReferer != "https://old.example.com/" {
		t.Fatalf("Referer = %q", gotReferer)
	}
	if gotUA != "imgferry/0.1 (+https://github.com/lmb666666/imgferry)" {
		t.Fatalf("default UA not set: %q", gotUA)
	}
	if f.Name != "a.png" {
		t.Fatalf("Name = %q", f.Name)
	}
}

func TestFetchRetryOn500(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "png")
	}))
	defer srv.Close()

	c := New(Options{MaxRetries: 2, Timeout: time.Second})
	f, err := c.Fetch(context.Background(), srv.URL+"/b.png")
	if err != nil {
		t.Fatalf("want success after retry, got %v", err)
	}
	if f.Name != "b.png" {
		t.Fatalf("Name = %q", f.Name)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestFetch404NoRetry(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(Options{MaxRetries: 3})
	_, err := c.Fetch(context.Background(), srv.URL+"/gone.png")
	if err == nil {
		t.Fatal("want error for 404")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("404 should not retry, attempts = %d", attempts)
	}
}

func TestDeriveNameFromContentType(t *testing.T) {
	// URL 无扩展名 → 按 Content-Type 补齐
	name, err := deriveName("https://host/files/pic", "image/jpeg", nil)
	if err != nil || name != "pic.jpg" {
		t.Fatalf("got %q, %v", name, err)
	}
	// 连路径都没有 → 用内容嗅探
	name, err = deriveName("https://host/", "application/octet-stream",
		[]byte("\x89PNG\r\n\x1a\nrest"))
	if err != nil || name != "image.png" {
		t.Fatalf("got %q, %v", name, err)
	}
	// 完全无法判断 → 报错
	if _, err := deriveName("https://host/mystery", "text/plain", []byte("hello")); err == nil {
		t.Fatal("want error for undetectable extension")
	}
}
