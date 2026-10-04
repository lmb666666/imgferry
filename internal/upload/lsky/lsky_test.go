package lsky

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lmb666666/imgferry/internal/upload"
)

func TestValidateAndUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/profile":
			if got := r.Header.Get("Authorization"); got != "Bearer 1|test" {
				t.Errorf("profile Authorization = %q", got)
			}
			fmt.Fprint(w, `{"status":true,"message":"success"}`)
		case "/api/v1/upload":
			if got := r.Header.Get("Authorization"); got != "Bearer 1|test" {
				t.Errorf("upload Authorization = %q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			} else if len(r.MultipartForm.File["file"]) != 1 {
				t.Error("缺少 multipart file 字段")
			}
			fmt.Fprint(w, `{"status":true,"message":"上传成功","data":{"links":{"url":"https://img.test/i/abc.png"}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a, err := New(Config{BaseURL: srv.URL, Token: "1|test"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Name() != "lsky" {
		t.Fatalf("Name = %q", a.Name())
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got, err := a.Upload("a.png", []byte("data"), "image/png")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got != "https://img.test/i/abc.png" {
		t.Fatalf("Upload url = %q", got)
	}
}

func TestTokenExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tokens":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"email":"a@b.c"`) ||
				!strings.Contains(string(body), `"password":"pw"`) {
				t.Errorf("tokens body = %s", body)
			}
			fmt.Fprint(w, `{"status":true,"message":"success","data":{"token":"1|fresh"}}`)
		case "/api/v1/profile":
			if got := r.Header.Get("Authorization"); got != "Bearer 1|fresh" {
				t.Errorf("profile Authorization = %q, want exchanged token", got)
			}
			fmt.Fprint(w, `{"status":true,"message":"success"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a, err := New(Config{BaseURL: srv.URL, Email: "a@b.c", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a.cfg.Token != "1|fresh" {
		t.Fatalf("token not exchanged: %q", a.cfg.Token)
	}
}

func TestUploadFailurePassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"status":false,"message":"操作频繁，请稍后再试"}`)
	}))
	defer srv.Close()

	a, _ := New(Config{BaseURL: srv.URL, Token: "1|test"})
	_, err := a.Upload("a.png", []byte("d"), "image/png")
	if err == nil || !strings.Contains(err.Error(), "操作频繁") {
		t.Fatalf("want rate-limit message passthrough, got %v", err)
	}
}

func TestUploadServerExceptionHintsStrategy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/upload":
			fmt.Fprint(w, `{"status":false,"message":"服务异常，请稍后再试"}`)
		case "/api/v1/strategies":
			fmt.Fprint(w, `{"status":true,"message":"success","data":{"strategies":[{"id":8,"name":"游客储存"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a, _ := New(Config{BaseURL: srv.URL, Token: "1|test"})
	_, err := a.Upload("a.png", []byte("d"), "image/png")
	if err == nil || !strings.Contains(err.Error(), "8=游客储存") {
		t.Fatalf("want strategy hint in error, got %v", err)
	}
	if upload.IsRateLimited(err) {
		t.Fatal("服务异常不应被判定为频控（会触发无意义重试）")
	}
}

func TestUploadStringStatusErrorShape(t *testing.T) {
	// 部分部署（如 PicUI）错误响应的 status 是字符串 "error"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"status":"error","message":"Unauthenticated.","time":1791016086}`)
	}))
	defer srv.Close()

	a, _ := New(Config{BaseURL: srv.URL, Token: "bad"})
	err := a.Validate()
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated.") {
		t.Fatalf("want original message passthrough, got %v", err)
	}
}

func TestNewConfigValidation(t *testing.T) {
	if _, err := New(Config{BaseURL: "img.example.com", Token: "x"}); err == nil {
		t.Fatal("want error for missing scheme")
	}
	if _, err := New(Config{BaseURL: "https://x.com", Token: "x", APIVersion: "v2"}); err == nil {
		t.Fatal("want error for unsupported api_version")
	}
}
