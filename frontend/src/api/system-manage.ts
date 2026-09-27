import request from '@/utils/http'
import { AppRouteRecord } from '@/types/router'

export type EpayPayType = 'alipay' | 'wxpay' | 'qqpay'

export interface SystemConfigData {
  siteName: string
  siteSubtitle: string
  siteLogo: string
  installedAt: string
  stationQQ: string
  icpNumber: string
  domainLicenseNotice: string
  registrationEnabled: boolean
  selfPurchaseEnabled: boolean
  piracyDetectionEnabled: boolean
  geetestEnabled: boolean
  geetestCaptchaId: string
  /** 验证 Key 只写不读：提交时留空表示保持已保存的 Key */
  geetestCaptchaKey?: string
  geetestCaptchaKeySet: boolean
}

export interface PaymentConfigData {
  easypayEnabled: boolean
  easypayGateway: string
  easypayPid: string
  easypayMerchantKey?: string
  easypayMerchantKeySet: boolean
  easypayDefaultType: EpayPayType
  easypayPayTypes: EpayPayType[]
  easypayNotifyUrl: string
  easypayReturnUrl: string
}

export interface PaymentV2ConfigData {
  easypayEnabled: boolean
  easypayGateway: string
  easypayPid: string
  easypayMerchantKey?: string
  easypayMerchantKeySet: boolean
  easypayPlatformKey?: string
  easypayPlatformKeySet: boolean
  easypayDefaultType: EpayPayType
  easypayPayTypes: EpayPayType[]
  easypayNotifyUrl: string
  easypayReturnUrl: string
}

export type SystemFeatureSwitchKey =
  | 'registrationEnabled'
  | 'selfPurchaseEnabled'
  | 'piracyDetectionEnabled'

export interface SystemFeatureSwitchData {
  key: SystemFeatureSwitchKey
  enabled: boolean
}

export interface RealnameAppItem {
  id: number
  appName: string
  appKey: string
}

export type RealnameProvider = 'alipay' | 'kuaitong' | 'tencent' | 'xiaomu'
export type KuaitongAuthType = 'face' | 'two_element'
export type XiaomuProductMode = 'three_element' | 'face_h5' | 'tencent_h5'
export type AlipayAuthMode = 'face' | 'info'
/** 实名生效范围：by_app 按应用勾选；all 全部应用强制 */
export type RealnameScope = 'by_app' | 'all'
/** 存量实名数据处理策略：keep 保持永不过期；backfill 按认证时间补算 */
export type RealnameLegacyPolicy = 'keep' | 'backfill'

export interface RealnameConfigData {
  enabled: boolean
  pluginEnabled: boolean
  provider: RealnameProvider
  appId: string
  privateKeySet: boolean
  alipayPublicKey: string
  gateway: string
  alipayAuthMode: AlipayAuthMode
  kuaitongAccessKey: string
  kuaitongSecretSet: boolean
  kuaitongAuthType: KuaitongAuthType
  tencentApiKey: string
  tencentSecretSet: boolean
  tencentBaseUrl: string
  tencentUsePackage: boolean
  tencentProductCode: string
  xiaomuAppKey: string
  xiaomuSecretSet: boolean
  xiaomuBaseUrl: string
  xiaomuProductMode: XiaomuProductMode
  requireAppIds: number[]
  scope: RealnameScope
  purchaseRequired: boolean
  purchaseCheckOwner: boolean
  validityDays: number
  graceDays: number
  remindDays: number
  allowIdentityChange: boolean
  legacyPolicy: RealnameLegacyPolicy
  apps: RealnameAppItem[]
}

export interface UpdateRealnameConfigParams {
  enabled: boolean
  provider: RealnameProvider
  appId: string
  privateKey?: string
  alipayPublicKey: string
  gateway: string
  alipayAuthMode: AlipayAuthMode
  kuaitongAccessKey: string
  kuaitongSecret?: string
  kuaitongAuthType: KuaitongAuthType
  tencentApiKey: string
  tencentApiSecret?: string
  tencentBaseUrl: string
  tencentUsePackage: boolean
  xiaomuAppKey: string
  xiaomuAppSecret?: string
  xiaomuBaseUrl: string
  xiaomuProductMode: XiaomuProductMode
  requireAppIds: number[]
  scope: RealnameScope
  purchaseRequired: boolean
  purchaseCheckOwner: boolean
  validityDays: number
  graceDays: number
  remindDays: number
  allowIdentityChange: boolean
  legacyPolicy: RealnameLegacyPolicy
}

/** 切换全局实名范围前的影响面预估 */
export interface RealnameImpactData {
  windowDays: number
  unverifiedUsers: number
  unverifiedAgents: number
  affectedLicenses: number
  blockedRequests: number
  blockedLicenses: number
}

export interface MailConfigData {
  provider: 'qq' | 'aliyun' | 'custom'
  smtpHost: string
  smtpPort: number
  smtpSecure: 'ssl' | 'starttls' | 'none'
  smtpUsername: string
  smtpPassword?: string
  passwordSet: boolean
  smtpFromEmail: string
  smtpFromName: string
  enabledPurchaseSuccess: boolean
  enabledExpireReminder: boolean
  enabledLicenseOpened: boolean
  enabledRealnameExpire: boolean
  expireRemindDays: string
  purchaseSubject: string
  purchaseContent: string
  purchaseContentType: 'text' | 'html'
  expireSubject: string
  expireContent: string
  expireContentType: 'text' | 'html'
  openedSubject: string
  openedContent: string
  openedContentType: 'text' | 'html'
  realnameExpireSubject: string
  realnameExpireContent: string
  realnameExpireContentType: 'text' | 'html'
}

export interface MailContentTypeUpdateData {
  template: 'purchase' | 'expire' | 'opened' | 'realname'
  contentType: 'text' | 'html'
  content: string
}

export interface MailLogSearchParams {
  page: number
  pageSize: number
  eventType?: string
  status?: string
  keyword?: string
}

export interface MailLogItem {
  id: number
  eventType: string
  targetType: string
  targetId?: number
  licenseId?: number
  recipient: string
  subject: string
  content: string
  status: string
  error: string
  remindDays?: number
  createdAt: string
  sentAt: string
}

// 获取用户列表
export function fetchGetUserList(params: Api.SystemManage.UserSearchParams) {
  return request.get<Api.SystemManage.UserList>({
    url: '/api/user/list',
    params
  })
}

// 删除用户
export function fetchDeleteUser(id: number) {
  return request.del<null>({
    url: `/api/user/${id}`,
    showSuccessMessage: true
  })
}

// 获取角色列表
export function fetchGetRoleList(params: Api.SystemManage.RoleSearchParams) {
  return request.get<Api.SystemManage.RoleList>({
    url: '/api/role/list',
    params
  })
}

// 创建角色
export function fetchCreateRole(data: any) {
  return request.post<any>({
    url: '/api/role/create',
    data,
    showSuccessMessage: true
  })
}

// 更新角色
export function fetchUpdateRole(id: number, data: any) {
  return request.put<any>({
    url: `/api/role/${id}`,
    data,
    showSuccessMessage: true
  })
}

// 删除角色
export function fetchDeleteRole(id: number) {
  return request.del<any>({
    url: `/api/role/${id}`,
    showSuccessMessage: true
  })
}

// 获取角色菜单权限
export function fetchRoleMenus(id: number) {
  return request.get<any>({
    url: `/api/role/${id}/menus`
  })
}

// 更新角色菜单权限
export function fetchUpdateRoleMenus(id: number, menuIds: number[]) {
  return request.put<any>({
    url: `/api/role/${id}/menus`,
    data: { menuIds },
    showSuccessMessage: true
  })
}

// 获取菜单列表（侧边栏动态路由用）
export function fetchGetMenuList() {
  return request.get<AppRouteRecord[]>({
    url: '/api/system/menus'
  })
}

// 获取菜单管理列表（全量树形）
export function fetchMenuManageList() {
  return request.get<any>({
    url: '/api/menu/list'
  })
}

// 创建菜单
export function fetchCreateMenu(data: any) {
  return request.post<any>({
    url: '/api/menu/create',
    data,
    showSuccessMessage: true
  })
}

// 更新菜单
export function fetchUpdateMenu(id: number, data: any) {
  return request.put<any>({
    url: `/api/menu/${id}`,
    data,
    showSuccessMessage: true
  })
}

// 删除菜单
export function fetchDeleteMenu(id: number) {
  return request.del<any>({
    url: `/api/menu/${id}`,
    showSuccessMessage: true
  })
}

export function fetchPublicSystemConfig() {
  return request.get<SystemConfigData>({
    url: '/api/system-config/public',
    params: { _t: Date.now() },
    showErrorMessage: false
  })
}

export function fetchSystemConfig() {
  return request.get<SystemConfigData>({
    url: '/api/system/config'
  })
}

export function fetchUpdateSystemConfig(data: SystemConfigData) {
  return request.put<SystemConfigData>({
    url: '/api/system/config',
    data
  })
}

export function fetchPaymentConfig() {
  return request.get<PaymentConfigData>({
    url: '/api/system/payment-config'
  })
}

export function fetchUpdatePaymentConfig(data: PaymentConfigData) {
  return request.put<PaymentConfigData>({
    url: '/api/system/payment-config',
    data
  })
}

export interface PaymentTestOrderData {
  orderNo: string
  amount: number | string
  payType: EpayPayType
  payUrl?: string
  status?: 'pending' | 'paid' | 'failed' | 'cancelled'
  paidAt?: string
  gatewayTradeNo?: string
}

// 发起易支付测试订单，支付成功后只记录结果，不入账。
export function fetchCreatePaymentTest(data: { amount: number; payType: EpayPayType }) {
  return request.post<PaymentTestOrderData>({
    url: '/api/system/payment-config/test',
    data
  })
}

export function fetchPaymentTestStatus(orderNo: string) {
  return request.get<PaymentTestOrderData>({
    url: `/api/system/payment-config/test/${encodeURIComponent(orderNo)}`,
    showErrorMessage: false
  })
}

// ========== 易支付 V2 ==========

export function fetchPaymentV2Config() {
  return request.get<PaymentV2ConfigData>({
    url: '/api/system/payment-v2-config'
  })
}

export function fetchUpdatePaymentV2Config(data: PaymentV2ConfigData) {
  return request.put<PaymentV2ConfigData>({
    url: '/api/system/payment-v2-config',
    data
  })
}

export function fetchCreatePaymentV2Test(data: { amount: number; payType: EpayPayType }) {
  return request.post<PaymentTestOrderData>({
    url: '/api/system/payment-v2-config/test',
    data
  })
}

export function fetchPaymentV2TestStatus(orderNo: string) {
  return request.get<PaymentTestOrderData>({
    url: `/api/system/payment-v2-config/test/${encodeURIComponent(orderNo)}`,
    showErrorMessage: false
  })
}

export type PaymentOrderSubjectType = 'user' | 'agent' | 'test'
export type PaymentOrderStatus = 'pending' | 'paid' | 'failed' | 'cancelled'

export interface PaymentOrderSearchParams {
  page: number
  pageSize: number
  orderNo?: string
  subjectType?: string
  status?: string
  payMethod?: string
}

export interface PaymentOrderItem {
  orderNo: string
  subjectType: PaymentOrderSubjectType
  subjectId: number
  subjectName: string
  amount: number
  paidAmount: number
  payChannel: string
  payMethod: string
  status: PaymentOrderStatus
  gatewayTradeNo: string
  remark: string
  createdAt: string
  paidAt: string
}

export function fetchPaymentOrderList(params: PaymentOrderSearchParams) {
  return request.get<{ list: PaymentOrderItem[]; total: number; page: number; pageSize: number }>({
    url: '/api/system/payment-orders',
    params
  })
}

// 独立保存用户端功能开关，避免提交其他未保存配置。
export function fetchUpdateSystemFeatureSwitch(key: SystemFeatureSwitchKey, enabled: boolean) {
  return request.put<SystemFeatureSwitchData>({
    url: `/api/system/config/switch/${key}`,
    data: { enabled },
    showErrorMessage: false,
    showSuccessMessage: false
  })
}

// 获取实名认证配置
export function fetchRealnameConfig() {
  return request.get<RealnameConfigData>({
    url: '/api/system/realname-config'
  })
}

// 更新实名认证配置
export function fetchUpdateRealnameConfig(data: UpdateRealnameConfigParams) {
  return request.put<RealnameConfigData>({
    url: '/api/system/realname-config',
    data,
    showSuccessMessage: true
  })
}

export interface RealnameRecordItem {
  id: number
  ownerType: string
  ownerId: number
  ownerName: string
  ownerEmail: string
  provider: string
  realName: string
  idCard: string
  status: 'passed' | 'failed'
  failReason: string
  serialNo: string
  score: string
  /** init 首次认证 / review 复核 */
  recordType: 'init' | 'review'
  /** 本次认证产生的有效期，空表示永久 */
  expiresAt: string
  /** 主体当前实名状态 */
  validity: 'unverified' | 'permanent' | 'valid' | 'expiring' | 'expired'
  ownerExpiresAt: string
  createdAt: string
}

export interface RealnameRecordListResult {
  list: RealnameRecordItem[]
  total: number
  page: number
  pageSize: number
}

// 实名认证记录列表（展示层脱敏，由后端处理）
export function fetchRealnameRecords(params: {
  page: number
  pageSize: number
  keyword?: string
  provider?: string
  status?: 'passed' | 'failed'
  recordType?: 'init' | 'review'
  validity?: 'expired' | 'expiring' | 'permanent'
}) {
  return request.get<RealnameRecordListResult>({
    url: '/api/system/realname-records',
    params
  })
}

/** 切换全局实名范围前的影响面预估（可选传统计窗口天数） */
export function fetchRealnameImpact(days?: number) {
  return request.get<RealnameImpactData>({
    url: '/api/system/realname-impact',
    params: days ? { days } : undefined
  })
}

/** 批量重置存量实名有效期：scope=unbackfilled 仅未设有效期的存量，all 全部重算 */
export function resetRealnameLegacyValidity(scope: 'unbackfilled' | 'all' | 'clear') {
  return request.post<{ affected: number; scope: string }>({
    url: '/api/system/realname-legacy-reset',
    params: { scope }
  })
}

export function fetchMailConfig() {
  return request.get<MailConfigData>({
    url: '/api/system/mail-config'
  })
}

export function fetchUpdateMailConfig(data: MailConfigData) {
  return request.put<MailConfigData>({
    url: '/api/system/mail-config',
    data
  })
}

export function fetchUpdateMailContentType(data: MailContentTypeUpdateData) {
  return request.put<{ message?: string }>({
    url: '/api/system/mail-config/content-type',
    data
  })
}

export function fetchTestMailConfig(recipient: string) {
  return request.post<any>({
    url: '/api/system/mail-config/test',
    data: { recipient },
    showSuccessMessage: true
  })
}

export function fetchMailLogList(params: MailLogSearchParams) {
  return request.get<{ list: MailLogItem[]; total: number; page: number; pageSize: number }>({
    url: '/api/system/mail-logs',
    params
  })
}

export function fetchMailLogDetail(id: number) {
  return request.get<MailLogItem>({
    url: `/api/system/mail-logs/${id}`
  })
}

// ========== 应用商店（插件中心） ==========

/** 插件 / 首页模板作者信息 */
export interface TemplateAuthor {
  name: string
  url: string
  email: string
}

export interface PluginInfo {
  canEnable?: boolean
  id: string
  category: string
  name: string
  description: string
  homepage: string
  icon: string
  version: string
  official: boolean
  author?: TemplateAuthor
  enabled: boolean
  configured: boolean
  local: boolean
  remote: boolean
  source: string
  downloadUrl: string
}

export interface PluginCategoryGroup {
  category: string
  title: string
  plugins: PluginInfo[]
}

export interface PluginSource {
  id: number
  name: string
  url: string
  state: 'ok' | 'error' | 'unknown'
}

export interface PluginListData {
  categories: PluginCategoryGroup[]
  sources: PluginSource[]
}

export interface PluginListParams {
  source?: string
  q?: string
}

export function fetchPluginList(params?: PluginListParams) {
  return request.get<PluginListData>({
    url: '/api/system/plugins',
    params
  })
}

export function fetchAddPluginSource(name: string, url: string) {
  return request.post<null>({
    url: '/api/system/plugin-sources',
    data: { name, url }
  })
}

export function fetchDeletePluginSource(id: number) {
  return request.del<null>({
    url: `/api/system/plugin-sources/${id}`
  })
}

export function fetchDownloadPlugin(id: string) {
  return request.post<null>({
    url: `/api/system/plugins/${id}/download`,
    timeout: 120_000
  })
}

export function fetchTogglePlugin(id: string, enabled: boolean) {
  return request.post<null>({
    url: `/api/system/plugins/${id}/toggle`,
    data: { enabled }
  })
}

export interface HomeTemplateInfo {
  updateAvailable?: boolean
  id: number | 'default'
  catalogId?: string
  templateId: string
  name: string
  description: string
  version: string
  source: string
  sourceUrl?: string
  sourceType?: 'json' | 'git' | 'builtin' | 'upload'
  format?: 'json' | 'zip'
  previewUrl?: string
  author?: TemplateAuthor
  sha256?: string
  schemaVersion?: number
  enabled: boolean
  installed: boolean
  available: boolean
  updatedAt?: string
}

export interface HomeTemplateListData {
  warning?: string
  list: HomeTemplateInfo[]
}

export function fetchHomeTemplateList(refresh = false) {
  return request.get<HomeTemplateListData>({
    url: '/api/system/home-templates',
    params: refresh ? { refresh: '1' } : undefined
  })
}

export function fetchUploadHomeTemplate(data: FormData) {
  return request.post<{ id: number }>({
    url: '/api/system/home-templates/upload',
    data,
    timeout: 120_000
  })
}

// ========== 内置软件源（内嵌远程仓库） ==========

/** 内置源提供的首页模板目录条目 */
export interface SoftwareSourceTemplate {
  id: string
  name: string
  description: string
  version: string
  previewUrl: string
  author: TemplateAuthor
  schemaVersion: number
  sha256: string
  templateUrl: string
  templatePath: string
}

/** 内置源提供的插件目录条目 */
export interface SoftwareSourcePlugin {
  id: string
  category: string
  name: string
  description: string
  homepage: string
  icon: string
  version: string
  official: boolean
  author: TemplateAuthor
}

export interface SoftwareSourceData {
  name: string
  sourceType: string
  schemaVersion: number
  homeTemplates: SoftwareSourceTemplate[]
  plugins: SoftwareSourcePlugin[]
}

/** 读取 Go 后端内置软件源的目录清单（公开接口，无需鉴权） */
export function fetchSoftwareSourcePlugins() {
  return request.get<SoftwareSourceData>({
    url: '/api/software-source/plugins'
  })
}

export function fetchRefreshPluginSource(id: number) {
  return request.post<{ plugins: number; homeTemplates: number }>({
    url: `/api/system/plugin-sources/${id}/refresh`
  })
}

export function fetchDownloadHomeTemplate(id: number | 'default') {
  return request.get<Blob>({
    url: `/api/system/home-templates/${id}/download`,
    responseType: 'blob',
    timeout: 120_000
  })
}

export function fetchInstallHomeTemplate(id: number | 'default') {
  return request.post<null>({ url: `/api/system/home-templates/${id}/install`, timeout: 120_000 })
}

export function fetchUninstallHomeTemplate(id: number | 'default') {
  return request.post<null>({ url: `/api/system/home-templates/${id}/uninstall` })
}

export function fetchDisableHomeTemplate(id: number | 'default') {
  return request.post<null>({ url: `/api/system/home-templates/${id}/disable` })
}

export function fetchEnableHomeTemplate(id: number | 'default') {
  return request.post<null>({
    url: `/api/system/home-templates/${id}/enable`,
    timeout: 120_000
  })
}
