package handler

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"auto_pro/config"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

// ========== 实名认证（支付宝 / 快瞳 / 靓仔聚合认证） ==========
//
// 支持三种服务商，管理员在系统设置「实名认证」分区切换：
//
// 支付宝（provider=alipay）：金融级实人认证，扫码刷脸。
//   - datadigital.fincloud.generalsaas.face.certify.initialize 初始化认证，生成 certify_id
//   - datadigital.fincloud.generalsaas.face.certify.query 查询认证结果
//
// 快瞳（provider=kuaitong）：支持身份证二要素核验与扫码人脸认证。
//   - GET  /s/api/getAccessToken 获取鉴权 token（服务端缓存，401/403 时主动刷新）
//   - POST /s/api/ocr/cloudCode/IdCard multipart 提交 token/idCard/realName，直接完成二要素核验
//   - POST /s/api/ocr/cloudCode/certificate multipart 提交 token/idCard/realName + 人像照片
//     （imgBase64，相似度分数 pic > 60 判定为同一人）
//   管理员选择二要素认证时，用户提交姓名+身份证号后直接核验，无需扫码；选择人脸认证时，
//   生成认证单与二维码，用户扫码打开本站拍照页 /realname-face?t=token 自动抓拍并完成比对。
//
// 靓仔聚合认证（provider=tencent）：生成本站拍照单，用户扫码打开本站拍照页
// /realname-face?t=token 自动抓拍，提交时调用靓仔 /verify 接口传入姓名、身份证和人脸图片完成核验。
//
// 管理员可按应用勾选哪些应用强制要求实名；开启后对应应用的授权校验接口
// 会对未实名用户返回 realname_required，拒绝安装。

const (
	realnameGroup              = "realname"
	realnameProviderAlipay     = "alipay"
	realnameProviderKuaitong   = "kuaitong"
	realnameProviderTencent    = "tencent"
	kuaitongAuthTypeFace       = "face"
	kuaitongAuthTypeTwoElement = "two_element"
	// 实名生效范围：by_app 沿用按应用勾选；all 对所有应用（含今后新增）强制实名。
	realnameScopeByApp = "by_app"
	realnameScopeAll   = "all"
	// 认证记录类型：init 首次认证；review 有效期内的再次核验或过期后的重新认证。
	realnameRecordTypeInit   = "init"
	realnameRecordTypeReview = "review"
	// 存量实名数据处理策略：keep 保持永不过期；backfill 按认证时间补算有效期。
	realnameLegacyKeep     = "keep"
	realnameLegacyBackfill = "backfill"
	// 默认有效期相关取值：0 天有效期表示永不过期。
	defaultRealnameGraceDays  = 15
	defaultRealnameRemindDays = 30
	// 实名判定结果：ok 无需拦截；grace 宽限期内放行但提示复核；
	// never 从未实名；expired 已过期且超出宽限期。
	realnameStatusOK      = "ok"
	realnameStatusGrace   = "grace"
	realnameStatusNever   = "never"
	realnameStatusExpired = "expired"
	// 授权校验失败原因，供 verify_logs 归因与前端区分提示。
	verifyReasonRealnameRequired = "realname_required"
	verifyReasonRealnameExpired  = "realname_expired"
	alipayDefaultGateway       = "https://openapi.alipay.com/gateway.do"
	alipaySandboxGateway       = "https://openapi-sandbox.dl.alipaydev.com/gateway.do"
	realnameInitMethod         = "datadigital.fincloud.generalsaas.face.certify.initialize"
	realnameQueryMethod        = "datadigital.fincloud.generalsaas.face.certify.query"
	realnameH5Prefix           = "https://certifyweb.alipay.com/certify/h5?certifyId="
	alipayAuthModeFace         = "face" // 扫码刷脸（face.certify 系列）
	alipayAuthModeInfo         = "info" // 实名信息验证：preconsult 录入 + 用户授权 + consult 比对
	alipayOauthMethod          = "alipay.system.oauth.token"
	alipayPreconsultMethod     = "alipay.user.certdoc.certverify.preconsult"
	alipayConsultMethod        = "alipay.user.certdoc.certverify.consult"
	alipayAuthorizeURL         = "https://openauth.alipay.com/oauth2/publicAppAuthorize.htm"
	alipaySandboxAuthorizeURL  = "https://openauth-sandbox.dl.alipaydev.com/oauth2/publicAppAuthorize.htm"
	alipayCallbackPath         = "/api/realname/alipay-callback"
	kuaitongTokenURL           = "https://ai.inspirvision.cn/s/api/getAccessToken"
	kuaitongVerifyURL          = "https://ai.inspirvision.cn/s/api/ocr/cloudCode/certificate"
	kuaitongIDCardURL          = "https://ai.inspirvision.cn/s/api/ocr/cloudCode/IdCard"
	tencentRealnameBaseURL     = "https://real.4775.cn/common/openapi"
	tencentRealnameProduct     = "cloud_tencent_renlian_zq"
	kuaitongTokenRefreshSkew   = time.Hour // token 剩余有效期不足该值时提前刷新
	kuaitongPassScore          = 60        // 快瞳人证合一判定阈值：相似度分数 > 60 为同一人
	maxRealNameLen             = 30
	realnameHTTPTimeout        = 10 * time.Second
	tencentRealnameHTTPTimeout = 30 * time.Second
	realnameCertifyIDMaxKeep   = 24 * time.Hour
)

type realnameConfig struct {
	Enabled            bool
	PluginEnabled      bool // 当前 provider 对应的插件是否在应用商店中启用
	Provider           string
	AppID              string
	PrivateKey         string
	AlipayPublicKey    string
	Gateway            string
	AlipayAuthMode     string // face 扫码刷脸 / info 实名信息验证（preconsult + 授权 + consult）
	KuaitongAccessKey  string
	KuaitongSecret     string
	KuaitongAuthType   string
	TencentAPIKey      string
	TencentAPISecret   string
	TencentBaseURL     string
	TencentProductCode string
	TencentUsePackage  bool
	XiaomuAppKey       string
	XiaomuAppSecret    string
	XiaomuBaseURL      string
	XiaomuProductMode  string
	RequireAppIDs      map[int64]bool
	// PR1 全局范围：by_app 按 RequireAppIDs 勾选；all 全部应用强制实名。
	Scope string
	// PR2 购买门槛：PurchaseRequired 购买/开通前要求实名；
	// PurchaseCheckOwner 代理为名下用户开通时，同时要求目标用户实名。
	PurchaseRequired   bool
	PurchaseCheckOwner bool
	// PR3 有效期：ValidityDays 0 表示永不过期；GraceDays 为到期后的宽限天数；
	// RemindDays 为到期前提醒天数；AllowIdentityChange 复核是否允许更换姓名/证件。
	ValidityDays        int
	GraceDays           int
	RemindDays          int
	AllowIdentityChange bool
	// LegacyPolicy 存量实名数据处理策略：keep 保持永不过期；backfill 按认证时间补算。
	LegacyPolicy string
}

type realnameConfigResponse struct {
	Enabled            bool              `json:"enabled"`
	PluginEnabled      bool              `json:"pluginEnabled"`
	Provider           string            `json:"provider"`
	AppID              string            `json:"appId"`
	PrivateKeySet      bool              `json:"privateKeySet"`
	AlipayPublicKey    string            `json:"alipayPublicKey"`
	Gateway            string            `json:"gateway"`
	AlipayAuthMode     string            `json:"alipayAuthMode"`
	KuaitongAccessKey  string            `json:"kuaitongAccessKey"`
	KuaitongSecretSet  bool              `json:"kuaitongSecretSet"`
	KuaitongAuthType   string            `json:"kuaitongAuthType"`
	TencentAPIKey      string            `json:"tencentApiKey"`
	TencentSecretSet   bool              `json:"tencentSecretSet"`
	TencentBaseURL     string            `json:"tencentBaseUrl"`
	TencentUsePackage  bool              `json:"tencentUsePackage"`
	TencentProductCode string            `json:"tencentProductCode"`
	XiaomuAppKey       string            `json:"xiaomuAppKey"`
	XiaomuSecretSet    bool              `json:"xiaomuSecretSet"`
	XiaomuBaseURL      string            `json:"xiaomuBaseUrl"`
	XiaomuProductMode  string            `json:"xiaomuProductMode"`
	RequireAppIDs      []int64           `json:"requireAppIds"`
	Scope              string            `json:"scope"`
	PurchaseRequired   bool              `json:"purchaseRequired"`
	PurchaseCheckOwner bool              `json:"purchaseCheckOwner"`
	ValidityDays       int               `json:"validityDays"`
	GraceDays          int               `json:"graceDays"`
	RemindDays         int               `json:"remindDays"`
	AllowIdentityChange bool             `json:"allowIdentityChange"`
	LegacyPolicy       string            `json:"legacyPolicy"`
	Apps               []realnameAppItem `json:"apps"`
}

type realnameAppItem struct {
	ID      int64  `json:"id"`
	AppName string `json:"appName"`
	AppKey  string `json:"appKey"`
}

type updateRealnameConfigRequest struct {
	Enabled           bool    `json:"enabled"`
	Provider          string  `json:"provider"`
	AppID             string  `json:"appId"`
	PrivateKey        string  `json:"privateKey"`
	AlipayPublicKey   string  `json:"alipayPublicKey"`
	Gateway           string  `json:"gateway"`
	AlipayAuthMode    string  `json:"alipayAuthMode"`
	KuaitongAccessKey string  `json:"kuaitongAccessKey"`
	KuaitongSecret    string  `json:"kuaitongSecret"`
	KuaitongAuthType  string  `json:"kuaitongAuthType"`
	TencentAPIKey     string  `json:"tencentApiKey"`
	TencentAPISecret  string  `json:"tencentApiSecret"`
	TencentBaseURL    string  `json:"tencentBaseUrl"`
	TencentUsePackage *bool   `json:"tencentUsePackage"`
	XiaomuAppKey      string  `json:"xiaomuAppKey"`
	XiaomuAppSecret   string  `json:"xiaomuAppSecret"`
	XiaomuBaseURL     string  `json:"xiaomuBaseUrl"`
	XiaomuProductMode string  `json:"xiaomuProductMode"`
	RequireAppIDs     []int64 `json:"requireAppIds"`
	Scope             string  `json:"scope"`
	PurchaseRequired  *bool   `json:"purchaseRequired"`
	PurchaseCheckOwner *bool  `json:"purchaseCheckOwner"`
	ValidityDays      *int    `json:"validityDays"`
	GraceDays         *int    `json:"graceDays"`
	RemindDays        *int    `json:"remindDays"`
	AllowIdentityChange *bool `json:"allowIdentityChange"`
	LegacyPolicy      string  `json:"legacyPolicy"`
}

// ensureRealnameStorage 幂等建表/建列：
//   - users / agents 表加实名相关字段（姓名与身份证号明文存储，展示层脱敏）
//   - realname_records 认证记录表，每次认证成功/失败留档
func ensureRealnameStorage(db *sql.DB) error {
	columns := []struct {
		table string
		name  string
		ddl   string
	}{
		{"users", "real_name", "ALTER TABLE users ADD COLUMN real_name VARCHAR(60) DEFAULT '' COMMENT '实名姓名' AFTER nickname"},
		{"users", "real_id_card", "ALTER TABLE users ADD COLUMN real_id_card VARCHAR(30) DEFAULT '' COMMENT '身份证号' AFTER real_name"},
		{"users", "realname_at", "ALTER TABLE users ADD COLUMN realname_at DATETIME DEFAULT NULL COMMENT '实名认证时间' AFTER real_id_card"},
		{"users", "realname_expires_at", "ALTER TABLE users ADD COLUMN realname_expires_at DATETIME DEFAULT NULL COMMENT '实名有效期截止时间，NULL 表示永不过期' AFTER realname_at"},
		{"agents", "real_name", "ALTER TABLE agents ADD COLUMN real_name VARCHAR(60) DEFAULT '' COMMENT '实名姓名'"},
		{"agents", "real_id_card", "ALTER TABLE agents ADD COLUMN real_id_card VARCHAR(30) DEFAULT '' COMMENT '身份证号'"},
		{"agents", "realname_at", "ALTER TABLE agents ADD COLUMN realname_at DATETIME DEFAULT NULL COMMENT '实名认证时间'"},
		{"agents", "realname_expires_at", "ALTER TABLE agents ADD COLUMN realname_expires_at DATETIME DEFAULT NULL COMMENT '实名有效期截止时间，NULL 表示永不过期' AFTER realname_at"},
	}
	for _, col := range columns {
		var count int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?
		`, col.table, col.name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(col.ddl); err != nil {
			return err
		}
	}

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS realname_records (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '主键ID',
		owner_type VARCHAR(20) NOT NULL DEFAULT 'user' COMMENT '主体类型(user/agent)',
		owner_id BIGINT UNSIGNED NOT NULL COMMENT '主体ID',
		provider VARCHAR(20) NOT NULL DEFAULT '' COMMENT '服务商(alipay/kuaitong/tencent)',
		real_name VARCHAR(60) NOT NULL DEFAULT '' COMMENT '实名姓名',
		id_card VARCHAR(30) NOT NULL DEFAULT '' COMMENT '身份证号',
		status VARCHAR(20) NOT NULL DEFAULT '' COMMENT '结果(passed/failed)',
		fail_reason TEXT NOT NULL COMMENT '详细失败原因',
		serial_no VARCHAR(64) NOT NULL DEFAULT '' COMMENT '服务商流水号',
		score VARCHAR(20) NOT NULL DEFAULT '' COMMENT '人脸相似度分数',
		record_type VARCHAR(20) NOT NULL DEFAULT 'init' COMMENT '认证类型(init首次/review复核)',
		expires_at DATETIME DEFAULT NULL COMMENT '本次认证产生的有效期截止时间，NULL 表示永久',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
		PRIMARY KEY (id),
		KEY idx_owner (owner_type, owner_id),
		KEY idx_created_at (created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='实名认证记录表'`)
	if err != nil {
		return err
	}

	// 老库的 realname_records 已在运行时建好，新增列需要单独补齐。
	for _, col := range []struct{ name, ddl string }{
		{"record_type", "ALTER TABLE realname_records ADD COLUMN record_type VARCHAR(20) NOT NULL DEFAULT 'init' COMMENT '认证类型(init首次/review复核)' AFTER score"},
		{"expires_at", "ALTER TABLE realname_records ADD COLUMN expires_at DATETIME DEFAULT NULL COMMENT '本次认证产生的有效期截止时间，NULL 表示永久' AFTER record_type"},
	} {
		var count int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'realname_records' AND COLUMN_NAME = ?
		`, col.name).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if _, err := db.Exec(col.ddl); err != nil {
			return err
		}
	}

	var failReasonLength sql.NullInt64
	if err := db.QueryRow(`
		SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'realname_records' AND COLUMN_NAME = 'fail_reason'
	`).Scan(&failReasonLength); err != nil {
		return err
	}
	if !failReasonLength.Valid || failReasonLength.Int64 < 65535 {
		_, err = db.Exec(`ALTER TABLE realname_records
			MODIFY COLUMN fail_reason TEXT NOT NULL COMMENT '详细失败原因'`)
		return err
	}
	return nil
}

// writeRealnameRecord 写入一条实名认证记录（姓名/身份证明文留档）。
// 用于失败/中间状态留档；认证通过请走 markRealnameVerified，以便同时记录有效期。
// 认证类型按主体当前实名状态判定：已实名者的再次失败记为 review，与通过路径口径一致。
func writeRealnameRecord(db *sql.DB, ownerType string, ownerID int64, provider, realName, idCard, status, failReason, serialNo, score string) {
	var previousAt sql.NullTime
	_ = db.QueryRow("SELECT realname_at FROM "+realnameOwnerTable(ownerType)+" WHERE id = ?", ownerID).Scan(&previousAt)
	writeRealnameRecordFull(db, ownerType, ownerID, provider, realName, idCard, status, failReason, serialNo, score, realnameRecordTypeFor(previousAt), time.Time{})
}

// writeRealnameRecordFull 带认证类型与有效期的完整写入。
func writeRealnameRecordFull(db *sql.DB, ownerType string, ownerID int64, provider, realName, idCard, status, failReason, serialNo, score, recordType string, expiresAt time.Time) {
	if !validRealnameRecordType(recordType) {
		recordType = realnameRecordTypeInit
	}
	var expires interface{}
	if !expiresAt.IsZero() {
		expires = expiresAt
	}
	_, _ = db.Exec(`INSERT INTO realname_records (owner_type, owner_id, provider, real_name, id_card, status, fail_reason, serial_no, score, record_type, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerType, ownerID, provider, realName, idCard, status, failReason, serialNo, score, recordType, expires)
}

func validRealnameRecordType(value string) bool {
	return value == realnameRecordTypeInit || value == realnameRecordTypeReview
}

func validRealnameScope(value string) bool {
	return value == realnameScopeByApp || value == realnameScopeAll
}

func validRealnameLegacyPolicy(value string) bool {
	return value == realnameLegacyKeep || value == realnameLegacyBackfill
}

// realnameExpiresAt 按有效期天数计算到期时间；validityDays <= 0 返回零值表示永久有效。
func realnameExpiresAt(now time.Time, validityDays int) time.Time {
	if validityDays <= 0 {
		return time.Time{}
	}
	return now.AddDate(0, 0, validityDays)
}

// realnameStillValid 判断实名是否仍在有效期内（含到期后的宽限期）。
// 从未认证返回 false；未设置到期时间为永久有效；设置了到期时间则比较宽限后的截止点。
func realnameStillValid(realnameAt, expiresAt sql.NullTime, graceDays int) bool {
	if !realnameAt.Valid {
		return false
	}
	if !expiresAt.Valid {
		return true
	}
	deadline := expiresAt.Time
	if graceDays > 0 {
		deadline = deadline.AddDate(0, 0, graceDays)
	}
	return time.Now().Before(deadline)
}

// realnameGraceExpired 判断实名是否已过期且超出宽限期（即需要拦截）。
// 与 realnameStillValid 互补：后者为 false 且曾实名过，才可能是宽限期内或彻底过期。
func realnameGraceExpired(realnameAt, expiresAt sql.NullTime, graceDays int) bool {
	if !realnameAt.Valid || !expiresAt.Valid {
		return false
	}
	return !realnameStillValid(realnameAt, expiresAt, graceDays)
}

// realnameRecordTypeFor 判断本次认证是首次认证还是复核。
func realnameRecordTypeFor(realnameAt sql.NullTime) string {
	if realnameAt.Valid {
		return realnameRecordTypeReview
	}
	return realnameRecordTypeInit
}

// markRealnameVerified 实名通过后的唯一写入入口：更新主体表实名字段与有效期，
// 并写入一条认证记录。所有服务商（支付宝/快瞳/靓仔/小沐）的通过分支都必须调用它，
// 避免有效期计算与留档逻辑散落多份。
func markRealnameVerified(db *sql.DB, ownerType string, ownerID int64, provider, realName, idCard string, cfg realnameConfig, serialNo, score string) error {
	table := realnameOwnerTable(ownerType)

	// 先读旧状态，用于判定首次/复核，并保留原认证时间用于存量补算。
	var previousAt sql.NullTime
	_ = db.QueryRow("SELECT realname_at FROM "+table+" WHERE id = ?", ownerID).Scan(&previousAt)

	now := time.Now()
	expiresAt := realnameExpiresAt(now, cfg.ValidityDays)
	var expiresValue interface{}
	if !expiresAt.IsZero() {
		expiresValue = expiresAt
	}

	if _, err := db.Exec(
		"UPDATE "+table+" SET real_name = ?, real_id_card = ?, realname_at = ?, realname_expires_at = ? WHERE id = ?",
		realName, idCard, now, expiresValue, ownerID,
	); err != nil {
		return err
	}

	writeRealnameRecordFull(db, ownerType, ownerID, provider, realName, idCard,
		realnameFaceStatusPassed, "", serialNo, score, realnameRecordTypeFor(previousAt), expiresAt)
	return nil
}

// realnameCheckResult 描述某个主体的实名校验结果，供授权校验与购买门槛复用。
type realnameCheckResult struct {
	Required bool
	Verified bool
	// Status 取值：ok 有效；grace 已过有效期但仍在宽限期（放行但需提示复核）；
	// never 从未实名；expired 已过期且超出宽限期。
	Status string
}

// checkRealnameForOwner 判断指定主体（user/agent）在给定配置下的实名状态。
// 管理员等未知归属返回无需实名，与历史行为保持一致。
func checkRealnameForOwner(db *sql.DB, cfg realnameConfig, ownerType string, ownerID int64) realnameCheckResult {
	table := ""
	switch ownerType {
	case "user":
		table = "users"
	case "agent":
		table = "agents"
	default:
		return realnameCheckResult{Required: false, Verified: true, Status: realnameStatusOK}
	}

	var realnameAt, expiresAt sql.NullTime
	if err := db.QueryRow(
		"SELECT realname_at, realname_expires_at FROM "+table+" WHERE id = ?", ownerID,
	).Scan(&realnameAt, &expiresAt); err != nil {
		return realnameCheckResult{Required: true, Verified: false, Status: realnameStatusNever}
	}

	if realnameStillValid(realnameAt, expiresAt, cfg.GraceDays) {
		// 已过有效期但仍在宽限期：放行，但前端应提示尽快重新认证。
		if realnameGraceExpired(realnameAt, expiresAt, 0) {
			return realnameCheckResult{Required: true, Verified: true, Status: realnameStatusGrace}
		}
		return realnameCheckResult{Required: true, Verified: true, Status: realnameStatusOK}
	}
	if realnameAt.Valid {
		return realnameCheckResult{Required: true, Verified: false, Status: realnameStatusExpired}
	}
	return realnameCheckResult{Required: true, Verified: false, Status: realnameStatusNever}
}

func validKuaitongAuthType(value string) bool {
	return value == kuaitongAuthTypeFace || value == kuaitongAuthTypeTwoElement
}

func loadRealnameConfig(db *sql.DB) (realnameConfig, error) {
	cfg := realnameConfig{
		Provider: realnameProviderAlipay, Gateway: alipayDefaultGateway,
		AlipayAuthMode:   alipayAuthModeFace,
		KuaitongAuthType: kuaitongAuthTypeFace,
		TencentBaseURL:   tencentRealnameBaseURL, TencentProductCode: tencentRealnameProduct, TencentUsePackage: true,
		XiaomuBaseURL: xiaomuRealnameBaseURL, XiaomuProductMode: xiaomuModeThreeElement,
		RequireAppIDs:       map[int64]bool{},
		Scope:               realnameScopeByApp,
		ValidityDays:        0,
		GraceDays:           defaultRealnameGraceDays,
		RemindDays:          defaultRealnameRemindDays,
		AllowIdentityChange: true,
		LegacyPolicy:        realnameLegacyKeep,
	}
	rows, err := db.Query("SELECT `key`, value FROM system_configs WHERE `group` = ?", realnameGroup)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return cfg, err
		}
		switch key {
		case "enabled":
			cfg.Enabled = value == "1"
		case "provider":
			if v := strings.TrimSpace(value); v == realnameProviderAlipay || v == realnameProviderKuaitong || v == realnameProviderTencent || v == realnameProviderXiaomu {
				cfg.Provider = v
			}
		case "app_id":
			cfg.AppID = strings.TrimSpace(value)
		case "private_key":
			cfg.PrivateKey = strings.TrimSpace(value)
		case "alipay_public_key":
			cfg.AlipayPublicKey = strings.TrimSpace(value)
		case "gateway":
			if v := strings.TrimSpace(value); v != "" {
				cfg.Gateway = v
			}
		case "alipay_auth_mode":
			if v := strings.TrimSpace(value); v == alipayAuthModeFace || v == alipayAuthModeInfo {
				cfg.AlipayAuthMode = v
			}
		case "kuaitong_access_key":
			cfg.KuaitongAccessKey = strings.TrimSpace(value)
		case "kuaitong_secret":
			cfg.KuaitongSecret = strings.TrimSpace(value)
		case "kuaitong_auth_type":
			if v := strings.TrimSpace(value); validKuaitongAuthType(v) {
				cfg.KuaitongAuthType = v
			}
		case "tencent_api_key":
			cfg.TencentAPIKey = strings.TrimSpace(value)
		case "tencent_api_secret":
			cfg.TencentAPISecret = strings.TrimSpace(value)
		case "tencent_base_url":
			if v := strings.TrimSpace(value); v != "" {
				cfg.TencentBaseURL = strings.TrimRight(v, "/")
			}
		case "tencent_product_code":
			if v := strings.TrimSpace(value); v != "" {
				cfg.TencentProductCode = v
			}
		case "tencent_use_package":
			cfg.TencentUsePackage = value == "1"
		case "xiaomu_app_key":
			cfg.XiaomuAppKey = strings.TrimSpace(value)
		case "xiaomu_app_secret":
			cfg.XiaomuAppSecret = strings.TrimSpace(value)
		case "xiaomu_base_url":
			if v := strings.TrimSpace(value); v != "" {
				cfg.XiaomuBaseURL = strings.TrimRight(v, "/")
			}
		case "xiaomu_product_mode":
			if v := strings.TrimSpace(value); validXiaomuProductMode(v) {
				cfg.XiaomuProductMode = v
			}
		case "require_app_ids":
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if id, err := strconv.ParseInt(part, 10, 64); err == nil && id > 0 {
					cfg.RequireAppIDs[id] = true
				}
			}
		case "scope":
			if v := strings.TrimSpace(value); validRealnameScope(v) {
				cfg.Scope = v
			}
		case "purchase_required":
			cfg.PurchaseRequired = value == "1"
		case "purchase_check_owner":
			cfg.PurchaseCheckOwner = value == "1"
		case "validity_days":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v >= 0 {
				cfg.ValidityDays = v
			}
		case "grace_days":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v >= 0 {
				cfg.GraceDays = v
			}
		case "remind_days":
			if v, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && v >= 0 {
				cfg.RemindDays = v
			}
		case "allow_identity_change":
			cfg.AllowIdentityChange = value == "1"
		case "legacy_policy":
			if v := strings.TrimSpace(value); validRealnameLegacyPolicy(v) {
				cfg.LegacyPolicy = v
			}
		}
	}
	if err := rows.Err(); err != nil {
		return cfg, err
	}

	// 插件中心联动：当前 provider 的插件未启用时，回落到已启用的实名插件。
	cfg.PluginEnabled = isPluginEnabled(db, realnamePluginIDByProvider(cfg.Provider))
	if !cfg.PluginEnabled {
		fallback := ""
		if isPluginEnabled(db, "alipay-realname") {
			fallback = realnameProviderAlipay
		} else if isPluginEnabled(db, "kuaitong-realname") {
			fallback = realnameProviderKuaitong
		} else if isPluginEnabled(db, "tencent-realname") {
			fallback = realnameProviderTencent
		} else if isPluginEnabled(db, "xiaomu-realname") {
			fallback = realnameProviderXiaomu
		}
		if fallback != "" && fallback != cfg.Provider {
			cfg.Provider = fallback
			cfg.PluginEnabled = true
		}
	}
	return cfg, nil
}

func loadRealnameApps(db *sql.DB) ([]realnameAppItem, error) {
	rows, err := db.Query("SELECT id, app_name, app_key FROM apps ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	apps := make([]realnameAppItem, 0)
	for rows.Next() {
		var item realnameAppItem
		if err := rows.Scan(&item.ID, &item.AppName, &item.AppKey); err != nil {
			return nil, err
		}
		apps = append(apps, item)
	}
	return apps, rows.Err()
}

// AdminRealnameConfig 读取实名认证配置（私钥不回显）。
func AdminRealnameConfig(c *gin.Context) {
	db, err := openSystemConfigDB()
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}

	if err := ensureSystemConfigStorage(db); err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "初始化系统配置失败"})
		return
	}

	cfg, err := loadRealnameConfig(db)
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "读取实名认证配置失败"})
		return
	}
	apps, err := loadRealnameApps(db)
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "读取应用列表失败"})
		return
	}

	ids := make([]int64, 0, len(cfg.RequireAppIDs))
	for id := range cfg.RequireAppIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	writeSystemConfig(c, http.StatusOK, gin.H{"code": 200, "msg": "", "data": realnameConfigResponse{
		Enabled:            cfg.Enabled,
		PluginEnabled:      cfg.PluginEnabled,
		Provider:           cfg.Provider,
		AppID:              cfg.AppID,
		PrivateKeySet:      cfg.PrivateKey != "",
		AlipayPublicKey:    cfg.AlipayPublicKey,
		Gateway:            cfg.Gateway,
		AlipayAuthMode:     cfg.AlipayAuthMode,
		KuaitongAccessKey:  cfg.KuaitongAccessKey,
		KuaitongSecretSet:  cfg.KuaitongSecret != "",
		KuaitongAuthType:   cfg.KuaitongAuthType,
		TencentAPIKey:      cfg.TencentAPIKey,
		TencentSecretSet:   cfg.TencentAPISecret != "",
		TencentBaseURL:     cfg.TencentBaseURL,
		TencentUsePackage:  cfg.TencentUsePackage,
		TencentProductCode: cfg.TencentProductCode,
		XiaomuAppKey:       cfg.XiaomuAppKey,
		XiaomuSecretSet:    cfg.XiaomuAppSecret != "",
		XiaomuBaseURL:      cfg.XiaomuBaseURL,
		XiaomuProductMode:  cfg.XiaomuProductMode,
		RequireAppIDs:      ids,
		Scope:              cfg.Scope,
		PurchaseRequired:   cfg.PurchaseRequired,
		PurchaseCheckOwner: cfg.PurchaseCheckOwner,
		ValidityDays:       cfg.ValidityDays,
		GraceDays:          cfg.GraceDays,
		RemindDays:         cfg.RemindDays,
		AllowIdentityChange: cfg.AllowIdentityChange,
		LegacyPolicy:       cfg.LegacyPolicy,
		Apps:               apps,
	}})
}

// AdminRealnameConfigUpdate 保存实名认证配置。
func AdminRealnameConfigUpdate(c *gin.Context) {
	var req updateRealnameConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	req.AppID = strings.TrimSpace(req.AppID)
	req.PrivateKey = strings.TrimSpace(req.PrivateKey)
	req.AlipayPublicKey = strings.TrimSpace(req.AlipayPublicKey)
	req.Gateway = strings.TrimSpace(req.Gateway)
	req.AlipayAuthMode = strings.TrimSpace(req.AlipayAuthMode)
	req.KuaitongAccessKey = strings.TrimSpace(req.KuaitongAccessKey)
	req.KuaitongSecret = strings.TrimSpace(req.KuaitongSecret)
	req.KuaitongAuthType = strings.TrimSpace(req.KuaitongAuthType)
	req.TencentAPIKey = strings.TrimSpace(req.TencentAPIKey)
	req.TencentAPISecret = strings.TrimSpace(req.TencentAPISecret)
	req.TencentBaseURL = strings.TrimRight(strings.TrimSpace(req.TencentBaseURL), "/")
	req.XiaomuAppKey = strings.TrimSpace(req.XiaomuAppKey)
	req.XiaomuAppSecret = strings.TrimSpace(req.XiaomuAppSecret)
	req.XiaomuBaseURL = strings.TrimRight(strings.TrimSpace(req.XiaomuBaseURL), "/")
	req.XiaomuProductMode = strings.TrimSpace(req.XiaomuProductMode)
	if req.Provider != realnameProviderKuaitong && req.Provider != realnameProviderTencent && req.Provider != realnameProviderXiaomu {
		req.Provider = realnameProviderAlipay
	}
	if req.Provider == realnameProviderAlipay {
		if req.Gateway == "" {
			req.Gateway = alipayDefaultGateway
		}
		if _, err := url.ParseRequestURI(req.Gateway); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "网关地址格式不正确"})
			return
		}
	}

	db, err := openSystemConfigDB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}

	if err := ensureSystemConfigStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化系统配置失败"})
		return
	}

	oldCfg, err := loadRealnameConfig(db)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "读取实名认证配置失败"})
		return
	}
	// 服务商只能在应用商店启用对应插件后变更
	if !oldCfg.PluginEnabled {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请先在应用商店启用一个实名认证服务商插件"})
		return
	}
	req.Provider = oldCfg.Provider
	if req.AlipayAuthMode == "" {
		req.AlipayAuthMode = oldCfg.AlipayAuthMode
	}
	if req.AlipayAuthMode != alipayAuthModeFace && req.AlipayAuthMode != alipayAuthModeInfo {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "支付宝实名认证模式不正确"})
		return
	}
	if req.KuaitongAuthType == "" {
		req.KuaitongAuthType = oldCfg.KuaitongAuthType
	}
	if !validKuaitongAuthType(req.KuaitongAuthType) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "快瞳认证类型不正确"})
		return
	}
	if req.TencentBaseURL == "" {
		req.TencentBaseURL = oldCfg.TencentBaseURL
	}
	if req.XiaomuBaseURL == "" {
		req.XiaomuBaseURL = oldCfg.XiaomuBaseURL
	}
	if req.XiaomuProductMode == "" {
		req.XiaomuProductMode = oldCfg.XiaomuProductMode
	}
	if !validXiaomuProductMode(req.XiaomuProductMode) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "小沐聚合实名认证产品不正确"})
		return
	}

	// 生效范围、购买门槛、有效期与存量策略：未提交时保留旧值，保证老的保存请求不会清空配置。
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = oldCfg.Scope
	}
	if !validRealnameScope(scope) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名生效范围不正确"})
		return
	}
	purchaseRequired := oldCfg.PurchaseRequired
	if req.PurchaseRequired != nil {
		purchaseRequired = *req.PurchaseRequired
	}
	purchaseCheckOwner := oldCfg.PurchaseCheckOwner
	if req.PurchaseCheckOwner != nil {
		purchaseCheckOwner = *req.PurchaseCheckOwner
	}
	validityDays := oldCfg.ValidityDays
	if req.ValidityDays != nil {
		validityDays = *req.ValidityDays
	}
	graceDays := oldCfg.GraceDays
	if req.GraceDays != nil {
		graceDays = *req.GraceDays
	}
	remindDays := oldCfg.RemindDays
	if req.RemindDays != nil {
		remindDays = *req.RemindDays
	}
	allowIdentityChange := oldCfg.AllowIdentityChange
	if req.AllowIdentityChange != nil {
		allowIdentityChange = *req.AllowIdentityChange
	}
	legacyPolicy := strings.TrimSpace(req.LegacyPolicy)
	if legacyPolicy == "" {
		legacyPolicy = oldCfg.LegacyPolicy
	}
	if !validRealnameLegacyPolicy(legacyPolicy) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "存量实名数据处理策略不正确"})
		return
	}
	if validityDays < 0 || validityDays > 36500 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名有效期应为 0-36500 天，0 表示永不过期"})
		return
	}
	if graceDays < 0 || graceDays > 3650 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "宽限期应为 0-3650 天"})
		return
	}
	if remindDays < 0 || remindDays > 3650 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "提醒提前天数应为 0-3650 天"})
		return
	}
	// 提醒在"进入到期前 remind_days 的窗口"时触发，提醒天数大于有效期时窗口永远不可能命中。
	if remindDays > 0 && validityDays > 0 && remindDays > validityDays {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "提醒提前天数不能大于实名有效期，否则提醒永远不会触发"})
		return
	}
	// 购买门槛依赖实名功能本身可用，否则用户无法完成认证会形成死锁。
	if purchaseRequired && !req.Enabled {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "启用购买前实名前，请先开启实名认证"})
		return
	}
	// 存量补算依赖有效期设置，否则无有效期可补。
	if legacyPolicy == realnameLegacyBackfill && validityDays <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "启用存量补算前，请先设置大于 0 的实名有效期"})
		return
	}

	privateKey := req.PrivateKey
	if privateKey == "" {
		privateKey = oldCfg.PrivateKey // 未提交新私钥时保留旧的
	}
	kuaitongSecret := req.KuaitongSecret
	if kuaitongSecret == "" {
		kuaitongSecret = oldCfg.KuaitongSecret // 未提交新 secret 时保留旧的
	}
	tencentSecret := req.TencentAPISecret
	if tencentSecret == "" {
		tencentSecret = oldCfg.TencentAPISecret
	}
	tencentUsePackage := oldCfg.TencentUsePackage
	if req.TencentUsePackage != nil {
		tencentUsePackage = *req.TencentUsePackage
	}
	xiaomuSecret := req.XiaomuAppSecret
	if xiaomuSecret == "" {
		xiaomuSecret = oldCfg.XiaomuAppSecret
	}

	if req.Enabled && req.Provider == realnameProviderAlipay {
		if req.AppID == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启实名认证前请填写支付宝应用 AppID"})
			return
		}
		if privateKey == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启实名认证前请填写应用私钥"})
			return
		}
		if req.AlipayPublicKey == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启实名认证前请填写支付宝公钥"})
			return
		}
		if _, err := parseAlipayPrivateKey(privateKey); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "应用私钥格式不正确：" + err.Error()})
			return
		}
	}
	if req.Enabled && req.Provider == realnameProviderKuaitong {
		if req.KuaitongAccessKey == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启实名认证前请填写快瞳 accessKey"})
			return
		}
		if kuaitongSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启实名认证前请填写快瞳 accessSecret"})
			return
		}
	}
	if req.Enabled && req.Provider == realnameProviderTencent {
		if req.TencentAPIKey == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启靓仔聚合认证前请填写 API Key"})
			return
		}
		if tencentSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启靓仔聚合认证前请填写 API Secret"})
			return
		}
		parsed, err := url.ParseRequestURI(req.TencentBaseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "腾讯实名认证接口地址必须是有效的 HTTPS 地址"})
			return
		}
	}
	if req.Enabled && req.Provider == realnameProviderXiaomu {
		if req.XiaomuAppKey == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启小沐聚合实名前请填写 AppKey"})
			return
		}
		if xiaomuSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "开启小沐聚合实名前请填写 AppSecret"})
			return
		}
		parsed, err := url.ParseRequestURI(req.XiaomuBaseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "小沐聚合实名接口地址必须是有效的 HTTPS 地址"})
			return
		}
	}

	// 校验应用 id 真实存在
	validIDs := map[int64]bool{}
	if apps, err := loadRealnameApps(db); err == nil {
		for _, app := range apps {
			validIDs[app.ID] = true
		}
	}
	ids := make([]int64, 0, len(req.RequireAppIDs))
	seen := map[int64]bool{}
	for _, id := range req.RequireAppIDs {
		if id <= 0 || seen[id] || !validIDs[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = strconv.FormatInt(id, 10)
	}

	items := []struct{ key, value, desc string }{
		{"enabled", boolConfigValue(req.Enabled), "是否启用实名认证"},
		{"provider", req.Provider, "实名认证服务商(alipay/kuaitong/tencent/xiaomu)"},
		{"app_id", req.AppID, "支付宝开放平台应用 AppID"},
		{"private_key", privateKey, "支付宝应用私钥(RSA2)"},
		{"alipay_public_key", req.AlipayPublicKey, "支付宝公钥"},
		{"gateway", req.Gateway, "支付宝网关地址"},
		{"alipay_auth_mode", req.AlipayAuthMode, "支付宝实名认证模式(face刷脸/info信息比对)"},
		{"kuaitong_access_key", req.KuaitongAccessKey, "快瞳 accessKey"},
		{"kuaitong_secret", kuaitongSecret, "快瞳 accessSecret"},
		{"kuaitong_auth_type", req.KuaitongAuthType, "快瞳认证类型(face/two_element)"},
		{"tencent_api_key", req.TencentAPIKey, "腾讯增强人脸开放 API Key"},
		{"tencent_api_secret", tencentSecret, "腾讯增强人脸开放 API Secret"},
		{"tencent_base_url", req.TencentBaseURL, "靓仔聚合认证开放 API Base URL"},
		{"tencent_product_code", tencentRealnameProduct, "靓仔聚合认证产品编码"},
		{"tencent_use_package", boolConfigValue(tencentUsePackage), "靓仔聚合认证是否优先使用套餐"},
		{"xiaomu_app_key", req.XiaomuAppKey, "小沐聚合实名 AppKey"},
		{"xiaomu_app_secret", xiaomuSecret, "小沐聚合实名 AppSecret"},
		{"xiaomu_base_url", req.XiaomuBaseURL, "小沐聚合实名开放 API Base URL"},
		{"xiaomu_product_mode", req.XiaomuProductMode, "小沐聚合实名认证产品(three_element/face_h5/tencent_h5)"},
		{"require_app_ids", strings.Join(idStrs, ","), "强制实名认证的应用ID列表"},
		{"scope", scope, "实名生效范围(by_app按应用/all全部应用)"},
		{"purchase_required", boolConfigValue(purchaseRequired), "购买前是否要求实名"},
		{"purchase_check_owner", boolConfigValue(purchaseCheckOwner), "代理为名下用户开通时是否同时校验目标用户实名"},
		{"validity_days", strconv.Itoa(validityDays), "实名有效期天数，0 表示永不过期"},
		{"grace_days", strconv.Itoa(graceDays), "实名到期后的宽限天数"},
		{"remind_days", strconv.Itoa(remindDays), "实名到期前提醒天数，0 表示不提醒"},
		{"allow_identity_change", boolConfigValue(allowIdentityChange), "复核时是否允许更换姓名/证件"},
		{"legacy_policy", legacyPolicy, "存量实名数据处理策略(keep永不过期/backfill按认证时间补算)"},
	}
	for _, item := range items {
		if _, err := db.Exec(`
			INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE value = VALUES(value), description = VALUES(description)
		`, realnameGroup, item.key, item.value, item.desc); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存实名认证配置失败"})
			return
		}
	}

	// 存量补算只在管理员显式选择时执行，且仅在从 keep 切入 backfill 或有效期变化的首次生效时回填。
	backfilled, backfillErr := applyRealnameLegacyBackfill(db, oldCfg, legacyPolicy, validityDays)
	msg := "实名认证配置已保存"
	if backfillErr != nil {
		msg = "配置已保存，但存量实名有效期补算失败：" + backfillErr.Error()
	} else if backfilled > 0 {
		msg = fmt.Sprintf("实名认证配置已保存，已为 %d 个存量实名主体补算有效期", backfilled)
	}

	// 有效期改回 0（永不过期）时清除已写入的到期时间，否则存量主体会继续按旧有效期过期被拦。
	cleared, clearErr := clearRealnameExpiryOnPermanent(db, oldCfg, validityDays)
	if clearErr != nil {
		msg += "；但清除存量实名有效期失败：" + clearErr.Error()
	} else if cleared > 0 {
		msg += fmt.Sprintf("；已为 %d 个主体恢复永久有效", cleared)
	}

	writeSystemConfig(c, http.StatusOK, gin.H{"code": 200, "msg": msg, "data": realnameConfigResponse{
		Enabled:            req.Enabled,
		PluginEnabled:      oldCfg.PluginEnabled,
		Provider:           req.Provider,
		AppID:              req.AppID,
		PrivateKeySet:      privateKey != "",
		AlipayPublicKey:    req.AlipayPublicKey,
		Gateway:            req.Gateway,
		AlipayAuthMode:     req.AlipayAuthMode,
		KuaitongAccessKey:  req.KuaitongAccessKey,
		KuaitongSecretSet:  kuaitongSecret != "",
		KuaitongAuthType:   req.KuaitongAuthType,
		TencentAPIKey:      req.TencentAPIKey,
		TencentSecretSet:   tencentSecret != "",
		TencentBaseURL:     req.TencentBaseURL,
		TencentUsePackage:  tencentUsePackage,
		TencentProductCode: tencentRealnameProduct,
		XiaomuAppKey:       req.XiaomuAppKey,
		XiaomuSecretSet:    xiaomuSecret != "",
		XiaomuBaseURL:      req.XiaomuBaseURL,
		XiaomuProductMode:  req.XiaomuProductMode,
		RequireAppIDs:      ids,
		Scope:              scope,
		PurchaseRequired:   purchaseRequired,
		PurchaseCheckOwner: purchaseCheckOwner,
		ValidityDays:       validityDays,
		GraceDays:          graceDays,
		RemindDays:         remindDays,
		AllowIdentityChange: allowIdentityChange,
		LegacyPolicy:       legacyPolicy,
	}})
}

// AdminRealnameImpactPreview 计算切换到「全部应用强制实名」的影响面。
// 数据来源为 verify_logs 中已记录的实名拦截记录，前端在开关旁展示预估影响，
// 避免管理员在不知情的情况下一次性拦住大量线上应用。
func AdminRealnameImpactPreview(c *gin.Context) {
	db, err := openSystemConfigDB()
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 365 {
			days = parsed
		}
	}

	// 宽限期内的主体实际仍放行，统计"会被拦截"的口径必须把它排除，
	// 与 checkRealnameForOwner 的拦截判定（到期 + 宽限期后才拦）保持一致。
	graceDays := 0
	if cfg, err := loadRealnameConfig(db); err == nil {
		graceDays = cfg.GraceDays
	}

	result := gin.H{
		"windowDays": days,
		// 未实名主体数量：从未认证 + 已过期且超出宽限期（宽限期内仍放行，不计入）。
		"unverifiedUsers":  0,
		"unverifiedAgents": 0,
		// 受影响授权：这些主体的在有效期内的授权数量。
		"affectedLicenses": 0,
		// 近期已被拦截的请求数，用于校准预估。
		"blockedRequests": 0,
		"blockedLicenses": 0,
	}

	var unverifiedUsers int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM users
		WHERE COALESCE(real_name, '') = '' OR realname_at IS NULL
		   OR (realname_expires_at IS NOT NULL AND DATE_ADD(realname_expires_at, INTERVAL ? DAY) <= NOW())
	`, graceDays).Scan(&unverifiedUsers)
	result["unverifiedUsers"] = unverifiedUsers

	var agentCount int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM agents
		WHERE COALESCE(real_name, '') = '' OR realname_at IS NULL
		   OR (realname_expires_at IS NOT NULL AND DATE_ADD(realname_expires_at, INTERVAL ? DAY) <= NOW())
	`, graceDays).Scan(&agentCount)
	result["unverifiedAgents"] = agentCount

	// 未实名主体名下仍在有效期的授权数：这些是切换全局范围后会被拦截的安装。
	var affectedLicenses int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM licenses l
		LEFT JOIN users u ON l.owner_type = 'user' AND u.id = l.owner_id
		LEFT JOIN agents a ON l.owner_type = 'agent' AND a.id = l.owner_id
		WHERE l.status = 'active'
		  AND (l.expired_at IS NULL OR l.expired_at > NOW())
		  AND (
			(l.owner_type = 'user' AND (u.realname_at IS NULL OR (u.realname_expires_at IS NOT NULL AND DATE_ADD(u.realname_expires_at, INTERVAL ? DAY) <= NOW())))
			OR (l.owner_type = 'agent' AND (a.realname_at IS NULL OR (a.realname_expires_at IS NOT NULL AND DATE_ADD(a.realname_expires_at, INTERVAL ? DAY) <= NOW())))
		  )
	`, graceDays, graceDays).Scan(&affectedLicenses)
	result["affectedLicenses"] = affectedLicenses

	// 近期被实名门槛拦截的请求，用于评估当前策略的实际影响。
	var blockedRequests, blockedLicenses int
	_ = db.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT license_id)
		FROM verify_logs
		WHERE fail_reason IN (?, ?)
		  AND created_at >= DATE_SUB(NOW(), INTERVAL ? DAY)
	`, verifyReasonRealnameRequired, verifyReasonRealnameExpired, days).Scan(&blockedRequests, &blockedLicenses)
	result["blockedRequests"] = blockedRequests
	result["blockedLicenses"] = blockedLicenses

	writeSystemConfig(c, http.StatusOK, gin.H{"code": 200, "msg": "", "data": result})
}

// AdminRealnameLegacyReset 批量重置指定主体范围的实名有效期，用于管理员显式开启存量复核。
// scope=all 按当前有效期重算全体存量；scope=unbackfilled 仅处理还没有有效期的存量实名；
// scope=clear 清空全部主体的到期时间，恢复永久有效（用于撤销补算）。
func AdminRealnameLegacyReset(c *gin.Context) {
	db, err := openSystemConfigDB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	scope := strings.TrimSpace(c.Query("scope"))
	if scope == "" {
		scope = "unbackfilled"
	}
	if scope != "all" && scope != "unbackfilled" && scope != "clear" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "重置范围不支持"})
		return
	}

	// 撤销补算不依赖有效期配置，单独处理。
	if scope == "clear" {
		affected, err := clearAllRealnameExpiry(db)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "撤销存量实名有效期失败"})
			return
		}
		msg := fmt.Sprintf("已恢复 %d 个实名主体为永久有效", affected)
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": msg, "data": gin.H{"affected": affected, "scope": scope}})
		return
	}

	cfg, err := loadRealnameConfig(db)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "读取实名认证配置失败"})
		return
	}
	if cfg.ValidityDays <= 0 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请先设置大于 0 的实名有效期，再进行存量重置"})
		return
	}

	where := "realname_at IS NOT NULL AND COALESCE(real_name, '') != ''"
	if scope == "unbackfilled" {
		where += " AND realname_expires_at IS NULL"
	}

	var affected int64
	for _, table := range []string{"users", "agents"} {
		result, err := db.Exec(`
			UPDATE `+table+`
			SET realname_expires_at = DATE_ADD(realname_at, INTERVAL ? DAY)
			WHERE `+where,
			cfg.ValidityDays)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "重置存量实名有效期失败"})
			return
		}
		rows, _ := result.RowsAffected()
		affected += rows
	}

	msg := fmt.Sprintf("已重置 %d 个存量实名主体的有效期", affected)
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": msg, "data": gin.H{"affected": affected, "scope": scope}})
}

// clearAllRealnameExpiry 清空 users/agents 上所有实名到期时间。
func clearAllRealnameExpiry(db *sql.DB) (int64, error) {
	var affected int64
	for _, table := range []string{"users", "agents"} {
		result, err := db.Exec("UPDATE " + table + " SET realname_expires_at = NULL WHERE realname_expires_at IS NOT NULL")
		if err != nil {
			return affected, err
		}
		rows, _ := result.RowsAffected()
		affected += rows
	}
	return affected, nil
}

// clearRealnameExpiryOnPermanent 有效期从有限天数改回 0（永久）时，清除全量主体的实名到期时间，
// 否则存量主体会继续按旧有效期过期被拦，与"0 表示永不过期"的配置语义冲突。
func clearRealnameExpiryOnPermanent(db *sql.DB, oldCfg realnameConfig, validityDays int) (int64, error) {
	if validityDays > 0 || oldCfg.ValidityDays <= 0 {
		return 0, nil
	}
	return clearAllRealnameExpiry(db)
}

// applyRealnameLegacyBackfill 按存量实名的认证时间补算有效期。
// keep（默认）不做任何改动，存量保持永不过期；backfill 只在本次保存真正启用时执行一次，
// 避免每次保存配置都全表重写。
func applyRealnameLegacyBackfill(db *sql.DB, oldCfg realnameConfig, policy string, validityDays int) (int64, error) {
	if policy != realnameLegacyBackfill || validityDays <= 0 {
		return 0, nil
	}
	if oldCfg.LegacyPolicy == realnameLegacyBackfill && oldCfg.ValidityDays == validityDays {
		return 0, nil
	}

	var affected int64
	for _, table := range []string{"users", "agents"} {
		result, err := db.Exec(`
			UPDATE `+table+`
			SET realname_expires_at = DATE_ADD(realname_at, INTERVAL ? DAY)
			WHERE realname_at IS NOT NULL
			  AND realname_expires_at IS NULL
			  AND COALESCE(real_name, '') != ''
		`, validityDays)
		if err != nil {
			return affected, err
		}
		rows, _ := result.RowsAffected()
		affected += rows
	}
	return affected, nil
}

// ========== 用户端 / 代理端实名 ==========

type userRealnameInitRequest struct {
	RealName string `json:"realName" binding:"required"`
	IDCard   string `json:"idCard" binding:"required"`
	// Mobile 仅小沐三要素模式使用：认证时临时填写，不写入 users / agents。
	Mobile string `json:"mobile"`
}

type realnamePendingOrder struct {
	OwnerType string `json:"ownerType"`
	OwnerID   uint   `json:"ownerId"`
	CertifyID string `json:"certifyId"`
	RealName  string `json:"realName"`
	IDCard    string `json:"idCard"`
}

func realnameOwnerTable(ownerType string) string {
	if ownerType == "agent" {
		return "agents"
	}
	return "users"
}

// realnameProviderConfigured 判断当前服务商的必填配置是否完整，返回提示文案。
// 与各发起分支内的配置校验保持一致，用于在进入限频等前置逻辑前快速失败。
func realnameProviderConfigured(cfg realnameConfig) (bool, string) {
	switch cfg.Provider {
	case realnameProviderXiaomu:
		if cfg.XiaomuAppKey == "" || cfg.XiaomuAppSecret == "" {
			return false, "管理员尚未完成小沐聚合实名配置"
		}
		if !validXiaomuProductMode(cfg.XiaomuProductMode) {
			return false, "管理员尚未选择小沐聚合实名认证产品"
		}
	case realnameProviderTencent:
		if cfg.TencentAPIKey == "" || cfg.TencentAPISecret == "" {
			return false, "管理员尚未完成靓仔聚合认证配置"
		}
	case realnameProviderKuaitong:
		if cfg.KuaitongAccessKey == "" || cfg.KuaitongSecret == "" {
			return false, "管理员尚未完成快瞳实名认证配置"
		}
	default: // 支付宝
		if cfg.AppID == "" || cfg.PrivateKey == "" {
			return false, "管理员尚未完成支付宝实名认证配置"
		}
	}
	return true, ""
}

func requireRealnameOwner(c *gin.Context, expectedRole string) (uint, bool) {
	role, _ := c.Get("role")
	if role != expectedRole {
		c.JSON(http.StatusOK, gin.H{"code": 403, "msg": "无权限"})
		return 0, false
	}
	ownerID, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "认证信息缺失"})
		return 0, false
	}
	id, ok := ownerID.(uint)
	if !ok || id == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 401, "msg": "认证信息异常"})
		return 0, false
	}
	return id, true
}

func encodeRealnamePendingOrder(order realnamePendingOrder) string {
	raw, _ := json.Marshal(order)
	return string(raw)
}

func parseRealnamePendingOrder(raw string) (realnamePendingOrder, error) {
	var order realnamePendingOrder
	if json.Unmarshal([]byte(raw), &order) == nil && order.OwnerID > 0 && order.CertifyID != "" {
		if order.OwnerType != "agent" {
			order.OwnerType = "user"
		}
		return order, nil
	}

	// Backward compatibility for pending user orders created before ownerType was stored.
	parts := strings.Split(raw, "|")
	if len(parts) < 2 {
		return realnamePendingOrder{}, errors.New("认证单数据损坏")
	}
	uid, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || uid == 0 {
		return realnamePendingOrder{}, errors.New("认证单数据损坏")
	}
	order = realnamePendingOrder{OwnerType: "user", OwnerID: uint(uid), CertifyID: parts[1]}
	if len(parts) >= 4 {
		order.RealName, order.IDCard = parts[2], parts[3]
	}
	return order, nil
}

// UserRealnameInit 用户提交姓名+身份证号，生成实名认证单。
func UserRealnameInit(c *gin.Context) {
	userID, ok := requireRealnameOwner(c, "user")
	if !ok {
		return
	}
	realnameInit(c, "user", userID)
}

// AgentRealnameInit 代理商提交姓名+身份证号，生成实名认证单。
func AgentRealnameInit(c *gin.Context) {
	agentID, ok := requireRealnameOwner(c, "agent")
	if !ok {
		return
	}
	realnameInit(c, "agent", agentID)
}

func realnameInit(c *gin.Context, ownerType string, ownerID uint) {

	var req userRealnameInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	req.RealName = strings.TrimSpace(req.RealName)
	req.IDCard = strings.ToUpper(strings.TrimSpace(req.IDCard))

	if utf8.RuneCountInString(req.RealName) < 2 || utf8.RuneCountInString(req.RealName) > maxRealNameLen {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请输入 2-30 个字符的真实姓名"})
		return
	}
	if !isValidIDCard(req.IDCard) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "身份证号格式不正确"})
		return
	}

	db, err := config.DB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}

	if err := ensureSystemConfigStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化配置失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	rnCfg, err := loadRealnameConfig(db)
	if err != nil || !rnCfg.Enabled {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "管理员尚未开启实名认证"})
		return
	}
	if !rnCfg.PluginEnabled {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名认证服务商未启用，请联系管理员"})
		return
	}
	// 服务商配置不完整时快速失败，避免后续限频等前置逻辑被白白消耗。
	if configured, configMsg := realnameProviderConfigured(rnCfg); !configured {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": configMsg})
		return
	}

	// 已实名处理：
	//   - 仍在有效期内且禁用换证时，仅允许本人以相同姓名与证件复核，避免绕过；
	//   - 已过期（含宽限期内）一律允许重新发起；
	//   - 允许换证时提交新证件即可覆盖，历史记录在 realname_records 留档。
	var existingName, existingIDCard string
	var existingAt, existingExpiresAt sql.NullTime
	_ = db.QueryRow(
		"SELECT real_name, real_id_card, realname_at, realname_expires_at FROM "+realnameOwnerTable(ownerType)+" WHERE id = ?",
		ownerID,
	).Scan(&existingName, &existingIDCard, &existingAt, &existingExpiresAt)

	if existingAt.Valid {
		if !rnCfg.AllowIdentityChange && strings.TrimSpace(existingIDCard) != "" &&
			existingIDCard != "已认证" &&
			(!strings.EqualFold(existingIDCard, req.IDCard) ||
				!strings.EqualFold(strings.TrimSpace(existingName), req.RealName)) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "当前配置不允许更换实名证件，请使用已认证的姓名与身份证号"})
			return
		}
		// 仅在实名仍完全有效（未过有效期）时限频；宽限期内和已过期都应允许立即重新认证。
		if realnameStillValid(existingAt, existingExpiresAt, 0) && !realnameReverifyAllowed(ownerType, ownerID) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "您已完成实名认证，如需重新认证请稍后再试"})
			return
		}
	}

	// 小沐聚合实名：三要素同步核验，人脸 / 微信模式跳转上游 H5 后轮询。
	if rnCfg.Provider == realnameProviderXiaomu {
		startXiaomuRealname(c, db, rnCfg, ownerType, ownerID, req)
		return
	}

	// 靓仔聚合认证生成本站拍照单，用户扫码后在本站拍照，提交时调用靓仔 API。
	if rnCfg.Provider == realnameProviderTencent {
		if rnCfg.TencentAPIKey == "" || rnCfg.TencentAPISecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "管理员尚未完成靓仔聚合认证配置"})
			return
		}
		token, terr := newRealnameFaceToken()
		if terr != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
			return
		}
		expiresAt := time.Now().Add(realnameFaceSessionTTL).Unix()
		value := realnameFaceSession{
			Provider:  rnCfg.Provider,
			OwnerType: ownerType,
			UserID:    int64(ownerID),
			RealName:  req.RealName,
			IDCard:    req.IDCard,
			ReturnURL: buildFrontendReturnURL(c, "", "/realname-face?t="+url.QueryEscape(token)),
			Status:    realnameFaceStatusPending,
			ExpireAt:  time.Unix(expiresAt, 0),
		}.encode()
		if _, err := db.Exec(`
			INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
			VALUES (?, ?, ?, '实名认证拍照单')
			ON DUPLICATE KEY UPDATE value = VALUES(value)
		`, realnameGroup, "kt_face_"+token, value); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"msg":  "请使用微信或支付宝扫码完成人脸核验",
			"data": gin.H{"status": "pending", "provider": rnCfg.Provider, "faceToken": token},
		})
		return
	}

	// 快瞳二要素直接核验；人脸模式继续生成本站扫码拍照单。
	if rnCfg.Provider == realnameProviderKuaitong {
		if rnCfg.KuaitongAccessKey == "" || rnCfg.KuaitongSecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "管理员尚未完成快瞳实名认证配置"})
			return
		}
		if rnCfg.KuaitongAuthType == kuaitongAuthTypeTwoElement {
			completeKuaitongTwoElement(c, db, rnCfg, ownerType, ownerID, req)
			return
		}
		token, terr := newRealnameFaceToken()
		if terr != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
			return
		}
		expiresAt := time.Now().Add(realnameFaceSessionTTL).Unix()
		value := realnameFaceSession{
			Provider:  rnCfg.Provider,
			OwnerType: ownerType,
			UserID:    int64(ownerID),
			RealName:  req.RealName,
			IDCard:    req.IDCard,
			ReturnURL: buildFrontendReturnURL(c, "", "/realname-face?t="+url.QueryEscape(token)),
			Status:    realnameFaceStatusPending,
			ExpireAt:  time.Unix(expiresAt, 0),
		}.encode()
		if _, err := db.Exec(`
			INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
			VALUES (?, ?, ?, '实名认证拍照单')
			ON DUPLICATE KEY UPDATE value = VALUES(value)
		`, realnameGroup, "kt_face_"+token, value); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"msg":  "请使用微信或支付宝扫码完成人脸核验",
			"data": gin.H{"status": "pending", "provider": rnCfg.Provider, "faceToken": token},
		})
		return
	}

	if rnCfg.AppID == "" || rnCfg.PrivateKey == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "管理员尚未完成支付宝实名认证配置"})
		return
	}

	ownerPrefix := "U"
	if ownerType == "agent" {
		ownerPrefix = "A"
	}
	outerOrderNo := fmt.Sprintf("RN%s%d%d", ownerPrefix, ownerID, time.Now().Unix())

	// 信息比对模式：preconsult 录入信息，用户扫码/唤起支付宝授权后回调完成比对。
	if rnCfg.AlipayAuthMode == alipayAuthModeInfo {
		startAlipayInfoRealname(c, db, rnCfg, ownerType, ownerID, outerOrderNo, req)
		return
	}

	initResp, err := alipayRealnameInitialize(rnCfg, outerOrderNo, req.RealName, req.IDCard)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化实名认证失败：" + err.Error()})
		return
	}

	// 认证通过后姓名、身份证明文写入主体表与认证记录表；接口展示时脱敏。
	_, _ = db.Exec(`
		INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
		VALUES (?, ?, ?, '实名认证临时单')
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`, realnameGroup, "pending_"+outerOrderNo, encodeRealnamePendingOrder(realnamePendingOrder{
		OwnerType: ownerType,
		OwnerID:   ownerID,
		CertifyID: initResp.CertifyID,
		RealName:  req.RealName,
		IDCard:    req.IDCard,
	}))

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "请使用支付宝扫码或跳转完成认证",
		"data": gin.H{
			"certifyId":    initResp.CertifyID,
			"outerOrderNo": outerOrderNo,
			"certifyUrl":   realnameH5Prefix + url.QueryEscape(initResp.CertifyID),
		},
	})
}

// UserRealnameQuery 用户轮询认证结果；认证成功后写入实名信息。
func UserRealnameQuery(c *gin.Context) {
	userID, ok := requireRealnameOwner(c, "user")
	if !ok {
		return
	}
	realnameQuery(c, "user", userID)
}

// AgentRealnameQuery 代理商轮询认证结果；认证成功后写入代理商实名信息。
func AgentRealnameQuery(c *gin.Context) {
	agentID, ok := requireRealnameOwner(c, "agent")
	if !ok {
		return
	}
	realnameQuery(c, "agent", agentID)
}

func realnameQuery(c *gin.Context, ownerType string, ownerID uint) {
	outerOrderNo := strings.TrimSpace(c.Query("outerOrderNo"))
	certifyID := strings.TrimSpace(c.Query("certifyId"))
	faceToken := strings.TrimSpace(c.Query("faceToken"))
	if certifyID == "" && faceToken == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "缺少认证参数"})
		return
	}

	db, err := config.DB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}

	if err := ensureSystemConfigStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化配置失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	rnCfg, err := loadRealnameConfig(db)
	if err != nil || !rnCfg.Enabled {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "管理员尚未开启实名认证"})
		return
	}
	if rnCfg.Provider == realnameProviderXiaomu {
		xiaomuRealnameQuery(c, db, rnCfg, ownerType, ownerID)
		return
	}
	// 信息比对模式：认证单走 faceToken 轮询，结果由支付宝授权回调写入
	if rnCfg.Provider == realnameProviderAlipay && rnCfg.AlipayAuthMode == alipayAuthModeInfo && strings.TrimSpace(c.Query("faceToken")) != "" {
		alipayInfoQuery(c, db, ownerType, ownerID)
		return
	}
	if rnCfg.Provider == realnameProviderKuaitong || rnCfg.Provider == realnameProviderTencent {
		realnameFaceQuery(c, db, rnCfg, ownerType, ownerID)
		return
	}

	// 防串号：支付宝认证单必须同时匹配当前角色、当前主体和 certifyId。
	if outerOrderNo == "" || certifyID == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "缺少认证参数"})
		return
	}
	var stored string
	err = db.QueryRow("SELECT value FROM system_configs WHERE `group` = ? AND `key` = ?", realnameGroup, "pending_"+outerOrderNo).Scan(&stored)
	pending, parseErr := parseRealnamePendingOrder(stored)
	if err != nil || parseErr != nil || pending.OwnerType != ownerType || pending.OwnerID != ownerID || pending.CertifyID != certifyID {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "认证单号不匹配"})
		return
	}

	queryResp, err := alipayRealnameQuery(rnCfg, certifyID, outerOrderNo)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "查询失败", "data": gin.H{"status": "pending"}})
		return
	}

	if queryResp.Passed != "T" {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未完成", "data": gin.H{"status": "pending"}})
		return
	}

	// 认证通过：优先用支付宝回传的明文姓名/证件号，未回传时取 init 时用户提交的值。
	// 数据库明文存储（audit 留档），接口响应统一脱敏。
	realName := queryResp.IdentityInfo.CertName
	idCard := strings.ToUpper(queryResp.IdentityInfo.CertNo)
	if realName == "" || idCard == "" {
		realName, idCard = pending.RealName, pending.IDCard
	}
	if realName == "" || idCard == "" {
		realName = "已认证"
		idCard = "已认证"
	}
	if err := markRealnameVerified(db, ownerType, int64(ownerID), realnameProviderAlipay, realName, idCard, rnCfg, certifyID, ""); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存实名认证结果失败，请稍后重试"})
		return
	}

	if outerOrderNo != "" {
		_, _ = db.Exec("DELETE FROM system_configs WHERE `group` = ? AND `key` = ?", realnameGroup, "pending_"+outerOrderNo)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实名认证成功",
		"data": gin.H{"status": "passed", "realName": maskRealName(realName), "idCard": maskIDCard(idCard)},
	})
}

func completeKuaitongTwoElement(c *gin.Context, db *sql.DB, cfg realnameConfig, ownerType string, ownerID uint, req userRealnameInitRequest) {
	message, serialNo, err := kuaitongVerifyIDCard(cfg, req.RealName, req.IDCard)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "核验请求失败：" + err.Error()})
		return
	}
	if message != "" {
		writeRealnameRecord(db, ownerType, int64(ownerID), realnameProviderKuaitong, req.RealName, req.IDCard, realnameFaceStatusFailed, message, serialNo, "")
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名核验未通过：" + message})
		return
	}

	if err := markRealnameVerified(db, ownerType, int64(ownerID), realnameProviderKuaitong, req.RealName, req.IDCard, cfg, serialNo, ""); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存实名认证结果失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实名认证成功",
		"data": gin.H{
			"status":   realnameFaceStatusPassed,
			"provider": realnameProviderKuaitong,
			"authType": kuaitongAuthTypeTwoElement,
			"realName": maskRealName(req.RealName),
			"idCard":   maskIDCard(req.IDCard),
		},
	})
}

// ========== 快瞳实名认证 ==========

// 快瞳 token 进程级缓存：官方要求自行缓存、不要频繁调鉴权接口（有限流）。
type kuaitongTokenCacheEntry struct {
	token     string
	expiresAt time.Time
}

var (
	kuaitongTokenMu    sync.Mutex
	kuaitongTokenCache = map[string]kuaitongTokenCacheEntry{}
)

// kuaitongGetToken 获取快瞳 access_token，优先命中缓存；forceRefresh 用于 401/403 后主动刷新。
func kuaitongGetToken(cfg realnameConfig, forceRefresh bool) (string, error) {
	cacheKey := cfg.KuaitongAccessKey

	kuaitongTokenMu.Lock()
	if !forceRefresh {
		if entry, ok := kuaitongTokenCache[cacheKey]; ok && entry.token != "" && time.Now().Add(kuaitongTokenRefreshSkew).Before(entry.expiresAt) {
			token := entry.token
			kuaitongTokenMu.Unlock()
			return token, nil
		}
	}
	kuaitongTokenMu.Unlock()

	token, expiresIn, err := kuaitongFetchToken(cfg)
	if err != nil {
		return "", err
	}

	kuaitongTokenMu.Lock()
	kuaitongTokenCache[cacheKey] = kuaitongTokenCacheEntry{
		token:     token,
		expiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	kuaitongTokenMu.Unlock()
	return token, nil
}

func kuaitongFetchToken(cfg realnameConfig) (string, int64, error) {
	reqURL := kuaitongTokenURL + "?accessKey=" + url.QueryEscape(cfg.KuaitongAccessKey) +
		"&accessSecret=" + url.QueryEscape(cfg.KuaitongSecret)

	client := &http.Client{Timeout: realnameHTTPTimeout}
	resp, err := client.Get(reqURL)
	if err != nil {
		return "", 0, fmt.Errorf("请求快瞳鉴权接口失败：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("读取快瞳响应失败：%w", err)
	}
	var result struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int64  `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, errors.New("快瞳鉴权响应解析失败")
	}
	if result.Status != 200 || result.Data.AccessToken == "" {
		msg := result.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", result.Status)
		}
		return "", 0, fmt.Errorf("快瞳鉴权失败：%s", msg)
	}
	if result.Data.ExpiresIn <= 0 {
		result.Data.ExpiresIn = 7 * 24 * 3600 // 文档说明 token 有效期七天
	}
	return result.Data.AccessToken, result.Data.ExpiresIn, nil
}

// kuaitongVerifyIDCard 调用快瞳身份证二要素核验（姓名+身份证号）。
func kuaitongVerifyIDCard(cfg realnameConfig, realName, idCard string) (string, string, error) {
	token, err := kuaitongGetToken(cfg, false)
	if err != nil {
		return "", "", err
	}

	message, serialNo, shouldRetry, err := kuaitongPostIDCard(token, realName, idCard)
	if err != nil {
		return "", "", err
	}
	if shouldRetry {
		token, err = kuaitongGetToken(cfg, true)
		if err != nil {
			return "", "", err
		}
		message, serialNo, _, err = kuaitongPostIDCard(token, realName, idCard)
		if err != nil {
			return "", "", err
		}
	}
	return message, serialNo, nil
}

func kuaitongPostIDCard(token, realName, idCard string) (string, string, bool, error) {
	client := &http.Client{Timeout: realnameHTTPTimeout}
	return kuaitongPostIDCardWithClient(client, kuaitongIDCardURL, token, realName, idCard)
}

func kuaitongPostIDCardWithClient(client *http.Client, endpoint, token, realName, idCard string) (string, string, bool, error) {
	var buf strings.Builder
	writer := multipart.NewWriter(&buf)
	fields := []struct {
		name  string
		value string
	}{
		{name: "token", value: token},
		{name: "idCard", value: idCard},
		{name: "realName", value: realName},
	}
	for _, field := range fields {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return "", "", false, fmt.Errorf("构造请求失败：%w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", "", false, fmt.Errorf("构造请求失败：%w", err)
	}

	resp, err := client.Post(endpoint, writer.FormDataContentType(), strings.NewReader(buf.String()))
	if err != nil {
		return "", "", false, fmt.Errorf("请求快瞳二要素核验接口失败：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", false, fmt.Errorf("读取快瞳响应失败：%w", err)
	}
	var result struct {
		Status   int    `json:"status"`
		Code     int    `json:"code"`
		Message  string `json:"message"`
		SerialNo string `json:"serialNo"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", false, errors.New("快瞳二要素核验响应解析失败")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		result.Status == http.StatusUnauthorized || result.Status == http.StatusForbidden {
		return "", "", true, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && result.Status == http.StatusOK {
		return "", result.SerialNo, false, nil
	}

	message := strings.TrimSpace(result.Message)
	if message == "" {
		if result.Code != 0 {
			message = fmt.Sprintf("业务码 %d", result.Code)
		} else {
			message = fmt.Sprintf("状态码 %d", result.Status)
		}
	}
	return message, result.SerialNo, false, nil
}

// kuaitongVerifyFace 调用快瞳人证合一核验（姓名+身份证号+人像照片比对）。
// 返回 (message, serialNo, score, error)：error 表示请求级失败；message 非空表示核验未通过。
func kuaitongVerifyFace(cfg realnameConfig, realName, idCard, imgBase64 string) (string, string, string, error) {
	token, err := kuaitongGetToken(cfg, false)
	if err != nil {
		return "", "", "", err
	}

	message, serialNo, score, shouldRetry, err := kuaitongPostVerify(token, realName, idCard, imgBase64)
	if err != nil {
		return "", "", "", err
	}
	if shouldRetry {
		// token 过期/无效，主动刷新后重试一次
		token, err = kuaitongGetToken(cfg, true)
		if err != nil {
			return "", "", "", err
		}
		message, serialNo, score, _, err = kuaitongPostVerify(token, realName, idCard, imgBase64)
		if err != nil {
			return "", "", "", err
		}
	}
	return message, serialNo, score, nil
}

// kuaitongPostVerify 执行一次人证合一核验请求，返回 (未通过原因, 流水号, 相似度分数, 是否需要刷新 token 重试, 请求级错误)。
func kuaitongPostVerify(token, realName, idCard, imgBase64 string) (string, string, string, bool, error) {
	var buf strings.Builder
	writer := multipart.NewWriter(&buf)
	fields := map[string]string{
		"token":     token,
		"idCard":    idCard,
		"realName":  realName,
		"imgBase64": imgBase64,
	}
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return "", "", "", false, fmt.Errorf("构造请求失败：%w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", "", "", false, fmt.Errorf("构造请求失败：%w", err)
	}

	client := &http.Client{Timeout: realnameHTTPTimeout}
	resp, err := client.Post(kuaitongVerifyURL, writer.FormDataContentType(), strings.NewReader(buf.String()))
	if err != nil {
		return "", "", "", false, fmt.Errorf("请求快瞳核验接口失败：%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", "", false, fmt.Errorf("读取快瞳响应失败：%w", err)
	}
	var result struct {
		Status   int    `json:"status"`
		Code     int    `json:"code"`
		Message  string `json:"message"`
		SerialNo string `json:"serialNo"`
		Data     struct {
			Pic string `json:"pic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", "", false, errors.New("快瞳核验响应解析失败")
	}

	// token 过期或非法账号：需要刷新 token 重试
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
		result.Status == http.StatusUnauthorized || result.Status == http.StatusForbidden {
		return "", "", "", true, nil
	}
	if result.Status == 200 && result.Code == 10000 {
		// data.pic 为相似度分数，分数大于阈值判定为同一人
		score, serr := strconv.ParseFloat(strings.TrimSpace(result.Data.Pic), 64)
		if serr == nil && score <= kuaitongPassScore {
			return fmt.Sprintf("人脸与证件照相似度不足（%.0f 分）", score), result.SerialNo, result.Data.Pic, false, nil
		}
		return "", result.SerialNo, result.Data.Pic, false, nil
	}
	msg := result.Message
	if msg == "" {
		msg = fmt.Sprintf("状态码 %d", result.Status)
	}
	return msg, result.SerialNo, "", false, nil
}

// tencentRealnameFlexText 兼容上游把业务码返回为字符串、数字或布尔值的情况。
type tencentRealnameFlexText string

func (v *tencentRealnameFlexText) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		*v = ""
	case strings.HasPrefix(trimmed, `"`):
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*v = tencentRealnameFlexText(strings.TrimSpace(text))
	default:
		*v = tencentRealnameFlexText(trimmed)
	}
	return nil
}

// tencentRealnameVerifyData 只声明形态稳定的字段。上游不同产品对“是否通过”的键名
// 并不统一（success / result / pass / status ...），结论判定交给 resolveTencentRealnameOutcome
// 从原始 data 中扫描，避免固定字段缺失时把成功响应误判为未通过。
type tencentRealnameVerifyData struct {
	Message   string                  `json:"message"`
	Code      tencentRealnameFlexText `json:"code"`
	RawData   json.RawMessage         `json:"raw_data"`
	CertifyID string                  `json:"certify_id"`
}

// tencentRealnameVerifyResponse 的 data 用原始字节承载：上游在参数校验失败时会把
// data 返回为 false 等非对象形态，固定结构会让真实错误信息被解析错误覆盖。
type tencentRealnameVerifyResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// decodeTencentRealnameVerifyData 返回解析结果，以及 data 非对象时的原始形态摘要。
func decodeTencentRealnameVerifyData(raw json.RawMessage) (tencentRealnameVerifyData, string) {
	var data tencentRealnameVerifyData
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return data, ""
	}
	if !strings.HasPrefix(trimmed, "{") {
		return data, summarizeTencentRealnameResponseBody([]byte(trimmed))
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return tencentRealnameVerifyData{}, summarizeTencentRealnameResponseBody([]byte(trimmed))
	}
	return data, ""
}

// ---- 结论判定：上游结论字段命名不统一，用候选键表统一扫描 ----

type tencentRealnameOutcome int

const (
	tencentRealnameOutcomeUnknown tencentRealnameOutcome = iota
	tencentRealnameOutcomePassed
	tencentRealnameOutcomeRejected
)

var (
	// 真值语义键：值本身表达通过与否。
	tencentRealnameOutcomeKeys = []string{
		"success", "is_success", "issuccess", "pass", "passed", "is_pass",
		"result", "verify_result", "check_result", "auth_result", "face_result",
		"match", "is_match", "status", "state", "result_code", "certify_status", "auth_status",
	}
	// 错误码语义键：0 表示通过。
	tencentRealnameErrCodeKeys = []string{"errcode", "err_code", "error_code", "errorcode"}

	tencentRealnameOutcomePassedValues = map[string]bool{
		"true": true, "1": true, "y": true, "yes": true, "ok": true,
		"success": true, "succeed": true, "succeeded": true, "pass": true, "passed": true,
		"通过": true, "成功": true, "一致": true, "认证成功": true, "核验通过": true,
	}
	tencentRealnameOutcomeRejectedValues = map[string]bool{
		"false": true, "0": true, "n": true, "no": true,
		"fail": true, "failed": true, "failure": true, "reject": true, "rejected": true,
		"未通过": true, "失败": true, "不一致": true, "认证失败": true, "核验失败": true,
	}
	tencentRealnameSuccessCodes = map[string]bool{
		"": true, "0": true, "00": true, "000": true, "0000": true,
		"200": true, "success": true, "ok": true,
	}
)

type tencentRealnameFieldScope struct {
	path   string
	fields map[string]interface{}
}

// collectTencentRealnameScopes 逐层展开 data，使 raw_data 及其嵌套对象里的结论字段同样可见。
func collectTencentRealnameScopes(path string, raw json.RawMessage, depth int) []tencentRealnameFieldScope {
	if depth < 0 || len(raw) == 0 {
		return nil
	}
	var fields map[string]interface{}
	if json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return nil
	}
	scopes := []tencentRealnameFieldScope{{path: path, fields: fields}}
	if depth == 0 {
		return scopes
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		nested, ok := fields[key].(map[string]interface{})
		if !ok {
			continue
		}
		encoded, err := json.Marshal(nested)
		if err != nil {
			continue
		}
		scopes = append(scopes, collectTencentRealnameScopes(path+"."+key, encoded, depth-1)...)
	}
	return scopes
}

func normalizeTencentRealnameFlagValue(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return strings.ToLower(strings.TrimSpace(typed))
	default:
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(typed)))
	}
}

func matchTencentRealnameOutcome(scope tencentRealnameFieldScope) (tencentRealnameOutcome, string) {
	normalizedFields := make(map[string]interface{}, len(scope.fields))
	for key, value := range scope.fields {
		normalizedFields[strings.ToLower(strings.TrimSpace(key))] = value
	}
	for _, key := range tencentRealnameOutcomeKeys {
		value, ok := normalizedFields[key]
		if !ok {
			continue
		}
		normalized := normalizeTencentRealnameFlagValue(value)
		switch {
		case tencentRealnameOutcomePassedValues[normalized]:
			return tencentRealnameOutcomePassed, scope.path + "." + key + "=" + normalized
		case tencentRealnameOutcomeRejectedValues[normalized]:
			return tencentRealnameOutcomeRejected, scope.path + "." + key + "=" + normalized
		}
	}
	for _, key := range tencentRealnameErrCodeKeys {
		value, ok := normalizedFields[key]
		if !ok {
			continue
		}
		normalized := normalizeTencentRealnameFlagValue(value)
		if tencentRealnameSuccessCodes[normalized] {
			return tencentRealnameOutcomePassed, scope.path + "." + key + "=" + normalized
		}
		return tencentRealnameOutcomeRejected, scope.path + "." + key + "=" + normalized
	}
	return tencentRealnameOutcomeUnknown, ""
}

// resolveTencentRealnameOutcome 返回结论及命中依据，依据会随认证记录一起留档。
func resolveTencentRealnameOutcome(raw json.RawMessage) (tencentRealnameOutcome, string) {
	for _, scope := range collectTencentRealnameScopes("data", raw, 2) {
		if outcome, basis := matchTencentRealnameOutcome(scope); outcome != tencentRealnameOutcomeUnknown {
			return outcome, basis
		}
	}
	return tencentRealnameOutcomeUnknown, ""
}

// tencentRealnameFaceResult 把“给用户看的原因”和“进认证记录的明细”分开：
// Reason 简短可读，Detail 含判定依据、响应码与完整上游正文。
type tencentRealnameFaceResult struct {
	Passed    bool
	Reason    string
	Detail    string
	SerialNo  string
	Score     string
	CertifyID string // 认证会话ID，用于查询最终结果
}

// tencentRealnameErrorCodeHints 来自聚合网关公开错误码表，用于把上游码翻译成处置建议。
var tencentRealnameErrorCodeHints = map[string]string{
	"AUTH_FAILED":          "签名验证失败，请核对 API Key/Secret 与服务器时间",
	"INSUFFICIENT_BALANCE": "上游余额不足，请充值后重试",
	"INSUFFICIENT_PACKAGE": "无可用套餐或套餐不含该产品，请购买套餐或关闭套餐扣费改用余额",
	"CHARGE_FAILED":        "上游扣费失败或并发冲突，请稍后重试",
	"PRODUCT_MISMATCH":     "product_code 与认证记录不一致",
	"RECORD_NOT_FOUND":     "未找到开放 API 认证记录，请核对 certify_id",
	"INVALID_PARAM":        "请求参数无效，请检查参数并确认未传 package_id",
	"PRODUCT_NOT_FOUND":    "产品不存在或已被禁用，请核对 product_code",
}

func tencentRealnameErrorCodeHint(businessCode string) string {
	return tencentRealnameErrorCodeHints[strings.ToUpper(strings.TrimSpace(businessCode))]
}

// tencentRealnameChargeFields 是网关通用响应里的扣费与套餐元数据，进认证记录用于核对是否计费。
var tencentRealnameChargeFields = []struct {
	key   string
	label string
}{
	{"is_charged", "已扣费"},
	{"charge_amount", "扣费金额"},
	{"package_used", "使用套餐"},
	{"package_id_used", "套餐ID"},
	{"use_package", "请求套餐模式"},
	{"cost_time", "上游耗时(ms)"},
}

// describeTencentRealnameCharge 汇总扣费元数据，缺失字段直接跳过。
func describeTencentRealnameCharge(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var fields map[string]interface{}
	if json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tencentRealnameChargeFields))
	for _, field := range tencentRealnameChargeFields {
		value, ok := fields[field.key]
		if !ok || value == nil {
			continue
		}
		parts = append(parts, field.label+"="+normalizeTencentRealnameFlagValue(value))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "；")
}

type tencentRealnameVerifyError struct {
	ResultUnknown bool
	Message       string
	HTTPStatus    int
	ResponseCode  int
	BusinessCode  string
	ResponseBody  string
}

func (e *tencentRealnameVerifyError) Error() string {
	return e.Message
}

func newTencentRealnameVerifyError(message string, resultUnknown bool) error {
	return &tencentRealnameVerifyError{ResultUnknown: resultUnknown, Message: message}
}

func newTencentRealnameResponseError(message string, resultUnknown bool, httpStatus, responseCode int, businessCode string) error {
	return &tencentRealnameVerifyError{
		ResultUnknown: resultUnknown,
		Message:       message,
		HTTPStatus:    httpStatus,
		ResponseCode:  responseCode,
		BusinessCode:  strings.TrimSpace(businessCode),
	}
}

func tencentRealnameErrorMetadata(err error) (httpStatus, responseCode int, businessCode string) {
	var target *tencentRealnameVerifyError
	if !errors.As(err, &target) {
		return 0, 0, ""
	}
	return target.HTTPStatus, target.ResponseCode, target.BusinessCode
}

// withTencentRealnameResponseBody 把完整上游正文挂到错误上，只用于认证记录留档，不进用户提示。
func withTencentRealnameResponseBody(err error, respBody []byte) error {
	var target *tencentRealnameVerifyError
	if err == nil || !errors.As(err, &target) {
		return err
	}
	target.ResponseBody = flattenTencentRealnameResponseBody(respBody)
	return target
}

func tencentRealnameErrorResponseBody(err error) string {
	var target *tencentRealnameVerifyError
	if !errors.As(err, &target) {
		return ""
	}
	return target.ResponseBody
}

func isTencentRealnameResultUnknown(err error) bool {
	var target *tencentRealnameVerifyError
	return errors.As(err, &target) && target.ResultUnknown
}

func tencentRealnameSignature(apiKey, timestamp, nonce, apiSecret string) string {
	mac := hmac.New(sha256.New, []byte(apiSecret))
	_, _ = mac.Write([]byte(apiKey + timestamp + nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

func newTencentRealnameNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

type tencentRealnameInitResult struct {
	CertifyID string
	AuthURL   string
}

type tencentRealnameInitData struct {
	Code       tencentRealnameFlexText `json:"code"`
	Message    string                  `json:"message"`
	CertifyID  string                  `json:"certify_id"`
	CertifyURL string                  `json:"certify_url"`
}

func tencentRealnameProductCode(cfg realnameConfig) string {
	if productCode := strings.TrimSpace(cfg.TencentProductCode); productCode != "" {
		return productCode
	}
	return tencentRealnameProduct
}

var tencentRealnameHTTPClient = &http.Client{Timeout: tencentRealnameHTTPTimeout}

func tencentRealnameRequest(cfg realnameConfig, endpointPath string, payload interface{}) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, newTencentRealnameVerifyError(fmt.Sprintf("构造靓仔聚合认证请求失败：%v", err), false)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := newTencentRealnameNonce()
	if err != nil {
		return 0, nil, newTencentRealnameVerifyError("生成靓仔聚合认证签名随机数失败", false)
	}
	endpoint := strings.TrimRight(cfg.TencentBaseURL, "/") + "/" + strings.TrimLeft(endpointPath, "/")

	logPayload := make(map[string]interface{})
	if err := json.Unmarshal(body, &logPayload); err == nil {
		for _, key := range []string{"name", "idcard", "id_card", "image", "image_base64", "img_base64", "certify_id"} {
			if _, exists := logPayload[key]; exists {
				logPayload[key] = "<redacted>"
			}
		}
		if sanitized, marshalErr := json.Marshal(logPayload); marshalErr == nil {
			fmt.Printf("[靓仔认证] 请求 %s %s, 参数: %s\n", http.MethodPost, endpointPath, string(sanitized))
		}
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, newTencentRealnameVerifyError(fmt.Sprintf("创建靓仔聚合认证请求失败：%v", err), false)
	}
	req.Header.Set("X-Api-Key", cfg.TencentAPIKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", tencentRealnameSignature(cfg.TencentAPIKey, timestamp, nonce, cfg.TencentAPISecret))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AuthPro-Tencent-Realname/1.0")

	resp, err := tencentRealnameHTTPClient.Do(req)
	if err != nil {
		fmt.Printf("[靓仔认证] 请求失败: %v\n", err)
		return 0, nil, newTencentRealnameVerifyError(fmt.Sprintf("请求靓仔聚合认证接口失败：%v", err), true)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		fmt.Printf("[靓仔认证] 读取响应失败: %v\n", err)
		return resp.StatusCode, nil, newTencentRealnameVerifyError(fmt.Sprintf("读取靓仔聚合认证响应失败：%v", err), true)
	}
	fmt.Printf("[靓仔认证] 响应状态: %d, 内容长度: %d\n", resp.StatusCode, len(respBody))

	return resp.StatusCode, respBody, nil
}

func buildTencentRealnameInitPayload(cfg realnameConfig, realName, idCard, returnURL string) map[string]interface{} {
	return map[string]interface{}{
		"product_code": tencentRealnameProductCode(cfg),
		"use_package":  cfg.TencentUsePackage,
		"name":         realName,
		"idcard":       idCard,
		"return_url":   returnURL,
	}
}

func tencentRealnameInitialize(cfg realnameConfig, realName, idCard, returnURL string) (tencentRealnameInitResult, error) {
	statusCode, respBody, err := tencentRealnameRequest(
		cfg,
		"/verify",
		buildTencentRealnameInitPayload(cfg, realName, idCard, returnURL),
	)
	if err != nil {
		return tencentRealnameInitResult{}, err
	}
	return parseTencentRealnameInitResponse(statusCode, respBody)
}

func parseTencentRealnameInitResponse(statusCode int, respBody []byte) (tencentRealnameInitResult, error) {
	var response tencentRealnameVerifyResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return tencentRealnameInitResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError(
				fmt.Sprintf("靓仔人脸认证初始化响应解析失败：HTTP %d，JSON错误：%v", statusCode, err),
				statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices,
				statusCode,
				0,
				"",
			),
			respBody,
		)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices || response.Code != 0 {
		_, err := parseTencentRealnameVerifyResponse(statusCode, respBody)
		if err != nil {
			return tencentRealnameInitResult{}, err
		}
		return tencentRealnameInitResult{}, newTencentRealnameResponseError("靓仔人脸认证初始化失败", false, statusCode, response.Code, "")
	}

	var data tencentRealnameInitData
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return tencentRealnameInitResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError("靓仔人脸认证初始化响应缺少有效 data", true, statusCode, response.Code, ""),
			respBody,
		)
	}
	certifyID := strings.TrimSpace(data.CertifyID)
	certifyURL := strings.TrimSpace(data.CertifyURL)
	if certifyID == "" || certifyURL == "" {
		return tencentRealnameInitResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError("靓仔人脸认证初始化未返回 certify_id 或 certify_url", true, statusCode, response.Code, string(data.Code)),
			respBody,
		)
	}
	parsedURL, err := url.ParseRequestURI(certifyURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return tencentRealnameInitResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError("靓仔人脸认证初始化返回的 certify_url 无效", true, statusCode, response.Code, string(data.Code)),
			respBody,
		)
	}
	return tencentRealnameInitResult{CertifyID: certifyID, AuthURL: certifyURL}, nil
}

func buildTencentRealnameQueryPayload(cfg realnameConfig, certifyID string) map[string]interface{} {
	return map[string]interface{}{
		"certify_id":   certifyID,
		"product_code": tencentRealnameProductCode(cfg),
	}
}

func tencentRealnameQuery(cfg realnameConfig, certifyID string) (tencentRealnameFaceResult, bool, error) {
	statusCode, respBody, err := tencentRealnameRequest(cfg, "/faceQuery", buildTencentRealnameQueryPayload(cfg, certifyID))
	if err != nil {
		return tencentRealnameFaceResult{}, false, err
	}
	return parseTencentRealnameQueryResponse(statusCode, respBody)
}

// tencentRealnameVerifyWithImage 调用靓仔人脸核验（姓名+身份证号+人像照片比对）。
// 返回 (faceResult, error)：error 表示请求级失败；faceResult.Passed 表示是否通过。
func tencentRealnameVerifyWithImage(cfg realnameConfig, realName, idCard, imgBase64 string) (tencentRealnameFaceResult, error) {
	// 从 data:image/...;base64,... 中提取纯 base64 部分
	imgData := imgBase64
	if strings.HasPrefix(imgBase64, "data:image/") {
		commaIdx := strings.Index(imgBase64, ";base64,")
		if commaIdx >= 0 {
			imgData = imgBase64[commaIdx+8:]
		}
	}

	// 靓仔接口要求 Base64 图片 ≤500KB
	decoded, err := base64.StdEncoding.DecodeString(imgData)
	if err != nil {
		return tencentRealnameFaceResult{}, newTencentRealnameVerifyError("图片数据无效", false)
	}
	if len(decoded) > 375*1024 { // 375KB 原始数据 ≈ 500KB Base64
		return tencentRealnameFaceResult{}, newTencentRealnameVerifyError("图片超过 500KB 限制，请压缩后重试", false)
	}

	payload := map[string]interface{}{
		"product_code": tencentRealnameProductCode(cfg),
		"use_package":  cfg.TencentUsePackage,
		"name":         realName,
		"idcard":       idCard,
		"image_base64": imgData,
	}

	statusCode, respBody, err := tencentRealnameRequest(cfg, "/verify", payload)
	if err != nil {
		return tencentRealnameFaceResult{}, err
	}

	return parseTencentRealnameVerifyResponse(statusCode, respBody)
}

func tencentRealnameQueryPending(raw json.RawMessage, message string) bool {
	pendingValues := map[string]bool{
		"pending": true, "processing": true, "waiting": true, "initialized": true,
		"initializing": true, "in_progress": true,
	}
	var fields map[string]interface{}
	if json.Unmarshal(raw, &fields) == nil {
		for _, key := range []string{"status", "state", "certify_status", "auth_status"} {
			if value, ok := fields[key]; ok && pendingValues[normalizeTencentRealnameFlagValue(value)] {
				return true
			}
		}
		if value, ok := fields["message"]; ok {
			message += " " + fmt.Sprint(value)
		}
	}
	message = strings.ToLower(strings.TrimSpace(message))
	for _, keyword := range []string{"pending", "processing", "waiting", "未完成", "认证中", "处理中", "等待认证", "尚未认证"} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func parseTencentRealnameQueryResponse(statusCode int, respBody []byte) (tencentRealnameFaceResult, bool, error) {
	var response tencentRealnameVerifyResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		_, parseErr := parseTencentRealnameVerifyResponse(statusCode, respBody)
		return tencentRealnameFaceResult{}, false, parseErr
	}
	if tencentRealnameQueryPending(response.Data, response.Message) {
		return tencentRealnameFaceResult{}, true, nil
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices || response.Code != 0 {
		_, err := parseTencentRealnameVerifyResponse(statusCode, respBody)
		return tencentRealnameFaceResult{}, false, err
	}
	data, dataAnomaly := decodeTencentRealnameVerifyData(response.Data)
	if dataAnomaly != "" {
		return tencentRealnameFaceResult{}, true, nil
	}
	outcome, _ := resolveTencentRealnameOutcome(response.Data)
	businessCode := strings.ToLower(strings.TrimSpace(string(data.Code)))
	if outcome == tencentRealnameOutcomeUnknown && tencentRealnameSuccessCodes[businessCode] {
		return tencentRealnameFaceResult{}, true, nil
	}
	result, err := parseTencentRealnameVerifyResponse(statusCode, respBody)
	return result, false, err
}

type realnameProductItem struct {
	ProductCode string `json:"productCode"`
	Name        string `json:"name"`
	Price       string `json:"price"`
}

type realnameProductRequest struct {
	TencentAPIKey    string `json:"tencentApiKey"`
	TencentAPISecret string `json:"tencentApiSecret"`
	TencentBaseURL   string `json:"tencentBaseUrl"`
}

type tencentRealnameProductsResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func AdminRealnameProducts(c *gin.Context) {
	var input realnameProductRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	db, err := openSystemConfigDB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}
	if err := ensureSystemConfigStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化系统配置失败"})
		return
	}
	cfg, err := loadRealnameConfig(db)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "读取实名认证配置失败"})
		return
	}

	apiKey := strings.TrimSpace(input.TencentAPIKey)
	if apiKey == "" {
		apiKey = cfg.TencentAPIKey
	}
	apiSecret := strings.TrimSpace(input.TencentAPISecret)
	if apiSecret == "" && apiKey == cfg.TencentAPIKey {
		apiSecret = cfg.TencentAPISecret
	}
	baseURL := strings.TrimRight(strings.TrimSpace(input.TencentBaseURL), "/")
	if baseURL == "" {
		baseURL = cfg.TencentBaseURL
	}
	if apiKey == "" || apiSecret == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "请先填写 API Key 和 API Secret"})
		return
	}
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "接口地址必须是有效的 HTTPS 地址"})
		return
	}

	products, err := fetchTencentRealnameProducts(apiKey, apiSecret, baseURL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "", "data": gin.H{"list": products}})
}

func fetchTencentRealnameProducts(apiKey, apiSecret, baseURL string) ([]realnameProductItem, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := newTencentRealnameNonce()
	if err != nil {
		return nil, errors.New("生成产品列表签名随机数失败")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/getProducts", nil)
	if err != nil {
		return nil, fmt.Errorf("创建产品列表请求失败：%w", err)
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", tencentRealnameSignature(apiKey, timestamp, nonce, apiSecret))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AuthPro-Tencent-Realname/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取产品列表失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, errors.New("读取产品列表失败")
	}
	return parseTencentRealnameProducts(resp.StatusCode, body)
}

func parseTencentRealnameProducts(statusCode int, body []byte) ([]realnameProductItem, error) {
	var response tencentRealnameProductsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.New("产品列表响应解析失败")
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices || response.Code != 0 {
		message := strings.TrimSpace(response.Message)
		var detail struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(response.Data, &detail) == nil {
			if message == "" {
				message = strings.TrimSpace(detail.Message)
			}
			if detail.Code != "" {
				if message == "" {
					message = detail.Code
				} else {
					message += "（" + detail.Code + "）"
				}
			}
		}
		if message == "" {
			message = fmt.Sprintf("HTTP %d", statusCode)
		}
		return nil, errors.New("获取产品列表失败：" + message)
	}

	var data interface{}
	decoder := json.NewDecoder(strings.NewReader(string(response.Data)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return nil, errors.New("产品列表数据格式不正确")
	}
	items := findTencentRealnameProductItems(data)
	if len(items) == 0 {
		return []realnameProductItem{}, nil
	}
	return items, nil
}

func findTencentRealnameProductItems(data interface{}) []realnameProductItem {
	var rawItems []interface{}
	switch value := data.(type) {
	case []interface{}:
		rawItems = value
	case map[string]interface{}:
		for _, key := range []string{"products", "productList", "product_list", "list", "items", "records", "data"} {
			if nested, ok := value[key]; ok {
				if items := findTencentRealnameProductItems(nested); len(items) > 0 {
					return items
				}
			}
		}
		rawItems = []interface{}{value}
	}

	items := make([]realnameProductItem, 0, len(rawItems))
	seen := map[string]bool{}
	for _, raw := range rawItems {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		productCode := firstTencentRealnameProductValue(item, "product_code", "productCode", "code")
		if productCode == "" || seen[productCode] {
			continue
		}
		seen[productCode] = true
		name := firstTencentRealnameProductValue(item, "product_name", "productName", "name", "title")
		if name == "" {
			name = productCode
		}
		items = append(items, realnameProductItem{
			ProductCode: productCode,
			Name:        name,
			Price:       firstTencentRealnameProductValue(item, "price", "unit_price", "unitPrice"),
		})
	}
	return items
}

func firstTencentRealnameProductValue(item map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := item[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return ""
}

func summarizeTencentRealnameResponseBody(body []byte) string {
	const maxRunes = 96

	text := flattenTencentRealnameResponseBody(body)
	runes := []rune(text)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return text
}

// flattenTencentRealnameResponseBody 压成单行但不截断，认证记录需要完整上游返回。
func flattenTencentRealnameResponseBody(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return "<empty>"
	}
	return text
}

// tencentRealnameVerifyDetail 组装进认证记录的明细：判定依据、扣费元数据、响应码、完整正文。
func tencentRealnameVerifyDetail(statusCode, responseCode int, basis, charge string, respBody []byte) string {
	lines := make([]string, 0, 4)
	if basis != "" {
		lines = append(lines, "判定依据："+basis)
	}
	if charge != "" {
		lines = append(lines, "扣费信息："+charge)
	}
	lines = append(lines, fmt.Sprintf("接口响应码：%d；HTTP状态：%d", responseCode, statusCode))
	lines = append(lines, "上游响应正文："+flattenTencentRealnameResponseBody(respBody))
	return strings.Join(lines, "\n")
}

func parseTencentRealnameVerifyResponse(statusCode int, respBody []byte) (tencentRealnameFaceResult, error) {
	var result tencentRealnameVerifyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		resultUnknown := statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
		detail := fmt.Sprintf(
			"腾讯增强人脸响应解析失败：HTTP %d，JSON错误：%v，响应正文：%s",
			statusCode,
			err,
			summarizeTencentRealnameResponseBody(respBody),
		)
		return tencentRealnameFaceResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError(detail, resultUnknown, statusCode, 0, ""),
			respBody,
		)
	}
	data, dataAnomaly := decodeTencentRealnameVerifyData(result.Data)
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices || result.Code != 0 {
		message := strings.TrimSpace(result.Message)
		detailMessage := strings.TrimSpace(data.Message)
		if detailMessage != "" && !strings.Contains(message, detailMessage) {
			if message == "" {
				message = detailMessage
			} else {
				message += "：" + detailMessage
			}
		}
		businessCode := strings.TrimSpace(string(data.Code))
		if businessCode != "" && !strings.Contains(message, businessCode) {
			if message == "" {
				message = businessCode
			} else {
				message += "（" + businessCode + "）"
			}
		}
		if message == "" && dataAnomaly != "" {
			message = "data=" + dataAnomaly
		}
		if message == "" {
			message = fmt.Sprintf("HTTP %d", statusCode)
		}
		if hint := tencentRealnameErrorCodeHint(businessCode); hint != "" && !strings.Contains(message, hint) {
			message += "；处置建议：" + hint
		}
		return tencentRealnameFaceResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError(
				"腾讯增强人脸请求失败："+message,
				false,
				statusCode,
				result.Code,
				businessCode,
			),
			respBody,
		)
	}

	if dataAnomaly != "" {
		detail := fmt.Sprintf(
			"腾讯增强人脸响应未包含认证结果：HTTP %d，data=%s，响应正文：%s",
			statusCode,
			dataAnomaly,
			summarizeTencentRealnameResponseBody(respBody),
		)
		return tencentRealnameFaceResult{}, withTencentRealnameResponseBody(
			newTencentRealnameResponseError(detail, true, statusCode, result.Code, ""),
			respBody,
		)
	}

	serialNo := data.CertifyID
	score := extractTencentRealnameScore(data.RawData)
	businessCode := strings.TrimSpace(string(data.Code))
	outcome, basis := resolveTencentRealnameOutcome(result.Data)
	if outcome == tencentRealnameOutcomeUnknown {
		// 接口层已判成功，响应里又找不到任何否定结论时按通过处理，
		// 完整正文仍会留档，便于事后核对上游到底返回了什么。
		if tencentRealnameSuccessCodes[strings.ToLower(businessCode)] {
			outcome = tencentRealnameOutcomePassed
			basis = "接口返回成功码，响应未包含结论字段"
		} else {
			outcome = tencentRealnameOutcomeRejected
			basis = "data.code=" + businessCode
		}
	}
	detail := tencentRealnameVerifyDetail(statusCode, result.Code, basis, describeTencentRealnameCharge(result.Data), respBody)
	if outcome == tencentRealnameOutcomePassed {
		return tencentRealnameFaceResult{
			Passed:    true,
			Detail:    detail,
			SerialNo:  serialNo,
			Score:     score,
			CertifyID: data.CertifyID,
		}, nil
	}
	reason := strings.TrimSpace(data.Message)
	if reason == "" {
		reason = businessCode
	} else if businessCode != "" && !strings.Contains(reason, businessCode) {
		reason += "（" + businessCode + "）"
	}
	if reason == "" {
		reason = "人脸核验未通过"
	}
	if hint := tencentRealnameErrorCodeHint(businessCode); hint != "" && !strings.Contains(reason, hint) {
		reason += "；处置建议：" + hint
	}
	return tencentRealnameFaceResult{
		Reason:    reason,
		Detail:    reason + "\n" + detail,
		SerialNo:  serialNo,
		Score:     score,
		CertifyID: data.CertifyID,
	}, nil
}

func extractTencentRealnameScore(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var data map[string]interface{}
	if json.Unmarshal(raw, &data) != nil {
		return ""
	}
	for _, key := range []string{"score", "similarity", "confidence"} {
		if value, ok := data[key]; ok {
			return fmt.Sprint(value)
		}
	}
	return ""
}

// ========== 管理端：实名认证记录 ==========

type realnameRecordItem struct {
	ID         int64  `json:"id"`
	OwnerType  string `json:"ownerType"`
	OwnerID    int64  `json:"ownerId"`
	OwnerName  string `json:"ownerName"` // 用户昵称/代理名称
	OwnerEmail string `json:"ownerEmail"`
	Provider   string `json:"provider"`
	RealName   string `json:"realName"` // 展示层脱敏
	IDCard     string `json:"idCard"`   // 展示层脱敏
	Status     string `json:"status"`
	FailReason string `json:"failReason"`
	SerialNo   string `json:"serialNo"`
	Score      string `json:"score"`
	RecordType string `json:"recordType"` // init 首次 / review 复核
	ExpiresAt  string `json:"expiresAt"`  // 本次认证产生的有效期
	// Validity 为主体当前实名状态：unverified 未实名 / permanent 永久有效 /
	// valid 有效 / expiring 30 天内到期 / expired 已过期。
	Validity       string `json:"validity"`
	OwnerExpiresAt string `json:"ownerExpiresAt"`
	CreatedAt      string `json:"createdAt"`
}

// AdminRealnameRecordList 实名认证记录列表（姓名/身份证展示时脱敏，库中明文存储）。
func AdminRealnameRecordList(c *gin.Context) {
	db, err := openSystemConfigDB()
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	ownerSource := ` FROM realname_records r
		LEFT JOIN (
			SELECT 'user' AS owner_type, id AS owner_id,
				COALESCE(nickname, '') AS owner_name, COALESCE(email, '') AS owner_email,
				COALESCE(real_name, '') AS real_name, COALESCE(real_id_card, '') AS real_id_card,
				realname_at, realname_expires_at
			FROM users
			UNION ALL
			SELECT 'agent' AS owner_type, id AS owner_id,
				COALESCE(name, '') AS owner_name, COALESCE(email, '') AS owner_email,
				COALESCE(real_name, '') AS real_name, COALESCE(real_id_card, '') AS real_id_card,
				realname_at, realname_expires_at
			FROM agents
		) o ON o.owner_type = r.owner_type AND o.owner_id = r.owner_id`
	where := ` WHERE r.status IN ('passed', 'failed')
		AND (
			r.status = 'failed'
			OR (
				o.realname_at IS NOT NULL
				AND TRIM(o.real_name) != '' AND TRIM(o.real_id_card) != ''
				AND o.real_name != '已认证' AND o.real_id_card != '已认证'
			)
		)`
	args := []interface{}{}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		where += ` AND (
			(CASE WHEN r.status = 'passed' THEN o.real_name ELSE r.real_name END) LIKE ?
			OR (CASE WHEN r.status = 'passed' THEN o.real_id_card ELSE r.id_card END) LIKE ?
			OR o.owner_name LIKE ? OR o.owner_email LIKE ? OR r.fail_reason LIKE ? OR r.serial_no LIKE ?
		)`
		like := "%" + kw + "%"
		args = append(args, like, like, like, like, like, like)
	}
	if provider := strings.TrimSpace(c.Query("provider")); provider == realnameProviderAlipay || provider == realnameProviderKuaitong || provider == realnameProviderTencent || provider == realnameProviderXiaomu {
		where += " AND r.provider = ?"
		args = append(args, provider)
	}
	if status := strings.TrimSpace(c.Query("status")); status == realnameFaceStatusPassed || status == realnameFaceStatusFailed {
		where += " AND r.status = ?"
		args = append(args, status)
	}
	if recordType := strings.TrimSpace(c.Query("recordType")); validRealnameRecordType(recordType) {
		where += " AND r.record_type = ?"
		args = append(args, recordType)
	}
	// 有效期筛选：expired 已过期（不含宽限期内）；expiring 在提醒窗口内即将过期。
	switch strings.TrimSpace(c.Query("validity")) {
	case "expired":
		where += " AND o.realname_expires_at IS NOT NULL AND o.realname_expires_at <= NOW()"
	case "expiring":
		where += " AND o.realname_expires_at IS NOT NULL AND o.realname_expires_at > NOW() AND o.realname_expires_at <= DATE_ADD(NOW(), INTERVAL 30 DAY)"
	case "permanent":
		where += " AND o.realname_expires_at IS NULL AND o.realname_at IS NOT NULL"
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*)"+ownerSource+where, args...).Scan(&total); err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "查询失败"})
		return
	}

	query := `SELECT r.id, r.owner_type, r.owner_id, r.provider,
		CASE WHEN r.status = 'passed' THEN o.real_name ELSE r.real_name END,
		CASE WHEN r.status = 'passed' THEN o.real_id_card ELSE r.id_card END,
		r.status, r.fail_reason, r.serial_no, r.score, r.record_type, r.expires_at, r.created_at,
		COALESCE(o.owner_name, ''), COALESCE(o.owner_email, ''),
		o.realname_at, o.realname_expires_at` +
		ownerSource + where + ` ORDER BY r.id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (page-1)*pageSize)

	rows, err := db.Query(query, args...)
	if err != nil {
		writeSystemConfig(c, http.StatusOK, gin.H{"code": 500, "msg": "查询失败"})
		return
	}
	defer rows.Close()

	list := make([]realnameRecordItem, 0)
	for rows.Next() {
		var item realnameRecordItem
		var createdAt, recordExpiresAt, ownerExpiresAt sql.NullTime
		var ownerRealnameAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.OwnerType, &item.OwnerID, &item.Provider, &item.RealName, &item.IDCard,
			&item.Status, &item.FailReason, &item.SerialNo, &item.Score, &item.RecordType, &recordExpiresAt, &createdAt,
			&item.OwnerName, &item.OwnerEmail, &ownerRealnameAt, &ownerExpiresAt); err != nil {
			continue
		}
		if createdAt.Valid {
			item.CreatedAt = createdAt.Time.Format("2006-01-02 15:04:05")
		}
		if recordExpiresAt.Valid {
			item.ExpiresAt = recordExpiresAt.Time.Format("2006-01-02 15:04:05")
		}
		// 主体当前实名状态：已过期 / 即将过期 / 有效 / 未实名。
		switch {
		case !ownerRealnameAt.Valid:
			item.Validity = "unverified"
		case !ownerExpiresAt.Valid:
			item.Validity = "permanent"
		case !ownerExpiresAt.Time.After(time.Now()):
			item.Validity = "expired"
		case ownerExpiresAt.Time.Before(time.Now().AddDate(0, 0, 30)):
			item.Validity = "expiring"
		default:
			item.Validity = "valid"
		}
		if ownerExpiresAt.Valid {
			item.OwnerExpiresAt = ownerExpiresAt.Time.Format("2006-01-02 15:04:05")
		}
		item.RealName = maskRealName(item.RealName)
		item.IDCard = maskIDCard(item.IDCard)
		list = append(list, item)
	}

	writeSystemConfig(c, http.StatusOK, gin.H{"code": 200, "msg": "", "data": gin.H{
		"list":     list,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	}})
}

// ========== 授权校验用：判断用户是否需要实名 ==========

// userRealnameRequired 返回（该应用是否要求实名，授权归属主体是否已实名，实名状态）。
// 用户与代理商都按各自主体表校验；未知归属类型才按管理员直接创建处理。
// 配置读取失败时放行（fail-open）并写一条告警日志，避免数据库抖动导致线上授权全部失败。
func userRealnameRequired(db *sql.DB, appID int64, licenseID int64) (required bool, verified bool, status string, err error) {
	rnCfg, err := loadRealnameConfig(db)
	if err != nil {
		fmt.Printf("[实名认证] 读取配置失败，本次授权校验放行：%v\n", err)
		return false, true, realnameStatusOK, nil
	}
	if !rnCfg.Enabled || !realnameAppInScope(rnCfg, appID) {
		return false, true, realnameStatusOK, nil
	}

	var ownerType string
	var ownerID int64
	if err := db.QueryRow("SELECT owner_type, owner_id FROM licenses WHERE id = ?", licenseID).Scan(&ownerType, &ownerID); err != nil {
		return true, false, realnameStatusNever, err
	}
	// 管理员直接创建的授权不受实名限制
	if ownerType != "user" && ownerType != "agent" {
		return true, true, realnameStatusOK, nil
	}

	result := checkRealnameForOwner(db, rnCfg, ownerType, ownerID)
	return result.Required, result.Verified, result.Status, nil
}

// realnameAppInScope 判断应用是否落在实名生效范围内：
// all 对所有应用（含今后新增）生效；by_app 只对管理员勾选的应用生效。
func realnameAppInScope(cfg realnameConfig, appID int64) bool {
	if cfg.Scope == realnameScopeAll {
		return true
	}
	return cfg.RequireAppIDs[appID]
}

// realnameBlockedReason 把实名状态映射为授权校验的拒绝原因，未拦截时返回空串。
func realnameBlockedReason(status string) string {
	switch status {
	case realnameStatusNever:
		return verifyReasonRealnameRequired
	case realnameStatusExpired:
		return verifyReasonRealnameExpired
	}
	return ""
}

// realnamePendingNotice 宽限期内虽放行，但需要提示用户尽快重新认证。
func realnamePendingNotice(status string) string {
	if status == realnameStatusGrace {
		return "实名信息已过期，当前处于宽限期，请尽快重新认证"
	}
	return ""
}

// ========== 购买前实名门槛 ==========

// 购买拦截原因，供前端区分"从未实名"与"实名过期"。
const (
	purchaseReasonRealnameRequired = "realname_required_purchase"
	purchaseReasonRealnameExpired  = "realname_expired_purchase"
)

// realnameCheckForPurchase 校验购买/开通前的实名门槛。
// 返回 (reason, message)：reason 为空表示放行。
//   - 开关未开启、实名功能未启用或插件未启用时一律放行，避免管理员未配置服务商时用户无法购买；
//   - payer 为实际付款方（user/agent），始终校验；
//   - owner 为目标授权归属，配置 purchase_check_owner 时在 owner 与 payer 不同主体时一并校验。
func realnameCheckForPurchase(db *sql.DB, payerType string, payerID int64, ownerType string, ownerID int64) (string, string) {
	rnCfg, err := loadRealnameConfig(db)
	if err != nil {
		fmt.Printf("[实名认证] 读取配置失败，本次购买校验放行：%v\n", err)
		return "", ""
	}
	if !rnCfg.Enabled || !rnCfg.PluginEnabled || !rnCfg.PurchaseRequired {
		return "", ""
	}

	type realnameSubject struct {
		ownerType string
		ownerID   int64
		isOwner   bool
	}
	targets := make([]realnameSubject, 0, 2)
	if payerID > 0 && (payerType == "user" || payerType == "agent") {
		targets = append(targets, realnameSubject{payerType, payerID, false})
	}
	if rnCfg.PurchaseCheckOwner && ownerID > 0 && (ownerType == "user" || ownerType == "agent") &&
		(ownerType != payerType || ownerID != payerID) {
		targets = append(targets, realnameSubject{ownerType, ownerID, true})
	}
	if len(targets) == 0 {
		return "", ""
	}

	for _, target := range targets {
		check := checkRealnameForOwner(db, rnCfg, target.ownerType, target.ownerID)
		switch check.Status {
		case realnameStatusNever:
			if target.isOwner {
				return purchaseReasonRealnameRequired, "该用户尚未完成实名认证，请先让用户完成实名后再为其开通"
			}
			return purchaseReasonRealnameRequired, "购买前需完成实名认证，请先在个人中心完成实名"
		case realnameStatusExpired:
			if target.isOwner {
				return purchaseReasonRealnameExpired, "该用户的实名信息已过期，请先让用户重新认证后再为其开通"
			}
			return purchaseReasonRealnameExpired, "实名信息已过期，请先重新认证后再购买"
		}
	}
	return "", ""
}

// realnamePurchaseCheckResponse 统一输出购买前的实名拦截响应，保证各购买入口文案一致。
func realnamePurchaseCheckResponse(c *gin.Context, reason, message string) {
	c.JSON(http.StatusOK, gin.H{
		"code": 403,
		"msg":  message,
		"data": gin.H{"result": "fail", "reason": reason},
	})
}

// realnamePurchaseBlocked 在任何扣款/建单之前调用：返回 true 表示已写入响应并应终止处理。
func realnamePurchaseBlocked(c *gin.Context, db *sql.DB, payerType string, payerID int64, ownerType string, ownerID int64) bool {
	reason, message := realnameCheckForPurchase(db, payerType, payerID, ownerType, ownerID)
	if reason == "" {
		return false
	}
	realnamePurchaseCheckResponse(c, reason, message)
	return true
}

// ========== 支付宝网关调用 ==========

type alipayRealnameInitResponse struct {
	CertifyID string `json:"certify_id"`
}

type alipayRealnameQueryResponse struct {
	Passed       string `json:"passed"` // T/F
	IdentityInfo struct {
		CertName string `json:"cert_name"`
		CertNo   string `json:"cert_no"`
	} `json:"identity_info"`
}

func alipayRealnameInitialize(cfg realnameConfig, outerOrderNo, realName, idCard string) (*alipayRealnameInitResponse, error) {
	bizContent, _ := json.Marshal(map[string]interface{}{
		"outer_order_no": outerOrderNo,
		"biz_code":       "FACE",
		"identity_param": map[string]string{
			"identity_type": "CERT_INFO",
			"cert_type":     "IDENTITY_CARD",
			"cert_name":     realName,
			"cert_no":       idCard,
		},
		"merchant_config": map[string]string{},
	})

	resp, err := alipayPost(cfg, realnameInitMethod, string(bizContent))
	if err != nil {
		return nil, err
	}
	node, ok := resp["datadigital_fincloud_generalsaas_face_certify_initialize_response"].(map[string]interface{})
	if !ok {
		return nil, errors.New("响应格式异常")
	}
	if code, _ := node["code"].(string); code != "10000" {
		subMsg, _ := node["sub_msg"].(string)
		if subMsg == "" {
			subMsg, _ = node["msg"].(string)
		}
		return nil, fmt.Errorf("支付宝返回错误：%s", subMsg)
	}
	certifyID, _ := node["certify_id"].(string)
	if certifyID == "" {
		return nil, errors.New("未返回 certify_id")
	}
	return &alipayRealnameInitResponse{CertifyID: certifyID}, nil
}

func alipayRealnameQuery(cfg realnameConfig, certifyID, outerOrderNo string) (*alipayRealnameQueryResponse, error) {
	biz := map[string]string{"certify_id": certifyID}
	if outerOrderNo != "" {
		biz["outer_order_no"] = outerOrderNo
	}
	bizContent, _ := json.Marshal(biz)

	resp, err := alipayPost(cfg, realnameQueryMethod, string(bizContent))
	if err != nil {
		return nil, err
	}
	node, ok := resp["datadigital_fincloud_generalsaas_face_certify_query_response"].(map[string]interface{})
	if !ok {
		return nil, errors.New("响应格式异常")
	}
	if code, _ := node["code"].(string); code != "10000" {
		return nil, fmt.Errorf("查询未成功")
	}
	raw, _ := json.Marshal(node)
	var out alipayRealnameQueryResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// alipayPost 以 RSA2 签名调用支付宝网关，返回整个响应 JSON。
func alipayPost(cfg realnameConfig, method, bizContent string) (map[string]interface{}, error) {
	return alipayPostParams(cfg, method, bizContent, nil)
}

// alipayPostParams 在 alipayPost 基础上支持顶层公共参数：
// oauth.token 的 grant_type/code、consult 的 auth_token 等不属于 biz_content。
func alipayPostParams(cfg realnameConfig, method, bizContent string, extraParams map[string]string) (map[string]interface{}, error) {
	privateKey, err := parseAlipayPrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("应用私钥解析失败：%w", err)
	}

	params := map[string]string{
		"app_id":    cfg.AppID,
		"method":    method,
		"format":    "JSON",
		"charset":   "utf-8",
		"sign_type": "RSA2",
		"timestamp": time.Now().Format("2006-01-02 15:04:05"),
		"version":   "1.0",
	}
	if bizContent != "" {
		params["biz_content"] = bizContent
	}
	for k, v := range extraParams {
		params[k] = v
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	signSource := strings.Join(pairs, "&")

	digest := sha256.Sum256([]byte(signSource))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("签名失败：%w", err)
	}
	params["sign"] = base64.StdEncoding.EncodeToString(signature)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	client := &http.Client{Timeout: realnameHTTPTimeout}
	gateway := cfg.Gateway
	if gateway == "" {
		gateway = alipayDefaultGateway
	}
	httpResp, err := client.PostForm(gateway, form)
	if err != nil {
		return nil, fmt.Errorf("请求支付宝网关失败：%w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取支付宝响应失败：%w", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("支付宝响应解析失败")
	}
	return result, nil
}

// ========== 支付宝实名信息验证（preconsult + 用户授权 + consult） ==========

type alipayPreconsultResponse struct {
	VerifyID string `json:"verify_id"`
}

type alipayConsultResponse struct {
	Passed     string `json:"passed"` // T 一致 / F 不一致
	FailReason string `json:"fail_reason"`
	FailParams string `json:"fail_params"`
}

// alipayPreconsultBizContent 按官方文档组装预咨询接口的业务参数：
// 平铺的 user_name/cert_type/cert_no，可选 mobile/logon_id 暂未使用。
// 文档：https://opendocs.alipay.com/open/02qq4q
func alipayPreconsultBizContent(realName, idCard string) string {
	bizContent, _ := json.Marshal(map[string]string{
		"user_name": realName,
		"cert_type": "IDENTITY_CARD",
		"cert_no":   idCard,
	})
	return string(bizContent)
}

// alipayPreconsult 录入用户实名信息，返回 verify_id（有效期 2 小时）。
func alipayPreconsult(cfg realnameConfig, realName, idCard string) (*alipayPreconsultResponse, error) {
	resp, err := alipayPost(cfg, alipayPreconsultMethod, alipayPreconsultBizContent(realName, idCard))
	if err != nil {
		return nil, err
	}
	node, ok := resp["alipay_user_certdoc_certverify_preconsult_response"].(map[string]interface{})
	if !ok {
		return nil, errors.New("响应格式异常")
	}
	if code, _ := node["code"].(string); code != "10000" {
		subMsg, _ := node["sub_msg"].(string)
		if subMsg == "" {
			subMsg, _ = node["msg"].(string)
		}
		return nil, fmt.Errorf("支付宝返回错误：%s", subMsg)
	}
	verifyID, _ := node["verify_id"].(string)
	if verifyID == "" {
		return nil, errors.New("支付宝未返回 verify_id")
	}
	return &alipayPreconsultResponse{VerifyID: verifyID}, nil
}

// alipayOauthToken 用授权码 auth_code 换取用户授权令牌。
func alipayOauthToken(cfg realnameConfig, authCode string) (string, error) {
	if strings.TrimSpace(authCode) == "" {
		return "", errors.New("缺少授权码 auth_code")
	}
	resp, err := alipayPostParams(cfg, alipayOauthMethod, "", map[string]string{
		"grant_type": "authorization_code",
		"code":       authCode,
	})
	if err != nil {
		return "", err
	}
	node, ok := resp["alipay_system_oauth_token_response"].(map[string]interface{})
	if !ok {
		// oauth 接口失败时错误节点为 alipay_error_response
		if errNode, ok := resp["error_response"].(map[string]interface{}); ok {
			subMsg, _ := errNode["sub_msg"].(string)
			if subMsg == "" {
				subMsg, _ = errNode["msg"].(string)
			}
			return "", fmt.Errorf("换取授权令牌失败：%s", subMsg)
		}
		return "", errors.New("换取授权令牌响应格式异常")
	}
	token, _ := node["access_token"].(string)
	if token == "" {
		subMsg, _ := node["sub_msg"].(string)
		if subMsg == "" {
			subMsg, _ = node["msg"].(string)
		}
		return "", fmt.Errorf("换取授权令牌失败：%s", subMsg)
	}
	return token, nil
}

// alipayConsult 对比预录入信息与用户支付宝实名信息，返回校验结果。
func alipayConsult(cfg realnameConfig, verifyID, accessToken string) (*alipayConsultResponse, error) {
	bizContent, _ := json.Marshal(map[string]string{"verify_id": verifyID})
	resp, err := alipayPostParams(cfg, alipayConsultMethod, string(bizContent), map[string]string{
		"auth_token": accessToken,
	})
	if err != nil {
		return nil, err
	}
	node, ok := resp["alipay_user_certdoc_certverify_consult_response"].(map[string]interface{})
	if !ok {
		return nil, errors.New("响应格式异常")
	}
	if code, _ := node["code"].(string); code != "10000" {
		subMsg, _ := node["sub_msg"].(string)
		if subMsg == "" {
			subMsg, _ = node["msg"].(string)
		}
		return nil, fmt.Errorf("支付宝返回错误：%s", subMsg)
	}
	raw, _ := json.Marshal(node)
	var out alipayConsultResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// alipayInfoAuthorizeURL 构造实名信息验证的用户授权链接。
// PC 端该页面自带登录二维码，移动端包 alipays scheme 唤起支付宝后端内打开。
func alipayInfoAuthorizeURL(cfg realnameConfig, verifyID, callbackURL string) string {
	base := alipayAuthorizeURL
	if cfg.Gateway == alipaySandboxGateway {
		base = alipaySandboxAuthorizeURL
	}
	values := url.Values{}
	values.Set("app_id", cfg.AppID)
	values.Set("scope", "id_verify")
	values.Set("redirect_uri", callbackURL)
	values.Set("cert_verify_id", verifyID)
	return base + "?" + values.Encode()
}

func parseAlipayPrivateKey(raw string) (*rsa.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, `\n`, "\n")
	if !strings.Contains(raw, "BEGIN") {
		raw = "-----BEGIN RSA PRIVATE KEY-----\n" + raw + "\n-----END RSA PRIVATE KEY-----"
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("PEM 解码失败")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("无法解析 RSA 私钥")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("不是 RSA 私钥")
	}
	return key, nil
}

// ========== 工具函数 ==========

func isValidIDCard(id string) bool {
	if len(id) == 15 {
		for _, ch := range id {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		return true
	}
	if len(id) != 18 {
		return false
	}
	for i, ch := range id {
		if i < 17 {
			if ch < '0' || ch > '9' {
				return false
			}
		} else if !(ch >= '0' && ch <= '9' || ch == 'X') {
			return false
		}
	}
	// 校验码
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := []byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(id[i]-'0') * weights[i]
	}
	return codes[sum%11] == id[17]
}

func maskRealName(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	if len(runes) <= 1 {
		return "*"
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

func maskIDCard(id string) string {
	if len(id) < 8 {
		if id == "" {
			return ""
		}
		return strings.Repeat("*", len(id))
	}
	return id[:4] + strings.Repeat("*", len(id)-8) + id[len(id)-4:]
}

// ========== 支付宝信息比对认证单（provider=alipay, auth_mode=info） ==========

const (
	alipayInfoVerifyIndexPrefix = "rn_verify_"   // system_configs 键前缀：verify_id -> 认证单 token
	alipayInfoSessionTTL        = 30 * time.Minute
)

// startAlipayInfoRealname 信息比对模式发起认证：preconsult 录入 + 生成授权链接。
// 认证单复用 kt_face_<token> 存储；rn_verify_<verify_id> 作为回调反查索引。
func startAlipayInfoRealname(c *gin.Context, db *sql.DB, cfg realnameConfig, ownerType string, ownerID uint, outerOrderNo string, req userRealnameInitRequest) {
	preResp, err := alipayPreconsult(cfg, req.RealName, req.IDCard)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "录入实名信息失败：" + err.Error()})
		return
	}

	token, err := newRealnameFaceToken()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
		return
	}
	callbackURL := buildRequestURL(c, alipayCallbackPath)
	session := realnameFaceSession{
		Provider:  realnameProviderAlipay,
		AuthMode:  alipayAuthModeInfo,
		OwnerType: ownerType,
		UserID:    int64(ownerID),
		RealName:  req.RealName,
		IDCard:    req.IDCard,
		Status:    realnameFaceStatusPending,
		OrderNo:   outerOrderNo,
		VerifyID:  preResp.VerifyID,
		ExpireAt:  time.Now().Add(alipayInfoSessionTTL),
	}
	if _, err := db.Exec(`
		INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
		VALUES (?, ?, ?, '支付宝信息比对认证单')
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`, realnameGroup, "kt_face_"+token, session.encode()); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
		return
	}
	if _, err := db.Exec(`
		INSERT INTO system_configs (`+"`group`"+`, `+"`key`"+`, value, description)
		VALUES (?, ?, ?, '支付宝信息比对回调索引')
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`, realnameGroup, alipayInfoVerifyIndexPrefix+preResp.VerifyID, token); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "创建认证单失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "请使用支付宝扫码或唤起支付宝完成授权",
		"data": gin.H{
			"status":     realnameFaceStatusPending,
			"provider":   realnameProviderAlipay,
			"authMode":   alipayAuthModeInfo,
			"faceToken":  token,
			"certifyUrl": alipayInfoAuthorizeURL(cfg, preResp.VerifyID, callbackURL),
		},
	})
}

// AlipayRealnameCallback 支付宝授权回调（公开接口）：
// 授权成功携带 auth_code，拒绝授权时缺失。凭 auth_code 换令牌后完成信息比对。
func AlipayRealnameCallback(c *gin.Context) {
	verifyID := strings.TrimSpace(c.Query("cert_verify_id"))
	authCode := strings.TrimSpace(c.Query("auth_code"))
	html := func(body string) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(body))
	}
	if verifyID == "" {
		html(realnameFaceErrorHTML("回调参数缺失，请返回重新发起认证"))
		return
	}
	// 同一 verify_id 的并发回调串行处理，避免重复调用 consult 与重复留档。
	unlock := lockAlipayInfoCallback(verifyID)
	defer unlock()

	db, err := config.DB()
	if err != nil {
		html(realnameFaceErrorHTML("数据库连接失败"))
		return
	}
	if err := ensureSystemConfigStorage(db); err != nil {
		html(realnameFaceErrorHTML("初始化配置失败"))
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		html(realnameFaceErrorHTML("初始化实名存储失败"))
		return
	}

	var token string
	if err := db.QueryRow(
		"SELECT value FROM system_configs WHERE `group` = ? AND `key` = ?",
		realnameGroup, alipayInfoVerifyIndexPrefix+verifyID,
	).Scan(&token); err != nil {
		html(realnameFaceErrorHTML("认证单不存在或已完成，请重新发起认证"))
		return
	}
	session, err := loadRealnameFaceSession(db, token)
	if err != nil {
		html(realnameFaceErrorHTML(err.Error()))
		return
	}
	if session.Provider != realnameProviderAlipay || session.VerifyID != verifyID {
		html(realnameFaceErrorHTML("认证单号不匹配，请重新发起认证"))
		return
	}

	// 用户拒绝授权或授权中断：会话保持 pending，允许重新扫码/唤起。
	if authCode == "" {
		html(realnameFaceResultHTML("✕", "授权未完成", "您取消了实名信息授权，请返回重新发起认证", false))
		return
	}

	cfg, err := loadRealnameConfig(db)
	if err != nil || !cfg.Enabled || cfg.Provider != realnameProviderAlipay {
		html(realnameFaceErrorHTML("实名认证服务不可用，请联系管理员"))
		return
	}

	accessToken, err := alipayOauthToken(cfg, authCode)
	if err != nil {
		html(realnameFaceErrorHTML("支付宝授权失败：" + htmlEscapeString(err.Error())))
		return
	}
	consultResp, err := alipayConsult(cfg, verifyID, accessToken)
	if err != nil {
		html(realnameFaceErrorHTML("实名信息比对失败：" + htmlEscapeString(err.Error())))
		return
	}

	if _, err := db.Exec("DELETE FROM system_configs WHERE `group` = ? AND `key` = ?",
		realnameGroup, alipayInfoVerifyIndexPrefix+verifyID); err != nil {
		// 索引删除失败时保留会话 pending，用户重新授权后仍可回收结果
		html(realnameFaceErrorHTML("认证结果保存失败，请稍后重新发起"))
		return
	}

	if consultResp.Passed != "T" {
		reason := strings.TrimSpace(consultResp.FailReason)
		if reason == "" {
			reason = "支付宝实名信息与提交信息不一致"
		}
		session.Status = realnameFaceStatusFailed
		session.FailMsg = reason
		saveRealnameFaceSession(db, token, session)
		writeRealnameRecord(db, session.OwnerType, session.UserID, realnameProviderAlipay,
			session.RealName, session.IDCard, realnameFaceStatusFailed, reason, session.VerifyID, "")
		html(realnameFaceResultHTML("✕", "认证未通过", htmlEscapeString(reason)+"，请返回重新发起认证", false))
		return
	}

	session.Status = realnameFaceStatusPassed
	session.FailMsg = ""
	saveRealnameFaceSession(db, token, session)
	if err := markRealnameVerified(db, session.OwnerType, session.UserID, realnameProviderAlipay,
		session.RealName, session.IDCard, cfg, session.VerifyID, ""); err != nil {
		session.Status = realnameFaceStatusPending
		saveRealnameFaceSession(db, token, session)
		html(realnameFaceErrorHTML("认证结果保存失败，请返回重新发起认证"))
		return
	}
	html(realnameFaceResultHTML("✓", "实名认证成功", "请返回网站继续操作", true))
}

// alipayInfoQuery 信息比对模式轮询：结果由回调写入，这里只回放本地会话状态。
func alipayInfoQuery(c *gin.Context, db *sql.DB, ownerType string, ownerID uint) {
	token := strings.TrimSpace(c.Query("faceToken"))
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "缺少 faceToken"})
		return
	}
	session, err := loadRealnameFaceSession(db, token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	if session.OwnerType != ownerType || session.UserID != int64(ownerID) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "认证单号不匹配"})
		return
	}
	switch session.Status {
	case realnameFaceStatusPassed:
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "实名认证成功", "data": realnameFacePassedData(session)})
	case realnameFaceStatusFailed, realnameFaceStatusUnknown:
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未通过", "data": realnameFaceFailureData(session)})
	default:
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未完成", "data": gin.H{"status": realnameFaceStatusPending}})
	}
}

// ========== 快瞳 / 靓仔聚合认证扫码拍照认证单 ==========

// 认证单存储在 system_configs（group=realname, key=kt_face_<token>），value 格式：
// uid|realName|idCard|status|failReason|expiresAtUnix
// status: pending(待拍照) / processing(核验中) / passed / failed

const (
	realnameFaceStatusPending    = "pending"
	realnameFaceStatusProcessing = "processing"
	realnameFaceStatusPassed     = "passed"
	realnameFaceStatusFailed     = "failed"
	realnameFaceStatusUnknown    = "unknown"
	realnameFaceSessionTTL       = 10 * time.Minute
	realnameFaceTokenLen         = 32
	realnameFaceImageMaxBytes    = 2 * 1024 * 1024 // 拍照图片大小上限 2MB
	realnameFaceMaxAttempts      = 3               // 单个认证单允许的拍照核验次数
)

var realnameFaceAttempts sync.Map // token -> 已尝试次数（进程级，防止单认证单被无限重试）

var alipayInfoCallbackLocks sync.Map // verify_id -> *sync.Mutex，同一授权回调串行处理

// lockAlipayInfoCallback 按 verify_id 加进程级互斥锁，返回解锁函数。
// 解锁后删除映射，避免锁对象随回调量累积；极端竞态下退化为无锁，与加锁前行为一致。
func lockAlipayInfoCallback(verifyID string) func() {
	v, _ := alipayInfoCallbackLocks.LoadOrStore(verifyID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return func() {
		mu.Unlock()
		alipayInfoCallbackLocks.Delete(verifyID)
	}
}

// 有效期内主动复核的限频：同一主体两次复核之间至少间隔 realnameReverifyInterval，
// 避免用户反复点击产生上游认证费用。进程级缓存，重启后重置可接受。
const realnameReverifyInterval = 24 * time.Hour

var (
	realnameReverifyMu       sync.Mutex
	realnameReverifyLastSeen = map[string]time.Time{}
)

// realnameReverifyAllowed 判断该主体是否允许在本轮发起有效期内复核；
// 返回 true 时同时记录本次时间戳。
func realnameReverifyAllowed(ownerType string, ownerID uint) bool {
	key := fmt.Sprintf("%s:%d", ownerType, ownerID)
	now := time.Now()

	realnameReverifyMu.Lock()
	defer realnameReverifyMu.Unlock()
	// 惰性清理：条目超过阈值时淘汰已过期的记录，避免 map 随主体数量无限增长。
	if len(realnameReverifyLastSeen) > 4096 {
		for k, last := range realnameReverifyLastSeen {
			if now.Sub(last) >= realnameReverifyInterval {
				delete(realnameReverifyLastSeen, k)
			}
		}
	}
	if last, ok := realnameReverifyLastSeen[key]; ok && now.Sub(last) < realnameReverifyInterval {
		return false
	}
	realnameReverifyLastSeen[key] = now
	return true
}

// newRealnameFaceToken 生成不可猜测的认证单 token。
func newRealnameFaceToken() (string, error) {
	buf := make([]byte, realnameFaceTokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type realnameFaceSession struct {
	Provider     string    `json:"provider"`
	OwnerType    string    `json:"ownerType"` // user / agent
	UserID       int64     `json:"ownerId"`
	RealName     string    `json:"realName"`
	IDCard       string    `json:"idCard"`
	ReturnURL    string    `json:"returnUrl,omitempty"`
	Status       string    `json:"status"`
	FailMsg      string    `json:"failMsg"`
	FailCode     string    `json:"failCode,omitempty"`
	HTTPStatus   int       `json:"httpStatus,omitempty"`
	ResponseCode int       `json:"responseCode,omitempty"`
	AuthMode     string    `json:"authMode,omitempty"`  // 小沐认证产品模式
	Mobile       string    `json:"mobile,omitempty"`    // 小沐三要素临时手机号
	OrderNo      string    `json:"orderNo,omitempty"`   // 上游订单号，用于对账与留档
	RecordID     string    `json:"recordId,omitempty"`  // 上游认证记录 ID，查询结果的 record 参数
	CertifyID    string    `json:"certifyId,omitempty"` // 靓仔人脸认证会话 ID
	VerifyID     string    `json:"verifyId,omitempty"`  // 支付宝信息比对 verify_id
	AuthURL      string    `json:"authUrl,omitempty"`   // 上游 H5 认证地址
	ExpireAt     time.Time `json:"expireAt"`
}

func parseRealnameFaceSession(raw string) (realnameFaceSession, error) {
	var session realnameFaceSession
	if json.Unmarshal([]byte(raw), &session) == nil && session.UserID > 0 && session.RealName != "" {
		if session.OwnerType != "agent" {
			session.OwnerType = "user"
		}
		if session.Provider == "" {
			session.Provider = realnameProviderKuaitong
		}
		return session, nil
	}

	parts := strings.Split(raw, "|")
	if len(parts) != 6 && len(parts) != 7 {
		return realnameFaceSession{}, errors.New("认证单数据损坏")
	}
	session = realnameFaceSession{Provider: realnameProviderKuaitong, OwnerType: "user"}
	offset := 0
	if len(parts) == 7 {
		if parts[0] == "agent" || parts[0] == "user" {
			session.OwnerType = parts[0]
		}
		offset = 1
	}
	uid, err := strconv.ParseInt(parts[offset], 10, 64)
	if err != nil {
		return realnameFaceSession{}, errors.New("认证单数据损坏")
	}
	exp, err := strconv.ParseInt(parts[offset+5], 10, 64)
	if err != nil {
		return realnameFaceSession{}, errors.New("认证单数据损坏")
	}
	session.UserID = uid
	session.RealName = parts[offset+1]
	session.IDCard = parts[offset+2]
	session.Status = parts[offset+3]
	session.FailMsg = parts[offset+4]
	session.ExpireAt = time.Unix(exp, 0)
	return session, nil
}

func (s realnameFaceSession) encode() string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func loadRealnameFaceSession(db *sql.DB, token string) (realnameFaceSession, error) {
	var raw string
	if err := db.QueryRow("SELECT value FROM system_configs WHERE `group` = ? AND `key` = ?", realnameGroup, "kt_face_"+token).Scan(&raw); err != nil {
		return realnameFaceSession{}, errors.New("认证单不存在或已过期")
	}
	session, err := parseRealnameFaceSession(raw)
	if err != nil {
		return realnameFaceSession{}, err
	}
	if time.Now().After(session.ExpireAt) {
		// 顺手清理过期认证单及其回调索引，避免 system_configs 行无限累积。
		_, _ = db.Exec("DELETE FROM system_configs WHERE `group` = ? AND `key` = ?", realnameGroup, "kt_face_"+token)
		if session.VerifyID != "" {
			_, _ = db.Exec("DELETE FROM system_configs WHERE `group` = ? AND `key` = ?", realnameGroup, alipayInfoVerifyIndexPrefix+session.VerifyID)
		}
		return realnameFaceSession{}, errors.New("认证单已过期，请重新发起")
	}
	return session, nil
}

func saveRealnameFaceSession(db *sql.DB, token string, session realnameFaceSession) {
	_, _ = db.Exec("UPDATE system_configs SET value = ? WHERE `group` = ? AND `key` = ?", session.encode(), realnameGroup, "kt_face_"+token)
}

// claimRealnameFaceSession 在事务内把待处理认证单切换为 processing，防止并发请求重复调用计费接口。
func claimRealnameFaceSession(db *sql.DB, token string) (realnameFaceSession, bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return realnameFaceSession{}, false, err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRow(
		"SELECT value FROM system_configs WHERE `group` = ? AND `key` = ? FOR UPDATE",
		realnameGroup,
		"kt_face_"+token,
	).Scan(&raw); err != nil {
		return realnameFaceSession{}, false, errors.New("认证单不存在或已过期")
	}
	session, err := parseRealnameFaceSession(raw)
	if err != nil {
		return realnameFaceSession{}, false, err
	}
	if time.Now().After(session.ExpireAt) {
		return realnameFaceSession{}, false, errors.New("认证单已过期，请重新发起")
	}
	if session.Status != realnameFaceStatusPending && session.Status != realnameFaceStatusFailed {
		return session, false, nil
	}

	session.Status = realnameFaceStatusProcessing
	session.FailMsg = ""
	session.FailCode = ""
	session.HTTPStatus = 0
	session.ResponseCode = 0
	if _, err := tx.Exec(
		"UPDATE system_configs SET value = ? WHERE `group` = ? AND `key` = ?",
		session.encode(),
		realnameGroup,
		"kt_face_"+token,
	); err != nil {
		return realnameFaceSession{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return realnameFaceSession{}, false, err
	}
	return session, true, nil
}

func claimTencentRealnameQuerySession(db *sql.DB, token string) (realnameFaceSession, bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return realnameFaceSession{}, false, err
	}
	defer tx.Rollback()

	var raw string
	if err := tx.QueryRow(
		"SELECT value FROM system_configs WHERE `group` = ? AND `key` = ? FOR UPDATE",
		realnameGroup,
		"kt_face_"+token,
	).Scan(&raw); err != nil {
		return realnameFaceSession{}, false, errors.New("认证单不存在或已过期")
	}
	session, err := parseRealnameFaceSession(raw)
	if err != nil {
		return realnameFaceSession{}, false, err
	}
	if time.Now().After(session.ExpireAt) {
		return realnameFaceSession{}, false, errors.New("认证单已过期，请重新发起")
	}
	if session.Provider != realnameProviderTencent || session.Status != realnameFaceStatusPending {
		return session, false, nil
	}
	session.Status = realnameFaceStatusProcessing
	if _, err := tx.Exec(
		"UPDATE system_configs SET value = ? WHERE `group` = ? AND `key` = ?",
		session.encode(),
		realnameGroup,
		"kt_face_"+token,
	); err != nil {
		return realnameFaceSession{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return realnameFaceSession{}, false, err
	}
	return session, true, nil
}

// realnameFaceAttemptAllowed 限制单个认证单的核验次数，避免快瞳接口被刷。
func realnameFaceAttemptAllowed(token string) bool {
	actual, _ := realnameFaceAttempts.LoadOrStore(token, 0)
	count := actual.(int)
	if count >= realnameFaceMaxAttempts {
		return false
	}
	realnameFaceAttempts.Store(token, count+1)
	return true
}

func applyTencentRealnameError(session realnameFaceSession, err error) realnameFaceSession {
	httpStatus, responseCode, businessCode := tencentRealnameErrorMetadata(err)
	session.FailCode = businessCode
	session.HTTPStatus = httpStatus
	session.ResponseCode = responseCode
	if isTencentRealnameResultUnknown(err) {
		session.Status = realnameFaceStatusUnknown
		session.FailMsg = "腾讯增强人脸服务响应中断，认证结果无法确认，请返回电脑端重新发起认证；详细错误：" + err.Error()
		return session
	}
	session.Status = realnameFaceStatusFailed
	session.FailMsg = err.Error()
	return session
}

func tencentRealnameRecordFailureReason(session realnameFaceSession, err error) string {
	details := make([]string, 0, 3)
	if session.FailCode != "" {
		details = append(details, "错误代码："+session.FailCode)
	}
	if session.ResponseCode != 0 {
		details = append(details, "接口响应码："+strconv.Itoa(session.ResponseCode))
	}
	if session.HTTPStatus != 0 {
		details = append(details, "HTTP状态："+strconv.Itoa(session.HTTPStatus))
	}
	lines := make([]string, 0, 3)
	if session.FailMsg != "" {
		lines = append(lines, session.FailMsg)
	}
	if len(details) > 0 {
		lines = append(lines, strings.Join(details, "；"))
	}
	if body := tencentRealnameErrorResponseBody(err); body != "" {
		lines = append(lines, "上游响应正文："+body)
	}
	return strings.Join(lines, "\n")
}

func realnameFacePassedData(session realnameFaceSession) gin.H {
	return gin.H{
		"status":   "passed",
		"realName": maskRealName(session.RealName),
		"idCard":   maskIDCard(session.IDCard),
	}
}

func realnameFaceFailureData(session realnameFaceSession) gin.H {
	data := gin.H{
		"status": "failed",
		"reason": session.FailMsg,
	}
	if session.FailCode != "" {
		data["errorCode"] = session.FailCode
	}
	if session.HTTPStatus != 0 {
		data["upstreamHttpStatus"] = session.HTTPStatus
	}
	if session.ResponseCode != 0 {
		data["upstreamResponseCode"] = session.ResponseCode
	}
	return data
}

// realnameFaceQuery 按主体轮询认证单状态；靓仔认证单在这里调用上游 faceQuery。
func realnameFaceQuery(c *gin.Context, db *sql.DB, cfg realnameConfig, ownerType string, ownerID uint) {
	token := strings.TrimSpace(c.Query("faceToken"))
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "缺少 faceToken"})
		return
	}
	session, err := loadRealnameFaceSession(db, token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	if session.OwnerType != ownerType || session.UserID != int64(ownerID) {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "认证单号不匹配"})
		return
	}
	if session.Provider == realnameProviderTencent && session.Status == realnameFaceStatusPending {
		if cfg.Provider != realnameProviderTencent || cfg.TencentAPIKey == "" || cfg.TencentAPISecret == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "靓仔聚合认证服务不可用"})
			return
		}
		// 靓仔使用本站拍照流程时，不生成 certifyId，认证结果在提交时已写入 session.Status，
		// 无需调用上游查询接口，直接返回本地状态即可。
		if session.CertifyID == "" {
			// 本站拍照流程，等待用户扫码提交人脸图片
		} else {
			claimedSession, claimed, claimErr := claimTencentRealnameQuerySession(db, token)
			if claimErr != nil {
				c.JSON(http.StatusOK, gin.H{"code": 400, "msg": claimErr.Error()})
				return
			}
			if !claimed {
				session = claimedSession
			} else {
				session = claimedSession
				fmt.Printf("[靓仔认证] 开始查询认证结果\n")
				result, pending, queryErr := tencentRealnameQuery(cfg, session.CertifyID)
				fmt.Printf("[靓仔认证] 查询结果: pending=%v, passed=%v, reason=%s, err=%v\n", pending, result.Passed, result.Reason, queryErr)
				switch {
				case queryErr != nil:
					if isTencentRealnameResultUnknown(queryErr) {
						session.Status = realnameFaceStatusPending
						saveRealnameFaceSession(db, token, session)
						c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证结果查询中", "data": gin.H{"status": realnameFaceStatusPending}})
						return
					}
					session = applyTencentRealnameError(session, queryErr)
					session.Status = realnameFaceStatusFailed
					saveRealnameFaceSession(db, token, session)
					writeRealnameRecord(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, realnameFaceStatusFailed, tencentRealnameRecordFailureReason(session, queryErr), session.CertifyID, "")
				case pending:
					session.Status = realnameFaceStatusPending
					saveRealnameFaceSession(db, token, session)
					c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未完成", "data": gin.H{"status": realnameFaceStatusPending}})
					return
				case !result.Passed:
					session.Status = realnameFaceStatusFailed
					session.FailMsg = result.Reason
					saveRealnameFaceSession(db, token, session)
					recordReason := result.Detail
					if recordReason == "" {
						recordReason = result.Reason
					}
					writeRealnameRecord(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, realnameFaceStatusFailed, recordReason, session.CertifyID, result.Score)
				default:
					if err := markRealnameVerified(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, cfg, session.CertifyID, result.Score); err != nil {
						c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存实名认证结果失败，请稍后重试"})
						return
					}
					session.Status = realnameFaceStatusPassed
					saveRealnameFaceSession(db, token, session)
				}
			}
		}
	}
	switch session.Status {
	case realnameFaceStatusPassed:
		c.JSON(http.StatusOK, gin.H{
			"code": 200,
			"msg":  "实名认证成功",
			"data": realnameFacePassedData(session),
		})
	case realnameFaceStatusFailed, realnameFaceStatusUnknown:
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未通过", "data": realnameFaceFailureData(session)})
	default:
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证未完成", "data": gin.H{"status": "pending"}})
	}
}

// RealnameFacePage 扫码落地页：返回无感拍照 HTML（无需登录，token 即凭证）。
func RealnameFacePage(c *gin.Context) {
	token := strings.TrimSpace(c.Query("t"))
	if token == "" {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML("链接无效，请重新扫码")))
		return
	}

	db, err := config.DB()
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML("数据库连接失败")))
		return
	}
	if err := ensureSystemConfigStorage(db); err != nil {
		c.Data(http.StatusInternalServerError, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML("初始化配置失败")))
		return
	}

	session, err := loadRealnameFaceSession(db, token)
	if err != nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML(err.Error())))
		return
	}
	if session.Status == realnameFaceStatusPassed {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(realnameFaceResultHTML("✓", "实名认证成功", "请返回电脑端继续操作", true)))
		return
	}
	if session.Status == realnameFaceStatusFailed {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(realnameFaceResultHTML("✕", "认证未通过", htmlEscapeString(session.FailMsg)+"，可点击下方按钮重试", false)))
		return
	}
	if session.Status == realnameFaceStatusUnknown {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML(htmlEscapeString(session.FailMsg))))
		return
	}

	if session.Provider == realnameProviderXiaomu {
		// 小沐认证单不走本站拍照采集，避免 token 误入拍照链路。
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(realnameFaceErrorHTML("该认证单无需拍照，请返回原页面继续认证")))
		return
	}

	page := strings.ReplaceAll(realnameFacePageHTML, "__TOKEN__", htmlEscapeString(token))
	page = strings.ReplaceAll(page, "__NAME__", htmlEscapeString(session.RealName))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

type realnameFaceSubmitRequest struct {
	Token     string `json:"token"`
	ImageData string `json:"imageData"`
}

// RealnameFaceSubmit 拍照页提交抓拍照片，后端调用认证单对应服务商完成核验。
func RealnameFaceSubmit(c *gin.Context) {
	var req realnameFaceSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.ImageData = strings.TrimSpace(req.ImageData)
	if req.Token == "" || req.ImageData == "" {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "参数错误"})
		return
	}

	// 校验图片：必须是 data:image/...;base64, 前缀且解码后不超过 2MB
	if !strings.HasPrefix(req.ImageData, "data:image/") {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "图片格式不正确"})
		return
	}
	commaIdx := strings.Index(req.ImageData, ";base64,")
	if commaIdx < 0 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "图片格式不正确"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.ImageData[commaIdx+8:])
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "图片数据无效"})
		return
	}
	if len(raw) > realnameFaceImageMaxBytes {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "图片超过 2MB 限制，请重试"})
		return
	}

	db, err := config.DB()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "数据库连接失败"})
		return
	}
	if err := ensureSystemConfigStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化配置失败"})
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "初始化实名存储失败"})
		return
	}

	session, err := loadRealnameFaceSession(db, req.Token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	if session.Status == realnameFaceStatusPassed {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "实名认证成功", "data": gin.H{"status": "passed"}})
		return
	}
	if session.Status == realnameFaceStatusProcessing {
		c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "核验中，请稍候", "data": gin.H{"status": "pending"}})
		return
	}
	if session.Status == realnameFaceStatusUnknown {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": session.FailMsg, "data": realnameFaceFailureData(session)})
		return
	}
	rnCfg, err := loadRealnameConfig(db)
	if err != nil || !rnCfg.Enabled || (session.Provider != realnameProviderKuaitong && session.Provider != realnameProviderTencent) || rnCfg.Provider != session.Provider {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名认证服务不可用，请联系管理员"})
		return
	}

	claimedSession, claimed, err := claimRealnameFaceSession(db, req.Token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	if !claimed {
		if claimedSession.Status == realnameFaceStatusPassed {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "实名认证成功", "data": gin.H{"status": "passed"}})
		} else {
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "核验中，请稍候", "data": gin.H{"status": "pending"}})
		}
		return
	}
	session = claimedSession
	if !realnameFaceAttemptAllowed(req.Token) {
		session.Status = realnameFaceStatusFailed
		session.FailMsg = "重试次数过多，请返回电脑端重新发起认证"
		saveRealnameFaceSession(db, req.Token, session)
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": session.FailMsg})
		return
	}

	var message, serialNo, score string
	var verr error
	var faceResult tencentRealnameFaceResult

	if session.Provider == realnameProviderKuaitong {
		message, serialNo, score, verr = kuaitongVerifyFace(rnCfg, session.RealName, session.IDCard, req.ImageData)
		faceResult = tencentRealnameFaceResult{
			Passed:   message == "",
			Reason:   message,
			Detail:   message,
			SerialNo: serialNo,
			Score:    score,
		}
	} else if session.Provider == realnameProviderTencent {
		faceResult, verr = tencentRealnameVerifyWithImage(rnCfg, session.RealName, session.IDCard, req.ImageData)
		message = faceResult.Reason
		serialNo = faceResult.SerialNo
		score = faceResult.Score
	}

	switch {
	case verr != nil:
		// 请求级失败（网络/网关异常）未产生确定结果，保留原有重试行为。
		session.Status = realnameFaceStatusPending
		saveRealnameFaceSession(db, req.Token, session)
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "核验请求失败：" + verr.Error() + "，请重试"})
		return
	case !faceResult.Passed:
		session.Status = realnameFaceStatusFailed
		session.FailMsg = faceResult.Reason
		saveRealnameFaceSession(db, req.Token, session)
		recordReason := faceResult.Detail
		if recordReason == "" {
			recordReason = faceResult.Reason
		}
		writeRealnameRecord(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, realnameFaceStatusFailed, recordReason, serialNo, score)
		c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名核验未通过：" + faceResult.Reason})
		return
	}

	// 靓仔认证通过后，如果返回了 certify_id，先查询最终结果再返回
	if session.Provider == realnameProviderTencent && faceResult.CertifyID != "" {
		fmt.Printf("[靓仔认证] 核验接口返回成功，开始查询最终结果\n")
		queryResult, pending, queryErr := tencentRealnameQuery(rnCfg, faceResult.CertifyID)
		fmt.Printf("[靓仔认证] 认证结果查询返回: pending=%v, passed=%v, reason=%s, err=%v\n", pending, queryResult.Passed, queryResult.Reason, queryErr)

		// 处理查询结果
		switch {
		case queryErr != nil:
			// 查询失败，按原结果处理但记录查询错误
			fmt.Printf("[靓仔认证] 查询失败: %v，使用核验接口结果\n", queryErr)
		case pending:
			// 仍在处理中，返回 pending 状态
			session.Status = realnameFaceStatusPending
			session.CertifyID = faceResult.CertifyID
			saveRealnameFaceSession(db, req.Token, session)
			c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "认证结果查询中，请稍候", "data": gin.H{"status": "pending"}})
			return
		case !queryResult.Passed:
			// 查询结果为不通过
			session.Status = realnameFaceStatusFailed
			session.FailMsg = queryResult.Reason
			saveRealnameFaceSession(db, req.Token, session)
			queryRecordReason := queryResult.Detail
			if queryRecordReason == "" {
				queryRecordReason = queryResult.Reason
			}
			writeRealnameRecord(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, realnameFaceStatusFailed, queryRecordReason, queryResult.SerialNo, queryResult.Score)
			c.JSON(http.StatusOK, gin.H{"code": 400, "msg": "实名核验未通过：" + queryResult.Reason})
			return
		default:
			// 查询结果为通过，更新 serialNo 和 score
			if queryResult.SerialNo != "" {
				serialNo = queryResult.SerialNo
			}
			if queryResult.Score != "" {
				score = queryResult.Score
			}
		}
	}

	if err := markRealnameVerified(db, session.OwnerType, session.UserID, session.Provider, session.RealName, session.IDCard, rnCfg, serialNo, score); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 500, "msg": "保存实名认证结果失败，请稍后重试"})
		return
	}
	session.Status = realnameFaceStatusPassed
	saveRealnameFaceSession(db, req.Token, session)
	realnameFaceAttempts.Delete(req.Token)
	c.JSON(http.StatusOK, gin.H{"code": 200, "msg": "实名认证成功", "data": gin.H{"status": "passed"}})
}

func htmlEscapeString(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}

const realnameFacePageStyle = `
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, "PingFang SC", "Helvetica Neue", sans-serif; background: #0f172a; color: #e2e8f0; min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 24px; }
.wrap { width: 100%; max-width: 420px; text-align: center; }
h1 { font-size: 20px; font-weight: 600; margin-bottom: 8px; }
.tip { font-size: 13px; color: #94a3b8; margin-bottom: 20px; line-height: 1.6; }
.camera-box { position: relative; width: 100%; border-radius: 16px; overflow: hidden; background: #1e293b; aspect-ratio: 3/4; }
video { width: 100%; height: 100%; object-fit: cover; transform: scaleX(-1); }
.frame { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; pointer-events: none; }
.frame::before { content: ""; width: 62%; aspect-ratio: 3/4; border: 3px solid rgba(34, 211, 238, .85); border-radius: 50%; box-shadow: 0 0 0 999px rgba(2, 6, 23, .35); }
.status { margin-top: 18px; font-size: 14px; color: #67e8f9; min-height: 22px; }
.btn { display: inline-block; margin-top: 16px; padding: 12px 40px; border: none; border-radius: 999px; background: linear-gradient(135deg, #06b6d4, #3b82f6); color: #fff; font-size: 15px; cursor: pointer; }
.btn:disabled { opacity: .5; }
.result-icon { font-size: 56px; margin-bottom: 16px; }
.result-icon.ok { color: #34d399; }
.result-icon.bad { color: #f87171; }
.result-title { font-size: 20px; font-weight: 600; margin-bottom: 10px; }
.result-desc { font-size: 14px; color: #94a3b8; line-height: 1.7; }
`

func realnameFaceErrorHTML(msg string) string {
	return "<!DOCTYPE html><html lang=" + `"zh-CN"` + "><head><meta charset=" + `"UTF-8"` + "><meta name=" + `"viewport"` + " content=" + `"width=device-width,initial-scale=1"` + "><title>实名认证</title><style>" + realnameFacePageStyle + "</style></head><body><div class=" + `"wrap"` + "><div class=" + `"result-icon bad"` + ">✕</div><div class=" + `"result-title"` + ">无法认证</div><div class=" + `"result-desc"` + ">" + msg + "</div></div></body></html>"
}

func realnameFaceResultHTML(icon, title, desc string, ok bool) string {
	cls := "ok"
	if !ok {
		cls = "bad"
	}
	retry := ""
	if !ok {
		retry = "<button class=" + `"btn"` + " onclick=" + `"location.reload()"` + ">重新拍照</button>"
	}
	return "<!DOCTYPE html><html lang=" + `"zh-CN"` + "><head><meta charset=" + `"UTF-8"` + "><meta name=" + `"viewport"` + " content=" + `"width=device-width,initial-scale=1"` + "><title>实名认证</title><style>" + realnameFacePageStyle + "</style></head><body><div class=" + `"wrap"` + "><div class=" + `"result-icon ` + cls + `"` + ">" + icon + "</div><div class=" + `"result-title"` + ">" + title + "</div><div class=" + `"result-desc"` + ">" + desc + "</div>" + retry + "</div></body></html>"
}

// 无感拍照页：自动申请摄像头，画面稳定后自动抓拍上传，全程无需用户点击。
const realnameFacePageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no">
<title>人脸核验</title>
<style>` + realnameFacePageStyle + `</style>
</head>
<body>
<div class="wrap">
  <h1>人脸核验</h1>
  <div class="tip">正在核验 <b>__NAME__</b> 的实名信息<br>请正对屏幕，保持光线充足，将面部置于框内</div>
  <div class="camera-box">
    <video id="video" autoplay playsinline muted></video>
    <div class="frame"></div>
  </div>
  <div class="status" id="status">正在启动摄像头...</div>
</div>
<canvas id="canvas" style="display:none"></canvas>
<script>
(function () {
  var TOKEN = "__TOKEN__";
  var video = document.getElementById("video");
  var canvas = document.getElementById("canvas");
  var statusEl = document.getElementById("status");
  var submitted = false;

  function setStatus(text) { statusEl.textContent = text; }

  function escapeHTML(value) {
    var chars = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
    return String(value).replace(/[&<>"']/g, function (ch) { return chars[ch]; });
  }

  function showResult(ok, title, desc, retry) {
    var html = '<div class="wrap"><div class="result-icon ' + (ok ? "ok" : "bad") + '">' + (ok ? "✓" : "✕") + '</div>' +
      '<div class="result-title">' + escapeHTML(title) + '</div><div class="result-desc">' + escapeHTML(desc) + '</div>';
    if (retry) html += '<button class="btn" onclick="location.reload()">重新拍照</button>';
    html += '</div>';
    document.body.innerHTML = html;
  }

  function imageByteLength(imageData) {
    var encoded = imageData.slice(imageData.indexOf(",") + 1);
    var padding = encoded.slice(-2) === "==" ? 2 : (encoded.slice(-1) === "=" ? 1 : 0);
    return Math.floor(encoded.length * 3 / 4) - padding;
  }

  function capture() {
    var w = video.videoWidth, h = video.videoHeight;
    if (!w || !h) return null;
    var presets = [[640, 0.76], [560, 0.7], [480, 0.64]];
    for (var i = 0; i < presets.length; i++) {
      var maxW = presets[i][0];
      var scale = w > maxW ? maxW / w : 1;
      canvas.width = Math.round(w * scale);
      canvas.height = Math.round(h * scale);
      var ctx = canvas.getContext("2d");
      ctx.translate(canvas.width, 0);
      ctx.scale(-1, 1);
      ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
      var image = canvas.toDataURL("image/jpeg", presets[i][1]);
      if (imageByteLength(image) <= 500 * 1024) return image;
    }
    return null;
  }

  function submit(imageData) {
    if (submitted) return;
    submitted = true;
    setStatus("正在核验，请稍候...");
    fetch("/api/realname/face/submit", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: TOKEN, imageData: imageData })
    }).then(function (r) { return r.json(); }).then(function (res) {
      if (res.code === 200) {
        showResult(true, "实名认证成功", "请返回电脑端继续操作", false);
      } else if (res.code === 500) {
        submitted = false;
        setStatus(res.msg || "核验失败，正在重试...");
        setTimeout(loop, 2000);
      } else {
        showResult(false, "认证未通过", res.msg || "请返回电脑端重新发起认证", true);
      }
    }).catch(function () {
      submitted = false;
      setStatus("网络异常，正在重试...");
      setTimeout(loop, 2000);
    });
  }

  function loop() {
    if (submitted) return;
    var image = capture();
    if (image && imageByteLength(image) <= 500 * 1024) {
      submit(image);
    } else {
      setTimeout(loop, 600);
    }
  }

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    setStatus("当前浏览器不支持摄像头，请使用微信/支付宝内置浏览器或系统相机扫码");
    return;
  }
  navigator.mediaDevices.getUserMedia({ video: { facingMode: "user", width: { ideal: 720 } }, audio: false })
    .then(function (stream) {
      video.srcObject = stream;
      setStatus("请正对屏幕，正在准备抓拍...");
      setTimeout(function () { setStatus("正在抓拍..."); loop(); }, 1600);
    })
    .catch(function () {
      setStatus("摄像头权限被拒绝，请在浏览器设置中允许后刷新重试");
    });
})();
</script>
</body>
</html>`

// realnameCertifyIDExpired 保留给将来清理 pending 单使用。
var _ = realnameCertifyIDMaxKeep
