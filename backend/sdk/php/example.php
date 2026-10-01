<?php
/**
 * AuthPro PHP SDK 接入示例
 *
 * 用法：把本目录的 AuthProLicense.php 复制到你的项目中，
 * 在站点入口文件（如 index.php）顶部加入下面的授权守卫。
 */
require_once __DIR__ . '/AuthProLicense.php';

$sdk = new AuthProLicense(
    'https://license.example.com', // 授权站地址（你的 AuthPro 站点）
    'your_app_key',                // 后台应用管理里的 appKey
    'your_app_secret'              // 后台应用管理里的 appSecret
);

// verify($domain, $serverIp, $licenseKey)
//  - domain / serverIp 留空时自动取当前访问域名与服务器 IP
//  - 应用使用授权码（卡密）时，把用户填写的 licenseKey 作为第三个参数传入
if (!$sdk->verify()) {
    http_response_code(403);
    exit('License invalid: ' . $sdk->getLastError());
}

// ===== 授权通过，下面是你的业务代码 =====
echo '授权有效，业务正常运行';
