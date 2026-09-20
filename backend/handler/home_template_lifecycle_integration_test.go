package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"auto_pro/middleware"
	"auto_pro/softwaresource"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type lifecycleTemplateState struct {
	ID              any    `json:"id"`
	TemplateID      string `json:"templateId"`
	Format          string `json:"format"`
	Enabled         bool   `json:"enabled"`
	Installed       bool   `json:"installed"`
	Available       bool   `json:"available"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

func TestPublishedTemplateLifecycleHTTP(t *testing.T) {
	sourceURL := os.Getenv("AUTH_PRO_TEMPLATE_TEST_SOURCE_URL")
	if sourceURL == "" {
		t.Skip("run from auth-pro-plug TestMySQLCrossProjectTemplateLifecycle")
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Hostname() != "127.0.0.1" || !strings.HasSuffix(os.Getenv("AUTO_PRO_DB_NAME"), "_test") {
		t.Fatal("use a loopback publisher and disposable *_test consumer database")
	}
	t.Setenv("AUTO_PRO_DATA_DIR", t.TempDir())
	client, err := softwaresource.NewClient(softwaresource.ClientConfig{
		BaseURL: sourceURL, CatalogKey: os.Getenv("AUTH_PRO_TEMPLATE_TEST_SOURCE_KEY"), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	restoreClient := softwaresource.SetDefaultForTest(client)
	defer restoreClient()
	db, err := openSystemConfigDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePluginStorage(db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/system/home-templates", middleware.JWTAuth(), middleware.RequireAdmin(), middleware.RequireSuperAdmin())
	admin.GET("", AdminHomeTemplateList)
	admin.GET("/:id/download", AdminHomeTemplateDownload)
	admin.POST("/:id/install", AdminHomeTemplateInstall)
	admin.POST("/:id/enable", AdminHomeTemplateEnable)
	admin.POST("/:id/disable", AdminHomeTemplateDisable)
	admin.POST("/:id/uninstall", AdminHomeTemplateUninstall)
	router.GET("/api/home-template/active", PublicActiveHomeTemplate)
	router.GET("/api/home-template/assets/:id/:revision/*filepath", PublicHomeTemplateAsset)
	consumer := httptest.NewServer(router)
	defer consumer.Close()
	sign := func(role string) string {
		claims := middleware.Claims{UserID: 1, Username: "lifecycle-admin", Role: "admin", RoleCode: role,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(middleware.JWTSecret())
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := sign("R_SUPER")
	request := func(method, target, credential, contentType string, body io.Reader) ([]byte, http.Header, int) {
		t.Helper()
		req, err := http.NewRequest(method, target, body)
		if err != nil {
			t.Fatal(err)
		}
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, err := consumer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return payload, response.Header, response.StatusCode
	}
	checkCode := func(payload []byte, expected int) {
		t.Helper()
		var envelope struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Code != expected {
			t.Fatalf("expected API code %d, got %.500s (%v)", expected, payload, err)
		}
	}
	list := func(key string) (lifecycleTemplateState, bool) {
		t.Helper()
		body, _, status := request(http.MethodGet, consumer.URL+"/api/system/home-templates?refresh=1", token, "", nil)
		checkCode(body, 200)
		if status != http.StatusOK {
			t.Fatal(status)
		}
		var envelope struct {
			Data struct {
				List []lifecycleTemplateState `json:"list"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		for _, item := range envelope.Data.List {
			if item.TemplateID == key {
				return item, true
			}
		}
		return lifecycleTemplateState{}, false
	}
	active := func() map[string]any {
		t.Helper()
		body, _, _ := request(http.MethodGet, consumer.URL+"/api/home-template/active", "", "", nil)
		checkCode(body, 200)
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	for _, action := range []string{"download", "install", "enable", "disable", "uninstall"} {
		method := http.MethodPost
		if action == "download" {
			method = http.MethodGet
		}
		for _, auth := range []struct {
			token string
			code  int
		}{{"", 401}, {sign("R_ADMIN"), 403}} {
			body, _, status := request(method, consumer.URL+"/api/system/home-templates/1/"+action, auth.token, "", nil)
			checkCode(body, auth.code)
			if auth.code == 401 && status != http.StatusUnauthorized {
				t.Fatalf("%s unauthenticated status=%d body=%.200s", action, status, body)
			}
		}
	}
	for _, format := range []string{"static", "json"} {
		t.Run(format, func(t *testing.T) {
			key := fmt.Sprintf("lifecycle-%s-%d", format, time.Now().UnixNano())
			entry := "index.html"
			entryContent := `<html><h1>ZIP lifecycle v1</h1><script src="/assets/app.js"></script></html>`
			if format == "json" {
				entry, entryContent = "template.json", `{"schemaVersion":1,"hero":{"title":"ZIP lifecycle v1","imageUrl":"assets/cover.png"}}`
			}
			makePackage := func(content string) []byte {
				return makeTestZIP(t, testZIPEntry{name: "dist/" + entry, data: content},
					testZIPEntry{name: "dist/assets/app.js", data: strings.Repeat("/* asset */", (2<<20)/11+1)},
					testZIPEntry{name: "dist/assets/cover.png", data: "bounded-test-image"})
			}
			archive := makePackage(entryContent)
			publish := func(id, version, status string, payload []byte) string {
				t.Helper()
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				metadata, _ := json.Marshal(map[string]string{"name": "ZIP lifecycle " + format, "identifier": key, "version": version, "author": "integration", "status": status})
				if err := writer.WriteField("metadata", string(metadata)); err != nil {
					t.Fatal(err)
				}
				if payload != nil {
					file, err := writer.CreateFormFile("template", key+".zip")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.Write(payload); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				method, target := http.MethodPost, sourceURL+"/api/v1/templates"
				if id != "" {
					method, target = http.MethodPatch, target+"/"+id
				}
				data, _, _ := request(method, target, os.Getenv("AUTH_PRO_TEMPLATE_TEST_ADMIN_TOKEN"), writer.FormDataContentType(), &body)
				checkCode(data, 200)
				var result struct {
					Data struct {
						ID     string `json:"id"`
						Format string `json:"format"`
						SHA256 string `json:"sha256"`
					} `json:"data"`
				}
				if err := json.Unmarshal(data, &result); err != nil {
					t.Fatal(err)
				}
				if result.Data.Format != "zip" || (payload != nil && result.Data.SHA256 != actualChecksumString(sha256.Sum256(payload))) {
					t.Fatalf("bad ZIP publication metadata: %.500s", data)
				}
				return result.Data.ID
			}
			sourceID := publish("", "1.0.0", "enabled", archive)
			state, found := list(key)
			if !found || state.Installed || state.Enabled || !state.Available || state.Format != "zip" {
				t.Fatalf("published ZIP not listed as uninstalled: %+v", state)
			}
			id := fmt.Sprint(state.ID)
			action := func(name string, expected int) {
				t.Helper()
				body, _, _ := request(http.MethodPost, consumer.URL+"/api/system/home-templates/"+id+"/"+name, token, "", nil)
				checkCode(body, expected)
			}
			download := func(expected []byte) {
				t.Helper()
				body, headers, status := request(http.MethodGet, consumer.URL+"/api/system/home-templates/"+id+"/download", token, "", nil)
				if status != 200 || headers.Get("Content-Type") != "application/zip" || !bytes.Equal(body, expected) || !strings.Contains(headers.Get("Content-Disposition"), ".zip") {
					t.Fatalf("package download changed bytes or MIME (status=%d, size=%d)", status, len(body))
				}
			}
			installedPath := func() string {
				t.Helper()
				var value string
				if err := db.QueryRow("SELECT installed_path FROM home_templates WHERE id=?", id).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			download(archive)
			if installedPath() != "" || active()["isDefault"] != true {
				t.Fatal("download must not install or enable")
			}
			action("install", 200)
			firstPath := installedPath()
			if firstPath == "" || active()["isDefault"] != true {
				t.Fatal("install must not activate")
			}
			if state, _ = list(key); !state.Installed || state.Enabled {
				t.Fatalf("wrong installed state: %+v", state)
			}
			action("enable", 200)
			running := active()
			if running["format"] != format || running["isDefault"] != false {
				t.Fatalf("unexpected runtime: %+v", running)
			}
			assetURL := running["entryUrl"]
			if format == "json" {
				assetURL = fmt.Sprint(running["assetBaseUrl"]) + "assets/cover.png"
			}
			asset, headers, status := request(http.MethodGet, consumer.URL+fmt.Sprint(assetURL), "", "", nil)
			if status != 200 || strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
				t.Fatalf("unexpected asset sandbox: %d %v", status, headers)
			}
			if format == "static" && !bytes.Contains(asset, []byte(`src="./assets/app.js"`)) {
				t.Fatal("static resource path not rebased")
			}
			action("disable", 200)
			if active()["isDefault"] != true || installedPath() != firstPath {
				t.Fatal("disable must preserve installation and restore default")
			}
			action("enable", 200)
			// Reject a database-supplied path outside the managed installation directory.
			outside := filepath.Join(t.TempDir(), "index.html")
			if err := os.WriteFile(outside, []byte("do-not-delete"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE home_templates SET installed_path=? WHERE id=?", outside, id); err != nil {
				t.Fatal(err)
			}
			action("uninstall", 400)
			if body, err := os.ReadFile(outside); err != nil || string(body) != "do-not-delete" {
				t.Fatal("uninstall touched unrelated files")
			}
			if _, err := db.Exec("UPDATE home_templates SET installed_path=? WHERE id=?", firstPath, id); err != nil {
				t.Fatal(err)
			}
			// A failed DB state update must restore the quarantined installation.
			trigger := "lifecycle_uninstall_fail_" + id
			if _, err := db.Exec("CREATE TRIGGER " + trigger + " BEFORE UPDATE ON home_templates FOR EACH ROW BEGIN IF OLD.id=" + id + " AND NEW.installed_path='' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='test uninstall rollback'; END IF; END"); err != nil {
				t.Fatal(err)
			}
			action("uninstall", 500)
			if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(firstPath); err != nil || active()["isDefault"] != false {
				t.Fatal("failed uninstall did not restore active installation")
			}
			action("uninstall", 200)
			if installedPath() != "" || active()["isDefault"] != true {
				t.Fatal("uninstall did not clear installation and active state")
			}
			if _, err := os.Stat(filepath.Dir(firstPath)); !os.IsNotExist(err) {
				t.Fatal("uninstall left the installation directory")
			}
			_, _, status = request(http.MethodGet, consumer.URL+fmt.Sprint(assetURL), "", "", nil)
			if status != 404 {
				t.Fatal("uninstalled assets remain accessible")
			}
			state, found = list(key)
			if !found || state.Installed || state.Enabled || !state.Available || fmt.Sprint(state.ID) != id {
				t.Fatalf("remote catalog was removed by local uninstall: %+v", state)
			}
			action("install", 200)
			action("enable", 200)
			oldPath := installedPath()
			nextArchive := makePackage(strings.ReplaceAll(entryContent, "v1", "v2"))
			publish(sourceID, "2.0.0", "enabled", nextArchive)
			state, _ = list(key)
			if !state.UpdateAvailable || !state.Installed || !state.Enabled {
				t.Fatalf("update not detected: %+v", state)
			}
			if running := active(); running["version"] != "1.0.0" || running["isDefault"] != false {
				t.Fatal("refresh switched the active version before update was accepted")
			}
			action("install", 400)
			if installedPath() != oldPath {
				t.Fatal("plain install silently switched an active template")
			}
			action("enable", 200)
			if installedPath() == oldPath {
				t.Fatal("new ZIP was not installed")
			}
			if _, err := os.Stat(filepath.Dir(oldPath)); !os.IsNotExist(err) {
				t.Fatal("old installation was not cleaned after a successful update")
			}
			download(nextArchive)
			publish(sourceID, "2.0.0", "disabled", nil)
			state, found = list(key)
			if !found || state.Available || !state.Installed || !state.Enabled {
				t.Fatalf("withdrawn installed template became unmanageable: %+v", state)
			}
			download(nextArchive)
			offline, err := softwaresource.NewClient(softwaresource.ClientConfig{BaseURL: "http://127.0.0.1:1", CatalogKey: "offline-test-key", Timeout: 20 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			undoOffline := softwaresource.SetDefaultForTest(offline)
			defer undoOffline()
			state, found = list(key)
			if !found || !state.Installed {
				t.Fatal("source outage hid installed template")
			}
			download(nextArchive)
			action("disable", 200)
			action("enable", 200)
			action("uninstall", 200)
			if installedPath() != "" || active()["isDefault"] != true {
				t.Fatal("offline uninstall failed")
			}
			undoOffline()
			for _, name := range []string{"install", "disable", "uninstall"} {
				body, _, _ := request(http.MethodPost, consumer.URL+"/api/system/home-templates/default/"+name, token, "", nil)
				checkCode(body, 400)
			}
			// Confirm the upload still exists on the publisher after local uninstall.
			body, _, _ := request(http.MethodGet, sourceURL+"/api/v1/templates/"+sourceID, os.Getenv("AUTH_PRO_TEMPLATE_TEST_ADMIN_TOKEN"), "", nil)
			checkCode(body, 200)
			localID, _ := strconv.ParseInt(id, 10, 64)
			if _, err := db.Exec("DELETE FROM home_templates WHERE id=? AND installed_path=''", localID); err != nil {
				t.Fatal(err)
			}
			t.Log("upload -> catalog -> download -> install -> enable -> disable -> uninstall -> reinstall/update -> offline/withdrawn operations: passed")
		})
	}
}
