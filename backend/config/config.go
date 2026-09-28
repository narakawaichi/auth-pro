package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DBConfig 数据库配置
type DBConfig struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

var (
	dbConfig *DBConfig
	mu       sync.RWMutex

	jwtSecret []byte
	secretMu  sync.RWMutex
)

// AppVersion 是当前系统整体版本号。前后端共用该版本，发布时通过 -ldflags 注入。
var AppVersion = "1.0.0"

// BuildTime 是二进制构建时间，发布时通过 -ldflags 注入。
var BuildTime = ""

// DefaultUpdateManifestURL 是默认的在线更新清单地址，可用 AUTO_PRO_UPDATE_URL 覆盖。
const DefaultUpdateManifestURL = "https://gitee.com/api/v5/repos/Zcy-sa/auth-pro/releases/latest"

// GetDataDir 获取运行数据目录。
func GetDataDir() string {
	return getDataDir()
}

// GetUpdateManifestURL 获取在线更新清单地址。
func GetUpdateManifestURL() string {
	if value := os.Getenv("AUTO_PRO_UPDATE_URL"); value != "" {
		return value
	}
	return DefaultUpdateManifestURL
}

// GetUpdateDir 获取更新包与更新日志目录。
func GetUpdateDir() string {
	dir := filepath.Join(getDataDir(), "updates")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetAppReleaseDir 获取应用版本更新包目录。
func GetAppReleaseDir() string {
	if dir := os.Getenv("AUTO_PRO_APP_RELEASE_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0755)
		return dir
	}
	dir := filepath.Join(getDataDir(), "app-releases")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetFrontendDir 获取当前正在提供服务的前端目录。
// 优先使用独立的 frontend/current；宝塔根目录部署时自动识别二进制上级的网站根目录。
func GetFrontendDir() string {
	if dir := os.Getenv("AUTO_PRO_FRONTEND_DIR"); dir != "" {
		return dir
	}

	executable, _ := os.Executable()
	return resolveFrontendDir(getDataDir(), executable)
}

func resolveFrontendDir(dataDir string, executable string) string {
	candidates := []string{
		filepath.Join(dataDir, "frontend", "current"),
		filepath.Join(filepath.Dir(dataDir), "frontend", "current"),
	}
	fallback := candidates[len(candidates)-1]
	if executable != "" {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates, executableDir, filepath.Dir(executableDir))
	}
	for _, dir := range candidates {
		if fileExists(filepath.Join(dir, "index.html")) {
			return dir
		}
	}
	return fallback
}

// GetServiceName 获取 systemd 服务名。
func GetServiceName() string {
	if name := os.Getenv("AUTO_PRO_SERVICE_NAME"); name != "" {
		return name
	}
	return "auth_pro"
}

// 获取数据文件目录
func getDataDir() string {
	if dir := os.Getenv("AUTO_PRO_DATA_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0755)
		return dir
	}

	cwd, _ := os.Getwd()
	executable, _ := os.Executable()
	return resolveDataDir(cwd, executable)
}

// resolveDataDir 在未显式配置 AUTO_PRO_DATA_DIR 时确定运行数据目录。
// 生产环境通常由 systemd 启动，进程工作目录可能是 / 或 unit 中的其他目录；
// 优先检查可执行文件所在目录可以避免在线更新重启后读取另一份 db.json。
func resolveDataDir(cwd string, executable string) string {
	candidates := make([]string, 0, 3)
	if executable != "" {
		candidates = append(candidates, filepath.Dir(executable))
	}
	if cwd != "" {
		candidates = append(candidates, cwd, filepath.Join(cwd, "backend"))
	}

	for _, dir := range candidates {
		if fileExists(filepath.Join(dir, "install.lock")) || fileExists(filepath.Join(dir, "db.json")) || fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
	}

	if executable != "" {
		return filepath.Dir(executable)
	}
	if cwd != "" {
		return cwd
	}
	return "."
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// 获取配置文件路径
func getConfigPath() string {
	return filepath.Join(getDataDir(), "db.json")
}

// 获取 install.lock 路径
func GetLockPath() string {
	return filepath.Join(getDataDir(), "install.lock")
}

// IsInstalled 检查是否已安装
func IsInstalled() bool {
	_, err := os.Stat(GetLockPath())
	return err == nil
}

// CreateLockFile 创建安装锁文件
func CreateLockFile() error {
	return os.WriteFile(GetLockPath(), []byte("installed"), 0644)
}

// SaveDBConfig 保存数据库配置
func SaveDBConfig(cfg *DBConfig) error {
	mu.Lock()
	defer mu.Unlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dbConfig = cfg
	return os.WriteFile(getConfigPath(), data, 0644)
}

// LoadDBConfig 加载数据库配置
func LoadDBConfig() (*DBConfig, error) {
	if cfg, ok := loadDBConfigFromEnv(); ok {
		return cfg, nil
	}

	mu.RLock()
	if dbConfig != nil {
		defer mu.RUnlock()
		return dbConfig, nil
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(getConfigPath())
	if err != nil {
		return nil, err
	}

	var cfg DBConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	dbConfig = &cfg
	return dbConfig, nil
}

// GetDSN 获取数据库连接字符串
func GetDSN(cfg *DBConfig) string {
	network := "tcp"
	address := cfg.Host + ":" + cfg.Port
	if strings.HasPrefix(cfg.Host, "unix:") {
		network = "unix"
		address = strings.TrimPrefix(cfg.Host, "unix:")
	}
	return cfg.Username + ":" + cfg.Password + "@" + network + "(" + address + ")/" + cfg.Database + "?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true"
}

func loadDBConfigFromEnv() (*DBConfig, bool) {
	host := strings.TrimSpace(os.Getenv("AUTO_PRO_DB_HOST"))
	if host == "" {
		return nil, false
	}
	port := strings.TrimSpace(os.Getenv("AUTO_PRO_DB_PORT"))
	if port == "" && !strings.HasPrefix(host, "unix:") {
		port = "3306"
	}
	return &DBConfig{
		Host:     host,
		Port:     port,
		Database: strings.TrimSpace(os.Getenv("AUTO_PRO_DB_NAME")),
		Username: os.Getenv("AUTO_PRO_DB_USER"),
		Password: os.Getenv("AUTO_PRO_DB_PASSWORD"),
	}, true
}

// GetPort 获取服务端口
// GetPluginDir 获取本地插件存放目录
func GetPluginDir() string {
	dir := filepath.Join(getDataDir(), "plugins")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetHomeTemplateDir 获取已安装首页模板的存放目录。
func GetHomeTemplateDir() string {
	dir := filepath.Join(getDataDir(), "home-templates")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// 官方软件源连接信息随后端编译，不从环境变量、配置文件或管理页面读取。
// 固定 Key 仅用于目录读取；内置不代表保密，不能用于管理写权限。
const (
	softwareSourceURL    = "https://plug.91ani.cn"
	softwareSourceAPIKey = "317e605c32344a416c907b9fad0a26b9adf87410554707c7f09d447b2de61cff"
)

func GetSoftwareSourceURL() string {
	return softwareSourceURL
}

func GetSoftwareSourceAPIKey() string {
	return softwareSourceAPIKey
}

func GetSoftwareSourceAdminURL() string {
	if value := strings.TrimSpace(os.Getenv("AUTO_PRO_SOFTWARE_SOURCE_ADMIN_URL")); value != "" {
		return strings.TrimRight(value, "/") + "/"
	}
	return GetSoftwareSourceURL() + "/admin/"
}

func GetSoftwareSourceTimeout() time.Duration {
	return durationEnv("AUTO_PRO_SOFTWARE_SOURCE_TIMEOUT", 5*time.Second)
}

func GetSoftwareSourceStaleTTL() time.Duration {
	return durationEnv("AUTO_PRO_SOFTWARE_SOURCE_STALE_TTL", 24*time.Hour)
}

func GetSoftwareSourceCacheDir() string {
	dir := filepath.Join(getDataDir(), "software-source-cache")
	_ = os.MkdirAll(dir, 0750)
	return dir
}

// DefaultAdvertisementURL 是默认的广告投放接口地址，可用 AUTO_PRO_ADVERTISEMENT_URL 覆盖。
const DefaultAdvertisementURL = "https://plug.91ani.cn/api/v1/public/advertisements"

func GetAdvertisementURL() string {
	return strings.TrimRight(strings.TrimSpace(envOrDefault("AUTO_PRO_ADVERTISEMENT_URL", DefaultAdvertisementURL)), "/")
}

func GetAdvertisementTimeout() time.Duration {
	return durationEnv("AUTO_PRO_ADVERTISEMENT_TIMEOUT", 5*time.Second)
}

// GetAdvertisementCacheTTL 是投放内容的新鲜期。广告图片是带签名的临时地址，
// 缓存过久会让前端拿到已过期的链接，所以默认只留 5 分钟。
func GetAdvertisementCacheTTL() time.Duration {
	return durationEnv("AUTO_PRO_ADVERTISEMENT_CACHE_TTL", 5*time.Minute)
}

// GetAdvertisementStaleTTL 是上游不可用时旧内容的容忍期，超出后广告位改回占位。
func GetAdvertisementStaleTTL() time.Duration {
	return durationEnv("AUTO_PRO_ADVERTISEMENT_STALE_TTL", time.Hour)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func GetPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "19127"
	}
	return port
}

// GetHost 获取服务监听地址，生产环境应设置为 127.0.0.1。
func GetHost() string {
	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	return host
}

// GetJWTSecretPath 获取 JWT 密钥文件路径
func GetJWTSecretPath() string {
	return filepath.Join(getDataDir(), "jwt.secret")
}

// LoadOrCreateJWTSecret 读取 JWT 密钥；文件不存在时生成随机密钥并持久化。
// 安装流程中会调用本函数生成密钥，之后所有进程复用同一密钥。
func LoadOrCreateJWTSecret() ([]byte, error) {
	secretMu.RLock()
	if jwtSecret != nil {
		defer secretMu.RUnlock()
		return jwtSecret, nil
	}
	secretMu.RUnlock()

	secretMu.Lock()
	defer secretMu.Unlock()
	if jwtSecret != nil {
		return jwtSecret, nil
	}

	if data, err := os.ReadFile(GetJWTSecretPath()); err == nil {
		if secret := bytes.TrimSpace(data); len(secret) >= 32 {
			jwtSecret = secret
			return jwtSecret, nil
		}
	}

	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	secret := []byte(hex.EncodeToString(raw))
	if err := os.WriteFile(GetJWTSecretPath(), secret, 0600); err != nil {
		return nil, err
	}
	jwtSecret = secret
	return jwtSecret, nil
}
