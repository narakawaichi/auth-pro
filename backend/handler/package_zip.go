package handler

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"auto_pro/config"
)

const (
	packageMaxFiles                = 2048
	packageMaxExtractedBytes int64 = 100 << 20
)

// ZIP paths are validated using both Unix and Windows rules, regardless of the host OS.
func validatePackagePath(name string) error {
	if name == "" || len(name) > 512 || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return errors.New("压缩包包含不安全的文件路径")
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	if len(parts) > 32 {
		return errors.New("压缩包目录层级过深")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, ". ") != part {
			return errors.New("压缩包包含不安全的文件路径")
		}
		for _, r := range part {
			if r < 32 || strings.ContainsRune(`<>"|?*`, r) {
				return errors.New("压缩包包含不安全的文件名")
			}
		}
		stem := strings.ToUpper(strings.TrimRight(strings.SplitN(part, ".", 2)[0], " "))
		stemRunes := []rune(stem)
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || stem == "CLOCK$" ||
			(len(stemRunes) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", stemRunes[3])) {
			return errors.New("压缩包包含系统保留文件名")
		}
	}
	return nil
}

// destination must be a new private staging directory, never a live installation.
func extractPackageZIP(payload []byte, destination string) error {
	if int64(len(payload)) > pluginPackageMaxSize {
		return errors.New("ZIP 不能超过 20 MiB")
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return errors.New("插件/模板包必须是有效的 ZIP 压缩包")
	}
	if len(archive.File) == 0 || len(archive.File) > packageMaxFiles {
		return fmt.Errorf("ZIP 必须包含文件，且条目数不能超过 %d", packageMaxFiles)
	}
	seen := make(map[string]bool)
	var total int64
	fileCount := 0
	for _, entry := range archive.File {
		if err := validatePackagePath(entry.Name); err != nil {
			return err
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return errors.New("ZIP 不允许符号链接或特殊文件")
		}
		key := strings.ToLower(strings.TrimSuffix(entry.Name, "/"))
		if seen[key] {
			return errors.New("ZIP 包含重复或大小写冲突的路径")
		}
		seen[key] = true
		if entry.UncompressedSize64 > uint64(packageMaxExtractedBytes-total) {
			return errors.New("ZIP 解压后总大小不能超过 100 MiB")
		}
		if strings.HasPrefix(entry.Name, "__MACOSX/") || path.Base(entry.Name) == ".DS_Store" {
			continue
		}
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			source.Close()
			return err
		}
		n, copyErr := io.Copy(file, io.LimitReader(source, packageMaxExtractedBytes-total+1))
		closeErr := file.Close()
		source.Close()
		if copyErr != nil {
			return fmt.Errorf("ZIP 文件解压/校验失败：%w", copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		total += n
		if total > packageMaxExtractedBytes {
			return errors.New("ZIP 解压后总大小不能超过 100 MiB")
		}
		fileCount++
	}
	if fileCount == 0 {
		return errors.New("ZIP 不包含可安装文件")
	}
	return nil
}

// Accept common ZIP layouts such as dist/index.html and project/plugin.json.
func packageContentRoot(directory string) (string, error) {
	for {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return "", err
		}
		if len(entries) != 1 || !entries[0].IsDir() {
			return directory, nil
		}
		directory = filepath.Join(directory, entries[0].Name())
	}
}

// IsInstalledPackageFile prevents the ordinary static server from serving installed
// template/plugin files when runtime data lives under the web root, keeping them on the
// dedicated asset endpoint that applies the security headers.
func IsInstalledPackageFile(filename string) bool {
	candidates := []string{filename}
	if resolved, err := filepath.EvalSymlinks(filename); err == nil {
		candidates = append(candidates, resolved)
	}
	for _, directory := range []string{config.GetPluginDir(), config.GetHomeTemplateDir()} {
		roots := []string{directory}
		if resolved, err := filepath.EvalSymlinks(directory); err == nil {
			roots = append(roots, resolved)
		}
		for _, root := range roots {
			root, err := filepath.Abs(root)
			if err != nil {
				continue
			}
			for _, candidate := range candidates {
				candidate, err := filepath.Abs(candidate)
				if err != nil {
					continue
				}
				rel, err := filepath.Rel(root, candidate)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
					return true
				}
			}
		}
	}
	return false
}
