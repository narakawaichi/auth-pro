package handler

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// sdkDownloadMaxFileBytes 限制单个 SDK 文件大小，作为嵌入内容异常时的兜底。
const sdkDownloadMaxFileBytes = 1 << 20

// AdminSdkDownload 把内嵌的 SDK 示例目录即时打包成 zip，供后台 SDK 示例页下载。
func AdminSdkDownload(sdkFS fs.FS) gin.HandlerFunc {
	return func(c *gin.Context) {
		payload, filename, err := buildSdkZip(sdkFS, c.Param("lang"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": err.Error()})
			return
		}
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		c.Header("Content-Type", "application/zip")
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Checksum-SHA256", actualChecksumString(sha256.Sum256(payload)))
		c.Data(http.StatusOK, "application/zip", payload)
	}
}

// buildSdkZip 把 sdkFS 根下名为 lang 的目录打包成 zip（顶层带 auth-pro-<lang>-sdk/ 前缀），
// 返回压缩内容与下载文件名。
func buildSdkZip(sdkFS fs.FS, lang string) ([]byte, string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	for _, r := range lang {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return nil, "", errors.New("SDK 语言标识不合法")
		}
	}
	if lang == "" {
		return nil, "", errors.New("SDK 语言标识不能为空")
	}
	if info, err := fs.Stat(sdkFS, lang); err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("暂未提供 %s 语言的 SDK 示例", lang)
	}

	var names []string
	if err := fs.WalkDir(sdkFS, lang, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("SDK 目录包含非常规文件：%s", name)
		}
		names = append(names, name)
		return nil
	}); err != nil {
		return nil, "", fmt.Errorf("读取 SDK 目录失败：%w", err)
	}
	if len(names) == 0 {
		return nil, "", fmt.Errorf("%s 语言的 SDK 示例为空", lang)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	prefix := "auth-pro-" + lang + "-sdk/"
	for _, name := range names {
		data, err := fs.ReadFile(sdkFS, name)
		if err != nil {
			return nil, "", fmt.Errorf("读取 SDK 文件失败：%w", err)
		}
		if len(data) > sdkDownloadMaxFileBytes {
			return nil, "", fmt.Errorf("SDK 文件过大：%s", name)
		}
		entry, err := writer.Create(prefix + strings.TrimPrefix(name, lang+"/"))
		if err != nil {
			return nil, "", err
		}
		if _, err := entry.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), strings.TrimSuffix(prefix, "/") + ".zip", nil
}
