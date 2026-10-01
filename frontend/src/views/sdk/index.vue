<template>
  <div class="sdk-page">
    <ElCard shadow="never" class="intro-card">
      <div class="intro-content">
        <div>
          <h2>SDK 接入示例</h2>
          <p
            >选择语言后复制示例代码，替换服务地址、appKey、appSecret 和授权目标即可接入授权校验；PHP
            另提供完整 SDK 包下载。</p
          >
        </div>
        <ElTag type="primary" size="large">License SDK</ElTag>
      </div>
    </ElCard>

    <ElCard shadow="never" class="config-card">
      <template #header>
        <span class="card-title">公共配置</span>
      </template>
      <ElDescriptions :column="2" border>
        <ElDescriptionsItem label="接口地址">POST /api/license/verify</ElDescriptionsItem>
        <ElDescriptionsItem label="Content-Type">application/json</ElDescriptionsItem>
        <ElDescriptionsItem label="签名规则"
          >v2 必填：HMAC-SHA256(appSecret, 固定换行规范串)，新增站点绑定必须使用
          v2</ElDescriptionsItem
        >
        <ElDescriptionsItem label="v2 规范串"
          >"v2\nappKey\nlicenseKey\ndomain\nserverIp\ntimestamp"（空字段保留空行）</ElDescriptionsItem
        >
        <ElDescriptionsItem label="v1 兼容"
          >MD5(appKey + target + timestamp + appSecret) 仅能验证历史已绑定站点</ElDescriptionsItem
        >
        <ElDescriptionsItem label="时间窗口"
          >timestamp 与服务器时间误差需在 10 分钟内</ElDescriptionsItem
        >
        <ElDescriptionsItem label="通过条件">code = 200 且 data.result = pass</ElDescriptionsItem>
      </ElDescriptions>
    </ElCard>

    <ElCard shadow="never" class="sdk-card">
      <template #header>
        <div class="sdk-header">
          <span class="card-title">代码示例</span>
          <ElButton type="primary" @click="copyCode(activeExample.code)">复制当前示例</ElButton>
        </div>
      </template>

      <ElTabs v-model="activeKey" class="sdk-tabs">
        <ElTabPane v-for="item in sdkExamples" :key="item.key" :label="item.label" :name="item.key">
          <div class="example-head">
            <div>
              <h3>{{ item.label }}</h3>
              <p>{{ item.desc }}</p>
            </div>
            <div class="example-actions">
              <ElButton
                v-if="item.downloadLang"
                type="primary"
                :loading="downloading"
                @click="downloadSdk(item)"
                >下载 {{ item.label }} SDK</ElButton
              >
              <ElButton @click="copyCode(item.code)">复制 {{ item.label }} 代码</ElButton>
            </div>
          </div>
          <pre><code>{{ item.code }}</code></pre>
        </ElTabPane>
      </ElTabs>
    </ElCard>
  </div>
</template>

<script setup lang="ts">
  import { computed, ref } from 'vue'
  import { ElMessage } from 'element-plus'
  import { fetchDownloadSdk } from '@/api/sdk'

  defineOptions({ name: 'SdkExamples' })

  interface SdkExample {
    key: string
    label: string
    desc: string
    code: string
    /** 配置后在示例头部显示 SDK 下载按钮，对应后端 /api/sdk/:lang/download */
    downloadLang?: string
  }

  const sdkExamples: SdkExample[] = [
    {
      key: 'php',
      label: 'PHP',
      desc: '适合 PHP Web 项目，在入口文件引入 SDK 即可完成授权守卫。下方为最小接入示例，完整封装（AuthProLicense 类、入口示例、说明文档）请点击右侧按钮下载。',
      code: `<?php
// 最小接入示例，完整封装请点右上角「下载 PHP SDK」（含 AuthProLicense.php、example.php、README.md）
require_once __DIR__ . '/AuthProLicense.php';

$sdk = new AuthProLicense(
    'https://license.example.com', // 授权站地址
    'your_app_key',                // 应用的 appKey
    'your_app_secret'              // 应用的 appSecret
);

// verify($domain, $serverIp, $licenseKey)
// domain / serverIp 留空自动取当前站点域名与 IP，licenseKey 按授权类型选填
if (!$sdk->verify()) {
    http_response_code(403);
    exit('License invalid: ' . $sdk->getLastError());
}`,
      downloadLang: 'php'
    },
    {
      key: 'java',
      label: 'Java',
      desc: '适合 Spring Boot 或普通 Java 服务，在启动或拦截器中调用。',
      code: `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

public class LicenseVerifier {
    private static final String BASE_URL = "https://license.example.com";
    private static final String APP_KEY = "your_app_key";
    private static final String APP_SECRET = "your_app_secret";

    public static boolean verify(String domain, String serverIp, String licenseKey) throws Exception {
        String safeDomain = domain == null ? "" : domain;
        String safeServerIp = serverIp == null ? "" : serverIp;
        String safeLicenseKey = licenseKey == null ? "" : licenseKey;
        long timestamp = System.currentTimeMillis() / 1000;
        String canonical = String.join("\\n",
            "v2", APP_KEY, safeLicenseKey, safeDomain, safeServerIp, Long.toString(timestamp));
        String sign = hmacSha256(APP_SECRET, canonical);

        String body = String.format(
            "{\\"appKey\\":\\"%s\\",\\"domain\\":\\"%s\\",\\"serverIp\\":\\"%s\\",\\"licenseKey\\":\\"%s\\",\\"timestamp\\":%d,\\"signVersion\\":\\"v2\\",\\"sign\\":\\"%s\\"}","
            APP_KEY, safeDomain, safeServerIp, safeLicenseKey, timestamp, sign
        );

        HttpRequest request = HttpRequest.newBuilder()
            .uri(URI.create(BASE_URL + "/api/license/verify"))
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build();

        String response = HttpClient.newHttpClient()
            .send(request, HttpResponse.BodyHandlers.ofString())
            .body();

        return response.contains("\\"code\\":200") && response.contains("\\"result\\":\\"pass\\"");
    }

    private static String hmacSha256(String secret, String text) throws Exception {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
        byte[] digest = mac.doFinal(text.getBytes(StandardCharsets.UTF_8));
        StringBuilder hex = new StringBuilder();
        for (byte b : digest) hex.append(String.format("%02x", b));
        return hex.toString();
    }
}`
    },
    {
      key: 'cpp',
      label: 'C++',
      desc: '适合原生程序或桌面端，示例依赖 libcurl 和 OpenSSL。',
      code: `#include <curl/curl.h>
#include <openssl/hmac.h>
#include <openssl/sha.h>
#include <iomanip>
#include <iostream>
#include <sstream>
#include <string>
#include <ctime>

std::string hmacSha256Hex(const std::string& key, const std::string& input) {
    unsigned char digest[EVP_MAX_MD_SIZE];
    unsigned int len = 0;
    HMAC(EVP_sha256(), key.c_str(), static_cast<int>(key.size()),
         reinterpret_cast<const unsigned char*>(input.c_str()), input.size(), digest, &len);
    std::ostringstream out;
    for (unsigned int i = 0; i < len; ++i)
        out << std::hex << std::setw(2) << std::setfill('0') << (int)digest[i];
    return out.str();
}

size_t writeCallback(void* contents, size_t size, size_t nmemb, std::string* output) {
    output->append(static_cast<char*>(contents), size * nmemb);
    return size * nmemb;
}

bool verifyLicense(const std::string& domain, const std::string& serverIp, const std::string& licenseKey = "") {
    std::string baseUrl = "https://license.example.com";
    std::string appKey = "your_app_key";
    std::string appSecret = "your_app_secret";
    long timestamp = std::time(nullptr);
    std::string canonical = "v2\\n" + appKey + "\\n" + licenseKey + "\\n" + domain + "\\n" + serverIp + "\\n" + std::to_string(timestamp);
    std::string sign = hmacSha256Hex(appSecret, canonical);

    std::string body = "{\\"appKey\\":\\"" + appKey + "\\",\\"domain\\":\\"" + domain +
        "\\",\\"serverIp\\":\\"" + serverIp + "\\",\\"licenseKey\\":\\"" + licenseKey + "\\",\\"timestamp\\":" +
        std::to_string(timestamp) + ",\\"signVersion\\":\\"v2\\",\\"sign\\":\\"" + sign + "\\"}";

    CURL* curl = curl_easy_init();
    std::string response;
    struct curl_slist* headers = nullptr;
    headers = curl_slist_append(headers, "Content-Type: application/json");

    curl_easy_setopt(curl, CURLOPT_URL, (baseUrl + "/api/license/verify").c_str());
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body.c_str());
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, writeCallback);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &response);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 8L);
    CURLcode result = curl_easy_perform(curl);

    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);

    return result == CURLE_OK && response.find("\\"code\\":200") != std::string::npos && response.find("\\"result\\":\\"pass\\"") != std::string::npos;
}`
    },
    {
      key: 'python',
      label: 'Python',
      desc: '适合 Django、Flask、FastAPI 或脚本服务。',
      code: `import hashlib
import hmac
import time
import requests

BASE_URL = 'https://license.example.com'
APP_KEY = 'your_app_key'
APP_SECRET = 'your_app_secret'


def verify_license(domain: str, server_ip: str = '', license_key: str = '') -> bool:
    timestamp = int(time.time())
    canonical = '\\n'.join(['v2', APP_KEY, license_key, domain, server_ip, str(timestamp)])
    sign = hmac.new(APP_SECRET.encode(), canonical.encode(), hashlib.sha256).hexdigest()

    payload = {
        'appKey': APP_KEY,
        'domain': domain,
        'serverIp': server_ip,
        'licenseKey': license_key,
        'timestamp': timestamp,
        'signVersion': 'v2',
        'sign': sign,
    }

    try:
        response = requests.post(f'{BASE_URL}/api/license/verify', json=payload, timeout=8)
        data = response.json()
        return data.get('code') == 200 and data.get('data', {}).get('result') == 'pass'
    except Exception:
        return False


if not verify_license('www.example.com', '203.0.113.10'):
    raise SystemExit('License invalid')`
    },
    {
      key: 'go',
      label: 'Go',
      desc: '适合 Go Web 服务，在启动阶段、中间件或定时任务里调用。',
      code:
        `package main

import (
    "bytes"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "strconv"
    "strings"
    "time"
)

type VerifyRequest struct {
    AppKey     string ` +
        '`json:"appKey"`' +
        `
    Domain     string ` +
        '`json:"domain"`' +
        `
    ServerIP   string ` +
        '`json:"serverIp"`' +
        `
    LicenseKey string ` +
        '`json:"licenseKey"`' +
        `
    Timestamp  int64  ` +
        '`json:"timestamp"`' +
        `
    SignVersion string ` +
        '`json:"signVersion"`' +
        `
    Sign       string ` +
        '`json:"sign"`' +
        `
}

type VerifyResponse struct {
    Code int ` +
        '`json:"code"`' +
        `
    Data struct {
        Result string ` +
        '`json:"result"`' +
        `
    } ` +
        '`json:"data"`' +
        `
}

func hmacSha256Hex(text, secret string) string {
    mac := hmac.New(sha256.New, []byte(secret))
    _, _ = mac.Write([]byte(text))
    return hex.EncodeToString(mac.Sum(nil))
}

func VerifyLicense(domain, serverIP, licenseKey string) bool {
    baseURL := "https://license.example.com"
    appKey := "your_app_key"
    appSecret := "your_app_secret"
    timestamp := time.Now().Unix()
    canonical := strings.Join([]string{
        "v2", appKey, licenseKey, domain, serverIP, strconv.FormatInt(timestamp, 10),
    }, "\\n")

    reqBody := VerifyRequest{
        AppKey: appKey,
        Domain: domain,
        ServerIP: serverIP,
        LicenseKey: licenseKey,
        Timestamp: timestamp,
        SignVersion: "v2",
        Sign: hmacSha256Hex(canonical, appSecret),
    }

    body, _ := json.Marshal(reqBody)
    resp, err := http.Post(baseURL+"/api/license/verify", "application/json", bytes.NewReader(body))
    if err != nil {
        return false
    }
    defer resp.Body.Close()

    var result VerifyResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return false
    }
    return result.Code == 200 && result.Data.Result == "pass"
}`
    },
    {
      key: 'node',
      label: 'Node.js',
      desc: '适合 Node.js、NestJS、Express 等服务端项目。',
      code: `import crypto from 'node:crypto'

const BASE_URL = 'https://license.example.com'
const APP_KEY = 'your_app_key'
const APP_SECRET = 'your_app_secret'

function hmacSha256Hex(text) {
  return crypto.createHmac('sha256', APP_SECRET).update(text).digest('hex')
}

export async function verifyLicense({ domain, serverIp = '', licenseKey = '' }) {
  const timestamp = Math.floor(Date.now() / 1000)
  const canonical = ['v2', APP_KEY, licenseKey, domain, serverIp, String(timestamp)].join('\\n')
  const sign = hmacSha256Hex(canonical)

  const response = await fetch(BASE_URL + '/api/license/verify', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      appKey: APP_KEY,
      domain,
      serverIp,
      licenseKey,
      timestamp,
      signVersion: 'v2',
      sign,
    }),
  })

  const data = await response.json()
  return data.code === 200 && data.data?.result === 'pass'
}`
    }
  ]

  const activeKey = ref(sdkExamples[0].key)
  const activeExample = computed(
    () => sdkExamples.find((item) => item.key === activeKey.value) || sdkExamples[0]
  )

  async function copyCode(code: string) {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(code)
      } else {
        const textarea = document.createElement('textarea')
        textarea.value = code
        textarea.style.position = 'fixed'
        textarea.style.opacity = '0'
        document.body.appendChild(textarea)
        textarea.select()
        document.execCommand('copy')
        document.body.removeChild(textarea)
      }
      ElMessage.success('代码已复制')
    } catch {
      ElMessage.error('复制失败，请手动复制')
    }
  }

  const downloading = ref(false)

  async function downloadSdk(item: SdkExample) {
    if (!item.downloadLang || downloading.value) return
    downloading.value = true
    let url = ''
    try {
      const file = await fetchDownloadSdk(item.downloadLang)
      url = URL.createObjectURL(file)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `auth-pro-${item.downloadLang}-sdk.zip`
      document.body.appendChild(anchor)
      anchor.click()
      anchor.remove()
      ElMessage.success('SDK 已开始下载')
    } catch {
      // 请求层已提示失败原因；避免把错误 JSON 保存成 zip
    } finally {
      if (url) setTimeout(() => URL.revokeObjectURL(url), 1000)
      downloading.value = false
    }
  }
</script>

<style scoped lang="scss">
  .sdk-page {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding: 16px;
  }

  .intro-card,
  .config-card,
  .sdk-card {
    max-width: 1180px;
  }

  .intro-content,
  .sdk-header,
  .example-head {
    display: flex;
    gap: 16px;
    align-items: center;
    justify-content: space-between;
  }

  .intro-content {
    h2 {
      margin: 0 0 8px;
      font-size: 24px;
      font-weight: 700;
    }

    p {
      margin: 0;
      line-height: 1.7;
      color: var(--el-text-color-secondary);
    }
  }

  .card-title {
    font-weight: 700;
  }

  .example-head {
    margin-bottom: 12px;

    h3 {
      margin: 0 0 6px;
      font-size: 18px;
      font-weight: 700;
    }

    p {
      margin: 0;
      color: var(--el-text-color-secondary);
    }
  }

  .example-actions {
    display: flex;
    flex-shrink: 0;
    gap: 12px;
  }

  pre {
    max-height: 620px;
    padding: 16px;
    margin: 0;
    overflow: auto;
    line-height: 1.7;
    color: #d1d5db;
    background: #111827;
    border-radius: 10px;
  }

  code {
    font-family: 'Roboto Mono', 'JetBrains Mono', Consolas, monospace;
    font-size: 13px;
    white-space: pre;
  }

  @media (width <= 768px) {
    .intro-content,
    .sdk-header,
    .example-head {
      flex-direction: column;
      align-items: flex-start;
    }
  }
</style>
