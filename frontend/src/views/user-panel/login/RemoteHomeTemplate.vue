<template>
  <div v-if="staticEntryUrl" class="static-home">
    <iframe
      ref="staticFrame"
      :src="staticEntryUrl"
      title="自定义首页模板"
      referrerpolicy="no-referrer"
    />
  </div>
  <FintechGoldHome
    v-else-if="stylePreset === 'fintech-gold'"
    :document="document"
    :site-name="siteName"
    :site-subtitle="siteSubtitle"
    :login-action="loginAccount"
  />
  <div v-else class="remote-home" :class="presetClass" :style="themeStyle">
    <header class="remote-header">
      <div class="remote-shell header-shell">
        <div class="remote-brand">
          <img :src="resolvedLogo" alt="网站 Logo" />
          <strong>{{ siteName }}</strong>
        </div>
        <ElButton type="primary" @click="openLogin">登录</ElButton>
      </div>
    </header>

    <main>
      <section class="remote-hero">
        <div class="remote-shell hero-grid">
          <div class="hero-copy">
            <span v-if="document.hero.badge" class="hero-badge">{{ document.hero.badge }}</span>
            <h1>
              {{ document.hero.title }}
              <span v-if="document.hero.highlight">{{ document.hero.highlight }}</span>
            </h1>
            <p>{{ document.hero.description || siteSubtitle }}</p>
            <div class="hero-actions">
              <ElButton type="primary" size="large" @click="openLogin">
                {{ document.hero.primaryAction?.label || '进入用户中心' }}
              </ElButton>
              <ElButton v-if="document.hero.secondaryAction" plain size="large" @click="openLogin">
                {{ document.hero.secondaryAction.label || '登录' }}
              </ElButton>
            </div>
          </div>
          <div v-if="heroImage" class="hero-image-wrap">
            <img :src="heroImage" alt="首页模板展示图" @error="heroImageFailed = true" />
          </div>
          <div v-else class="hero-placeholder">
            <IconifyIcon icon="ri:shield-check-line" />
            <strong>License Service</strong>
          </div>
        </div>
      </section>

      <section v-if="visibleFeatures.length" class="remote-features">
        <div class="remote-shell feature-grid">
          <article v-for="feature in visibleFeatures" :key="feature.title">
            <IconifyIcon :icon="safeTemplateIcon(feature.icon)" />
            <h2>{{ feature.title }}</h2>
            <p>{{ feature.description }}</p>
          </article>
        </div>
      </section>
    </main>

    <footer class="remote-footer">
      <div class="remote-shell">
        {{ document.footer?.text || `© ${currentYear} ${siteName}` }}
      </div>
    </footer>
  </div>

  <ElDialog
    v-model="loginVisible"
    width="min(460px, calc(100vw - 28px))"
    :class="['remote-login-dialog', dialogPresetClass]"
    :style="themeStyle"
    destroy-on-close
  >
    <template #header>
      <div class="dialog-brand"><img :src="resolvedLogo" alt="网站 Logo" />{{ siteName }}</div>
    </template>
    <h2>用户登录</h2>
    <p class="dialog-subtitle">登录后查看并管理您的授权</p>
    <ElForm ref="loginFormRef" :model="loginForm" :rules="loginRules" @submit.prevent>
      <ElFormItem prop="username">
        <ElInput v-model="loginForm.username" size="large" placeholder="手机号 / 邮箱 / 用户ID" />
      </ElFormItem>
      <ElFormItem prop="password">
        <ElInput
          v-model="loginForm.password"
          size="large"
          type="password"
          show-password
          placeholder="登录密码"
          @keyup.enter="handleLogin"
        />
      </ElFormItem>
      <ElButton class="login-submit" type="primary" :loading="loading" @click="handleLogin">
        登录用户中心
      </ElButton>
    </ElForm>
  </ElDialog>
</template>

<script setup lang="ts">
  import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
  import { useRoute, useRouter } from 'vue-router'
  import { ElMessage, type FormRules } from 'element-plus'
  import { Icon as IconifyIcon } from '@iconify/vue'
  import axios from 'axios'
  import FintechGoldHome from './fintech-gold/index.vue'
  import { useSystemConfigStore } from '@/store/modules/system-config'
  import { useGeetestLoginCaptcha } from '@/utils/geetest'
  import {
    type HomeTemplateDocument,
    isHomeTemplateStylePreset,
    safeTemplateColor,
    safeTemplateIcon,
    safeTemplateImageURL
  } from './home-template'

  defineOptions({ name: 'RemoteHomeTemplate' })

  const props = defineProps<{ document: HomeTemplateDocument; staticEntryUrl?: string }>()
  const staticFrame = ref<HTMLIFrameElement>()

  function handleTemplateMessage(event: MessageEvent) {
    // Only the installed iframe may open the host login dialog; never share credentials or tokens.
    if (
      props.staticEntryUrl &&
      event.source === staticFrame.value?.contentWindow &&
      event.data?.type === 'auth-pro:login'
    ) {
      openLogin()
    }
  }
  onMounted(() => window.addEventListener('message', handleTemplateMessage))
  onUnmounted(() => window.removeEventListener('message', handleTemplateMessage))
  const router = useRouter()
  const route = useRoute()
  const systemConfigStore = useSystemConfigStore()
  const { siteName, siteSubtitle, resolvedLogo } = storeToRefs(systemConfigStore)

  const currentYear = new Date().getFullYear()
  const loginVisible = ref(false)

  // 极验行为验证：启用后登录前弹出滑块验证
  const {
    captchaEnabled,
    preload: preloadCaptcha,
    verify: verifyCaptcha,
    reset: resetCaptcha
  } = useGeetestLoginCaptcha()
  onMounted(preloadCaptcha)
  const loading = ref(false)
  const loginFormRef = ref()
  const loginForm = reactive({ username: '', password: '' })
  const heroImageFailed = ref(false)
  const loginRules: FormRules = {
    username: [{ required: true, message: '请输入手机号、邮箱或用户ID', trigger: 'blur' }],
    password: [{ required: true, message: '请输入密码', trigger: 'blur' }]
  }

  const themeStyle = computed(() => ({
    '--remote-primary': safeTemplateColor(props.document.theme?.primaryColor, '#4d6bfe'),
    '--remote-background': safeTemplateColor(props.document.theme?.backgroundColor, '#f7f8fc'),
    '--remote-text': safeTemplateColor(props.document.theme?.textColor, '#172033')
  }))
  const stylePreset = computed(() =>
    isHomeTemplateStylePreset(props.document.stylePreset) ? props.document.stylePreset : 'standard'
  )
  const presetClass = computed(() => `remote-home--${stylePreset.value}`)
  const dialogPresetClass = computed(() => `remote-login-dialog--${stylePreset.value}`)
  const heroImage = computed(() =>
    heroImageFailed.value ? '' : safeTemplateImageURL(props.document.hero.imageUrl)
  )
  const visibleFeatures = computed(() => (props.document.features || []).slice(0, 12))

  function openLogin() {
    loginVisible.value = true
  }

  interface HomeLoginResponse {
    code: number
    msg?: string
    data?: { accessToken?: string; converted?: boolean; loginPath?: string; [key: string]: unknown }
  }

  // Both visual presets share the host's login and agent-account conversion contract.
  async function loginAccount(account: string, password: string) {
    // 极验行为验证：启用后先完成滑块验证再提交登录
    let captchaParams = {}
    if (captchaEnabled.value) {
      let result
      try {
        result = await verifyCaptcha()
      } catch (error) {
        throw new Error(error instanceof Error ? error.message : '行为验证初始化失败')
      }
      if (!result) throw new Error('请先完成行为验证')
      captchaParams = result
    }
    let response: HomeLoginResponse
    try {
      const { data } = await axios.post<HomeLoginResponse>(
        '/api/user-panel/login',
        { account, password, ...captchaParams },
        {
          validateStatus: (status) => (status >= 200 && status < 300) || status === 409
        }
      )
      response = data
    } catch {
      resetCaptcha()
      throw new Error('网络错误，请稍后重试')
    }
    const token = response.data?.accessToken
    if (response.code === 200 && typeof token === 'string' && token) {
      localStorage.setItem('user_panel_token', token)
      localStorage.setItem('user_panel_info', JSON.stringify(response.data))
      ElMessage.success('登录成功')
      const redirect =
        typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/user')
          ? route.query.redirect
          : '/user/dashboard'
      await router.push(redirect)
      return
    }
    if (response.code === 409 && response.data?.converted === true) {
      loginVisible.value = false
      ElMessage.success(response.msg || '该账号已升级为代理，请前往代理端登录')
      await router.push(response.data.loginPath || '/agent-panel/login?upgraded=1')
      return
    }
    resetCaptcha()
    throw new Error(response.msg || '登录失败')
  }

  function handleLogin() {
    loginFormRef.value?.validate(async (valid: boolean) => {
      if (!valid || loading.value) return
      loading.value = true
      try {
        await loginAccount(loginForm.username.trim(), loginForm.password.trim())
      } catch (error) {
        ElMessage.error(error instanceof Error ? error.message : '登录失败')
      } finally {
        loading.value = false
      }
    })
  }

  onMounted(() => {
    if (route.query.impersonate !== '1') return
    const raw = sessionStorage.getItem('impersonate_user_panel')
    if (!raw) return
    try {
      const info = JSON.parse(raw)
      localStorage.setItem('user_panel_token', info.accessToken)
      localStorage.setItem('user_panel_info', raw)
      sessionStorage.removeItem('impersonate_user_panel')
      ElMessage.success('已登录该用户账号')
      router.replace('/user/dashboard')
    } catch {
      sessionStorage.removeItem('impersonate_user_panel')
    }
  })
</script>

<style scoped lang="scss">
  .static-home {
    min-height: 100vh;

    iframe {
      display: block;
      width: 100%;
      height: 100vh;
      border: 0;
    }
  }

  .remote-home {
    min-height: 100vh;
    color: var(--remote-text);
    background: var(--remote-background);
  }

  .remote-home--cartoon-blue {
    position: relative;
    overflow: hidden;
    background:
      radial-gradient(circle at 8% 30%, rgb(22 143 229 / 10%) 0 110px, transparent 112px),
      radial-gradient(circle at 73% 86%, rgb(230 74 74 / 8%) 0 58px, transparent 60px),
      linear-gradient(145deg, var(--remote-background), #e7f7ff 58%, #fff8f4);

    .remote-header {
      background: rgb(255 255 255 / 82%);
      border-bottom-color: rgb(21 51 74 / 8%);
    }

    .remote-brand img {
      padding: 4px;
      background: #fff;
      border-radius: 50%;
      box-shadow: 0 8px 24px rgb(22 143 229 / 18%);
    }

    :deep(.el-button) {
      border-radius: 999px;
    }

    :deep(.el-button--primary) {
      box-shadow: 0 10px 24px color-mix(in srgb, var(--remote-primary) 24%, transparent);
    }

    .hero-badge {
      background: #fff;
      box-shadow: 0 8px 22px rgb(22 143 229 / 12%);
    }

    h1 {
      letter-spacing: -3px;

      span {
        color: #e64a4a;
      }
    }

    .hero-placeholder {
      position: relative;
      overflow: visible;
      color: #fff;
      background: var(--remote-primary);
      border: 16px solid rgb(255 255 255 / 82%);
      border-radius: 50%;
      box-shadow: 0 30px 80px rgb(22 143 229 / 22%);

      &::before {
        position: absolute;
        right: 25%;
        bottom: 20%;
        width: 46%;
        height: 23%;
        content: '';
        border: 7px solid #fff;
        border-top: 0;
        border-radius: 0 0 999px 999px;
      }

      &::after {
        position: absolute;
        top: 14%;
        left: 46%;
        width: 34px;
        height: 34px;
        content: '';
        background: #f6c445;
        border: 7px solid #e64a4a;
        border-radius: 50%;
      }

      svg,
      strong {
        position: relative;
        z-index: 1;
      }

      svg {
        margin-bottom: 0;
        transform: translateY(-24px);
      }

      strong {
        display: none;
      }
    }

    .feature-grid article {
      border-color: rgb(21 51 74 / 7%);
      border-radius: 26px;
      box-shadow: 0 18px 48px rgb(22 143 229 / 10%);
    }

    .remote-footer {
      background: rgb(255 255 255 / 72%);
      border-top-color: rgb(21 51 74 / 8%);
    }
  }

  .remote-home--fintech-gold {
    position: relative;
    overflow: hidden;
    color: var(--remote-text);
    background-color: var(--remote-background);
    background-image:
      linear-gradient(rgb(243 186 47 / 5%) 1px, transparent 1px),
      linear-gradient(90deg, rgb(243 186 47 / 5%) 1px, transparent 1px);
    background-size: 44px 44px;

    .remote-header {
      background: rgb(9 11 16 / 88%);
      border-bottom-color: #252a34;
    }

    .remote-brand strong {
      letter-spacing: 0.04em;
    }

    :deep(.el-button) {
      border-radius: 3px;
    }

    :deep(.el-button--primary) {
      --el-button-text-color: #090b10;
      --el-button-hover-text-color: #090b10;
    }

    :deep(.el-button.is-plain) {
      color: #f4f6fa;
      background: transparent;
      border-color: #3a404c;
    }

    .hero-badge {
      padding-left: 12px;
      color: var(--remote-primary);
      letter-spacing: 0.08em;
      background: rgb(243 186 47 / 8%);
      border-left: 3px solid var(--remote-primary);
      border-radius: 0;
    }

    h1 {
      letter-spacing: -2px;
    }

    .hero-copy > p,
    .feature-grid p {
      color: #aeb4bf;
    }

    .hero-placeholder {
      position: relative;
      clip-path: polygon(12% 0, 100% 0, 100% 88%, 88% 100%, 0 100%, 0 12%);
      color: var(--remote-primary);
      background: #11151c;
      border-color: #343b47;
      border-radius: 7px;
      box-shadow: 18px 18px 0 rgb(243 186 47 / 8%);

      &::before,
      &::after {
        position: absolute;
        content: '';
      }

      &::before {
        inset: 58px;
        border: 1px solid var(--remote-primary);
        transform: rotate(45deg);
      }

      &::after {
        top: 28px;
        right: 28px;
        width: 70px;
        height: 4px;
        background: var(--remote-primary);
      }

      svg,
      strong {
        position: relative;
        z-index: 1;
      }
    }

    .feature-grid article {
      background: #11151c;
      border-color: #2b313d;
      border-radius: 5px;
      box-shadow: none;
    }

    .remote-footer {
      color: #8f96a3;
      background: #090b10;
      border-top-color: #252a34;
    }
  }

  .remote-shell {
    width: min(1160px, calc(100% - 40px));
    margin: 0 auto;
  }

  .remote-header {
    background: rgb(255 255 255 / 86%);
    backdrop-filter: blur(14px);
    border-bottom: 1px solid rgb(23 32 51 / 8%);
  }

  .header-shell {
    display: flex;
    align-items: center;
    justify-content: space-between;
    min-height: 72px;
  }

  .remote-brand,
  .dialog-brand {
    display: flex;
    gap: 10px;
    align-items: center;

    img {
      width: 38px;
      height: 38px;
      object-fit: contain;
    }
  }

  :deep(.el-button--primary) {
    --el-button-bg-color: var(--remote-primary);
    --el-button-border-color: var(--remote-primary);
  }

  .remote-hero {
    display: flex;
    align-items: center;
    min-height: 660px;
    padding: 90px 0;
  }

  .hero-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.1fr) minmax(320px, 0.9fr);
    gap: 72px;
    align-items: center;
  }

  .hero-badge {
    display: inline-flex;
    padding: 8px 14px;
    font-size: 13px;
    color: var(--remote-primary);
    background: color-mix(in srgb, var(--remote-primary) 10%, white);
    border-radius: 999px;
  }

  h1 {
    margin: 22px 0;
    font-size: clamp(42px, 5vw, 68px);
    line-height: 1.08;
    letter-spacing: -2px;

    span {
      display: block;
      color: var(--remote-primary);
    }
  }

  .hero-copy > p {
    max-width: 680px;
    font-size: 18px;
    line-height: 1.8;
    color: color-mix(in srgb, var(--remote-text) 66%, transparent);
  }

  .hero-actions {
    display: flex;
    gap: 12px;
    margin-top: 34px;
  }

  .hero-image-wrap,
  .hero-placeholder {
    overflow: hidden;
    background: #fff;
    border: 1px solid rgb(23 32 51 / 8%);
    border-radius: 28px;
    box-shadow: 0 28px 80px rgb(23 32 51 / 12%);
  }

  .hero-image-wrap img {
    display: block;
    width: 100%;
    min-height: 380px;
    object-fit: cover;
  }

  .hero-placeholder {
    display: grid;
    place-content: center;
    min-height: 380px;
    color: var(--remote-primary);
    text-align: center;

    svg {
      width: 94px;
      height: 94px;
      margin: 0 auto 18px;
    }
  }

  .remote-features {
    padding: 0 0 100px;
  }

  .feature-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 18px;

    article {
      padding: 28px;
      background: #fff;
      border: 1px solid rgb(23 32 51 / 8%);
      border-radius: 18px;
    }

    svg {
      width: 30px;
      height: 30px;
      color: var(--remote-primary);
    }

    h2 {
      margin: 18px 0 10px;
      font-size: 20px;
    }

    p {
      margin: 0;
      line-height: 1.7;
      color: color-mix(in srgb, var(--remote-text) 62%, transparent);
    }
  }

  .remote-footer {
    padding: 28px 0;
    font-size: 13px;
    color: color-mix(in srgb, var(--remote-text) 55%, transparent);
    text-align: center;
    background: #fff;
    border-top: 1px solid rgb(23 32 51 / 8%);
  }

  .dialog-subtitle {
    margin: 6px 0 22px;
    color: var(--el-text-color-secondary);
  }

  .login-submit {
    width: 100%;
    height: 44px;
  }

  @media (width <= 820px) {
    .remote-hero {
      min-height: 0;
      padding: 72px 0;
    }

    .hero-grid,
    .feature-grid {
      grid-template-columns: 1fr;
    }

    .hero-grid {
      gap: 42px;
    }

    h1 {
      font-size: 42px;
    }

    .remote-home--fintech-gold h1 {
      font-size: 38px;
    }
  }

  :global(.el-dialog.remote-login-dialog--cartoon-blue) {
    border-radius: 28px !important;
  }

  :global(.remote-login-dialog--cartoon-blue .el-input__wrapper),
  :global(.remote-login-dialog--cartoon-blue .el-button) {
    border-radius: 999px;
  }

  :global(.el-dialog.remote-login-dialog--fintech-gold) {
    color: #f4f6fa;
    background: #11151c;
    border: 1px solid #2b313d;
    border-radius: 7px !important;
  }

  :global(.remote-login-dialog--fintech-gold .el-dialog__title),
  :global(.remote-login-dialog--fintech-gold h2) {
    color: #f4f6fa;
  }

  :global(.remote-login-dialog--fintech-gold .el-input__wrapper) {
    background: #090b10;
    border-radius: 3px;
    box-shadow: 0 0 0 1px #343b47 inset;
  }

  :global(.remote-login-dialog--fintech-gold .el-input__inner) {
    color: #f4f6fa;
  }

  :global(.remote-login-dialog--fintech-gold .el-button) {
    color: #090b10;
    background: var(--remote-primary);
    border-color: var(--remote-primary);
    border-radius: 3px;
  }
</style>
