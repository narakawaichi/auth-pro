package handler

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestAlipayInfoAuthorizeURLProduction(t *testing.T) {
	cfg := realnameConfig{
		AppID:   "2021000000000000",
		Gateway: alipayDefaultGateway,
	}
	callback := "https://demo.example.com/api/realname/alipay-callback"
	link := alipayInfoAuthorizeURL(cfg, "verify123", callback)

	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("授权链接解析失败：%v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "openauth.alipay.com" {
		t.Fatalf("正式环境授权链接域名错误：%s", link)
	}
	if parsed.Path != "/oauth2/publicAppAuthorize.htm" {
		t.Fatalf("授权链接路径错误：%s", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("app_id") != cfg.AppID {
		t.Fatalf("app_id 期望 %s，实际 %s", cfg.AppID, query.Get("app_id"))
	}
	if query.Get("scope") != "id_verify" {
		t.Fatalf("scope 期望 id_verify，实际 %s", query.Get("scope"))
	}
	if query.Get("redirect_uri") != callback {
		t.Fatalf("redirect_uri 期望 %s，实际 %s", callback, query.Get("redirect_uri"))
	}
	if query.Get("cert_verify_id") != "verify123" {
		t.Fatalf("cert_verify_id 期望 verify123，实际 %s", query.Get("cert_verify_id"))
	}
}

func TestAlipayInfoAuthorizeURLSandbox(t *testing.T) {
	cfg := realnameConfig{
		AppID:   "9021000000000000",
		Gateway: alipaySandboxGateway,
	}
	link := alipayInfoAuthorizeURL(cfg, "verify-sandbox", "https://demo.example.com/api/realname/alipay-callback")
	if !strings.HasPrefix(link, alipaySandboxAuthorizeURL) {
		t.Fatalf("沙箱网关应使用沙箱授权地址：%s", link)
	}
	if !strings.Contains(link, "cert_verify_id=verify-sandbox") {
		t.Fatalf("沙箱授权链接缺少 cert_verify_id：%s", link)
	}
}

func TestAlipayInfoAuthorizeURLEncodesCallback(t *testing.T) {
	cfg := realnameConfig{AppID: "app&id=1", Gateway: alipayDefaultGateway}
	link := alipayInfoAuthorizeURL(cfg, "v", "https://demo.example.com/cb?a=1&b=2")
	if !strings.Contains(link, "redirect_uri=") {
		t.Fatalf("缺少 redirect_uri：%s", link)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("链接解析失败：%v", err)
	}
	if got := parsed.Query().Get("redirect_uri"); got != "https://demo.example.com/cb?a=1&b=2" {
		t.Fatalf("redirect_uri 未正确编码：%s", got)
	}
}

func TestAlipayPreconsultBizContentMatchesDoc(t *testing.T) {
	// 官方文档要求平铺的 user_name/cert_type/cert_no，
	// 不得再使用 face.certify 系列的 outer_order_no/biz_code/identity_param 结构。
	raw := alipayPreconsultBizContent("张三", "230100199901010001")
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("biz_content 不是合法 JSON：%v", err)
	}
	if payload["user_name"] != "张三" {
		t.Fatalf("user_name 期望 张三，实际 %v", payload["user_name"])
	}
	if payload["cert_type"] != "IDENTITY_CARD" {
		t.Fatalf("cert_type 期望 IDENTITY_CARD，实际 %v", payload["cert_type"])
	}
	if payload["cert_no"] != "230100199901010001" {
		t.Fatalf("cert_no 不符：%v", payload["cert_no"])
	}
	for _, key := range []string{"outer_order_no", "biz_code", "identity_param", "merchant_order_no", "cert_name"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("biz_content 不应包含 face.certify 字段 %s：%s", key, raw)
		}
	}
}

func TestAlipayConsultResponseDecode(t *testing.T) {
	raw := map[string]interface{}{
		"alipay_user_certdoc_certverify_consult_response": map[string]interface{}{
			"code":        "10000",
			"msg":         "Success",
			"passed":      "T",
			"fail_reason": "姓名不一致",
			"fail_params": "[\"user_name\"]",
		},
		"sign": "x",
	}
	body, _ := json.Marshal(raw)
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("响应解析失败：%v", err)
	}
	node, ok := resp["alipay_user_certdoc_certverify_consult_response"].(map[string]interface{})
	if !ok {
		t.Fatal("响应节点结构错误")
	}
	if code, _ := node["code"].(string); code != "10000" {
		t.Fatalf("业务码期望 10000，实际 %v", node["code"])
	}
	var out alipayConsultResponse
	nodeBytes, _ := json.Marshal(node)
	if err := json.Unmarshal(nodeBytes, &out); err != nil {
		t.Fatalf("业务结构解析失败：%v", err)
	}
	if out.Passed != "T" || out.FailReason != "姓名不一致" || out.FailParams != "[\"user_name\"]" {
		t.Fatalf("解析结果不符：%+v", out)
	}
}

func TestAlipayInfoSessionRoundTrip(t *testing.T) {
	session := realnameFaceSession{
		Provider:  realnameProviderAlipay,
		AuthMode:  alipayAuthModeInfo,
		OwnerType: "user",
		UserID:    42,
		RealName:  "张三",
		IDCard:    "11010519491231002X",
		Status:    realnameFaceStatusPending,
		OrderNo:   "RNU421700000000",
		VerifyID:  "671ffcda5447bc87e9ed2f669eb143d4",
	}
	parsed, err := parseRealnameFaceSession(session.encode())
	if err != nil {
		t.Fatalf("会话解析失败：%v", err)
	}
	if parsed.Provider != realnameProviderAlipay || parsed.AuthMode != alipayAuthModeInfo {
		t.Fatalf("会话 provider/authMode 不符：%+v", parsed)
	}
	if parsed.VerifyID != session.VerifyID || parsed.OrderNo != session.OrderNo {
		t.Fatalf("会话 verifyId/orderNo 不符：%+v", parsed)
	}
	if parsed.OwnerType != "user" || parsed.UserID != 42 {
		t.Fatalf("会话归属不符：%+v", parsed)
	}
}
