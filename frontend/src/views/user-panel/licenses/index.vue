<template>
  <div class="user-licenses">
    <!-- 授权列表卡片：筛选工具栏 + 表格 + 分页 -->
    <div class="art-card licenses-card">
      <div class="licenses-toolbar">
        <div class="toolbar-filters">
          <el-input
            v-model="searchForm.keyword"
            placeholder="搜索域名 / IP / 密钥"
            clearable
            class="filter-keyword"
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <iconify-icon icon="ri:search-line" width="15" />
            </template>
          </el-input>
          <el-select
            v-model="searchForm.appId"
            placeholder="全部应用"
            clearable
            class="filter-select"
          >
            <el-option v-for="app in appList" :key="app.id" :label="app.name" :value="app.id" />
          </el-select>
          <el-select
            v-model="searchForm.status"
            placeholder="全部状态"
            clearable
            class="filter-select status-select"
          >
            <el-option label="正常" value="active" />
            <el-option label="即将到期" value="expiring" />
            <el-option label="已过期" value="expired" />
          </el-select>
          <el-button type="primary" @click="handleSearch">查询</el-button>
          <el-button @click="handleReset">重置</el-button>
        </div>
        <el-button type="primary" class="redeem-btn" @click="openRedeemDialog">
          <iconify-icon icon="ri:ticket-2-line" width="15" />
          兑换卡密
        </el-button>
      </div>

      <el-table :data="tableData" stripe v-loading="loading" class="licenses-table">
        <el-table-column label="域名/IP/密钥" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="target-cell">
              <span class="target-icon" :class="`target-icon-${row.type}`">
                <iconify-icon :icon="typeIconMap[row.type] || 'ri:global-line'" width="15" />
              </span>
              <el-tag v-if="row.bindingPending" type="warning" size="small">待绑定</el-tag>
              <span v-else class="target-value" :class="{ mono: row.type !== 'domain' }">{{
                row.domain || '--'
              }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="appName" label="应用" width="110" show-overflow-tooltip />
        <el-table-column prop="typeLabel" label="类型" width="80">
          <template #default="{ row }">
            <el-tag :type="typeTagMap[row.type]" size="small" effect="light">{{
              row.typeLabel
            }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="statusLabel" label="状态" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="statusTagMap[row.status]" size="small" effect="light">{{
              row.statusLabel
            }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="expireAt" label="到期时间" width="130" />
        <el-table-column prop="createdAt" label="开通时间" width="130" />
        <el-table-column prop="source" label="来源" width="100">
          <template #default="{ row }">
            <span class="source-text">{{ row.source }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="amount" label="套餐原价" width="110" align="right">
          <template #default="{ row }">
            <span class="amount-text">{{ formatLicenseAmount(row.amount) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="站点" width="110" align="center">
          <template #default="{ row }">
            <el-button
              v-if="row.type === 'key'"
              link
              type="primary"
              size="small"
              @click="openSiteDialog(row)"
            >
              已绑定 {{ row.boundSites ?? 0 }}{{ Number(row.maxSites) ? ` / ${row.maxSites}` : '' }}
            </el-button>
            <span v-else class="text-secondary">--</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right" align="center">
          <template #default="{ row }">
            <el-button
              v-if="canEditTargetType(row.type)"
              link
              type="primary"
              size="small"
              @click="openEditDialog(row)"
            >
              {{ row.bindingPending ? '绑定目标' : '修改目标' }}
            </el-button>
            <el-text v-else-if="row.type !== 'key'" type="info" size="small">不可修改</el-text>
            <el-button link type="primary" size="small" @click="openVersionsDialog(row)">
              版本下载
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无授权记录" :image-size="80" />
        </template>
      </el-table>

      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :page-sizes="[10, 20, 50]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          @current-change="fetchList"
          @size-change="fetchList"
        />
      </div>
    </div>

    <el-dialog v-model="editDialog.visible" title="修改授权目标" width="420px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="授权类型">
          <el-tag :type="typeTagMap[editDialog.type]" size="small">{{
            editDialog.typeLabel
          }}</el-tag>
        </el-form-item>
        <el-form-item :label="targetLabel">
          <div class="target-editor">
            <el-input
              v-model="editDialog.target"
              :placeholder="targetPlaceholder"
              :disabled="editDialog.type === 'key'"
              :clearable="editDialog.type !== 'key'"
            />
            <el-button
              v-if="editDialog.type === 'key'"
              type="primary"
              plain
              :loading="editDialog.refreshing"
              title="生成新的16位密钥"
              @click="resetLicenseKey"
            >
              <iconify-icon icon="ri:refresh-line" width="16" />
              重置
            </el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDialog.visible = false">取消</el-button>
        <el-button
          v-if="editDialog.type !== 'key'"
          type="primary"
          :loading="editDialog.submitting"
          @click="submitEditTarget"
        >
          保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="siteDialog.visible" title="密钥绑定站点" width="680px" destroy-on-close>
      <el-alert
        v-if="siteDialog.maxSites > 0"
        :title="`当前已绑定 ${siteDialog.list.length} / ${siteDialog.maxSites} 个站点，达到上限后新站点验证会被拒绝，可解绑释放名额。`"
        type="info"
        show-icon
        :closable="false"
        class="mb-3"
      />
      <el-alert
        v-else
        title="该密钥不限制站点数量。"
        type="info"
        show-icon
        :closable="false"
        class="mb-3"
      />
      <el-table
        :data="siteDialog.list"
        size="small"
        v-loading="siteDialog.loading"
        max-height="360"
      >
        <el-table-column label="类型" width="80">
          <template #default="{ row }">
            <el-tag :type="row.targetType === 'ip' ? 'warning' : undefined" size="small">
              {{ row.targetType === 'ip' ? 'IP' : '域名' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="target" label="站点" min-width="160" show-overflow-tooltip />
        <el-table-column prop="serverIp" label="最近服务器IP" width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <span>{{ row.serverIp || '--' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="firstSeenAt" label="首次绑定" width="160" />
        <el-table-column prop="lastSeenAt" label="最近验证" width="160" />
        <el-table-column label="操作" width="80" align="center">
          <template #default="{ row }">
            <el-button link type="danger" size="small" @click="handleUnbindSite(row)"
              >解绑</el-button
            >
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无绑定站点" :image-size="60" />
        </template>
      </el-table>
    </el-dialog>

    <el-dialog v-model="redeemDialog.visible" title="兑换卡密" width="460px" destroy-on-close>
      <el-alert
        title="兑换后授权将归当前账号，不能转给他人。"
        type="info"
        show-icon
        :closable="false"
        class="redeem-alert"
      />
      <el-form label-width="72px" @submit.prevent="submitRedeem">
        <el-form-item label="卡密">
          <el-input
            v-model="redeemDialog.cardCode"
            placeholder="请输入卡密"
            maxlength="64"
            clearable
            autocomplete="off"
            @keyup.enter="submitRedeem"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="redeemDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="redeemDialog.submitting" @click="submitRedeem"
          >确认兑换</el-button
        >
      </template>
    </el-dialog>

    <el-dialog v-model="redeemResult.visible" title="兑换结果" width="520px" destroy-on-close>
      <el-result icon="success" :title="redeemResult.idempotent ? '该卡密已兑换' : '卡密兑换成功'">
        <template #sub-title>
          <div class="redeem-summary">
            <div>{{ redeemResult.appName }} · {{ redeemResult.planName }}</div>
            <div>授权编号：{{ redeemResult.licenseNo }}</div>
            <div>授权类型：{{ redeemResult.typeLabel }} 有效期至：{{ redeemResult.expireAt }}</div>
          </div>
        </template>
        <template #extra>
          <div v-if="redeemResult.type === 'key'" class="license-key-result">
            <el-input :model-value="redeemResult.licenseKey" readonly>
              <template #append>
                <el-button @click="copyRedeemedKey">复制密钥</el-button>
              </template>
            </el-input>
          </div>
          <el-alert
            v-else
            title="授权尚未绑定目标，请在列表中点击“绑定目标”后使用。"
            type="warning"
            show-icon
            :closable="false"
          />
        </template>
      </el-result>
      <template #footer>
        <el-button type="primary" @click="closeRedeemResult">完成</el-button>
      </template>
    </el-dialog>

    <LicenseVersionsDialog
      ref="versionsDialogRef"
      api-prefix="/api/user-panel"
      token-key="user_panel_token"
    />
  </div>
</template>

<script setup lang="ts">
  import { ref, reactive, onMounted, onBeforeUnmount, computed } from 'vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { Icon as IconifyIcon } from '@iconify/vue'
  import axios from 'axios'
  import LicenseVersionsDialog from '@/components/core/panels/LicenseVersionsDialog.vue'

  const purchaseRefreshEvent = 'user-panel-purchase-refresh'
  const loading = ref(false)
  const searchForm = reactive({ keyword: '', appId: '', status: '' })
  const pagination = reactive({ page: 1, pageSize: 10, total: 0 })
  const appList = ref<{ id: number; name: string }[]>([])
  const tableData = ref<any[]>([])
  const editDialog = reactive({
    visible: false,
    submitting: false,
    refreshing: false,
    id: 0,
    type: '',
    typeLabel: '',
    target: ''
  })

  const siteDialog = reactive({
    visible: false,
    loading: false,
    licenseId: 0,
    licenseNo: '',
    maxSites: 0,
    list: [] as any[]
  })

  const versionsDialogRef = ref<InstanceType<typeof LicenseVersionsDialog>>()

  function openVersionsDialog(row: any) {
    versionsDialogRef.value?.open({ id: row.id, appName: row.appName })
  }

  const redeemDialog = reactive({
    visible: false,
    submitting: false,
    cardCode: ''
  })
  const redeemResult = reactive({
    visible: false,
    licenseNo: '',
    appName: '',
    planName: '',
    type: '',
    typeLabel: '',
    licenseKey: '',
    expireAt: '',
    idempotent: false
  })

  type TagType = 'primary' | 'success' | 'warning' | 'info' | 'danger'
  const typeTagMap: Record<string, TagType | undefined> = {
    domain: undefined,
    wildcard: 'success',
    ip: 'warning',
    key: 'info'
  }
  const typeIconMap: Record<string, string> = {
    domain: 'ri:global-line',
    wildcard: 'ri:asterisk',
    ip: 'ri:router-line',
    key: 'ri:key-2-line'
  }
  const statusTagMap: Record<string, TagType | undefined> = {
    active: 'success',
    expiring: 'warning',
    expired: 'info'
  }
  const editableTargetTypes = new Set(['domain', 'wildcard', 'ip', 'key'])
  const targetLabel = computed(() => {
    if (editDialog.type === 'ip') return 'IP'
    if (editDialog.type === 'key') return '授权密钥'
    if (editDialog.type === 'wildcard') return '泛域名'
    return '域名'
  })
  const targetPlaceholder = computed(() => `请输入${targetLabel.value}`)

  function getToken() {
    const stored = localStorage.getItem('user_panel_token')
    return stored || ''
  }

  async function fetchApps() {
    try {
      const { data } = await axios.get('/api/user-panel/apps', {
        headers: { Authorization: `Bearer ${getToken()}` }
      })
      if (data.code === 200) appList.value = data.data || []
    } catch {
      ElMessage.error('加载应用列表失败')
    }
  }

  async function fetchList() {
    loading.value = true
    try {
      const { data } = await axios.get('/api/user-panel/licenses', {
        headers: { Authorization: `Bearer ${getToken()}` },
        params: {
          keyword: searchForm.keyword || undefined,
          appId: searchForm.appId || undefined,
          status: searchForm.status || undefined,
          page: pagination.page,
          pageSize: pagination.pageSize
        }
      })
      if (data.code === 200) {
        tableData.value = data.data.list || []
        pagination.total = data.data.total || 0
      }
    } catch {
      ElMessage.error('加载授权列表失败')
    } finally {
      loading.value = false
    }
  }

  function handlePurchaseRefresh() {
    fetchList()
  }

  function handleSearch() {
    pagination.page = 1
    fetchList()
  }
  function handleReset() {
    searchForm.keyword = ''
    searchForm.appId = ''
    searchForm.status = ''
    handleSearch()
  }
  function canEditTargetType(type: string) {
    return editableTargetTypes.has(type)
  }
  function formatLicenseAmount(amount: unknown) {
    if (amount === null || amount === undefined || amount === '') return '--'
    return `¥${Number(amount).toFixed(2)}`
  }

  function openEditDialog(row: any) {
    editDialog.id = row.id
    editDialog.type = row.type
    editDialog.typeLabel = row.typeLabel
    editDialog.target = row.domain || ''
    editDialog.visible = true
  }

  async function submitEditTarget() {
    const target = editDialog.target.trim()
    if (!target) {
      ElMessage.warning(`请输入${targetLabel.value}`)
      return
    }

    editDialog.submitting = true
    try {
      const { data } = await axios.put(
        `/api/user-panel/licenses/${editDialog.id}/target`,
        { target },
        {
          headers: { Authorization: `Bearer ${getToken()}` }
        }
      )
      if (data.code === 200) {
        ElMessage.success(data.msg || '更新成功')
        editDialog.visible = false
        fetchList()
      } else {
        ElMessage.error(data.msg || '更新失败')
      }
    } catch {
      ElMessage.error('更新失败')
    } finally {
      editDialog.submitting = false
    }
  }

  async function resetLicenseKey() {
    editDialog.refreshing = true
    try {
      const { data } = await axios.post(
        `/api/user-panel/licenses/${editDialog.id}/refresh-key`,
        {},
        {
          headers: { Authorization: `Bearer ${getToken()}` }
        }
      )
      if (data.code === 200) {
        editDialog.target = data.data?.licenseKey || ''
        ElMessage.success(data.msg || '密钥已重置')
        fetchList()
      } else {
        ElMessage.error(data.msg || '重置失败')
      }
    } catch {
      ElMessage.error('重置失败')
    } finally {
      editDialog.refreshing = false
    }
  }

  async function openSiteDialog(row: any) {
    siteDialog.licenseId = Number(row.id)
    siteDialog.licenseNo = row.licenseNo || ''
    siteDialog.maxSites = Number(row.maxSites) || 0
    siteDialog.visible = true
    await fetchLicenseSites()
  }

  async function fetchLicenseSites() {
    siteDialog.loading = true
    try {
      const { data } = await axios.get(`/api/user-panel/licenses/${siteDialog.licenseId}/sites`, {
        headers: { Authorization: `Bearer ${getToken()}` }
      })
      if (data.code === 200) {
        siteDialog.list = data.data?.list || []
        if (data.data?.maxSites !== undefined) siteDialog.maxSites = Number(data.data.maxSites)
      } else {
        ElMessage.error(data.msg || '加载绑定站点失败')
      }
    } catch {
      ElMessage.error('加载绑定站点失败')
    } finally {
      siteDialog.loading = false
    }
  }

  async function handleUnbindSite(row: any) {
    try {
      await ElMessageBox.confirm(`确定解绑站点「${row.target}」？解绑后名额立即释放。`, '提示', {
        type: 'warning'
      })
      const { data } = await axios.delete(
        `/api/user-panel/licenses/${siteDialog.licenseId}/sites/${row.id}`,
        {
          headers: { Authorization: `Bearer ${getToken()}` }
        }
      )
      if (data.code === 200) {
        ElMessage.success(data.msg || '解绑成功')
        await fetchLicenseSites()
        fetchList()
      } else {
        ElMessage.error(data.msg || '解绑失败')
      }
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') {
        ElMessage.error('解绑失败，请稍后重试')
      }
    }
  }

  function openRedeemDialog() {
    redeemDialog.cardCode = ''
    redeemDialog.submitting = false
    redeemDialog.visible = true
  }

  async function submitRedeem() {
    if (redeemDialog.submitting) return
    const cardCode = redeemDialog.cardCode.trim()
    if (!cardCode) {
      ElMessage.warning('请输入卡密')
      return
    }

    redeemDialog.submitting = true
    try {
      const { data } = await axios.post(
        '/api/user-panel/cards/redeem',
        { cardCode },
        {
          headers: { Authorization: `Bearer ${getToken()}` }
        }
      )
      if (data.code !== 200) {
        ElMessage.error(data.msg || '兑换失败')
        return
      }
      redeemDialog.visible = false
      Object.assign(redeemResult, {
        ...data.data,
        visible: true,
        licenseKey: data.data?.licenseKey || '',
        idempotent: data.data?.idempotent === true
      })
      await fetchList()
    } catch {
      ElMessage.error('兑换失败，请稍后重试')
    } finally {
      redeemDialog.submitting = false
    }
  }

  async function copyRedeemedKey() {
    if (!redeemResult.licenseKey) return
    try {
      await navigator.clipboard.writeText(redeemResult.licenseKey)
      ElMessage.success('密钥已复制')
    } catch {
      ElMessage.error('复制失败，请手动复制')
    }
  }

  function closeRedeemResult() {
    redeemResult.visible = false
  }

  onMounted(() => {
    fetchApps()
    fetchList()
    window.addEventListener(purchaseRefreshEvent, handlePurchaseRefresh)
  })

  onBeforeUnmount(() => {
    window.removeEventListener(purchaseRefreshEvent, handlePurchaseRefresh)
  })
</script>

<style scoped lang="scss">
  .user-licenses {
    .art-card {
      overflow: hidden;
      background: var(--el-bg-color);
      border-radius: 12px !important;
    }
  }

  .mb-3 {
    margin-bottom: 12px;
  }

  .text-secondary {
    color: var(--el-text-color-secondary);
  }

  .licenses-card {
    padding: 20px;
  }

  // 工具栏：左侧筛选，右侧兑换入口
  .licenses-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 16px;
  }

  .toolbar-filters {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .filter-keyword {
    width: 220px;
  }

  .filter-select {
    width: 130px;
  }

  .status-select {
    width: 120px;
  }

  .redeem-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }

  // 表格细节
  .licenses-table {
    :deep(.el-table__header th) {
      background: var(--el-fill-color-light);
      color: var(--el-text-color-secondary);
      font-weight: 600;
    }
  }

  .target-cell {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  .target-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: 8px;
    flex-shrink: 0;
    background: var(--el-color-primary-light-9);
    color: var(--el-color-primary);
  }

  .target-icon-wildcard {
    background: var(--el-color-success-light-9);
    color: var(--el-color-success);
  }

  .target-icon-ip {
    background: var(--el-color-warning-light-9);
    color: var(--el-color-warning);
  }

  .target-icon-key {
    background: var(--el-color-info-light-9);
    color: var(--el-color-info);
  }

  .target-value {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--el-text-color-primary);

    &.mono {
      font-family: 'Roboto Mono', monospace;
      font-size: 12px;
    }
  }

  .source-text {
    font-size: 12px;
    color: var(--el-text-color-secondary);
  }

  .amount-text {
    font-family: 'DIN Alternate', 'Roboto Mono', monospace;
    font-weight: 600;
    color: var(--el-text-color-primary);
  }

  .pagination-wrapper {
    display: flex;
    justify-content: flex-end;
    margin-top: 16px;
  }

  .target-editor {
    display: flex;
    gap: 8px;
    align-items: center;
    width: 100%;
  }

  .target-editor .el-button {
    flex: 0 0 auto;
  }

  .target-editor .el-button :deep(.el-icon) {
    margin-right: 4px;
  }

  .redeem-alert {
    margin-bottom: 18px;
  }

  .redeem-summary {
    display: grid;
    gap: 6px;
    color: var(--el-text-color-regular);
  }

  .license-key-result {
    width: 100%;
    min-width: 360px;
  }

  @media (max-width: 768px) {
    .licenses-card {
      padding: 14px;
    }

    .filter-keyword,
    .filter-select,
    .status-select {
      width: 100%;
    }

    .toolbar-filters {
      width: 100%;
    }

    .redeem-btn {
      width: 100%;
      justify-content: center;
    }

    .pagination-wrapper {
      justify-content: center;
    }
  }
</style>
