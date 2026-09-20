package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"auto_pro/softwaresource"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
)

func TestSoftwareSourceHTTPIntegration(t *testing.T) {
	if os.Getenv("AUTO_PRO_RUN_SOFTWARE_SOURCE_INTEGRATION") != "1" {
		t.Skip("set AUTO_PRO_RUN_SOFTWARE_SOURCE_INTEGRATION=1")
	}
	baseDSN := os.Getenv("AUTO_PRO_SOFTWARE_SOURCE_TEST_DSN")
	serviceURL := os.Getenv("AUTO_PRO_SOFTWARE_SOURCE_TEST_URL")
	if baseDSN == "" || serviceURL == "" {
		t.Fatal("integration DSN and service URL are required")
	}
	baseConfig, err := mysql.ParseDSN(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	controlConfig := *baseConfig
	controlConfig.DBName = ""
	control, err := sql.Open("mysql", controlConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	databaseName := "auth_pro_p4_" + randomP4Suffix(t)
	if _, err := control.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = control.Exec("DROP DATABASE `" + databaseName + "`") })
	t.Setenv("AUTO_PRO_DB_HOST", baseConfig.Addr[:strings.LastIndex(baseConfig.Addr, ":")])
	t.Setenv("AUTO_PRO_DB_PORT", baseConfig.Addr[strings.LastIndex(baseConfig.Addr, ":")+1:])
	t.Setenv("AUTO_PRO_DB_NAME", databaseName)
	t.Setenv("AUTO_PRO_DB_USER", baseConfig.User)
	t.Setenv("AUTO_PRO_DB_PASSWORD", baseConfig.Passwd)
	dataDir := t.TempDir()
	t.Setenv("AUTO_PRO_DATA_DIR", dataDir)

	client, err := softwaresource.NewClient(softwaresource.ClientConfig{
		BaseURL: serviceURL, CatalogKey: "p3-catalog-key",
		Timeout: 5 * time.Second, StaleTTL: time.Hour, CacheDir: filepath.Join(dataDir, "catalog-cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	restore := softwaresource.SetDefaultForTest(client)
	t.Cleanup(restore)
	db, err := openSystemConfigDB()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePluginStorage(db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	pluginPackage := makeTestZIP(t, testZIPEntry{name: "payload.txt", data: "authorization-local-plugin-package"})
	pluginFixture := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.json":
			_, _ = fmt.Fprintf(response, `{"name":"Authorization Plugin Source","plugins":[{"id":"p4-demo-plugin","category":"other","name":"P4 Demo Plugin","description":"授权本地插件源测试","version":"1.0.0","downloadUrl":"http://%s/plugin.pkg"}],"homeTemplates":[]}`, request.Host)
		case "/plugin.pkg":
			_, _ = response.Write(pluginPackage)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer pluginFixture.Close()
	addSourceResponse := invokeHandler(t, http.MethodPost, "/api/system/plugin-sources",
		strings.NewReader(fmt.Sprintf(`{"name":"Authorization Plugin Source","url":%q}`, pluginFixture.URL+"/index.json")), nil, AdminPluginSourceAdd)
	if !strings.Contains(addSourceResponse.Body.String(), `"code":200`) {
		t.Fatalf("local plugin source add response=%s", addSourceResponse.Body.String())
	}
	authorizationRouter := gin.New()
	authorizationRouter.GET("/api/system/plugins", AdminPluginList)
	authorizationRouter.GET("/api/system/home-templates", AdminHomeTemplateList)
	authorizationRouter.POST("/api/system/home-templates/:id/enable", AdminHomeTemplateEnable)
	authorizationRouter.POST("/api/system/plugins/:id/download", AdminPluginDownload)
	authorizationRouter.POST("/api/system/plugin-sources/:id/refresh", AdminPluginSourceRefresh)
	authorizationRouter.GET("/api/home-template/active", PublicActiveHomeTemplate)
	authorizationRouter.GET("/api/software-source/templates/:id/preview", PublicSoftwareSourceTemplatePreview)
	authorizationServer := httptest.NewServer(authorizationRouter)
	defer authorizationServer.Close()
	listResponse, err := http.Get(authorizationServer.URL + "/api/system/plugins")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(listResponse.Body)
	listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK || !strings.Contains(string(listBody), "P4 Demo Plugin") {
		t.Fatalf("authorization HTTP service did not expose remote catalog: %s", listBody)
	}

	pluginResponse := invokeHandler(t, http.MethodGet, "/api/system/plugins", nil, nil, AdminPluginList)
	var pluginPayload struct {
		Code int `json:"code"`
		Data struct {
			Categories []struct {
				Plugins []pluginInfo `json:"plugins"`
			} `json:"categories"`
			Sources []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"sources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(pluginResponse.Body.Bytes(), &pluginPayload); err != nil || pluginPayload.Code != 200 {
		t.Fatalf("plugin list response=%s err=%v", pluginResponse.Body.String(), err)
	}
	foundRemoteApp := false
	for _, category := range pluginPayload.Data.Categories {
		for _, plugin := range category.Plugins {
			if plugin.ID == "p4-demo-plugin" && plugin.Remote && plugin.Name == "P4 Demo Plugin" {
				foundRemoteApp = true
			}
		}
	}
	if !foundRemoteApp {
		t.Fatal("remote app metadata was not returned through authorization BFF")
	}

	templateResponse := invokeHandler(t, http.MethodGet, "/api/system/home-templates", nil, nil, AdminHomeTemplateList)
	var templatePayload struct {
		Code int `json:"code"`
		Data struct {
			List []struct {
				ID         any    `json:"id"`
				CatalogID  string `json:"catalogId"`
				TemplateID string `json:"templateId"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(templateResponse.Body.Bytes(), &templatePayload); err != nil || templatePayload.Code != 200 {
		t.Fatalf("template list response=%s err=%v", templateResponse.Body.String(), err)
	}
	var localTemplateID int64
	var catalogTemplateID string
	for _, template := range templatePayload.Data.List {
		if template.TemplateID == "fintech-gold-home" {
			localTemplateID = int64(template.ID.(float64))
			catalogTemplateID = template.CatalogID
		}
	}
	if localTemplateID == 0 || catalogTemplateID == "" {
		t.Fatalf("stable template mapping missing: %+v", templatePayload.Data.List)
	}
	secondTemplateResponse := invokeHandler(t, http.MethodGet, "/api/system/home-templates", nil, nil, AdminHomeTemplateList)
	if !strings.Contains(secondTemplateResponse.Body.String(), `"catalogId":"`+catalogTemplateID+`"`) {
		t.Fatalf("template mapping changed on repeated read: %s", secondTemplateResponse.Body.String())
	}
	mappingDB, err := openSystemConfigDB()
	if err != nil {
		t.Fatal(err)
	}
	var mappedCount int
	if err := mappingDB.QueryRow("SELECT COUNT(*) FROM home_templates WHERE catalog_id IS NOT NULL").Scan(&mappedCount); err != nil {
		mappingDB.Close()
		t.Fatal(err)
	}
	mappingDB.Close()
	if mappedCount != 2 {
		t.Fatalf("mapped template rows=%d, want 2", mappedCount)
	}
	if err := enableAppStoreTemplate(context.Background(), strconv.FormatInt(localTemplateID, 10)); err != nil {
		t.Fatal(err)
	}
	offlineClient, err := softwaresource.NewClient(softwaresource.ClientConfig{BaseURL: "http://127.0.0.1:1", CatalogKey: "offline", Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	restoreOnline := softwaresource.SetDefaultForTest(offlineClient)
	offlinePluginList := invokeHandler(t, http.MethodGet, "/api/system/plugins", nil, nil, AdminPluginList)
	if !strings.Contains(offlinePluginList.Body.String(), `"id":"epay"`) {
		restoreOnline()
		t.Fatalf("authorization plugin list unexpectedly depends on template service: %s", offlinePluginList.Body.String())
	}
	if err := enableAppStoreTemplate(context.Background(), strconv.FormatInt(localTemplateID, 10)); err != nil {
		restoreOnline()
		t.Fatalf("installed template could not be activated offline: %v", err)
	}
	restoreOnline()
	activeResponse := invokeHandler(t, http.MethodGet, "/api/home-template/active", nil, nil, PublicActiveHomeTemplate)
	if !strings.Contains(activeResponse.Body.String(), `"isDefault":false`) || !strings.Contains(activeResponse.Body.String(), `"fintech-gold-home"`) {
		t.Fatalf("active template response=%s", activeResponse.Body.String())
	}
	previewResponse := invokeHandler(t, http.MethodGet, "/api/software-source/templates/"+catalogTemplateID+"/preview", nil,
		gin.Params{{Key: "id", Value: catalogTemplateID}}, PublicSoftwareSourceTemplatePreview)
	if previewResponse.Code != http.StatusOK || !strings.Contains(previewResponse.Header().Get("Content-Type"), "image/svg") {
		t.Fatalf("preview status=%d type=%q", previewResponse.Code, previewResponse.Header().Get("Content-Type"))
	}

	downloadResponse := invokeHandler(t, http.MethodPost, "/api/system/plugins/p4-demo-plugin/download", nil,
		gin.Params{{Key: "id", Value: "p4-demo-plugin"}}, AdminPluginDownload)
	if !strings.Contains(downloadResponse.Body.String(), `"code":200`) {
		t.Fatalf("download response=%s", downloadResponse.Body.String())
	}
	artifact, err := os.ReadFile(filepath.Join(dataDir, "plugins", "p4-demo-plugin", "payload.txt"))
	if err != nil || string(artifact) != "authorization-local-plugin-package" {
		t.Fatalf("downloaded artifact=%q err=%v", artifact, err)
	}
	// Uploaded JSON and static ZIPs must install and activate without the remote catalog.
	restoreSource := softwaresource.SetDefaultForTest(offlineClient)
	defer restoreSource()
	for _, entry := range []testZIPEntry{
		{name: "template.json", data: `{"schemaVersion":1,"hero":{"title":"Uploaded ZIP home"}}`},
		{name: "index.html", data: `<html><h1>Uploaded ZIP home</h1></html>`},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("file", "custom-home.zip")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(makeTestZIP(t, entry)); err != nil {
			t.Fatal(err)
		}
		if err := form.WriteField("name", "Uploaded ZIP home"); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/system/home-templates/upload", &body)
		ctx.Request.Header.Set("Content-Type", form.FormDataContentType())
		AdminHomeTemplateUpload(ctx)
		var uploaded struct {
			Code int `json:"code"`
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil || uploaded.Code != 200 || uploaded.Data.ID <= 0 {
			t.Fatalf("upload: %s %v", response.Body, err)
		}
		if err := enableAppStoreTemplate(context.Background(), strconv.FormatInt(uploaded.Data.ID, 10)); err != nil {
			t.Fatal(err)
		}
		items, err := listAppStoreTemplates(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item.ID == strconv.FormatInt(uploaded.Data.ID, 10) {
				found = item.Enabled && item.Installed && item.SourceType == "upload"
			}
		}
		if !found {
			t.Fatal("uploaded template missing from offline list")
		}
		active := invokeHandler(t, http.MethodGet, "/api/home-template/active", nil, nil, PublicActiveHomeTemplate)
		if !strings.Contains(active.Body.String(), "Uploaded ZIP home") {
			t.Fatalf("active upload: %s", active.Body)
		}
		if entry.name == "index.html" {
			var result struct {
				Data struct {
					EntryURL string `json:"entryUrl"`
				} `json:"data"`
			}
			if err := json.Unmarshal(active.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(result.Data.EntryURL, "/")
			if len(parts) != 7 {
				t.Fatalf("invalid static entry URL: %s", result.Data.EntryURL)
			}
			asset := invokeHandler(t, http.MethodGet, result.Data.EntryURL, nil, gin.Params{
				{Key: "id", Value: parts[4]}, {Key: "revision", Value: parts[5]}, {Key: "filepath", Value: "/index.html"},
			}, PublicHomeTemplateAsset)
			if asset.Code != 200 || !strings.Contains(asset.Body.String(), "Uploaded ZIP home") || strings.Contains(asset.Header().Get("Content-Security-Policy"), "sandbox") {
				t.Fatalf("static asset: %s %v", asset.Body, asset.Header())
			}
		}
	}
	pluginDB, err := openSystemConfigDB()
	if err != nil {
		t.Fatal(err)
	}
	var localSourceID int64
	if err := pluginDB.QueryRow("SELECT id FROM plugin_sources WHERE name='Authorization Plugin Source'").Scan(&localSourceID); err != nil {
		pluginDB.Close()
		t.Fatal(err)
	}
	pluginDB.Close()
	refreshResponse := invokeHandler(t, http.MethodPost, fmt.Sprintf("/api/system/plugin-sources/%d/refresh", localSourceID), nil,
		gin.Params{{Key: "id", Value: strconv.FormatInt(localSourceID, 10)}}, AdminPluginSourceRefresh)
	if !strings.Contains(refreshResponse.Body.String(), `"code":200`) {
		t.Fatalf("source refresh response=%s", refreshResponse.Body.String())
	}
}

func invokeHandler(t *testing.T, method, target string, body *strings.Reader, params gin.Params, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		requestBody = body
	}
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, requestBody)
	if body != nil {
		c.Request.Header.Set("Content-Type", "application/json")
	}
	c.Params = params
	handler(c)
	return recorder
}

func randomP4Suffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s", hex.EncodeToString(raw))
}
