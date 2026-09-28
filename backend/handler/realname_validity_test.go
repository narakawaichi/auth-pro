package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 本文件覆盖实名有效期、范围判定与购买门槛。
// 使用按 SQL 形态应答的内存驱动：loadRealnameConfig 会连带读取 plugins 表与执行建表语句，
// 手工编排 SQL 顺序过于脆弱，因此这里按语义回答。

// realnameGateTestState 保存一次测试内的配置与主体实名数据。
type realnameGateTestState struct {
	mu sync.Mutex
	// configs 为 system_configs 表中 group=realname 的键值。
	configs map[string]string
	// subjects 按表名返回主体实名行，缺失表示该主体从未实名。
	subjects map[string]realnameSubjectRow
	// configErr 非空时配置查询失败，用于覆盖 fail-open。
	configErr error
	// subjectErr 非空时主体查询失败。
	subjectErr error
	// pluginEnabled 为实名服务商插件开关，nil 视为启用。
	pluginEnabled *bool
	// licenseOwner 为 licenses 表返回的归属，nil 表示查不到该授权。
	licenseOwner *realnameLicenseOwner
	// queriedSubjects 记录实际查询过的主体表，用于断言是否校验了目标用户。
	queriedSubjects []string
}

// realnameLicenseOwner 表示授权归属主体。
type realnameLicenseOwner struct {
	ownerType string
	ownerID   int64
}

type realnameSubjectRow struct {
	at      interface{}
	expires interface{}
}

func (s *realnameGateTestState) markQueried(table string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queriedSubjects = append(s.queriedSubjects, table)
}

func (s *realnameGateTestState) subjectTables() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queriedSubjects...)
}

func (s *realnameGateTestState) pluginOn() bool {
	if s.pluginEnabled == nil {
		return true
	}
	return *s.pluginEnabled
}

type realnameGateTestDriver struct{ state *realnameGateTestState }

type realnameGateTestConn struct{ state *realnameGateTestState }

type realnameGateTestTx struct{}

type realnameGateTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

type realnameGateTestResult struct{ rowsAffected int64 }

func (d realnameGateTestDriver) Open(string) (driver.Conn, error) {
	return realnameGateTestConn{state: d.state}, nil
}

func (c realnameGateTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by realname gate test driver")
}

func (c realnameGateTestConn) Close() error { return nil }

func (c realnameGateTestConn) Begin() (driver.Tx, error) { return realnameGateTestTx{}, nil }

func (realnameGateTestTx) Commit() error   { return nil }
func (realnameGateTestTx) Rollback() error { return nil }

func (c realnameGateTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	compact := compactSQL(query)
	switch {
	case strings.Contains(compact, "information_schema.COLUMNS"),
		strings.Contains(compact, "information_schema.STATISTICS"):
		return &realnameGateTestRows{columns: []string{"count"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(compact, "SELECT COALESCE(MIN(created_at), NOW()) FROM admins"):
		return &realnameGateTestRows{columns: []string{"installed_at"}, values: [][]driver.Value{{time.Now()}}}, nil
	case strings.Contains(compact, "SELECT enabled FROM plugins"):
		if !c.state.pluginOn() {
			return &realnameGateTestRows{columns: []string{"enabled"}, values: [][]driver.Value{{int64(0)}}}, nil
		}
		return &realnameGateTestRows{columns: []string{"enabled"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(compact, "FROM system_configs"):
		if c.state.configErr != nil {
			return nil, c.state.configErr
		}
		rows := make([][]driver.Value, 0, len(c.state.configs))
		for key, value := range c.state.configs {
			rows = append(rows, []driver.Value{key, value})
		}
		return &realnameGateTestRows{columns: []string{"key", "value"}, values: rows}, nil
	case strings.Contains(compact, "FROM users") && strings.Contains(compact, "realname_at"):
		c.state.markQueried("users")
		if c.state.subjectErr != nil {
			return nil, c.state.subjectErr
		}
		return c.subjectRows("users"), nil
	case strings.Contains(compact, "FROM agents") && strings.Contains(compact, "realname_at"):
		c.state.markQueried("agents")
		if c.state.subjectErr != nil {
			return nil, c.state.subjectErr
		}
		return c.subjectRows("agents"), nil
	case strings.Contains(compact, "FROM licenses"):
		if c.state.licenseOwner == nil {
			return &realnameGateTestRows{columns: []string{"owner_type", "owner_id"}}, nil
		}
		return &realnameGateTestRows{
			columns: []string{"owner_type", "owner_id"},
			values:  [][]driver.Value{{c.state.licenseOwner.ownerType, c.state.licenseOwner.ownerID}},
		}, nil
	}
	return &realnameGateTestRows{columns: []string{"value"}}, nil
}

func (c realnameGateTestConn) subjectRows(table string) driver.Rows {
	row, ok := c.state.subjects[table]
	if !ok {
		return &realnameGateTestRows{columns: []string{"realname_at", "realname_expires_at"}}
	}
	return &realnameGateTestRows{
		columns: []string{"realname_at", "realname_expires_at"},
		values:  [][]driver.Value{{row.at, row.expires}},
	}
}

func (c realnameGateTestConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return realnameGateTestResult{rowsAffected: 1}, nil
}

func (r *realnameGateTestRows) Columns() []string { return r.columns }
func (r *realnameGateTestRows) Close() error      { return nil }

func (r *realnameGateTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func (r realnameGateTestResult) LastInsertId() (int64, error) { return 0, nil }
func (r realnameGateTestResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

var realnameGateTestDriverID atomic.Uint64

func openRealnameGateTestDB(t *testing.T, state *realnameGateTestState) *sql.DB {
	t.Helper()
	if state.configs == nil {
		state.configs = map[string]string{}
	}
	if state.subjects == nil {
		state.subjects = map[string]realnameSubjectRow{}
	}
	name := fmt.Sprintf("realname-gate-test-%d", realnameGateTestDriverID.Add(1))
	sql.Register(name, realnameGateTestDriver{state: state})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// enabledRealnameConfig 构造"实名已开启 + 购买前需实名 + 校验目标用户"的配置。
func enabledRealnameConfig() map[string]string {
	return map[string]string{
		"enabled":              "1",
		"purchase_required":    "1",
		"purchase_check_owner": "1",
		"grace_days":           "15",
	}
}

func TestRealnameStillValid(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		realnameAt sql.NullTime
		expiresAt  sql.NullTime
		graceDays  int
		want       bool
	}{
		{name: "从未实名", graceDays: defaultRealnameGraceDays, want: false},
		{name: "永久有效", realnameAt: sql.NullTime{Time: now.AddDate(-2, 0, 0), Valid: true}, graceDays: defaultRealnameGraceDays, want: true},
		{name: "有效期内", realnameAt: sql.NullTime{Time: now.AddDate(0, 0, -10), Valid: true}, expiresAt: sql.NullTime{Time: now.AddDate(0, 0, 20), Valid: true}, graceDays: defaultRealnameGraceDays, want: true},
		{name: "刚过期仍在宽限期", realnameAt: sql.NullTime{Time: now.AddDate(0, 0, -100), Valid: true}, expiresAt: sql.NullTime{Time: now.AddDate(0, 0, -3), Valid: true}, graceDays: defaultRealnameGraceDays, want: true},
		{name: "超出宽限期", realnameAt: sql.NullTime{Time: now.AddDate(0, 0, -100), Valid: true}, expiresAt: sql.NullTime{Time: now.AddDate(0, 0, -30), Valid: true}, graceDays: defaultRealnameGraceDays, want: false},
		{name: "宽限期为 0 时到期即失效", realnameAt: sql.NullTime{Time: now.AddDate(0, 0, -100), Valid: true}, expiresAt: sql.NullTime{Time: now.AddDate(0, 0, -1), Valid: true}, graceDays: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := realnameStillValid(tc.realnameAt, tc.expiresAt, tc.graceDays); got != tc.want {
				t.Fatalf("realnameStillValid() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRealnameGraceExpired(t *testing.T) {
	now := time.Now()
	valid := sql.NullTime{Time: now, Valid: true}
	if realnameGraceExpired(sql.NullTime{}, sql.NullTime{}, defaultRealnameGraceDays) {
		t.Fatal("从未实名不应判定为过期")
	}
	if realnameGraceExpired(valid, sql.NullTime{}, defaultRealnameGraceDays) {
		t.Fatal("永久有效不应判定为过期")
	}
	if realnameGraceExpired(valid, sql.NullTime{Time: now.AddDate(0, 0, 5), Valid: true}, defaultRealnameGraceDays) {
		t.Fatal("有效期内不应判定为过期")
	}
	if !realnameGraceExpired(valid, sql.NullTime{Time: now.AddDate(0, 0, -30), Valid: true}, defaultRealnameGraceDays) {
		t.Fatal("超出宽限期应判定为过期")
	}
}

func TestRealnameExpiresAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := realnameExpiresAt(now, 0); !got.IsZero() {
		t.Fatalf("validityDays=0 应返回零值表示永久有效，got %v", got)
	}
	if got := realnameExpiresAt(now, -1); !got.IsZero() {
		t.Fatalf("负数有效期应视为永久有效，got %v", got)
	}
	if got, want := realnameExpiresAt(now, 365), now.AddDate(0, 0, 365); !got.Equal(want) {
		t.Fatalf("realnameExpiresAt(365) = %v, want %v", got, want)
	}
}

func TestRealnameRecordTypeFor(t *testing.T) {
	if got := realnameRecordTypeFor(sql.NullTime{}); got != realnameRecordTypeInit {
		t.Fatalf("首次认证记录类型 = %q, want %q", got, realnameRecordTypeInit)
	}
	if got := realnameRecordTypeFor(sql.NullTime{Time: time.Now(), Valid: true}); got != realnameRecordTypeReview {
		t.Fatalf("重复认证记录类型 = %q, want %q", got, realnameRecordTypeReview)
	}
}

func TestValidRealnameScope(t *testing.T) {
	if !validRealnameScope(realnameScopeByApp) || !validRealnameScope(realnameScopeAll) {
		t.Fatal("by_app 与 all 都应视为合法范围")
	}
	if validRealnameScope("global") || validRealnameScope("") {
		t.Fatal("未知范围应判定为非法")
	}
}

func TestValidRealnameLegacyPolicy(t *testing.T) {
	if !validRealnameLegacyPolicy(realnameLegacyKeep) || !validRealnameLegacyPolicy(realnameLegacyBackfill) {
		t.Fatal("keep 与 backfill 都应视为合法存量策略")
	}
	if validRealnameLegacyPolicy("reset") || validRealnameLegacyPolicy("") {
		t.Fatal("未知存量策略应判定为非法")
	}
}

func TestRealnameBlockedReason(t *testing.T) {
	if got := realnameBlockedReason(realnameStatusNever); got != verifyReasonRealnameRequired {
		t.Fatalf("never 映射为 %q, want %q", got, verifyReasonRealnameRequired)
	}
	if got := realnameBlockedReason(realnameStatusExpired); got != verifyReasonRealnameExpired {
		t.Fatalf("expired 映射为 %q, want %q", got, verifyReasonRealnameExpired)
	}
	for _, status := range []string{realnameStatusOK, realnameStatusGrace} {
		if got := realnameBlockedReason(status); got != "" {
			t.Fatalf("%s 不应拦截，got %q", status, got)
		}
	}
}

func TestRealnamePendingNoticeOnlyForGrace(t *testing.T) {
	if realnamePendingNotice(realnameStatusGrace) == "" {
		t.Fatal("宽限期内应给出提示文案")
	}
	for _, status := range []string{realnameStatusOK, realnameStatusNever, realnameStatusExpired} {
		if got := realnamePendingNotice(status); got != "" {
			t.Fatalf("%s 不应给出宽限期提示，got %q", status, got)
		}
	}
}

func TestRealnameAppInScope(t *testing.T) {
	byAppCfg := realnameConfig{Scope: realnameScopeByApp, RequireAppIDs: map[int64]bool{7: true}}
	if !realnameAppInScope(byAppCfg, 7) {
		t.Fatal("按应用模式下勾选的应用应在范围内")
	}
	if realnameAppInScope(byAppCfg, 9) {
		t.Fatal("按应用模式下未勾选的应用不应在范围内")
	}

	allCfg := realnameConfig{Scope: realnameScopeAll, RequireAppIDs: map[int64]bool{}}
	for _, appID := range []int64{7, 9, 1024} {
		if !realnameAppInScope(allCfg, appID) {
			t.Fatalf("全部应用模式下应用 %d 应在范围内", appID)
		}
	}
}

func TestLoadRealnameConfigDefaultsMatchLegacyBehavior(t *testing.T) {
	// 老库没有 scope 等新配置时应回落默认值，行为与改动前一致。
	state := &realnameGateTestState{configs: map[string]string{"enabled": "1"}}
	db := openRealnameGateTestDB(t, state)
	cfg, err := loadRealnameConfig(db)
	if err != nil {
		t.Fatalf("loadRealnameConfig() error = %v", err)
	}
	if cfg.Scope != realnameScopeByApp {
		t.Fatalf("默认范围 = %q, want %q", cfg.Scope, realnameScopeByApp)
	}
	if cfg.ValidityDays != 0 {
		t.Fatalf("默认有效期 = %d, want 0（永不过期）", cfg.ValidityDays)
	}
	if cfg.GraceDays != defaultRealnameGraceDays {
		t.Fatalf("默认宽限期 = %d, want %d", cfg.GraceDays, defaultRealnameGraceDays)
	}
	if cfg.RemindDays != defaultRealnameRemindDays {
		t.Fatalf("默认提醒天数 = %d, want %d", cfg.RemindDays, defaultRealnameRemindDays)
	}
	if !cfg.AllowIdentityChange {
		t.Fatal("默认应允许更换证件，与历史行为一致")
	}
	if cfg.LegacyPolicy != realnameLegacyKeep {
		t.Fatalf("默认存量策略 = %q, want %q", cfg.LegacyPolicy, realnameLegacyKeep)
	}
	if cfg.PurchaseRequired || cfg.PurchaseCheckOwner {
		t.Fatal("购买门槛默认应保持关闭，避免升级后行为突变")
	}
}

func TestLoadRealnameConfigReadsNewKeys(t *testing.T) {
	state := &realnameGateTestState{configs: map[string]string{
		"enabled":               "1",
		"scope":                 realnameScopeAll,
		"purchase_required":     "1",
		"purchase_check_owner":  "1",
		"validity_days":         "365",
		"grace_days":            "0",
		"remind_days":           "7",
		"allow_identity_change": "0",
		"legacy_policy":         realnameLegacyBackfill,
	}}
	db := openRealnameGateTestDB(t, state)
	cfg, err := loadRealnameConfig(db)
	if err != nil {
		t.Fatalf("loadRealnameConfig() error = %v", err)
	}
	if cfg.Scope != realnameScopeAll {
		t.Fatalf("范围 = %q, want %q", cfg.Scope, realnameScopeAll)
	}
	if cfg.ValidityDays != 365 {
		t.Fatalf("有效期 = %d, want 365", cfg.ValidityDays)
	}
	if cfg.GraceDays != 0 {
		t.Fatalf("宽限期 = %d, want 0", cfg.GraceDays)
	}
	if cfg.RemindDays != 7 {
		t.Fatalf("提醒天数 = %d, want 7", cfg.RemindDays)
	}
	if cfg.AllowIdentityChange {
		t.Fatal("allow_identity_change=0 时应禁止更换证件")
	}
	if cfg.LegacyPolicy != realnameLegacyBackfill {
		t.Fatalf("存量策略 = %q, want %q", cfg.LegacyPolicy, realnameLegacyBackfill)
	}
	if !cfg.PurchaseRequired || !cfg.PurchaseCheckOwner {
		t.Fatal("购买门槛配置未生效")
	}
}

func TestLoadRealnameConfigIgnoresInvalidEnumValues(t *testing.T) {
	state := &realnameGateTestState{configs: map[string]string{
		"enabled":       "1",
		"scope":         "global",
		"legacy_policy": "reset",
	}}
	db := openRealnameGateTestDB(t, state)
	cfg, err := loadRealnameConfig(db)
	if err != nil {
		t.Fatalf("loadRealnameConfig() error = %v", err)
	}
	if cfg.Scope != realnameScopeByApp {
		t.Fatalf("非法范围应回落 by_app，got %q", cfg.Scope)
	}
	if cfg.LegacyPolicy != realnameLegacyKeep {
		t.Fatalf("非法存量策略应回落 keep，got %q", cfg.LegacyPolicy)
	}
}

func TestCheckRealnameForOwnerStatuses(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		ownerType  string
		table      string
		row        *realnameSubjectRow
		subjectErr error
		wantStatus string
		wantVerify bool
	}{
		{name: "从未实名", ownerType: "user", table: "users", wantStatus: realnameStatusNever},
		{
			name:       "永久有效",
			ownerType:  "user",
			table:      "users",
			row:        &realnameSubjectRow{at: now.AddDate(-1, 0, 0)},
			wantStatus: realnameStatusOK,
			wantVerify: true,
		},
		{
			name:       "有效期内",
			ownerType:  "agent",
			table:      "agents",
			row:        &realnameSubjectRow{at: now.AddDate(0, 0, -10), expires: now.AddDate(0, 0, 20)},
			wantStatus: realnameStatusOK,
			wantVerify: true,
		},
		{
			name:       "宽限期内放行但需提示复核",
			ownerType:  "user",
			table:      "users",
			row:        &realnameSubjectRow{at: now.AddDate(0, 0, -100), expires: now.AddDate(0, 0, -3)},
			wantStatus: realnameStatusGrace,
			wantVerify: true,
		},
		{
			name:       "超出宽限期",
			ownerType:  "user",
			table:      "users",
			row:        &realnameSubjectRow{at: now.AddDate(0, 0, -100), expires: now.AddDate(0, 0, -30)},
			wantStatus: realnameStatusExpired,
		},
		{
			name:       "主体查询失败按未实名处理",
			ownerType:  "user",
			table:      "users",
			subjectErr: errors.New("查询失败"),
			wantStatus: realnameStatusNever,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &realnameGateTestState{subjectErr: tc.subjectErr}
			if tc.row != nil {
				state.subjects = map[string]realnameSubjectRow{tc.table: *tc.row}
			}
			db := openRealnameGateTestDB(t, state)
			got := checkRealnameForOwner(db, realnameConfig{GraceDays: defaultRealnameGraceDays}, tc.ownerType, 7)
			if got.Status != tc.wantStatus || got.Verified != tc.wantVerify || !got.Required {
				t.Fatalf("checkRealnameForOwner() = %#v, want status=%s verified=%v required=true",
					got, tc.wantStatus, tc.wantVerify)
			}
		})
	}

	// 管理员直接创建的授权不受实名限制，与历史行为一致。
	state := &realnameGateTestState{}
	db := openRealnameGateTestDB(t, state)
	got := checkRealnameForOwner(db, realnameConfig{GraceDays: defaultRealnameGraceDays}, "admin", 7)
	if got.Required || !got.Verified || got.Status != realnameStatusOK {
		t.Fatalf("未知归属类型应直接放行，got %#v", got)
	}
}

// graceRow 构造"已过有效期但仍在宽限期内"的主体行。
func graceRow() realnameSubjectRow {
	return realnameSubjectRow{
		at:      time.Now().AddDate(0, 0, -100),
		expires: time.Now().AddDate(0, 0, -3),
	}
}

// expiredRow 构造"超出宽限期"的主体行。
func expiredRow() realnameSubjectRow {
	return realnameSubjectRow{
		at:      time.Now().AddDate(0, 0, -100),
		expires: time.Now().AddDate(0, 0, -30),
	}
}

func TestRealnameCheckForPurchaseBlocksNeverVerifiedPayer(t *testing.T) {
	state := &realnameGateTestState{configs: enabledRealnameConfig()}
	db := openRealnameGateTestDB(t, state)
	reason, message := realnameCheckForPurchase(db, "user", 7, "user", 7)
	if reason != purchaseReasonRealnameRequired {
		t.Fatalf("reason = %q, want %q", reason, purchaseReasonRealnameRequired)
	}
	if message == "" {
		t.Fatal("拦截时必须给出提示文案")
	}
}

func TestRealnameCheckForPurchaseBlocksExpiredPayer(t *testing.T) {
	state := &realnameGateTestState{
		configs:  enabledRealnameConfig(),
		subjects: map[string]realnameSubjectRow{"users": expiredRow()},
	}
	db := openRealnameGateTestDB(t, state)
	reason, message := realnameCheckForPurchase(db, "user", 7, "user", 7)
	if reason != purchaseReasonRealnameExpired {
		t.Fatalf("reason = %q, want %q", reason, purchaseReasonRealnameExpired)
	}
	if message == "" {
		t.Fatal("实名过期拦截时必须给出提示文案")
	}
}

func TestRealnameCheckForPurchaseAllowsGracePeriod(t *testing.T) {
	state := &realnameGateTestState{
		configs:  enabledRealnameConfig(),
		subjects: map[string]realnameSubjectRow{"users": graceRow()},
	}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("宽限期内应放行，got reason = %q", reason)
	}
}

func TestRealnameCheckForPurchaseAllowsVerifiedPayer(t *testing.T) {
	state := &realnameGateTestState{
		configs:  enabledRealnameConfig(),
		subjects: map[string]realnameSubjectRow{"users": {at: time.Now().AddDate(-1, 0, 0)}},
	}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("已实名应放行，got reason = %q", reason)
	}
}

func TestRealnameCheckForPurchaseChecksTargetOwner(t *testing.T) {
	// 代理本人永久有效，名下目标用户从未实名：应拦截并提示先让用户实名。
	state := &realnameGateTestState{
		configs:  enabledRealnameConfig(),
		subjects: map[string]realnameSubjectRow{"agents": {at: time.Now().AddDate(-1, 0, 0)}},
	}
	db := openRealnameGateTestDB(t, state)
	reason, message := realnameCheckForPurchase(db, "agent", 3, "user", 7)
	if reason != purchaseReasonRealnameRequired {
		t.Fatalf("reason = %q, want %q", reason, purchaseReasonRealnameRequired)
	}
	if message == "" {
		t.Fatal("拦截目标用户时必须给出提示文案")
	}
	if tables := state.subjectTables(); len(tables) != 2 || tables[0] != "agents" || tables[1] != "users" {
		t.Fatalf("应依次校验代理与目标用户，实际查询 %v", tables)
	}
}

func TestRealnameCheckForPurchaseBlocksUnverifiedPayerBeforeOwner(t *testing.T) {
	// 付款方与目标用户都未实名时，应给出付款方自身的提示，且不再查询目标用户。
	state := &realnameGateTestState{configs: enabledRealnameConfig()}
	db := openRealnameGateTestDB(t, state)
	reason, message := realnameCheckForPurchase(db, "agent", 3, "user", 7)
	if reason != purchaseReasonRealnameRequired {
		t.Fatalf("reason = %q, want %q", reason, purchaseReasonRealnameRequired)
	}
	if strings.Contains(message, "该用户") {
		t.Fatalf("应先提示付款方本人实名，got %q", message)
	}
	if tables := state.subjectTables(); len(tables) != 1 || tables[0] != "agents" {
		t.Fatalf("付款方未实名时应提前返回，实际查询 %v", tables)
	}
}

func TestRealnameCheckForPurchaseSkipsOwnerWhenDisabled(t *testing.T) {
	// purchase_check_owner=0 时只校验付款方，不再查询目标用户表。
	configs := enabledRealnameConfig()
	configs["purchase_check_owner"] = "0"
	state := &realnameGateTestState{
		configs:  configs,
		subjects: map[string]realnameSubjectRow{"agents": {at: time.Now().AddDate(-1, 0, 0)}},
	}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "agent", 3, "user", 7); reason != "" {
		t.Fatalf("关闭目标用户校验后应放行，got reason = %q", reason)
	}
	if tables := state.subjectTables(); len(tables) != 1 || tables[0] != "agents" {
		t.Fatalf("只应校验代理本人，实际查询 %v", tables)
	}
}

func TestRealnameCheckForPurchaseSkipsSelfOwner(t *testing.T) {
	// 用户为自己购买时只校验一次主体，避免重复查询。
	state := &realnameGateTestState{
		configs:  enabledRealnameConfig(),
		subjects: map[string]realnameSubjectRow{"users": {at: time.Now().AddDate(-1, 0, 0)}},
	}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("已实名应放行，got reason = %q", reason)
	}
	if tables := state.subjectTables(); len(tables) != 1 {
		t.Fatalf("本人购买只应校验一次，实际查询 %v", tables)
	}
}

func TestRealnameCheckForPurchaseDisabledPassesThrough(t *testing.T) {
	configs := enabledRealnameConfig()
	configs["purchase_required"] = "0"
	state := &realnameGateTestState{configs: configs}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("开关关闭时应放行，got reason = %q", reason)
	}
	if tables := state.subjectTables(); len(tables) != 0 {
		t.Fatalf("开关关闭时不应查询主体表，实际查询 %v", tables)
	}
}

func TestRealnameCheckForPurchaseSkipsWhenMasterSwitchOff(t *testing.T) {
	configs := enabledRealnameConfig()
	configs["enabled"] = "0"
	state := &realnameGateTestState{configs: configs}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("实名总开关关闭时应放行，got reason = %q", reason)
	}
}

func TestRealnameCheckForPurchaseSkipsWhenPluginDisabled(t *testing.T) {
	// 服务商插件未启用时放行，避免管理员未配好服务商导致用户无法购买。
	disabled := false
	state := &realnameGateTestState{
		configs:       enabledRealnameConfig(),
		pluginEnabled: &disabled,
	}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("服务商插件未启用时应放行，got reason = %q", reason)
	}
}

func TestRealnameCheckForPurchaseFailsOpenOnConfigError(t *testing.T) {
	// 配置读取失败时放行，避免数据库抖动导致全员无法购买。
	state := &realnameGateTestState{configErr: errors.New("配置查询失败")}
	db := openRealnameGateTestDB(t, state)
	if reason, _ := realnameCheckForPurchase(db, "user", 7, "user", 7); reason != "" {
		t.Fatalf("读取失败应放行，got reason = %q", reason)
	}
}

func TestUserRealnameRequiredFailOpenOnConfigError(t *testing.T) {
	state := &realnameGateTestState{configErr: errors.New("配置查询失败")}
	db := openRealnameGateTestDB(t, state)
	required, verified, status, err := userRealnameRequired(db, 9, 1)
	if err != nil {
		t.Fatalf("配置读取失败应返回 nil error，got %v", err)
	}
	if required || !verified || status != realnameStatusOK {
		t.Fatalf("读取失败应放行，got required=%v verified=%v status=%s", required, verified, status)
	}
}

func TestUserRealnameRequiredSkipsAppOutOfScope(t *testing.T) {
	state := &realnameGateTestState{configs: map[string]string{
		"enabled":           "1",
		"scope":             realnameScopeByApp,
		"require_app_ids":   "7",
		"validity_days":     "365",
		"purchase_required": "1",
	}}
	db := openRealnameGateTestDB(t, state)
	required, verified, status, err := userRealnameRequired(db, 9, 1)
	if err != nil {
		t.Fatalf("userRealnameRequired() error = %v", err)
	}
	if required || !verified || status != realnameStatusOK {
		t.Fatalf("未勾选的应用不应要求实名，got required=%v verified=%v status=%s", required, verified, status)
	}
	if tables := state.subjectTables(); len(tables) != 0 {
		t.Fatalf("不在范围内时不应查询主体表，实际查询 %v", tables)
	}
}

func TestUserRealnameRequiredAllScopeChecksOwner(t *testing.T) {
	state := &realnameGateTestState{
		configs: map[string]string{
			"enabled":       "1",
			"scope":         realnameScopeAll,
			"validity_days": "365",
		},
		subjects:     map[string]realnameSubjectRow{"users": expiredRow()},
		licenseOwner: &realnameLicenseOwner{ownerType: "user", ownerID: 7},
	}
	db := openRealnameGateTestDB(t, state)
	required, verified, status, err := userRealnameRequired(db, 9, 1)
	if err != nil {
		t.Fatalf("userRealnameRequired() error = %v", err)
	}
	if !required || verified || status != realnameStatusExpired {
		t.Fatalf("全部应用模式下过期实名应被拦截，got required=%v verified=%v status=%s", required, verified, status)
	}
	if tables := state.subjectTables(); len(tables) != 1 || tables[0] != "users" {
		t.Fatalf("应校验用户主体，实际查询 %v", tables)
	}
}

func TestUserRealnameRequiredByAppScope(t *testing.T) {
	// 按应用模式下命中勾选的应用，未实名应被拦截并映射为 realname_required。
	state := &realnameGateTestState{
		configs: map[string]string{
			"enabled":         "1",
			"scope":           realnameScopeByApp,
			"require_app_ids": "9",
		},
		licenseOwner: &realnameLicenseOwner{ownerType: "user", ownerID: 7},
	}
	db := openRealnameGateTestDB(t, state)
	required, verified, status, err := userRealnameRequired(db, 9, 1)
	if err != nil {
		t.Fatalf("userRealnameRequired() error = %v", err)
	}
	if !required || verified || status != realnameStatusNever {
		t.Fatalf("勾选应用下未实名应被拦截，got required=%v verified=%v status=%s", required, verified, status)
	}
	if got := realnameBlockedReason(status); got != verifyReasonRealnameRequired {
		t.Fatalf("拒绝原因 = %q, want %q", got, verifyReasonRealnameRequired)
	}
}

func TestUserRealnameRequiredExemptsAdminOwnedLicense(t *testing.T) {
	state := &realnameGateTestState{
		configs:      map[string]string{"enabled": "1", "scope": realnameScopeAll},
		licenseOwner: &realnameLicenseOwner{ownerType: "admin", ownerID: 1},
	}
	db := openRealnameGateTestDB(t, state)
	required, verified, status, err := userRealnameRequired(db, 9, 1)
	if err != nil {
		t.Fatalf("userRealnameRequired() error = %v", err)
	}
	if !required || !verified || status != realnameStatusOK {
		t.Fatalf("管理员创建的授权应豁免实名，got required=%v verified=%v status=%s", required, verified, status)
	}
	if tables := state.subjectTables(); len(tables) != 0 {
		t.Fatalf("豁免时不应查询主体表，实际查询 %v", tables)
	}
}
