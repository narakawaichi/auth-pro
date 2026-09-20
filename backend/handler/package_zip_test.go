package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"auto_pro/config"
	"github.com/gin-gonic/gin"
)

type testZIPEntry struct {
	name, data string
	mode       os.FileMode
}

func makeTestZIP(t *testing.T, entries ...testZIPEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPackageZIPRejectsUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/absolute.txt", `C:/escape.txt`, `dir\escape.txt`, "dir/../escape.txt", "a//b", "file:stream", "trailing. ", "NUL.txt", "NUL .txt", "COM¹.txt", "CONOUT$"} {
		t.Run(name, func(t *testing.T) {
			if err := extractPackageZIP(makeTestZIP(t, testZIPEntry{name: name, data: "bad"}), t.TempDir()); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	for name, entries := range map[string][]testZIPEntry{
		"symlink":       {{name: "link", data: "../outside", mode: os.ModeSymlink | 0777}},
		"duplicate":     {{name: "a.txt", data: "one"}, {name: "A.txt", data: "two"}},
		"empty":         {},
		"metadata_only": {{name: "__MACOSX/._index.html", data: "metadata"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := extractPackageZIP(makeTestZIP(t, entries...), t.TempDir()); err == nil {
				t.Fatal("unsafe/empty ZIP accepted")
			}
		})
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if _, err := writer.CreateRaw(&zip.FileHeader{Name: "bomb", UncompressedSize64: uint64(packageMaxExtractedBytes + 1)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractPackageZIP(buffer.Bytes(), t.TempDir()); err == nil {
		t.Fatal("oversized archive accepted")
	}
	entries := make([]testZIPEntry, packageMaxFiles+1)
	if err := extractPackageZIP(makeTestZIP(t, entries...), t.TempDir()); err == nil {
		t.Fatal("entry limit not enforced")
	}
	corrupt := makeTestZIP(t, testZIPEntry{name: "entry.txt", data: "checksum-content"})
	corrupt[bytes.Index(corrupt, []byte("checksum-content"))] ^= 1
	if err := extractPackageZIP(corrupt, t.TempDir()); err == nil {
		t.Fatal("bad CRC accepted")
	}
}

func TestPluginZIPDownloadInstallsAndKeepsLastGoodVersion(t *testing.T) {
	t.Setenv("AUTO_PRO_DATA_DIR", t.TempDir())
	plugin := pluginInfo{ID: "demo-plugin", Name: "Demo", Category: "other", Source: "Test Source", Version: "1.0.0"}
	root := filepath.Join(config.GetPluginDir(), plugin.ID)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugin.pkg"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	ids, err := loadLocalPluginIDs()
	if err != nil || ids[plugin.ID] {
		t.Fatalf("legacy package must not count as installed: %v", err)
	}
	archive := makeTestZIP(t, testZIPEntry{name: "dist/plugin.json", data: `{"id":"demo-plugin"}`}, testZIPEntry{name: "dist/assets/data.txt", data: "installed"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	payload, err := downloadPluginPackage(server.URL + "/plugin.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := installPluginZIP(payload, plugin); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "assets", "data.txt")
	if data, err := os.ReadFile(file); err != nil || string(data) != "installed" {
		t.Fatalf("not extracted: %q %v", data, err)
	}
	plugins, err := loadLocalPlugins()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, installed := range plugins {
		if installed.ID == plugin.ID {
			found = installed.Local && !installed.Remote && !installed.CanEnable && installed.Source == plugin.Source && installed.Icon == "ri:puzzle-line"
		}
	}
	if !found {
		t.Fatal("installed plugin missing from local registry")
	}
	for _, bad := range [][]byte{
		[]byte("not a zip"),
		makeTestZIP(t, testZIPEntry{name: "../bad", data: "bad"}),
		makeTestZIP(t, testZIPEntry{name: "plugin.json", data: `{"id":"another-plugin"}`}),
		makeTestZIP(t, testZIPEntry{name: ".installed.json", data: `{}`}),
	} {
		if err := installPluginZIP(bad, plugin); err == nil {
			t.Fatal("bad archive installed")
		}
		if data, err := os.ReadFile(file); err != nil || string(data) != "installed" {
			t.Fatal("failed install damaged existing files")
		}
	}
	if err := installPluginZIP(archive, pluginInfo{ID: "epay"}); err == nil {
		t.Fatal("builtin overwritten")
	}
}

func TestUploadedHomeTemplateZIPAndSafeAssets(t *testing.T) {
	t.Setenv("AUTO_PRO_DATA_DIR", t.TempDir())
	for _, entry := range []testZIPEntry{
		{name: "template.json", data: `{"schemaVersion":1,"hero":{"title":"ZIP home","imageUrl":"assets/cover.svg"}}`},
		{name: "index.html", data: `<html><script type="module" src="./assets/app.js"></script><h1>ZIP home</h1></html>`},
	} {
		t.Run(entry.name, func(t *testing.T) {
			entry.name = "dist/" + entry.name
			archive := makeTestZIP(t, entry, testZIPEntry{name: "dist/assets/app.js", data: `document.body.dataset.loaded = 'yes'`},
				testZIPEntry{name: "dist/assets/cover.svg", data: `<svg xmlns="http://www.w3.org/2000/svg"/>`})
			installed, checksum, err := installUploadedHomeTemplateZIP(archive)
			if err != nil {
				t.Fatal(err)
			}
			if !installedUploadedTemplateMatches(installed, checksum) {
				root, rootErr := uploadedHomeTemplateRoot(installed)
				t.Fatalf("uploaded template was not installed: path=%q root=%q err=%v checksum=%s", installed, root, rootErr, checksum)
			}
			root := filepath.Dir(installed)
			requestAsset := func(name, revision string) *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(response)
				ctx.Request = httptest.NewRequest(http.MethodGet, "/asset", nil)
				ctx.Params = gin.Params{{Key: "filepath", Value: name}, {Key: "revision", Value: revision}}
				serveUploadedHomeTemplateAsset(ctx, installed)
				ctx.Writer.WriteHeaderNow()
				return response
			}
			response := requestAsset("/assets/app.js", filepath.Base(root))
			if response.Code != 200 || !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
				t.Fatalf("asset: %d %s", response.Code, response.Body)
			}
			policy := response.Header().Get("Content-Security-Policy")
			if strings.Contains(policy, "sandbox") || !strings.Contains(policy, "object-src 'none'") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("unsafe headers: %v", response.Header())
			}
			for _, name := range []string{"/../secret.json", "/assets/../../secret.json", "/.env.json", "/script.php", "/missing.js"} {
				if response := requestAsset(name, filepath.Base(root)); response.Code != 404 {
					t.Fatalf("unsafe asset served: %s", name)
				}
			}
			if requestAsset("/assets/app.js", "old-version").Code != 404 {
				t.Fatal("revision mismatch accepted")
			}
			if err := os.WriteFile(installed, []byte("corrupted"), 0644); err != nil {
				t.Fatal(err)
			}
			if installedUploadedTemplateMatches(installed, checksum) {
				t.Fatal("checksum mismatch accepted")
			}
		})
	}
	for _, entry := range []testZIPEntry{{name: "src/App.vue", data: "source, not a build"}, {name: "template.json", data: `{}`}, {name: "index.html", data: ""}} {
		if _, _, err := installUploadedHomeTemplateZIP(makeTestZIP(t, entry)); err == nil {
			t.Fatalf("invalid template accepted: %s", entry.name)
		}
	}
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("not multipart"))
	AdminHomeTemplateUpload(ctx)
	var result struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Code != 400 {
		t.Fatalf("bad upload response: %s", response.Body)
	}
}

func TestInstalledPackagesCannotBypassAssetSandboxViaStaticServer(t *testing.T) {
	webRoot := t.TempDir()
	t.Setenv("AUTO_PRO_DATA_DIR", filepath.Join(webRoot, "backend"))
	for _, root := range []string{config.GetHomeTemplateDir(), config.GetPluginDir()} {
		if !IsInstalledPackageFile(filepath.Join(root, "upload-123", "index.html")) {
			t.Fatal("runtime package path exposed by static server")
		}
	}
	if IsInstalledPackageFile(filepath.Join(webRoot, "assets", "index.js")) {
		t.Fatal("ordinary frontend assets blocked")
	}
	if IsInstalledPackageFile(filepath.Join(webRoot, "backend", "home-templates-public", "index.html")) {
		t.Fatal("sibling directory falsely blocked")
	}
}
