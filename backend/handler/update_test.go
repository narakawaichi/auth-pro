package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"auto_pro/config"
)

type onlineUpdateRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip onlineUpdateRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func validOnlineUpdateManifestForTest() *onlineUpdateManifest {
	return &onlineUpdateManifest{
		Version:    "1.0.1",
		Channel:    "stable",
		MinVersion: "0.0.0",
		Package: onlineUpdatePackage{
			OS:       runtime.GOOS,
			Arch:     runtime.GOARCH,
			FileName: "auth_pro-full-v1.0.1.tar.gz",
			URL:      "https://gitee.com/Zcy-sa/auth-pro/releases/download/v1.0.1/auth_pro-full-v1.0.1.tar.gz",
			SHA256:   strings.Repeat("a", 64),
			Size:     1024,
		},
		Actions: onlineUpdateActions{
			UpdateFrontend: true,
			UpdateBackend:  true,
			RestartBackend: true,
			BackupDatabase: true,
		},
	}
}

func TestValidateOnlineUpdateManifest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := validateOnlineUpdateManifest(validOnlineUpdateManifestForTest()); err != nil {
			t.Fatalf("validateOnlineUpdateManifest() error = %v", err)
		}
	})

	t.Run("placeholder URL", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.URL = "https://updates.your-domain.com/packages/update.tar.gz"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("placeholder URL was accepted")
		}
	})

	t.Run("invalid hash", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.SHA256 = "替换为真实SHA256"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("invalid SHA256 was accepted")
		}
	})

	t.Run("untrusted Gitee repository", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.URL = "https://gitee.com/another/repository/releases/download/v1.0.1/update.tar.gz"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("package from another Gitee repository was accepted")
		}
	})

	t.Run("untrusted third-party host", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.URL = "https://downloads.example.com/update.tar.gz"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("package from a third-party host was accepted")
		}
	})

	t.Run("non-standard HTTPS port", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.URL = "https://gitee.com:8443/Zcy-sa/auth-pro/releases/download/v1.0.1/update.tar.gz"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("non-standard HTTPS port was accepted")
		}
	})

	t.Run("insecure URL", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		manifest.Package.URL = "http://gitee.com/Zcy-sa/auth-pro/releases/download/v1.0.1/update.tar.gz"
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("HTTP package URL was accepted")
		}
	})

	t.Run("wrong architecture", func(t *testing.T) {
		manifest := validOnlineUpdateManifestForTest()
		if runtime.GOARCH == "amd64" {
			manifest.Package.Arch = "arm64"
		} else {
			manifest.Package.Arch = "amd64"
		}
		if err := validateOnlineUpdateManifest(manifest); err == nil {
			t.Fatal("incompatible architecture was accepted")
		}
	})
}

func TestOnlineUpdateGiteeRedirectPolicy(t *testing.T) {
	initialURL := "https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest"
	client, err := newOnlineUpdateHTTPClient(initialURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	via := []*http.Request{{URL: mustParseOnlineUpdateTestURL(t, initialURL)}}

	for name, target := range map[string]string{
		"Gitee release path":    "https://gitee.com/Zcy-sa/auth-pro/releases/download/v1.0.1/latest.json",
		"Gitee attachment path": "https://gitee.com/Zcy-sa/auth-pro/attach_files/123/download/latest.json",
		"Gitee asset storage":   "https://foruda.gitee.com/attach_file/123/latest.json?token=test",
	} {
		t.Run("allows "+name, func(t *testing.T) {
			if err := client.CheckRedirect(&http.Request{URL: mustParseOnlineUpdateTestURL(t, target)}, via); err != nil {
				t.Fatalf("trusted redirect was rejected: %v", err)
			}
		})
	}

	for name, target := range map[string]string{
		"HTTP downgrade":          "http://gitee.com/Zcy-sa/auth-pro/releases/download/v1.0.1/latest.json",
		"non-standard HTTPS port": "https://foruda.gitee.com:8443/attach_file/123/latest.json",
		"another repository":      "https://gitee.com/another/repository/releases/download/v1.0.1/latest.json",
		"untrusted storage host":  "https://example.com/update.tar.gz",
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if err := client.CheckRedirect(&http.Request{URL: mustParseOnlineUpdateTestURL(t, target)}, via); err == nil {
				t.Fatal("untrusted redirect was accepted")
			}
		})
	}
}

func TestFetchGiteeLatestManifestURL(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = onlineUpdateRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload string
		switch request.URL.Path {
		case "/api/v5/repos/Zcy-sa/auth-pro/releases/latest":
			payload = `{"id":123}`
		case "/api/v5/repos/Zcy-sa/auth-pro/releases/123/attach_files":
			payload = `[{"name":"latest.json","browser_download_url":"https://gitee.com/Zcy-sa/auth-pro/releases/download/v1.2.3/latest.json"}]`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})
	defer func() { http.DefaultTransport = originalTransport }()

	got, err := fetchGiteeLatestManifestURL("https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://gitee.com/Zcy-sa/auth-pro/releases/download/v1.2.3/latest.json"
	if got != want {
		t.Fatalf("manifest URL = %q, want %q", got, want)
	}
}

func TestParseOnlineUpdateVersionRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{"1", "1.2", "1.2.3-beta", "release-1.2.3", "1/../../backend"} {
		if _, ok := parseOnlineUpdateVersion(value); ok {
			t.Fatalf("unsafe version %q was accepted", value)
		}
	}
	for _, value := range []string{"1.2.3", "v1.2.3"} {
		if _, ok := parseOnlineUpdateVersion(value); !ok {
			t.Fatalf("valid version %q was rejected", value)
		}
	}
}

func TestParseOnlineUpdateURLAllowsCustomHTTPSMirrorPort(t *testing.T) {
	if _, err := parseOnlineUpdateURL("https://mirror.example.com:8443/latest.json"); err != nil {
		t.Fatalf("custom HTTPS mirror port was rejected: %v", err)
	}
}

func TestParseOnlineUpdateURLRejectsUntrustedGiteePaths(t *testing.T) {
	for _, value := range []string{
		"https://gitee.com/another/repository/releases/download/v1.0.1/latest.json",
		"https://gitee.com/Zcy-sa/auth-pro/raw/master/latest.json",
		"https://gitee.com:8443/Zcy-sa/auth-pro/releases/download/v1.0.1/latest.json",
	} {
		if _, err := parseOnlineUpdateURL(value); err == nil {
			t.Fatalf("untrusted Gitee URL %q was accepted", value)
		}
	}
}

func mustParseOnlineUpdateTestURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestValidateExtractedOnlineUpdatePackageAcceptsUTF8BOM(t *testing.T) {
	stagingDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stagingDir, "backend"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(stagingDir, "index.html"):          "index",
		filepath.Join(stagingDir, "version.json"):        `{"version":"1.0.5"}`,
		filepath.Join(stagingDir, "backend", "auth_pro"): "binary",
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{
		"version":"1.0.5",
		"frontendDir":".",
		"backendFile":"backend/auth_pro",
		"requiredFiles":[]
	}`)...)
	if err := os.WriteFile(filepath.Join(stagingDir, "manifest.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}

	pkg, err := validateExtractedOnlineUpdatePackage(stagingDir, "1.0.5")
	if err != nil {
		t.Fatalf("BOM manifest was rejected: %v", err)
	}
	if pkg.Version != "1.0.5" || pkg.FrontendDir != "." || pkg.BackendFile != "backend/auth_pro" {
		t.Fatalf("unexpected manifest: %#v", pkg)
	}
}

func TestOnlineUpdateHistory(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"releases": [
				{"version":"1.0.0","channel":"","releasedAt":"2026-01-01T00:00:00Z","notes":[" 首个版本 ",""]},
				{"version":"1.2.0","channel":"stable","releasedAt":"2026-03-01T00:00:00Z","notes":["功能更新"]},
				{"version":"1.0.0","channel":"stable","releasedAt":"2026-02-01T00:00:00Z","notes":["重复记录"]}
			]
		}`))
	}))
	defer server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()

	t.Setenv("AUTO_PRO_UPDATE_URL", server.URL+"/latest.json")
	manifest := validOnlineUpdateManifestForTest()
	manifest.ReleasesURL = server.URL + "/releases.json"

	releases, releasesURL, err := fetchOnlineUpdateReleases(manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if releasesURL != manifest.ReleasesURL {
		t.Fatalf("releases URL = %q, want %q", releasesURL, manifest.ReleasesURL)
	}
	if len(releases) != 2 || releases[0].Version != "1.2.0" || releases[1].Version != "1.0.0" {
		t.Fatalf("unexpected releases: %#v", releases)
	}
	if releases[1].Channel != "stable" || len(releases[1].Notes) != 1 || releases[1].Notes[0] != "首个版本" {
		t.Fatalf("release was not normalized: %#v", releases[1])
	}
}

func TestOnlineUpdateHistoryRejectsCrossOriginURL(t *testing.T) {
	t.Setenv("AUTO_PRO_UPDATE_URL", "https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest")
	manifest := validOnlineUpdateManifestForTest()
	manifest.ReleasesURL = "https://mirror.example.com/releases.json"
	if _, err := resolveOnlineUpdateReleasesURL(manifest); err == nil {
		t.Fatal("cross-origin releases URL was accepted")
	}
}

func TestOnlineUpdateAvailable(t *testing.T) {
	manifest := validOnlineUpdateManifestForTest()
	if available, versionErr := onlineUpdateAvailable("1.0.0", manifest); !available || versionErr != "" {
		t.Fatalf("onlineUpdateAvailable() = %v, %q", available, versionErr)
	}
	if available, versionErr := onlineUpdateAvailable("1.0.1", manifest); available || versionErr != "" {
		t.Fatalf("same version result = %v, %q", available, versionErr)
	}
	manifest.MinVersion = "1.0.0"
	if available, versionErr := onlineUpdateAvailable("0.9.9", manifest); available || versionErr == "" {
		t.Fatalf("minimum version was not enforced: %v, %q", available, versionErr)
	}
}

func TestOnlineUpdateJobResultSurvivesRestart(t *testing.T) {
	t.Setenv("AUTO_PRO_DATA_DIR", t.TempDir())
	job := &onlineUpdateJob{
		ID:        "U-test-persist",
		Status:    "restarting",
		Message:   "服务正在切换并重启",
		Progress:  95,
		Version:   "1.0.1",
		Logs:      []string{"开始更新"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	persistOnlineUpdateJob(job)
	if err := os.WriteFile(onlineUpdateJobStatePath(job.ID)+".result", []byte("success\n"), 0600); err != nil {
		t.Fatal(err)
	}

	loaded := loadOnlineUpdateJob(job.ID)
	if loaded == nil || loaded.Status != "success" || loaded.Message != "更新完成" || loaded.Progress != 100 {
		t.Fatalf("unexpected persisted job: %#v", loaded)
	}
	loadedAgain := loadOnlineUpdateJob(job.ID)
	if loadedAgain == nil || len(loadedAgain.Logs) != 2 {
		t.Fatalf("result reconciliation was not idempotent: %#v", loadedAgain)
	}
}

func TestWriteOnlineUpdateScriptSupportsWebsiteRoot(t *testing.T) {
	shell, err := exec.LookPath("/bin/sh")
	if err != nil {
		t.Skip("/bin/sh is not available")
	}

	root := t.TempDir()
	dataDir := filepath.Join(root, "backend")
	frontendSource := filepath.Join(root, "staging-frontend")
	stagingDir := filepath.Join(root, "staging")
	for _, dir := range []string{dataDir, frontendSource, filepath.Join(stagingDir, "backend")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(root, "index.html"):                "old",
		filepath.Join(frontendSource, "index.html"):      "new",
		filepath.Join(frontendSource, "version.json"):    `{"version":"1.0.1"}`,
		filepath.Join(stagingDir, "backend", "auth_pro"): "binary",
	} {
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AUTO_PRO_DATA_DIR", dataDir)
	t.Setenv("AUTO_PRO_SERVICE_NAME", "auth_pro_test")

	scriptPath, err := writeOnlineUpdateScript(
		"U-script-test",
		stagingDir,
		&extractedOnlineUpdateManifest{BackendFile: "backend/auth_pro"},
		frontendSource,
		"1.0.1",
		root,
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, expected := range []string{
		`FRONTEND_MODE="inplace"`,
		`cp -a "$FRONTEND_SOURCE/assets/." "$FRONTEND_ROOT/assets/"`,
		`mv -f "$APP_STAGE" "$APP_BIN"`,
		`finish_job success`,
		`rollback_frontend`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("generated script missing %q", expected)
		}
	}
	if output, err := exec.Command(shell, "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("generated script syntax error: %v\n%s", err, output)
	}

	if config.GetDataDir() != dataDir {
		t.Fatalf("unexpected data directory: %s", config.GetDataDir())
	}
}

func TestOnlineUpdateScriptCommandFallsBackOnNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("仅验证非 Linux 环境的回退行为；Linux 下是否使用 systemd-run 依赖运行环境")
	}
	cmd := onlineUpdateScriptCommand("U-test", "/tmp/update.sh")
	if len(cmd.Args) < 2 || cmd.Args[0] != "/bin/sh" || cmd.Args[1] != "/tmp/update.sh" {
		t.Fatalf("expected /bin/sh fallback, got %#v", cmd.Args)
	}
}
