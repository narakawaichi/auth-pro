# AuthPro PHP SDK

AuthPro 授权站 PHP 接入 SDK，封装 `/api/license/verify` 授权校验的签名、请求与结果判定。

## 环境要求

- PHP 7.0+
- cURL 扩展

## 包内容

| 文件 | 说明 |
| --- | --- |
| `AuthProLicense.php` | SDK 主类，唯一必须引入的文件 |
| `example.php` | 入口文件接入示例 |
| `README.md` | 本文档 |

## 快速接入

1. 把 `AuthProLicense.php` 复制到你的项目中。
2. 在站点入口文件顶部加入：

```php
<?php
require_once __DIR__ . '/AuthProLicense.php';

$sdk = new AuthProLicense(
    'https://license.example.com', // 授权站地址
    'your_app_key',                // 应用的 appKey
    'your_app_secret'              // 应用的 appSecret
);

if (!$sdk->verify()) {
    http_response_code(403);
    exit('License invalid: ' . $sdk->getLastError());
}

// 授权通过，继续业务代码
```

也可以使用 `verifyOrExit()` 一行完成守卫：

```php
$sdk->verifyOrExit(); // 失败时自动 403 并输出原因
```

## API

### `__construct($baseUrl, $appKey, $appSecret, $timeout = 8)`

- `$baseUrl`：授权站地址，不带尾随斜杠。
- `$appKey` / `$appSecret`：AuthPro 后台「应用管理」中对应应用的密钥对。
- `$timeout`：请求超时时间（秒），默认 8。

### `verify($domain = '', $serverIp = '', $licenseKey = '')`

校验授权，返回 `true` / `false`。

- `$domain` 留空时自动取 `$_SERVER['HTTP_HOST']`（去掉端口）。
- `$serverIp` 留空时自动取 `$_SERVER['SERVER_ADDR']`。
- 应用使用授权码（卡密）时，把用户填写的授权码作为 `$licenseKey` 传入；否则留空。

### `getLastError()`

返回最近一次失败原因（网络错误、授权无效等），便于排查。

### `getLastResponse()`

返回最近一次接口返回的完整数组，调试时可用。

## 校验规则（与后台「SDK 示例」页面一致）

- 接口：`POST /api/license/verify`，`Content-Type: application/json`
- v2 签名：`HMAC-SHA256(appSecret, "v2\nappKey\nlicenseKey\ndomain\nserverIp\ntimestamp")`，空字段保留空行
- 域名 / IP 规范化：SDK 在签名前会对 domain、serverIp 做与服务端一致的规范化（小写、去协议头与端口、去结尾点号、IPv6 转压缩标准形），保证双方签名串一致
- 时间窗口：`timestamp` 与服务器时间误差需在 10 分钟内，请保证业务服务器时间准确（建议配置 NTP）
- 通过条件：返回 JSON 中 `code = 200` 且 `data.result = pass`

## 常见问题

- **提示「网络请求失败」**：检查业务服务器能否访问授权站地址、防火墙与 DNS。
- **提示签名相关错误**：确认 appKey / appSecret 与应用匹配，且服务器时间误差在 10 分钟内。
- **Apache / Nginx 下获取不到域名**：SDK 默认取 `HTTP_HOST`，反向代理场景请显式传 `$domain`。
- **生产环境建议缓存校验结果**（如 10~30 分钟），避免每个请求都调用授权接口影响性能。
