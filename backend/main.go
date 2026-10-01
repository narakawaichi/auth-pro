package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"auto_pro/appstore"
	"auto_pro/config"
	"auto_pro/handler"
	"auto_pro/middleware"

	"github.com/gin-gonic/gin"
)

//go:embed static/*
var staticFS embed.FS

//go:embed sdk
var sdkFS embed.FS

func main() {
	r := gin.Default()
	appStoreServer := appstore.NewServer(handler.NewAppStoreTemplateRepository())

	sdkExamplesFS, err := fs.Sub(sdkFS, "sdk")
	if err != nil {
		log.Fatal("failed to load sdk examples:", err)
	}

	// CORS
	r.Use(middleware.Cors())

	// API 路由
	api := r.Group("/api")
	{
		appStoreServer.RegisterAdminRoutes(api)

		// 安装状态检查保持公开（前端路由守卫依赖）
		install := api.Group("/install")
		{
			install.GET("/status", handler.InstallStatus)

			// 安装写接口：已安装后整体关闭；未安装时拒绝跨域调用
			installProtected := install.Group("/")
			installProtected.Use(middleware.InstallGuard())
			{
				installProtected.POST("/test-db", handler.InstallTestDB)
				installProtected.POST("/init-tables", handler.InstallInitTables)
				installProtected.POST("/create-admin", handler.InstallCreateAdmin)
			}
		}

		// 公开系统配置（登录前可读取品牌信息）
		api.GET("/system-config/public", handler.PublicSystemConfig)
		api.GET("/system/version", handler.SystemVersion)

		// 公开授权校验（SDK 调用，无需后台登录）
		api.POST("/license/verify", handler.LicenseVerify)
		// 应用客户端使用签名授权检查版本，并通过短期令牌下载本地更新包。
		api.POST("/app/version/check", handler.AppVersionCheck)
		api.GET("/app/version/download", handler.AppVersionDownload)

		// 易支付回调（支付网关调用，无需登录）
		api.Any("/payment/easypay/notify", handler.EpayNotify)
		api.Any("/payment/easypay/return", handler.EpayReturn)

		// 易支付 V2 回调（支付网关调用，无需登录）
		api.Any("/payment/easypay-v2/notify", handler.EpayV2Notify)
		api.Any("/payment/easypay-v2/return", handler.EpayV2Return)

		// 快瞳 / 腾讯云增强人脸拍照提交（扫码手机端调用，token 即凭证，无需登录）
		api.POST("/realname/face/submit", handler.RealnameFaceSubmit)

		// 支付宝信息比对授权回调（scope=id_verify，redirect_uri 指向此接口）
		api.GET("/realname/alipay-callback", handler.AlipayRealnameCallback)

		// 认证相关（无需鉴权）
		auth := api.Group("/auth")
		{
			auth.POST("/login", handler.Login)
		}

		// 代理端（无需管理员鉴权）
		agentAuth := api.Group("/agent-panel")
		{
			agentAuth.POST("/login", handler.AgentPanelLogin)
		}

		// 代理端（需鉴权）
		agentSecured := api.Group("/agent-panel")
		agentSecured.Use(middleware.JWTAuth(), middleware.RequireFreshPassword("agents"))
		{
			agentSecured.GET("/apps", handler.AgentPanelAppList)
			agentSecured.GET("/apps/purchase", handler.AgentPanelPurchaseApps)
			agentSecured.GET("/users/options", handler.AgentPanelUserOptions)
			agentSecured.GET("/licenses", handler.AgentPanelLicenseList)
			agentSecured.POST("/cards/redeem", handler.AgentLicenseCardRedeem)
			agentSecured.PUT("/licenses/:id", handler.AgentPanelLicenseUpdate)
			agentSecured.POST("/licenses/:id/refresh-key", handler.AgentPanelLicenseRefreshKey)
			agentSecured.GET("/licenses/:id/sites", handler.AgentLicenseSiteList)
			agentSecured.DELETE("/licenses/:id/sites/:siteId", handler.AgentLicenseSiteUnbind)
			agentSecured.GET("/balance", handler.AgentPanelBalance)
			agentSecured.GET("/profile", handler.AgentPanelProfile)
			agentSecured.PUT("/profile", handler.AgentPanelUpdateProfile)
			agentSecured.POST("/change-password", handler.AgentPanelChangePassword)
			agentSecured.POST("/realname/init", handler.AgentRealnameInit)
			agentSecured.GET("/realname/query", handler.AgentRealnameQuery)
			agentSecured.GET("/dashboard/stats", handler.AgentPanelStats)
			agentSecured.GET("/dashboard/info", handler.AgentPanelInfo)
			agentSecured.GET("/dashboard/trend", handler.AgentPanelTrend)
			agentSecured.GET("/dashboard/app-dist", handler.AgentPanelAppDist)
			agentSecured.GET("/dashboard/recent-licenses", handler.AgentPanelRecentLicenses)
			agentSecured.GET("/finance/overview", handler.AgentPanelFinanceOverview)
			agentSecured.GET("/finance/quotas", handler.AgentPanelFinanceQuotas)
			agentSecured.GET("/finance/transactions", handler.AgentPanelFinanceTransactions)
			agentSecured.GET("/recharge/options", handler.AgentPanelRechargeOptions)
			agentSecured.POST("/recharge/orders", handler.AgentPanelRechargeCreate)
			agentSecured.GET("/recharge/orders/:orderNo", handler.AgentPanelRechargeStatus)
			agentSecured.GET("/purchase/pay-options", handler.AgentPanelPurchasePayOptions)
			agentSecured.POST("/purchase", handler.AgentPanelPurchase)
			agentSecured.GET("/purchase/orders/:orderNo", handler.AgentPanelPurchaseOrderStatus)
			agentSecured.GET("/licenses/:id/versions", handler.PanelLicenseVersions)
			agentSecured.POST("/licenses/:id/versions/:versionId/download-url", handler.PanelLicenseVersionDownloadURL)
			agentSecured.POST("/tickets", handler.PanelTicketCreate)
			agentSecured.GET("/tickets", handler.PanelTicketList)
			agentSecured.GET("/tickets/unread-count", handler.PanelTicketUnreadCount)
			agentSecured.GET("/tickets/:id", handler.PanelTicketDetail)
			agentSecured.POST("/tickets/:id/replies", handler.PanelTicketReply)
			agentSecured.PUT("/tickets/:id/close", handler.PanelTicketClose)
		}

		// 用户端（无需管理员鉴权）
		userAuth := api.Group("/user-panel")
		{
			userAuth.POST("/login", handler.UserLogin)
			userAuth.GET("/license-query", handler.PublicUserLicenseQuery)
			userAuth.GET("/agent-query", handler.PublicAgentQuery)
			userAuth.GET("/target-query", handler.PublicTargetQuery)
			userAuth.POST("/register/email-code", handler.UserSendRegisterEmailCode)
			userAuth.POST("/register", handler.UserRegister)
			userAuth.POST("/forgot-password", handler.UserForgotPassword)
			userAuth.POST("/reset-password", handler.UserResetPassword)
		}
		api.GET("/home-template/active", handler.PublicActiveHomeTemplate)
		api.GET("/home-template/assets/:id/:revision/*filepath", handler.PublicHomeTemplateAsset)
		// 内置软件源（内嵌远程仓库）：目录清单与模板示例图片
		api.GET("/software-source/plugins", handler.PublicSoftwareSourcePlugins)
		api.GET("/software-source/previews/:file", handler.PublicSoftwareSourcePreview)
		api.GET("/software-source/templates/:id/preview", handler.PublicSoftwareSourceTemplatePreview)
		api.POST("/internal/software-source/cache/invalidate", handler.InternalSoftwareSourceCacheInvalidate)

		// 外部广告投放（上游未开放 CORS，由后端代理转发并缓存）
		api.GET("/advertisements", handler.PublicAdvertisements)

		// 用户端（需鉴权）
		userSecured := api.Group("/user-panel")
		userSecured.Use(middleware.JWTAuth(), middleware.RequireActiveUser(), middleware.RequireFreshPassword("users"))
		{
			userSecured.GET("/dashboard", handler.UserDashboard)
			userSecured.GET("/licenses", handler.UserLicenseList)
			userSecured.POST("/cards/redeem", handler.UserLicenseCardRedeem)
			userSecured.PUT("/licenses/:id/target", handler.UserLicenseUpdateTarget)
			userSecured.POST("/licenses/:id/refresh-key", handler.UserLicenseRefreshKey)
			userSecured.GET("/licenses/:id/sites", handler.UserLicenseSiteList)
			userSecured.DELETE("/licenses/:id/sites/:siteId", handler.UserLicenseSiteUnbind)
			userSecured.GET("/apps", handler.UserAppList)
			userSecured.GET("/apps/purchase", handler.UserAppListForPurchase)
			userSecured.GET("/balance", handler.UserGetBalance)
			userSecured.GET("/recharge/options", handler.UserRechargeOptions)
			userSecured.POST("/recharge/orders", handler.UserRechargeCreate)
			userSecured.GET("/recharge/orders/:orderNo", handler.UserRechargeStatus)
			userSecured.GET("/recharge-v2/options", handler.UserRechargeV2Options)
			userSecured.POST("/recharge-v2/orders", handler.UserRechargeV2Create)
			userSecured.GET("/purchase/pay-options", handler.UserPurchasePayOptions)
			userSecured.POST("/purchase", handler.UserPurchase)
			userSecured.GET("/purchase/orders/:orderNo", handler.UserPurchaseOrderStatus)
			userSecured.GET("/agent-upgrade/levels", handler.UserAgentUpgradeLevels)
			userSecured.POST("/agent-upgrade/orders", handler.UserAgentUpgradeCreate)
			userSecured.GET("/agent-upgrade/orders/:orderNo", handler.UserAgentUpgradeOrderStatus)
			userSecured.DELETE("/agent-upgrade/orders/:orderNo", handler.UserAgentUpgradeCancel)
			userSecured.GET("/profile", handler.UserProfile)
			userSecured.PUT("/profile", handler.UserUpdateProfile)
			userSecured.POST("/change-password", handler.UserChangePassword)
			userSecured.POST("/realname/init", handler.UserRealnameInit)
			userSecured.GET("/realname/query", handler.UserRealnameQuery)
			userSecured.GET("/licenses/:id/versions", handler.PanelLicenseVersions)
			userSecured.POST("/licenses/:id/versions/:versionId/download-url", handler.PanelLicenseVersionDownloadURL)
			userSecured.POST("/tickets", handler.PanelTicketCreate)
			userSecured.GET("/tickets", handler.PanelTicketList)
			userSecured.GET("/tickets/unread-count", handler.PanelTicketUnreadCount)
			userSecured.GET("/tickets/:id", handler.PanelTicketDetail)
			userSecured.POST("/tickets/:id/replies", handler.PanelTicketReply)
			userSecured.PUT("/tickets/:id/close", handler.PanelTicketClose)
		}

		// 需要鉴权的路由（仅管理员角色）
		secured := api.Group("/")
		secured.Use(middleware.JWTAuth(), middleware.RequireAdmin(), middleware.RequireFreshPassword("admins"))

		// 超级管理员专属接口：后端强制对齐前端 R_SUPER 权限，防止 R_ADMIN 越权直接调用。
		superSecured := secured.Group("")
		superSecured.Use(middleware.RequireSuperAdmin())
		{
			secured.GET("/user/info", handler.GetUserInfo)
			secured.PUT("/user/info", handler.UpdateUserInfo)
			secured.POST("/user/change-password", handler.ChangePassword)
			secured.GET("/user/list", handler.AdminUserList)
			secured.POST("/user/create", handler.AdminUserCreate)
			secured.PUT("/user/:id", handler.AdminUserUpdate)
			secured.PUT("/user/:id/toggle", handler.AdminUserToggle)
			secured.DELETE("/user/:id", handler.AdminUserDelete)
			secured.POST("/user/:id/impersonate", handler.AdminImpersonateUser)
			secured.GET("/system/menus", handler.GetMenuList)
			superSecured.GET("/system/config", handler.AdminSystemConfig)
			superSecured.PUT("/system/config", handler.AdminSystemConfigUpdate)
			superSecured.PUT("/system/config/switch/:key", handler.AdminSystemFeatureSwitchUpdate)
			superSecured.GET("/system/payment-config", handler.AdminPaymentConfig)
			superSecured.PUT("/system/payment-config", handler.AdminPaymentConfigUpdate)
			superSecured.POST("/system/payment-config/test", handler.AdminPaymentTestCreate)
			superSecured.GET("/system/payment-config/test/:orderNo", handler.AdminPaymentTestStatus)
			superSecured.GET("/system/payment-v2-config", handler.AdminPaymentV2Config)
			superSecured.PUT("/system/payment-v2-config", handler.AdminPaymentV2ConfigUpdate)
			superSecured.POST("/system/payment-v2-config/test", handler.AdminPaymentV2TestCreate)
			superSecured.GET("/system/payment-v2-config/test/:orderNo", handler.AdminPaymentV2TestStatus)
			secured.GET("/system/payment-orders", handler.AdminPaymentOrderList)
			superSecured.GET("/system/plugins", handler.AdminPluginList)
			superSecured.POST("/system/plugins/:id/toggle", handler.AdminPluginToggle)
			superSecured.POST("/system/plugins/:id/download", handler.AdminPluginDownload)
			superSecured.POST("/system/plugin-sources", handler.AdminPluginSourceAdd)
			superSecured.DELETE("/system/plugin-sources/:id", handler.AdminPluginSourceDelete)
			superSecured.POST("/system/plugin-sources/:id/refresh", handler.AdminPluginSourceRefresh)
			superSecured.GET("/system/home-templates", handler.AdminHomeTemplateList)
			superSecured.POST("/system/home-templates/upload", handler.AdminHomeTemplateUpload)
			superSecured.POST("/system/home-templates/:id/enable", handler.AdminHomeTemplateEnable)
			superSecured.GET("/system/home-templates/:id/download", handler.AdminHomeTemplateDownload)
			superSecured.POST("/system/home-templates/:id/install", handler.AdminHomeTemplateInstall)
			superSecured.POST("/system/home-templates/:id/disable", handler.AdminHomeTemplateDisable)
			superSecured.POST("/system/home-templates/:id/uninstall", handler.AdminHomeTemplateUninstall)
			superSecured.GET("/system/realname-config", handler.AdminRealnameConfig)
			superSecured.PUT("/system/realname-config", handler.AdminRealnameConfigUpdate)
			superSecured.GET("/system/realname-impact", handler.AdminRealnameImpactPreview)
			superSecured.POST("/system/realname-legacy-reset", handler.AdminRealnameLegacyReset)
			superSecured.POST("/system/realname-products", handler.AdminRealnameProducts)
			superSecured.GET("/system/realname-records", handler.AdminRealnameRecordList)
			superSecured.GET("/system/mail-config", handler.AdminMailConfig)
			superSecured.PUT("/system/mail-config", handler.AdminMailConfigUpdate)
			superSecured.PUT("/system/mail-config/content-type", handler.AdminMailContentTypeUpdate)
			superSecured.POST("/system/mail-config/test", handler.AdminMailConfigTest)
			superSecured.GET("/system/mail-logs", handler.AdminMailLogList)
			superSecured.GET("/system/mail-logs/:id", handler.AdminMailLogDetail)
			superSecured.GET("/system/update/status", handler.AdminOnlineUpdateStatus)
			superSecured.GET("/system/update/history", handler.AdminOnlineUpdateHistory)
			superSecured.POST("/system/update/check", handler.AdminOnlineUpdateCheck)
			superSecured.POST("/system/update/apply", handler.AdminOnlineUpdateApply)
			superSecured.GET("/system/update/jobs/:id", handler.AdminOnlineUpdateJob)
			secured.GET("/license/dashboard", handler.LicenseDashboard)
			secured.GET("/dashboard/overview", handler.AdminDashboardOverview)
			secured.GET("/dashboard/cards", handler.AdminDashboardCards)
			secured.GET("/dashboard/trend", handler.AdminDashboardTrend)
			secured.GET("/dashboard/license-status", handler.AdminDashboardLicenseStatus)
			secured.GET("/dashboard/payment-methods", handler.AdminDashboardPaymentMethods)
			secured.GET("/dashboard/agent-metrics", handler.AdminDashboardAgentMetrics)
			secured.GET("/dashboard/user-metrics", handler.AdminDashboardUserMetrics)
			secured.GET("/dashboard/app-metrics", handler.AdminDashboardAppMetrics)
			secured.GET("/dashboard/app-ranking", handler.AdminDashboardAppRanking)
			secured.GET("/dashboard/agent-ranking", handler.AdminDashboardAgentRanking)
			secured.GET("/dashboard/activities", handler.AdminDashboardActivities)
			secured.GET("/dashboard/quick-entries", handler.AdminDashboardQuickEntries)
			secured.GET("/license/list", handler.LicenseList)
			secured.GET("/license/:id/sites", handler.AdminLicenseSiteList)
			secured.DELETE("/license/:id/sites/:siteId", handler.AdminLicenseSiteUnbind)
			secured.GET("/license/query-by-user", handler.UserLicenseQuery)
			secured.GET("/license/cards/batches", handler.AdminLicenseCardBatchList)
			secured.POST("/license/cards/batches", handler.AdminLicenseCardBatchCreate)
			secured.PUT("/license/cards/batches/:id/status", handler.AdminLicenseCardBatchToggle)
			secured.DELETE("/license/cards/batches/:id", handler.AdminLicenseCardBatchDelete)
			secured.GET("/license/cards/batches/:id/cards", handler.AdminLicenseCardList)
			secured.GET("/license/cards/batches/:id/export", handler.AdminLicenseCardExport)
			secured.PUT("/license/cards/:id/status", handler.AdminLicenseCardToggle)
			secured.GET("/license/apps", handler.AppList)
			secured.GET("/license/owners", handler.LicenseOwnerOptions)
			secured.GET("/app/list", handler.AppManageList)
			secured.POST("/app/create", handler.AppCreate)
			secured.PUT("/app/:id", handler.AppUpdate)
			secured.PUT("/app/:id/license-required", handler.AppLicenseRequiredUpdate)
			secured.PUT("/app/:id/reset-secret", handler.AppResetSecret)
			secured.DELETE("/app/:id", handler.AppDelete)
			secured.GET("/app/:id/versions", handler.AppVersionList)
			secured.POST("/app/:id/versions", handler.AppVersionCreate)
			secured.PUT("/app/:id/versions/:versionId", handler.AppVersionUpdate)
			secured.DELETE("/app/:id/versions/:versionId", handler.AppVersionDelete)
			secured.POST("/app/:id/versions/:versionId/download-url", handler.AppVersionAdminDownloadURL)
			secured.GET("/plan/list", handler.PlanList)
			secured.POST("/plan/create", handler.PlanCreate)
			secured.PUT("/plan/:id", handler.PlanUpdate)
			secured.PUT("/plan/:id/toggle", handler.PlanToggle)
			secured.DELETE("/plan/:id", handler.PlanDelete)
			secured.GET("/promotion/campaigns", handler.AdminPromotionCampaignList)
			secured.POST("/promotion/campaigns", handler.AdminPromotionCampaignCreate)
			secured.PUT("/promotion/campaigns/:id", handler.AdminPromotionCampaignUpdate)
			secured.PUT("/promotion/campaigns/:id/toggle", handler.AdminPromotionCampaignToggle)
			secured.DELETE("/promotion/campaigns/:id", handler.AdminPromotionCampaignDelete)
			secured.GET("/verify-log/list", handler.VerifyLogList)
			secured.DELETE("/verify-log/clear", handler.VerifyLogClear)
			// SDK 示例包下载（后台 SDK 示例页）
			secured.GET("/sdk/:lang/download", handler.AdminSdkDownload(sdkExamplesFS))
			secured.GET("/agent/list", handler.AgentList)
			secured.POST("/agent/create", handler.AgentCreate)
			secured.PUT("/agent/:id", handler.AgentUpdate)
			secured.PUT("/agent/:id/toggle", handler.AgentToggle)
			secured.POST("/agent/:id/recharge", handler.AgentRecharge)
			secured.DELETE("/agent/:id", handler.AgentDelete)
			secured.POST("/agent/:id/impersonate", handler.AdminImpersonateAgent)
			secured.GET("/agent/select-list", handler.AgentSelectList)
			secured.GET("/agent-level/list", handler.AgentLevelList)
			secured.GET("/agent-level/select-list", handler.AgentLevelSelectList)
			secured.POST("/agent-level/create", handler.AgentLevelCreate)
			secured.PUT("/agent-level/:id", handler.AgentLevelUpdate)
			secured.DELETE("/agent-level/:id", handler.AgentLevelDelete)
			secured.GET("/admin/agent-upgrade/stats", handler.AdminAgentUpgradeStats)
			secured.GET("/admin/agent-upgrade/orders", handler.AdminAgentUpgradeOrderList)
			secured.GET("/admin/agent-upgrade/conversions", handler.AdminAccountConversionList)
			secured.GET("/admin/agent-upgrade/conversions/:id", handler.AdminAccountConversionDetail)
			secured.GET("/transaction/list", handler.TransactionList)
			secured.GET("/transaction/stats", handler.TransactionStats)
			secured.GET("/quota/list", handler.QuotaList)
			secured.POST("/quota/create", handler.QuotaCreate)
			secured.PUT("/quota/:id", handler.QuotaUpdate)
			secured.DELETE("/quota/:id", handler.QuotaDelete)
			secured.POST("/license/create", handler.LicenseCreate)
			secured.PUT("/license/:id", handler.LicenseUpdate)
			secured.PUT("/license/:id/toggle", handler.LicenseToggle)
			secured.DELETE("/license/:id", handler.LicenseDelete)

			// 反盗版 - 追踪
			secured.GET("/piracy/tracking/stats", handler.PiracyTrackingStats)
			secured.GET("/piracy/tracking/list", handler.PiracyTrackingList)
			secured.GET("/piracy/tracking/:id", handler.PiracyTrackingDetail)
			secured.POST("/piracy/tracking/create", handler.PiracyTrackingCreate)
			secured.PUT("/piracy/tracking/:id/block", handler.PiracyTrackingBlock)
			secured.PUT("/piracy/tracking/:id/unblock", handler.PiracyTrackingUnblock)
			secured.POST("/piracy/tracking/batch-block", handler.PiracyTrackingBatchBlock)
			// 反盗版 - 告警
			secured.GET("/piracy/alert/stats", handler.PiracyAlertStats)
			secured.GET("/piracy/alert/list", handler.PiracyAlertList)
			secured.PUT("/piracy/alert/:id/mark", handler.PiracyAlertMark)
			secured.POST("/piracy/alert/batch-mark", handler.PiracyAlertBatchMark)
			// 反盗版 - 黑名单
			secured.GET("/piracy/blacklist/list", handler.PiracyBlacklistList)
			secured.POST("/piracy/blacklist/create", handler.PiracyBlacklistCreate)
			secured.PUT("/piracy/blacklist/:id", handler.PiracyBlacklistUpdate)
			secured.DELETE("/piracy/blacklist/:id", handler.PiracyBlacklistDelete)
			secured.POST("/piracy/blacklist/batch-delete", handler.PiracyBlacklistBatchDelete)
			// 反盗版 - 数据报表
			secured.GET("/piracy/report/overview", handler.ReportOverview)
			// 角色管理
			superSecured.GET("/role/list", handler.RoleList)
			superSecured.POST("/role/create", handler.RoleCreate)
			superSecured.PUT("/role/:id", handler.RoleUpdate)
			superSecured.DELETE("/role/:id", handler.RoleDelete)
			superSecured.GET("/role/:id/menus", handler.RoleMenus)
			superSecured.PUT("/role/:id/menus", handler.RoleUpdateMenus)
			// 菜单管理
			superSecured.GET("/menu/list", handler.MenuManageList)
			superSecured.POST("/menu/create", handler.MenuManageCreate)
			superSecured.PUT("/menu/:id", handler.MenuManageUpdate)
			superSecured.DELETE("/menu/:id", handler.MenuManageDelete)

			secured.GET("/ticket/list", handler.AdminTicketList)
			secured.GET("/ticket/unread-count", handler.AdminTicketUnreadCount)
			secured.GET("/ticket/:id", handler.AdminTicketDetail)
			secured.POST("/ticket/:id/reply", handler.AdminTicketReply)
			secured.PUT("/ticket/:id/status", handler.AdminTicketStatus)
		}
	}

	// 快瞳 / 腾讯云增强人脸扫码拍照落地页（无需登录，token 即凭证）
	r.GET("/realname-face", handler.RealnameFacePage)

	// 静态文件服务：优先使用部署目录下的真实前端产物（AUTO_PRO_FRONTEND_DIR
	// 或数据目录里的 frontend/current），不存在时退回内嵌产物。
	// 这样即使支付回跳意外落到后端地址，也能拿到最新页面而不是旧 embed。
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal("failed to load static files:", err)
	}
	embedServer := http.FileServer(http.FS(staticSub))
	frontendDir := config.GetFrontendDir()
	var diskServer http.Handler
	if info, err := os.Stat(filepath.Join(frontendDir, "index.html")); err == nil && !info.IsDir() {
		diskServer = http.FileServer(http.Dir(frontendDir))
		log.Printf("Serving frontend from disk: %s", frontendDir)
	}
	softwareSourceAdminURL := config.GetSoftwareSourceAdminURL()
	r.GET(appstore.PagePrefix, func(c *gin.Context) {
		c.Redirect(http.StatusFound, softwareSourceAdminURL)
	})
	r.GET(appstore.PagePrefix+"/*filepath", func(c *gin.Context) {
		target := softwareSourceAdminURL
		requestedPath := strings.Trim(strings.TrimSpace(c.Param("filepath")), "/")
		if requestedPath == "dashboard" || requestedPath == "templates" || requestedPath == "apps" || requestedPath == "sources" {
			target += requestedPath
		}
		c.Redirect(http.StatusFound, target)
	})
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "请求的资源不存在"})
			return
		}

		cleanPath := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")

		if diskServer != nil {
			if cleanPath != "." {
				if info, err := os.Stat(filepath.Join(frontendDir, cleanPath)); err == nil && !info.IsDir() {
					if handler.IsInstalledPackageFile(filepath.Join(frontendDir, cleanPath)) {
						c.Status(http.StatusNotFound)
						return
					}
					diskServer.ServeHTTP(c.Writer, c.Request)
					return
				}
			}
			c.File(filepath.Join(frontendDir, "index.html"))
			return
		}

		if cleanPath != "." {
			if _, err := fs.Stat(staticSub, cleanPath); err == nil {
				embedServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		indexHTML, err := fs.ReadFile(staticSub, "index.html")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "前端入口文件不存在"})
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	// 启动后台邮件到期提醒任务
	handler.StartMailReminderWorker()
	handler.StartPurchaseOrderExpiryWorker()
	handler.StartRealnameExpiryWorker()

	// 兜底迁移：补齐购买订单字段，修正历史线上购买流水与价格快照
	func() {
		db, err := config.DB()
		if err != nil {
			return
		}
		if err := handler.EnsureAppVersionsTable(db); err != nil {
			log.Printf("ensure app_versions table failed: %v", err)
		}
		if err := handler.EnsureAppPurchaseLicenseTypesColumn(db); err != nil {
			log.Printf("ensure app purchase license types failed: %v", err)
		}
		if err := handler.EnsurePurchaseOrderUserIDColumn(db); err != nil {
			log.Printf("ensure user_id column failed: %v", err)
		}
		if err := handler.EnsureLicensePurchasePriceSnapshotSchema(db); err != nil {
			log.Printf("ensure license purchase price snapshots failed: %v", err)
		}
		if err := handler.EnsurePromotionCampaignSchema(db); err != nil {
			log.Printf("ensure promotion campaign schema failed: %v", err)
		}
		if err := handler.EnsureAccountUpgradeSchema(db); err != nil {
			log.Printf("ensure account upgrade schema failed: %v", err)
		}
		if err := ensureLicenseSiteLimitSchema(db); err != nil {
			log.Printf("ensure license site limit schema failed: %v", err)
		}
		handler.BackfillLicensePurchaseTransactions(db)
	}()

	// 启动
	host := config.GetHost()
	port := config.GetPort()
	log.Printf("Server starting on %s:%s", host, port)
	if err := r.Run(host + ":" + port); err != nil {
		log.Fatal("Server failed:", err)
	}
}
