<template>
  <div class="user-profile">
    <el-row :gutter="20">
      <!-- 左侧：用户摘要 -->
      <el-col :xs="24" :md="8" :lg="7">
        <div class="art-card profile-side">
          <el-avatar :size="76" class="side-avatar">
            <iconify-icon icon="ri:user-3-fill" width="38" />
          </el-avatar>
          <div class="side-name">
            <span class="nickname">{{ profileForm.nickname || '未设置昵称' }}</span>
            <el-tag
              v-if="realnameVerified"
              type="success"
              size="small"
              effect="light"
              class="verified-tag"
            >
              <iconify-icon icon="ri:shield-check-fill" width="12" />
              已实名
            </el-tag>
            <el-tag v-else-if="realnameEnabled" type="warning" size="small" effect="light">
              未实名
            </el-tag>
          </div>
          <div class="side-meta">
            <div class="meta-row">
              <iconify-icon icon="ri:mail-line" width="14" />
              <span class="meta-text">{{ profileForm.email || '-' }}</span>
            </div>
            <div class="meta-row meta-id" @click="handleCopyId" title="点击复制">
              <iconify-icon icon="ri:file-copy-line" width="14" />
              <span class="meta-text">ID: {{ profileForm.userId }}</span>
            </div>
          </div>
          <el-button size="small" plain class="side-avatar-btn">更换头像</el-button>
        </div>
      </el-col>

      <!-- 右侧：功能分区 -->
      <el-col :xs="24" :md="16" :lg="17">
        <div class="art-card profile-main">
          <el-tabs v-model="activeTab" class="profile-tabs">
            <el-tab-pane label="基本信息" name="profile">
              <el-form :model="profileForm" label-width="80px" class="profile-form">
                <el-form-item label="用户ID">
                  <el-input :model-value="profileForm.userId" disabled>
                    <template #append>
                      <el-button @click="handleCopyId">复制</el-button>
                    </template>
                  </el-input>
                </el-form-item>
                <el-form-item label="邮箱">
                  <el-input v-model="profileForm.email" />
                </el-form-item>
                <el-form-item label="手机号">
                  <el-input v-model="profileForm.phone" placeholder="可用于登录，留空表示不绑定" />
                </el-form-item>
                <el-form-item label="昵称">
                  <el-input v-model="profileForm.nickname" />
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="handleSaveProfile">保存修改</el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>

            <el-tab-pane name="realname">
              <template #label>
                实名认证
                <el-badge v-if="realnameEnabled && !realnameVerified" is-dot class="tab-badge" />
              </template>

              <div v-if="realnameVerified" class="realname-done">
                <div class="realname-badge">
                  <iconify-icon icon="ri:shield-check-fill" width="18" />
                  <span>已完成实名认证</span>
                </div>
                <el-alert
                  v-if="realnameStatus === 'grace'"
                  type="warning"
                  :closable="false"
                  show-icon
                  title="实名信息已过期，当前处于宽限期"
                  description="宽限期内仍可正常使用，请尽快在下方重新认证以恢复完整权限"
                  class="mb-3"
                />
                <div class="realname-info">
                  <div
                    ><label>姓名</label><span>{{ realnameInfo.realName }}</span></div
                  >
                  <div
                    ><label>身份证号</label><span>{{ realnameInfo.realIdCard }}</span></div
                  >
                  <div v-if="realnameInfo.realnameAt"
                    ><label>认证时间</label><span>{{ realnameInfo.realnameAt }}</span></div
                  >
                  <div
                    ><label>有效期</label
                    ><span>{{ realnameInfo.realnameExpiresAt || '永久有效' }}</span></div
                  >
                </div>
                <el-divider />
                <div class="realname-reverify">
                  <p class="reverify-tip">
                    如需更换信息或提前复核，可提交后重新认证（会覆盖当前实名信息，历史记录保留）
                  </p>
                  <el-form :model="realnameForm" label-width="80px" class="profile-form">
                    <el-form-item label="真实姓名">
                      <el-input
                        v-model="realnameForm.realName"
                        maxlength="30"
                        placeholder="请输入与身份证一致的姓名"
                      />
                    </el-form-item>
                    <el-form-item label="身份证号">
                      <el-input
                        v-model="realnameForm.idCard"
                        maxlength="18"
                        placeholder="请输入 18 位身份证号"
                      />
                    </el-form-item>
                    <el-form-item v-if="needRealnameMobile" label="手机号">
                      <el-input
                        v-model="realnameForm.mobile"
                        maxlength="11"
                        placeholder="请输入本人实名手机号"
                      />
                    </el-form-item>
                    <el-form-item>
                      <el-button :loading="realnameSubmitting" @click="handleRealnameSubmit">
                        {{ realnameSubmitButtonText }}
                      </el-button>
                    </el-form-item>
                  </el-form>
                </div>
              </div>

              <div v-else>
                <el-alert
                  v-if="!realnameEnabled"
                  type="info"
                  :closable="false"
                  title="当前站点未开启实名认证"
                  class="mb-3"
                />
                <template v-else>
                  <el-alert
                    v-if="realnameStatus === 'expired'"
                    type="error"
                    :closable="false"
                    show-icon
                    title="实名信息已过期，授权安装与购买已被拦截"
                    description="请重新提交姓名与身份证号完成认证"
                    class="mb-3"
                  />
                  <el-alert
                    v-else
                    type="warning"
                    :closable="false"
                    :title="realnameRequirementText"
                    class="mb-3"
                  />
                  <el-form :model="realnameForm" label-width="80px" class="profile-form">
                    <el-form-item label="真实姓名">
                      <el-input
                        v-model="realnameForm.realName"
                        maxlength="30"
                        placeholder="请输入与身份证一致的姓名"
                      />
                    </el-form-item>
                    <el-form-item label="身份证号">
                      <el-input
                        v-model="realnameForm.idCard"
                        maxlength="18"
                        placeholder="请输入 18 位身份证号"
                      />
                    </el-form-item>
                    <el-form-item v-if="needRealnameMobile" label="手机号">
                      <el-input
                        v-model="realnameForm.mobile"
                        maxlength="11"
                        placeholder="请输入本人实名手机号"
                      />
                      <div class="realname-mobile-tip">
                        仅用于本次三要素核验，不会保存到账号资料
                      </div>
                    </el-form-item>
                    <el-form-item>
                      <el-button
                        type="primary"
                        :loading="realnameSubmitting"
                        @click="handleRealnameSubmit"
                      >
                        {{ realnameSubmitButtonText }}
                      </el-button>
                    </el-form-item>
                  </el-form>
                </template>
              </div>
            </el-tab-pane>

            <el-tab-pane label="安全设置" name="security">
              <el-form :model="passwordForm" label-width="80px" class="profile-form">
                <el-form-item label="旧密码">
                  <el-input
                    v-model="passwordForm.oldPassword"
                    type="password"
                    show-password
                    placeholder="请输入当前密码"
                  />
                </el-form-item>
                <el-form-item label="新密码">
                  <el-input
                    v-model="passwordForm.newPassword"
                    type="password"
                    show-password
                    placeholder="至少 6 位"
                  />
                </el-form-item>
                <el-form-item label="确认密码">
                  <el-input
                    v-model="passwordForm.confirmPassword"
                    type="password"
                    show-password
                    placeholder="再次输入新密码"
                  />
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="handleChangePassword">修改密码</el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>
          </el-tabs>
        </div>
      </el-col>
    </el-row>

    <el-dialog
      v-model="certifyDialogVisible"
      :title="certifyDialogTitle"
      width="360px"
      :close-on-click-modal="false"
      @close="stopCertifyPolling"
    >
      <div class="certify-dialog">
        <img v-if="certifyQrDataUrl" :src="certifyQrDataUrl" alt="认证二维码" class="certify-qr" />
        <p class="certify-tip">{{ certifyDialogTip }}</p>
        <el-button
          v-if="isAlipayInfoCertify && isMobileDevice && !isWeChatBrowser"
          type="primary"
          @click="openAlipayAuth"
          >打开支付宝授权</el-button
        >
        <p v-if="isAlipayInfoCertify && isWeChatBrowser" class="certify-tip">
          微信内无法唤起支付宝，请右上角选择浏览器打开，或使用支付宝扫码
        </p>
        <el-button
          v-if="
            certifyProvider !== 'kuaitong' &&
            certifyProvider !== 'tencent' &&
            !(isAlipayInfoCertify && isMobileDevice)
          "
          type="primary"
          link
          @click="openCertifyUrl"
          >在浏览器中打开认证页面</el-button
        >
        <div class="certify-status">
          <el-icon v-if="certifyPolling" class="is-loading"><Loading /></el-icon>
          <span>{{ certifyStatusText }}</span>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
  import { reactive, ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
  import { useRoute, useRouter } from 'vue-router'
  import { ElMessage } from 'element-plus'
  import { Loading } from '@element-plus/icons-vue'
  import { Icon as IconifyIcon } from '@iconify/vue'
  import QRCode from 'qrcode'
  import axios from 'axios'

  const router = useRouter()
  const route = useRoute()

  function getToken() {
    return localStorage.getItem('user_panel_token') || ''
  }
  const headers = () => ({ Authorization: `Bearer ${getToken()}` })

  const profileForm = reactive({
    userId: '',
    email: '',
    phone: '',
    nickname: ''
  })

  const passwordForm = reactive({
    oldPassword: '',
    newPassword: '',
    confirmPassword: ''
  })

  const realnameEnabled = ref(false)
  const realnameProvider = ref('')
  const realnameAuthMode = ref('')
  const realnameScope = ref('by_app')
  const realnameStatus = ref('ok')
  const purchaseRealnameRequired = ref(false)
  const realnameVerified = ref(false)
  const realnameInfo = reactive({
    realName: '',
    realIdCard: '',
    realnameAt: '',
    realnameExpiresAt: ''
  })
  const realnameForm = reactive({ realName: '', idCard: '', mobile: '' })
  const realnameSubmitting = ref(false)

  // 实名入口提示：按生效范围与购买门槛换文案，避免用户在两处看到不同说法。
  const realnameRequirementText = computed(() => {
    if (realnameScope.value === 'all') {
      return '本站已开启全站实名，完成实名后才能安装应用'
    }
    if (purchaseRealnameRequired.value) {
      return '购买授权前需完成实名认证，部分应用安装时也会校验'
    }
    return '部分应用要求完成实名认证后才能安装使用'
  })

  const activeTab = ref('profile')
  const certifyDialogVisible = ref(false)
  const certifyQrDataUrl = ref('')
  const certifyUrl = ref('')
  const certifyOuterOrderNo = ref('')
  const certifyId = ref('')
  const certifyProvider = ref('')
  const certifyAuthMode = ref('')
  const faceToken = ref('')
  const certifyPolling = ref(false)
  const certifyStatusText = ref('等待认证...')
  let certifyTimer: ReturnType<typeof setInterval> | null = null

  async function fetchProfile() {
    try {
      const { data } = await axios.get('/api/user-panel/profile', { headers: headers() })
      if (data.code === 200) {
        const d = data.data
        profileForm.userId = String(d.userId)
        profileForm.email = d.email
        profileForm.phone = d.phone || ''
        profileForm.nickname = d.nickname
        realnameEnabled.value = !!d.realnameEnabled
        realnameProvider.value = d.realnameProvider || ''
        realnameAuthMode.value = d.realnameAuthMode || ''
        realnameScope.value = d.realnameScope || 'by_app'
        realnameStatus.value = d.realnameStatus || 'ok'
        purchaseRealnameRequired.value = !!d.purchaseRealnameRequired
        realnameVerified.value = !!d.realnameVerified
        realnameInfo.realName = d.realName || ''
        realnameInfo.realIdCard = d.realIdCard || ''
        realnameInfo.realnameAt = d.realnameAt || ''
        realnameInfo.realnameExpiresAt = d.realnameExpiresAt || ''
        // 通知外层布局刷新实名到期提示条
        window.dispatchEvent(new CustomEvent('user-panel-realname-refresh'))
      }
    } catch {
      // 个人信息加载失败时保持空表单，由用户手动刷新
    }
  }

  async function handleSaveProfile() {
    if (!profileForm.nickname && !profileForm.email && !profileForm.phone) {
      ElMessage.warning('请至少修改一项')
      return
    }
    if (profileForm.phone && !/^1\d{10}$/.test(profileForm.phone.trim())) {
      ElMessage.warning('手机号格式不正确')
      return
    }
    try {
      const { data } = await axios.put(
        '/api/user-panel/profile',
        {
          nickname: profileForm.nickname,
          email: profileForm.email,
          phone: profileForm.phone.trim()
        },
        { headers: headers() }
      )
      if (data.code === 200) {
        ElMessage.success('个人信息已更新')
        // 同步 localStorage
        const info = JSON.parse(localStorage.getItem('user_panel_info') || '{}')
        if (profileForm.nickname) info.nickname = profileForm.nickname
        if (profileForm.email) info.email = profileForm.email
        localStorage.setItem('user_panel_info', JSON.stringify(info))
      } else {
        ElMessage.error(data.msg || '更新失败')
      }
    } catch {
      ElMessage.error('请求失败')
    }
  }

  async function handleChangePassword() {
    if (!passwordForm.oldPassword || !passwordForm.newPassword) {
      ElMessage.warning('请填写完整')
      return
    }
    if (passwordForm.newPassword.length < 6) {
      ElMessage.warning('新密码至少6位')
      return
    }
    if (passwordForm.newPassword !== passwordForm.confirmPassword) {
      ElMessage.error('两次密码输入不一致')
      return
    }
    try {
      const { data } = await axios.post(
        '/api/user-panel/change-password',
        {
          oldPassword: passwordForm.oldPassword,
          newPassword: passwordForm.newPassword
        },
        { headers: headers() }
      )
      if (data.code === 200) {
        ElMessage.success('密码已修改，请重新登录')
        Object.assign(passwordForm, { oldPassword: '', newPassword: '', confirmPassword: '' })
        setTimeout(() => {
          localStorage.removeItem('user_panel_token')
          localStorage.removeItem('user_panel_info')
          router.push('/user/login')
        }, 800)
      } else {
        ElMessage.error(data.msg || '修改失败')
      }
    } catch {
      ElMessage.error('请求失败')
    }
  }

  async function handleRealnameSubmit() {
    const realName = realnameForm.realName.trim()
    const idCard = realnameForm.idCard.trim().toUpperCase()
    const mobile = realnameForm.mobile.trim()
    if (realName.length < 2) {
      ElMessage.warning('请输入真实姓名')
      return
    }
    if (!/^\d{17}[\dX]$/.test(idCard)) {
      ElMessage.warning('请输入正确的 18 位身份证号')
      return
    }
    if (needRealnameMobile.value && !/^1[3-9]\d{9}$/.test(mobile)) {
      ElMessage.warning('请输入正确的 11 位手机号')
      return
    }
    realnameSubmitting.value = true
    try {
      const { data } = await axios.post(
        '/api/user-panel/realname/init',
        needRealnameMobile.value ? { realName, idCard, mobile } : { realName, idCard },
        { headers: headers() }
      )
      if (data.code !== 200) {
        ElMessage.error(data.msg || '实名认证失败')
        return
      }
      certifyProvider.value = data.data?.provider || realnameProvider.value
      certifyAuthMode.value = data.data?.authMode || ''
      if (data.data?.status === 'passed') {
        ElMessage.success('实名认证成功')
        resetRealnameForm()
        fetchProfile()
        return
      }
      // 支付宝信息比对：授权链接生成二维码，移动端可唤起支付宝，认证单用 faceToken 轮询
      if (isAlipayInfoCertify.value && data.data?.faceToken) {
        faceToken.value = data.data.faceToken
        certifyUrl.value = data.data.certifyUrl || ''
        certifyQrDataUrl.value = certifyUrl.value
          ? await QRCode.toDataURL(certifyUrl.value, { width: 220, margin: 1 })
          : ''
        certifyDialogVisible.value = true
        startCertifyPolling()
        return
      }
      // 小沐聚合实名：跳转上游认证页，认证单用 faceToken 轮询
      if (certifyProvider.value === 'xiaomu' && data.data?.faceToken) {
        faceToken.value = data.data.faceToken
        certifyUrl.value = data.data.certifyUrl || ''
        certifyQrDataUrl.value = certifyUrl.value
          ? await QRCode.toDataURL(certifyUrl.value, { width: 220, margin: 1 })
          : ''
        certifyDialogVisible.value = true
        startCertifyPolling()
        return
      }
      // 快瞳与靓仔人脸：生成本站拍照认证单，扫码后抓拍完成核验
      if (
        (data.data?.provider === 'kuaitong' || data.data?.provider === 'tencent') &&
        data.data?.faceToken
      ) {
        faceToken.value = data.data.faceToken
        const faceUrl = `${location.origin}/realname-face?t=${data.data.faceToken}`
        certifyUrl.value = faceUrl
        certifyQrDataUrl.value = await QRCode.toDataURL(faceUrl, { width: 220, margin: 1 })
        certifyDialogVisible.value = true
        startCertifyPolling()
        return
      }
      certifyUrl.value = data.data.certifyUrl
      certifyOuterOrderNo.value = data.data.outerOrderNo
      certifyId.value = data.data.certifyId
      certifyQrDataUrl.value = await QRCode.toDataURL(data.data.certifyUrl, {
        width: 220,
        margin: 1
      })
      certifyDialogVisible.value = true
      startCertifyPolling()
    } catch {
      ElMessage.error('请求失败')
    } finally {
      realnameSubmitting.value = false
    }
  }

  function startCertifyPolling() {
    stopCertifyPolling()
    certifyPolling.value = true
    certifyStatusText.value = '等待认证...'
    certifyTimer = setInterval(async () => {
      try {
        const params = isTokenPollingProvider(certifyProvider.value)
          ? { faceToken: faceToken.value }
          : { outerOrderNo: certifyOuterOrderNo.value, certifyId: certifyId.value }
        const { data } = await axios.get('/api/user-panel/realname/query', {
          params,
          headers: headers()
        })
        if (data.code === 200 && data.data?.status === 'passed') {
          stopCertifyPolling()
          certifyDialogVisible.value = false
          ElMessage.success('实名认证成功')
          resetRealnameForm()
          fetchProfile()
        } else if (data.code === 200 && data.data?.status === 'failed') {
          stopCertifyPolling()
          certifyStatusText.value = data.data.reason
            ? `认证未通过：${data.data.reason}`
            : '认证未通过，请重新提交'
        } else {
          certifyStatusText.value = '等待认证...'
        }
      } catch {
        certifyStatusText.value = '查询中...'
      }
    }, 3000)
  }

  function stopCertifyPolling() {
    certifyPolling.value = false
    if (certifyTimer) {
      clearInterval(certifyTimer)
      certifyTimer = null
    }
  }

  function openCertifyUrl() {
    if (certifyUrl.value) window.open(certifyUrl.value, '_blank')
  }

  function isFaceRealnameProvider(provider: string | undefined) {
    return provider === 'kuaitong' || provider === 'tencent'
  }

  // 认证单通过 faceToken 轮询的服务商（本站拍照单与聚合认证单）
  function isTokenPollingProvider(provider: string | undefined) {
    return (
      isFaceRealnameProvider(provider) ||
      provider === 'xiaomu' ||
      (provider === 'alipay' && certifyAuthMode.value === 'info')
    )
  }

  // 支付宝信息比对认证：授权链接 + 唤起支付宝
  const isAlipayInfoCertify = computed(
    () => certifyProvider.value === 'alipay' && certifyAuthMode.value === 'info'
  )

  const isMobileDevice = /Android|iPhone|iPad|iPod|Mobile/i.test(navigator.userAgent)

  const isWeChatBrowser = /MicroMessenger/i.test(navigator.userAgent)

  function openAlipayAuth() {
    if (!certifyUrl.value) return
    window.location.href =
      'alipays://platformapi/startapp?appId=20000067&url=' + encodeURIComponent(certifyUrl.value)
  }

  const needRealnameMobile = computed(
    () => realnameProvider.value === 'xiaomu' && realnameAuthMode.value === 'three_element'
  )

  const realnameSubmitButtonText = computed(() => {
    if (realnameProvider.value === 'xiaomu') {
      return realnameAuthMode.value === 'three_element' ? '提交并核验' : '提交并跳转认证'
    }
    if (realnameProvider.value === 'tencent') return '提交并扫码认证'
    return isFaceRealnameProvider(realnameProvider.value)
      ? '提交并扫码认证'
      : '提交并进行支付宝认证'
  })

  const certifyDialogTitle = computed(() => {
    if (certifyProvider.value === 'xiaomu') {
      return '实名认证'
    }
    if (isAlipayInfoCertify.value) {
      return '支付宝实名认证'
    }
    return isFaceRealnameProvider(certifyProvider.value) ? '扫码人脸核验' : '支付宝扫码认证'
  })

  const certifyDialogTip = computed(() => {
    if (certifyProvider.value === 'xiaomu') {
      return '请扫码或点击下方链接完成实名认证'
    }
    if (isAlipayInfoCertify.value) {
      return isMobileDevice
        ? '请点击下方按钮唤起支付宝完成授权，或用另一台设备的支付宝扫码'
        : '请使用支付宝 App 扫码完成实名信息授权'
    }
    return isFaceRealnameProvider(certifyProvider.value)
      ? '请使用微信或支付宝扫码完成人脸核验'
      : '请使用支付宝 App 扫码，或'
  })

  function resetRealnameForm() {
    realnameForm.realName = ''
    realnameForm.idCard = ''
    realnameForm.mobile = ''
  }

  function handleCopyId() {
    navigator.clipboard.writeText(profileForm.userId)
    ElMessage.success('已复制用户ID')
  }

  // 支持从购买页/仪表盘带 ?tab=realname 直达实名标签；
  // 已在个人中心时路由参数变化不会重挂载组件，需监听 query 同步切换。
  watch(
    () => route.query.tab,
    (tab) => {
      if (typeof tab === 'string' && ['profile', 'realname', 'security'].includes(tab)) {
        activeTab.value = tab
      }
    },
    { immediate: true }
  )

  onMounted(() => {
    fetchProfile()
  })
  onBeforeUnmount(() => {
    stopCertifyPolling()
  })
</script>

<style scoped lang="scss">
  .user-profile {
    background: var(--el-bg-color);

    .art-card {
      overflow: hidden;
      background: var(--el-bg-color);
      border-radius: 12px !important;
    }
  }

  .mb-3 {
    margin-bottom: 12px;
  }

  .profile-side {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 32px 20px 28px;
    text-align: center;

    .side-avatar {
      background: var(--el-color-primary-light-7);
      color: var(--el-color-primary);
    }

    .side-name {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-top: 14px;

      .nickname {
        font-size: 17px;
        font-weight: 600;
        color: var(--el-text-color-primary);
        max-width: 180px;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .verified-tag {
        display: inline-flex;
        align-items: center;
        gap: 3px;
      }
    }

    .side-meta {
      width: 100%;
      margin-top: 16px;
      padding-top: 16px;
      border-top: 1px dashed var(--el-border-color-lighter);

      .meta-row {
        display: flex;
        align-items: center;
        justify-content: center;
        gap: 6px;
        font-size: 13px;
        color: var(--el-text-color-secondary);
        line-height: 1.9;

        .meta-text {
          overflow: hidden;
          text-overflow: ellipsis;
          white-space: nowrap;
        }
      }

      .meta-id {
        cursor: pointer;

        &:hover {
          color: var(--el-color-primary);
        }
      }
    }

    .side-avatar-btn {
      margin-top: 18px;
    }
  }

  .profile-main {
    padding: 6px 20px 20px;
    min-height: 380px;

    .profile-tabs {
      :deep(.el-tabs__header) {
        margin-bottom: 20px;
      }

      .tab-badge {
        margin-left: 4px;
        vertical-align: 2px;
      }
    }
  }

  .profile-form {
    max-width: 420px;
  }

  .realname-mobile-tip {
    margin-top: 4px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--el-text-color-secondary);
  }

  .realname-done {
    .realname-badge {
      display: flex;
      align-items: center;
      gap: 6px;
      margin-bottom: 14px;
      font-size: 14px;
      font-weight: 600;
      color: var(--el-color-success);
    }

    .realname-info {
      > div {
        display: flex;
        align-items: center;
        margin-bottom: 10px;
        font-size: 14px;

        label {
          width: 80px;
          color: var(--el-text-color-secondary);
        }

        span {
          color: var(--el-text-color-primary);
        }
      }
    }

    .realname-reverify {
      .reverify-tip {
        margin: 0 0 16px;
        font-size: 13px;
        line-height: 1.7;
        color: var(--el-text-color-secondary);
      }
    }
  }

  .certify-dialog {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 8px 0;

    .certify-qr {
      width: 220px;
      height: 220px;
      margin-bottom: 12px;
      border: 1px solid var(--el-border-color-lighter);
      border-radius: 8px;
    }

    .certify-tip {
      margin: 0 0 4px;
      font-size: 14px;
      color: var(--el-text-color-regular);
    }

    .certify-status {
      display: flex;
      align-items: center;
      gap: 6px;
      margin-top: 16px;
      font-size: 13px;
      color: var(--el-text-color-secondary);
    }
  }

  @media (max-width: 768px) {
    .profile-side {
      margin-bottom: 16px;
    }
  }
</style>
