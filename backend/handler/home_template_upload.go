package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"auto_pro/appstore"
	"auto_pro/config"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
)

// Local uploads have no remote source URL. Write it explicitly for older schemas
// where source_url is NOT NULL without a default (including the shipped schema.sql).
const insertUploadedHomeTemplateSQL = `INSERT INTO home_templates
	(template_key, source_id, name, description, version, source_url, source_type, author_name,
	 sha256, schema_version, installed_path, installed_at, available)
	VALUES (?, 0, ?, ?, ?, '', 'upload', ?, ?, ?, ?, NOW(), 1)`

func AdminHomeTemplateUpload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pluginPackageMaxSize+(1<<20))
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	header, err := c.FormFile("file")
	if err != nil || header.Size <= 0 || header.Size > pluginPackageMaxSize || !strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请上传不超过 20 MiB 的首页模板 ZIP"})
		return
	}
	file, err := header.Open()
	if err != nil {
		writeAppStoreError(c, appstore.ClientError("读取上传文件失败", err))
		return
	}
	payload, err := readPluginReader(file, pluginPackageMaxSize)
	file.Close()
	if err != nil {
		writeAppStoreError(c, appstore.ClientError("读取上传文件失败："+err.Error(), err))
		return
	}
	name := truncateText(c.PostForm("name"), 100)
	if name == "" {
		name = truncateText(strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)), 100)
	}
	version := truncateText(c.PostForm("version"), 40)
	if version == "" {
		version = "1.0.0"
	}
	db, err := openSystemConfigDB()
	if err != nil {
		writeAppStoreError(c, appstore.ServerError("数据库连接失败", err))
		return
	}
	if err := ensurePluginStorage(db); err != nil {
		writeAppStoreError(c, appstore.ServerError("初始化模板存储失败", err))
		return
	}
	installedPath, checksum, err := installUploadedHomeTemplateZIP(payload)
	if err != nil {
		writeAppStoreError(c, appstore.ClientError("模板安装失败："+err.Error(), err))
		return
	}
	registered := false
	defer func() {
		if !registered {
			_ = os.RemoveAll(filepath.Dir(installedPath))
		}
	}()
	schemaVersion := 1
	if filepath.Ext(installedPath) == ".html" {
		schemaVersion = 0
	}
	result, err := db.ExecContext(c.Request.Context(), insertUploadedHomeTemplateSQL,
		filepath.Base(filepath.Dir(installedPath)), name, truncateText(c.PostForm("description"), 500), version,
		truncateText(c.PostForm("author"), 100), checksum, schemaVersion, installedPath)
	if err != nil {
		_ = c.Error(err) // Keep the database cause in server logs, not in the public response.
		writeAppStoreError(c, appstore.ServerError("登记模板安装状态失败", err))
		return
	}
	registered = true
	id, err := result.LastInsertId()
	if err != nil {
		writeAppStoreError(c, appstore.ServerError("读取模板标识失败，请刷新列表", err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "首页模板已上传并安装，请在列表中启用", "data": gin.H{"id": id}})
}

func installUploadedHomeTemplateZIP(payload []byte) (installedPath, checksum string, err error) {
	root := config.GetHomeTemplateDir()
	stage, err := os.MkdirTemp(root, ".upload-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	if err := extractPackageZIP(payload, stage); err != nil {
		return "", "", err
	}
	content, err := packageContentRoot(stage)
	if err != nil {
		return "", "", err
	}
	entry := "index.html"
	if _, err := os.Stat(filepath.Join(content, entry)); errors.Is(err, os.ErrNotExist) {
		entry = "template.json"
	}
	entryPayload, err := readLimitedFile(filepath.Join(content, entry), homeTemplateMaxBytes)
	if err != nil {
		return "", "", errors.New("ZIP 根目录需包含 index.html 或 template.json（入口不超过 2 MiB）")
	}
	if err := validateUploadedTemplateEntry(entry, entryPayload); err != nil {
		return "", "", err
	}
	if entry == "index.html" {
		normalized, err := normalizeUploadedTemplateAssets(content, entryPayload)
		if err != nil {
			return "", "", err
		}
		if !bytes.Equal(normalized, entryPayload) {
			if err := os.WriteFile(filepath.Join(content, entry), normalized, 0644); err != nil {
				return "", "", err
			}
			entryPayload = normalized
		}
	}
	target := filepath.Join(root, strings.TrimPrefix(filepath.Base(stage), "."))
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return "", "", errors.New("安装目录冲突，请重新上传")
	}
	if err := os.Rename(content, target); err != nil {
		return "", "", err
	}
	return filepath.Join(target, entry), actualChecksumString(sha256.Sum256(entryPayload)), nil
}

func validateUploadedTemplateEntry(entry string, payload []byte) error {
	switch entry {
	case "template.json":
		return validateHomeTemplateDocument(payload)
	case "index.html":
		if strings.TrimSpace(string(payload)) != "" && !strings.ContainsRune(string(payload), 0) {
			return nil
		}
	}
	return errors.New("首页模板入口必须是有效的 template.json 或非空 index.html")
}

func uploadedHomeTemplateRoot(installedPath string) (string, error) {
	if filepath.Base(installedPath) != "index.html" && filepath.Base(installedPath) != "template.json" {
		return "", errors.New("模板入口不合法")
	}
	base, err := filepath.EvalSymlinks(config.GetHomeTemplateDir())
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(installedPath))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, root)
	if err != nil || strings.ContainsAny(rel, "/\\") || !strings.HasPrefix(rel, "upload-") {
		return "", errors.New("模板路径超出安装目录")
	}
	return root, nil
}

func installedUploadedTemplateMatches(installedPath, checksum string) bool {
	if _, err := uploadedHomeTemplateRoot(installedPath); err != nil {
		return false
	}
	info, err := os.Lstat(installedPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	payload, err := readLimitedFile(installedPath, homeTemplateMaxBytes)
	return err == nil && strings.EqualFold(actualChecksumString(sha256.Sum256(payload)), checksum) &&
		validateUploadedTemplateEntry(filepath.Base(installedPath), payload) == nil
}

func listUploadedHomeTemplates(ctx context.Context, db *sql.DB, activeID int64) ([]appstore.Template, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, template_key, name, description, version, author_name, author_url,
		author_email, sha256, schema_version, installed_path FROM home_templates WHERE source_type='upload' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []appstore.Template{}
	for rows.Next() {
		var item appstore.Template
		var id int64
		var installedPath string
		if err := rows.Scan(&id, &item.TemplateID, &item.Name, &item.Description, &item.Version, &item.Author.Name,
			&item.Author.URL, &item.Author.Email, &item.SHA256, &item.SchemaVersion, &installedPath); err != nil {
			return nil, err
		}
		item.ID, item.Source, item.SourceType = strconv.FormatInt(id, 10), "本地上传", "upload"
		item.Installed = installedUploadedTemplateMatches(installedPath, item.SHA256)
		item.Available, item.Enabled = item.Installed, activeID == id
		normalizeAppStoreTemplateMetadata(&item)
		items = append(items, item)
	}
	return items, rows.Err()
}

func homeTemplateAssetBaseURL(id int64, installedPath string) string {
	return fmt.Sprintf("/api/home-template/assets/%d/%s/", id, filepath.Base(filepath.Dir(installedPath)))
}

func PublicHomeTemplateAsset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.Status(http.StatusNotFound)
		return
	}
	db, err := openSystemConfigDB()
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	var installedPath string
	if err := db.QueryRowContext(c.Request.Context(), "SELECT installed_path FROM home_templates WHERE id=?", id).Scan(&installedPath); err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	serveUploadedHomeTemplateAsset(c, installedPath)
}

func serveUploadedHomeTemplateAsset(c *gin.Context, installedPath string) {
	root, err := uploadedHomeTemplateRoot(installedPath)
	if err != nil || c.Param("revision") != filepath.Base(root) {
		c.Status(http.StatusNotFound)
		return
	}
	name := strings.TrimPrefix(c.Param("filepath"), "/")
	if validatePackagePath(name) != nil {
		c.Status(http.StatusNotFound)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	// Never expose server scripts, executables, dotfiles, or arbitrary uploaded source files.
	switch ext {
	case ".html", ".css", ".js", ".mjs", ".json", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".otf":
	default:
		c.Status(http.StatusNotFound)
		return
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			c.Status(http.StatusNotFound)
			return
		}
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		c.Status(http.StatusNotFound)
		return
	}
	file, err := os.Open(target)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		c.Status(http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(ext)
	if ext == ".js" || ext == ".mjs" {
		contentType = "text/javascript; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Access-Control-Allow-Origin", "*") // allow ES modules and cross-origin resources
	c.Header("Cache-Control", "no-cache")
	// The template keeps full same-origin access; only safe defaults remain enforced on
	// the response so direct navigation of the HTML/SVG URL keeps them too.
	c.Header("Content-Security-Policy", "default-src * data: blob: 'unsafe-inline' 'unsafe-eval'; object-src 'none'; frame-ancestors 'self'")
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
}

// Vite's default base is "/". Rebase only root-relative HTML resources which
// actually exist in this ZIP; host navigation, API links and remote URLs stay unchanged.
func normalizeUploadedTemplateAssets(directory string, payload []byte) ([]byte, error) {
	tokenizer := html.NewTokenizer(bytes.NewReader(payload))
	var result bytes.Buffer
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if !errors.Is(tokenizer.Err(), io.EOF) {
				return nil, tokenizer.Err()
			}
			break
		}
		raw := append([]byte(nil), tokenizer.Raw()...)
		changed := false
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			token := tokenizer.Token()
			for i := range token.Attr {
				attr := &token.Attr[i]
				if attr.Key != "src" && attr.Key != "href" && attr.Key != "poster" {
					continue
				}
				if !strings.HasPrefix(attr.Val, "/") || strings.HasPrefix(attr.Val, "//") {
					continue
				}
				reference, err := url.Parse(attr.Val)
				if err != nil {
					continue
				}
				name := strings.TrimPrefix(reference.Path, "/")
				if validatePackagePath(name) != nil {
					continue
				}
				info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(name)))
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				attr.Val = "." + attr.Val
				changed = true
			}
			if changed {
				result.WriteString(token.String())
			}
		}
		if !changed {
			result.Write(raw)
		}
		if result.Len() > homeTemplateMaxBytes {
			return nil, errors.New("模板入口文件不能超过 2 MiB")
		}
	}
	return result.Bytes(), nil
}
