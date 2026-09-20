import { expect, test, type Page, type Route } from '@playwright/test'

const publicSystemConfig = {
  code: 200,
  msg: '',
  data: {
    siteName: '授权管理系统',
    siteSubtitle: '专业的软件授权与服务平台',
    siteLogo: '',
    registrationEnabled: true
  }
}

async function mockPublicAPIs(page: Page, activeTemplate: Record<string, unknown>) {
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route: Route) => {
    const requestURL = new URL(route.request().url())
    if (requestURL.pathname === '/api/install/status') {
      await route.fulfill({ status: 200, json: { installed: true } })
      return
    }
    if (requestURL.pathname === '/api/system-config/public') {
      await route.fulfill({ status: 200, json: publicSystemConfig })
      return
    }
    if (requestURL.pathname === '/api/home-template/active') {
      await route.fulfill({ status: 200, json: { code: 200, msg: '', data: activeTemplate } })
      return
    }
    await route.fulfill({ status: 200, json: { code: 404, msg: 'not found' } })
  })
}

test('未启用远程模板时渲染默认首页并保持 URL', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 'default',
    templateId: 'default',
    name: '默认首页模板',
    version: 'builtin',
    isDefault: true
  })

  await page.goto('/user/login')

  await expect(page.locator('.license-home')).toBeVisible()
  await expect(
    page.getByRole('heading', { name: '让每一份软件授权 清晰、安全、可管理' })
  ).toBeVisible()
  await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
})

test('活动模板改变页面内容但不改变 URL', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 7,
    templateId: 'clean-home',
    name: '清新首页',
    version: '1.0.0',
    isDefault: false,
    schemaVersion: 1,
    document: {
      schemaVersion: 1,
      theme: { primaryColor: '#16a085', backgroundColor: '#f2fbf8' },
      hero: {
        badge: 'REMOTE TEMPLATE',
        title: '远程首页已启用',
        highlight: 'URL 保持不变',
        description: '声明式模板安全渲染',
        primaryAction: { label: '登录用户中心', type: 'login' }
      },
      features: [{ title: '安全渲染', description: '不执行远程脚本' }]
    }
  })

  await page.goto('/user/login')

  await expect(page.locator('.remote-home')).toBeVisible()
  await expect(page.getByRole('heading', { name: '远程首页已启用 URL 保持不变' })).toBeVisible()
  await expect(page.locator('.license-home')).toHaveCount(0)
  await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
})

test('活动模板格式错误时自动回退默认首页', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 8,
    templateId: 'broken-home',
    name: '错误模板',
    version: '1.0.0',
    isDefault: false,
    schemaVersion: 2,
    document: { schemaVersion: 2, hero: { title: '不应渲染' } }
  })

  await page.goto('/user/login')

  await expect(page.locator('.license-home')).toBeVisible()
  await expect(page.locator('.remote-home')).toHaveCount(0)
  await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
})

for (const template of [
  {
    id: 11,
    templateId: 'cartoon-blue',
    name: '圆趣蓝白红',
    stylePreset: 'cartoon-blue',
    title: '授权服务，也可以',
    highlight: '简单又亲切',
    className: 'remote-home--cartoon-blue'
  },
  {
    id: 12,
    templateId: 'fintech-gold',
    name: '黑金金融科技',
    stylePreset: 'fintech-gold',
    title: '让授权管理保持',
    highlight: '清晰、快速、可信',
    className: 'gold-home'
  }
] as const) {
  test(`${template.name}预设可渲染且保持登录页 URL`, async ({ page }) => {
    await mockPublicAPIs(page, {
      id: template.id,
      templateId: template.templateId,
      name: template.name,
      version: '1.0.0',
      isDefault: false,
      schemaVersion: 1,
      document: {
        schemaVersion: 1,
        stylePreset: template.stylePreset,
        theme: {
          primaryColor: template.stylePreset === 'cartoon-blue' ? '#168fe5' : '#f3ba2f',
          backgroundColor: template.stylePreset === 'cartoon-blue' ? '#f1faff' : '#090b10',
          textColor: template.stylePreset === 'cartoon-blue' ? '#15334a' : '#f4f6fa'
        },
        hero: {
          title: template.title,
          highlight: template.highlight,
          primaryAction: { label: '进入用户中心', type: 'login' }
        }
      }
    })

    await page.goto('/user/login')

    await expect(page.locator(`.${template.className}`)).toBeVisible()
    await expect(
      page.getByRole('heading', { name: `${template.title} ${template.highlight}` })
    ).toBeVisible()
    await expect(page.locator('.license-home')).toHaveCount(0)
    await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
  })
}

test('未知风格预设自动回退默认首页', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 13,
    templateId: 'unknown-style',
    name: '未知风格',
    version: '1.0.0',
    isDefault: false,
    schemaVersion: 1,
    document: {
      schemaVersion: 1,
      stylePreset: 'unknown-style',
      hero: { title: '不应渲染' }
    }
  })

  await page.goto('/user/login')

  await expect(page.locator('.license-home')).toBeVisible()
  await expect(page.locator('.remote-home')).toHaveCount(0)
  await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
})

test('远程模板登录沿用原请求契约与本地存储键', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 7,
    templateId: 'clean-home',
    name: '清新首页',
    version: '1.0.0',
    isDefault: false,
    schemaVersion: 1,
    document: {
      schemaVersion: 1,
      hero: { title: '远程首页', primaryAction: { label: '登录用户中心', type: 'login' } }
    }
  })
  let loginPayload: unknown
  await page.route('**/api/user-panel/login', async (route) => {
    loginPayload = route.request().postDataJSON()
    await route.fulfill({
      status: 200,
      json: {
        code: 200,
        msg: '登录成功',
        data: { accessToken: 'test-token', userId: 1, email: 'user@example.com', nickname: 'user' }
      }
    })
  })

  await page.goto('/user/login')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByPlaceholder('手机号 / 邮箱 / 用户ID').fill('user@example.com')
  await page.getByPlaceholder('登录密码').fill('test-password')
  await page
    .locator('.remote-login-dialog')
    .getByRole('button', { name: '登录用户中心', exact: true })
    .click()

  await expect
    .poll(() => loginPayload)
    .toEqual({ account: 'user@example.com', password: 'test-password' })
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('user_panel_token')))
    .toBe('test-token')
})

test('静态 ZIP 首页不叠加浮动按钮并保持系统登录', async ({ page }) => {
  const base = '/api/home-template/assets/99/upload-12345/'
  await mockPublicAPIs(page, {
    id: 99,
    templateId: 'upload-12345',
    name: '自定义 ZIP 首页',
    version: '1.0.0',
    isDefault: false,
    format: 'static',
    entryUrl: `${base}index.html`
  })
  await page.addInitScript(() => {
    if (window === window.top) localStorage.setItem('zip-test-token', 'must-stay-private')
  })
  await page.route('**/api/home-template/assets/**', async (route) => {
    const isScript = route.request().url().endsWith('/assets/main.js')
    await route.fulfill({
      status: 200,
      contentType: isScript ? 'text/javascript; charset=utf-8' : 'text/html; charset=utf-8',
      headers: {
        'Access-Control-Allow-Origin': '*',
        'Content-Security-Policy':
          "default-src * data: blob: 'unsafe-inline' 'unsafe-eval'; object-src 'none'; frame-ancestors 'self'"
      },
      body: isScript
        ? `
        document.querySelector('h1').textContent = 'ZIP 静态脚本已运行';
        try { document.body.dataset.token = window.parent.localStorage.getItem('zip-test-token') || ''; }
        catch { document.body.dataset.isolated = 'yes'; }
        document.querySelector('button').onclick = () => window.parent.postMessage({ type: 'auth-pro:login' }, '*');
      `
        : '<!doctype html><html><body><h1>加载中</h1><button>模板登录按钮</button><script type="module" src="./assets/main.js"></script></body></html>'
    })
  })
  await page.goto('/user/login')
  const frame = page.frameLocator('iframe[title="自定义首页模板"]')
  await expect(frame.getByRole('heading', { name: 'ZIP 静态脚本已运行' })).toBeVisible()
  await expect(frame.locator('body')).toHaveAttribute('data-token', 'must-stay-private')
  await expect(page.locator('.static-home iframe')).not.toHaveAttribute('sandbox')
  await expect(page.locator('.static-home-actions')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '使用默认首页', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '登录用户中心', exact: true })).toHaveCount(0)
  await expect(page).toHaveURL('http://127.0.0.1:4175/user/login')
  await page.evaluate(() => window.postMessage({ type: 'auth-pro:login' }, '*'))
  await expect(page.getByPlaceholder('手机号 / 邮箱 / 用户ID')).not.toBeVisible()
  await frame.getByRole('button', { name: '模板登录按钮' }).click()
  await expect(page.getByPlaceholder('手机号 / 邮箱 / 用户ID')).toBeVisible()
  await page.locator('.el-dialog__headerbtn').click()
  await expect(page.getByPlaceholder('手机号 / 邮箱 / 用户ID')).not.toBeVisible()
  await expect(frame.getByRole('heading', { name: 'ZIP 静态脚本已运行' })).toBeVisible()
  await expect(page.locator('.license-home')).toHaveCount(0)
  // The response CSP applies to direct navigation as well as iframe rendering.
  await page.goto(`${base}index.html`)
  await expect(page.locator('body')).toHaveAttribute('data-token', 'must-stay-private')
})

test('ZIP JSON 模板使用安装目录内的相对图片资源', async ({ page }) => {
  const base = '/api/home-template/assets/99/upload-12345/'
  await mockPublicAPIs(page, {
    id: 99,
    templateId: 'upload-12345',
    name: 'JSON ZIP',
    version: '1.0.0',
    isDefault: false,
    format: 'json',
    assetBaseUrl: base,
    document: { schemaVersion: 1, hero: { title: 'ZIP JSON 首页', imageUrl: 'assets/cover.svg' } }
  })
  await page.route('**/api/home-template/assets/**', async (route) => {
    await route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="blue"/></svg>'
    })
  })
  await page.goto('/user/login')
  await expect(page.getByAltText('首页模板展示图')).toHaveAttribute(
    'src',
    `http://127.0.0.1:4175${base}assets/cover.svg`
  )
  await expect(page.getByAltText('首页模板展示图')).toBeVisible()
})

test('非法静态模板地址回退默认首页', async ({ page }) => {
  await mockPublicAPIs(page, {
    id: 99,
    templateId: 'bad',
    name: 'bad',
    version: '1.0.0',
    isDefault: false,
    format: 'static',
    entryUrl: 'https://example.invalid/untrusted.html'
  })
  await page.goto('/user/login')
  await expect(page.locator('.license-home')).toBeVisible()
  await expect(page.locator('.static-home')).toHaveCount(0)
})
