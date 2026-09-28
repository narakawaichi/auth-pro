<!-- 登录页面 -->
<template>
  <div class="auth-login-page">
    <div class="flex w-full h-screen items-center justify-center">
      <div class="relative">
        <div class="auth-right-wrap">
          <div class="form">
            <h3 class="title">{{ $t('login.title') }}</h3>
            <p class="sub-title">{{ $t('login.subTitle') }}</p>
            <ElForm
              ref="formRef"
              :model="formData"
              :rules="rules"
              :key="formKey"
              @keyup.enter="handleSubmit"
              style="margin-top: 25px"
            >
              <ElFormItem prop="username">
                <ElInput
                  class="custom-height"
                  :placeholder="$t('login.placeholder.username')"
                  v-model.trim="formData.username"
                />
              </ElFormItem>
              <ElFormItem prop="password">
                <ElInput
                  class="custom-height"
                  :placeholder="$t('login.placeholder.password')"
                  v-model.trim="formData.password"
                  type="password"
                  autocomplete="off"
                  show-password
                />
              </ElFormItem>

              <div class="flex-cb mt-2 text-sm">
                <ElCheckbox v-model="formData.rememberPassword">{{
                  $t('login.rememberPwd')
                }}</ElCheckbox>
                <RouterLink class="text-theme" :to="{ name: 'ForgetPassword' }">{{
                  $t('login.forgetPwd')
                }}</RouterLink>
              </div>

              <div style="margin-top: 30px">
                <ElButton
                  class="w-full custom-height"
                  type="primary"
                  @click="handleSubmit"
                  :loading="loading"
                  v-ripple
                >
                  {{ $t('login.btnText') }}
                </ElButton>
              </div>
            </ElForm>
          </div>
        </div>
      </div>
    </div>
    <footer v-if="icpNumber" class="auth-site-footer">
      <a href="https://beian.miit.gov.cn/" target="_blank" rel="noopener noreferrer">
        {{ icpNumber }}
      </a>
    </footer>
  </div>
</template>

<script setup lang="ts">
  import { useUserStore } from '@/store/modules/user'
  import { useSystemConfigStore } from '@/store/modules/system-config'
  import { useI18n } from 'vue-i18n'
  import { HttpError } from '@/utils/http/error'
  import { fetchLogin } from '@/api/auth'
  import { ElMessage, ElNotification, type FormInstance, type FormRules } from 'element-plus'
  import { useGeetestLoginCaptcha } from '@/utils/geetest'
  import { isAdminAppStorePath, normalizeAdminLoginRedirect } from '@/utils/router/loginRedirect'

  defineOptions({ name: 'Login' })

  const systemConfigStore = useSystemConfigStore()
  const { siteName, icpNumber } = storeToRefs(systemConfigStore)
  const { t, locale } = useI18n()
  const formKey = ref(0)

  // 监听语言切换，重置表单
  watch(locale, () => {
    formKey.value++
  })

  // 极验行为验证：启用时点击登录弹出验证，未启用则直接提交
  const {
    captchaEnabled,
    preload: preloadCaptcha,
    verify: verifyCaptcha,
    reset: resetCaptcha
  } = useGeetestLoginCaptcha()
  onMounted(preloadCaptcha)

  const userStore = useUserStore()
  const router = useRouter()
  const route = useRoute()

  const formRef = ref<FormInstance>()

  const formData = reactive({
    username: '',
    password: '',
    rememberPassword: true
  })

  const rules = computed<FormRules>(() => ({
    username: [{ required: true, message: t('login.placeholder.username'), trigger: 'blur' }],
    password: [{ required: true, message: t('login.placeholder.password'), trigger: 'blur' }]
  }))

  const loading = ref(false)

  const resolveLoginRedirect = () =>
    normalizeAdminLoginRedirect(route.query.redirect) || '/dashboard/console'

  // 登录
  const handleSubmit = async () => {
    if (!formRef.value) return

    let loginAttempted = false
    let loginCompleted = false

    try {
      // 表单验证
      const valid = await formRef.value.validate()
      if (!valid) return

      // 极验行为验证：启用时提交前弹出极验滑块，未启用则直接提交
      let captchaParams = {}
      if (captchaEnabled.value) {
        let result
        try {
          result = await verifyCaptcha()
        } catch (error) {
          ElMessage.error(error instanceof Error ? error.message : '行为验证初始化失败')
          return
        }
        // null = 用户关闭或组件出错，中止提交
        if (!result) return
        captchaParams = result
      }

      loginAttempted = true
      loading.value = true

      // 登录请求
      const { username, password } = formData

      const { token, refreshToken } = await fetchLogin({
        userName: username,
        password,
        ...captchaParams
      })

      // 验证token
      if (!token) {
        throw new Error('Login failed - no token received')
      }

      // 存储 token 和登录状态
      userStore.setToken(token, refreshToken)
      userStore.setLoginStatus(true)

      // 独立应用商店需要重新请求 Go 静态入口，不能交给主后台 Router 接管。
      const redirectTarget = resolveLoginRedirect()
      if (isAdminAppStorePath(redirectTarget.split(/[?#]/, 1)[0])) {
        loginCompleted = true
        window.location.replace(redirectTarget)
        return
      }

      // 登录页不接受安装向导或外部地址作为回跳目标
      const navigationFailure = await router.replace(redirectTarget)
      if (navigationFailure) {
        throw navigationFailure
      }

      loginCompleted = true
      showLoginSuccessNotice()
    } catch (error) {
      // 处理 HttpError
      if (error instanceof HttpError) {
        // console.log(error.code)
      } else {
        // 处理非 HttpError
        // ElMessage.error('登录失败，请稍后重试')
        console.error('[Login] Unexpected error:', error)
      }
    } finally {
      loading.value = false
      if (loginAttempted && !loginCompleted) {
        resetCaptcha()
      }
    }
  }

  // 登录成功提示
  const showLoginSuccessNotice = () => {
    setTimeout(() => {
      ElNotification({
        title: t('login.success.title'),
        type: 'success',
        duration: 2500,
        zIndex: 10000,
        message: `${t('login.success.message')}, ${siteName.value}!`
      })
    }, 1000)
  }
</script>

<style scoped>
  @import './style.css';

  .auth-login-page {
    position: relative;
    min-height: 100vh;
  }

  .auth-site-footer {
    position: absolute;
    right: 0;
    bottom: 14px;
    left: 0;
    z-index: 2;
    font-size: 12px;
    text-align: center;
  }

  .auth-site-footer a {
    color: var(--art-gray-500);
    text-decoration: none;
  }

  .auth-site-footer a:hover {
    color: var(--main-color);
  }
</style>
