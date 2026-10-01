<?php
/**
 * AuthProLicense - AuthPro 授权站 PHP SDK
 *
 * 封装 POST /api/license/verify 授权校验：
 *  - v2 签名：HMAC-SHA256(appSecret, "v2\nappKey\nlicenseKey\ndomain\nserverIp\ntimestamp")
 *  - 通过条件：返回 JSON 中 code = 200 且 data.result = pass
 *
 * 依赖：PHP 7.0+，cURL 扩展。
 */
class AuthProLicense
{
    /** @var string 授权站地址，例如 https://license.example.com */
    private $baseUrl;

    /** @var string 应用的 appKey */
    private $appKey;

    /** @var string 应用的 appSecret */
    private $appSecret;

    /** @var int 请求超时时间（秒） */
    private $timeout;

    /** @var string 最近一次失败原因 */
    private $lastError = '';

    /** @var array|null 最近一次接口返回的完整数据，调试时可用 */
    private $lastResponse = null;

    /**
     * @param string $baseUrl   授权站地址，不带尾随斜杠
     * @param string $appKey    后台应用管理里的 appKey
     * @param string $appSecret 后台应用管理里的 appSecret
     * @param int    $timeout   请求超时时间（秒），默认 8
     */
    public function __construct($baseUrl, $appKey, $appSecret, $timeout = 8)
    {
        $this->baseUrl = rtrim(trim($baseUrl), '/');
        $this->appKey = trim($appKey);
        $this->appSecret = $appSecret;
        $this->timeout = max(1, (int) $timeout);
    }

    /** @return string 最近一次失败原因（网络错误 / 授权无效等），成功时为空字符串 */
    public function getLastError()
    {
        return $this->lastError;
    }

    /** @return array|null 最近一次接口返回的完整数组 */
    public function getLastResponse()
    {
        return $this->lastResponse;
    }

    /**
     * 校验授权是否有效。
     *
     * @param string $domain     绑定域名，留空自动取 $_SERVER['HTTP_HOST']（去掉端口）
     * @param string $serverIp   服务器 IP，留空自动取 $_SERVER['SERVER_ADDR']
     * @param string $licenseKey 授权码，按应用授权类型选填
     * @return bool true 表示授权有效
     */
    public function verify($domain = '', $serverIp = '', $licenseKey = '')
    {
        $this->lastError = '';
        $this->lastResponse = null;

        if ($this->baseUrl === '' || $this->appKey === '' || $this->appSecret === '') {
            $this->lastError = 'SDK 未正确配置 baseUrl / appKey / appSecret';
            return false;
        }

        if ($domain === '') {
            $domain = self::detectDomain();
        }
        if ($serverIp === '') {
            $serverIp = self::detectServerIp();
        }

        // 与服务端规范化逻辑对齐（小写、去协议头/端口、IPv6 压缩标准形），保证签名一致
        $domain = self::normalizeDomain($domain);
        $serverIp = self::normalizeServerIp($serverIp);

        $timestamp = time();
        $payload = array(
            'appKey' => $this->appKey,
            'domain' => $domain,
            'serverIp' => $serverIp,
            'licenseKey' => $licenseKey,
            'timestamp' => $timestamp,
            'signVersion' => 'v2',
            'sign' => $this->sign($licenseKey, $domain, $serverIp, $timestamp),
        );

        $result = $this->postJson('/api/license/verify', $payload);
        if ($result === null) {
            return false; // lastError 已由 postJson 写入
        }

        $data = json_decode($result['body'], true);
        if (!is_array($data)) {
            $this->lastError = '接口返回异常（HTTP ' . $result['status'] . '），不是合法 JSON';
            return false;
        }
        $this->lastResponse = $data;

        $code = isset($data['code']) ? (int) $data['code'] : 0;
        $pass = isset($data['data']['result']) && $data['data']['result'] === 'pass';
        if ($code === 200 && $pass) {
            return true;
        }

        $this->lastError = isset($data['msg']) && $data['msg'] !== ''
            ? (string) $data['msg']
            : '授权校验未通过';
        return false;
    }

    /**
     * 校验失败时直接以 403 终止请求，适合放在站点入口文件顶部做授权守卫。
     *
     * @param string $message 失败时输出的前缀文案
     */
    public function verifyOrExit($domain = '', $serverIp = '', $licenseKey = '', $message = 'License invalid')
    {
        if ($this->verify($domain, $serverIp, $licenseKey)) {
            return;
        }
        http_response_code(403);
        $detail = $this->lastError !== '' ? $message . ': ' . $this->lastError : $message;
        exit($detail);
    }

    /**
     * v2 签名：HMAC-SHA256(appSecret, 固定换行规范串)，空字段保留空行。
     * 规范串格式："v2\nappKey\nlicenseKey\ndomain\nserverIp\ntimestamp"
     */
    private function sign($licenseKey, $domain, $serverIp, $timestamp)
    {
        $canonical = implode("\n", array(
            'v2',
            $this->appKey,
            $licenseKey,
            $domain,
            $serverIp,
            (string) $timestamp,
        ));
        return hash_hmac('sha256', $canonical, $this->appSecret);
    }

    /**
     * 发送 JSON POST 请求。
     *
     * @return array|null 成功时返回 ['status' => int, 'body' => string]，传输失败返回 null
     */
    private function postJson($path, array $payload)
    {
        if (!function_exists('curl_init')) {
            $this->lastError = 'PHP 未启用 cURL 扩展';
            return null;
        }

        $ch = curl_init($this->baseUrl . $path);
        curl_setopt_array($ch, array(
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_POST => true,
            CURLOPT_HTTPHEADER => array('Content-Type: application/json'),
            CURLOPT_POSTFIELDS => json_encode($payload),
            CURLOPT_TIMEOUT => $this->timeout,
            CURLOPT_CONNECTTIMEOUT => min(5, $this->timeout),
        ));

        $body = curl_exec($ch);
        if ($body === false) {
            $this->lastError = '网络请求失败：' . curl_error($ch);
            curl_close($ch);
            return null;
        }
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        return array('status' => $status, 'body' => $body);
    }

    /** @return string 当前访问域名（未规范化，交由 normalizeDomain 处理） */
    private static function detectDomain()
    {
        return isset($_SERVER['HTTP_HOST']) ? trim($_SERVER['HTTP_HOST']) : '';
    }

    /** @return string 当前服务器 IP */
    private static function detectServerIp()
    {
        return isset($_SERVER['SERVER_ADDR']) ? trim($_SERVER['SERVER_ADDR']) : '';
    }

    /**
     * 与服务端 normalizeLicenseDomain 对齐的域名规范化：
     * 小写、去 http(s):// 前缀、截断路径、去端口、去结尾点号、去方括号，IP 转标准形。
     */
    public static function normalizeDomain($domain)
    {
        $value = strtolower(trim($domain));
        if (strpos($value, 'http://') === 0) {
            $value = substr($value, 7);
        } elseif (strpos($value, 'https://') === 0) {
            $value = substr($value, 8);
        }
        $slash = strpos($value, '/');
        if ($slash !== false) {
            $value = substr($value, 0, $slash);
        }
        // 去端口：先处理 [IPv6]:port，再处理 host:port（IPv6 字面量本身保留）
        if (preg_match('/^\[([^\]]+)\]:\d+$/', $value, $matches)) {
            $value = $matches[1];
        } elseif (substr_count($value, ':') === 1 && preg_match('/^([^:]+):\d+$/', $value, $matches)) {
            $value = $matches[1];
        }
        $value = rtrim($value, '.');
        if (substr($value, -1) === ']') {
            $value = substr($value, 0, -1);
        }
        if (substr($value, 0, 1) === '[') {
            $value = substr($value, 1);
        }
        if ($value === '') {
            return '';
        }
        if (filter_var($value, FILTER_VALIDATE_IP)) {
            return self::canonicalIp($value);
        }
        return $value;
    }

    /**
     * 与服务端 normalizeLicenseServerIP 对齐的 IP 规范化：
     * 去 %zone、去方括号，合法 IP 转标准形，非法 IP 返回空字符串。
     */
    public static function normalizeServerIp($ip)
    {
        $value = trim($ip);
        $zone = strrpos($value, '%');
        if ($zone !== false && $zone > 0) {
            $value = substr($value, 0, $zone);
        }
        if ($value !== '' && substr($value, -1) === ']') {
            $value = substr($value, 0, -1);
        }
        if (substr($value, 0, 1) === '[') {
            $value = substr($value, 1);
        }
        if (filter_var($value, FILTER_VALIDATE_IP)) {
            return self::canonicalIp($value);
        }
        return '';
    }

    /** IP 转压缩标准形（与 Go net.IP.String() 一致，如 2001:0db8::1 → 2001:db8::1） */
    private static function canonicalIp($ip)
    {
        $packed = @inet_pton($ip);
        if ($packed === false) {
            return $ip;
        }
        $out = @inet_ntop($packed);
        return $out === false ? $ip : $out;
    }
}
