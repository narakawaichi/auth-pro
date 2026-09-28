package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetDSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  DBConfig
		want string
	}{
		{
			name: "tcp",
			cfg:  DBConfig{Host: "127.0.0.1", Port: "3306", Database: "auto_pro", Username: "root", Password: "secret"},
			want: "root:secret@tcp(127.0.0.1:3306)/auto_pro?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true",
		},
		{
			name: "unix socket",
			cfg:  DBConfig{Host: "unix:/tmp/mysql.sock", Database: "auto_pro", Username: "root"},
			want: "root:@unix(/tmp/mysql.sock)/auto_pro?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetDSN(&tt.cfg); got != tt.want {
				t.Fatalf("GetDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSoftwareSourceConfig(t *testing.T) {
	t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_URL", "http://127.0.0.1:19128/")
	t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_ADMIN_URL", "https://source.example.com/admin")
	t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_TIMEOUT", "3s")
	t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_STALE_TTL", "12h")
	if got := GetSoftwareSourceURL(); got != "https://plug.91ani.cn" {
		t.Fatalf("software source URL = %q", got)
	}
	if got := GetSoftwareSourceAdminURL(); got != "https://source.example.com/admin/" {
		t.Fatalf("software source admin URL = %q", got)
	}
	if GetSoftwareSourceTimeout() != 3*time.Second || GetSoftwareSourceStaleTTL() != 12*time.Hour {
		t.Fatal("software source durations were not parsed")
	}
}

func TestSoftwareSourceConnectionIsBuiltin(t *testing.T) {
	for _, value := range []string{"", "   ", "deployment-override"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_URL", value)
			t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_API_KEY", value)
			t.Setenv("AUTO_PRO_SOFTWARE_SOURCE_ADMIN_URL", "")
			if GetSoftwareSourceURL() != "https://plug.91ani.cn" {
				t.Fatal("software source URL must not depend on environment variables")
			}
			if len(GetSoftwareSourceAPIKey()) != 64 || GetSoftwareSourceAPIKey() == value {
				t.Fatal("software source key must be built in, not supplied by the environment")
			}
			if GetSoftwareSourceAdminURL() != "https://plug.91ani.cn/admin/" {
				t.Fatal("default software source admin URL must use the built-in host")
			}
		})
	}
}

func TestAdvertisementConfig(t *testing.T) {
	t.Setenv("AUTO_PRO_ADVERTISEMENT_URL", "")
	if got := GetAdvertisementURL(); got != DefaultAdvertisementURL {
		t.Fatalf("默认广告接口地址 = %q", got)
	}
	// 投放方给出的地址常带多余的尾斜杠，拼 query 前必须归一化
	t.Setenv("AUTO_PRO_ADVERTISEMENT_URL", " https://plug.example.com/api/v1/public/advertisements// ")
	t.Setenv("AUTO_PRO_ADVERTISEMENT_TIMEOUT", "2s")
	t.Setenv("AUTO_PRO_ADVERTISEMENT_CACHE_TTL", "30s")
	t.Setenv("AUTO_PRO_ADVERTISEMENT_STALE_TTL", "6h")
	if got := GetAdvertisementURL(); got != "https://plug.example.com/api/v1/public/advertisements" {
		t.Fatalf("广告接口地址 = %q", got)
	}
	if GetAdvertisementTimeout() != 2*time.Second || GetAdvertisementCacheTTL() != 30*time.Second ||
		GetAdvertisementStaleTTL() != 6*time.Hour {
		t.Fatal("广告相关时长未按环境变量解析")
	}
}

func TestLoadDBConfigFromEnv(t *testing.T) {
	t.Setenv("AUTO_PRO_DB_HOST", "unix:/tmp/auto-pro-test.sock")
	t.Setenv("AUTO_PRO_DB_PORT", "")
	t.Setenv("AUTO_PRO_DB_NAME", "auto_pro_test")
	t.Setenv("AUTO_PRO_DB_USER", "test_user")
	t.Setenv("AUTO_PRO_DB_PASSWORD", "test_password")

	cfg, err := LoadDBConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "unix:/tmp/auto-pro-test.sock" || cfg.Port != "" || cfg.Database != "auto_pro_test" || cfg.Username != "test_user" || cfg.Password != "test_password" {
		t.Fatalf("unexpected environment database config: %#v", cfg)
	}
}

func TestDefaultUpdateManifestURL(t *testing.T) {
	t.Setenv("AUTO_PRO_UPDATE_URL", "")
	if got := GetUpdateManifestURL(); got != "https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest" {
		t.Fatalf("GetUpdateManifestURL() = %q", got)
	}
}

func TestResolveFrontendDirForWebsiteRoot(t *testing.T) {
	root := t.TempDir()
	backendDir := filepath.Join(root, "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveFrontendDir(backendDir, filepath.Join(backendDir, "auth_pro"))
	if got != root {
		t.Fatalf("resolveFrontendDir() = %q, want %q", got, root)
	}
}

func TestGetUpdateManifestURL(t *testing.T) {
	t.Setenv("AUTO_PRO_UPDATE_URL", "")
	if got := GetUpdateManifestURL(); got != "https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest" {
		t.Fatalf("GetUpdateManifestURL() = %q", got)
	}

	t.Setenv("AUTO_PRO_UPDATE_URL", "https://mirror.example.com/latest.json")
	if got := GetUpdateManifestURL(); got != "https://mirror.example.com/latest.json" {
		t.Fatalf("GetUpdateManifestURL() override = %q", got)
	}
}

func TestResolveDataDirPrefersExecutableDirectory(t *testing.T) {
	root := t.TempDir()
	executableDir := filepath.Join(root, "release", "backend")
	cwd := filepath.Join(root, "working")
	if err := os.MkdirAll(executableDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(executableDir, "db.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := resolveDataDir(cwd, filepath.Join(executableDir, "auth_pro")); got != executableDir {
		t.Fatalf("resolveDataDir() = %q, want %q", got, executableDir)
	}
}

func TestResolveDataDirFallsBackToWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "working")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "install.lock"), []byte("installed"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := resolveDataDir(cwd, filepath.Join(root, "missing", "auth_pro")); got != cwd {
		t.Fatalf("resolveDataDir() = %q, want %q", got, cwd)
	}
}

func TestGetFrontendDirOverride(t *testing.T) {
	frontendDir := t.TempDir()
	t.Setenv("AUTO_PRO_FRONTEND_DIR", frontendDir)
	if got := GetFrontendDir(); got != frontendDir {
		t.Fatalf("GetFrontendDir() = %q, want %q", got, frontendDir)
	}
}
