package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name, requestURL, host, port, proto, want string
	}{
		{"direct HTTP", "http://backend.example:8080/", "", "", "", "http://backend.example:8080"},
		{"direct TLS", "https://backend.example:8443/", "", "", "", "https://backend.example:8443"},
		{"forwarded host port wins", "http://backend:8080/", "pay.example:9443", "8443", "https", "https://pay.example:9443"},
		{"separate port", "http://backend:8080/", "pay.example", "8443", "https", "https://pay.example:8443"},
		{"proxy chain", "http://backend:8080/", " pay.example , internal:8080", " 8443 , 80", " HTTPS , http", "https://pay.example:8443"},
		{"proxy chain existing port", "http://backend/", "pay.example:9443, internal", "8443, 80", "https, http", "https://pay.example:9443"},
		{"first proto wins", "http://backend/", "pay.example", "8080", "http, https", "http://pay.example:8080"},
		{"fallback host", "http://backend.example/", "", "8443", "https", "https://backend.example:8443"},
		{"empty first host", "http://backend.example/", " , internal", "8443", "https", "https://backend.example:8443"},
		{"fallback existing port", "http://backend.example:8080/", "", "8443", "", "http://backend.example:8080"},
		{"IPv4", "http://backend/", "192.0.2.1", "8443", "https", "https://192.0.2.1:8443"},
		{"bracketed IPv6", "http://backend/", "[2001:db8::1]", "8443", "https", "https://[2001:db8::1]:8443"},
		{"bare IPv6", "http://backend/", "2001:db8::1", "8443", "https", "https://[2001:db8::1]:8443"},
		{"IPv6 existing port", "http://backend/", "[2001:db8::1]:9443", "8443", "https", "https://[2001:db8::1]:9443"},
		{"IPv6 proxy chain", "http://backend/", "[2001:db8::1], [::1]", "8443, 80", "https, http", "https://[2001:db8::1]:8443"},
		{"IPv6 without port", "http://backend/", "[2001:db8::1]", "", "https", "https://[2001:db8::1]"},
		{"bare IPv6 without port", "http://backend/", "2001:db8::1", "", "https", "https://[2001:db8::1]"},
		{"direct IPv6", "http://[2001:db8::1]:8080/", "", "", "", "http://[2001:db8::1]:8080"},
		{"missing port", "http://backend/", "pay.example", "", "https", "https://pay.example"},
		{"non-numeric port", "http://backend/", "pay.example", "bad", "https", "https://pay.example"},
		{"signed port", "http://backend/", "pay.example", "+8443", "https", "https://pay.example"},
		{"zero port", "http://backend/", "pay.example", "0", "https", "https://pay.example"},
		{"negative port", "http://backend/", "pay.example", "-1", "https", "https://pay.example"},
		{"out of range port", "http://backend/", "pay.example", "65536", "https", "https://pay.example"},
		{"minimum port", "http://backend/", "pay.example", "1", "https", "https://pay.example:1"},
		{"maximum port", "http://backend/", "pay.example", "65535", "https", "https://pay.example:65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &gin.Context{Request: httptest.NewRequest(http.MethodGet, tt.requestURL, nil)}
			c.Request.Header.Set("X-Forwarded-Host", tt.host)
			c.Request.Header.Set("X-Forwarded-Port", tt.port)
			c.Request.Header.Set("X-Forwarded-Proto", tt.proto)
			for _, path := range []string{"", "/api/payment/easypay/notify", "/api/payment/easypay/return", "/api/payment/easypay-v2/notify", "/api/payment/easypay-v2/return"} {
				got := buildRequestURL(c, path)
				if got != tt.want+path {
					t.Errorf("buildRequestURL(%q) = %q, want %q", path, got, tt.want+path)
				}
				if _, err := url.Parse(got); err != nil {
					t.Errorf("invalid generated URL %q: %v", got, err)
				}
			}
		})
	}
}

func TestEpayPaymentCallbackURLs(t *testing.T) {
	// Keep frontend URL resolution deterministic and avoid local port probing.
	t.Setenv("AUTO_PRO_FRONTEND_URL", "https://frontend.example:9443")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	merchantKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))

	for _, version := range []string{"easypay", "easypay-v2"} {
		for _, configured := range []string{"automatic", "explicit", "notify only", "return only"} {
			t.Run(version+"/"+configured, func(t *testing.T) {
				var notifyURL, returnURL string
				if configured == "explicit" || configured == "notify only" {
					notifyURL = "https://notify.example:9443/custom/notify?token=a%2Fb"
				}
				if configured == "explicit" || configured == "return only" {
					returnURL = "https://[2001:db8::2]:10443/custom/return?source=pay%2Btest"
				}
				wantNotify, wantReturn := notifyURL, returnURL
				if wantNotify == "" {
					wantNotify = "https://pay.example:8443/api/payment/" + version + "/notify"
				}
				if wantReturn == "" {
					wantReturn = "https://pay.example:8443/api/payment/" + version + "/return"
				}
				checkCallbacks := func(values url.Values) {
					t.Helper()
					if got := values.Get("notify_url"); got != wantNotify {
						t.Errorf("notify_url = %q, want %q", got, wantNotify)
					}
					if got := values.Get("return_url"); got != wantReturn {
						t.Errorf("return_url = %q, want %q", got, wantReturn)
					}
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "http://backend:8080/", nil)
				c.Request.Header.Set("X-Forwarded-Host", "pay.example, internal")
				c.Request.Header.Set("X-Forwarded-Port", "8443, 80")
				c.Request.Header.Set("X-Forwarded-Proto", "https, http")
				if version == "easypay" {
					payURL, _, err := buildEpaySubmitURL(c, epayConfig{
						Gateway: "https://gateway.example", PID: "test", Key: "test-key",
						NotifyURL: notifyURL, ReturnURL: returnURL,
					}, "TEST001", 100, "alipay", "test", "/user/dashboard")
					if err != nil {
						t.Fatal(err)
					}
					parsed, err := url.Parse(payURL)
					if err != nil {
						t.Fatal(err)
					}
					checkCallbacks(parsed.Query())
					return
				}
				// Only the external gateway is faked; URL assembly, signing and form encoding run normally.
				gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					checkCallbacks(r.PostForm)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"code":1,"payurl":"https://gateway.example/checkout"}`))
				}))
				defer gateway.Close()
				_, _, err := buildEpayV2Payment(c, epayV2Config{
					Gateway: gateway.URL, PID: "test", MerchantKey: merchantKey,
					NotifyURL: notifyURL, ReturnURL: returnURL,
				}, "TEST001", 100, "alipay", "test", "/user/dashboard")
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
