package handler

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"time"

	"auto_pro/config"
)

// ========== 实名到期提醒 ==========
//
// 独立于授权到期提醒：实名的有效期挂在 users / agents 主体上，与授权无关，
// 且提醒文案与去重口径都不同，因此单独一个 worker 与一套模板。
//
// 触发条件：开启实名认证、设置了有效期（validity_days > 0）、配置了提醒天数，
// 且主体已进入提醒窗口（到期日 ≤ 今天 + remind_days）。窗口用 ≤ 而不是精确等于，
// 这样调大 remind_days 后已在窗口内的主体也能在下一次扫描被覆盖，不会漏提醒。
// 幂等：event_key = realname_expire:<主体类型>:<主体ID>:<到期日>:<提前天数>，
// 同一有效期只提醒一次；主体重新认证产生新的到期日后可再次提醒。

const realnameExpireEventType = "realname_expire"

var realnameExpiryWorkerOnce sync.Once

// StartRealnameExpiryWorker 周期扫描实名即将到期的主体并发送提醒。
func StartRealnameExpiryWorker() {
	realnameExpiryWorkerOnce.Do(func() {
		go func() {
			time.Sleep(45 * time.Second)
			runRealnameExpiryScan()
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				runRealnameExpiryScan()
			}
		}()
	})
}

func runRealnameExpiryScan() {
	db, err := config.DB()
	if err != nil {
		return
	}
	if err := ensureRealnameStorage(db); err != nil {
		return
	}
	cfg, err := loadRealnameConfig(db)
	if err != nil || !cfg.Enabled || cfg.ValidityDays <= 0 || cfg.RemindDays <= 0 {
		return
	}

	mailCfg, err := loadMailConfig(db, true)
	if err != nil || !mailCfg.EnabledRealnameExpire {
		return
	}
	if err := ensureMailStorage(db); err != nil {
		return
	}

	for _, ownerType := range []string{"user", "agent"} {
		scanRealnameExpiryByOwner(db, mailCfg, cfg, ownerType)
	}
}

func scanRealnameExpiryByOwner(db *sql.DB, mailCfg mailConfig, cfg realnameConfig, ownerType string) {
	table := realnameOwnerTable(ownerType)
	emailColumn := "email"
	nameColumn := "nickname"
	if ownerType == "agent" {
		nameColumn = "name"
	}

	// NOT EXISTS 排除已按当前提醒设置发送过的主体（幂等键口径与 sendRealnameExpireMail 一致），
	// 让 LIMIT 200 只覆盖未提醒行，避免同一批已提醒主体反复占满扫描配额导致后续主体漏提醒。
	rows, err := db.Query(`
		SELECT id, COALESCE(`+emailColumn+`, ''), COALESCE(NULLIF(`+nameColumn+`, ''), `+emailColumn+`, ''),
		       COALESCE(real_name, ''), realname_expires_at
		FROM `+table+`
		WHERE realname_expires_at IS NOT NULL
		  AND realname_expires_at > NOW()
		  AND DATE(realname_expires_at) <= DATE(DATE_ADD(NOW(), INTERVAL ? DAY))
		  AND NOT EXISTS (
			SELECT 1 FROM mail_send_logs m
			WHERE m.event_key = CONCAT(?, ':', ?, ':', `+table+`.id, ':', DATE_FORMAT(`+table+`.realname_expires_at, '%Y%m%d'), ':', ?)
		  )
		ORDER BY realname_expires_at ASC
		LIMIT 200
	`, cfg.RemindDays, realnameExpireEventType, ownerType, cfg.RemindDays)
	if err != nil {
		return
	}
	defer rows.Close()

	type realnameExpiryTarget struct {
		id        int64
		email     string
		name      string
		realName  string
		expiresAt time.Time
	}
	targets := make([]realnameExpiryTarget, 0)
	for rows.Next() {
		var target realnameExpiryTarget
		var expiresAt sql.NullTime
		if rows.Scan(&target.id, &target.email, &target.name, &target.realName, &expiresAt) != nil || !expiresAt.Valid {
			continue
		}
		target.expiresAt = expiresAt.Time
		targets = append(targets, target)
	}

	for _, target := range targets {
		sendRealnameExpireMail(db, mailCfg, cfg, ownerType, target.id, target.email, target.name, target.realName, target.expiresAt)
	}
}

// sendRealnameExpireMail 走与授权提醒相同的日志与 SMTP 通道，但使用独立的模板与幂等键。
func sendRealnameExpireMail(db *sql.DB, mailCfg mailConfig, cfg realnameConfig, ownerType string, ownerID int64, recipient, ownerName, realName string, expiresAt time.Time) {
	daysLeft := int(time.Until(expiresAt).Hours() / 24)
	if daysLeft < 0 {
		daysLeft = 0
	}

	// 幂等键含到期日，重新认证产生新有效期后可再次提醒。
	eventKey := strings.Join([]string{
		realnameExpireEventType,
		ownerType,
		strconv.FormatInt(ownerID, 10),
		expiresAt.Format("20060102"),
		strconv.Itoa(cfg.RemindDays),
	}, ":")

	if strings.TrimSpace(recipient) == "" {
		logID, inserted := createMailLogWithKey(db, realnameExpireEventType, ownerType, ownerID, "", "", "", cfg.RemindDays, eventKey)
		if inserted {
			markMailLogSkipped(db, logID, "收件邮箱为空")
		}
		return
	}

	vars := map[string]string{
		"{{ownerName}}": firstNonEmpty(ownerName, recipient),
		"{{realName}}":  realName,
		"{{expiresAt}}": expiresAt.Format("2006-01-02 15:04:05"),
		"{{daysLeft}}":  strconv.Itoa(daysLeft),
		"{{siteName}}":  loadSiteNameForMail(db),
	}
	subject := renderMailTemplate(mailCfg.RealnameExpireSubject, vars)
	content := renderMailTemplate(mailCfg.RealnameExpireContent, vars)

	logID, inserted := createMailLogWithKey(db, realnameExpireEventType, ownerType, ownerID, recipient, subject, content, cfg.RemindDays, eventKey)
	if !inserted {
		return
	}
	if err := sendSMTPMail(mailCfg, mailMessage{To: recipient, Subject: subject, Content: content, ContentType: mailCfg.RealnameExpireContentType}); err != nil {
		markMailLogFailed(db, logID, err.Error())
		return
	}
	markMailLogSent(db, logID)
}
