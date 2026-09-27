package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

// ========== 应用商店（插件中心） ==========
//
// 插件化能力位：支付服务商、实名认证服务商等以后续可扩展的方式注册。
// 每个插件是一条 plugins 表记录（id 唯一），enabled 控制该能力位使用哪个实现。
// 同一 category 下同时只允许一个插件处于启用状态（启用一个会自动停用同类其他插件）。

type pluginInfo struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Homepage    string         `json:"homepage"`
	Icon        string         `json:"icon"`
	Version     string         `json:"version"`
	Official    bool           `json:"official"`
	Author      templateAuthor `json:"author"`
	Enabled     bool           `json:"enabled"`
	Configured  bool           `json:"configured"`
	CanEnable   bool           `json:"canEnable"`   // 当前服务是否包含该插件的运行实现
	Local       bool           `json:"local"`       // 本地已有（内置或已安装）
	Source      string         `json:"source"`      // 内置: builtin；远程插件: 来源仓库名
	Remote      bool           `json:"remote"`      // 仅存在于远程仓库、本地未安装
	DownloadURL string         `json:"downloadUrl"` // 远程插件包地址（未安装时用于下载）
	Hidden      bool           `json:"-"`           // 暂时从应用商店隐藏，底层能力与历史状态保留
}

// pluginCatalog 内置插件清单（代码注册，数据库只持久化启用状态）。
var pluginCatalog = []pluginInfo{
	{
		ID:          "epay",
		Category:    "payment",
		Name:        "易支付 V1",
		Description: "彩虹易支付聚合支付接口（MD5 页面跳转版），支持支付宝 / 微信 / QQ 钱包收单",
		Icon:        "ri:bank-card-line",
		Version:     "1.0.0",
		Official:    true,
	},
	{
		ID:          "epay-v2",
		Category:    "payment",
		Name:        "易支付 V2",
		Description: "彩虹易支付 V2 接口（RSA-SHA256 服务端下单），支持支付宝 / 微信 / QQ 钱包收单",
		Icon:        "ri:bank-card-2-line",
		Version:     "2.0.0",
		Official:    true,
	},
	{
		ID:          "alipay-realname",
		Category:    "realname",
		Name:        "支付宝实名认证",
		Description: "金融级实人认证，支持扫码刷脸与支付宝信息比对授权两种模式，认证模式在系统配置中切换",
		Icon:        "ri:alipay-line",
		Version:     "1.0.0",
		Official:    true,
	},
	{
		ID:          "kuaitong-realname",
		Category:    "realname",
		Name:        "快瞳实名认证",
		Description: "支持姓名与身份证二要素核验，也可扫码拍照完成人脸认证",
		Icon:        "ri:id-card-line",
		Version:     "1.0.0",
		Official:    true,
	},
	{
		ID:          "tencent-realname",
		Category:    "realname",
		Name:        "靓仔聚合认证",
		Description: "靓仔聚合实名认证服务:接入地址为:http://real.4775.cn/",
		Homepage:    "http://real.4775.cn/",
		Icon:        "ri:id-card-line",
		Version:     "1.0.0",
		Official:    true,
	},
	{
		ID:          "xiaomu-realname",
		Category:    "realname",
		Name:        "小沐聚合实名",
		Description: "小沐聚合实名认证服务，支持三要素核验、人脸认证与微信实名，认证产品在系统配置中切换",
		Homepage:    "https://smapi.x1m1.cn/",
		Icon:        "ri:shield-user-line",
		Version:     "1.0.0",
		Official:    true,
	},
}

func findCatalogPlugin(id string) (pluginInfo, bool) {
	for _, p := range pluginCatalog {
		if p.ID == id {
			return p, true
		}
	}
	return pluginInfo{}, false
}

func listedCatalogPlugins() []pluginInfo {
	plugins := make([]pluginInfo, 0, len(pluginCatalog))
	for _, plugin := range pluginCatalog {
		if plugin.Hidden {
			continue
		}
		// 官方插件统一署名内置作者，保证目录信息字段完整。
		if plugin.Official && plugin.Author.Name == "" {
			plugin.Author = builtinAuthor
		}
		plugins = append(plugins, plugin)
	}
	return plugins
}

// ensurePluginStorage 幂等建表。
func ensurePluginStorage(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS plugins (
			id VARCHAR(60) NOT NULL PRIMARY KEY COMMENT '插件标识',
			category VARCHAR(30) NOT NULL DEFAULT '' COMMENT '能力分类',
			enabled TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否启用',
			updated_at DATETIME DEFAULT NULL COMMENT '更新时间',
			KEY idx_category (category)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='应用商店插件'
	`); err != nil {
		return err
	}
	if err := ensurePluginSourceStorage(db); err != nil {
		return err
	}
	if err := ensureSystemConfigStorage(db); err != nil {
		return err
	}
	if err := ensureDefaultPluginState(db); err != nil {
		return err
	}
	return ensureHomeTemplateStorage(db)
}

// ensureDefaultPluginState 只在腾讯实名插件尚无状态记录时执行一次默认迁移。
func ensureDefaultPluginState(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO plugins (id, category, enabled, updated_at)
		VALUES ('tencent-realname', 'realname', 1, NOW())
		ON DUPLICATE KEY UPDATE id = VALUES(id)
	`)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return tx.Commit()
	}

	if _, err := tx.Exec(`
		UPDATE plugins SET enabled = 0, updated_at = NOW()
		WHERE category = 'realname' AND id != 'tencent-realname'
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO system_configs (` + "`group`" + `, ` + "`key`" + `, value, description)
		VALUES ('realname', 'provider', 'tencent', '实名认证服务商(alipay/kuaitong/tencent)')
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`); err != nil {
		return err
	}
	return tx.Commit()
}

func loadPluginEnabledMap(db *sql.DB) (map[string]bool, error) {
	enabled := map[string]bool{}
	rows, err := db.Query("SELECT id, enabled FROM plugins")
	if err != nil {
		return enabled, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var en int
		if err := rows.Scan(&id, &en); err != nil {
			return enabled, err
		}
		enabled[id] = en == 1
	}
	return enabled, rows.Err()
}

// pluginConfigured 判断插件是否已填写可用配置。
func pluginConfigured(db *sql.DB, id string) bool {
	switch id {
	case "epay":
		cfg, err := loadEpayConfig(db)
		return err == nil && cfg.Gateway != "" && cfg.PID != "" && cfg.Key != ""
	case "epay-v2":
		cfg, err := loadEpayV2Config(db)
		return err == nil && cfg.Gateway != "" && cfg.PID != "" && cfg.MerchantKey != "" && cfg.PlatformKey != ""
	case "alipay-realname":
		cfg, err := loadRealnameConfig(db)
		return err == nil && cfg.AppID != "" && cfg.PrivateKey != "" && cfg.AlipayPublicKey != ""
	case "kuaitong-realname":
		cfg, err := loadRealnameConfig(db)
		return err == nil && cfg.KuaitongAccessKey != "" && cfg.KuaitongSecret != ""
	case "tencent-realname":
		cfg, err := loadRealnameConfig(db)
		return err == nil && cfg.TencentAPIKey != "" && cfg.TencentAPISecret != "" && cfg.TencentBaseURL != "" && cfg.TencentProductCode != ""
	case "xiaomu-realname":
		cfg, err := loadRealnameConfig(db)
		return err == nil && cfg.XiaomuAppKey != "" && cfg.XiaomuAppSecret != "" && cfg.XiaomuBaseURL != "" && cfg.XiaomuProductMode != ""
	}
	return false
}

// AdminPluginList 插件列表，按分类分组返回；合并本地插件与各软件源的远程插件。
// 支持 ?source=<id> 只看某个软件源、?source=local 只看本地、?q= 关键词过滤。
func normalizePluginCategory(category string) string {
	switch category {
	case "payment", "realname":
		return category
	}
	return "other"
}

// loadLocalPluginIDs 返回本地已有的插件 id（内置 + plugins 目录下已完成安装）。
func loadLocalPluginIDs() (map[string]bool, error) {
	ids := map[string]bool{}
	for _, plugin := range pluginCatalog {
		ids[plugin.ID] = true
	}
	plugins, err := loadLocalPlugins()
	if err != nil {
		return nil, err
	}
	for _, plugin := range plugins {
		ids[plugin.ID] = true
	}
	return ids, nil
}

var pluginSourceURLPattern = regexp.MustCompile(`^https?://`)

func looksLikeGitRepositoryURL(rawURL string) bool {
	value := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rawURL)), "/")
	return strings.HasSuffix(value, ".git") || strings.Contains(value, "github.com/") && !strings.HasSuffix(value, ".json")
}

func validatePluginSourceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !pluginSourceURLPattern.MatchString(raw) {
		return "", errors.New("仓库地址必须以 http:// 或 https:// 开头")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("仓库地址格式不正确")
	}
	return raw, nil
}

// AdminPluginSourceAdd 添加软件源；会立即拉取一次仓库清单做校验。
// AdminPluginSourceDelete 删除软件源（不影响已下载到本地的插件）。
// ========== 插件下载 ==========

var pluginIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,58}$`)

// AdminPluginDownload 从软件源下载插件包到本地 plugins 目录。
// AdminPluginToggle 启用/停用插件；启用时自动停用同分类其他插件。
func AdminPluginToggle(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	plugin, ok := findCatalogPlugin(id)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"code": 404, "msg": "插件不存在"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	db, err := openSystemConfigDB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}

	if err := ensurePluginStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化插件存储失败"})
		return
	}

	if req.Enabled {
		// 同分类互斥：先停用同类，再启用目标
		if _, err := db.Exec("UPDATE plugins SET enabled = 0, updated_at = NOW() WHERE category = ? AND id != ?", plugin.Category, id); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "更新插件状态失败"})
			return
		}
	}

	enabledVal := 0
	if req.Enabled {
		enabledVal = 1
	}
	if _, err := db.Exec(`
		INSERT INTO plugins (id, category, enabled, updated_at)
		VALUES (?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), updated_at = NOW()
	`, id, plugin.Category, enabledVal); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存插件状态失败"})
		return
	}

	// 实名/支付的启用插件变更后，同步对应模块配置中的 provider，保持一处生效
	switch plugin.Category {
	case "realname":
		provider := realnameProviderAlipay
		if req.Enabled && id == "kuaitong-realname" {
			provider = realnameProviderKuaitong
		} else if req.Enabled && id == "tencent-realname" {
			provider = realnameProviderTencent
		} else if req.Enabled && id == "xiaomu-realname" {
			provider = realnameProviderXiaomu
		}
		_, _ = db.Exec(`
			INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
			VALUES ('realname', 'provider', ?, '实名认证服务商(alipay/kuaitong/tencent/xiaomu)')
			ON DUPLICATE KEY UPDATE value = VALUES(value)
		`, provider)
	}

	msg := "插件已停用"
	if req.Enabled {
		msg = "插件已启用"
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": msg})
}

// isPluginEnabled 供其他模块查询插件是否启用；存储异常时按未启用处理。
func isPluginEnabled(db *sql.DB, id string) bool {
	if err := ensurePluginStorage(db); err != nil {
		return false
	}
	var enabled int
	if err := db.QueryRow("SELECT enabled FROM plugins WHERE id = ?", id).Scan(&enabled); err != nil {
		return false
	}
	return enabled == 1
}

// realnamePluginIDByProvider 反查 provider 对应的插件 id。
func realnamePluginIDByProvider(provider string) string {
	if provider == realnameProviderKuaitong {
		return "kuaitong-realname"
	}
	if provider == realnameProviderTencent {
		return "tencent-realname"
	}
	if provider == realnameProviderXiaomu {
		return "xiaomu-realname"
	}
	return "alipay-realname"
}

var _ = json.Marshal // 保留 encoding/json 引用，后续插件自定义元数据会用到
